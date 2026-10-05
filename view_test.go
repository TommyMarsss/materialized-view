package main

import (
	"math/rand"
	"testing"
)

func mustApply(t *testing.T, v *View, op Operation) []Event {
	t.Helper()
	events, err := v.Apply(op)
	if err != nil {
		t.Fatalf("Apply(%v) 出错: %v", op, err)
	}
	return events
}

func aggOf(t *testing.T, v *View, group string) GroupAgg {
	t.Helper()
	agg, ok := v.groups[group]
	if !ok {
		t.Fatalf("分组 %q 不存在", group)
	}
	return *agg
}

// 插入：聚合值累加、分组创建事件
func TestInsertCreatesGroupAndAccumulates(t *testing.T) {
	v := NewView(true)
	ev := mustApply(t, v, Operation{OpInsert, Record{1, "A", 10}})
	if len(ev) != 1 || ev[0].Type != EvGroupCreated {
		t.Fatalf("首条插入应产生 group_created 事件, 得到 %+v", ev)
	}
	ev = mustApply(t, v, Operation{OpInsert, Record{2, "A", 20}})
	if len(ev) != 1 || ev[0].Type != EvGroupAdjusted {
		t.Fatalf("二次插入应产生 group_adjusted 事件, 得到 %+v", ev)
	}
	agg := aggOf(t, v, "A")
	if agg.Count != 2 || agg.Sum != 30 || agg.Avg() != 15 {
		t.Fatalf("聚合错误: %+v", agg)
	}
}

// 同组更新：必须先撤销旧值再加入新值，sum 不得虚高
func TestUpdateSameGroupCorrectsAggregate(t *testing.T) {
	v := NewView(true)
	mustApply(t, v, Operation{OpInsert, Record{1, "A", 10}})
	mustApply(t, v, Operation{OpInsert, Record{2, "A", 20}})
	mustApply(t, v, Operation{OpUpdate, Record{1, "A", 100}})
	agg := aggOf(t, v, "A")
	// 正确结果: 100 + 20 = 120；若简单累加新值会得到 130（虚高）
	if agg.Count != 2 || agg.Sum != 120 {
		t.Fatalf("更新后聚合错误（疑似未撤销旧值）: %+v", agg)
	}
}

// 跨组更新：记录从 A 移到 B，两个分组都必须被正确修正
func TestUpdateGroupKeyChangeAdjustsBothGroups(t *testing.T) {
	v := NewView(true)
	mustApply(t, v, Operation{OpInsert, Record{1, "A", 10}})
	mustApply(t, v, Operation{OpInsert, Record{2, "A", 20}})
	mustApply(t, v, Operation{OpInsert, Record{3, "B", 5}})
	events := mustApply(t, v, Operation{OpUpdate, Record{2, "B", 50}})

	a := aggOf(t, v, "A")
	b := aggOf(t, v, "B")
	if a.Count != 1 || a.Sum != 10 {
		t.Fatalf("旧分组 A 未正确撤销贡献: %+v", a)
	}
	if b.Count != 2 || b.Sum != 55 {
		t.Fatalf("新分组 B 未正确加入贡献: %+v", b)
	}
	// 事件应覆盖两个分组
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.Group] = true
	}
	if !seen["A"] || !seen["B"] {
		t.Fatalf("跨组更新应产生 A、B 两个分组的事件: %+v", events)
	}
}

// 删除：撤销贡献；最后一条删除后分组被清理（remove-empty 策略）
func TestDeleteRemovesEmptyGroup(t *testing.T) {
	v := NewView(true)
	mustApply(t, v, Operation{OpInsert, Record{1, "A", 10}})
	mustApply(t, v, Operation{OpInsert, Record{2, "A", 20}})
	ev := mustApply(t, v, Operation{OpDelete, Record{ID: 1}})
	if ev[0].Type != EvGroupAdjusted {
		t.Fatalf("非最后一条删除应为 adjusted 事件: %+v", ev)
	}
	ev = mustApply(t, v, Operation{OpDelete, Record{ID: 2}})
	if len(ev) != 1 || ev[0].Type != EvGroupRemoved {
		t.Fatalf("最后一条删除应产生 group_removed 事件: %+v", ev)
	}
	if _, ok := v.groups["A"]; ok {
		t.Fatal("分组 A 应从视图中完全移除")
	}
	if len(v.Snapshot()) != 0 {
		t.Fatalf("视图应为空: %+v", v.Snapshot())
	}
}

// 保留策略：空分组保留为 count=0 条目
func TestDeleteKeepsEmptyGroupWhenConfigured(t *testing.T) {
	v := NewView(false)
	mustApply(t, v, Operation{OpInsert, Record{1, "A", 10}})
	ev := mustApply(t, v, Operation{OpDelete, Record{ID: 1}})
	if len(ev) != 1 || ev[0].Type != EvGroupKept {
		t.Fatalf("保留策略下应产生 group_kept 事件: %+v", ev)
	}
	agg := aggOf(t, v, "A")
	if agg.Count != 0 || agg.Sum != 0 {
		t.Fatalf("空分组应为 count=0,sum=0: %+v", agg)
	}
	// 空分组上重新插入应复活该分组
	mustApply(t, v, Operation{OpInsert, Record{2, "A", 7}})
	agg = aggOf(t, v, "A")
	if agg.Count != 1 || agg.Sum != 7 {
		t.Fatalf("复活分组聚合错误: %+v", agg)
	}
}

// 跨组更新导致旧分组变空：旧分组按策略清理
func TestUpdateMovingLastRecordCleansUpOldGroup(t *testing.T) {
	v := NewView(true)
	mustApply(t, v, Operation{OpInsert, Record{1, "A", 10}})
	mustApply(t, v, Operation{OpInsert, Record{2, "B", 5}})
	events := mustApply(t, v, Operation{OpUpdate, Record{1, "B", 10}})
	if _, ok := v.groups["A"]; ok {
		t.Fatal("记录 #1 移出后分组 A 应被清理")
	}
	var removed bool
	for _, e := range events {
		if e.Type == EvGroupRemoved && e.Group == "A" {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("应产生 A 的 group_removed 事件: %+v", events)
	}
	b := aggOf(t, v, "B")
	if b.Count != 2 || b.Sum != 15 {
		t.Fatalf("分组 B 聚合错误: %+v", b)
	}
}

// 非法操作：重复插入、更新/删除不存在的记录
func TestInvalidOperationsRejected(t *testing.T) {
	v := NewView(true)
	mustApply(t, v, Operation{OpInsert, Record{1, "A", 10}})
	if _, err := v.Apply(Operation{OpInsert, Record{1, "A", 99}}); err == nil {
		t.Fatal("重复插入应报错")
	}
	if _, err := v.Apply(Operation{OpUpdate, Record{999, "A", 1}}); err == nil {
		t.Fatal("更新不存在的记录应报错")
	}
	if _, err := v.Apply(Operation{OpDelete, Record{ID: 999}}); err == nil {
		t.Fatal("删除不存在的记录应报错")
	}
	// 出错后状态不被破坏
	agg := aggOf(t, v, "A")
	if agg.Count != 1 || agg.Sum != 10 {
		t.Fatalf("非法操作后状态被污染: %+v", agg)
	}
}

// 差分测试：大量随机混合操作序列，每步都与全量重算比对（两种清理策略）
func TestDifferentialRandomStreams(t *testing.T) {
	groups := []string{"A", "B", "C", "D", "E"}
	for _, removeEmpty := range []bool{true, false} {
		for seed := int64(0); seed < 200; seed++ {
			r := rand.New(rand.NewSource(seed))
			ops := randomStream(r, 300, groups)
			v := NewView(removeEmpty)
			for i, op := range ops {
				if _, err := v.Apply(op); err != nil {
					t.Fatalf("removeEmpty=%v seed=%d step=%d: %v", removeEmpty, seed, i, err)
				}
				if !v.CheckConsistency() {
					t.Fatalf("removeEmpty=%v seed=%d step=%d (%v): 增量视图与全量重算不一致\n增量: %+v",
						removeEmpty, seed, i, op, v.Snapshot())
				}
			}
		}
	}
}

// RunStream 应在每步记录一致性校验结果
func TestRunStreamAllStepsConsistent(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	ops := append(demoStream(), randomStream(r, 100, []string{"A", "B", "C"})...)
	v := NewView(true)
	steps, err := RunStream(v, ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != len(ops) {
		t.Fatalf("步数不符: %d != %d", len(steps), len(ops))
	}
	for _, s := range steps {
		if !s.Consistent {
			t.Fatalf("步骤 %d (%s) 不一致", s.Index, s.Op)
		}
	}
}
