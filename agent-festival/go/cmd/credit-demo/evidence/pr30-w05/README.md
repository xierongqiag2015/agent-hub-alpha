# PR #30 组合候选：W05 单侧部署与 Bus 恢复记录（41a3675）

按项目负责人批准的 B 方案，W05 在自己的环境独立部署 PR #30 当前头，并运行仓库自带的 Agent Bus 验收脚本。这里只是 **W05 单侧证据**：
- 不是联合验收；
- 不走 WorkSwarm 插件路径（插件操作由 W02 负责）；
- 不涉及年报场景。

原始记录在 `raw/`，从本文件开始看。

## 环境

| 项 | 值 |
|---|---|
| 源码 | `41a36755563ebeb442f2d93050c1d3430d510e80`（PR #30 当前头，取自 GitCode MR !11），未改代码 |
| 宿主 | Windows 11 + WSL2 Ubuntu 24.04.4（内核 6.6.87.2-microsoft-standard-WSL2）+ Docker Desktop 29.2.1 / Compose 5.1.0；Docker 可用 16 核、16 GB |
| 构建与启动 | `demo.py --bus prepare` → `demo.py --bus build all` → `demo.py --bus start` |
| 模型 | GLM，经 OpenAI 兼容接口调用；接口返回的模型标识为 `glm-5-2-260617`。W02 用 qwen-plus |
| 凭据 | 模型配置、演示密码、Bus 凭据都在仓库外的 600 权限文件中；证据文件已按实际取值扫描，均未出现 |

**来源与工具链**（`sources.lock.json` 原样）：
- agent-solution `bf702bc`，取自 lock 中的 GitCode 地址。
- WorkSwarm `1142e5fb3`：从 GitHub 拉取多次中断，改从 GitCode 镜像 `openJiuwen/jiuwenswarm` 取同一提交。提交哈希一致，origin 仍为 lock 中的 GitHub 地址。
- Go 1.25.0：按 lock 的 sha256 校验。
- uv 0.11.7：PyPI wheel，已校验 sha256。

**构建代理**：
- Debian 安全源的 `openjdk-21-jdk-headless` 从本机连不通，直接构建两次都失败。
- 因此用仓库已支持的构建参数 `HTTP_PROXY` / `HTTPS_PROXY` 经本机代理构建。
- 代理只用于构建：Docker 预定义的代理参数不进入镜像层，`maven_settings.py` 用后删除；运行时不走代理。
- 第一次带代理构建时 WorkSwarm 的 `npm ci` 偶发失败，第二次通过（`raw/build-milestones.log`）。

### 镜像

| 服务 | 镜像 | ID |
|---|---|---|
| credit | agent-hub-issue16/credit:bus-v1 | `5e5c5229ff7b` |
| orchestrator | agent-hub-issue16/orchestrator:bus-v1 | `7c454e5240fb` |
| workswarm | agent-hub-issue16/workswarm:1142e5fb3-local | `8fa8ec4239e1` |
| professional（material/analysis/writer） | agent-hub-issue16/professional:bus-v1 | `6b19773e21e6` |
| rdc | agent-hub-issue16/rdc:bf702bc | `315186974da9` |
| gateway | agent-hub-issue16/gateway:bf702bc | `cac5d07658ae` |
| bus-db | postgres:15-alpine（lock 中的 digest） | `09e4f20b14dd` |
| 基础镜像 | python:3.11-slim | `sha256:07d4824036af…`（构建当日拉取；compose 未固定该 digest，两边可能不同） |
| 基础镜像 | node:22-slim | `sha256:e9f565d32859f0cbfb52125eafe8be39390f24dac302dc3cdf566e8ba3ab21ee`（构建日志中 BuildKit 解析的 digest；只在构建阶段使用，未留在本地镜像库） |
| front | nginx:alpine（compose 浮动标签） | `sha256:fed7e14be83cbd2661a75cdc5b872d936cf155d46c327084d30b8df19ae88d72` |

- 镜像都在本地构建，没有仓库 digest；构建清单见 `raw/build-all.json`。
- 挂载进 WorkSwarm 的插件文件（sha256 前 16 位）：
  - `frontend/dist/app.js` `df366b46d0c5a512`
  - `extension.py` `d3b9a19256be3e00`
  - `extension.yaml` `4e2543f79d48343d`
  - `web/graph-flow.js` `d93821a712a1b3f2`

  前三项与 W02 回填一致。
- 执行人：xierongqiag2015（W05），脚本执行；脚本见 `scripts/` 与 `deploy/smoke_bus.py`。
- 日志中的本机用户路径已替换为 `<home>`。
- 配置文件摘要（sha256 前 16 位）：
  - compose.yaml `3766502dc524aed0`
  - compose.bus.yaml `9be9ee4c3ddf6ed4`
  - sources.lock.json `bbf15ebf3a25dea6`
- 完整值见各证据文件 `environment.config_sha256`。

## 结果（2026-11-17，北京时间）

启动后 10 个服务全部健康，3 个 Bus 角色被 RDC 发现且 ONLINE（11:25）。

| 场景 | 任务 | 注入 | 结果 |
|---|---|---|---|
| normal | R1 | 无 | 7 个节点完成，其中 5 个经 Bus 调用 3 个真实角色；并行分析、上下文隔离、产物关联检查通过 |
| host-kill | R2 | intake 完成、financial/industry/bureau 已派发未返回时，SIGKILL 编排器并重启 | 完成；intake 的产物与模型事件不变（调用增量 0）；三个在途节点沿用原请求、未重发；共领取 2 次 |
| disconnect | R3 | 丢弃一次 GetTask 应答 | intake 进入 unknown，任务 waiting；登录后恢复，沿用原请求与原 task；5 个远端请求各 1 次 SendMessage；完成 |
| late | R4 | GetTask 应答迟到 | 同上；intake 的模型事件前后一致；5 次 SendMessage；完成 |
| storage-failure | R5 | 远端结果已完成、写正式状态时让原子 rename 真实失败 | 服务冻结、健康检查 503；落盘前不显示成功；下游未启动；修复存储并重启后对账原 task，未重算远端；完成 |
| gateway-restart | R6 | intake 已派发时重启 Gateway | 任务停在 waiting，intake 为 unknown；恢复只查询原任务、未重发；下游保持 pending |
| runtime-restart | R7 | intake 已派发时重启专业 Runtime | 同上 |
| probe_bus --rdc-outage | — | 协议准入与 RDC 不可用 | 12 项通过：三张卡与实例、未授权 401、未知 task、非法请求 400、未知能力 503、Runtime 拒绝越权范围、未注册 Agent 与伪造地址被拒、缺权限预检不过、RDC 不可用时目录标为不可用 |

原始记录：`raw/<场景>-<时间戳>.json`（含逐 0.5 秒的图快照与运行记录，已按脚本规则脱敏），控制台输出见 `raw/smoke-console.log`。

**关于 `checks`**：
- 各文件的 `checks` 是脚本在断言通过后写入的结论，核对时请以快照、`gateway_exchanges`、`transport_events` 等原始记录为准。
- disconnect、late 中 `no_completed_node_model_increment=false` 不是失败：这两个场景在恢复前没有已完成节点（intake 是第一个节点、处于 unknown），这一项不适用。已完成节点的调用增量见 host-kill 与 go-kill。

**storage-failure 的原始时间线**（2026-11-17 20:23 补跑，R10，`raw/storage-failure-traced-*`）：
- 原 storage-failure 记录里"健康检查 503"和注入、恢复时刻只体现在脚本断言中。
- 补跑时用 `scripts/w05_rd_extra.py storage-failure-traced` 原样调用 smoke_bus.py 的 storage-failure，同时记录：
  - 每条注入/恢复命令的时间戳；
  - 每 0.25 秒一次的 `/healthz` 状态码与响应。

| 相对注入 | 事件 |
|---|---|
| +0.00s | 注入：credit 容器内把 `state.json` 改名为备份并以目录占位（原子 rename 真实失败的前提） |
| +0.21s | 放行已完成的远端结果（故障代理） |
| +0.51s | `/healthz` → 503 `durable state unavailable; restore storage and restart` |
| +0.92s | 恢复存储（删除占位目录、改回原文件） |
| +1.15s | 重启 credit（期间 healthz 连接被重置） |
| +1.90s | 重启编排器 |
| +2.81s | `/healthz` → 200 |

R10 最终完成，intake 未重算（四项检查同原场景）。完整序列见 `raw/storage-failure-traced-timeline.txt`，编排器与 credit 日志在恢复前导出。

注意：该文件的 `healthz_summary` 字段因脚本比较类型错误全为 0，已在脚本中修正；逐条的 `healthz_samples` 是正确的原始记录。

**边界**：Gateway 与 Runtime 重启后，上游任务的归属不在其内存中保留，原任务只能停在 unknown，需要人工核对。这次验证的是不重发、下游不启动，**不是**自动无损续跑。

## R-D 补充：执行中停 Go 与两个客户端并发恢复（2026-11-17 19:00）

脚本：`scripts/w05_rd_extra.py`（基于 smoke_bus.py 的辅助函数，只记录与判定，不在中途断言退出）、`scripts/resume_once.py`。两个场景都在故障代理后运行，每次 SendMessage 按请求号在传输层计数。

| 场景 | 任务 | 注入 | 结果 |
|---|---|---|---|
| two-clients | R9 | 先丢一次 GetTask 应答让任务 waiting，再从插件 API（`/api/resume`）和已登录确认页会话两处同时恢复 | 只有一个被接受（插件 API 200），另一个 409；只新增 1 次领取；intake 沿用原请求与原 task；5 个远端请求各 1 次 SendMessage；任务完成 |
| go-kill | R8 | intake 完成、financial/industry/bureau 刚派发时 SIGKILL Go 控制服务，5 秒后启动，14.6 秒后健康 | 见下 |

**go-kill 结果**（时间线见 `raw/go-kill-transport-timeline.txt`）：

**符合预期的部分：**
- 新租约接管（第 2 次领取），旧执行者失权；
- 已完成的 intake 产物与模型事件不变，调用增量为 0；
- 三个在途节点各 1 次 SendMessage，恢复后用原 task 查询（+16.3s），没有重发。

**发现：Go 停机会导致在途远端任务失败。**
- financial 的专业 Runtime 在执行时要从 Go 的成员工具接口 `credit:8091/member-mcp/financial-analyst` 取数据工具。
- Go 停机期间连接失败（`Node tools unavailable` → `Professional node failed`），远端任务以 `TASK_STATE_FAILED` 结束，见 `raw/go-kill-analysis-agent-errors.log`。
- industry/bureau 在停机前已拿到工具，继续执行。

**系统如实停下：**
- 任务停在 waiting，给出"等待远端结果；未确认的调用不会自动重发"；
- 人工恢复一次后，industry/bureau 取回原 task 结果（请求号、task、模型事件均不变，无新增远端调用）；
- financial 仍为 failed，任务以 failed 结束，原因 `step id=financial failed: Remote task ended: TASK_STATE_FAILED`；
- 下游 judge/report 未执行，不伪成功。

**另两处观察：**
- 任务进入 waiting/failed 后，本地黑盒节点 related 的投影仍显示 running（Done 进入等待或失败时未重置正在执行的本地节点），用户面可能误读，需与 W02 对齐。
- 故障代理 overlay 与事后恢复都会重建编排器容器，本次编排器日志随旧容器丢失；以后复跑先导出日志再恢复。

**结论**：Go 停机期间不重发、不伪成功、旧执行者失权都成立。但"恢复复用原结果"只对停机前已完成、或已拿到工具的节点成立。依赖 Go 成员工具的在途远端节点会在远端失败，远端节点 attempt 固定为 1，系统不会自动重试，只能人工处置或新版本计划。这是专业 Runtime 对控制服务的可用性耦合，需要在共同验收中明确为边界。

## D2 场景自测（2026-11-17 21:45，`raw/d2-selftest/`）

明天组合 D2 中 W05 负责的场景，先在 W05 环境用脚本自测。恢复都经 `/api/resume`（插件调用的同一接口）发起。D2 当天可加 `--resume external`，改由人在插件里操作。

两个新增工具都只用于测试：
- `scripts/w05_fault_proxy.py`：仓库故障代理的副本，只加一个模式 `drop-send-reply-once`——照常转发 SendMessage，但给调用方返回 502。原有模式行为不变。
- `scripts/compose.w05-send-test.yaml`：把上面这份代理挂在原路径上。

产品代码未改。

| 场景 | 任务 | 结果 |
|---|---|---|
| R-A：2 个远端节点完成后，下一个查询应答丢失（`ra-completed-then-unknown`） | R11 | Bus 部分全部符合预期：intake、bureau 未重算；financial 进入 unknown，恢复后按原 task 查询完成；4 个远端请求各 1 次 SendMessage。**但任务以 failed 结束**，见下方发现 |
| R-A 变体：四个远端节点和本地黑盒都完成后，report 的查询应答丢失（`ra-unknown-at-report`） | R12 | intake、financial、industry、bureau 未重算；report 恢复后按原 task 查询完成；5 个请求各 1 次；任务完成 |
| R-C：等待中注入保存失败后恢复（`rc-resume-storage-failure`） | R13 | 恢复返回 503 未被接受，healthz 503；修复存储重启后任务仍为 waiting；第二次恢复被接受，任务完成；request/task 不变，各 1 次 SendMessage |
| R-B：首条 SendMessage 回执丢失（`rb-send-reply-lost`） | R14 | 远端任务已创建（代理记录了上游返回的 task id），调用方只拿到 502，intake 为 UNKNOWN 且无 task id；恢复后仍为 UNKNOWN、未重发（SendMessage 1 次），任务停在 waiting。上游任务成为孤儿，需要人工对账，这是现有设计边界 |
| R-B：恢复已落盘、回复丢失（`rb-resume-reply-lost`） | R15 | 发出恢复后不读回复直接断开；1.5 秒后任务已为 queued/running（恢复已落盘）；再提交返回 409，无第二次效果；只新增 1 次领取；任务完成 |

**发现：同一编排器进程内恢复时，Loop Forest 节点重跑失败**（R11）。
- 经过：related（本地黑盒，Loop Forest）在第 1 次领取中已开始执行。financial 进入 unknown 使任务中止，related 注册的进程级全局工具 `fork_subtask` 没有卸载。恢复后同一进程再次执行 related，报 `loop-forest process-global tool registration failed: fork_subtask`，任务失败。
- 出处：agent-solution `common/agent-core-ext-java/agent-core-ext-react-rails/.../LoopForestSuite.java` 的 `registerTool`（`Runner.resourceMgr()` 是进程级注册表），以及 credit-orchestrator 对它的使用，不在 W05 Bus 链路。
- 之前没碰到的原因：host-kill 是编排器进程重启后接管；disconnect、late 在第一个节点就进入等待，related 尚未开始。
- 对 D2 的影响：W02 在插件上做 R-A 时，如果 related 已经开始，也会遇到。R-A 可改用 report 变体验证恢复语义，这个问题单独交给编排器/Loop Forest 的负责方。

## AR-04 M1–M3：执行端模型绑定的真实环境实验（2026-11-17 23:44，`raw/ar04-m14/`）

**被测对象**：
- 候选分支 `ar04-exec-binding` 的 `6e42596`：在 #31 `de6e4c1` 之上加执行端模型绑定（PR #31 复核的 M1/M2）；
- 本地构建镜像 `w05-m14/credit:dev`（`33e1d461ff02`）、`w05-m14/orchestrator:dev`（`ac312605b005`）、`w05-m14/professional:dev`（`2154dc58db63`）；
- 构建时容器内 Go 测试全部通过。

**实验环境**：
- 独立 compose 项目（`agent-hub-m14b`，credit 在 5291），不影响 D2 环境；
- 执行端只能经 `scripts/model_egress.py` 访问真实模型接口。该记录器只记时间、调用方、路径、请求里的 `model` 字段和状态码，不记内容和密钥；
- 叠加文件 `scripts/compose.w05-m14.yaml`，驱动脚本 `scripts/ar04_m14_experiment.py`；
- 模型：A = `glm-5-2-260617`，B = `glm-4-flash`。

| 阶段 | 做法 | 结果（run3，完整顺序重跑） |
|---|---|---|
| M3 升级 | 旧头 41a3675 镜像上采纳含判断节点的任务 R1，intake 远端已派发时 kill 编排器；换候选镜像启动 | R1 接管时停为 blocked：`任务在版本绑定上线前采纳，没有可解析的规约绑定…不会改用当前规约继续`；升级后无新领取、无新远端调用、出口 0 请求 |
| M1 本地 | 任务绑定 A，编排器配置 B | 领取被拒，R2 blocked：`任务绑定 glm-5-2-260617，执行器 orchestrator-1 上报 glm-4-flash`；从未领取，无远端调用，出口 0 请求 |
| M1 远端 | 编排器 A，三个专业 Runtime 配置 B | 领取正常（领取记录写明模型 A）；intake 的 Runtime 在调用模型前上报 B，R3 blocked：`远端 credit-material 上报 glm-4-flash`；无模型事件，出口上 Runtime 0 请求，没有任何发往 B 的请求 |
| M2 正例 | 全部 A | R4 完成。出口 19 次请求全部为 A（编排器 10、Runtime 9），Runtime 侧 9 次模型事件逐次记录实际模型 A，与出口计数一致；任务绑定 A |

**关于 run1**：第一次完整顺序运行中，M3、M1 本地、M1 远端的结果与上表相同，M2 失败。
- 现象：intake 的远端任务在派发后 1 秒内结束为 `TASK_STATE_FAILED`，Runtime 没有错误日志，出口 0 请求（任何模型都没被调用）。
- 复现情况：单独重跑 M2（run2）通过，完整顺序重跑（run3）也全部通过，未复现。
- 结论（当时）：原因未查明，可能与 M2 开始前 12 秒刚重建 Runtime 容器有关。这一失败没有发生任何模型调用，不影响绑定结论。原始记录保留在 `run1-*`。
- 2026-11-18 复跑已查明，见下一节。当时说“Runtime 没有错误日志”，是因为只看了 intake 绑定的 material-agent；错误其实记在另一个 Runtime 的日志里。

**边界**：保证的是"执行端配置并实际请求的模型名"等于任务绑定（出口记录的是请求中的 `model` 字段）。如果服务商在同一模型名下换了实际模型，这里检测不到。旧版本执行端（不上报模型）一律拒绝。

## AR-04 复跑：7b58ed1（2026-11-18 13:05–13:28，`raw/ar04-m14-r2/`）

**被测对象**：
- PR #31 当前头 `7b58ed1`：复核 P1（blocked 任务不再给任何节点发新许可）和 P2（只拒绝不匹配的执行器，任务继续排队）的修复；
- `7b58ed1` 只改 Go，只重建了 `w05-m14/credit:dev`（`4c47390ddd2c`）；编排器与 Runtime 镜像沿用上节（Java 自 `6e42596` 起未改）。

**记录器修正**（复核指出旧记录器在上游返回后才写记录）：
- `scripts/model_egress.py` 现在每个请求写两行，共用 `request_id`：转发前写 `request_started`（含调用方、`model` 字段、在途数），并 fsync；上游返回或失败后写 `response_finished` / `error`；
- `scripts/recorder_timing_check.py` 按复核的反例离线检查：假上游收到请求后阻塞，此时记录里必须已有 `request_started`。新记录器通过；旧记录器在响应前没有记录文件，检查失败；
- 驱动脚本按 `request_started` 计数，所以“出口 0 请求”包括发出后没有返回的请求。

| 阶段 | 结果 |
|---|---|
| M3 升级 | run4：与上节相同，接管时停为 blocked，原因指向规约不可解析；升级后无新领取、无新远端调用、出口 0 请求 |
| M1 本地（按 P2 改了预期） | run4：任务保持 `queued`，等待模型 A 的执行器；拒绝记录写明 `orchestrator-1` 上报 B、任务绑定 A，22 次轮询都被拒（同一条记录累加计数）；从未领取，无远端调用，出口无 `request_started` |
| M1 远端 | run4、run5、run6 三次一致：blocked，原因同时写出两个模型；无模型事件，Runtime 出口 0 请求，没有发往 B 的请求 |
| M2 正例 | run4、run5 失败，run6 通过（见下） |

**M2 失败的原因**：
- run4（完整顺序）：intake 绑定 `credit-material`，但收到这个远端任务的是 writer-agent（material-agent 日志里没有请求）。writer 调 `/agent/context` 得到 409，节点失败。0 次模型请求。
- 为验证，驱动补记了 Runtime 重建前后的容器 IP，再成对运行：
  - run5（`m14f`，就绪后立即发起）：material `.6→.4`、analysis `.3→.6`、writer `.4→.3`。intake 被 analysis-agent 收到，它的新 IP 正是 material 的旧 IP；同样是 `/agent/context` 409，0 次模型请求；
  - run6（`m14g`，就绪后等 35 秒）：IP 同样轮换，M2 完成。出口 19 次 `request_started`（编排器 10、Runtime 9）全部为 A，均有对应的 `response_finished`（200）；Runtime 侧模型事件逐次记录 A。
- 已证实：失败时，Gateway 把任务送到了占用“绑定 Runtime 旧 IP”的另一个容器。Go 在 `/agent/context` 比对服务身份，不一致就拒绝，所以错投的任务没有执行，也没有调用模型。
- 推断（未直接观测）：Gateway（JVM）缓存了重建前的 DNS 解析。JVM 默认正向缓存 30 秒，与“等 35 秒后通过”一致。
- 结论：这不是 AR-04 绑定的缺陷。问题出在原地重建 Runtime（同一主机名、新 IP）后的路由可用性，归 Gateway / 部署处理。实验的就绪条件改为：compose 健康检查通过，再等 35 秒（`M14_SETTLE`）。

## R-A 同进程恢复：候选 e1961fe8（2026-11-18 21:42—21:44，`raw/d2-e196/`）

**被测对象**：
- 集成候选 `e1961fe8`，含 #44 的 SDK 关闭补丁：编排器内 `agent-core-ext-orchestration` 版本为 `0.1.3-aha-drain1`，已从镜像内 jar 读回。
- 本地构建镜像 `w05-e196/{orchestrator,professional,credit}:dev`，完整 ID 见 `run.log`。
- 独立 compose 项目 `agent-hub-e196`，credit 在 5391，不影响 41a3675 的 D2 环境；Runtime 健康后再等 35 秒。
- 镜像与源码的对应（11-20 应 W02 要求补充）：
  - `raw/d2-e196/build-e196.log`：构建记录摘录，含源码 `e1961fe8dcfc1c859b515854f793f24948d844fe`、SDK 补丁应用（`patching file …BlueprintScheduler.java`）、以 `-Drevision=0.1.3-aha-drain1` 构建与安装 SDK 的各行，以及三个镜像 ID：orchestrator `4d92ee97b81d…`、professional `733535de3330…`、credit `d5c31ece3def…`。运行记录 `environment.images` 中的编排器镜像与之相同。
  - `raw/d2-e196/orchestrator-sdk-readback.txt`：从同一编排器镜像内的 jar 读回 `agent-core-ext-orchestration` 的 `version=0.1.3-aha-drain1`；`BlueprintScheduler.class` 含 `shutdownNow`、`awaitTermination`、`isTerminated`（补丁后的 `close()`）。
  - 工具注册释放没有单独的卸载日志；证据是间接的：恢复后同进程重新安装 Loop Forest，没有 `fork_subtask` 冲突。

**做法**：
- 沿用 `w05_rd_extra.py ra-completed-then-unknown`，以 `--resume external` 运行。
- 外层 `e196_ra_watch.py` 在任务进入 waiting 后，向编排器 JVM 发 SIGQUIT 取线程转储，随后立即经 `/api/resume`（插件使用的同一接口）恢复，并在前后三次记录编排器进程标识。

**结果（R1，一次恢复即完成）**：

| 时刻（UTC） | 事件 |
|---|---|
| 13:42:51.801 | 第 1 次领取（epoch 1） |
| 13:43:17.996 | intake、bureau 完成后注入 drop-query-once；financial 的查询应答被丢弃 |
| 13:43:18.090 | related 的 member-MCP 连接被中断（InterruptedException） |
| 13:43:18.223 | related 在途的模型调用被取消（CancellationException: model call interrupted） |
| 13:43:18.228 | `run R1 failed`：try-with-resources 先执行 `close()`，等成员退出后才记录失败并上报，任务转 waiting |
| 约 13:43:20.5 | 线程转储：所有线程栈中都没有 BlueprintScheduler / LoopForest / ReActAgent / RemoteAgentMember / Ledgered；只剩空闲线程池线程与 lease 轮询 |
| 13:43:23.250 | 第 2 次领取（epoch 2），同一 JVM |
| 13:44:34.185 | `run R1 done` |

- waiting 时的状态：intake、bureau 完成；financial unknown；industry 远端在途；related 本地在途。
- 同一进程：容器启动时间、重启次数（0）、宿主 PID、容器内 PID 1 的启动 tick，在恢复前、waiting 时、完成后三次一致。
- 全程没有 `fork_subtask` 或重复注册错误。
- intake、bureau 复用（request、产物、model_events 不变，各 2 条模型事件）；financial 按原 task 查询完成；industry 沿用原 request/task 完成；related 在新调度实例中重算后完成。
- 原始传输事件：5 个 request 各 1 次 SendMessage，唯一故障是注入的那次 GetTask 503。

**边界**：
- 本次 related 的模型调用响应了中断。上游不响应中断时，`close()` 会一直等待，宿主被阻塞，这一边界未在此覆盖。
- 恢复走的是 `/api/resume`，与插件同一接口；插件页面路径由 W02 覆盖。

## report 的 400（W02 R9）：时钟回拨与 Java 修复候选

- **被拒的是哪一次**：W02 记录中，report 的 SendMessage 与前 6 次 GetTask 都进了 Go 的记录。之后故障代理还记到一次 GetTask（`1ae4c563`，HTTP 200），它不在 Go 的记录里，之后再无请求。
- **是哪个字段**：41a3675 的 `RemoteAgentMember.rpc()` 生成的各字段取值恒合规；唯一可能不满足的是 `end.Before(start)`，即调用前后两次读取墙钟之间墙钟回拨。
- **旁证**：W05 本机 WSL2 内核日志有 `systemd-journald: Time jumped backwards`。
- **修复候选**：fork 分支 `gateway-time-order`，基于 e1961fe8。
  - `d1dec69`：加时钟注入点（行为不变）与测试。测试驱动真实的 `execute()`，墙钟每读一次后退 50 ms。
  - `7de8c55`：`finished_at` = `started_at` + 单调时钟经过的时间，Go 校验不变。
  - 结果（`raw/d2-e196/java-tests-red-green.log`）：`d1dec69` 上新测试失败（`started_at` 03.700Z，`finished_at` 03.650Z）；`7de8c55` 上 5/5 通过，含 SchedulerRecoveryTest 与 JudgmentErrorsTest。

## 对照 PR #30 的 R-A…U-B 矩阵

| 编号 | 本记录覆盖到的程度 | 仍需补的部分 |
|---|---|---|
| R-A | host-kill：已有完成节点、后续节点在途时，杀执行器后恢复，完成节点调用增量为 0，在途节点沿用原请求 | 从 WorkSwarm 插件触发恢复（W02 操作）；后续节点为 unknown（而非在途）的组合 |
| R-B | 未覆盖 | 恢复已落盘、回复被丢弃 / 浏览器断链（双方） |
| R-C | storage-failure：Go 侧真实写失败，不伪成功、下游不启动、恢复后对账；storage-failure-traced 补了 healthz 503 与注入/恢复时间戳 | 经插件路径恢复时注入保存失败，并把日志关联到该命令 |
| R-D | host-kill（执行器）、gateway-restart、runtime-restart、go-kill（执行中停 Go）、two-clients（两个客户端同时恢复，只有一个生效） | go-kill 发现在途远端节点因依赖 Go 成员工具而在远端失败，需确定处置边界；两个客户端的插件路径操作由 W02 补 |
| M-A | 由上面 AR-04 M1（本地 / 远端）在 #31 `7b58ed1` 上覆盖 | 与 #30 组合后的共同部署上复跑 |
| U-B | 不适用 | 依赖 #31 进入组合候选 |

## 复跑

```sh
cd agent-festival/go/cmd/credit-demo/deploy
python3 demo.py --bus prepare && python3 demo.py --bus build all && python3 demo.py --bus start
CREDIT_DEMO_PASSWORD=<secrets 文件中的值> python3 smoke_bus.py normal
# 故障场景按 deploy/README.md："故障脚本"一节先加入测试代理 overlay，再依次运行
python3 smoke_bus.py host-kill   # disconnect / late / storage-failure / gateway-restart / runtime-restart
python3 probe_bus.py --rdc-outage
python3 demo.py --bus start      # 恢复直连 Gateway
```
