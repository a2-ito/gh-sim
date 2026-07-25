package workflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultDir はワークフローファイルの既定の配置場所。
const DefaultDir = ".github/workflows"

// Parse はワークフロー YAML を解釈する。path はエラーメッセージ用の表示名。
func Parse(src []byte, path string) (*Workflow, error) {
	var w Workflow
	if err := yaml.Unmarshal(src, &w); err != nil {
		return nil, fmt.Errorf("%s: YAML を解釈できません: %w", path, err)
	}
	w.Path = path
	if err := w.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &w, nil
}

// Load は単一のワークフローファイルを読み込む。
func Load(path string) (*Workflow, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ワークフローを読み込めません: %w", err)
	}
	return Parse(src, path)
}

// LoadDir はディレクトリ内のワークフローファイルをファイル名順に読み込む。
// 一部のファイルが不正でも残りは読み込み、エラーはまとめて返す。
// このため戻り値のワークフロー一覧とエラーは同時に非ゼロになりうる。
func LoadDir(dir string) ([]*Workflow, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("ワークフローディレクトリを読めません: %w", err)
	}

	var (
		out  []*Workflow
		errs []error
	)
	for _, e := range entries {
		if e.IsDir() || !isWorkflowFile(e.Name()) {
			continue
		}
		w, err := Load(filepath.Join(dir, e.Name()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, w)
	}
	if len(out) == 0 && len(errs) == 0 {
		return nil, fmt.Errorf("%s にワークフローファイル（*.yml / *.yaml）が見つかりません", dir)
	}
	return out, errors.Join(errs...)
}

// isWorkflowFile はワークフローとして読む対象の拡張子かを判定する。
func isWorkflowFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yml", ".yaml":
		return true
	}
	return false
}

// Validate はワークフロー定義の静的な整合性を検証する。
// needs の循環検出は依存グラフの構築時に行うため、ここでは扱わない。
func (w *Workflow) Validate() error {
	var errs []error

	if len(w.On.Events) == 0 {
		errs = append(errs, errors.New("on: が指定されていません"))
	}
	if len(w.Jobs.Order) == 0 {
		errs = append(errs, errors.New("jobs: にジョブが1つもありません"))
	}

	for _, job := range w.Jobs.List() {
		if err := job.validate(w); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// validate はジョブ単体の整合性を検証する。
func (j *Job) validate(w *Workflow) error {
	var errs []error
	label := fmt.Sprintf("jobs.%s (%d行目)", j.ID, j.Line)

	switch {
	case j.IsReusableCall() && len(j.Steps) > 0:
		errs = append(errs, fmt.Errorf("%s: uses と steps は同時に指定できません", label))
	case !j.IsReusableCall() && len(j.Steps) == 0:
		errs = append(errs, fmt.Errorf("%s: steps または uses のどちらかが必要です", label))
	}

	// 再利用可能ワークフローの呼び出しでは runs-on を書かない。
	if !j.IsReusableCall() && !j.RunsOn.Set {
		errs = append(errs, fmt.Errorf("%s: runs-on が指定されていません", label))
	}

	for _, need := range j.Needs {
		if need == j.ID {
			errs = append(errs, fmt.Errorf("%s: 自分自身を needs に指定しています", label))
			continue
		}
		if _, ok := w.Jobs.Items[need]; !ok {
			errs = append(errs, fmt.Errorf("%s: needs に存在しないジョブ %q が指定されています", label, need))
		}
	}

	seen := make(map[string]int, len(j.Steps))
	for i, s := range j.Steps {
		stepLabel := fmt.Sprintf("%s の steps[%d]", label, i)
		if s.Uses != "" && s.Run != "" {
			errs = append(errs, fmt.Errorf("%s: uses と run は同時に指定できません", stepLabel))
		}
		if s.Uses == "" && s.Run == "" {
			errs = append(errs, fmt.Errorf("%s: uses または run のどちらかが必要です", stepLabel))
		}
		if s.ID == "" {
			continue
		}
		if prev, dup := seen[s.ID]; dup {
			errs = append(errs, fmt.Errorf("%s: step の id %q が steps[%d] と重複しています", stepLabel, s.ID, prev))
			continue
		}
		seen[s.ID] = i
	}
	return errors.Join(errs...)
}
