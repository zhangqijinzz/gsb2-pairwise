#!/usr/bin/env python3
import argparse
import shutil
import tempfile
import json
import re
import sqlite3
import zipfile
from copy import copy
from datetime import datetime, time, timedelta
from pathlib import Path
from xml.etree import ElementTree as ET

from openpyxl import load_workbook
from openpyxl.utils import get_column_letter


MAIN_NS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
NS = {"a": MAIN_NS}
ET.register_namespace("", MAIN_NS)

SESSION_TIME_RE = re.compile(r"T\(([^)]+)\)")
CLAIM_SEQ_RE = re.compile(r"label-\d+-(\d+)$")
CUSTOM_PROJECT_RE = re.compile(r"^zw-\d+$", re.IGNORECASE)


def parse_session_time(session_id: str) -> datetime | None:
    match = SESSION_TIME_RE.search(session_id or "")
    if not match:
        return None
    try:
        return datetime.strptime(match.group(1), "%Y/%m/%d %H:%M:%S")
    except ValueError:
        return None


def claim_seq(task_id: str) -> int:
    match = CLAIM_SEQ_RE.search(task_id or "")
    return int(match.group(1)) if match else 0


def excel_repo_id(row: sqlite3.Row) -> str:
    project_name = (row["project_name"] or "").strip()
    seq = claim_seq(row["task_id"])
    if CUSTOM_PROJECT_RE.match(project_name):
        project_number = int(project_name.split("-", 1)[1])
        return f"zw-{project_number}-{seq}"
    return f"A-{int(row['gitlab_project_id'])}-{seq}"


def normalize_report_id(value: str) -> str:
    return re.sub(r"\s+", "", (value or "").strip()).lower()


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


def cell_text(cell: ET.Element, shared_strings: list[str]) -> str | None:
    value = cell.find("a:v", NS)
    if cell.attrib.get("t") == "s" and value is not None:
        return shared_strings[int(value.text or "0")]
    if cell.attrib.get("t") == "inlineStr":
        return "".join(text.text or "" for text in cell.findall(".//a:t", NS))
    return value.text if value is not None else None


def get_existing_bounds(sheet_root: ET.Element, shared_strings: list[str]) -> tuple[int, int]:
    max_row = 0
    max_code = 0
    for row in sheet_root.findall("a:sheetData/a:row", NS):
        row_num = int(row.attrib.get("r", "0"))
        max_row = max(max_row, row_num)
        for cell in row.findall("a:c", NS):
            if col_index(cell.attrib.get("r", "")) != 1:
                continue
            value = cell_text(cell, shared_strings)
            if value and value.isdigit():
                max_code = max(max_code, int(value))
            break
    return max_row, max_code


def excel_date(value: datetime) -> str:
    return f"{value.year}/{value.month}/{value.day}"


def excel_task_type(task_type: str) -> str:
    return "0-1代码生成" if task_type == "代码生成" else task_type


def excel_task_difficulty(difficulty: str | None) -> str:
    return (difficulty or "").strip()


def bool_status(value, true_text: str, false_text: str) -> str:
    if value is None:
        return ""
    return true_text if bool(int(value)) else false_text


def effective_task_type(prompt_text: str, fallback: str) -> str:
    prompt = (prompt_text or "").strip()
    if prompt.startswith("修复"):
        return "Bug修复"
    return fallback


def workbook_headers(ws) -> dict[str, int]:
    headers: dict[str, int] = {}
    for cell in ws[1]:
        if cell.value is None:
            continue
        headers[str(cell.value).strip()] = int(cell.column)
    return headers


def first_present(headers: dict[str, int], names: tuple[str, ...]) -> str | None:
    for name in names:
        if name in headers:
            return name
    return None


def ensure_repo_commit_headers(ws) -> dict[str, int]:
    headers = workbook_headers(ws)
    has_repo_url = first_present(headers, ("RepoURL", "Repo URL")) is not None
    has_commit_id = first_present(headers, ("CommitId", "Commit ID")) is not None
    if has_repo_url and has_commit_id:
        return headers

    anchor = headers.get("User Prompt")
    if anchor is None:
        raise RuntimeError("Excel 表头缺少 User Prompt，无法插入 RepoURL / CommitId")

    insert_at = anchor + 1
    missing_headers: list[str] = []
    if not has_repo_url:
        missing_headers.append("RepoURL")
    if not has_commit_id:
        missing_headers.append("CommitId")

    ws.insert_cols(insert_at, len(missing_headers))
    for offset, header in enumerate(missing_headers):
        ws.cell(row=1, column=insert_at + offset).value = header
    return workbook_headers(ws)


def ensure_export_columns_visible(ws, headers: dict[str, int]) -> None:
    visible_widths = {
        "RepoURL": 32,
        "Repo URL": 32,
        "CommitId": 42,
        "Commit ID": 42,
        "任务类型": 14,
        "业务领域": 16,
        "修改范围": 16,
        "任务难度": 12,
        "任务是否完成": 18,
        "过程与产物是否满意": 18,
        "不满意原因": 72,
    }
    for header, width in visible_widths.items():
        col_num = headers.get(header)
        if col_num is None:
            continue
        column_letter = get_column_letter(col_num)
        ws.column_dimensions[column_letter].hidden = False
        ws.column_dimensions[column_letter].width = width


def review_bool_value(review: sqlite3.Row | None, field: str) -> int | None:
    if review is None:
        return None
    value = review[field]
    if value is not None:
        return int(value)
    status = (review["status"] or "").lower()
    if status == "pass":
        return 1
    if status == "warning":
        return 0
    return None


def is_forbidden_review_failure(row: sqlite3.Row) -> bool:
    notes = (row["review_notes"] or "").lower()
    return "forbidden" in notes or "403" in notes


def is_effective_review(row: sqlite3.Row) -> bool:
    status = (row["status"] or "").strip().lower()
    if status in ("", "none", "running"):
        return False
    if is_forbidden_review_failure(row):
        return False
    return True


def is_void_review(row: sqlite3.Row) -> bool:
    is_20260428_void = (
        int(row["gitlab_project_id"]) == 737
        and row["project_name"] == "label-00737"
        and row["task_type"] == "Feature迭代"
        and claim_seq(row["task_id"]) == 5
        and int(row["round_number"]) in (8, 9, 10)
    )
    is_20260429_void = (
        (
            int(row["gitlab_project_id"]) == 764
            and row["project_name"] == "label-00764"
            and row["task_type"] == "Feature迭代"
            and claim_seq(row["task_id"]) == 1
            and int(row["round_number"]) == 3
        )
        or (
            int(row["gitlab_project_id"]) == 778
            and row["project_name"] == "label-00778"
            and row["task_type"] == "Feature迭代"
            and claim_seq(row["task_id"]) == 1
            and int(row["round_number"]) == 6
        )
    )
    return is_20260428_void or is_20260429_void


def connect_sqlite(db_path: Path) -> tuple[sqlite3.Connection, Path | None]:
    try:
        conn = sqlite3.connect(db_path)
        conn.execute("select 1")
        return conn, None
    except sqlite3.Error:
        tmp = Path(tempfile.gettempdir()) / f"pinru-export-{datetime.now().strftime('%Y%m%d%H%M%S')}.db"
        shutil.copy2(db_path, tmp)
        return sqlite3.connect(tmp), tmp


def load_pinru_rows(
    db_path: Path,
    start_day: datetime,
    end_day: datetime | None = None,
    exclude_project_ids: set[int] | None = None,
    include_review_ids: set[str] | None = None,
    exclude_report_ids: set[str] | None = None,
    exclude_session_ids: set[str] | None = None,
) -> list[list[str | int | None]]:
    start = datetime.combine(start_day.date(), time.min)
    if end_day is None:
        end = start + timedelta(days=1)
    else:
        end = datetime.combine(end_day.date(), time.min) + timedelta(days=1)
    exclude_project_ids = exclude_project_ids or set()
    include_review_ids = include_review_ids or set()
    exclude_report_ids = {normalize_report_id(item) for item in (exclude_report_ids or set())}
    exclude_session_ids = {item.strip() for item in (exclude_session_ids or set()) if item.strip()}

    conn, tmp_db_path = connect_sqlite(db_path)
    conn.row_factory = sqlite3.Row

    reviews: dict[tuple[str, str], list[sqlite3.Row]] = {}
    for row in conn.execute(
        """
        select
          t.gitlab_project_id,
          t.project_name,
          t.task_type,
          t.prompt_difficulty,
          t.id as task_id,
          r.id as review_id,
          r.model_run_id,
          r.round_number,
          r.status,
          r.is_completed,
          r.is_satisfied,
          r.prompt_text,
          r.review_notes,
          r.dissatisfaction_summary,
          r.project_type,
          r.change_scope,
          datetime(r.updated_at,'unixepoch','localtime') as updated_time
        from ai_review_rounds r
        join tasks t on t.id = r.task_id
        order by t.id, r.model_run_id, r.round_number
        """,
    ):
        if is_void_review(row):
            continue
        if not is_effective_review(row):
            continue
        reviews.setdefault((row["task_id"], row["model_run_id"]), []).append(row)

    code_push_records: dict[tuple[str, str, int], sqlite3.Row] = {}
    code_push_records_by_session_id: dict[str, sqlite3.Row] = {}
    repo_urls_by_name: dict[str, str] = {}
    github_username = ""
    account = conn.execute(
        "select username from github_accounts order by is_default desc, updated_at desc limit 1"
    ).fetchone()
    if account is not None:
        github_username = (account["username"] or "").strip()
    for row in conn.execute(
        """
        select *
        from code_push_records
        where coalesce(commit_sha, '') <> ''
        order by updated_at desc, created_at desc
        """,
    ):
        key = (row["task_id"], row["model_run_id"], int(row["session_index"]))
        code_push_records.setdefault(key, row)
        session_id = (row["session_id"] or "").strip()
        if session_id:
            code_push_records_by_session_id.setdefault(session_id, row)
        repo_name = (row["repo_name"] or "").strip()
        repo_url = (row["repo_url"] or "").strip()
        if repo_name and repo_url:
            repo_urls_by_name.setdefault(repo_name, repo_url)

    rows = []
    for row in conn.execute(
        """
        select
          t.gitlab_project_id,
          t.project_name,
          t.task_type,
          t.prompt_difficulty,
          t.project_type as task_project_type,
          t.change_scope as task_change_scope,
          t.id as task_id,
          mr.id as model_run_id,
          mr.model_name,
          mr.session_list
        from model_runs mr
        join tasks t on t.id = mr.task_id
        where coalesce(mr.session_list, '') <> '' and mr.session_list <> '[]'
        """
    ):
        if int(row["gitlab_project_id"]) in exclude_project_ids:
            continue
        try:
            sessions = json.loads(row["session_list"] or "[]")
        except json.JSONDecodeError:
            continue

        effective_sessions = []
        for source_index, session in enumerate(sessions):
            session_id = (session.get("sessionId") or "").strip()
            session_dt = parse_session_time(session_id)
            if not session_id or session_dt is None or not (start <= session_dt < end):
                continue
            if session_id in exclude_session_ids:
                continue
            if session.get("consumeQuota") is False:
                continue
            effective_sessions.append((source_index, session, session_dt))

        repo_id = excel_repo_id(row)
        if normalize_report_id(repo_id) in exclude_report_ids:
            continue

        review_list = reviews.get((row["task_id"], row["model_run_id"]), [])
        for round_number, (source_index, session, session_dt) in enumerate(effective_sessions, start=1):
            session_id = (session.get("sessionId") or "").strip()
            session_task_type = (session.get("taskType") or row["task_type"] or "").strip()

            review = review_list[round_number - 1] if round_number - 1 < len(review_list) else None
            if include_review_ids and (review is None or review["review_id"] not in include_review_ids):
                continue
            user_prompt = (
                ((review["prompt_text"] or "").strip() if review else "")
                or (session.get("userConversation") or "").strip()
            )
            session_task_type = effective_task_type(user_prompt, session_task_type)
            is_completed = review_bool_value(review, "is_completed")
            is_satisfied = review_bool_value(review, "is_satisfied")

            satisfied_text = bool_status(is_satisfied, "满意", "不满意")
            dissatisfaction_summary = (review["dissatisfaction_summary"] or "").strip() if review else ""
            review_notes = dissatisfaction_summary or ((review["review_notes"] or "").strip() if review else "")
            session_evaluation = (session.get("evaluation") or "").strip()
            if satisfied_text == "不满意" and session_evaluation and not dissatisfaction_summary:
                review_notes = session_evaluation

            project_type = (review["project_type"] or "").strip() if review else ""
            if not project_type:
                project_type = (row["task_project_type"] or "").strip()
            change_scope = (review["change_scope"] or "").strip() if review else ""
            if not change_scope:
                change_scope = (row["task_change_scope"] or "").strip()

            code_push = code_push_records_by_session_id.get(session_id)
            if code_push is None:
                code_push = code_push_records.get((row["task_id"], row["model_run_id"], source_index))
            repo_name = (code_push["repo_name"] or "").strip() if code_push else repo_id
            repo_url = (code_push["repo_url"] or "").strip() if code_push else ""
            if not repo_url:
                repo_url = repo_urls_by_name.get(repo_name, "")
            if not repo_url and github_username and repo_id:
                repo_url = f"https://github.com/{github_username}/{repo_id}"
            commit_id = (code_push["commit_sha"] or "").strip() if code_push else ""

            rows.append(
                {
                    "sort_key": (
                        int(row["gitlab_project_id"]),
                        claim_seq(row["task_id"]),
                        round_number,
                        session_dt,
                        session_task_type,
                    ),
                    "values": {
                        "Repo ID": repo_id,
                        "提交日期": excel_date(session_dt),
                        "Trae Session ID": session_id,
                        "User Prompt": user_prompt,
                        "RepoURL": repo_url,
                        "Repo URL": repo_url,
                        "CommitId": commit_id,
                        "Commit ID": commit_id,
                        "任务类型": excel_task_type(session_task_type),
                        "业务领域": project_type,
                        "修改范围": change_scope,
                        "任务难度": excel_task_difficulty(row["prompt_difficulty"]),
                        "任务是否完成": bool_status(is_completed, "完成了任务", "未完成任务"),
                        "过程与产物是否满意": satisfied_text,
                        "不满意原因": review_notes if satisfied_text == "不满意" else None,
                    },
                }
            )

    conn.close()
    if tmp_db_path is not None:
        tmp_db_path.unlink(missing_ok=True)
    rows.sort(key=lambda item: item["sort_key"])
    return rows


def make_cell(row_num: int, col_num: int, value, style: str | None = None) -> ET.Element | None:
    if value is None or value == "":
        return None
    cell = ET.Element(f"{{{MAIN_NS}}}c", {"r": f"{col_name(col_num)}{row_num}"})
    if style:
        cell.attrib["s"] = style
    if col_num == 1 and isinstance(value, int):
        value_el = ET.SubElement(cell, f"{{{MAIN_NS}}}v")
        value_el.text = str(value)
        return cell

    cell.attrib["t"] = "inlineStr"
    inline = ET.SubElement(cell, f"{{{MAIN_NS}}}is")
    text = ET.SubElement(inline, f"{{{MAIN_NS}}}t")
    text.text = str(value)
    if str(value).strip() != str(value) or "\n" in str(value):
        text.attrib["{http://www.w3.org/XML/1998/namespace}space"] = "preserve"
    return cell


def append_rows_to_workbook(input_path: Path, output_path: Path, rows: list[list[str | int | None]]) -> None:
    with zipfile.ZipFile(input_path, "r") as source:
        shared_strings = read_shared_strings(source)
        sheet_xml = source.read("xl/worksheets/sheet1.xml")
        sheet_root = ET.fromstring(sheet_xml)
        sheet_data = sheet_root.find("a:sheetData", NS)
        if sheet_data is None:
            raise RuntimeError("sheetData not found")

        max_row, max_code = get_existing_bounds(sheet_root, shared_strings)
        dimension = sheet_root.find("a:dimension", NS)

        for offset, values in enumerate(rows, start=1):
            row_num = max_row + offset
            code = max_code + offset
            values = copy(values)
            values[0] = code
            row_el = ET.Element(f"{{{MAIN_NS}}}row", {"r": str(row_num)})
            for col_num, value in enumerate(values, start=1):
                style = "2" if col_num in (2, 5, 6) else None
                cell = make_cell(row_num, col_num, value, style)
                if cell is not None:
                    row_el.append(cell)
            sheet_data.append(row_el)

        if dimension is not None:
            dimension.attrib["ref"] = f"A1:Q{max_row + len(rows)}"

        rendered_sheet = ET.tostring(sheet_root, encoding="utf-8", xml_declaration=True)

        with zipfile.ZipFile(output_path, "w", compression=zipfile.ZIP_DEFLATED) as target:
            for item in source.infolist():
                data = rendered_sheet if item.filename == "xl/worksheets/sheet1.xml" else source.read(item.filename)
                target.writestr(item, data)


def write_rows_by_header(input_path: Path, output_path: Path, rows: list[dict], replace_data: bool) -> None:
    wb = load_workbook(input_path)
    ws = wb.active
    headers = ensure_repo_commit_headers(ws)
    if "Repo ID" not in headers or "Trae Session ID" not in headers:
        raise RuntimeError("Excel 表头缺少 Repo ID 或 Trae Session ID")
    ensure_export_columns_visible(ws, headers)

    if replace_data and ws.max_row > 1:
        ws.delete_rows(2, ws.max_row - 1)

    max_code = 0
    code_col = headers.get("编码")
    if code_col:
        for row_num in range(2, ws.max_row + 1):
            value = ws.cell(row=row_num, column=code_col).value
            if isinstance(value, int):
                max_code = max(max_code, value)
            elif isinstance(value, str) and value.isdigit():
                max_code = max(max_code, int(value))

    repo_url_header = first_present(headers, ("RepoURL", "Repo URL"))
    commit_id_header = first_present(headers, ("CommitId", "Commit ID"))

    for offset, item in enumerate(rows, start=1):
        row_num = ws.max_row + 1
        values = dict(item["values"])
        if code_col:
            ws.cell(row=row_num, column=code_col).value = max_code + offset
        if repo_url_header and "RepoURL" in values:
            values[repo_url_header] = values["RepoURL"]
        if commit_id_header and "CommitId" in values:
            values[commit_id_header] = values["CommitId"]

        for header, value in values.items():
            col_num = headers.get(header)
            if col_num is None:
                continue
            ws.cell(row=row_num, column=col_num).value = value

    wb.save(output_path)


def parse_date(value: str) -> datetime:
    return datetime.strptime(value, "%Y-%m-%d")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--date", default="2026-04-28")
    parser.add_argument("--start-date")
    parser.add_argument("--end-date")
    parser.add_argument("--exclude-project-id", action="append", type=int, default=[])
    parser.add_argument("--exclude-report-id", action="append", default=[])
    parser.add_argument("--exclude-session-id", action="append", default=[])
    parser.add_argument("--review-id", action="append", default=[])
    parser.add_argument("--input", default="/Users/tory/Downloads/Solo coder-0417.xlsx")
    parser.add_argument("--output", default="/Users/tory/Documents/trae_projects/PINRU/Solo coder-0417-session-filled-20260428.xlsx")
    parser.add_argument("--db", default="/Users/tory/.pinru/pinru.db")
    parser.add_argument("--expected-rows", type=int)
    parser.add_argument("--replace-data", action="store_true")
    args = parser.parse_args()

    input_path = Path(args.input)
    output_path = Path(args.output)
    db_path = Path(args.db)

    if args.start_date:
        end_date = parse_date(args.end_date) if args.end_date else datetime.now()
        rows = load_pinru_rows(
            db_path,
            parse_date(args.start_date),
            end_date,
            set(args.exclude_project_id),
            set(args.review_id),
            set(args.exclude_report_id),
            set(args.exclude_session_id),
        )
    else:
        rows = load_pinru_rows(
            db_path,
            parse_date(args.date),
            exclude_project_ids=set(args.exclude_project_id),
            include_review_ids=set(args.review_id),
            exclude_report_ids=set(args.exclude_report_id),
            exclude_session_ids=set(args.exclude_session_id),
        )
    if args.expected_rows is not None and len(rows) != args.expected_rows:
        raise RuntimeError(f"expected {args.expected_rows} rows for {args.date}, got {len(rows)}")
    write_rows_by_header(input_path, output_path, rows, args.replace_data)
    print(output_path)
    print(f"appended_rows={len(rows)}")


if __name__ == "__main__":
    main()
