package trigger

import (
	"strings"
	"testing"

	"github.com/a2-ito/gh-sim/internal/workflow"
)

// minimalJobs は Validate を通すための最小のジョブ定義。
const minimalJobs = `
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`

func parseWorkflow(t *testing.T, src string) *workflow.Workflow {
	t.Helper()
	w, err := workflow.Parse([]byte(src+minimalJobs), "test.yml")
	if err != nil {
		t.Fatalf("ワークフローの解析に失敗しました: %v", err)
	}
	return w
}

func mustMatch(t *testing.T, w *workflow.Workflow, ev *Event) *Result {
	t.Helper()
	res, err := Match(w, ev)
	if err != nil {
		t.Fatalf("照合に失敗しました: %v", err)
	}
	return res
}

func TestMatch_EventName(t *testing.T) {
	w := parseWorkflow(t, "on: [push, pull_request]")

	if res := mustMatch(t, w, &Event{Name: "push", Ref: "refs/heads/main"}); !res.Triggered {
		t.Errorf("push で起動するべきです: %s", res.Reason)
	}
	if res := mustMatch(t, w, &Event{Name: "pull_request"}); !res.Triggered {
		t.Errorf("pull_request で起動するべきです: %s", res.Reason)
	}

	res := mustMatch(t, w, &Event{Name: "schedule"})
	if res.Triggered {
		t.Error("定義に無い schedule では起動しないべきです")
	}
	if !strings.Contains(res.Reason, "on: に schedule がありません") {
		t.Errorf("理由が想定と違います: %s", res.Reason)
	}
}

func TestMatch_Branches(t *testing.T) {
	w := parseWorkflow(t, `
on:
  push:
    branches: [main, 'release/**']
`)
	tests := []struct {
		ref  string
		want bool
	}{
		{"refs/heads/main", true},
		{"refs/heads/release/1.0", true},
		{"refs/heads/release/a/b", true},
		{"refs/heads/develop", false},
		{"refs/heads/feature/x", false},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			res := mustMatch(t, w, &Event{Name: "push", Ref: tt.ref})
			if res.Triggered != tt.want {
				t.Errorf("Triggered = %v, want %v（理由: %s）", res.Triggered, tt.want, res.Reason)
			}
		})
	}
}

func TestMatch_BranchesIgnore(t *testing.T) {
	w := parseWorkflow(t, `
on:
  push:
    branches-ignore: ['wip/**', 'tmp-*']
`)
	tests := []struct {
		ref  string
		want bool
	}{
		{"refs/heads/main", true},
		{"refs/heads/feature/x", true},
		{"refs/heads/wip/experiment", false},
		{"refs/heads/tmp-fix", false},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			res := mustMatch(t, w, &Event{Name: "push", Ref: tt.ref})
			if res.Triggered != tt.want {
				t.Errorf("Triggered = %v, want %v（理由: %s）", res.Triggered, tt.want, res.Reason)
			}
		})
	}
}

// TestMatch_BranchFilterExcludesTags は branches のみを指定した push が
// タグへの push では起動しないことを確かめる。よく誤解される挙動。
func TestMatch_BranchFilterExcludesTags(t *testing.T) {
	w := parseWorkflow(t, `
on:
  push:
    branches: [main]
`)
	res := mustMatch(t, w, &Event{Name: "push", Ref: "refs/tags/v1.0.0"})
	if res.Triggered {
		t.Error("branches のみ指定されている場合、タグへの push では起動しないべきです")
	}
	if !strings.Contains(res.Reason, "タグ v1.0.0 への push では起動しません") {
		t.Errorf("理由が想定と違います: %s", res.Reason)
	}
}

// TestMatch_TagFilterExcludesBranches は tags のみを指定した push が
// ブランチへの push では起動しないことを確かめる。
func TestMatch_TagFilterExcludesBranches(t *testing.T) {
	w := parseWorkflow(t, `
on:
  push:
    tags: ['v*.*.*']
`)
	if res := mustMatch(t, w, &Event{Name: "push", Ref: "refs/heads/main"}); res.Triggered {
		t.Errorf("tags のみ指定されている場合、ブランチへの push では起動しないべきです: %s", res.Reason)
	}

	tests := map[string]bool{
		"refs/tags/v1.0.0":  true,
		"refs/tags/v10.2.3": true,
		"refs/tags/v1.0":    false,
		"refs/tags/beta":    false,
	}
	for ref, want := range tests {
		t.Run(ref, func(t *testing.T) {
			res := mustMatch(t, w, &Event{Name: "push", Ref: ref})
			if res.Triggered != want {
				t.Errorf("Triggered = %v, want %v（理由: %s）", res.Triggered, want, res.Reason)
			}
		})
	}
}

// TestMatch_PullRequestUsesBaseRef は pull_request の branches フィルタが
// head ではなくマージ先ブランチと照合されることを確かめる。
func TestMatch_PullRequestUsesBaseRef(t *testing.T) {
	w := parseWorkflow(t, `
on:
  pull_request:
    branches: [main]
`)
	// head が feature/x でも base が main なら起動する。
	res := mustMatch(t, w, &Event{
		Name:    "pull_request",
		Action:  "opened",
		Ref:     "refs/heads/feature/x",
		BaseRef: "main",
	})
	if !res.Triggered {
		t.Errorf("base が main なら起動するべきです: %s", res.Reason)
	}

	// base が develop なら起動しない。
	res = mustMatch(t, w, &Event{
		Name:    "pull_request",
		Action:  "opened",
		Ref:     "refs/heads/feature/x",
		BaseRef: "develop",
	})
	if res.Triggered {
		t.Errorf("base が develop なら起動しないべきです: %s", res.Reason)
	}

	// base が不明な場合は判定を保留し、注記を残す。
	res = mustMatch(t, w, &Event{Name: "pull_request", Action: "opened"})
	if !res.Triggered {
		t.Errorf("base が不明な場合は起動する前提で進めるべきです: %s", res.Reason)
	}
	if len(res.Notes) == 0 || !strings.Contains(res.Notes[0], "マージ先ブランチが指定されていない") {
		t.Errorf("判定を保留した注記が必要です: %v", res.Notes)
	}
}

// TestMatch_PullRequestDefaultTypes は types を省略した pull_request が
// opened / synchronize / reopened のみで起動することを確かめる。
func TestMatch_PullRequestDefaultTypes(t *testing.T) {
	w := parseWorkflow(t, "on: pull_request")
	tests := map[string]bool{
		"opened":      true,
		"synchronize": true,
		"reopened":    true,
		"closed":      false,
		"labeled":     false,
		"edited":      false,
	}
	for action, want := range tests {
		t.Run(action, func(t *testing.T) {
			res := mustMatch(t, w, &Event{Name: "pull_request", Action: action, BaseRef: "main"})
			if res.Triggered != want {
				t.Errorf("Triggered = %v, want %v（理由: %s）", res.Triggered, want, res.Reason)
			}
		})
	}
}

func TestMatch_ExplicitTypes(t *testing.T) {
	w := parseWorkflow(t, `
on:
  pull_request:
    types: [closed, labeled]
`)
	tests := map[string]bool{
		"closed":  true,
		"labeled": true,
		"opened":  false,
	}
	for action, want := range tests {
		t.Run(action, func(t *testing.T) {
			res := mustMatch(t, w, &Event{Name: "pull_request", Action: action, BaseRef: "main"})
			if res.Triggered != want {
				t.Errorf("Triggered = %v, want %v（理由: %s）", res.Triggered, want, res.Reason)
			}
		})
	}
}

// TestMatch_TypesUnspecifiedActionIsDeferred はアクティビティタイプを
// 指定しなかった場合に判定を保留することを確かめる。
func TestMatch_TypesUnspecifiedActionIsDeferred(t *testing.T) {
	w := parseWorkflow(t, `
on:
  pull_request:
    types: [closed]
`)
	res := mustMatch(t, w, &Event{Name: "pull_request", BaseRef: "main"})
	if !res.Triggered {
		t.Errorf("タイプ未指定なら起動する前提で進めるべきです: %s", res.Reason)
	}
	if len(res.Notes) == 0 || !strings.Contains(res.Notes[0], "アクティビティタイプが指定されていない") {
		t.Errorf("判定を保留した注記が必要です: %v", res.Notes)
	}
}

func TestMatch_Paths(t *testing.T) {
	w := parseWorkflow(t, `
on:
  push:
    paths: ['src/**', '*.go']
`)
	tests := []struct {
		name    string
		changed []string
		want    bool
	}{
		{"一致するファイルがある", []string{"src/main.go"}, true},
		{"一部が一致する", []string{"README.md", "src/lib/a.go"}, true},
		{"ルートの go ファイル", []string{"main.go"}, true},
		{"一致しない", []string{"README.md", "docs/a.md"}, false},
		// * はスラッシュを跨がないので docs/a.go は *.go に一致しない。
		{"サブディレクトリの go ファイルは対象外", []string{"docs/a.go"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := mustMatch(t, w, &Event{Name: "push", Ref: "refs/heads/main", ChangedPaths: tt.changed})
			if res.Triggered != tt.want {
				t.Errorf("Triggered = %v, want %v（理由: %s）", res.Triggered, tt.want, res.Reason)
			}
		})
	}
}

// TestMatch_PathsIgnore は paths-ignore が「変更ファイルすべてが
// 除外対象のときだけ起動を止める」挙動であることを確かめる。
func TestMatch_PathsIgnore(t *testing.T) {
	w := parseWorkflow(t, `
on:
  push:
    paths-ignore: ['**.md', 'docs/**']
`)
	tests := []struct {
		name    string
		changed []string
		want    bool
	}{
		{"すべて除外対象なら起動しない", []string{"README.md", "docs/a.md"}, false},
		{"1つでも対象外があれば起動する", []string{"README.md", "src/main.go"}, true},
		{"除外に該当しない", []string{"src/main.go"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := mustMatch(t, w, &Event{Name: "push", Ref: "refs/heads/main", ChangedPaths: tt.changed})
			if res.Triggered != tt.want {
				t.Errorf("Triggered = %v, want %v（理由: %s）", res.Triggered, tt.want, res.Reason)
			}
		})
	}
}

// TestMatch_PathsDeferredWithoutChangedFiles は変更ファイルが未指定のとき
// paths フィルタの判定を保留し、その旨を注記することを確かめる。
func TestMatch_PathsDeferredWithoutChangedFiles(t *testing.T) {
	w := parseWorkflow(t, `
on:
  push:
    paths: ['src/**']
`)
	res := mustMatch(t, w, &Event{Name: "push", Ref: "refs/heads/main"})
	if !res.Triggered {
		t.Errorf("変更ファイル未指定なら起動する前提で進めるべきです: %s", res.Reason)
	}
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n, "変更ファイルが指定されていない") {
			found = true
		}
	}
	if !found {
		t.Errorf("判定を保留した注記が必要です: %v", res.Notes)
	}
}

// TestMatch_FiltersIgnoredForUnsupportedEvents は
// schedule や workflow_dispatch でフィルタが適用されないことを確かめる。
func TestMatch_FiltersIgnoredForUnsupportedEvents(t *testing.T) {
	w := parseWorkflow(t, `
on:
  schedule:
    - cron: '0 0 * * *'
  workflow_dispatch:
`)
	for _, name := range []string{"schedule", "workflow_dispatch"} {
		t.Run(name, func(t *testing.T) {
			res := mustMatch(t, w, &Event{Name: name})
			if !res.Triggered {
				t.Errorf("%s では起動するべきです: %s", name, res.Reason)
			}
		})
	}
}

func TestMatch_BadPatternReturnsError(t *testing.T) {
	w := parseWorkflow(t, `
on:
  push:
    branches: ['?bad']
`)
	if _, err := Match(w, &Event{Name: "push", Ref: "refs/heads/main"}); err == nil {
		t.Fatal("不正なパターンはエラーになるべきです")
	}
}

func TestNormalizeRef(t *testing.T) {
	tests := map[string]string{
		"main":             "refs/heads/main",
		"feature/x":        "refs/heads/feature/x",
		"refs/heads/main":  "refs/heads/main",
		"refs/tags/v1.0.0": "refs/tags/v1.0.0",
		"":                 "",
	}
	for in, want := range tests {
		if got := NormalizeRef(in); got != want {
			t.Errorf("NormalizeRef(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMatch_RealWorkflows は testdata の実サンプルで判定を確かめる。
func TestMatch_RealWorkflows(t *testing.T) {
	ws, err := workflow.LoadDir("../../testdata/workflows")
	if err != nil {
		t.Fatalf("ワークフローの読み込みに失敗しました: %v", err)
	}
	byName := map[string]*workflow.Workflow{}
	for _, w := range ws {
		byName[w.Name] = w
	}

	tests := []struct {
		workflow string
		ev       *Event
		want     bool
	}{
		// CI は main への push で起動する。
		{"CI", &Event{Name: "push", Ref: "refs/heads/main", ChangedPaths: []string{"src/a.go"}}, true},
		// ドキュメントのみの変更は paths-ignore で除外される。
		{"CI", &Event{Name: "push", Ref: "refs/heads/main", ChangedPaths: []string{"README.md"}}, false},
		// 対象外のブランチでは起動しない。
		{"CI", &Event{Name: "push", Ref: "refs/heads/topic/x"}, false},
		// release ブランチは release/** に一致する。
		{"CI", &Event{Name: "push", Ref: "refs/heads/release/1.0"}, true},
		// タグへの push では CI は起動しない。
		{"CI", &Event{Name: "push", Ref: "refs/tags/v1.0.0"}, false},
		// Release はタグへの push で起動する。
		{"Release", &Event{Name: "push", Ref: "refs/tags/v1.2.3"}, true},
		{"Release", &Event{Name: "push", Ref: "refs/heads/main"}, false},
		{"Release", &Event{Name: "workflow_dispatch"}, true},
		// Nightly は schedule と手動実行で起動する。
		{"Nightly", &Event{Name: "schedule"}, true},
		{"Nightly", &Event{Name: "push", Ref: "refs/heads/main"}, false},
	}
	for _, tt := range tests {
		w := byName[tt.workflow]
		if w == nil {
			t.Fatalf("ワークフロー %q が見つかりません", tt.workflow)
		}
		name := tt.workflow + "/" + tt.ev.Name + "/" + tt.ev.Ref
		t.Run(name, func(t *testing.T) {
			res := mustMatch(t, w, tt.ev)
			if res.Triggered != tt.want {
				t.Errorf("Triggered = %v, want %v（理由: %s）", res.Triggered, tt.want, res.Reason)
			}
		})
	}
}
