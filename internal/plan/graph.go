package plan

import (
	"fmt"
	"slices"
	"strings"

	"github.com/a2-ito/gh-sim/internal/workflow"
)

// Graph はジョブの依存関係グラフを表す。
type Graph struct {
	// Layers は同時に実行できるジョブの集まりを実行順に並べたもの。
	// 同じレイヤ内のジョブは定義順に並ぶ。
	Layers [][]string

	// Order はトポロジカル順に並べたジョブ ID。
	Order []string

	// Needs はジョブ ID から、依存先のジョブ ID（定義順）への対応。
	// needs の重複は取り除かれている。
	Needs map[string][]string

	// Dependents はジョブ ID から、それに依存するジョブ ID への対応。
	Dependents map[string][]string

	// layerOf はジョブ ID からレイヤ番号への対応。
	layerOf map[string]int
}

// LayerOf はジョブが属するレイヤ番号を返す。未知のジョブでは -1 を返す。
func (g *Graph) LayerOf(id string) int {
	if n, ok := g.layerOf[id]; ok {
		return n
	}
	return -1
}

// BuildGraph は needs から依存グラフを構築する。
// 循環参照がある場合はエラーを返す。
func BuildGraph(w *workflow.Workflow) (*Graph, error) {
	jobs := w.Jobs.List()

	g := &Graph{
		Needs:      make(map[string][]string, len(jobs)),
		Dependents: make(map[string][]string, len(jobs)),
		layerOf:    make(map[string]int, len(jobs)),
	}

	// needs を正規化する。重複と、存在しないジョブへの参照は取り除く。
	// 参照の妥当性は Workflow.Validate が別途エラーとして報告する。
	for _, job := range jobs {
		seen := make(map[string]bool, len(job.Needs))
		var needs []string
		for _, n := range job.Needs {
			if n == job.ID || seen[n] {
				continue
			}
			if _, ok := w.Jobs.Items[n]; !ok {
				continue
			}
			seen[n] = true
			needs = append(needs, n)
		}
		g.Needs[job.ID] = needs
	}

	// 依存の逆向きの辺を作る。並びを安定させるため定義順で走査する。
	for _, job := range jobs {
		for _, n := range g.Needs[job.ID] {
			g.Dependents[n] = append(g.Dependents[n], job.ID)
		}
	}

	// 未処理の依存数。処理済みのジョブは -1 にする。
	remaining := make(map[string]int, len(jobs))
	for _, job := range jobs {
		remaining[job.ID] = len(g.Needs[job.ID])
	}

	done := 0
	for done < len(jobs) {
		// 依存がすべて解決したジョブを定義順に集める。
		var layer []string
		for _, job := range jobs {
			if remaining[job.ID] == 0 {
				layer = append(layer, job.ID)
			}
		}
		if len(layer) == 0 {
			return nil, cycleError(jobs, remaining, g.Needs)
		}

		n := len(g.Layers)
		for _, id := range layer {
			remaining[id] = -1
			g.layerOf[id] = n
			g.Order = append(g.Order, id)
		}
		for _, id := range layer {
			for _, d := range g.Dependents[id] {
				if remaining[d] > 0 {
					remaining[d]--
				}
			}
		}
		g.Layers = append(g.Layers, layer)
		done += len(layer)
	}

	return g, nil
}

// cycleError は循環している依存関係を示すエラーを作る。
func cycleError(jobs []*workflow.Job, remaining map[string]int, needs map[string][]string) error {
	var stuck []string
	for _, job := range jobs {
		if remaining[job.ID] > 0 {
			stuck = append(stuck, job.ID)
		}
	}

	// 循環の経路を1つ示せると原因が追いやすいので探索する。
	if cycle := findCycle(stuck, needs); len(cycle) > 0 {
		return fmt.Errorf("needs に循環があります: %s", strings.Join(cycle, " → "))
	}
	return fmt.Errorf("needs に循環があります（関係するジョブ: %s）", strings.Join(stuck, ", "))
}

// findCycle は循環している経路を1つ探して返す。見つからない場合は nil。
func findCycle(candidates []string, needs map[string][]string) []string {
	// 探索対象を集合にしておき、循環に無関係なジョブを辿らないようにする。
	inScope := make(map[string]bool, len(candidates))
	for _, id := range candidates {
		inScope[id] = true
	}

	var path []string
	onPath := map[string]bool{}
	visited := map[string]bool{}

	var walk func(id string) []string
	walk = func(id string) []string {
		if onPath[id] {
			// 経路上で再訪した位置から後ろが循環部分。
			start := slices.Index(path, id)
			cycle := append([]string{}, path[start:]...)
			return append(cycle, id)
		}
		if visited[id] {
			return nil
		}
		visited[id] = true
		onPath[id] = true
		path = append(path, id)
		for _, n := range needs[id] {
			if !inScope[n] {
				continue
			}
			if cycle := walk(n); cycle != nil {
				return cycle
			}
		}
		path = path[:len(path)-1]
		onPath[id] = false
		return nil
	}

	for _, id := range candidates {
		if cycle := walk(id); cycle != nil {
			return cycle
		}
	}
	return nil
}
