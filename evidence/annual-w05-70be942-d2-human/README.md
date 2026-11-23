# 年报 D2：HUMAN 任务在 W05 接收环境从 WorkSwarm 走到下载（PR #69 `70be942`，2026-11-23）

对应要求：
- [#69 xingchao 11-23](https://github.com/chaosxingxc-orion/agent-hub-alpha/pull/69#issuecomment-7089538631)：W05 在接收环境用同一 HUMAN 任务从 WorkSwarm 走到下载，核对报告摘要和原四个远端任务；
- [#16 分工表](https://github.com/chaosxingxc-orion/agent-hub-alpha/issues/16#issuecomment-7089539476) 中 W05 一行：接收环境、负责人、联调时段、任务与 Gateway 记录。

## 环境

| 项目 | 内容 |
|---|---|
| 版本 | 主仓 Draft PR #69 头 `70be94241149b2b97401177634b7750df49b8921`：统一候选 `67fd5b05` + 年报 fork PR #1 `4004ab4` + 人工核验门禁修复 `6ba1d1f`、`70be942` |
| 接收环境 | W05 本机 WSL2 Ubuntu 24.04，原生进程，按 LINUX-RUN 部署；PG16 容器 `report-pg16`。由 `4004ab4` 原地升级：PR #69 在 `demos/report-demo/` 下改 24 个文件，没有数据库迁移；重建控制面、编排 jar 和插件，`experts-java` 未改动 |
| 绑定 | 模型 deepseek-v4-1-flash-260910，model_binding_digest `sha256:8f336627…`（升级前后不变）；execution_profile `sha256:845cc6e0…`（升级后由 `bind_profile.py` 重新生成） |
| 升级后检查 | `validate_services.py`、`identity_probe.py` 均 all_passed（含 6 项身份正反例）；`go vet` 通过；`tests/projection.test.cjs` 通过；已安装的 annual-report SKILL 与仓库一致。Java `ProtocolAndCheckerTest` 未跑成：本机 Maven 代理配置指向失效地址，取不到 surefire 的 JUnit provider |
| 专家与链路 | 现有四位年报专家（report-extractor、business-analyst、asset-quality-analyst、report-writer），各 1 个实例；经 RDC/Gateway DIRECT 调用 |
| 时区 | 全文时间为 +08:00 |

## 任务与操作人

- 任务 **T-32758d16c0f1bde333df2f7d819f9de3**，图版本 g1，graph_digest `sha256:52b1b1fd630c4d59e2dde7b540a96a3a50b5a25a15fac8c7b206d81b7959619e`，选择记录 `selection-0ca1e804…`。
- 8 个节点：extract → {business, quality} → draft0 → check0 → revise1 / check1（条件 check0=REVISE）→ select_report。
- 图顶层带 `human_review_required: true`。
- 规划对话由 W05 脚本发起（`prompt.txt`），对话中明确要求“即使程序检查通过，本报告也必须由人核验接受后才可批准和交付”，脚本在准入前停止（`plan-only.json`）。
- 准入、人工核验、恢复、批准、交付、下载都由 **xierongqiag2015** 本人在编排台和 Go 可信页操作，使用账号 `demo`。W05 脚本只做规划和只读取证。

## 时间线（控制面事件，`raw/events-all.json`）

| 时间 | 事件 | 说明 |
|---|---|---|
| 10:02:46 | PROPOSED | 候选图生成，状态 AWAITING_APPROVAL，远端调用 0 次。编排台显示“执行图要求人工核验……”（`screenshots/2-candidate-in-plugin.png`） |
| 11:21:38 | ADMITTED | 用户在编排台准入 |
| 11:21:40—11:22:55 | SEND_INTENT ×4 | extract、business、quality、draft0 各 1 次 |
| 11:23:24 | check0 提交 | 结论 **PASS**，`human_review` 为空，`object_digest` `sha256:a6fadedb…` |
| 11:23:24 | COMPLETION_CHECKED | WAIT，缺 `human-review:check0`；任务 **WAITING_INPUT**，插件理由“获准执行图要求人工核验” |
| 11:26:07、11:50:30 | PAUSE_CHANGED ×2 | 核验前用户两次点了“继续”，任务两次都留在 WAITING_INPUT，没有领取、派发或跳过 |
| 15:32:45 | HUMAN_REVIEW_RECORDED | **ACCEPT**，回执 `human-review-25a35eb7ac1672c8e3093ea86666dd96`，核验人 demo。绑定 check_artifact `03e45f16…`、object_digest `sha256:a6fadedb…`、graph_digest `sha256:52b1b1fd…`。理由原文：“已逐项核对八项指标的 2025/2024 数值与年报原文页码一致，结论可交付。” |
| 15:46:34 | PAUSE_CHANGED → QUEUED | 用户恢复原任务，沿原图 g1 |
| 15:46:35 | SKIP_AUTHORIZED ×2；select_report 提交 | revise1、check1 依据 check0 授权跳过；select_report 选中 draft0，并携带人工核验回执；任务 AWAITING_ACCEPTANCE |
| 17:06:05 | REPORT_APPROVED | `approval-9f9c3476…`，artifact `a6fadedb…` |
| 17:06:25 | REPORT_DELIVERED | 交付回执 `delivery-receipt-0324378282473bb431c3aa57875a5067`，artifact_digest `sha256:a6fadedb…` |
| 17:06 | 下载 | `download/T-32758d16…-annual-report.json`，3458 字节 |

## 核对结果

| 项 | 结果 |
|---|---|
| 程序 PASS 后仍待人工核验 | 是。check0 PASS 后，任务停在 WAITING_INPUT；核验前两次“继续”都没有推进 |
| 报告摘要一致 | 下载文件 SHA-256 `a6fadedbc81503c0bee9dbb512640ed2f9aa764d8e012e81af43b49712703aa3`，与 draft0 报告、check0 的 object_digest、人工核验回执、批准记录、交付回执五处相同 |
| 原四个远端任务 | 核验前（WAITING_INPUT）与交付后的 `/calls` 逐字段相同：call_id、attempt（均为 1）、input_digest、message_id、context_id、remote_task_id、状态（COMPLETED）、无冲突。远端任务号：extract `913ad817…`、business `ef16af9e…`、quality `918e687c…`、draft0 `860856b4…` |
| PG 发送意图 | SEND_INTENT 共 4 条，全部在 11:22:55 之前；核验、恢复、批准、交付期间新增 0 |
| Gateway 发送计数 | 准入前基线：日志累计 SendMessage 48 次（之前各任务的总和）。准入后 4 次：11:21:41.295、11:22:06.024、11:22:06.042、11:22:55.380。之后到交付以后，新增 **0**；Gateway 日志最后一行为 11:23:24（draft0 的最后一次查询），人工核验到交付期间 Gateway 没有任何请求 |
| 插件投影 | 交付后编排台：DELIVERED、交付回执、“关联任务 4 · 已保存 4 · 待核对 0”（`screenshots/3-delivered-plugin.png`） |

对照任务：W05 已交付的程序 PASS 任务 T-824c5bf9（`4004ab4`，未要求人工核验，check0 PASS 后直接待批准），见 `evidence/annual-w05-4004ab4`。

## 文件

- `prompt.txt`、`plan-only.json`：规划对话原文与规划记录（准入前停止，远端调用 0）。
- `candidate-task.json`：获准前的候选图。
- `watch.jsonl`：只读轮询的状态变化。
- `raw/<阶段>-{task,workbench,calls}.json`：准入前、准入后、待核验、核验后、交付后的快照。
- `raw/events-all.json`：该任务的全部控制面事件。
- `raw/gw-*.txt`：Gateway 审计按时间窗口的计数。
- `download/`：用户下载的正式 JSON 原件。
- `screenshots/`：规划对话、准入前的编排台、交付后的编排台和报告。

所有快照中 RDC 路由句柄 `routeHandle` 已替换为 `<redacted>`，不含令牌或密码。

## 未覆盖与待回填

- 人工核验由操作人本人完成，独立复核人、业务签收人待 W07 / W06 指定。
- W04 的核验重试幂等修复（#69）合入后，如要求以新 SHA 为准，W05 在同一环境复跑。
- W02 的插件两态展示（必须人工核验 / 按程序检查结果处理），由 W02 回填。
- Java 检查器单元测试未在本机跑通，见“环境”一节。
