# W05 年报链单侧记录：`0358acb`（2026-11-18 — 11-19）

W05（xierongqiag2015）在自己的环境上按 B 方案部署 W03/W04 候选并实跑的记录。这是单侧记录，不代替共同 D1 接收、W06 业务验收或独立复跑。

## 接收对象与版本

| 项 | 值 |
|---|---|
| W03/W04 候选 | `0358acbad0ce1b0ce73772f31eda845057ba4dc5`（#16 本轮决定的接收基线） |
| WorkSwarm | `1f168664259fcdc088c2af50afd786e513a7d026` |
| agent-solution（RDC / Gateway） | `eb48c4824491e3d462ae234ffaaf9736c174523e` |
| agent-runtime-java / agent-core-java | `77004f14162e…` / `1cb70314065a…` |
| execution_profile | `sha256:233e9a6f689cb8e366b274536f5f91b614d5955e16bc041b48cfae8fbc9caf10` |
| model_binding_digest | `sha256:5036fa47d1d796084346c8dfb6e03b17f8a7137b70f5c7217dc9dee7bfd52b18` |

制品 sha256（完整值见 `raw/environment.json`）：
- `report-orchestrator.jar` `666f909383755025…`
- `report-experts.jar` `3e165813191c726c…`
- `report-control` `180bfc895a6eba3b…`
- 插件 `frontend/dist`（整棵树） `37ffeb9f4933b50a…`

## 环境

- **服务**：Windows 11 上的 WSL2 Ubuntu 24.04，按 `demos/report-demo/LINUX-RUN.md` 原生运行，全部只绑定 127.0.0.1。
- **工具链**：OpenJDK 21.0.13、Go 1.25.0、Maven 3.9.16、Node 24.22.1、Python 3.12.3、uv 0.11.7、Keycloak 26.8.2。
- **数据库**：PostgreSQL 16.16 容器（127.0.0.1:55432），库 `agent_hub_demo`、`agent_rdc`。
- **模型**：
  - 11-18 前三次运行：GLM `glm-5-2-260617`；
  - 11-18 22:21 起：`deepseek-v4-1-flash-260910`（同一接口与密钥，只换模型名并重新绑定；新绑定 `execution_profile` `sha256:adeb6664…`、`model_binding_digest` `sha256:8f336627…`，旧绑定已备份）。
  - 均为 OpenAI 兼容接口，`DEMO_MODEL_ENDPOINT_PROFILE=openai`，专家预算用默认值（max_tokens 12000，迭代 4 / 8）。
- **测试客户端**：Windows 上的 Python + Playwright + Edge，经 WSL 的 localhost 转发访问 5173。运行前核对过：Windows 侧这些端口都是 `wslrelay` 转发，`control_session_id` 与 WSL 内一致，没有命中 Windows 上的其他实例。
- **凭据**：都在仓库外的 600 权限文件中。本目录文件已按实际取值扫描（26 个值），均未出现。

## 部署差异（逐项登记，没有改控制和业务语义）

1. **构建来源**：jar 和 `report-control` 在 `8c31bcd` 上构建。`8c31bcd` 与 `0358acb` 的 `src/main`、部署脚本、`configure.py` 完全相同，差异只有文档、插件 flow 视图、一个验证脚本和一个 Java 测试；执行配置指纹的输入也相同。切换到 `0358acb` 时只重建了插件前端，并重新装配 WorkSwarm（`scripts/wsl_report_switch_0358.sh`）。
2. **WSL 下 `services.py` 识别不了自己的进程**：
   - WSL 时钟同步后 btime 平移约 28 秒，psutil 的进程创建时间随之变化，`services.py` 的 0.01 秒容差就对不上了；
   - 停止前改为人工核对 PID、命令行、工作目录和监听端口（`scripts/wsl_report_restart_compiler.sh`、`wsl_report_ws_restart.sh`）。
3. **WorkSwarm 首次启动覆盖装配结果**：
   - 装配后首次启动时，`config/config.yaml` 和 `config/.env` 被重建为模板内容（`setup_guide: true`，模型占位值）；
   - 按 LOCAL-RUN 第 9 节重新装配并重启后正常，之后没有再被覆盖。
4. **worker 开启**：开启 worker 后需要重启 compiler，因差异 2 同样经人工核对后重启。

## 运行记录

| 任务 | 源码 | 开始（UTC） | 结果 |
|---|---|---|---|
| `T-20ef410acff24a0aab3afb19297656d8` | `8c31bcd` | 05:24:40 | extract 失败：专家 JSON 截断（`JsonEOFException`），0 个正式节点 |
| `T-0949586ac6722295125f7aa057fb753f` | `8c31bcd` | 05:30:23 | extract、business 落为正式结果；quality 失败：返回中文总结而非 JSON |
| `T-01858b3e91f6a6c15f51ac96158ecb2e` | `0358acb` | 06:22:12 | 与上一行相同：2 个正式节点，quality 以同样方式失败 |

`0358acb` 这一次按年报 D1 的链路逐段核对（`raw/T-01858b3e-0358acb.json`）：

| 段 | 证据 |
|---|---|
| 新对话 | `live_smoke.py` 从 `/chat/new` 选年报协作规划助手并提交；规划由模型实时生成 |
| 能力选择 | 任务带选择记录 `selection-c314b1b1c6e4553455ab3dc672fddc4d` |
| 新图 | 8 个节点；`model_binding_digest` 与本环境发布的一致 |
| 校验 / 确认 / 采纳 | 插件门禁提交后任务从 `AWAITING_APPROVAL` 进入 `RUNNING`；确认前远端调用为 0（`raw/platform-live-smoke-T-01858b3e.json` 的 `zero_calls_before_confirmation`） |
| 真实 Bus 节点 | `remote_calls` 3。extract（report-extractor）、business（business-analyst）完成，quality（asset-quality-analyst）失败；执行方式 `agent_bus` / `DIRECT` |
| PG 正式结果 | `/verify`：`formal_nodes` 2（extract、business 为 `COMMITTED`）。`evidence_intact` 为 false，因为其余节点未成为正式结果 |
| 插件 | 编排台显示同一任务：选择记录、正式状态 FAILED、Agent Bus DIRECT、“关联任务 3 · 已保存 2 · 待核对 0 · 失败 1”（`raw/plugin-T-01858b3e-0358acb.png`） |

## deepseek 对照与 D1 全链（2026-11-18 22:21 — 11-19 14:32，`raw/deepseek/`）

按 yougq 的建议，只把模型换成 deepseek-flash（`scripts/wsl_report_rebind.sh`：备份旧绑定 → 按归属核对停止 → `bind_profile.py` → 重启 → `validate_services`、`identity_probe` 通过）。

| 任务 | 开始（UTC+8） | 结果 |
|---|---|---|
| `T-bdc4aa18d8f8e0b979a7d0e628dc2e94` | 11-18 22:23 | 四个专家全部完成；check0 程序检查无失败项、4 条待人工核验 → 人工核验通过后**死锁**，见下 |
| `T-8fae6cb5b646d334defc623e6e42e469` | 11-19 14:29 | check0 直接 PASS；**一直走到 DELIVERED** |

GLM 下三次运行都有专家输出不合约，deepseek 下两次运行四个专家全部完成：问题可归到 GLM 模型本身。

### `T-8fae6cb5`：D1 链路逐段核对

| 段 | 证据（`raw/deepseek/T-8fae6cb5-deepseek-delivered.json` 等） |
|---|---|
| 新对话 → 能力选择 → 新图 | 规划模型实时生成 8 节点图，选择记录 `selection-582b8d8b8d5a162b48bcafea7533b499`，模型绑定为 deepseek 的新摘要 |
| 校验 / 确认 / 采纳 | 插件门禁提交；确认前远端调用为 0（`zero_calls_before_confirmation`） |
| 真实 Bus 节点 | 4 次远端调用全部完成：report-extractor、business-analyst、asset-quality-analyst、report-writer，各自唯一远端 task |
| W04 正式结果 | 6 个正式节点；revise1、check1 由 check0 PASS 授权跳过；select_report 选定报告 |
| 检查 → 批准 → 交付 | check0 PASS；`REPORT_APPROVED`、`REPORT_DELIVERED` 各 1 次；交付回执 `delivery-receipt-7115403d8cc4ffb8a159813278e527ba`（14:32:29） |
| 下载与完整性 | 下载字节与正式报告摘要 `sha256:69d0d558…` 一致；`/verify` 的 `evidence_intact` 为 true |
| 插件 | 编排台显示同一任务（`plugin-T-8fae6cb5.png`），可信页报告区（`trusted-T-8fae6cb5-report.png`） |

**批准与交付的操作方式（如实说明）**：
- 本次批准与交付由候选自带的 `live_smoke.py` 用测试账号 `demo` 自动完成，不是人工批准。
- 这违背了 W05 内部“提交前先由 xierongqiag2015 检查”的约定；事后 xierongqiag2015 核对了交付报告的内容，同意保留。
- 报告的四条结论都是原文表格行的逐字引用，局限性一栏写明趋势解释未核验、风险部分为空。业务质量需 W06 业务验收。

### `T-bdc4aa18`：HUMAN 路径（真实人工核验）与死锁

- 22:25:43 check0 返回 HUMAN，4 条“Review finding support”。
- 11-19 14:12:15 **xierongqiag2015 本人**在 W04 可信页完成人工核验：打开固定快照原文 PDF 第 14、16 页逐项核对，ACCEPT，回执 `human-review-67f5d31469ce3f88bf221f8155c62ecc`（使用测试账号 `demo` 登录，理由中写明本人与核验范围；见 `trusted-T-bdc4aa18-review.png`）。
- 之前 11-18 23:14 有一次 W05 自动化代操作的提交，因可信页 3 秒刷新清空理由，返回 400，未留下核验记录（`human-proxy-attempt-T-bdc4aa18.json`）。
- 核验后点“继续”，任务仍不推进。原因（已对照代码与事件）：
  1. check0 返回 HUMAN 后，Java 紧接着解析 `select_report`，得到 `WAIT`；
  2. Java 把 `WAIT` 映射为 `UNKNOWN` 上报，控制面把任务从 `WAITING_INPUT` 改为 `WAITING_REMOTE`，节点标为 UNKNOWN；
  3. 恢复后 `resolve` 对 UNKNOWN 节点只返回 WAIT（远端核对分支），而 `select_report` 是本地节点，永远拿不到许可；
  4. 租约不释放，Java 每约 30 秒重领一次，暂停前共 1802 次，审计事件 14775 条。
- 11-19 14:27:10 在可信页“暂停新节点”，以让出单活动图名额、停止空转；任务与记录原样保留，未改 PG。
- 已反馈 yougq：fork PR #1 [issuecomment-7001672877](https://github.com/yougq/agent-hub-alpha/pull/1#issuecomment-7001672877)。

## 开放项

- **HUMAN 路径死锁**（候选缺陷）：程序检查返回 HUMAN 的任务，人工核验后无法继续。已反馈 yougq，待修复后在新任务上补验“HUMAN → 继续 → 选定 → 批准 → 交付”。
- **可信页刷新清空核验理由**（候选缺陷）：已一并反馈。
- **批准与交付未经人工操作**：`T-8fae6cb5` 的批准、交付由测试脚本自动完成；人工批准、交付及 W06 业务验收仍需在共同 D1/D2 中完成。
- **GLM 输出不合约**：换用 deepseek-flash 后不再出现；GLM 本身的问题保留记录，未继续处理。
- **传输层计数代理尚未接入**；**实际执行实例**仍为 `not_collected`。

## 复现

服务按 LINUX-RUN 第 1—4 节部署。本目录 `scripts/` 是 W05 实际使用的辅助脚本，凭据都从私有文件读取，不进命令行。

```sh
# Windows 测试客户端（服务在 WSL）
python <repo>/demos/report-demo/tests/live_smoke.py --private-root //wsl$/Ubuntu/<home>/report-demo-env/repo/.local/report-demo \
  --web-url http://127.0.0.1:5173 --browser-executable "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"
# 记录（只读接口）与插件截图
python3 scripts/collect_annual_d1.py <输出目录>
python scripts/annual_plugin_view.py <任务ID> <输出前缀>
```
