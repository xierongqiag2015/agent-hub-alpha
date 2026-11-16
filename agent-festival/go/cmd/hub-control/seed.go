package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// -seed fills a running instance with clearly labelled simulated tasks so the
// console has something to show before the Java host (W03) and checkers (W06)
// are connected. It only uses the public HTTP API and the demo accounts of
// deploy/bootstrap.example.json; secrets come from HUB_SECRET_* variables.
// Remote agent replies and checker verdicts are simulated.

const seedTag = "【模拟数据】"

type seedClient struct {
	base  string
	hc    *http.Client
	toks  map[string]string
	lease time.Duration
}

func (c *seedClient) do(tok, method, path string, body any) (int, map[string]any) {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		log.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (c *seedClient) ok(who, path string, body any) map[string]any {
	method := "POST"
	if body == nil {
		method = "GET"
	}
	st, out := c.do(c.toks[who], method, path, body)
	if st != 200 {
		log.Fatalf("%s %s as %s: %d %v", method, path, who, st, out)
	}
	return out
}

var seedTargets = map[string]string{"material": "rdc:annual-report/material", "analysis": "rdc:annual-report/analysis", "writer": "rdc:annual-report/writer"}

var seedSnapshot = map[string]any{"id": "cmb-2025-annual-report-a", "sha256": "52c49dcdc00d4f5436adabc6561044d7a539e9bb0a8ebbdb692a1da6237459da"}

var seedGraph = map[string]any{"report": "write", "nodes": []map[string]any{
	{"id": "extract", "capability": "material"},
	{"id": "operating", "capability": "analysis", "depends_on": []string{"extract"}},
	{"id": "asset_quality", "capability": "analysis", "depends_on": []string{"extract"}},
	{"id": "write", "capability": "writer", "depends_on": []string{"operating", "asset_quality"}},
}}

var seedCaps = map[string]string{"extract": "material", "operating": "analysis", "asset_quality": "analysis", "write": "writer"}

// indicator rows from W01 (#21 draft, verification pending)
func seedIndicators() []any {
	row := func(id string, v25, v24 float64, unit, kind string, chg float64, pdf, printed int, quote string) map[string]any {
		return map[string]any{"id": id, "values": map[string]any{"2025": v25, "2024": v24}, "unit": unit,
			"change": map[string]any{"kind": kind, "value": chg}, "citation": map[string]any{"pdf_page": pdf, "printed_page": printed, "quote": quote}}
	}
	return []any{
		row("operating_income", 337532, 337488, "人民币百万元", "pct", 0.01, 14, 13, "营业收入"),
		row("net_profit_attributable", 150181, 148391, "人民币百万元", "pct", 1.21, 14, 13, "归属于本行股东的净利润"),
		row("net_interest_margin", 1.87, 1.98, "%", "pp", -0.11, 16, 15, "净利息收益率"),
		row("cost_income_ratio", 31.98, 31.89, "%", "pp", 0.09, 16, 15, "成本收入比"),
		row("npl_balance", 68206, 65610, "人民币百万元", "pct", 3.96, 15, 14, "不良贷款余额"),
		row("npl_ratio", 0.94, 0.95, "%", "pp", -0.01, 16, 15, "不良贷款率"),
		row("provision_coverage", 391.79, 411.98, "%", "pp", -20.19, 16, 15, "拨备覆盖率"),
		row("cet1_advanced", 14.16, 14.86, "%", "pp", -0.70, 16, 15, "核心一级资本充足率"),
	}
}

func seedReport(variant string) map[string]any {
	r := map[string]any{
		"snapshot":   "cmb-2025-annual-report-a",
		"scope":      map[string]any{"entity": "招商银行股份有限公司", "basis": "本集团", "currency_unit": "人民币百万元", "period": "2025", "compare_period": "2024"},
		"indicators": seedIndicators(),
		"sections": []any{
			map[string]any{"id": "operating", "findings": []any{map[string]any{"text": "营业收入与上年基本持平（+0.01%），归属于本行股东的净利润增长 1.21%。", "evidence": []any{map[string]any{"indicator": "operating_income"}, map[string]any{"indicator": "net_profit_attributable"}}}}},
			map[string]any{"id": "asset_quality", "findings": []any{map[string]any{"text": "不良贷款率下降 0.01 个百分点至 0.94%，拨备覆盖率下降 20.19 个百分点。", "evidence": []any{map[string]any{"indicator": "npl_ratio"}, map[string]any{"indicator": "provision_coverage"}}}}},
		},
		"limitations": []any{seedTag + "远端 Agent 回执与检查结论均为模拟", "参考答案来自 W01 草稿（#21），尚未人工核对"},
	}
	if variant != "" {
		r["variant"] = variant
	}
	return r
}

func seedSecrets() map[string]string {
	env := map[string]string{"demo-approver": "HUB_SECRET_DEMO_APPROVER", "planner": "HUB_SECRET_PLANNER", "graph-host": "HUB_SECRET_GRAPH_HOST",
		"checker-calc": "HUB_SECRET_CHECKER_CALC", "checker-citation": "HUB_SECRET_CHECKER_CITATION",
		"demo-curator": "HUB_SECRET_DEMO_CURATOR", "hub-admin": "HUB_SECRET_HUB_ADMIN"}
	out := map[string]string{}
	for id, v := range env {
		if os.Getenv(v) == "" {
			log.Fatalf("seed: %s is empty (load the deployment's secrets.env first)", v)
		}
		out[id] = os.Getenv(v)
	}
	return out
}

// simulated snapshot versions for the knowledge-consumption scenarios; their
// digests are derived from a label, not from a real file
func simSnapshot(label string) map[string]any {
	sum := sha256.Sum256([]byte(label))
	return map[string]any{"id": "cmb-2025-annual-report-a", "sha256": hex.EncodeToString(sum[:]), "title": seedTag + label}
}

var (
	snapV2 = simSnapshot("更正版 v2（模拟）")
	snapV3 = simSnapshot("错页版 v3（模拟）")
)

func runSeed(base string, force bool, lease time.Duration) {
	c := &seedClient{base: strings.TrimRight(base, "/"), hc: &http.Client{Timeout: 30 * time.Second}, toks: map[string]string{}, lease: lease}
	for id, secret := range seedSecrets() {
		st, out := c.do("", "POST", "/v1/login", map[string]any{"id": id, "secret": secret})
		if st != 200 {
			log.Fatalf("seed: login %s: %d %v", id, st, out)
		}
		c.toks[id] = out["token"].(string)
	}
	have := map[string]bool{}
	if !force {
		existing, _ := c.ok("demo-approver", "/v1/tasks?limit=200", nil)["tasks"].([]any)
		for _, t := range existing {
			have[t.(map[string]any)["goal"].(string)] = true
		}
	}
	// each scenario is added only if a task with its title is missing
	scenario := func(title string, fn func(goal string)) {
		goal := seedTag + title
		if have[goal] {
			log.Printf("seed: already present: %s", title)
			return
		}
		fn(goal)
	}
	v1 := seedSnapshot

	scenario("招商银行 2025 年报经营与资产质量简报 · 已交付", func(goal string) {
		a, l := c.start(goal, v1)
		rep := c.runAll(l, seedReport(""))
		c.checks(rep, "pass", nil, "pass", nil)
		c.ok("demo-approver", "/v1/commands/accept", map[string]any{"command_id": "seed-accept-" + a, "task": a, "artifact": rep})
		log.Printf("seed: %s delivered (first consumer of snapshot v1)", a)
	})
	scenario("招商银行 2025 年报简报 · 检查已通过，待确认交付（资料节点跨任务复用）", func(goal string) {
		a, l := c.start(goal, v1)
		rep := c.runAll(l, seedReport("待确认交付"))
		c.checks(rep, "pass", nil, "pass", nil)
		c.release(l)
		log.Printf("seed: %s ready for acceptance", a)
	})
	scenario("招商银行 2025 年报简报 · 引用核验转人工复核", func(goal string) {
		a, l := c.start(goal, v1)
		rep := c.runAll(l, seedReport("引用待复核"))
		c.checks(rep, "pass", nil, "human", map[string]any{"item": "net_interest_margin", "reason": "引用页 PDF 16 / 印刷页 15 的原文行需要人工核对（页码规则：正文印刷页 = PDF 页 - 1）"})
		c.release(l)
		log.Printf("seed: %s waiting for human review", a)
	})
	scenario("招商银行 2025 年报简报 · 计算检查不通过", func(goal string) {
		a, l := c.start(goal, v1)
		bad := seedReport("百分点误写")
		bad["sections"] = []any{map[string]any{"id": "operating", "findings": []any{map[string]any{"text": "成本收入比上升 0.09%。", "evidence": []any{map[string]any{"indicator": "cost_income_ratio"}}}}}}
		rep := c.runAll(l, bad)
		c.checks(rep, "fail", map[string]any{"item": "cost_income_ratio", "reason": "比率变化应以百分点表述：上升 0.09 个百分点，而不是 0.09%"}, "pass", nil)
		c.release(l)
		log.Printf("seed: %s rejected by calc", a)
	})
	scenario("招商银行 2025 年报简报 · 候选图待批准", func(goal string) {
		bad1 := map[string]any{"report": "write", "nodes": []map[string]any{
			{"id": "scrape", "capability": "web_crawler"},
			{"id": "write", "capability": "writer", "depends_on": []string{"scrape"}},
		}}
		p1 := c.ok("planner", "/v1/plans", map[string]any{"goal": goal, "material": []any{v1}, "graph": bad1})
		c.ok("planner", "/v1/plans", map[string]any{"task_id": p1["task"], "goal": goal, "material": []any{v1}, "graph": seedGraph})
		log.Printf("seed: %s draft (v1 rejected before mount, v2 awaiting approval)", p1["task"])
	})

	// knowledge consumption: version, hit, revocation
	c.ok("hub-admin", "/v1/admin/materials", snapV2)
	c.ok("hub-admin", "/v1/admin/materials", snapV3)
	scenario("知识消费 · 材料换成更正版：版本不同，不复用旧结果", func(goal string) {
		a, l := c.start(goal, snapV2)
		rep := c.runAll(l, seedReport("更正版"))
		c.checks(rep, "pass", nil, "pass", nil)
		c.release(l)
		log.Printf("seed: %s consumed snapshot v2 for the first time (miss: different version)", a)
	})
	scenario("知识消费 · 命中更正版的已有抽取结果", func(goal string) {
		a, l := c.start(goal, snapV2)
		rep := c.runAll(l, seedReport("更正版命中"))
		c.checks(rep, "pass", nil, "pass", nil)
		c.release(l)
		log.Printf("seed: %s reused the v2 extraction (hit)", a)
	})
	scenario("知识消费 · 依据快照被撤回：已有记录保留，禁止交付", func(goal string) {
		a, l := c.start(goal, snapV3)
		rep := c.runAll(l, seedReport("错页版"))
		c.checks(rep, "pass", nil, "pass", nil)
		c.release(l)
		c.ok("demo-curator", "/v1/commands/revoke", map[string]any{"command_id": "seed-revoke-v3", "ref": "material:" + snapV3["id"].(string) + "@" + snapV3["sha256"].(string),
			"reason": "快照缺第 16 页，属错误版本（模拟）"})
		log.Printf("seed: %s built on snapshot v3, which was then revoked", a)
	})
	scenario("知识消费 · 已撤回的快照不能再启动新任务", func(goal string) {
		p := c.ok("planner", "/v1/plans", map[string]any{"goal": goal, "material": []any{snapV3}, "graph": seedGraph})
		log.Printf("seed: %s draft refused before mount (material revoked)", p["task"])
	})

	scenario("招商银行 2025 年报简报 · 执行中断后接管（远端调用结果未知）", func(goal string) {
		e, l := c.start(goal, v1)
		c.runNode(l, "extract", map[string]any{"indicators": 8, "source": "cmb-2025-annual-report-a"})
		cl := c.ok("graph-host", "/v1/exec/claim", map[string]any{"task": e, "token": l.token, "node": "operating", "material": seedInput("operating", v1), "sources": []any{v1}})
		call := c.ok("graph-host", "/v1/exec/calls/bind", map[string]any{"task": e, "token": l.token, "o": cl["o"], "a": cl["a"], "name": "ask", "target": seedTargets["analysis"], "payload": map[string]any{"node": "operating", "template": "w01-report-v1"}})
		c.ok("graph-host", "/v1/exec/calls/dispatch", map[string]any{"task": e, "token": l.token, "b": call["b"]})
		c.ok("graph-host", "/v1/exec/progress", map[string]any{"task": e, "token": l.token, "o": cl["o"], "a": cl["a"], "seq": 1, "kind": "activity", "detail": "已向分析 Agent 发送请求，等待回执"})
		log.Printf("seed: %s executor stops heartbeating; waiting %s for the lease to expire", e, c.lease+2*time.Second)
		time.Sleep(c.lease + 2*time.Second)
		l2 := c.leaseTask(e, v1)
		c.ok("graph-host", "/v1/exec/claim", map[string]any{"task": e, "token": l2.token, "node": "extract", "material": seedInput("extract", v1), "sources": []any{v1}})
		log.Printf("seed: %s taken over (epoch 2); the operating call is unknown and will not be resent", e)
	})
	log.Printf("seed: done")
}

func seedInput(node string, snap map[string]any) map[string]any {
	return map[string]any{"snapshot": snap, "instructions": "role:" + node, "template": "w01-report-v1"}
}

// start proposes the demo graph on one snapshot, adopts it and leases it.
func (c *seedClient) start(goal string, snap map[string]any) (string, seedLease) {
	p := c.ok("planner", "/v1/plans", map[string]any{"goal": goal, "material": []any{snap}, "graph": seedGraph})
	task := p["task"].(string)
	c.ok("demo-approver", "/v1/commands/adopt", map[string]any{"command_id": "seed-adopt-" + task, "task": task, "rev": p["rev"], "digest": p["digest"]})
	return task, c.leaseTask(task, snap)
}

type seedLease struct {
	task, token string
	snap        map[string]any
}

func (c *seedClient) leaseTask(task string, snap map[string]any) seedLease {
	l := c.ok("graph-host", "/v1/exec/lease", map[string]any{"task": task})
	return seedLease{task: task, token: l["token"].(string), snap: snap}
}

func (c *seedClient) release(l seedLease) {
	c.ok("graph-host", "/v1/exec/release", map[string]any{"task": l.task, "token": l.token})
}

// runNode drives one node the way the Java host does and returns the digest
// of its formal result. A reuse candidate from another task is adopted.
func (c *seedClient) runNode(l seedLease, node string, body any) string {
	cl := c.ok("graph-host", "/v1/exec/claim", map[string]any{"task": l.task, "token": l.token, "node": node, "material": seedInput(node, l.snap), "sources": []any{l.snap}})
	if cl["action"] == "reuse" {
		return cl["result"].(map[string]any)["artifact"].(string)
	}
	if rc, ok := cl["reuse_candidate"].(map[string]any); ok {
		out := c.ok("graph-host", "/v1/exec/commit", map[string]any{"task": l.task, "token": l.token, "o": cl["o"], "a": cl["a"],
			"outcome": "completed", "body": rc["body"], "reused_from": rc["o"]})
		return out["result"].(map[string]any)["artifact"].(string)
	}
	o, a := cl["o"], cl["a"]
	acts := map[string][]string{
		"extract":       {"读取快照 " + fmt.Sprint(l.snap["id"]), "定位第 14—16 页主要指标", "抽取 8 项指标"},
		"operating":     {"读取抽取结果", "分析营收与利润变化"},
		"asset_quality": {"读取抽取结果", "分析不良与拨备变化"},
		"write":         {"汇总两个分析节点", "按 W01 模板生成结构化报告"},
	}[node]
	for i, d := range acts {
		c.ok("graph-host", "/v1/exec/progress", map[string]any{"task": l.task, "token": l.token, "o": o, "a": a, "seq": i + 1, "kind": "activity", "detail": d})
	}
	call := c.ok("graph-host", "/v1/exec/calls/bind", map[string]any{"task": l.task, "token": l.token, "o": o, "a": a, "name": "ask",
		"target": seedTargets[seedCaps[node]], "payload": map[string]any{"node": node, "template": "w01-report-v1"}})
	c.ok("graph-host", "/v1/exec/calls/dispatch", map[string]any{"task": l.task, "token": l.token, "b": call["b"]})
	c.ok("graph-host", "/v1/exec/calls/observe", map[string]any{"task": l.task, "token": l.token, "b": call["b"], "kind": "accepted", "remote_ref": "sim-a2a-" + strings.ReplaceAll(fmt.Sprint(o), "/", "-")})
	c.ok("graph-host", "/v1/exec/calls/observe", map[string]any{"task": l.task, "token": l.token, "b": call["b"], "kind": "succeeded", "result": body})
	out := c.ok("graph-host", "/v1/exec/commit", map[string]any{"task": l.task, "token": l.token, "o": o, "a": a, "outcome": "completed", "body": body})
	return out["result"].(map[string]any)["artifact"].(string)
}

func (c *seedClient) runAll(l seedLease, report map[string]any) string {
	c.runNode(l, "extract", map[string]any{"indicators": 8, "source": fmt.Sprint(l.snap["id"]), "snapshot_sha256": l.snap["sha256"]})
	c.runNode(l, "operating", map[string]any{"section": "operating", "variant": report["variant"]})
	c.runNode(l, "asset_quality", map[string]any{"section": "asset_quality", "variant": report["variant"]})
	return c.runNode(l, "write", report)
}

func (c *seedClient) checks(artifact, calc string, calcF any, cite string, citeF any) {
	c.ok("checker-calc", "/v1/checks", map[string]any{"artifact": artifact, "checker": "calc", "checker_version": "sim-0.1", "rule_version": "w01-r1", "verdict": calc, "findings": calcF})
	c.ok("checker-citation", "/v1/checks", map[string]any{"artifact": artifact, "checker": "citation", "checker_version": "sim-0.1", "rule_version": "w01-r1", "verdict": cite, "findings": citeF})
}
