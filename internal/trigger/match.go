package trigger

import (
	"fmt"
	"slices"
	"strings"

	"github.com/a2-ito/gh-sim/internal/workflow"
)

// Event はシミュレートする GitHub のイベントを表す。
// ワークフロー側のトリガー定義は workflow.Event、
// こちらは「実際に起きたことにする」入力を表す。
type Event struct {
	// Name は "push" や "pull_request" などのイベント名。
	Name string

	// Action は types フィルタと照合するアクティビティタイプ（"opened" など）。
	// 空の場合は types フィルタの判定を保留する。
	Action string

	// Ref は対象の完全な ref（refs/heads/main、refs/tags/v1.0.0）。
	Ref string

	// BaseRef は pull_request のマージ先ブランチ名。
	// pull_request の branches フィルタは head ではなく base と照合する。
	BaseRef string

	// ChangedPaths は変更されたファイルのパス一覧。
	// 空の場合は paths フィルタを評価せず判定を保留する。
	ChangedPaths []string

	// Payload は github.event に入るイベントペイロード。
	Payload map[string]any
}

// Result はトリガー照合の結果を表す。
type Result struct {
	// Triggered はワークフローが起動するかを表す。
	Triggered bool

	// Matched は照合対象になったトリガー定義。イベント名が無い場合は nil。
	Matched *workflow.Event

	// Reason は判定の理由。起動しない場合はその原因を示す。
	Reason string

	// Notes は判定を保留した項目などの補足。
	Notes []string
}

// filterResult はフィルタ1件の判定結果。
type filterResult struct {
	// pass はこのフィルタを通過したかを表す。
	pass bool
	// reason は通過しなかった理由。
	reason string
	// note は判定を保留した場合の補足。
	note string
}

// pass はフィルタ通過を表す結果を返す。
func pass() filterResult { return filterResult{pass: true} }

// block はフィルタで止まったことを表す結果を返す。
func block(format string, args ...any) filterResult {
	return filterResult{reason: fmt.Sprintf(format, args...)}
}

// skip は判定を保留してフィルタを通す結果を返す。
func skip(format string, args ...any) filterResult {
	return filterResult{pass: true, note: fmt.Sprintf(format, args...)}
}

// NormalizeRef は短い指定を完全な ref に補う。
// refs/ で始まらない指定はブランチ名として扱う。
func NormalizeRef(ref string) string {
	if ref == "" || strings.HasPrefix(ref, "refs/") {
		return ref
	}
	return "refs/heads/" + ref
}

// Match はワークフローがイベントで起動するかを判定する。
func Match(w *workflow.Workflow, ev *Event) (*Result, error) {
	def := w.On.Find(ev.Name)
	if def == nil {
		return &Result{
			Reason: fmt.Sprintf("on: に %s がありません（定義: %s）",
				ev.Name, strings.Join(w.On.Names(), ", ")),
		}, nil
	}

	res := &Result{Matched: def}

	// フィルタはイベント種別によって適用できるものが異なる。
	checks := []func() (filterResult, error){
		func() (filterResult, error) { return matchTypes(def, ev), nil },
		func() (filterResult, error) { return matchRef(def, ev) },
		func() (filterResult, error) { return matchPaths(def, ev) },
	}
	for _, check := range checks {
		fr, err := check()
		if err != nil {
			return nil, err
		}
		if fr.note != "" {
			res.Notes = append(res.Notes, fr.note)
		}
		if !fr.pass {
			res.Reason = fr.reason
			return res, nil
		}
	}

	res.Triggered = true
	res.Reason = fmt.Sprintf("on.%s に一致します", def.Name)
	return res, nil
}

// matchTypes は types フィルタを照合する。
func matchTypes(def *workflow.Event, ev *Event) filterResult {
	types := def.Types
	if len(types) == 0 {
		types = defaultTypes(def.Name)
	}
	if len(types) == 0 {
		// types の概念が無いイベント、または全タイプが対象。
		return pass()
	}
	if ev.Action == "" {
		return skip("アクティビティタイプが指定されていないため on.%s.types は評価していません", def.Name)
	}
	if slices.Contains(types, ev.Action) {
		return pass()
	}
	return block("アクティビティタイプ %q が on.%s.types (%s) に含まれていません",
		ev.Action, def.Name, strings.Join(types, ", "))
}

// defaultTypes は types を省略した場合に適用されるアクティビティタイプを返す。
// pull_request 系は省略時でも3つに限られ、closed などでは起動しない。
func defaultTypes(event string) []string {
	switch event {
	case "pull_request", "pull_request_target":
		return []string{"opened", "synchronize", "reopened"}
	}
	// 他のイベントは省略すると全タイプが対象になる。
	return nil
}

// matchRef は branches / tags フィルタを照合する。
func matchRef(def *workflow.Event, ev *Event) (filterResult, error) {
	hasBranchFilter := len(def.Branches) > 0 || len(def.BranchesIgnore) > 0
	hasTagFilter := len(def.Tags) > 0 || len(def.TagsIgnore) > 0
	if !hasBranchFilter && !hasTagFilter {
		return pass(), nil
	}
	if !supportsRefFilter(def.Name) {
		return pass(), nil
	}

	// pull_request では branches フィルタをマージ先ブランチと照合する。
	if isPullRequest(def.Name) {
		if !hasBranchFilter {
			return pass(), nil
		}
		if ev.BaseRef == "" {
			return skip("マージ先ブランチが指定されていないため on.%s.branches は評価していません", def.Name), nil
		}
		return matchRefFilters(def.Branches, def.BranchesIgnore, ev.BaseRef, "branches", def.Name)
	}

	branch, isBranch := branchFromRef(ev.Ref)
	tag, isTag := tagFromRef(ev.Ref)
	switch {
	case isTag:
		if !hasTagFilter {
			// branches のみが指定されている場合、タグへの push では起動しない。
			return block("on.%s に branches フィルタのみが指定されているため、タグ %s への push では起動しません",
				def.Name, tag), nil
		}
		return matchRefFilters(def.Tags, def.TagsIgnore, tag, "tags", def.Name)

	case isBranch:
		if !hasBranchFilter {
			// tags のみが指定されている場合、ブランチへの push では起動しない。
			return block("on.%s に tags フィルタのみが指定されているため、ブランチ %s への push では起動しません",
				def.Name, branch), nil
		}
		return matchRefFilters(def.Branches, def.BranchesIgnore, branch, "branches", def.Name)
	}

	if ev.Ref == "" {
		return skip("ref が指定されていないため on.%s のブランチ・タグフィルタは評価していません", def.Name), nil
	}
	return skip("ref %q はブランチでもタグでもないため on.%s のフィルタは評価していません", ev.Ref, def.Name), nil
}

// matchRefFilters は肯定リストと否定リストで ref 名を照合する。
func matchRefFilters(include, exclude []string, name, field, event string) (filterResult, error) {
	if len(include) > 0 {
		ok, err := MatchFilters(include, name)
		if err != nil {
			return filterResult{}, err
		}
		if !ok {
			return block("%s が on.%s.%s (%s) に一致しません",
				name, event, field, strings.Join(include, ", ")), nil
		}
		return pass(), nil
	}

	ok, err := MatchFilters(exclude, name)
	if err != nil {
		return filterResult{}, err
	}
	if ok {
		return block("%s が on.%s.%s-ignore (%s) に一致します",
			name, event, field, strings.Join(exclude, ", ")), nil
	}
	return pass(), nil
}

// matchPaths は paths / paths-ignore フィルタを照合する。
func matchPaths(def *workflow.Event, ev *Event) (filterResult, error) {
	if len(def.Paths) == 0 && len(def.PathsIgnore) == 0 {
		return pass(), nil
	}
	if !supportsPathFilter(def.Name) {
		return pass(), nil
	}
	if len(ev.ChangedPaths) == 0 {
		field := "paths"
		if len(def.PathsIgnore) > 0 {
			field = "paths-ignore"
		}
		return skip("変更ファイルが指定されていないため on.%s.%s は評価していません", def.Name, field), nil
	}

	if len(def.Paths) > 0 {
		// 変更ファイルのうち1つでも一致すれば起動する。
		for _, p := range ev.ChangedPaths {
			ok, err := MatchFilters(def.Paths, p)
			if err != nil {
				return filterResult{}, err
			}
			if ok {
				return pass(), nil
			}
		}
		return block("変更ファイルが on.%s.paths (%s) に一致しません",
			def.Name, strings.Join(def.Paths, ", ")), nil
	}

	// paths-ignore は変更ファイルがすべて除外対象のときだけ起動を止める。
	for _, p := range ev.ChangedPaths {
		ok, err := MatchFilters(def.PathsIgnore, p)
		if err != nil {
			return filterResult{}, err
		}
		if !ok {
			return pass(), nil
		}
	}
	return block("変更ファイルがすべて on.%s.paths-ignore (%s) に一致します",
		def.Name, strings.Join(def.PathsIgnore, ", ")), nil
}

// supportsRefFilter は branches / tags フィルタが有効なイベントかを返す。
func supportsRefFilter(event string) bool {
	switch event {
	case "push", "pull_request", "pull_request_target", "workflow_run":
		return true
	}
	return false
}

// supportsPathFilter は paths / paths-ignore フィルタが有効なイベントかを返す。
func supportsPathFilter(event string) bool {
	switch event {
	case "push", "pull_request", "pull_request_target":
		return true
	}
	return false
}

// isPullRequest は pull_request 系のイベントかを返す。
func isPullRequest(event string) bool {
	return event == "pull_request" || event == "pull_request_target"
}

// branchFromRef は refs/heads/... からブランチ名を取り出す。
func branchFromRef(ref string) (string, bool) {
	return strings.CutPrefix(ref, "refs/heads/")
}

// tagFromRef は refs/tags/... からタグ名を取り出す。
func tagFromRef(ref string) (string, bool) {
	return strings.CutPrefix(ref, "refs/tags/")
}
