package main

import "math"

// Recompute 对当前活跃记录集合做全量聚合，作为增量维护正确性的黄金标准。
// 它不看增量视图的任何状态，只从记录集合重新计算。
func Recompute(records map[string]Record) map[string]GroupAgg {
	out := make(map[string]GroupAgg)
	for _, r := range records {
		g := out[r.Group]
		g.Count++
		g.Sum += r.Value
		out[r.Group] = g
	}
	return out
}

const epsilon = 1e-9

// ConsistentWith 校验增量视图与全量重算结果是否一致。
//
// 规则：
//   - 重算结果中的每个分组，必须在视图中存在且 count/sum 一致；
//   - 视图中的每个分组，若 count>0 必须与重算一致；
//     count==0 的空分组仅在 RetainEmptyGroups 策略下允许存在
//     （空分组是历史痕迹，全量重算无法复现，故不参与比对）。
func ConsistentWith(view *View, records map[string]Record) (bool, string) {
	full := Recompute(records)
	groups := view.Groups()

	for key, want := range full {
		got, ok := groups[key]
		if !ok {
			return false, "group " + key + " missing from incremental view"
		}
		if got.Count != want.Count || math.Abs(got.Sum-want.Sum) > epsilon {
			return false, "group " + key + " mismatch"
		}
	}
	for key, got := range groups {
		if got.Count == 0 {
			if view.Policy() == RetainEmptyGroups {
				continue
			}
			return false, "group " + key + " is empty but was not removed"
		}
		if _, ok := full[key]; !ok {
			return false, "group " + key + " exists in view but not in full recompute"
		}
	}
	return true, ""
}
