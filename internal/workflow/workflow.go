package workflow

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Workflow は1つのワークフローファイルを表す。
type Workflow struct {
	Name        string       `yaml:"name"`
	RunName     Scalar       `yaml:"run-name"`
	On          On           `yaml:"on"`
	Env         StringMap    `yaml:"env"`
	Defaults    *Defaults    `yaml:"defaults"`
	Concurrency *Concurrency `yaml:"concurrency"`
	Permissions Permissions  `yaml:"permissions"`
	Jobs        Jobs         `yaml:"jobs"`

	// Path はワークフローファイルのパス。YAML には現れない。
	Path string `yaml:"-"`
}

// DisplayName は表示用の名前を返す。name: が無い場合はファイルパスで代替する。
func (w *Workflow) DisplayName() string {
	if w.Name != "" {
		return w.Name
	}
	return w.Path
}

// Jobs は jobs: を定義順を保ったまま保持する。
// マップとして読むと順序が失われ、出力の並びが実行ごとに変わってしまうため。
type Jobs struct {
	// Order は job ID の定義順。
	Order []string
	// Items は job ID から Job への対応。
	Items map[string]*Job
}

// List は Job を定義順で返す。
func (j Jobs) List() []*Job {
	out := make([]*Job, 0, len(j.Order))
	for _, id := range j.Order {
		out = append(out, j.Items[id])
	}
	return out
}

func (j *Jobs) UnmarshalYAML(n *yaml.Node) error {
	if isNull(n) {
		return fmt.Errorf("%d行目: jobs: が空です", n.Line)
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%d行目: jobs はマップである必要がありますが %sでした", n.Line, kindName(n.Kind))
	}
	j.Order = make([]string, 0, len(n.Content)/2)
	j.Items = make(map[string]*Job, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i]
		if key.Kind != yaml.ScalarNode {
			return fmt.Errorf("%d行目: job ID は文字列である必要がありますが %sでした", key.Line, kindName(key.Kind))
		}
		if _, dup := j.Items[key.Value]; dup {
			return fmt.Errorf("%d行目: job %q が重複しています", key.Line, key.Value)
		}
		// ID と Line は yaml:"-" なので Decode で上書きされない。
		job := &Job{ID: key.Value, Line: key.Line}
		if err := n.Content[i+1].Decode(job); err != nil {
			return fmt.Errorf("jobs.%s: %w", key.Value, err)
		}
		j.Order = append(j.Order, key.Value)
		j.Items[key.Value] = job
	}
	return nil
}

// Job は1つのジョブ定義を表す。
type Job struct {
	// ID は jobs: 直下のキー。
	ID string `yaml:"-"`
	// Line は YAML 上の行番号。
	Line int `yaml:"-"`

	Name        string       `yaml:"name"`
	Needs       StringList   `yaml:"needs"`
	If          Scalar       `yaml:"if"`
	RunsOn      RunsOn       `yaml:"runs-on"`
	Environment *Environment `yaml:"environment"`
	Concurrency *Concurrency `yaml:"concurrency"`
	Outputs     StringMap    `yaml:"outputs"`
	Env         StringMap    `yaml:"env"`
	Defaults    *Defaults    `yaml:"defaults"`
	Steps       []*Step      `yaml:"steps"`

	TimeoutMinutes  Scalar `yaml:"timeout-minutes"`
	ContinueOnError Scalar `yaml:"continue-on-error"`

	Strategy    *Strategy             `yaml:"strategy"`
	Container   *Container            `yaml:"container"`
	Services    map[string]*Container `yaml:"services"`
	Permissions Permissions           `yaml:"permissions"`

	// Uses は再利用可能ワークフローの呼び出し先。設定されている場合 Steps は使えない。
	Uses    string    `yaml:"uses"`
	With    StringMap `yaml:"with"`
	Secrets Secrets   `yaml:"secrets"`
}

// DisplayName は表示用の名前を返す。name: が無い場合は job ID で代替する。
func (j *Job) DisplayName() string {
	if j.Name != "" {
		return j.Name
	}
	return j.ID
}

// IsReusableCall は再利用可能ワークフローの呼び出しかを返す。
func (j *Job) IsReusableCall() bool { return j.Uses != "" }

// Step は1つのステップ定義を表す。
type Step struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	If   Scalar `yaml:"if"`

	Uses string `yaml:"uses"`
	Run  string `yaml:"run"`

	Shell            string    `yaml:"shell"`
	WorkingDirectory string    `yaml:"working-directory"`
	With             StringMap `yaml:"with"`
	Env              StringMap `yaml:"env"`

	TimeoutMinutes  Scalar `yaml:"timeout-minutes"`
	ContinueOnError Scalar `yaml:"continue-on-error"`
}

// DisplayName は表示用の名前を返す。
// name: が無い場合は Actions の表示に倣い uses / run から導出する。
func (s *Step) DisplayName() string {
	switch {
	case s.Name != "":
		return s.Name
	case s.Uses != "":
		return "Run " + s.Uses
	case s.Run != "":
		// run: が複数行の場合、Actions は1行目のみを表示する。
		line := strings.TrimSpace(s.Run)
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		return "Run " + line
	}
	return "(名前なし)"
}

// RunsOn は runs-on: の string / []string / {group, labels} 形式を表す。
type RunsOn struct {
	// Labels はランナーラベルの一覧。
	Labels []string
	// Group はランナーグループ名。
	Group string
	// Set はフィールドが明示的に書かれていたかを表す。
	Set bool
}

func (r *RunsOn) UnmarshalYAML(n *yaml.Node) error {
	r.Set = true
	switch {
	case isNull(n):
		r.Set = false
		return nil
	case n.Kind == yaml.ScalarNode:
		r.Labels = []string{n.Value}
		return nil
	case n.Kind == yaml.SequenceNode:
		var v StringList
		if err := v.UnmarshalYAML(n); err != nil {
			return err
		}
		r.Labels = v
		return nil
	case n.Kind == yaml.MappingNode:
		var v struct {
			Group  string     `yaml:"group"`
			Labels StringList `yaml:"labels"`
		}
		if err := n.Decode(&v); err != nil {
			return err
		}
		r.Group = v.Group
		r.Labels = v.Labels
		return nil
	default:
		return fmt.Errorf("%d行目: runs-on を解釈できません（%s）", n.Line, kindName(n.Kind))
	}
}

// String は表示用のランナー表現を返す。
func (r RunsOn) String() string {
	switch {
	case r.Group != "" && len(r.Labels) > 0:
		return fmt.Sprintf("%s [%s]", r.Group, strings.Join(r.Labels, ", "))
	case r.Group != "":
		return r.Group
	case len(r.Labels) > 0:
		return strings.Join(r.Labels, ", ")
	}
	return "(未指定)"
}

// Strategy は strategy: を表す。
type Strategy struct {
	Matrix      *Matrix `yaml:"matrix"`
	FailFast    Scalar  `yaml:"fail-fast"`
	MaxParallel Scalar  `yaml:"max-parallel"`
}

// Matrix は strategy.matrix を表す。軸は定義順を保持する。
type Matrix struct {
	// Order は軸名の定義順。include / exclude は含まない。
	Order []string
	// Axes は軸名から取りうる値の一覧への対応。
	Axes map[string][]any
	// AxisExprs は軸の値が式で与えられた場合の生文字列（node: ${{ fromJSON(...) }} など）。
	AxisExprs map[string]string

	Include []OrderedMap
	Exclude []OrderedMap

	// Expr は matrix 全体が式で与えられた場合の生文字列。
	Expr string
}

func (m *Matrix) UnmarshalYAML(n *yaml.Node) error {
	if isNull(n) {
		return nil
	}
	// matrix: ${{ fromJSON(needs.setup.outputs.matrix) }} のような全体指定。
	if n.Kind == yaml.ScalarNode {
		m.Expr = n.Value
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%d行目: matrix はマップである必要がありますが %sでした", n.Line, kindName(n.Kind))
	}

	m.Axes = map[string][]any{}
	m.AxisExprs = map[string]string{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i]
		val := n.Content[i+1]
		switch key.Value {
		case "include":
			list, err := decodeMapList(val, "include")
			if err != nil {
				return err
			}
			m.Include = list
		case "exclude":
			list, err := decodeMapList(val, "exclude")
			if err != nil {
				return err
			}
			m.Exclude = list
		default:
			switch val.Kind {
			case yaml.SequenceNode:
				var vs []any
				if err := val.Decode(&vs); err != nil {
					return fmt.Errorf("matrix.%s: %w", key.Value, err)
				}
				m.Axes[key.Value] = vs
				m.Order = append(m.Order, key.Value)
			case yaml.ScalarNode:
				// 軸の値を式で与えるケース。展開は評価フェーズで行う。
				m.AxisExprs[key.Value] = val.Value
				m.Order = append(m.Order, key.Value)
			default:
				return fmt.Errorf("%d行目: matrix.%s は配列である必要がありますが %sでした", val.Line, key.Value, kindName(val.Kind))
			}
		}
	}
	return nil
}

// decodeMapList は matrix の include / exclude を読む。
func decodeMapList(n *yaml.Node, field string) ([]OrderedMap, error) {
	if isNull(n) {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%d行目: matrix.%s はマップの配列である必要がありますが %sでした", n.Line, field, kindName(n.Kind))
	}
	out := make([]OrderedMap, 0, len(n.Content))
	for _, c := range n.Content {
		if c.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%d行目: matrix.%s の要素はマップである必要がありますが %sでした", c.Line, field, kindName(c.Kind))
		}
		var m OrderedMap
		if err := m.UnmarshalYAML(c); err != nil {
			return nil, fmt.Errorf("matrix.%s: %w", field, err)
		}
		out = append(out, m)
	}
	return out, nil
}

// OrderedMap は YAML のマップをキーの定義順を保ったまま保持する。
// matrix の include / exclude ではキーの順序が展開結果の表示順に影響するため、
// 通常のマップとして読むと出力の並びが崩れてしまう。
type OrderedMap struct {
	// Keys はキーの定義順。
	Keys []string
	// Values はキーから値への対応。
	Values map[string]any
}

func (m *OrderedMap) UnmarshalYAML(n *yaml.Node) error {
	if isNull(n) {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%d行目: マップを期待しましたが %sでした", n.Line, kindName(n.Kind))
	}
	m.Values = make(map[string]any, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i]
		if key.Kind != yaml.ScalarNode {
			return fmt.Errorf("%d行目: キーは文字列である必要がありますが %sでした", key.Line, kindName(key.Kind))
		}
		var v any
		if err := n.Content[i+1].Decode(&v); err != nil {
			return fmt.Errorf("%d行目: %s の値を解釈できません: %w", n.Content[i+1].Line, key.Value, err)
		}
		if _, dup := m.Values[key.Value]; !dup {
			m.Keys = append(m.Keys, key.Value)
		}
		m.Values[key.Value] = v
	}
	return nil
}

// Container は container: / services: の定義を表す。
// container: node:18 のような文字列形式も受け付ける。
type Container struct {
	Image       string    `yaml:"image"`
	Credentials StringMap `yaml:"credentials"`
	Env         StringMap `yaml:"env"`
	Ports       []string  `yaml:"ports"`
	Volumes     []string  `yaml:"volumes"`
	Options     string    `yaml:"options"`
}

func (c *Container) UnmarshalYAML(n *yaml.Node) error {
	if isNull(n) {
		return nil
	}
	if n.Kind == yaml.ScalarNode {
		c.Image = n.Value
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%d行目: container は文字列またはマップである必要がありますが %sでした", n.Line, kindName(n.Kind))
	}
	// 同じ型で再帰しないよう別型に読み替える。
	type containerBody Container
	var body containerBody
	if err := n.Decode(&body); err != nil {
		return err
	}
	*c = Container(body)
	return nil
}

// Environment は environment: の string / {name, url} 形式を表す。
type Environment struct {
	Name string
	URL  string
}

func (e *Environment) UnmarshalYAML(n *yaml.Node) error {
	if isNull(n) {
		return nil
	}
	if n.Kind == yaml.ScalarNode {
		e.Name = n.Value
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%d行目: environment は文字列またはマップである必要がありますが %sでした", n.Line, kindName(n.Kind))
	}
	var body struct {
		Name string `yaml:"name"`
		URL  string `yaml:"url"`
	}
	if err := n.Decode(&body); err != nil {
		return err
	}
	e.Name = body.Name
	e.URL = body.URL
	return nil
}

// Defaults は defaults: を表す。
type Defaults struct {
	Run *RunDefaults `yaml:"run"`
}

// RunDefaults は defaults.run: を表す。
type RunDefaults struct {
	Shell            string `yaml:"shell"`
	WorkingDirectory string `yaml:"working-directory"`
}

// Concurrency は concurrency: の string / {group, cancel-in-progress} 形式を表す。
type Concurrency struct {
	Group            string
	CancelInProgress Scalar
}

func (c *Concurrency) UnmarshalYAML(n *yaml.Node) error {
	if isNull(n) {
		return nil
	}
	if n.Kind == yaml.ScalarNode {
		c.Group = n.Value
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%d行目: concurrency は文字列またはマップである必要がありますが %sでした", n.Line, kindName(n.Kind))
	}
	var body struct {
		Group            string `yaml:"group"`
		CancelInProgress Scalar `yaml:"cancel-in-progress"`
	}
	if err := n.Decode(&body); err != nil {
		return err
	}
	c.Group = body.Group
	c.CancelInProgress = body.CancelInProgress
	return nil
}

// Secrets は再利用可能ワークフロー呼び出しの secrets: を表す。
// secrets: inherit という文字列形式と、個別に渡すマップ形式がある。
type Secrets struct {
	Inherit bool
	Items   StringMap
	Set     bool
}

func (s *Secrets) UnmarshalYAML(n *yaml.Node) error {
	if isNull(n) {
		return nil
	}
	s.Set = true
	if n.Kind == yaml.ScalarNode {
		if n.Value != "inherit" {
			return fmt.Errorf("%d行目: secrets に指定できる文字列は inherit のみです（%q が指定されました）", n.Line, n.Value)
		}
		s.Inherit = true
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%d行目: secrets は inherit またはマップである必要がありますが %sでした", n.Line, kindName(n.Kind))
	}
	var m StringMap
	if err := m.UnmarshalYAML(n); err != nil {
		return err
	}
	s.Items = m
	return nil
}
