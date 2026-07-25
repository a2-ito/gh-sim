package expr

import (
	"errors"
	"strings"
	"testing"
)

func TestFn_Contains(t *testing.T) {
	tests := map[string]string{
		// 文字列としての部分一致。大文字小文字は区別しない。
		"contains('Hello world', 'llo')": "true",
		"contains('Hello world', 'LLO')": "true",
		"contains('Hello', 'xyz')":       "false",
		"contains('Hello', '')":          "true",
		// 数値も文字列化して比較される。
		"contains(123, '2')": "true",
		// 配列は要素の等価比較。緩い比較なので型が違っても一致する。
		"contains(fromJSON('[1,2,3]'), 2)":         "true",
		"contains(fromJSON('[1,2,3]'), '2')":       "true",
		"contains(fromJSON('[1,2,3]'), 9)":         "false",
		"contains(fromJSON('[\"a\",\"b\"]'), 'A')": "true",
		"contains(fromJSON('[]'), 'a')":            "false",
	}
	for src, want := range tests {
		if got := evalJSON(t, src, nil); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}

func TestFn_StartsWithEndsWith(t *testing.T) {
	tests := map[string]string{
		"startsWith('Hello world', 'Hello')": "true",
		"startsWith('Hello world', 'hello')": "true",
		"startsWith('Hello', 'Hello world')": "false",
		"startsWith('Hello', '')":            "true",
		"startswith('Hello', 'he')":          "true",
		"endsWith('Hello world', 'world')":   "true",
		"endsWith('Hello world', 'WORLD')":   "true",
		"endsWith('Hello', 'xyz')":           "false",
		"endsWith('Hello', '')":              "true",
		// 数値も文字列化される。
		"startsWith(1234, '12')": "true",
	}
	for src, want := range tests {
		if got := evalJSON(t, src, nil); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}

func TestFn_Format(t *testing.T) {
	tests := map[string]string{
		"format('{0} and {1}', 'a', 'b')":  `"a and b"`,
		"format('{1} then {0}', 'a', 'b')": `"b then a"`,
		"format('{0}{0}', 'x')":            `"xx"`,
		"format('no placeholder')":         `"no placeholder"`,
		// {{ と }} はそれぞれ { } として出力される。
		"format('{{literal}}')":  `"{literal}"`,
		"format('{{{0}}}', 'v')": `"{v}"`,
		// 値は型変換されて埋め込まれる。
		"format('{0}', true)": `"true"`,
		"format('{0}', null)": `""`,
		"format('{0}', 1.5)":  `"1.5"`,
	}
	for src, want := range tests {
		if got := evalJSON(t, src, nil); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}

func TestFn_FormatErrors(t *testing.T) {
	tests := []struct {
		src     string
		wantMsg string
	}{
		{"format('{0}')", "{0} に対応する引数がありません"},
		{"format('{2}', 'a')", "{2} に対応する引数がありません"},
		{"format('{x}', 'a')", "{x} を引数の番号として解釈できません"},
		{"format('{0', 'a')", "閉じられていない { があります"},
		{"format('a}b', 'x')", "対応する { のない } があります"},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			if _, err := Evaluate(tt.src, nil); err == nil {
				t.Fatal("エラーになるべきです")
			} else if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("エラーに %q が含まれるべきです: %v", tt.wantMsg, err)
			}
		})
	}
}

func TestFn_Join(t *testing.T) {
	tests := map[string]string{
		// 区切り文字の既定値はカンマ。
		"join(fromJSON('[1,2,3]'))":             `"1,2,3"`,
		"join(fromJSON('[1,2,3]'), '-')":        `"1-2-3"`,
		"join(fromJSON('[1,2,3]'), ' / ')":      `"1 / 2 / 3"`,
		"join(fromJSON('[]'), '-')":             `""`,
		"join(fromJSON('[\"a\",null,1]'), '|')": `"a||1"`,
		// 配列以外は単一の値として文字列化される。
		"join('abc', '-')": `"abc"`,
		"join(42)":         `"42"`,
	}
	for src, want := range tests {
		if got := evalJSON(t, src, nil); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}

// TestFn_ToJSON は toJSON がインデント付きで、
// キーの順序を保って出力することを確かめる。
func TestFn_ToJSON(t *testing.T) {
	ctx := NewContext()
	o := NewObject()
	o.Set("zebra", Number(1))
	o.Set("alpha", String("x"))
	o.Set("nested", Array([]Value{Number(1), Bool(true), Null()}))
	ctx.Set("obj", Obj(o))

	v, err := Evaluate("toJSON(obj)", ctx)
	if err != nil {
		t.Fatalf("評価に失敗しました: %v", err)
	}
	want := `{
  "zebra": 1,
  "alpha": "x",
  "nested": [
    1,
    true,
    null
  ]
}`
	if got := v.AsString(); got != want {
		t.Errorf("toJSON の出力が想定と違います:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestFn_ToJSONScalars(t *testing.T) {
	tests := map[string]string{
		"toJSON('abc')": `"\"abc\""`,
		"toJSON(1)":     `"1"`,
		"toJSON(true)":  `"true"`,
		"toJSON(null)":  `"null"`,
	}
	for src, want := range tests {
		if got := evalJSON(t, src, nil); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}

func TestFn_FromJSON(t *testing.T) {
	tests := map[string]string{
		`fromJSON('1')`:                     "1",
		`fromJSON('"abc"')`:                 `"abc"`,
		`fromJSON('true')`:                  "true",
		`fromJSON('null')`:                  "null",
		`fromJSON('')`:                      "null",
		`fromJSON('  ')`:                    "null",
		`fromJSON('[1,2]')`:                 "[1,2]",
		`fromJSON('[]')`:                    "[]",
		`fromJSON('{}')`:                    "{}",
		`fromJSON('{"a":1}').a`:             "1",
		`fromJSON('{"a":{"b":2}}').a.b`:     "2",
		`fromJSON('[{"n":1},{"n":2}]').*.n`: "[1,2]",
		// 数値は整数と小数を区別せず扱う。
		`fromJSON('1.5')`:   "1.5",
		`fromJSON('[1.0]')`: "[1]",
	}
	for src, want := range tests {
		if got := evalJSON(t, src, nil); got != want {
			t.Errorf("%q: got %s, want %s", src, got, want)
		}
	}
}

// TestFn_FromJSONPreservesKeyOrder は fromJSON がキーの出現順を保つことを確かめる。
// matrix を fromJSON で組み立てる場合、軸の順序が出力順に影響する。
func TestFn_FromJSONPreservesKeyOrder(t *testing.T) {
	v, err := Evaluate(`toJSON(fromJSON('{"zebra":1,"alpha":2,"middle":3}'))`, nil)
	if err != nil {
		t.Fatalf("評価に失敗しました: %v", err)
	}
	want := `{
  "zebra": 1,
  "alpha": 2,
  "middle": 3
}`
	if got := v.AsString(); got != want {
		t.Errorf("キーの順序が保たれていません:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestFn_FromJSONError(t *testing.T) {
	if _, err := Evaluate(`fromJSON('{invalid}')`, nil); err == nil {
		t.Fatal("不正な JSON はエラーになるべきです")
	} else if !strings.Contains(err.Error(), "JSON として解釈できません") {
		t.Errorf("想定外のエラーです: %v", err)
	}
}

func TestFn_HashFiles(t *testing.T) {
	ctx := NewContext()
	var gotPatterns []string
	ctx.HashFiles = func(patterns []string) (string, error) {
		gotPatterns = patterns
		return "deadbeef", nil
	}

	if got := evalJSON(t, "hashFiles('**/go.sum', 'go.mod')", ctx); got != `"deadbeef"` {
		t.Errorf("hashFiles の結果: got %s", got)
	}
	want := []string{"**/go.sum", "go.mod"}
	if len(gotPatterns) != 2 || gotPatterns[0] != want[0] || gotPatterns[1] != want[1] {
		t.Errorf("hashFiles に渡されたパターン: got %v, want %v", gotPatterns, want)
	}
}

// TestFn_HashFilesUnavailable は hashFiles の実装が無い評価環境では
// エラーになることを確かめる。実ファイルに依存するため黙って値を返さない。
func TestFn_HashFilesUnavailable(t *testing.T) {
	if _, err := Evaluate("hashFiles('go.mod')", NewContext()); err == nil {
		t.Fatal("実装が無い場合はエラーになるべきです")
	} else if !strings.Contains(err.Error(), "この評価環境では使えません") {
		t.Errorf("想定外のエラーです: %v", err)
	}
}

func TestFn_HashFilesError(t *testing.T) {
	ctx := NewContext()
	ctx.HashFiles = func([]string) (string, error) {
		return "", errors.New("ファイルが見つかりません")
	}
	if _, err := Evaluate("hashFiles('nosuch')", ctx); err == nil {
		t.Fatal("エラーになるべきです")
	} else if !strings.Contains(err.Error(), "ファイルが見つかりません") {
		t.Errorf("元のエラーが伝わるべきです: %v", err)
	}
}

// TestFn_NameIsCaseInsensitive は関数名の大文字小文字が区別されないことを確かめる。
func TestFn_NameIsCaseInsensitive(t *testing.T) {
	for _, src := range []string{"toJSON(1)", "tojson(1)", "TOJSON(1)", "ToJson(1)"} {
		if got := evalJSON(t, src, nil); got != `"1"` {
			t.Errorf("%q: got %s", src, got)
		}
	}
}
