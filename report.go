package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Step 是回放序列中的一帧：一条变更、它产生的视图事件、应用后的视图快照
// 以及该时刻增量视图与全量重算的一致性校验结果。
type Step struct {
	Index      int                `json:"index"`
	Change     string             `json:"change"`
	Op         string             `json:"op"`
	Events     []Event            `json:"events"`
	Groups     map[string]GroupAgg `json:"groups"`
	LiveCount  int                `json:"liveCount"`
	Consistent bool               `json:"consistent"`
}

// Report 是嵌入 HTML 的完整回放数据。
type Report struct {
	Policy    string `json:"policy"`
	NumSteps  int    `json:"numSteps"`
	AllPassed bool   `json:"allPassed"`
	Steps     []Step `json:"steps"`
}

// WriteReport 把回放数据渲染为单一自包含 HTML 文件（无外部依赖）。
func WriteReport(path string, report Report) error {
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	html := strings.Replace(reportTemplate, "/*__DATA__*/", string(data), 1)
	return os.WriteFile(path, []byte(html), 0o644)
}

// BuildReport 依次应用变更流，每步做一致性校验并记录快照。
func BuildReport(policy CleanupPolicy, changes []Change) (Report, error) {
	view := NewView(policy)
	report := Report{Policy: policy.String(), AllPassed: true}
	for i, c := range changes {
		events, err := view.Apply(c)
		if err != nil {
			return report, fmt.Errorf("step %d: %w", i, err)
		}
		ok, msg := ConsistentWith(view, view.LiveRecords())
		if !ok {
			report.AllPassed = false
			return report, fmt.Errorf("step %d: consistency check failed: %s", i, msg)
		}
		snap := make(map[string]GroupAgg, len(view.Groups()))
		for k, g := range view.Groups() {
			snap[k] = g
		}
		report.Steps = append(report.Steps, Step{
			Index: i, Change: c.String(), Op: c.Op.String(),
			Events: events, Groups: snap, LiveCount: len(view.LiveRecords()),
			Consistent: ok,
		})
	}
	report.NumSteps = len(report.Steps)
	return report, nil
}
