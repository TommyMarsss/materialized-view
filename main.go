package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	var (
		n      = flag.Int("n", 60, "变更条数")
		seed   = flag.Int64("seed", 42, "随机种子")
		groups = flag.Int("groups", 4, "分组键空间大小")
		maxLive = flag.Int("max-live", 12, "活跃记录数上限")
		ids    = flag.Int("ids", 20, "记录 ID 空间大小")
		retain = flag.Bool("retain-empty", false, "分组清空后保留空条目（默认彻底移除）")
		out    = flag.String("out", "report.html", "输出 HTML 文件路径")
	)
	flag.Parse()

	policy := RemoveEmptyGroups
	if *retain {
		policy = RetainEmptyGroups
	}

	changes := GenerateStream(GenConfig{
		Seed: *seed, NumOps: *n, NumGroups: *groups, MaxLive: *maxLive, NumIDs: *ids,
	})

	report, err := BuildReport(policy, changes)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := WriteReport(*out, report); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("applied %d changes, policy=%s, consistency: all steps match full recompute\n",
		len(changes), policy)
	fmt.Printf("report written to %s\n", *out)
}
