package expr

import (
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
)

// Status はステータス関数（success() など）の評価に使う状態を表す。
type Status int

const (
	// StatusSuccess は直前までの処理がすべて成功した状態。ドライランの既定値。
	StatusSuccess Status = iota
	// StatusFailure は先行する処理が失敗した状態。
	StatusFailure
	// StatusCancelled は実行が中断された状態。
	StatusCancelled
	// StatusSkipped は先行する処理が実行されなかった状態。
	// success() も failure() も偽になり、always() だけが真になる。
	StatusSkipped
)

// String は状態の名前を返す。
func (s Status) String() string {
	switch s {
	case StatusSuccess:
		return "success"
	case StatusFailure:
		return "failure"
	case StatusCancelled:
		return "cancelled"
	case StatusSkipped:
		return "skipped"
	}
	return "unknown"
}

// Context は式の評価環境を表す。
type Context struct {
	// Vars はコンテキスト名から値への対応。github / env / matrix / needs などを入れる。
	// ここに無い名前を式が参照するとエラーになるため、
	// 利用を許すコンテキストは値が空でも登録しておく。
	Vars map[string]Value

	// Status はステータス関数の評価に使う状態。
	Status Status

	// HashFiles は hashFiles() の実装。
	// nil の場合は hashFiles() の呼び出しをエラーとして扱う。
	HashFiles func(patterns []string) (string, error)

	// Unknowns は評価中に参照された不明値の由来を、重複を除いて記録する。
	// 中身がある場合、評価結果は仮のものにすぎない。
	// 記録は評価ごとに独立させたいため Clone では引き継がない。
	Unknowns []string
}

// NewContext は空のコンテキストを返す。
func NewContext() *Context {
	return &Context{Vars: map[string]Value{}}
}

// Set はコンテキストを登録する。
func (c *Context) Set(name string, v Value) *Context {
	if c.Vars == nil {
		c.Vars = map[string]Value{}
	}
	c.Vars[name] = v
	return c
}

// Clone はコンテキストを複製する。
// ジョブや matrix の組み合わせごとに一部のコンテキストだけを
// 差し替えて評価するために使う。
// 不明値の記録は評価ごとの情報なので引き継がない。
func (c *Context) Clone() *Context {
	n := &Context{
		Vars:      make(map[string]Value, len(c.Vars)),
		Status:    c.Status,
		HashFiles: c.HashFiles,
	}
	maps.Copy(n.Vars, c.Vars)
	return n
}

// noteUnknown は不明値が参照されたことを記録する。
func (c *Context) noteUnknown(label string) {
	if label == "" || slices.Contains(c.Unknowns, label) {
		return
	}
	c.Unknowns = append(c.Unknowns, label)
}

// lookup はコンテキスト名を解決する。名前の大文字小文字は区別しない。
func (c *Context) lookup(name string) (Value, bool) {
	if v, ok := c.Vars[name]; ok {
		return v, true
	}
	for k, v := range c.Vars {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return Null(), false
}

// availableNames は利用できるコンテキスト名を昇順で返す。エラーメッセージで使う。
func (c *Context) availableNames() []string {
	names := make([]string, 0, len(c.Vars))
	for k := range c.Vars {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Eval は式を評価する。
func (e *Expression) Eval(ctx *Context) (Value, error) {
	if ctx == nil {
		ctx = NewContext()
	}
	return evalNode(e.root, ctx)
}

// Evaluate は式を解析して評価する。同じ式を繰り返し評価する場合は
// Compile で解析結果を再利用するほうがよい。
func Evaluate(src string, ctx *Context) (Value, error) {
	e, err := Compile(src)
	if err != nil {
		return Null(), err
	}
	return e.Eval(ctx)
}

// evalNode はノードを評価する。
// 不明値が現れたことを漏らさず記録するため、
// 実際の評価は evalNodeValue に委ね、結果をここで検査する。
func evalNode(n node, ctx *Context) (Value, error) {
	v, err := evalNodeValue(n, ctx)
	if err == nil && v.IsUnknown() {
		ctx.noteUnknown(v.UnknownLabel())
	}
	return v, err
}

func evalNodeValue(n node, ctx *Context) (Value, error) {
	switch x := n.(type) {
	case *literalNode:
		return x.val, nil

	case *contextNode:
		v, ok := ctx.lookup(x.name)
		if !ok {
			return Null(), posErrorf(x.p, "コンテキスト %q は参照できません（使えるのは %s）",
				x.name, strings.Join(ctx.availableNames(), ", "))
		}
		return v, nil

	case *propertyNode:
		obj, err := evalNode(x.obj, ctx)
		if err != nil {
			return Null(), err
		}
		return property(obj, x.name), nil

	case *indexNode:
		obj, err := evalNode(x.obj, ctx)
		if err != nil {
			return Null(), err
		}
		idx, err := evalNode(x.index, ctx)
		if err != nil {
			return Null(), err
		}
		return indexValue(obj, idx), nil

	case *filterNode:
		obj, err := evalNode(x.obj, ctx)
		if err != nil {
			return Null(), err
		}
		return filterValue(obj), nil

	case *notNode:
		v, err := evalNode(x.operand, ctx)
		if err != nil {
			return Null(), err
		}
		return Bool(!v.Truthy()), nil

	case *binaryNode:
		return evalBinary(x, ctx)

	case *callNode:
		return evalCall(x, ctx)
	}
	return Null(), posErrorf(n.pos(), "評価できない式です")
}

// evalBinary は二項演算を評価する。
func evalBinary(n *binaryNode, ctx *Context) (Value, error) {
	// && と || は短絡評価を行い、真偽値ではなく被演算子の値そのものを返す。
	// これにより ${{ cond && 'a' || 'b' }} が三項演算子のように書ける。
	switch n.op {
	case tokAnd:
		left, err := evalNode(n.left, ctx)
		if err != nil {
			return Null(), err
		}
		if !left.Truthy() {
			return left, nil
		}
		return evalNode(n.right, ctx)

	case tokOr:
		left, err := evalNode(n.left, ctx)
		if err != nil {
			return Null(), err
		}
		if left.Truthy() {
			return left, nil
		}
		return evalNode(n.right, ctx)
	}

	left, err := evalNode(n.left, ctx)
	if err != nil {
		return Null(), err
	}
	right, err := evalNode(n.right, ctx)
	if err != nil {
		return Null(), err
	}

	switch n.op {
	case tokEq:
		return Bool(Equal(left, right)), nil
	case tokNe:
		return Bool(!Equal(left, right)), nil
	}

	cmp, ok := Compare(left, right)
	if !ok {
		// 数値に変換できない値が混ざる比較は常に false になる。
		return Bool(false), nil
	}
	switch n.op {
	case tokLt:
		return Bool(cmp < 0), nil
	case tokLe:
		return Bool(cmp <= 0), nil
	case tokGt:
		return Bool(cmp > 0), nil
	case tokGe:
		return Bool(cmp >= 0), nil
	}
	return Null(), posErrorf(n.p, "未対応の演算子 %s です", n.op)
}

// property はプロパティアクセスを評価する。
// 存在しないプロパティやオブジェクト以外へのアクセスは null になる。
func property(v Value, name string) Value {
	// フィルタ結果に対するアクセスは各要素へ写像する。
	// github.event.commits.*.message のような形を成り立たせるための扱い。
	if v.kind == KindArray && v.filtered {
		out := make([]Value, 0, len(v.arr))
		for _, item := range v.arr {
			if item.kind != KindObject || item.obj == nil {
				continue
			}
			// 対象のプロパティを持たない要素は結果から除かれる。
			if got, ok := item.obj.Get(name); ok {
				out = append(out, got)
			}
		}
		return filteredArray(out)
	}
	if v.kind == KindObject && v.obj != nil {
		got, _ := v.obj.Get(name)
		return got
	}
	return Null()
}

// indexValue はインデックスアクセスを評価する。範囲外や型不一致は null になる。
func indexValue(obj, idx Value) Value {
	switch obj.kind {
	case KindArray:
		n := idx.AsNumber()
		if math.IsNaN(n) {
			return Null()
		}
		i := int(n)
		if i < 0 || i >= len(obj.arr) {
			return Null()
		}
		return obj.arr[i]
	case KindObject:
		if obj.obj == nil {
			return Null()
		}
		got, _ := obj.obj.Get(idx.AsString())
		return got
	}
	return Null()
}

// filterValue は * によるオブジェクトフィルタを評価する。
// 配列はその要素、オブジェクトはその値の一覧を対象にする。
func filterValue(v Value) Value {
	switch v.kind {
	case KindArray:
		return filteredArray(v.arr)
	case KindObject:
		if v.obj == nil {
			return filteredArray(nil)
		}
		return filteredArray(v.obj.Values())
	}
	return filteredArray(nil)
}

// filteredArray はフィルタ結果としての配列を返す。
func filteredArray(items []Value) Value {
	return Value{kind: KindArray, arr: items, filtered: true}
}
