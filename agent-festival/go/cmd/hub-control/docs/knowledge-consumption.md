# 知识消费（K 路径 / F-02C）在 hub-control 中的实现

本文对应的设计出处：
- [#4 正文](https://github.com/chaosxingxc-orion/agent-hub-alpha/issues/4)中的 K 路径："新任务使用 K 仍提交自己的 R(o)；失效禁止后续消费，不改写已发生事实"；
- [#14 评审回复](https://github.com/chaosxingxc-orion/agent-hub-alpha/pull/14#issuecomment-6824366971)里的 F-02C"新任务知识消费快路径"；
- [#16](https://github.com/chaosxingxc-orion/agent-hub-alpha/issues/16) 的 A10（跨任务资料复用、材料版本变更）和 A08（引用依据）。

```text
Tnew ≠ Told
ReuseCandidate = Kθ(输入, 版本, 来源, 保鲜, 权限, 适用性)
AdoptForNewTask(ReuseCandidate) 仍须满足本任务检查/授权
```

## 1. 逐项对应

| F-02C 要素 | 实现 | 位置 | 测试 |
|---|---|---|---|
| 输入、版本 | 节点输入摘要 κ_in 覆盖能力定义、声明的来源（快照 id + SHA-256）、指令和模板。只有 κ_in 完全一致才命中；换材料版本或换指令都不命中 | `exec.go` Claim、`knowledge.go` lookup | `TestK_MissReasonsAreSpecific` |
| 来源 | 节点必须声明它消费的来源，记入 `node_sources`；复用时写入 `reused_from`，并能追溯到最初计算的那个结果 | `knowledge.go` checkSources、origin | `TestK_HitRecordsSourceAndSavedCalls` |
| 保鲜 | 策略规定复用时限 `reuse_max_age_hours`；另外可以撤回某个来源或某个结果 | `knowledge.go` lookup、Revoke | `TestK_MissReasonsAreSpecific`、`TestK_Revoked*` |
| 权限 | 声明的来源必须在任务获批时的材料清单里，且已登记、未撤回；否则拒绝认领 | `knowledge.go` checkSources | `TestK_SourcesMustBeApprovedAndDeclared` |
| 适用性 | 按精确等价判定：能力定义、来源和指令都一致才算适用；能力必须在策略里声明 `reusable`。语义层面的适用性判断留给 #6 能力知识服务 | `policy.go` Capability.Reusable | `TestK_HitRecordsSourceAndSavedCalls`（不参与的节点） |
| 记录来源与省下的调用 | 每次查找都记下命中/未命中/不参与和原因；命中时统计最初计算节省的远端调用次数 | `knowledge_lookups` 表、savedCalls | `TestK_HitRecordsSourceAndSavedCalls` |
| 不沿用批准和效果身份 | 复用的结果只成为本任务节点 o 的正式结果；检查按产物摘要，交付批准按任务，各自重新做；远端调用 b 不复用 | `delivery.go` Accept | `TestA10`、`TestK_RevokedSourceBlocksDeliveryButNotHistory` |
| 查到之后、提交之前失效 | 提交复用结果时再查一次是否撤回，撤回了就拒绝 | `exec.go` Commit | `TestK_RevokedBetweenLookupAndCommit` |
| 失效禁止后续消费，不改写已发生事实 | 撤回之后：不能再被新节点消费、不能再启动新任务；依赖它且尚未交付的报告门禁变为 `stale_basis`，禁止交付；已有结果和已交付的报告保持不变，只标出警示 | `knowledge.go` taskBasisRevoked、`delivery.go` gate | `TestK_RevokedSourceBlocksDeliveryButNotHistory` |

## 2. 未命中原因

控制台和 `GET /v1/tasks/{id}` 中每个节点都带 `lookup.reason`，取值如下：

- 命中："输入、来源版本与能力定义完全一致，可复用"
- 未命中，分四种：
  - "没有其他任务消费过这些来源"
  - "已有结果基于同一材料的其他版本，版本不同不复用"
  - "已有结果使用了相同来源，但指令、模板或能力定义不同，不适用"
  - "已有结果超出保鲜期""已有结果不可用：来源 … 已撤回"
- 不参与："能力 … 未声明可跨任务复用，按首次计算处理"

## 3. 撤回

- 接口：`POST /v1/commands/revoke {command_id, ref, reason}`。`ref` 写成 `material:<id>@<sha256>` 或 `result:<o>`。
- 权限：需要 `curate` 角色，或者是管理员。撤回必须写原因，并按命令去重。
- 控制台："知识来源"页列出所有材料版本和可复用的结果，有 `curate` 角色的账号可以直接撤回。

## 4. 与知识消费工具的对接点

这一节是后续工作，尚未实现：

1. **来源登记**：W01 的 `source.json`（快照 id、SHA-256、页数）直接对应本模块登记的材料。知识消费工具管理的知识包或知识库版本，也可以按同样的 `id@digest` 形式登记为来源。
2. **引用依据（A08）**：报告中每条结论的 citation（PDF 页、印刷页、原文行）由 W06 的引用检查器核对，检查结论绑定报告摘要。知识消费工具那套"检索 → 读原文 → 带证据回答"的流程，可以作为引用检查器的一种实现接入 `POST /v1/checks`。
3. **语义适用性**：目前只按精确等价判定。要做"同一问题的近似表述也能复用"，需要 #6 能力知识服务给出适用性声明；接入之后，仍须满足本表的权限、保鲜和撤回约束。
