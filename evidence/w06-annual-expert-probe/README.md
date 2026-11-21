# W06 年报节点契约探针（W05，2026-11-21 17:06—17:09）

把**已交付年报任务 T-824c5bf9** 各节点交给业务层的真实消息，原样交给 W06 Agent，同时交给 W03/W04 原角色 Agent 作为基线，用年报平台层自己的接收规则判定。探针不经过控制面和 Gateway，不创建任务，对接收环境没有改动。

- 框架与探针代码：fork `w06-bus-runtime` `bc21148`，`agent-festival/examples/w06-annual-expert`。平台层取自 W03/W04 `4004ab4`，原样复制，sha256 逐个校验通过。
- 消息还原（`message-*.json`）：binding 取自 T-824c5bf9 的 `NODE_RESOLVED` 审计事件，按 `BoundExpertHandler.prepareTask` 的规则处理。
  - extract：去掉 task_id、graph_revision、node_id、goal、focus，附完整的受控材料（已核验的第 14—16 页）。
  - business、write：binding 原样，附材料的 id 和摘要；上游是该任务里已提交的正式结果。
- 判定：
  - 先用平台层的 `BoundExpertHandler.parseOutput`，要求输出是一个完整的 JSON 对象；
  - 再按年报角色契约检查（`prompts/<role>.md`）：八项指标的 id、2025 和 2024 两期数值、变动、页码、印刷页和原文引用；经营节点要有 section_id 和 findings；写作节点要有 operating、asset_quality、risks 三节。
- 模型：deepseek-v4-1-flash-260910，模型绑定摘要与年报任务相同。W06 Agent 的迭代上限和温度取自批准的模型绑定（4 次，0.1）。

## 结果（`summary.json`）

| 角色 | 基线：W03/W04 原 Agent | W06 |
|---|---|---|
| extract | 12.7 s，平台接受，契约全部通过 | 8.8 s，**平台拒收**：`Expert output must be one complete JSON object`。输出是 W06 指标契约的 JSON 数组，每条只有本期值（value 为带千分位的字符串），没有 2024 值、变动、印刷页和原文引用 |
| business | 29.7 s，平台接受，契约全部通过（`section_id=operating`） | 24.5 s，**平台拒收**：输出是 Markdown（`## 经营分析…`）。内容本身有据：上游给了两期数据，W06 正确算出营业收入同比 +0.01% |
| write | 21.5 s，平台接受，operating、asset_quality、risks 三节齐全 | 40.4 s，平台接受（是 W06 的 `AnnualReportArtifacts` 对象），但**年报 `report/1` 契约不通过**：缺 snapshot、scope、indicators、limitations，sections 没有 id |

**结论**：
- 基线三项全部通过，说明探针还原的消息准确。
- W06 三个角色的差距都在**输出契约**，不在能力：数值、引用和计算本身是准的。这与 #16 对接方案第二节的判断一致。
- W06 按年报契约调整输出后，可以直接用同一个探针复验，再进入真实节点替换。

## 文件

- `message-{extract,business,write}.json`：还原的业务层消息，执行身份主体已脱敏。
- `{baseline,w06}-{role}.json`：每次运行的原始输出、平台判定和用时。
- `{baseline,w06}-{role}.log`：运行日志尾部，已去掉堆栈。
- `summary.json`、`probe-config.json`（只含模型绑定摘要）。
