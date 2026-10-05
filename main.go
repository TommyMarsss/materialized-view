package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
)

// demoStream 手工构造的变更流：覆盖插入、同组更新、跨组更新、删除、分组清理
func demoStream() []Operation {
	return []Operation{
		{OpInsert, Record{1, "A", 10}},
		{OpInsert, Record{2, "A", 20}},
		{OpInsert, Record{3, "B", 5}},
		{OpUpdate, Record{1, "A", 15}},  // 同组更新：撤销 10 加入 15
		{OpUpdate, Record{2, "B", 25}},  // 跨组更新：A→B，两边都要修正
		{OpInsert, Record{4, "C", 100}}, // 创建分组 C
		{OpDelete, Record{ID: 4}},       // 删除 C 唯一记录 → 分组 C 被清理
		{OpDelete, Record{ID: 1}},       // A 减员
		{OpUpdate, Record{3, "A", 7}},   // B→A 跨组移动
		{OpDelete, Record{ID: 2}},       // B 减员
		{OpDelete, Record{ID: 3}},       // A 最后一条离开 → 分组 A 被清理
	}
}

// randomStream 生成随机混合操作序列（值取整数，保证浮点求和精确可比）
func randomStream(r *rand.Rand, n int, groups []string) []Operation {
	ops := make([]Operation, 0, n)
	live := make([]int, 0, 16)
	nextID := 1
	for i := 0; i < n; i++ {
		// 无存活记录时只能插入；否则按 4:3:3 比例插/改/删
		roll := r.Intn(10)
		if len(live) == 0 || roll < 4 {
			rec := Record{nextID, groups[r.Intn(len(groups))], float64(r.Intn(100) + 1)}
			nextID++
			ops = append(ops, Operation{OpInsert, rec})
			live = append(live, rec.ID)
		} else if roll < 7 {
			idx := r.Intn(len(live))
			rec := Record{live[idx], groups[r.Intn(len(groups))], float64(r.Intn(100) + 1)}
			ops = append(ops, Operation{OpUpdate, rec})
		} else {
			idx := r.Intn(len(live))
			ops = append(ops, Operation{OpDelete, Record{ID: live[idx]}})
			live = append(live[:idx], live[idx+1:]...)
		}
	}
	return ops
}

func main() {
	var (
		out       = flag.String("out", "view-replay.html", "输出 HTML 文件路径")
		nRandom   = flag.Int("random", 40, "在演示流之后追加的随机操作数（0 表示不追加）")
		seed      = flag.Int64("seed", 42, "随机种子")
		keepEmpty = flag.Bool("keep-empty", false, "保留空分组（默认：分组最后一条记录离开时清理该分组）")
	)
	flag.Parse()

	view := NewView(!*keepEmpty)
	ops := demoStream()
	if *nRandom > 0 {
		r := rand.New(rand.NewSource(*seed))
		ops = append(ops, randomStream(r, *nRandom, []string{"A", "B", "C", "D", "E"})...)
	}

	steps, err := RunStream(view, ops)
	if err != nil {
		fmt.Fprintln(os.Stderr, "一致性校验失败:", err)
		os.Exit(1)
	}

	html, err := RenderHTML(steps, !*keepEmpty)
	if err != nil {
		fmt.Fprintln(os.Stderr, "生成 HTML 失败:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, []byte(html), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "写入文件失败:", err)
		os.Exit(1)
	}
	fmt.Printf("已处理 %d 条变更，每步均与全量重算一致。回放文件：%s\n", len(steps), *out)
}
