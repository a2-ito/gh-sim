package ghctx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a2-ito/gh-sim/internal/expr"
	"github.com/a2-ito/gh-sim/internal/trigger"
	"github.com/a2-ito/gh-sim/internal/workflow"
)

const testWorkflow = `
name: CI
on:
  push:
  workflow_dispatch:
    inputs:
      dry-run:
        type: boolean
        default: true
      count:
        type: number
        default: 3
      target:
        type: string
        required: true
jobs:
  a:
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`

func testBase(t *testing.T, cfg *Config) (*expr.Context, []string) {
	t.Helper()
	w, err := workflow.Parse([]byte(testWorkflow), "ci.yml")
	if err != nil {
		t.Fatalf("ワークフローの解析に失敗しました: %v", err)
	}
	if cfg == nil {
		cfg = &Config{Event: &trigger.Event{Name: "push", Ref: "refs/heads/main"}}
	}
	return Base(w, cfg)
}

// evalStr はコンテキストのもとで式を評価し、文字列として返す。
func evalStr(t *testing.T, ctx *expr.Context, src string) string {
	t.Helper()
	v, err := expr.Evaluate(src, ctx)
	if err != nil {
		t.Fatalf("式 %q の評価に失敗しました: %v", src, err)
	}
	return v.AsString()
}

func TestBase_GitHubContext(t *testing.T) {
	ctx, _ := testBase(t, &Config{
		Event:      &trigger.Event{Name: "push", Ref: "refs/heads/feature/x"},
		Repository: "edash/app",
		Actor:      "alice",
		SHA:        "abc123",
	})

	tests := map[string]string{
		"github.event_name":       "push",
		"github.ref":              "refs/heads/feature/x",
		"github.ref_name":         "feature/x",
		"github.ref_type":         "branch",
		"github.repository":       "edash/app",
		"github.repository_owner": "edash",
		"github.actor":            "alice",
		"github.sha":              "abc123",
		"github.workflow":         "CI",
		"github.run_number":       "1",
	}
	for src, want := range tests {
		if got := evalStr(t, ctx, src); got != want {
			t.Errorf("%s = %q, want %q", src, got, want)
		}
	}
}

// TestBase_RefTypeForTags はタグの ref が正しく解釈されることを確かめる。
func TestBase_RefTypeForTags(t *testing.T) {
	ctx, _ := testBase(t, &Config{
		Event: &trigger.Event{Name: "push", Ref: "refs/tags/v1.2.3"},
	})
	if got := evalStr(t, ctx, "github.ref_type"); got != "tag" {
		t.Errorf("github.ref_type = %q, want tag", got)
	}
	if got := evalStr(t, ctx, "github.ref_name"); got != "v1.2.3" {
		t.Errorf("github.ref_name = %q, want v1.2.3", got)
	}
}

// TestBase_PullRequestRefs は pull_request で base_ref と head_ref が
// 入り、それ以外のイベントでは空になることを確かめる。
func TestBase_PullRequestRefs(t *testing.T) {
	ctx, _ := testBase(t, &Config{
		Event: &trigger.Event{
			Name:    "pull_request",
			Action:  "opened",
			Ref:     "refs/heads/feature/x",
			BaseRef: "main",
		},
	})
	if got := evalStr(t, ctx, "github.base_ref"); got != "main" {
		t.Errorf("github.base_ref = %q, want main", got)
	}
	if got := evalStr(t, ctx, "github.head_ref"); got != "feature/x" {
		t.Errorf("github.head_ref = %q, want feature/x", got)
	}
	// 合成されたペイロードから base のブランチを辿れる。
	if got := evalStr(t, ctx, "github.event.pull_request.base.ref"); got != "main" {
		t.Errorf("github.event.pull_request.base.ref = %q, want main", got)
	}

	// push では両方空になる。
	ctx, _ = testBase(t, nil)
	if got := evalStr(t, ctx, "github.base_ref"); got != "" {
		t.Errorf("push では base_ref は空であるべきです: %q", got)
	}
}

// TestBase_PayloadOverridesSynthesized は与えたペイロードが
// 合成された内容より優先されることを確かめる。
func TestBase_PayloadOverridesSynthesized(t *testing.T) {
	ctx, notes := testBase(t, &Config{
		Event: &trigger.Event{
			Name: "pull_request",
			Ref:  "refs/heads/x",
			Payload: map[string]any{
				"pull_request": map[string]any{"number": 42, "draft": true},
			},
		},
	})
	if got := evalStr(t, ctx, "github.event.pull_request.number"); got != "42" {
		t.Errorf("与えたペイロードが使われるべきです: %q", got)
	}
	for _, n := range notes {
		if strings.Contains(n, "最小限の内容で補って") {
			t.Errorf("ペイロードを与えた場合に補完の注記は不要です: %v", notes)
		}
	}
}

func TestBase_SynthesizedPayloadIsNoted(t *testing.T) {
	_, notes := testBase(t, nil)
	found := false
	for _, n := range notes {
		if strings.Contains(n, "最小限の内容で補って") {
			found = true
		}
	}
	if !found {
		t.Errorf("ペイロードを補ったことを注記するべきです: %v", notes)
	}
}

// TestBase_InputsDefaultsAndTypes は入力の既定値と型変換を確かめる。
func TestBase_InputsDefaultsAndTypes(t *testing.T) {
	ctx, notes := testBase(t, &Config{Event: &trigger.Event{Name: "workflow_dispatch"}})

	// boolean と number は型を保って解釈される。
	v, err := expr.Evaluate("inputs.dry-run", ctx)
	if err != nil {
		t.Fatalf("評価に失敗しました: %v", err)
	}
	if v.Kind() != expr.KindBool || !v.Truthy() {
		t.Errorf("inputs.dry-run は真偽値の true になるべきです: %s %v", v.Kind(), v.AsString())
	}
	v, err = expr.Evaluate("inputs.count", ctx)
	if err != nil {
		t.Fatalf("評価に失敗しました: %v", err)
	}
	if v.Kind() != expr.KindNumber || v.AsString() != "3" {
		t.Errorf("inputs.count は数値の3になるべきです: %s %v", v.Kind(), v.AsString())
	}

	// 既定値が無い必須入力は空文字列になり、注記が残る。
	if got := evalStr(t, ctx, "inputs.target"); got != "" {
		t.Errorf("inputs.target = %q, want 空文字列", got)
	}
	found := false
	for _, n := range notes {
		if strings.Contains(n, "inputs.target は必須ですが") {
			found = true
		}
	}
	if !found {
		t.Errorf("必須入力が未指定であることを注記するべきです: %v", notes)
	}
}

func TestBase_InputsFromConfig(t *testing.T) {
	ctx, _ := testBase(t, &Config{
		Event:  &trigger.Event{Name: "workflow_dispatch"},
		Inputs: map[string]string{"dry-run": "false", "target": "prod", "extra": "x"},
	})

	v, _ := expr.Evaluate("inputs.dry-run", ctx)
	if v.Kind() != expr.KindBool || v.Truthy() {
		t.Errorf("inputs.dry-run は false になるべきです: %v", v.AsString())
	}
	if got := evalStr(t, ctx, "inputs.target"); got != "prod" {
		t.Errorf("inputs.target = %q", got)
	}
	// 定義に無い入力も参照できる。
	if got := evalStr(t, ctx, "inputs.extra"); got != "x" {
		t.Errorf("inputs.extra = %q", got)
	}
}

// TestBase_SecretsAreNullByDefault は未指定のシークレットが null になり、
// その旨が注記されることを確かめる。判定がずれうる点を隠さないための挙動。
func TestBase_SecretsAreNullByDefault(t *testing.T) {
	ctx, notes := testBase(t, nil)

	v, err := expr.Evaluate("secrets.MY_TOKEN", ctx)
	if err != nil {
		t.Fatalf("評価に失敗しました: %v", err)
	}
	if !v.IsNull() {
		t.Errorf("未指定のシークレットは null になるべきです: %s", v.Kind())
	}
	found := false
	for _, n := range notes {
		if strings.Contains(n, "secrets が未指定") {
			found = true
		}
	}
	if !found {
		t.Errorf("シークレット未指定を注記するべきです: %v", notes)
	}

	// GITHUB_TOKEN は常に参照できる。
	v, _ = expr.Evaluate("secrets.GITHUB_TOKEN", ctx)
	if v.IsNull() {
		t.Error("GITHUB_TOKEN は空文字列として参照できるべきです")
	}
}

func TestBase_SecretsFromConfig(t *testing.T) {
	ctx, notes := testBase(t, &Config{
		Event:   &trigger.Event{Name: "push", Ref: "refs/heads/main"},
		Secrets: map[string]string{"MY_TOKEN": "s3cret"},
	})
	if got := evalStr(t, ctx, "secrets.MY_TOKEN"); got != "s3cret" {
		t.Errorf("secrets.MY_TOKEN = %q", got)
	}
	for _, n := range notes {
		if strings.Contains(n, "secrets が未指定") {
			t.Errorf("シークレットを与えた場合に注記は不要です: %v", notes)
		}
	}
}

func TestBase_VarsAndEnv(t *testing.T) {
	ctx, _ := testBase(t, &Config{
		Event: &trigger.Event{Name: "push", Ref: "refs/heads/main"},
		Vars:  map[string]string{"REGION": "ap-northeast-1"},
		Env:   map[string]string{"EXTRA": "1"},
	})
	if got := evalStr(t, ctx, "vars.REGION"); got != "ap-northeast-1" {
		t.Errorf("vars.REGION = %q", got)
	}
	if got := evalStr(t, ctx, "env.EXTRA"); got != "1" {
		t.Errorf("env.EXTRA = %q", got)
	}
}

// TestEvalEnv は env の値に含まれる式が解決されることを確かめる。
func TestEvalEnv(t *testing.T) {
	ctx, _ := testBase(t, nil)

	parent := expr.NewObject()
	parent.Set("BASE", expr.String("base-value"))

	env, notes := EvalEnv(parent, workflow.StringMap{
		"PLAIN":    "literal",
		"FROM_CTX": "${{ github.ref_name }}",
		"FROM_ENV": "${{ env.BASE }}-suffix",
		"BROKEN":   "${{ nosuchcontext.x }}",
	}, ctx)

	tests := map[string]string{
		"BASE":     "base-value",
		"PLAIN":    "literal",
		"FROM_CTX": "main",
		"FROM_ENV": "base-value-suffix",
	}
	for k, want := range tests {
		v, ok := env.Get(k)
		if !ok {
			t.Errorf("env.%s が設定されていません", k)
			continue
		}
		if got := v.AsString(); got != want {
			t.Errorf("env.%s = %q, want %q", k, got, want)
		}
	}

	// 評価できない値は設定せず、注記を残す。
	if _, ok := env.Get("BROKEN"); ok {
		t.Error("評価できない env は設定しないべきです")
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "BROKEN") {
		t.Errorf("評価できなかったことを注記するべきです: %v", notes)
	}
}

func TestRunnerOSFromLabels(t *testing.T) {
	tests := []struct {
		labels []string
		want   string
	}{
		{[]string{"ubuntu-latest"}, "Linux"},
		{[]string{"ubuntu-22.04"}, "Linux"},
		{[]string{"macos-latest"}, "macOS"},
		{[]string{"macos-14"}, "macOS"},
		{[]string{"windows-latest"}, "Windows"},
		{[]string{"self-hosted", "linux", "x64"}, "Linux"},
		{[]string{"self-hosted"}, ""},
		{nil, ""},
	}
	for _, tt := range tests {
		if got := RunnerOSFromLabels(tt.labels); got != tt.want {
			t.Errorf("RunnerOSFromLabels(%v) = %q, want %q", tt.labels, got, tt.want)
		}
	}
}

func TestWithRunner(t *testing.T) {
	ctx, _ := testBase(t, nil)
	ctx = WithRunner(ctx, "macOS")
	if got := evalStr(t, ctx, "runner.os"); got != "macOS" {
		t.Errorf("runner.os = %q, want macOS", got)
	}
}

func TestConfig_Normalize(t *testing.T) {
	cfg := &Config{Event: &trigger.Event{Name: "push", Ref: "main"}}
	cfg.Normalize()

	// 短い ref はブランチとして補われる。
	if cfg.Event.Ref != "refs/heads/main" {
		t.Errorf("ref = %q, want refs/heads/main", cfg.Event.Ref)
	}
	if cfg.Repository == "" || cfg.Actor == "" || cfg.RunID == "" {
		t.Errorf("既定値が入るべきです: %+v", cfg)
	}
	if len(cfg.SHA) != 40 {
		t.Errorf("SHA は40桁になるべきです: %q", cfg.SHA)
	}

	// イベントを省略した場合も既定値で動く。
	empty := &Config{}
	empty.Normalize()
	if empty.Event == nil || empty.Event.Name != "push" {
		t.Errorf("イベントの既定値が入るべきです: %+v", empty.Event)
	}
}

// TestBase_ContextsAreRegistered は式から参照できるコンテキストが
// 値が空でも登録されていることを確かめる。
// 未登録の名前はエラーになるため、参照を許すものは必ず登録しておく必要がある。
func TestBase_ContextsAreRegistered(t *testing.T) {
	ctx, _ := testBase(t, nil)
	for _, name := range []string{
		"github", "env", "vars", "secrets", "inputs",
		"matrix", "needs", "steps", "job", "runner", "strategy",
	} {
		if _, ok := ctx.Vars[name]; !ok {
			t.Errorf("コンテキスト %q が登録されていません", name)
		}
	}
}

// TestHashFiles は hashFiles の計算が内容に依存し、
// 一致するファイルが無い場合は空文字列になることを確かめる。
func TestHashFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.sum"), "aaa")
	writeFile(t, filepath.Join(dir, "sub", "go.sum"), "bbb")
	writeFile(t, filepath.Join(dir, "README.md"), "docs")

	h1, err := HashFiles(dir, []string{"**/go.sum"})
	if err != nil {
		t.Fatalf("計算に失敗しました: %v", err)
	}
	if h1 == "" {
		t.Fatal("一致するファイルがあるのでハッシュが返るべきです")
	}
	// SHA-256 の16進表記は64文字。
	if len(h1) != 64 {
		t.Errorf("ハッシュの長さが想定と違います: %d", len(h1))
	}

	// 同じ内容なら同じ結果になる。
	h2, err := HashFiles(dir, []string{"**/go.sum"})
	if err != nil {
		t.Fatalf("計算に失敗しました: %v", err)
	}
	if h1 != h2 {
		t.Error("同じ内容なら同じハッシュになるべきです")
	}

	// 内容が変わればハッシュも変わる。
	writeFile(t, filepath.Join(dir, "go.sum"), "ccc")
	h3, err := HashFiles(dir, []string{"**/go.sum"})
	if err != nil {
		t.Fatalf("計算に失敗しました: %v", err)
	}
	if h1 == h3 {
		t.Error("内容が変わればハッシュも変わるべきです")
	}

	// 対象が変われば結果も変わる。
	h4, err := HashFiles(dir, []string{"*.md"})
	if err != nil {
		t.Fatalf("計算に失敗しました: %v", err)
	}
	if h4 == h3 {
		t.Error("対象ファイルが違えばハッシュも違うべきです")
	}

	// 一致するファイルが無い場合は空文字列。
	h5, err := HashFiles(dir, []string{"nosuch/**"})
	if err != nil {
		t.Fatalf("計算に失敗しました: %v", err)
	}
	if h5 != "" {
		t.Errorf("一致が無い場合は空文字列になるべきです: %q", h5)
	}
}

// TestHashFilesThroughExpression は式から hashFiles を呼べることを確かめる。
func TestHashFilesThroughExpression(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.sum"), "aaa")

	ctx, _ := testBase(t, &Config{
		Event:   &trigger.Event{Name: "push", Ref: "refs/heads/main"},
		WorkDir: dir,
	})
	got := evalStr(t, ctx, "hashFiles('**/go.sum')")
	if len(got) != 64 {
		t.Errorf("式から hashFiles を呼べるべきです: %q", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("ディレクトリを作れません: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("ファイルを書けません: %v", err)
	}
}
