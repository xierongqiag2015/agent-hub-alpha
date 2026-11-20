# 旧任务两条升级路径的逐项对照（2026-11-20）

对应 #30 处置决定第 1 条：在旧数据副本上分别跑“首次升级”和“已被旧候选阻断后的再次升级”，逐项比对原任务、终态、产物摘要及回执。

- 数据：W05 D2 环境 `agent-hub-issue16`（41a3675）的 credit 数据目录，只读拷贝，16 个任务（11 done、3 waiting、2 failed）。旧采纳请求均为 `adoption_version 0.1.0`，账本模型 `glm-5-2-260617`。
- 代码：
  - 旧候选：e1961fe8 的 Go 代码。使用的工作区在 7de8c55，它在 e1961fe8 之上只改了 Java，`agent-festival/go` 下与 e1961fe8 的差异为 0 行。
  - 本 PR：fork 分支 `blocked-reason` 的 `5fb1c7c`，即 a7e1f93 合入候选 `2dc971b1` 之后再加恢复与处置记录。
- 环境：WSL2 Ubuntu 24.04，Go 1.25。诊断测试要在 `cmd/credit-demo` 目录下运行，因为账本 schema 按当前目录查找；在别处运行，所有账本都会被判为不可读。

## 步骤

1. **旧候选**：e1961fe8 对一份拷贝执行启动对账，结果留作“已被旧候选阻断”的数据（`TestReconUpgradeE196`）。
2. **首次升级**：本 PR 对原始数据的另一份拷贝执行启动对账，连续两次。
3. **再次升级**：本 PR 对第 1 步的结果执行启动对账，同样连续两次。

两条路径都检查第二次重启不改写状态（Rev 不变）。逐任务比对的内容：状态，以及最终输出、节点产物、决策、`decision_op`、`business_request`、回执、回执状态、采纳账本文件这几项的摘要。

## 结果

| 任务 | 41a3675 原状态 | e1961fe8 后 | 首次升级 | 再次升级 | 最终输出 / 节点产物 / 决策 / 回执 / 采纳账本 | 首次升级处置 | 再次升级处置 |
|---|---|---|---|---|---|---|---|
| R1 | done | blocked | done | done | 与原始一致 | FLAGGED / LEGACY_ADOPTION_PROTOCOL | RESTORED / LEGACY_ADOPTION_PROTOCOL |
| R2 | done | blocked | done | done | 与原始一致 | FLAGGED / LEGACY_ADOPTION_PROTOCOL | RESTORED / LEGACY_ADOPTION_PROTOCOL |
| R3 | done | blocked | done | done | 与原始一致 | FLAGGED / LEGACY_ADOPTION_PROTOCOL | RESTORED / LEGACY_ADOPTION_PROTOCOL |
| R4 | done | blocked | done | done | 与原始一致 | FLAGGED / LEGACY_ADOPTION_PROTOCOL | RESTORED / LEGACY_ADOPTION_PROTOCOL |
| R5 | done | blocked | done | done | 与原始一致 | FLAGGED / LEGACY_ADOPTION_PROTOCOL | RESTORED / LEGACY_ADOPTION_PROTOCOL |
| R6 | waiting | waiting | waiting | waiting | 与原始一致 | — | — |
| R7 | waiting | waiting | waiting | waiting | 与原始一致 | — | — |
| R8 | failed | failed | failed | failed | 与原始一致 | — | — |
| R9 | done | blocked | done | done | 与原始一致 | FLAGGED / LEGACY_ADOPTION_PROTOCOL | RESTORED / LEGACY_ADOPTION_PROTOCOL |
| R10 | done | blocked | done | done | 与原始一致 | FLAGGED / LEGACY_ADOPTION_PROTOCOL | RESTORED / LEGACY_ADOPTION_PROTOCOL |
| R11 | failed | failed | failed | failed | 与原始一致 | — | — |
| R12 | done | blocked | done | done | 与原始一致 | FLAGGED / LEGACY_ADOPTION_PROTOCOL | RESTORED / LEGACY_ADOPTION_PROTOCOL |
| R13 | waiting | waiting | waiting | waiting | 与原始一致 | — | — |
| R14 | done | blocked | done | done | 与原始一致 | FLAGGED / LEGACY_ADOPTION_PROTOCOL | RESTORED / LEGACY_ADOPTION_PROTOCOL |
| R15 | done | blocked | done | done | 与原始一致 | FLAGGED / LEGACY_ADOPTION_PROTOCOL | RESTORED / LEGACY_ADOPTION_PROTOCOL |
| R16 | done | blocked | done | done | 与原始一致 | FLAGGED / LEGACY_ADOPTION_PROTOCOL | RESTORED / LEGACY_ADOPTION_PROTOCOL |

**e1961fe8 的行为**：11 个 done 任务被阻断，最终输出被替换成“RECONCILIATION_REQUIRED: 采纳账本与快照操作序列不一致……”。它留下对账前快照 `reconciliation-754187da….json`。这份快照的内容摘要和首次升级时的对账前状态相同，都是原始数据。

**再次升级的依据**：11 个任务都从这份快照恢复，处置记录的 `source` 写的就是它。恢复的前提有两条：
- 快照中该任务为 done 或 failed；
- 除阻断会改动的字段（状态、最终输出、令牌、阻断原因、对账标记）外，任务记录与快照完全一致。

两个条件都满足才恢复。没有快照，或快照之后记录有其他变化，任务保持 blocked，标为 `PENDING_REVIEW`（待核对）。这两种情况本数据里没有出现，由 `TestReconciliationKeepsOlderBlockWithoutEvidence` 覆盖。

**恢复后的任务不会领取执行权**：普通领取和恢复都不放行，由 `TestReconciliationRestoresRunBlockedByOlderBuild` 覆盖。waiting 任务没有采纳记录，两条路径都不受影响。

## 文件

- `upgrade-paths.json`：16 个任务各四种视图，依次是原始、e1961fe8 后、首次升级、再次升级。每种视图包含状态、上述各项摘要、最终输出前 60 字、`blocked`/`reconciliation`。
- `first-disposition-754187da….json`：首次升级的处置记录，11 条，均为 `done→done FLAGGED`，来源是各自的 `runs/<id>/adopt.jsonl`。
- `second-disposition-2e4d69cf….json`：再次升级的处置记录，11 条，均为 `blocked→done RESTORED`，来源是 `reconciliation-754187da….json`。
- `console.log`：两段诊断的输出。
- `recon_upgrade_local_test.*.go.txt`、`recon_two_paths.sh.txt`：诊断代码和运行脚本，未提交到产品分支，供复跑。

对账前快照 `reconciliation-*.json` 是完整的旧 state，可能含租约令牌，所以只列文件名，不入库。

## 未覆盖

- W02 环境的 R1—R13 数据 W05 拿不到，只在 W05 自己的 41a3675 数据上验证了同一类情形。W02 环境若保留 e1961fe8 生成的对账前快照，升级后结果应与上表“再次升级”一列相同；若快照已不在，任务保持 blocked，标为待核对。
- 不迁移旧协议数据：0.1.0 的采纳记录仍然不经当前回放判定，只按 `LEGACY_ADOPTION_PROTOCOL` 单列。
