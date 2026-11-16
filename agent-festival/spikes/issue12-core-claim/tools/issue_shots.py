"""Focused screenshots for the Issue comment: heading + log + checks, no state dump."""
from pathlib import Path
from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[1]
REPORT = (ROOT / "results" / "report.html").as_uri()
import os
SCALE = float(os.environ.get("SHOT_SCALE", "2"))
EXT = os.environ.get("SHOT_EXT", "png")
OUT = ROOT / "results" / os.environ.get("SHOT_DIR", "issue-images")
OUT.mkdir(parents=True, exist_ok=True)
CHROME = r"C:\Program Files\Google\Chrome\Application\chrome.exe"

SCENARIOS = [
    ("05-e3b-zombie-commit.png", "#E3b"),
    ("06-e3c-zombie-dispatch.png", "#E3c"),
    ("07-e2d-await-resume.png", "#E2d"),
    ("08-e1-identity.png", "#E1"),
    ("09-e5-timeout.png", "#E5"),
    ("10-e4-knowledge.png", "#E4"),
]

def fix(name):
    return name[:-3] + EXT


def opts():
    return {"type": "jpeg", "quality": 90} if EXT == "jpg" else {}


with sync_playwright() as p:
    browser = p.chromium.launch(executable_path=CHROME)
    page = browser.new_page(viewport={"width": 1400, "height": 1000}, device_scale_factor=SCALE)
    page.goto(REPORT)
    page.add_style_tag(content=".sidebar{display:none}.content{max-width:1360px;margin:0 auto}.scn details{display:none}")
    page.wait_for_timeout(300)

    def clip_between(name, top_sel, bottom_sel, pad=10):
        rect = "el => { const r = el.getBoundingClientRect(); return {x: r.left + scrollX, y: r.top + scrollY, width: r.width, height: r.height}; }"
        a = page.locator(top_sel).evaluate(rect)
        b = page.locator(bottom_sel).evaluate(rect)
        page.screenshot(path=str(OUT / fix(name)), full_page=True, **opts(),
                        clip={"x": a["x"] - pad, "y": a["y"] - pad, "width": a["width"] + 2 * pad,
                              "height": b["y"] + b["height"] - a["y"] + 2 * pad})
        print("saved", name)

    clip_between("01-conclusion.png", "#overview", ".tldr")
    page.locator("#mechanism + .arch").screenshot(path=str(OUT / fix("02-mechanism.png")), **opts())
    print("saved 02-mechanism.png")
    clip_between("03-summary.png", "#summary-table", "#summary-table + p.cap")
    page.locator("#vs").screenshot(path=str(OUT / fix("04-control-vs-wrapper.png")), **opts())
    print("saved 04-control-vs-wrapper.png")
    for name, sel in SCENARIOS:
        clip_between(name, f"{sel} h2", f"{sel} table.checks")
    browser.close()
