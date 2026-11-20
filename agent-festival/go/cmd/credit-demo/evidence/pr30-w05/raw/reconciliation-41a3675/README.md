# 旧任务对账：41a3675 数据在 e1961fe8 与修复候选上的对照（2026-11-20）

数据：W05 D2 环境 `agent-hub-issue16`（41a3675）的 credit 数据目录，只读拷贝，16 个任务（11 done、3 waiting、2 failed）；旧采纳请求均为 `adoption_version 0.1.0`，账本模型 `glm-5-2-260617`。

- `e1961fe8-check-detail.txt`：用 e1961fe8 的 `checkAdoptionSnapshot` 逐个诊断，并附回放引擎细节。11 个 done 任务均为 `recomputed 0 events, record has 1`、`conformance=pass`：当前采纳状态机拒绝 0.1.0 请求，重放不产生事件，被报成“采纳账本与快照操作序列不一致”。
- `after-e196.txt`：在临时拷贝上跑 e1961fe8 的完整启动对账：11 个 done 任务全部变为 blocked，最终报告被阻断原因覆盖（`final kept=false`）。
- `after-blockr.txt`：在另一份临时拷贝上跑修复候选（fork 分支 `blocked-reason` a7e1f93）：11 个 done 任务保持 done、最终报告保留，旁边记 `RECONCILIATION_REQUIRED/LEGACY_ADOPTION_PROTOCOL`。waiting、failed 任务两边都不变。
- `*_local_test.go.txt`：两段本地诊断代码（未提交到产品分支），供复跑。
