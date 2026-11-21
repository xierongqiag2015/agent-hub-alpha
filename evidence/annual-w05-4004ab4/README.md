# W05 年报链单侧记录：`4004ab4`（2026-11-20）

W05（xierongqiag2015）在自己的接收环境中，把 W03/W04 的 D2 修复交接点 `4004ab4` 从真实对话跑到**正式交付**（11-21 由用户批准、交付、下载）。这是单侧记录，不代替 D2 联合验收、W06 业务验收或 W07 独立复核。

## 接收对象与版本

| 项 | 值 |
|---|---|
| W03/W04 交接点 | `4004ab4d1e9f5507ac05ce0dc1dd7b9a7e978edb`（[#16 决定](https://github.com/chaosxingxc-orion/agent-hub-alpha/issues/16#issuecomment-7029994184)：D2 修复交接点；D1 仍以 0358acb 为准） |
| 相对 00d3af7 的增量 | 批准与交付回执复用、受阻原因投影、远端观察跨控制面故障保留、人工核验先于分支执行；专家代码未变，无数据库迁移 |
| WorkSwarm / agent-solution | `1f168664…` / `eb48c482…`（与 0358acb 记录相同） |
| execution_profile | `sha256:7347e6987981a8804e30958a753bdf1da08d4d2fea1757f8fce1326bfcad63d1` |
| model_binding_digest | `sha256:8f336627d4812aa1c2b4bd1651af9d0264a04cd55e518a2ae8db8514c70ceefd`（deepseek-v4-1-flash-260910，与升级前相同） |
| 制品 sha256 | `report-orchestrator.jar` `f99a09780a28dd3f…`；`report-experts.jar` `3e165813191c726c…`（与 0358acb 相同）；`report-control` `934bcee964d67396…` |

- **环境**：WSL2 Ubuntu 24.04，按 LINUX-RUN 原生运行，PG16 容器。升级在 12:42–12:45（UTC+8）完成：离线构建 control、编排器和插件，按归属检查停服，重新绑定，重启。之后 `validate_services` 和 `identity_probe` 都是 all_passed。
- **环境事件**（如实登记）：
  - 09:15–10:57：W05 脚本把规划助手误设为 GLM，这段时间没有规划任何任务；
  - 10:57：Docker Desktop 与 Ubuntu 的集成断开，发行版空闲时被自动关闭，服务停止，11:08 恢复；
  - 11:25–11:45：WSL 迁移出 C 盘，之后恢复；
  - 11:13：在 00d3af7 上规划过候选 T-52b9df74…，交接点改为 4004ab4 后作废，从未准入，远端调用为 0。

## 任务 T-824c5bf97f244563f9cd9e2a4579302e

| 时间（UTC+8） | 事件 | 操作者 |
|---|---|---|
| 12:46:53 | 规划助手读取能力目录，写入选择记录 `selection-d535ace2…`（摘要 `af9264fe…`） | W05 自动化（只提交对话） |
| 12:47:01 | PROPOSED：候选图 8 节点，摘要 `5c227314…fbc6`；AWAITING_APPROVAL，远端调用 0 | 系统 |
| 14:07:14.243 | ADMITTED：在编排台核对后批准 | **用户 xierongqiag2015** |
| 14:07:15.7 → 14:07:46.3 | extract → report-extractor | 系统 / Bus |
| 14:07:46.4 → 14:08:09.3 / 14:09:01.2 | business → business-analyst；quality → asset-quality-analyst（并行） | 系统 / Bus |
| 14:09:01.3 → 14:09:35.5 | draft0 → report-writer | 系统 / Bus |
| 14:09:35.593 | check0 = PASS，failures 为空；revise1、check1 按图授权跳过；select_report 选中 draft0 | 系统 |
| 14:09:35.648 | COMPLETION_CHECKED → AWAITING_ACCEPTANCE | 系统 |

| 11-21 16:53:13 | REPORT_APPROVED：`approval-04f93ecfe5c93c7b6216446faeb8b843`，报告 `dd786aba…320d` | **用户 xierongqiag2015**（编排台，演示账号） |
| 11-21 16:53:32 | REPORT_DELIVERED：回执 `delivery-receipt-d32695c79eacfa1cb3b8e049e7331361`，delivery `0eb9c269-…`，artifact_digest `sha256:dd786aba…320d` | **用户 xierongqiag2015** |
| 11-21 16:53 | 下载正式 JSON：4,076 字节，sha256 `dd786aba46e6bfd932da9c753e70161b083c1adc4e8284efa062ffb113c0320d`，与报告摘要和交付回执一致 | **用户 xierongqiag2015** |

验收后 `/verify`：`status=DELIVERED`、`evidence_intact=true`、`issues=[]`、`remote_calls=4`、`formal_nodes=6`、审计事件 361 条。从 11-21 11:20 服务重启到验收完成，Gateway 审计为 0 行：批准、交付、下载都不经 Bus，没有任何重发（`raw/T-824c5bf9/task-after-delivery.json`、`verify-after-delivery.json`、`events-acceptance.json`、`download-check.json`；截图 `shots/8-plugin-delivered-1655.png`）。

**选择记录 → 获准 Graph → Bus 服务 → 正式结果**

| 能力（选择记录） | 节点（获准 Graph `5c227314…`） | Bus 服务（AgentCard） | 远端任务 ID | 结果 |
|---|---|---|---|---|
| extractor@1 | extract | report-extractor | `d894d05b-742f-4088-b984-cbd596eead82` | COMPLETED |
| business@1 | business | business-analyst | `4161c7a8-5101-453d-815f-68daefaf0cc5` | COMPLETED |
| quality@1 | quality | asset-quality-analyst | `b64bf2d5-5e39-4b98-8a03-bd52a5dd7aa7` | COMPLETED |
| writer@1 | draft0 | report-writer | `0fd05407-559d-411b-87c2-da22c9c0ab7d` | COMPLETED；报告 `sha256:dd786aba…320d`，check0 PASS |

- **选择记录的排除与未知**：`parent-only@1` 被排除（任务口径为本集团，能力只支持母公司）；`unknown@1` 判为未知（未声明输入、输出和适用范围）。组合校验为 compatible。
- **Bus 汇总**：services 4、planned_nodes 5（含跳过的 revise1）、completed 4、reused 0、failed 0、unresolved 0。每个专家节点各有 1 条 SEND_INTENT 和 1 条 EXPERT_INPUT_AUTHORIZED，attempt 均为 1，没有冲突。
- **证据完整性**：`/verify` 返回 `evidence_intact=true`。
- **报告核对**：8 项指标的两期数值、变动和 PDF 页码，都与 W01 `demo/annual-report/answers.json` 逐项一致；6 条结论都是年报表格行的逐字引用，所以检查器直接 PASS，没有进入 HUMAN。

## 实际执行实例与 Gateway 发送次数（16:00 现场取证，`raw/instance-and-gateway/`）

**Gateway 发送次数**：Gateway（agent-gateway 0.1.3，PID 1100）对每个请求写一行治理审计 `LoggingAuditSink`，含方法和结果，但不含请求号和目标 Agent。按时间窗口统计，从 12:45 升级重启到 16:00 取证：

| 时间窗口（UTC+8） | SendMessage | GetTask |
|---|---|---|
| 12:45:00 – 14:07:14.2（准入前：规划、浏览、截图） | 0 | 0 |
| 14:07:14.2 – 14:09:35.7（本任务执行） | **4**，全部 PASSED | 141，全部 PASSED |
| 14:09:35.7 – 16:00（完成后：反复读取、截图、取证） | 0 | 0 |

- 这段时间只有本任务处于执行状态，所以窗口内的请求都归属本任务。T-52b9df74 从未准入，更早的任务不在执行中。
- 4 次 SendMessage 的时间是 14:07:16.987、14:07:46.414、14:07:46.428、14:09:01.296，与 Go 的 4 条 SEND_INTENT（14:07:15.715、14:07:46.387、14:07:46.395、14:09:01.275）一一对应。
- 没有任何重发，完成后的读取和截图也没有产生发送。

**实际执行实例**：
- 每次调用时 RDC 都只返回 1 个实例（ONLINE，`maxConcurrency=1`），实例号记录在 `calls.json` 的路由里；
- 16:00 再查 RDC，实例号与调用时相同，仍各只有 1 个；
- 监听各端口的进程与服务记录的 PID、boot_id、启动 tick 一致，说明从 12:43 启动到取证时没有换过进程。

| 节点 | Agent | RDC 实例 | 进程（boot `4602f186…`） |
|---|---|---|---|
| extract | report-extractor | `i-3e0ab76d2ee46d4b` | PID 3088，tick 420806，:8093 |
| business | business-analyst | `i-6315f907fd8eae7b` | PID 3163，tick 421551，:8094 |
| quality | asset-quality-analyst | `i-8a4e816a69626666` | PID 3246，tick 422188，:8096 |
| draft0 | report-writer | `i-d60a882b80276a7c` | PID 3317，tick 422761，:8097 |

**限制**：
- 专家进程不在日志里记录任务号，所以“这次调用由该进程执行”是排他推断：只有一个登记实例，端口上始终是同一进程，Gateway 为 DIRECT 路由。这不是专家侧的直接观测。
- 这个窗口内 Gateway 的计数靠时间和“只有一个任务在执行”来归属，不是按请求号逐一对应。
- `ps` 显示的启动时间受 WSL btime 漂移影响，比日志晚约 14 分钟，所以进程身份以 boot_id 加启动 tick 为准。

## 未覆盖

- **D2 要求的同一条真实 HUMAN 任务**：本任务没有触发 HUMAN。
- **W06**：专家是 W03/W04 提供的 `experts-java`，基于 openJiuwen Runtime/Core，真实调用模型；W06 的实现（PR #36）没有接入。
- **界面问题**（只记现象）：
  - 对话内的任务卡片显示 `Failed to fetch`，状态停在 AWAITING_APPROVAL（`shots/1a-…`）；
  - Bus 执行详情中“模型调用尝试”显示为 `undefined 次`（`shots/7-…`）。

## 文件

| 路径 | 内容 |
|---|---|
| `raw/T-824c5bf9/plan-only.json` | 规划过程：提示词、准入前停点、摘要 |
| `raw/T-824c5bf9/task-pre-admission.json`、`workbench-pre-admission.json`、`calls-pre-admission.json` | 准入前的任务、工作台投影（含完整选择记录）、调用（为空） |
| `raw/T-824c5bf9/task-after-execution.json`、`workbench-after-execution.json`、`calls.json` | 执行后的任务、投影和 4 个 Bus 调用（含路由、远端任务号） |
| `raw/T-824c5bf9/events.json` | 全部 359 条审计事件；超过 1500 字符的文档替换为长度和 sha256 |
| `raw/T-824c5bf9/check0-artifact.json`、`report-draft0.json` | 程序检查结果、报告正文 |
| `raw/T-824c5bf9/status-watch.jsonl` | 只读监视记录的状态变化 |
| `raw/instance-and-gateway/` | Gateway 审计（12:45 起，主体 ID 已脱敏）、按窗口的计数、专家进程与 RDC 实例对照 |
| `shots/` | 9 张真实页面截图，文件名带截取时间：规划对话、准入前编排台、选择记录、执行后编排台、Bus 调用清单与详情、报告。截图只浏览和展开，没有点击可信操作 |
