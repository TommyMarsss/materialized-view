# 增量物化视图维护（分组聚合）

纯 Go 标准库实现的分组聚合物化视图增量维护引擎：消费基表的 Insert / Update / Delete
变更流，增量维护每个分组的 `count / sum / avg`，并在每一步与全量重算结果比对，
最后生成一个自包含的静态 HTML 回放报告。

## 快速开始

```bash
go test ./...          # 运行全部测试（含随机差分测试）
go run . -out report.html   # 生成回放报告，浏览器打开即可
# 可选参数：-n 变更条数 -seed 随机种子 -groups 分组数 -retain-empty 保留空分组
```

## 增量维护算法

视图维护两类状态：

- `groups: 分组键 -> {count, sum}`（`avg = sum / count` 派生，不单独存储）；
- `index: 记录ID -> 当前记录`（活跃记录索引，用于校验变更的 before 镜像）。

每条变更携带 before/after 镜像（Insert 只有 after，Delete 只有 before，Update 两者都有）。
核心不变量是 **先撤销、再加入**：

- **Insert**：`add(after)` —— 目标分组 `count+1, sum+=value`；分组不存在则创建（`GROUP_CREATED`）。
- **Delete**：`retract(before)` —— 目标分组 `count-1, sum-=value`。
- **Update**：先 `retract(before)` 再 `add(after)`。
  - 同分组改值：旧值贡献被完整撤销后才加入新值，`sum` 不会虚高；
  - 跨分组移动：`retract` 作用于旧分组、`add` 作用于新分组，两侧聚合同时被修正。

变更应用前会校验 before 镜像与 `index` 中当前记录一致，不一致（重复插入、删除不存在的
记录、过期的 before 镜像）直接报错，防止静默撤销错误的贡献。

## 分组清理策略

由 `-retain-empty` / `CleanupPolicy` 控制：

- **RemoveEmptyGroups（默认）**：分组计数归零时从视图中彻底删除该分组，并发出
  `GROUP_REMOVED` 事件。清理发生在 `retract` 使 `count` 降为 0 的同一变更内，
  不会留下 `count=0` 的空条目。
- **RetainEmptyGroups**：保留 `count=0, sum=0` 的空分组（适用于下游需要稳定分组键
  集合的场景），不发出 `GROUP_REMOVED`。

## 一致性验证（黄金标准）

`ConsistentWith(view, liveRecords)` 在每一步变更后执行：对当前活跃记录集合做
**全量重算**（`Recompute`），与增量视图逐一比对：

- 重算结果中的每个分组必须存在于视图且 `count/sum` 一致（浮点容差 1e-9）；
- 视图中 `count>0` 的分组必须出现在重算结果中；
- `count=0` 的空分组仅在 RetainEmptyGroups 策略下允许存在（空分组是历史痕迹，
  全量重算无法复现，故豁免比对）。

`TestDifferentialAgainstFullRecompute` 用 200 个随机种子 × 300 步混合操作、
`TestDifferentialGroupChurn` 用小分组空间 + 高删除率密集触发分组创建/清理，
两种清理策略下逐步执行上述校验。

## HTML 回放报告

`go run .` 处理完整段变更流后输出单一静态 HTML（数据与逻辑全部内嵌，无第三方库、
无需服务）。打开后可逐步/自动回放每条变更，并高亮三类事件：

- 🟩 **分组创建**（GROUP_CREATED）
- 🟨 **聚合修正**（AGGREGATE_ADJUSTED，含更新撤销/加入、跨分组迁移两侧）
- 🟥 **分组清理**（GROUP_REMOVED）

每一步同时展示该时刻的一致性校验结果与活跃记录数。

## 项目结构

| 文件 | 职责 |
|---|---|
| `view.go` | 变更模型、增量维护引擎（add/retract）、事件、清理策略 |
| `recompute.go` | 全量重算与一致性比对 |
| `stream.go` | 确定性随机变更流生成器 |
| `report.go` | 逐步应用 + 校验 + 快照，输出报告数据 |
| `template.go` | 内嵌的单文件 HTML 回放模板 |
| `main.go` | CLI 入口 |
| `view_test.go` | 单元测试与随机差分测试 |
