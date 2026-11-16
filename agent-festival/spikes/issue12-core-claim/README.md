# nga-core-spike

验证 agent-hub-alpha Issue #4 的一个前提：Runtime/Core 负责调度、Go 控制面持有节点执行身份 o 和正式结果。在真实的 openJiuwen Java Core 上，这个分工能否在重跑、崩溃、并发、旧进程复活和知识失效等情况下，保证 `CommittedResults(o) ≤ 1`，并且业务效果不重复。

## 组成

| 目录 | 内容 |
| --- | --- |
| `java/` | `agent-core-java 0.1.17`（Maven Central）上的授信图，以及业务节点的包装组件 `ClaimNode` |
| `go/` | 控制面 `ctl`：SQLite 事务，接口为 `claim / commit / b/bind / b/dispatch / b/observe / b/recover`；同进程内还提供一个可注入延迟、可查询、按键幂等的银行 API 替身 |
| `tools/run_scenarios.py` | 场景驱动：每个场景重置状态，启动 Worker 进程，用 `Runtime.halt(137)` 模拟 kill -9，最后读取控制面状态做断言 |
| `results/<场景>/` | `log.txt`（进程日志）、`state.json`（控制面全量状态）、`verdict.json`（逐项断言） |

授信图（8 个业务节点）：

```
start → evidence → kyc(子工作流: kyc_check) → loop(supplement ×3) → judge(S1 替身) → credit_request(业务 API) → await_receipt(中断等待) → end
```

## 运行

需要 JDK 17、Go 1.24 和 Python 3.10 以上。

```sh
cd go && go build -o ctl . && ./ctl -db ../results/ctl.db &        # 控制面 :18080，银行替身 :18081
cd java && mvn -q package dependency:build-classpath -Dmdep.outputFile=target/cp.txt
SPIKE_JAVA=$JAVA_HOME/bin/java python3 tools/run_scenarios.py   # 全部场景；也可以指定，如 E2c E3b
python3 tools/make_report.py                                    # 生成 report.html
```

## 边界

- 模型和业务 API 都是替身。这里验证的是控制机制，不验证模型能力或银行事务。
- 只涉及 Core 层，没有经过 Runtime 的 HTTP/A2A 路径，也没有接 WorkSwarm。
- 控制面是单实例。租约固定、不续约，所以租约必须长于节点执行时间，生产环境需要心跳续约。
- 派发隔离做在控制面签发许可的环节。如果旧进程已经拿到许可、又延迟发送，只能靠提供方幂等键兜底；要在出口处实时校验代次，需要出口代理（P04）。

## 出处与范围

- 本目录是 [#12](https://github.com/chaosxingxc-orion/agent-hub-alpha/issues/12) 实验的源码与原始结果，按 [#16 的交付卡](https://github.com/chaosxingxc-orion/agent-hub-alpha/issues/16#issuecomment-6912424270) 公开，用于核对其中"#12 实验映射（对照 #19 S01—S12）"一节。
- 声明层级：组件实验。被测对象是 openJiuwen Core 0.1.17 的 Workflow 引擎加业务节点包装组件，没有经过 Runtime HTTP/A2A、Agent Bus 或 BlueprintScheduler；不是 Demo 集成验证。
- `results/` 下是 2026-11-10 的原始运行记录（`log.txt`、`state.json`、`verdict.json`），`run-all.txt` 和 `repeat-2/`、`repeat-3/` 是三轮全量重复运行的汇总。截图没有入库，见 #12 原帖。
- 未入库：上游 Core 源码包（`tools/core-sources/`，从 Maven Central 的 `agent-core-java:0.1.17` 获取）、构建产物和 SQLite 数据文件。
