package plan

import (
	"strings"
	"testing"

	"github.com/a2-ito/gh-sim/internal/expr"
	"github.com/a2-ito/gh-sim/internal/workflow"
)

// parseMatrix はジョブ1つ分のワークフローを解析して matrix を取り出す。
func parseMatrix(t *testing.T, strategy string) *workflow.Matrix {
	t.Helper()
	src := `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
` + strategy + `
    steps:
      - run: echo hi
`
	w, err := workflow.Parse([]byte(src), "test.yml")
	if err != nil {
		t.Fatalf("ワークフローの解析に失敗しました: %v", err)
	}
	st := w.Jobs.Items["a"].Strategy
	if st == nil {
		return nil
	}
	return st.Matrix
}

// comboStrings は展開結果を "key=value" の並びで表現する。
func comboStrings(exp *Expansion) []string {
	out := make([]string, 0, len(exp.Combinations))
	for _, c := range exp.Combinations {
		var parts []string
		for _, k := range c.Values.Keys() {
			v, _ := c.Values.Get(k)
			parts = append(parts, k+"="+v.AsString())
		}
		out = append(out, strings.Join(parts, " "))
	}
	return out
}

func mustExpand(t *testing.T, m *workflow.Matrix, ctx *expr.Context) *Expansion {
	t.Helper()
	exp, err := Expand(m, ctx)
	if err != nil {
		t.Fatalf("展開に失敗しました: %v", err)
	}
	return exp
}

func assertCombos(t *testing.T, exp *Expansion, want []string) {
	t.Helper()
	got := comboStrings(exp)
	if len(got) != len(want) {
		t.Fatalf("組み合わせの数が違います: got %d, want %d\ngot:  %v\nwant: %v",
			len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("組み合わせ[%d]: got %q, want %q\n全体:\ngot:  %v\nwant: %v",
				i, got[i], want[i], got, want)
		}
	}
}

// TestExpand_NoMatrix は matrix が無いジョブでも
// 組み合わせ1件として扱えることを確かめる。
func TestExpand_NoMatrix(t *testing.T) {
	exp := mustExpand(t, nil, nil)
	if len(exp.Combinations) != 1 {
		t.Fatalf("組み合わせは1件になるべきです: got %d", len(exp.Combinations))
	}
	if exp.Combinations[0].Values.Len() != 0 {
		t.Errorf("値を持たない組み合わせになるべきです: %v", comboStrings(exp))
	}
}

// TestExpand_CartesianOrder は直積の順序を確かめる。
// 最初に定義した軸が外側、最後の軸が最も速く変化する。
func TestExpand_CartesianOrder(t *testing.T) {
	m := parseMatrix(t, `
    strategy:
      matrix:
        os: [ubuntu-22.04, ubuntu-20.04]
        version: [10, 12, 14]`)
	exp := mustExpand(t, m, nil)
	assertCombos(t, exp, []string{
		"os=ubuntu-22.04 version=10",
		"os=ubuntu-22.04 version=12",
		"os=ubuntu-22.04 version=14",
		"os=ubuntu-20.04 version=10",
		"os=ubuntu-20.04 version=12",
		"os=ubuntu-20.04 version=14",
	})
	if len(exp.Axes) != 2 || exp.Axes[0] != "os" || exp.Axes[1] != "version" {
		t.Errorf("軸の順序が想定と違います: %v", exp.Axes)
	}
}

func TestExpand_Exclude(t *testing.T) {
	m := parseMatrix(t, `
    strategy:
      matrix:
        node: [18, 20, 22]
        os: [ubuntu-latest, macos-latest]
        exclude:
          - node: 18
            os: macos-latest`)
	exp := mustExpand(t, m, nil)
	assertCombos(t, exp, []string{
		"node=18 os=ubuntu-latest",
		"node=20 os=ubuntu-latest",
		"node=20 os=macos-latest",
		"node=22 os=ubuntu-latest",
		"node=22 os=macos-latest",
	})
}

// TestExpand_ExcludePartialKeys は exclude に一部の軸だけ書いた場合に
// その値を持つ組み合わせがまとめて除外されることを確かめる。
func TestExpand_ExcludePartialKeys(t *testing.T) {
	m := parseMatrix(t, `
    strategy:
      matrix:
        node: [18, 20]
        os: [ubuntu-latest, macos-latest]
        exclude:
          - os: macos-latest`)
	exp := mustExpand(t, m, nil)
	assertCombos(t, exp, []string{
		"node=18 os=ubuntu-latest",
		"node=20 os=ubuntu-latest",
	})
}

// TestExpand_IncludeOfficialExample は GitHub 公式ドキュメントの include の例を
// そのまま検証する。include は仕様が細かく、
//   - 元の軸のキーは条件として使われ、値は上書きされない
//   - include で追加された値は後続の include で上書きされうる
//   - どの組み合わせにも当てはまらない場合は新しい組み合わせになる
//
// という3点を同時に満たす必要がある。
func TestExpand_IncludeOfficialExample(t *testing.T) {
	m := parseMatrix(t, `
    strategy:
      matrix:
        fruit: [apple, pear]
        animal: [cat, dog]
        include:
          - color: green
          - color: pink
            animal: cat
          - fruit: apple
            shape: circle
          - fruit: banana
          - fruit: banana
            animal: cat`)
	exp := mustExpand(t, m, nil)
	assertCombos(t, exp, []string{
		"fruit=apple animal=cat color=pink shape=circle",
		"fruit=apple animal=dog color=green shape=circle",
		"fruit=pear animal=cat color=pink",
		"fruit=pear animal=dog color=green",
		"fruit=banana",
		"fruit=banana animal=cat",
	})
}

// TestExpand_IncludeAfterExclude は exclude の後に include が処理され、
// 除外された組み合わせを include で復活させられることを確かめる。
func TestExpand_IncludeAfterExclude(t *testing.T) {
	m := parseMatrix(t, `
    strategy:
      matrix:
        node: [18, 20]
        os: [ubuntu-latest, macos-latest]
        exclude:
          - node: 18
            os: macos-latest
        include:
          - node: 18
            os: macos-latest
            experimental: true`)
	exp := mustExpand(t, m, nil)
	assertCombos(t, exp, []string{
		"node=18 os=ubuntu-latest",
		"node=20 os=ubuntu-latest",
		"node=20 os=macos-latest",
		"node=18 os=macos-latest experimental=true",
	})
}

// TestExpand_IncludeOnly は軸を持たず include だけで
// 組み合わせを列挙する書き方を確かめる。
func TestExpand_IncludeOnly(t *testing.T) {
	m := parseMatrix(t, `
    strategy:
      matrix:
        include:
          - node: 18
            os: ubuntu-latest
          - node: 22
            os: macos-latest`)
	exp := mustExpand(t, m, nil)
	assertCombos(t, exp, []string{
		"node=18 os=ubuntu-latest",
		"node=22 os=macos-latest",
	})
}

// TestExpand_AxisFromExpression は軸の値を式で与えた場合に
// 評価して展開できることを確かめる。
func TestExpand_AxisFromExpression(t *testing.T) {
	m := parseMatrix(t, `
    strategy:
      matrix:
        node: ${{ fromJSON(inputs.versions) }}
        os: [ubuntu-latest]`)

	ctx := expr.NewContext()
	ctx.Set("inputs", expr.FromGo(map[string]any{"versions": "[18, 20]"}))

	exp := mustExpand(t, m, ctx)
	assertCombos(t, exp, []string{
		"node=18 os=ubuntu-latest",
		"node=20 os=ubuntu-latest",
	})
}

// TestExpand_WholeMatrixFromExpression は matrix 全体を式で与えた場合に
// 評価して展開できることを確かめる。
func TestExpand_WholeMatrixFromExpression(t *testing.T) {
	m := parseMatrix(t, `
    strategy:
      matrix: ${{ fromJSON(needs.setup.outputs.matrix) }}`)

	ctx := expr.NewContext()
	ctx.Set("needs", expr.FromGo(map[string]any{
		"setup": map[string]any{
			"outputs": map[string]any{
				"matrix": `{"node":[18,20],"os":["ubuntu-latest"]}`,
			},
		},
	}))

	exp := mustExpand(t, m, ctx)
	assertCombos(t, exp, []string{
		"node=18 os=ubuntu-latest",
		"node=20 os=ubuntu-latest",
	})
}

// TestExpand_UnresolvableExpressionIsNoted は式を解決できない場合に
// 展開を諦めて注記を残すことを確かめる。判定できないことを黙って隠さない。
func TestExpand_UnresolvableExpressionIsNoted(t *testing.T) {
	m := parseMatrix(t, `
    strategy:
      matrix: ${{ fromJSON(needs.setup.outputs.matrix) }}`)

	// needs コンテキストを与えないので評価に失敗する。
	exp := mustExpand(t, m, expr.NewContext())
	if len(exp.Combinations) != 1 || exp.Combinations[0].Values.Len() != 0 {
		t.Errorf("展開できない場合は組み合わせなしとして扱うべきです: %v", comboStrings(exp))
	}
	if len(exp.Notes) == 0 {
		t.Fatal("注記が残るべきです")
	}
	if !strings.Contains(exp.Notes[0], "評価できませんでした") {
		t.Errorf("注記の内容が想定と違います: %v", exp.Notes)
	}
}

func TestExpand_AxisExpressionNotArrayIsNoted(t *testing.T) {
	m := parseMatrix(t, `
    strategy:
      matrix:
        node: ${{ inputs.version }}`)

	ctx := expr.NewContext()
	ctx.Set("inputs", expr.FromGo(map[string]any{"version": 18}))

	exp := mustExpand(t, m, ctx)
	if len(exp.Notes) == 0 || !strings.Contains(exp.Notes[0], "number なので展開できません") {
		t.Errorf("配列でない場合は注記が必要です: %v", exp.Notes)
	}
}

// TestCombination_String は表示用の文字列が
// Actions のジョブ名表記に合わせて値のみを並べることを確かめる。
func TestCombination_String(t *testing.T) {
	m := parseMatrix(t, `
    strategy:
      matrix:
        node: [18]
        os: [ubuntu-latest]`)
	exp := mustExpand(t, m, nil)
	if got := exp.Combinations[0].String(); got != "18, ubuntu-latest" {
		t.Errorf("String() = %q, want %q", got, "18, ubuntu-latest")
	}
}

// TestExpand_RealWorkflow は testdata の CI ワークフローで
// exclude と include が併用された場合の展開を確かめる。
func TestExpand_RealWorkflow(t *testing.T) {
	ws, err := workflow.LoadDir("../../testdata/workflows")
	if err != nil {
		t.Fatalf("読み込みに失敗しました: %v", err)
	}
	var ci *workflow.Workflow
	for _, w := range ws {
		if w.Name == "CI" {
			ci = w
		}
	}
	if ci == nil {
		t.Fatal("CI ワークフローが見つかりません")
	}

	exp := mustExpand(t, ci.Jobs.Items["test"].Strategy.Matrix, nil)
	// node 3種 × os 2種から macos の node 18 を除外した5件。
	// さらに node 22 / ubuntu の組み合わせに coverage が付く。
	assertCombos(t, exp, []string{
		"node=18 os=ubuntu-latest",
		"node=20 os=ubuntu-latest",
		"node=20 os=macos-latest",
		"node=22 os=ubuntu-latest coverage=true",
		"node=22 os=macos-latest",
	})
}
