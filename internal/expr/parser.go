package expr

import "fmt"

// node は式の構文木のノードを表す。
type node interface {
	// pos は式内の0始まりのバイト位置を返す。エラー表示に使う。
	pos() int
}

// literalNode は数値・文字列・true・false・null のリテラル。
type literalNode struct {
	val Value
	p   int
}

// contextNode は github や matrix などのコンテキスト参照。
type contextNode struct {
	name string
	p    int
}

// propertyNode は . によるプロパティアクセス。
type propertyNode struct {
	obj  node
	name string
	p    int
}

// indexNode は [ ] によるインデックスアクセス。
type indexNode struct {
	obj   node
	index node
	p     int
}

// filterNode は * によるオブジェクトフィルタ。
// github.event.commits.*.message のように、配列やオブジェクトの
// 各要素から同じプロパティを集めて配列にする。
type filterNode struct {
	obj node
	p   int
}

// callNode は関数呼び出し。
type callNode struct {
	name string
	args []node
	p    int
}

// notNode は ! による否定。
type notNode struct {
	operand node
	p       int
}

// binaryNode は二項演算。
type binaryNode struct {
	op    tokenKind
	left  node
	right node
	p     int
}

func (n *literalNode) pos() int  { return n.p }
func (n *contextNode) pos() int  { return n.p }
func (n *propertyNode) pos() int { return n.p }
func (n *indexNode) pos() int    { return n.p }
func (n *filterNode) pos() int   { return n.p }
func (n *callNode) pos() int     { return n.p }
func (n *notNode) pos() int      { return n.p }
func (n *binaryNode) pos() int   { return n.p }

// Expression は解析済みの式を表す。
type Expression struct {
	src  string
	root node
}

// Source は元の式文字列を返す。
func (e *Expression) Source() string { return e.src }

// Compile は式を解析する。構文エラーはこの時点で検出される。
func Compile(src string) (*Expression, error) {
	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tokEOF {
		return nil, posErrorf(t.pos, "%s は余分です", t.kind)
	}
	return &Expression{src: src, root: root}, nil
}

// posErrorf は式内の位置を添えたエラーを作る。
func posErrorf(pos int, format string, args ...any) error {
	return fmt.Errorf("%d文字目: %s", pos+1, fmt.Sprintf(format, args...))
}

type parser struct {
	toks []token
	i    int
}

func (p *parser) peek() token { return p.toks[p.i] }

func (p *parser) next() token {
	t := p.toks[p.i]
	if p.i < len(p.toks)-1 {
		p.i++
	}
	return t
}

// accept は次のトークンが期待する種別なら読み進める。
func (p *parser) accept(k tokenKind) bool {
	if p.peek().kind == k {
		p.next()
		return true
	}
	return false
}

// expect は次のトークンが期待する種別であることを確かめて読み進める。
func (p *parser) expect(k tokenKind) (token, error) {
	t := p.peek()
	if t.kind != k {
		return token{}, posErrorf(t.pos, "%s を期待しましたが %s でした", k, t.kind)
	}
	return p.next(), nil
}

// parseOr は || を解析する。最も優先順位が低い。
func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokOr {
		t := p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &binaryNode{op: tokOr, left: left, right: right, p: t.pos}
	}
	return left, nil
}

// parseAnd は && を解析する。
func (p *parser) parseAnd() (node, error) {
	left, err := p.parseEquality()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokAnd {
		t := p.next()
		right, err := p.parseEquality()
		if err != nil {
			return nil, err
		}
		left = &binaryNode{op: tokAnd, left: left, right: right, p: t.pos}
	}
	return left, nil
}

// parseEquality は == と != を解析する。
func (p *parser) parseEquality() (node, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for {
		k := p.peek().kind
		if k != tokEq && k != tokNe {
			return left, nil
		}
		t := p.next()
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = &binaryNode{op: k, left: left, right: right, p: t.pos}
	}
}

// parseComparison は < <= > >= を解析する。
func (p *parser) parseComparison() (node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		k := p.peek().kind
		if k != tokLt && k != tokLe && k != tokGt && k != tokGe {
			return left, nil
		}
		t := p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &binaryNode{op: k, left: left, right: right, p: t.pos}
	}
}

// parseUnary は ! を解析する。比較演算子より優先順位が高い。
func (p *parser) parseUnary() (node, error) {
	if p.peek().kind == tokNot {
		t := p.next()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &notNode{operand: operand, p: t.pos}, nil
	}
	return p.parsePostfix()
}

// parsePostfix は . と [ ] の連鎖を解析する。
func (p *parser) parsePostfix() (node, error) {
	cur, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().kind {
		case tokDot:
			t := p.next()
			// .* はオブジェクトフィルタ。
			if p.accept(tokStar) {
				cur = &filterNode{obj: cur, p: t.pos}
				continue
			}
			name := p.peek()
			if name.kind != tokIdent {
				return nil, posErrorf(name.pos, ". の後にはプロパティ名が必要ですが %s でした", name.kind)
			}
			p.next()
			cur = &propertyNode{obj: cur, name: name.str, p: t.pos}

		case tokLBracket:
			t := p.next()
			// [*] も .* と同じくオブジェクトフィルタとして扱う。
			if p.accept(tokStar) {
				if _, err := p.expect(tokRBracket); err != nil {
					return nil, err
				}
				cur = &filterNode{obj: cur, p: t.pos}
				continue
			}
			idx, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(tokRBracket); err != nil {
				return nil, err
			}
			cur = &indexNode{obj: cur, index: idx, p: t.pos}

		default:
			return cur, nil
		}
	}
}

// parsePrimary はリテラル・コンテキスト参照・関数呼び出し・括弧を解析する。
func (p *parser) parsePrimary() (node, error) {
	t := p.peek()
	switch t.kind {
	case tokNumber:
		p.next()
		return &literalNode{val: Number(t.num), p: t.pos}, nil

	case tokString:
		p.next()
		return &literalNode{val: String(t.str), p: t.pos}, nil

	case tokLParen:
		p.next()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return inner, nil

	case tokIdent:
		p.next()
		// リテラルは小文字表記のみ。GitHub の式言語と同じ扱い。
		switch t.str {
		case "true":
			return &literalNode{val: Bool(true), p: t.pos}, nil
		case "false":
			return &literalNode{val: Bool(false), p: t.pos}, nil
		case "null":
			return &literalNode{val: Null(), p: t.pos}, nil
		}
		// 直後が ( なら関数呼び出し。
		if p.peek().kind == tokLParen {
			return p.parseCallArgs(t)
		}
		return &contextNode{name: t.str, p: t.pos}, nil

	case tokEOF:
		return nil, posErrorf(t.pos, "式が途中で終わっています")

	default:
		return nil, posErrorf(t.pos, "%s は値の位置には書けません", t.kind)
	}
}

// parseCallArgs は関数呼び出しの引数リストを解析する。
func (p *parser) parseCallArgs(name token) (node, error) {
	if _, err := p.expect(tokLParen); err != nil {
		return nil, err
	}
	call := &callNode{name: name.str, p: name.pos}
	if p.accept(tokRParen) {
		return call, nil
	}
	for {
		arg, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		call.args = append(call.args, arg)
		if p.accept(tokComma) {
			continue
		}
		if _, err := p.expect(tokRParen); err != nil {
			return nil, err
		}
		return call, nil
	}
}
