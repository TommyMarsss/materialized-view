package main

import (
	"math"
	"testing"
)

func rec(id, group string, value float64) *Record {
	return &Record{ID: id, Group: group, Value: value}
}

func mustApply(t *testing.T, v *View, c Change) []Event {
	t.Helper()
	events, err := v.Apply(c)
	if err != nil {
		t.Fatalf("Apply(%v) failed: %v", c, err)
	}
	return events
}

func aggEq(a, b GroupAgg) bool {
	return a.Count == b.Count && math.Abs(a.Sum-b.Sum) < epsilon
}

// 插入：新分组被创建，聚合值正确。
func TestInsertCreatesGroup(t *testing.T) {
	v := NewView(RemoveEmptyGroups)
	events := mustApply(t, v, Change{Op: OpInsert, After: rec("r1", "A", 10)})

	if len(events) != 1 || events[0].Kind != EventGroupCreated || events[0].Group != "A" {
		t.Fatalf("expected GROUP_CREATED on A, got %+v", events)
	}
	g := v.Groups()["A"]
	if g.Count != 1 || g.Sum != 10 || g.Avg() != 10 {
		t.Fatalf("bad aggregate: %+v", g)
	}
}

// 更新（同分组改值）：必须先撤销旧值再加入新值，sum 不得虚高。
func TestUpdateSameGroupCorrectsSum(t *testing.T) {
	v := NewView(RemoveEmptyGroups)
	mustApply(t, v, Change{Op: OpInsert, After: rec("r1", "A", 10)})
	mustApply(t, v, Change{Op: OpInsert, After: rec("r2", "A", 20)})

	mustApply(t, v, Change{Op: OpUpdate, Before: rec("r1", "A", 10), After: rec("r1", "A", 99)})

	g := v.Groups()["A"]
	want := GroupAgg{Count: 2, Sum: 119} // 不是 10+20+99=129
	if !aggEq(g, want) {
		t.Fatalf("sum inflated: got %+v want %+v", g, want)
	}
	if g.Avg() != 119.0/2 {
		t.Fatalf("bad avg: %v", g.Avg())
	}
}

// 更新（跨分组）：记录从 A 移到 B，A 撤销旧贡献、B 加入新贡献。
func TestUpdateAcrossGroups(t *testing.T) {
	v := NewView(RemoveEmptyGroups)
	mustApply(t, v, Change{Op: OpInsert, After: rec("r1", "A", 10)})
	mustApply(t, v, Change{Op: OpInsert, After: rec("r2", "A", 30)})
	mustApply(t, v, Change{Op: OpInsert, After: rec("r3", "B", 5)})

	events := mustApply(t, v, Change{Op: OpUpdate, Before: rec("r1", "A", 10), After: rec("r1", "B", 40)})

	if got := v.Groups()["A"]; !aggEq(got, GroupAgg{Count: 1, Sum: 30}) {
		t.Fatalf("source group A not corrected: %+v", got)
	}
	if got := v.Groups()["B"]; !aggEq(got, GroupAgg{Count: 2, Sum: 45}) {
		t.Fatalf("target group B not corrected: %+v", got)
	}
	// 两个分组都应产生修正事件
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.Group] = true
	}
	if !seen["A"] || !seen["B"] {
		t.Fatalf("expected events on both A and B, got %+v", events)
	}
}

// 更新把分组的最后一条记录移走：源分组必须被清理。
func TestUpdateMovingLastRecordRemovesSourceGroup(t *testing.T) {
	v := NewView(RemoveEmptyGroups)
	mustApply(t, v, Change{Op: OpInsert, After: rec("r1", "A", 10)})

	events := mustApply(t, v, Change{Op: OpUpdate, Before: rec("r1", "A", 10), After: rec("r1", "B", 10)})

	if _, exists := v.Groups()["A"]; exists {
		t.Fatal("group A should have been removed")
	}
	var removed bool
	for _, e := range events {
		if e.Kind == EventGroupRemoved && e.Group == "A" {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("expected GROUP_REMOVED event for A, got %+v", events)
	}
}

// 删除最后一条记录：remove 策略下分组消失。
func TestDeleteLastRecordRemovesGroup(t *testing.T) {
	v := NewView(RemoveEmptyGroups)
	mustApply(t, v, Change{Op: OpInsert, After: rec("r1", "A", 10)})
	mustApply(t, v, Change{Op: OpInsert, After: rec("r2", "A", 20)})
	mustApply(t, v, Change{Op: OpDelete, Before: rec("r2", "A", 20)})

	if _, exists := v.Groups()["A"]; !exists {
		t.Fatal("group A should still exist with one record")
	}
	events := mustApply(t, v, Change{Op: OpDelete, Before: rec("r1", "A", 10)})
	if _, exists := v.Groups()["A"]; exists {
		t.Fatal("group A should have been removed after last delete")
	}
	if len(events) != 1 || events[0].Kind != EventGroupRemoved {
		t.Fatalf("expected GROUP_REMOVED, got %+v", events)
	}
}

// 删除最后一条记录：retain 策略下保留 count=0 的空分组。
func TestDeleteLastRecordRetainsGroup(t *testing.T) {
	v := NewView(RetainEmptyGroups)
	mustApply(t, v, Change{Op: OpInsert, After: rec("r1", "A", 10)})
	events := mustApply(t, v, Change{Op: OpDelete, Before: rec("r1", "A", 10)})

	g, exists := v.Groups()["A"]
	if !exists {
		t.Fatal("group A should be retained under retain policy")
	}
	if g.Count != 0 || g.Sum != 0 {
		t.Fatalf("expected empty aggregate, got %+v", g)
	}
	for _, e := range events {
		if e.Kind == EventGroupRemoved {
			t.Fatalf("retain policy must not emit GROUP_REMOVED, got %+v", events)
		}
	}
}

// 非法变更必须报错：重复插入、删除不存在、before 镜像不匹配。
func TestInvalidChangesRejected(t *testing.T) {
	v := NewView(RemoveEmptyGroups)
	mustApply(t, v, Change{Op: OpInsert, After: rec("r1", "A", 10)})

	if _, err := v.Apply(Change{Op: OpInsert, After: rec("r1", "A", 1)}); err == nil {
		t.Fatal("duplicate insert should fail")
	}
	if _, err := v.Apply(Change{Op: OpDelete, Before: rec("nope", "A", 1)}); err == nil {
		t.Fatal("delete of missing record should fail")
	}
	if _, err := v.Apply(Change{Op: OpUpdate, Before: rec("r1", "A", 999), After: rec("r1", "A", 1)}); err == nil {
		t.Fatal("update with stale before-image should fail")
	}
	if _, err := v.Apply(Change{Op: OpUpdate, Before: rec("r1", "A", 10), After: rec("r2", "A", 10)}); err == nil {
		t.Fatal("update changing record id should fail")
	}
}

// 差分测试：大量随机混合操作序列，每一步增量视图都必须与全量重算一致。
func TestDifferentialAgainstFullRecompute(t *testing.T) {
	for _, policy := range []CleanupPolicy{RemoveEmptyGroups, RetainEmptyGroups} {
		for seed := int64(0); seed < 200; seed++ {
			changes := GenerateStream(GenConfig{
				Seed: seed, NumOps: 300, NumGroups: 4, MaxLive: 12, NumIDs: 20,
			})
			v := NewView(policy)
			for i, c := range changes {
				if _, err := v.Apply(c); err != nil {
					t.Fatalf("policy=%v seed=%d step=%d: apply failed: %v", policy, seed, i, err)
				}
				if ok, msg := ConsistentWith(v, v.LiveRecords()); !ok {
					t.Fatalf("policy=%v seed=%d step=%d: %s", policy, seed, i, msg)
				}
			}
		}
	}
}

// 差分测试（小分组空间 + 高删除率）：密集触发分组创建与清理。
func TestDifferentialGroupChurn(t *testing.T) {
	for seed := int64(0); seed < 100; seed++ {
		changes := GenerateStream(GenConfig{
			Seed: seed, NumOps: 500, NumGroups: 2, MaxLive: 4, NumIDs: 6,
		})
		v := NewView(RemoveEmptyGroups)
		for i, c := range changes {
			if _, err := v.Apply(c); err != nil {
				t.Fatalf("seed=%d step=%d: %v", seed, i, err)
			}
			if ok, msg := ConsistentWith(v, v.LiveRecords()); !ok {
				t.Fatalf("seed=%d step=%d: %s", seed, i, msg)
			}
		}
	}
}
