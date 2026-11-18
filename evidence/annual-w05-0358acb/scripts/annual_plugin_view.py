"""Read-only: open the WorkSwarm 编排台 plugin for one task and screenshot it."""
import sys
from playwright.sync_api import sync_playwright

task, out = sys.argv[1], sys.argv[2]
with sync_playwright() as pw:
    b = pw.chromium.launch(headless=True, executable_path=r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe")
    page = b.new_page(viewport={"width": 1500, "height": 1000})
    page.goto("http://127.0.0.1:5173/chat/new")
    page.wait_for_timeout(2000)
    page.get_by_role("button", name="编排台", exact=True).click()
    frame = page.frame_locator("iframe")
    frame.locator(".wb-item", has_text=task).click()
    page.wait_for_timeout(3000)
    page.screenshot(path=out + "-overview.png")
    text = frame.locator("body").inner_text()
    open(out + "-text.txt", "w", encoding="utf-8").write(text)
    print(len(text), [l for l in text.splitlines() if any(k in l for k in ("extract", "business", "quality", "资料提取", "经营", "资产质量", "失败", "FAILED", "正式", "COMMITTED"))][:20])
    b.close()
