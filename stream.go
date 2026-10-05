package main

import (
	"fmt"
	"math/rand"
)

// GenConfig 控制随机变更流生成。
type GenConfig struct {
	Seed      int64   // 随机种子，保证可复现
	NumOps    int     // 变更条数
	NumGroups int     // 分组键空间大小（小空间迫使分组反复创建/清理）
	MaxLive   int     // 活跃记录数上限，超过后提高删除/更新概率
	NumIDs    int     // ID 空间大小
}

// GenerateStream 生成一条确定性的随机 Insert/Update/Delete 混合变更流。
// 生成的变更保证对空基表合法：只更新/删除当前活跃记录。
func GenerateStream(cfg GenConfig) []Change {
	rng := rand.New(rand.NewSource(cfg.Seed))
	groups := make([]string, cfg.NumGroups)
	for i := range groups {
		groups[i] = string(rune('A' + i))
	}
	randValue := func() float64 {
		// 保留两位小数，避免浮点噪声干扰展示与比对
		return float64(rng.Intn(20001)-10000) / 100
	}
	randGroup := func() string { return groups[rng.Intn(len(groups))] }

	live := make(map[string]Record)
	var changes []Change
	for len(changes) < cfg.NumOps {
		// 根据活跃记录数动态调整操作概率
		insertW, updateW, deleteW := 5, 3, 2
		if len(live) == 0 {
			updateW, deleteW = 0, 0
		}
		if len(live) >= cfg.MaxLive {
			insertW, updateW, deleteW = 1, 3, 4
		}
		total := insertW + updateW + deleteW
		roll := rng.Intn(total)

		switch {
		case roll < insertW:
			id := fmt.Sprintf("r%03d", rng.Intn(cfg.NumIDs))
			if _, taken := live[id]; taken {
				continue
			}
			rec := Record{ID: id, Group: randGroup(), Value: randValue()}
			live[id] = rec
			changes = append(changes, Change{Op: OpInsert, After: &rec})

		case roll < insertW+updateW:
			id, rec := pickLive(rng, live)
			_ = id
			before := rec
			after := rec
			// 50% 概率改分组键（触发跨分组修正），否则只改值
			if rng.Intn(2) == 0 {
				after.Group = randGroup()
			}
			after.Value = randValue()
			live[after.ID] = after
			changes = append(changes, Change{Op: OpUpdate, Before: &before, After: &after})

		default:
			_, rec := pickLive(rng, live)
			before := rec
			delete(live, rec.ID)
			changes = append(changes, Change{Op: OpDelete, Before: &before})
		}
	}
	return changes
}

func pickLive(rng *rand.Rand, live map[string]Record) (string, Record) {
	keys := make([]string, 0, len(live))
	for k := range live {
		keys = append(keys, k)
	}
	k := keys[rng.Intn(len(keys))]
	return k, live[k]
}
