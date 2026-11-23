# #30 第 2 条：撤权对照（2026-11-23，统一候选 bc03eb2）

要求：
- [xingchao 11-23](https://github.com/chaosxingxc-orion/agent-hub-alpha/pull/30#issuecomment-7089539066) 第 2 条：旧报告与摘要可读，后续动作重过控制面，拒绝依据可查，Gateway 新增发送数为 0；
- [yougq 11-23](https://github.com/chaosxingxc-orion/agent-hub-alpha/pull/30#issuecomment-7091601845)：沿用 W05 的 `bc03eb23` 隔离环境和合成授信数据；一条已保留报告与回执的历史任务，一个判断已采纳、后续动作尚未派发的测试任务；按 `caller_holds` 中 `credit.apply/execute` 做正反对照；按测试任务和请求身份记录控制面拒绝、撤权起止时间、网关发送计数及证据位置。

## 环境

| 项目 | 内容 |
|---|---|
| 代码 | 统一候选 `bc03eb2315ef6b32b4f5df9ac55a0c393967c77a` |
| 镜像 | orchestrator `a22cf20c3fdf`、professional `d382af7d0050`、credit `19d21bc61b82`（撤权前）→ `d6081157746b`（撤权后，见下）；RDC `315186974da9`、Gateway `cac5d07658ae` |
| 部署 | WSL2 Ubuntu 24.04 + Docker Desktop，独立 compose 项目 `agent-hub-bc03`（credit 127.0.0.1:5393）；真实 RDC/Gateway（DIRECT）和三个专业 Agent，编排器经故障代理访问 Gateway（本场景不注入故障，只计数） |
| 数据与模型 | 合成授信案例 C006（模拟授信报告与模拟银行申请）；模型 glm-5-2-260617 |
| 负责人与时段 | W05 xierongqiag2015，脚本驱动；2026-11-23 17:42:41—17:47:38（+08:00） |
| 入口 | 与插件相同：`/mcp` 规划、`/api/approve`、`/api/resume`、`/api/graph`、`/api/plans` 和登录后的 `/ui/*` |

## 撤权方式

`credit-dd` 场景包（含 `caller_holds`）编译在 credit 程序里，运行时没有修改入口。撤权的做法是：从 `bc03eb2` 导出源码，**只删掉** `caller_holds` 中 `tool credit.apply / execute` 这一项（8 项 → 7 项，差异见 `run/revoked-build.txt`），构建测试专用镜像 `w05-bc03/credit:revoked`（`d6081157`），然后替换 credit 容器并重启，数据卷保留。

- 该镜像的 Dockerfile 只在这份副本里去掉了 `go test`，因为 `scenario_golden_test` 固定了场景包 `caller_holds` 的摘要；`go vet` 和构建保留。构建脚本见 `run/wsl_bc03_revoked_build.sh`。
- 撤权前后场景包摘要：`sha256:7c21089b…` → `sha256:cd0647c9…`。
- 撤权起止：**17:47:09.832—17:47:16.638**（credit 以新镜像重建并通过健康检查）。

## 任务

| 任务 | 用途 | 团队 |
|---|---|---|
| **R4**（计划 P6） | 历史任务 / 正对照：保有授权，跑到模拟银行回执 | 与 `smoke_bus.py` 的 7 个节点相同，另加 `submit`（credit-submitter），依赖 judge 和 report，`run_if` judge 含 `DECISION=GRANT` |
| **R5**（计划 P7） | 测试任务：判断已采纳、提交尚未派发 | 同上 |

R5 在 judge 已采纳（grant-50000）、report 在远端 writer 执行中时停止编排器（17:47:09.691）。此时 `submit` 为 pending，账本只有 1 条 adoption/adopted，没有银行回执。

## 结果

| 项 | 结果 |
|---|---|
| 正对照 R4 | done，判断 grant-50000，账本 adopted → dispatched → completed，银行回执“approved（业务请求 br-c006-r4，额度 50000 USD）”。采纳时锁定的授权为 `credit.apply/execute` |
| 撤权生效 | 撤权前，同一团队的新提案预检通过；撤权后预检被拒：“not held by caller: tool:credit.apply action=execute”，`not_held`：“submit（授信申请提交）需要 credit.apply execute，客户经理没有这项权限” |
| 后续动作重过控制面 | 编排器恢复后，R5 的 report 沿原远端任务查询 1 次并提交原结果；随后 `submit` 调用 `/exec/effect`，控制面按当前授权（7 项，不含 `credit.apply`）复核，**HTTP 400** |
| 拒绝依据可查 | 账本 `runs/R5/adopt.jsonl`：adoption/adopted 之后只有一条 control/**rejected**，原因“live grant set no longer covers the grants pinned at decision time (revocation check, 14-A4)”；**没有 dispatched，没有银行回执**。任务 failed，最终输出写明“/exec/effect -> 400 INVALID_REQUEST dispatch rejected: …”；状态中的 `dispatch` 记录附带当时的授权集合 |
| 拒绝后不能绕过 | 拒绝后 `/api/resume` 返回 409（任务已不在等待状态） |
| Gateway 新增发送 | 撤权期间 0；撤权到 R5 结束（17:47:16—17:47:37）SendMessage **0**（代理和 Gateway 审计一致），只有 report 原任务的查询 1 次。R5 的 5 个远端调用在冻结前后请求号、远端任务号不变 |
| 旧报告与摘要可读 | 撤权后两个已登录会话读 R4：`/api/graph`、`/api/plans`、`/ui/run/R4`、`/ui/plan/P6/v1` 均为 200。状态、判断、回执、最终报告摘要 `sha256:6de6f94e…`、账本，与撤权前完全一致；读取前后状态版本不变 |

各时间窗口的计数（`windows`；代理 = 编排器与 Gateway 之间的计数，审计 = Gateway 日志）：

| 窗口 | 时间 | 代理 SendMessage | Gateway 审计 SendMessage |
|---|---|---|---|
| R4 全程 | 17:43:37—17:45:41 | 5 | 5 |
| R5 至冻结 | 17:45:41—17:47:09 | 5（均为 R5 的请求号） | 5 |
| 撤权 | 17:47:09—17:47:16 | 0 | 0 |
| 撤权后至 R5 结束 | 17:47:16—17:47:37 | **0** | **0** |
| 读 R4 | 17:47:37—17:47:38 | 0 | 0 |

## 发现的问题（请 W04 看）

派发被拒后，`submit` 节点仍为 `running`，活动文案是“**已派发，等银行回执**”，而任务已 failed、账本是 rejected。原因是 `Effect()` 在调用 `m.Dispatch` 之后、判断结果之前就写了这句文案，被拒时没有改回。插件和 `/ui` 会把一次被拒的派发显示成已派发。

## 文件

- `rv-effect-revoke-1795427017.json`：场景证据，含两条任务在各阶段的状态、账本、调用快照，撤权前后的预检，窗口计数和检查结果。已脱敏。
- `history-R4-adopt.jsonl`、`test-R5-adopt.jsonl`：两条任务的采纳账本原文。
- `fault-events.jsonl`：代理记录的全部 SendMessage/GetTask。
- `gateway.log`：Gateway 容器日志，主体标识已脱敏。`credit.log`、`orchestrator.log`（含 400 原文）、`run.log`、`scenario.out` 为运行日志。
- `run/`：撤权镜像构建脚本与差异、运行脚本、两个镜像覆盖配置。场景脚本见 `scripts/w05_rd_extra.py rv-effect-revoke`。

## 说明

- 同日 17:33 的第一次运行，撤权后的预检是在编排器停止时做的，被拒原因是“编排器不可达”，不能作为撤权依据。已改为编排器恢复后再探测，本目录是 17:42 的第二次运行。
- 模拟银行在 credit 进程内，不经过 Agent Bus。Gateway 计数核对的是：撤权并恢复后，没有任何新的远端 Agent 发送。
- 授权变更在 credit 程序层面，不涉及 RDC 角色或 Gateway 主体。专业 Agent 经 Gateway 派发时的主体撤权（yougq 提到的可选项）本次未做。
- W07 独立复核尚未进行。
