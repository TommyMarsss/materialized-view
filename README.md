# 增量物化视图维护（Incremental Materialized View Maintenance）

纯 Go 标准库实现的分组聚合视图（`GROUP BY group` 上的 `COUNT / SUM / AVG`）增量维护器：给定基表的插入 / 更新 / 删除变更流，逐条增量修正聚合结果，并在每一步与全量重算比对验证一致性。处理完变更流后生成**单一静态 HTML 文件**（数据与逻辑全部内嵌，零依赖），在浏览器中逐条回放变更对视图的影响。

## 快速开始

```bash
go test ./...                 # 运行全部测试（含随机差分测试）
go run .                      # 处理演示+随机变更流，生成 view-replay.html
go run . -keep-empty          # 使用"保留空分组"策略
go run . -random 200 -seed 7  # 追加 200 条随机操作，指定种子
open view-replay.html         # 浏览器中回放（←/→ 方向键步进）
```

## 一、增量维护算法

视图内部维护两份状态：

- `records`：基表镜像（`id → record`），用于在更新/删除时取回**旧值**；
- `groups`：分组聚合（`group → {count, sum}`），`avg` 由 `sum/count` 派生。

三类操作的处理：

| 操作 | 增量动作 |
|------|----------|
| **INSERT** | 将新记录的贡献加入其分组（`count+1, sum+value`）；分组不存在则创建。 |
| **UPDATE** | **先撤销旧值的贡献，再加入新值的贡献**（`update = delete(old) + insert(new)`）。若分组键改变（A→B），撤销发生在旧分组 A、加入发生在新分组 B，两个分组都被正确修正。绝不能把新值直接累加——那会使 sum 虚高。 |
| **DELETE** | 撤销该记录的贡献，从基表镜像中移除。 |

每次贡献变更都会产生一个事件（`group_created` / `group_adjusted` / `group_removed` / `group_kept`），记录分组、前后聚合值与说明，供回放高亮。

## 二、分组清理策略

当一条记录（因删除或更新移出）使某分组 `count` 降为 0 时，由构造参数 `RemoveEmptyGroups` 控制：

- **`true`（默认，remove-empty）**：分组从视图中**完全移除**，不产生 `count=0` 的残留条目；事件为 `group_removed`。
- **`false`（keep-empty）**：保留 `count=0, sum=0` 的空条目（sum 显式归零以消除浮点残差）；事件为 `group_kept`。后续向该分组的插入会"复活"它。

命令行用 `-keep-empty` 切换。

## 三、一致性验证（黄金标准）

`CheckConsistency()` 在**每一步操作之后**执行：对当前有效记录集合从零全量重新聚合（`FullRecompute`），与增量维护的视图逐分组比对（count 精确相等，sum 允许 1e-9 浮点误差；测试中的值取整数，浮点求和精确）。任何一步发散即报错并指出步号与操作——这是增量视图正确性的黄金标准。keep-empty 策略下，全量重算会补上历史上出现过、当前为空的分组键再比对。

`RunStream` 对整段变更流执行此校验并收集每步快照，最终渲染为回放 HTML。

## 四、HTML 回放

`view-replay.html` 为单一静态文件：变更流各步的操作、事件、视图快照、基表镜像与一致性结论全部以 JSON 内嵌，回放逻辑为原生 JavaScript，无任何框架/图形库/网络请求。功能：

- 上一步/下一步/播放/进度条/键盘 ←→ 逐条回放；
- 每步显示一致性徽标（✓ 与全量重算一致）；
- 视图表格按事件类型高亮：**分组创建**（绿）、**聚合修正**（黄）、**分组清理**（红，被清理分组以删除线行展示）、**空分组保留**（灰）；
- 事件面板列出该步全部维护事件及前后聚合值变化。

## 五、测试覆盖（`view_test.go`）

- `TestInsertCreatesGroupAndAccumulates` — 插入的聚合累加与分组创建事件；
- `TestUpdateSameGroupCorrectsAggregate` — 同组更新先撤销旧值再加入新值，sum 不虚高；
- `TestUpdateGroupKeyChangeAdjustsBothGroups` — 跨组更新（A→B）两边聚合都正确修正；
- `TestDeleteRemovesEmptyGroup` / `TestDeleteKeepsEmptyGroupWhenConfigured` — 两种策略下的分组清理时机；
- `TestUpdateMovingLastRecordCleansUpOldGroup` — 更新移走最后一条记录触发旧分组清理；
- `TestInvalidOperationsRejected` — 重复插入、更新/删除不存在记录均报错且不污染状态；
- `TestDifferentialRandomStreams` — **差分测试**：200 个种子 × 300 步随机混合操作 × 两种清理策略，每步与全量重算比对；
- `TestRunStreamAllStepsConsistent` — 整段流每步一致性标志均为真。

## 项目结构

| 文件 | 职责 |
|------|------|
| `view.go` | 视图核心：操作应用、贡献增删、清理策略、全量重算与一致性校验 |
| `replay.go` | 变更流执行（逐步校验）与单文件 HTML 回放渲染 |
| `main.go` | CLI：演示流 + 随机流生成，输出回放文件 |
| `view_test.go` | 单元测试与随机差分测试 |
