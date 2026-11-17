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

- 镜像都在本地构建，没有仓库 digest；构建清单见 `raw/build-all.json`。
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

## 对照 PR #30 的 R-A…U-B 矩阵

| 编号 | 本记录覆盖到的程度 | 仍需补的部分 |
|---|---|---|
| R-A | host-kill：已有完成节点、后续节点在途时，杀执行器后恢复，完成节点调用增量为 0，在途节点沿用原请求 | 从 WorkSwarm 插件触发恢复（W02 操作）；后续节点为 unknown（而非在途）的组合 |
| R-B | 未覆盖 | 恢复已落盘、回复被丢弃 / 浏览器断链（双方） |
| R-C | storage-failure：Go 侧真实写失败，不伪成功、下游不启动、恢复后对账 | 经插件路径恢复时注入保存失败，并把日志关联到该命令 |
| R-D | host-kill（执行器）、gateway-restart、runtime-restart、go-kill（执行中停 Go）、two-clients（两个客户端同时恢复，只有一个生效） | go-kill 发现在途远端节点因依赖 Go 成员工具而在远端失败，需确定处置边界；两个客户端的插件路径操作由 W02 补 |
| M-A | 未执行 | 任务绑定 A、执行端改为 B：需要先按 PR #31 补执行端绑定，另行回填 |
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
