# hub-control：#16 知识消费（K 路径）与正式结果控制

`hub-control` 服务于 [#16](https://github.com/chaosxingxc-orion/agent-hub-alpha/issues/16) 公开年报 demo，从知识消费方向出发，主要负责两件事：

- **知识消费**：A10，以及 #14 评审中的 F-02C"新任务知识消费快路径"。新任务能不能复用已有结果，按输入、版本、来源、保鲜、权限、适用性判定；每次查找都留下记录和原因；撤回知识只阻止以后的消费和交付，不改写已经发生的记录。详见 [docs/knowledge-consumption.md](docs/knowledge-consumption.md)。
- **知识消费所依托的控制记录**：命令去重、候选图检查与采纳、节点执行身份、远端调用身份、正式结果、检查绑定、交付门禁、审计和状态投影。这些作为接口提供给 W04 和 W05，与 W05 的 Go 控制记录对齐后，应只保留一套。

它不调度图，也不调用模型和 Agent。推进图的是 Java 宿主（W03）；用户面（W02）只读本模块的投影，用户操作以命令形式提交给本模块。

**状态**：原型，单实例。演示身份由本服务签发，不是企业身份认证。下文"保证与未覆盖范围"一节逐条列出了边界。

## 运行

```bash
cd agent-festival/go/cmd/hub-control
export HUB_SECRET_DEMO_APPROVER=... HUB_SECRET_PLANNER=... HUB_SECRET_GRAPH_HOST=... # 见 deploy/bootstrap.example.json
go run . -addr 127.0.0.1:8740 -db /data/hub-control.db -bootstrap deploy/bootstrap.example.json
```

- 数据库是一个 SQLite 文件（WAL，`synchronous=FULL`），必须放在持久卷上。
- `-bootstrap` 只追加和更新账号、材料和策略，可以重复执行。同一策略版本如果内容不同，会被拒绝。
- 健康检查：`GET /healthz` 检查数据库能否应答；`GET /healthz?deep=1` 额外执行 `PRAGMA integrity_check`。

验证：

```bash
go vet ./... && go test ./... -count=1 -v   # 26 项验收/知识消费测试 + 4 项进程级测试（强杀、示例配置、模拟数据）
```

`-race` 需要 cgo。本地 Windows 环境没有 C 工具链，所以没有跑；Linux 上执行 `CGO_ENABLED=1 go test -race ./...` 即可。

### 部署（systemd，单机）

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o hub-control .
# 把 hub-control、deploy/install.sh、deploy/hub-control.service 拷到主机同一目录，
# 再放一份 bootstrap.json（以 deploy/bootstrap.example.json 为模板），然后执行：
sudo sh install.sh
```

- 服务以 `DynamicUser` 运行，默认只监听 `127.0.0.1:8740`。要对外开放，按下面两步做；注意走的是明文 HTTP，令牌和密钥都不加密，正式使用前应在前面加 TLS 反向代理：
  - 在主机安全组放行 8740；
  - 写入 drop-in `/etc/systemd/system/hub-control.service.d/public.conf`，内容为 `[Service]` 加一行 `Environment=HUB_ADDR=0.0.0.0:8740`。
- 数据库放在 `/var/lib/hub-control/`。
- 账号密钥在第一次安装时随机生成，写入 `/opt/hub-control/etc/secrets.env`（0600），重装不会覆盖。
- 资源上限为 `MemoryMax=128M`、`Nice=10`，适合共用主机。实际运行时内存约 3 MB。
- 重新应用 bootstrap 会更新账号的角色和密钥，但不会重新启用已停用的账号。

## 控制台与演示数据

浏览器打开服务根路径 `/`，就是内置的控制台（`control/web/console.html`，单文件，无外部依赖）。登录后可以看到：

- 任务列表；
- 已采纳图，节点按状态着色；
- 报告交付门禁和检查结论；
- 节点的尝试 a 与远端调用 b；
- 待核对的调用；
- 候选图（草稿任务会预览待批准的那一版）；
- 反馈案例和审计事件。

用户可以在控制台里执行批准、暂停/恢复、人工复核、确认交付和提交案例。每次点击都生成一个 `command_id`；网络失败时带同一个 id 重试，保证至多执行一次。

控制台只能操作，不能越权：它调用的接口和其他客户端相同，权限照样由服务端判定。例如 `demo-viewer` 没有任何角色，看到的按钮是禁用的。

Java 宿主（W03）和检查器（W06）接入之前，可以先灌一批**模拟数据**：

```bash
set -a; . /opt/hub-control/etc/secrets.env; set +a
hub-control -seed http://127.0.0.1:8740     # 已有模拟任务时不重复灌；-seed-force 再灌一组
```

会生成 10 个标有【模拟数据】的任务。其中 6 个演示控制链路：

- 已交付；
- 检查通过、待确认交付（资料节点跨任务复用）；
- 引用核验转人工复核；
- 计算检查不通过；
- 候选图待批准（v1 在挂载前被拒）；
- 执行中断后接管、远端调用结果未知。

另外 4 个演示知识消费：

- 材料换成更正版，版本不同，不复用旧结果；
- 命中更正版的已有抽取结果；
- 依据快照被撤回：已有记录保留，但禁止交付；
- 已撤回的快照不能再启动新任务。

已有同名任务时跳过对应场景。账号见 `deploy/bootstrap.example.json`，其中 `demo-curator` 负责撤回知识，`hub-admin` 负责登记材料。

远端 Agent 的回执和检查结论都是模拟的；指标数值取自 W01 草稿（#21），还没有人工核对。

## 身份与对象

记号沿用 [#19](https://github.com/chaosxingxc-orion/agent-hub-alpha/issues/19#issuecomment-6824365882) 和 [#14](https://github.com/chaosxingxc-orion/agent-hub-alpha/pull/14#issuecomment-6824366971) 的评审回复。

| 记号 | 含义 | 在本模块中的形态 |
|---|---|---|
| T | 任务 | `tasks.id`，由规划者提交候选图时创建，用户采纳后生效 |
| g | 已采纳图版本 | `tasks.adopted_rev` + `adopted_digest`。采纳后冻结，改目标或改拓扑需要新建任务 |
| o | 逻辑节点执行 | `ExecID(T, g, node, iter)` = `T1/g1/write#0`。重试和恢复都保留 o |
| a | 尝试 | `attempts(o, a)`，每次 claim 新建一个。接管后旧尝试记为 `lost` |
| b | 一次远端请求 | `CallID(o, name)` = `T1/g1/extract#0!ask`。重试复用同一个 b，不为同一意图另起请求 |
| κ | 材料摘要 | `jcs-sha256-v1:` + SHA-256(RFC 8785 规范化材料)。**一律由本模块从材料本身计算**，不接受调用方提交的摘要 |
| epoch | 执行所有权代次 | `tasks.lease_epoch`。所有执行写入都要带当前租约令牌 |

需要额外说明的几点：

- 用户命令按 `(scope, issuer, command_id)` 唯一。issuer 是会话令牌里的主体，不取请求头。
- 节点 o 在第一次 claim 时绑定 κ_in；之后改了输入材料，一律判为冲突。
- 每个 o 最多一个正式结果，每个 b 只绑定一个载荷。这些都由表约束保证，不靠代码路径。

## 接口

全部接口都要求 `Authorization: Bearer <POST /v1/login 返回的令牌>`。`X-User-ID` 之类的请求头会被忽略。

| 面 | 接口 | 调用方 |
|---|---|---|
| 管理 | `POST /v1/admin/policies`、`POST /v1/admin/materials` | admin |
| 用户 | `POST /v1/plans` | 规划者（agent）或用户：提交候选图，返回检查问题和需要确认的摘要 |
| 用户 | `POST /v1/commands/{adopt,pause,resume,review,accept,case}` | user，必须带 `command_id`（每次点击生成一次） |
| 运行 | `POST /v1/exec/{lease,heartbeat,release,claim,commit,fail,progress}` | executor（Java 宿主） |
| 运行 | `POST /v1/exec/calls/{bind,dispatch,observe}` | executor |
| 检查 | `POST /v1/checks` | checker（W06），只能以自己被授权的 checker 名义写入 |
| 投影 | `GET /v1/tasks/{id}`、`/v1/tasks/{id}/unresolved`、`/v1/events?task=&after=`、`/v1/artifacts/{digest}` | 已登录主体，只读 |
| 出口 | `POST /v1/permits/verify` | 出口代理（P04 预留，demo Gateway 未接） |

### 节点包装组件的调用顺序（W03 对接）

```text
lease(task)                         → token, epoch, graph, unresolved calls
claim(task, token, node, iter, material)
  action=reuse        → 直接返回 result.body，不调用模型或工具（A05）
  action=in_progress  → 等待或加入原尝试，不另起
  action=run          → 拿到 a；如果带 reuse_candidate，可以按跨任务复用提交
    bind(o, a, name, target, payload) → b, κ
    dispatch(b)                      → permit；发送前最后一步
    observe(b, accepted|succeeded|failed|timeout, remote_ref, result)
  commit(o, a, completed|business_rejected, body[, basis][, reused_from])
  或 fail(o, a, detail)           技术失败，不是业务拒绝
heartbeat / release
```

与 W05 `credit-demo` 执行协议（`/exec/*`）的对应关系：

| W05 | hub-control | 差别 |
|---|---|---|
| `/exec/lease`、`/exec/heartbeat` | `lease`、`heartbeat` | 相同（租约 + 令牌 + epoch） |
| `/exec/step/get` | `claim` | 多了 κ_in 绑定、依赖就绪检查、暂停检查、尝试 a、跨任务复用候选 |
| `/exec/step/put` | `commit` | 结果按内容寻址；区分正式拒绝和技术失败；未决调用阻塞提交 |
| `BeginCall`/`UpdateCall`/`CommitCall` | `bind`+`dispatch` / `observe` / `commit` | b 由 (o, name) 确定；是否允许重发由能力契约声明 |
| `/exec/branch` | `progress` | 按 (o, a, seq) 幂等 |
| `/exec/done` | `release` | 未决调用不会被改写为完成 |

## 验收覆盖（本模块范围）

| #16 验收项 | 测试 | 本模块证明了什么 |
|---|---|---|
| A01 | `TestA01_HappyPathDeliversCheckedReport` | 候选图→采纳→四节点→两项检查→交付，链路完整（远端调用为模拟观测） |
| A02 | `TestA02_InvalidGraphRejectedBeforeMount` | 未登记能力、未知依赖、环、超界循环、未登记材料都在挂载前拒绝，不可租用 |
| A03 | `TestA03_SameCommandSameContentReturnsOriginal` | 同一 command_id、同一内容返回原结果，只有一次采纳 |
| A04 | `TestA04_SameIdentityChangedInputIsConflict` | 命令、节点 κ_in、调用载荷、正式结果四层改输入都判冲突，原记录不变 |
| A05 | `TestA05_…`、`TestKill9MidRunThenRestart` | 控制进程被强杀重启后，已完成节点复用、尝试数为 0 增量；旧执行者被拒绝 |
| A06 | `TestA06_LostReplyIsQueriedNotResent` | 超时转为 unknown，不重发；已受理的调用改为查询；迟到或重复回执、旧执行者的回执都只记录不生效 |
| A09 | `TestA09_ChangedReportNeedsNewCheckAndApproval` | 检查绑定精确摘要；改过的报告不能交付，也不能顶替正式结果；修订产物需要重新检查 |
| A10 | `TestA10_CrossTaskReuseIsBoundToMaterialVersion` | 只有声明可复用的能力、κ_in 相同时才能跨任务复用；换材料版本后不复用；不能伪造来源 |
| A10 / F-02C | `TestK_*`（5 项） | 来源必须在获批材料内；未命中给出具体原因；命中时统计省下的调用；查到之后、提交之前撤回会被拒；撤回后禁止交付，但不改写历史记录。见 [docs/knowledge-consumption.md](docs/knowledge-consumption.md) |
| A11 | `TestA11_PauseStopsNewWorkButNotInFlightCalls` | 暂停后不启动新节点、不派发新调用，在途调用照常落地，身份不变 |
| A12 | `TestA12_DuplicateAndLateProgressNeverRegress` | 重复或乱序事件不倒退；重启后投影一致 |
| A13 | `TestA13_FailedWriteLeavesNothingVisible`、`TestKill9DuringWriteBurst` | 提交失败时整笔回滚、无痕迹；强杀后已确认的写入全部还在，integrity_check 通过 |
| A14 | `TestA14_CaseLinksOldEvidenceAndNewPolicyAppliesToNewTasks` | 反馈案例关联旧证据和旧策略版本；新策略只作用于新任务 |
| #20 矩阵 | `TestI20_*` | 见 [docs/control-rights.md](docs/control-rights.md) §3 |

## 保证与未覆盖范围

- **单实例。** 一个进程独占一个数据库文件。没有多实例接管和 HA。控制进程不可用时，执行者拿不到 claim，只能等待，不会自行推进。
- **演示身份。** 账号密钥由本服务校验，会话由本服务签名。每次采纳和交付都记录 `assurance=demo-account`，投影里会明确标注。没有接 WorkSwarm 企业版或 IdP。
- **执行者可信度。** 执行者是认证过的运行面主体。节点输入材料和产物内容由执行者提供，本模块对其计算 κ 并绑定，但不能证明执行者没有虚报材料。
- **出口。** 派发许可绑定 (b, κ, epoch, 第几次发送)，有效期默认 15 秒。Agent Bus Gateway 目前不校验许可，所以刚失去租约的执行者在许可有效期内仍可能发出请求。这是公开的剩余风险，P04 出口代理可以用 `/v1/permits/verify` 补上。
- **远端恢复。** 远端 Runtime 或 Gateway 丢失状态时，本模块把调用保持在 unknown 或 accepted，并给出唯一允许的下一步（查询、按契约重发、人工核对），不会自动推断结果。
- **知识适用性**只按精确等价判定（能力定义、来源版本、指令、模板都相同）。近似问题的语义复用要依赖 #6 能力知识服务给出适用性声明。
- **报告语义检查**属于 W06。本模块只保证检查结论绑定到精确摘要和规则版本，以及门禁据此决定能否交付。
- **掉电持久性**依赖 SQLite `synchronous=FULL` 加底层存储的 fsync 语义。已在 Windows 上用进程强杀验证，掉电场景没有实测。

## 复用清单（对应 #4 统一回复 C04）

| 组件 | 复用方式 | 原因 |
|---|---|---|
| `internal/rfc8785`（festival） | 直接导入，用于计算 κ | 已有差分测试的 RFC 8785 实现 |
| `internal/adopt`、`internal/evidlog`（festival） | 不复用 | 每次追加都要整文件重写，没有 fsync（#11），而且正式结果和账本是两个文件（#4 CX4）。本模块用单个 SQLite 事务把状态转移和审计事件一起提交 |
| SQLite（`modernc.org/sqlite`，纯 Go） | 存储 | 有事务、唯一约束和 WAL，单文件便于挂持久卷，不需要 cgo |
| W05 `credit-demo` 执行协议 | 语义兼容（见上表） | 便于 Java 宿主从 W05 迁移过来，不另起一套协议 |
