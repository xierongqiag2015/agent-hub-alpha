# W06 年报专家（W05 托管框架）

让 W06（PR #36）的 Agent 作为**年报节点的业务层**运行，外层是 W03/W04 年报专家的**平台绑定层**（`experts-java` @ `4004ab4`），平台层原样复用：

```text
Gateway ─A2A─▶ IngressIdentityFilter（入口身份）─▶ BoundExpertHandler（向 Go 申请授权；核对模型绑定、执行配置、能力身份；
                                                                   只把 {binding, material} 交给业务层；要求输出单个 JSON 对象，再包装交回）
                                                   └─▶ 业务层：W06 Agent（本模块唯一的替换点）
```

| 文件 | 来源 | 作用 |
|---|---|---|
| `BoundExpertHandler`、`IngressIdentityFilter`、`ModelBinding`、`ExpertApplication`、`application.yml`、`prompts/*.md` | W03/W04 `4004ab4`，构建时原样复制，`platform.sha256` 逐个校验 | 平台绑定层；`ExpertApplication` 和 prompts 只给基线探针使用 |
| `W06ExpertApplication` | W05 | 与 `ExpertApplication` 相同的装配，业务 Agent 换成 W06 |
| `W06BusinessAgents` | W05 | 角色映射：extract → W06 指标 Agent（平台已经给出受控材料原文，不需要 W06 的快照资料 Agent）；business → W06 经营 Agent；write → W06 写作 Agent；quality 暂不托管，等 W03 定 Loop Forest。模型参数取自已批准的模型绑定 |
| `W06ContractProbe`、`probe/w06_contract_probe.py` | W05 | 契约探针，见下文 |
| `build.sh` | W05 | W06 库层（`../w06-agent-runtime/adapt-w06.sh`）、平台层（sha256 校验）、打包 |

## 契约探针

`probe/w06_contract_probe.py` 从已交付任务的 `NODE_RESOLVED` 审计事件取出各节点的真实 binding，按 `BoundExpertHandler.prepareTask` 的规则还原交给业务层的消息：
- extract 节点去掉 task_id、graph_revision、node_id、goal、focus，并附完整材料；
- 其他节点只附材料的 id 和摘要。

然后把还原的消息经同一个 `JiuwenCoreAgentHandler` 路径，分别交给 W06 Agent 和 W03/W04 的原角色 Agent（基线），用平台层自己的 `parseOutput` 判断能否被接收，再按年报角色契约逐项检查。探针不经过控制面和 Gateway，也不创建任务。

2026-11-21 在 T-824c5bf9 上的结果（deepseek-v4-1-flash-260910）：

| 角色 | 基线（W03/W04 原 Agent） | W06 |
|---|---|---|
| extract | 平台接受；契约全部通过 | **平台拒收**：输出是 JSON 数组（W06 指标契约），不是单个对象；只有本期值 |
| business | 平台接受；契约全部通过 | **平台拒收**：输出是 Markdown（但上游给了两期数据时，同比计算正确） |
| write | 平台接受；operating、asset_quality、risks 三节齐全 | 平台接受（是 JSON 对象），**年报契约不通过**：缺 snapshot、scope、indicators、limitations，各节没有 id |

基线全部通过，说明还原的消息是准确的；W06 这三处差距与 #16 上的对接方案一致，需要 W06 按年报契约调整输出。

## 运行

```sh
MAVEN_SETTINGS=<settings.xml> sh build.sh <W06 源码 .../source/w06-annual-report-agent> <experts-java @4004ab4> <输出目录>
python3 probe/w06_contract_probe.py <输出目录>        # 年报接收环境（WSL），需要已交付任务的审计事件
# 作为年报专家进程运行：与 experts-java 相同的环境变量（REPORT_EXPERT_CONFIG、DEMO_MODEL_* 等）
java -jar w06-annual-expert.jar
```

**未做的部分**：真正替换年报节点（RDC 登记为节点能力的 agent_id、W04 目录 `remote_binding` 和专家令牌、`bind_profile`）要等 W06 对齐输出、W04 配合，见 #16 方案第四节。
