# 4004ab4 上从真实入口触发 HUMAN 的两次尝试（W05，2026-11-21 / 11-22）

D2 要求在**同一条真实 HUMAN 任务**上完成真人核验 → 恢复原图 → 人工批准 → 交付 → 下载。W05 在接收环境（W03/W04 `4004ab4`，deepseek-v4-1-flash-260910）里，用真实对话规划了两条任务，专门让报告包含分析性判断。两条都由 xierongqiag2015 在编排台准入，检查 `check0` 都直接 **PASS**，没有进入 HUMAN。

| 任务 | 对话（`dialogue.txt`） | 获准图中 draft0 的指令 | check0 | 正式结论 | 局限说明 |
|---|---|---|---|---|---|
| T-b2befd84 | 要求至少两条分析性判断，附原文依据，交人工核验 | “另须给出至少两条分析性判断……显式标注为待人工核验项” | PASS，`human_review` 为空 | 4 条，均为原文指标行的逐字引用 | 2 条写着“分析性判断（待人工核验）” |
| T-616a1d93 | 进一步要求分析性判断**必须写进正式结论（findings），不得只放在 limitations** | “这些判断必须出现在 findings，不得只放在 limitations” | PASS，`human_review` 为空 | 4 条，均为原文指标行的逐字引用 | 净息差收窄、拨备覆盖率下降等判断写着“需人工核验” |

此前在同一版本上的 T-824c5bf9（标准对话）也是 PASS。

## 原因

- **写作专家的固定提示词**（`experts-java/.../prompts/write.md`）要求：正式结论采用“可机械核验的保守形式”，逐字引用原文指标行；“未证明的驱动因素写入 limitations”。即使获准图的节点指令明确要求，写作专家仍按固定提示词执行。
- **检查器**（`ReportChecker`）只核对正式结论；对局限说明只要求非空，不看内容。
- 0358acb 到 4004ab4 之间，`write.md` 和 `ReportChecker` 都没有改动。11-18 的 T-bdc4aa18（0358acb）进入 HUMAN，是模型偶然把非逐字论断写进了正式结论，不能稳定复现。

**结论**：在 4004ab4 上，无法通过对话从真实入口稳定触发 HUMAN。D2 的“同一条真实 HUMAN 任务”不论由谁操作，都需要 W03/W04 提供一个稳定的触发方式。

## 同时暴露的一个门禁缺口

两条任务的获准图中，选稿节点的指令都写明“分析性判断须转人工核验确认后才能批准”，报告的局限说明也自标“需人工核验”。但控制面没有据此要求人工审阅：两份报告都处于可批准状态，可以不经人工核验直接批准、交付。

两条任务都停在 AWAITING_ACCEPTANCE，**没有批准**，保留作为证据。

## 文件

`T-b2befd84/`、`T-616a1d93/` 下各有：
- `dialogue.txt`：对话原文；
- `plan-only.json`：规划记录；
- `pre-admission-task.json`：准入前任务，含获准图各节点指令；
- `after-task.json`、`after-workbench.json`、`after-calls.json`：执行后快照，check0 结果在 `nodes.check0.result.output`；
- `report-draft0.json`：报告正文；
- `watch.jsonl`：状态变化。

`summary.json` 是两条任务的要点对照。
