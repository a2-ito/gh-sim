// gh-sim は GitHub Actions のワークフローをドライランするコマンド。
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/a2-ito/gh-sim/internal/expr"
	"github.com/a2-ito/gh-sim/internal/ghctx"
	"github.com/a2-ito/gh-sim/internal/plan"
	"github.com/a2-ito/gh-sim/internal/report"
	"github.com/a2-ito/gh-sim/internal/trigger"
	"github.com/a2-ito/gh-sim/internal/workflow"
)

// version はビルド時に -ldflags '-X main.version=...' で埋め込む。
var version = "dev"

const usage = `gh-sim - GitHub Actions のワークフローをドライランするツール

使い方:
  gh-sim <コマンド> [オプション]

コマンド:
  run       イベントを与えて、どのジョブが実行されるかを判定する
  eval      式を1つ評価する
  parse     ワークフローを読み込んで構造を表示する
  version   バージョンを表示する

例:
  gh-sim run -event push -ref main
  gh-sim run -event pull_request -base-ref main -changed src/a.go -steps
  gh-sim eval '${{ github.ref == "'"'"'refs/heads/main'"'"' }}'

各コマンドのオプションは gh-sim <コマンド> -h で確認できます。
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "エラー:", err)
		}
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errors.New("コマンドを指定してください")
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:], stdout)
	case "eval":
		return cmdEval(args[1:], stdout)
	case "parse":
		return cmdParse(args[1:], stdout)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "gh-sim %s\n", version)
		return nil
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("不明なコマンド %q", args[0])
	}
}

// keyValueFlag は key=value 形式で繰り返し指定できるフラグ。
type keyValueFlag map[string]string

func (f keyValueFlag) String() string { return "" }

func (f keyValueFlag) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("key=value の形式で指定してください（%q が指定されました）", s)
	}
	f[k] = v
	return nil
}

// listFlag はカンマ区切り、または繰り返しで複数の値を受けるフラグ。
type listFlag []string

func (f *listFlag) String() string { return strings.Join(*f, ",") }

func (f *listFlag) Set(s string) error {
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*f = append(*f, part)
		}
	}
	return nil
}

// eventFlags はイベントとコンテキストに関する共通のフラグ群。
type eventFlags struct {
	event   string
	action  string
	ref     string
	baseRef string
	changed listFlag
	payload string

	repo  string
	actor string
	sha   string

	inputs  keyValueFlag
	envs    keyValueFlag
	vars    keyValueFlag
	secrets keyValueFlag
	outputs keyValueFlag

	runnerOS string
	workDir  string
}

// register はフラグ集合にイベント関連のオプションを登録する。
func (e *eventFlags) register(fs *flag.FlagSet) {
	e.inputs = keyValueFlag{}
	e.envs = keyValueFlag{}
	e.vars = keyValueFlag{}
	e.secrets = keyValueFlag{}
	e.outputs = keyValueFlag{}

	fs.StringVar(&e.event, "event", "push", "シミュレートするイベント名")
	fs.StringVar(&e.action, "action", "", "アクティビティタイプ（opened、closed など）")
	fs.StringVar(&e.ref, "ref", "refs/heads/main", "対象の ref（refs/ を省略するとブランチ名として扱う）")
	fs.StringVar(&e.baseRef, "base-ref", "", "pull_request のマージ先ブランチ名")
	fs.Var(&e.changed, "changed", "変更されたファイル（カンマ区切り、繰り返し指定可）")
	fs.StringVar(&e.payload, "payload", "", "イベントペイロードの JSON ファイル")

	fs.StringVar(&e.repo, "repo", "", "owner/repo 形式のリポジトリ名")
	fs.StringVar(&e.actor, "actor", "", "イベントを起こしたユーザー名")
	fs.StringVar(&e.sha, "sha", "", "対象のコミット SHA")

	fs.Var(e.inputs, "input", "workflow_dispatch の入力（key=value、繰り返し指定可）")
	fs.Var(e.envs, "env", "環境変数（key=value、繰り返し指定可）")
	fs.Var(e.vars, "var", "リポジトリ変数（key=value、繰り返し指定可）")
	fs.Var(e.secrets, "secret", "シークレット（key=value、繰り返し指定可）")
	fs.Var(e.outputs, "output", "ジョブの出力（ジョブID.出力名=value、繰り返し指定可）\n"+
		"\t指定しない出力は不明値として扱われ、それに依存する条件は判定不能になります")

	fs.StringVar(&e.runnerOS, "runner-os", "", "runner.os の値（省略時は runs-on から推定）")
	fs.StringVar(&e.workDir, "workdir", ".", "hashFiles の基準ディレクトリ")
}

// config はフラグからドライランの入力条件を組み立てる。
func (e *eventFlags) config() (*ghctx.Config, error) {
	ev := &trigger.Event{
		Name:         e.event,
		Action:       e.action,
		Ref:          trigger.NormalizeRef(e.ref),
		BaseRef:      e.baseRef,
		ChangedPaths: e.changed,
	}

	if e.payload != "" {
		src, err := os.ReadFile(e.payload)
		if err != nil {
			return nil, fmt.Errorf("イベントペイロードを読めません: %w", err)
		}
		var m map[string]any
		if err := json.Unmarshal(src, &m); err != nil {
			return nil, fmt.Errorf("イベントペイロードを JSON として解釈できません: %w", err)
		}
		ev.Payload = m
	}

	return &ghctx.Config{
		Event:      ev,
		Repository: e.repo,
		Actor:      e.actor,
		SHA:        e.sha,
		Inputs:     e.inputs,
		Env:        e.envs,
		Vars:       e.vars,
		Secrets:    e.secrets,
		Outputs:    e.outputs,
		RunnerOS:   e.runnerOS,
		WorkDir:    e.workDir,
	}, nil
}

func cmdRun(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(out)

	dir := fs.String("dir", workflow.DefaultDir, "読み込むワークフローディレクトリ")
	file := fs.String("file", "", "単一のワークフローファイルを読み込む（-dir より優先）")
	showSteps := fs.Bool("steps", false, "ステップの一覧も表示する")
	showNotes := fs.Bool("notes", true, "判定を保留した項目などの注記を表示する")
	showAll := fs.Bool("all", false, "起動しないワークフローも表示する")
	noColor := fs.Bool("no-color", false, "色付けを無効にする")
	asJSON := fs.Bool("json", false, "結果を JSON で出力する")

	var ef eventFlags
	ef.register(fs)

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := ef.config()
	if err != nil {
		return err
	}

	workflows, loadErr := loadWorkflows(*dir, *file)
	if len(workflows) == 0 && loadErr != nil {
		return loadErr
	}

	plans := make([]*plan.Plan, 0, len(workflows))
	for _, w := range workflows {
		// ワークフローごとに独立した条件で判定するため設定を複製する。
		c := *cfg
		p, err := plan.Build(w, &c)
		if err != nil {
			return fmt.Errorf("%s: %w", w.Path, err)
		}
		plans = append(plans, p)
	}

	if *asJSON {
		if err := report.WriteJSON(out, plans); err != nil {
			return err
		}
	} else {
		opts := report.Options{
			ShowSteps:        *showSteps,
			ShowNotes:        *showNotes,
			ShowNotTriggered: *showAll,
			Color:            !*noColor && isTerminal(out),
		}
		if err := report.Write(out, plans, opts); err != nil {
			return err
		}
	}

	// 読み込めなかったファイルがあった場合は、結果を出したうえで報告する。
	return loadErr
}

func cmdEval(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(out)

	file := fs.String("file", "", "コンテキストの組み立てに使うワークフローファイル")
	asJSON := fs.Bool("json", false, "結果を JSON で出力する")

	var ef eventFlags
	ef.register(fs)

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("評価する式を1つ指定してください（例: gh-sim eval 'github.ref'）")
	}

	cfg, err := ef.config()
	if err != nil {
		return err
	}

	w, err := evalWorkflow(*file)
	if err != nil {
		return err
	}
	ctx, _ := ghctx.Base(w, cfg)

	// ${{ }} の有無どちらでも書けるよう、if と同じ扱いで解析する。
	tmpl, err := expr.CompileCondition(positional[0])
	if err != nil {
		return err
	}
	v, err := tmpl.EvalValue(ctx)
	if err != nil {
		return err
	}

	if *asJSON {
		fmt.Fprintln(out, v.JSON(false))
		return nil
	}
	fmt.Fprintf(out, "%s\t(%s, 条件としては %s)\n", v.AsString(), v.Kind(), truthyLabel(v.Truthy()))
	return nil
}

// evalWorkflow は eval のコンテキスト作成に使うワークフローを用意する。
// ファイルが指定されない場合は最小限のものを合成する。
func evalWorkflow(path string) (*workflow.Workflow, error) {
	if path != "" {
		return workflow.Load(path)
	}
	const src = `
on: push
jobs:
  eval:
    runs-on: ubuntu-latest
    steps:
      - run: 'true'
`
	return workflow.Parse([]byte(src), "(コンテキスト用の仮ワークフロー)")
}

// truthyLabel は条件として評価した場合の真偽を日本語で返す。
func truthyLabel(b bool) string {
	if b {
		return "真"
	}
	return "偽"
}

func cmdParse(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("parse", flag.ContinueOnError)
	fs.SetOutput(out)
	dir := fs.String("dir", workflow.DefaultDir, "読み込むワークフローディレクトリ")
	file := fs.String("file", "", "単一のワークフローファイルを読み込む（-dir より優先）")
	if err := fs.Parse(args); err != nil {
		return err
	}

	workflows, loadErr := loadWorkflows(*dir, *file)
	if len(workflows) == 0 && loadErr != nil {
		return loadErr
	}

	for i, w := range workflows {
		if i > 0 {
			fmt.Fprintln(out)
		}
		printWorkflow(out, w)
	}
	return loadErr
}

// loadWorkflows はファイル指定があれば単一ファイル、無ければディレクトリを読み込む。
func loadWorkflows(dir, file string) ([]*workflow.Workflow, error) {
	if file != "" {
		w, err := workflow.Load(file)
		if err != nil {
			return nil, err
		}
		return []*workflow.Workflow{w}, nil
	}
	return workflow.LoadDir(dir)
}

// printWorkflow はワークフローの構造を1件分表示する。
func printWorkflow(out io.Writer, w *workflow.Workflow) {
	fmt.Fprintf(out, "%s\n", w.Path)
	if w.Name != "" {
		fmt.Fprintf(out, "  name: %s\n", w.Name)
	}
	fmt.Fprintf(out, "  on:   %s\n", strings.Join(w.On.Names(), ", "))
	fmt.Fprintln(out, "  jobs:")

	for _, job := range w.Jobs.List() {
		var attrs []string
		if job.IsReusableCall() {
			attrs = append(attrs, "uses: "+job.Uses)
		} else {
			attrs = append(attrs, "runs-on: "+job.RunsOn.String())
			attrs = append(attrs, fmt.Sprintf("steps: %d", len(job.Steps)))
		}
		if len(job.Needs) > 0 {
			attrs = append(attrs, "needs: "+strings.Join(job.Needs, ", "))
		}
		if job.Strategy != nil && job.Strategy.Matrix != nil {
			m := job.Strategy.Matrix
			axes := m.Expr
			if axes == "" {
				axes = strings.Join(m.Order, " × ")
			}
			attrs = append(attrs, "matrix: "+axes)
		}
		if job.If.Set {
			attrs = append(attrs, "if: "+job.If.Raw)
		}
		fmt.Fprintf(out, "    %-12s %s\n", job.ID, strings.Join(attrs, " · "))
	}
}

// parseInterspersed はフラグと位置引数が混在していても解釈できるようにする。
// 標準の flag は最初の位置引数で解釈を止めてしまうため、
// 位置引数を取り出しながら残りを繰り返し解釈する。
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// isTerminal は出力先が端末かを判定する。色付けの既定値に使う。
func isTerminal(out io.Writer) bool {
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
