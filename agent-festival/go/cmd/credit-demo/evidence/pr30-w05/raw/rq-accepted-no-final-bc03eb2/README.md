# #30：远端已受理、暂时没有最终响应时零重发（2026-11-23，统一候选 bc03eb2）

要求原文（[#30 xingchao 11-23](https://github.com/chaosxingxc-orion/agent-hub-alpha/pull/30#issuecomment-7089539066) 第 1 条）：W05 在接收环境构造一次远端受理后暂时没有最终响应的任务。
- 固定任务、节点、调用及远端关联标识，保存首次发送前后的 Gateway 记录；
- 经过超时、刷新及恢复，沿原身份查询；
- 结果仍未知时维持待对账，Gateway 中该业务动作新增发送数必须为 0；
- 另列查询次数；如后续收到原响应，核对仅提交一份正式结果。

## 环境

| 项目 | 内容 |
|---|---|
| 代码 | 统一候选 `bc03eb2315ef6b32b4f5df9ac55a0c393967c77a`（#60 已合入 `67fd5b05`） |
| 镜像 | orchestrator `a22cf20c3fdf`、credit `19d21bc61b82`、professional `d382af7d0050`，在 W05 环境本地构建（`run/wsl_bc03_build.sh`）；RDC `315186974da9`、Gateway `cac5d07658ae`（agent-solution `bf702bc`） |
| 部署 | WSL2 Ubuntu 24.04 + Docker Desktop，独立 compose 项目 `agent-hub-bc03`（credit 127.0.0.1:5393）；其他项目未动 |
| 链路 | 真实 RDC/Gateway（DIRECT）和三个专业 Agent。编排器经故障代理访问 Gateway，代理按 JSON-RPC id 记录每次 SendMessage/GetTask |
| 数据与模型 | 合成授信案例 C006（只生成模拟报告，不提交银行操作）；模型 glm-5-2-260617 |
| 负责人与时段 | W05 xierongqiag2015，脚本驱动；2026-11-23 10:23:03—10:34:20（+08:00） |
| 入口 | 与插件相同：`/mcp` 规划、`/api/approve`、`/api/resume`、`/api/graph`、`/api/plans`、登录后的 `/ui/*` |

## 构造方法

故障代理新增 `mask-next-send` 模式（`scripts/w05_fault_proxy.py`）。只改调用方看到的回执，Agent 和 Gateway 都是真实的：
- 下一个 SendMessage 原样转发、原样回执，记住它的远端任务号；
- 之后对该任务的查询照常转发给真实 Gateway。上游已是终态时，回给调用方的状态改为 `TASK_STATE_WORKING`、去掉产物，同时记录上游的真实状态和产物摘要（算法同 Go `CommitCall`）；
- 解除后，查询拿到的就是真实 Gateway 的原回执。

本次被屏蔽的是任务 R1 的第一个远端节点 `intake`。专业 Agent 在受理后约 12 秒就完成了，此后调用方一直看不到结果。

固定的调用身份：

| 字段 | 值 |
|---|---|
| node_execution_id | `sha256:ca07dde0…8f11a0` |
| attempt / input_digest | `1` / `sha256:c3df88ac…aecf7` |
| request_id（JSON-RPC id） | `aa2f77741d77c401fee61b4c` |
| message_id / context_id | `0d5a4119981140c06d93314f` / `ctx-6abaf36d72127a711fa870db` |
| remote_task_id | `2a17830a-b6d6-4ea3-b2f8-aee1f2ec9bb3` |

## 结果

| 阶段 | 时间 | 任务 / 节点 | 该调用发送 | 该调用查询 | Gateway 审计 SendMessage |
|---|---|---|---|---|---|
| 首次发送前 | 10:24:06.4—07.0 | 规划、批准 | 0 | 0 | 1（即首次发送，见注） |
| 首次发送 + 调用方等待 4 分钟 | 10:24:07—10:28:16 | running → **waiting / unknown** | **1** | 235（其中 224 次上游已完成、被屏蔽） | 0 |
| 刷新：两个已登录会话，5 轮读遍 `/api/graph`（按任务、按计划）、`/api/plans`、`/ui/`、`/ui/plan/P1/v1`、`/ui/run/R1`，共 60 次，均为 200 | 10:28:16 | waiting / unknown，状态版本 rev 245 不变 | 0 | 0 | 0 |
| 恢复（一）：重启 credit 和编排器 | 10:28:16—10:28:59 | waiting / unknown，rev 245 不变 | 0 | 0 | 0 |
| 恢复（二）：结果仍被屏蔽时 `/api/resume` | 10:28:59—10:33:09 | 编排器沿原远端任务号查询，再等 4 分钟后回到 **waiting / unknown** | **0** | 236（全部被屏蔽） | 0 |
| 解除屏蔽后 `/api/resume` | 10:33:09—10:34:17 | 第 1 次查询即拿到原回执并提交，`intake` done，下游执行，任务 done | **0** | 1 | 4（下游 4 个节点各 1 次，请求号各不相同） |

- **零重发**：该调用全程只发送 1 次。从首次发送到解除屏蔽，共 9 分钟，经历超时、刷新、重启、再恢复、再超时，Gateway 审计没有任何 SendMessage。
- **身份不变**：5 个阶段的 node_execution_id、attempt、input_digest、request_id、message_id、context_id、remote_task_id 完全一致。待对账原因为“远端结果待核对：Remote task requires waiting or input: TASK_STATE_WORKING；未重发”。
- **只提交一份正式结果**：屏蔽期间上游回执的产物摘要始终只有一个，`sha256:6b6cc8f6…61906`。解除后 Go 提交的 `artifact_digest` 就是它，`artifact_id` 为 `artifact-ca07dde0…`。远端只产出一份结果，调用方也只提交了这一份。
- **Go 侧记录**：该调用的 `gateway_exchanges` 共 473 条，其中 SendMessage 1 条、GetTask 472 条，与代理记录一致。

注：
- Gateway 在收到请求时写审计，代理在收到回执后写事件，两者相差 0.1—0.2 秒。“首次发送前”窗口里的那 1 次，就是首次发送本身：Gateway 时间 10:24:06.816，代理时间 10:24:07.002。全程 Gateway 共 5 次 SendMessage，与代理的 5 次逐一对应，见 `timeline.txt`。
- Gateway 的 GetTask 比该调用多出约每分钟 6 次，来自 Gateway 容器的健康检查（`deploy/bus/health_gateway.py` 每 10 秒查询一次不存在的任务）。

## 文件

- `rq-accepted-no-final-1795400646.json`：场景证据，包括各阶段调用快照、`gateway_exchanges`、窗口计数、检查结果、状态变化时的图快照。已脱敏。
- `fault-events.jsonl`：代理记录的全部 SendMessage/GetTask 事件，带上游真实状态和产物摘要。
- `gateway.log`：Gateway 容器日志，主体标识已脱敏。`credit.log`、`orchestrator.log`、`material-agent.log`、`run.log`、`scenario.out` 为运行日志。
- `timeline.txt`：时间点、窗口计数，以及 Gateway 与代理的 SendMessage 对照。
- `run/`：构建和运行脚本，以及镜像覆盖配置。场景脚本见 `scripts/w05_rd_extra.py rq-accepted-no-final`。

## 未覆盖

- 本次是传输层屏蔽，远端实际在 12 秒内已完成。远端长时间真的不结束（例如模型调用挂起）没有单独构造；调用方看到的现象相同，处理路径也相同。
- 授权撤销对照（#30 第 2 条，W04/W05）另行提交；年报链的 HUMAN 任务另见 #69。
- W07 独立复核尚未进行。
