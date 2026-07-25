package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/a2-ito/gh-sim/internal/ghctx"
	"github.com/a2-ito/gh-sim/internal/plan"
	"github.com/a2-ito/gh-sim/internal/trigger"
	"github.com/a2-ito/gh-sim/internal/workflow"
)

func buildPlans(t *testing.T, src string, cfg *ghctx.Config) []*plan.Plan {
	t.Helper()
	w, err := workflow.Parse([]byte(src), "test.yml")
	if err != nil {
		t.Fatalf("ワークフローの解析に失敗しました: %v", err)
	}
	if cfg == nil {
		cfg = &ghctx.Config{Event: &trigger.Event{Name: "push", Ref: "refs/heads/main"}}
	}
	p, err := plan.Build(w, cfg)
	if err != nil {
		t.Fatalf("計画の作成に失敗しました: %v", err)
	}
	return []*plan.Plan{p}
}

func render(t *testing.T, plans []*plan.Plan, opts Options) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, plans, opts); err != nil {
		t.Fatalf("出力に失敗しました: %v", err)
	}
	return buf.String()
}

// TestWrite_Tree はツリー出力の形を固定する。
func TestWrite_Tree(t *testing.T) {
	plans := buildPlans(t, `
name: CI
on: push
jobs:
  lint:
    runs-on: ubuntu-latest
    steps: [{run: npm run lint}]
  deploy:
    needs: lint
    if: false
    runs-on: ubuntu-latest
    steps: [{run: ./deploy.sh}]
`, nil)

	got := render(t, plans, Options{})
	want := `CI  test.yml
  ✔ 起動する — on.push に一致します
  ├─ ✔ lint  ubuntu-latest · ステップ 1
  └─ ✘ deploy  ubuntu-latest · ステップ 1 · needs: lint
     if が偽: false
ワークフロー 1 件中 1 件が起動 · ジョブ 実行 1 件 · スキップ 1 件
`
	if got != want {
		t.Errorf("出力が想定と違います:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestWrite_MatrixTree は matrix があるジョブが
// 組み合わせを子ノードとして並べることを確かめる。
func TestWrite_MatrixTree(t *testing.T) {
	plans := buildPlans(t, `
name: CI
on: push
jobs:
  test:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest]
    steps: [{run: npm test}]
`, nil)

	got := render(t, plans, Options{})
	want := `CI  test.yml
  ✔ 起動する — on.push に一致します
  └─ ✔ test  matrix: os → 2 件
     ├─ ✔ test (ubuntu-latest)  ubuntu-latest · ステップ 1
     └─ ✔ test (macos-latest)  macos-latest · ステップ 1
ワークフロー 1 件中 1 件が起動 · ジョブ 実行 2 件 · スキップ 0 件
`
	if got != want {
		t.Errorf("出力が想定と違います:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestWrite_Steps はステップ表示を有効にした場合の出力を確かめる。
func TestWrite_Steps(t *testing.T) {
	plans := buildPlans(t, `
name: CI
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: 失敗時のみ
        if: failure()
        run: echo ng
`, nil)

	got := render(t, plans, Options{ShowSteps: true})
	want := `CI  test.yml
  ✔ 起動する — on.push に一致します
  └─ ✔ a  ubuntu-latest · ステップ 2
     ├─ ✔ Run actions/checkout@v4
     └─ ✘ 失敗時のみ
        if が偽: failure()
ワークフロー 1 件中 1 件が起動 · ジョブ 実行 1 件 · スキップ 0 件
`
	if got != want {
		t.Errorf("出力が想定と違います:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestWrite_NotTriggeredIsHiddenByDefault は起動しないワークフローが
// 既定では表示されず、-all 相当の指定で表示されることを確かめる。
func TestWrite_NotTriggeredIsHiddenByDefault(t *testing.T) {
	plans := buildPlans(t, `
name: Release
on:
  push:
    tags: ['v*']
jobs:
  a:
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	got := render(t, plans, Options{})
	if strings.Contains(got, "Release") {
		t.Errorf("既定では起動しないワークフローを表示しないべきです:\n%s", got)
	}
	if !strings.Contains(got, "起動するワークフローはありません") {
		t.Errorf("該当が無いことを伝えるべきです:\n%s", got)
	}

	got = render(t, plans, Options{ShowNotTriggered: true})
	if !strings.Contains(got, "Release") {
		t.Errorf("指定した場合は表示するべきです:\n%s", got)
	}
	if !strings.Contains(got, "起動しない —") {
		t.Errorf("起動しない理由を表示するべきです:\n%s", got)
	}
}

// TestWrite_Notes は注記の表示を切り替えられることを確かめる。
func TestWrite_Notes(t *testing.T) {
	plans := buildPlans(t, `
name: CI
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	got := render(t, plans, Options{ShowNotes: true})
	if !strings.Contains(got, "secrets が未指定") {
		t.Errorf("注記が表示されるべきです:\n%s", got)
	}

	got = render(t, plans, Options{ShowNotes: false})
	if strings.Contains(got, "secrets が未指定") {
		t.Errorf("注記を抑制できるべきです:\n%s", got)
	}
}

// TestWrite_Color は色付けが ANSI エスケープで行われることを確かめる。
func TestWrite_Color(t *testing.T) {
	plans := buildPlans(t, `
name: CI
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	plain := render(t, plans, Options{})
	colored := render(t, plans, Options{Color: true})

	if strings.Contains(plain, "\033[") {
		t.Errorf("色付けを無効にした場合はエスケープを含まないべきです: %q", plain)
	}
	if !strings.Contains(colored, "\033[32m✔") {
		t.Errorf("実行されるジョブは緑で表示されるべきです: %q", colored)
	}
}

// TestWrite_UnknownStatus は判定できない条件が ? として示されることを確かめる。
func TestWrite_UnknownStatus(t *testing.T) {
	plans := buildPlans(t, `
name: CI
on: push
jobs:
  a:
    if: nosuchcontext.x
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	got := render(t, plans, Options{})
	if !strings.Contains(got, "? a") {
		t.Errorf("判定不能は ? で示されるべきです:\n%s", got)
	}
	if !strings.Contains(got, "判定不能 1 件") {
		t.Errorf("集計に判定不能の件数が出るべきです:\n%s", got)
	}
}

func TestWriteJSON(t *testing.T) {
	plans := buildPlans(t, `
name: CI
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        node: [18, 20]
    steps:
      - run: npm test
      - if: false
        run: echo ng
`, nil)

	var buf bytes.Buffer
	if err := WriteJSON(&buf, plans); err != nil {
		t.Fatalf("JSON 出力に失敗しました: %v", err)
	}

	var docs []struct {
		Workflow  string `json:"workflow"`
		Triggered bool   `json:"triggered"`
		Jobs      []struct {
			ID        string   `json:"id"`
			Status    string   `json:"status"`
			Axes      []string `json:"matrixAxes"`
			Instances []struct {
				Name   string         `json:"name"`
				Status string         `json:"status"`
				Matrix map[string]any `json:"matrix"`
				Steps  []struct {
					Name   string `json:"name"`
					Status string `json:"status"`
				} `json:"steps"`
			} `json:"instances"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(buf.Bytes(), &docs); err != nil {
		t.Fatalf("出力した JSON を読み戻せません: %v\n%s", err, buf.String())
	}

	if len(docs) != 1 || docs[0].Workflow != "CI" || !docs[0].Triggered {
		t.Fatalf("ワークフローの情報が想定と違います: %+v", docs)
	}
	job := docs[0].Jobs[0]
	if job.ID != "test" || job.Status != "run" {
		t.Errorf("ジョブの情報が想定と違います: %+v", job)
	}
	if len(job.Axes) != 1 || job.Axes[0] != "node" {
		t.Errorf("matrix の軸が想定と違います: %v", job.Axes)
	}
	if len(job.Instances) != 2 {
		t.Fatalf("組み合わせは2件になるべきです: %d", len(job.Instances))
	}
	// matrix の値は型を保ったまま出力される。
	if got := job.Instances[0].Matrix["node"]; got != float64(18) {
		t.Errorf("matrix の値が想定と違います: %#v", got)
	}
	if got := job.Instances[0].Steps[1].Status; got != "skip" {
		t.Errorf("ステップの状態が想定と違います: %q", got)
	}
}

// TestWriteJSON_NotTriggered は起動しない場合も理由が JSON に含まれることを確かめる。
func TestWriteJSON_NotTriggered(t *testing.T) {
	plans := buildPlans(t, `
name: Release
on:
  push:
    tags: ['v*']
jobs:
  a:
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	var buf bytes.Buffer
	if err := WriteJSON(&buf, plans); err != nil {
		t.Fatalf("JSON 出力に失敗しました: %v", err)
	}
	if !strings.Contains(buf.String(), `"triggered": false`) {
		t.Errorf("起動しないことが出力されるべきです:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "tags フィルタのみ") {
		t.Errorf("理由が出力されるべきです:\n%s", buf.String())
	}
}
