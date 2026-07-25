package workflow

import (
	"reflect"
	"strings"
	"testing"
)

// minimalJobs は Validate を通すための最小のジョブ定義。
const minimalJobs = `
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`

func mustParse(t *testing.T, src string) *Workflow {
	t.Helper()
	w, err := Parse([]byte(src), "test.yml")
	if err != nil {
		t.Fatalf("パースに失敗しました: %v", err)
	}
	return w
}

// TestParse_OnKeyIsNotBool は on: が YAML 1.1 の真偽値として
// 解釈されずイベント定義として読めることを確かめる。
func TestParse_OnKeyIsNotBool(t *testing.T) {
	w := mustParse(t, "on: push"+minimalJobs)
	if got := w.On.Names(); !reflect.DeepEqual(got, []string{"push"}) {
		t.Errorf("on の解釈が想定と違います: got %v, want [push]", got)
	}
}

func TestParse_OnForms(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"スカラー", "on: push", []string{"push"}},
		{"配列", "on: [push, pull_request]", []string{"push", "pull_request"}},
		{
			"マップ",
			`
on:
  push:
    branches: [main]
  pull_request:
`,
			[]string{"push", "pull_request"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := mustParse(t, tt.src+minimalJobs)
			if got := w.On.Names(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("イベント名が想定と違います: got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParse_OnFilters(t *testing.T) {
	w := mustParse(t, `
on:
  push:
    branches: [main, 'release/**']
    paths-ignore:
      - '**.md'
  pull_request:
    types: [opened, synchronize]
    branches-ignore: [wip]
  schedule:
    - cron: '0 0 * * *'
    - cron: '30 5 * * 1'
  workflow_dispatch:
    inputs:
      env:
        description: デプロイ先
        required: true
        type: choice
        options: [dev, prod]
      dry-run:
        type: boolean
        default: true
`+minimalJobs)

	push := w.On.Find("push")
	if push == nil {
		t.Fatal("push イベントが見つかりません")
	}
	if !reflect.DeepEqual(push.Branches, []string{"main", "release/**"}) {
		t.Errorf("push.branches: got %v", push.Branches)
	}
	if !reflect.DeepEqual(push.PathsIgnore, []string{"**.md"}) {
		t.Errorf("push.paths-ignore: got %v", push.PathsIgnore)
	}

	pr := w.On.Find("pull_request")
	if pr == nil {
		t.Fatal("pull_request イベントが見つかりません")
	}
	if !reflect.DeepEqual(pr.Types, []string{"opened", "synchronize"}) {
		t.Errorf("pull_request.types: got %v", pr.Types)
	}
	if !reflect.DeepEqual(pr.BranchesIgnore, []string{"wip"}) {
		t.Errorf("pull_request.branches-ignore: got %v", pr.BranchesIgnore)
	}

	sched := w.On.Find("schedule")
	if sched == nil {
		t.Fatal("schedule イベントが見つかりません")
	}
	if !reflect.DeepEqual(sched.Cron, []string{"0 0 * * *", "30 5 * * 1"}) {
		t.Errorf("schedule.cron: got %v", sched.Cron)
	}

	wd := w.On.Find("workflow_dispatch")
	if wd == nil {
		t.Fatal("workflow_dispatch イベントが見つかりません")
	}
	if !reflect.DeepEqual(wd.InputOrder, []string{"env", "dry-run"}) {
		t.Errorf("inputs の定義順が保たれていません: got %v", wd.InputOrder)
	}
	if in := wd.Inputs["env"]; in == nil || !in.Required || !reflect.DeepEqual(in.Options, []string{"dev", "prod"}) {
		t.Errorf("inputs.env: got %+v", wd.Inputs["env"])
	}
	if in := wd.Inputs["dry-run"]; in == nil || in.Default.Raw != "true" {
		t.Errorf("inputs.dry-run の default: got %+v", wd.Inputs["dry-run"])
	}
}

func TestParse_OnConflictingFilters(t *testing.T) {
	_, err := Parse([]byte(`
on:
  push:
    branches: [main]
    branches-ignore: [wip]
`+minimalJobs), "test.yml")
	if err == nil {
		t.Fatal("branches と branches-ignore の併記はエラーになるべきです")
	}
	if !strings.Contains(err.Error(), "同時に指定できません") {
		t.Errorf("想定外のエラーです: %v", err)
	}
}

// TestParse_JobOrderPreserved はジョブの定義順が保たれることを確かめる。
// マップとして読むと順序が失われ、出力の並びが実行ごとに変わってしまう。
func TestParse_JobOrderPreserved(t *testing.T) {
	w := mustParse(t, `
on: push
jobs:
  zebra:
    runs-on: ubuntu-latest
    steps: [{run: echo 1}]
  alpha:
    runs-on: ubuntu-latest
    steps: [{run: echo 2}]
  middle:
    runs-on: ubuntu-latest
    steps: [{run: echo 3}]
`)
	want := []string{"zebra", "alpha", "middle"}
	if !reflect.DeepEqual(w.Jobs.Order, want) {
		t.Errorf("ジョブの定義順が保たれていません: got %v, want %v", w.Jobs.Order, want)
	}
}

func TestParse_NeedsForms(t *testing.T) {
	w := mustParse(t, `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps: [{run: echo a}]
  b:
    runs-on: ubuntu-latest
    steps: [{run: echo b}]
  single:
    needs: a
    runs-on: ubuntu-latest
    steps: [{run: echo c}]
  multi:
    needs: [a, b]
    runs-on: ubuntu-latest
    steps: [{run: echo d}]
`)
	if got := []string(w.Jobs.Items["single"].Needs); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("needs のスカラー形式: got %v", got)
	}
	if got := []string(w.Jobs.Items["multi"].Needs); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("needs の配列形式: got %v", got)
	}
}

func TestParse_RunsOnForms(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantLabels []string
		wantGroup  string
		wantString string
	}{
		{"スカラー", "runs-on: ubuntu-latest", []string{"ubuntu-latest"}, "", "ubuntu-latest"},
		{"配列", "runs-on: [self-hosted, linux, x64]", []string{"self-hosted", "linux", "x64"}, "", "self-hosted, linux, x64"},
		{
			"グループ指定",
			`runs-on:
      group: my-runners
      labels: [gpu]`,
			[]string{"gpu"}, "my-runners", "my-runners [gpu]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := mustParse(t, `
on: push
jobs:
  a:
    `+tt.src+`
    steps: [{run: echo hi}]
`)
			got := w.Jobs.Items["a"].RunsOn
			if !reflect.DeepEqual(got.Labels, tt.wantLabels) {
				t.Errorf("labels: got %v, want %v", got.Labels, tt.wantLabels)
			}
			if got.Group != tt.wantGroup {
				t.Errorf("group: got %q, want %q", got.Group, tt.wantGroup)
			}
			if got.String() != tt.wantString {
				t.Errorf("String(): got %q, want %q", got.String(), tt.wantString)
			}
		})
	}
}

// TestParse_EnvValuesBecomeStrings は env や with に数値や真偽値を書いても
// 文字列として取り込まれることを確かめる。Actions 上では常に文字列扱いになる。
func TestParse_EnvValuesBecomeStrings(t *testing.T) {
	w := mustParse(t, `
on: push
env:
  RETRY: 3
  DEBUG: true
  VERSION: 1.10
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with:
          node-version: 20
          cache: npm
`)
	want := StringMap{"RETRY": "3", "DEBUG": "true", "VERSION": "1.10"}
	if !reflect.DeepEqual(w.Env, want) {
		t.Errorf("env: got %v, want %v", w.Env, want)
	}
	gotWith := w.Jobs.Items["a"].Steps[0].With
	wantWith := StringMap{"node-version": "20", "cache": "npm"}
	if !reflect.DeepEqual(gotWith, wantWith) {
		t.Errorf("with: got %v, want %v", gotWith, wantWith)
	}
}

func TestParse_IfIsKeptRaw(t *testing.T) {
	w := mustParse(t, `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    if: ${{ github.ref == 'refs/heads/main' }}
    steps:
      - run: echo 1
        if: success()
      - run: echo 2
        if: true
      - run: echo 3
`)
	job := w.Jobs.Items["a"]
	if job.If.Raw != "${{ github.ref == 'refs/heads/main' }}" || !job.If.HasExpr() {
		t.Errorf("job の if: got %+v", job.If)
	}
	if got := job.Steps[0].If; got.Raw != "success()" || got.HasExpr() {
		t.Errorf("steps[0] の if: got %+v", got)
	}
	// if: true はリテラルの真偽値として読めるべき。
	if v, ok := job.Steps[1].If.Bool(); !ok || !v {
		t.Errorf("steps[1] の if: リテラル true として読めていません: %+v", job.Steps[1].If)
	}
	// if を書いていないステップは Set が false。
	if job.Steps[2].If.Set {
		t.Errorf("steps[2] は if を書いていないので Set は false であるべきです")
	}
}

func TestParse_Matrix(t *testing.T) {
	w := mustParse(t, `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      max-parallel: 2
      matrix:
        node: [18, 20, 22]
        os: [ubuntu-latest, macos-latest]
        include:
          - node: 22
            os: ubuntu-latest
            experimental: true
        exclude:
          - node: 18
            os: macos-latest
    steps: [{run: echo hi}]
  fromjson:
    runs-on: ubuntu-latest
    strategy:
      matrix: ${{ fromJSON(needs.setup.outputs.matrix) }}
    steps: [{run: echo hi}]
  axisexpr:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        node: ${{ fromJSON(inputs.versions) }}
    steps: [{run: echo hi}]
`)
	st := w.Jobs.Items["a"].Strategy
	if st == nil || st.Matrix == nil {
		t.Fatal("strategy.matrix が読めていません")
	}
	if !reflect.DeepEqual(st.Matrix.Order, []string{"node", "os"}) {
		t.Errorf("matrix の軸の定義順: got %v", st.Matrix.Order)
	}
	if !reflect.DeepEqual(st.Matrix.Axes["node"], []any{18, 20, 22}) {
		t.Errorf("matrix.node: got %#v", st.Matrix.Axes["node"])
	}
	if len(st.Matrix.Include) != 1 || st.Matrix.Include[0].Values["experimental"] != true {
		t.Errorf("matrix.include: got %#v", st.Matrix.Include)
	}
	// include のキーは定義順で保持される。展開結果の表示順に影響するため。
	if got := st.Matrix.Include[0].Keys; !reflect.DeepEqual(got, []string{"node", "os", "experimental"}) {
		t.Errorf("matrix.include のキー順: got %v", got)
	}
	if len(st.Matrix.Exclude) != 1 || st.Matrix.Exclude[0].Values["node"] != 18 {
		t.Errorf("matrix.exclude: got %#v", st.Matrix.Exclude)
	}
	if v, ok := st.FailFast.Bool(); !ok || v {
		t.Errorf("fail-fast: got %+v", st.FailFast)
	}
	if v, ok := st.MaxParallel.Int(); !ok || v != 2 {
		t.Errorf("max-parallel: got %+v", st.MaxParallel)
	}

	// matrix 全体を式で与えた場合。
	if got := w.Jobs.Items["fromjson"].Strategy.Matrix.Expr; got == "" {
		t.Error("matrix 全体の式が保持されていません")
	}
	// 軸の値を式で与えた場合。
	if got := w.Jobs.Items["axisexpr"].Strategy.Matrix.AxisExprs["node"]; got != "${{ fromJSON(inputs.versions) }}" {
		t.Errorf("軸の式が保持されていません: got %q", got)
	}
}

func TestParse_ContainerAndSecretsAndPermissions(t *testing.T) {
	w := mustParse(t, `
on: push
permissions:
  contents: read
  id-token: write
jobs:
  a:
    runs-on: ubuntu-latest
    container: node:20
    services:
      db:
        image: postgres:16
        ports: ['5432:5432']
    steps: [{run: echo hi}]
  call:
    uses: ./.github/workflows/reusable.yml
    permissions: read-all
    secrets: inherit
    with:
      target: prod
`)
	if got := w.Permissions.Scopes; !reflect.DeepEqual(map[string]string(got), map[string]string{"contents": "read", "id-token": "write"}) {
		t.Errorf("permissions のマップ形式: got %v", got)
	}

	a := w.Jobs.Items["a"]
	if a.Container == nil || a.Container.Image != "node:20" {
		t.Errorf("container の文字列形式: got %+v", a.Container)
	}
	if db := a.Services["db"]; db == nil || db.Image != "postgres:16" {
		t.Errorf("services.db: got %+v", a.Services["db"])
	}

	call := w.Jobs.Items["call"]
	if !call.IsReusableCall() {
		t.Error("uses を持つジョブは再利用可能ワークフロー呼び出しとして扱われるべきです")
	}
	if call.Permissions.All != "read-all" {
		t.Errorf("permissions の文字列形式: got %+v", call.Permissions)
	}
	if !call.Secrets.Inherit {
		t.Errorf("secrets: inherit: got %+v", call.Secrets)
	}
	if call.With["target"] != "prod" {
		t.Errorf("with.target: got %v", call.With)
	}
}

func TestParse_StepDisplayName(t *testing.T) {
	w := mustParse(t, `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - name: 明示的な名前
        run: echo 1
      - uses: actions/checkout@v4
      - run: |
          echo 1行目
          echo 2行目
`)
	steps := w.Jobs.Items["a"].Steps
	want := []string{"明示的な名前", "Run actions/checkout@v4", "Run echo 1行目"}
	for i, w := range want {
		if got := steps[i].DisplayName(); got != w {
			t.Errorf("steps[%d].DisplayName(): got %q, want %q", i, got, w)
		}
	}
}

func TestValidate_Errors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantMsg string
	}{
		{
			"on がない",
			`
jobs:
  a:
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`,
			"on: が指定されていません",
		},
		{
			"jobs が空",
			"on: push\njobs: {}\n",
			"ジョブが1つもありません",
		},
		{
			"存在しない needs",
			`
on: push
jobs:
  a:
    needs: [nosuchjob]
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`,
			`存在しないジョブ "nosuchjob"`,
		},
		{
			"自己参照する needs",
			`
on: push
jobs:
  a:
    needs: a
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`,
			"自分自身を needs に指定しています",
		},
		{
			"runs-on がない",
			`
on: push
jobs:
  a:
    steps: [{run: echo hi}]
`,
			"runs-on が指定されていません",
		},
		{
			"steps も uses もない",
			`
on: push
jobs:
  a:
    runs-on: ubuntu-latest
`,
			"steps または uses のどちらかが必要です",
		},
		{
			"uses と steps の併記",
			`
on: push
jobs:
  a:
    uses: ./.github/workflows/r.yml
    steps: [{run: echo hi}]
`,
			"uses と steps は同時に指定できません",
		},
		{
			"run と uses の併記",
			`
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        run: echo hi
`,
			"uses と run は同時に指定できません",
		},
		{
			"step の id 重複",
			`
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - id: dup
        run: echo 1
      - id: dup
        run: echo 2
`,
			`id "dup" が steps[0] と重複しています`,
		},
		{
			"job の重複",
			`
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps: [{run: echo 1}]
  a:
    runs-on: ubuntu-latest
    steps: [{run: echo 2}]
`,
			`job "a" が重複しています`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.src), "test.yml")
			if err == nil {
				t.Fatalf("エラーになるべきですが成功しました")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("エラーメッセージに %q が含まれるべきです:\n%v", tt.wantMsg, err)
			}
		})
	}
}
