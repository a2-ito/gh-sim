package expr

import (
	"strings"
	"testing"
)

func TestTemplate_Eval(t *testing.T) {
	ctx := testContext()
	tests := map[string]string{
		// 式を含まない文字列はそのまま返る。
		"plain text":                "plain text",
		"":                          "",
		"${{ github.ref }}":         "refs/heads/main",
		"ref is ${{ github.ref }}":  "ref is refs/heads/main",
		"${{ github.ref }} is ref":  "refs/heads/main is ref",
		"${{ 1 }}-${{ 2 }}":         "1-2",
		"v${{ github.run_number }}": "v42",
		// 値は文字列に型変換される。
		"${{ true }}": "true",
		"${{ null }}": "",
		"${{ 1.5 }}":  "1.5",
		// 式の中の文字列リテラルに }} が含まれても終端と誤認しない。
		"${{ '}}' }}":    "}}",
		"${{ 'a}}b' }}c": "a}}bc",
		// ${ だけの並びは式として扱わない。
		"${notexpr}": "${notexpr}",
		"$ {{ x }}":  "$ {{ x }}",
	}
	for src, want := range tests {
		t.Run(src, func(t *testing.T) {
			tmpl, err := CompileTemplate(src)
			if err != nil {
				t.Fatalf("解析に失敗しました: %v", err)
			}
			got, err := tmpl.Eval(ctx)
			if err != nil {
				t.Fatalf("評価に失敗しました: %v", err)
			}
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// TestTemplate_EvalValuePreservesType は全体がちょうど1つの式である場合に
// 値の型が保たれることを確かめる。with: に配列や真偽値を渡す場合に効く。
func TestTemplate_EvalValuePreservesType(t *testing.T) {
	tests := map[string]string{
		// 単一の式なら型が保たれる。
		"${{ 1 }}":                 "1",
		"${{ true }}":              "true",
		"${{ null }}":              "null",
		"${{ 'abc' }}":             `"abc"`,
		"${{ fromJSON('[1,2]') }}": "[1,2]",
		// リテラルが混在すると文字列になる。
		"v${{ 1 }}":        `"v1"`,
		"${{ 1 }}${{ 2 }}": `"12"`,
		"${{ true }} ":     `"true "`,
		// 式を含まない場合も文字列。
		"plain": `"plain"`,
	}
	for src, want := range tests {
		t.Run(src, func(t *testing.T) {
			tmpl, err := CompileTemplate(src)
			if err != nil {
				t.Fatalf("解析に失敗しました: %v", err)
			}
			v, err := tmpl.EvalValue(nil)
			if err != nil {
				t.Fatalf("評価に失敗しました: %v", err)
			}
			if got := v.JSON(false); got != want {
				t.Errorf("got %s, want %s", got, want)
			}
		})
	}
}

func TestTemplate_HasExpr(t *testing.T) {
	tests := map[string]bool{
		"plain":      false,
		"":           false,
		"${{ 1 }}":   true,
		"a${{ 1 }}b": true,
		"${notexpr}": false,
	}
	for src, want := range tests {
		tmpl, err := CompileTemplate(src)
		if err != nil {
			t.Fatalf("%q の解析に失敗しました: %v", src, err)
		}
		if got := tmpl.HasExpr(); got != want {
			t.Errorf("%q: HasExpr() = %v, want %v", src, got, want)
		}
	}
}

func TestTemplate_Errors(t *testing.T) {
	tests := []struct {
		src     string
		wantMsg string
	}{
		{"${{ 1", "}} で閉じられていません"},
		{"${{ 'unclosed }}", "}} で閉じられていません"},
		{"${{ nosuchfunc() }}", "存在しません"},
		{"${{ 'a' == }}", "式が途中で終わっています"},
		{"${{ == 'a' }}", "は値の位置には書けません"},
		{"prefix ${{ }} suffix", "式が途中で終わっています"},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			tmpl, err := CompileTemplate(tt.src)
			if err == nil {
				// 解析が通る場合は評価時にエラーになるはず。
				_, err = tmpl.Eval(NewContext())
			}
			if err == nil {
				t.Fatalf("エラーになるべきです")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("エラーに %q が含まれるべきです:\n%v", tt.wantMsg, err)
			}
		})
	}
}

// TestCompileCondition は if: の値が ${{ }} の有無どちらでも
// 式として評価されることを確かめる。
func TestCompileCondition(t *testing.T) {
	ctx := testContext()
	tests := map[string]bool{
		// ${{ }} を省略した形。
		"success()":                       true,
		"failure()":                       false,
		"github.ref == 'refs/heads/main'": true,
		"github.event_name == 'schedule'": false,
		"!inputs.dry-run":                 true,
		"true":                            true,
		"false":                           false,
		// ${{ }} で囲んだ形。
		"${{ success() }}":                       true,
		"${{ github.ref == 'refs/heads/main' }}": true,
		"${{ false }}":                           false,
		// 前後に空白があっても真偽が変わらない。
		"  ${{ false }}  ": false,
		"  false  ":        false,
		// 値の truthy 判定に従う。
		"github.ref":      true,
		"matrix.coverage": true,
		"github.nosuch":   false,
	}
	for src, want := range tests {
		t.Run(src, func(t *testing.T) {
			cond, err := CompileCondition(src)
			if err != nil {
				t.Fatalf("解析に失敗しました: %v", err)
			}
			got, err := cond.EvalCondition(ctx)
			if err != nil {
				t.Fatalf("評価に失敗しました: %v", err)
			}
			if got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

func TestCompileCondition_Empty(t *testing.T) {
	if _, err := CompileCondition("   "); err == nil {
		t.Fatal("空の条件式はエラーになるべきです")
	}
}
