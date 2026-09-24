#!/usr/bin/env python3
"""Export one frozen satisfaction batch to an XLSX delivery directory.

Usage:
  export_satisfaction.py --input batch.json --output DIRECTORY [--draft]

Stdout is always one JSON object with outputPath, reportPath, rows, and issues.
Formal validation failures return exit code 2. Unsafe paths, malformed JSON, and
other execution failures return exit code 1. Draft completeness issues are
reported while the command returns 0.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import sys
import xml.etree.ElementTree as ET
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo

from check_submission import inspect as inspect_submission


MAIN_NS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
NS = "{" + MAIN_NS + "}"
XML_SPACE = "{http://www.w3.org/XML/1998/namespace}space"
ET.register_namespace("", MAIN_NS)

TEMPLATE = Path(__file__).with_name("template.xlsx")
SHEET_PATH = "xl/worksheets/sheet1.xml"
SCORE_COLUMNS = [12, 14, 16, 18, 20]  # zero based: M/O/Q/S/U
ENUM_COLUMNS = {5, 6, 8, 9, 10, 12, 14, 16, 18, 20}
FULL_SHA = re.compile(r"[0-9a-fA-F]{40}\Z")
SNAPSHOT = re.compile(r"https://github\.com/[^/]+/[^/]+/commit/([0-9a-fA-F]{40})\Z")
ROUND_STATUSES = {"complete", "pending", "conflict", "excluded"}
ENVIRONMENTS = {"无外部依赖", "有外部依赖，未容器化", "已容器化，可一键起环境"}
TASK_TYPES = {"Bug修复", "0-1代码生成", "feature迭代", "Feature迭代", "代码理解", "代码重构", "工程化", "代码测试"}
DIFFICULTIES = {"简单", "中等", "困难", "地狱"}
OPERATING_SYSTEMS = {"MacOS/Linux", "Windows"}
FIXED_ZIP_TIME = (1980, 1, 1, 0, 0, 0)


class ExportFailure(Exception):
    """An execution or path-safety failure that blocks formal and draft output."""


def _issue(scope, message):
    return f"{scope}: {message}"


def _as_text(value):
    return value if isinstance(value, str) else ""


def _opaque_component(prefix, *identities):
    digest = hashlib.sha256()
    for identity in identities:
        digest.update(_as_text(identity).encode("utf-8"))
        digest.update(b"\0")
    return prefix + "-" + digest.hexdigest()


def _within(path, root):
    try:
        path.relative_to(root)
        return True
    except ValueError:
        return False


def _source_path(value, input_dir):
    if not isinstance(value, str) or not value.strip():
        return None
    path = Path(value)
    selected = path if path.is_absolute() else input_dir / path
    return Path(os.path.abspath(str(selected)))


def _resolve_member(root, value, label):
    if not value:
        return None, None
    raw = Path(value)
    resolved_root = root.resolve(strict=False)
    resolved = (raw if raw.is_absolute() else root / raw).resolve(strict=False)
    if not _within(resolved, resolved_root):
        raise ExportFailure(f"{label} escapes capture directory")
    return resolved, resolved.relative_to(resolved_root)


def _scan_capture(root, output):
    if root is None:
        return
    if root.is_symlink():
        raise ExportFailure(f"capture directory is a symlink: {root}")
    if not root.exists() or not root.is_dir():
        return
    if _within(output.resolve(strict=False), root.resolve(strict=False)):
        raise ExportFailure("output directory cannot be inside a capture directory")
    resolved_root = root.resolve(strict=True)
    for current, directories, files in os.walk(root, followlinks=False):
        for name in directories + files:
            entry = Path(current) / name
            if not entry.is_symlink():
                continue
            try:
                target = entry.resolve(strict=True)
            except OSError as exc:
                raise ExportFailure(f"capture contains broken symlink: {entry}: {exc}") from exc
            if not _within(target, resolved_root):
                raise ExportFailure(f"capture symlink escapes capture directory: {entry}")


def _matching_evaluation(round_item):
    evidence_hash = round_item.get("evidenceHash")
    evaluations = round_item.get("evaluations")
    if not isinstance(evaluations, list):
        return None
    for evaluation in reversed(evaluations):
        if isinstance(evaluation, dict) and evaluation.get("evidenceHash") == evidence_hash:
            return evaluation
    return None


def _round_sort_key(indexed_round):
    index, item = indexed_round
    order = item.get("order")
    order_value = order if isinstance(order, int) and not isinstance(order, bool) else 2 ** 31
    return (_as_text(item.get("sessionId")), order_value, index)


def _case_sort_key(indexed_case):
    index, item = indexed_case
    return (_as_text(item.get("projectId")), _as_text(item.get("taskId")), _as_text(item.get("taskName")), index)


def _validate_evaluation(evaluation, scope):
    issues = []
    if evaluation is None:
        return [_issue(scope, "no evaluation matches the round evidenceHash")]
    if evaluation.get("status") != "ready":
        issues.append(_issue(scope, "matching evaluation status is not ready"))
    scores = evaluation.get("scores")
    if not isinstance(scores, list) or len(scores) != 5:
        issues.append(_issue(scope, "evaluation scores must contain five integers"))
    else:
        for index, score in enumerate(scores, 1):
            if isinstance(score, bool) or not isinstance(score, int) or not 3 <= score <= 5:
                issues.append(_issue(scope, f"evaluation score {index} must be an integer from 3 to 5"))
    prompt = _as_text(evaluation.get("nextPrompt")).strip()
    prompt_type = _as_text(evaluation.get("nextPromptType")).strip()
    if scores == [5, 5, 5, 5, 5] and (prompt or prompt_type):
        issues.append(_issue(scope, "五维满分不能包含修复提示词，请重新审核"))
    has_bug = any(isinstance(item, dict) and item.get("kind") == "bug" for item in (evaluation.get("issues") or []))
    if not has_bug and (prompt or prompt_type):
        issues.append(_issue(scope, "修复提示词缺少已确认的代码问题依据，不能仅因低分生成，请重新审核"))
    if has_bug:
        if not isinstance(scores, list) or not scores or type(scores[0]) is not int or not 3 <= scores[0] < 5:
            issues.append(_issue(scope, "存在功能遗漏或 Bug，交付完整性必须低于 5 分，请重新审核"))
        if not prompt.startswith("修复") or not prompt[2:].strip() or prompt_type != "Bug修复":
            issues.append(_issue(scope, "Bug 必须有以“修复”开头的具体修复提示词，请重新审核"))
    descriptions = evaluation.get("descriptions")
    if not isinstance(descriptions, list) or len(descriptions) != 5:
        issues.append(_issue(scope, "evaluation descriptions must contain five strings"))
    else:
        for index, description in enumerate(descriptions, 1):
            if not isinstance(description, str) or not description.strip():
                issues.append(_issue(scope, f"evaluation description {index} is missing"))
            elif len(description) > 32767:
                issues.append(_issue(scope, f"evaluation description {index} exceeds Excel's 32767-character limit"))
    if evaluation.get("taskType") not in TASK_TYPES:
        issues.append(_issue(scope, "taskType is missing or outside the template enum"))
    if evaluation.get("difficulty") not in DIFFICULTIES:
        issues.append(_issue(scope, "difficulty is missing or outside the template enum"))
    if evaluation.get("environment") not in ENVIRONMENTS:
        issues.append(_issue(scope, "environment is missing or outside the template enum"))
    if evaluation.get("os") not in OPERATING_SYSTEMS:
        issues.append(_issue(scope, "os is missing or outside the template enum"))
    for field in ("language", "harnessVersion", "skillHash", "model", "id"):
        if not _as_text(evaluation.get(field)).strip():
            issues.append(_issue(scope, f"evaluation {field} is missing"))
    evidence = evaluation.get("evidence")
    if not isinstance(evidence, list) or not evidence or not all(isinstance(item, str) and item.strip() for item in evidence):
        issues.append(_issue(scope, "evaluation evidence must contain at least one concrete reference"))
    missing = evaluation.get("missing")
    if not isinstance(missing, list):
        issues.append(_issue(scope, "evaluation missing must be an array"))
    elif missing:
        issues.append(_issue(scope, "evaluation still has missing evidence: " + "; ".join(map(str, missing))))
    return issues


def _evaluation_score_total(evaluation):
    if not isinstance(evaluation, dict):
        return None
    scores = evaluation.get("scores")
    if not isinstance(scores, list) or len(scores) != 5:
        return None
    if any(isinstance(score, bool) or not isinstance(score, int) or not 3 <= score <= 5 for score in scores):
        return None
    return sum(scores)


def _write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def _copy_capture(source, destination):
    if destination.exists():
        raise ExportFailure(f"attachment destination already exists: {destination}")
    destination.mkdir(parents=True)
    source_root = source.resolve(strict=True)

    def copy_link(link, copied_link):
        resolved_target = link.resolve(strict=True)
        copied_target = destination / resolved_target.relative_to(source_root)
        relative_target = os.path.relpath(copied_target, copied_link.parent)
        copied_link.symlink_to(relative_target, target_is_directory=resolved_target.is_dir())

    for current, directories, files in os.walk(source, followlinks=False):
        current_path = Path(current)
        relative = current_path.resolve(strict=True).relative_to(source_root)
        copied_directory = destination / relative
        copied_directory.mkdir(parents=True, exist_ok=True)
        traversed_directories = []
        for name in directories:
            child = current_path / name
            copied_child = copied_directory / name
            if child.is_symlink():
                copy_link(child, copied_child)
            else:
                copied_child.mkdir(exist_ok=True)
                traversed_directories.append(name)
        directories[:] = traversed_directories
        for name in files:
            child = current_path / name
            copied_child = copied_directory / name
            if child.is_symlink():
                copy_link(child, copied_child)
            else:
                shutil.copyfile(child, copied_child)


def _trace_mentions(capture_info, session_id, prompt_id):
    trace = capture_info.get("trace")
    if trace is None or not trace.is_file() or not session_id or not prompt_id:
        return False
    try:
        content = trace.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return False
    session_literal = json.dumps(session_id, ensure_ascii=False)
    prompt_literal = json.dumps(prompt_id, ensure_ascii=False)
    return session_literal in content and prompt_literal in content


def _fallback_trace(captures, session_id, prompt_id):
    def created_key(info):
        created_at = info["raw"].get("createdAt")
        numeric = created_at if isinstance(created_at, (int, float)) and not isinstance(created_at, bool) else -1
        return numeric, info["sourceIndex"]

    for capture_info in sorted(captures, key=created_key, reverse=True):
        if _trace_mentions(capture_info, session_id, prompt_id):
            return capture_info
    return None


def _portable_evidence_reference(value, review_attachment):
    text = _as_text(value)
    if not review_attachment or not text:
        return text
    normalized = text.replace("\\", "/")
    for subtree in ("verification/", "evidence/", "initial/", "input.json", "evaluation.json", "evaluation-schema.json", "evaluator.log"):
        marker = "/" + subtree
        if normalized.startswith(subtree):
            suffix = normalized
            break
        if marker in normalized:
            suffix = normalized.split(marker, 1)[1]
            suffix = subtree.rstrip("/") + "/" + suffix if subtree.endswith("/") else subtree + suffix
            break
    else:
        return text
    return (Path(review_attachment) / suffix).as_posix()


def _inline_cell(row, column, value, style):
    reference = _column_name(column + 1) + str(row)
    cell = ET.Element(NS + "c", {"r": reference, "s": str(style), "t": "inlineStr"})
    inline = ET.SubElement(cell, NS + "is")
    text = ET.SubElement(inline, NS + "t")
    if value[:1].isspace() or value[-1:].isspace() or "\n" in value or "\r" in value:
        text.set(XML_SPACE, "preserve")
    text.text = value
    return cell


def _numeric_cell(row, column, value, style):
    reference = _column_name(column + 1) + str(row)
    cell = ET.Element(NS + "c", {"r": reference, "s": str(style), "t": "n"})
    if value is not None:
        ET.SubElement(cell, NS + "v").text = str(value)
    return cell


def _column_name(number):
    result = ""
    while number:
        number, remainder = divmod(number - 1, 26)
        result = chr(65 + remainder) + result
    return result


def _build_workbook(rows, destination):
    if not TEMPLATE.is_file():
        raise ExportFailure(f"bundled template is missing: {TEMPLATE}")
    with ZipFile(TEMPLATE, "r") as source:
        entries = [(item.filename, source.read(item.filename)) for item in source.infolist()]
    replacement = None
    for filename, data in entries:
        if filename == SHEET_PATH:
            root = ET.fromstring(data)
            sheet_data = root.find(NS + "sheetData")
            header = sheet_data.find(NS + "row")
            for existing in list(sheet_data):
                if existing is not header:
                    sheet_data.remove(existing)
            for row_number, values in enumerate(rows, 2):
                row = ET.SubElement(sheet_data, NS + "row", {
                    "r": str(row_number), "ht": "90", "customHeight": "1",
                })
                for column, value in enumerate(values):
                    style = 7 if column in ENUM_COLUMNS else 6
                    if column in SCORE_COLUMNS:
                        row.append(_numeric_cell(row_number, column, value, style))
                    elif value is None or value == "":
                        row.append(_numeric_cell(row_number, column, None, style))
                    else:
                        row.append(_inline_cell(row_number, column, str(value), style))
            last_row = max(1, len(rows) + 1)
            root.find(NS + "dimension").set("ref", "A1:AB" + str(last_row))
            auto_filter = root.find(NS + "autoFilter")
            if auto_filter is not None:
                auto_filter.set("ref", "A1:AB" + str(last_row))
            validation_end = max(100, last_row)
            validations = root.find(NS + "dataValidations")
            if validations is not None:
                for validation in validations.findall(NS + "dataValidation"):
                    reference = validation.get("sqref", "")
                    match = re.fullmatch(r"\$?([A-Z]+)\$?2:\$?\1\$?\d+", reference)
                    if match:
                        column = match.group(1)
                        validation.set("sqref", f"{column}2:{column}{validation_end}")
                        if column == "J":
                            formula = validation.find(NS + "formula1")
                            if formula is not None:
                                formula.text = '"Bug修复,0-1代码生成,Feature迭代,feature迭代,代码理解,代码重构,工程化,代码测试"'
            replacement = ET.tostring(root, encoding="utf-8", xml_declaration=True)
            break
    if replacement is None:
        raise ExportFailure("bundled template has no worksheet")
    destination.parent.mkdir(parents=True, exist_ok=True)
    with ZipFile(destination, "w", ZIP_DEFLATED, compresslevel=9) as target:
        for filename, data in entries:
            info = ZipInfo(filename, FIXED_ZIP_TIME)
            info.compress_type = ZIP_DEFLATED
            info.external_attr = 0o600 << 16
            target.writestr(info, replacement if filename == SHEET_PATH else data)


def _flatten_issues(evaluation):
    if not evaluation or not isinstance(evaluation.get("issues"), list):
        return ""
    descriptions = []
    for item in evaluation["issues"]:
        if isinstance(item, dict) and _as_text(item.get("description")).strip():
            descriptions.append(item["description"].strip())
    return "；".join(descriptions)


def _row_values(batch, case, round_item, evaluation, trace_relative, draft, overlong_prompt):
    has_evaluation = bool(evaluation)
    raw_scores = evaluation.get("scores", []) if has_evaluation else []
    raw_descriptions = evaluation.get("descriptions", []) if has_evaluation else []
    scores = [
        score if isinstance(score, int) and not isinstance(score, bool) and 3 <= score <= 5 else None
        for score in raw_scores[:5]
    ]
    scores.extend([None] * (5 - len(scores)))
    descriptions = [value if isinstance(value, str) else "" for value in raw_descriptions[:5]]
    descriptions.extend([""] * (5 - len(descriptions)))
    harness_version = _as_text(evaluation.get("harnessVersion")) if has_evaluation else ""
    snapshot = _as_text(case.get("snapshotUrl"))
    values = [
        "" if overlong_prompt else _as_text(round_item.get("prompt")),
        _as_text(round_item.get("sessionId")),
        _as_text(round_item.get("promptId")),
        snapshot,
        trace_relative or "",
        _as_text(evaluation.get("environment")) if has_evaluation else "",
        "Claude Code" if harness_version else "",
        harness_version,
        _as_text(evaluation.get("os")) if has_evaluation else "",
        _as_text(case.get("taskType")) or (_as_text(evaluation.get("taskType")) if has_evaluation else ""),
        _as_text(evaluation.get("difficulty")) if has_evaluation else "",
        _as_text(evaluation.get("language")) if has_evaluation else "",
        scores[0],
        descriptions[0],
        scores[1],
        descriptions[1],
        scores[2],
        descriptions[2],
        scores[3],
        descriptions[3],
        scores[4],
        descriptions[4],
        _flatten_issues(evaluation) if has_evaluation else "",
        _as_text(batch.get("submitter")),
        _as_text(batch.get("submittedAt")),
        "待审核" if has_evaluation else "",
        "",
        "",
    ]
    return values


def _evidence_markdown(batch, manifest_rounds):
    lines = [
        "# Evidence index",
        "",
        "This file indexes AI-generated evaluations. It does not represent human annotation, external approval, or independent verification by the exporter.",
        "",
        "Project: " + _as_text(batch.get("projectName")),
        "",
    ]
    for item in manifest_rounds:
        if not item["included"]:
            continue
        lines.extend([
            f"## {item['taskId']} / {item['sessionId']} / {item['promptId']}",
            "",
            f"- Round status supplied by the parser: `{item['status']}`",
            f"- Evidence hash: `{item['evidenceHash']}`",
            f"- Evaluation ID: `{item.get('evaluationId', '')}`",
            f"- Evaluation model recorded by the caller: `{item.get('evaluationModel', '')}`",
            f"- Skill hash recorded by the caller: `{item.get('skillHash', '')}`",
            f"- Copied trace: `{item.get('tracePath', '')}`",
            f"- Copied AI review evidence: `{item.get('reviewAttachment', '')}`",
        ])
        evidence = item.get("evidence", [])
        missing = item.get("missing", [])
        evaluation_issues = item.get("evaluationIssues", [])
        portable_evidence = [
            _portable_evidence_reference(value, item.get("reviewAttachment", ""))
            for value in evidence
        ]
        lines.append("- Evidence references supplied to the evaluation (mapped into the copied review directory when path-like): " + ("; ".join(_one_line(value) for value in portable_evidence) if portable_evidence else "none supplied"))
        lines.append("- Missing evidence recorded by the evaluation: " + ("; ".join(_one_line(value) for value in missing) if missing else "none recorded"))
        if evaluation_issues:
            lines.append("- Issues recorded by the AI evaluation:")
            for issue in evaluation_issues:
                lines.append("  - " + _one_line(json.dumps(issue, ensure_ascii=False, sort_keys=True)))
        else:
            lines.append("- Issues recorded by the AI evaluation: none recorded")
        lines.extend(["- Independent verification performed by this exporter: none", ""])
    return "\n".join(lines).rstrip() + "\n"


def _one_line(value):
    return str(value).replace("\r", "\\r").replace("\n", "\\n")


def _report_markdown(batch, draft, rows, issues, checker=None):
    lines = [
        "# Satisfaction export report",
        "",
        f"- Project: {_one_line(_as_text(batch.get('projectName')))}",
        f"- Mode: {'draft' if draft else 'formal'}",
        f"- Exported rows: {rows}",
        f"- AI evaluation attribution: recorded in evidence.md",
        f"- External approval: not checked",
        "",
        "## Issues",
        "",
    ]
    if issues:
        lines.extend("- " + _one_line(item) for item in issues)
    else:
        lines.append("- None found by structural checks")
    lines.extend(["", "## Checker scope", ""])
    if checker is None:
        lines.extend([
            "- Automated syntax/structure: NOT RUN",
            "- Semantic evaluation: NOT CHECKED",
            "",
            "The workbook checker did not run because formal preflight did not produce a workbook.",
        ])
    else:
        lines.extend([
            f"- Automated syntax/structure: {checker['field_status']}",
            "- Semantic evaluation: NOT CHECKED",
            f"- Rows read back: {checker['rows_checked']}",
            f"- Round identity check: {checker['round_identity_check']}",
            "- Validation ranges: " + json.dumps(checker.get("validation_ranges", {}), ensure_ascii=False, sort_keys=True),
            "- Semantic checks left explicit for review:",
        ])
        lines.extend("  - " + item for item in checker.get("not_checked", []))
    return "\n".join(lines).rstrip() + "\n"


def export_batch(batch, input_dir, input_hash, output, draft):
    if not isinstance(batch, dict):
        raise ExportFailure("input root must be a JSON object")
    cases = batch.get("cases")
    if not isinstance(cases, list):
        raise ExportFailure("input cases must be an array")
    top_issues = batch.get("issues", [])
    if not isinstance(top_issues, list):
        raise ExportFailure("input issues must be an array")

    blockers = [_issue("batch", _one_line(item)) for item in top_issues]
    manifest_cases = []
    manifest_rounds = []
    prepared_rows = []
    capture_sources = {}
    review_sources = {}
    seen_rounds = {}

    for case_index, case in sorted(enumerate(cases), key=_case_sort_key):
        if not isinstance(case, dict):
            raise ExportFailure("each case must be an object")
        task_id = _as_text(case.get("taskId"))
        project_id = _as_text(case.get("projectId"))
        if not task_id:
            blockers.append(_issue(f"case {case_index + 1}", "taskId is missing"))
        task_component = _opaque_component("task", project_id, task_id, str(case_index))
        scope = "case " + task_id
        initial_sha = _as_text(case.get("initialSha"))
        snapshot_url = _as_text(case.get("snapshotUrl"))
        snapshot_match = SNAPSHOT.fullmatch(snapshot_url)
        if not FULL_SHA.fullmatch(initial_sha):
            blockers.append(_issue(scope, "initialSha must be a full 40-character SHA"))
        if not snapshot_match:
            blockers.append(_issue(scope, "snapshotUrl must be a GitHub commit permalink with a full SHA"))
        elif initial_sha and snapshot_match.group(1).lower() != initial_sha.lower():
            blockers.append(_issue(scope, "snapshotUrl SHA does not match initialSha"))
        if case.get("completed") is not True:
            blockers.append(_issue(scope, "case is not completed"))

        captures = case.get("captures")
        rounds = case.get("rounds")
        if not isinstance(captures, list) or not isinstance(rounds, list):
            raise ExportFailure(scope + " must contain captures and rounds arrays")
        case_capture_ids = set()
        capture_manifest = []
        case_capture_infos = []
        for capture_index, capture in enumerate(captures):
            if not isinstance(capture, dict):
                raise ExportFailure(scope + " contains a non-object capture")
            capture_id = _as_text(capture.get("id"))
            if not capture_id:
                blockers.append(_issue(scope + f" capture {capture_index + 1}", "captureId is missing"))
            if capture_id in case_capture_ids:
                raise ExportFailure(scope + " has duplicate captureId " + capture_id)
            case_capture_ids.add(capture_id)
            capture_component = _opaque_component("capture", project_id, task_id, capture_id, str(capture_index))
            source = _source_path(capture.get("dir"), input_dir)
            if source is None or not source.is_dir():
                blockers.append(_issue(scope + " capture " + capture_id, "capture directory is missing"))
            _scan_capture(source, output)
            if source is None:
                trace, trace_member = None, None
                code, code_member = None, None
            else:
                trace, trace_member = _resolve_member(source, capture.get("tracePath"), "tracePath")
                code, code_member = _resolve_member(source, capture.get("codePath"), "codePath")
            destination_relative = Path("attachments") / task_component / capture_component
            capture_info = {
                "source": source,
                "destination": output / destination_relative,
                "destinationRelative": destination_relative,
                "trace": trace,
                "traceMember": trace_member,
                "code": code,
                "codeMember": code_member,
                "raw": capture,
                "rawID": capture_id,
                "sourceIndex": capture_index,
            }
            capture_sources[(task_id, capture_id)] = capture_info
            case_capture_infos.append(capture_info)
            capture_manifest.append({
                "id": capture_id,
                "hash": capture.get("hash", ""),
                "createdAt": capture.get("createdAt"),
                "attachmentRoot": destination_relative.as_posix(),
                "tracePath": trace_member.as_posix() if trace_member else "",
                "codePath": code_member.as_posix() if code_member else "",
            })
        manifest_cases.append({
            "taskId": task_id,
            "projectId": project_id,
            "taskName": case.get("taskName", ""),
            "initialSha": initial_sha,
            "snapshotUrl": snapshot_url,
            "completed": case.get("completed") is True,
            "captures": capture_manifest,
        })

        for original_index, round_item in sorted(enumerate(rounds), key=_round_sort_key):
            if not isinstance(round_item, dict):
                raise ExportFailure(scope + " contains a non-object round")
            round_scope = f"{scope} round {original_index + 1}"
            session_id = _as_text(round_item.get("sessionId"))
            prompt_id = _as_text(round_item.get("promptId"))
            prompt = round_item.get("prompt")
            status = round_item.get("status")
            reason = _as_text(round_item.get("reason"))
            included = status != "excluded"
            if status not in ROUND_STATUSES:
                blockers.append(_issue(round_scope, "unknown round status"))
            if not included and not reason.strip():
                blockers.append(_issue(round_scope, "excluded round requires an evidence-based reason"))
            if included and status != "complete":
                blockers.append(_issue(round_scope, f"round status is {status!r}, not complete"))
            if not session_id:
                blockers.append(_issue(round_scope, "sessionId is missing"))
            if not prompt_id:
                blockers.append(_issue(round_scope, "promptId is missing"))
            if not isinstance(prompt, str) or not prompt:
                blockers.append(_issue(round_scope, "original prompt text is missing"))
                prompt = "" if not isinstance(prompt, str) else prompt
            identity = (session_id, prompt_id)
            if all(identity):
                if identity in seen_rounds:
                    blockers.append(_issue(round_scope, "duplicate SessionID + PromptID; first at " + seen_rounds[identity]))
                else:
                    seen_rounds[identity] = round_scope

            evaluation = _matching_evaluation(round_item)
            if evaluation and case.get("taskType"):
                evaluation = dict(evaluation, taskType=case["taskType"])
            if included:
                blockers.extend(_validate_evaluation(evaluation, round_scope))
            score_total = _evaluation_score_total(evaluation)
            if included and evaluation and evaluation.get("status") == "ready" and score_total is not None and score_total > 21:
                included = False
                reason = f"五维评分总分 {score_total} 超过 21，不符合平台收录规则；评分按真实结果保留"
            review_attachment = ""
            if included and evaluation:
                review_source = _source_path(evaluation.get("reviewPath"), input_dir)
                if review_source is None or not review_source.is_dir():
                    label = "ready evaluation review evidence directory is missing" if evaluation.get("status") == "ready" else "evaluation review evidence directory is missing"
                    blockers.append(_issue(round_scope, label))
                else:
                    _scan_capture(review_source, output)
                    evaluation_component = _opaque_component(
                        "evaluation",
                        evaluation.get("id"),
                        round_item.get("evidenceHash"),
                    )
                    review_relative = Path("attachments") / task_component / "reviews" / evaluation_component
                    review_attachment = review_relative.as_posix()
                    review_sources[review_attachment] = {
                        "source": review_source,
                        "destination": output / review_relative,
                    }
            capture_id = _as_text(round_item.get("captureId"))
            exact_capture = capture_sources.get((task_id, capture_id)) if capture_id else None
            capture_info = exact_capture
            if capture_info is None or capture_info.get("trace") is None or not capture_info["trace"].is_file():
                capture_info = _fallback_trace(case_capture_infos, session_id, prompt_id)
            trace_relative = ""
            trace_capture_id = ""
            if included:
                if capture_info is None:
                    blockers.append(_issue(round_scope, "trace attachment is missing and no capture raw trace contains the round identity"))
                else:
                    trace = capture_info["trace"]
                    if trace is None or not trace.is_file():
                        blockers.append(_issue(round_scope, "trace attachment is missing"))
                    else:
                        trace_relative = (capture_info["destinationRelative"] / capture_info["traceMember"]).as_posix()
                        trace_capture_id = capture_info["rawID"]
                    code = exact_capture["code"] if exact_capture is not None else None
                    if exact_capture is not None and code is not None and not code.exists():
                        blockers.append(_issue(round_scope, "captured codePath is missing"))

            overlong_prompt = len(prompt) > 32767
            prompt_attachment = ""
            if included and overlong_prompt:
                blockers.append(_issue(round_scope, "original prompt exceeds Excel's 32767-character limit"))
                round_component = _opaque_component("round", session_id, prompt_id, str(original_index))
                prompt_attachment = (Path("attachments") / task_component / "original-prompts" / f"{round_component}.txt").as_posix()

            manifest_item = {
                "taskId": task_id,
                "projectId": project_id,
                "taskName": case.get("taskName", ""),
                "sessionId": session_id,
                "promptId": prompt_id,
                "prompt": prompt,
                "attachments": round_item.get("attachments", []),
                "order": round_item.get("order"),
                "status": status,
                "reason": reason,
                "included": included,
                "valid": included,
                "scoreTotal": score_total,
                "evidenceHash": round_item.get("evidenceHash", ""),
                "roundVersion": round_item.get("version", ""),
                "cwd": round_item.get("cwd", ""),
                "captureId": capture_id,
                "traceCaptureId": trace_capture_id,
                "tracePath": trace_relative,
                "reviewAttachment": review_attachment,
                "reviewHash": evaluation.get("reviewHash", "") if evaluation else "",
                "promptAttachment": prompt_attachment,
                "evaluationId": evaluation.get("id", "") if evaluation else "",
                "evaluationStatus": evaluation.get("status", "") if evaluation else "",
                "evaluationModel": evaluation.get("model", "") if evaluation else "",
                "skillHash": evaluation.get("skillHash", "") if evaluation else "",
                "evidence": evaluation.get("evidence", []) if evaluation else [],
                "missing": evaluation.get("missing", []) if evaluation else [],
                "evaluationIssues": evaluation.get("issues", []) if evaluation else [],
            }
            manifest_rounds.append(manifest_item)
            if included:
                prepared_rows.append({
                    "case": case,
                    "round": round_item,
                    "evaluation": evaluation,
                    "traceRelative": trace_relative,
                    "overlongPrompt": overlong_prompt,
                    "promptAttachment": prompt_attachment,
                    "prompt": prompt,
                })

    output.mkdir(parents=True, exist_ok=True)
    manifest = {
        "schemaVersion": 1,
        "projectName": batch.get("projectName", ""),
        "submitter": batch.get("submitter", ""),
        "submittedAt": batch.get("submittedAt", ""),
        "draft": draft,
        "inputSha256": input_hash,
        "inputIssues": top_issues,
        "cases": manifest_cases,
        "rounds": manifest_rounds,
    }
    manifest_path = output / "manifest.json"
    report_path = output / "report.md"
    _write_json(manifest_path, manifest)
    (output / "evidence.md").write_text(_evidence_markdown(batch, manifest_rounds), encoding="utf-8")

    if blockers and not draft:
        report_path.write_text(_report_markdown(batch, draft, 0, blockers), encoding="utf-8")
        return {"outputPath": "", "reportPath": str(report_path), "rows": 0, "issues": blockers}, 2

    for capture_info in capture_sources.values():
        source = capture_info["source"]
        if source is not None and source.is_dir():
            _copy_capture(source, capture_info["destination"])
    for review_info in review_sources.values():
        _copy_capture(review_info["source"], review_info["destination"])
    for item in prepared_rows:
        if item["overlongPrompt"]:
            destination = output / item["promptAttachment"]
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_text(item["prompt"], encoding="utf-8")

    workbook_path = output / "submission.xlsx"
    values = []
    previous_task = None
    for item in prepared_rows:
        task_identity = (item["case"].get("projectId"), item["case"].get("taskId"))
        if batch.get("separateTasks") and previous_task is not None and previous_task != task_identity:
            values.append([None] * 28)
        values.append(_row_values(batch, item["case"], item["round"], item["evaluation"] or {}, str((output / item["traceRelative"]).resolve()) if item["traceRelative"] else "", draft, item["overlongPrompt"]))
        previous_task = task_identity
    _build_workbook(values, workbook_path)
    checker = inspect_submission(workbook_path, manifest, allow_draft=draft)
    checker_issues = [
        _issue(entry.get("location", "checker"), entry.get("issue", "unknown checker error"))
        for entry in checker.get("errors", [])
    ]
    all_issues = blockers + checker_issues
    if checker_issues and not draft:
        workbook_path.unlink(missing_ok=True)
        report_path.write_text(_report_markdown(batch, draft, 0, all_issues, checker), encoding="utf-8")
        return {"outputPath": "", "reportPath": str(report_path), "rows": 0, "issues": all_issues}, 2
    report_path.write_text(_report_markdown(batch, draft, len(prepared_rows), all_issues, checker), encoding="utf-8")
    return {
        "outputPath": str(workbook_path),
        "reportPath": str(report_path),
        "rows": len(prepared_rows),
        "issues": all_issues,
    }, 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--draft", action="store_true")
    args = parser.parse_args()
    result = {"outputPath": "", "reportPath": "", "rows": 0, "issues": []}
    exit_code = 1
    try:
        raw = args.input.read_bytes()
        batch = json.loads(raw.decode("utf-8"))
        result, exit_code = export_batch(
            batch,
            args.input.parent.resolve(),
            hashlib.sha256(raw).hexdigest(),
            args.output.resolve(),
            args.draft,
        )
    except Exception as exc:
        result["issues"] = [f"{type(exc).__name__}: {exc}"]
    print(json.dumps(result, ensure_ascii=False, separators=(",", ":")))
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
