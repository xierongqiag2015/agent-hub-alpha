# W05 年报链单侧记录：`4004ab4`（2026-11-20）

W05（xierongqiag2015）在自己的接收环境中，把 W03/W04 的 D2 修复交接点 `4004ab4` 从真实对话跑到“待验收”。这是单侧记录，不代替 D2 联合验收、W06 业务验收或 W07 独立复核。

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

## 未覆盖

- **批准、交付、下载**：任务停在 AWAITING_ACCEPTANCE，由用户在编排台操作，本记录生成时还没进行。
- **D2 要求的同一条真实 HUMAN 任务**：本任务没有触发 HUMAN。
- **执行实例与 Gateway 发送次数**：路由记录为 `actual_execution_instance = not_collected`；只有控制面的 SEND_INTENT，没有 Gateway 侧计数。
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
| `shots/` | 9 张真实页面截图，文件名带截取时间：规划对话、准入前编排台、选择记录、执行后编排台、Bus 调用清单与详情、报告。截图只浏览和展开，没有点击可信操作 |
