package expr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Kind は式の値の型を表す。
type Kind int

const (
	KindNull Kind = iota
	KindBool
	KindNumber
	KindString
	KindArray
	KindObject
)

// String は Kind の名前を返す。エラーメッセージで使う。
func (k Kind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindBool:
		return "boolean"
	case KindNumber:
		return "number"
	case KindString:
		return "string"
	case KindArray:
		return "array"
	case KindObject:
		return "object"
	}
	return "unknown"
}

// Value は式の評価結果を表す。
// GitHub Actions の式言語は null / boolean / number / string / array / object の
// 6種の値を持ち、比較時に独自の型変換規則を適用する。
type Value struct {
	kind Kind
	num  float64
	str  string
	b    bool
	arr  []Value
	obj  *Object
	// filtered は * によるオブジェクトフィルタの結果であることを表す。
	// フィルタ結果へのプロパティアクセスは各要素へ写像されるため、
	// 通常の配列と区別する必要がある。
	filtered bool

	// unknown は「実行しないと確定しない値」の由来を表す。
	// ドライランでは needs や steps の outputs が該当する。
	// 空でない場合、この値を使った判定は仮のものにすぎない。
	unknown string
}

// Unknown は実行しないと確定しない値を返す。
// label には needs.build.outputs.version のように参照元を示す文字列を渡す。
// 値としては null と同じように振る舞うため、既存の評価規則を変えずに扱える。
func Unknown(label string) Value {
	return Value{kind: KindNull, unknown: label}
}

// IsUnknown は実行しないと確定しない値かを返す。
func (v Value) IsUnknown() bool { return v.unknown != "" }

// UnknownLabel は不明値の由来を返す。不明値でない場合は空文字列。
func (v Value) UnknownLabel() string { return v.unknown }

// Null は null 値を返す。
func Null() Value { return Value{kind: KindNull} }

// Bool は真偽値を返す。
func Bool(b bool) Value { return Value{kind: KindBool, b: b} }

// Number は数値を返す。
func Number(f float64) Value { return Value{kind: KindNumber, num: f} }

// String は文字列値を返す。
func String(s string) Value { return Value{kind: KindString, str: s} }

// Array は配列値を返す。
func Array(items []Value) Value { return Value{kind: KindArray, arr: items} }

// Obj はオブジェクト値を返す。
func Obj(o *Object) Value {
	if o == nil {
		o = NewObject()
	}
	return Value{kind: KindObject, obj: o}
}

// Kind は値の型を返す。
func (v Value) Kind() Kind { return v.kind }

// IsNull は null かを返す。
func (v Value) IsNull() bool { return v.kind == KindNull }

// Items は配列の要素を返す。配列でない場合は nil を返す。
func (v Value) Items() []Value { return v.arr }

// Object は保持しているオブジェクトを返す。オブジェクトでない場合は nil を返す。
func (v Value) Object() *Object { return v.obj }

// Truthy は条件判定における真偽を返す。
// false / 0 / -0 / NaN / 空文字列 / null が偽で、それ以外は真。
// 配列とオブジェクトは中身が空でも真になる。
func (v Value) Truthy() bool {
	switch v.kind {
	case KindNull:
		return false
	case KindBool:
		return v.b
	case KindNumber:
		return v.num != 0 && !math.IsNaN(v.num)
	case KindString:
		return v.str != ""
	}
	return true
}

// AsNumber は数値への型変換を行う。変換できない場合は NaN を返す。
func (v Value) AsNumber() float64 {
	switch v.kind {
	case KindNull:
		return 0
	case KindBool:
		if v.b {
			return 1
		}
		return 0
	case KindNumber:
		return v.num
	case KindString:
		return parseNumber(v.str)
	}
	// 配列とオブジェクトは数値に変換できない。
	return math.NaN()
}

// AsString は文字列への型変換を行う。
func (v Value) AsString() string {
	switch v.kind {
	case KindNull:
		return ""
	case KindBool:
		if v.b {
			return "true"
		}
		return "false"
	case KindNumber:
		return formatNumber(v.num)
	case KindString:
		return v.str
	}
	// GitHub 本体は配列やオブジェクトの文字列化に対応せず空文字列を返すが、
	// ドライランでは中身が見えたほうが有用なので JSON 表現を返す。
	return v.JSON(false)
}

// parseNumber は文字列を数値に変換する。
// 空文字列と空白のみの文字列は 0、変換できない場合は NaN を返す。
func parseNumber(s string) float64 {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0
	}
	if f, err := strconv.ParseFloat(t, 64); err == nil {
		return f
	}
	// 0x1f や 0o17 のような表記は ParseFloat が受け付けないため個別に扱う。
	if i, err := strconv.ParseInt(t, 0, 64); err == nil {
		return float64(i)
	}
	return math.NaN()
}

// formatNumber は数値を GitHub Actions の表記に合わせて文字列にする。
func formatNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	// 整数として表せる範囲は小数点を付けずに出力する。
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// Equal は == の緩い等価比較を行う。
// 型が一致しない場合は双方を数値に変換して比較し、
// 文字列同士の比較では大文字小文字を区別しない。
// NaN はどの値とも等しくならない。
func Equal(a, b Value) bool {
	if a.kind == b.kind {
		switch a.kind {
		case KindNull:
			return true
		case KindBool:
			return a.b == b.b
		case KindNumber:
			return a.num == b.num
		case KindString:
			return strings.EqualFold(a.str, b.str)
		case KindArray:
			return equalArray(a.arr, b.arr)
		case KindObject:
			return equalObject(a.obj, b.obj)
		}
	}
	an, bn := a.AsNumber(), b.AsNumber()
	if math.IsNaN(an) || math.IsNaN(bn) {
		return false
	}
	return an == bn
}

// equalArray は配列同士を要素単位で比較する。
// GitHub 本体は参照で比較するが、ドライランでは構造の一致で近似する。
func equalArray(a, b []Value) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// equalObject はオブジェクト同士をキーと値で比較する。
func equalObject(a, b *Object) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Len() != b.Len() {
		return false
	}
	for _, k := range a.Keys() {
		av, _ := a.Get(k)
		bv, ok := b.Get(k)
		if !ok || !Equal(av, bv) {
			return false
		}
	}
	return true
}

// Compare は大小比較の結果を返す。
// 双方を数値に変換して比較するため、数値化できない値が含まれる場合は ok が false になる。
func Compare(a, b Value) (result int, ok bool) {
	an, bn := a.AsNumber(), b.AsNumber()
	if math.IsNaN(an) || math.IsNaN(bn) {
		return 0, false
	}
	switch {
	case an < bn:
		return -1, true
	case an > bn:
		return 1, true
	}
	return 0, true
}

// JSON は toJSON 相当の JSON 文字列を返す。
// pretty が真の場合はインデント付きで出力する。
func (v Value) JSON(pretty bool) string {
	var sb strings.Builder
	v.writeJSON(&sb, pretty, 0)
	return sb.String()
}

func (v Value) writeJSON(sb *strings.Builder, pretty bool, depth int) {
	switch v.kind {
	case KindNull:
		sb.WriteString("null")
	case KindBool:
		sb.WriteString(strconv.FormatBool(v.b))
	case KindNumber:
		// JSON に NaN や Infinity は書けないため null に落とす。
		if math.IsNaN(v.num) || math.IsInf(v.num, 0) {
			sb.WriteString("null")
			return
		}
		sb.WriteString(formatNumber(v.num))
	case KindString:
		sb.WriteString(quoteJSON(v.str))
	case KindArray:
		if len(v.arr) == 0 {
			sb.WriteString("[]")
			return
		}
		sb.WriteByte('[')
		for i, item := range v.arr {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeIndent(sb, pretty, depth+1)
			item.writeJSON(sb, pretty, depth+1)
		}
		writeIndent(sb, pretty, depth)
		sb.WriteByte(']')
	case KindObject:
		if v.obj == nil || v.obj.Len() == 0 {
			sb.WriteString("{}")
			return
		}
		sb.WriteByte('{')
		for i, k := range v.obj.Keys() {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeIndent(sb, pretty, depth+1)
			sb.WriteString(quoteJSON(k))
			sb.WriteByte(':')
			if pretty {
				sb.WriteByte(' ')
			}
			item, _ := v.obj.Get(k)
			item.writeJSON(sb, pretty, depth+1)
		}
		writeIndent(sb, pretty, depth)
		sb.WriteByte('}')
	}
}

// writeIndent は pretty 出力時のみ改行とインデントを書き込む。
func writeIndent(sb *strings.Builder, pretty bool, depth int) {
	if !pretty {
		return
	}
	sb.WriteByte('\n')
	sb.WriteString(strings.Repeat("  ", depth))
}

// quoteJSON は文字列を JSON の文字列リテラルに変換する。
func quoteJSON(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// GitHub の toJSON は < や & をエスケープしないため、HTML エスケープを切る。
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return strconv.Quote(s)
	}
	return strings.TrimRight(buf.String(), "\n")
}

// FromGo は Go の値を式の値に変換する。
// YAML や JSON をデコードした結果を式の世界へ持ち込むために使う。
func FromGo(v any) Value {
	switch x := v.(type) {
	case nil:
		return Null()
	case Value:
		return x
	case bool:
		return Bool(x)
	case string:
		return String(x)
	case int:
		return Number(float64(x))
	case int64:
		return Number(float64(x))
	case float64:
		return Number(x)
	case []Value:
		return Array(x)
	case []any:
		items := make([]Value, 0, len(x))
		for _, it := range x {
			items = append(items, FromGo(it))
		}
		return Array(items)
	case map[string]any:
		o := NewObject()
		for _, k := range sortedKeys(x) {
			o.Set(k, FromGo(x[k]))
		}
		return Obj(o)
	case map[string]string:
		o := NewObject()
		for _, k := range sortedKeys(x) {
			o.Set(k, String(x[k]))
		}
		return Obj(o)
	}
	// 想定外の型は文字列表現として取り込み、評価を止めない。
	return String(fmt.Sprint(v))
}

// sortedKeys はマップのキーを昇順で返す。
// Go のマップは反復順が不定なため、出力を安定させるために並べ替える。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Object は順序を保持したキーと値の集合。
// toJSON の出力順を定義順に合わせるため、挿入順を保持する。
type Object struct {
	keys   []string
	values map[string]Value

	// unknownPrefix が設定されている場合、未知のキーへのアクセスは
	// null ではなく不明値を返す。steps.<id>.outputs のように
	// キーを静的に知りえない箇所で使う。
	unknownPrefix string
}

// SetUnknownFallback は未知のキーを参照したときに不明値を返すようにする。
// prefix は不明値のラベルの前置き（steps.meta.outputs など）。
func (o *Object) SetUnknownFallback(prefix string) {
	o.unknownPrefix = prefix
}

// NewObject は空のオブジェクトを返す。
func NewObject() *Object {
	return &Object{values: map[string]Value{}}
}

// Set はキーに値を設定する。既存のキーは順序を保ったまま上書きする。
func (o *Object) Set(key string, v Value) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = v
}

// Get はキーに対応する値を返す。
// GitHub のコンテキスト参照は大文字小文字を区別しないため、
// 完全一致で見つからない場合は大文字小文字を無視して探す。
func (o *Object) Get(key string) (Value, bool) {
	if v, ok := o.values[key]; ok {
		return v, true
	}
	for _, k := range o.keys {
		if strings.EqualFold(k, key) {
			return o.values[k], true
		}
	}
	// 静的にキーを知りえない箇所では、未知のキーも
	// 「値が無い」ではなく「確定していない」として扱う。
	if o.unknownPrefix != "" {
		return Unknown(o.unknownPrefix + "." + key), true
	}
	return Null(), false
}

// Keys は挿入順のキー一覧を返す。
func (o *Object) Keys() []string { return o.keys }

// Len はキーの数を返す。
func (o *Object) Len() int { return len(o.keys) }

// Clone はオブジェクトの複製を返す。値そのものは共有する。
// matrix の直積を組み立てる際、組み合わせごとに独立したオブジェクトが必要になる。
func (o *Object) Clone() *Object {
	c := &Object{
		keys:          make([]string, len(o.keys)),
		values:        make(map[string]Value, len(o.values)),
		unknownPrefix: o.unknownPrefix,
	}
	copy(c.keys, o.keys)
	maps.Copy(c.values, o.values)
	return c
}

// Values は挿入順の値一覧を返す。オブジェクトフィルタで使う。
func (o *Object) Values() []Value {
	out := make([]Value, 0, len(o.keys))
	for _, k := range o.keys {
		out = append(out, o.values[k])
	}
	return out
}
