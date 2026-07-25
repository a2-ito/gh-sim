// Package ghctx は式評価に渡す GitHub Actions のコンテキストを組み立てる。
package ghctx

import (
	"fmt"
	"sort"
	"strings"

	"github.com/a2-ito/gh-sim/internal/expr"
	"github.com/a2-ito/gh-sim/internal/trigger"
	"github.com/a2-ito/gh-sim/internal/workflow"
)

// Config はドライランの入力条件を表す。
type Config struct {
	// Event はシミュレートするイベント。
	Event *trigger.Event

	// Repository は owner/repo 形式のリポジトリ名。
	Repository string
	// Actor はイベントを起こしたユーザー名。
	Actor string
	// SHA は対象のコミット SHA。
	SHA string
	// RunID と RunNumber は実行の識別子。
	RunID     string
	RunNumber int

	// Inputs は workflow_dispatch の入力。
	Inputs map[string]string
	// Env は追加の環境変数。ワークフローの env より先に読み込まれる。
	Env map[string]string
	// Vars はリポジトリ変数。
	Vars map[string]string
	// Secrets はシークレット。指定しなかったものは null として評価される。
	Secrets map[string]string

	// Outputs はジョブの出力。"ジョブID.出力名" をキーにする。
	// 出力は実行しないと確定しないため、ここで与えられなかったものは
	// 不明値として扱われ、それに依存する条件は判定不能になる。
	Outputs map[string]string

	// RunnerOS はランナーの OS 表記。空の場合は runs-on のラベルから推定する。
	RunnerOS string

	// WorkDir は hashFiles の基準になるディレクトリ。
	WorkDir string
	// ServerURL は github.server_url の値。
	ServerURL string
}

// Normalize は未指定の項目に既定値を入れる。
func (c *Config) Normalize() {
	if c.Event == nil {
		c.Event = &trigger.Event{Name: "push"}
	}
	c.Event.Ref = trigger.NormalizeRef(c.Event.Ref)

	if c.Repository == "" {
		c.Repository = "owner/repo"
	}
	if c.Actor == "" {
		c.Actor = "octocat"
	}
	if c.SHA == "" {
		// 実際の SHA は分からないため、桁数だけ合わせた値を入れる。
		c.SHA = strings.Repeat("0", 40)
	}
	if c.RunID == "" {
		c.RunID = "1"
	}
	if c.RunNumber == 0 {
		c.RunNumber = 1
	}
	if c.WorkDir == "" {
		c.WorkDir = "."
	}
	if c.ServerURL == "" {
		c.ServerURL = "https://github.com"
	}
}

// Base はワークフロー全体で共通のコンテキストを組み立てる。
// matrix や needs のようにジョブごとに変わるものは空の値で登録しておき、
// ジョブ単位の評価時に差し替える。
// 戻り値の注記には、値を確定できなかった項目が入る。
func Base(w *workflow.Workflow, cfg *Config) (*expr.Context, []string) {
	cfg.Normalize()

	var notes []string
	ctx := expr.NewContext()
	ctx.HashFiles = func(patterns []string) (string, error) {
		return HashFiles(cfg.WorkDir, patterns)
	}

	gh, ghNotes := buildGitHub(w, cfg)
	ctx.Set("github", expr.Obj(gh))
	notes = append(notes, ghNotes...)

	inputs, inNotes := buildInputs(w, cfg)
	ctx.Set("inputs", expr.Obj(inputs))
	notes = append(notes, inNotes...)

	ctx.Set("vars", expr.Obj(stringObject(cfg.Vars)))

	secrets, secNotes := buildSecrets(cfg)
	ctx.Set("secrets", expr.Obj(secrets))
	notes = append(notes, secNotes...)

	// env はワークフローの env を評価して重ねる。
	base := stringObject(cfg.Env)
	env, envNotes := EvalEnv(base, w.Env, ctx)
	ctx.Set("env", expr.Obj(env))
	notes = append(notes, envNotes...)

	// ジョブ単位で差し替えるものは、参照できるように空で登録しておく。
	ctx.Set("matrix", expr.Null())
	ctx.Set("needs", expr.Obj(expr.NewObject()))
	ctx.Set("steps", expr.Obj(expr.NewObject()))
	ctx.Set("job", expr.Obj(buildJobContext()))
	ctx.Set("runner", expr.Obj(buildRunner(cfg.RunnerOS)))
	ctx.Set("strategy", expr.Obj(expr.NewObject()))

	return ctx, notes
}

// buildGitHub は github コンテキストを組み立てる。
func buildGitHub(w *workflow.Workflow, cfg *Config) (*expr.Object, []string) {
	ev := cfg.Event
	var notes []string

	o := expr.NewObject()
	o.Set("event_name", expr.String(ev.Name))
	o.Set("ref", expr.String(ev.Ref))
	o.Set("ref_name", expr.String(refName(ev.Ref)))
	o.Set("ref_type", expr.String(refType(ev.Ref)))
	o.Set("sha", expr.String(cfg.SHA))
	o.Set("actor", expr.String(cfg.Actor))
	o.Set("triggering_actor", expr.String(cfg.Actor))
	o.Set("repository", expr.String(cfg.Repository))
	o.Set("repository_owner", expr.String(ownerOf(cfg.Repository)))
	o.Set("repository_url", expr.String(cfg.ServerURL+"/"+cfg.Repository))
	o.Set("workflow", expr.String(w.DisplayName()))
	o.Set("workflow_ref", expr.String(fmt.Sprintf("%s/%s@%s", cfg.Repository, w.Path, ev.Ref)))
	o.Set("run_id", expr.String(cfg.RunID))
	o.Set("run_number", expr.Number(float64(cfg.RunNumber)))
	o.Set("run_attempt", expr.Number(1))
	o.Set("workspace", expr.String(cfg.WorkDir))
	o.Set("server_url", expr.String(cfg.ServerURL))
	o.Set("api_url", expr.String("https://api.github.com"))
	o.Set("graphql_url", expr.String("https://api.github.com/graphql"))
	o.Set("action", expr.String("__run"))
	o.Set("job", expr.String(""))

	// base_ref と head_ref は pull_request 系でのみ値が入る。
	if isPullRequest(ev.Name) {
		o.Set("base_ref", expr.String(ev.BaseRef))
		o.Set("head_ref", expr.String(refName(ev.Ref)))
	} else {
		o.Set("base_ref", expr.String(""))
		o.Set("head_ref", expr.String(""))
	}

	// token は値を持たないが、参照できるようにしておく。
	o.Set("token", expr.String(""))

	if len(ev.Payload) > 0 {
		o.Set("event", expr.FromGo(ev.Payload))
	} else {
		o.Set("event", expr.Obj(synthesizeEvent(cfg)))
		notes = append(notes, "イベントペイロードが未指定のため github.event は最小限の内容で補っています")
	}

	return o, notes
}

// synthesizeEvent はペイロードが与えられていない場合に、
// よく参照されるフィールドだけを持つ最小限の github.event を作る。
func synthesizeEvent(cfg *Config) *expr.Object {
	ev := cfg.Event
	o := expr.NewObject()

	repo := expr.NewObject()
	repo.Set("name", nameOf(cfg.Repository))
	repo.Set("full_name", expr.String(cfg.Repository))
	repo.Set("default_branch", expr.String("main"))
	o.Set("repository", expr.Obj(repo))

	sender := expr.NewObject()
	sender.Set("login", expr.String(cfg.Actor))
	o.Set("sender", expr.Obj(sender))

	if ev.Action != "" {
		o.Set("action", expr.String(ev.Action))
	}

	switch {
	case isPullRequest(ev.Name):
		pr := expr.NewObject()
		pr.Set("number", expr.Number(1))
		pr.Set("draft", expr.Bool(false))
		pr.Set("merged", expr.Bool(false))

		base := expr.NewObject()
		base.Set("ref", expr.String(ev.BaseRef))
		pr.Set("base", expr.Obj(base))

		head := expr.NewObject()
		head.Set("ref", expr.String(refName(ev.Ref)))
		head.Set("sha", expr.String(cfg.SHA))
		pr.Set("head", expr.Obj(head))

		pr.Set("labels", expr.Array(nil))
		o.Set("pull_request", expr.Obj(pr))
		o.Set("number", expr.Number(1))

	case ev.Name == "push":
		o.Set("ref", expr.String(ev.Ref))
		o.Set("before", expr.String(strings.Repeat("0", 40)))
		o.Set("after", expr.String(cfg.SHA))
		o.Set("commits", expr.Array(nil))
	}

	return o
}

// buildInputs は workflow_dispatch / workflow_call の入力を組み立てる。
func buildInputs(w *workflow.Workflow, cfg *Config) (*expr.Object, []string) {
	o := expr.NewObject()
	var notes []string

	for _, event := range []string{"workflow_dispatch", "workflow_call"} {
		def := w.On.Find(event)
		if def == nil {
			continue
		}
		for _, key := range def.InputOrder {
			in := def.Inputs[key]
			if given, ok := cfg.Inputs[key]; ok {
				o.Set(key, convertInput(in.Type, given))
				continue
			}
			if in.Default.Set {
				o.Set(key, convertInput(in.Type, in.Default.Raw))
				continue
			}
			if in.Required {
				notes = append(notes, fmt.Sprintf("on.%s.inputs.%s は必須ですが値が指定されていません", event, key))
			}
			o.Set(key, zeroForType(in.Type))
		}
	}

	// 定義に無い入力が指定された場合も参照できるようにしておく。
	for _, k := range sortedKeys(cfg.Inputs) {
		if _, ok := o.Get(k); !ok {
			o.Set(k, expr.String(cfg.Inputs[k]))
		}
	}
	return o, notes
}

// convertInput は入力の型指定に合わせて値を変換する。
func convertInput(typ, raw string) expr.Value {
	switch typ {
	case "boolean":
		return expr.Bool(strings.EqualFold(strings.TrimSpace(raw), "true"))
	case "number":
		return expr.FromGo(expr.String(raw).AsNumber())
	}
	return expr.String(raw)
}

// zeroForType は値が指定されなかった入力の既定値を返す。
func zeroForType(typ string) expr.Value {
	switch typ {
	case "boolean":
		return expr.Bool(false)
	case "number":
		return expr.Number(0)
	}
	return expr.String("")
}

// buildSecrets は secrets コンテキストを組み立てる。
// 値を与えられていないシークレットは null になるため、
// secrets.X != ” のような条件は実際の結果と異なりうる。
func buildSecrets(cfg *Config) (*expr.Object, []string) {
	o := stringObject(cfg.Secrets)
	// GITHUB_TOKEN は常に存在するため、指定が無くても空文字列で登録する。
	if _, ok := o.Get("GITHUB_TOKEN"); !ok {
		o.Set("GITHUB_TOKEN", expr.String(""))
	}
	if len(cfg.Secrets) == 0 {
		return o, []string{"secrets が未指定のため、参照はすべて null として評価されます"}
	}
	return o, nil
}

// buildJobContext は job コンテキストを組み立てる。
// ドライランでは実行結果が無いため status は success として扱う。
func buildJobContext() *expr.Object {
	o := expr.NewObject()
	o.Set("status", expr.String("success"))
	o.Set("container", expr.Obj(expr.NewObject()))
	o.Set("services", expr.Obj(expr.NewObject()))
	return o
}

// buildRunner は runner コンテキストを組み立てる。
func buildRunner(os string) *expr.Object {
	o := expr.NewObject()
	o.Set("os", expr.String(os))
	o.Set("arch", expr.String("X64"))
	o.Set("name", expr.String("gh-sim"))
	o.Set("temp", expr.String("/tmp"))
	o.Set("tool_cache", expr.String("/opt/hostedtoolcache"))
	o.Set("debug", expr.String(""))
	return o
}

// RunnerOSFromLabels は runs-on のラベルからランナーの OS を推定する。
// 推定できない場合は空文字列を返す。
func RunnerOSFromLabels(labels []string) string {
	for _, l := range labels {
		lower := strings.ToLower(l)
		switch {
		case strings.HasPrefix(lower, "ubuntu"), strings.Contains(lower, "linux"):
			return "Linux"
		case strings.HasPrefix(lower, "macos"), strings.Contains(lower, "darwin"):
			return "macOS"
		case strings.HasPrefix(lower, "windows"):
			return "Windows"
		}
	}
	return ""
}

// WithRunner はランナー情報を差し替えたコンテキストを返す。
func WithRunner(ctx *expr.Context, os string) *expr.Context {
	return ctx.Clone().Set("runner", expr.Obj(buildRunner(os)))
}

// EvalEnv は env のマップを評価して親スコープに重ねる。
//
// GitHub では env の値から同じ env ブロック内の別のキーを参照できないため、
// 評価には親スコープの env だけを使う。
func EvalEnv(parent *expr.Object, m workflow.StringMap, ctx *expr.Context) (*expr.Object, []string) {
	out := parent.Clone()
	if len(m) == 0 {
		return out, nil
	}

	evalCtx := ctx.Clone().Set("env", expr.Obj(parent))
	var notes []string
	for _, k := range sortedKeys(m) {
		tmpl, err := expr.CompileTemplate(m[k])
		if err != nil {
			notes = append(notes, fmt.Sprintf("env.%s の式を解析できません: %v", k, err))
			continue
		}
		s, err := tmpl.Eval(evalCtx)
		if err != nil {
			notes = append(notes, fmt.Sprintf("env.%s を評価できません: %v", k, err))
			continue
		}
		out.Set(k, expr.String(s))
	}
	return out, notes
}

// stringObject は文字列のマップをオブジェクトに変換する。
func stringObject[V ~string](m map[string]V) *expr.Object {
	o := expr.NewObject()
	for _, k := range sortedKeys(m) {
		o.Set(k, expr.String(string(m[k])))
	}
	return o
}

// sortedKeys はマップのキーを昇順で返す。出力を安定させるために使う。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// refName は refs/heads/main から main のように表示用の名前を取り出す。
func refName(ref string) string {
	for _, prefix := range []string{"refs/heads/", "refs/tags/"} {
		if s, ok := strings.CutPrefix(ref, prefix); ok {
			return s
		}
	}
	return ref
}

// refType は ref の種別を返す。ブランチでもタグでもない場合は空文字列。
func refType(ref string) string {
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		return "branch"
	case strings.HasPrefix(ref, "refs/tags/"):
		return "tag"
	}
	return ""
}

// ownerOf は owner/repo からオーナー名を取り出す。
func ownerOf(repository string) string {
	if owner, _, ok := strings.Cut(repository, "/"); ok {
		return owner
	}
	return repository
}

// nameOf は owner/repo からリポジトリ名を取り出す。
func nameOf(repository string) expr.Value {
	if _, name, ok := strings.Cut(repository, "/"); ok {
		return expr.String(name)
	}
	return expr.String(repository)
}

// isPullRequest は pull_request 系のイベントかを返す。
func isPullRequest(event string) bool {
	return event == "pull_request" || event == "pull_request_target"
}
