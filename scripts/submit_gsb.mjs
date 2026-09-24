#!/usr/bin/env node

/**
 * Submit every row in a Pair-wise GSB workbook to SOLO-QA.
 *
 * Safe default: validate and print the rows only.
 * Actual submission: add --submit. Add --yes for unattended runs.
 * Credentials are read from GSB_USERNAME and GSB_PASSWORD.
 */

import fs from 'node:fs/promises';
import fsSync from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import readline from 'node:readline/promises';
import { stdin as input, stdout as output } from 'node:process';

const SUBMIT_URL = 'https://solo2.jzxhnh.com/app/gsb/submit';
const REQUIRED_HEADERS = [
  'User Prompt',
  '任务类型',
  '任务难度',
  '语言/框架',
  'Harness',
  'Harness 版本',
  '操作系统',
  '环境可复现等级',
  '初始环境快照',
  'A-SessionID',
  'A-轨迹文件',
  'A-产物快照',
  'A-运行录屏',
  'B-SessionID',
  'B-轨迹文件',
  'B-产物快照',
  'B-运行录屏',
  'A-交付完整性',
  'A-交付完整性描述',
  'B-交付完整性',
  'B-交付完整性描述',
  'GSB 结论',
  'GSB 理由',
  '有效性',
  '备注',
];

const FILE_FIELDS = [
  ['A-轨迹文件', 'A-轨迹文件', 27 * 1024 * 1024, ['.jsonl']],
  ['A-运行录屏', 'A-运行录屏', 500 * 1024 * 1024, ['.mp4', '.mov', '.webm', '.m4v']],
  ['B-轨迹文件', 'B-轨迹文件', 27 * 1024 * 1024, ['.jsonl']],
  ['B-运行录屏', 'B-运行录屏', 500 * 1024 * 1024, ['.mp4', '.mov', '.webm', '.m4v']],
];

const PYTHON_READ_WORKBOOK = String.raw`
import json
import sys
from pathlib import Path
from openpyxl import load_workbook

request = json.load(sys.stdin)
workbook_path = Path(request["path"]).expanduser().resolve()
requested_sheet = request.get("sheet")
required = request["required_headers"]

wb = load_workbook(workbook_path, read_only=True, data_only=True)
sheet_names = [requested_sheet] if requested_sheet else wb.sheetnames
selected = None
selected_headers = None
for sheet_name in sheet_names:
    if sheet_name not in wb.sheetnames:
        continue
    ws = wb[sheet_name]
    headers = []
    for cell in next(ws.iter_rows(min_row=1, max_row=1)):
        value = cell.value
        headers.append("" if value is None else str(value).strip())
    if all(h in headers for h in required):
        selected = ws
        selected_headers = headers
        break

if selected is None:
    found = {}
    for sheet_name in wb.sheetnames:
        ws = wb[sheet_name]
        row = next(ws.iter_rows(min_row=1, max_row=1), ())
        headers = ["" if c.value is None else str(c.value).strip() for c in row]
        found[sheet_name] = headers
    raise RuntimeError(json.dumps({"message": "没有找到包含完整 GSB 表头的工作表", "sheets": found}, ensure_ascii=False))

rows = []
for excel_row, values in enumerate(selected.iter_rows(min_row=2, values_only=True), start=2):
    normalized = {}
    for index, header in enumerate(selected_headers):
        if not header:
            continue
        value = values[index] if index < len(values) else None
        normalized[header] = "" if value is None else str(value)
    if not any(str(value).strip() for value in normalized.values()):
        continue
    normalized["__excel_row"] = excel_row
    rows.append(normalized)

print(json.dumps({"sheet": selected.title, "rows": rows}, ensure_ascii=False))
`;

function parseArgs(argv) {
  const args = {
    excel: null,
    excelDir: process.env.GSB_EXCEL_DIR || '/Users/zqj/.pinru/annotation/exports',
    sheet: null,
    submit: false,
    yes: false,
    headless: false,
    startRow: null,
    limit: null,
    retry: false,
    stateFile: path.resolve('.gsb-submit-state.json'),
  };

  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--excel') args.excel = argv[++i];
    else if (arg === '--excel-dir') args.excelDir = argv[++i];
    else if (arg === '--sheet') args.sheet = argv[++i];
    else if (arg === '--submit') args.submit = true;
    else if (arg === '--yes') args.yes = true;
    else if (arg === '--headless') args.headless = true;
    else if (arg === '--start-row') args.startRow = Number(argv[++i]);
    else if (arg === '--limit') args.limit = Number(argv[++i]);
    else if (arg === '--retry') args.retry = true;
    else if (arg === '--state-file') args.stateFile = path.resolve(argv[++i]);
    else if (arg === '--help' || arg === '-h') {
      printHelp();
      process.exit(0);
    } else {
      throw new Error(`未知参数：${arg}`);
    }
  }

  if (args.startRow !== null && (!Number.isInteger(args.startRow) || args.startRow < 2)) {
    throw new Error('--start-row 必须是大于等于 2 的 Excel 行号');
  }
  if (args.limit !== null && (!Number.isInteger(args.limit) || args.limit < 1)) {
    throw new Error('--limit 必须是正整数');
  }
  return args;
}

function printHelp() {
  console.log(`用法：
  GSB_USERNAME=账号 GSB_PASSWORD=密码 node scripts/submit_gsb.mjs [选项]

默认只读取并校验 Excel，不会打开浏览器提交。

选项：
  --excel PATH       指定工作簿；省略时从 --excel-dir 递归选择最新的有效 GSB xlsx
  --excel-dir PATH   自动找 Excel 的目录，默认 /Users/zqj/.pinru/annotation/exports
  --sheet NAME       指定工作表；省略时自动寻找包含完整表头的工作表
  --submit           执行登录、填表、自动上传附件并提交
  --yes              跳过提交前确认，仅建议在人工核对过摘要后使用
  --headless         无头浏览器运行
  --start-row N      从 Excel 行号 N 开始
  --limit N          最多处理 N 行
  --retry            不使用成功状态文件跳过已提交行
  --state-file PATH  成功状态文件，默认 .gsb-submit-state.json
`);
}

function bundledPython() {
  const candidate = '/Users/zqj/.cache/codex-runtimes/codex-primary-runtime/dependencies/python/bin/python3';
  return fsSync.existsSync(candidate) ? candidate : 'python3';
}

function readWorkbookViaPython(workbookPath, sheet) {
  const request = JSON.stringify({ path: workbookPath, sheet, required_headers: REQUIRED_HEADERS });
  const result = spawnSync(bundledPython(), ['-c', PYTHON_READ_WORKBOOK], {
    input: request,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    throw new Error(`读取 Excel 失败：${(result.stderr || result.stdout || '').trim()}`);
  }
  return JSON.parse(result.stdout);
}

async function listXlsxFiles(root) {
  const result = [];
  async function visit(current, depth = 0) {
    if (depth > 6) return;
    let entries;
    try {
      entries = await fs.readdir(current, { withFileTypes: true });
    } catch {
      return;
    }
    for (const entry of entries) {
      if (entry.name.startsWith('~$') || entry.name === 'node_modules' || entry.name.startsWith('.')) continue;
      const fullPath = path.join(current, entry.name);
      if (entry.isDirectory()) await visit(fullPath, depth + 1);
      else if (entry.isFile() && entry.name.toLowerCase().endsWith('.xlsx')) {
        const stat = await fs.stat(fullPath);
        result.push({ path: fullPath, mtimeMs: stat.mtimeMs });
      }
    }
  }
  await visit(path.resolve(root));
  return result.sort((a, b) => b.mtimeMs - a.mtimeMs);
}

async function loadWorkbook(args) {
  if (args.excel) {
    const workbookPath = path.resolve(args.excel);
    const parsed = readWorkbookViaPython(workbookPath, args.sheet);
    return { path: workbookPath, ...parsed };
  }

  const candidates = await listXlsxFiles(args.excelDir);
  const failures = [];
  for (const candidate of candidates) {
    try {
      const parsed = readWorkbookViaPython(candidate.path, args.sheet);
      return { path: candidate.path, ...parsed };
    } catch (error) {
      failures.push(`${candidate.path}: ${error.message}`);
    }
  }
  const detail = failures.length ? `\n${failures.slice(0, 5).join('\n')}` : '';
  throw new Error(`在 ${path.resolve(args.excelDir)} 下没有找到有效的 GSB xlsx${detail}`);
}

function nonEmpty(value) {
  return String(value ?? '').trim();
}

const COMPLETENESS_FIELDS = [
  'A-交付完整性',
  'B-交付完整性',
];
const COMPLETENESS_NEGATIVE = /(?:未(?:验证|核验|执行|测试|覆盖|完成|实现|交付|达到|满足|通过)|没有(?:执行|测试|覆盖|完成|实现|交付|达到|满足|对应|相应)|缺少|缺乏|存在(?:功能)?(?:遗漏|问题|缺陷)|仍(?:有|会|未|无法|不能)|尚未|不足|遗漏|缺陷|不完整|不一致|未通过|返工|只(?:完成|实现)|仅(?:完成|实现)|证据不足|只能确认)/u;
const COMPLETENESS_CONDITION = /(?:失败|报错|异常|无法|不能|缺少|缺乏|不足|不一致|未通过|回归)/u;
const COMPLETENESS_UNRESOLVED_CONDITION = /(?:尚未|仍(?:有|会|未|无法|不能)|(?:无法|不能|未能)(?:恢复|完成|使用|读取|保存|迁移)(?:[。！？；，,]|$))/u;
const COMPLETENESS_POSITIVE_ABSENCE = /(?:没有|未|无)(?:(?:发现|出现|检测到|看到)[^。！？；，,]{0,12})?(?:任何|明显|实际|功能|关键|主要|重大)?(?:问题|缺陷|遗漏|不足|缺口|回归|未覆盖|未验证)/gu;
const COMPLETENESS_POSITIVE_RETENTION = /(?:不(?:抹掉|清除|覆盖|隐藏)|保留|继续保留)[^。！？；]{0,16}尚未恢复的[^。！？；]{0,12}(?:警告|提示|错误)/gu;
const COMPLETENESS_POSITIVE_COVERAGE = /(?:新增|补充|添加|完善)?(?:回归)?(?:测试|用例|测试文件|\.test\.(?:ts|tsx|js|jsx))[^。！？；]{0,32}覆盖[^。！？；]{0,240}/gu;
const COMPLETENESS_POSITIVE_CONSISTENCY = /(?:保持|避免|消除|不再|不会|不出现|不发生|防止)[^。！？；]{0,24}不一致/gu;
const COMPLETENESS_POSITIVE_CONTINUATION = /(?:仍(?:会|能|可)|依然|继续)[^。！？；]{0,24}(?:提交|持久化|保存|显示|恢复|可用|完成|保留|同步|写入|交付)/gu;
const COMPLETENESS_POSITIVE_TEST_CHANGE = /(?:[A-Za-z0-9_./-]+\.test\.(?:ts|tsx|js|jsx))[^。！？；]{0,80}(?:新增|补上|补充|添加|完善)[^。！？；]{0,80}(?:回归(?:测试|用例)|测试|用例)|(?:测试|用例)[^。！？；]{0,80}(?:新增|补上|补充|添加|完善)/gu;
const COMPLETENESS_HANDLED_STATE = /(?:未完成(?:拖拽|绘制|操作|手势|状态)|(?:拖拽|草稿|起点|终点|字段|障碍|网格|尺寸|宽度|高度|大小)?(?:缺少|缺失|不足))[^。！？；]{0,48}(?:收尾清理|清空|清掉|丢弃|取消|补齐|补上|填充|填入|补空|置空|设为空|给空|按默认|回退|恢复|同步|写回|走|处理|提示)/gu;
const COMPLETENESS_HANDLED_CONDITION = /[^。！？；]{0,80}(?:失败|报错|异常|无法|不能|缺少|缺乏|不足|不一致|未通过|回归)[^。！？；]{0,36}(?:后[^。！？；]{0,16}(?:成功|恢复|重试|回退|清除|消失|保留|同步|写入|显示|通过|完成|修正|修复)|时[^。！？；]{0,36}(?:补齐|补上|填充|填入|补空|置空|设为空|给空|按默认|回退|恢复|同步|保留|清除|写入|显示|转入|转为|落到|落回|改走|改为|切换到|不再当成|不再视为|走|改|仍|也|只|仅|成功|兜底|可以|能够|修正|修复|作为(?:数据)?来源)|(?:只|仅)(?:写|清|更新|刷新|改|保留|显示)|(?:也|仍)?(?:能|可以|能够)(?:恢复|回退|使用|读取|保留|同步|完成)|的记录(?:也)?(?:走|进入|交给)|的字段[^。！？；]{0,16}(?:补齐|补上|迁移|归一化)|(?:字段|场景|用例|回归用例)[^。！？；]{0,16}(?:覆盖|补齐|通过)|(?:由|被)[^。！？；]{0,16}(?:清除|保留|恢复|回退|同步)|(?:单独|独立)[^。！？；]{0,12}(?:成|为|处理|保留|显示|一路|一条|通道)|(?:回退|恢复|同步|保留|写回|迁移|使用|补上|填充|填入|作为(?:数据)?来源)[^。！？；]{0,20})/gu;

function completenessHasNegative(description) {
  let text = description;
  text = text.replace(COMPLETENESS_POSITIVE_ABSENCE, '');
  text = text.replace(COMPLETENESS_POSITIVE_RETENTION, '');
  text = text.replace(COMPLETENESS_POSITIVE_COVERAGE, (candidate) => (
    COMPLETENESS_NEGATIVE.test(candidate) || COMPLETENESS_UNRESOLVED_CONDITION.test(candidate) ? candidate : ''
  ));
  text = text.replace(COMPLETENESS_POSITIVE_CONSISTENCY, '');
  text = text.replace(COMPLETENESS_POSITIVE_CONTINUATION, '');
  text = text.replace(COMPLETENESS_POSITIVE_TEST_CHANGE, '');
  if (COMPLETENESS_UNRESOLVED_CONDITION.test(text)) return true;
  text = text.replace(COMPLETENESS_HANDLED_STATE, '');
  const handled = text.replace(COMPLETENESS_HANDLED_CONDITION, '');
  if (COMPLETENESS_NEGATIVE.test(handled)) return true;
  return handled.split(/[。！？；]/u).some((clause) => COMPLETENESS_CONDITION.test(clause));
}

function rowKey(row) {
  return [row['A-SessionID'], row['B-SessionID'], row['初始环境快照']].map(nonEmpty).join('|');
}

async function validateRows(rows, workbookPath) {
  const errors = [];
  for (const row of rows) {
    const excelRow = row.__excel_row;
    const missing = REQUIRED_HEADERS.filter((header) => header !== '备注' && !nonEmpty(row[header]));
    if (missing.length) errors.push(`Excel 第 ${excelRow} 行缺少必填值：${missing.join('、')}`);
    if (nonEmpty(row['A-SessionID']) === nonEmpty(row['B-SessionID'])) {
      errors.push(`Excel 第 ${excelRow} 行 A-SessionID 与 B-SessionID 相同`);
    }
    for (const header of COMPLETENESS_FIELDS) {
      const raw = nonEmpty(row[header]);
      if (!raw) continue;
      const score = Number(raw);
      if (!Number.isInteger(score) || score < 1 || score > 5) {
        errors.push(`Excel 第 ${excelRow} 行 ${header} 必须是 1 到 5 的整数：${raw}`);
      }
      const side = header.startsWith('A-') ? 'A' : 'B';
      const description = nonEmpty(row[`${side}-交付完整性描述`]);
      if (!description) {
        errors.push(`Excel 第 ${excelRow} 行 ${side}-交付完整性描述不能为空`);
      } else if (score === 5 && completenessHasNegative(description)) {
        errors.push(`Excel 第 ${excelRow} 行 ${side}-交付完整性为 5 分时，描述只能写已交付的正向依据，不能包含功能缺口、失败、未验证或证据边界`);
      }
    }

    for (const [header, , maxBytes, extensions] of FILE_FIELDS) {
      const raw = nonEmpty(row[header]);
      if (!raw) continue;
      const filePath = path.isAbsolute(raw) ? raw : path.resolve(path.dirname(workbookPath), raw);
      row[`__path_${header}`] = filePath;
      if (!fsSync.existsSync(filePath)) {
        errors.push(`Excel 第 ${excelRow} 行附件不存在：${filePath}`);
        continue;
      }
      const stat = await fs.stat(filePath);
      const extension = path.extname(filePath).toLowerCase();
      if (!extensions.includes(extension)) {
        errors.push(`Excel 第 ${excelRow} 行 ${header} 扩展名不支持：${extension || '(无)'}`);
      }
      if (stat.size > maxBytes) {
        errors.push(`Excel 第 ${excelRow} 行 ${header} 超过平台大小限制：${stat.size} bytes`);
      }
    }
  }
  if (errors.length) throw new Error(errors.join('\n'));
}

function printRows(workbook, rows) {
  console.log(`工作簿：${workbook.path}`);
  console.log(`工作表：${workbook.sheet}，数据行：${rows.length}`);
  for (const row of rows) {
    const prompt = nonEmpty(row['User Prompt']).replace(/\s+/g, ' ');
    console.log(`- Excel 第 ${row.__excel_row} 行 | ${row['GSB 结论']} | A=${row['A-SessionID']}(${row['A-交付完整性']}/5) | B=${row['B-SessionID']}(${row['B-交付完整性']}/5) | ${prompt.slice(0, 70)}${prompt.length > 70 ? '…' : ''}`);
  }
}

function loadState(filePath) {
  try {
    return JSON.parse(fsSync.readFileSync(filePath, 'utf8'));
  } catch {
    return { submitted: {} };
  }
}

async function saveState(filePath, state) {
  await fs.writeFile(filePath, `${JSON.stringify(state, null, 2)}\n`, 'utf8');
}

function loadPlaywright() {
  try {
    return createRequire(import.meta.url)('playwright');
  } catch (firstError) {
    const bundledModules = process.env.PLAYWRIGHT_NODE_MODULES || '/Users/zqj/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules';
    try {
      return createRequire(path.join(bundledModules, '__gsb_loader__.cjs'))('playwright');
    } catch {
      throw new Error(`找不到 Playwright。请安装 playwright，或设置 PLAYWRIGHT_NODE_MODULES。原始错误：${firstError.message}`);
    }
  }
}

async function waitForUpload(group, page, timeoutMs = 300_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const text = await group.innerText().catch(() => '');
    if (text && !text.includes('尚未上传') && !text.includes('上传中')) return;
    await page.waitForTimeout(500);
  }
  const text = await group.innerText().catch(() => '');
  throw new Error(`附件上传超时：${text}`);
}

async function fileInputForGroup(page, label, fallbackIndex) {
  const groups = page.locator('[role="group"]').filter({ hasText: label });
  if (await groups.count()) {
    const input = groups.first().locator('input[type="file"]');
    if (await input.count()) return { input, group: groups.first() };
  }
  const input = page.locator('input[type="file"]').nth(fallbackIndex);
  return { input, group: input.locator('xpath=..') };
}

function escapeRegex(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

async function chooseCombo(page, label, value) {
  const combo = page.getByRole('combobox', { name: label, exact: true });
  // The workbook and platform use different casing for some values, e.g.
  // "Feature迭代" in Excel vs. "feature迭代" in the platform.
  const option = page.getByRole('option', {
    name: new RegExp(`^${escapeRegex(nonEmpty(value))}$`, 'i'),
  });

  // Element Plus renders the combobox as a readonly input. Keyboard opening
  // avoids transparent placeholder layers and sticky headers intercepting a
  // mouse click when the form has been scrolled.
  await combo.scrollIntoViewIfNeeded();
  await combo.press('ArrowDown').catch(() => {});
  try {
    await option.waitFor({ state: 'visible', timeout: 5_000 });
  } catch {
    // Fallback for a browser/Element Plus combination where ArrowDown does
    // not open the popup.
    const wrapper = combo.locator('xpath=ancestor::*[contains(@class, "el-select")][1]');
    if (await wrapper.count()) await wrapper.click({ force: true });
    else await combo.click({ force: true });
    try {
      await option.waitFor({ state: 'visible', timeout: 5_000 });
    } catch {
      throw new Error(`下拉框“${label}”未打开或没有选项“${value}”`);
    }
  }
  await option.click();
}

async function visible(locator) {
  return locator.isVisible().catch(() => false);
}

async function pageDiagnostic(page) {
  const url = await page.url().catch(() => '(unknown)');
  const title = await page.title().catch(() => '(unknown)');
  const body = await page.locator('body').innerText().catch(() => '');
  return `URL: ${url}\n标题: ${title}\n页面文字:\n${body.slice(0, 2500)}`;
}

async function waitForLoginOrSubmit(page, timeoutMs = 30_000) {
  const loginBox = page.getByPlaceholder('请输入账号');
  const submitHeading = page.getByRole('heading', { name: '提交 GSB 数据', exact: true });
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await visible(submitHeading)) return 'submit';
    if (await visible(loginBox)) return 'login';
    await page.waitForTimeout(250);
  }
  throw new Error(`平台页面未进入登录页或提交页。\n${await pageDiagnostic(page)}`);
}

async function waitForSubmitPage(page, timeoutMs = 30_000) {
  const submitHeading = page.getByRole('heading', { name: '提交 GSB 数据', exact: true });
  try {
    await submitHeading.waitFor({ state: 'visible', timeout: timeoutMs });
  } catch {
    throw new Error(`登录后没有进入提交 GSB 页面。\n${await pageDiagnostic(page)}`);
  }
}

async function fillRow(page, row) {
  const text = (label) => page.getByRole('textbox', { name: label, exact: true });
  const score = (label) => page.getByRole('spinbutton', { name: label, exact: true });
  const optionalText = async (label, value) => {
    const field = text(label);
    if (await field.count()) await field.fill(nonEmpty(value));
  };
  await text('User Prompt').fill(nonEmpty(row['User Prompt']));
  await chooseCombo(page, '任务类型', nonEmpty(row['任务类型']));
  await chooseCombo(page, '任务难度', nonEmpty(row['任务难度']));
  await text('语言/框架').fill(nonEmpty(row['语言/框架']));
  await chooseCombo(page, 'Harness', nonEmpty(row['Harness']));
  await text('Harness 版本').fill(nonEmpty(row['Harness 版本']));
  await chooseCombo(page, '操作系统', nonEmpty(row['操作系统']));
  await chooseCombo(page, '环境可复现等级', nonEmpty(row['环境可复现等级']));
  await text('初始环境快照').fill(nonEmpty(row['初始环境快照']));
  await text('A-SessionID').fill(nonEmpty(row['A-SessionID']));
  await text('A-产物快照').fill(nonEmpty(row['A-产物快照']));
  await score('A-交付完整性').fill(nonEmpty(row['A-交付完整性']));
  await text('A-交付完整性描述').fill(nonEmpty(row['A-交付完整性描述']));
  await text('B-SessionID').fill(nonEmpty(row['B-SessionID']));
  await text('B-产物快照').fill(nonEmpty(row['B-产物快照']));
  await score('B-交付完整性').fill(nonEmpty(row['B-交付完整性']));
  await text('B-交付完整性描述').fill(nonEmpty(row['B-交付完整性描述']));
  await chooseCombo(page, 'GSB 结论', nonEmpty(row['GSB 结论']));
  await text('GSB 理由').fill(nonEmpty(row['GSB 理由']));
  // 当前质检平台提交页没有“备注”控件；Excel 中的备注仅作为本地导出信息保留。
  await optionalText('备注', row['备注']);

  for (const [index, [header, label]] of FILE_FIELDS.entries()) {
    const filePath = row[`__path_${header}`];
    const { input, group } = await fileInputForGroup(page, label, index);
    await input.setInputFiles(filePath);
    await waitForUpload(group, page);
    console.log(`  已上传 ${header}：${path.basename(filePath)}`);
  }
}

async function loginAndOpen(page, username, password) {
  await page.goto(SUBMIT_URL, { waitUntil: 'domcontentloaded' });
  const pageState = await waitForLoginOrSubmit(page);
  if (pageState === 'login') {
    const loginBox = page.getByPlaceholder('请输入账号');
    await loginBox.fill(username);
    await page.getByPlaceholder('请输入密码').fill(password);
    await page.getByRole('button', { name: '登 录' }).click();
    await waitForSubmitPage(page);
  }
  if (await page.getByText('修改初始密码').count()) {
    throw new Error('账号被要求修改初始密码，请先在平台网页中完成修改后再运行脚本');
  }
}

async function submitOne(page, row) {
  await page.goto(SUBMIT_URL, { waitUntil: 'domcontentloaded' });
  await waitForSubmitPage(page);
  await fillRow(page, row);
  await page.getByRole('button', { name: '提交并质检', exact: true }).click();
  let bodyText = '';
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    bodyText = await page.locator('body').innerText();
    const success = /(提交成功|质检通过|已提交|质检结果)/.test(bodyText);
    const failure = /(提交失败|上传失败|校验失败|请填写[^。\n]*|请上传[^。\n]*)/.test(bodyText);
    if (success || failure) break;
    await page.waitForTimeout(1_000);
  }
  const success = /(提交成功|质检通过|已提交|质检结果)/.test(bodyText);
  const failure = /(提交失败|上传失败|校验失败|请填写[^。\n]*|请上传[^。\n]*)/.test(bodyText);
  if (!success || failure) {
    throw new Error(`平台没有确认成功，请检查页面。\n${bodyText.slice(-3000)}`);
  }
}

async function confirmSubmission(rows) {
  console.log(`\n即将向 ${SUBMIT_URL} 提交 ${rows.length} 行数据，并自动上传每行的 4 个附件。`);
  console.log('请确认 Excel 内容、附件和 GSB 账号均正确。');
  const rl = readline.createInterface({ input, output });
  const answer = await rl.question('输入 SUBMIT 才开始：');
  rl.close();
  if (answer.trim() !== 'SUBMIT') throw new Error('未输入 SUBMIT，已取消提交');
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const workbook = await loadWorkbook(args);
  let rows = workbook.rows;
  if (args.startRow !== null) rows = rows.filter((row) => row.__excel_row >= args.startRow);
  if (args.limit !== null) rows = rows.slice(0, args.limit);
  if (!rows.length) throw new Error('没有待处理的数据行');

  await validateRows(rows, workbook.path);
  printRows(workbook, rows);
  if (!args.submit) {
    console.log('\n校验完成。默认未提交；如确认无误，请加 --submit 执行浏览器自动化。');
    return;
  }

  if (!process.env.GSB_USERNAME || !process.env.GSB_PASSWORD) {
    throw new Error('提交模式需要设置 GSB_USERNAME 和 GSB_PASSWORD 环境变量，不把密码写进脚本。');
  }
  if (!args.yes) await confirmSubmission(rows);

  const state = loadState(args.stateFile);
  const pending = args.retry ? rows : rows.filter((row) => !state.submitted[rowKey(row)]);
  if (!pending.length) {
    console.log('状态文件显示这些行都已成功提交。需要重新提交时加 --retry。');
    return;
  }
  if (pending.length !== rows.length) console.log(`已跳过 ${rows.length - pending.length} 行成功记录。`);

  const { chromium } = loadPlaywright();
  const executablePath = process.env.GSB_BROWSER_EXECUTABLE;
  const launchOptions = { headless: args.headless };
  if (executablePath) launchOptions.executablePath = executablePath;
  else if (process.platform === 'darwin' && fsSync.existsSync('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')) {
    launchOptions.executablePath = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
  }

  const browser = await chromium.launch(launchOptions);
  const context = await browser.newContext();
  const page = await context.newPage();
  try {
    await loginAndOpen(page, process.env.GSB_USERNAME, process.env.GSB_PASSWORD);
    for (const row of pending) {
      console.log(`\n开始提交 Excel 第 ${row.__excel_row} 行：A=${row['A-SessionID']}，B=${row['B-SessionID']}`);
      await submitOne(page, row);
      state.submitted[rowKey(row)] = { excelRow: row.__excel_row, submittedAt: new Date().toISOString() };
      await saveState(args.stateFile, state);
      console.log(`提交成功：Excel 第 ${row.__excel_row} 行`);
    }
  } finally {
    await browser.close();
  }
}

main().catch((error) => {
  console.error(`\n${error.message}`);
  process.exitCode = 1;
});
