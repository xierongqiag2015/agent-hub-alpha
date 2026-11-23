# PR #70 接收复验：撤权拒绝时提交节点的状态（2026-11-23，`db85c79`）

[PR #70](https://github.com/chaosxingxc-orion/agent-hub-alpha/pull/70) 修复了 W05 撤权对照（`../rv-effect-revoke-bc03eb2`）中发现的问题：派发被拒后，提交节点仍为 `running`，文案是“已派发，等银行回执”。本目录在 W05 隔离环境，用同一场景复验修复版。

## 环境

| 项目 | 内容 |
|---|---|
| 代码 | PR #70 头 `db85c79fb96a51304ca3d423565852a0bcd849bf`（统一候选 `bc03eb23` + 修复），只改 credit-demo 的 Go 代码 |
| 镜像 | credit `f251268d29188c0e`（`db85c79` 原样构建，含 PR 新增测试在内的全部 `go test`）；撤权用 credit `b4ab52b1d6119fe7`（同一源码，只删 `caller_holds` 中 `credit.apply/execute`，该副本构建时跳过 `go test`、保留 `go vet`）。orchestrator `a22cf20c3fdf`、professional `d382af7d0050` 沿用 `bc03eb2`（PR #70 未改）；RDC/Gateway 同 `bc03eb2` |
| 部署 | WSL2 Ubuntu 24.04 + Docker Desktop，新建独立项目 `agent-hub-db85`（credit 127.0.0.1:5394，新数据卷） |
| 数据与模型 | 合成授信案例 C006；glm-5-2-260617 |
| 负责人与时段 | W05 xierongqiag2015，脚本驱动；2026-11-23 22:24:48—22:29:49（+08:00） |
| 场景 | `scripts/w05_rd_extra.py rv-effect-revoke`，与 `bc03eb2` 的那次相同；另加一项：派发被拒后读取提交节点的状态记录和 `/api/graph` 投影，然后重启 credit（仍是撤权镜像），再读一次 |

## 结果

| 项 | `bc03eb2`（修复前，17:42） | `db85c79`（本次） |
|---|---|---|
| 正对照（本次 R1） | 回执 approved | 回执“approved（业务请求 br-c006-r1，额度 50000 USD）”，账本 adopted → dispatched → completed |
| 撤权 | 17:47:09—17:47:16 | **22:29:15.421—22:29:22.362**；之后预检拒绝“not held by caller: tool:credit.apply action=execute” |
| 测试任务（本次 R2）的派发 | HTTP 400，账本 adopted + control/rejected | 相同：HTTP 400，账本 adopted + control/**rejected**（14-A4），没有 dispatched，没有银行回执；拒绝后 `/api/resume` 返回 409 |
| 提交节点状态记录 | `running` /“已派发，等银行回执” | **`failed` /“派发被拒”**，finished_at 14:29:29Z |
| `/api/graph` 投影（插件与 `/ui` 共用） | 同上，显示为已派发 | **`failed` /“派发被拒”** |
| 重启 credit 后 | — | 仍为 **`failed` /“派发被拒”** |
| 撤权后 Gateway 新增发送 | 0 | **0**（代理和 Gateway 审计一致）；只有 report 原任务的查询 1 次 |
| 旧报告与回执 | 不变 | R1 撤权后读取：回执、最终报告摘要 `sha256:18a9e99d…`、账本与撤权前一致，读取不改变状态版本 |

## 文件

- `rv-effect-revoke-1795443951.json`：场景证据，含 `checks`、`submit_views`（撤权拒绝后、重启后各一次）、窗口计数等。
- `history-R1-adopt.jsonl`、`test-R2-adopt.jsonl`：采纳账本原文。
- `fault-events.jsonl`、`gateway.log`（主体标识已脱敏）、`credit.log`、`orchestrator.log`、`run.log`、`scenario.out`。
- `run/`：构建脚本与记录、运行脚本、镜像覆盖配置。
