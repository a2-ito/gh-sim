package workflow

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// On はワークフローのトリガー定義を表す。
// on: push / on: [push, pull_request] / on: {push: {branches: [main]}} の
// 3形態すべてを Event の並びに正規化する。
type On struct {
	Events []*Event
}

// Find は指定名のイベント定義を返す。定義が無ければ nil を返す。
func (o On) Find(name string) *Event {
	for _, e := range o.Events {
		if e.Name == name {
			return e
		}
	}
	return nil
}

// Names は定義されたイベント名を定義順で返す。
func (o On) Names() []string {
	out := make([]string, 0, len(o.Events))
	for _, e := range o.Events {
		out = append(out, e.Name)
	}
	return out
}

// Event は単一のトリガーイベントとそのフィルタを表す。
// フィルタはイベント種別によって使えるものが異なるが、
// ドライランでは種別ごとの適用可否を照合フェーズで判断するため、
// ここでは書かれたものをそのまま保持する。
type Event struct {
	// Name は "push" や "pull_request" などのイベント名。
	Name string

	// Types は types: フィルタ（pull_request の opened など）。
	Types []string

	Branches       []string
	BranchesIgnore []string
	Tags           []string
	TagsIgnore     []string
	Paths          []string
	PathsIgnore    []string

	// Cron は schedule: に列挙された cron 式。
	Cron []string

	// Inputs は workflow_dispatch / workflow_call の入力定義。
	Inputs map[string]*Input

	// InputOrder は Inputs の定義順。
	InputOrder []string

	// Workflows は workflow_run の workflows: フィルタ。
	Workflows []string

	// Line は YAML 上の行番号。
	Line int
}

// eventFilters は Event のフィルタ群を YAML から読むための中間表現。
type eventFilters struct {
	Types          StringList `yaml:"types"`
	Branches       StringList `yaml:"branches"`
	BranchesIgnore StringList `yaml:"branches-ignore"`
	Tags           StringList `yaml:"tags"`
	TagsIgnore     StringList `yaml:"tags-ignore"`
	Paths          StringList `yaml:"paths"`
	PathsIgnore    StringList `yaml:"paths-ignore"`
	Workflows      StringList `yaml:"workflows"`
}

// Input は workflow_dispatch / workflow_call の入力定義を表す。
type Input struct {
	Description string   `yaml:"description"`
	Required    bool     `yaml:"required"`
	Default     Scalar   `yaml:"default"`
	Type        string   `yaml:"type"`
	Options     []string `yaml:"options"`
}

func (o *On) UnmarshalYAML(n *yaml.Node) error {
	switch {
	case isNull(n):
		return fmt.Errorf("%d行目: on: が空です。トリガーを1つ以上指定してください", n.Line)

	case n.Kind == yaml.ScalarNode:
		o.Events = []*Event{{Name: n.Value, Line: n.Line}}
		return nil

	case n.Kind == yaml.SequenceNode:
		for _, c := range n.Content {
			if c.Kind != yaml.ScalarNode {
				return fmt.Errorf("%d行目: on: の配列形式ではイベント名のみ書けます（フィルタを付けるにはマップ形式を使ってください）", c.Line)
			}
			o.Events = append(o.Events, &Event{Name: c.Value, Line: c.Line})
		}
		return nil

	case n.Kind == yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]
			val := n.Content[i+1]
			if key.Kind != yaml.ScalarNode {
				return fmt.Errorf("%d行目: イベント名は文字列である必要がありますが %sでした", key.Line, kindName(key.Kind))
			}
			ev := &Event{Name: key.Value, Line: key.Line}
			if err := ev.decodeBody(val); err != nil {
				return fmt.Errorf("on.%s: %w", ev.Name, err)
			}
			o.Events = append(o.Events, ev)
		}
		return nil

	default:
		return fmt.Errorf("%d行目: on: を解釈できません（%s）", n.Line, kindName(n.Kind))
	}
}

// decodeBody は on.<event> の値部分を読む。
func (e *Event) decodeBody(n *yaml.Node) error {
	// `on: { push: }` のようにフィルタ無しで書かれた場合。
	if isNull(n) {
		return nil
	}

	// schedule だけは配列で cron を列挙する特殊な形をしている。
	if e.Name == "schedule" {
		if n.Kind != yaml.SequenceNode {
			return fmt.Errorf("%d行目: schedule は cron を並べた配列である必要がありますが %sでした", n.Line, kindName(n.Kind))
		}
		for _, c := range n.Content {
			var entry struct {
				Cron string `yaml:"cron"`
			}
			if err := c.Decode(&entry); err != nil {
				return fmt.Errorf("%d行目: cron の指定を解釈できません: %w", c.Line, err)
			}
			if entry.Cron == "" {
				return fmt.Errorf("%d行目: cron が空です", c.Line)
			}
			e.Cron = append(e.Cron, entry.Cron)
		}
		return nil
	}

	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%d行目: フィルタはマップである必要がありますが %sでした", n.Line, kindName(n.Kind))
	}

	var f eventFilters
	if err := n.Decode(&f); err != nil {
		return err
	}
	e.Types = f.Types
	e.Branches = f.Branches
	e.BranchesIgnore = f.BranchesIgnore
	e.Tags = f.Tags
	e.TagsIgnore = f.TagsIgnore
	e.Paths = f.Paths
	e.PathsIgnore = f.PathsIgnore
	e.Workflows = f.Workflows

	// inputs は定義順を保ちたいので個別に読む。
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value != "inputs" {
			continue
		}
		inputs := n.Content[i+1]
		if isNull(inputs) {
			break
		}
		if inputs.Kind != yaml.MappingNode {
			return fmt.Errorf("%d行目: inputs はマップである必要がありますが %sでした", inputs.Line, kindName(inputs.Kind))
		}
		e.Inputs = make(map[string]*Input, len(inputs.Content)/2)
		for j := 0; j+1 < len(inputs.Content); j += 2 {
			name := inputs.Content[j].Value
			in := &Input{}
			if err := inputs.Content[j+1].Decode(in); err != nil {
				return fmt.Errorf("inputs.%s: %w", name, err)
			}
			e.Inputs[name] = in
			e.InputOrder = append(e.InputOrder, name)
		}
		break
	}

	// branches と branches-ignore の併記は GitHub 側でエラーになる。
	if len(e.Branches) > 0 && len(e.BranchesIgnore) > 0 {
		return fmt.Errorf("%d行目: branches と branches-ignore は同時に指定できません", n.Line)
	}
	if len(e.Tags) > 0 && len(e.TagsIgnore) > 0 {
		return fmt.Errorf("%d行目: tags と tags-ignore は同時に指定できません", n.Line)
	}
	if len(e.Paths) > 0 && len(e.PathsIgnore) > 0 {
		return fmt.Errorf("%d行目: paths と paths-ignore は同時に指定できません", n.Line)
	}
	return nil
}
