"""Build the W06 material snapshot (manifest.json + pages/page-NNN.txt) from the annual chain's
verified CMB 2025 projection and the W01 reference answers' cited pages. Usage: w06_snapshot.py <out dir>"""
import hashlib
import json
import sys
from pathlib import Path

repo = Path.home() / "report-demo-env/repo"
material = json.loads((repo / ".local/report-demo/material.json").read_text(encoding="utf-8"))
answers = json.loads((repo / "demo/annual-report/answers.json").read_text(encoding="utf-8"))
out = Path(sys.argv[1])
(out / "pages").mkdir(parents=True, exist_ok=True)
KEY = {"revenue": "OPERATING_REVENUE", "net_profit_attributable": "NET_PROFIT_ATTRIBUTABLE",
       "net_interest_margin": "NET_INTEREST_MARGIN", "cost_to_income": "COST_INCOME_RATIO", "npl_balance": "NPL_BALANCE",
       "npl_ratio": "NPL_RATIO", "provision_coverage": "PROVISION_COVERAGE", "cet1_ratio_advanced": "CET1_RATIO"}
pages = {}
for p in material["pages"]:
    text = p["text"]
    (out / "pages" / f"page-{int(p['pdf_page']):03d}.txt").write_text(text, encoding="utf-8")
    pages[int(p["pdf_page"])] = {"printed_page": p.get("printed_page"), "sha256": hashlib.sha256(text.encode("utf-8")).hexdigest()}
items = answers["indicators"] if isinstance(answers["indicators"], list) else list(answers["indicators"].values())
index = {}
for it in items:
    src = it.get("source") or it.get("citation") or {}
    page = src.get("pdf_page") if isinstance(src, dict) else None
    if it.get("id") in KEY and page in pages:
        index[KEY[it["id"]]] = page
missing = sorted(set(KEY.values()) - set(index))
manifest = {"snapshot": material.get("id"), "title": material.get("title"), "material_digest": material.get("digest"),
            "projection_digest": material.get("projection_digest"), "source": "annual chain verified projection (pages 14-16) + W01 answers.json cited pages",
            "metric_index": index, "pages": pages}
(out / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding="utf-8")
print(json.dumps({"pages": sorted(pages), "metric_index": index, "missing_index": missing}, ensure_ascii=False))
