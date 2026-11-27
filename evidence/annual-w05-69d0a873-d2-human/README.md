# 年报 D2：HUMAN 任务在 W05 接收环境从 WorkSwarm 走到下载（PR #69 `69d0a873`，2026-11-27）

按 [yougq 11-23](https://github.com/chaosxingxc-orion/agent-hub-alpha/issues/16#issuecomment-7091602280) 指定的接收版本 `69d0a873` 复跑。`70be942` 上的第一次运行见 `../annual-w05-70be942-d2-human`。W05 回填项对应 #16 分工表：接收环境、负责人、联调时段与时区、任务 ID、图版本及摘要、核验和交付回执、核验前后原四个远端调用及 Gateway 发送计数、下载摘要。

## 环境

| 项目 | 内容 |
|---|---|
| 版本 | 主仓 Draft PR #69 头 `69d0a873dfab50b9368735f1f11cf0ce639862b2`：`70be942` + `b620fc6`（人工核验重试幂等）+ `ef30e95`（批准前展示核验策略）+ 合入统一候选 `bc03eb23` |
| 接收环境 | W05 本机 WSL2 Ubuntu 24.04，原生进程，按 LINUX-RUN 部署；PG16 容器 `report-pg16`。11-23 17:17 由 `70be942` 原地升级，没有数据库迁移：重建控制面、编排 jar、插件，`experts-java` 未改动 |
| 绑定 | 模型 deepseek-v4-1-flash-260910，model_binding_digest `sha256:8f336627…`；execution_profile `sha256:845cc6e0…`（与 `70be942` 相同） |
| 升级后检查 | `validate_services.py`、`identity_probe.py` all_passed；`go vet`、`tests/projection.test.cjs` 通过。11-26 22:08 本机重启，11-27 20:25 用恢复脚本重启全部服务，自检通过，版本不变 |
| 专家与链路 | 现有四位年报专家，各 1 个实例，经 RDC/Gateway DIRECT 调用 |
| 时区 | +08:00 |

## 任务与操作人

- 任务 **T-54158e50f7ee11c2e40adda9b8da15a0**，图版本 g1，graph_digest `sha256:005d7db3159a37738ea81a4b66e00b51431cef53b13dc19e37c44be8201ee878`，选择记录 `selection-e22fcc82…`；8 个节点，结构同第一次；`human_review_required: true`。
- 规划：11-23 17:21，由 W05 脚本用与第一次相同的对话发起（`prompt.txt`），准入前停止。
- 准入、人工核验、恢复、批准、交付、下载：**xierongqiag2015** 本人在编排台和 Go 可信页操作，账号 `demo`，时间 11-27 20:51—21:08。

## 时间线（`raw/events-all.json`）

| 时间 | 事件 | 说明 |
|---|---|---|
| 11-23 17:21:25 | PROPOSED | 远端调用 0。插件批准框显示“**必须人工核验** · 执行图 v1 · `sha256:005d7db3…`”（`screenshots/2b-approval-form-policy.png`，`ef30e95` 新增） |
| 11-27 20:51:59 | ADMITTED | 用户准入 |
| 20:52:00—20:53:23 | SEND_INTENT ×4 | extract、business、quality、draft0 |
| 20:53:52 | check0 PASS；COMPLETION_CHECKED WAIT | `human_review` 为空；缺 `human-review:check0`，任务 WAITING_INPUT，理由“获准执行图要求人工核验” |
| 21:02:14 | HUMAN_REVIEW_RECORDED | **ACCEPT**，回执 `human-review-04b5326fc20fb94f2467b7f8f12035e0`，核验人 demo；绑定 check_artifact `1990adfc…`、object_digest `sha256:6b7e2b77…`、graph_digest `sha256:005d7db3…`。理由原文：“已对照年报 PDF 第 14—16 页逐项核对八项指标的 2025/2024 数值、单位和变动，均与原文一致；口径为本集团，期间正确。接受交付。” |
| 21:06:35 | PAUSE_CHANGED → QUEUED | 用户恢复原任务（g1） |
| 21:06:37 | SKIP_AUTHORIZED ×2；select_report；AWAITING_ACCEPTANCE | revise1、check1 依 check0 跳过；select_report 选 draft0，并携带人工核验回执 |
| 21:08:20 | REPORT_APPROVED | `approval-362f3dcc…` |
| 21:08:41 | REPORT_DELIVERED | `delivery-receipt-a2711d26df779b05d1f4b811590a460a` |
| 21:08 | 下载 | `download/T-54158e50…-annual-report.json`，3653 字节 |

## 核对结果

| 项 | 结果 |
|---|---|
| 程序 PASS 后仍待人工核验 | 是 |
| 摘要一致 | 下载文件 SHA-256 `6b7e2b77aa0bc6769620c28925c0ed68f85c5f9bd08a8e48c2b28e97704159f0`，与 draft0 报告、check0 的 object_digest、人工核验回执、批准记录、交付回执相同 |
| 原四个远端调用 | 核验前（WAITING_INPUT）与交付后的 `/calls` 逐字段相同（call_id、attempt 1、input_digest、message_id、context_id、remote_task_id、COMPLETED、无冲突）。远端任务号：extract `a735c604…`、business `217ff139…`、quality `ce52d011…`、draft0 `16a627a5…` |
| PG 发送意图 | SEND_INTENT 4 条，均在 20:53:23 之前；核验到交付新增 0 |
| Gateway 发送计数 | 日志累计 SendMessage：准入前 52（11-23 记录，之前各任务之和），交付后 56。新增的 4 次在 20:52:01.145、20:52:21.983、20:52:21.992、20:53:23.234，即四位专家各 1 次；20:53:24 之后新增 **0**，Gateway 日志最后一行 20:53:52（draft0 最后一次查询），核验到交付期间没有任何 Gateway 请求 |
| 插件投影 | 交付后编排台：DELIVERED、交付回执、“关联任务 4 · 已保存 4 · 待核对 0” |

对照：程序 PASS 任务 T-824c5bf9（`4004ab4`，未要求人工核验，PASS 后直接待批准），见 `../annual-w05-4004ab4`。

## 文件

与 `../annual-w05-70be942-d2-human` 相同的结构：`prompt.txt`、`plan-only.json`、`candidate-task.json`、`watch.jsonl`、`raw/` 各阶段快照与全部事件、`raw/gw-*.txt` Gateway 计数、`download/` 下载原件、`screenshots/`（含批准框核验策略）。快照中 RDC 路由句柄已整值替换为 `<redacted>`，不含令牌或密码。

## 未覆盖与待回填

- 人工核验由操作人本人完成；独立复核人、业务签收人待 W07 / W06 指定。
- 核验重试幂等（`b620fc6`）本次未单独构造重复提交，以 W04 的回归为准。
- W02 的两态展示：本次只看到“必须人工核验”一态；“按程序检查结果处理”由 W02 回填。
