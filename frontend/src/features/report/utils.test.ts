import { describe, expect, it } from 'vitest';
import type { TaskFromDB, ModelRunFromDB, AiReviewRoundFromDB } from '../../api/task';
import type { ReportRow } from './types';
import { assembleReportRows, buildReportMarkdown, resolveReportRepoId } from './utils';

function createTask(overrides: Partial<TaskFromDB> = {}): TaskFromDB {
  return {
    id: 'task-1',
    gitlabProjectId: 1849,
    projectName: 'label-01849',
    status: 'Claimed',
    taskType: overrides.taskType ?? '未归类',
    sessionList: overrides.sessionList ?? [],
    localPath: null,
    promptText: overrides.promptText ?? null,
    promptDifficulty: overrides.promptDifficulty ?? '一般',
    promptGenerationStatus: 'idle',
    promptGenerationError: null,
    promptGenerationStartedAt: null,
    promptGenerationFinishedAt: null,
    createdAt: 1,
    updatedAt: 1,
    notes: null,
    projectConfigId: 'project-1',
    projectType: '',
    changeScope: '',
    ...overrides,
  };
}

function createModelRun(overrides: Partial<ModelRunFromDB> = {}): ModelRunFromDB {
  return {
    id: 'run-1',
    taskId: 'task-1',
    modelName: 'cotv21-pro',
    branchName: null,
    localPath: null,
    prUrl: null,
    originUrl: null,
    gsbScore: null,
    status: 'pending',
    startedAt: null,
    finishedAt: null,
    sessionId: null,
    conversationRounds: 0,
    conversationDate: null,
    submitError: null,
    reviewStatus: 'none',
    reviewRound: 0,
    reviewNotes: null,
    ...overrides,
  };
}

describe('report utils', () => {
  it('uses readable project names for local imported tasks', () => {
    expect(
      resolveReportRepoId(
        createTask({
          id: 'pproject-1__bug__label-8123456789012345-1',
          gitlabProjectId: 8_123_456_789_012_345,
          projectName: 'B-198',
        }),
      ),
    ).toBe('B-198-1');
  });

  it('keeps numeric repo ids for regular gitlab tasks', () => {
    expect(resolveReportRepoId(createTask({ gitlabProjectId: 1849 }))).toBe('1849');
  });

  it('sorts rows by displayed repo id with natural order', () => {
    const tasks = [
      createTask({
        id: 'pproject-1__bug__label-8123456789012346-2',
        gitlabProjectId: 8_123_456_789_012_346,
        projectName: 'B-10',
      }),
      createTask({
        id: 'pproject-1__bug__label-8123456789012345-1',
        gitlabProjectId: 8_123_456_789_012_345,
        projectName: 'B-2',
      }),
    ];

    const modelRunsByTask = new Map<string, ModelRunFromDB[]>(
      tasks.map((task) => [
        task.id,
        [
          createModelRun({
            id: `run-${task.id}`,
            taskId: task.id,
            sessionList: [
              {
                sessionId: `${task.id}-session`,
                taskType: '未归类',
                consumeQuota: true,
              },
            ],
          }),
        ],
      ]),
    );

    const rows = assembleReportRows(
      tasks,
      modelRunsByTask,
      new Map<string, AiReviewRoundFromDB[]>(),
    );

    expect(rows.map((row) => row.repoId)).toEqual(['B-2-1', 'B-10-2']);
  });

  it('uses failed review round decisions over default successful session flags', () => {
    const task = createTask({ id: 'task-review-fail' });
    const modelRun = createModelRun({
      id: 'run-review-fail',
      taskId: task.id,
      sessionList: [
        {
          sessionId: 'session-fail',
          taskType: 'Bug修复',
          consumeQuota: true,
          isCompleted: true,
          isSatisfied: true,
          evaluation: '',
        },
      ],
    });
    const failedRound: AiReviewRoundFromDB = {
      id: 'round-fail',
      taskId: task.id,
      modelRunId: modelRun.id,
      localPath: '/tmp/project',
      modelName: 'cotv21-pro',
      roundNumber: 1,
      originalPrompt: '修复列表筛选异常',
      promptText: '修复列表筛选异常',
      promptDifficulty: '一般',
      status: 'warning',
      isCompleted: false,
      isSatisfied: false,
      reviewNotes: '复审执行失败：stream disconnected',
      dissatisfactionSummary: '',
      nextPrompt: '',
      nextPromptTaskType: '未归类',
      projectType: '',
      changeScope: '',
      keyLocations: '',
      jobId: 'job-fail',
      createdAt: 1,
      updatedAt: 2,
    };

    const rows = assembleReportRows(
      [task],
      new Map([[task.id, [modelRun]]]),
      new Map([[task.id, [failedRound]]]),
    );

    expect(rows).toHaveLength(1);
    expect(rows[0].isCompleted).toBe(false);
    expect(rows[0].isSatisfied).toBe(false);
    expect(rows[0].dissatisfactionReason).toBe('复审执行失败：stream disconnected');
  });

  it('builds markdown grouped by repo and task', () => {
    const rows: ReportRow[] = [
      {
        taskId: 'task-1',
        repoId: '2031',
        sessionId: 'session-a',
        sessionIndex: 0,
        promptText: '修复登录报错',
        taskType: 'bug',
        projectType: 'Web前端',
        changeScope: '单文件',
        isCompleted: true,
        isSatisfied: false,
        dissatisfactionReason: '样式未对齐',
        aiProjectType: '',
        aiChangeScope: '',
      },
      {
        taskId: 'task-1',
        repoId: '2031',
        sessionId: 'session-b',
        sessionIndex: 1,
        promptText: '修复登录报错',
        taskType: 'bug',
        projectType: 'Web前端',
        changeScope: '单文件',
        isCompleted: false,
        isSatisfied: true,
        dissatisfactionReason: '',
        aiProjectType: '',
        aiChangeScope: '',
      },
      {
        taskId: 'task-2',
        repoId: '2031',
        sessionId: '',
        sessionIndex: -1,
        promptText: '补充列表筛选',
        taskType: 'feature',
        projectType: '',
        changeScope: '',
        isCompleted: null,
        isSatisfied: null,
        dissatisfactionReason: '',
        aiProjectType: '全栈Web应用',
        aiChangeScope: '模块内多文件',
      },
      {
        taskId: 'task-3',
        repoId: '2040',
        sessionId: 'session-c',
        sessionIndex: 0,
        promptText: '完善导出文档',
        taskType: 'refactor',
        projectType: '纯后端服务',
        changeScope: '跨模块多文件',
        isCompleted: true,
        isSatisfied: true,
        dissatisfactionReason: '',
        aiProjectType: '',
        aiChangeScope: '',
      },
    ];

    expect(buildReportMarkdown(rows)).toBe(`# 报表 Markdown 清单

## 项目编号：2031

### 任务 1
- 任务类型：bug
- Prompt：修复登录报错
- 业务领域：Web前端
- 修改范围：单文件
- 是否完成：第1次：是；第2次：否
- 是否满意：第1次：否；第2次：是
- 不满意原因/点评：第1次：样式未对齐；第2次：未填写

### 任务 2
- 任务类型：feature
- Prompt：补充列表筛选
- 业务领域：全栈Web应用
- 修改范围：模块内多文件
- 是否完成：未填写
- 是否满意：未填写
- 不满意原因/点评：未填写

## 项目编号：2040

### 任务 1
- 任务类型：refactor
- Prompt：完善导出文档
- 业务领域：纯后端服务
- 修改范围：跨模块多文件
- 是否完成：是
- 是否满意：是
- 不满意原因/点评：未填写`);
  });

  it('returns fallback markdown when rows are empty', () => {
    expect(buildReportMarkdown([])).toBe('# 报表 Markdown 清单\n\n暂无数据');
  });
});
