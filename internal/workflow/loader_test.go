package workflow

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// testdataDir はリポジトリ直下の testdata へのパス。
func testdataDir(elem ...string) string {
	return filepath.Join(append([]string{"..", "..", "testdata"}, elem...)...)
}

func TestLoadDir(t *testing.T) {
	ws, err := LoadDir(testdataDir("workflows"))
	if err != nil {
		t.Fatalf("読み込みに失敗しました: %v", err)
	}

	// os.ReadDir はファイル名順に返すため、結果もその順になる。
	var names []string
	for _, w := range ws {
		names = append(names, w.Name)
	}
	want := []string{"CI", "Nightly", "Release"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("読み込み順が想定と違います: got %v, want %v", names, want)
	}

	ci := ws[0]
	if got := ci.On.Names(); !reflect.DeepEqual(got, []string{"push", "pull_request"}) {
		t.Errorf("ci.yml の on: got %v", got)
	}
	if got := ci.Jobs.Order; !reflect.DeepEqual(got, []string{"lint", "test", "deploy"}) {
		t.Errorf("ci.yml のジョブ順: got %v", got)
	}
	// runs-on に式を書いた場合はラベルとして生の文字列を保持する。
	if got := ci.Jobs.Items["test"].RunsOn.Labels; !reflect.DeepEqual(got, []string{"${{ matrix.os }}"}) {
		t.Errorf("ci.yml の test.runs-on: got %v", got)
	}

	nightly := ws[1]
	notify := nightly.Jobs.Items["notify"]
	if !notify.IsReusableCall() {
		t.Error("nightly.yml の notify は再利用可能ワークフロー呼び出しであるべきです")
	}
	if !notify.Secrets.Inherit {
		t.Error("nightly.yml の notify は secrets: inherit であるべきです")
	}
}

// TestLoadDir_PartialFailure は一部のファイルが不正でも
// 残りを読み込み、エラーは併せて返すことを確かめる。
func TestLoadDir_PartialFailure(t *testing.T) {
	ws, err := LoadDir(testdataDir("broken", "mixed"))
	if err == nil {
		t.Fatal("不正なファイルがあるためエラーを返すべきです")
	}
	if !strings.Contains(err.Error(), "runs-on が指定されていません") {
		t.Errorf("想定外のエラーです: %v", err)
	}
	if len(ws) != 1 || ws[0].Name != "Good" {
		t.Errorf("正しいファイルは読み込まれるべきです: got %+v", ws)
	}
}

func TestLoadDir_NoSuchDir(t *testing.T) {
	if _, err := LoadDir(testdataDir("nosuchdir")); err == nil {
		t.Fatal("存在しないディレクトリではエラーになるべきです")
	}
}

func TestLoadDir_NoWorkflows(t *testing.T) {
	// ワークフローを含まないディレクトリを用意する。
	dir := t.TempDir()
	if _, err := LoadDir(dir); err == nil {
		t.Fatal("ワークフローが無い場合はエラーになるべきです")
	}
}

func TestIsWorkflowFile(t *testing.T) {
	tests := map[string]bool{
		"ci.yml":     true,
		"ci.yaml":    true,
		"ci.YML":     true,
		"README.md":  false,
		"ci.yml.bak": false,
		"noext":      false,
	}
	for name, want := range tests {
		if got := isWorkflowFile(name); got != want {
			t.Errorf("isWorkflowFile(%q): got %v, want %v", name, got, want)
		}
	}
}
