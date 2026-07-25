package plan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/a2-ito/gh-sim/internal/workflow"
)

// jobsWorkflow は jobs 定義だけを与えてワークフローを組み立てる。
func jobsWorkflow(t *testing.T, jobs string) *workflow.Workflow {
	t.Helper()
	w, err := workflow.Parse([]byte("on: push\njobs:\n"+jobs), "test.yml")
	if err != nil {
		t.Fatalf("ワークフローの解析に失敗しました: %v", err)
	}
	return w
}

// job は最小のジョブ定義を1つ分の YAML にする。
func job(id string, needs string) string {
	s := "  " + id + ":\n"
	if needs != "" {
		s += "    needs: " + needs + "\n"
	}
	s += "    runs-on: ubuntu-latest\n    steps: [{run: echo hi}]\n"
	return s
}

func mustGraph(t *testing.T, w *workflow.Workflow) *Graph {
	t.Helper()
	g, err := BuildGraph(w)
	if err != nil {
		t.Fatalf("グラフの構築に失敗しました: %v", err)
	}
	return g
}

func TestBuildGraph_Linear(t *testing.T) {
	w := jobsWorkflow(t, job("a", "")+job("b", "a")+job("c", "b"))
	g := mustGraph(t, w)

	want := [][]string{{"a"}, {"b"}, {"c"}}
	if !reflect.DeepEqual(g.Layers, want) {
		t.Errorf("レイヤ: got %v, want %v", g.Layers, want)
	}
	if !reflect.DeepEqual(g.Order, []string{"a", "b", "c"}) {
		t.Errorf("実行順: got %v", g.Order)
	}
	for id, wantLayer := range map[string]int{"a": 0, "b": 1, "c": 2} {
		if got := g.LayerOf(id); got != wantLayer {
			t.Errorf("LayerOf(%q) = %d, want %d", id, got, wantLayer)
		}
	}
}

// TestBuildGraph_Parallel は依存の無いジョブが同じレイヤに入り、
// レイヤ内では定義順に並ぶことを確かめる。
func TestBuildGraph_Parallel(t *testing.T) {
	w := jobsWorkflow(t, job("zebra", "")+job("alpha", "")+job("middle", ""))
	g := mustGraph(t, w)

	want := [][]string{{"zebra", "alpha", "middle"}}
	if !reflect.DeepEqual(g.Layers, want) {
		t.Errorf("レイヤ: got %v, want %v", g.Layers, want)
	}
}

// TestBuildGraph_Diamond は分岐して合流する依存関係を確かめる。
func TestBuildGraph_Diamond(t *testing.T) {
	w := jobsWorkflow(t,
		job("setup", "")+
			job("build", "setup")+
			job("test", "setup")+
			job("deploy", "[build, test]"))
	g := mustGraph(t, w)

	want := [][]string{{"setup"}, {"build", "test"}, {"deploy"}}
	if !reflect.DeepEqual(g.Layers, want) {
		t.Errorf("レイヤ: got %v, want %v", g.Layers, want)
	}
	if got := g.Dependents["setup"]; !reflect.DeepEqual(got, []string{"build", "test"}) {
		t.Errorf("setup の後続: got %v", got)
	}
	if got := g.Needs["deploy"]; !reflect.DeepEqual(got, []string{"build", "test"}) {
		t.Errorf("deploy の依存先: got %v", got)
	}
}

// TestBuildGraph_LayerFollowsDeepestDependency は依存の深さが異なる場合、
// 最も深い依存に合わせてレイヤが決まることを確かめる。
func TestBuildGraph_LayerFollowsDeepestDependency(t *testing.T) {
	w := jobsWorkflow(t,
		job("a", "")+
			job("b", "a")+
			job("c", "[a, b]"))
	g := mustGraph(t, w)

	// c は a だけなら1層目に置けるが、b に依存するため2層目になる。
	want := [][]string{{"a"}, {"b"}, {"c"}}
	if !reflect.DeepEqual(g.Layers, want) {
		t.Errorf("レイヤ: got %v, want %v", g.Layers, want)
	}
}

// TestBuildGraph_DuplicateNeeds は needs に同じジョブを重複して書いても
// 依存が二重に数えられないことを確かめる。
func TestBuildGraph_DuplicateNeeds(t *testing.T) {
	w := jobsWorkflow(t, job("a", "")+job("b", "[a, a]"))
	g := mustGraph(t, w)

	if got := g.Needs["b"]; !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("重複は取り除かれるべきです: got %v", got)
	}
	want := [][]string{{"a"}, {"b"}}
	if !reflect.DeepEqual(g.Layers, want) {
		t.Errorf("レイヤ: got %v, want %v", g.Layers, want)
	}
}

func TestBuildGraph_CycleTwoJobs(t *testing.T) {
	w := jobsWorkflow(t, job("a", "b")+job("b", "a"))
	_, err := BuildGraph(w)
	if err == nil {
		t.Fatal("循環はエラーになるべきです")
	}
	if !strings.Contains(err.Error(), "needs に循環があります") {
		t.Errorf("想定外のエラーです: %v", err)
	}
	// 循環の経路が示されること。
	if !strings.Contains(err.Error(), "→") {
		t.Errorf("循環の経路が示されるべきです: %v", err)
	}
}

func TestBuildGraph_CycleThreeJobs(t *testing.T) {
	w := jobsWorkflow(t, job("a", "c")+job("b", "a")+job("c", "b"))
	_, err := BuildGraph(w)
	if err == nil {
		t.Fatal("循環はエラーになるべきです")
	}
	for _, id := range []string{"a", "b", "c"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("エラーに %q が含まれるべきです: %v", id, err)
		}
	}
}

// TestBuildGraph_CycleWithIndependentJobs は循環に関係しないジョブが
// エラーメッセージに混ざらないことを確かめる。
func TestBuildGraph_CycleWithIndependentJobs(t *testing.T) {
	w := jobsWorkflow(t, job("free", "")+job("a", "b")+job("b", "a"))
	_, err := BuildGraph(w)
	if err == nil {
		t.Fatal("循環はエラーになるべきです")
	}
	if strings.Contains(err.Error(), "free") {
		t.Errorf("循環に無関係なジョブは含まれないべきです: %v", err)
	}
}

func TestBuildGraph_LayerOfUnknownJob(t *testing.T) {
	w := jobsWorkflow(t, job("a", ""))
	g := mustGraph(t, w)
	if got := g.LayerOf("nosuch"); got != -1 {
		t.Errorf("未知のジョブでは -1 を返すべきです: got %d", got)
	}
}

// TestBuildGraph_RealWorkflow は testdata の CI ワークフローで
// レイヤ分けを確かめる。
func TestBuildGraph_RealWorkflow(t *testing.T) {
	ws, err := workflow.LoadDir("../../testdata/workflows")
	if err != nil {
		t.Fatalf("読み込みに失敗しました: %v", err)
	}
	for _, w := range ws {
		g, err := BuildGraph(w)
		if err != nil {
			t.Fatalf("%s: %v", w.Path, err)
		}
		if w.Name != "CI" {
			continue
		}
		want := [][]string{{"lint"}, {"test"}, {"deploy"}}
		if !reflect.DeepEqual(g.Layers, want) {
			t.Errorf("CI のレイヤ: got %v, want %v", g.Layers, want)
		}
	}
}
