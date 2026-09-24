#!/usr/bin/env python3
import argparse
import json
import re
import shutil
import sqlite3
import tempfile
from datetime import datetime, time, timedelta
from pathlib import Path


SESSION_TIME_RE = re.compile(r"T\(([^)]+)\)")
STRUCTURED_REASON_RE = re.compile(r"过程不满意[:：].+产物不满意[:：].+", re.S)
FORBIDDEN_CROSS_ROUND_RE = re.compile(r"(上一轮|上一版|这一轮已经|继续|同上)")
REVIEW_FAILURE_RE = re.compile(r"(复审执行失败|codex 执行失败|claude 执行失败|stream disconnected|unexpected status|Forbidden)", re.I)

PRODUCT_MARKERS = (
    "未满意点回指",
    "当前不满意点回指",
    "不满意点回指",
    "但对照",
    "但相对",
    "但当前",
    "但按",
    "但原始任务",
    "但提示词",
    "但 prompt",
    "但 ",
)

DEFECT_MARKERS = (
    "当前实现仍",
    "现有实现仍",
    "当前代码仍",
    "当前仍",
    "未看到",
    "没有看到",
    "缺少",
    "未落地",
    "未实现",
    "未接入",
    "不会",
    "无法",
    "不能",
    "不符合",
    "不一致",
    "仍可能",
    "仍然",
)

DATA_LINK_WORDS = ("接口", "字段", "返回", "参数", "前端", "后端", "页面", "表单", "按钮", "状态值", "联表", "blob")
VALIDATION_WORDS = ("边界", "status=0", "0值", "0 值", "真值", "空值", "计算", "口径", "过零率", "周期", "宽限期", "事务", "回滚")
PLANNING_WORDS = ("未看到", "没有看到", "缺少", "未落地", "未实现", "未接入", "只覆盖", "只处理", "只做")
INSTRUCTION_WORDS = ("不新增依赖", "不要改", "接口契约", "保持不变", "沿用现有", "代码风格保持")
FIX_WORDS = ("修复", "仍", "还保留", "没有清理", "未完全")
MAIN_GAP_WORDS = ("主链路缺口", "核心链路缺口", "关键链路缺口", "主流程缺口", "核心交付物")


def parse_date(value: str) -> datetime:
    return datetime.strptime(value, "%Y-%m-%d")


def parse_session_time(session_id: str) -> datetime | None:
    match = SESSION_TIME_RE.search(session_id or "")
    if not match:
        return None
    try:
        return datetime.strptime(match.group(1), "%Y/%m/%d %H:%M:%S")
    except ValueError:
        return None


def date_bounds(day: datetime, end_day: datetime | None) -> tuple[datetime, datetime]:
    start = datetime.combine(day.date(), time.min)
    if end_day is None:
        end = start + timedelta(days=1)
    else:
        end = datetime.combine(end_day.date(), time.min) + timedelta(days=1)
    return start, end


def connect_sqlite(db_path: Path) -> tuple[sqlite3.Connection, Path | None]:
    try:
        conn = sqlite3.connect(db_path)
        conn.execute("select 1")
        return conn, None
    except sqlite3.Error:
        tmp = Path(tempfile.gettempdir()) / f"pinru-normalize-{datetime.now().strftime('%Y%m%d%H%M%S')}.db"
        shutil.copy2(db_path, tmp)
        return sqlite3.connect(tmp), tmp


def is_structured_reason(value: str | None) -> bool:
    return bool(STRUCTURED_REASON_RE.search((value or "").strip()))


def normalize_summary(value: str) -> str:
    text = (value or "").strip()
    text = re.sub(r"^```(?:json|text)?\s*", "", text)
    text = re.sub(r"\s*```$", "", text)
    text = re.sub(r"\s+", " ", text)
    text = text.replace("过程不满意 :", "过程不满意：")
    text = text.replace("产物不满意 :", "产物不满意：")
    return text.strip()


def compact_text(value: str | None) -> str:
    text = (value or "").strip()
    text = re.sub(r"\s+", " ", text)
    text = text.replace("current_prompt/original_prompt", "prompt")
    text = text.replace("original_prompt/current_prompt", "prompt")
    text = text.replace("current_prompt", "prompt")
    text = text.replace("original_prompt", "prompt")
    text = text.replace("isCompleted=true", "")
    text = text.replace("isCompleted 为 true", "")
    text = re.sub(r"\s*，?\s*所以\s*isCompleted\s*为?\s*true\s*", "，", text)
    return text.strip(" ；;，,。")


def strip_technical_detail(value: str) -> str:
    text = compact_text(value)
    text = re.sub(r"`[^`]+`", "", text)
    text = re.sub(r"\b[\w.-]+\.(?:vue|js|ts|tsx|jsx|java|go|py|xml|sql|md)(?::\d+)?\b", "", text, flags=re.I)
    text = re.sub(r"\b[A-Za-z_][A-Za-z0-9_]*\([^)]*\)", "", text)
    text = re.sub(r"\b[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*\b", "", text)
    text = re.sub(r"\b[A-Za-z_][A-Za-z0-9_]{2,}\b", "", text)
    text = re.sub(r"\s+", " ", text)
    text = re.sub(r"[:：]\s*[，。；;]", "：", text)
    return text.strip(" ；;，,。")


def light_clean_evidence(value: str) -> str:
    text = compact_text(value)
    text = re.sub(r"`([^`]+)`", r"\1", text)
    text = re.sub(r"\b[\w./-]+\.(?:vue|js|ts|tsx|jsx|java|go|py|xml|sql|md)(?::\d+(?:-\d+)?)?\b", "", text, flags=re.I)
    text = re.sub(r"\s+", " ", text)
    text = re.sub(r"\s+([，。；：])", r"\1", text)
    text = re.sub(r"([，；：])\s+", r"\1", text)
    text = re.sub(r"\b[A-Za-z]:?\\[^\s，。；]+", "", text)
    text = text.replace("original_prompt/current_prompt", "prompt")
    text = text.replace("current_prompt/original_prompt", "prompt")
    text = text.replace("current_prompt", "prompt")
    text = text.replace("original_prompt", "prompt")
    text = re.sub(r"\s*所以\s*(?:完成但不满意|未达到\s*90\s*分满意标准|isCompleted\s*=\s*true)\s*", "", text)
    text = re.sub(r"\s*因此\s*(?:未达到\s*90\s*分|未达到满意标准)\s*", "，未达到满意标准", text)
    return text.strip(" ；;，,。")


def limit_text(value: str, max_len: int) -> str:
    text = strip_technical_detail(value)
    if len(text) <= max_len:
        return text
    cut = text[:max_len]
    for sep in ("；", "，", "。"):
        idx = cut.rfind(sep)
        if idx >= max_len * 0.45:
            return cut[:idx].strip(" ；;，,。")
    return cut.rstrip(" ；;，,。")


def ensure_sentence(value: str) -> str:
    text = compact_text(value).strip(" ；;，,。")
    if not text:
        return ""
    return text if text.endswith(("。", "！", "？")) else text + "。"


def trim_success_lead(text: str) -> str:
    cleaned = compact_text(text)
    for marker in PRODUCT_MARKERS:
        index = cleaned.find(marker)
        if index >= 0:
            cleaned = cleaned[index:]
            break
    cleaned = re.sub(r"^(当前)?不?满意点回指\s*[:：]?\s*", "", cleaned)
    cleaned = re.sub(r"^但\s*", "", cleaned)
    cleaned = cleaned.strip(" ：:，,")
    return cleaned


def first_defect_sentence(text: str) -> str:
    sentences = [part.strip() for part in re.split(r"(?<=[。！？])", compact_text(text)) if part.strip()]
    selected = [sentence for sentence in sentences if any(marker in sentence for marker in DEFECT_MARKERS)]
    if selected:
        return "".join(selected[:2])
    return compact_text(text)


def sentences(text: str) -> list[str]:
    return [part.strip() for part in re.split(r"(?<=[。！？])", compact_text(text)) if part.strip()]


def sentence_has_defect(text: str) -> bool:
    return any(marker in text for marker in DEFECT_MARKERS) or any(
        marker in text
        for marker in (
            "主链路",
            "未达到",
            "只能判定",
            "不能判定",
            "无法确认",
            "无法核验",
            "未能核验",
            "不满意",
            "错误",
            "风险",
        )
    )


def extract_defect_fragment(review_notes: str) -> str:
    raw = compact_text(review_notes)
    if not raw:
        return ""
    for marker in ("当前不满意点回指", "未满意点回指", "不满意点回指"):
        index = raw.find(marker)
        if index >= 0:
            return raw[index + len(marker) :].strip(" ：:，,。")

    candidates: list[str] = []
    for marker in ("但", "但prompt", "但 prompt", "但原始任务", "但当前节点", "但提示词", "但对照", "但相对", "但由于", "但同一"):
        start = 0
        while True:
            index = raw.find(marker, start)
            if index < 0:
                break
            candidates.append(raw[index + len(marker) :].strip(" ：:，,。"))
            start = index + len(marker)
    defect_candidates = [candidate for candidate in candidates if sentence_has_defect(candidate)]
    if defect_candidates:
        return defect_candidates[-1]

    defect_sentences = [sentence for sentence in sentences(raw) if sentence_has_defect(sentence)]
    if defect_sentences:
        return "".join(defect_sentences[-2:])
    return raw


def process_issue(value: str) -> str:
    text = light_clean_evidence(value)
    text = re.sub(r"^(?:prompt\s*)?(?:中|里)?\s*(?:明确)?要求[“\"]?[^，。；]{2,80}[”\"]?[，；]", "", text)
    text = re.sub(r"^要求[“\"]?[^，。；]{2,80}[”\"]?[，；]", "", text)
    text = re.sub(r"^同时要求[“\"]?[^，。；]{2,80}[”\"]?[，；]", "", text)
    text = re.sub(r"^(?:当前|现有|代码|报告|接口|页面)", "", text)
    text = text.strip(" ：:，,。")
    for marker in ("未看到", "缺少", "没有", "无法", "不能", "不会", "未能", "仍", "只", "存在"):
        index = text.find(marker)
        if index >= 0 and index < 80:
            text = text[index:]
            break
    return concise_evidence(text, 48)


def concise_evidence(value: str, max_len: int) -> str:
    text = light_clean_evidence(value)
    text = re.sub(r"^(?:prompt\s*)?(?:中|里)?\s*(?:明确)?要求", "要求", text)
    text = re.sub(r"^按\s*prompt\s*(?:的)?", "要求", text)
    text = re.sub(r"^对照\s*prompt\s*(?:的)?", "要求", text)
    text = re.sub(r"^但", "", text)
    text = re.sub(r"中[“\"]([^”\"]+)[”\"]", r"要求\1", text)
    text = re.sub(r"[“\"]([^”\"]+)[”\"]([“\"])", r"\1\2", text)
    text = re.sub(r"所以(?:本轮)?核心交付物.*$", "", text)
    text = re.sub(r"所以完成但不满意$", "", text)
    text = re.sub(r"因此完成但未达到满意$", "", text)
    text = re.sub(r"，?未达到\s*90\s*分满意标准?$", "，未达到满意标准", text)
    text = re.sub(r"^[:：，,]+", "", text)
    text = text.strip(" ；;，,。")
    if len(text) <= max_len:
        return text
    cut = text[:max_len]
    for sep in ("。", "；", "，"):
        idx = cut.rfind(sep)
        if idx >= max_len * 0.45:
            return cut[:idx].strip(" ；;，,。")
    return cut.rstrip(" ；;，,。")


def normalize_product_reason(record: dict) -> str:
    review_notes = compact_text(record.get("review_notes", ""))
    if REVIEW_FAILURE_RE.search(review_notes):
        return ensure_sentence("本轮复审没有得到有效验收结论，复审记录只有执行失败信息，无法作为产物满意依据")
    product = extract_defect_fragment(review_notes)
    return ensure_sentence(repair_stripped_product_text(concise_evidence(product, 150)))


def process_source(record: dict, product_reason: str) -> str:
    return compact_text(
        " ".join(
            [
                record.get("task_type", ""),
                record.get("user_prompt", ""),
                record.get("current_prompt", ""),
                record.get("review_notes", ""),
                product_reason,
            ]
        )
    )


def has_any(text: str, words: tuple[str, ...]) -> bool:
    return any(word in text for word in words)


def is_build_verification_gap(text: str) -> bool:
    cleaned = compact_text(text)
    return has_any(cleaned, ("编译", "构建")) and has_any(cleaned, ("无法实际完成", "未能执行", "缺少 mvn", "无 wrapper", "无法确认", "不能判定"))


def quoted_items_around_prompt_gap(text: str) -> str:
    cleaned = compact_text(text)
    if not has_any(cleaned, MAIN_GAP_WORDS):
        return ""
    prompt_index = min(
        [index for marker in ("prompt", "original_prompt", "current_prompt", "提示词") if (index := cleaned.find(marker)) >= 0],
        default=-1,
    )
    if prompt_index < 0:
        return ""
    tail = cleaned[prompt_index:]
    items = re.findall(r"[“\"]([^”\"]{2,80})[”\"]", tail)
    items = [item.strip(" ；;，,。") for item in items if item.strip(" ；;，,。")]
    if not items:
        return ""
    return "、".join(items[:4])


def repair_stripped_product_text(text: str) -> str:
    cleaned = compact_text(text)
    cleaned = re.sub(r"^(?:中|里|的)[“\"]", "prompt 要求“", cleaned)
    cleaned = re.sub(r"^中(?=[^，。]{2,60}(?:缺口|不符合|不一致|未实现|未落地))", "prompt ", cleaned)
    cleaned = re.sub(r"\s+，", "，", cleaned)
    cleaned = re.sub(r"，\s+", "，", cleaned)
    cleaned = re.sub(r"只开放给\s*，", "只开放范围不完整，", cleaned)
    cleaned = re.sub(r"///?list接口", "会员资料列表", cleaned)
    cleaned = re.sub(r"/+\s*接口", "相关接口", cleaned)
    cleaned = re.sub(r"\s+未按", "未按", cleaned)
    cleaned = re.sub(r"要求“([^”]+)”", r"要求\1", cleaned)
    cleaned = re.sub(r"(^|[。；])的[“\"]", r"\1prompt 要求“", cleaned)
    return cleaned.strip(" ；;，,。")


def extract_between(text: str, start_marker: str, end_markers: tuple[str, ...]) -> str:
    start = text.find(start_marker)
    if start < 0:
        return ""
    start += len(start_marker)
    end = len(text)
    for marker in end_markers:
        index = text.find(marker, start)
        if index >= 0:
            end = min(end, index)
    return limit_text(text[start:end], 52)


def infer_process_dimension(record: dict, product_reason: str) -> str:
    source = compact_text(
        " ".join(
            [
                record.get("task_type", ""),
                record.get("user_prompt", ""),
                record.get("current_prompt", ""),
                record.get("review_notes", ""),
                product_reason,
            ]
        )
    )
    if REVIEW_FAILURE_RE.search(source):
        return "复审流程异常"
    if is_build_verification_gap(source):
        return "验证覆盖不完整"
    if "无法核验" in source or "未能核验" in source or "只能判定" in source:
        return "验证证据不足"
    if has_any(source, INSTRUCTION_WORDS):
        return "指令限制未遵循"
    if "Bug修复" in source and has_any(source, FIX_WORDS):
        return "修复执行不完整"
    if has_any(source, MAIN_GAP_WORDS):
        return "任务规划不完整"
    if has_any(source, VALIDATION_WORDS):
        return "验证覆盖不完整"
    if has_any(source, DATA_LINK_WORDS) and has_any(source, ("拿不到", "无法", "不会", "没有", "未返回", "未传递", "未展示", "未同步")):
        return "数据链路设计不完整"
    if has_any(source, PLANNING_WORDS):
        return "任务规划不完整"
    if has_any(source, ("做成", "当成", "理解", "要求")) and has_any(source, ("不符合", "不一致", "偏离", "只", "漏")):
        return "prompt 理解不充分"
    return "任务规划不完整"


def normalize_process_reason(record: dict, product_reason: str) -> str:
    source = process_source(record, product_reason)
    dimension = infer_process_dimension(record, product_reason)
    defect = concise_evidence(extract_defect_fragment(record.get("review_notes", "")), 86)
    missing = extract_between(source, "未看到", ("，", "；", "。"))
    if not missing:
        missing = extract_between(source, "缺少", ("，", "；", "。"))
    if not missing:
        missing = extract_between(source, "没有", ("，", "；", "。"))
    limited_product = limit_text(product_reason, 58)

    if dimension == "复审流程异常":
        return "输出不正常：复审没有正常完成，只留下执行失败信息，不能作为有效验收结论。"
    if dimension == "验证证据不足":
        return "验证覆盖不完整：复审记录里仍有关键结果无法核验，说明本轮验收没有把影响满意度的证据补齐。"
    if dimension == "指令限制未遵循":
        return "指令遵循不足：prompt 里已有明确限制，但交付时没有把这类限制当成必须遵守的边界。"
    if dimension == "修复执行不完整":
        if defect:
            return f"修复执行不完整：本轮修复后复审仍指出{defect}，说明修复没有覆盖到目标链路的残留问题。"
        return "修复执行不完整：修复目标没有闭环到回归检查，复审记录里仍能看到同类问题残留。"
    if dimension == "验证覆盖不完整":
        if is_build_verification_gap(source):
            return "验证覆盖不完整：修复后没有完成编译或构建检查，无法确认本轮修改是否还残留基础可运行问题。"
        if defect:
            return f"验证覆盖不完整：本轮验收没有覆盖到{defect}，导致关键边界或计算口径问题留到复审阶段才暴露。"
        return "验证覆盖不完整：验收时没有覆盖关键边界、异常分支或计算口径，导致问题留到复审阶段才暴露。"
    if dimension == "数据链路设计不完整":
        if defect:
            return f"数据链路设计不完整：本轮没有把相关页面、数据传递和结果反馈串起来核对，复审仍指出{defect}。"
        return "数据链路设计不完整：没有把用户操作、数据传递和页面反馈作为一条完整链路来核对。"
    if dimension == "任务规划不完整":
        if defect:
            return f"任务规划不完整：本轮只覆盖了部分交付内容，没有把{defect}纳入完整验收范围。"
        if missing:
            return f"任务规划不完整：复审记录显示仍缺少{missing}，说明本轮处理没有把这个关键环节纳入验收。"
        return "任务规划不完整：复审记录显示仍有关键入口、状态变化或联动流程没有纳入验收。"
    if dimension == "prompt 理解不充分":
        if defect:
            return f"模型未能完全理解 prompt：交付结果和需求目标仍有偏差，复审集中指出{defect}。"
        return f"模型未能完全理解 prompt：本轮交付和需求目标仍有偏差，复审集中指出的是{limited_product}。"
    return "任务规划不完整：复审记录显示仍有关键流程没有进入本轮验收范围，导致交付结果和 prompt 目标存在偏差。"


def build_rule_summary(record: dict) -> str:
    existing = compact_text(record.get("session_evaluation", ""))
    if existing and is_structured_reason(existing):
        return validate_summary(existing)
    product = normalize_product_reason(record)
    process = normalize_process_reason(record, product)
    return validate_summary(f"过程不满意：{process}产物不满意：{product}")


def validate_summary(value: str) -> str:
    text = normalize_summary(value)
    if not is_structured_reason(text):
        raise ValueError("模型输出缺少“过程不满意/产物不满意”两段")
    if FORBIDDEN_CROSS_ROUND_RE.search(text):
        raise ValueError("模型输出包含跨轮引用")
    if len(text) > 900:
        raise ValueError("模型输出过长")
    return text


def load_records(
    conn: sqlite3.Connection,
    start: datetime,
    end: datetime,
    force: bool,
    limit: int | None,
) -> list[dict]:
    conn.row_factory = sqlite3.Row
    records: list[dict] = []
    for row in conn.execute(
        """
        select
          t.id as task_id,
          t.gitlab_project_id,
          t.project_name,
          t.task_type as task_type,
          t.prompt_text as task_prompt_text,
          mr.id as model_run_id,
          mr.model_name,
          json_each.key as session_index,
          json_extract(json_each.value, '$.sessionId') as session_id,
          json_extract(json_each.value, '$.taskType') as session_task_type,
          json_extract(json_each.value, '$.evaluation') as session_evaluation,
          json_extract(json_each.value, '$.userConversation') as user_conversation,
          r.id as review_id,
          r.round_number,
          r.original_prompt,
          r.prompt_text as review_prompt_text,
          r.review_notes,
          r.key_locations,
          r.project_type,
          r.change_scope
        from model_runs mr
        join tasks t on t.id = mr.task_id
        join json_each(mr.session_list)
        join ai_review_rounds r
          on r.task_id = t.id
         and r.model_run_id = mr.id
         and r.round_number = json_each.key + 1
        where json_valid(mr.session_list)
          and r.is_satisfied = 0
        order by t.gitlab_project_id, t.id, r.round_number
        """
    ):
        session_id = (row["session_id"] or "").strip()
        session_dt = parse_session_time(session_id)
        if session_dt is None or not (start <= session_dt < end):
            continue
        session_evaluation = (row["session_evaluation"] or "").strip()
        if session_evaluation and is_structured_reason(session_evaluation) and not force:
            continue
        if not (row["review_notes"] or "").strip():
            continue
        records.append(
            {
                "review_id": row["review_id"],
                "task_id": row["task_id"],
                "repo": f"A-{int(row['gitlab_project_id'])}",
                "project_name": row["project_name"],
                "task_type": (row["session_task_type"] or row["task_type"] or "").strip(),
                "model_run_id": row["model_run_id"],
                "model_name": row["model_name"],
                "session_index": int(row["session_index"]),
                "session_id": session_id,
                "session_evaluation": session_evaluation,
                "round_number": int(row["round_number"]),
                "user_prompt": (row["user_conversation"] or row["review_prompt_text"] or row["task_prompt_text"] or "").strip(),
                "original_prompt": (row["original_prompt"] or "").strip(),
                "current_prompt": (row["review_prompt_text"] or "").strip(),
                "review_notes": (row["review_notes"] or "").strip(),
                "key_locations": (row["key_locations"] or "").strip(),
                "project_type": (row["project_type"] or "").strip(),
                "change_scope": (row["change_scope"] or "").strip(),
            }
        )
        if limit is not None and len(records) >= limit:
            break
    return records


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--date", default=datetime.now().strftime("%Y-%m-%d"))
    parser.add_argument("--start-date")
    parser.add_argument("--end-date")
    parser.add_argument("--db", default="/Users/tory/.pinru/pinru.db")
    parser.add_argument("--limit", type=int)
    parser.add_argument("--force", action="store_true")
    parser.add_argument("--jsonl")
    args = parser.parse_args()

    db_path = Path(args.db)
    start_day = parse_date(args.start_date) if args.start_date else parse_date(args.date)
    end_day = parse_date(args.end_date) if args.end_date else None
    start, end = date_bounds(start_day, end_day)

    conn, tmp_db_path = connect_sqlite(db_path)
    try:
        records = load_records(conn, start, end, args.force, args.limit)
        print(f"pending={len(records)}")
        success = 0
        failed = 0
        for index, record in enumerate(records, start=1):
            label = f"{record['task_id']}#round{record['round_number']}"
            try:
                summary = build_rule_summary(record)
                item = {
                    "task_id": record["task_id"],
                    "round_number": record["round_number"],
                    "session_id": record["session_id"],
                    "summary": summary,
                }
                if args.jsonl:
                    with open(args.jsonl, "a", encoding="utf-8") as fh:
                        fh.write(json.dumps(item, ensure_ascii=False) + "\n")
                else:
                    print(f"[{index}/{len(records)}] {label}: {summary}")
                success += 1
            except Exception as err:
                failed += 1
                print(f"[{index}/{len(records)}] failed {label}: {err}")
        print(f"success={success} failed={failed}")
    finally:
        conn.close()
        if tmp_db_path is not None:
            tmp_db_path.unlink(missing_ok=True)


if __name__ == "__main__":
    main()
