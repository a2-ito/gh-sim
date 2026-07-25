package expr

import (
	"fmt"
	"strings"
)

// Template は ${{ }} を含みうる文字列を表す。
// env の値や run の内容のように、リテラルと式が混在する箇所に使う。
type Template struct {
	src   string
	parts []templatePart
}

// templatePart はテンプレートを構成する断片。
// expr が nil の場合は literal がそのまま出力される。
type templatePart struct {
	literal string
	expr    *Expression
}

// Source は元の文字列を返す。
func (t *Template) Source() string { return t.src }

// HasExpr は式を含むかを返す。
func (t *Template) HasExpr() bool {
	for _, p := range t.parts {
		if p.expr != nil {
			return true
		}
	}
	return false
}

// Exprs は含まれる式を出現順に返す。
func (t *Template) Exprs() []*Expression {
	var out []*Expression
	for _, p := range t.parts {
		if p.expr != nil {
			out = append(out, p.expr)
		}
	}
	return out
}

// CompileTemplate は ${{ }} を含む文字列を解析する。
func CompileTemplate(src string) (*Template, error) {
	t := &Template{src: src}
	i := 0
	for i < len(src) {
		rel := strings.Index(src[i:], "${{")
		if rel < 0 {
			t.parts = append(t.parts, templatePart{literal: src[i:]})
			break
		}
		start := i + rel
		if start > i {
			t.parts = append(t.parts, templatePart{literal: src[i:start]})
		}
		end, err := findCloseBraces(src, start+3)
		if err != nil {
			return nil, err
		}
		exprSrc := src[start+3 : end]
		e, err := Compile(exprSrc)
		if err != nil {
			return nil, fmt.Errorf("式 ${{%s}} を解析できません: %w", exprSrc, err)
		}
		t.parts = append(t.parts, templatePart{expr: e})
		i = end + 2
	}
	// 空文字列の場合もリテラル1つとして扱い、評価時に空文字列を返す。
	if len(t.parts) == 0 {
		t.parts = []templatePart{{literal: ""}}
	}
	return t, nil
}

// CompileCondition は if: の値を解析する。
// GitHub では if: の値は ${{ }} を省略して式だけを書けるため、
// 囲みが無い場合は全体を式として扱う。
func CompileCondition(src string) (*Template, error) {
	trimmed := strings.TrimSpace(src)
	if trimmed == "" {
		return nil, fmt.Errorf("条件式が空です")
	}
	if !strings.Contains(trimmed, "${{") {
		e, err := Compile(trimmed)
		if err != nil {
			return nil, err
		}
		return &Template{src: src, parts: []templatePart{{expr: e}}}, nil
	}
	// 前後の空白を落としてから解析する。空白が残ると
	// ${{ }} 単体の条件が文字列補間として扱われ、常に真になってしまう。
	return CompileTemplate(trimmed)
}

// findCloseBraces は式の終わりを表す }} の位置を返す。
// 式中の文字列リテラルに現れる }} は終端として扱わない。
func findCloseBraces(src string, from int) (int, error) {
	i := from
	inString := false
	for i < len(src) {
		c := src[i]
		if inString {
			if c == '\'' {
				// '' はエスケープされた ' なので文字列は続く。
				if i+1 < len(src) && src[i+1] == '\'' {
					i += 2
					continue
				}
				inString = false
			}
			i++
			continue
		}
		switch {
		case c == '\'':
			inString = true
			i++
		case c == '}' && i+1 < len(src) && src[i+1] == '}':
			return i, nil
		default:
			i++
		}
	}
	return 0, fmt.Errorf("%d文字目の ${{ が }} で閉じられていません", from-2)
}

// Eval はテンプレートを文字列として評価する。
func (t *Template) Eval(ctx *Context) (string, error) {
	var sb strings.Builder
	for _, p := range t.parts {
		if p.expr == nil {
			sb.WriteString(p.literal)
			continue
		}
		v, err := p.expr.Eval(ctx)
		if err != nil {
			return "", err
		}
		sb.WriteString(v.AsString())
	}
	return sb.String(), nil
}

// EvalValue はテンプレートを値として評価する。
// 全体がちょうど1つの式だけで構成される場合は型を保った値を返し、
// リテラルが混在する場合は文字列として補間した結果を返す。
func (t *Template) EvalValue(ctx *Context) (Value, error) {
	if len(t.parts) == 1 && t.parts[0].expr != nil {
		return t.parts[0].expr.Eval(ctx)
	}
	s, err := t.Eval(ctx)
	if err != nil {
		return Null(), err
	}
	return String(s), nil
}

// EvalCondition は条件として評価し真偽を返す。
func (t *Template) EvalCondition(ctx *Context) (bool, error) {
	v, err := t.EvalValue(ctx)
	if err != nil {
		return false, err
	}
	return v.Truthy(), nil
}
