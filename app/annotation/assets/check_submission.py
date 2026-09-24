#!/usr/bin/env python3
"""Read a 28-column satisfaction XLSX and report structural checks as JSON.

The checker uses only Python's standard library. It verifies workbook fields,
identities, prompt text, score cell types, attachment paths, and validation
ranges. It does not decide whether an evaluation is semantically correct or
whether an external reviewer will accept the submission.
"""

import argparse
import collections
import json
from pathlib import Path
import posixpath
import re
import sys
import xml.etree.ElementTree as ET
from zipfile import ZipFile


NS = "{http://schemas.openxmlformats.org/spreadsheetml/2006/main}"
RID = "{http://schemas.openxmlformats.org/officeDocument/2006/relationships}id"
HEADERS = [
    "User Prompt", "SessionID", "TurnID/PromptID", "初始环境快照", "轨迹文件",
    "环境可复现等级", "Harness", "Harness 版本", "操作系统", "任务类型", "任务难度",
    "语言/框架", "交付完整性", "交付完整性 - 描述", "指令遵循", "指令遵循 - 描述",
    "任务规划", "任务规划 - 描述", "推理能力", "推理能力 - 描述", "执行能力",
    "执行能力 - 描述", "其他问题", "提交人", "提交时间", "审核状态", "占位",
    "审核备注 (1)",
]
ENUMS = {
    "F": ["无外部依赖", "有外部依赖，未容器化", "已容器化，可一键起环境"],
    "G": ["Claude Code", "Codex CLI"],
    "I": ["MacOS/Linux", "Windows"],
    "J": ["Bug修复", "0-1代码生成", "feature迭代", "Feature迭代", "代码理解", "代码重构", "工程化", "代码测试"],
    "K": ["简单", "中等", "困难", "地狱"],
    "Z": ["待审核", "审核通过", "审核打回", "废弃", "打回"],
}
COLS = [chr(65 + index) for index in range(26)] + ["AA", "AB"]
SCORE_COLS = ["M", "O", "Q", "S", "U"]
DESCRIPTION_COLS = ["N", "P", "R", "T", "V"]
VALIDATED_COLS = ["F", "G", "I", "J", "K", "Z"] + SCORE_COLS
NOT_CHECKED = [
    "manifest provenance/completeness",
    "artifact attribution and actual tests",
    "network interruption causality",
    "score/reason consistency and natural prose",
    "snapshot accessibility and pre-task provenance",
    "external approval and AI-labeling acceptance",
]


def _cell_value(cell, shared_strings):
    kind = cell.get("t", "n")
    value = cell.findtext(NS + "v")
    inline = cell.find(NS + "is")
    if kind == "inlineStr":
        return "" if inline is None else "".join(inline.itertext())
    if kind == "s" and value is not None:
        return shared_strings[int(value)]
    return value


def _manifest_value(item, camel, snake=None):
    if camel in item:
        return item[camel]
    return item.get(snake or camel)


def _validation_covers(reference, column, last_row):
    for token in str(reference or "").split():
        match = re.fullmatch(r"\$?([A-Z]+)\$?(\d+):\$?([A-Z]+)\$?(\d+)", token)
        if match and match.group(1) == column and match.group(3) == column:
            if int(match.group(2)) <= 2 and int(match.group(4)) >= last_row:
                return True
    return False


def inspect(path, manifest=None, selected=None, allow_draft=False):
    """Inspect *path* and return a JSON-serializable check report."""
    path = Path(path)
    errors = []
    review = []
    records = []
    seen = {}
    validation_ranges = {}

    def flag(destination, location, issue):
        destination.append({"location": location, "issue": issue})

    with ZipFile(path) as archive:
        names = set(archive.namelist())
        required_parts = {"xl/workbook.xml", "xl/_rels/workbook.xml.rels"}
        missing_parts = sorted(required_parts - names)
        if missing_parts:
            raise ValueError("Missing XLSX parts: " + ", ".join(missing_parts))
        shared_strings = []
        if "xl/sharedStrings.xml" in names:
            shared_strings = [
                "".join(item.itertext())
                for item in ET.fromstring(archive.read("xl/sharedStrings.xml"))
            ]
        workbook = ET.fromstring(archive.read("xl/workbook.xml"))
        relationships = {
            item.get("Id"): item.get("Target")
            for item in ET.fromstring(archive.read("xl/_rels/workbook.xml.rels"))
        }
        sheets = workbook.find(NS + "sheets")
        sheet_names = [item.get("name") for item in sheets]
        if selected and selected not in sheet_names:
            raise ValueError("Unknown sheet: " + selected)

        for sheet in sheets:
            name = sheet.get("name")
            if selected and name != selected:
                continue
            target = relationships[sheet.get(RID)]
            target = target.lstrip("/") if target.startswith("/") else posixpath.normpath("xl/" + target)
            root = ET.fromstring(archive.read(target))
            rows = []
            for row in root.findall(NS + "sheetData/" + NS + "row"):
                cells = {}
                for cell in row.findall(NS + "c"):
                    column = re.sub(r"\d+", "", cell.get("r"))
                    cells[column] = (
                        _cell_value(cell, shared_strings),
                        cell.get("t", "n"),
                        cell.find(NS + "f") is not None,
                    )
                rows.append((int(row.get("r")), cells))
            if not rows:
                flag(errors, name, "Missing header")
                continue
            actual_headers = [rows[0][1].get(column, (None,))[0] for column in COLS]
            if actual_headers != HEADERS:
                flag(errors, name, "First 28 headers/order differ from current template")
                continue

            data_rows = []
            for number, cells in rows[1:]:
                values = {column: item[0] for column, item in cells.items()}
                if any(value is not None and str(value).strip() for value in values.values()):
                    data_rows.append((number, cells, values))
            last_data_row = max([number for number, _, _ in data_rows] or [2])

            validations = root.find(NS + "dataValidations")
            if validations is not None:
                for validation in validations.findall(NS + "dataValidation"):
                    reference = validation.get("sqref", "")
                    for column in VALIDATED_COLS:
                        if any(token.replace("$", "").startswith(column + "2:") for token in reference.split()):
                            validation_ranges[column] = reference
            for column in VALIDATED_COLS:
                reference = validation_ranges.get(column, "")
                if not _validation_covers(reference, column, last_data_row):
                    flag(errors, name + ":" + column, "Data validation does not cover every exported row")

            for number, cells, values in data_rows:
                location = f"{name}!{number}"
                if not allow_draft:
                    for column in COLS[:22] + ["X"]:
                        if values.get(column) is None or not str(values[column]).strip():
                            flag(errors, location + ":" + column, "Required value missing")
                score_values = []
                for column in SCORE_COLS:
                    value, kind, formula = cells.get(column, (None, "", False))
                    if value is None and allow_draft:
                        continue
                    if kind != "n" or formula or not re.fullmatch(r"[3-5]", str(value)):
                        flag(errors, location + ":" + column, "Score must be a numeric integer 3–5")
                    else:
                        score_values.append(int(value))
                if len(score_values) == 5 and sum(score_values) > 21:
                    flag(errors, location, "Five-dimensional score total exceeds 21 and is not collectable")
                for column, choices in ENUMS.items():
                    if values.get(column) and values[column] not in choices:
                        flag(errors, location + ":" + column, "Value outside template enum")
                for column in ["A", "B", "C", "D", "E", "X"]:
                    if cells.get(column, (None, None, False))[2]:
                        flag(errors, location + ":" + column, "Use literal text, not a formula")
                snapshot = values.get("D") or ""
                if snapshot and not re.fullmatch(r"https://github\.com/[^/]+/[^/]+/commit/[0-9a-fA-F]{40}", snapshot):
                    flag(errors, location + ":D", "Initial snapshot must be a full 40-character commit permalink")
                elif not snapshot and not allow_draft:
                    flag(errors, location + ":D", "Initial snapshot must be a full 40-character commit permalink")
                key = (values.get("B"), values.get("C"))
                if all(key):
                    if key in seen:
                        flag(errors, location, "Duplicate SessionID + PromptID; first at " + seen[key])
                    seen[key] = location
                attachment = values.get("E") or ""
                if attachment:
                    attachment_path = Path(attachment)
                    if not attachment_path.is_absolute():
                        flag(errors, location + ":E", "Trace must be a local absolute JSONL file path")
                    elif attachment_path.suffix.lower() != ".jsonl" or not attachment_path.is_file():
                        flag(errors, location + ":E", "Local JSONL trace file does not exist")
                if not values.get("Y"):
                    flag(review, location + ":Y", "Fill actual submission time when submission occurs")
                if values.get("G") == "Codex CLI":
                    flag(review, location + ":G", "Current source specification lists Claude Code only")
                note = re.sub(r"\s+", "", values.get("AB") or "")
                for column in DESCRIPTION_COLS:
                    if len(note) >= 20 and note in re.sub(r"\s+", "", values.get(column) or ""):
                        flag(review, location + ":" + column, "Review note copied into description; inspect attribution")
                records.append({
                    "location": location,
                    "sessionId": key[0],
                    "promptId": key[1],
                    "prompt": values.get("A"),
                })

    if not records and not allow_draft:
        flag(errors, "workbook", "No submission rows")
    session_counts = collections.Counter(record["sessionId"] for record in records if record["sessionId"])
    for session_id, count in session_counts.items():
        if count > 10:
            flag(review, "session " + str(session_id), "More than 10 rounds are retained; report collection risk without deleting rows")

    if manifest is not None:
        expected = {}
        manifest_rounds = manifest.get("rounds")
        if not isinstance(manifest_rounds, list):
            raise ValueError("Manifest requires a rounds array")
        for item in manifest_rounds:
            session_id = _manifest_value(item, "sessionId", "session_id")
            prompt_id = _manifest_value(item, "promptId", "prompt_id")
            included = item.get("included", item.get("valid"))
            key = (session_id, prompt_id)
            if key in expected:
                flag(errors, "manifest", "Duplicate SessionID + PromptID: " + str(key))
                continue
            if type(included) is not bool:
                raise ValueError("Manifest requires explicit boolean included/valid")
            prompt = item.get("prompt")
            if included and not isinstance(prompt, str):
                raise ValueError("Included manifest rounds require raw string prompt")
            if not included and not item.get("reason"):
                raise ValueError("Excluded manifest rounds require evidence-based reason")
            expected[key] = {"included": included, "prompt": prompt}
        for record in records:
            key = (record["sessionId"], record["promptId"])
            original = expected.get(key)
            if not original or not original["included"]:
                flag(errors, record["location"], "Row not present as included in trace-derived manifest")
            elif record["prompt"] != original["prompt"]:
                flag(errors, record["location"] + ":A", "Prompt differs from exact manifest text")
        for key, item in expected.items():
            if item["included"] and key not in seen:
                if allow_draft and len(item["prompt"]) > 32767:
                    continue
                flag(errors, "manifest", "Missing included round: " + str(key))

    return {
        "field_status": "FAIL" if errors else "PASS",
        "automated_scope": "syntax_and_structure_only",
        "semantic_status": "NOT_CHECKED",
        "rows_checked": len(records),
        "round_identity_check": "compared_to_supplied_manifest" if manifest is not None else "NOT_CHECKED",
        "validation_ranges": validation_ranges,
        "errors": errors,
        "requires_review": review,
        "not_checked": NOT_CHECKED,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("xlsx", type=Path)
    parser.add_argument("--rounds-json", type=Path)
    parser.add_argument("--sheet")
    parser.add_argument("--allow-draft", action="store_true")
    args = parser.parse_args()
    try:
        manifest = json.loads(args.rounds_json.read_text(encoding="utf-8")) if args.rounds_json else None
        result = inspect(args.xlsx, manifest, args.sheet, args.allow_draft)
    except Exception as exc:
        result = {
            "field_status": "FAIL",
            "automated_scope": "syntax_and_structure_only",
            "semantic_status": "NOT_CHECKED",
            "rows_checked": 0,
            "round_identity_check": "NOT_CHECKED",
            "validation_ranges": {},
            "errors": [{"location": "checker", "issue": f"{type(exc).__name__}: {exc}"}],
            "requires_review": [],
            "not_checked": NOT_CHECKED,
        }
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return int(result["field_status"] == "FAIL")


if __name__ == "__main__":
    sys.exit(main())
