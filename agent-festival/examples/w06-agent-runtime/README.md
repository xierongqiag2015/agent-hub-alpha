# W06 角色 Agent 的 Agent Bus 外壳（W05）

把 W06 在 [PR #36](https://github.com/chaosxingxc-orion/agent-hub-alpha/pull/36) 中交付的五个年报角色 Agent，托管为真实的 A2A 服务：资料 material、指标 metrics、经营 operation、资产质量 asset-quality、写作 writer。实例登记进 Agent Bus RDC，经 Gateway（DIRECT）调用。

W06 的库层代码（`W06-wanshoulu-1117/source/w06-annual-report-agent/agents`，提交 `71daf010a4`）原样使用，只在构建时打三处一行的适配，见下文。

## 结构

| 文件 | 作用 |
|---|---|
| `pom.xml` | Spring Boot 4.0.7；openJiuwen Runtime `agent-service-app` 0.1.4、`agent-core-java` 0.1.17（与 W06 和 professional-agent-runtime 同版本）；依赖 `com.agenthub.w06:agents:0.1.0-SNAPSHOT` |
| `W06RoleHandler.java` | Runtime 的 `AgentHandler`。按 `W06_ROLE` 调 W06 对应的工厂构造 Agent，启动时构造一次；每个 A2A 消息执行一次，返回 Agent 输出。A2A 任务生命周期由 Runtime 负责 |
| `application.yml` | A2A 卡片：`name` 为 `AGENT_SERVICE_ID`，版本 1.0.0，非流式，只绑定 127.0.0.1 |
| `build.sh` | 在一次性 Maven 容器中复制 W06 源码、打适配、安装 W06 agents 库，再打包本外壳 |
| `patches/w06-pr36-adaptations.patch` | 三处适配的说明与 diff |
| `ops/` | 年报接收环境（W03/W04 4004ab4，WSL 原生）上的运维脚本：快照、启停、RDC 登记与授权、心跳、经 Gateway 调用、核对 |

## 对 W06 PR #36 的三处适配（都是按原样托管时跑不起来的缺陷，请 W06 在上游修正）

1. **快照未装载**：`MaterialAgentFactory.build()` 只创建了快照仓库，没有调用 `load()`，外壳也拿不到这个对象。不改的话，`read_page` 永远返回“快照中没有第 N 页”。
2. **提示词中的 `%` 未转义**：`MetricAgentFactory` 的提示词里有“亿元/% 等”，又经过 `String.formatted()`，构造 Agent 时就抛 `UnknownFormatConversionException`，指标 Agent 无法启动。改为 `%%` 后输出文字不变。
3. **`ask_user` 工具没有参数 schema**：指标、经营、资产质量、写作四个工厂里的 `ask_user` 都没有 `inputParams`，模型接口拒绝整个请求（HTTP 400：`tools.N.function.parameters must be a JSON Schema of type object`）。补上 W06 自己在 `list_metrics_index` 里用的空对象 schema 即可。

`build.sh` 对每处适配都做断言：上游已修复、原文变化、或命中次数不是 1 时停止构建。

另外，W06 的 `checker` 模块（`ToolGateway` 用到 PDFBox 3 的 `org.apache.pdfbox.Loader`）在当前依赖下编译失败，但本外壳不需要它。

## 行为说明

- **写作 Agent 的中断**：写作 Agent 注册了 W06 的 `AskUserRail`。输入缺少契约要求的字段时（`task_id`、公司、期间、八指标 artifact、两路草稿），它会调用 `ask_user` 中断等待人工；本外壳目前把这种情况报为任务失败，并在日志里记录中断结果。
- **INPUT_REQUIRED 未接**：把中断映射成 A2A `INPUT_REQUIRED` 并支持恢复，要和年报编排契约一起定，尚未实现。

## 构建与运行

```sh
MAVEN_SETTINGS=<含代理的 settings.xml，可省> sh build.sh <W06 源码目录 .../source/w06-annual-report-agent> <输出目录>
# 年报接收环境（~/report-demo-env），在 WSL 中：
python3 ops/w06_snapshot.py ~/w06-bus/snapshot     # 由已核验的招行第 14—16 页与 W01 answers.json 页码生成
python3 ops/w06_services.py start                  # 五个实例，127.0.0.1:18111—18115，模型取环境配置
python3 ops/w06_bus_register.py register           # RDC 登记；新建角色 w06-bus-trial 追加给执行身份，原角色不动
python3 ops/w06_bus_register.py heartbeat &        # 每 8 秒心跳，404 时重新登记
python3 ops/w06_bus_call.py                        # 经 Gateway 以执行身份调用：资料→指标→经营∥资产质量→写作
python3 ops/w06_check_run.py <calls.json> <起> <止> # 核对原样转抄、对照 W01、Gateway 审计计数、RDC 实例与进程身份
```

`ops/w06_bus_call.py` 是 W05 的测试驱动，它把上一步的真实输出作为下一步的输入，不是 W03 的 Graph 执行器。让年报任务的节点改由这些 Agent 执行，需要与 W03/W04 对齐节点输入（受控材料、授权）和类型化输出契约。
