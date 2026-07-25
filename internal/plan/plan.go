package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/a2-ito/gh-sim/internal/expr"
	"github.com/a2-ito/gh-sim/internal/ghctx"
	"github.com/a2-ito/gh-sim/internal/trigger"
	"github.com/a2-ito/gh-sim/internal/workflow"
)

// Status はジョブやステップの実行見込みを表す。
type Status int

const (
	// StatusRun は実行される見込み。
	StatusRun Status = iota
	// StatusSkip は条件により実行されない。
	StatusSkip
	// StatusUnknown は条件を評価できず判定できない。
	StatusUnknown
)

// String は状態の名前を返す。
func (s Status) String() string {
	switch s {
	case StatusRun:
		return "run"
	case StatusSkip:
		return "skip"
	case StatusUnknown:
		return "unknown"
	}
	return "invalid"
}

// Plan はワークフロー1つのドライラン結果を表す。
type Plan struct {
	// Workflow は対象のワークフロー。
	Workflow *workflow.Workflow

	// Trigger はトリガー照合の結果。
	Trigger *trigger.Result

	// Graph は依存グラフ。起動しない場合は nil。
	Graph *Graph

	// Jobs はワークフローの定義順に並んだジョブの判定結果。
	Jobs []*JobPlan

	// Notes は判定を保留した項目などの補足。
	Notes []string
}

// Triggered はワークフローが起動するかを返す。
func (p *Plan) Triggered() bool {
	return p.Trigger != nil && p.Trigger.Triggered
}

// JobPlan はジョブ1つの判定結果を表す。
type JobPlan struct {
	// Job は対象のジョブ定義。
	Job *workflow.Job

	// Layer は依存グラフ上のレイヤ番号。
	Layer int

	// Axes は matrix の軸名。matrix が無い場合は空。
	Axes []string

	// Instances は matrix 展開後のジョブ。matrix が無い場合も1件入る。
	Instances []*JobInstance

	// Notes はこのジョブに関する補足。
	Notes []string
}

// Status はジョブ全体の実行見込みを返す。
// matrix の組み合わせのいずれかが実行されるなら実行と見なす。
func (j *JobPlan) Status() Status {
	unknown := false
	for _, inst := range j.Instances {
		switch inst.Status {
		case StatusRun:
			return StatusRun
		case StatusUnknown:
			unknown = true
		}
	}
	if unknown {
		return StatusUnknown
	}
	return StatusSkip
}

// JobInstance は matrix 展開後のジョブ1件を表す。
type JobInstance struct {
	// Name は表示名。matrix がある場合は組み合わせを含む。
	Name string

	// Matrix は組み合わせ。matrix が無い場合は空。
	Matrix *Combination

	// Status は実行見込み。
	Status Status

	// Reason は実行されない理由、または判定できない理由。
	Reason string

	// RunsOn は式を解決したあとのランナー表記。
	RunsOn string

	// Steps はステップの判定結果。
	Steps []*StepPlan
}

// StepPlan はステップ1つの判定結果を表す。
type StepPlan struct {
	// Index はジョブ内でのステップの位置。
	Index int
	// Name は表示名。
	Name string
	// Status は実行見込み。
	Status Status
	// Reason は実行されない理由、または判定できない理由。
	Reason string
}

// Build はワークフローのドライラン計画を作る。
func Build(w *workflow.Workflow, cfg *ghctx.Config) (*Plan, error) {
	cfg.Normalize()

	p := &Plan{Workflow: w}

	res, err := trigger.Match(w, cfg.Event)
	if err != nil {
		return nil, err
	}
	p.Trigger = res
	p.Notes = append(p.Notes, res.Notes...)
	if !res.Triggered {
		return p, nil
	}

	g, err := BuildGraph(w)
	if err != nil {
		return nil, err
	}
	p.Graph = g

	base, notes := ghctx.Base(w, cfg)
	p.Notes = append(p.Notes, notes...)

	// needs の結果を後続へ渡すため、トポロジカル順に評価する。
	results := make(map[string]expr.Value, len(g.Order))
	statuses := make(map[string]Status, len(g.Order))
	plans := make(map[string]*JobPlan, len(g.Order))

	for _, id := range g.Order {
		jp, err := buildJob(w.Jobs.Items[id], g, base, cfg, results, statuses)
		if err != nil {
			return nil, err
		}
		plans[id] = jp
		statuses[id] = jp.Status()
		results[id] = needsEntry(jp, cfg)
	}

	// 出力は定義順に並べる。
	for _, id := range w.Jobs.Order {
		p.Jobs = append(p.Jobs, plans[id])
	}
	return p, nil
}

// buildJob はジョブ1つの判定を行う。
func buildJob(
	job *workflow.Job,
	g *Graph,
	base *expr.Context,
	cfg *ghctx.Config,
	results map[string]expr.Value,
	statuses map[string]Status,
) (*JobPlan, error) {
	jp := &JobPlan{Job: job, Layer: g.LayerOf(job.ID)}

	// needs コンテキストを組み立て、実行されない依存と
	// 判定が確定しない依存をそれぞれ集める。
	needsObj := expr.NewObject()
	var depSkipped, depUnknown []string
	for _, n := range g.Needs[job.ID] {
		if v, ok := results[n]; ok {
			needsObj.Set(n, v)
		}
		switch statuses[n] {
		case StatusSkip:
			depSkipped = append(depSkipped, n)
		case StatusUnknown:
			depUnknown = append(depUnknown, n)
		}
	}

	ctx := base.Clone()
	ctx.Set("needs", expr.Obj(needsObj))

	// 依存ジョブが実行されない場合、暗黙の success() は偽になる。
	// always() を書いたジョブだけが実行される状態を表す。
	if len(depSkipped) > 0 {
		ctx.Status = expr.StatusSkipped
	}

	// github.job を差し替える。
	if gh, ok := ctx.Vars["github"]; ok && gh.Kind() == expr.KindObject {
		o := gh.Object().Clone()
		o.Set("job", expr.String(job.ID))
		ctx.Set("github", expr.Obj(o))
	}

	// ジョブの env をワークフローの env に重ねる。
	jobEnv, envNotes := ghctx.EvalEnv(envOf(ctx), job.Env, ctx)
	ctx.Set("env", expr.Obj(jobEnv))
	jp.Notes = append(jp.Notes, envNotes...)

	// matrix の展開には matrix コンテキスト自身を使えないため、
	// この時点のコンテキストで評価する。
	exp, err := Expand(matrixOf(job), ctx)
	if err != nil {
		return nil, fmt.Errorf("jobs.%s: %w", job.ID, err)
	}
	jp.Axes = exp.Axes
	jp.Notes = append(jp.Notes, exp.Notes...)

	for _, combo := range exp.Combinations {
		inst := buildInstance(job, combo, ctx, cfg, depSkipped, depUnknown)
		jp.Instances = append(jp.Instances, inst)
	}
	return jp, nil
}

// buildInstance は matrix の組み合わせ1件分の判定を行う。
func buildInstance(
	job *workflow.Job,
	combo *Combination,
	jobCtx *expr.Context,
	cfg *ghctx.Config,
	depSkipped []string,
	depUnknown []string,
) *JobInstance {
	ctx := jobCtx.Clone()
	if combo.Values.Len() > 0 {
		ctx.Set("matrix", expr.Obj(combo.Values))
	}

	inst := &JobInstance{Matrix: combo}

	// runs-on を解決し、ラベルからランナーの OS を推定する。
	runsOn, labels := resolveRunsOn(job.RunsOn, ctx)
	inst.RunsOn = runsOn
	if cfg.RunnerOS == "" {
		if os := ghctx.RunnerOSFromLabels(labels); os != "" {
			ctx = ghctx.WithRunner(ctx, os)
		}
	}

	inst.Name = instanceName(job, combo, ctx)
	inst.Status, inst.Reason = evalCondition(job.If, ctx, depSkipped)

	// 依存ジョブの判定が確定していない場合、このジョブの判定も確定しない。
	// 実行される見込みだった場合だけ引き下げる（すでに skip と判定された
	// 条件は依存の結果によらず偽なので、そのまま扱う）。
	if inst.Status == StatusRun && len(depUnknown) > 0 {
		inst.Status = StatusUnknown
		inst.Reason = fmt.Sprintf("依存ジョブ %s の判定が確定しないため", strings.Join(depUnknown, ", "))
	}

	// 実行されないジョブのステップは判定しても意味がないため、
	// 一覧だけを同じ状態で並べる。
	if inst.Status == StatusSkip {
		for i, s := range job.Steps {
			inst.Steps = append(inst.Steps, &StepPlan{
				Index:  i,
				Name:   s.DisplayName(),
				Status: StatusSkip,
				Reason: "ジョブが実行されないため",
			})
		}
		return inst
	}

	inst.Steps = buildSteps(job, ctx)
	return inst
}

// buildSteps はステップの判定を行う。
func buildSteps(job *workflow.Job, jobCtx *expr.Context) []*StepPlan {
	out := make([]*StepPlan, 0, len(job.Steps))

	// steps コンテキストは先行ステップの結果を持つが、
	// ドライランでは outputs の値が分からないため空で登録する。
	steps := expr.NewObject()
	baseEnv := envOf(jobCtx)

	for i, s := range job.Steps {
		ctx := jobCtx.Clone()
		ctx.Set("steps", expr.Obj(steps))

		env, _ := ghctx.EvalEnv(baseEnv, s.Env, ctx)
		ctx.Set("env", expr.Obj(env))

		sp := &StepPlan{Index: i, Name: evalName(s.DisplayName(), ctx)}
		sp.Status, sp.Reason = evalCondition(s.If, ctx, nil)
		out = append(out, sp)

		if s.ID == "" {
			continue
		}
		// ステップの出力は run の中で決まるため静的には分からない。
		// どの出力名を参照されても不明値を返すようにしておく。
		outputs := expr.NewObject()
		outputs.SetUnknownFallback("steps." + s.ID + ".outputs")

		entry := expr.NewObject()
		entry.Set("outputs", expr.Obj(outputs))
		entry.Set("outcome", expr.String("success"))
		entry.Set("conclusion", expr.String("success"))
		steps.Set(s.ID, expr.Obj(entry))
	}
	return out
}

// evalCondition は if 条件を評価して実行見込みを返す。
// if を書いていない場合は GitHub と同じく暗黙の success() が適用される。
func evalCondition(cond workflow.Scalar, ctx *expr.Context, depSkipped []string) (Status, string) {
	src := "success()"
	explicit := cond.Set
	if explicit {
		src = cond.Raw
	}

	tmpl, err := expr.CompileCondition(src)
	if err != nil {
		return StatusUnknown, fmt.Sprintf("if の式を解析できません: %v", err)
	}

	// 不明値の参照を拾うため、評価ごとに独立したコンテキストを使う。
	evalCtx := ctx.Clone()
	ok, err := tmpl.EvalCondition(evalCtx)
	if err != nil {
		return StatusUnknown, fmt.Sprintf("if を評価できません: %v", err)
	}

	// 実行しないと確定しない値を参照している場合、評価結果は仮のものに
	// すぎないため判定不能として扱う。真になった場合も、実際の値次第で
	// 偽になりうるので同じ扱いにする。
	if len(evalCtx.Unknowns) > 0 {
		assumed := "スキップ"
		if ok {
			assumed = "実行"
		}
		return StatusUnknown, fmt.Sprintf("%s の値が不明です（不明なまま評価すると%s）",
			strings.Join(evalCtx.Unknowns, ", "), assumed)
	}

	if ok {
		return StatusRun, ""
	}

	switch {
	case explicit:
		return StatusSkip, fmt.Sprintf("if が偽: %s", strings.TrimSpace(src))
	case len(depSkipped) > 0:
		return StatusSkip, fmt.Sprintf("依存ジョブ %s が実行されないため", strings.Join(depSkipped, ", "))
	default:
		return StatusSkip, "先行する処理が成功しないため"
	}
}

// instanceName はジョブの表示名を作る。
func instanceName(job *workflow.Job, combo *Combination, ctx *expr.Context) string {
	if job.Name != "" {
		return evalName(job.Name, ctx)
	}
	// name: が無い場合、GitHub は matrix の値を括弧で添える。
	if combo.Values.Len() > 0 {
		return fmt.Sprintf("%s (%s)", job.ID, combo.String())
	}
	return job.ID
}

// evalName は表示名に含まれる式を解決する。
// 解決できない場合は元の表記をそのまま返し、何が書かれていたか分かるようにする。
func evalName(name string, ctx *expr.Context) string {
	tmpl, err := expr.CompileTemplate(name)
	if err != nil {
		return name
	}
	s, err := tmpl.Eval(ctx)
	if err != nil {
		return name
	}
	return s
}

// resolveRunsOn は runs-on の式を解決し、表示用の文字列と解決後のラベルを返す。
func resolveRunsOn(r workflow.RunsOn, ctx *expr.Context) (display string, labels []string) {
	labels = make([]string, 0, len(r.Labels))
	for _, l := range r.Labels {
		labels = append(labels, evalName(l, ctx))
	}
	resolved := workflow.RunsOn{Labels: labels, Group: r.Group, Set: r.Set}
	return resolved.String(), labels
}

// matrixOf はジョブの matrix 定義を取り出す。定義が無い場合は nil。
func matrixOf(job *workflow.Job) *workflow.Matrix {
	if job.Strategy == nil {
		return nil
	}
	return job.Strategy.Matrix
}

// needsEntry は needs コンテキストに入れるジョブの結果を作る。
// 出力は実行しないと確定しないため、値が与えられていなければ不明値にする。
// これに依存する条件は「偽」ではなく「判定不能」として扱われる。
func needsEntry(jp *JobPlan, cfg *ghctx.Config) expr.Value {
	o := expr.NewObject()
	o.Set("result", expr.String(resultName(jp.Status())))

	prefix := "needs." + jp.Job.ID + ".outputs"
	outputs := expr.NewObject()
	// outputs: の宣言が無い出力を参照された場合も不明として扱う。
	// 宣言漏れと「値が空」を静的に区別できないため、安全側に寄せる。
	outputs.SetUnknownFallback(prefix)

	for _, k := range sortedKeys(jp.Job.Outputs) {
		if v, ok := cfg.Outputs[jp.Job.ID+"."+k]; ok {
			outputs.Set(k, expr.String(v))
			continue
		}
		outputs.Set(k, expr.Unknown(prefix+"."+k))
	}
	o.Set("outputs", expr.Obj(outputs))
	return expr.Obj(o)
}

// resultName は needs.<job>.result に入る文字列を返す。
func resultName(s Status) string {
	if s == StatusSkip {
		return "skipped"
	}
	// 判定できない場合も、後続の評価を進めるため成功として扱う。
	return "success"
}

// envOf はコンテキストから env オブジェクトを取り出す。
func envOf(ctx *expr.Context) *expr.Object {
	v, ok := ctx.Vars["env"]
	if !ok || v.Kind() != expr.KindObject {
		return expr.NewObject()
	}
	return v.Object()
}

// sortedKeys はマップのキーを昇順で返す。出力を安定させるために使う。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
