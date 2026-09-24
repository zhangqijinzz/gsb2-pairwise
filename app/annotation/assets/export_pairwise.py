#!/usr/bin/env python3
import argparse
import json
from pathlib import Path
import re
import sys
import xml.etree.ElementTree as ET
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo


MAIN_NS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
NS = "{" + MAIN_NS + "}"
REL_NS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
XML_SPACE = "{http://www.w3.org/XML/1998/namespace}space"
ET.register_namespace("", MAIN_NS)
ET.register_namespace("r", REL_NS)

TEMPLATE = Path(__file__).with_name("template.xlsx")
SHEET_PATH = "xl/worksheets/sheet1.xml"
WORKBOOK_PATH = "xl/workbook.xml"
FIXED_ZIP_TIME = (1980, 1, 1, 0, 0, 0)

HEADERS = [
    "User Prompt", "任务类型", "任务难度", "语言/框架", "Harness", "Harness 版本",
    "操作系统", "环境可复现等级", "初始环境快照", "A-SessionID", "A-轨迹文件",
    "A-产物快照", "A-运行录屏", "B-SessionID", "B-轨迹文件", "B-产物快照",
    "B-运行录屏", "A-交付完整性", "A-交付完整性描述", "B-交付完整性", "B-交付完整性描述",
    "GSB 结论", "GSB 理由", "有效性", "备注",
]
CONCLUSIONS = {"A_better": "A 更好", "B_better": "B 更好", "same": "Same"}
WIDTHS = [36, 18, 14, 28, 16, 16, 18, 24, 48, 24, 42, 48, 42, 24, 42, 48, 42, 14, 48, 14, 48, 14, 72, 22, 32]

COMPLETENESS_NEGATIVE = re.compile(
    r"(?:未(?:验证|核验|执行|测试|覆盖|完成|实现|交付|达到|满足|通过)"
    r"|没有(?:执行|测试|覆盖|完成|实现|交付|达到|满足|对应|相应)"
    r"|缺少|缺乏|存在(?:功能)?(?:遗漏|问题|缺陷)|仍(?:有|会|未|无法|不能)|尚未|不足|遗漏|缺陷"
    r"|不完整|不一致|未通过|返工|只(?:完成|实现)|仅(?:完成|实现)|证据不足|只能确认)"
)
COMPLETENESS_CONDITION = re.compile(
    r"(?:失败|报错|异常|无法|不能|缺少|缺乏|不足|不一致|未通过|回归)"
)
COMPLETENESS_UNRESOLVED_CONDITION = re.compile(
    r"(?:尚未|仍(?:有|会|未|无法|不能)|(?:无法|不能|未能)(?:恢复|完成|使用|读取|保存|迁移)(?:[。！？；，,]|$))"
)
COMPLETENESS_POSITIVE_ABSENCE = re.compile(
    r"(?:没有|未|无)(?:(?:发现|出现|检测到|看到)[^。！？；，,]{0,12})?"
    r"(?:任何|明显|实际|功能|关键|主要|重大)?(?:问题|缺陷|遗漏|不足|缺口|回归|未覆盖|未验证)"
)
COMPLETENESS_POSITIVE_RETENTION = re.compile(
    r"(?:不(?:抹掉|清除|覆盖|隐藏)|保留|继续保留)[^。！？；]{0,16}尚未恢复的[^。！？；]{0,12}(?:警告|提示|错误)"
)
COMPLETENESS_POSITIVE_COVERAGE = re.compile(
    r"(?:新增|补充|添加|完善)?(?:回归)?(?:测试|用例|测试文件|\.test\.(?:ts|tsx|js|jsx))"
    r"[^。！？；]{0,32}覆盖[^。！？；]{0,240}"
)
COMPLETENESS_POSITIVE_CONSISTENCY = re.compile(
    r"(?:保持|避免|消除|不再|不会|不出现|不发生|防止)[^。！？；]{0,24}不一致"
)
COMPLETENESS_POSITIVE_CONTINUATION = re.compile(
    r"(?:仍(?:会|能|可)|依然|继续)[^。！？；]{0,24}(?:提交|持久化|保存|显示|恢复|可用|完成|保留|同步|写入|交付)"
)
COMPLETENESS_POSITIVE_TEST_CHANGE = re.compile(
    r"(?:[A-Za-z0-9_./-]+\.test\.(?:ts|tsx|js|jsx))"
    r"[^。！？；]{0,80}(?:新增|补上|补充|添加|完善)"
    r"[^。！？；]{0,80}(?:回归(?:测试|用例)|测试|用例)"
    r"|(?:测试|用例)[^。！？；]{0,80}(?:新增|补上|补充|添加|完善)"
)
COMPLETENESS_HANDLED_STATE = re.compile(
    r"(?:未完成(?:拖拽|绘制|操作|手势|状态)"
    r"|(?:拖拽|草稿|起点|终点|字段|障碍|网格|尺寸|宽度|高度|大小)?(?:缺少|缺失|不足))"
    r"[^。！？；]{0,48}(?:收尾清理|清空|清掉|丢弃|取消|补齐|补上|填充|填入|补空|置空|设为空|给空|按默认|回退|恢复|同步|写回|走|处理|提示)"
)
COMPLETENESS_HANDLED_CONDITION = re.compile(
    r"[^。！？；]{0,80}(?:失败|报错|异常|无法|不能|缺少|缺乏|不足|不一致|未通过|回归)"
    r"[^。！？；]{0,36}(?:"
    r"后[^。！？；]{0,16}(?:成功|恢复|重试|回退|清除|消失|保留|同步|写入|显示|通过|完成|修正|修复)"
    r"|时[^。！？；]{0,36}(?:补齐|补上|填充|填入|补空|置空|设为空|给空|按默认|回退|恢复|同步|保留|清除|写入|显示|转入|转为|落到|落回|改走|改为|切换到|不再当成|不再视为|走|改|仍|也|只|仅|成功|兜底|可以|能够|修正|修复|作为(?:数据)?来源)"
    r"|(?:只|仅)(?:写|清|更新|刷新|改|保留|显示)"
    r"|(?:也|仍)?(?:能|可以|能够)(?:恢复|回退|使用|读取|保留|同步|完成)"
    r"|的记录(?:也)?(?:走|进入|交给)"
    r"|的字段[^。！？；]{0,16}(?:补齐|补上|迁移|归一化)"
    r"|(?:字段|场景|用例|回归用例)[^。！？；]{0,16}(?:覆盖|补齐|通过)"
    r"|(?:由|被)[^。！？；]{0,16}(?:清除|保留|恢复|回退|同步)"
    r"|(?:单独|独立)[^。！？；]{0,12}(?:成|为|处理|保留|显示|一路|一条|通道)"
    r"|(?:回退|恢复|同步|保留|写回|迁移|使用|补上|填充|填入|作为(?:数据)?来源)[^。！？；]{0,20}"
    r")"
)


def validate_completeness(score, description, side):
    try:
        score_value = int(score)
    except (TypeError, ValueError):
        raise ValueError(f"{side} 交付完整性评分必须是 1 到 5 的整数")
    text = str(description or "").strip()
    if not 1 <= score_value <= 5:
        raise ValueError(f"{side} 交付完整性评分必须是 1 到 5 的整数")
    if not text:
        raise ValueError(f"{side} 交付完整性描述不能为空")
    text_without_positive_absence = COMPLETENESS_POSITIVE_ABSENCE.sub("", text)
    text_without_positive_absence = COMPLETENESS_POSITIVE_RETENTION.sub("", text_without_positive_absence)

    def remove_positive_coverage(match):
        candidate = match.group(0)
        if COMPLETENESS_NEGATIVE.search(candidate) or COMPLETENESS_UNRESOLVED_CONDITION.search(candidate):
            return candidate
        return ""

    text_without_positive_absence = COMPLETENESS_POSITIVE_COVERAGE.sub(
        remove_positive_coverage, text_without_positive_absence
    )
    text_without_positive_absence = COMPLETENESS_POSITIVE_CONSISTENCY.sub("", text_without_positive_absence)
    text_without_positive_absence = COMPLETENESS_POSITIVE_CONTINUATION.sub("", text_without_positive_absence)
    text_without_positive_absence = COMPLETENESS_POSITIVE_TEST_CHANGE.sub("", text_without_positive_absence)
    handled_state_removed = COMPLETENESS_HANDLED_STATE.sub("", text_without_positive_absence)
    handled = COMPLETENESS_HANDLED_CONDITION.sub("", handled_state_removed)
    if score_value == 5 and (
        COMPLETENESS_UNRESOLVED_CONDITION.search(text_without_positive_absence)
        or COMPLETENESS_NEGATIVE.search(handled)
        or any(COMPLETENESS_CONDITION.search(clause) for clause in re.split(r"[。！？；]", handled))
    ):
        raise ValueError(f"{side} 交付完整性为 5 分时，描述只能写已交付的正向依据，不能包含未完成缺口、未验证或证据边界")


def current_review(case):
    reviews = (case.get("pairwise") or {}).get("reviews") or []
    return reviews[-1] if reviews else {}


def captured_trace(case, run):
    capture_id = run.get("captureId")
    for capture in case.get("captures") or []:
        if capture.get("id") == capture_id and capture.get("tracePath"):
            return capture["tracePath"]
    return run.get("tracePath", "")


def row(case, payload):
    pairwise = case.get("pairwise") or {}
    run_a = pairwise.get("runA") or {}
    run_b = pairwise.get("runB") or {}
    review = current_review(case)
    return [
        pairwise.get("prompt", ""), case.get("taskType", ""), case.get("promptDifficulty", ""),
        pairwise.get("language", ""),
        pairwise.get("harness", ""), pairwise.get("harnessVersion", ""), pairwise.get("os", ""),
        pairwise.get("environment", ""), case.get("snapshotUrl", ""),
        run_a.get("sessionId", ""), captured_trace(case, run_a), run_a.get("deliverableUrl", ""),
        run_a.get("videoPath") or run_a.get("videoUrl", ""), run_b.get("sessionId", ""),
        captured_trace(case, run_b), run_b.get("deliverableUrl", ""),
        run_b.get("videoPath") or run_b.get("videoUrl", ""),
        review.get("aCompletenessScore", ""), review.get("aCompletenessDescription", ""),
        review.get("bCompletenessScore", ""), review.get("bCompletenessDescription", ""),
        CONCLUSIONS.get(review.get("conclusion"), ""), review.get("reason", ""),
        pairwise.get("validity", ""), pairwise.get("notes", ""),
    ]


def column_name(number):
    result = ""
    while number:
        number, remainder = divmod(number - 1, 26)
        result = chr(65 + remainder) + result
    return result


def inline_cell(row_number, column, value, style):
    reference = column_name(column + 1) + str(row_number)
    cell = ET.Element(NS + "c", {"r": reference, "s": str(style), "t": "inlineStr"})
    inline = ET.SubElement(cell, NS + "is")
    text = ET.SubElement(inline, NS + "t")
    value = str(value)
    if value[:1].isspace() or value[-1:].isspace() or "\n" in value or "\r" in value:
        text.set(XML_SPACE, "preserve")
    text.text = value
    return cell


def number_cell(row_number, column, value, style):
    reference = column_name(column + 1) + str(row_number)
    cell = ET.Element(NS + "c", {"r": reference, "s": str(style)})
    number = ET.SubElement(cell, NS + "v")
    number.text = str(value)
    return cell


def build_sheet(data, data_rows):
    root = ET.fromstring(data)
    sheet_data = root.find(NS + "sheetData")
    sheet_data.clear()

    header = ET.SubElement(sheet_data, NS + "row", {"r": "1", "ht": "36", "customHeight": "1"})
    for column, value in enumerate(HEADERS):
        header.append(inline_cell(1, column, value, 4))

    for row_number, values in enumerate(data_rows, 2):
        xml_row = ET.SubElement(sheet_data, NS + "row", {
            "r": str(row_number), "ht": "90", "customHeight": "1",
        })
        for column, value in enumerate(values):
            if value is not None and value != "":
                if column in (17, 19):
                    xml_row.append(number_cell(row_number, column, value, 6))
                else:
                    xml_row.append(inline_cell(row_number, column, value, 6))

    last_column = column_name(len(HEADERS))
    last_row = max(1, len(data_rows) + 1)
    root.find(NS + "dimension").set("ref", f"A1:{last_column}{last_row}")

    columns = root.find(NS + "cols")
    columns.clear()
    for index, width in enumerate(WIDTHS, 1):
        ET.SubElement(columns, NS + "col", {
            "width": str(width), "customWidth": "1", "min": str(index), "max": str(index),
        })

    auto_filter = root.find(NS + "autoFilter")
    if auto_filter is not None:
        auto_filter.set("ref", f"A1:{last_column}{last_row}")
    validations = root.find(NS + "dataValidations")
    if validations is not None:
        root.remove(validations)
    return ET.tostring(root, encoding="utf-8", xml_declaration=True)


def build_workbook(data_rows, destination):
    if not TEMPLATE.is_file():
        raise FileNotFoundError(f"bundled template is missing: {TEMPLATE}")
    with ZipFile(TEMPLATE, "r") as source:
        entries = [(item.filename, source.read(item.filename)) for item in source.infolist()]

    replacements = {}
    for filename, data in entries:
        if filename == SHEET_PATH:
            replacements[filename] = build_sheet(data, data_rows)
        elif filename == WORKBOOK_PATH:
            root = ET.fromstring(data)
            sheet = root.find(NS + "sheets/" + NS + "sheet")
            sheet.set("name", "Pair-wise GSB")
            defined_name = root.find(NS + "definedNames/" + NS + "definedName")
            if defined_name is not None:
                last_column = column_name(len(HEADERS))
                defined_name.text = f"'Pair-wise GSB'!$A$1:${last_column}${max(1, len(data_rows) + 1)}"
            replacements[filename] = ET.tostring(root, encoding="utf-8", xml_declaration=True)

    destination.parent.mkdir(parents=True, exist_ok=True)
    with ZipFile(destination, "w", ZIP_DEFLATED, compresslevel=9) as target:
        for filename, data in entries:
            info = ZipInfo(filename, FIXED_ZIP_TIME)
            info.compress_type = ZIP_DEFLATED
            info.external_attr = 0o600 << 16
            target.writestr(info, replacements.get(filename, data))


def export(payload, output):
    output.mkdir(parents=True, exist_ok=True)
    cases = payload.get("cases") or []
    for case in cases:
        review = current_review(case)
        validate_completeness(review.get("aCompletenessScore"), review.get("aCompletenessDescription"), "A")
        validate_completeness(review.get("bCompletenessScore"), review.get("bCompletenessDescription"), "B")
    data_rows = [row(case, payload) for case in cases]
    workbook_path = (output / "pairwise-gsb.xlsx").resolve()
    build_workbook(data_rows, workbook_path)

    issues = payload.get("issues") or []
    report_path = (output / "pairwise-report.md").resolve()
    lines = ["# Pair-wise GSB 导出报告", "", f"- 题目数：{len(payload.get('cases') or [])}", f"- 待补项：{len(issues)}"]
    if issues:
        lines.extend(["", "## 待补材料", ""] + [f"- {item}" for item in issues])
    report_path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    return {
        "outputPath": str(workbook_path),
        "reportPath": str(report_path),
        "rows": len(cases),
        "issues": issues,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--draft", action="store_true")
    args = parser.parse_args()
    try:
        payload = json.loads(Path(args.input).read_text(encoding="utf-8"))
        print(json.dumps(export(payload, Path(args.output)), ensure_ascii=False))
    except Exception as exc:
        print(json.dumps({"outputPath": "", "reportPath": "", "rows": 0, "issues": [str(exc)]}, ensure_ascii=False))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
