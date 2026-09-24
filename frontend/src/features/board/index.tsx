import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { Events } from '@wailsio/runtime';
import { useAppStore, TaskStatus, TaskType, Task } from '../../store';
import {
  buildTaskTypeOverviewSummaries,
} from '../../shared/lib/taskTypeOverview';
import {
  getProjectTaskSettings,
} from '../../api/config';
import {
  deleteTask,
  listTaskChildDirectories,
  openTaskLocalFolder,
} from '../../api/task';
import {
  submitJob,
  submitSessionSyncJob,
  submitAiReviewJob,
  deleteAiReviewJob,
  type JobProgressEvent,
} from '../../api/job';
import type { ModelRunFromDB, ReviewStatus, TaskChildDirectory } from '../../api/task';
import { listCodePushRecords, type CodePushRecord } from '../../api/codePush';
import {
  BatchActionBar,
} from './components/BatchActionBar';
import {
  CardSize,
} from './components/BoardPresentation';
import {
  BoardMainContent,
} from './components/BoardMainContent';
import {
  filterBoardTasks,
  getAvailableExecutionRounds,
  groupBoardTasks,
  sortBoardTasks,
  type BoardSortOption,
} from './utils/boardTaskView';
import {
  BoardLayerStack,
  type TaskCardContextMenuState,
} from './components/BoardLayerStack';
import { useBoardTaskDetail } from './hooks/useBoardTaskDetail';
import { useTaskCardContainerActions } from './hooks/useTaskCardContainerActions';
import { listCases } from '../../api/annotation';
import { getTableProgress, type TableProgress } from '../annotation/tableProgress';

const COLUMNS: TaskStatus[] = [
  'Claimed',
  'Downloading',
  'Downloaded',
  'PromptReady',
  'ExecutionCompleted',
  'Submitted',
  'Error',
];
const DRAWER_ESC_CONFIRM_WINDOW_MS = 1600;
const BOARD_EXPANDED_GROUPS_STORAGE_KEY = 'pinru.board.expandedGroups.v1';
const BOARD_CARD_SIZE_STORAGE_KEY = 'pinru.board.cardSize.v1';
const BOARD_CARD_SIZES: CardSize[] = ['sm', 'four', 'md', 'lg'];
const AI_REVIEW_COMMIT_REQUIRED_MESSAGE = '请先提交代码，再发起 AI 复审';

function loadExpandedGroupsFromStorage() {
  try {
    const raw = window.localStorage.getItem(BOARD_EXPANDED_GROUPS_STORAGE_KEY);
    if (!raw) return new Set<string>();
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return new Set<string>();
    return new Set(parsed.filter((item): item is string => typeof item === 'string'));
  } catch {
    return new Set<string>();
  }
}

function saveExpandedGroupsToStorage(groupKeys: Set<string>) {
  try {
    window.localStorage.setItem(
      BOARD_EXPANDED_GROUPS_STORAGE_KEY,
      JSON.stringify([...groupKeys]),
    );
  } catch {
    // Ignore storage failures; the board remains usable without persistence.
  }
}

function loadCardSizeFromStorage(): CardSize {
  try {
    const raw = window.localStorage.getItem(BOARD_CARD_SIZE_STORAGE_KEY);
    return BOARD_CARD_SIZES.includes(raw as CardSize) ? (raw as CardSize) : 'md';
  } catch {
    return 'md';
  }
}

function saveCardSizeToStorage(cardSize: CardSize) {
  try {
    window.localStorage.setItem(BOARD_CARD_SIZE_STORAGE_KEY, cardSize);
  } catch {
    // Ignore storage failures; the board falls back to the default card size.
  }
}

function normalizeTaskChildDirectoryList(
  directories: TaskChildDirectory[] | null | undefined,
): TaskChildDirectory[] {
  return Array.isArray(directories) ? directories : [];
}

function resolveLatestModelRunSession(run: ModelRunFromDB | null | undefined) {
  if (!run) return null;
  const sessions = Array.isArray(run.sessionList) ? run.sessionList : [];
  for (let index = sessions.length - 1; index >= 0; index -= 1) {
    const sessionId = sessions[index]?.sessionId?.trim();
    if (sessionId) {
      return { sessionId, sessionIndex: index };
    }
  }
  const fallbackSessionId = run.sessionId?.trim();
  if (fallbackSessionId) {
    return {
      sessionId: fallbackSessionId,
      sessionIndex: Math.max(run.conversationRounds - 1, 0),
    };
  }
  return null;
}

function hasCommittedCodeForAiReview(
  records: CodePushRecord[],
  modelRunId: string | null | undefined,
  sessionId?: string | null,
) {
  const normalizedModelRunId = modelRunId?.trim();
  if (!normalizedModelRunId) {
    return false;
  }

  return records.some((record) => {
    if (record.modelRunId?.trim() !== normalizedModelRunId) {
      return false;
    }
    if (!record.commitSha?.trim()) {
      return false;
    }
    return sessionId ? record.sessionId?.trim() === sessionId : true;
  });
}

export default function Board() {
  const navigate = useNavigate();
  const tasks                  = useAppStore(s => s.tasks);
  const loadTasks              = useAppStore(s => s.loadTasks);
  const removeTaskFromStore    = useAppStore(s => s.removeTask);
  const activeProject          = useAppStore(s => s.activeProject);
  const [annotationRefresh, setAnnotationRefresh] = useState(0);
  const [tableSummary, setTableSummary] = useState<{ projectId: string; byTask: Record<string, TableProgress> }>({ projectId: '', byTask: {} });
  useEffect(() => {
    const projectId = activeProject?.id;
    if (!projectId) return;
    let current = true;
    let timer: ReturnType<typeof setTimeout>;
    const refresh = async () => {
      try {
        const cases = await listCases(projectId);
        if (current) setTableSummary({ projectId, byTask: Object.fromEntries(cases.map((item) => [item.taskId, getTableProgress(item)])) });
      } catch { /* Preserve last saved state during temporary read failures. */ }
      finally { if (current) timer=setTimeout(refresh, 5000); }
    };
    void refresh();
    return () => { current = false; clearTimeout(timer); };
  }, [activeProject?.id, tasks, annotationRefresh]);
  const setActiveProject       = useAppStore(s => s.setActiveProject);
  const loadActiveProject      = useAppStore(s => s.loadActiveProject);
  const updateTaskStatusInStore = useAppStore(s => s.updateTaskStatus);
  const updateTaskTypeInStore = useAppStore(s => s.updateTaskType);
  const aiReviewVisible = useAppStore((s) => s.aiReviewVisible);
  const sourceModelName = activeProject?.sourceModelFolder?.trim() || 'ORIGIN';
  const projectTaskSettings = useMemo(
    () =>
      getProjectTaskSettings(activeProject, [
        ...tasks.map((task) => task.taskType),
        ...tasks.flatMap((task) => task.sessionList.map((session) => session.taskType)),
      ]),
    [activeProject, tasks],
  );
  const availableTaskTypes = projectTaskSettings.taskTypes;
  const projectQuotas = projectTaskSettings.quotas;
  const projectTotals = projectTaskSettings.totals;

  const [showProjectPanel, setShowProjectPanel] = useState(false);
  const [showProjectOverview, setShowProjectOverview] = useState(false);
  const [search, setSearch]           = useState('');
  const [activeTypes, setActiveTypes]   = useState<Set<TaskType>>(new Set());
  const [activeStages, setActiveStages] = useState<Set<TaskStatus>>(new Set());
  const [activeRounds, setActiveRounds] = useState<Set<number>>(new Set());
  const [activeReviewStatuses, setActiveReviewStatuses] = useState<Set<ReviewStatus>>(new Set());
  const [cardSize, setCardSize]         = useState<CardSize>(loadCardSizeFromStorage);
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(
    loadExpandedGroupsFromStorage,
  );
  const [sortBy, setSortBy] = useState<BoardSortOption>('project-desc');
  const [pendingDelete, setPendingDelete]     = useState<Task | null>(null);
  const [deleting, setDeleting]   = useState(false);
  const [deleteError, setDeleteError] = useState('');
  const [taskCardContextMenu, setTaskCardContextMenu] = useState<TaskCardContextMenuState | null>(null);
  const [taskCardContextMenuError, setTaskCardContextMenuError] = useState('');
  const [taskCardFolderOpening, setTaskCardFolderOpening] = useState(false);
  const [taskCardChildDirectories, setTaskCardChildDirectories] = useState<TaskChildDirectory[]>([]);
  const [taskCardChildDirectoriesLoading, setTaskCardChildDirectoriesLoading] = useState(false);
  const [taskCardQuickActionLoadingPath, setTaskCardQuickActionLoadingPath] = useState<string | null>(null);
  const [drawerEscCloseHintVisible, setDrawerEscCloseHintVisible] = useState(false);
  const [selectionMode, setSelectionMode] = useState(false);
  const [selectedTaskIds, setSelectedTaskIds] = useState<Set<string>>(new Set());
  const taskCardContextMenuRef = useRef<HTMLDivElement | null>(null);
  const taskCardContextMenuRequestIdRef = useRef(0);
  const drawerEscCloseHintTimeoutRef = useRef<number | null>(null);
  const drawerEscLastPressedAtRef = useRef<number | null>(null);
  const notifyAnnotationChanged = useCallback(() => {
    setAnnotationRefresh((value) => value + 1);
  }, []);
  const containerActions = useTaskCardContainerActions({
    projectId: activeProject?.id ?? null,
    onAnnotationChanged: notifyAnnotationChanged,
  });
  const detail = useBoardTaskDetail({
    activeProject,
    availableTaskTypes,
    sourceModelName,
    tasks,
    loadTasks,
    loadActiveProject,
    updateTaskStatusInStore,
    updateTaskTypeInStore,
  });

  const toggleType = (id: TaskType) =>
    setActiveTypes(prev => { const n = new Set(prev); n.has(id) ? n.delete(id) : n.add(id); return n; });

  const toggleStage = (id: TaskStatus) =>
    setActiveStages(prev => { const n = new Set(prev); n.has(id) ? n.delete(id) : n.add(id); return n; });

  const toggleRound = (round: number) =>
    setActiveRounds(prev => { const n = new Set(prev); n.has(round) ? n.delete(round) : n.add(round); return n; });

  const toggleReviewStatus = (status: ReviewStatus) =>
    setActiveReviewStatuses(prev => { const n = new Set(prev); n.has(status) ? n.delete(status) : n.add(status); return n; });

  const ensureAiReviewCodeCommitted = async ({
    taskId,
    modelRunId,
    run,
    onMessage,
  }: {
    taskId: string;
    modelRunId: string | null | undefined;
    run?: ModelRunFromDB | null;
    onMessage: (message: string) => void;
  }) => {
    const session = resolveLatestModelRunSession(run);
    if (run && !session) {
      onMessage('当前模型执行还没有可用 sessionId，请先保存 session 并提交代码');
      return false;
    }

    let records: CodePushRecord[] = [];
    try {
      records = await listCodePushRecords(taskId);
    } catch (error) {
      onMessage(error instanceof Error ? error.message : '读取代码提交记录失败');
      return false;
    }

    if (!hasCommittedCodeForAiReview(records, modelRunId, session?.sessionId ?? null)) {
      onMessage(AI_REVIEW_COMMIT_REQUIRED_MESSAGE);
      return false;
    }
    onMessage('');
    return true;
  };

  const toggleGroupCollapse = (groupKey: string) =>
    setExpandedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(groupKey)) {
        next.delete(groupKey);
      } else {
        next.add(groupKey);
      }
      return next;
    });

  const availableExecutionRounds = useMemo(() => getAvailableExecutionRounds(tasks), [tasks]);

  const hasFilters =
    activeTypes.size > 0 ||
    activeStages.size > 0 ||
    activeRounds.size > 0 ||
    activeReviewStatuses.size > 0 ||
    search.length > 0;
  const clearFilters = () => {
    setActiveTypes(new Set());
    setActiveStages(new Set());
    setActiveRounds(new Set());
    setActiveReviewStatuses(new Set());
    setSearch('');
  };

  const clearDrawerEscCloseHintTimer = () => {
    if (drawerEscCloseHintTimeoutRef.current !== null) {
      window.clearTimeout(drawerEscCloseHintTimeoutRef.current);
      drawerEscCloseHintTimeoutRef.current = null;
    }
  };

  const resetDrawerEscCloseHint = () => {
    clearDrawerEscCloseHintTimer();
    drawerEscLastPressedAtRef.current = null;
    setDrawerEscCloseHintVisible(false);
  };

  const closeDetailDrawer = () => {
    resetDrawerEscCloseHint();
    detail.setSelected(null);
  };

  const closeTaskCardContextMenu = useCallback(() => {
    taskCardContextMenuRequestIdRef.current += 1;
    setTaskCardContextMenu(null);
    setTaskCardContextMenuError('');
    setTaskCardFolderOpening(false);
    setTaskCardChildDirectories([]);
    setTaskCardChildDirectoriesLoading(false);
    setTaskCardQuickActionLoadingPath(null);
  }, []);

  const openTaskCardContextMenu = (event: MouseEvent, task: Task) => {
    event.preventDefault();
    event.stopPropagation();

    const padding = 12;
    const menuWidth = 296;
    const estimatedHeight = 460;

    setTaskCardContextMenuError('');
    setTaskCardFolderOpening(false);
    setTaskCardChildDirectories([]);
    setTaskCardChildDirectoriesLoading(true);
    setTaskCardQuickActionLoadingPath(null);
    setTaskCardContextMenu({
      task,
      position: {
        x: Math.max(padding, Math.min(event.clientX, window.innerWidth - menuWidth - padding)),
        y: Math.max(padding, Math.min(event.clientY, window.innerHeight - estimatedHeight - padding)),
      },
    });

    const requestId = taskCardContextMenuRequestIdRef.current + 1;
    taskCardContextMenuRequestIdRef.current = requestId;
    void (async () => {
      try {
        const directories = await listTaskChildDirectories(task.id);
        if (taskCardContextMenuRequestIdRef.current !== requestId) {
          return;
        }
        setTaskCardChildDirectories(normalizeTaskChildDirectoryList(directories));
      } catch (error) {
        if (taskCardContextMenuRequestIdRef.current !== requestId) {
          return;
        }
        setTaskCardContextMenuError(error instanceof Error ? error.message : '加载子文件夹失败');
      } finally {
        if (taskCardContextMenuRequestIdRef.current === requestId) {
          setTaskCardChildDirectoriesLoading(false);
        }
      }
    })();
  };

  useEffect(() => { loadTasks(); }, [loadTasks]);
  useEffect(() => { loadActiveProject(); }, [loadActiveProject]);

  useEffect(() => {
    const cancel = Events.On('job:progress', (event: { data: JobProgressEvent }) => {
      const data = event.data;
      if (data.jobType.startsWith('annotation_') && ['done', 'error', 'cancelled'].includes(data.status)) {
        setAnnotationRefresh((value) => value + 1);
        return;
      }
      if (data.jobType === 'session_sync') {
        detail.handleSessionSyncEvent(data);
        if (data.status === 'done' || data.status === 'error' || data.status === 'cancelled') {
          void loadTasks();
        }
        return;
      }

      if (data.jobType === 'ai_review') {
        const taskId = data.taskId ?? '';
        if (!taskId) {
          return;
        }
        if (
          detail.selected?.id === taskId &&
          (data.status === 'done' || data.status === 'error' || data.status === 'cancelled')
        ) {
          void detail.refreshModelRuns();
        }
        if (data.status === 'done' || data.status === 'error' || data.status === 'cancelled') {
          void loadTasks();
        }
        return;
      }

      if (data.jobType !== 'prompt_generate') {
        return;
      }

      const taskId = data.taskId ?? '';
      if (!taskId) {
        return;
      }

      if (data.status === 'done') {
        useAppStore.setState((state) => ({
          tasks: state.tasks.map((task) =>
            task.id === taskId
              ? {
                  ...task,
                  status:
                    task.status === 'ExecutionCompleted' || task.status === 'Submitted'
                      ? task.status
                      : 'PromptReady',
                  promptGenerationStatus: 'done',
                  promptGenerationError: null,
                }
              : task,
          ),
        }));
        void loadTasks();
        return;
      }

      if (data.status === 'error') {
        useAppStore.setState((state) => ({
          tasks: state.tasks.map((task) =>
            task.id === taskId
              ? {
                  ...task,
                  promptGenerationStatus: 'error',
                  promptGenerationError: data.errorMessage ?? null,
                }
              : task,
          ),
        }));
        void loadTasks();
        return;
      }

      if (data.status === 'cancelled') {
        useAppStore.setState((state) => ({
          tasks: state.tasks.map((task) =>
            task.id === taskId
              ? {
                  ...task,
                  promptGenerationStatus: 'idle',
                  promptGenerationError: null,
                }
              : task,
          ),
        }));
        void loadTasks();
      }
    });

    return () => { cancel(); };
  }, [detail, loadTasks]);

  useEffect(() => {
    if (!taskCardContextMenu) {
      return undefined;
    }

    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target as Node;
      if (taskCardContextMenuRef.current?.contains(target)) {
        return;
      }
      // Also allow clicks inside the prompt-gen fly-out portal
      if ((target as Element).closest?.('[data-prompt-gen-flyout]')) {
        return;
      }
      closeTaskCardContextMenu();
    };

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        closeTaskCardContextMenu();
      }
    };

    const handleViewportChange = () => {
      closeTaskCardContextMenu();
    };

    window.addEventListener('pointerdown', handlePointerDown);
    window.addEventListener('keydown', handleKeyDown);
    window.addEventListener('resize', handleViewportChange);

    return () => {
      window.removeEventListener('pointerdown', handlePointerDown);
      window.removeEventListener('keydown', handleKeyDown);
      window.removeEventListener('resize', handleViewportChange);
    };
  }, [closeTaskCardContextMenu, taskCardContextMenu]);

  const shouldHandleDrawerEscape =
    Boolean(detail.selected) &&
    !taskCardContextMenu &&
    !pendingDelete &&
    !showProjectOverview &&
    !showProjectPanel &&
    detail.sessionExtractCandidates.length <= 1;

  useEffect(() => {
    resetDrawerEscCloseHint();
  }, [detail.selected?.id]);

  useEffect(() => {
    if (shouldHandleDrawerEscape) {
      return undefined;
    }
    resetDrawerEscCloseHint();
    return undefined;
  }, [shouldHandleDrawerEscape, detail.selected?.id]);

  useEffect(() => {
    if (!shouldHandleDrawerEscape) {
      return undefined;
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.repeat) {
        return;
      }

      event.preventDefault();
      event.stopPropagation();

      const now = Date.now();
      const lastPressedAt = drawerEscLastPressedAtRef.current;
      if (
        lastPressedAt !== null &&
        now - lastPressedAt <= DRAWER_ESC_CONFIRM_WINDOW_MS
      ) {
        closeDetailDrawer();
        return;
      }

      drawerEscLastPressedAtRef.current = now;
      setDrawerEscCloseHintVisible(true);
      clearDrawerEscCloseHintTimer();
      drawerEscCloseHintTimeoutRef.current = window.setTimeout(() => {
        drawerEscLastPressedAtRef.current = null;
        setDrawerEscCloseHintVisible(false);
        drawerEscCloseHintTimeoutRef.current = null;
      }, DRAWER_ESC_CONFIRM_WINDOW_MS);
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [shouldHandleDrawerEscape, detail.selected?.id]);

  useEffect(() => () => {
    clearDrawerEscCloseHintTimer();
  }, []);

  useEffect(() => {
    if (!selectionMode || detail.selected) return undefined;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        exitSelectionMode();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [selectionMode, detail.selected]);

  const toggleSelectionMode = () => {
    setSelectionMode((prev) => {
      if (prev) setSelectedTaskIds(new Set());
      return !prev;
    });
  };

  const toggleTaskSelection = (taskId: string) => {
    setSelectedTaskIds((prev) => {
      const next = new Set(prev);
      next.has(taskId) ? next.delete(taskId) : next.add(taskId);
      return next;
    });
  };

  const exitSelectionMode = () => {
    setSelectionMode(false);
    setSelectedTaskIds(new Set());
  };

  const handleGeneratePromptFromContextMenu = (constraints: string[], scope: string) => {
    if (!taskCardContextMenu) return;
    const { task } = taskCardContextMenu;
    closeTaskCardContextMenu();

    void (async () => {
      try {
        const inputPayload = JSON.stringify({
          taskId: task.id,
          taskType: task.taskType,
          constraints: constraints.length > 0 ? constraints : ['无约束'],
          scopes: scope ? [scope] : [],
          thinkingBudget: '',
        });
        await submitJob({
          jobType: 'prompt_generate',
          taskId: task.id,
          inputPayload,
          timeoutSeconds: 1200,
        });
        useAppStore.getState().loadBackgroundJobs();
      } finally {
        void loadTasks();
      }
    })();

    // Refresh quickly so the task card shows 'running' status
    window.setTimeout(() => void loadTasks(), 400);
  };

  const handleAiReview = (run: ModelRunFromDB) => {
    if (!run.localPath) return;
    const taskId = run.taskId?.trim();
    if (!taskId) return;
    const localPath = run.localPath.trim();
    if (!localPath) return;
    void (async () => {
      try {
        const committed = await ensureAiReviewCodeCommitted({
          taskId,
          modelRunId: run.id,
          run,
          onMessage: detail.setDrawerError,
        });
        if (!committed) {
          return;
        }
        await submitAiReviewJob(taskId, {
          modelRunId: run.id ?? null,
          modelName: run.modelName,
          localPath,
        });
        useAppStore.getState().loadBackgroundJobs();
        if (aiReviewVisible && detail.selected?.id === taskId) {
          detail.setActiveDrawerTab('ai-review');
          void detail.refreshModelRuns();
        }
      } catch (error) {
        detail.setDrawerError(error instanceof Error ? error.message : '提交 AI 复审失败');
      }
    })();
    window.setTimeout(() => {
      void loadTasks();
      if (detail.selected?.id === taskId) {
        void detail.refreshModelRuns();
      }
    }, 600);
  };

  const handleSubmitNextAiReviewRound = (
    modelRunId: string,
    modelName: string,
    localPath: string,
    nextPromptOverride?: string,
    reviewRoundId?: string,
  ) => {
    const taskId = detail.selected?.id?.trim();
    if (!taskId || !localPath) return;

    void (async () => {
      try {
        const matchingRun = detail.selectedModelRuns.find((run) => run.id === modelRunId) ?? null;
        const committed = await ensureAiReviewCodeCommitted({
          taskId,
          modelRunId,
          run: matchingRun,
          onMessage: detail.setDrawerError,
        });
        if (!committed) {
          return;
        }
        await submitAiReviewJob(taskId, {
          reviewRoundId: reviewRoundId ?? null,
          modelRunId: modelRunId ?? null,
          modelName,
          localPath,
          nextPromptOverride,
        });
        useAppStore.getState().loadBackgroundJobs();
        detail.setActiveDrawerTab('ai-review');
        void detail.refreshModelRuns();
      } catch (error) {
        detail.setDrawerError(error instanceof Error ? error.message : '提交下一轮 AI 复审失败');
      }
    })();

    window.setTimeout(() => {
      void loadTasks();
      void detail.refreshModelRuns();
    }, 600);
  };

  const handleTaskCardAiReview = (directory: TaskChildDirectory) => {
    const taskId = taskCardContextMenu?.task.id?.trim();
    const localPath = directory.path?.trim();
    if (!taskId || !localPath) return;

    setTaskCardContextMenuError('');
    setTaskCardQuickActionLoadingPath(localPath);
    void (async () => {
      try {
        const committed = await ensureAiReviewCodeCommitted({
          taskId,
          modelRunId: directory.modelRunId ?? null,
          onMessage: setTaskCardContextMenuError,
        });
        if (!committed) {
          return;
        }
        await submitAiReviewJob(taskId, {
          modelRunId: directory.modelRunId ?? null,
          modelName: directory.modelName?.trim() || directory.name,
          localPath,
        });
        useAppStore.getState().loadBackgroundJobs();
        if (aiReviewVisible && detail.selected?.id === taskId) {
          detail.setActiveDrawerTab('ai-review');
        }
        if (taskCardContextMenu?.task.id === taskId) {
          closeTaskCardContextMenu();
        }
      } catch (error) {
        if (taskCardContextMenu?.task.id === taskId) {
          setTaskCardContextMenuError(
            error instanceof Error ? error.message : '提交 AI 复审失败',
          );
        }
      } finally {
        if (detail.selected?.id === taskId) {
          void detail.refreshModelRuns();
        }
        setTaskCardQuickActionLoadingPath((current) => (current === localPath ? null : current));
      }
    })();
    window.setTimeout(() => {
      void loadTasks();
      if (detail.selected?.id === taskId) {
        void detail.refreshModelRuns();
      }
    }, 600);
  };

  const handleDeleteAiReviewRecord = async (jobId: string) => {
    await deleteAiReviewJob(jobId);
    await Promise.all([
      loadTasks(),
      detail.refreshModelRuns(),
      useAppStore.getState().loadBackgroundJobs(),
    ]);
  };

  const handleResetAiReviewRound = async (roundId: string) => {
    await detail.handleResetAiReviewRound(roundId);
  };

  const handleAfterBatchApply = async (
    field: 'status' | 'taskType',
    value: string,
    taskIds: string[],
  ) => {
    if (field !== 'status' || value !== 'ExecutionCompleted' || taskIds.length === 0) {
      return;
    }

    await Promise.allSettled(taskIds.map((taskId) => submitSessionSyncJob(taskId)));
    void useAppStore.getState().loadBackgroundJobs();
  };

  const handleOpenTaskLocalFolder = async () => {
    if (!taskCardContextMenu || taskCardFolderOpening) {
      return;
    }

    setTaskCardFolderOpening(true);
    setTaskCardContextMenuError('');
    try {
      await openTaskLocalFolder(taskCardContextMenu.task.id);
      closeTaskCardContextMenu();
    } catch (error) {
      setTaskCardContextMenuError(error instanceof Error ? error.message : '打开本地目录失败');
      setTaskCardFolderOpening(false);
    }
  };

  useEffect(() => {
    const allowedTaskTypes = new Set(availableTaskTypes);
    setActiveTypes((prev) => {
      const next = new Set([...prev].filter((taskType) => allowedTaskTypes.has(taskType)));
      if (next.size === prev.size && [...next].every((taskType) => prev.has(taskType))) {
        return prev;
      }
      return next;
    });
  }, [availableTaskTypes]);

  useEffect(() => {
    const allowedRounds = new Set(availableExecutionRounds);
    setActiveRounds((prev) => {
      const next = new Set([...prev].filter((round) => allowedRounds.has(round)));
      if (next.size === prev.size && [...next].every((round) => prev.has(round))) {
        return prev;
      }
      return next;
    });
  }, [availableExecutionRounds]);

  const filtered = useMemo(
    () => filterBoardTasks(tasks, { search, activeTypes, activeStages, activeRounds, activeReviewStatuses }),
    [tasks, search, activeTypes, activeStages, activeRounds, activeReviewStatuses],
  );

  // 各维度的计数基准：排除自身维度，反映其他维度当前筛选的结果
  const tasksForStageCount = useMemo(
    () => filterBoardTasks(tasks, { search, activeTypes, activeStages: new Set(), activeRounds, activeReviewStatuses }),
    [tasks, search, activeTypes, activeRounds, activeReviewStatuses],
  );
  const tasksForRoundCount = useMemo(
    () => filterBoardTasks(tasks, { search, activeTypes, activeStages, activeRounds: new Set(), activeReviewStatuses }),
    [tasks, search, activeTypes, activeStages, activeReviewStatuses],
  );
  const tasksForReviewStatusCount = useMemo(
    () => filterBoardTasks(tasks, { search, activeTypes, activeStages, activeRounds, activeReviewStatuses: new Set() }),
    [tasks, search, activeTypes, activeStages, activeRounds],
  );

  const sortedTasks = useMemo(() => sortBoardTasks(filtered, sortBy), [filtered, sortBy]);

  const groupedTasks = useMemo(
    () => groupBoardTasks(availableTaskTypes, sortedTasks),
    [availableTaskTypes, sortedTasks],
  );

  const collapsedGroups = useMemo(
    () =>
      new Set(
        groupedTasks
          .map((group) => group.groupKey)
          .filter((groupKey) => !expandedGroups.has(groupKey)),
      ),
    [expandedGroups, groupedTasks],
  );

  useEffect(() => {
    saveExpandedGroupsToStorage(expandedGroups);
  }, [expandedGroups]);

  useEffect(() => {
    saveCardSizeToStorage(cardSize);
  }, [cardSize]);

  const projectTaskSummaries = useMemo(
    () => buildTaskTypeOverviewSummaries(availableTaskTypes, tasks, projectQuotas, projectTotals),
    [availableTaskTypes, tasks, projectQuotas, projectTotals],
  );
  const projectTaskRemainingToCompleteByType = useMemo(
    () =>
      Object.fromEntries(
        projectTaskSummaries.map((summary) => [
          summary.taskType,
          summary.remainingToCompleteCount,
        ]),
      ) as Record<string, number | null>,
    [projectTaskSummaries],
  );

  const visibleProjectTaskSummaries = useMemo(
    () =>
      projectTaskSummaries.filter((summary) =>
        summary.remainingQuota !== null ||
        summary.waitingTasks.length > 0 ||
        summary.processingTasks.length > 0 ||
        summary.submittedSessionCount > 0 ||
        summary.errorTasks.length > 0,
      ),
    [projectTaskSummaries],
  );

  const gridClassBySize: Record<CardSize, string> = {
    sm: 'grid-cols-2 sm:grid-cols-3 md:grid-cols-4 xl:grid-cols-5',
    four: 'grid-cols-1 sm:grid-cols-2 md:grid-cols-3 xl:grid-cols-4',
    md: 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-3',
    lg: 'grid-cols-1 sm:grid-cols-2',
  };

  const handleDeleteTask = async () => {
    if (!pendingDelete) return;
    setDeleting(true);
    setDeleteError('');
    try {
      await deleteTask(pendingDelete.id);
      removeTaskFromStore(pendingDelete.id);
      if (detail.selected?.id === pendingDelete.id) detail.setSelected(null);
      setPendingDelete(null);
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : '删除题卡失败');
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div className="h-full flex flex-col overflow-hidden">
      <BoardMainContent
        tableProgressByTask={tableSummary.projectId === activeProject?.id ? tableSummary.byTask : undefined}
        containerActionStateByTaskId={containerActions.stateByTaskId}
        search={search}
        sortBy={sortBy}
        totalTaskCount={tasks.length}
        availableTaskTypes={availableTaskTypes}
        activeTypes={activeTypes}
        activeStages={activeStages}
        activeRounds={activeRounds}
        activeReviewStatuses={activeReviewStatuses}
        cardSize={cardSize}
        hasFilters={hasFilters}
        availableExecutionRounds={availableExecutionRounds}
        tasks={tasks}
        tasksForStageCount={tasksForStageCount}
        tasksForRoundCount={tasksForRoundCount}
        tasksForReviewStatusCount={tasksForReviewStatusCount}
        sortedTasks={sortedTasks}
        groupedTasks={groupedTasks}
        visibleProjectTaskSummaries={visibleProjectTaskSummaries}
        gridClass={gridClassBySize[cardSize]}
        collapsedGroups={collapsedGroups}
        onSearchChange={setSearch}
        onClearSearch={() => setSearch('')}
        onSortChange={setSortBy}
        onCardSizeChange={setCardSize}
        onOpenProjectPanel={() => setShowProjectPanel(true)}
        onOpenProjectOverview={() => setShowProjectOverview(true)}
        onToggleType={toggleType}
        onToggleStage={toggleStage}
        onToggleRound={toggleRound}
        onToggleReviewStatus={toggleReviewStatus}
        onClearFilters={clearFilters}
        onToggleGroupCollapse={toggleGroupCollapse}
        onSelectTask={detail.setSelected}
        onCopyContainerCommand={(task) => void containerActions.copyContainerCommand(task)}
        onBindContainerAndCopyPrompt={(task) => void containerActions.bindContainerAndCopyPrompt(task)}
        onOpenTaskContextMenu={openTaskCardContextMenu}
        onDeleteTask={(task) => {
          setDeleteError('');
          setPendingDelete(task);
        }}
        selectionMode={selectionMode}
        selectedTaskIds={selectedTaskIds}
        onToggleSelectionMode={toggleSelectionMode}
        onToggleTaskSelection={toggleTaskSelection}
      />

      {containerActions.feedback && (
        <div
          role={containerActions.feedback.tone === 'error' ? 'alert' : 'status'}
          className={`fixed bottom-5 right-5 z-50 flex max-w-sm items-center gap-3 rounded-2xl border px-4 py-3 text-sm font-medium shadow-xl ${
            containerActions.feedback.tone === 'error'
              ? 'border-red-200 bg-red-50 text-red-700 dark:border-red-900/60 dark:bg-red-950/90 dark:text-red-300'
              : 'border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-900/60 dark:bg-blue-950/90 dark:text-blue-300'
          }`}
        >
          <span>{containerActions.feedback.message}</span>
          <button
            type="button"
            className="rounded-lg px-1.5 py-0.5 text-xs opacity-70 hover:opacity-100"
            onClick={containerActions.clearFeedback}
          >
            关闭
          </button>
        </div>
      )}

      <BoardLayerStack
        taskCardContextMenu={taskCardContextMenu}
        taskCardContextMenuRef={taskCardContextMenuRef}
        availableTaskTypes={availableTaskTypes}
        statusOptions={COLUMNS}
        localFolderOpening={taskCardFolderOpening}
        contextMenuChildDirectories={taskCardChildDirectories}
        contextMenuChildDirectoriesLoading={taskCardChildDirectoriesLoading}
        quickActionLoadingPath={taskCardQuickActionLoadingPath}
        actionError={taskCardContextMenuError}
        onOpenLocalFolder={() => {
          void handleOpenTaskLocalFolder();
        }}
        onTaskCardStatusChange={(status) => {
          if (!taskCardContextMenu) return;
          closeTaskCardContextMenu();
          void detail.handleStatusChange(taskCardContextMenu.task.id, status);
        }}
        onTaskCardGeneratePrompt={handleGeneratePromptFromContextMenu}
        onTaskCardTaskTypeChange={async (taskType) => {
          if (!taskCardContextMenu) return;
          setTaskCardContextMenuError('');
          const result = await detail.handleTaskTypeChange(
            taskCardContextMenu.task.id,
            taskType,
            { skipConfirm: true },
          );
          if (result.ok) {
            closeTaskCardContextMenu();
            return;
          }
          if (result.error) {
            setTaskCardContextMenuError(result.error);
          }
        }}
        onTaskCardQuickAiReview={aiReviewVisible ? handleTaskCardAiReview : undefined}
        showProjectOverview={showProjectOverview}
        activeProject={activeProject}
        taskCount={tasks.length}
        onCloseProjectOverview={() => setShowProjectOverview(false)}
        onNormalizeProjectOverview={loadTasks}
        pendingDelete={pendingDelete}
        deleting={deleting}
        deleteError={deleteError}
        onCancelDelete={() => {
          setPendingDelete(null);
          setDeleteError('');
        }}
        onConfirmDelete={() => void handleDeleteTask()}
        detail={detail}
        detailEscCloseHintVisible={drawerEscCloseHintVisible}
        onCloseDetailDrawer={closeDetailDrawer}
        taskTypeRemainingToCompleteByType={projectTaskRemainingToCompleteByType}
        sourceModelName={sourceModelName}
        onOpenSubmit={() => {
          if (!detail.selected) return;
          closeDetailDrawer();
          navigate(`/submit?taskId=${detail.selected.id}`);
        }}
        showProjectPanel={showProjectPanel}
        onCloseProjectPanel={() => setShowProjectPanel(false)}
        onProjectSaved={(updated) => {
          setActiveProject(updated);
          setShowProjectPanel(false);
        }}
        onAiReview={aiReviewVisible ? handleAiReview : undefined}
        onDeleteAiReviewRecord={aiReviewVisible ? handleDeleteAiReviewRecord : undefined}
        onResetAiReviewRound={aiReviewVisible ? handleResetAiReviewRound : undefined}
        onSubmitNextAiReviewRound={aiReviewVisible ? handleSubmitNextAiReviewRound : undefined}
      />

      {selectionMode && selectedTaskIds.size > 0 && (
        <BatchActionBar
          selectedCount={selectedTaskIds.size}
          selectedTaskIds={selectedTaskIds}
          availableTaskTypes={availableTaskTypes}
          onAfterApply={handleAfterBatchApply}
          onAfterDelete={async (taskIds) => {
            taskIds.forEach((id) => removeTaskFromStore(id));
            if (detail.selected && taskIds.includes(detail.selected.id)) {
              detail.setSelected(null);
            }
            await loadTasks();
          }}
          onDone={() => {
            void loadTasks();
            exitSelectionMode();
          }}
          onCancel={exitSelectionMode}
        />
      )}
    </div>
  );
}
