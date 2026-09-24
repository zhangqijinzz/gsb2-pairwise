import { FileArchive, FileText, FolderSearch, GitBranch, Info, Loader2, RefreshCw, Scale, Sparkles, X } from 'lucide-react';
import { useEffect, useMemo, useState, type ComponentType, type ReactNode } from 'react';
import type {
  CustomProjectCandidateScanResult,
  ImportLocalSourcesResult,
  NormalizeManagedSourceFoldersResult,
  QuestionBankSyncResult,
} from '../../../api/git';
import type {
  CustomProjectPromptDocumentDetail,
  CustomPromptCounts,
  GenerateCustomProjectPromptDocumentsResult,
} from '../../../api/llm';
import type { CreateTasksFromCustomPromptDocumentsResult } from '../../../api/task';

type ActionState = 'idle' | 'running' | 'ok' | 'warn' | 'error';

const STATE_DOT: Record<ActionState, string> = {
  idle: 'bg-stone-300 dark:bg-stone-600',
  running: 'bg-amber-400 animate-pulse',
  ok: 'bg-emerald-500',
  warn: 'bg-orange-400',
  error: 'bg-red-500',
};

function QuickAction({
  icon: Icon,
  label,
  explain,
  state,
  disabled,
  onClick,
  meta,
}: {
  icon: ComponentType<{ className?: string }>;
  label: string;
  explain: string;
  state: ActionState;
  disabled?: boolean;
  onClick: () => void;
  meta?: ReactNode;
}) {
  const running = state === 'running';
  return (
    <div className="flex flex-col items-start gap-1">
      <div className="inline-flex items-center">
        <button
          type="button"
          onClick={onClick}
          disabled={disabled || running}
          className="group inline-flex items-center gap-1.5 rounded-full border border-stone-200 bg-white/80 pl-2.5 pr-3 py-1.5 text-[12px] font-medium text-stone-700 transition-colors hover:border-stone-300 hover:bg-stone-50 hover:text-stone-900 disabled:cursor-not-allowed disabled:opacity-50 dark:border-stone-700/70 dark:bg-stone-900/50 dark:text-stone-300 dark:hover:border-stone-600 dark:hover:bg-stone-800/60 dark:hover:text-stone-100 cursor-default"
        >
          {running ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin text-amber-500" />
          ) : (
            <Icon className="h-3.5 w-3.5 text-stone-400 group-hover:text-stone-600 dark:text-stone-500 dark:group-hover:text-stone-300" />
          )}
          <span>{label}</span>
          <span className={`ml-0.5 inline-block h-1.5 w-1.5 rounded-full ${STATE_DOT[state]}`} aria-hidden />
        </button>
        <span
          tabIndex={0}
          role="note"
          aria-label={`${label}：${explain}`}
          className="group/tip relative ml-1 inline-flex h-6 w-6 items-center justify-center rounded-full text-stone-400 transition-colors hover:bg-stone-100 hover:text-stone-600 focus:bg-stone-100 focus:text-stone-600 focus:outline-none dark:text-stone-500 dark:hover:bg-stone-800/60 dark:hover:text-stone-300 dark:focus:bg-stone-800/60 dark:focus:text-stone-300 cursor-help"
        >
          <Info className="h-3.5 w-3.5" />
          <span
            role="tooltip"
            className="pointer-events-none absolute bottom-full left-1/2 z-50 mb-1.5 w-max max-w-[240px] -translate-x-1/2 translate-y-1 rounded-md bg-stone-900 px-2.5 py-1.5 text-[11px] font-normal leading-snug text-stone-100 opacity-0 shadow-lg transition-[opacity,transform] duration-75 group-hover/tip:translate-y-0 group-hover/tip:opacity-100 group-focus/tip:translate-y-0 group-focus/tip:opacity-100 dark:bg-stone-100 dark:text-stone-900"
          >
            {explain}
            <span
              aria-hidden
              className="absolute left-1/2 top-full -translate-x-1/2 border-4 border-transparent border-t-stone-900 dark:border-t-stone-100"
            />
          </span>
        </span>
      </div>
      {meta && (
        <div className="pl-2.5 text-[11px] leading-snug text-stone-500 dark:text-stone-400">
          {meta}
        </div>
      )}
    </div>
  );
}

export function SyncToolbar({
  importingLocalSources,
  localImportError,
  localImportResult,
  onScan,
  customProjectImporting,
  customProjectScanLoading,
  customProjectImportError,
  customProjectImportResult,
  customProjectPromptDocGenerating,
  customProjectPromptDocError,
  customProjectPromptDocResult,
  customPromptTaskCreating,
  customPromptTaskError,
  customPromptTaskResult,
  onScanCustomProjects,
  onCreateTasksFromGeneratedPromptDocs,
  onCreateTasksFromPickedPromptDocs,
  onImportArchives,
  syncing,
  syncError,
  syncResult,
  configuredGitLabQuestionIds,
  onSync,
  normalizing,
  normalizeError,
  normalizeResult,
  onNormalize,
}: {
  importingLocalSources: boolean;
  localImportError: string;
  localImportResult: ImportLocalSourcesResult | null;
  onScan: () => void;
  customProjectImporting: boolean;
  customProjectScanLoading: boolean;
  customProjectImportError: string;
  customProjectImportResult: ImportLocalSourcesResult | null;
  customProjectPromptDocGenerating: boolean;
  customProjectPromptDocError: string;
  customProjectPromptDocResult: GenerateCustomProjectPromptDocumentsResult | null;
  customPromptTaskCreating: boolean;
  customPromptTaskError: string;
  customPromptTaskResult: CreateTasksFromCustomPromptDocumentsResult | null;
  onScanCustomProjects: () => void;
  onCreateTasksFromGeneratedPromptDocs: () => void;
  onCreateTasksFromPickedPromptDocs: () => void;
  onImportArchives: () => void;
  syncing: boolean;
  syncError: string;
  syncResult: QuestionBankSyncResult | null;
  configuredGitLabQuestionIds: number[];
  onSync: () => void;
  normalizing: boolean;
  normalizeError: string;
  normalizeResult: NormalizeManagedSourceFoldersResult | null;
  onNormalize: () => void;
}) {
  const localState: ActionState = importingLocalSources
    ? 'running'
    : localImportError
      ? 'error'
      : localImportResult
        ? localImportResult.errorCount > 0
          ? 'warn'
          : localImportResult.importedCount > 0 || localImportResult.removedCount > 0
            ? 'ok'
            : 'idle'
        : 'idle';

  const gitlabState: ActionState = syncing
    ? 'running'
    : syncError
      ? 'error'
      : syncResult
        ? syncResult.errorCount > 0
          ? 'warn'
          : syncResult.syncedCount > 0
            ? 'ok'
            : 'idle'
        : 'idle';

  const customProjectState: ActionState = customProjectScanLoading || customProjectImporting || customProjectPromptDocGenerating
    ? 'running'
    : customProjectImportError || customProjectPromptDocError
      ? 'error'
      : customProjectImportResult
        ? customProjectImportResult.errorCount > 0
          ? 'warn'
          : customProjectImportResult.importedCount > 0
            ? 'ok'
            : 'idle'
        : 'idle';

  const normalizeState: ActionState = normalizing
    ? 'running'
    : normalizeError
      ? 'error'
      : normalizeResult
        ? normalizeResult.errorCount > 0
          ? 'warn'
          : normalizeResult.renamedCount + normalizeResult.updatedCount > 0
            ? 'ok'
            : 'idle'
        : 'idle';

  const customPromptTaskState: ActionState = customPromptTaskCreating
    ? 'running'
    : customPromptTaskError
      ? 'error'
      : customPromptTaskResult
        ? customPromptTaskResult.errorCount > 0
          ? 'warn'
          : customPromptTaskResult.createdCount > 0
            ? 'ok'
            : 'idle'
        : 'idle';

  const gitlabConfigured = configuredGitLabQuestionIds.length > 0;
  const gitlabExplain = gitlabConfigured
    ? `从 ${configuredGitLabQuestionIds.length} 个已配置的 GitLab 题目 ID 拉取最新题库到本地`
    : '未配置题目 ID，请在下方「GitLab 题库」卡片中添加';

  const localMeta = localImportError ? (
    <span className="text-red-500">{localImportError}</span>
  ) : localImportResult ? (
    <span>
      入库 <b className="text-stone-700 dark:text-stone-300">{localImportResult.importedCount}</b>
      <span className="mx-1 text-stone-300 dark:text-stone-700">·</span>
      跳过 {localImportResult.skippedCount}
      {localImportResult.removedCount > 0 && (
        <>
          <span className="mx-1 text-stone-300 dark:text-stone-700">·</span>
          清理 {localImportResult.removedCount}
        </>
      )}
      {localImportResult.errorCount > 0 && (
        <>
          <span className="mx-1 text-stone-300 dark:text-stone-700">·</span>
          <span className="text-red-500">错误 {localImportResult.errorCount}</span>
        </>
      )}
    </span>
  ) : null;

  const gitlabMeta = syncError ? (
    <span className="text-red-500">{syncError}</span>
  ) : syncResult ? (
    <span>
      同步 <b className="text-stone-700 dark:text-stone-300">{syncResult.syncedCount}</b>
      <span className="mx-1 text-stone-300 dark:text-stone-700">·</span>
      跳过 {syncResult.skippedCount}
      {syncResult.errorCount > 0 && (
        <>
          <span className="mx-1 text-stone-300 dark:text-stone-700">·</span>
          <span className="text-red-500">错误 {syncResult.errorCount}</span>
        </>
      )}
    </span>
  ) : gitlabConfigured ? (
    <span>已配置 {configuredGitLabQuestionIds.length} 个题目 ID</span>
  ) : null;

  const customProjectMeta = customProjectImportError ? (
    <span className="text-red-500">{customProjectImportError}</span>
  ) : customProjectPromptDocError ? (
    <span className="text-red-500">{customProjectPromptDocError}</span>
  ) : customProjectPromptDocResult ? (
    <span>
      文档 <b className="text-stone-700 dark:text-stone-300">{customProjectPromptDocResult.generatedCount}</b>
      {customProjectPromptDocResult.errorCount > 0 && (
        <>
          <span className="mx-1 text-stone-300 dark:text-stone-700">·</span>
          <span className="text-red-500">错误 {customProjectPromptDocResult.errorCount}</span>
        </>
      )}
    </span>
  ) : customProjectImportResult ? (
    <span>
      入库 <b className="text-stone-700 dark:text-stone-300">{customProjectImportResult.importedCount}</b>
      <span className="mx-1 text-stone-300 dark:text-stone-700">·</span>
      跳过 {customProjectImportResult.skippedCount}
      {customProjectImportResult.errorCount > 0 && (
        <>
          <span className="mx-1 text-stone-300 dark:text-stone-700">·</span>
          <span className="text-red-500">错误 {customProjectImportResult.errorCount}</span>
          {customProjectImportResult.details
            .filter((detail) => detail.status === 'error')
            .map((detail) => (
              <span key={`${detail.name}:${detail.path}`} className="ml-1 text-red-500">
                {detail.name}：{detail.message}
              </span>
            ))}
        </>
      )}
    </span>
  ) : null;

  const normalizeMeta = normalizeError ? (
    <span className="text-red-500">{normalizeError}</span>
  ) : normalizeResult ? (
    <span>
      扫描 {normalizeResult.totalTasks}
      <span className="mx-1 text-stone-300 dark:text-stone-700">·</span>
      重命名 {normalizeResult.renamedCount}
      <span className="mx-1 text-stone-300 dark:text-stone-700">·</span>
      补 Git {normalizeResult.gitInitializedCount}
      {normalizeResult.errorCount > 0 && (
        <>
          <span className="mx-1 text-stone-300 dark:text-stone-700">·</span>
          <span className="text-red-500">错误 {normalizeResult.errorCount}</span>
        </>
      )}
    </span>
  ) : null;

  const customPromptTaskMeta = customPromptTaskError ? (
    <span className="text-red-500">{customPromptTaskError}</span>
  ) : customPromptTaskResult ? (
    <span>
      创建 <b className="text-stone-700 dark:text-stone-300">{customPromptTaskResult.createdCount}</b>
      {customPromptTaskResult.errorCount > 0 && (
        <>
          <span className="mx-1 text-stone-300 dark:text-stone-700">·</span>
          <span className="text-red-500">错误 {customPromptTaskResult.errorCount}</span>
        </>
      )}
    </span>
  ) : null;

  return (
    <div className="flex flex-col gap-4 rounded-xl border border-stone-200 bg-white/60 px-4 py-3.5 dark:border-stone-800 dark:bg-stone-900/40 md:flex-row md:items-start md:justify-between">
      {/* 主操作：导入压缩包（需要用户选文件，保留为醒目 CTA） */}
      <div className="flex items-start gap-3">
        <div className="flex h-9 w-9 flex-none items-center justify-center rounded-lg bg-stone-100 text-stone-600 dark:bg-stone-800 dark:text-stone-300">
          <FileArchive className="h-4 w-4" />
        </div>
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-[13px] font-semibold text-stone-900 dark:text-stone-100">
              导入压缩包
            </span>
            <span className={`inline-block h-1.5 w-1.5 rounded-full ${STATE_DOT[localState]}`} aria-hidden />
          </div>
          <p className="mt-0.5 text-[11px] leading-snug text-stone-500 dark:text-stone-400">
            选择本地 .zip / .7z 文件拷入项目并入库
          </p>
          <button
            type="button"
            onClick={onImportArchives}
            disabled={importingLocalSources}
            className="mt-2 inline-flex items-center justify-center gap-1.5 rounded-lg border border-stone-900 bg-stone-900 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-stone-800 disabled:opacity-50 dark:border-stone-100 dark:bg-stone-100 dark:text-stone-900 dark:hover:bg-stone-200 cursor-default"
          >
            {importingLocalSources && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {importingLocalSources ? '导入中' : '选择压缩包'}
          </button>
        </div>
      </div>

      {/* 次级操作：不需要输入参数，做成一排精致的小胶囊按钮 */}
      <div className="flex flex-col gap-2.5 md:items-end">
        <div className="flex flex-wrap gap-2 md:justify-end">
          <QuickAction
            icon={Sparkles}
            label="刷新自定义项目"
            explain="扫描全局自定义项目根目录第一层匹配前缀的文件夹，列出当前项目还没有的项目"
            state={customProjectState}
            onClick={onScanCustomProjects}
          />
          <QuickAction
            icon={FileText}
            label="文档建任务"
            explain="读取已审核的自定义项目提示词 md，按分类创建任务并写入任务提示词"
            state={customPromptTaskState}
            onClick={onCreateTasksFromPickedPromptDocs}
          />
          {customProjectPromptDocResult && customProjectPromptDocResult.generatedCount > 0 && (
            <QuickAction
              icon={FileText}
              label="用新文档建任务"
              explain="使用本次刚生成的提示词文档创建任务"
              state={customPromptTaskState}
              onClick={onCreateTasksFromGeneratedPromptDocs}
            />
          )}
          <QuickAction
            icon={FolderSearch}
            label="重新扫描"
            explain="扫描项目根目录下已存在的压缩包与源码目录，将未入库的题目补入本地题库"
            state={localState}
            onClick={onScan}
          />
          <QuickAction
            icon={GitBranch}
            label="同步 GitLab"
            explain={gitlabExplain}
            state={gitlabState}
            disabled={!gitlabConfigured}
            onClick={onSync}
          />
          <QuickAction
            icon={Scale}
            label="归一"
            explain="按统一命名规则重命名任务/源码目录，并为缺失的目录补齐 .git 仓库"
            state={normalizeState}
            onClick={onNormalize}
          />
        </div>
        {(localMeta || customProjectMeta || customPromptTaskMeta || gitlabMeta || normalizeMeta) && (
          <div className="flex flex-wrap gap-x-4 gap-y-1 text-[11px] leading-snug text-stone-500 dark:text-stone-400 md:justify-end">
            {localMeta && <span>扫描 · {localMeta}</span>}
            {customProjectMeta && <span>自定义 · {customProjectMeta}</span>}
            {customPromptTaskMeta && <span>文档任务 · {customPromptTaskMeta}</span>}
            {gitlabMeta && <span>GitLab · {gitlabMeta}</span>}
            {normalizeMeta && <span>归一 · {normalizeMeta}</span>}
          </div>
        )}
      </div>
    </div>
  );
}

export function CustomProjectPickerModal({
  open,
  scanResult,
  importing,
  promptDocGenerating,
  error,
  promptDocError,
  onClose,
  onImport,
}: {
  open: boolean;
  scanResult: CustomProjectCandidateScanResult | null;
  importing: boolean;
  promptDocGenerating: boolean;
  error: string;
  promptDocError: string;
  onClose: () => void;
  onImport: (projectNames: string[], counts: CustomPromptCounts) => void;
}) {
  const candidates = scanResult?.candidates ?? [];
  const prefixLabel = formatCustomProjectPrefixes(scanResult?.prefixes);
  const [selectedNames, setSelectedNames] = useState<string[]>([]);
  const [quantities, setQuantities] = useState({ codeGen: '10', feature: '10', bugFix: '2' });
  const [difficultyQuantities, setDifficultyQuantities] = useState({ difficult: '20', hell: '2' });
  const counts: CustomPromptCounts = {
    codeGen: Number(quantities.codeGen),
    feature: Number(quantities.feature),
    bugFix: Number(quantities.bugFix),
    difficult: Number(difficultyQuantities.difficult),
    hell: Number(difficultyQuantities.hell),
  };
  const totalCount = counts.codeGen + counts.feature + counts.bugFix;
  const difficultyTotal = counts.difficult + counts.hell;
  const allQuantityValues = [
    quantities.codeGen,
    quantities.feature,
    quantities.bugFix,
    difficultyQuantities.difficult,
    difficultyQuantities.hell,
  ];
  const parsedCounts = [counts.codeGen, counts.feature, counts.bugFix, counts.difficult, counts.hell];
  const validCountInputs = allQuantityValues.every((value) => /^\d+$/.test(value))
    && parsedCounts.every((n) => Number.isSafeInteger(n) && n >= 0)
    && Number.isSafeInteger(totalCount)
    && Number.isSafeInteger(difficultyTotal);
  const difficultyAllocationMatches = validCountInputs && difficultyTotal === totalCount;
  const validCounts = validCountInputs && difficultyAllocationMatches;
  const selectedNameSet = useMemo(() => new Set(selectedNames), [selectedNames]);

  useEffect(() => {
    if (open) {
      setSelectedNames([]);
    }
  }, [open, scanResult]);

  if (!open || !scanResult) {
    return null;
  }

  const toggleProject = (name: string) => {
    setSelectedNames((current) =>
      current.includes(name)
        ? current.filter((item) => item !== name)
        : [...current, name],
    );
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/35 px-3 sm:px-4 py-3 sm:py-8 backdrop-blur-sm">
      <div className="flex max-h-[92vh] w-full max-w-3xl flex-col rounded-2xl border border-stone-200 bg-white shadow-2xl dark:border-stone-800 dark:bg-stone-950">
        <div className="flex items-start justify-between gap-4 border-b border-stone-100 px-4 sm:px-5 py-3 sm:py-4 dark:border-stone-850">
          <div className="min-w-0">
            <h3 className="text-base font-semibold text-stone-900 dark:text-stone-100">
              选择要导入的自定义项目
            </h3>
            <p className="mt-1 text-xs leading-relaxed text-stone-500 dark:text-stone-400">
              已扫描 {scanResult.totalCount} 个 {prefixLabel} 文件夹，当前项目还可导入 {candidates.length} 个。
            </p>
            <p className="mt-1 truncate text-[11px] text-stone-400 dark:text-stone-500">
              {scanResult.rootPath}
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={importing || promptDocGenerating}
            className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-stone-400 transition-colors hover:bg-stone-100 hover:text-stone-700 disabled:opacity-50 dark:hover:bg-stone-800 dark:hover:text-stone-200 cursor-default"
            aria-label="关闭"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto px-4 sm:px-5 py-3 sm:py-4">
          {(error || promptDocError) && (
            <div className="mb-3 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-600 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
              {error || promptDocError}
            </div>
          )}
          <fieldset className="mb-4 rounded-xl border border-stone-200 p-3 dark:border-stone-700" disabled={importing || promptDocGenerating}>
            <legend className="px-1 text-sm font-semibold dark:text-stone-100">每个项目生成数量</legend>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
              {([['codeGen', '0-1代码生成'], ['feature', 'Feature迭代'], ['bugFix', 'Bug修复']] as const).map(([key, label]) => (
                <label key={key} className="text-xs text-stone-600 dark:text-stone-300">
                  {label}
                  <input
                    type="number" min="0" step="1" value={quantities[key]}
                    onChange={(event) => setQuantities((current) => ({ ...current, [key]: event.target.value }))}
                    className="mt-1 w-full rounded-lg border border-stone-300 bg-transparent p-2 dark:border-stone-700"
                  />
                </label>
              ))}
              {['代码理解', '代码测试', '代码重构'].map((label) => (
                <label key={label} className="text-xs text-stone-500">
                  {label}
                  <input type="number" value={0} disabled className="mt-1 w-full rounded-lg border border-stone-200 bg-stone-100 p-2 dark:border-stone-700 dark:bg-stone-800" />
                </label>
              ))}
            </div>
            <div className="mt-4 border-t border-stone-200 pt-3 dark:border-stone-700">
              <p className="mb-2 text-xs font-medium text-stone-700 dark:text-stone-200">难度数量分配</p>
              <div className="grid grid-cols-2 gap-3">
                {([['difficult', '困难'], ['hell', '地狱']] as const).map(([key, label]) => (
                  <label key={key} className="text-xs text-stone-600 dark:text-stone-300">
                    {label}
                    <input
                      type="number" min="0" step="1" value={difficultyQuantities[key]}
                      onChange={(event) => setDifficultyQuantities((current) => ({ ...current, [key]: event.target.value }))}
                      className="mt-1 w-full rounded-lg border border-stone-300 bg-transparent p-2 dark:border-stone-700"
                    />
                  </label>
                ))}
              </div>
            </div>
            <p className="mt-2 text-xs text-stone-500">
              前三类可输入非负整数，填 0 表示不生成；其他题型固定为 0，不参与生成。
              {!validCountInputs
                ? '请输入有效的非负整数。'
                : difficultyAllocationMatches
                  ? `每个项目共 ${totalCount} 题，其中困难 ${counts.difficult} 题、地狱 ${counts.hell} 题，难度下限为困难。`
                  : `难度数量合计 ${difficultyTotal} 题，与题型总数 ${totalCount} 题不一致。`}
            </p>
          </fieldset>
          {candidates.length === 0 ? (
            <div className="rounded-xl border border-dashed border-stone-200 px-4 py-8 text-center text-sm text-stone-500 dark:border-stone-800 dark:text-stone-400">
              没有可导入的新项目。
            </div>
          ) : (
            <div className="space-y-2">
              {candidates.map((candidate) => {
                const checked = selectedNameSet.has(candidate.name);
                return (
                  <label
                    key={candidate.name}
                    className="flex cursor-default items-start gap-3 rounded-xl border border-stone-200 bg-stone-50/70 px-3 py-3 transition-colors hover:border-stone-300 hover:bg-stone-100/70 dark:border-stone-800 dark:bg-stone-900/50 dark:hover:border-stone-700 dark:hover:bg-stone-900"
                  >
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={() => toggleProject(candidate.name)}
                      disabled={importing || promptDocGenerating}
                      className="mt-1 h-4 w-4 shrink-0 rounded border-stone-300 text-stone-900 focus:ring-stone-400 dark:border-stone-700 dark:bg-stone-950"
                    />
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="text-sm font-semibold text-stone-900 dark:text-stone-100">
                          {candidate.name}
                        </span>
                      </div>
                      <p className="mt-1 truncate text-xs text-stone-500 dark:text-stone-400">
                        {candidate.path}
                      </p>
                    </div>
                  </label>
                );
              })}
            </div>
          )}
        </div>

        <div className="flex items-center justify-between gap-3 border-t border-stone-100 px-4 sm:px-5 py-3 sm:py-4 dark:border-stone-850">
          <span className="text-xs text-stone-500 dark:text-stone-400">
            已选 {selectedNames.length} 个
          </span>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={onClose}
              disabled={importing || promptDocGenerating}
              className="rounded-lg border border-stone-200 px-3 py-2 text-xs font-medium text-stone-700 transition-colors hover:bg-stone-50 disabled:opacity-50 dark:border-stone-700 dark:text-stone-300 dark:hover:bg-stone-800 cursor-default"
            >
              取消
            </button>
            <button
              type="button"
              onClick={() => onImport(selectedNames, counts)}
              disabled={importing || promptDocGenerating || selectedNames.length === 0 || !validCounts}
              className="inline-flex items-center gap-1.5 rounded-lg border border-stone-900 bg-stone-900 px-3 py-2 text-xs font-medium text-white transition-colors hover:bg-stone-800 disabled:cursor-not-allowed disabled:opacity-50 dark:border-stone-100 dark:bg-stone-100 dark:text-stone-900 dark:hover:bg-stone-200 cursor-default"
            >
              {(importing || promptDocGenerating) && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {promptDocGenerating ? '生成文档中' : importing ? '导入中' : '导入并生成文档'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

export function CustomPromptPreviewModal({
  open,
  docs,
  generating,
  saving,
  creating,
  error,
  status,
  onClose,
  onSave,
  onRegenerate,
  onConfirm,
}: {
  open: boolean;
  docs: CustomProjectPromptDocumentDetail[];
  generating: boolean;
  saving: boolean;
  creating: boolean;
  error: string;
  status: string;
  onClose: () => void;
  onSave: (path: string, content: string) => Promise<void>;
  onRegenerate: (projectName: string) => Promise<void>;
  onConfirm: (drafts: Record<string, string>) => Promise<void>;
}) {
  const [activePath, setActivePath] = useState('');
  const [drafts, setDrafts] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!open) return;
    setActivePath((current) =>
      current && docs.some((doc) => doc.outputPath === current)
        ? current
        : docs[0]?.outputPath ?? '',
    );
    setDrafts((current) => {
      const next: Record<string, string> = {};
      for (const doc of docs) {
        next[doc.outputPath] = current[doc.outputPath] ?? doc.content ?? '';
      }
      return next;
    });
  }, [open, docs]);

  if (!open) {
    return null;
  }

  const activeDoc = docs.find((doc) => doc.outputPath === activePath) ?? docs[0];
  const activeContent = activeDoc ? drafts[activeDoc.outputPath] ?? activeDoc.content ?? '' : '';
  const busy = generating || saving || creating;
  const dirty = activeDoc ? activeContent.trim() !== (activeDoc.content ?? '').trim() : false;

  const handleSaveActive = async () => {
    if (!activeDoc) return;
    await onSave(activeDoc.outputPath, activeContent);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/35 px-3 sm:px-4 py-3 sm:py-8 backdrop-blur-sm">
      <div className="flex max-h-[92vh] w-full max-w-6xl flex-col overflow-hidden rounded-2xl border border-stone-200 bg-white shadow-2xl dark:border-stone-800 dark:bg-stone-950">
        <div className="flex items-start justify-between gap-4 border-b border-stone-100 px-4 sm:px-5 py-3 sm:py-4 dark:border-stone-850">
          <div className="min-w-0">
            <h3 className="text-base font-semibold text-stone-900 dark:text-stone-100">
              预览并确认提示词文档
            </h3>
            <p className="mt-1 text-xs leading-relaxed text-stone-500 dark:text-stone-400">
              提示词已落地到 md 文件。这里可以先预览、修改并保存，确认后再创建 PR 任务目录。
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-full text-stone-400 transition-colors hover:bg-stone-100 hover:text-stone-700 disabled:opacity-50 dark:hover:bg-stone-800 dark:hover:text-stone-200 cursor-default"
            aria-label="关闭"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="grid min-h-0 flex-1 grid-cols-[180px_minmax(0,1fr)] sm:grid-cols-[220px_minmax(0,1fr)]">
          <aside className="min-h-0 overflow-y-auto border-r border-stone-100 bg-stone-50/60 p-3 dark:border-stone-850 dark:bg-stone-900/30">
            <div className="space-y-1">
              {docs.map((doc) => {
                const selected = doc.outputPath === activeDoc?.outputPath;
                return (
                  <button
                    key={doc.outputPath}
                    type="button"
                    onClick={() => setActivePath(doc.outputPath)}
                    className={`w-full rounded-lg px-3 py-2 text-left transition-colors cursor-default ${
                      selected
                        ? 'bg-stone-900 text-white dark:bg-stone-100 dark:text-stone-900'
                        : 'text-stone-600 hover:bg-white hover:text-stone-900 dark:text-stone-300 dark:hover:bg-stone-800 dark:hover:text-stone-50'
                    }`}
                  >
                    <div className="truncate text-xs font-semibold">{doc.projectName}</div>
                    <div className={`mt-0.5 truncate text-[10px] ${selected ? 'opacity-70' : 'text-stone-400 dark:text-stone-500'}`}>
                      {doc.outputPath}
                    </div>
                  </button>
                );
              })}
            </div>
          </aside>

          <main className="flex min-h-0 flex-col">
            <div className="flex items-center justify-between gap-3 border-b border-stone-100 px-4 py-3 dark:border-stone-850">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="truncate text-sm font-semibold text-stone-900 dark:text-stone-100">
                    {activeDoc?.projectName ?? '未选择文档'}
                  </span>
                  {dirty && (
                    <span className="rounded-full bg-amber-100 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:bg-amber-950/50 dark:text-amber-300">
                      未保存
                    </span>
                  )}
                </div>
                <p className="mt-0.5 truncate text-[11px] text-stone-400 dark:text-stone-500">
                  {activeDoc?.outputPath}
                </p>
              </div>
              <div className="flex shrink-0 items-center gap-2">
                <button
                  type="button"
                  onClick={handleSaveActive}
                  disabled={!activeDoc || saving || creating}
                  className="rounded-lg border border-stone-200 px-3 py-1.5 text-xs font-medium text-stone-700 transition-colors hover:bg-stone-50 disabled:opacity-50 dark:border-stone-700 dark:text-stone-300 dark:hover:bg-stone-800 cursor-default"
                >
                  {saving ? '保存中' : '保存当前文档'}
                </button>
                <button
                  type="button"
                  onClick={() => activeDoc && onRegenerate(activeDoc.projectName)}
                  disabled={!activeDoc || generating || creating}
                  className="inline-flex items-center gap-1.5 rounded-lg border border-stone-200 px-3 py-1.5 text-xs font-medium text-stone-700 transition-colors hover:bg-stone-50 disabled:opacity-50 dark:border-stone-700 dark:text-stone-300 dark:hover:bg-stone-800 cursor-default"
                >
                  {generating && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                  {generating ? '重新生成中' : '重新生成当前项目'}
                </button>
              </div>
            </div>

            {(error || status) && (
              <div
                className={`mx-4 mt-3 rounded-lg border px-3 py-2 text-xs ${
                  error
                    ? 'border-red-200 bg-red-50 text-red-600 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300'
                    : 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300'
                }`}
              >
                {error || status}
              </div>
            )}

            <div className="min-h-0 flex-1 p-4">
              <textarea
                value={activeContent}
                onChange={(event) => {
                  if (!activeDoc) return;
                  setDrafts((current) => ({
                    ...current,
                    [activeDoc.outputPath]: event.target.value,
                  }));
                }}
                spellCheck={false}
                className="h-full min-h-[160px] sm:min-h-[260px] w-full resize-none rounded-xl border border-stone-200 bg-white px-4 py-3 font-mono text-xs leading-6 text-stone-800 outline-none focus:border-stone-400 focus:ring-2 focus:ring-stone-400/20 dark:border-stone-800 dark:bg-stone-950 dark:text-stone-100 dark:focus:border-stone-600"
              />
            </div>
          </main>
        </div>

        <div className="flex items-center justify-between gap-3 border-t border-stone-100 px-4 sm:px-5 py-3 sm:py-4 dark:border-stone-850">
          <div className="text-xs text-stone-500 dark:text-stone-400">
            共 {docs.length} 个提示词就绪文档，确认前请保存修改。
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={onClose}
              disabled={busy}
              className="rounded-lg border border-stone-200 px-3 py-2 text-xs font-medium text-stone-700 transition-colors hover:bg-stone-50 disabled:opacity-50 dark:border-stone-700 dark:text-stone-300 dark:hover:bg-stone-800 cursor-default"
            >
              稍后处理
            </button>
            <button
              type="button"
              onClick={() => onConfirm(drafts)}
              disabled={docs.length === 0 || busy}
              className="inline-flex items-center gap-1.5 rounded-lg border border-stone-900 bg-stone-900 px-4 py-2 text-xs font-semibold text-white transition-colors hover:bg-stone-800 disabled:cursor-not-allowed disabled:opacity-50 dark:border-stone-100 dark:bg-stone-100 dark:text-stone-900 dark:hover:bg-stone-200 cursor-default"
            >
              {creating && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {creating ? '创建中' : '确认并创建任务'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function formatCustomProjectPrefixes(prefixes: string | null | undefined) {
  const parts = String(prefixes || 'zw')
    .split(/[,\s，；;]+/)
    .map((item) => item.trim())
    .filter(Boolean);
  const unique = Array.from(new Set(parts.length > 0 ? parts : ['zw']));
  return unique.map((item) => `${item}*`).join('、');
}

// 旧的 LocalScanCard/GitLabSyncCard/NormalizeCard 已被 SyncToolbar 取代，为避免其它模块直接引用时的破坏性改动，此处仅导出新组件。
export { RefreshCw };
