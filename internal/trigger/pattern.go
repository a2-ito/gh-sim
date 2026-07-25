package trigger

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// patternCache はコンパイル済みパターンを再利用するためのキャッシュ。
// 同じパターンがワークフローやファイルごとに何度も照合されるため、
// 正規表現のコンパイルを繰り返さないようにする。
var patternCache sync.Map // map[string]*compiledPattern

// compiledPattern はコンパイル済みのフィルタパターン。
type compiledPattern struct {
	re  *regexp.Regexp
	err error
}

// MatchPattern は GitHub のフィルタパターンに対象が一致するかを返す。
func MatchPattern(pattern, target string) (bool, error) {
	c := compilePattern(pattern)
	if c.err != nil {
		return false, c.err
	}
	return c.re.MatchString(target), nil
}

// MatchFilters はフィルタパターンの一覧に対象が一致するかを返す。
// ! で始まるパターンはそれより前のパターンによる一致を打ち消し、
// 最後に一致したパターンの符号が結果になる。
func MatchFilters(patterns []string, target string) (bool, error) {
	matched := false
	for _, p := range patterns {
		negate := strings.HasPrefix(p, "!")
		if negate {
			p = p[1:]
		}
		ok, err := MatchPattern(p, target)
		if err != nil {
			return false, err
		}
		if !ok {
			continue
		}
		matched = !negate
	}
	return matched, nil
}

// compilePattern は GitHub のフィルタパターンを正規表現に変換する。
//
// GitHub のパターン構文は一般的な glob とは異なる点がある。
//   - * はスラッシュ以外の0文字以上
//   - ** はスラッシュを含む0文字以上
//   - ? は直前の要素の0個か1個（「任意の1文字」ではない）
//   - + は直前の要素の1個以上
//   - [] は文字クラス
//
// ? と + が量指定子として働くため、glob ライブラリでは正しく判定できない。
func compilePattern(pattern string) *compiledPattern {
	if cached, ok := patternCache.Load(pattern); ok {
		return cached.(*compiledPattern)
	}
	c := buildPattern(pattern)
	patternCache.Store(pattern, c)
	return c
}

func buildPattern(pattern string) *compiledPattern {
	var sb strings.Builder
	sb.WriteByte('^')

	// hasAtom は量指定子（? と +）を適用できる要素が直前にあるかを表す。
	hasAtom := false

	i := 0
	for i < len(pattern) {
		switch c := pattern[i]; c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				// ** はパスの区切りを跨いで一致する。
				sb.WriteString("(?:.*)")
				i += 2
			} else {
				sb.WriteString("(?:[^/]*)")
				i++
			}
			hasAtom = true

		case '?':
			if !hasAtom {
				return &compiledPattern{err: fmt.Errorf("パターン %q: ? の前に対象となる要素がありません", pattern)}
			}
			sb.WriteByte('?')
			// 量指定子を重ねて適用することはできない。
			hasAtom = false
			i++

		case '+':
			if !hasAtom {
				return &compiledPattern{err: fmt.Errorf("パターン %q: + の前に対象となる要素がありません", pattern)}
			}
			sb.WriteByte('+')
			hasAtom = false
			i++

		case '[':
			end := strings.IndexByte(pattern[i:], ']')
			if end < 0 {
				return &compiledPattern{err: fmt.Errorf("パターン %q: [ が ] で閉じられていません", pattern)}
			}
			// 文字クラスは正規表現の記法とほぼ同じなのでそのまま持ち込む。
			sb.WriteString(pattern[i : i+end+1])
			hasAtom = true
			i += end + 1

		case '\\':
			if i+1 >= len(pattern) {
				return &compiledPattern{err: fmt.Errorf("パターン %q: \\ の後に文字がありません", pattern)}
			}
			sb.WriteString(regexp.QuoteMeta(string(pattern[i+1])))
			hasAtom = true
			i += 2

		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
			hasAtom = true
			i++
		}
	}

	sb.WriteByte('$')
	re, err := regexp.Compile(sb.String())
	if err != nil {
		return &compiledPattern{err: fmt.Errorf("パターン %q を解釈できません: %w", pattern, err)}
	}
	return &compiledPattern{re: re}
}
