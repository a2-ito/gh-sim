package expr

import (
	"fmt"
	"strconv"
	"strings"
)

// tokenKind はトークンの種類を表す。
type tokenKind int

const (
	tokEOF tokenKind = iota
	tokLParen
	tokRParen
	tokLBracket
	tokRBracket
	tokDot
	tokStar
	tokComma
	tokNot
	tokLt
	tokLe
	tokGt
	tokGe
	tokEq
	tokNe
	tokAnd
	tokOr
	tokNumber
	tokString
	tokIdent
)

// String はトークン種別の表示名を返す。エラーメッセージで使う。
func (k tokenKind) String() string {
	switch k {
	case tokEOF:
		return "式の終わり"
	case tokLParen:
		return "("
	case tokRParen:
		return ")"
	case tokLBracket:
		return "["
	case tokRBracket:
		return "]"
	case tokDot:
		return "."
	case tokStar:
		return "*"
	case tokComma:
		return ","
	case tokNot:
		return "!"
	case tokLt:
		return "<"
	case tokLe:
		return "<="
	case tokGt:
		return ">"
	case tokGe:
		return ">="
	case tokEq:
		return "=="
	case tokNe:
		return "!="
	case tokAnd:
		return "&&"
	case tokOr:
		return "||"
	case tokNumber:
		return "数値"
	case tokString:
		return "文字列"
	case tokIdent:
		return "識別子"
	}
	return "不明なトークン"
}

// token は字句解析の結果を表す。
type token struct {
	kind tokenKind
	// text は元の表記。エラーメッセージで使う。
	text string
	// num は数値リテラルの値。
	num float64
	// str は文字列リテラルのエスケープ解決後の値、または識別子の名前。
	str string
	// pos は式内の0始まりのバイト位置。
	pos int
}

// tokenize は式を字句解析する。
func tokenize(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]

		if isSpace(c) {
			i++
			continue
		}

		// 2文字の演算子を先に判定する。
		if i+1 < len(src) {
			switch src[i : i+2] {
			case "==":
				toks = append(toks, token{kind: tokEq, text: "==", pos: i})
				i += 2
				continue
			case "!=":
				toks = append(toks, token{kind: tokNe, text: "!=", pos: i})
				i += 2
				continue
			case "<=":
				toks = append(toks, token{kind: tokLe, text: "<=", pos: i})
				i += 2
				continue
			case ">=":
				toks = append(toks, token{kind: tokGe, text: ">=", pos: i})
				i += 2
				continue
			case "&&":
				toks = append(toks, token{kind: tokAnd, text: "&&", pos: i})
				i += 2
				continue
			case "||":
				toks = append(toks, token{kind: tokOr, text: "||", pos: i})
				i += 2
				continue
			}
		}

		switch c {
		case '(':
			toks = append(toks, token{kind: tokLParen, text: "(", pos: i})
			i++
			continue
		case ')':
			toks = append(toks, token{kind: tokRParen, text: ")", pos: i})
			i++
			continue
		case '[':
			toks = append(toks, token{kind: tokLBracket, text: "[", pos: i})
			i++
			continue
		case ']':
			toks = append(toks, token{kind: tokRBracket, text: "]", pos: i})
			i++
			continue
		case '.':
			toks = append(toks, token{kind: tokDot, text: ".", pos: i})
			i++
			continue
		case '*':
			toks = append(toks, token{kind: tokStar, text: "*", pos: i})
			i++
			continue
		case ',':
			toks = append(toks, token{kind: tokComma, text: ",", pos: i})
			i++
			continue
		case '!':
			toks = append(toks, token{kind: tokNot, text: "!", pos: i})
			i++
			continue
		case '<':
			toks = append(toks, token{kind: tokLt, text: "<", pos: i})
			i++
			continue
		case '>':
			toks = append(toks, token{kind: tokGt, text: ">", pos: i})
			i++
			continue
		case '\'':
			tok, next, err := lexString(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, tok)
			i = next
			continue
		case '"':
			return nil, fmt.Errorf("%d文字目: 式の中の文字列はシングルクォートで囲んでください（ダブルクォートは使えません）", i+1)
		case '=':
			return nil, fmt.Errorf("%d文字目: 単独の = は使えません（等価比較は == です）", i+1)
		case '&':
			return nil, fmt.Errorf("%d文字目: 単独の & は使えません（論理積は && です）", i+1)
		case '|':
			return nil, fmt.Errorf("%d文字目: 単独の | は使えません（論理和は || です）", i+1)
		}

		// 数値リテラル。単項マイナスは符号として数値の一部に取り込む。
		// 式言語に算術演算子は無いため、これで曖昧さは生じない。
		if isDigit(c) || (c == '-' && i+1 < len(src) && isDigit(src[i+1])) {
			tok, next, err := lexNumber(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, tok)
			i = next
			continue
		}

		if isIdentStart(c) {
			start := i
			i++
			for i < len(src) && isIdentPart(src[i]) {
				i++
			}
			name := src[start:i]
			toks = append(toks, token{kind: tokIdent, text: name, str: name, pos: start})
			continue
		}

		return nil, fmt.Errorf("%d文字目: 解釈できない文字 %q があります", i+1, string(c))
	}

	toks = append(toks, token{kind: tokEOF, text: "", pos: len(src)})
	return toks, nil
}

// lexString はシングルクォート文字列を読む。連続する2つのクォートは
// エスケープされた1文字のクォートとして扱う。
func lexString(src string, start int) (token, int, error) {
	var sb strings.Builder
	i := start + 1
	for i < len(src) {
		if src[i] != '\'' {
			sb.WriteByte(src[i])
			i++
			continue
		}
		// '' はエスケープされた ' を表す。
		if i+1 < len(src) && src[i+1] == '\'' {
			sb.WriteByte('\'')
			i += 2
			continue
		}
		return token{
			kind: tokString,
			text: src[start : i+1],
			str:  sb.String(),
			pos:  start,
		}, i + 1, nil
	}
	return token{}, 0, fmt.Errorf("%d文字目: 文字列が閉じられていません", start+1)
}

// lexNumber は数値リテラルを読む。10進・16進・8進と指数表記に対応する。
func lexNumber(src string, start int) (token, int, error) {
	i := start
	if src[i] == '-' {
		i++
	}

	// 0x / 0o / 0b の基数付き表記。
	if i+1 < len(src) && src[i] == '0' && isBasePrefix(src[i+1]) {
		i += 2
		for i < len(src) && isBaseDigit(src[i]) {
			i++
		}
		text := src[start:i]
		n, err := strconv.ParseInt(text, 0, 64)
		if err != nil {
			return token{}, 0, fmt.Errorf("%d文字目: 数値 %q を解釈できません", start+1, text)
		}
		return token{kind: tokNumber, text: text, num: float64(n), pos: start}, i, nil
	}

	for i < len(src) && isDigit(src[i]) {
		i++
	}
	// 小数部。ドットの直後が数字のときだけ取り込み、プロパティアクセスと区別する。
	if i+1 < len(src) && src[i] == '.' && isDigit(src[i+1]) {
		i++
		for i < len(src) && isDigit(src[i]) {
			i++
		}
	}
	// 指数部。
	if i < len(src) && (src[i] == 'e' || src[i] == 'E') {
		j := i + 1
		if j < len(src) && (src[j] == '+' || src[j] == '-') {
			j++
		}
		if j < len(src) && isDigit(src[j]) {
			i = j
			for i < len(src) && isDigit(src[i]) {
				i++
			}
		}
	}

	text := src[start:i]
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return token{}, 0, fmt.Errorf("%d文字目: 数値 %q を解釈できません", start+1, text)
	}
	return token{kind: tokNumber, text: text, num: f, pos: start}, i, nil
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isBasePrefix(c byte) bool {
	switch c {
	case 'x', 'X', 'o', 'O', 'b', 'B':
		return true
	}
	return false
}

func isBaseDigit(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') || c == '_'
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isIdentPart は識別子の2文字目以降に使える文字かを判定する。
// inputs.dry-run のようにプロパティ名にハイフンが現れるため、ハイフンも含める。
// 式言語に減算が無いので、これによる曖昧さは生じない。
func isIdentPart(c byte) bool {
	return isIdentStart(c) || isDigit(c) || c == '-'
}
