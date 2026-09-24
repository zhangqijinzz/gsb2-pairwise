import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  deleteQuestionBankItem,
  importSelectedCustomProjects,
  importQuestionBankArchives,
  listQuestionBankItems,
  pickQuestionBankArchives,
  refreshQuestionBankItem,
  scanLocalQuestionBank,
  scanCustomProjectCandidates,
  syncGitLabQuestionBank,
  normalizeManagedSourceFolders,
  type CustomProjectCandidateScanResult,
  type ImportLocalSourcesResult,
  type QuestionBankItem,
  type QuestionBankSyncResult,
  type NormalizeManagedSourceFoldersResult,
} from '../../../api/git';
import {
  pickCustomPromptDocuments,
  type CreateTasksFromCustomPromptDocumentsResult,
} from '../../../api/task';
import {
  getJob,
  submitCustomPromptDocumentGenerateJob,
  submitCustomPromptTaskCreateJob,
} from '../../../api/job';
import {
  readCustomProjectPromptDocument,
  saveCustomProjectPromptDocument,
  type CustomProjectPromptDocumentDetail,
  type CustomPromptCounts,
  type GenerateCustomProjectPromptDocumentsResult,
} from '../../../api/llm';
import { useAppStore } from '../../../store';
import { parseQuestionBankProjectIds } from '../utils/claimUtils';

function wait(ms: number): Promise<void> {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

async function waitForJobOutput<T>(jobId: string, fallbackMessage: string): Promise<T> {
  while (true) {
    const job = await getJob(jobId);
    if (!job) {
      throw new Error('后台任务不存在');
    }
    if (job.status === 'done') {
      if (!job.outputPayload) {
        throw new Error(fallbackMessage);
      }
      return JSON.parse(job.outputPayload) as T;
    }
    if (job.status === 'error') {
      throw new Error(job.errorMessage || fallbackMessage);
    }
    if (job.status === 'cancelled') {
      throw new Error('后台任务已取消');
    }
    await wait(1000);
  }
}

const customPromptHeadingPattern =
  /^\s{0,3}(?:#{1,6}\s*)?(?:\*\*)?\s*(0-1代码生成|Feature迭代|代码理解|Bug修复|代码重构|工程化|代码测试|未归类)\s*(?:\*\*)?\s*$/;
const customPromptItemPattern = /^\s*(?:[-*]\s+|\d+[.、)]\s+)(.*)$/;
const customPromptDifficultyPattern = /^【(?:简单|一般|困难|地狱)】\s*(.*)$/;

function stripCustomPromptDifficulty(value: string) {
  const trimmed = value.trim();
  const matches = customPromptDifficultyPattern.exec(trimmed);
  return (matches?.[1] ?? trimmed).trim();
}

function hasCustomPromptEntries(content: string | null | undefined) {
  let currentType = '';
  let currentPrompt: string | null = null;

  const flush = () => {
    const ready = !!currentPrompt?.trim();
    currentPrompt = null;
    return ready;
  };

  const lines = String(content ?? '').replaceAll('\r\n', '\n').split('\n');
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) {
      continue;
    }
    const heading = customPromptHeadingPattern.exec(trimmed);
    if (heading) {
      if (flush()) {
        return true;
      }
      currentType = heading[1];
      continue;
    }
    if (!currentType) {
      continue;
    }
    const item = customPromptItemPattern.exec(line);
    if (item) {
      if (flush()) {
        return true;
      }
      currentPrompt = stripCustomPromptDifficulty(item[1] ?? '');
      continue;
    }
    if (currentPrompt !== null) {
      currentPrompt = `${currentPrompt}\n${trimmed}`.trim();
    }
  }

  return flush();
}

function getPromptReadyDocs(docs: CustomProjectPromptDocumentDetail[]) {
  return docs.filter((doc) => hasCustomPromptEntries(doc.content));
}

export type QuestionBankState = {
  questionBankItems: QuestionBankItem[];
  questionBankLoading: boolean;
  questionBankError: string;
  questionBankFilter: string;
  setQuestionBankFilter: (value: string) => void;
  filteredQuestionBankItems: QuestionBankItem[];
  selectableFilteredQuestionBankItems: QuestionBankItem[];
  selectedQuestionIds: number[];
  selectedQuestionIdSet: Set<number>;
  selectedQuestionBankItems: QuestionBankItem[];
  selectedQuestionCount: number;
  readyQuestionCount: number;
  allFilteredSelected: boolean;
  toggleQuestionSelection: (item: QuestionBankItem) => void;
  toggleSelectAllFiltered: () => void;
  selectAllFiltered: () => void;
  clearSelection: () => void;
  invertSelectionOnFiltered: () => void;
  reloadQuestionBankItems: () => Promise<void>;
  importingLocalSources: boolean;
  localImportError: string;
  localImportResult: ImportLocalSourcesResult | null;
  handleScanLocalQuestionBank: () => Promise<void>;
  customProjectImporting: boolean;
  customProjectImportError: string;
  customProjectImportResult: ImportLocalSourcesResult | null;
  customProjectPromptDocGenerating: boolean;
  customProjectPromptDocError: string;
  customProjectPromptDocResult: GenerateCustomProjectPromptDocumentsResult | null;
  customPromptTaskCreating: boolean;
  customPromptTaskError: string;
  customPromptTaskResult: CreateTasksFromCustomPromptDocumentsResult | null;
  customPromptPreviewOpen: boolean;
  customPromptPreviewDocs: CustomProjectPromptDocumentDetail[];
  customPromptPreviewSaving: boolean;
  customPromptPreviewError: string;
  customPromptPreviewStatus: string;
  customProjectScanResult: CustomProjectCandidateScanResult | null;
  customProjectScanLoading: boolean;
  customProjectPickerOpen: boolean;
  closeCustomProjectPicker: () => void;
  handleScanCustomProjects: () => Promise<void>;
  handleImportSelectedCustomProjects: (projectNames: string[], counts?: CustomPromptCounts) => Promise<void>;
  handleCreateTasksFromGeneratedPromptDocs: () => Promise<void>;
  handleCreateTasksFromPickedPromptDocs: () => Promise<void>;
  closeCustomPromptPreview: () => void;
  handleSaveCustomPromptDocument: (path: string, content: string) => Promise<void>;
  handleRegenerateCustomPromptDocument: (projectName: string) => Promise<void>;
  handleConfirmCustomPromptPreview: (drafts?: Record<string, string>) => Promise<void>;
  handleImportArchivesViaPicker: () => Promise<void>;
  questionBankSyncing: boolean;
  questionBankSyncError: string;
  questionBankSyncResult: QuestionBankSyncResult | null;
  handleSyncGitLabQuestionBank: () => Promise<void>;
  refreshingQuestionId: number | null;
  handleRefreshQuestionBankItem: (questionId: number) => Promise<void>;
  deletingQuestionId: number | null;
  deleteError: string;
  handleDeleteQuestionBankItem: (questionId: number) => Promise<void>;
  configuredGitLabQuestionIds: number[];
  normalizeResult: NormalizeManagedSourceFoldersResult | null;
  normalizing: boolean;
  normalizeError: string;
  handleNormalize: () => Promise<void>;
};

export function useQuestionBank(projectId: string, questionBankProjectIdsRaw: string): QuestionBankState {
  const loadTasks = useAppStore((state) => state.loadTasks);
  const loadBackgroundJobs = useAppStore((state) => state.loadBackgroundJobs);

  const [questionBankItems, setQuestionBankItems] = useState<QuestionBankItem[]>([]);
  const [questionBankLoading, setQuestionBankLoading] = useState(false);
  const [questionBankError, setQuestionBankError] = useState('');
  const [questionBankFilter, setQuestionBankFilter] = useState('');
  const [selectedQuestionIds, setSelectedQuestionIds] = useState<number[]>([]);

  const [importingLocalSources, setImportingLocalSources] = useState(false);
  const [localImportError, setLocalImportError] = useState('');
  const [localImportResult, setLocalImportResult] = useState<ImportLocalSourcesResult | null>(null);
  const [customProjectImporting, setCustomProjectImporting] = useState(false);
  const [customProjectImportError, setCustomProjectImportError] = useState('');
  const [customProjectImportResult, setCustomProjectImportResult] = useState<ImportLocalSourcesResult | null>(null);
  const [customProjectPromptDocGenerating, setCustomProjectPromptDocGenerating] = useState(false);
  const [customProjectPromptDocError, setCustomProjectPromptDocError] = useState('');
  const [customProjectPromptDocResult, setCustomProjectPromptDocResult] = useState<GenerateCustomProjectPromptDocumentsResult | null>(null);
  const [customPromptTaskCreating, setCustomPromptTaskCreating] = useState(false);
  const [customPromptTaskError, setCustomPromptTaskError] = useState('');
  const [customPromptTaskResult, setCustomPromptTaskResult] = useState<CreateTasksFromCustomPromptDocumentsResult | null>(null);
  const [customPromptPreviewOpen, setCustomPromptPreviewOpen] = useState(false);
  const [customPromptPreviewDocs, setCustomPromptPreviewDocs] = useState<CustomProjectPromptDocumentDetail[]>([]);
  const [customPromptPreviewSaving, setCustomPromptPreviewSaving] = useState(false);
  const [customPromptPreviewError, setCustomPromptPreviewError] = useState('');
  const [customPromptPreviewStatus, setCustomPromptPreviewStatus] = useState('');
  const [customProjectScanResult, setCustomProjectScanResult] = useState<CustomProjectCandidateScanResult | null>(null);
  const [customProjectScanLoading, setCustomProjectScanLoading] = useState(false);
  const [customProjectPickerOpen, setCustomProjectPickerOpen] = useState(false);

  const [questionBankSyncing, setQuestionBankSyncing] = useState(false);
  const [questionBankSyncError, setQuestionBankSyncError] = useState('');
  const [questionBankSyncResult, setQuestionBankSyncResult] = useState<QuestionBankSyncResult | null>(null);
  const [refreshingQuestionId, setRefreshingQuestionId] = useState<number | null>(null);

  const [normalizing, setNormalizing] = useState(false);
  const [normalizeError, setNormalizeError] = useState('');
  const [normalizeResult, setNormalizeResult] = useState<NormalizeManagedSourceFoldersResult | null>(null);

  const [deletingQuestionId, setDeletingQuestionId] = useState<number | null>(null);
  const [deleteError, setDeleteError] = useState('');

  const configuredGitLabQuestionIds = useMemo(
    () => parseQuestionBankProjectIds(questionBankProjectIdsRaw || ''),
    [questionBankProjectIdsRaw],
  );

  const reloadQuestionBankItems = useCallback(async () => {
    if (!projectId) {
      setQuestionBankItems([]);
      setSelectedQuestionIds([]);
      setQuestionBankError('');
      return;
    }
    setQuestionBankLoading(true);
    setQuestionBankError('');
    try {
      const items = await listQuestionBankItems(projectId);
      setQuestionBankItems(items);
      setSelectedQuestionIds((prev) =>
        prev.filter((questionId) =>
          items.some((item) => item.questionId === questionId && item.status === 'ready'),
        ),
      );
    } catch (error) {
      setQuestionBankError(error instanceof Error ? error.message : '加载题库失败');
    } finally {
      setQuestionBankLoading(false);
    }
  }, [projectId]);

  const handleNormalize = useCallback(async () => {
    setNormalizing(true);
    setNormalizeError('');
    try {
      const result = await normalizeManagedSourceFolders(projectId);
      setNormalizeResult(result);
      await loadTasks();
    } catch (error) {
      setNormalizeError(error instanceof Error ? error.message : '归一处理失败');
    } finally {
      setNormalizing(false);
    }
  }, [projectId, loadTasks]);

  const handleScanLocalQuestionBank = useCallback(async () => {
    setImportingLocalSources(true);
    setLocalImportError('');
    try {
      const result = await scanLocalQuestionBank(projectId);
      setLocalImportResult(result);
      await reloadQuestionBankItems();
      if (result.importedCount > 0) {
        await handleNormalize();
      }
    } catch (error) {
      setLocalImportError(error instanceof Error ? error.message : '本地题源扫描失败');
    } finally {
      setImportingLocalSources(false);
    }
  }, [projectId, reloadQuestionBankItems, handleNormalize]);

  const handleScanCustomProjects = useCallback(async () => {
    if (!projectId) return;
    setCustomProjectScanLoading(true);
    setCustomProjectImportError('');
    setCustomProjectPromptDocError('');
    setCustomPromptTaskError('');
    setCustomProjectImportResult(null);
    setCustomProjectPromptDocResult(null);
    setCustomPromptTaskResult(null);
    try {
      const result = await scanCustomProjectCandidates(projectId);
      setCustomProjectScanResult(result);
      setCustomProjectPickerOpen(true);
    } catch (error) {
      setCustomProjectImportError(error instanceof Error ? error.message : '刷新自定义项目失败');
    } finally {
      setCustomProjectScanLoading(false);
    }
  }, [projectId]);

  const closeCustomProjectPicker = useCallback(() => {
    if (customProjectImporting || customProjectPromptDocGenerating) return;
    setCustomProjectPickerOpen(false);
  }, [customProjectImporting, customProjectPromptDocGenerating]);

  const documentCounts = useRef<Record<string, CustomPromptCounts>>({});
  const handleImportSelectedCustomProjects = useCallback(async (projectNames: string[], counts?: CustomPromptCounts) => {
    if (!projectId || projectNames.length === 0) return;
    let generatingPromptDocs = false;
    setCustomProjectImporting(true);
    setCustomProjectPromptDocGenerating(false);
    setCustomProjectImportError('');
    setCustomProjectPromptDocError('');
    try {
      const result = await importSelectedCustomProjects(projectId, projectNames);
      setCustomProjectImportResult(result);
      await reloadQuestionBankItems();
      if (result.importedCount > 0) {
        await handleNormalize();
      }
      const importedNames = result.details
        .filter((detail) => detail.status === 'imported')
        .map((detail) => detail.name);
      if (importedNames.length > 0) {
        generatingPromptDocs = true;
        setCustomProjectPromptDocGenerating(true);
        const docRequest = {
          projectId,
          projectNames: importedNames,
          counts,
        };
        if (counts) importedNames.forEach((name) => { documentCounts.current[`${projectId}:${name}`] = counts; });
        const docJob = await submitCustomPromptDocumentGenerateJob(docRequest);
        await loadBackgroundJobs();
        const docResult = await waitForJobOutput<GenerateCustomProjectPromptDocumentsResult>(
          docJob.id,
          '生成提示词文档失败',
        );
        setCustomProjectPromptDocResult(docResult);
        const generatedDocs = docResult.details.filter((detail) => detail.status === 'generated');
        const promptReadyDocs = getPromptReadyDocs(generatedDocs);
        const skippedCount = generatedDocs.length - promptReadyDocs.length;
        setCustomPromptPreviewDocs(promptReadyDocs);
        setCustomPromptPreviewStatus(skippedCount > 0 ? `已隐藏 ${skippedCount} 个无提示词文档` : '');
        setCustomPromptPreviewOpen(promptReadyDocs.length > 0);
        if (generatedDocs.length > 0 && promptReadyDocs.length === 0) {
          setCustomProjectPromptDocError('没有提示词就绪文档可创建');
        }
        await loadBackgroundJobs();
      }
      setCustomProjectPickerOpen(false);
    } catch (error) {
      const message = error instanceof Error ? error.message : '导入自定义项目失败';
      if (generatingPromptDocs) {
        setCustomProjectPromptDocError(message);
      } else {
        setCustomProjectImportError(message);
      }
    } finally {
      setCustomProjectImporting(false);
      setCustomProjectPromptDocGenerating(false);
    }
  }, [projectId, reloadQuestionBankItems, handleNormalize, loadBackgroundJobs]);

  const createTasksFromPromptDocPaths = useCallback(async (documentPaths: string[]) => {
    const paths = documentPaths.filter((path) => path.trim().length > 0);
    if (!projectId || paths.length === 0) return;
    setCustomPromptTaskCreating(true);
    setCustomPromptTaskError('');
    try {
      const job = await submitCustomPromptTaskCreateJob({
        projectId,
        documentPaths: paths,
      });
      await loadBackgroundJobs();
      const result = await waitForJobOutput<CreateTasksFromCustomPromptDocumentsResult>(
        job.id,
        '从提示词文档创建任务失败',
      );
      setCustomPromptTaskResult(result);
      await loadTasks();
    } catch (error) {
      setCustomPromptTaskError(error instanceof Error ? error.message : '从提示词文档创建任务失败');
    } finally {
      setCustomPromptTaskCreating(false);
    }
  }, [projectId, loadTasks, loadBackgroundJobs]);

  const handleCreateTasksFromGeneratedPromptDocs = useCallback(async () => {
    const paths = customProjectPromptDocResult?.details
      .filter((detail) => detail.status === 'generated' && detail.outputPath && hasCustomPromptEntries(detail.content))
      .map((detail) => detail.outputPath) ?? [];
    if (customProjectPromptDocResult && paths.length === 0) {
      setCustomPromptTaskError('没有提示词就绪文档可创建');
      return;
    }
    await createTasksFromPromptDocPaths(paths);
  }, [customProjectPromptDocResult, createTasksFromPromptDocPaths]);

  const handleCreateTasksFromPickedPromptDocs = useCallback(async () => {
    setCustomPromptTaskError('');
    let paths: string[] = [];
    try {
      paths = await pickCustomPromptDocuments();
    } catch (error) {
      setCustomPromptTaskError(error instanceof Error ? error.message : '选择提示词文档失败');
      return;
    }
    if (paths.length === 0) return;
    try {
      const docs = await Promise.all(paths.map((path) => readCustomProjectPromptDocument(path)));
      const promptReadyDocs = getPromptReadyDocs(docs);
      const skippedCount = docs.length - promptReadyDocs.length;
      setCustomPromptPreviewDocs(promptReadyDocs);
      setCustomPromptPreviewStatus(skippedCount > 0 ? `已隐藏 ${skippedCount} 个无提示词文档` : '');
      setCustomPromptPreviewOpen(promptReadyDocs.length > 0);
      if (docs.length > 0 && promptReadyDocs.length === 0) {
        setCustomPromptTaskError('没有提示词就绪文档可创建');
      }
    } catch (error) {
      setCustomPromptTaskError(error instanceof Error ? error.message : '读取提示词文档失败');
    }
  }, []);

  const closeCustomPromptPreview = useCallback(() => {
    if (customPromptPreviewSaving || customPromptTaskCreating || customProjectPromptDocGenerating) return;
    setCustomPromptPreviewOpen(false);
  }, [customPromptPreviewSaving, customPromptTaskCreating, customProjectPromptDocGenerating]);

  const handleSaveCustomPromptDocument = useCallback(async (path: string, content: string) => {
    setCustomPromptPreviewSaving(true);
    setCustomPromptPreviewError('');
    setCustomPromptPreviewStatus('');
    try {
      const saved = await saveCustomProjectPromptDocument(path, content);
      setCustomPromptPreviewDocs((docs) =>
        docs.map((doc) =>
          doc.outputPath === path
            ? { ...doc, content: saved.content, status: saved.status, message: saved.message }
            : doc,
        ),
      );
      setCustomPromptPreviewStatus('已保存');
    } catch (error) {
      setCustomPromptPreviewError(error instanceof Error ? error.message : '保存提示词文档失败');
    } finally {
      setCustomPromptPreviewSaving(false);
    }
  }, []);

  const handleRegenerateCustomPromptDocument = useCallback(async (projectName: string) => {
    if (!projectId || !projectName) return;
    setCustomProjectPromptDocGenerating(true);
    setCustomPromptPreviewError('');
    setCustomPromptPreviewStatus('');
    try {
      const job = await submitCustomPromptDocumentGenerateJob({
        projectId,
        projectNames: [projectName],
        counts: documentCounts.current[`${projectId}:${projectName}`],
      });
      await loadBackgroundJobs();
      const result = await waitForJobOutput<GenerateCustomProjectPromptDocumentsResult>(
        job.id,
        '重新生成提示词文档失败',
      );
      setCustomProjectPromptDocResult(result);
      const generated = result.details.find((detail) => detail.status === 'generated');
      if (!generated) {
        throw new Error(result.details[0]?.message || '重新生成提示词文档失败');
      }
      setCustomPromptPreviewDocs((docs) =>
        docs.map((doc) => (doc.projectName === projectName ? generated : doc)),
      );
      setCustomPromptPreviewStatus('已重新生成');
    } catch (error) {
      setCustomPromptPreviewError(error instanceof Error ? error.message : '重新生成提示词文档失败');
    } finally {
      setCustomProjectPromptDocGenerating(false);
    }
  }, [projectId, loadBackgroundJobs]);

  const handleConfirmCustomPromptPreview = useCallback(async (drafts: Record<string, string> = {}) => {
    if (Object.keys(drafts).length > 0) {
      setCustomPromptPreviewSaving(true);
      setCustomPromptPreviewError('');
      try {
        const savedDocs = await Promise.all(
          customPromptPreviewDocs.map(async (doc) => {
            const draft = drafts[doc.outputPath];
            if (draft === undefined || draft.trim() === (doc.content ?? '').trim()) {
              return doc;
            }
            return saveCustomProjectPromptDocument(doc.outputPath, draft);
          }),
        );
        setCustomPromptPreviewDocs(savedDocs);
      } catch (error) {
        setCustomPromptPreviewError(error instanceof Error ? error.message : '保存提示词文档失败');
        return;
      } finally {
        setCustomPromptPreviewSaving(false);
      }
    }
    const docsForCreate = Object.keys(drafts).length > 0
      ? customPromptPreviewDocs.map((doc) => ({
          ...doc,
          content: drafts[doc.outputPath] ?? doc.content,
        }))
      : customPromptPreviewDocs;
    const promptReadyDocs = getPromptReadyDocs(docsForCreate);
    if (promptReadyDocs.length === 0) {
      setCustomPromptPreviewError('没有提示词就绪文档可创建');
      return;
    }
    const paths = promptReadyDocs
      .filter((doc) => doc.outputPath && doc.status !== 'error')
      .map((doc) => doc.outputPath);
    await createTasksFromPromptDocPaths(paths);
    setCustomPromptPreviewOpen(false);
  }, [customPromptPreviewDocs, createTasksFromPromptDocPaths]);

  const handleImportArchivesViaPicker = useCallback(async () => {
    setLocalImportError('');
    let paths: string[] = [];
    try {
      paths = await pickQuestionBankArchives();
    } catch (error) {
      setLocalImportError(error instanceof Error ? error.message : '选择压缩包失败');
      return;
    }
    if (paths.length === 0) return;

    setImportingLocalSources(true);
    try {
      const result = await importQuestionBankArchives(projectId, paths);
      setLocalImportResult(result);
      await reloadQuestionBankItems();
      if (result.importedCount > 0) {
        await handleNormalize();
      }
    } catch (error) {
      setLocalImportError(error instanceof Error ? error.message : '导入压缩包失败');
    } finally {
      setImportingLocalSources(false);
    }
  }, [projectId, reloadQuestionBankItems, handleNormalize]);

  const handleSyncGitLabQuestionBank = useCallback(async () => {
    setQuestionBankSyncing(true);
    setQuestionBankSyncError('');
    try {
      const result = await syncGitLabQuestionBank(projectId);
      setQuestionBankSyncResult(result);
      await reloadQuestionBankItems();
      if (result.syncedCount > 0) {
        await handleNormalize();
      }
    } catch (error) {
      setQuestionBankSyncError(error instanceof Error ? error.message : '同步 GitLab 题库失败');
    } finally {
      setQuestionBankSyncing(false);
    }
  }, [projectId, reloadQuestionBankItems, handleNormalize]);

  const handleRefreshQuestionBankItem = useCallback(async (questionId: number) => {
    setRefreshingQuestionId(questionId);
    setQuestionBankSyncError('');
    try {
      const result = await refreshQuestionBankItem(projectId, questionId);
      setQuestionBankSyncResult(result);
      await reloadQuestionBankItems();
    } catch (error) {
      setQuestionBankSyncError(error instanceof Error ? error.message : '刷新题库源码失败');
    } finally {
      setRefreshingQuestionId(null);
    }
  }, [projectId, reloadQuestionBankItems]);

  const handleDeleteQuestionBankItem = useCallback(async (questionId: number) => {
    if (!projectId) return;
    setDeletingQuestionId(questionId);
    setDeleteError('');
    try {
      await deleteQuestionBankItem(projectId, questionId);
      setSelectedQuestionIds((prev) => prev.filter((id) => id !== questionId));
      await reloadQuestionBankItems();
    } catch (error) {
      setDeleteError(error instanceof Error ? error.message : '删除题库条目失败');
    } finally {
      setDeletingQuestionId(null);
    }
  }, [projectId, reloadQuestionBankItems]);

  // Load question bank on project change
  useEffect(() => {
    void reloadQuestionBankItems();
  }, [reloadQuestionBankItems]);

  // Auto-scan local sources on mount
  useEffect(() => {
    if (!projectId) return;
    let cancelled = false;

    const runImport = async () => {
      setImportingLocalSources(true);
      setLocalImportError('');
      try {
        const result = await scanLocalQuestionBank(projectId);
        if (cancelled) return;
        setLocalImportResult(result);
        await reloadQuestionBankItems();
        if (result.importedCount > 0) {
          await loadTasks();
        }
      } catch (error) {
        if (cancelled) return;
        setLocalImportError(error instanceof Error ? error.message : '本地题源扫描失败');
      } finally {
        if (!cancelled) setImportingLocalSources(false);
      }
    };

    void runImport();
    return () => { cancelled = true; };
  }, [projectId, reloadQuestionBankItems, loadTasks]);

  // Derived state
  const selectedQuestionIdSet = useMemo(() => new Set(selectedQuestionIds), [selectedQuestionIds]);

  const filteredQuestionBankItems = useMemo(() => {
    const keyword = questionBankFilter.trim().toLowerCase();
    if (!keyword) return questionBankItems;
    return questionBankItems.filter(
      (item) =>
        item.displayName.toLowerCase().includes(keyword) ||
        String(item.questionId).includes(keyword) ||
        item.sourceKind.toLowerCase().includes(keyword),
    );
  }, [questionBankFilter, questionBankItems]);

  const selectableFilteredQuestionBankItems = useMemo(
    () => filteredQuestionBankItems.filter((item) => item.status === 'ready'),
    [filteredQuestionBankItems],
  );

  const selectedQuestionBankItems = useMemo(
    () =>
      questionBankItems.filter(
        (item) => item.status === 'ready' && selectedQuestionIdSet.has(item.questionId),
      ),
    [questionBankItems, selectedQuestionIdSet],
  );

  const readyQuestionCount = useMemo(
    () => questionBankItems.filter((item) => item.status === 'ready').length,
    [questionBankItems],
  );

  const selectedQuestionCount = selectedQuestionBankItems.length;

  const allFilteredSelected =
    selectableFilteredQuestionBankItems.length > 0 &&
    selectableFilteredQuestionBankItems.every((item) => selectedQuestionIdSet.has(item.questionId));

  const toggleQuestionSelection = useCallback((item: QuestionBankItem) => {
    if (item.status !== 'ready') return;
    setSelectedQuestionIds((prev) =>
      prev.includes(item.questionId)
        ? prev.filter((value) => value !== item.questionId)
        : [...prev, item.questionId],
    );
  }, []);

  const toggleSelectAllFiltered = useCallback(() => {
    const visibleIds = selectableFilteredQuestionBankItems.map((item) => item.questionId);
    if (visibleIds.length === 0) return;
    setSelectedQuestionIds((prev) => {
      if (allFilteredSelected) {
        return prev.filter((questionId) => !visibleIds.includes(questionId));
      }
      return [...new Set([...prev, ...visibleIds])];
    });
  }, [selectableFilteredQuestionBankItems, allFilteredSelected]);

  const selectAllFiltered = useCallback(() => {
    const visibleIds = selectableFilteredQuestionBankItems.map((item) => item.questionId);
    if (visibleIds.length === 0) return;
    setSelectedQuestionIds((prev) => [...new Set([...prev, ...visibleIds])]);
  }, [selectableFilteredQuestionBankItems]);

  const clearSelection = useCallback(() => {
    setSelectedQuestionIds([]);
  }, []);

  const invertSelectionOnFiltered = useCallback(() => {
    const visibleIds = selectableFilteredQuestionBankItems.map((item) => item.questionId);
    if (visibleIds.length === 0) return;
    setSelectedQuestionIds((prev) => {
      const prevSet = new Set(prev);
      const outsideFiltered = prev.filter((id) => !visibleIds.includes(id));
      const invertedInsideFiltered = visibleIds.filter((id) => !prevSet.has(id));
      return [...outsideFiltered, ...invertedInsideFiltered];
    });
  }, [selectableFilteredQuestionBankItems]);

  return {
    questionBankItems,
    questionBankLoading,
    questionBankError,
    questionBankFilter,
    setQuestionBankFilter,
    filteredQuestionBankItems,
    selectableFilteredQuestionBankItems,
    selectedQuestionIds,
    selectedQuestionIdSet,
    selectedQuestionBankItems,
    selectedQuestionCount,
    readyQuestionCount,
    allFilteredSelected,
    toggleQuestionSelection,
    toggleSelectAllFiltered,
    selectAllFiltered,
    clearSelection,
    invertSelectionOnFiltered,
    reloadQuestionBankItems,
    importingLocalSources,
    localImportError,
    localImportResult,
    handleScanLocalQuestionBank,
    customProjectImporting,
    customProjectImportError,
    customProjectImportResult,
    customProjectPromptDocGenerating,
    customProjectPromptDocError,
    customProjectPromptDocResult,
    customPromptTaskCreating,
    customPromptTaskError,
    customPromptTaskResult,
    customPromptPreviewOpen,
    customPromptPreviewDocs,
    customPromptPreviewSaving,
    customPromptPreviewError,
    customPromptPreviewStatus,
    customProjectScanResult,
    customProjectScanLoading,
    customProjectPickerOpen,
    closeCustomProjectPicker,
    handleScanCustomProjects,
    handleImportSelectedCustomProjects,
    handleCreateTasksFromGeneratedPromptDocs,
    handleCreateTasksFromPickedPromptDocs,
    closeCustomPromptPreview,
    handleSaveCustomPromptDocument,
    handleRegenerateCustomPromptDocument,
    handleConfirmCustomPromptPreview,
    handleImportArchivesViaPicker,
    questionBankSyncing,
    questionBankSyncError,
    questionBankSyncResult,
    handleSyncGitLabQuestionBank,
    refreshingQuestionId,
    handleRefreshQuestionBankItem,
    deletingQuestionId,
    deleteError,
    handleDeleteQuestionBankItem,
    configuredGitLabQuestionIds,
    normalizeResult,
    normalizing,
    normalizeError,
    handleNormalize,
  };
}
