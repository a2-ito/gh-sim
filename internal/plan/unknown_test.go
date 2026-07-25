package plan

import (
	"strings"
	"testing"

	"github.com/a2-ito/gh-sim/internal/ghctx"
	"github.com/a2-ito/gh-sim/internal/trigger"
)

// outputsWorkflow は前段のジョブの出力で後段の実行が決まる構成。
// 実際のリポジトリでよく使われる形。
const outputsWorkflow = `
on: push
jobs:
  changes:
    runs-on: ubuntu-latest
    outputs:
      environment: ${{ steps.detect.outputs.environment }}
      backend: ${{ steps.detect.outputs.backend }}
    steps:
      - id: detect
        run: echo "environment=develop" >> "$GITHUB_OUTPUT"
  deploy-dev:
    needs: changes
    if: needs.changes.outputs.environment == 'develop'
    runs-on: ubuntu-latest
    steps: [{run: ./deploy.sh}]
  skip-when-set:
    needs: changes
    if: needs.changes.outputs.environment != 'develop'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  independent:
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`

// TestBuild_OutputsAreUnknownNotFalse は出力に依存する条件が
// 「偽」ではなく「判定不能」として扱われることを確かめる。
//
// 出力は実行しないと確定しないため、偽と断定すると
// 「このジョブは動かない」という誤った結論を示してしまう。
func TestBuild_OutputsAreUnknownNotFalse(t *testing.T) {
	p := buildPlan(t, outputsWorkflow, nil)

	// 出力を参照する条件は真偽どちらの見込みでも判定不能になる。
	assertStatus(t, p, "deploy-dev", StatusUnknown)
	assertStatus(t, p, "skip-when-set", StatusUnknown)

	// 出力に依存しないジョブは従来どおり確定する。
	assertStatus(t, p, "changes", StatusRun)
	assertStatus(t, p, "independent", StatusRun)

	// 理由にはどの参照が不明かと、仮に評価した場合の結果が示される。
	reason := jobPlan(t, p, "deploy-dev").Instances[0].Reason
	if !strings.Contains(reason, "needs.changes.outputs.environment の値が不明です") {
		t.Errorf("不明な参照が示されるべきです: %s", reason)
	}
	if !strings.Contains(reason, "スキップ") {
		t.Errorf("仮に評価した場合の結果が示されるべきです: %s", reason)
	}

	// != 比較は不明値のままだと真になるが、実際の値次第で偽になりうる。
	reason = jobPlan(t, p, "skip-when-set").Instances[0].Reason
	if !strings.Contains(reason, "実行") {
		t.Errorf("真の見込みであることが示されるべきです: %s", reason)
	}
}

// TestBuild_OutputsGivenExplicitly は出力を与えると
// 確定した判定になることを確かめる。
func TestBuild_OutputsGivenExplicitly(t *testing.T) {
	cfg := &ghctx.Config{
		Event:   &trigger.Event{Name: "push", Ref: "refs/heads/main"},
		Outputs: map[string]string{"changes.environment": "develop"},
	}
	p := buildPlan(t, outputsWorkflow, cfg)

	assertStatus(t, p, "deploy-dev", StatusRun)
	assertStatus(t, p, "skip-when-set", StatusSkip)

	// 別の値を与えれば判定が反転する。
	cfg = &ghctx.Config{
		Event:   &trigger.Event{Name: "push", Ref: "refs/heads/main"},
		Outputs: map[string]string{"changes.environment": "production"},
	}
	p = buildPlan(t, outputsWorkflow, cfg)

	assertStatus(t, p, "deploy-dev", StatusSkip)
	assertStatus(t, p, "skip-when-set", StatusRun)
}

// TestBuild_UndeclaredOutputIsAlsoUnknown は outputs: の宣言が無い出力を
// 参照した場合も不明として扱うことを確かめる。
// 宣言漏れと「値が空」を静的に区別できないため、安全側に寄せる。
func TestBuild_UndeclaredOutputIsAlsoUnknown(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  first:
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  second:
    needs: first
    if: needs.first.outputs.nosuch == 'x'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	assertStatus(t, p, "second", StatusUnknown)
	if reason := jobPlan(t, p, "second").Instances[0].Reason; !strings.Contains(reason, "needs.first.outputs.nosuch") {
		t.Errorf("参照名が示されるべきです: %s", reason)
	}
}

// TestBuild_NeedsResultStaysDecidable は needs.<job>.result が
// 確定値として扱われ、判定不能にならないことを確かめる。
// result はスキップ判定から導けるため、出力とは区別する必要がある。
func TestBuild_NeedsResultStaysDecidable(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  first:
    if: false
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  check:
    needs: first
    if: always() && needs.first.result == 'skipped'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	assertStatus(t, p, "check", StatusRun)
}

// TestBuild_NeedsWildcardResultStaysDecidable は実務でよく使われる
// needs.*.result のイディオムが確定判定できることを確かめる。
func TestBuild_NeedsWildcardResultStaysDecidable(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  b:
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  gate:
    needs: [a, b]
    if: always() && !contains(needs.*.result, 'failure') && !contains(needs.*.result, 'cancelled')
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	assertStatus(t, p, "gate", StatusRun)
}

// TestBuild_UnknownDependencyCascades は判定が確定しないジョブに
// 依存する後続も確定しないことを確かめる。
func TestBuild_UnknownDependencyCascades(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  changes:
    runs-on: ubuntu-latest
    outputs:
      flag: ${{ steps.x.outputs.flag }}
    steps: [{id: x, run: echo hi}]
  middle:
    needs: changes
    if: needs.changes.outputs.flag == 'true'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  last:
    needs: middle
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	assertStatus(t, p, "middle", StatusUnknown)
	// middle が確定しないので last も確定しない。
	assertStatus(t, p, "last", StatusUnknown)
	if reason := jobPlan(t, p, "last").Instances[0].Reason; !strings.Contains(reason, "依存ジョブ middle の判定が確定しない") {
		t.Errorf("連鎖の理由が示されるべきです: %s", reason)
	}
}

// TestBuild_SkipTakesPrecedenceOverUnknownDependency は
// 依存が確定しない場合でも、自身の条件が確定的に偽なら
// スキップとして扱うことを確かめる。
func TestBuild_SkipTakesPrecedenceOverUnknownDependency(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  changes:
    runs-on: ubuntu-latest
    outputs:
      flag: ${{ steps.x.outputs.flag }}
    steps: [{id: x, run: echo hi}]
  middle:
    needs: changes
    if: needs.changes.outputs.flag == 'true'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
  never:
    needs: middle
    if: github.event_name == 'schedule'
    runs-on: ubuntu-latest
    steps: [{run: echo hi}]
`, nil)

	// 依存は確定しないが、push イベントなので条件は確実に偽。
	assertStatus(t, p, "never", StatusSkip)
}

// TestBuild_StepOutputsAreUnknown は同一ジョブ内のステップ出力を
// 参照した条件も判定不能になることを確かめる。
func TestBuild_StepOutputsAreUnknown(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - id: detect
        run: echo "changed=true" >> "$GITHUB_OUTPUT"
      - if: steps.detect.outputs.changed == 'true'
        run: echo build
      - if: steps.detect.outcome == 'success'
        run: echo always
`, nil)

	steps := jobPlan(t, p, "a").Instances[0].Steps

	// 出力に依存するステップは判定不能。
	if steps[1].Status != StatusUnknown {
		t.Errorf("出力に依存するステップは判定不能になるべきです: %v (%s)", steps[1].Status, steps[1].Reason)
	}
	if !strings.Contains(steps[1].Reason, "steps.detect.outputs.changed") {
		t.Errorf("参照名が示されるべきです: %s", steps[1].Reason)
	}

	// outcome は確定値なので判定できる。
	if steps[2].Status != StatusRun {
		t.Errorf("outcome は確定値として扱われるべきです: %v (%s)", steps[2].Status, steps[2].Reason)
	}
}

// TestBuild_JobStatusReflectsUnknown はジョブ全体の状態に
// 判定不能が反映されることを確かめる。
func TestBuild_JobStatusReflectsUnknown(t *testing.T) {
	p := buildPlan(t, `
on: push
jobs:
  changes:
    runs-on: ubuntu-latest
    outputs:
      flag: ${{ steps.x.outputs.flag }}
    steps: [{id: x, run: echo hi}]
  test:
    needs: changes
    if: needs.changes.outputs.flag == 'true'
    runs-on: ubuntu-latest
    strategy:
      matrix:
        node: [18, 20]
    steps: [{run: echo hi}]
`, nil)

	jp := jobPlan(t, p, "test")
	if jp.Status() != StatusUnknown {
		t.Errorf("ジョブ全体も判定不能になるべきです: %v", jp.Status())
	}
	// matrix の各組み合わせが判定不能になる。
	for i, inst := range jp.Instances {
		if inst.Status != StatusUnknown {
			t.Errorf("組み合わせ[%d]: got %v, want unknown", i, inst.Status)
		}
	}
}
