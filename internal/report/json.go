package report

import (
	"encoding/json"
	"io"

	"github.com/a2-ito/gh-sim/internal/plan"
)

// JSON 出力は内部の型と切り離しておく。
// 内部構造を変えても出力の互換性を保てるようにするため。
type jsonPlan struct {
	Workflow  string    `json:"workflow"`
	Path      string    `json:"path"`
	Triggered bool      `json:"triggered"`
	Reason    string    `json:"reason"`
	Notes     []string  `json:"notes,omitempty"`
	Jobs      []jsonJob `json:"jobs"`
}

type jsonJob struct {
	ID        string         `json:"id"`
	Layer     int            `json:"layer"`
	Status    string         `json:"status"`
	Needs     []string       `json:"needs,omitempty"`
	Axes      []string       `json:"matrixAxes,omitempty"`
	Uses      string         `json:"uses,omitempty"`
	Notes     []string       `json:"notes,omitempty"`
	Instances []jsonInstance `json:"instances"`
}

type jsonInstance struct {
	Name   string         `json:"name"`
	Status string         `json:"status"`
	Reason string         `json:"reason,omitempty"`
	RunsOn string         `json:"runsOn,omitempty"`
	Matrix map[string]any `json:"matrix,omitempty"`
	Steps  []jsonStep     `json:"steps,omitempty"`
}

type jsonStep struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// WriteJSON は計画を JSON で書き出す。
func WriteJSON(out io.Writer, plans []*plan.Plan) error {
	docs := make([]jsonPlan, 0, len(plans))
	for _, p := range plans {
		docs = append(docs, toJSONPlan(p))
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(docs)
}

func toJSONPlan(p *plan.Plan) jsonPlan {
	doc := jsonPlan{
		Workflow:  p.Workflow.DisplayName(),
		Path:      p.Workflow.Path,
		Triggered: p.Triggered(),
		Notes:     p.Notes,
		Jobs:      []jsonJob{},
	}
	if p.Trigger != nil {
		doc.Reason = p.Trigger.Reason
	}
	for _, jp := range p.Jobs {
		doc.Jobs = append(doc.Jobs, toJSONJob(jp))
	}
	return doc
}

func toJSONJob(jp *plan.JobPlan) jsonJob {
	job := jsonJob{
		ID:        jp.Job.ID,
		Layer:     jp.Layer,
		Status:    jp.Status().String(),
		Needs:     jp.Job.Needs,
		Axes:      jp.Axes,
		Uses:      jp.Job.Uses,
		Notes:     jp.Notes,
		Instances: []jsonInstance{},
	}
	for _, inst := range jp.Instances {
		job.Instances = append(job.Instances, toJSONInstance(inst))
	}
	return job
}

func toJSONInstance(inst *plan.JobInstance) jsonInstance {
	doc := jsonInstance{
		Name:   inst.Name,
		Status: inst.Status.String(),
		Reason: inst.Reason,
		RunsOn: inst.RunsOn,
	}
	if inst.Matrix != nil && inst.Matrix.Values.Len() > 0 {
		doc.Matrix = make(map[string]any, inst.Matrix.Values.Len())
		for _, k := range inst.Matrix.Values.Keys() {
			v, _ := inst.Matrix.Values.Get(k)
			doc.Matrix[k] = json.RawMessage(v.JSON(false))
		}
	}
	for _, sp := range inst.Steps {
		doc.Steps = append(doc.Steps, jsonStep{
			Index:  sp.Index,
			Name:   sp.Name,
			Status: sp.Status.String(),
			Reason: sp.Reason,
		})
	}
	return doc
}
