"""Pause one annual task on the W04 trusted page with the test account
(authorised by xierongqiag2015). Records the POST status; never prints the password."""
import json
import sys
import time
from pathlib import Path

from playwright.sync_api import sync_playwright

task, out = sys.argv[1], Path(sys.argv[2])
cfg = json.loads(Path("//wsl$/Ubuntu/home/xrq/report-demo-env/repo/.local/report-demo/config.json").read_text(encoding="utf-8"))
posts = []
with sync_playwright() as pw:
    b = pw.chromium.launch(headless=True, executable_path=r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe")
    p = b.new_page(viewport={"width": 1300, "height": 900})
    def on_response(r):
        if r.request.method == "POST":
            entry = {"at": time.time(), "path": r.url.split("8091", 1)[-1], "status": r.status}
            try:
                body = r.json()
                entry["body"] = {k: body[k] for k in ("status", "pause_requested", "error") if isinstance(body, dict) and k in body}
            except Exception:
                pass
            posts.append(entry)
    p.on("response", on_response)
    p.goto(f"http://127.0.0.1:8091/ui/tasks/{task}"); p.wait_for_timeout(1500)
    p.locator("input").nth(0).fill(cfg["user"]); p.locator("input[type=password]").first.fill(cfg["user_password"])
    p.get_by_role("button", name="登录", exact=True).click(); p.wait_for_timeout(2500)
    pause = p.locator("#pause")
    print("pause enabled:", pause.is_enabled())
    if pause.is_enabled():
        pause.click(); p.wait_for_timeout(2500)
    p.screenshot(path=str(out), full_page=True)
    b.close()
login_safe = [x for x in posts if not x["path"].startswith("/auth/")]
print(json.dumps(login_safe, ensure_ascii=False))
