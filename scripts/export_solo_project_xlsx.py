#!/usr/bin/env python3
import argparse
import json
import re
import sqlite3
import zipfile
from datetime import datetime
from pathlib import Path
from typing import Optional
from xml.etree import ElementTree as ET


MAIN_NS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
NS = {"a": MAIN_NS}
ET.register_namespace("", MAIN_NS)

SESSION_TIME_RE = re.compile(r"T\(([^)]+)\)")
CLAIM_SEQ_RE = re.compile(r"label-\d+-(\d+)$")
TRAILING_NUMBER_RE = re.compile(r"-(\d+)$")


def claim_seq(task_id: str) -> int:
    match = CLAIM_SEQ_RE.search(task_id or "")
    return int(match.group(1)) if match else 0


def trailing_number(value: str) -> int:
    match = TRAILING_NUMBER_RE.search(value or "")
    return int(match.group(1)) if match else 0


def parse_session_time(session_id: str) -> Optional[datetime]:
    match = SESSION_TIME_RE.search(session_id or "")
    if not match:
        return None
    try:
        return datetime.strptime(match.group(1), "%Y/%m/%d %H:%M:%S")
    except ValueError:
        return None


def excel_date(value: datetime) -> str:
    return f"{value.year}/{value.month}/{value.day}"


def bool_text(value, true_text: str, false_text: str) -> str:
    if value is None:
        return ""
    return true_text if int(value) else false_text


def review_bool(review: Optional[sqlite3.Row], field: str) -> Optional[int]:
    if review is None:
        return None
    value = review[field]
    if value is not None:
        return int(value)
    status = (review["status"] or "").lower()
    if status == "pass":
        return 1
    if status in ("warning", "failed"):
        return 0
    return None


def effective_task_type(prompt_text: str, fallback: str) -> str:
    prompt = (prompt_text or "").strip()
    if prompt.startswith("修复"):
        return "Bug修复"
    return fallback


def task_type_for_excel(value: str) -> str:
    return "0-1代码生成" if value == "代码生成" else value


def col_name(index: int) -> str:
    name = ""
    while index:
        index, rem = divmod(index - 1, 26)
        name = chr(65 + rem) + name
    return name


def col_index(cell_ref: str) -> int:
    letters = "".join(ch for ch in cell_ref if ch.isalpha())
    result = 0
    for char in letters:
        result = result * 26 + ord(char.upper()) - 64
    return result


def read_shared_strings(source: zipfile.ZipFile) -> list[str]:
    if "xl/sharedStrings.xml" not in source.namelist():
        return []
    root = ET.fromstring(source.read("xl/sharedStrings.xml"))
    return [
        "".join(text.text or "" for text in item.findall(".//a:t", NS))
        for item in root.findall("a:si", NS)
    ]


def cell_text(cell: ET.Element, shared_strings: list[str]) -> str:
    value = cell.find("a:v", NS)
    if cell.attrib.get("t") == "s" and value is not None:
        return shared_strings[int(value.text or "0")]
    if cell.attrib.get("t") == "inlineStr":
        return "".join(text.text or "" for text in cell.findall(".//a:t", NS))
    return value.text if value is not None else ""


def read_headers(path: Path) -> list[str]:
    with zipfile.ZipFile(path, "r") as source:
        shared_strings = read_shared_strings(source)
        root = ET.fromstring(source.read("xl/worksheets/sheet1.xml"))
        first_row = root.find("a:sheetData/a:row", NS)
        if first_row is None:
            raise RuntimeError("template has no header row")
        cells = []
        for cell in first_row.findall("a:c", NS):
            cells.append((col_index(cell.attrib.get("r", "")), cell_text(cell, shared_strings)))
        max_col = max(index for index, _ in cells)
        headers = [""] * max_col
        for index, text in cells:
            headers[index - 1] = text.strip()
        return headers


def make_cell(row_num: int, col_num: int, value, style: Optional[str] = None) -> Optional[ET.Element]:
    if value is None or value == "":
        return None
    cell = ET.Element(f"{{{MAIN_NS}}}c", {"r": f"{col_name(col_num)}{row_num}"})
    if style:
        cell.attrib["s"] = style
    cell.attrib["t"] = "inlineStr"
    inline = ET.SubElement(cell, f"{{{MAIN_NS}}}is")
    text = ET.SubElement(inline, f"{{{MAIN_NS}}}t")
    text.text = str(value)
    if str(value).strip() != str(value) or "\n" in str(value):
        text.attrib["{http://www.w3.org/XML/1998/namespace}space"] = "preserve"
    return cell


def write_xlsx(template: Path, output: Path, headers: list[str], rows: list[dict[str, str]]) -> None:
    with zipfile.ZipFile(template, "r") as source:
        root = ET.fromstring(source.read("xl/worksheets/sheet1.xml"))
        sheet_data = root.find("a:sheetData", NS)
        if sheet_data is None:
            raise RuntimeError("sheetData not found")
        existing_rows = sheet_data.findall("a:row", NS)
        for row in existing_rows[1:]:
            sheet_data.remove(row)

        for offset, item in enumerate(rows, start=2):
            row_el = ET.Element(f"{{{MAIN_NS}}}row", {"r": str(offset)})
            for col_num, header in enumerate(headers, start=1):
                cell = make_cell(offset, col_num, item.get(header), "5")
                if cell is not None:
                    row_el.append(cell)
            sheet_data.append(row_el)

        dimension = root.find("a:dimension", NS)
        if dimension is not None:
            dimension.attrib["ref"] = f"A1:{col_name(len(headers))}{len(rows) + 1}"

        rendered = ET.tostring(root, encoding="utf-8", xml_declaration=True)
        output.parent.mkdir(parents=True, exist_ok=True)
        with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED) as target:
            for item in source.infolist():
                data = rendered if item.filename == "xl/worksheets/sheet1.xml" else source.read(item.filename)
                target.writestr(item, data)


def load_rows(
    db_path: Path,
    project_name: str,
) -> tuple[list[dict[str, str]], list[dict[str, str]], dict[str, int]]:
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row

    reviews: dict[tuple[str, str], list[sqlite3.Row]] = {}
    for review in conn.execute(
        """
        select
          r.*,
          t.project_type as task_project_type,
          t.change_scope as task_change_scope,
          t.prompt_difficulty as task_prompt_difficulty
        from ai_review_rounds r
        join tasks t on t.id = r.task_id
        where t.project_name = ?
          and coalesce(r.status, '') not in ('', 'none', 'running')
        order by r.task_id, r.model_run_id, r.round_number
        """,
        (project_name,),
    ):
        reviews.setdefault((review["task_id"], review["model_run_id"]), []).append(review)

    code_push_by_session: dict[str, sqlite3.Row] = {}
    code_push_by_key: dict[tuple[str, str, int], sqlite3.Row] = {}
    repo_url_by_name: dict[str, str] = {}
    for record in conn.execute(
        """
        select *
        from code_push_records
        where task_id in (select id from tasks where project_name = ?)
        order by updated_at desc, created_at desc
        """,
        (project_name,),
    ):
        session_id = (record["session_id"] or "").strip()
        if session_id:
            code_push_by_session.setdefault(session_id, record)
        code_push_by_key.setdefault(
            (record["task_id"], record["model_run_id"], int(record["session_index"])),
            record,
        )
        repo_name = (record["repo_name"] or "").strip()
        repo_url = (record["repo_url"] or "").strip()
        if repo_name and repo_url:
            repo_url_by_name.setdefault(repo_name, repo_url)

    github_username = ""
    account = conn.execute(
        "select username from github_accounts order by is_default desc, updated_at desc limit 1"
    ).fetchone()
    if account:
        github_username = (account["username"] or "").strip()

    rows: list[dict[str, str]] = []
    validation: list[dict[str, str]] = []
    for run in conn.execute(
        """
        select
          t.id as task_id,
          t.gitlab_project_id,
          t.project_name,
          t.task_type,
          t.project_type,
          t.change_scope,
          t.prompt_difficulty,
          mr.id as model_run_id,
          mr.session_list
        from model_runs mr
        join tasks t on t.id = mr.task_id
        where t.project_name = ?
          and coalesce(mr.session_list, '') <> ''
          and mr.session_list <> '[]'
        order by t.created_at, t.id
        """,
        (project_name,),
    ):
        try:
            sessions = json.loads(run["session_list"] or "[]")
        except json.JSONDecodeError:
            continue

        effective_sessions = []
        for source_index, session in enumerate(sessions):
            session_id = (session.get("sessionId") or "").strip()
            session_time = parse_session_time(session_id)
            if not session_id or session_time is None:
                continue
            effective_sessions.append((source_index, session, session_time))

        task_reviews = reviews.get((run["task_id"], run["model_run_id"]), [])
        seq = claim_seq(run["task_id"])
        repo_id = f"{project_name}-{seq}" if seq else project_name
        for round_index, (source_index, session, session_time) in enumerate(effective_sessions, start=1):
            review = task_reviews[source_index] if source_index < len(task_reviews) else None
            session_id = (session.get("sessionId") or "").strip()
            user_prompt = (
                ((review["prompt_text"] or "").strip() if review else "")
                or (session.get("userConversation") or "").strip()
            )
            raw_task_type = (session.get("taskType") or run["task_type"] or "").strip()
            task_type = task_type_for_excel(effective_task_type(user_prompt, raw_task_type))
            is_completed = review_bool(review, "is_completed")
            if is_completed is None and session.get("isCompleted") is not None:
                is_completed = 1 if session.get("isCompleted") else 0
            is_satisfied = review_bool(review, "is_satisfied")
            if is_satisfied is None and session.get("isSatisfied") is not None:
                is_satisfied = 1 if session.get("isSatisfied") else 0
            satisfied = bool_text(is_satisfied, "满意", "不满意")
            review_notes = ""
            if review is not None:
                review_notes = ((review["dissatisfaction_summary"] or "").strip() or (review["review_notes"] or "").strip())
            if satisfied == "不满意" and (session.get("evaluation") or "").strip() and not (review and review["dissatisfaction_summary"]):
                review_notes = (session.get("evaluation") or "").strip()

            project_type = ((review["project_type"] or "").strip() if review else "") or (run["project_type"] or "").strip()
            change_scope = ((review["change_scope"] or "").strip() if review else "") or (run["change_scope"] or "").strip()
            prompt_difficulty = (run["prompt_difficulty"] or "").strip()

            code_push = code_push_by_key.get((run["task_id"], run["model_run_id"], source_index))
            if code_push is None:
                code_push = code_push_by_session.get(session_id)
            repo_name = (code_push["repo_name"] or "").strip() if code_push else ""
            repo_url = (code_push["repo_url"] or "").strip() if code_push else ""
            repo_url_source = "code_push_records"
            if not repo_url and repo_name:
                repo_url = repo_url_by_name.get(repo_name, "")
                repo_url_source = "same repo_name fill"
            if not repo_url and github_username and repo_name:
                repo_url = f"https://github.com/{github_username}/{repo_name}"
                repo_url_source = "github account fallback"
            commit_id = (code_push["commit_sha"] or "").strip() if code_push else ""

            validation_row = {
                "row": "",
                "sort_key": (seq, source_index, session_time.strftime("%Y-%m-%d %H:%M:%S"), session_id),
                "task_id": run["task_id"],
                "model_run_id": run["model_run_id"],
                "session_index": str(source_index),
                "session_id": session_id,
                "repo_id": repo_id,
                "repo_name": repo_name,
                "repo_url": repo_url,
                "repo_url_source": repo_url_source,
                "commit_id": commit_id,
                "db_commit": (code_push["commit_sha"] or "").strip() if code_push else "",
                "db_repo_url": (code_push["repo_url"] or "").strip() if code_push else "",
                "db_status": (code_push["status"] or "").strip() if code_push else "missing",
            }

            rows.append(
                {
                    "_sort_key": (seq, source_index, session_time.strftime("%Y-%m-%d %H:%M:%S"), session_id),
                    "编码": str(len(rows) + 1),
                    "Repo ID": repo_id,
                    "做题人": "",
                    "提交日期": excel_date(session_time),
                    "Trae Session ID": session_id,
                    "User Prompt": user_prompt,
                    "RepoURL": repo_url,
                    "CommitId": commit_id,
                    "任务类型": task_type,
                    "业务领域": project_type,
                    "修改范围": change_scope,
                    "任务难度": prompt_difficulty,
                    "任务是否完成": bool_text(is_completed, "完成了任务", "未完成任务"),
                    "过程与产物是否满意": satisfied,
                    "不满意原因": review_notes if satisfied == "不满意" else "",
                    "单选": "",
                    "质检状态": "",
                    "质检备注": "",
                    "是否提交字节": "",
                }
            )
            validation_row["row"] = str(len(rows))
            validation.append(validation_row)

    conn.close()
    rows.sort(key=lambda item: item["_sort_key"])
    for index, row in enumerate(rows, start=1):
        row["编码"] = str(index)
    validation.sort(key=lambda item: item["sort_key"])
    for index, item in enumerate(validation, start=1):
        item["row"] = str(index)

    duplicate_sessions = len(rows) - len({row["Trae Session ID"] for row in rows})
    missing_pr_records = sum(1 for item in validation if item["db_status"] == "missing")
    stats = {
        "rows": len(rows),
        "validation_rows": len(validation),
        "duplicate_sessions": duplicate_sessions,
        "empty_repo_url": sum(1 for row in rows if not row["RepoURL"]),
        "empty_commit": sum(1 for row in rows if not row["CommitId"]),
        "missing_pr_records": missing_pr_records,
        "repo_url_filled": sum(
            1
            for item in validation
            if item["repo_url_source"] != "code_push_records"
        ),
    }
    return rows, validation, stats


def write_report(
    project_name: str,
    template: Path,
    output: Path,
    report_output: Path,
    validation: list[dict[str, str]],
    stats: dict[str, int],
) -> None:
    duplicate_ids = sorted(
        session_id
        for session_id in {item["session_id"] for item in validation if not item.get("filtered")}
        if sum(1 for item in validation if not item.get("filtered") and item["session_id"] == session_id) > 1
    )
    issues = []
    filled = []
    missing_commit_rows = []
    missing_pr_rows = []
    for item in validation:
        row_issues = []
        if not item["commit_id"]:
            row_issues.append("CommitId为空")
            missing_commit_rows.append(item)
        if item["db_status"] == "missing":
            row_issues.append("PR数据库缺记录")
            missing_pr_rows.append(item)
        if item["db_commit"] and item["commit_id"] != item["db_commit"]:
            row_issues.append("CommitId与PR数据库不一致")
        if not item["repo_url"]:
            row_issues.append("RepoURL/ReportURL为空")
        if item["repo_url_source"] != "code_push_records":
            filled.append(item)
        if row_issues:
            issue = dict(item)
            issue["issue"] = "；".join(row_issues)
            issues.append(issue)

    lines = [
        f"# {project_name} XLSX 导出校验",
        "",
        f"- XLSX：`{output.resolve()}`",
        f"- 模板：`{template}`",
        "- 取数口径：`/Users/chengzhiqiang/pinru-session-export`（model_runs.session_list + ai_review_rounds + code_push_records）",
        f"- Repo ID 格式：`{project_name}-1` / `{project_name}-2` ...",
        f"- 导出行数：{stats['rows']}",
        f"- 校验覆盖 session 行数：{stats['validation_rows']}",
        f"- sessionId 重复数：{stats['duplicate_sessions']}",
        f"- RepoURL/ReportURL 为空行数：{stats['empty_repo_url']}",
        f"- RepoURL/ReportURL 补齐行数：{stats['repo_url_filled']}",
        f"- CommitId 为空导出行数：{stats['empty_commit']}",
        f"- PR数据库缺记录行数：{stats['missing_pr_records']}",
        f"- PR数据库校验需关注行数：{len(issues)}",
        "",
    ]
    if duplicate_ids:
        lines.extend(["## 重复 sessionId", *[f"- `{item}`" for item in duplicate_ids], ""])
    if filled:
        lines.extend(
            [
                "## RepoURL/ReportURL 补齐行",
                "| 编码 | Repo ID | Task ID | RepoURL | CommitId | 来源 |",
                "| --- | --- | --- | --- | --- | --- |",
            ]
        )
        for item in filled:
            lines.append(
                f"| {item['row']} | `{item['repo_id']}` | `{item['task_id']}` | {item['repo_url'] or '空'} | {item['commit_id'] or '空'} | {item['repo_url_source']} |"
            )
        lines.append("")
    if missing_commit_rows:
        lines.extend(
            [
                "## CommitId 为空导出行",
                "| 编码 | Repo ID | Task ID | SessionIndex | RepoURL | PR状态 |",
                "| --- | --- | --- | --- | --- | --- |",
            ]
        )
        for item in missing_commit_rows:
            lines.append(
                f"| {item['row']} | `{item['repo_id']}` | `{item['task_id']}` | {item['session_index']} | {item['repo_url'] or '空'} | {item['db_status']} |"
            )
        lines.append("")
    if missing_pr_rows:
        lines.extend(
            [
                "## PR数据库缺记录行",
                "| 编码 | Repo ID | Task ID | SessionIndex | RepoURL | CommitId |",
                "| --- | --- | --- | --- | --- | --- |",
            ]
        )
        for item in missing_pr_rows:
            lines.append(
                f"| {item['row']} | `{item['repo_id']}` | `{item['task_id']}` | {item['session_index']} | {item['repo_url'] or '空'} | {item['commit_id'] or '空'} |"
            )
        lines.append("")
    if issues:
        lines.extend(
            [
                "## PR数据库校验需关注",
                "| 编码 | Repo ID | Task ID | RepoURL | CommitId | 问题 |",
                "| --- | --- | --- | --- | --- | --- |",
            ]
        )
        for item in issues:
            lines.append(
                f"| {item['row']} | `{item['repo_id']}` | `{item['task_id']}` | {item['repo_url'] or '空'} | {item['commit_id'] or '空'} | {item['issue']} |"
            )
        lines.append("")
    report_output.parent.mkdir(parents=True, exist_ok=True)
    report_output.write_text("\n".join(lines), encoding="utf-8")


def main() -> None:
    parser = argparse.ArgumentParser(description="Export one PINRU project to Solo Coder XLSX.")
    parser.add_argument("--project", required=True)
    parser.add_argument("--db", default="/Users/chengzhiqiang/.pinru/pinru.db")
    parser.add_argument("--template", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--report-output", required=True)
    args = parser.parse_args()

    template = Path(args.template)
    output = Path(args.output)
    report_output = Path(args.report_output)
    headers = read_headers(template)
    expected = [
        "编码",
        "Repo ID",
        "做题人",
        "提交日期",
        "Trae Session ID",
        "User Prompt",
        "RepoURL",
        "CommitId",
        "任务类型",
        "业务领域",
        "修改范围",
        "任务难度",
        "任务是否完成",
        "过程与产物是否满意",
        "不满意原因",
        "单选",
        "质检状态",
        "质检备注",
        "是否提交字节",
    ]
    if headers != expected:
        raise RuntimeError(f"unexpected template headers: {headers}")
    rows, validation, stats = load_rows(Path(args.db), args.project)
    write_xlsx(template, output, headers, rows)
    write_report(args.project, template, output, report_output, validation, stats)
    print(output.resolve())
    print(report_output.resolve())
    print(json.dumps(stats, ensure_ascii=False, sort_keys=True))


if __name__ == "__main__":
    main()
