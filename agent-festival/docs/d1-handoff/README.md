# D1 交接样例：W05（Agent Bus 与能力接入）+ 知识消费

对应 [#16 交付卡](https://github.com/chaosxingxc-orion/agent-hub-alpha/issues/16#issuecomment-6912424270) 的 D1 第 3—5 项。负责人：xierongqiag2015。基线：PR #30 头 `7dfd061`。

每份样例都标明来源，按 [#4 交付标准](https://github.com/chaosxingxc-orion/agent-hub-alpha/issues/4#issuecomment-6904513067) 区分声明层级：

| 层级 | 含义 |
|---|---|
| 代码生成 | 由下列测试驱动真实代码路径输出，可复跑 |
| 契约样例 | 尚未实现，约定结构与验收用例，实现后由代码生成同结构记录 |

## 一、W05：调用身份、映射与观测（代码生成）

生成方式：`D1_SAMPLES_DIR=<目录> go test -run TestD1W05Samples ./agent-festival/go/cmd/credit-demo`（需 Linux）。

运行的是 PR #30 中的 `busFixture`、`BeginCall`、`UpdateCall`、`AgentContext/ModelCall`、`CommitCall`、`graph`，注册中心为测试替身。节点范围凭据已脱敏。

| 文件 | 内容 |
|---|---|
| `w05/01-capability-binding.json` | 节点 → 专家 → RDC agent id → 已批准的绑定（AgentCard 版本、卡片摘要、契约摘要） |
| `w05/02-send-intent.json` | 发送前持久化的意图：节点执行身份、尝试、输入摘要，以及 request/message/context id 和发现快照 |
| `w05/03-accepted.json` | 对方受理：remote task id、Gateway 的 SendMessage 观测（只记元数据，不存正文和凭据） |
| `w05/04-committed-result.json` | 结果按输入摘要提交，得到产物 id 与摘要；模型使用事件 |
| `w05/05-graph-projection.json` | 用户面读取的图投影 |
| `w05/06-unknown-then-retry.json` | 回执丢失：GetTask 返回 TASK_NOT_FOUND 后状态为 unknown；同一节点重试只返回 action=query，SendMessage 仍只发过 1 次 |

身份映射：

| 本项目记号 | PR #30 中的字段 |
|---|---|
| o | `node_execution_id`，即 sha(run, 图版本, 节点, 轮次) |
| a | `attempt` |
| c | `request_id`、`message_id`、`context_id`，加远端的 `remote_task_id` |
| b | 本场景不对外产生业务效果。远端 Agent 调用的同一请求身份就是上述 c；重试不新建 |

## 二、W05：部署能力声明（逐项是否支持）

对应 #16 ALK-04："支持项逐项实测；不支持的如实返回"。

| 能力 | 声明 | 依据 | 验证层级 |
|---|---|---|---|
| 发送去重 | 同一节点、同一输入重试时，不发第二次 SendMessage | `TestBusDuplicateBeginNeverResendsAndRejectsChangedInput`，样例 06 | 作者测试（Linux 通过） |
| 远端幂等（同一 messageId 重发由对方去重） | **不声明** | 未验证对方 Runtime 是否按 messageId 去重 | — |
| 查询 | 支持，按 remote task id 调 GetTask | 样例 06，`TestBusWaitingResumeQueriesOriginalTaskAndFencesOldHost` | 作者测试 |
| 取消 | **不支持** | Runtime 的 A2A 分发没有路由 CancelTask（[`A2aJsonRpcController.java:178-223`](https://github.com/openJiuwen-ai/agent-runtime-java/blob/2b29d534a0b872d840d21c13b94d3843995799b8/service/agent-service-app/src/main/java/com/openjiuwen/service/app/controller/a2a/A2aJsonRpcController.java#L178-L223)） | 源码核对 |
| 宿主重启后恢复 | 已提交的结果直接复用，模型调用不增加 | `TestBusPersistenceReloadReusesArtifactWithoutModelIncrement` | 作者测试 |
| 等待中的任务恢复 | 查询原远端任务，旧宿主被隔离 | `TestBusWaitingResumeQueriesOriginalTaskAndFencesOldHost` | 作者测试 |
| Gateway 或远端 Runtime 丢失任务状态 | 保持 unknown，需人工核对；不自动恢复 | 原 W05 报告 R15/R16 | 作者报告，未复跑 |
| 能力版本漂移 | 发送前拒绝，不写意图 | `TestBusCapabilityVersionDriftRejectsBeforeIntent` | 作者测试 |

## 三、知识消费：跨任务复用（代码生成，组件验证）

生成方式：`K_SAMPLES_DIR=<目录> go test -run TestKnowledgeSamples ./agent-festival/go/cmd/hub-control`，位于分支 `feat/issue16-w04-control`。录下的是 hub-control 的真实 HTTP 请求与回执，会话令牌已替换为占位符。

| 文件 | 场景 | 关键回执 |
|---|---|---|
| `knowledge/k-01-first-consumer-miss.json` | 快照 v1 的第一个消费者 | 认领回执 `lookup.outcome=miss`，原因"没有其他任务消费过这些来源"；照常计算并提交 |
| `knowledge/k-02-cross-task-hit.json` | 新任务、同快照同指令 | `lookup.outcome=hit` 并带 `reuse_candidate`；提交时写 `reused_from`；任务视图显示节省的远端调用为 1 |
| `knowledge/k-03-version-miss.json` | 换成更正版快照 v2 | `miss`，原因"已有结果基于同一材料的其他版本，版本不同不复用" |
| `knowledge/k-04-revoked-before-commit.json` | 查到候选后，提交前原结果被撤回 | 提交返回 409"…已撤回"；之后再查为 `miss`，原因写明撤回 |

**接入 PR #30 的位置**（D2 前实现，作为 Go 服务里的一个模块，不另起服务）：

- 本地节点：在 `StepGet` 判断"已完成则复用"之后、标记 running 之前查找；命中时以 `reused_from` 经 `StepPut` 同一提交入口写入本任务结果。
- 远端节点：在 `BeginCall` 写入发送意图之前查找；命中则不发送，经 `CommitCall` 同一入口提交，节省的远端调用计入投影。
- 两处都要求节点声明 `sources`，并纳入输入摘要。撤回检查与 AR-04 的版本绑定共用同一份"任务绑定"记录。

## 四、知识消费：能力召回与选择生成图（契约样例，尚未实现）

| 文件 | 内容 |
|---|---|
| `knowledge/alk05-scenario-package.json` | 年报场景包：4 个获准能力、1 个只支持母公司口径应被排除的候选、1 个信息不足标为未知的候选；治理状态与摘要 |
| `knowledge/alk05-selection-records.json` | 同一场景包下两种任务重点（经营加资产质量 / 只看资产质量）的选择记录：召回 → 资格判定（通过/排除/未知及原因）→ 排序（注明分数来源）→ 选择 → 数据绑定组合检查 → 图节点映射；另附 4 条验收反例 |

D2 验收对应 ALK-05、ALK-06 和 AR-06：两种任务重点得出有依据的不同选择；高分但不适用的候选被排除且派发次数为零；全部未知不生成图；组合不兼容在采纳前被拒；新增获准能力只改场景包。

## 五、未覆盖

- AgentCard 的真实 id、版本与摘要要等年报专业角色部署后填写（W05/W06）。
- 第一部分是测试替身注册中心上的代码路径，不是真实 Bus 部署的观测；真实部署的观测在 D2 提交。
- 知识复用目前在 hub-control 中完成组件验证，尚未接入 PR #30。
