package plan

import (
	"strings"
	"testing"

	"github.com/a2-ito/gh-sim/internal/ghctx"
	"github.com/a2-ito/gh-sim/internal/trigger"
	"github.com/a2-ito/gh-sim/internal/workflow"
)

// pushConfig は main への push を表す既定の入力条件を返す。
func pushConfig() *ghctx.Config {
	return &ghctx.Config{
		Event: &trigger.Event{Name: "push", Ref: "refs/heads/main"},
	}
}

func buildPlan(t *testing.T, src string, cfg *ghctx.Config) *Plan {
	t.Helper()
	w, err := workflow.Parse([]byte(src), "test.yml")
	if err != nil {
		t.Fatalf("ワークフローの解析に失敗しました: %v", err)
	}
	if cfg == nil {
		cfg = pushConfig()
	}
	p, err := Build(w, cfg)
	if err != nil {
		t.Fatalf("計画の作成に失敗しました: %v", err)
	}
	return p
}

// jobPlan は指定 ID のジョブの判定結果を返す。
func jobPlan(t *testing.T, p *Plan, id string) *JobPlan {
	t.Helper()
	for _, jp := range p.Jobs {
		if jp.Job.ID == id {
			return jp
		}
	}
	t.Fatalf("ジョブ %q が見つかりません", id)
	return nil
}

// assertStatus はジョブの状態を検証する。
func assertStatus(t *testing.T, p *Plan, id string, want Status) {
	t.Helper()
	jp := jobPlan(t, p, id)
	if got := jp.Status(); got != want {
		reason := ""
		if len(jp.Instances) > 0 {
			reason = jp.Instances[0].Reason
		}
		t.Errorf("ジョブ %q の状態: got %v, want %v（理由: %s）", id, got, want, reason)
	}
}

func TestBuild_NotTriggered(t *testing.T) {
	p := buildPlan(t, `
on:
  push:
    branches: [main]
jobs:
  a:
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, &ghctx.Config{Event: &trigger.Event{Name: "push", Ref: "refs/heads/develop"}})

	if p.Triggered() {
		t.Error("develop への push では起動しないべきです")
	}
	// 起動しない場合はジョブの判定を行わない。
	if len(p.Jobs) != 0 {
		t.Errorf("起動しない場合はジョブを判定しないべきです: %d 件", len(p.Jobs))
	}
}

func TestBuild_JobIfFalse(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  always-run:
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  never-run:
    if: github.ref == 'refs/heads/nosuch'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	assertStatus(t, p, "always-run", StatusRun)
	assertStatus(t, p, "never-run", StatusSkip)

	jp := jobPlan(t, p, "never-run")
	if !strings.Contains(jp.Instances[0].Reason, "if が偽") {
		t.Errorf("理由が想定と違います: %s", jp.Instances[0].Reason)
	}
}

// TestBuild_SkippedDependencyCascades は依存ジョブが実行されない場合、
// if を書いていない後続ジョブも実行されないことを確かめる。
// GitHub では if を省略すると暗黙の success() が適用されるため、
// 先行ジョブが skip されると後続も skip される。
func TestBuild_SkippedDependencyCascades(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  first:
    if: false
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  second:
    needs: first
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  third:
    needs: second
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	assertStatus(t, p, "first", StatusSkip)
	assertStatus(t, p, "second", StatusSkip)
	// 連鎖は2段以上先まで伝わる。
	assertStatus(t, p, "third", StatusSkip)

	jp := jobPlan(t, p, "second")
	if !strings.Contains(jp.Instances[0].Reason, "依存ジョブ first が実行されないため") {
		t.Errorf("理由が想定と違います: %s", jp.Instances[0].Reason)
	}
}

// TestBuild_AlwaysRunsAfterSkippedDependency は依存が skip されても
// if: always() を書いたジョブは実行されることを確かめる。
func TestBuild_AlwaysRunsAfterSkippedDependency(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  first:
    if: false
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  notify:
    needs: first
    if: always()
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  on-failure:
    needs: first
    if: failure()
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  on-success:
    needs: first
    if: success()
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	assertStatus(t, p, "first", StatusSkip)
	// always() は依存の状態に関係なく真になる。
	assertStatus(t, p, "notify", StatusRun)
	// 依存は失敗ではなく skip なので failure() は偽。
	assertStatus(t, p, "on-failure", StatusSkip)
	assertStatus(t, p, "on-success", StatusSkip)
}

// TestBuild_NeedsResultIsReferenceable は needs.<job>.result を
// 条件式から参照できることを確かめる。
func TestBuild_NeedsResultIsReferenceable(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  first:
    if: false
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  check-skipped:
    needs: first
    if: always() && needs.first.result == 'skipped'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  check-success:
    needs: first
    if: always() && needs.first.result == 'success'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	assertStatus(t, p, "check-skipped", StatusRun)
	assertStatus(t, p, "check-success", StatusSkip)
}

// TestBuild_MatrixStepConditions は matrix の組み合わせごとに
// ステップの if 判定が変わることを確かめる。
func TestBuild_MatrixStepConditions(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        node: [18, 20]
        include:
          - node: 20
            coverage: true
    steps:
      - run: npm test
      - name: Upload coverage
        if: ${{ matrix.coverage }}
        run: npm run coverage
`, nil)

	jp := jobPlan(t, p, "test")
	if len(jp.Instances) != 2 {
		t.Fatalf("組み合わせは2件になるべきです: got %d", len(jp.Instances))
	}

	// node 18 には coverage が無いので Upload coverage は実行されない。
	if got := jp.Instances[0].Steps[1].Status; got != StatusSkip {
		t.Errorf("node 18 の coverage ステップ: got %v, want skip", got)
	}
	// node 20 には include で coverage が付くので実行される。
	if got := jp.Instances[1].Steps[1].Status; got != StatusRun {
		t.Errorf("node 20 の coverage ステップ: got %v, want run", got)
	}
	// 条件が無いステップは常に実行される。
	for i, inst := range jp.Instances {
		if got := inst.Steps[0].Status; got != StatusRun {
			t.Errorf("組み合わせ[%d] の最初のステップ: got %v, want run", i, got)
		}
	}
}

// TestBuild_RunsOnFromMatrix は runs-on に matrix の値を使った場合に
// 解決されることを確かめる。
func TestBuild_RunsOnFromMatrix(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  test:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest]
    steps: [{run: echo hi}]
`, nil)

	jp := jobPlan(t, p, "test")
	want := []string{"ubuntu-latest", "macos-latest"}
	for i, w := range want {
		if got := jp.Instances[i].RunsOn; got != w {
			t.Errorf("組み合わせ[%d] の runs-on: got %q, want %q", i, got, w)
		}
	}
}

// TestBuild_JobNameIsEvaluated はジョブ名に含まれる式が
// matrix を反映して解決されることを確かめる。
func TestBuild_JobNameIsEvaluated(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  test:
    name: Test on node ${{ matrix.node }}
    runs-on: ubuntu-latest
    strategy:
      matrix:
        node: [18, 20]
    steps: [{run: echo hi}]
`, nil)

	jp := jobPlan(t, p, "test")
	want := []string{"Test on node 18", "Test on node 20"}
	for i, w := range want {
		if got := jp.Instances[i].Name; got != w {
			t.Errorf("組み合わせ[%d] の表示名: got %q, want %q", i, got, w)
		}
	}
}

// TestBuild_DefaultJobNameIncludesMatrix は name: を書かない場合に
// GitHub と同じく matrix の値が括弧で添えられることを確かめる。
func TestBuild_DefaultJobNameIncludesMatrix(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        node: [18]
        os: [ubuntu-latest]
    steps: [{run: echo hi}]
`, nil)

	jp := jobPlan(t, p, "test")
	if got := jp.Instances[0].Name; got != "test (18, ubuntu-latest)" {
		t.Errorf("既定の表示名: got %q", got)
	}
}

// TestBuild_EnvIsEvaluated は env の式が評価され、
// 条件式から参照できることを確かめる。
func TestBuild_EnvIsEvaluated(t *testing.T) {
	p := buildPlan(t, `
on: push
env:
  TARGET: ${{ github.ref_name }}
jobs:
  deploy:
    if: env.TARGET == 'main'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  wrong:
    if: env.TARGET == 'develop'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	assertStatus(t, p, "deploy", StatusRun)
	assertStatus(t, p, "wrong", StatusSkip)
}

// TestBuild_JobEnvOverridesWorkflowEnv はジョブの env が
// ワークフローの env を上書きすることを確かめる。
func TestBuild_JobEnvOverridesWorkflowEnv(t *testing.T) {
	p := buildPlan(t, `
on: push
env:
  STAGE: dev
jobs:
  a:
    env:
      STAGE: prod
    if: env.STAGE == 'prod'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  b:
    if: env.STAGE == 'dev'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	assertStatus(t, p, "a", StatusRun)
	assertStatus(t, p, "b", StatusRun)
}

// TestBuild_UnknownCondition は評価できない条件を
// 判定不能として扱い、黙って実行扱いにしないことを確かめる。
func TestBuild_UnknownCondition(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  a:
    if: nosuchcontext.value == 'x'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	assertStatus(t, p, "a", StatusUnknown)
	jp := jobPlan(t, p, "a")
	if !strings.Contains(jp.Instances[0].Reason, "if を評価できません") {
		t.Errorf("理由が想定と違います: %s", jp.Instances[0].Reason)
	}
}

// TestBuild_SkippedJobStepsAreMarkedSkipped は実行されないジョブの
// ステップもまとめて skip として並ぶことを確かめる。
func TestBuild_SkippedJobStepsAreMarkedSkipped(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  a:
    if: false
    runs-on: ubuntu-latest
    steps:
      - run: echo 1
      - run: echo 2
`, nil)

	jp := jobPlan(t, p, "a")
	if len(jp.Instances[0].Steps) != 2 {
		t.Fatalf("ステップは2件並ぶべきです: got %d", len(jp.Instances[0].Steps))
	}
	for i, sp := range jp.Instances[0].Steps {
		if sp.Status != StatusSkip {
			t.Errorf("ステップ[%d]: got %v, want skip", i, sp.Status)
		}
	}
}

// TestBuild_StepsContextIsAvailable は先行ステップの id を
// steps コンテキストから参照できることを確かめる。
func TestBuild_StepsContextIsAvailable(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - id: meta
        run: echo hi
      - if: steps.meta.outcome == 'success'
        run: echo ok
      - if: steps.nosuch.outcome == 'success'
        run: echo ng
`, nil)

	steps := jobPlan(t, p, "a").Instances[0].Steps
	if steps[1].Status != StatusRun {
		t.Errorf("先行ステップの outcome を参照できるべきです: %v (%s)", steps[1].Status, steps[1].Reason)
	}
	// 存在しない id の参照は null になり、条件は偽になる。
	if steps[2].Status != StatusSkip {
		t.Errorf("存在しない id の参照は偽になるべきです: %v", steps[2].Status)
	}
}

// TestBuild_Layers は依存グラフのレイヤが JobPlan に入ることを確かめる。
func TestBuild_Layers(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  b:
    needs: a
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	if got := jobPlan(t, p, "a").Layer; got != 0 {
		t.Errorf("a のレイヤ: got %d, want 0", got)
	}
	if got := jobPlan(t, p, "b").Layer; got != 1 {
		t.Errorf("b のレイヤ: got %d, want 1", got)
	}
}

// TestBuild_JobOrderFollowsDefinition は出力順が定義順であることを確かめる。
// 評価はトポロジカル順に行うが、表示は書いた順のほうが読みやすい。
func TestBuild_JobOrderFollowsDefinition(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  last:
    needs: first
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  first:
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	var ids []string
	for _, jp := range p.Jobs {
		ids = append(ids, jp.Job.ID)
	}
	if len(ids) != 2 || ids[0] != "last" || ids[1] != "first" {
		t.Errorf("出力順は定義順であるべきです: got %v", ids)
	}
}

// TestBuild_InputsFromDispatch は workflow_dispatch の入力が
// 既定値と指定値の両方で反映されることを確かめる。
func TestBuild_InputsFromDispatch(t *testing.T) {
	const src = `
on:
  workflow_dispatch:
    inputs:
      dry-run:
        type: boolean
        default: true
      target:
        type: string
        default: dev
jobs:
  deploy:
    if: ${{ !inputs.dry-run }}
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  show:
    if: inputs.target == 'prod'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`
	// 既定値のままなら dry-run は true なので deploy は実行されない。
	p := buildPlan(t, src, &ghctx.Config{Event: &trigger.Event{Name: "workflow_dispatch"}})
	assertStatus(t, p, "deploy", StatusSkip)
	assertStatus(t, p, "show", StatusSkip)

	// 入力を与えると判定が変わる。
	p = buildPlan(t, src, &ghctx.Config{
		Event:  &trigger.Event{Name: "workflow_dispatch"},
		Inputs: map[string]string{"dry-run": "false", "target": "prod"},
	})
	assertStatus(t, p, "deploy", StatusRun)
	assertStatus(t, p, "show", StatusRun)
}

// TestBuild_CycleIsReported は needs の循環が計画作成時に
// エラーとして報告されることを確かめる。
func TestBuild_CycleIsReported(t *testing.T) {
	w, err := workflow.Parse([]byte(`
on: push
jobs:
  a:
    needs: b
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  b:
    needs: a
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`), "test.yml")
	if err != nil {
		t.Fatalf("解析に失敗しました: %v", err)
	}
	if _, err := Build(w, pushConfig()); err == nil {
		t.Fatal("循環はエラーになるべきです")
	}
}
