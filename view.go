package main

import (
	"fmt"
	"sort"
)

// OpType 变更操作类型
type OpType string

const (
	OpInsert OpType = "insert"
	OpUpdate OpType = "update"
	OpDelete OpType = "delete"
)

// Record 基表记录：ID 为主键，Group 为分组键，Value 为被聚合的数值
type Record struct {
	ID    int     `json:"id"`
	Group string  `json:"group"`
	Value float64 `json:"value"`
}

// Operation 一条变更操作。Delete 只需 Rec.ID 有效。
type Operation struct {
	Type OpType `json:"type"`
	Rec  Record `json:"rec"`
}

func (op Operation) String() string {
	switch op.Type {
	case OpInsert:
		return fmt.Sprintf("INSERT #%d (%s, %v)", op.Rec.ID, op.Rec.Group, op.Rec.Value)
	case OpUpdate:
		return fmt.Sprintf("UPDATE #%d -> (%s, %v)", op.Rec.ID, op.Rec.Group, op.Rec.Value)
	case OpDelete:
		return fmt.Sprintf("DELETE #%d", op.Rec.ID)
	}
	return "?"
}

// GroupAgg 一个分组的聚合状态。Avg 由 Sum/Count 派生，不单独存储。
type GroupAgg struct {
	Count int     `json:"count"`
	Sum   float64 `json:"sum"`
}

func (g GroupAgg) Avg() float64 {
	if g.Count == 0 {
		return 0
	}
	return g.Sum / float64(g.Count)
}

// EventType 增量维护过程中产生的、可供回放高亮的事件类型
type EventType string

const (
	EvGroupCreated  EventType = "group_created"  // 新分组被创建
	EvGroupAdjusted EventType = "group_adjusted" // 已有分组聚合被增量修正
	EvGroupRemoved  EventType = "group_removed"  // 分组被清理（最后一条记录离开）
	EvGroupKept     EventType = "group_kept"     // 分组已空但按策略保留
)

// Event 描述一次聚合状态变化，Before/After 为该分组变化前后的聚合值
type Event struct {
	Type   EventType `json:"type"`
	Group  string    `json:"group"`
	Before GroupAgg  `json:"before"`
	After  GroupAgg  `json:"after"`
	Detail string    `json:"detail"`
}

// View 增量物化视图：按 Group 分组的 count/sum/avg 聚合。
// RemoveEmptyGroups 控制分组清理策略：
//   - true: 分组内最后一条记录被删除/移出时，分组从视图中完全移除（默认语义）
//   - false: 保留 count=0 的空分组条目
type View struct {
	RemoveEmptyGroups bool
	records           map[int]Record // 当前有效记录（基表镜像）
	groups            map[string]*GroupAgg
}

func NewView(removeEmptyGroups bool) *View {
	return &View{
		RemoveEmptyGroups: removeEmptyGroups,
		records:           make(map[int]Record),
		groups:            make(map[string]*GroupAgg),
	}
}

// Apply 应用一条变更操作，增量维护聚合视图，返回本次操作产生的事件序列。
// 对非法操作（重复插入、更新/删除不存在的记录）返回错误且不改状态。
func (v *View) Apply(op Operation) ([]Event, error) {
	switch op.Type {
	case OpInsert:
		if _, ok := v.records[op.Rec.ID]; ok {
			return nil, fmt.Errorf("insert: record #%d already exists", op.Rec.ID)
		}
		v.records[op.Rec.ID] = op.Rec
		return v.addContribution(op.Rec), nil

	case OpUpdate:
		old, ok := v.records[op.Rec.ID]
		if !ok {
			return nil, fmt.Errorf("update: record #%d does not exist", op.Rec.ID)
		}
		// 关键：先撤销旧值的贡献，再加入新值的贡献。
		// 若分组键改变，撤销发生在旧分组、加入发生在新分组，两边都被正确修正。
		var events []Event
		events = append(events, v.removeContribution(old)...)
		v.records[op.Rec.ID] = op.Rec
		events = append(events, v.addContribution(op.Rec)...)
		return events, nil

	case OpDelete:
		old, ok := v.records[op.Rec.ID]
		if !ok {
			return nil, fmt.Errorf("delete: record #%d does not exist", op.Rec.ID)
		}
		events := v.removeContribution(old)
		delete(v.records, op.Rec.ID)
		return events, nil
	}
	return nil, fmt.Errorf("unknown op type %q", op.Type)
}

// addContribution 把一条记录的贡献加入其分组
func (v *View) addContribution(r Record) []Event {
	agg, ok := v.groups[r.Group]
	if !ok {
		agg = &GroupAgg{}
		v.groups[r.Group] = agg
	}
	before := *agg
	agg.Count++
	agg.Sum += r.Value
	if !ok {
		return []Event{{
			Type: EvGroupCreated, Group: r.Group,
			Before: GroupAgg{}, After: *agg,
			Detail: fmt.Sprintf("分组 %q 创建（记录 #%d 加入）", r.Group, r.ID),
		}}
	}
	return []Event{{
		Type: EvGroupAdjusted, Group: r.Group,
		Before: before, After: *agg,
		Detail: fmt.Sprintf("记录 #%d 加入分组 %q：count %d→%d, sum %v→%v",
			r.ID, r.Group, before.Count, agg.Count, before.Sum, agg.Sum),
	}}
}

// removeContribution 撤销一条记录对其分组的贡献，并按策略处理空分组
func (v *View) removeContribution(r Record) []Event {
	agg, ok := v.groups[r.Group]
	if !ok {
		return nil // 不变式保证不会发生；防御性返回
	}
	before := *agg
	agg.Count--
	agg.Sum -= r.Value
	if agg.Count == 0 {
		if v.RemoveEmptyGroups {
			delete(v.groups, r.Group)
			return []Event{{
				Type: EvGroupRemoved, Group: r.Group,
				Before: before, After: GroupAgg{},
				Detail: fmt.Sprintf("分组 %q 最后一条记录 #%d 离开，分组被清理", r.Group, r.ID),
			}}
		}
		agg.Sum = 0 // 保留策略下归零，避免浮点残差
		return []Event{{
			Type: EvGroupKept, Group: r.Group,
			Before: before, After: *agg,
			Detail: fmt.Sprintf("分组 %q 已空，按策略保留（count=0）", r.Group),
		}}
	}
	return []Event{{
		Type: EvGroupAdjusted, Group: r.Group,
		Before: before, After: *agg,
		Detail: fmt.Sprintf("记录 #%d 离开分组 %q：count %d→%d, sum %v→%v",
			r.ID, r.Group, before.Count, agg.Count, before.Sum, agg.Sum),
	}}
}

// GroupRow 视图快照中的一行（按分组键排序输出）
type GroupRow struct {
	Group string  `json:"group"`
	Count int     `json:"count"`
	Sum   float64 `json:"sum"`
	Avg   float64 `json:"avg"`
}

// Snapshot 返回当前视图状态的有序快照
func (v *View) Snapshot() []GroupRow {
	rows := make([]GroupRow, 0, len(v.groups))
	for g, agg := range v.groups {
		rows = append(rows, GroupRow{Group: g, Count: agg.Count, Sum: agg.Sum, Avg: agg.Avg()})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Group < rows[j].Group })
	return rows
}

// Records 返回当前有效记录（按 ID 排序），用于回放展示
func (v *View) Records() []Record {
	recs := make([]Record, 0, len(v.records))
	for _, r := range v.records {
		recs = append(recs, r)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].ID < recs[j].ID })
	return recs
}

// FullRecompute 黄金标准：对一组有效记录从零全量聚合。
// removeEmptyGroups 语义下结果天然不含空分组；
// keepEmpty 语义下需额外保留曾出现过的分组键，由调用方传入 knownGroups。
func FullRecompute(records []Record) map[string]GroupAgg {
	out := make(map[string]GroupAgg)
	for _, r := range records {
		agg := out[r.Group]
		agg.Count++
		agg.Sum += r.Value
		out[r.Group] = agg
	}
	return out
}

const epsilon = 1e-9

func almostEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < epsilon
}

// ConsistentWith 校验增量视图与全量重算结果是否完全一致
func (v *View) ConsistentWith(full map[string]GroupAgg) bool {
	if len(v.groups) != len(full) {
		return false
	}
	for g, agg := range v.groups {
		f, ok := full[g]
		if !ok || f.Count != agg.Count || !almostEqual(f.Sum, agg.Sum) {
			return false
		}
	}
	return true
}

// CheckConsistency 对当前状态做全量重算并比对，返回是否一致
func (v *View) CheckConsistency() bool {
	recs := v.Records()
	full := FullRecompute(recs)
	if !v.RemoveEmptyGroups {
		// 保留策略下，全量重算需补上历史上出现过、当前为空的分组
		for g := range v.groups {
			if _, ok := full[g]; !ok {
				full[g] = GroupAgg{}
			}
		}
	}
	return v.ConsistentWith(full)
}
