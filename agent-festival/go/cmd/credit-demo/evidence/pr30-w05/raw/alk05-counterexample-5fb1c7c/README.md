# #33 反例：不兼容组合在派发前被拒（2026-11-20，PR #60 头 5fb1c7c）

对应 [#33 接收意见](https://github.com/chaosxingxc-orion/agent-hub-alpha/pull/33#issuecomment-7029995519) 中的“同时验证不兼容组合在派发前被拒”。

## 环境

与 `../item2-5fb1c7c/` 相同：PR #60 头 `5fb1c7c` 的镜像，独立 compose 项目 `agent-hub-5fb1`，真实 RDC/Gateway 和三个专业 Agent，编排器经故障代理访问 Gateway。

能力选择用的是 credit 镜像自带的场景包 `deploy/scenarios/annual-report-cmb-2025.json`。入口是规划器 MCP（`/mcp`）的 `select_capabilities` 和 `propose_plan`，与 WorkSwarm 规划助手调用的是同一入口。

## 步骤与结果

1. `select_capabilities`：查询条件为招商银行、本集团、2025 对比 2024、经营 + 资产质量、材料 `cmb-2025-annual-report-a`。得到选择记录 `S1`：
   - outcome `graph`，组合 `compatible`；
   - 选中 extractor、operating、asset-quality、writer；
   - `parent-only-analyst` 因口径被排除。
2. 用 `S1` 提交三种不兼容的团队，均在生成计划之前被拒：

| 尝试 | 拒绝原因（原文） | 计划数 / 任务数 / 发送数 |
|---|---|---|
| 经营分析不再依赖提取 | `the team does not compose: operating 的输入 indicator-set 没有上游成员提供` | 4/4/11 → 4/4/11 |
| 去掉提取节点 | `the team does not compose: operating 的输入 indicator-set 没有上游成员提供; asset_quality 的输入 indicator-set 没有上游成员提供` | 4/4/11 → 4/4/11 |
| 加入已排除的能力 | `agent parent: ar.parent-only-analyst was not selected in S1 (excluded：硬约束不满足：任务口径为 本集团，能力只支持 母公司)` | 4/4/11 → 4/4/11 |

3. 对照：用 `S1` 原样提交，生成计划 P5，预检通过。没有批准，所以任务数仍为 4，发送数仍为 11。11 是本项目此前几个场景已有的发送次数。

## 范围

- **验证的代码**：#33 的选择与组合检查，位于 credit-demo。这次验证用的是 credit 镜像里的年报场景包，没有走 W04 年报控制面。
- **年报链**：年报链的能力选择由 W04 control-go 实现（`POST /v1/selections`）。它在真实年报任务 T-824c5bf9 中的贯通证据（选择记录 → 获准 Graph → Bus 服务 → 正式结果）另见年报证据分支。
- **包级不兼容**：场景包本身就组合不起来（outcome `no_graph`）的情形，由组件测试 `TestIncompatibleCompositionRejectedBeforeAdoption` 覆盖，本轮没有改动部署包。

## 文件

- `selection-incompatible-*.json`：选择记录摘要、逐次尝试的请求结果和前后计数；
- `scenario.out`、`run.log`（镜像与时间）、`credit.log`；
- 场景脚本：`../../scripts/w05_rd_extra.py selection-incompatible`。
