package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update は golden ファイルを現在の出力で書き換える。
// go test ./cmd/gh-sim -run TestExamples -update で指定する。
var update = flag.Bool("update", false, "golden ファイルを現在の出力で更新する")

const (
	examplesDir  = "../../examples/workflows"
	goldenDir    = "../../examples/golden"
	scenarioFile = "../../examples/scenarios.txt"
)

// commonArgs はすべてのシナリオに先に付く引数。
// 各シナリオの引数を後に足すため、必要なら上書きできる。
func commonArgs() []string {
	return []string{"run", "-dir", examplesDir, "-all", "-notes=false"}
}

// scenario はサンプルに対して試す1件の条件。
type scenario struct {
	name string
	args []string
}

// loadScenarios はシナリオ定義を読み込む。
func loadScenarios(t *testing.T) []scenario {
	t.Helper()

	f, err := os.Open(scenarioFile)
	if err != nil {
		t.Fatalf("シナリオ定義を読めません: %v", err)
	}
	defer f.Close()

	var out []scenario
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		name := fields[0]
		if seen[name] {
			t.Fatalf("%s:%d: シナリオ名 %q が重複しています", scenarioFile, line, name)
		}
		seen[name] = true
		out = append(out, scenario{name: name, args: fields[1:]})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("シナリオ定義の読み取りに失敗しました: %v", err)
	}
	if len(out) == 0 {
		t.Fatalf("%s にシナリオがありません", scenarioFile)
	}
	return out
}

// TestExamples はサンプルワークフローに対する出力を golden ファイルと比べる。
// 判定ロジックを変えたときに、どのシナリオの結果が変わったかが差分で分かる。
func TestExamples(t *testing.T) {
	scenarios := loadScenarios(t)

	if *update {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatalf("golden ディレクトリを作れません: %v", err)
		}
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			args := append(commonArgs(), sc.args...)

			var buf bytes.Buffer
			if err := run(args, &buf, &buf); err != nil {
				t.Fatalf("実行に失敗しました（引数: %v）: %v\n%s", args, err, buf.String())
			}

			// 何をどう実行した結果なのかを golden の先頭に残す。
			got := fmt.Sprintf("$ gh-sim %s\n\n%s", strings.Join(args, " "), buf.String())
			// パス表記はテストの実行位置に依存するため、
			// リポジトリルートから実行した場合の表記に揃える。
			got = strings.ReplaceAll(got, "../../examples/", "examples/")
			path := filepath.Join(goldenDir, sc.name+".txt")

			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatalf("golden を書けません: %v", err)
				}
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("golden を読めません（make e2e-update で作成できます）: %v", err)
			}
			if got != string(want) {
				t.Errorf("出力が golden と一致しません。%s\n\n--- 実際 ---\n%s",
					firstDiff(got, string(want)), got)
			}
		})
	}
}

// firstDiff は最初に食い違った行を示す。
func firstDiff(got, want string) string {
	g := strings.Split(got, "\n")
	w := strings.Split(want, "\n")
	for i := range max(len(g), len(w)) {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return fmt.Sprintf("%d行目が違います:\n  実際: %q\n  期待: %q", i+1, gl, wl)
		}
	}
	return "行数だけが違います"
}

// TestExamples_AllWorkflowsParse はサンプルがすべて読み込めることを確かめる。
// サンプルを追加したときに、構文の誤りをここで検出する。
func TestExamples_AllWorkflowsParse(t *testing.T) {
	var buf bytes.Buffer
	if err := run([]string{"parse", "-dir", examplesDir}, &buf, &buf); err != nil {
		t.Fatalf("サンプルの読み込みに失敗しました: %v\n%s", err, buf.String())
	}
}

// TestExamples_GoldenFilesAreNotStale は golden とシナリオの対応を確かめる。
// シナリオを削ったのに golden が残っている状態を防ぐ。
func TestExamples_GoldenFilesAreNotStale(t *testing.T) {
	if *update {
		t.Skip("更新時は検証しない")
	}

	expected := map[string]bool{}
	for _, sc := range loadScenarios(t) {
		expected[sc.name+".txt"] = true
	}

	entries, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatalf("golden ディレクトリを読めません: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		if !expected[e.Name()] {
			t.Errorf("対応するシナリオが無い golden が残っています: %s", e.Name())
		}
	}
}
