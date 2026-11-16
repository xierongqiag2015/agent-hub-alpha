"""Scenario driver for the NGA Core spike.

Runs each scenario against a fresh control-plane state, launches Worker
processes (real openJiuwen Core 0.1.17), injects crashes with Runtime.halt,
then pulls the control-plane state and asserts the pass conditions.
Writes results/<scenario>/{log.txt,state.json,verdict.json}.
"""
import json
import os
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
RESULTS = ROOT / "results"
JAVA = Path(os.environ["SPIKE_JAVA"])
CTL = "http://127.0.0.1:18080"
CP = str(ROOT / "java" / "target" / "classes") + ";" + (ROOT / "java" / "target" / "cp.txt").read_text().strip()


def ctl(path, body=None):
    data = json.dumps(body or {}).encode()
    req = urllib.request.Request(CTL + path, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read())


class Run:
    def __init__(self, name, title):
        self.name, self.title = name, title
        self.dir = RESULTS / name
        self.dir.mkdir(parents=True, exist_ok=True)
        for f in self.dir.glob("*.db*"):
            f.unlink()
        self.log = []
        self.checks = []
        ctl("/reset")

    def worker(self, label, wait=True, **kw):
        args = [str(JAVA), "-Duser.language=en", "-Dfile.encoding=UTF-8", "-cp", CP, "spike.Worker"]
        kw.setdefault("cpdb", str(self.dir / "checkpoint.db"))
        args += [f"{k}={v}" for k, v in kw.items()]
        self.log.append(f"$ worker {' '.join(f'{k}={v}' for k, v in kw.items() if k != 'cpdb')}")
        p = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, encoding="utf-8")
        p.label = label
        if wait:
            return self.collect(p)
        return p

    def collect(self, p):
        out, _ = p.communicate(timeout=300)
        lines = [l for l in out.splitlines() if l[:2].isdigit() and l[2] == ":"]
        errors = [l for l in out.splitlines() if "Exception" in l and "spike" not in l][:3]
        self.log += lines + [f"  !! {e}" for e in errors]
        self.log.append(f"  -> {p.label} exit code {p.returncode}" + (" (halted = simulated kill -9)" if p.returncode == 137 else ""))
        return p.returncode

    def check(self, label, ok, detail=""):
        self.checks.append({"check": label, "pass": bool(ok), "detail": str(detail)})

    def finish(self, extra=None):
        state = ctl("/state")
        (self.dir / "state.json").write_text(json.dumps(state, ensure_ascii=False, indent=2), encoding="utf-8")
        (self.dir / "log.txt").write_text("\n".join(self.log) + "\n", encoding="utf-8")
        verdict = {"scenario": self.name, "title": self.title, "pass": all(c["pass"] for c in self.checks),
                   "checks": self.checks, **(extra or {})}
        (self.dir / "verdict.json").write_text(json.dumps(verdict, ensure_ascii=False, indent=2), encoding="utf-8")
        mark = "PASS" if verdict["pass"] else "FAIL"
        print(f"[{mark}] {self.name}: {self.title}")
        for c in self.checks:
            print(f"    {'ok ' if c['pass'] else 'BAD'} {c['check']}  {c['detail']}")
        return state


N_NODES = 8  # evidence, kyc_check, supplement x3, judge, credit_request, await_receipt


def counters(state):
    return {c["name"]: c["n"] for c in state.get("counters") or []}


def results_for(state, task):
    return [r for r in state.get("results") or [] if r["o"].startswith(task + "/")]


def bank(state):
    b = state["bank"]
    return b["calls"] or {}, b["effects"] or {}


def e1():
    r = Run("E1", "稳定 o：重跑取回同一 o，循环轮次得到不同 o")
    r.worker("W1", task="T1", owner="W1")
    s1 = ctl("/state")
    c1 = counters(s1)
    r.worker("W1-rerun", task="T1", owner="W1")
    s = r.finish_state = ctl("/state")
    c2 = counters(s)
    os_ = sorted(x["o"] for x in results_for(s, "T1"))
    sup = [o for o in os_ if "/supplement/" in o]
    r.check("循环 3 轮得到 3 个不同 o（Core executableId 三轮相同）", len(set(sup)) == 3, sup)
    r.check("重跑后正式结果数不变（每个 o 恰好 1 条）", len(results_for(s1, "T1")) == len(os_) == N_NODES, f"{len(results_for(s1,'T1'))} -> {len(os_)}")
    r.check("重跑时所有业务节点内层调用 0 次", c1 == c2, f"inner counters first={c1} rerun={c2}")
    calls, effects = bank(s)
    r.check("业务 API 效果 1 次、调用 1 次", len(effects) == 1 and sum(calls.values()) == 1, f"calls={calls}")
    kyc = [x for x in os_ if "/kyc_check/" in x]
    r.check("子工作流内节点拿到同一 T（o 以 T1/ 开头）", kyc == ["T1/kyc_check/-"], kyc)
    reuse = sum(1 for e in s["events"] if e["kind"] == "claim:reuse")
    r.check("重跑全部走 REUSE", reuse == N_NODES, f"claim:reuse events={reuse}")
    r.finish()


def e2a():
    r = Run("E2a", "提交之后、下一节点之前进程被杀")
    r.worker("W1", task="T2a", owner="W1", crash="judge:after_commit")
    r.worker("W2", task="T2a", owner="W2")
    s = ctl("/state")
    c = counters(s)
    r.check("judge 内层（S1 模型）只执行 1 次", c.get("model:s1") == 1 and c.get("inner:judge") == 1, c)
    r.check("重跑取回原 judge 结果", any(e["kind"] == "claim:reuse" and "judge" in e["o"] for e in s["events"]))
    r.check("每个 o 恰好 1 条正式结果", len(results_for(s, "T2a")) == N_NODES, len(results_for(s, "T2a")))
    calls, effects = bank(s)
    r.check("业务 API 效果 1 次", len(effects) == 1, f"calls={calls}")
    r.finish()


def e2b():
    r = Run("E2b", "内层已执行、提交之前进程被杀（租约过期后接管）")
    r.worker("W1", task="T2b", owner="W1", crash="judge:after_inner_before_commit", lease_ms=1500)
    time.sleep(1.8)
    r.worker("W2", task="T2b", owner="W2")
    s = ctl("/state")
    c = counters(s)
    judge = [x for x in s["node_exec"] if x["o"] == "T2b/judge/-"][0]
    res = [x for x in s["results"] if x["o"] == "T2b/judge/-"]
    r.check("同一 o 被 W2 接管，owner_gen 1 -> 2", judge["owner_gen"] == 2 and judge["owner"].startswith("W2"), judge)
    r.check("judge 正式结果仍只有 1 条，且由 gen=2 提交", len(res) == 1 and res[0]["owner_gen"] == 2, res)
    r.check("未提交的计算允许重算（S1 调用 2 次，如实计数）", c.get("model:s1") == 2, c)
    calls, effects = bank(s)
    r.check("业务 API 效果 1 次", len(effects) == 1, f"calls={calls}")
    r.finish()


def e2c():
    r = Run("E2c", "业务 API 已受理、回执未入账时进程被杀")
    r.worker("W1", task="T2c", owner="W1", crash="credit_request:after_api_before_observe", lease_ms=1500)
    before = ctl("/state")
    b_before = [x for x in before["business_requests"]][0]
    time.sleep(1.8)
    r.worker("W2", task="T2c", owner="W2")
    s = ctl("/state")
    calls, effects = bank(s)
    b = s["business_requests"][0]
    kinds = [e["kind"] for e in s["events"]]
    r.check("崩溃时本地状态为 DISPATCH_COMMITTED（本地无回执）", b_before["state"] == "DISPATCH_COMMITTED", b_before["state"])
    r.check("恢复走 dispatch:recover → 查询提供方，而不是盲目重发", "dispatch:recover" in kinds and "recover:query-found" in kinds)
    r.check("业务 API 调用 1 次、效果 1 次（没有重发）", sum(calls.values()) == 1 and len(effects) == 1, f"calls={calls}")
    r.check("回执取自提供方查询，B 收敛为 SUCCEEDED", b["state"] == "SUCCEEDED" and b["provider_ref"].startswith("BANK-"), b)
    r.finish()


def e2c2():
    r = Run("E2c2", "已提交派发、请求未发出时进程被杀（提供方查无此单）")
    r.worker("W1", task="T2c2", owner="W1", crash="credit_request:after_dispatch_commit", lease_ms=1500)
    time.sleep(1.8)
    r.worker("W2", task="T2c2", owner="W2")
    s = ctl("/state")
    calls, effects = bank(s)
    att = [a for a in s["attempts"]]
    kinds = [e["kind"] for e in s["events"]]
    r.check("恢复先查询；查无此单，按能力契约用同一幂等键重发", "recover:resend-same-key" in kinds)
    r.check("尝试记录 a=1（崩溃）、a=2（重发），B 不变", len(att) == 2 and len({a["b"] for a in att}) == 1, [(a["a"], a["state"]) for a in att])
    r.check("业务 API 效果 1 次", len(effects) == 1 and sum(calls.values()) == 1, f"calls={calls}")
    r.finish()


def e2d():
    r = Run("E2d", "中断等待：检查点与 Go 记录的先后")
    r.worker("W1", task="T2d", owner="W1", bank_mode="pending", after_interrupt="halt")
    s0 = ctl("/state")
    b = s0["business_requests"][0]["b"]
    ctl("/b/observe", {"b": b, "outcome": "succeeded", "ref": "BANK-0001", "actor": "bank-callback"})
    r.log.append(f"$ provider callback: {b} succeeded (Go records it; resume input not yet delivered)")
    r.worker("W2-resume", task="T2d", owner="W2", resume="stale-hint:still-pending")
    s = ctl("/state")
    c = counters(s)
    aw = [x for x in s["results"] if x["o"] == "T2d/await_receipt/-"]
    r.check("恢复输入是过期提示，节点仍以 Go 记录为准（SUCCEEDED）", aw and '"SUCCEEDED"' in aw[0]["result"], aw)
    r.check("等待前节点（evidence/loop/judge/credit_request）恢复时内层 0 次重跑",
            c.get("inner:judge") == 1 and c.get("inner:credit_request") == 1 and c.get("inner:evidence") == 1, c)
    calls, effects = bank(s)
    r.check("业务 API 效果 1 次", len(effects) == 1 and sum(calls.values()) == 1, f"calls={calls}")
    r.check("await_receipt 正式结果 1 条", len(aw) == 1)
    r.finish()


def e3():
    r = Run("E3", "两个进程同时跑同一任务")
    p1 = r.worker("W1", wait=False, task="T3", owner="W1", judge_delay_ms=1500, lease_ms=5000)
    time.sleep(0.15)
    p2 = r.worker("W2", wait=False, task="T3", owner="W2", judge_delay_ms=1500, lease_ms=5000)
    r.collect(p1)
    r.collect(p2)
    s = ctl("/state")
    c = counters(s)
    kinds = [e["kind"] for e in s["events"]]
    calls, effects = bank(s)
    r.check("出现 claim:busy（后到者加入等待而不是并行执行）", "claim:busy" in kinds, kinds.count("claim:busy"))
    r.check("每个业务节点内层只执行 1 次", all(v == 1 for k, v in c.items() if k.startswith("inner:") and "supplement" not in k), c)
    r.check("每个 o 恰好 1 条正式结果", len(results_for(s, "T3")) == N_NODES, len(results_for(s, "T3")))
    r.check("业务 API 效果 1 次", len(effects) == 1, f"calls={calls}")
    r.finish()


def e3b():
    r = Run("E3b", "旧 worker 复活：新 owner 仍在执行时（提交处按代次隔离）")
    p1 = r.worker("W1-zombie", wait=False, task="T3b", owner="W1", judge_delay_ms=3000, lease_ms=1000)
    time.sleep(2.0)
    p2 = r.worker("W2", wait=False, task="T3b", owner="W2", judge_delay_ms=2500, lease_ms=6000)
    r.collect(p2)
    r.collect(p1)
    s = ctl("/state")
    res = [x for x in s["results"] if x["o"] == "T3b/judge/-"]
    kinds = [e["kind"] for e in s["events"]]
    calls, effects = bank(s)
    r.check("W2 在 W1 租约过期后接管 judge（claim:takeover，gen 1 -> 2）", "claim:takeover" in kinds)
    r.check("W1 复活后以 gen=1 提交被拒（commit:fenced）", "commit:fenced" in kinds)
    r.check("W1 随后加入等待（claim:busy），最终复用 W2 的结果", "claim:busy" in kinds and any(e["kind"] == "claim:reuse" and e["o"] == "T3b/judge/-" and e["actor"].startswith("W1") for e in s["events"]))
    r.check("judge 正式结果 1 条，由 gen=2 提交", len(res) == 1 and res[0]["owner_gen"] == 2, res)
    r.check("每个 o 恰好 1 条正式结果；业务 API 效果 1 次", len(results_for(s, "T3b")) == N_NODES and len(effects) == 1, f"results={len(results_for(s,'T3b'))} calls={calls}")
    r.finish()


def e3b2():
    r = Run("E3b2", "旧 worker 复活：新 owner 已提交后（重复提交被识别）")
    p1 = r.worker("W1-zombie", wait=False, task="T3b2", owner="W1", judge_delay_ms=3000, lease_ms=1000)
    time.sleep(2.0)
    p2 = r.worker("W2", wait=False, task="T3b2", owner="W2", lease_ms=1000)
    r.collect(p2)
    r.collect(p1)
    s = ctl("/state")
    res = [x for x in s["results"] if x["o"] == "T3b2/judge/-"]
    kinds = [e["kind"] for e in s["events"]]
    calls, effects = bank(s)
    r.check("W2 接管并提交 judge（gen=2）", "claim:takeover" in kinds and len(res) == 1 and res[0]["owner_gen"] == 2, res)
    r.check("W1 复活后提交被识别为重复（commit:duplicate），采用已有正式结果，不覆盖", "commit:duplicate" in kinds)
    r.check("每个 o 恰好 1 条正式结果；业务 API 效果 1 次", len(results_for(s, "T3b2")) == N_NODES and len(effects) == 1, f"results={len(results_for(s,'T3b2'))} calls={calls}")
    r.finish()


def e3c():
    r = Run("E3c", "旧 worker 复活：卡在派发前（派发许可处隔离）")
    p1 = r.worker("W1-zombie", wait=False, task="T3c", owner="W1", pre_dispatch_pause_ms=3000, lease_ms=1000)
    time.sleep(2.2)
    p2 = r.worker("W2", wait=False, task="T3c", owner="W2", lease_ms=1000)
    r.collect(p2)
    r.collect(p1)
    s = ctl("/state")
    kinds = [e["kind"] for e in s["events"]]
    calls, effects = bank(s)
    att = s["attempts"] or []
    r.check("W2 接管 credit_request 并派发（gen=2）", "claim:takeover" in kinds and any(a["owner_gen"] == 2 for a in att), [(a["a"], a["owner_gen"]) for a in att])
    r.check("W1 复活后派发许可被拒（dispatch:fenced），没有发出请求", "dispatch:fenced" in kinds)
    r.check("业务 API 调用 1 次、效果 1 次", sum(calls.values()) == 1 and len(effects) == 1, f"calls={calls}")
    r.check("每个 o 恰好 1 条正式结果", len(results_for(s, "T3c")) == N_NODES, len(results_for(s, "T3c")))
    r.finish()


def e4():
    r = Run("E4", "查到 K 之后、提交之前 K 失效")
    ctl("/k/publish", {"id": "k-evidence-v1", "node": "evidence", "version": "1"})
    r.worker("W1", task="T4", owner="W1", invalidate_k_after_find="true")
    s = ctl("/state")
    ev = [x for x in s["results"] if x["o"] == "T4/evidence/-"]
    kinds = [e["kind"] for e in s["events"]]
    r.check("提交被拒（commit:k_invalid）", "commit:k_invalid" in kinds)
    r.check("没有残留基于失效 K 的正式结果", ev and ev[0]["k_id"] == "" and "K:" not in ev[0]["result"], ev)
    r.check("改走 S2 调查后提交，evidence 正式结果 1 条", len(ev) == 1 and counters(s).get("model:s2") == 1, counters(s))
    r.finish()


def e5():
    r = Run("E5", "业务 API 超时后又成功返回")
    r.worker("W1", task="T5", owner="W1", bank_delay_ms=2500, bank_timeout_ms=800, recover_interval_ms=3000)
    s = ctl("/state")
    calls, effects = bank(s)
    b = s["business_requests"][0]
    kinds = [e["kind"] for e in s["events"]]
    r.check("超时记为 UNRESOLVED（未知），不是失败", "observe:timeout" in kinds)
    r.check("恢复查询到迟到的成功，不重发", "recover:query-found" in kinds and "recover:resend-same-key" not in kinds)
    r.check("业务 API 调用 1 次、效果 1 次", sum(calls.values()) == 1 and len(effects) == 1, f"calls={calls}")
    r.check("B 最终 SUCCEEDED", b["state"] == "SUCCEEDED", b["state"])
    r.finish()


def c0():
    r = Run("C0", "对照组：同一崩溃点，不经控制面（Core 原生用法）")
    r.worker("W1", task="TC0", owner="W1", naive="true", crash="credit_request:after_api_before_observe")
    r.worker("W2", task="TC0", owner="W2", naive="true")
    s = ctl("/state")
    c = counters(s)
    calls, effects = bank(s)
    r.check("[缺口复现] 崩溃时无检查点，整张图从头重跑：S1 模型调用 2 次", c.get("model:s1") == 2, c)
    r.check("[缺口复现] 业务 API 效果 2 次（同一笔授信放了两次）", len(effects) == 2, f"effects={list(effects)}")
    r.finish({"control": True})


ALL = {"C0": c0, "E1": e1, "E2a": e2a, "E2b": e2b, "E2c": e2c, "E2c2": e2c2, "E2d": e2d, "E3": e3, "E3b": e3b, "E3b2": e3b2, "E3c": e3c, "E4": e4, "E5": e5}

if __name__ == "__main__":
    names = sys.argv[1:] or list(ALL)
    for n in names:
        ALL[n]()
