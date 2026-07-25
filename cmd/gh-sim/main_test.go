package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// workflowsDir は testdata のワークフローディレクトリ。
const workflowsDir = "../../testdata/workflows"

// execute は CLI を実行して標準出力・標準エラーとエラーを返す。
func execute(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	err = run(args, &out, &errBuf)
	return out.String(), errBuf.String(), err
}

func TestRun_Version(t *testing.T) {
	stdout, _, err := execute(t, "version")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	if !strings.HasPrefix(stdout, "gh-sim ") {
		t.Errorf("バージョンが出力されるべきです: %q", stdout)
	}
}

func TestRun_Help(t *testing.T) {
	stdout, _, err := execute(t, "help")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	for _, want := range []string{"run", "eval", "parse"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("ヘルプに %q が含まれるべきです", want)
		}
	}
}

func TestRun_NoArgs(t *testing.T) {
	_, stderr, err := execute(t)
	if err == nil {
		t.Fatal("引数なしはエラーになるべきです")
	}
	if !strings.Contains(stderr, "使い方") {
		t.Errorf("使い方が案内されるべきです: %q", stderr)
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	_, _, err := execute(t, "nosuchcmd")
	if err == nil {
		t.Fatal("不明なコマンドはエラーになるべきです")
	}
	if !strings.Contains(err.Error(), "不明なコマンド") {
		t.Errorf("エラーの内容が想定と違います: %v", err)
	}
}

func TestRun_Dryrun(t *testing.T) {
	stdout, _, err := execute(t, "run", "-dir", workflowsDir, "-event", "push", "-ref", "main", "-notes=false")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}

	// main への push では CI が起動する。
	if !strings.Contains(stdout, "CI") {
		t.Errorf("CI が表示されるべきです:\n%s", stdout)
	}
	if !strings.Contains(stdout, "✔ 起動する") {
		t.Errorf("起動することが示されるべきです:\n%s", stdout)
	}
	// 起動しないワークフローは既定で表示されない。
	if strings.Contains(stdout, "Nightly") {
		t.Errorf("既定では起動しないワークフローを表示しないべきです:\n%s", stdout)
	}
	// matrix が展開される。
	if !strings.Contains(stdout, "matrix: node × os → 5 件") {
		t.Errorf("matrix の展開結果が示されるべきです:\n%s", stdout)
	}
}

// TestRun_ShortRefIsNormalized は refs/ を省略した指定が
// ブランチ名として扱われることを確かめる。
func TestRun_ShortRefIsNormalized(t *testing.T) {
	short, _, err := execute(t, "run", "-dir", workflowsDir, "-ref", "main", "-notes=false")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	full, _, err := execute(t, "run", "-dir", workflowsDir, "-ref", "refs/heads/main", "-notes=false")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	if short != full {
		t.Errorf("main と refs/heads/main は同じ結果になるべきです:\n--- short ---\n%s\n--- full ---\n%s", short, full)
	}
}

func TestRun_ShowAll(t *testing.T) {
	stdout, _, err := execute(t, "run", "-dir", workflowsDir, "-ref", "main", "-all", "-notes=false")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	for _, name := range []string{"CI", "Nightly", "Release"} {
		if !strings.Contains(stdout, name) {
			t.Errorf("-all では %s も表示されるべきです:\n%s", name, stdout)
		}
	}
	if !strings.Contains(stdout, "✘ 起動しない") {
		t.Errorf("起動しない理由が表示されるべきです:\n%s", stdout)
	}
}

func TestRun_Steps(t *testing.T) {
	stdout, _, err := execute(t, "run", "-file", filepath.Join(workflowsDir, "ci.yml"),
		"-ref", "main", "-steps", "-notes=false")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	if !strings.Contains(stdout, "Run actions/checkout@v4") {
		t.Errorf("ステップが表示されるべきです:\n%s", stdout)
	}
	// matrix に coverage が無い組み合わせでは Upload coverage が skip される。
	if !strings.Contains(stdout, "if が偽: ${{ matrix.coverage }}") {
		t.Errorf("ステップの skip 理由が表示されるべきです:\n%s", stdout)
	}
}

// TestRun_TagTriggersRelease はタグへの push で Release だけが起動することを確かめる。
func TestRun_TagTriggersRelease(t *testing.T) {
	stdout, _, err := execute(t, "run", "-dir", workflowsDir, "-ref", "refs/tags/v1.2.3", "-notes=false")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	if !strings.Contains(stdout, "Release") {
		t.Errorf("Release が起動するべきです:\n%s", stdout)
	}
	if strings.Contains(stdout, "✔ 起動する — on.push に一致します\n  ├─ ✔ Lint") {
		t.Errorf("CI は起動しないべきです:\n%s", stdout)
	}
}

func TestRun_PullRequest(t *testing.T) {
	stdout, _, err := execute(t, "run", "-dir", workflowsDir,
		"-event", "pull_request", "-action", "opened", "-base-ref", "main",
		"-ref", "feature/x", "-notes=false")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	if !strings.Contains(stdout, "CI") {
		t.Errorf("pull_request で CI が起動するべきです:\n%s", stdout)
	}
	// deploy は push 限定なので実行されない。
	if !strings.Contains(stdout, "✘ deploy") {
		t.Errorf("deploy は skip されるべきです:\n%s", stdout)
	}
}

// TestRun_ChangedPathsFilter は変更ファイルによる paths フィルタの判定を確かめる。
func TestRun_ChangedPathsFilter(t *testing.T) {
	// ドキュメントのみの変更は paths-ignore で除外される。
	stdout, _, err := execute(t, "run", "-dir", workflowsDir, "-ref", "main",
		"-changed", "README.md", "-notes=false")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	if strings.Contains(stdout, "✔ 起動する") {
		t.Errorf("README.md のみの変更では起動しないべきです:\n%s", stdout)
	}

	// コードの変更があれば起動する。
	stdout, _, err = execute(t, "run", "-dir", workflowsDir, "-ref", "main",
		"-changed", "README.md,src/main.go", "-notes=false")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	if !strings.Contains(stdout, "✔ 起動する") {
		t.Errorf("コードの変更があれば起動するべきです:\n%s", stdout)
	}
}

func TestRun_JSON(t *testing.T) {
	stdout, _, err := execute(t, "run", "-dir", workflowsDir, "-ref", "main", "-json")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}

	var docs []map[string]any
	if err := json.Unmarshal([]byte(stdout), &docs); err != nil {
		t.Fatalf("JSON として読めません: %v\n%s", err, stdout)
	}
	if len(docs) != 3 {
		t.Errorf("3件のワークフローが出力されるべきです: %d", len(docs))
	}
	// JSON では起動しないワークフローも含める。
	var triggered int
	for _, d := range docs {
		if d["triggered"] == true {
			triggered++
		}
	}
	if triggered != 1 {
		t.Errorf("起動するのは1件のはずです: %d", triggered)
	}
}

func TestRun_Eval(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"リテラル", []string{"eval", "1 == 1"}, "true"},
		{"コンテキスト参照", []string{"eval", "github.ref", "-ref", "main"}, "refs/heads/main"},
		{"ref_name", []string{"eval", "github.ref_name", "-ref", "refs/tags/v1.0.0"}, "v1.0.0"},
		{"三項演算子の代用", []string{"eval", "github.ref_name == 'main' && 'prod' || 'dev'", "-ref", "main"}, "prod"},
		{"関数", []string{"eval", "format('{0}!', 'hi')"}, "hi!"},
		// フラグを式より前に置いても解釈できる。
		{"フラグが先", []string{"eval", "-ref", "develop", "github.ref_name"}, "develop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, _, err := execute(t, tt.args...)
			if err != nil {
				t.Fatalf("失敗しました: %v", err)
			}
			if !strings.HasPrefix(stdout, tt.want) {
				t.Errorf("出力が想定と違います: got %q, want の先頭 %q", stdout, tt.want)
			}
		})
	}
}

func TestRun_EvalJSON(t *testing.T) {
	stdout, _, err := execute(t, "eval", "fromJSON('[1,2]')", "-json")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	if strings.TrimSpace(stdout) != "[1,2]" {
		t.Errorf("JSON 出力が想定と違います: %q", stdout)
	}
}

func TestRun_EvalErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{"式なし", []string{"eval"}, "評価する式を1つ指定してください"},
		{"式が2つ", []string{"eval", "1", "2"}, "評価する式を1つ指定してください"},
		{"ダブルクォート", []string{"eval", `github.ref == "main"`}, "シングルクォート"},
		{"未知のコンテキスト", []string{"eval", "nosuch.value"}, "参照できません"},
		{"未知の関数", []string{"eval", "nosuchfn()"}, "存在しません"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := execute(t, tt.args...)
			if err == nil {
				t.Fatal("エラーになるべきです")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("エラーに %q が含まれるべきです: %v", tt.wantMsg, err)
			}
		})
	}
}

func TestRun_Parse(t *testing.T) {
	stdout, _, err := execute(t, "parse", "-dir", workflowsDir)
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	for _, want := range []string{"name: CI", "on:   push, pull_request", "runs-on: ubuntu-latest"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("出力に %q が含まれるべきです:\n%s", want, stdout)
		}
	}
}

func TestRun_NoSuchDir(t *testing.T) {
	_, _, err := execute(t, "run", "-dir", "../../testdata/nosuchdir")
	if err == nil {
		t.Fatal("存在しないディレクトリはエラーになるべきです")
	}
}

// TestRun_PartialLoadFailure は一部のワークフローが不正な場合、
// 読めたものの結果を出したうえでエラーを報告することを確かめる。
func TestRun_PartialLoadFailure(t *testing.T) {
	stdout, _, err := execute(t, "run", "-dir", "../../testdata/broken/mixed", "-ref", "main", "-notes=false")
	if err == nil {
		t.Fatal("不正なファイルがあるためエラーを返すべきです")
	}
	if !strings.Contains(stdout, "Good") {
		t.Errorf("読めたワークフローの結果は出力されるべきです:\n%s", stdout)
	}
	if !strings.Contains(err.Error(), "runs-on が指定されていません") {
		t.Errorf("読み込みエラーの内容が伝わるべきです: %v", err)
	}
}

// TestRun_KeyValueFlagValidation は key=value 形式でない指定を弾くことを確かめる。
func TestRun_KeyValueFlagValidation(t *testing.T) {
	_, _, err := execute(t, "run", "-dir", workflowsDir, "-input", "notakeyvalue")
	if err == nil {
		t.Fatal("key=value でない指定はエラーになるべきです")
	}
}

// TestRun_PayloadFile はイベントペイロードをファイルから読めることを確かめる。
func TestRun_PayloadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "event.json")
	payload := `{"pull_request": {"number": 99, "draft": true}}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("ファイルを書けません: %v", err)
	}

	stdout, _, err := execute(t, "eval", "github.event.pull_request.number",
		"-event", "pull_request", "-payload", path)
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	if !strings.HasPrefix(stdout, "99") {
		t.Errorf("ペイロードの値が使われるべきです: %q", stdout)
	}

	// 不正な JSON はエラーになる。
	if err := os.WriteFile(path, []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("ファイルを書けません: %v", err)
	}
	if _, _, err := execute(t, "eval", "1", "-payload", path); err == nil {
		t.Fatal("不正な JSON はエラーになるべきです")
	}
}

// TestRun_SecretsAndVars はシークレットと変数を与えて評価できることを確かめる。
func TestRun_SecretsAndVars(t *testing.T) {
	stdout, _, err := execute(t, "eval", "secrets.TOKEN", "-secret", "TOKEN=abc")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	if !strings.HasPrefix(stdout, "abc") {
		t.Errorf("シークレットが反映されるべきです: %q", stdout)
	}

	stdout, _, err = execute(t, "eval", "vars.REGION", "-var", "REGION=tokyo")
	if err != nil {
		t.Fatalf("失敗しました: %v", err)
	}
	if !strings.HasPrefix(stdout, "tokyo") {
		t.Errorf("変数が反映されるべきです: %q", stdout)
	}
}
