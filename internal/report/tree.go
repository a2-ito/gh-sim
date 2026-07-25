// Package report はドライラン結果を人が読める形に整えて出力する。
package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/a2-ito/gh-sim/internal/plan"
)

// Options は出力の調整項目。
type Options struct {
	// ShowSteps はステップの一覧を表示するか。
	ShowSteps bool
	// ShowNotes は判定を保留した項目などの注記を表示するか。
	ShowNotes bool
	// ShowNotTriggered は起動しないワークフローも表示するか。
	ShowNotTriggered bool
	// Color は記号や見出しに色を付けるか。
	Color bool
}

// ツリーを描く罫線。
const (
	branchMid  = "├─ "
	branchLast = "└─ "
	pipeMid    = "│  "
	pipeLast   = "   "
)

// writer はツリー出力の状態を持つ。
type writer struct {
	out  io.Writer
	opts Options
	err  error
}

// Write はワークフローごとのドライラン結果を書き出す。
func Write(out io.Writer, plans []*plan.Plan, opts Options) error {
	w := &writer{out: out, opts: opts}

	shown := 0
	for _, p := range plans {
		if !p.Triggered() && !opts.ShowNotTriggered {
			continue
		}
		if shown > 0 {
			w.line("")
		}
		w.writePlan(p)
		shown++
	}

	if shown == 0 {
		w.line("このイベントで起動するワークフローはありません（-all で対象外も表示できます）")
		w.line("")
	}
	w.writeSummary(plans)
	return w.err
}

// writePlan はワークフロー1つ分を書き出す。
func (w *writer) writePlan(p *plan.Plan) {
	w.line("%s  %s", w.bold(p.Workflow.DisplayName()), w.dim(p.Workflow.Path))

	if !p.Triggered() {
		w.line("  %s 起動しない — %s", w.red("✘"), p.Trigger.Reason)
		w.writeNotes("  ", p.Notes)
		return
	}

	w.line("  %s 起動する — %s", w.green("✔"), p.Trigger.Reason)
	w.writeNotes("  ", p.Notes)

	for i, jp := range p.Jobs {
		w.writeJob("  ", i == len(p.Jobs)-1, jp)
	}
}

// writeJob はジョブ1つ分を書き出す。
func (w *writer) writeJob(prefix string, last bool, jp *plan.JobPlan) {
	branch, pipe := branchMid, pipeMid
	if last {
		branch, pipe = branchLast, pipeLast
	}
	child := prefix + pipe

	// matrix が無い場合はジョブと組み合わせを1行にまとめる。
	if len(jp.Axes) == 0 && len(jp.Instances) == 1 {
		inst := jp.Instances[0]
		attrs := append(instanceAttrs(jp, inst), jobAttrs(jp)...)
		w.line("%s%s %s%s", prefix+branch, w.mark(inst.Status), inst.Name, w.dim(attrText(attrs)))
		w.writeReason(child, inst.Status, inst.Reason)
		w.writeSteps(child, inst)
		w.writeNotes(child, jp.Notes)
		return
	}

	// matrix がある場合はジョブを見出しにして組み合わせを子に並べる。
	w.line("%s%s %s%s", prefix+branch, w.mark(jp.Status()), jp.Job.ID, w.dim(attrText(jobAttrs(jp))))
	w.writeNotes(child, jp.Notes)

	for i, inst := range jp.Instances {
		instLast := i == len(jp.Instances)-1
		instBranch, instPipe := branchMid, pipeMid
		if instLast {
			instBranch, instPipe = branchLast, pipeLast
		}
		w.line("%s%s %s%s", child+instBranch, w.mark(inst.Status), inst.Name, w.dim(attrText(instanceAttrs(jp, inst))))
		w.writeReason(child+instPipe, inst.Status, inst.Reason)
		w.writeSteps(child+instPipe, inst)
	}
}

// writeSteps はステップの一覧を書き出す。
func (w *writer) writeSteps(prefix string, inst *plan.JobInstance) {
	if !w.opts.ShowSteps || len(inst.Steps) == 0 {
		return
	}
	for i, sp := range inst.Steps {
		branch, pipe := branchMid, pipeMid
		if i == len(inst.Steps)-1 {
			branch, pipe = branchLast, pipeLast
		}
		w.line("%s%s %s", prefix+branch, w.mark(sp.Status), sp.Name)
		// ジョブごと実行されない場合の理由はジョブ側に出ているので繰り返さない。
		if sp.Reason != "ジョブが実行されないため" {
			w.writeReason(prefix+pipe, sp.Status, sp.Reason)
		}
	}
}

// writeReason は実行されない理由や判定できない理由を書き出す。
func (w *writer) writeReason(prefix string, status plan.Status, reason string) {
	if reason == "" {
		return
	}
	switch status {
	case plan.StatusSkip:
		w.line("%s%s", prefix, w.dim(reason))
	case plan.StatusUnknown:
		w.line("%s%s", prefix, w.yellow(reason))
	}
}

// writeNotes は注記を書き出す。
func (w *writer) writeNotes(prefix string, notes []string) {
	if !w.opts.ShowNotes {
		return
	}
	for _, n := range notes {
		w.line("%s%s %s", prefix, w.yellow("!"), w.dim(n))
	}
}

// writeSummary は全体の集計を書き出す。
func (w *writer) writeSummary(plans []*plan.Plan) {
	var triggered, run, skip, unknown int
	for _, p := range plans {
		if !p.Triggered() {
			continue
		}
		triggered++
		for _, jp := range p.Jobs {
			for _, inst := range jp.Instances {
				switch inst.Status {
				case plan.StatusRun:
					run++
				case plan.StatusSkip:
					skip++
				case plan.StatusUnknown:
					unknown++
				}
			}
		}
	}

	parts := []string{
		fmt.Sprintf("ワークフロー %d 件中 %d 件が起動", len(plans), triggered),
		fmt.Sprintf("ジョブ 実行 %d 件", run),
		fmt.Sprintf("スキップ %d 件", skip),
	}
	if unknown > 0 {
		parts = append(parts, fmt.Sprintf("判定不能 %d 件", unknown))
	}
	w.line("%s", w.dim(strings.Join(parts, " · ")))
}

// instanceAttrs は組み合わせに添える属性を組み立てる。
func instanceAttrs(jp *plan.JobPlan, inst *plan.JobInstance) []string {
	var attrs []string
	if inst.RunsOn != "" && !jp.Job.IsReusableCall() {
		attrs = append(attrs, inst.RunsOn)
	}
	if n := len(inst.Steps); n > 0 {
		attrs = append(attrs, fmt.Sprintf("ステップ %d", n))
	}
	return attrs
}

// jobAttrs はジョブに添える属性を組み立てる。
func jobAttrs(jp *plan.JobPlan) []string {
	var attrs []string
	if len(jp.Job.Needs) > 0 {
		attrs = append(attrs, "needs: "+strings.Join(jp.Job.Needs, ", "))
	}
	if len(jp.Axes) > 0 {
		attrs = append(attrs, fmt.Sprintf("matrix: %s → %d 件",
			strings.Join(jp.Axes, " × "), len(jp.Instances)))
	}
	if jp.Job.IsReusableCall() {
		attrs = append(attrs, "uses: "+jp.Job.Uses)
	}
	return attrs
}

// attrText は属性を1つの文字列にまとめる。属性が無い場合は空文字列。
func attrText(attrs []string) string {
	if len(attrs) == 0 {
		return ""
	}
	return "  " + strings.Join(attrs, " · ")
}

// line は1行書き出す。最初に発生したエラーを保持する。
func (w *writer) line(format string, args ...any) {
	if w.err != nil {
		return
	}
	_, w.err = fmt.Fprintf(w.out, format+"\n", args...)
}

// mark は状態を表す記号を返す。
func (w *writer) mark(s plan.Status) string {
	switch s {
	case plan.StatusRun:
		return w.green("✔")
	case plan.StatusSkip:
		return w.red("✘")
	}
	return w.yellow("?")
}

// 色付けの補助。Color が偽の場合は装飾せずそのまま返す。
func (w *writer) paint(s, code string) string {
	if !w.opts.Color {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (w *writer) green(s string) string  { return w.paint(s, "32") }
func (w *writer) red(s string) string    { return w.paint(s, "31") }
func (w *writer) yellow(s string) string { return w.paint(s, "33") }
func (w *writer) dim(s string) string    { return w.paint(s, "90") }
func (w *writer) bold(s string) string   { return w.paint(s, "1") }
