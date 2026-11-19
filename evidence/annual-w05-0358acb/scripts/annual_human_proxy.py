"""W05 automation proxy for the HUMAN review on the W04 trusted page (authorised
by xierongqiag2015): log in with the test account, accept check0 with an
explicit basis, press 继续. Records network status codes and screenshots;
never prints the password."""
import json
import sys
import time
from pathlib import Path

from playwright.sync_api import sync_playwright

task, out_dir = sys.argv[1], Path(sys.argv[2])
out_dir.mkdir(parents=True, exist_ok=True)
cfg = json.loads(Path("//wsl$/Ubuntu/home/xrq/report-demo-env/repo/.local/report-demo/config.json").read_text(encoding="utf-8"))
REASON = ("W05 自动化代操作（xierongqiag2015 授权，非真实人工核验，真实人工核验待补）："
          "逐项核对 4 条结论，均为年报原文表格行的逐字引用——营业收入、归属于本行股东的净利润（PDF 第 14 页/印刷第 13 页 2.1 节），"
          "不良贷款率、拨备覆盖率（PDF 第 16 页/印刷第 15 页 2.2 节）；数值、期间、口径与 W01 acceptance/answers.json 逐字一致。"
          "本次未打开原文 PDF 独立核对；只覆盖依据是否支撑，不覆盖分析质量与业务结论。")
record = {"task_id": task, "operator": "W05 automation proxy (test account demo), authorised by xierongqiag2015",
          "reason": REASON, "network": [], "steps": []}

def step(name, **kv):
    record["steps"].append({"at": time.time(), "step": name, **kv})
    print(json.dumps({"step": name, **kv}, ensure_ascii=False), flush=True)

with sync_playwright() as pw:
    b = pw.chromium.launch(headless=True, executable_path=r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe")
    page = b.new_page(viewport={"width": 1400, "height": 1000})
    def on_response(r):
        if "/v1/" in r.url or "/auth/" in r.url:
            if r.request.method != "GET":
                entry = {"at": time.time(), "method": r.request.method, "path": r.url.split("8091", 1)[-1], "status": r.status}
                try:
                    body = r.json()
                    entry["body"] = {k: body[k] for k in ("receipt", "status", "decision", "outcome", "error", "code", "review") if isinstance(body, dict) and k in body}
                except Exception:
                    pass
                record["network"].append(entry)
    page.on("response", on_response)
    page.goto(f"http://127.0.0.1:8091/ui/tasks/{task}")
    page.wait_for_timeout(1500)
    inputs = page.locator("input")
    inputs.nth(0).fill(cfg["user"])
    page.locator("input[type=password]").first.fill(cfg["user_password"])
    page.get_by_role("button", name="登录", exact=True).click()
    page.wait_for_timeout(2500)
    page.screenshot(path=str(out_dir / "1-after-login.png"), full_page=True)
    step("logged_in")
    textarea = page.locator("textarea")
    if textarea.count() == 0:
        step("no_pending_review"); page.screenshot(path=str(out_dir / "1b-no-review.png"), full_page=True); b.close()
        (out_dir / "human-proxy.json").write_text(json.dumps(record, ensure_ascii=False, indent=2), encoding="utf-8"); sys.exit(1)
    textarea.first.fill(REASON)
    page.screenshot(path=str(out_dir / "2-reason-filled.png"), full_page=True)
    page.get_by_role("button", name="人工核验接受", exact=True).first.click()
    page.wait_for_timeout(3000)
    page.screenshot(path=str(out_dir / "3-after-accept.png"), full_page=True)
    step("accepted")
    resume = page.locator("#resume")
    step("resume_enabled", enabled=resume.is_enabled())
    if resume.is_enabled():
        resume.click()
        page.wait_for_timeout(3000)
        step("resume_clicked")
    page.get_by_role("button", name="刷新", exact=True).click()
    page.wait_for_timeout(2000)
    page.screenshot(path=str(out_dir / "4-after-continue.png"), full_page=True)
    record["page_text_after"] = page.locator("body").inner_text()[:4000]
    b.close()
(out_dir / "human-proxy.json").write_text(json.dumps(record, ensure_ascii=False, indent=2), encoding="utf-8")
print(json.dumps(record["network"], ensure_ascii=False)[:1500])
