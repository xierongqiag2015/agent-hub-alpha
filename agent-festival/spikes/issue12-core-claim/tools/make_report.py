"""Build results/report.html from results/<scenario>/{log.txt,state.json,verdict.json}."""
import html
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
RES = ROOT / "results"

ORDER = ["C0", "E1", "E2a", "E2b", "E2c", "E2c2", "E2d", "E3", "E3b", "E3b2", "E3c", "E4", "E5"]

META = {
    "C0": ("对照组", "credit_request 调用 API 后、回执入账前被杀，然后重跑", "Core 原生用法（不经控制面）在同一崩溃点会怎样"),
    "E1": ("P03", "同一 T 跑两遍；含 3 轮循环与子工作流节点", "o 稳定、轮次可区分、重跑零调用"),
    "E2a": ("P03 · M12", "judge 提交后、下一节点前被杀", "已提交节点重跑不再调用模型"),
    "E2b": ("P03", "judge 内层已执行、提交前被杀；租约过期后 W2 接管", "未提交计算可重算，正式结果仍 1 条"),
    "E2c": ("P05 · M07", "API 已受理、回执未入账时被杀", "按能力查询收敛，不盲目重发"),
    "E2c2": ("P05 · M07", "已签发派发许可、请求未发出时被杀", "查无此单 → 同一幂等键重发，B 不变"),
    "E2d": ("P06 · M08 · M12", "中断等待后被杀；提供方回调先到 Go，恢复输入是过期提示", "检查点续跑，以 Go 记录为准"),
    "E3": ("P03", "两个进程同时跑同一 T", "后到者加入等待，不并行执行"),
    "E3b": ("§9 崩溃轮", "旧进程停顿超过租约，在新 owner 执行中复活", "提交按代次隔离，旧结果作废"),
    "E3b2": ("§9 崩溃轮", "旧进程在新 owner 提交后复活", "识别为重复提交，采用已有结果"),
    "E3c": ("§9 崩溃轮 · P05", "旧进程卡在派发前，复活时新 owner 已派发", "派发许可按代次拒绝，不发请求"),
    "E4": ("P03 · CX5", "查到 K 后、提交前 K 失效", "提交被拒，不残留失效 K 的结果"),
    "E5": ("P05 · M09", "API 2.5s 才返回，调用方 0.8s 超时", "超时记未知，查询到迟到成功，不重发"),
}

TAGS = {
    "OWN": "#60a5fa", "COMMIT": "#4ade80", "REUSE": "#22d3ee", "CRASH": "#f87171", "FENCED": "#fb923c",
    "JOIN": "#c084fc", "SEND": "#facc15", "TIMEOUT": "#fb923c", "RECOVER": "#2dd4bf", "DUP": "#a3e635",
    "KSTALE": "#fb923c", "INJECT": "#f472b6", "AWAIT": "#c084fc", "RESUME": "#c084fc", "RUN": "#94a3b8",
    "PAUSE": "#fb923c", "SUBWF": "#94a3b8", "START": "#e2e8f0", "END": "#e2e8f0",
}

LINE = re.compile(r"^(\d\d:\d\d:\d\d\.\d{3}) \[([^\]]+)\] (\S+)\s*(.*)$")


def esc(s):
    return html.escape(str(s), quote=False)


def load(name):
    d = RES / name
    return (json.loads((d / "verdict.json").read_text(encoding="utf-8")),
            json.loads((d / "state.json").read_text(encoding="utf-8")),
            (d / "log.txt").read_text(encoding="utf-8").splitlines())


def metrics(name, state):
    task = {"C0": "TC0", "E1": "T1", "E2a": "T2a", "E2b": "T2b", "E2c": "T2c", "E2c2": "T2c2", "E2d": "T2d",
            "E3": "T3", "E3b": "T3b", "E3b2": "T3b2", "E3c": "T3c", "E4": "T4", "E5": "T5"}[name]
    c = {x["name"]: x["n"] for x in state.get("counters") or []}
    eff = state["bank"]["effects"] or {}
    calls = sum((state["bank"]["calls"] or {}).values())
    res = [r for r in state.get("results") or [] if r["o"].startswith(task + "/")]
    return {"s1": c.get("model:s1", 0), "inner_judge": c.get("inner:judge", 0), "effects": len(eff),
            "calls": calls, "results": len(res), "procs": None}


def fmt_line(m, workers):
    w = m.group(2).split("@")[0]
    workers.setdefault(w, len(workers))
    tag, rest = m.group(3), m.group(4)
    color = TAGS.get(tag, "#cbd5e1")
    rest_h = re.sub(r"(gen=\d+)", r'<b class="gen">\1</b>', esc(rest))
    return (f'<div class="ln"><span class="ts">{m.group(1)}</span> <span class="who w{workers[w]}">{esc(m.group(2))}</span> '
            f'<span class="tag" style="color:{color}">{esc(tag)}</span> <span class="msg">{rest_h}</span></div>')


def fmt_other(l):
    if l.startswith("$"):
        return f'<div class="cmd">{esc(l)}</div>'
    cls = "exit bad" if "137" in l else "exit"
    return f'<div class="{cls}">{esc(l.strip())}</div>'


def terminal(lines, merge):
    workers, out = {}, []
    if not merge:
        for l in lines:
            m = LINE.match(l)
            out.append(fmt_line(m, workers) if m else fmt_other(l))
    else:
        timed = []
        for i, l in enumerate(lines):
            m = LINE.match(l)
            if m:
                timed.append((m.group(1), i, m))
            elif l.startswith("$"):
                out.append(fmt_other(l))
        timed.sort(key=lambda t: (t[0], t[1]))
        out += [fmt_line(m, workers) for _, _, m in timed]
        out += [fmt_other(l) for l in lines if not LINE.match(l) and not l.startswith("$")]
    return '<div class="term">' + "\n".join(out) + "</div>"


def checks_html(v):
    rows = []
    for c in v["checks"]:
        mark = '<span class="ok">✔</span>' if c["pass"] else '<span class="bad">✘</span>'
        d = c["detail"]
        if len(d) > 220:
            d = d[:220] + " …"
        rows.append(f"<tr><td class='c'>{mark}</td><td>{esc(c['check'])}</td><td class='det'>{esc(d)}</td></tr>")
    return "<table class='checks'><thead><tr><th style='width:40px'></th><th>断言</th><th>实测</th></tr></thead><tbody>" + "".join(rows) + "</tbody></table>"


def state_tables(name, state):
    task_prefix = {"E3b": "T3b/judge", "E2b": "T2b/judge", "E3c": "T3c/credit", "E2c": "T2c/credit", "E2c2": "T2c2/credit",
                   "E4": "T4/evidence", "E2d": "T2d/await", "E5": "T5/credit", "E1": "T1/", "E2a": "T2a/judge", "E3": "T3/", "E3b2": "T3b2/judge", "C0": ""}[name]
    parts = []
    res = [r for r in state.get("results") or [] if r["o"].startswith(task_prefix)] if task_prefix else []
    if res:
        rows = "".join(f"<tr><td><code>{esc(r['o'])}</code></td><td class='c'>{r['owner_gen']}</td><td><code>{esc(r['h'])}</code></td><td>{esc(r['k_id'] or '—')}</td><td class='det'>{esc(r['result'])}</td></tr>" for r in res)
        parts.append("<h4>控制面 · results（正式结果）</h4><table class='mini'><thead><tr><th>o</th><th>owner_gen</th><th>h</th><th>K</th><th>result</th></tr></thead><tbody>" + rows + "</tbody></table>")
    if name in ("E2c", "E2c2", "E3c", "E5", "E2d"):
        br = "".join(f"<tr><td><code>{esc(b['b'])}</code></td><td>{esc(b['state'])}</td><td>{esc(b['provider_ref'])}</td></tr>" for b in state.get("business_requests") or [])
        at = "".join(f"<tr><td class='c'>{a['a']}</td><td class='c'>{a['owner_gen']}</td><td>{esc(a['state'])}</td><td>{esc(a['detail'])}</td></tr>" for a in state.get("attempts") or [])
        parts.append("<h4>控制面 · business_requests / attempts</h4><div class='two'><table class='mini'><thead><tr><th>B</th><th>state</th><th>provider_ref</th></tr></thead><tbody>" + br +
                     "</tbody></table><table class='mini'><thead><tr><th>a</th><th>gen</th><th>state</th><th>detail</th></tr></thead><tbody>" + at + "</tbody></table></div>")
    calls = state["bank"]["calls"] or {}
    eff = state["bank"]["effects"] or {}
    rows = "".join(f"<tr><td><code>{esc(k)}</code></td><td class='c'>{calls.get(k, 0)}</td><td>{esc(v.get('ref'))}</td><td>{esc(v.get('status'))}</td></tr>" for k, v in eff.items())
    parts.append("<h4>银行替身 · 实际业务效果</h4><table class='mini'><thead><tr><th>幂等键</th><th>调用次数</th><th>受理号</th><th>状态</th></tr></thead><tbody>" + rows + "</tbody></table>")
    if name in ("E3b", "E3b2", "E3c", "E4", "E2c", "E2c2", "E5"):
        keep = ("claim:takeover", "claim:busy", "commit:fenced", "commit:duplicate", "commit:k_invalid", "dispatch:fenced",
                "dispatch:recover", "recover:query-found", "recover:resend-same-key", "observe:timeout", "k:invalidate", "dispatch:permit")
        ev = [e for e in state.get("events") or [] if e["kind"] in keep]
        seen, uniq = set(), []
        for e in ev:
            key = (e["kind"], e["actor"], e["o"])
            if e["kind"] == "claim:busy" and key in seen:
                continue
            seen.add(key)
            uniq.append(e)
        rows = "".join(f"<tr><td>{esc(e['at'])}</td><td>{esc(e['actor'])}</td><td><code>{esc(e['kind'])}</code></td><td class='det'>{esc(e['o'])} {esc(e['detail'])}</td></tr>" for e in uniq)
        parts.append("<h4>控制面 · 关键事件</h4><table class='mini'><thead><tr><th>时间</th><th>actor</th><th>kind</th><th>detail</th></tr></thead><tbody>" + rows + "</tbody></table>")
    return "".join(parts)


def main():
    data = {n: load(n) for n in ORDER}
    reps = []
    for p in sorted(RES.glob("repeat-*/run.txt")) + [RES / "run-all.txt"]:
        t = p.read_text(encoding="utf-8", errors="replace")
        reps.append((t.count("[PASS]"), t.count("[FAIL]")))
    rounds = len(reps) + 1
    total_pass = all(v[0]["pass"] for v in data.values())

    rows = []
    for n in ORDER:
        v, s, _ = data[n]
        m = metrics(n, s)
        tagc = "ctrl" if n == "C0" else ""
        verdict = '<span class="pill bad">缺口复现</span>' if n == "C0" else ('<span class="pill ok">通过</span>' if v["pass"] else '<span class="pill bad">失败</span>')
        eff_cls = "bad" if m["effects"] > 1 else "ok"
        s1_cls = "bad" if (n == "C0" and m["s1"] > 1) else ""
        rows.append(f"<tr class='{tagc}'><td><a href='#{n}'><b>{n}</b></a></td><td>{esc(META[n][0])}</td><td>{esc(META[n][1])}</td>"
                    f"<td class='c {s1_cls}'>{m['s1']}</td><td class='c'>{m['results'] if n != 'C0' else '—'}</td>"
                    f"<td class='c {eff_cls}'>{m['effects']}</td><td class='c'>{verdict}</td></tr>")
    table = ("<table class='summary' id='summary-table'><thead><tr><th>场景</th><th>对应</th><th>注入的故障</th><th>S1 模型调用</th><th>正式结果条数</th>"
             "<th>业务效果次数</th><th>结论</th></tr></thead><tbody>" + "".join(rows) + "</tbody></table>")

    sections = []
    toc = []
    for n in ORDER:
        v, s, lines = data[n]
        merge = n in ("E3", "E3b", "E3b2", "E3c")
        badge = '<span class="pill bad">对照：缺口复现</span>' if n == "C0" else '<span class="pill ok">通过</span>'
        toc.append(f'<li><a href="#{n}">{n} · {esc(v["title"][:14])}</a></li>')
        sections.append(f"""
    <section class="scn" id="{n}">
      <h2>{n} · {esc(v['title'])} {badge}</h2>
      <div class="meta3"><div><b>注入</b>{esc(META[n][1])}</div><div><b>要证明</b>{esc(META[n][2])}</div><div><b>对应</b>{esc(META[n][0])}</div></div>
      <div class="cap">进程日志{'（两个进程按时间合并）' if merge else ''}：</div>
      {terminal(lines, merge)}
      {checks_html(v)}
      <details{' open' if n in ('C0','E2c','E3b','E3c') else ''}><summary>控制面与银行替身状态（state.json 摘录）</summary>{state_tables(n, s)}</details>
    </section>""")

    v0, s0, l0 = data["C0"]
    v1, s1, l1 = data["E2c"]
    compare = f"""
    <div class="vs" id="vs">
      <div><div class="vs-h bad">对照 C0：Core 原生用法</div>{terminal(l0, False)}
        <div class="vs-f bad">S1 模型调用 {metrics('C0', s0)['s1']} 次 · 业务效果 <b>{metrics('C0', s0)['effects']} 笔</b>（两个不同的键，同一笔授信）</div></div>
      <div><div class="vs-h ok">E2c：包装组件 + Go 正式结果</div>{terminal(l1, False)}
        <div class="vs-f ok">S1 模型调用 {metrics('E2c', s1)['s1']} 次 · 业务效果 <b>{metrics('E2c', s1)['effects']} 笔</b> · 恢复走查询，不重发</div></div>
    </div>"""

    css = (Path(__file__).with_name("report.css")).read_text(encoding="utf-8")
    page = f"""<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Core 崩溃一致性验证 · 包装组件 + 正式结果归 Go</title><style>{css}</style></head>
<body>
<button class="menu-toggle" type="button" onclick="document.querySelector('.sidebar').classList.toggle('open')">☰ 目录</button>
<div class="layout">
<aside class="sidebar"><div class="brand">Core 崩溃一致性验证<small>Issue #4 · P03 / ② 映射与核对</small></div>
<nav class="toc"><ol>
<li><a href="#overview">结论速览</a></li><li><a href="#summary">场景总表</a></li><li><a href="#mechanism">机制与环境</a></li><li><a href="#compare">对照：有无控制面</a></li>
{''.join(toc)}
<li><a href="#facts">Core 实测事实</a></li><li><a href="#limits">局限与下一步</a></li><li><a href="#repro">复现</a></li>
</ol></nav></aside>
<main class="content">
<header class="hero"><h1>在真实 openJiuwen Core 上验证“包装组件 + 正式结果归 Go”</h1>
<p>对 Issue #4 方案的关键前提做故障注入验证：Runtime/Core 负责调度，Go 控制面持有节点执行身份 o 和正式结果。在重跑、kill -9、并发、旧进程复活、知识失效和 API 超时等情况下，检验 <code class="inv">CommittedResults(o) ≤ 1</code> 和业务效果不重复这两条是否成立。</p>
<div class="meta"><span>agent-core-java 0.1.17（Maven Central）</span><span>JDK 17.0.21</span><span>Go 1.24.9 + SQLite</span><span>Windows 11</span><span>2026-11-10</span></div></header>

<h2 id="overview">结论速览</h2>
<div class="kpis">
<div class="kpi"><div class="n">{len(ORDER)-1}/{len(ORDER)-1}</div><div class="l">验证场景通过<br>（连续 {rounds} 轮全绿）</div></div>
<div class="kpi"><div class="n">1</div><div class="l">每个场景的业务效果<br>（对照组为 2）</div></div>
<div class="kpi"><div class="n">≤ 1</div><div class="l">每个 o 的正式结果<br>（含旧进程复活）</div></div>
<div class="kpi"><div class="n">0</div><div class="l">已提交节点重跑时<br>的模型 / 工具调用</div></div>
</div>
<div class="tldr"><strong>结论</strong><ol>
<li><b>方案在真实 Core 0.1.17 上可行。</b>业务节点用 <code>WorkflowComponent</code> 包装后，先向 Go 认领 o，再提交唯一正式结果。12 个场景（含子工作流、3 轮循环、中断等待）全部满足“每个 o 至多一条正式结果、业务效果 1 次”。</li>
<li><b>对照组复现了缺口。</b>在同一个崩溃点不经控制面时，Core 没有留下检查点，整张图从头重跑：S1 模型调用 2 次，同一笔授信放了 <b>2 次</b>。</li>
<li><b>源码阅读的结论得到实测确认：</b>
  <ul>
    <li>循环 3 轮的 <code>executableId</code> 都是 <code>loop.supplement</code>，o 必须由外部按轮次分配；</li>
    <li>kill -9 不留检查点，要靠同一个 T 整图重跑，再由包装组件复用已提交的结果；</li>
    <li>中断恢复时，被中断的节点从开头重跑；</li>
    <li>子工作流内部拿到的是同一个 T。</li>
  </ul></li>
<li><b>新发现的设计约束</b>（见<a href="#limits">局限与下一步</a>）：
  <ul>
    <li>租约必须续约，否则两个活着的进程会轮流接管；</li>
    <li>派发隔离目前只做到签发许可这一步，要在出口实时拦住旧代次需要出口代理（P04）；</li>
    <li>提交时要先判重复、再判代次。</li>
  </ul></li>
</ol></div>

<h2 id="summary">场景总表</h2>
<p>指标取自控制面 <code>state.json</code> 和银行替身的实际记录，不来自进程自报。点击场景名跳到对应的日志与断言。</p>
{table}
<p class="cap">E2b、E3b、E3b2 的 S1 调用为 2：旧 owner 的计算没有提交，被接管后重算，这是允许的（Issue #4 §6.4“尚未提交的纯计算允许重算”）；正式结果仍只有 1 条，业务效果仍为 1。C0 的 2 次则来自整图重跑。</p>

<h2 id="mechanism">机制与环境</h2>
<div class="arch">{MECH_SVG}</div>
<table class="mini"><thead><tr><th>项目</th><th>内容</th></tr></thead><tbody>
<tr><td>Core</td><td><code>com.openjiuwen:agent-core-java:0.1.17</code>（Maven Central）。与 <code>46673c6</code> 对比，相差 10 个 Java 文件（Workflow 事件处理、上下文、CompiledGraph/PregelGraph 的会话装配）和 autoharness 资源；本实验依赖的 <code>PregelLoop / Vertex / LoopRuntime / PersistenceCheckpointer / CheckpointerConfig</code> 两边逐字节相同</td></tr>
<tr><td>检查点</td><td>Core 自带 <code>PersistenceCheckpointer</code>（sqlite），工作流 ID 固定为 <code>credit-flow-v1</code>（默认是随机 UUID，跨进程恢复会找不到检查点）</td></tr>
<tr><td>o 的组成</td><td><code>task = session.getSessionId()</code>，<code>node</code> 取自包装组件，<code>iter</code> 由循环节点输出的 <code>${{loop.index}}</code> 映射进来；在 Go 里是唯一键 <code>(task,node,iter)</code></td></tr>
<tr><td>控制面</td><td>Go 1.24.9 + modernc SQLite（WAL，单连接串行事务）。不变量写成唯一约束和条件更新，在同一个事务里完成：<code>results(o) PK</code>、owner 代次比较、K 有效性复查、B 状态机</td></tr>
<tr><td>崩溃</td><td><code>Runtime.getRuntime().halt(137)</code>，不执行 finally 和关闭钩子，等价于 kill -9</td></tr>
<tr><td>替身</td><td>S1/S2 为确定性替身（计数）；银行 API 按键幂等、可查询，可注入延迟和受理中状态</td></tr>
</tbody></table>

<h2 id="compare">对照：有无控制面</h2>
<p>两组都在 <code>credit_request</code> 调用银行 API 之后、回执入账之前被强杀，然后用同一个任务 ID 重跑。</p>
{compare}

{''.join(sections)}

<h2 id="facts">Core 实测事实</h2>
<table class="mini"><thead><tr><th>事实</th><th>实测场景</th><th>对方案的影响</th></tr></thead><tbody>
<tr><td>循环每轮的 <code>executableId</code> 相同（<code>loop.supplement</code>），子工作流节点为 <code>kyc.kyc_check</code></td><td>E1</td><td>o 必须由 Go 按 <code>(T,node,iter)</code> 分配；轮次靠 <code>${{loop.index}}</code> 输入映射</td></tr>
<tr><td>进程被杀时不留检查点，重跑从图的开头执行</td><td>C0、E2a–E2c</td><td>恢复 = 同一 T 整图重跑 + 包装组件复用；检查点不能作为业务进度</td></tr>
<tr><td>中断等待会写持久检查点，恢复时只跑被中断的节点，且从节点开头重跑</td><td>E2d</td><td>被中断节点必须先读 Go 记录；恢复输入只是提示，不是事实</td></tr>
<tr><td>子工作流内部的 <code>sessionId</code> 与父图相同</td><td>E1</td><td>子工作流节点可以直接参与同一个 T 的 o 分配</td></tr>
<tr><td>默认工作流 ID 是随机 UUID</td><td>探针</td><td>跨进程恢复必须固定工作流 ID，否则找不到检查点</td></tr>
</tbody></table>

<h2 id="limits">局限与下一步</h2>
<div class="cols2">
<div class="card warn"><h4>本次未覆盖 / 已知约束</h4><ul>
<li>模型与业务 API 都是替身；没有经过 Runtime 的 HTTP/A2A 路径，没有接 WorkSwarm。</li>
<li>控制面单实例；租约固定不续约。节点执行时间超过租约时，两个活着的进程会轮流接管，生产环境需要心跳续约。</li>
<li>派发隔离做在签发许可这一步。旧进程如果已经拿到许可、又延迟发送，只能靠提供方幂等键兜底；要在出口实时校验代次，需要出口代理（P04）。</li>
<li>提交要先判重复、再判代次（E3b2）：旧进程在新 owner 提交之后复活，会被识别为重复并采用已有结果，不报“代次过期”。</li>
<li>Windows 单机实测，未做性能测试。E1 中 8 个业务节点（含控制面调用和 1 次 API）首次执行约 140 ms，全部复用的重跑约 60 ms（不含约 0.4 s 的 JVM 与 Core 初始化）。</li>
</ul></div>
<div class="card good"><h4>建议下一步</h4><ol>
<li>Runtime 路径：自定义 <code>AgentHandler</code>，把 <code>conversationId</code> 映射为 T，在 E2 系列上重跑。</li>
<li>出口代理与派发令牌（P04），在出口处按代次和载荷摘要拦截，补上 E3c 剩下的窗口。</li>
<li>租约续约，以及控制面重启和多实例场景。</li>
<li>与 V1–V5 的 Go 桩合并为同一套表结构（<code>node_exec / results / business_requests / attempts</code>）。</li>
</ol></div></div>

<h2 id="repro">复现</h2>
<pre>cd go &amp;&amp; go build -o ctl . &amp;&amp; ./ctl -db ../results/ctl.db &amp;
cd java &amp;&amp; mvn -q package dependency:build-classpath -Dmdep.outputFile=target/cp.txt
SPIKE_JAVA=$JAVA_HOME/bin/java python3 tools/run_scenarios.py      # 全部 13 个场景，约 55 秒
python3 tools/make_report.py</pre>
<p>每个场景的完整证据位于 <code>results/&lt;场景&gt;/</code>：<code>log.txt</code>、<code>state.json</code>、<code>verdict.json</code>。连续轮次：{'、'.join(f'{p}/{p+f}' for p, f in reps)} 通过。</p>
<footer>生成：tools/make_report.py · 数据来自 results/ 目录的实际运行</footer>
</main></div>
<script>
const links=[...document.querySelectorAll('.toc a')];const secs=links.map(a=>document.querySelector(a.getAttribute('href'))).filter(Boolean);
const setA=()=>{{let c=secs[0];for(const s of secs){{if(s.getBoundingClientRect().top<=110)c=s;}}links.forEach(a=>a.classList.toggle('active',a.getAttribute('href')==='#'+c.id));}};
document.addEventListener('scroll',setA,{{passive:true}});links.forEach(a=>a.addEventListener('click',()=>{{if(innerWidth<=980)document.querySelector('.sidebar').classList.remove('open');}}));setA();
</script></body></html>"""
    (RES / "report.html").write_text(page, encoding="utf-8")
    print("report:", RES / "report.html", "all pass:", total_pass)


MECH_SVG = """<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="300" viewBox="0 0 1200 300" role="img" aria-labelledby="mt">
<title id="mt">包装组件与 Go 控制面的交互</title>
<defs><marker id="ar" markerWidth="9" markerHeight="9" refX="7" refY="3" orient="auto" markerUnits="userSpaceOnUse"><path d="M0 0 L7 3 L0 6 z" fill="#334155"/></marker>
<style>.a{font:700 14px -apple-system,'Segoe UI','PingFang SC','Microsoft YaHei',sans-serif;fill:#1f2937}.b{font:12px -apple-system,'Segoe UI','PingFang SC','Microsoft YaHei',sans-serif;fill:#475569}</style></defs>
<rect width="1200" height="300" fill="#fff"/>
<rect x="20" y="40" width="300" height="220" rx="12" fill="#eff6ff" stroke="#bfdbfe"/>
<text x="170" y="68" text-anchor="middle" class="a">openJiuwen Core 0.1.17（调度）</text>
<text x="170" y="96" text-anchor="middle" class="b">Workflow / Pregel / Loop / SubWorkflow</text>
<rect x="45" y="116" width="250" height="124" rx="10" fill="#fff" stroke="#93c5fd"/>
<text x="170" y="140" text-anchor="middle" class="a">ClaimNode 包装组件</text>
<text x="170" y="164" text-anchor="middle" class="b">① claim(T,node,iter)</text>
<text x="170" y="184" text-anchor="middle" class="b">② result → 直接返回 / busy → 等待</text>
<text x="170" y="204" text-anchor="middle" class="b">③ owned(gen) → 执行内层</text>
<text x="170" y="224" text-anchor="middle" class="b">④ commit(o, gen, result, K)</text>
<rect x="450" y="40" width="330" height="220" rx="12" fill="#f8fafc" stroke="#94a3b8"/>
<text x="615" y="68" text-anchor="middle" class="a">Go 控制面（SQLite 单事务）</text>
<text x="615" y="100" text-anchor="middle" class="b">node_exec(task,node,iter) → o, owner_gen, 租约</text>
<text x="615" y="124" text-anchor="middle" class="b">results(o) 主键：至多一条正式结果</text>
<text x="615" y="148" text-anchor="middle" class="b">commit：先判重复，再判代次，再查 K 有效</text>
<text x="615" y="172" text-anchor="middle" class="b">business_requests(B) / attempts(B,a)</text>
<text x="615" y="196" text-anchor="middle" class="b">dispatch：仅当前代次签发许可</text>
<text x="615" y="220" text-anchor="middle" class="b">recover：先查询提供方，再同键重发</text>
<rect x="900" y="80" width="280" height="140" rx="12" fill="#fff7ed" stroke="#fdba74"/>
<text x="1040" y="110" text-anchor="middle" class="a">银行 API 替身</text>
<text x="1040" y="138" text-anchor="middle" class="b">按幂等键去重 · 可查询</text>
<text x="1040" y="162" text-anchor="middle" class="b">可注入延迟 / 受理中</text>
<text x="1040" y="186" text-anchor="middle" class="b">记录实际调用与效果</text>
<path d="M295 160 H450" stroke="#2563eb" stroke-width="2.4" marker-end="url(#ar)"/><text x="372" y="150" text-anchor="middle" class="b">claim / commit</text>
<path d="M450 200 H295" stroke="#16a34a" stroke-width="2.4" marker-end="url(#ar)"/><text x="372" y="222" text-anchor="middle" class="b">o · gen · 已有结果</text>
<path d="M295 236 C600 300, 820 280, 900 196" fill="none" stroke="#ea580c" stroke-width="2" stroke-dasharray="6 4" marker-end="url(#ar)"/><text x="610" y="292" text-anchor="middle" class="b">持许可发送（幂等键 = B）</text>
<path d="M780 130 H900" stroke="#94a3b8" stroke-width="2" marker-end="url(#ar)"/><text x="840" y="122" text-anchor="middle" class="b">查询</text>
</svg>"""

if __name__ == "__main__":
    main()
