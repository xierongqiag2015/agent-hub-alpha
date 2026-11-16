# 控制权表、故障矩阵与可信来源表（W04，hub-control）

本文回应以下三条评审要求，范围限定为 `hub-control` 这个单实例原型：

- [#19 评审回复](https://github.com/chaosxingxc-orion/agent-hub-alpha/issues/19#issuecomment-6824365882)：控制权表与故障矩阵；
- [#20 评审回复](https://github.com/chaosxingxc-orion/agent-hub-alpha/issues/20#issuecomment-6824366475)：可信来源表与正反用例；
- [#14 评审回复](https://github.com/chaosxingxc-orion/agent-hub-alpha/pull/14#issuecomment-6824366971)：o/a/b 映射，以及技术失败和业务拒绝的区分。

表中每一行都写明提交位置（函数和表约束）以及对应的测试。没有测试的行，标注为"未覆盖"。

## 1. 控制权表（#19 How 1）

| 对象/转移 | 谁检查 | 谁提交、提交到哪里 | 运行者读什么 | 拒绝/等待如何反馈 |
|---|---|---|---|---|
| 图候选 → 采纳版本 | `CheckGraph`（能力是否登记、依赖、环、循环上界、节点数）和 `materialProblems`（材料快照是否登记）。提交候选时查一次，采纳时按当时生效的策略再查一次 | 用户（`approve` 角色，每次请求都重读数据库）调用 `Adopt`，写 `tasks.adopted_rev/adopted_digest/policy_version`。采纳的是用户看到的摘要，摘要不符即拒绝 | `Lease` 返回已采纳的图、修订号、摘要和策略版本；恢复时读原版本，不重新生成 | `blocked`（问题清单）、`conflict`（"计划在展示后变了"，或者任务已采纳、不可替换） |
| 依赖就绪 → 调度节点 | 本模块只检查**准入**：节点在已采纳图内、迭代次数在上界内、依赖节点已有正式结果、任务未暂停 | 下一个节点由 Java 宿主（W03）决定；本模块没有 ready 队列 | `claim` 返回 run/reuse/in_progress | `not_ready`、`paused`、`forbidden` |
| 执行所有权 → 接管 | `fence`：每次写入都核对当前租约令牌和 epoch | `Lease` 发出 epoch+1 和新令牌，同一事务里把旧尝试标为 `lost`、把 `dispatched` 的调用标为 `unknown` | 新的所有者从 `Lease.unresolved` 拿到待处理调用和唯一允许的下一步 | 旧执行者的写入全部返回 `fenced`。重复提交一个已经落地的结果时返回 `duplicate`，不报错 |
| 调用意图 → 已发送/未知/回执 | `BindCall`：目标必须在能力的 targets 内，κ 由本模块计算。`Dispatch`：首次发送只能从 bound 状态发出；再次发送需要能力契约写明 `resend=same_key` | `calls(b)` 持久化状态 bound → dispatched → accepted/unknown → succeeded/failed_confirmed，每条回执都记入 `call_observations` | 发送前最后一步是 `dispatch` 拿许可；"许可已发出但请求未发送"的窗口在接管时转为 unknown | `blocked`：可能已到达对方、契约不允许重发，或者已受理应改为查询。`conflict`：历史只读 |
| 候选产物 → 正式结果 | `Commit`：尝试必须是当前 epoch 下正在运行的那一个；节点没有未决调用；正式拒绝必须给出依据；跨任务复用要求 κ_in 相同、能力可复用、来源产物一致 | `results(o)` 主键保证每个 o 只有一个正式结果；产物按内容寻址，存进 `artifacts(digest)` | 用户面从 `GET /v1/tasks/{id}` 读取结果，节点从 `claim` 的 reuse 返回读取 | `conflict`：同一 o 提交了不同结果。`blocked`：还有未决调用。`invalid`：拿技术失败冒充业务拒绝 |
| 报告 → 交付 | `gate`：每个必需检查器都要在**同一个摘要**、规定的规则版本下给出 pass；human 结论需要复核人通过；error、fail、未覆盖、未配置分别对应不同状态 | 用户（`accept` 角色，当时重读）调用 `Accept`，写 `deliveries(task)`；同一事务里关闭执行租约 | 交付后执行者不能再租用这个任务 | `blocked` 加门禁状态与原因；`conflict`：确认的摘要不是当前正式报告 |
| 用户暂停/补充/重规划 | `Pause` 和 `Resume` 需要 `operate` 角色 | 写 `tasks.paused` | 未派发的：不再派发（`dispatch` 返回 paused）。在途的：照常接收回执，不伪称已取消。已完成的：原样复用。重规划等于新建任务（已采纳的图冻结） | `paused`。"补充材料"目前没有单独命令，归为未覆盖 |

## 2. 故障矩阵（#19 验收标准，#14 §1）

| 故障点 | 处理 | 测试 |
|---|---|---|
| 发送前崩溃（已绑定、未拿许可） | b 保持 bound；新的所有者可以正常发送一次 | `TestA04`（重新绑定返回同一个 b） |
| 拿到许可、发送结果未知（执行者或控制进程崩溃） | 接管时 dispatched 转为 unknown；`dispatch` 拒绝重发（契约为 never）；`unresolved` 给出"人工核对"或"查询" | `TestA06`、`TestKill9MidRunThenRestart` |
| 远端已生效但回执丢失（已受理，有 remote_ref） | 状态保持 accepted，下一步是查询 remote_ref；再次发送被拒 | `TestA06` |
| 回执到达但本地未提交（提交事务失败） | 整笔回滚，正式结果和审计事件都不出现；重试可以再次提交 | `TestA13` |
| 正式结果已落地、调用方没收到确认 | 调用方重试提交同一结果，返回 `duplicate`，即使它已经被隔离 | `TestA05` |
| 接管后旧执行者恢复 | 提交、认领、绑定、派发都返回 `fenced`；它带来的回执记为观测，`applied=0` | `TestA05`、`TestA06` |
| 旧执行者已拿到许可、在有效期内发送 | **只能部分防护**：许可在接管后已经失效（`VerifyPermit` 判为 stale），但 Gateway 不校验许可。剩余窗口不超过许可有效期（默认 15 秒） | `TestI20_PermitBindsPayloadEpochAndSend`（校验侧） |
| 控制进程 kill -9（写入进行中） | 已确认的写入全部保留，没有半条记录，integrity_check 通过；会话签名密钥存在库内，令牌在重启后仍然有效 | `TestKill9DuringWriteBurst`、`TestKill9MidRunThenRestart` |
| 下载/依赖等技术失败 | `Fail` 只结束当前尝试，o 保持开放可以重试，不会成为可复用的业务事实 | `TestI20_TechnicalFailureIsNotABusinessRejection` |
| 远端 Runtime 或 Gateway 丢状态 | 本模块不推断结果，按契约给出下一步。远端侧的恢复不由本模块负责 | 未覆盖（属于 W05/W07 联测） |
| 控制进程长期不可用 | 执行者无法 claim，只能等待；没有 HA | 未覆盖（如实声明单实例） |

## 3. 可信来源表（#20 How 1）

| 输入 | 来源 | 校验位置 | 存储/关联键 | 消费者 | 失效条件 | 绕过测试 |
|---|---|---|---|---|---|---|
| 发起者身份 | 本服务签发的会话令牌（HMAC），账号密钥加盐哈希存储 | `Authenticate`：验签、检查过期，再从库里读当前主体 | `principals.id` 作为 issuer | 所有接口 | 账号被停用，下一次请求立即失效；令牌 8 小时后过期 | 只带 `X-User-ID` 返回 401；bob 的令牌加 `X-User-ID: alice`，仍按 bob 处理，返回 403；篡改签名返回 401（`TestI20_IdentityIsNotSelfReported`） |
| 命令身份 | 可信入口在用户点击时生成 command_id；issuer 来自会话 | `Tx.command` | `commands(scope, issuer, command_id)` 主键，同时存命令内容的 κ | 采纳、暂停、复核、交付、开案例 | 不失效；同一 id 换内容判冲突 | `TestA03`、`TestA04` |
| 批准 | 有 `approve`/`accept` 角色的用户对**摘要**执行操作 | `Adopt` 和 `Accept` 在事务内用 `x.current` 重读角色 | 采纳和交付记录批准人与 `assurance` | 执行租约、交付门禁 | 撤销角色后立即失效；摘要变化即不匹配 | 规划者批准自己的计划返回 403；撤销 accept 后交付被拒（`TestI20_AuthorityIsCheckedNowNotAtLogin`） |
| 权限（执行者、检查器） | 主体类型加角色，每次请求重读 | `fence`、`x.current`、`check:<名称>` 角色 | — | 执行写入、检查写入 | 停用账号或撤销角色 | 被停用的执行者写入被拒；检查器冒用其他检查器名义被拒 |
| 图/规则修订 | 管理员发布的策略版本；任务在采纳时绑定 | `PublishPolicy`（同一版本内容不同时拒绝）；`Claim` 按任务绑定的版本取能力定义 | `tasks.policy_version` | 认领、绑定、交付门禁 | 新版本只作用于新任务 | `TestA14` |
| 检查 | 检查器对**精确摘要**给出结论 | `RecordCheck`（同一版本给出不同结论判冲突）；`gate` 只认同一摘要加规定规则版本 | `checks(artifact, checker, checker_version, rule_version)` | 交付门禁 | 产物改动后摘要变化，旧检查自然不适用 | `TestA09`、`TestI20_VerdictsMapToDistinctAllowedActions` |
| 摘要（κ） | 本模块从材料本身计算（RFC 8785 + SHA-256 + 规范化版本 + 用途标签） | `Digest`、`kappa` | 节点 κ_in、调用 κ、产物摘要、计划摘要 | 冲突判定、复用、许可校验 | 不接受调用方提交的摘要 | 许可配错载荷或错目标被拒（`TestI20_PermitBindsPayloadEpochAndSend`） |
| 完成证据 | 调用的回执观测（受理号、结果）；节点的正式结果 | `Observe` 只应用能推进状态的观测；`Commit` 要求节点没有未决调用 | `call_observations(b, seq)`、`results(o)` | 交付门禁、投影 | 终态之后的回执只记录 | `TestA06`、`TestI20_HistoryReadsNeverTriggerEffects` |

## 4. #20 正反用例对照

| #20 验收项 | 本模块的结果 | 测试 |
|---|---|---|
| 合法身份、有效批准、正确材料 | 正常交付（远端回执为模拟观测，真实回执要等 W05 联测） | `TestA01` |
| 伪造身份或批准、直接绕过入口 | 401/403，无状态变化 | `TestI20_IdentityIsNotSelfReported` |
| 检查后替换命令字段或版本 | 不能交付，也不能顶替正式结果 | `TestA09` |
| 计算完成后撤权或规约失效 | 交付被拒；已经发生的不回滚 | `TestI20_AuthorityIsCheckedNowNotAtLogin` |
| deny/缺证据/allow 只产生对应的动作 | fail → 新任务修订或开案例；human → 复核；error → 重跑检查；未覆盖 → 等待检查；pass → 交付 | `TestI20_VerdictsMapToDistinctAllowedActions` |
| 超时但外部已经生效 | unknown，不新建请求；查询或人工核对 | `TestA06` |
| 报告检查后被修改 | 原检查和原批准不能放行修改后的报告 | `TestA09` |
| 历史读取/回放 | 读操作不写事件；已完成的调用不能再次派发 | `TestI20_HistoryReadsNeverTriggerEffects` |
| 无规则 / 规则未覆盖 / 检查报错 | `not_configured`（禁止自动交付）/ `uncovered` / `check_error`，三者分开 | `TestI20_NoRulesIsNotPermission`、`TestI20_VerdictsMapToDistinctAllowedActions` |

## 5. 未覆盖与剩余风险

1. **真实出口。** Agent Bus Gateway 不校验许可。节点进程如果持有其他凭据或网络路由，可以绕过 `dispatch` 直接调用。这需要 W05/W07 在部署上证明凭据隔离，本模块无法单独保证。
2. **执行者自报材料。** κ_in 由本模块计算，但材料内容来自执行者。如果执行者虚报材料，可能骗到不该有的复用。缓解办法是把节点输入里的快照引用限定为已登记材料，这一步还没做。
3. **企业身份。** 只有演示账号。WorkSwarm 到本模块的用户委托（W02）还没有闭环。
4. **单实例，没有 HA。**
5. **补充材料**没有单独命令，**取消**也没有对应的远端动作（Runtime 没有路由 `CancelTask`）。
