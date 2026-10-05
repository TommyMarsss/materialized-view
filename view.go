package main

import "fmt"

// Op 是基表变更操作类型。
type Op int

const (
	OpInsert Op = iota
	OpUpdate
	OpDelete
)

func (o Op) String() string {
	switch o {
	case OpInsert:
		return "INSERT"
	case OpUpdate:
		return "UPDATE"
	case OpDelete:
		return "DELETE"
	}
	return "UNKNOWN"
}

// Record 是基表中的一条记录。Group 为分组键，Value 为被聚合的度量值。
type Record struct {
	ID    string  `json:"id"`
	Group string  `json:"group"`
	Value float64 `json:"value"`
}

// Change 描述一次基表变更。增量维护要求变更携带足够信息以撤销旧贡献：
//   - Insert: 仅 After
//   - Delete: 仅 Before
//   - Update: Before 与 After 均携带（Before 用于撤销旧贡献）
type Change struct {
	Op     Op      `json:"op"`
	Before *Record `json:"before,omitempty"`
	After  *Record `json:"after,omitempty"`
}

func (c Change) String() string {
	switch c.Op {
	case OpInsert:
		return fmt.Sprintf("INSERT %s(group=%s value=%.2f)", c.After.ID, c.After.Group, c.After.Value)
	case OpUpdate:
		return fmt.Sprintf("UPDATE %s(group=%s value=%.2f) -> (group=%s value=%.2f)",
			c.Before.ID, c.Before.Group, c.Before.Value, c.After.Group, c.After.Value)
	case OpDelete:
		return fmt.Sprintf("DELETE %s(group=%s value=%.2f)", c.Before.ID, c.Before.Group, c.Before.Value)
	}
	return "?"
}

// CleanupPolicy 控制分组内最后一条记录被移除后，空分组是否保留在视图中。
type CleanupPolicy int

const (
	// RemoveEmptyGroups 分组计数归零时从视图中彻底删除该分组（默认）。
	RemoveEmptyGroups CleanupPolicy = iota
	// RetainEmptyGroups 分组计数归零时仍保留 count=0 的空条目。
	RetainEmptyGroups
)

func (p CleanupPolicy) String() string {
	if p == RetainEmptyGroups {
		return "retain-empty-groups"
	}
	return "remove-empty-groups"
}

// GroupAgg 是一个分组的聚合状态。Avg 由 Sum/Count 派生，不单独存储，
// 保证平均值永远与 sum/count 一致。
type GroupAgg struct {
	Count int     `json:"count"`
	Sum   float64 `json:"sum"`
}

// Avg 返回分组平均值；空分组返回 0。
func (g GroupAgg) Avg() float64 {
	if g.Count == 0 {
		return 0
	}
	return g.Sum / float64(g.Count)
}

// EventKind 标识一次变更对视图造成的影响类别，用于回放高亮。
type EventKind int

const (
	EventGroupCreated       EventKind = iota // 新分组出现
	EventAggregateAdjusted                   // 已有分组聚合值被修正
	EventGroupRemoved                        // 分组被清理（仅 RemoveEmptyGroups 策略）
)

func (k EventKind) String() string {
	switch k {
	case EventGroupCreated:
		return "GROUP_CREATED"
	case EventAggregateAdjusted:
		return "AGGREGATE_ADJUSTED"
	case EventGroupRemoved:
		return "GROUP_REMOVED"
	}
	return "?"
}

// Event 记录一次变更引起的单个分组级别的视图变化。
type Event struct {
	Kind   EventKind `json:"kind"`
	Group  string    `json:"group"`
	Before GroupAgg  `json:"before"` // 变更前该分组的聚合（新建时为零值）
	After  GroupAgg  `json:"after"`  // 变更后该分组的聚合（删除时为零值）
	Note   string    `json:"note"`   // 人类可读的修正说明
}

// View 是增量维护的分组聚合视图（count / sum / avg）。
// 除了聚合结果外，还维护一份活跃记录索引 index（ID -> 当前记录），
// 用于校验变更流中 Before 镜像与当前状态一致——这是撤销旧贡献的前提。
type View struct {
	policy CleanupPolicy
	groups map[string]GroupAgg
	index  map[string]Record
}

// NewView 创建空视图。
func NewView(policy CleanupPolicy) *View {
	return &View{
		policy: policy,
		groups: make(map[string]GroupAgg),
		index:  make(map[string]Record),
	}
}

// Policy 返回视图的分组清理策略。
func (v *View) Policy() CleanupPolicy { return v.policy }

// Groups 返回当前视图内容（按分组键）。
func (v *View) Groups() map[string]GroupAgg { return v.groups }

// LiveRecords 返回当前活跃记录索引。
func (v *View) LiveRecords() map[string]Record { return v.index }

// Apply 应用一条变更，增量维护视图，返回产生的分组级事件列表。
// 核心不变量：任何记录对聚合的贡献先被完整撤销（retract），再按新值加入（add），
// 因此更新不会虚高，分组键变更会同时修正源分组与目标分组。
func (v *View) Apply(c Change) ([]Event, error) {
	var events []Event
	switch c.Op {
	case OpInsert:
		if c.After == nil {
			return nil, fmt.Errorf("insert requires After record")
		}
		if _, exists := v.index[c.After.ID]; exists {
			return nil, fmt.Errorf("insert %s: record already exists", c.After.ID)
		}
		v.add(*c.After, &events)
		v.index[c.After.ID] = *c.After

	case OpUpdate:
		if c.Before == nil || c.After == nil {
			return nil, fmt.Errorf("update requires Before and After records")
		}
		if c.Before.ID != c.After.ID {
			return nil, fmt.Errorf("update: id mismatch %s vs %s", c.Before.ID, c.After.ID)
		}
		cur, exists := v.index[c.Before.ID]
		if !exists {
			return nil, fmt.Errorf("update %s: record not found", c.Before.ID)
		}
		if cur != *c.Before {
			return nil, fmt.Errorf("update %s: before image %+v does not match current %+v", c.Before.ID, *c.Before, cur)
		}
		// 先撤销旧值贡献，再加入新值贡献。分组键变化时，
		// retract 作用于旧分组、add 作用于新分组，两侧都被正确修正。
		v.retract(*c.Before, &events)
		v.add(*c.After, &events)
		v.index[c.After.ID] = *c.After

	case OpDelete:
		if c.Before == nil {
			return nil, fmt.Errorf("delete requires Before record")
		}
		cur, exists := v.index[c.Before.ID]
		if !exists {
			return nil, fmt.Errorf("delete %s: record not found", c.Before.ID)
		}
		if cur != *c.Before {
			return nil, fmt.Errorf("delete %s: before image %+v does not match current %+v", c.Before.ID, *c.Before, cur)
		}
		v.retract(*c.Before, &events)
		delete(v.index, c.Before.ID)
	}
	return events, nil
}

// add 把一条记录的贡献加入其分组。
func (v *View) add(r Record, events *[]Event) {
	before, exists := v.groups[r.Group]
	after := GroupAgg{Count: before.Count + 1, Sum: before.Sum + r.Value}
	v.groups[r.Group] = after
	if !exists {
		*events = append(*events, Event{
			Kind: EventGroupCreated, Group: r.Group, Before: GroupAgg{}, After: after,
			Note: fmt.Sprintf("group %q created by record %s", r.Group, r.ID),
		})
	} else {
		*events = append(*events, Event{
			Kind: EventAggregateAdjusted, Group: r.Group, Before: before, After: after,
			Note: fmt.Sprintf("added contribution of %s (value=%.2f)", r.ID, r.Value),
		})
	}
}

// retract 撤销一条记录对其分组的贡献；计数归零时按清理策略处理空分组。
func (v *View) retract(r Record, events *[]Event) {
	before := v.groups[r.Group]
	after := GroupAgg{Count: before.Count - 1, Sum: before.Sum - r.Value}
	if after.Count == 0 && v.policy == RemoveEmptyGroups {
		delete(v.groups, r.Group)
		*events = append(*events, Event{
			Kind: EventGroupRemoved, Group: r.Group, Before: before, After: GroupAgg{},
			Note: fmt.Sprintf("last record %s left group %q; group removed", r.ID, r.Group),
		})
		return
	}
	v.groups[r.Group] = after
	*events = append(*events, Event{
		Kind: EventAggregateAdjusted, Group: r.Group, Before: before, After: after,
		Note: fmt.Sprintf("retracted contribution of %s (value=%.2f)", r.ID, r.Value),
	})
}
