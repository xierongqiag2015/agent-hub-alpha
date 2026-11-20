# #30 处置决定第 2 条：从真实派发入口验证（2026-11-20，PR #60 头 5fb1c7c）

要求原文（[#30 决定](https://github.com/chaosxingxc-orion/agent-hub-alpha/pull/30#issuecomment-7029993473)）：W04/W05 从真实派发入口验证以下几点，附重启前后回执、对账结果与发送次数，由 W07 独立复核：
- 读取历史报告不会领取执行权；
- 有未解决冲突时拒绝派发；
- 获准的后续动作重新核对当前权限、修订与证据；
- 账本已采纳而快照写入失败、已发出而回执未知这两种情况，均保留原操作或请求身份，不得重新生成已采纳决定，不得盲目重发。

## 环境

| 项目 | 内容 |
|---|---|
| 代码 | fork `blocked-reason` `5fb1c7c`（PR #60 头 = 候选 `2dc971b1` + 旧任务处置） |
| 镜像 | orchestrator `10da4a5b8cb0`，credit `2120f0ad721e`，professional `7e639549a02d`；编排器带 SDK 补丁 0.1.3-aha-drain1 |
| 部署 | WSL2 Ubuntu 24.04 + Docker Desktop。独立 compose 项目 `agent-hub-5fb1`（credit 127.0.0.1:5392）、`agent-hub-m14b`（5291），W05 D2 环境 `agent-hub-issue16` 未动 |
| 链路 | 真实 RDC/Gateway（DIRECT）和三个专业 Agent；编排器经故障代理访问 Gateway，代理按 JSON-RPC id（即 request_id）记录每次 SendMessage/GetTask |
| 数据与模型 | 合成授信案例 C006（仅生成模拟报告，不提交银行操作）；模型 glm-5-2-260617（AR-04 的 B 模型为 glm-4-flash） |
| 操作者 | W05（xierongqiag2015），脚本驱动；入口与插件相同：`/mcp` 规划、`/api/approve`、`/api/resume`、`/api/graph`、`/api/plans` 和登录后的 `/ui/*` 页面 |

## 结果

| 要求 | 场景 | 结果 |
|---|---|---|
| 读取历史报告不会领取执行权 | `history-read-no-lease` | 任务 R4 完成后，两个已登录会话对 `/api/graph`、`/api/plans`、`/ui/`、`/ui/plan/P4/v1`、`/ui/run/R4` 并发读取 5 轮，共 50 次，均为 200；之后重启 credit 和编排器。读取前、读取后、重启后：状态 done、epoch 1、租约 1、令牌为空、状态版本 rev 229，均不变；5 个远端请求各发送 1 次，无新发送 |
| 账本已采纳而快照写入失败 | `adopt-snapshot-fail` | 判断节点 running 时，把 `state.json` 换成同名目录，使原子改名失败。约 30 秒后判断提交：账本写入 `op-R2` 的采纳事件，快照写入失败，服务冻结（503）。还原文件并重启 credit 和编排器后：<br>· R2 为 blocked，投影 `RECONCILIATION_REQUIRED / LEDGER_SNAPSHOT_MISMATCH / BLOCKED`，来源 `runs/R2/adopt.jsonl`；<br>· 账本字节在重启前后一致，原操作号 `op-R2` 保留；<br>· 决策未重新生成，判断节点未再执行；<br>· 处置写入 `disposition-*.json`；4 个远端请求各发送 1 次 |
| 有未解决冲突时拒绝派发 | 同上（R2） | 对账未通过的 R2：`/api/resume` 返回 409；重启后等待 30 秒以上，没有被领取，租约数不变 |
| 已发出而回执未知 | `rb-send-reply-lost`、`rb-send-reply-lost-restart` | intake 的 SendMessage 已转发到真实 Gateway，上游任务已建，但回执丢失，节点为 UNKNOWN、任务 waiting。<br>· 第一个场景：恢复后仍 waiting，请求号不变，发送次数 1；<br>· 第二个场景：重启 credit 和编排器后仍 waiting，请求号 `d0ab8fc7…` 前后一致，再恢复也不重发（发送次数 1），节点保持未解决，等人工对账 |
| 获准的后续动作重新核对当前修订与证据（规约版本、执行模型） | AR-04 `m3`、`m1-local`、`m1-remote`、`m2`（以 5fb1c7c 为新版本复跑） | `m3`：41a3675 镜像上执行中的任务升级到 5fb1c7c 后被阻断，原因写明规约版本不可解析；之后没有新领取、新模型请求或新远端调用。<br>`m1-local`：任务绑定 A、编排器为 B，22 次领取均被拒，原因写明两个模型名，任务保持排队，零模型请求。<br>`m1-remote`：远端执行端为 B，任务在调用模型前被阻断，零模型请求。<br>`m2`：全部为 A 时完成，19 次模型请求均为 A |

每个场景的证据 JSON 中都有：
- 重启前后的调用快照：请求号、远端任务号、产物摘要、模型事件；
- 状态和租约；
- 传输层的发送计数；
- 对账处置：`adopt-snapshot-fail` 中的 `dispositions`。

## 未覆盖

- **权限（grants）在后续动作时的复核**：本轮没有单列真实入口场景。现有机制和覆盖：派发外部动作时，按当前计划的场景重新取授权（`liveGrants`），交给采纳状态机核对；有组件测试，例如 #45 的对账测试。
- **W02 环境的同类验证、W07 独立复核**：尚未进行。
- **模型与数据**：用的是合成授信数据和 GLM 模型，没有覆盖年报链。年报链的派发入口由 W04 的 Go/PG 控制，另行验证。
- **“已发出而回执未知”的收尾**：上游任务已建成而 Go 侧没有远端任务号，这时只保留原请求、不重发，上游孤儿任务需人工对账；本轮没有自动收敛。

## 文件

| 目录 | 内容 |
|---|---|
| `history-read-no-lease/`、`adopt-snapshot-fail/`、`rb-send-reply-lost/`、`rb-send-reply-lost-restart/` | 证据 JSON（已去除令牌与口令）、`fault-events.jsonl`（传输层事件）、`run.log`（镜像与时间）、`scenario.out`、`credit.log`（Go 日志） |
| `ar04-m14/` | AR-04 证据 JSON、`experiment.log`、`credit.log`、`model-egress.jsonl`（只有模型名、调用方和状态） |
| 运行方式 | 场景脚本 `../../scripts/w05_rd_extra.py`（本轮新增 `rb-send-reply-lost-restart`、`adopt-snapshot-fail`、`history-read-no-lease`），AR-04 脚本 `../../scripts/ar04_m14_experiment.py` |
