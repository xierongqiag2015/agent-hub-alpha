"""Capture element screenshots of results/report.html for the Issue comment."""
import sys
from pathlib import Path
from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[1]
REPORT = (ROOT / "results" / "report.html").as_uri()
OUT = ROOT / "results" / "screenshots"
CHROME = r"C:\Program Files\Google\Chrome\Application\chrome.exe"

SHOTS = [
    ("01-overview.png", "#overview", "#summary"),
    ("02-summary-table.png", "#summary-table", None),
    ("03-c0-vs-e2c.png", "#vs", None),
    ("04-e3b-zombie-commit-fenced.png", "#E3b", None),
    ("05-e3c-zombie-dispatch-fenced.png", "#E3c", None),
    ("06-e2d-await-resume.png", "#E2d", None),
    ("07-e1-identity.png", "#E1", None),
    ("08-e5-timeout-late-success.png", "#E5", None),
    ("09-e4-knowledge-invalid.png", "#E4", None),
]

with sync_playwright() as p:
    browser = p.chromium.launch(executable_path=CHROME)
    page = browser.new_page(viewport={"width": 1500, "height": 1000}, device_scale_factor=2)
    page.goto(REPORT)
    page.add_style_tag(content=".sidebar{display:none}.content{max-width:1400px;margin:0 auto}")
    page.wait_for_timeout(300)
    for name, sel, until in SHOTS:
        el = page.locator(sel)
        if until:
            box1 = el.bounding_box()
            box2 = page.locator(until).bounding_box()
            page.screenshot(path=str(OUT / name), full_page=True,
                            clip={"x": box1["x"] - 10, "y": box1["y"] - 10, "width": box1["width"] + 20,
                                  "height": box2["y"] - box1["y"] + 4})
        else:
            el.screenshot(path=str(OUT / name))
        print("saved", name)
    full = browser.new_page(viewport={"width": 1440, "height": 1000})
    full.goto(REPORT)
    full.wait_for_timeout(300)
    full.screenshot(path=str(OUT / "00-report-top.png"))
    browser.close()
