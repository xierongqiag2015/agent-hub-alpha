# W05 年报链单侧记录：`0358acb`（2026-11-18）

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
- **模型**：GLM `glm-5-2-260617`，OpenAI 兼容接口，`DEMO_MODEL_ENDPOINT_PROFILE=openai`。专家预算用默认值（max_tokens 12000，迭代 4 / 8）。
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

## 开放项

- **专家输出不符合声明的 JSON**：三次运行中，extract 失败一次（截断），quality 两次返回中文总结，后者可稳定复现。已在 yougq PR #1 反馈，等待对模型或输出策略的处理。完整报告的检查、批准和交付因此还没走到。
- **日志缺 finish_reason**：无法确认截断是否由推理 token 占用预算造成。
- **传输层计数代理尚未接入**：经 `config.bus.gateway_url` 接入，主链跑通后再接。
- **实际执行实例**：仍为 `not_collected`。

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
