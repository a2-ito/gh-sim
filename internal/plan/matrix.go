package plan

import (
	"fmt"
	"strings"

	"github.com/a2-ito/gh-sim/internal/expr"
	"github.com/a2-ito/gh-sim/internal/workflow"
)

// Combination は matrix の1つの組み合わせを表す。
type Combination struct {
	// Values は軸名から値への対応。matrix コンテキストとして式評価に渡す。
	Values *expr.Object
}

// String は組み合わせを表示用の文字列にする。
// Actions のジョブ名表示に合わせて値のみをカンマ区切りで並べる。
func (c *Combination) String() string {
	parts := make([]string, 0, c.Values.Len())
	for _, k := range c.Values.Keys() {
		v, _ := c.Values.Get(k)
		parts = append(parts, v.AsString())
	}
	return strings.Join(parts, ", ")
}

// Expansion は matrix 展開の結果を表す。
type Expansion struct {
	// Combinations は展開された組み合わせ。
	// matrix が無いジョブでは長さ1の「空の組み合わせ」になる。
	Combinations []*Combination

	// Axes は軸名の定義順。
	Axes []string

	// Notes は展開時の補足。式を解決できなかった場合などに入る。
	Notes []string
}

// axis は matrix の1軸を表す。
type axis struct {
	name   string
	values []expr.Value
}

// resolved は式を解決した後の matrix 定義。
type resolved struct {
	axes     []axis
	includes []*expr.Object
	excludes []*expr.Object
	notes    []string
}

// Expand は strategy.matrix を展開する。
// matrix が無い場合は組み合わせ1件（空）の結果を返すため、
// 呼び出し側は matrix の有無で分岐せずに扱える。
func Expand(m *workflow.Matrix, ctx *expr.Context) (*Expansion, error) {
	if m == nil {
		return &Expansion{Combinations: []*Combination{{Values: expr.NewObject()}}}, nil
	}

	r, err := resolveMatrix(m, ctx)
	if err != nil {
		return nil, err
	}

	exp := &Expansion{Notes: r.notes}
	for _, ax := range r.axes {
		exp.Axes = append(exp.Axes, ax.name)
	}

	// 軸が1つも解決できなかった場合は展開できないので、
	// matrix なしと同じ扱いにして注記を残す。
	if len(r.axes) == 0 && len(r.includes) == 0 {
		exp.Combinations = []*Combination{{Values: expr.NewObject()}}
		if len(exp.Notes) == 0 {
			exp.Notes = append(exp.Notes, "matrix に軸がないため展開していません")
		}
		return exp, nil
	}

	// 軸が無く include だけがある場合、include の各要素がそのまま
	// 独立した組み合わせになる。空の組み合わせに順に重ねてはいけない。
	if len(r.axes) == 0 {
		combos := make([]*Combination, 0, len(r.includes))
		for _, inc := range r.includes {
			combos = append(combos, &Combination{Values: inc.Clone()})
		}
		exp.Combinations = applyExclude(combos, r.excludes)
		return exp, nil
	}

	combos := cartesian(r.axes)

	// GitHub は exclude を先に処理し、そのあとで include を適用する。
	// これにより include で除外済みの組み合わせを復活させられる。
	combos = applyExclude(combos, r.excludes)

	axisNames := make(map[string]bool, len(r.axes))
	for _, ax := range r.axes {
		axisNames[ax.name] = true
	}
	combos = applyInclude(combos, r.includes, axisNames)

	exp.Combinations = combos
	return exp, nil
}

// resolveMatrix は matrix 定義の式を解決し、軸と include / exclude を取り出す。
func resolveMatrix(m *workflow.Matrix, ctx *expr.Context) (*resolved, error) {
	// matrix 全体が式で与えられている場合。
	if m.Expr != "" {
		return resolveMatrixExpr(m.Expr, ctx)
	}

	r := &resolved{}
	for _, name := range m.Order {
		// 軸の値が式で与えられている場合は評価して配列にする。
		if src, ok := m.AxisExprs[name]; ok {
			values, note, err := resolveAxisExpr(name, src, ctx)
			if err != nil {
				return nil, err
			}
			if note != "" {
				r.notes = append(r.notes, note)
				continue
			}
			r.axes = append(r.axes, axis{name: name, values: values})
			continue
		}
		raw := m.Axes[name]
		if len(raw) == 0 {
			r.notes = append(r.notes, fmt.Sprintf("matrix.%s に値がありません", name))
			continue
		}
		values := make([]expr.Value, 0, len(raw))
		for _, v := range raw {
			values = append(values, expr.FromGo(v))
		}
		r.axes = append(r.axes, axis{name: name, values: values})
	}

	r.includes = toObjects(m.Include)
	r.excludes = toObjects(m.Exclude)
	return r, nil
}

// resolveMatrixExpr は matrix 全体が式の場合に評価して軸を取り出す。
func resolveMatrixExpr(src string, ctx *expr.Context) (*resolved, error) {
	tmpl, err := expr.CompileTemplate(src)
	if err != nil {
		return nil, fmt.Errorf("matrix の式を解析できません: %w", err)
	}
	v, err := tmpl.EvalValue(ctx)
	if err != nil {
		// 評価できない場合は展開を諦め、注記を残して先へ進む。
		return &resolved{notes: []string{fmt.Sprintf("matrix の式 %s を評価できませんでした: %v", src, err)}}, nil
	}
	if v.Kind() != expr.KindObject {
		return &resolved{notes: []string{
			fmt.Sprintf("matrix の式 %s の評価結果が %s なので展開できません", src, v.Kind()),
		}}, nil
	}

	r := &resolved{}
	obj := v.Object()
	for _, k := range obj.Keys() {
		item, _ := obj.Get(k)
		switch k {
		case "include":
			r.includes = valuesToObjects(item)
		case "exclude":
			r.excludes = valuesToObjects(item)
		default:
			if item.Kind() != expr.KindArray {
				r.notes = append(r.notes, fmt.Sprintf("matrix.%s が配列ではないため展開できません", k))
				continue
			}
			r.axes = append(r.axes, axis{name: k, values: item.Items()})
		}
	}
	return r, nil
}

// resolveAxisExpr は軸の値が式で与えられた場合に評価して配列にする。
// 評価できない、または配列にならない場合は note を返して軸を諦める。
func resolveAxisExpr(name, src string, ctx *expr.Context) (values []expr.Value, note string, err error) {
	tmpl, err := expr.CompileTemplate(src)
	if err != nil {
		return nil, "", fmt.Errorf("matrix.%s の式を解析できません: %w", name, err)
	}
	v, err := tmpl.EvalValue(ctx)
	if err != nil {
		return nil, fmt.Sprintf("matrix.%s の式を評価できませんでした: %v", name, err), nil
	}
	if v.Kind() != expr.KindArray {
		return nil, fmt.Sprintf("matrix.%s の式の評価結果が %s なので展開できません", name, v.Kind()), nil
	}
	return v.Items(), "", nil
}

// toObjects は YAML 由来のマップ一覧をオブジェクト一覧に変換する。
// キーの定義順は展開結果の表示順に影響するため保持する。
func toObjects(list []workflow.OrderedMap) []*expr.Object {
	if len(list) == 0 {
		return nil
	}
	out := make([]*expr.Object, 0, len(list))
	for _, m := range list {
		o := expr.NewObject()
		for _, k := range m.Keys {
			o.Set(k, expr.FromGo(m.Values[k]))
		}
		out = append(out, o)
	}
	return out
}

// valuesToObjects は式の評価結果からオブジェクト一覧を取り出す。
func valuesToObjects(v expr.Value) []*expr.Object {
	if v.Kind() != expr.KindArray {
		return nil
	}
	out := make([]*expr.Object, 0, len(v.Items()))
	for _, item := range v.Items() {
		if item.Kind() != expr.KindObject {
			continue
		}
		out = append(out, item.Object())
	}
	return out
}

// cartesian は軸の直積を作る。
// 最初に定義された軸が最も外側になり、最後の軸が最も速く変化する。
// この順序は GitHub が生成するジョブの並びに合わせている。
func cartesian(axes []axis) []*Combination {
	combos := []*Combination{{Values: expr.NewObject()}}
	for _, ax := range axes {
		next := make([]*Combination, 0, len(combos)*len(ax.values))
		for _, c := range combos {
			for _, v := range ax.values {
				o := c.Values.Clone()
				o.Set(ax.name, v)
				next = append(next, &Combination{Values: o})
			}
		}
		combos = next
	}
	return combos
}

// applyExclude は exclude に一致する組み合わせを取り除く。
// exclude のキーがすべて一致する組み合わせが対象になる。
func applyExclude(combos []*Combination, excludes []*expr.Object) []*Combination {
	if len(excludes) == 0 {
		return combos
	}
	out := make([]*Combination, 0, len(combos))
	for _, c := range combos {
		if matchesAny(c, excludes) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// matchesAny は組み合わせが exclude のいずれかに完全に該当するかを返す。
func matchesAny(c *Combination, excludes []*expr.Object) bool {
	for _, ex := range excludes {
		matched := true
		for _, k := range ex.Keys() {
			want, _ := ex.Get(k)
			got, ok := c.Values.Get(k)
			if !ok || !expr.Equal(got, want) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// applyInclude は include を適用する。
//
// GitHub の規則は次のとおり。
//   - include の各要素は、元の軸のキーについて値が一致する組み合わせに対して、
//     残りのキーを追加する。元の軸の値は上書きしない。
//   - include で追加された値は、後続の include で上書きされうる。
//   - どの組み合わせにも当てはまらない場合は新しい組み合わせとして追加する。
func applyInclude(combos []*Combination, includes []*expr.Object, axisNames map[string]bool) []*Combination {
	for _, inc := range includes {
		applied := false
		for _, c := range combos {
			if !includeApplies(c, inc, axisNames) {
				continue
			}
			applied = true
			for _, k := range inc.Keys() {
				// 元の軸のキーは条件として使うだけで、値は書き換えない。
				if axisNames[k] {
					continue
				}
				v, _ := inc.Get(k)
				c.Values.Set(k, v)
			}
		}
		if !applied {
			combos = append(combos, &Combination{Values: inc.Clone()})
		}
	}
	return combos
}

// includeApplies は include の要素を組み合わせに適用できるかを判定する。
// 元の軸に含まれるキーの値がすべて一致する必要がある。
func includeApplies(c *Combination, inc *expr.Object, axisNames map[string]bool) bool {
	for _, k := range inc.Keys() {
		if !axisNames[k] {
			continue
		}
		want, _ := inc.Get(k)
		got, ok := c.Values.Get(k)
		if !ok || !expr.Equal(got, want) {
			return false
		}
	}
	return true
}
