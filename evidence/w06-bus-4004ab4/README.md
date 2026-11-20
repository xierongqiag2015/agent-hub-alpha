# W06 角色 Agent 经 Agent Bus 调通（W05，2026-11-20，年报接收环境 4004ab4）

W05 把 W06 在 [PR #36](https://github.com/chaosxingxc-orion/agent-hub-alpha/pull/36) 交付的五个年报角色 Agent 托管为真实的 A2A 服务，登记进年报接收环境的 Agent Bus RDC，并用年报编排器同一个执行身份经 Gateway（DIRECT）调用。五个 Agent 串成一条链：资料 → 指标 → 经营 ∥ 资产质量 → 写作，**全部完成**。

这条链由 W05 的测试驱动推进，不是 W03 的 Graph 执行器；年报任务的节点仍由 W03/W04 的专家执行，见“未覆盖”。

## 版本

| 项 | 值 |
|---|---|
| W06 库层 | PR #36 `71daf010a4`，`W06-wanshoulu-1117/source/w06-annual-report-agent/agents`；原样使用，构建时打三处一行适配 |
| 外壳 | fork `w06-bus-runtime` `7ab8eb4`，`agent-festival/examples/w06-agent-runtime`；jar sha256 `724ce11c39d00502…`，打过适配的 W06 agents 库 `829d2e4a2bf7bf01…` |
| Runtime | openJiuwen `agent-service-app` 0.1.4、`agent-core-java` 0.1.17，与 W06 声明的版本相同 |
| Agent Bus | 年报接收环境的 RDC / Gateway 0.1.3（agent-solution `eb48c482`），Gateway DIRECT，治理审计开启 |
| 调用身份 | 年报执行身份（Keycloak client credentials），与编排器相同 |
| 模型 | deepseek-v4-1-flash-260910（OpenAI 兼容接口，与年报链相同） |
| 材料 | 年报链已核验的招行 2025 年报第 14—16 页，转为 W06 快照格式；8 个指标的页码索引取自 W01 `answers.json` 的引用页码（`raw/snapshot-manifest.json`） |

## 结果（16:52:11—16:53:54，`raw/calls-1795164833.json`）

| 步骤 | Agent（RDC 实例） | 远端任务 | 用时 | 结果 |
|---|---|---|---|---|
| 1 资料 | w06-material（`i-db5636ecd521e1d9`） | `10543b9d…` | 10.2 s | COMPLETED：八项指标都在快照中定位，逐项给出 digest、页码和原文摘录 |
| 2 指标 | w06-metrics（`i-2c53fa93026be749`） | `6b779fd2…` | 18.9 s | COMPLETED：按 W06 契约输出 8 条结构化指标，带来源 digest 和页码 |
| 3 经营 | w06-operation（`i-1282157d6a6c4c2e`） | `735c0cfa…` | 12.2 s | COMPLETED：经营分析，带 [C-n] 引用 |
| 4 资产质量 | w06-asset-quality（`i-20f64f7f745327df`） | `a62bca67…` | 21.1 s | COMPLETED：资产质量分析，带 [C-n] 引用 |
| 5 写作 | w06-writer（`i-eeedbf34049bd81b`） | `d03d3bae…` | 39.1 s | COMPLETED：`AnnualReportArtifacts` JSON，含 task_id、company、report_period、metrics、citations（8 条）、sections（经营分析、资产质量分析） |

核对结果见 `raw/calls-1795164833-check.json`：
- **写作 Agent 原样转抄八指标**：`writer_metrics_verbatim = true`，符合 W06 写作契约的第 1 条铁律。
- **8 项指标的 2025 年数值都与 W01 `answers.json` 一致**：营业收入 337,532、归母净利润 150,181、净息差 1.87、成本收入比 31.98、不良余额 68,206、不良率 0.94、拨备覆盖率 391.79、核心一级资本充足率 14.16（高级法）。
- **Gateway 治理审计**（`logs/gateway-audit-since-1641.log`，主体 ID 已脱敏）：同一窗口内 SendMessage **5 次**（每个 Agent 1 次），GetTask 46 次，全部 PASSED。这段时间年报链没有其他任务在执行。
- **RDC 与进程**：每个 W06 Agent 都只有 1 个 ONLINE 实例；进程的 PID、boot_id、启动 tick 与启动记录一致（`raw/services.json`）。心跳每 8 秒一次，全部 200。

## 过程中暴露的 W06 缺陷（已在外壳构建时适配，请 W06 在上游修正）

| 时间 | 现象 | 原因 | 处理 |
|---|---|---|---|
| 16:37 | w06-metrics 启动即退出：`UnknownFormatConversionException: Conversion = ' '` | 指标提示词里的“亿元/% 等”经过 `String.formatted()` | 适配 2：改为 `%%`，输出文字不变 |
| 16:41 | 资料 Agent 完成；指标、经营、资产质量、写作四个 Agent 的模型请求被拒：`tools.2.function.parameters must be a JSON Schema of type object`（HTTP 400） | 四个工厂里的 `ask_user` 工具都没有参数 schema | 适配 3：补上空对象 schema，与 W06 `list_metrics_index` 的写法相同 |
| 构建前 | 资料 Agent 的快照从未装载，`read_page` 会返回“没有第 N 页” | `MaterialAgentFactory.build()` 没有调用 `SnapshotStore.load()` | 适配 1：补一行 `store.load()` |
| 16:45、16:50 | 写作 Agent 在第 1 轮就调用 `ask_user`，被 W06 的 `AskUserRail` 中断（`result_type=interrupt`） | 测试驱动只给了两路分析，缺少写作契约要求的 task_id、公司、期间和八指标 artifact；这是 W06 有意的设计 | 测试驱动补全契约字段后，16:52 完成（`raw/calls-1795164700.json` 为补全前的记录） |

**另外**：W06 的 `checker` 模块（数值与引用核验、工具网关）在当前依赖下编译失败，原因是 `ToolGateway` 引用了 `org.apache.pdfbox.Loader`。本轮没有运行 W06 的核验器。

## 对环境的改动（如实登记）

- 新增 5 个进程：W06 Agent，监听 127.0.0.1:18111—18115；另有 1 个心跳进程，均在 `~/w06-bus/` 下。
- RDC 新登记 5 个 Agent 实例：`w06-material`、`w06-metrics`、`w06-operation`、`w06-asset-quality`、`w06-writer`。
- 新建角色 `w06-bus-trial`，只包含这 5 个 Agent，并**追加**给执行身份：角色由 `[report-demo-executor]` 变为 `[report-demo-executor, w06-bus-trial]`。原角色和它的 4 个专家授权都没动（`raw/grant.json`）。

## 观察

- **W06 指标契约只含本期值**：每条输出只有 value、unit、period，没有上期值。所以经营和资产质量 Agent 在部分轮次中明确写出“输入未提供基期，不作同比判断”，没有自行编造。年报 D2 要的是“2025 对比 2024”，契约需要补上上期值。
- **单位偶尔缺失**：资产质量 Agent 在一轮中指出不良贷款余额的单位缺失（16:52 那一轮），说明各轮输出存在模型波动。

## 未覆盖

- **年报任务的节点没有改由 W06 执行**：W03 Graph 和 W04 Go 控制面调用的仍是 W03/W04 的四个专家。要改为 W06，需要与 W03/W04 对齐节点输入（受控材料、`/v1/experts/{agent}/calls/{call}/authorize` 授权）和类型化输出（`indicators/1`、`report/1` 等），并由 xingchao/W07 决定 D2 是否替换。
- **写作 Agent 的人工中断**：没有映射为 A2A `INPUT_REQUIRED` 并支持恢复；目前报为失败，并在日志中保留中断结果。
- **W06 核验器**：checker 模块编译失败，没有运行。
- **独立复核**：W06 本人没有复核，W07 也没有。

## 文件

| 路径 | 内容 |
|---|---|
| `raw/calls-1795164833.json` | 成功的完整链：每次调用的请求号、消息号、上下文号、远端任务号、轮询次数、用时和完整输出 |
| `raw/calls-1795164833-check.json` | 原样转抄、对照 W01、Gateway 计数、RDC 实例与进程身份 |
| `raw/calls-1795164133.json`、`calls-1795164408.json`、`calls-1795164700.json`、`writer-only-1795164560.json` | 过程中的失败记录，对应上表 |
| `raw/registrations.json`、`grant.json`、`services.json`、`heartbeat-status.json`、`snapshot-manifest.json` | RDC 登记回执、授权变更与权限预览、进程记录、心跳、快照清单 |
| `logs/` | Gateway 审计（16:41 起），以及指标与写作 Agent 的缺陷日志摘录 |
