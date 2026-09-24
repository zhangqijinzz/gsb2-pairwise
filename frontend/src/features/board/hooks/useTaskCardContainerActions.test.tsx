import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { AnnotationCase } from '../../../api/annotation';
import type { BackgroundJob } from '../../../api/job';
import type { TaskFromDB } from '../../../api/task';
import type { Task } from '../../../store';
import { useTaskCardContainerActions } from './useTaskCardContainerActions';

const api = vi.hoisted(() => ({
  bindContainer: vi.fn(),
  getAnnotationContainerApiKey: vi.fn(),
  getTask: vi.fn(),
  listCases: vi.fn(),
  listContainers: vi.fn(),
  prepareCase: vi.fn(),
  publishSnapshot: vi.fn(),
  waitForAnnotationJob: vi.fn(),
  writeClipboardText: vi.fn(),
}));

vi.mock('../../../api/annotation', async () => ({
  ...await vi.importActual<typeof import('../../../api/annotation')>('../../../api/annotation'),
  bindContainer: api.bindContainer,
  listCases: api.listCases,
  listContainers: api.listContainers,
  prepareCase: api.prepareCase,
  publishSnapshot: api.publishSnapshot,
}));

vi.mock('../../../api/config', async () => ({
  ...await vi.importActual<typeof import('../../../api/config')>('../../../api/config'),
  getAnnotationContainerApiKey: api.getAnnotationContainerApiKey,
}));

vi.mock('../../../api/task', async () => ({
  ...await vi.importActual<typeof import('../../../api/task')>('../../../api/task'),
  getTask: api.getTask,
}));

vi.mock('../../../shared/lib/clipboard', () => ({
  writeClipboardText: api.writeClipboardText,
}));

vi.mock('../../annotation/job', () => ({
  waitForAnnotationJob: api.waitForAnnotationJob,
}));

const SHA = 'a'.repeat(40);

function makeCase(overrides: Partial<AnnotationCase> = {}): AnnotationCase {
  return {
    taskId: 'p1__feat__label-123-11',
    projectId: 'project-1',
    taskName: 'xh-05',
    sourcePath: '/tasks/xh-05-bug修复-11',
    initialSha: SHA,
    snapshotUrl: `https://github.com/example/xh-05-11/commit/${SHA}`,
    containerId: '',
    containerName: '',
    workspacePath: '',
    repoRelativePath: 'xh-05-bug修复-11',
    sessionId: '',
    tracePath: '',
    completed: false,
    rounds: [],
    captures: [],
    revision: 1,
    updatedAt: 1,
    ...overrides,
  };
}

function makeTask(): Task {
  return {
    id: 'p1__feat__label-123-11',
    projectId: '123',
    projectName: 'xh-05',
    status: 'PromptReady',
    taskType: 'Bug修复',
    sessionList: [],
    promptDifficulty: '困难',
    promptGenerationStatus: 'done',
    promptGenerationError: null,
    createdAt: 1,
    executionRounds: 1,
    aiReviewRounds: 0,
    aiReviewStatus: 'none',
    hasCollectedPairwiseGsb: false,
    hasGeneratedGsb: false,
    progress: 0,
    totalModels: 0,
    runningModels: 0,
  };
}

function makeTaskDetail(): TaskFromDB {
  return {
    id: 'p1__feat__label-123-11',
    gitlabProjectId: 123,
    projectName: 'xh-05',
    status: 'PromptReady',
    taskType: 'Bug修复',
    sessionList: [],
    localPath: '/tasks/xh-05-bug修复-11',
    promptText: '修复订单筛选逻辑',
    promptGenerationStatus: 'done',
    promptGenerationError: null,
    promptGenerationStartedAt: 1,
    promptGenerationFinishedAt: 2,
    promptDifficulty: '困难',
    createdAt: 1,
    updatedAt: 2,
    notes: null,
    projectConfigId: 'project-1',
    projectType: '',
    changeScope: '',
  };
}

function makeJob(id: string, output?: AnnotationCase): BackgroundJob {
  return {
    id,
    jobType: 'annotation_prepare',
    taskId: 'p1__feat__label-123-11',
    status: output ? 'done' : 'pending',
    progress: output ? 100 : 0,
    progressMessage: null,
    errorMessage: null,
    inputPayload: '{}',
    outputPayload: output ? JSON.stringify(output) : null,
    retryCount: 0,
    maxRetries: 1,
    timeoutSeconds: 1800,
    createdAt: 1,
    startedAt: null,
    finishedAt: null,
  };
}

describe('useTaskCardContainerActions', () => {
  beforeEach(() => {
    Object.values(api).forEach((mock) => mock.mockReset());
    api.getAnnotationContainerApiKey.mockResolvedValue('saved-key');
    api.writeClipboardText.mockResolvedValue(undefined);
  });

  it('prepares and publishes a missing snapshot before copying the container command', async () => {
    const initial = makeCase({ initialSha: '', snapshotUrl: '' });
    const prepared = makeCase({ snapshotUrl: '', revision: 2 });
    const published = makeCase({ revision: 3 });
    api.listCases.mockResolvedValue([initial]);
    api.prepareCase.mockResolvedValue(makeJob('prepare-job'));
    api.publishSnapshot.mockResolvedValue(makeJob('publish-job'));
    api.waitForAnnotationJob
      .mockResolvedValueOnce(makeJob('prepare-job', prepared))
      .mockResolvedValueOnce(makeJob('publish-job', published));

    const { result } = renderHook(() => useTaskCardContainerActions({ projectId: 'project-1' }));

    await act(async () => {
      await result.current.copyContainerCommand(makeTask());
    });

    expect(api.prepareCase).toHaveBeenCalledWith(initial.taskId);
    expect(api.publishSnapshot).toHaveBeenCalledWith(initial.taskId);
    expect(api.prepareCase.mock.invocationCallOrder[0]).toBeLessThan(api.publishSnapshot.mock.invocationCallOrder[0]);
    expect(api.publishSnapshot.mock.invocationCallOrder[0]).toBeLessThan(api.writeClipboardText.mock.invocationCallOrder[0]);
    expect(api.writeClipboardText).toHaveBeenCalledWith(expect.stringContaining('CONTAINER_NAME="xh05-claude-11"'));
    expect(api.writeClipboardText).toHaveBeenCalledWith(expect.stringContaining("apikey='saved-key'"));
    expect(result.current.stateByTaskId[initial.taskId]?.commandCopied).toBe(true);
    expect(result.current.feedback).toEqual({ tone: 'success', message: '容器启动命令已复制' });
  });

  it('binds the exact running container before copying the persisted prompt', async () => {
    const current = makeCase();
    const bound = makeCase({
      containerId: 'container-11',
      containerName: 'xh05-claude-11',
      workspacePath: '/workspace',
      revision: 2,
    });
    api.listCases.mockResolvedValue([current]);
    api.listContainers.mockResolvedValue([
      { id: 'container-1', name: 'xh05-claude-1', state: 'running', image: 'claude', workspacePath: '/workspace-1' },
      { id: 'container-11', name: 'xh05-claude-11', state: 'running', image: 'claude', workspacePath: '/workspace-11' },
    ]);
    api.bindContainer.mockResolvedValue(makeJob('bind-job'));
    api.waitForAnnotationJob.mockResolvedValue(makeJob('bind-job', bound));
    api.getTask.mockResolvedValue(makeTaskDetail());

    const { result } = renderHook(() => useTaskCardContainerActions({ projectId: 'project-1' }));

    await act(async () => {
      await result.current.bindContainerAndCopyPrompt(makeTask());
    });

    expect(api.bindContainer).toHaveBeenCalledWith({
      taskId: current.taskId,
      containerId: 'container-11',
      repoRelativePath: 'xh-05-bug修复-11',
      copyRepository: true,
    });
    expect(api.bindContainer.mock.invocationCallOrder[0]).toBeLessThan(api.writeClipboardText.mock.invocationCallOrder[0]);
    expect(api.writeClipboardText).toHaveBeenCalledWith('修复订单筛选逻辑');
    expect(result.current.stateByTaskId[current.taskId]?.bindPromptCompleted).toBe(true);
    expect(result.current.feedback).toEqual({ tone: 'success', message: '容器已绑定，提示词已复制' });
  });
});
