import { useCallback, useEffect, useRef, useState } from 'react';
import {
  bindContainer,
  listCases,
  listContainers,
  prepareCase,
  publishSnapshot,
  type AnnotationCase,
} from '../../../api/annotation';
import { getAnnotationContainerApiKey } from '../../../api/config';
import type { BackgroundJob } from '../../../api/job';
import { getTask } from '../../../api/task';
import { writeClipboardText } from '../../../shared/lib/clipboard';
import type { Task } from '../../../store';
import { buildContainerCommand } from '../../annotation/containerCommand';
import { waitForAnnotationJob } from '../../annotation/job';

export type TaskCardContainerActionState = {
  commandCopied: boolean;
  bindPromptCompleted: boolean;
  busyAction: 'command' | 'bind' | null;
  busyLabel: string;
};

export type TaskCardContainerFeedback = {
  tone: 'success' | 'error';
  message: string;
};

const EMPTY_ACTION_STATE: TaskCardContainerActionState = {
  commandCopied: false,
  bindPromptCompleted: false,
  busyAction: null,
  busyLabel: '',
};

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}

function folderName(path: string) {
  const normalized = path.replace(/[\\/]+$/, '');
  return normalized.split(/[\\/]/).pop() || 'repository';
}

function parseCaseOutput(job: BackgroundJob, label: string): AnnotationCase {
  if (!job.outputPayload) throw new Error(`${label}完成，但没有返回题目结果`);
  try {
    return JSON.parse(job.outputPayload) as AnnotationCase;
  } catch {
    throw new Error(`${label}完成，但返回结果无法读取`);
  }
}

export function useTaskCardContainerActions({
  projectId,
  onAnnotationChanged,
}: {
  projectId: string | null;
  onAnnotationChanged?: () => void;
}) {
  const [stateByTaskId, setStateByTaskId] = useState<Record<string, TaskCardContainerActionState>>({});
  const [feedback, setFeedback] = useState<TaskCardContainerFeedback | null>(null);
  const activeProjectIdRef = useRef(projectId);
  const busyTaskIdsRef = useRef(new Set<string>());

  useEffect(() => {
    activeProjectIdRef.current = projectId;
    busyTaskIdsRef.current.clear();
    setStateByTaskId({});
    setFeedback(null);
  }, [projectId]);

  const updateTaskState = useCallback((taskId: string, patch: Partial<TaskCardContainerActionState>) => {
    setStateByTaskId((current) => ({
      ...current,
      [taskId]: { ...EMPTY_ACTION_STATE, ...current[taskId], ...patch },
    }));
  }, []);

  const setBusyLabel = useCallback((taskId: string, action: 'command' | 'bind', label: string) => {
    updateTaskState(taskId, { busyAction: action, busyLabel: label });
  }, [updateTaskState]);

  const loadCase = useCallback(async (taskId: string, targetProjectId: string) => {
    const cases = await listCases(targetProjectId);
    const annotationCase = cases.find((item) => item.taskId === taskId);
    if (!annotationCase) throw new Error('当前项目中没有找到该题目的标注记录');
    return annotationCase;
  }, []);

  const waitForCaseJob = useCallback(async (submitted: BackgroundJob, label: string) => {
    const finished = await waitForAnnotationJob(submitted.id);
    return parseCaseOutput(finished, label);
  }, []);

  const ensureSnapshot = useCallback(async (
    current: AnnotationCase,
    action: 'command' | 'bind',
    targetProjectId: string,
  ) => {
    let annotationCase = current;
    if (!annotationCase.initialSha.trim()) {
      setBusyLabel(annotationCase.taskId, action, '正在准备初始快照');
      annotationCase = await waitForCaseJob(await prepareCase(annotationCase.taskId), '准备初始快照');
      if (activeProjectIdRef.current !== targetProjectId) return null;
      onAnnotationChanged?.();
    }
    if (!annotationCase.snapshotUrl.trim()) {
      setBusyLabel(annotationCase.taskId, action, '正在发布 GitHub 初始快照');
      annotationCase = await waitForCaseJob(await publishSnapshot(annotationCase.taskId), '发布 GitHub 初始快照');
      if (activeProjectIdRef.current !== targetProjectId) return null;
      onAnnotationChanged?.();
    }
    if (!annotationCase.initialSha.trim() || !annotationCase.snapshotUrl.trim()) {
      throw new Error('初始快照地址缺失，请检查 GitHub 账号配置后重试');
    }
    return annotationCase;
  }, [onAnnotationChanged, setBusyLabel, waitForCaseJob]);

  const runAction = useCallback(async (
    task: Task,
    action: 'command' | 'bind',
    work: (targetProjectId: string) => Promise<string>,
  ) => {
    const targetProjectId = projectId?.trim();
    if (!targetProjectId) {
      setFeedback({ tone: 'error', message: '当前没有可用项目' });
      return false;
    }
    if (busyTaskIdsRef.current.has(task.id)) return false;
    busyTaskIdsRef.current.add(task.id);
    setFeedback(null);
    setBusyLabel(task.id, action, action === 'command' ? '正在检查初始快照' : '正在刷新容器');
    try {
      const message = await work(targetProjectId);
      if (activeProjectIdRef.current !== targetProjectId) return false;
      setFeedback({ tone: 'success', message });
      return true;
    } catch (error) {
      if (activeProjectIdRef.current === targetProjectId) {
        setFeedback({ tone: 'error', message: errorMessage(error) });
      }
      return false;
    } finally {
      busyTaskIdsRef.current.delete(task.id);
      if (activeProjectIdRef.current === targetProjectId) {
        updateTaskState(task.id, { busyAction: null, busyLabel: '' });
      }
    }
  }, [projectId, setBusyLabel, updateTaskState]);

  const copyContainerCommand = useCallback(async (task: Task) => runAction(
    task,
    'command',
    async (targetProjectId) => {
      const loaded = await loadCase(task.id, targetProjectId);
      const annotationCase = await ensureSnapshot(loaded, 'command', targetProjectId);
      if (!annotationCase) return '';
      setBusyLabel(task.id, 'command', '正在复制容器启动命令');
      const apiKey = await getAnnotationContainerApiKey();
      await writeClipboardText(buildContainerCommand(annotationCase, apiKey).command);
      updateTaskState(task.id, { commandCopied: true });
      return '容器启动命令已复制';
    },
  ), [ensureSnapshot, loadCase, runAction, setBusyLabel, updateTaskState]);

  const bindContainerAndCopyPrompt = useCallback(async (task: Task) => runAction(
    task,
    'bind',
    async (targetProjectId) => {
      const loaded = await loadCase(task.id, targetProjectId);
      const annotationCase = await ensureSnapshot(loaded, 'bind', targetProjectId);
      if (!annotationCase) return '';

      const taskDetail = await getTask(task.id);
      const prompt = taskDetail?.promptText?.trim() ?? '';
      if (!prompt) throw new Error('该题目还没有可复制的提示词');

      setBusyLabel(task.id, 'bind', '正在刷新并匹配容器');
      const containers = await listContainers();
      const expectedName = buildContainerCommand(annotationCase).containerName.toLowerCase();
      const matches = containers.filter((container) => container.name.toLowerCase() === expectedName);
      if (matches.length === 0) throw new Error(`未找到本题容器 ${expectedName}，请先执行容器启动命令`);
      if (matches.length > 1) throw new Error(`找到多个同名容器 ${expectedName}，请进入详情手动确认`);
      const matched = matches[0];
      if (matched.state !== 'running') throw new Error(`容器 ${matched.name} 未运行，请启动后重试`);

      if (annotationCase.containerId !== matched.id) {
        setBusyLabel(task.id, 'bind', '正在复制仓库并绑定容器');
        await waitForCaseJob(await bindContainer({
          taskId: task.id,
          containerId: matched.id,
          repoRelativePath: annotationCase.repoRelativePath || folderName(annotationCase.sourcePath),
          copyRepository: true,
        }), '复制并绑定容器');
        if (activeProjectIdRef.current !== targetProjectId) return '';
        onAnnotationChanged?.();
      }

      setBusyLabel(task.id, 'bind', '容器已绑定，正在复制提示词');
      await writeClipboardText(prompt);
      updateTaskState(task.id, { bindPromptCompleted: true });
      return '容器已绑定，提示词已复制';
    },
  ), [ensureSnapshot, loadCase, onAnnotationChanged, runAction, setBusyLabel, updateTaskState, waitForCaseJob]);

  return {
    stateByTaskId,
    feedback,
    clearFeedback: () => setFeedback(null),
    copyContainerCommand,
    bindContainerAndCopyPrompt,
  };
}
