package ghctx

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// globCache はコンパイル済みのパターンを再利用する。
var globCache sync.Map // map[string]*compiledGlob

type compiledGlob struct {
	re  *regexp.Regexp
	err error
}

// matchGlobList は hashFiles のパターン一覧で対象を判定する。
// ! で始まるパターンはそれまでの一致を打ち消す。
func matchGlobList(patterns []string, target string) (bool, error) {
	matched := false
	for _, p := range patterns {
		negate := strings.HasPrefix(p, "!")
		if negate {
			p = p[1:]
		}
		ok, err := matchGlob(p, target)
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

// matchGlob は hashFiles のパターンに対象が一致するかを返す。
//
// hashFiles のパターンは @actions/glob（minimatch 系）に従うもので、
// ワークフローの paths / branches フィルタとは構文が異なる。
// 混同すると判定がずれるため、実装を分けている。
//
//	**/  … 0階層以上のディレクトリ。**/go.sum はルートの go.sum にも一致する
//	**   … パスの区切りを跨ぐ任意の文字列
//	*    … スラッシュ以外の0文字以上
//	?    … 任意の1文字（フィルタ側の「直前の要素の0個か1個」ではない）
//	[]   … 文字クラス
func matchGlob(pattern, target string) (bool, error) {
	c := compileGlob(pattern)
	if c.err != nil {
		return false, c.err
	}
	return c.re.MatchString(target), nil
}

func compileGlob(pattern string) *compiledGlob {
	if cached, ok := globCache.Load(pattern); ok {
		return cached.(*compiledGlob)
	}
	c := buildGlob(pattern)
	globCache.Store(pattern, c)
	return c
}

func buildGlob(pattern string) *compiledGlob {
	var sb strings.Builder
	sb.WriteByte('^')

	i := 0
	for i < len(pattern) {
		switch c := pattern[i]; c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				// **/ は0階層以上に一致させる。これによりルート直下の
				// ファイルも **/name の対象になる。
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					sb.WriteString("(?:.*/)?")
					i += 3
					continue
				}
				sb.WriteString(".*")
				i += 2
				continue
			}
			sb.WriteString("[^/]*")
			i++

		case '?':
			sb.WriteString("[^/]")
			i++

		case '[':
			end := strings.IndexByte(pattern[i:], ']')
			if end < 0 {
				return &compiledGlob{err: fmt.Errorf("パターン %q: [ が ] で閉じられていません", pattern)}
			}
			sb.WriteString(pattern[i : i+end+1])
			i += end + 1

		case '\\':
			if i+1 >= len(pattern) {
				return &compiledGlob{err: fmt.Errorf("パターン %q: \\ の後に文字がありません", pattern)}
			}
			sb.WriteString(regexp.QuoteMeta(string(pattern[i+1])))
			i += 2

		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}

	sb.WriteByte('$')
	re, err := regexp.Compile(sb.String())
	if err != nil {
		return &compiledGlob{err: fmt.Errorf("パターン %q を解釈できません: %w", pattern, err)}
	}
	return &compiledGlob{re: re}
}
