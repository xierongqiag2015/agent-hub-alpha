"""Read-only: log in to the trusted page and screenshot the human-review and
report sections of one task (no decision, approve, deliver, pause or resume)."""
import json, sys
from pathlib import Path
from playwright.sync_api import sync_playwright
task, prefix = sys.argv[1], sys.argv[2]
cfg = json.loads(Path("//wsl$/Ubuntu/home/xrq/report-demo-env/repo/.local/report-demo/config.json").read_text(encoding="utf-8"))
with sync_playwright() as pw:
    b = pw.chromium.launch(headless=True, executable_path=r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe")
    p = b.new_page(viewport={"width": 1300, "height": 900})
    posts = []
    p.on("request", lambda r: posts.append(r.url.split("8091", 1)[-1]) if r.method == "POST" else None)
    p.goto(f"http://127.0.0.1:8091/ui/tasks/{task}"); p.wait_for_timeout(1500)
    p.locator("input").nth(0).fill(cfg["user"]); p.locator("input[type=password]").first.fill(cfg["user_password"])
    p.get_by_role("button", name="登录", exact=True).click(); p.wait_for_timeout(3000)
    for title, name in (("待人工核验的检查", "review"), ("核验与交付报告", "report")):
        sec = p.locator("section", has=p.locator("h2", has_text=title)).first
        if sec.count():
            sec.screenshot(path=f"{prefix}-{name}.png")
            print(name, "ok")
    print("POSTs:", posts)
    b.close()
