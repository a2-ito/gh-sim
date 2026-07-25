package workflow

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// kindName は yaml.Kind を日本語のエラーメッセージ向けに表現する。
func kindName(k yaml.Kind) string {
	switch k {
	case yaml.DocumentNode:
		return "ドキュメント"
	case yaml.SequenceNode:
		return "配列"
	case yaml.MappingNode:
		return "マップ"
	case yaml.ScalarNode:
		return "スカラー"
	case yaml.AliasNode:
		return "エイリアス"
	}
	return "不明なノード"
}

// isNull は明示的な null ノード（`on: push:` のような空の値）かを判定する。
func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// StringList は string と []string のどちらでも書けるフィールドを表す。
// needs, branches, types などが該当する。
type StringList []string

func (s *StringList) UnmarshalYAML(n *yaml.Node) error {
	switch {
	case isNull(n):
		*s = nil
		return nil
	case n.Kind == yaml.ScalarNode:
		*s = StringList{n.Value}
		return nil
	case n.Kind == yaml.SequenceNode:
		out := make(StringList, 0, len(n.Content))
		for _, c := range n.Content {
			if c.Kind != yaml.ScalarNode {
				return fmt.Errorf("%d行目: 配列の要素は文字列である必要がありますが %sでした", c.Line, kindName(c.Kind))
			}
			out = append(out, c.Value)
		}
		*s = out
		return nil
	default:
		return fmt.Errorf("%d行目: 文字列または文字列の配列を期待しましたが %sでした", n.Line, kindName(n.Kind))
	}
}

// Scalar はリテラル値と ${{ }} 式のどちらも取りうるフィールドを表す。
// timeout-minutes: 10 と timeout-minutes: ${{ inputs.t }} を同じ型で保持し、
// 式の解決は評価フェーズに委ねる。
type Scalar struct {
	// Raw は YAML 上の表記をそのまま文字列にしたもの。
	Raw string
	// Set はフィールドが明示的に書かれていたかを表す。
	Set bool
	// Line は YAML 上の行番号。
	Line int
}

func (s *Scalar) UnmarshalYAML(n *yaml.Node) error {
	if isNull(n) {
		return nil
	}
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("%d行目: 単一の値を期待しましたが %sでした", n.Line, kindName(n.Kind))
	}
	s.Raw = n.Value
	s.Set = true
	s.Line = n.Line
	return nil
}

// HasExpr は値が ${{ }} を含むかを返す。
func (s Scalar) HasExpr() bool { return strings.Contains(s.Raw, "${{") }

// Bool をリテラルの真偽値として解釈する。式を含む場合は ok が false になる。
func (s Scalar) Bool() (val bool, ok bool) {
	if !s.Set || s.HasExpr() {
		return false, false
	}
	switch strings.ToLower(s.Raw) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// Int をリテラルの整数として解釈する。式を含む場合は ok が false になる。
func (s Scalar) Int() (val int, ok bool) {
	if !s.Set || s.HasExpr() {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(s.Raw))
	if err != nil {
		return 0, false
	}
	return v, true
}

// StringMap は値に数値や真偽値を書いても文字列として取り込むマップを表す。
// env: や with: は YAML 上で数値を書けるが、Actions 上では文字列として扱われる。
type StringMap map[string]string

func (m *StringMap) UnmarshalYAML(n *yaml.Node) error {
	if isNull(n) {
		*m = nil
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%d行目: マップを期待しましたが %sでした", n.Line, kindName(n.Kind))
	}
	out := make(StringMap, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i]
		val := n.Content[i+1]
		if key.Kind != yaml.ScalarNode {
			return fmt.Errorf("%d行目: キーは文字列である必要がありますが %sでした", key.Line, kindName(key.Kind))
		}
		if val.Kind == yaml.ScalarNode {
			out[key.Value] = val.Value
			continue
		}
		// 配列やマップを値に置くのは Actions 上では不正だが、ドライランを
		// 止めないよう YAML として再直列化した文字列で保持しておく。
		b, err := yaml.Marshal(val)
		if err != nil {
			return fmt.Errorf("%d行目: %s の値を解釈できません: %w", val.Line, key.Value, err)
		}
		out[key.Value] = strings.TrimRight(string(b), "\n")
	}
	*m = out
	return nil
}

// Permissions は permissions: の "read-all" / "write-all" 形式と、
// スコープごとに指定するマップ形式の双方を表す。
type Permissions struct {
	// All は "read-all" / "write-all" が指定された場合の値。
	All string
	// Scopes は contents: read のようなスコープ単位の指定。
	Scopes map[string]string
	// Set はフィールドが明示的に書かれていたかを表す。空マップ（全権限剥奪）と未指定を区別する。
	Set bool
}

func (p *Permissions) UnmarshalYAML(n *yaml.Node) error {
	p.Set = true
	switch {
	case isNull(n):
		// permissions: だけ書いた場合は全スコープ none 相当。
		p.Scopes = map[string]string{}
		return nil
	case n.Kind == yaml.ScalarNode:
		p.All = n.Value
		return nil
	case n.Kind == yaml.MappingNode:
		var m StringMap
		if err := m.UnmarshalYAML(n); err != nil {
			return err
		}
		p.Scopes = m
		return nil
	default:
		return fmt.Errorf("%d行目: permissions は文字列またはマップである必要がありますが %sでした", n.Line, kindName(n.Kind))
	}
}
