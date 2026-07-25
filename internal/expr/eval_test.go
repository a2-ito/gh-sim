package expr

import (
	"strings"
	"testing"
)

// testContext は評価テスト用のコンテキストを組み立てる。
func testContext() *Context {
	ctx := NewContext()
	ctx.Set("github", FromGo(map[string]any{
		"ref":        "refs/heads/main",
		"event_name": "push",
		"actor":      "octocat",
		"run_number": 42,
		"event": map[string]any{
			"commits": []any{
				map[string]any{
					"message": "first commit",
					"author":  map[string]any{"name": "alice"},
				},
				map[string]any{
					"message": "second commit",
				},
			},
			"pull_request": map[string]any{
				"draft": false,
				"head":  map[string]any{"ref": "feature/x"},
				"labels": []any{
					map[string]any{"name": "bug"},
					map[string]any{"name": "urgent"},
				},
			},
		},
	}))
	ctx.Set("env", FromGo(map[string]any{"CI": "true", "RETRY": "3"}))
	ctx.Set("matrix", FromGo(map[string]any{"node": 20, "coverage": true}))
	ctx.Set("inputs", FromGo(map[string]any{"dry-run": false, "target": "prod"}))
	ctx.Set("secrets", FromGo(map[string]any{}))
	return ctx
}

// evalJSON は式を評価し、結果を JSON 表現で返す。
// 文字列比較で値と型の両方を検証できる。
func evalJSON(t *testing.T, src string, ctx *Context) string {
	t.Helper()
	v, err := Evaluate(src, ctx)
	if err != nil {
		t.Fatalf("式 %q の評価に失敗しました: %v", src, err)
	}
	return v.JSON(false)
}

func TestEval_Literals(t *testing.T) {
	tests := map[string]string{
		"true":      "true",
		"false":     "false",
		"null":      "null",
		"1":         "1",
		"-1":        "-1",
		"1.5":       "1.5",
		"-2.25":     "-2.25",
		"0x1f":      "31",
		"1e3":       "1000",
		"1.5e-2":    "0.015",
		"'abc'":     `"abc"`,
		"''":        `""`,
		"'it''s'":   `"it's"`,
		"'a}}b'":    `"a}}b"`,
		"  42  ":    "42",
		"'日本語'":     `"日本語"`,
		"'quote\"'": `"quote\""`,
	}
	for src, want := range tests {
		if got := evalJSON(t, src, nil); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}

// TestEval_LooseEquality は == の型変換規則を検証する。
// 型が違う場合は数値へ変換し、文字列同士は大文字小文字を無視する。
func TestEval_LooseEquality(t *testing.T) {
	tests := map[string]string{
		"1 == 1":         "true",
		"1 == 2":         "false",
		"'a' == 'a'":     "true",
		"'a' == 'A'":     "true",
		"'ABC' == 'abc'": "true",
		"'1' == 1":       "true",
		"'1.0' == 1":     "true",
		"' 1 ' == 1":     "true",
		"true == 1":      "true",
		"false == 0":     "true",
		"true == 'true'": "false",
		"null == 0":      "true",
		"null == ''":     "true",
		"null == null":   "true",
		"null == false":  "true",
		"'' == 0":        "true",
		"'abc' == 1":     "false",
		"'abc' != 1":     "true",
		"'abc' == null":  "false",
		"1 != 2":         "true",
		"'0x1f' == 31":   "true",
	}
	for src, want := range tests {
		if got := evalJSON(t, src, nil); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}

func TestEval_Comparison(t *testing.T) {
	tests := map[string]string{
		"1 < 2":        "true",
		"2 < 1":        "false",
		"1 <= 1":       "true",
		"2 > 1":        "true",
		"1 >= 2":       "false",
		"'2' > 1":      "true",
		"true > false": "true",
		"null < 1":     "true",
		// 数値に変換できない値が混ざる比較は常に false。
		"'a' < 'b'":  "false",
		"'a' > 'b'":  "false",
		"'a' <= 'b'": "false",
	}
	for src, want := range tests {
		if got := evalJSON(t, src, nil); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}

// TestEval_LogicalOperatorsReturnValues は && と || が真偽値ではなく
// 被演算子の値を返すことを検証する。三項演算子の代用として使われる挙動。
func TestEval_LogicalOperatorsReturnValues(t *testing.T) {
	tests := map[string]string{
		"true && 'yes'":       `"yes"`,
		"false && 'yes'":      "false",
		"'' && 'yes'":         `""`,
		"0 && 'yes'":          "0",
		"null && 'yes'":       "null",
		"true || 'no'":        "true",
		"false || 'fallback'": `"fallback"`,
		"'' || 'fallback'":    `"fallback"`,
		"'a' || 'b'":          `"a"`,
		"null || null":        "null",
		// 三項演算子の代用パターン。
		"1 == 1 && 'prod' || 'dev'": `"prod"`,
		"1 == 2 && 'prod' || 'dev'": `"dev"`,
	}
	for src, want := range tests {
		if got := evalJSON(t, src, nil); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}

func TestEval_Precedence(t *testing.T) {
	tests := map[string]string{
		// ! は比較より優先順位が高いので (!'a') == false になる。
		"!'a' == false": "true",
		"!true":         "false",
		"!''":           "true",
		"!null":         "true",
		"!0":            "true",
		"!1":            "false",
		"!!'a'":         "true",
		// && は || より優先順位が高い。
		"true || false && false":   "true",
		"(true || false) && false": "false",
		// == は && より優先順位が高い。
		"1 == 1 && 2 == 2": "true",
		"1 < 2 == true":    "true",
	}
	for src, want := range tests {
		if got := evalJSON(t, src, nil); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}

func TestEval_ContextAccess(t *testing.T) {
	ctx := testContext()
	tests := map[string]string{
		"github.ref":        `"refs/heads/main"`,
		"github.run_number": "42",
		"env.CI":            `"true"`,
		"matrix.node":       "20",
		"matrix.coverage":   "true",
		// プロパティ名の大文字小文字は区別しない。
		"github.REF": `"refs/heads/main"`,
		"GITHUB.ref": `"refs/heads/main"`,
		"env.ci":     `"true"`,
		// ハイフンを含むプロパティ名。
		"inputs.dry-run": "false",
		// 存在しないプロパティは null になり、その先を辿っても null。
		"github.nosuch":        "null",
		"github.nosuch.deeper": "null",
		// 添字アクセス。
		"github['ref']":                      `"refs/heads/main"`,
		"github.event.commits[0].message":    `"first commit"`,
		"github.event.commits[1].message":    `"second commit"`,
		"github.event.commits[9].message":    "null",
		"github.event.commits[-1]":           "null",
		"github.event.pull_request.head.ref": `"feature/x"`,
		"github.event.pull_request.draft":    "false",
		// 添字に式を書ける。式言語に算術演算子は無いので減算は書けない。
		"github.event.commits[0 == 0 && 1 || 0].message": `"second commit"`,
	}
	for src, want := range tests {
		if got := evalJSON(t, src, ctx); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}

// TestEval_ObjectFilter は * によるオブジェクトフィルタを検証する。
// フィルタ結果へのプロパティアクセスは各要素へ写像される。
func TestEval_ObjectFilter(t *testing.T) {
	ctx := testContext()
	tests := map[string]string{
		"github.event.commits.*.message":          `["first commit","second commit"]`,
		"github.event.commits[*].message":         `["first commit","second commit"]`,
		"github.event.pull_request.labels.*.name": `["bug","urgent"]`,
		// 対象のプロパティを持たない要素は結果から除かれる。
		"github.event.commits.*.author.name": `["alice"]`,
		// オブジェクトに対するフィルタは値の一覧になる。
		// FromGo は Go のマップをキー昇順で取り込むため coverage, node の順になる。
		"matrix.*": `[true,20]`,
		// フィルタ結果は contains や join に渡せる。
		"contains(github.event.pull_request.labels.*.name, 'urgent')": "true",
		"contains(github.event.pull_request.labels.*.name, 'nope')":   "false",
		"join(github.event.commits.*.message, ' / ')":                 `"first commit / second commit"`,
	}
	for src, want := range tests {
		if got := evalJSON(t, src, ctx); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}

func TestEval_StatusFunctions(t *testing.T) {
	tests := []struct {
		status Status
		src    string
		want   string
	}{
		{StatusSuccess, "success()", "true"},
		{StatusSuccess, "failure()", "false"},
		{StatusSuccess, "cancelled()", "false"},
		{StatusSuccess, "always()", "true"},
		{StatusFailure, "success()", "false"},
		{StatusFailure, "failure()", "true"},
		{StatusFailure, "always()", "true"},
		{StatusCancelled, "cancelled()", "true"},
		{StatusCancelled, "success()", "false"},
		{StatusCancelled, "always()", "true"},
	}
	for _, tt := range tests {
		ctx := NewContext()
		ctx.Status = tt.status
		if got := evalJSON(t, tt.src, ctx); got != tt.want {
			t.Errorf("状態 %s での %q: got %s, want %s", tt.status, tt.src, got, tt.want)
		}
	}
}

func TestEval_Errors(t *testing.T) {
	ctx := testContext()
	tests := []struct {
		src     string
		wantMsg string
	}{
		{`"abc"`, "シングルクォート"},
		{"1 +", "解釈できない文字"},
		{"'unterminated", "文字列が閉じられていません"},
		{"(1", ") を期待しました"},
		{"1)", "は余分です"},
		{"github.", "プロパティ名が必要です"},
		{"github[", "式が途中で終わっています"},
		{"nosuchctx", `コンテキスト "nosuchctx" は参照できません`},
		{"nosuchfunc()", "関数 nosuchfunc() は存在しません"},
		{"contains('a')", "contains() の引数は2個ですが1個"},
		{"contains('a','b','c')", "contains() の引数は2個ですが3個"},
		{"success(1)", "success() の引数は0個ですが1個"},
		{"join()", "join() の引数は1〜2個ですが0個"},
		{"format()", "format() の引数は1個以上必要ですが0個"},
		{"a = b", "単独の = は使えません"},
		{"true & false", "単独の & は使えません"},
		{"true | false", "単独の | は使えません"},
		{"", "式が途中で終わっています"},
		{"==", "は値の位置には書けません"},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			_, err := Evaluate(tt.src, ctx)
			if err == nil {
				t.Fatalf("式 %q はエラーになるべきです", tt.src)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("エラーに %q が含まれるべきです:\n%v", tt.wantMsg, err)
			}
		})
	}
}

// TestEval_ErrorMentionsAvailableContexts は未知のコンテキストを
// 参照したとき、使える名前が示されることを確かめる。
func TestEval_ErrorMentionsAvailableContexts(t *testing.T) {
	ctx := NewContext()
	ctx.Set("github", Null())
	ctx.Set("matrix", Null())
	_, err := Evaluate("gitub.ref", ctx)
	if err == nil {
		t.Fatal("エラーになるべきです")
	}
	if !strings.Contains(err.Error(), "github, matrix") {
		t.Errorf("使えるコンテキスト名が示されるべきです: %v", err)
	}
}

// TestEval_RealWorldConditions は実際のワークフローで書かれる条件式を検証する。
func TestEval_RealWorldConditions(t *testing.T) {
	ctx := testContext()
	tests := map[string]string{
		"github.ref == 'refs/heads/main'":                                "true",
		"github.ref == 'refs/heads/main' && github.event_name == 'push'": "true",
		"github.event_name == 'pull_request'":                            "false",
		"startsWith(github.ref, 'refs/heads/')":                          "true",
		"!github.event.pull_request.draft":                               "true",
		"contains(github.ref, 'main')":                                   "true",
		"github.actor != 'dependabot[bot]'":                              "true",
		"!inputs.dry-run":                                                "true",
		"format('deploy-{0}-{1}', inputs.target, github.run_number)":     `"deploy-prod-42"`,
		"env.RETRY > 2":                                                  "true",
	}
	for src, want := range tests {
		if got := evalJSON(t, src, ctx); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}
