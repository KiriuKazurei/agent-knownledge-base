"""Run the real OCR engine against the complex-layout acceptance PDF."""

import argparse
import json
import os
import sys
import time
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "services" / "worker" / "src"))


def compact(value: str) -> str:
    return "".join(value.split())


def ordered(text: str, markers: list[str]) -> bool:
    positions = [text.find(compact(marker)) for marker in markers]
    return all(position >= 0 for position in positions) and positions == sorted(positions)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--package", required=True)
    parser.add_argument("--pdf", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()

    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    os.environ["KAH_DATA_ROOT"] = str(output / "data")

    from knowledge_worker import ocr_plugin
    from knowledge_worker.parsers import parse_document

    if ocr_plugin.status()["installed"]:
        ocr_plugin.uninstall()
    ocr_plugin.install(str(Path(args.package).resolve()))

    started = time.perf_counter()
    result = parse_document(
        Path(args.pdf).resolve(),
        "complex-layout-acceptance",
        "application/pdf",
        Path(args.pdf).name,
    )
    elapsed = round(time.perf_counter() - started, 3)
    pages: dict[int, str] = {}
    locations: dict[int, dict] = {}
    for chunk in result["chunks"]:
        page = int(chunk["location"]["page"])
        pages[page] = pages.get(page, "") + "\n" + chunk["text"]
        locations[page] = chunk["location"]

    normalized = {page: compact(text) for page, text in pages.items()}
    page1 = normalized.get(1, "")
    page2 = normalized.get(2, "")
    page3 = normalized.get(3, "")
    page4 = normalized.get(4, "")
    tables = locations.get(2, {}).get("ocrLayout", {}).get("tables", [])
    expected_headers = ["编号", "类型", "页数", "状态"]
    expected_rows = [
        ["T01", "扫描合同", "12", "通过"],
        ["T02", "双栏报告", "8", "复核"],
        ["T03", "旋转附件", "3", "失败"],
        ["T04", "低对比度", "5", "通过"],
    ]

    checks = {
        "all_pages_classified_complete": result["extraction"]["status"] == "complete"
        and result["extraction"]["pageCount"] == 4
        and not result["extraction"]["missingPages"],
        "page_end_markers_preserved": all(
            marker in normalized.get(page, "")
            for page, marker in ((1, "P1-END双栏页结束"), (2, "P2-END表格页结束"),
                                 (3, "P3-END旋转页结束"), (4, "P4-END文档结束"))
        ),
        "two_column_content_complete": all(marker in page1 for marker in ("L1", "L2", "L3", "R1", "R2", "R3")),
        "two_column_human_reading_order": ordered(page1, ["L1", "L2", "L3", "R1", "R2", "R3"]),
        "table_cells_complete": all(
            compact(value) in page2
            for value in ("T01", "扫描合同", "12", "通过", "T02", "双栏报告", "8", "复核",
                          "T03", "旋转附件", "3", "失败", "T04", "低对比度", "5")
        ),
        "table_row_association_order": bool(tables) and tables[0].get("headers") == expected_headers and tables[0].get("rows") == expected_rows,
        "low_contrast_text": compact("C1 低对比度正文：灰色文字也应被识别。") in page3,
        "rotated_90_text": "ROT90" in page3 and compact("旋转九十度文字") in page3,
        "sidebar_order": ordered(page3, ["S1", "S2", "S3"]),
        "hierarchy_and_footnote_order": ordered(
            page4, ["H1一级标题", "H1.1二级标题", "第一步", "H1.2二级标题", "第四步", "[1]页级定位"]
        ),
    }
    report = {
        "pdf": str(Path(args.pdf).resolve()),
        "elapsedSeconds": elapsed,
        "extraction": result["extraction"],
        "checks": checks,
        "passed": sum(checks.values()),
        "total": len(checks),
        "pages": pages,
        "tables": tables,
    }
    (output / "complex-ocr-results.json").write_text(
        json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8"
    )
    print(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
