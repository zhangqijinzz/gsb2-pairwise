import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { AnnotationCase } from '../../api/annotation';
import { AnnotationWorkspace } from './index';

const api = vi.hoisted(() => ({
  getConfig: vi.fn(),
  getAnnotationContainerApiKey: vi.fn(),
  getProjects: vi.fn(),
  bindContainer: vi.fn(),
  bindPairwiseContainer: vi.fn(),
  batchCaptureAndPrepareTable: vi.fn(),
  batchReviewPairwise: vi.fn(),
  cancelAnnotationJob: vi.fn(),
  captureAndPrepareTable: vi.fn(),
  capturePairwiseSide: vi.fn(),
  enablePairwise: vi.fn(),
  exportCases: vi.fn(),
  getAnnotationJob: vi.fn(),
  listCases: vi.fn(),
  listContainers: vi.fn(),
  listTraces: vi.fn(),
  preflight: vi.fn(),
  preflightPairwise: vi.fn(),
  prepareCase: vi.fn(),
  reviewRound: vi.fn(),
  reviewPairwise: vi.fn(),
  exportPairwise: vi.fn(),
  saveCaseSettings: vi.fn(),
  savePairwiseSettings: vi.fn(),
}));

const wailsClipboard = vi.hoisted(() => ({
  setText: vi.fn(),
}));

vi.mock('@wailsio/runtime', async () => ({
  ...await vi.importActual<typeof import('@wailsio/runtime')>('@wailsio/runtime'),
  Clipboard: { SetText: wailsClipboard.setText },
}));

vi.mock('../../api/config', async () => ({
  ...await vi.importActual<typeof import('../../api/config')>('../../api/config'),
  getConfig: api.getConfig,
  getAnnotationContainerApiKey: api.getAnnotationContainerApiKey,
  getProjects: api.getProjects,
}));

vi.mock('../../api/annotation', async () => {
  const actual = await vi.importActual<typeof import('../../api/annotation')>('../../api/annotation');
  return { ...actual, ...api };
});

function makeCase(overrides: Partial<AnnotationCase> = {}): AnnotationCase {
  return {
    taskId: 'task-1',
    projectId: 'project-1',
    taskName: '低分也保留的任务',
    sourcePath: '/source/repo-one',
    initialSha: 'abc123',
    snapshotUrl: '',
    containerId: 'container-1',
    containerName: 'claude-one',
    workspacePath: '/workspace',
    repoRelativePath: 'repo-one',
    sessionId: 'session-1',
    tracePath: '/trace/session-1.jsonl',
    completed: false,
    rounds: [
      {
        promptId: 'prompt-1',
        sessionId: 'session-1',
        prompt: '修复真实问题',
        order: 1,
        status: 'complete',
        reason: '',
        evidenceHash: 'evidence-1',
        sourceStart: 10,
        sourceEnd: 40,
        version: 'v1',
        cwd: '/workspace/repo-one',
        captureId: 'capture-1',
        evaluations: [
          {
            id: 'evaluation-1',
            createdAt: 1,
            skillHash: 'skill-1',
            model: 'reviewer',
            evidenceHash: 'evidence-1',
            status: 'complete',
            scores: [1, 2, null, 4, 5],
            descriptions: ['理解不足', '规划有遗漏', '验证证据缺失', '实现基本正确', '交付清楚'],
            taskType: 'bugfix',
            difficulty: 'medium',
            language: 'TypeScript',
            environment: 'docker',
            harnessVersion: '1',
            os: 'linux',
            evidence: ['轨迹 10-40', 'capture-1'],
            missing: ['独立验证输出'],
            issues: [{ description: '边界未覆盖', evidence: '轨迹 31', kind: 'verification' }],
            nextPrompt: '补充边界测试并记录结果',
            nextPromptType: 'verification',
          },
        ],
      },
    ],
    captures: [],
    revision: 1,
    updatedAt: 1,
    ...overrides,
  };
}

type PairwiseReviews = NonNullable<AnnotationCase['pairwise']>['reviews'];

function makePairwiseCase(taskId: string, taskName: string, reviews: PairwiseReviews): AnnotationCase {
  return makeCase({
    taskId, taskName, mode: 'pairwise_gsb',
    pairwise: {
      prompt: '实现加法', language: 'Go', harness: 'Codex CLI', harnessVersion: '1', os: 'MacOS/Linux', environment: '', validity: '有效', notes: '', autoRecordEnabled: false, reviews,
      runA: { side: 'A', branch: 'A', containerId: 'ca', containerName: 'ca', workspacePath: '/a', repoRelativePath: 'repo', sessionId: 'sa', tracePath: '/a.jsonl', turnCount: 1, captureId: 'a', captureHash: 'ha', traceHash: 'ta', deliverableSha: 'b'.repeat(40), deliverableUrl: `https://github.com/u/r/commit/${'b'.repeat(40)}`, videoStatus: 'missing', videoPath: '', videoUrl: '', recordingError: '', preparedAt: 1, capturedAt: 1, committedAt: 1 },
      runB: { side: 'B', branch: 'B', containerId: 'cb', containerName: 'cb', workspacePath: '/b', repoRelativePath: 'repo', sessionId: 'sb', tracePath: '/b.jsonl', turnCount: 1, captureId: 'b', captureHash: 'hb', traceHash: 'tb', deliverableSha: 'c'.repeat(40), deliverableUrl: `https://github.com/u/r/commit/${'c'.repeat(40)}`, videoStatus: 'missing', videoPath: '', videoUrl: '', recordingError: '', preparedAt: 1, capturedAt: 1, committedAt: 1 },
    },
  });
}

function makeReview(overrides: Partial<PairwiseReviews[number]> = {}): PairwiseReviews[number] {
  return {
    current: true, id: 'review-1', status: 'ready', conclusion: 'A_better', reason: 'A 完成了题目要求的关键改动，B 只改了样式。',
    model: 'Codex CLI', skillHash: 'skill', sourceHashA: 'a', sourceHashB: 'b', reviewPath: '/review', reviewHash: 'hash', createdAt: 1,
    ...overrides,
  };
}

describe('AnnotationWorkspace', () => {
  beforeEach(() => {
    Object.values(api).forEach((mock) => mock.mockReset());
    wailsClipboard.setText.mockReset().mockResolvedValue(undefined);
    api.getConfig.mockResolvedValue('xh04,cyc');
    api.getAnnotationContainerApiKey.mockResolvedValue('');
    api.getProjects.mockResolvedValue([
      { id: 'project-1', name: '项目一' },
      { id: 'project-2', name: '项目二' },
    ]);
    api.listCases.mockResolvedValue([makeCase()]);
    api.listContainers.mockResolvedValue([]);
    api.listTraces.mockResolvedValue([]);
  });

  it('orders configured container prefix groups and keeps unmatched names last', async () => {
    api.listContainers.mockResolvedValue([
      { id: 'cyc-4', name: 'cyc-claude-4', state: 'running', image: 'claude', workspacePath: '/cyc-4' },
      { id: 'zulu', name: 'zulu-runner', state: 'running', image: 'claude', workspacePath: '/zulu' },
      { id: 'xh04-2', name: 'xh04-claude-2', state: 'running', image: 'claude', workspacePath: '/xh04-2' },
      { id: 'cyc-12', name: 'cyc-claude-12', state: 'running', image: 'claude', workspacePath: '/cyc-12' },
      { id: 'alpha', name: 'alpha-runner', state: 'running', image: 'claude', workspacePath: '/alpha' },
      { id: 'xh04-10', name: 'xh04-claude-10', state: 'running', image: 'claude', workspacePath: '/xh04-10' },
    ]);

    render(<AnnotationWorkspace projectId="project-1" taskId="task-1" />);

    const select = await screen.findByRole('combobox', { name: '容器' });
    await waitFor(() => expect(Array.from(select.querySelectorAll('option')).map((option) => option.textContent)).toEqual([
      '选择实际容器',
      'xh04-claude-10 · running · /xh04-10',
      'xh04-claude-2 · running · /xh04-2',
      'cyc-claude-12 · running · /cyc-12',
      'cyc-claude-4 · running · /cyc-4',
      'alpha-runner · running · /alpha',
      'zulu-runner · running · /zulu',
    ]));
  });

  it('restores running state and disables duplicate reviews after reopening details', async () => {
    const c=makeCase({preparation:{jobId:'running-job',status:'running',progress:40,message:'第 1 轮：等待模型响应',error:'',startedAt:1,finishedAt:0,lastActivityAt:1}});
    c.rounds[0].evaluations=[];
    api.listCases.mockResolvedValue([c]);
    render(<AnnotationWorkspace projectId="project-1" taskId="task-1" view="review" />);
    expect(await screen.findByText('制表中 · 已复审 0/1 轮')).toBeInTheDocument();
    expect(screen.getByRole('button',{name:'审核本轮'})).toBeDisabled();
    expect(screen.getByText(/超过 2 分钟没有新的审核进展/)).toBeInTheDocument();
  });

  it('shows persisted failure and offers resume without requiring the container', async () => {
    const c=makeCase({containerId:'',preparation:{jobId:'failed-job',status:'error',progress:15,message:'第 1 轮',error:'审核执行超时',startedAt:1,finishedAt:100,lastActivityAt:50}});
    c.rounds[0].evaluations=[];
    api.listCases.mockResolvedValue([c]);
    render(<AnnotationWorkspace projectId="project-1" taskId="task-1" view="review" />);
    expect(await screen.findByText('制表失败 · 已复审 0/1 轮')).toBeInTheDocument();
    expect(screen.getByRole('button',{name:'继续准备未完成轮次'})).toBeEnabled();
    expect(screen.getByText(/审核执行超时；已保存的轨迹与评分保留/)).toBeInTheDocument();
  });

  it('explains completed functionality with process deductions without demanding repair', async () => {
    const c=makeCase();
    const e=c.rounds[0].evaluations![0];
    e.status='ready'; e.scores=[5,5,4,5,4]; e.issues=[{kind:'process',description:'重复读取',evidence:'原轨迹两次读取同一文件'}];e.nextPrompt='';e.missing=[];
    e.requirementChecks=[{requirement:'支持导出',status:'completed',evidence:'评价助手执行导出测试通过'}];
    api.listCases.mockResolvedValue([c]);
    render(<AnnotationWorkspace projectId="project-1" taskId="task-1" view="review" />);
    expect(await screen.findByText('已完成 · 支持导出')).toBeInTheDocument();
    expect(screen.getByText(/本轮审核未发现待修复的代码问题/)).toBeInTheDocument();
    expect(screen.queryByText('下一轮修复提示词（仅复制）')).not.toBeInTheDocument();
  });

  it('prepares table data directly from the capture panel without another round', async () => {
    api.captureAndPrepareTable.mockResolvedValue({ id: 'table-job', status: 'pending' });
    api.getAnnotationJob.mockResolvedValue({ id: 'table-job', status: 'done', outputPayload: JSON.stringify(makeCase()) });
    render(<AnnotationWorkspace projectId="project-1" taskId="task-1" />);
    const button = await screen.findByRole('button', { name: '采集并准备制表数据' });
    const tracePath = screen.getByLabelText('本机 JSONL 绝对路径');
    fireEvent.change(tracePath, { target: { value: '/tmp/current.jsonl' } });
    await waitFor(() => expect(tracePath).toHaveValue('/tmp/current.jsonl'));
    fireEvent.click(button);
    await waitFor(() => expect(api.captureAndPrepareTable).toHaveBeenCalledWith({ taskId: 'task-1', tracePath: '/tmp/current.jsonl' }));
    expect(await screen.findByText('采集并准备制表数据已完成')).toBeInTheDocument();
    expect(api.reviewRound).not.toHaveBeenCalled();
    expect(api.saveCaseSettings).not.toHaveBeenCalled();
  });

  it('keeps export actions out of the task detail container panel', async () => {
    render(<AnnotationWorkspace projectId="project-1" taskId="task-1" />);
    await screen.findByText('容器与轨迹');
    expect(screen.queryByRole('button', { name: '选择题目导出' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '导出本题 Excel' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '导出全项目已制表' })).not.toBeInTheDocument();
  });

  it('scopes the detail capture panel to the requested task without batch controls', async () => {
    api.listCases.mockResolvedValue([makeCase(), makeCase({ taskId: 'task-2', taskName: '当前详情题目', repoRelativePath: 'repo-two' })]);
    render(<AnnotationWorkspace projectId="project-1" taskId="task-2" />);
    expect(await screen.findByText('当前详情题目')).toBeInTheDocument();
    expect(screen.queryByText('低分也保留的任务')).not.toBeInTheDocument();
    expect(screen.queryByText('题目进度')).not.toBeInTheDocument();
    expect(screen.queryByText('批次预检与统一导出')).not.toBeInTheDocument();
    expect(screen.queryByRole('textbox', { name: '仓库相对路径' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '采集轨迹' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '采集并准备制表数据' })).toBeInTheDocument();
    expect(api.listTraces).toHaveBeenCalledWith('task-2');
    api.captureAndPrepareTable.mockResolvedValue({ id: 'detail-capture', status: 'pending' });
    api.getAnnotationJob.mockResolvedValue({ id: 'detail-capture', status: 'done', outputPayload: JSON.stringify(makeCase({ taskId: 'task-2', taskName: '当前详情题目' })) });
    fireEvent.change(screen.getByLabelText('本机 JSONL 绝对路径'), { target: { value: '/tmp/task-two.jsonl' } });
    fireEvent.click(screen.getByRole('button', { name: '采集并准备制表数据' }));
    await waitFor(() => expect(api.captureAndPrepareTable).toHaveBeenCalledWith({ taskId: 'task-2', tracePath: '/tmp/task-two.jsonl' }));
    expect(await screen.findByText('采集并准备制表数据已完成')).toBeInTheDocument();
  });

  it('refreshes trace candidates even when the saved case revision has not changed', async () => {
    render(<AnnotationWorkspace projectId="project-1" taskId="task-1" />);
    await waitFor(() => expect(api.listTraces).toHaveBeenCalledTimes(1));
    api.listTraces.mockResolvedValue([{ path: '/new.jsonl', sessionId: 'new-session', size: 10 }]);
    const refreshButton = screen.getByRole('button', { name: '刷新容器' });
    await waitFor(() => expect(refreshButton).toBeEnabled());
    fireEvent.click(refreshButton);
    expect(await screen.findByRole('option', { name: /new-session/ })).toBeInTheDocument();
  });

  it('does not fall back to another task when the detail task is missing', async () => {
    render(<AnnotationWorkspace projectId="project-1" taskId="missing-task" />);
    expect(await screen.findByText('当前题目暂无标注记录，请先确认题目已导入当前项目')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '采集轨迹' })).not.toBeInTheDocument();
    expect(api.listTraces).not.toHaveBeenCalled();
  });

  it('selects task numbers independently for batch GSB review and export', async () => {
    const pairwise = makeCase({
      initialSha: 'a'.repeat(40), snapshotUrl: `https://github.com/u/r/commit/${'a'.repeat(40)}`, mode: 'pairwise_gsb',
      pairwise: {
        prompt: '实现加法', language: 'Go', harness: 'Codex CLI', harnessVersion: '1', os: 'MacOS/Linux', environment: '', validity: '有效', notes: '', autoRecordEnabled: false,
        reviews: [{ current: true, id: 'review-1', status: 'ready', conclusion: 'A_better', reason: 'A 完成了题目要求的关键改动，B 只改了样式。', model: 'Codex CLI', skillHash: 'skill', sourceHashA: 'a', sourceHashB: 'b', reviewPath: '/review', reviewHash: 'hash', createdAt: 1 }],
        runA: { side: 'A', branch: 'A', containerId: 'ca', containerName: 'ca', workspacePath: '/a', repoRelativePath: 'repo', sessionId: 'sa', tracePath: '/a.jsonl', turnCount: 1, captureId: 'a', captureHash: 'ha', traceHash: 'ta', deliverableSha: 'b'.repeat(40), deliverableUrl: `https://github.com/u/r/commit/${'b'.repeat(40)}`, videoStatus: 'missing', videoPath: '', videoUrl: '', recordingError: '', preparedAt: 1, capturedAt: 1, committedAt: 1 },
        runB: { side: 'B', branch: 'B', containerId: 'cb', containerName: 'cb', workspacePath: '/b', repoRelativePath: 'repo', sessionId: 'sb', tracePath: '/b.jsonl', turnCount: 1, captureId: 'b', captureHash: 'hb', traceHash: 'tb', deliverableSha: 'c'.repeat(40), deliverableUrl: `https://github.com/u/r/commit/${'c'.repeat(40)}`, videoStatus: 'missing', videoPath: '', videoUrl: '', recordingError: '', preparedAt: 1, capturedAt: 1, committedAt: 1 },
      },
    });
    api.listCases.mockResolvedValue([pairwise]);
    api.batchReviewPairwise.mockResolvedValue({ id: 'pairwise-batch', status: 'pending' });
    api.exportPairwise.mockResolvedValue({ id: 'pairwise-export', status: 'pending' });
    api.getAnnotationJob.mockImplementation((id: string) => Promise.resolve(id === 'pairwise-export'
      ? { id, status: 'done', outputPayload: JSON.stringify({ outputPath: '/exports/gsb.xlsx', reportPath: '/exports/report.md', rows: 1, issues: [] }) }
      : { id, status: 'done', outputPayload: JSON.stringify({ total: 1, reviewed: 1, reused: 0, skipped: 0, failed: 0, items: [] }) }));
    const { rerender } = render(<AnnotationWorkspace projectId="project-1" />);
    expect(await screen.findByText(/Pair-wise GSB 批量审核与导出/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '批量采集并准备制表数据' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '一键导出已制表' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Pair-wise 预检' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /草稿|正式/ })).not.toBeInTheDocument();
    fireEvent.click(await screen.findByRole('button', { name: '批量审核 GSB' }));
    const task = screen.getByRole('checkbox', { name: /审核 低分也保留的任务/ });
    expect(task).not.toBeChecked();
    fireEvent.click(task);
    fireEvent.click(screen.getByRole('button', { name: '审核所选题目（1）' }));
    await waitFor(() => expect(api.batchReviewPairwise).toHaveBeenCalledWith({ projectId: 'project-1', taskIds: ['task-1'], force: false }));
    expect(await screen.findByText(/批量 GSB 完成：新审核 1 题/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '批量导出 GSB' }));
    expect(api.exportPairwise).not.toHaveBeenCalled();
    const exportTask = screen.getByRole('checkbox', { name: /导出 低分也保留的任务/ });
    expect(exportTask).not.toBeChecked();
    fireEvent.click(exportTask);
    fireEvent.click(screen.getByRole('button', { name: '导出所选题目（1）' }));
    await waitFor(() => expect(api.exportPairwise).toHaveBeenCalledWith({ projectId: 'project-1', taskIds: ['task-1'], submitter: '', submittedAt: '' }));

    rerender(<AnnotationWorkspace projectId="project-1" taskId="task-1" />);
    await screen.findByText('GSB 对比结果');
    expect(screen.queryByRole('button', { name: '生成 GSB' })).not.toBeInTheDocument();
  });

  it('lists only generated GSB tasks for export, blocks stale ones and supports select all', async () => {
    const ready = makePairwiseCase('task-1', '已完成 GSB 的任务', [makeReview()]);
    const stale = makePairwiseCase('task-2', '证据已变化的 GSB 任务', [makeReview({ id: 'review-2', current: false })]);
    const pending = makePairwiseCase('task-3', '尚未生成 GSB 的任务', []);
    api.listCases.mockResolvedValue([ready, stale, pending]);
    api.exportPairwise.mockResolvedValue({ id: 'pairwise-export', status: 'pending' });
    api.getAnnotationJob.mockResolvedValue({
      id: 'pairwise-export', status: 'done',
      outputPayload: JSON.stringify({ outputPath: '/exports/gsb.xlsx', reportPath: '/exports/report.md', rows: 1, issues: [] }),
    });
    render(<AnnotationWorkspace projectId="project-1" />);
    fireEvent.click(await screen.findByRole('button', { name: '批量导出 GSB' }));

    expect(screen.queryByRole('checkbox', { name: /导出 尚未生成 GSB 的任务/ })).not.toBeInTheDocument();
    const staleBox = screen.getByRole('checkbox', { name: /导出 证据已变化的 GSB 任务/ });
    expect(staleBox).toBeDisabled();
    expect(screen.getByText('A/B 证据已变化，需重新审核')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '导出所选题目（0）' })).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: '全选已生成 GSB 的 1 题' }));
    expect(screen.getByRole('checkbox', { name: /导出 已完成 GSB 的任务/ })).toBeChecked();
    fireEvent.click(screen.getByRole('button', { name: '导出所选题目（1）' }));
    await waitFor(() => expect(api.exportPairwise).toHaveBeenCalledWith({ projectId: 'project-1', taskIds: ['task-1'], submitter: '', submittedAt: '' }));

    fireEvent.click(await screen.findByRole('button', { name: '取消全选' }));
    expect(screen.getByRole('checkbox', { name: /导出 已完成 GSB 的任务/ })).not.toBeChecked();
  });

  it('says so when no task has a generated GSB yet', async () => {
    api.listCases.mockResolvedValue([makePairwiseCase('task-1', '尚未生成 GSB 的任务', [])]);
    render(<AnnotationWorkspace projectId="project-1" />);
    fireEvent.click(await screen.findByRole('button', { name: '批量导出 GSB' }));
    expect(screen.getByText('当前项目还没有已生成 GSB 的题目')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /全选/ })).not.toBeInTheDocument();
  });

  it('captures and commits both sides from the single A/B action', async () => {
    const pairwise = makePairwiseCase('task-1', '需采集两侧的任务', []);
    api.listCases.mockResolvedValue([pairwise]);
    api.capturePairwiseSide.mockResolvedValue({ id: 'job-capture', status: 'pending' });
    api.getAnnotationJob.mockResolvedValue({ id: 'job-capture', status: 'done', outputPayload: JSON.stringify(pairwise) });
    render(<AnnotationWorkspace projectId="project-1" taskId="task-1" />);

    fireEvent.click(await screen.findByRole('button', { name: '一键采集 A/B' }));
    await waitFor(() => expect(api.capturePairwiseSide).toHaveBeenCalledTimes(2));
    expect(api.capturePairwiseSide.mock.calls.map((call) => call[0].side)).toEqual(['A', 'B']);
    expect(await screen.findByText(/一键采集完成：A、B/)).toBeInTheDocument();
  });

  it('reports the side that failed instead of hiding a partial A/B capture', async () => {
    const pairwise = makePairwiseCase('task-1', '需采集两侧的任务', []);
    api.listCases.mockResolvedValue([pairwise]);
    api.capturePairwiseSide
      .mockResolvedValueOnce({ id: 'job-a', status: 'pending' })
      .mockResolvedValueOnce({ id: 'job-b', status: 'pending' });
    api.getAnnotationJob.mockImplementation((id: string) => Promise.resolve(id === 'job-a'
      ? { id, status: 'done', outputPayload: JSON.stringify(pairwise) }
      : { id, status: 'error', errorMessage: 'B 侧容器未就绪' }));
    render(<AnnotationWorkspace projectId="project-1" taskId="task-1" />);

    fireEvent.click(await screen.findByRole('button', { name: '一键采集 A/B' }));
    expect(await screen.findByText(/一键采集 A\/B 未全部完成：B 采集失败：B 侧容器未就绪（已完成 A）/)).toBeInTheDocument();
  });

  it('automatically enables GSB when entering an untouched task card', async () => {
    const legacy = makeCase({
      initialSha: 'a'.repeat(40), snapshotUrl: `https://github.com/u/r/commit/${'a'.repeat(40)}`,
      mode: 'legacy', rounds: [], captures: [], sessionId: '', tracePath: '',
    });
    const enabled = {
      ...legacy,
      revision: 2,
      mode: 'pairwise_gsb' as const,
      pairwise: {
        prompt: '修复真实问题', language: 'TypeScript', harness: 'Claude Code', harnessVersion: '2.1.197', os: 'MacOS/Linux', environment: '已容器化，可一键起环境', validity: '有效', notes: '', autoRecordEnabled: false, reviews: [],
        runA: { side: 'A' as const, branch: 'A', containerId: '', containerName: '', workspacePath: '', repoRelativePath: 'repo-one', sessionId: '', tracePath: '', turnCount: 0, captureId: '', captureHash: '', traceHash: '', deliverableSha: '', deliverableUrl: '', videoStatus: 'missing', videoPath: '', videoUrl: '', recordingError: '', preparedAt: 0, capturedAt: 0, committedAt: 0 },
        runB: { side: 'B' as const, branch: 'B', containerId: '', containerName: '', workspacePath: '', repoRelativePath: 'repo-one', sessionId: '', tracePath: '', turnCount: 0, captureId: '', captureHash: '', traceHash: '', deliverableSha: '', deliverableUrl: '', videoStatus: 'missing', videoPath: '', videoUrl: '', recordingError: '', preparedAt: 0, capturedAt: 0, committedAt: 0 },
      },
    };
    api.listCases.mockResolvedValue([legacy]);
    api.enablePairwise.mockResolvedValue({ id: 'enable-pairwise', status: 'pending' });
    api.getAnnotationJob.mockResolvedValue({ id: 'enable-pairwise', status: 'done', outputPayload: JSON.stringify(enabled) });

    render(<AnnotationWorkspace projectId="project-1" taskId="task-1" />);

    await waitFor(() => expect(api.enablePairwise).toHaveBeenCalledWith({ taskId: 'task-1' }));
    expect(await screen.findByText('GSB 对比结果')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '启用 Pair-wise GSB' })).not.toBeInTheDocument();
  });

  it('refreshes once, binds both exact containers, then copies one prompt', async () => {
    const unbound = makeCase({
      taskId: 'p1__feat__label-123-11', taskName: 'xh-05', sourcePath: '/tasks/xh-05-feature-11',
      repoRelativePath: 'xh-05-feature-11',
      initialSha: 'a'.repeat(40), snapshotUrl: `https://github.com/u/r/commit/${'a'.repeat(40)}`, mode: 'pairwise_gsb',
      pairwise: {
        prompt: '实现加法', language: 'TypeScript, React', harness: 'Claude Code', harnessVersion: '2.1.197', os: 'MacOS/Linux', environment: '', validity: '有效', notes: '', autoRecordEnabled: false, reviews: [],
        runA: { side: 'A', branch: 'A', containerId: '', containerName: '', workspacePath: '', repoRelativePath: 'xh-05-feature-11', sessionId: '', tracePath: '', turnCount: 0, captureId: '', captureHash: '', traceHash: '', deliverableSha: '', deliverableUrl: '', videoStatus: 'missing', videoPath: '', videoUrl: '', recordingError: '', preparedAt: 0, capturedAt: 0, committedAt: 0 },
        runB: { side: 'B', branch: 'B', containerId: '', containerName: '', workspacePath: '', repoRelativePath: 'xh-05-feature-11', sessionId: '', tracePath: '', turnCount: 0, captureId: '', captureHash: '', traceHash: '', deliverableSha: '', deliverableUrl: '', videoStatus: 'missing', videoPath: '', videoUrl: '', recordingError: '', preparedAt: 0, capturedAt: 0, committedAt: 0 },
      },
    });
    const boundA = { ...unbound, revision: 2, pairwise: { ...unbound.pairwise!, runA: { ...unbound.pairwise!.runA, containerId: 'container-a', containerName: 'xh05-claude-11-a', workspacePath: '/workspace-a' } } };
    const boundBoth = { ...boundA, revision: 3, pairwise: { ...boundA.pairwise!, runB: { ...boundA.pairwise!.runB, containerId: 'container-b', containerName: 'xh05-claude-11-b', workspacePath: '/workspace-b' } } };
    api.listCases.mockResolvedValue([unbound]);
    api.listContainers.mockResolvedValueOnce([]).mockResolvedValue([
      { id: 'container-a', name: 'xh05-claude-11-a', state: 'running', image: 'claude', workspacePath: '/workspace-a' },
      { id: 'container-b', name: 'xh05-claude-11-b', state: 'running', image: 'claude', workspacePath: '/workspace-b' },
    ]);
    api.bindPairwiseContainer.mockResolvedValueOnce({ id: 'bind-a', status: 'pending' }).mockResolvedValueOnce({ id: 'bind-b', status: 'pending' });
    api.getAnnotationJob.mockResolvedValueOnce({ id: 'bind-a', status: 'done', outputPayload: JSON.stringify(boundA) }).mockResolvedValueOnce({ id: 'bind-b', status: 'done', outputPayload: JSON.stringify(boundBoth) });
    render(<AnnotationWorkspace projectId="project-1" taskId={unbound.taskId} />);

    await screen.findByRole('region', { name: '运行 A' });
    fireEvent.click(screen.getByRole('button', { name: '刷新并绑定 A/B' }));

    await waitFor(() => expect(api.bindPairwiseContainer).toHaveBeenCalledWith({
      taskId: unbound.taskId, side: 'A', containerId: 'container-a', repoRelativePath: 'xh-05-feature-11', copyRepository: true,
    }));
    expect(api.bindPairwiseContainer).toHaveBeenCalledWith({
      taskId: unbound.taskId, side: 'B', containerId: 'container-b', repoRelativePath: 'xh-05-feature-11', copyRepository: true,
    });
    expect(await screen.findByText('已绑定 xh05-claude-11-a')).toBeInTheDocument();
    expect(await screen.findByText('已绑定 xh05-claude-11-b')).toBeInTheDocument();
    expect(wailsClipboard.setText).not.toHaveBeenCalledWith('实现加法');
    fireEvent.click(screen.getByRole('button', { name: '复制提示词' }));
    await screen.findByRole('button', { name: '提示词已复制' });
    expect(wailsClipboard.setText).toHaveBeenCalledWith('实现加法');
  });

  it('copies the startup command for the fixed task number in the detail panel', async () => {
    api.getAnnotationContainerApiKey.mockResolvedValue('saved-container-key');
    api.listCases.mockResolvedValue([makeCase({ taskId: 'p1__feat__label-123-9', taskName: 'cyc-03', sourcePath: '/tasks/cyc-03-feature迭代-9' })]);
    render(<AnnotationWorkspace projectId="project-1" taskId="p1__feat__label-123-9" />);
    const copy = await screen.findByRole('button', { name: '复制容器命令' });
    await waitFor(() => expect(copy).toBeEnabled());
    fireEvent.click(copy);
    await waitFor(() => expect(wailsClipboard.setText).toHaveBeenCalledWith(expect.stringContaining('CONTAINER_NAME="cyc03-claude-9"')));
    expect(wailsClipboard.setText).toHaveBeenCalledWith(expect.stringContaining('RUN_DIR="$BASE_DIR/run-9"'));
    expect(wailsClipboard.setText).toHaveBeenCalledWith(expect.stringContaining("apikey='saved-container-key'"));
    expect(await screen.findByText('容器启动命令已复制，请在本地终端执行')).toBeInTheDocument();
    const completedCopy = screen.getByRole('button', { name: '容器命令已复制' });
    expect(completedCopy).toBeEnabled();
    fireEvent.click(completedCopy);
    await waitFor(() => expect(wailsClipboard.setText).toHaveBeenCalledTimes(2));
    expect(screen.queryByText('查看命令')).not.toBeInTheDocument();
    expect(screen.queryByText(/新容器启动后/)).not.toBeInTheDocument();
  });

  it('shows only the compact container actions in the requested order', async () => {
    api.listCases.mockResolvedValue([makeCase({ snapshotUrl: 'https://github.com/example/repo/tree/abc123' })]);
    api.listContainers.mockResolvedValue([{ id: 'container-1', name: 'xh04-claude-1', state: 'running', image: 'claude', workspacePath: '/workspace' }]);
    render(
      <AnnotationWorkspace
        projectId="project-1"
        taskId="task-1"
        promptText="实现订单筛选功能"
        onPromptCopy={vi.fn()}
      />,
    );

    const heading = await screen.findByRole('heading', { name: '容器与轨迹' });
    const headingArea = heading.parentElement?.parentElement;
    expect(headingArea).not.toBeNull();
    expect(within(headingArea as HTMLElement).getByRole('button', { name: '复制容器命令' })).toBeInTheDocument();
    expect(within(headingArea as HTMLElement).queryByRole('button', { name: '刷新' })).not.toBeInTheDocument();

    const projectInfo = screen.getByRole('group', { name: '项目信息' });
    const projectPath = within(projectInfo).getByText('/source/repo-one');
    const snapshot = within(projectInfo).getByRole('link', { name: /GitHub 初始环境快照/ });
    expect(projectPath.compareDocumentPosition(snapshot) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    const actions = screen.getByRole('group', { name: '容器快捷操作' });
    expect(within(actions).getAllByRole('button').map((button) => button.textContent?.trim())).toEqual([
      '刷新容器',
      '复制并绑定',
      '复制提示词',
    ]);
    expect(within(actions).getByRole('combobox', { name: '容器' })).toBeInTheDocument();
    expect(screen.queryByText('快速开始')).not.toBeInTheDocument();
    expect(screen.queryByText(/第 [123] 步/)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '关联已有仓库' })).not.toBeInTheDocument();
    expect(screen.queryByRole('textbox', { name: '仓库相对路径' })).not.toBeInTheDocument();
  });

  it('matches the exact task container on refresh, binds it, then copies the prompt', async () => {
    const unbound = makeCase({
      taskId: 'p1__feat__label-123-11',
      taskName: 'xh-05',
      sourcePath: '/tasks/xh-05-bug修复-11',
      containerId: '',
      containerName: '',
      workspacePath: '',
      repoRelativePath: 'xh-05-bug修复-11',
    });
    const bound = makeCase({
      ...unbound,
      containerId: 'container-11',
      containerName: 'xh05-claude-11',
      workspacePath: '/workspace',
      revision: 2,
    });
    const onPromptCopy = vi.fn().mockResolvedValue(undefined);
    api.listCases.mockResolvedValue([unbound]);
    api.bindContainer.mockResolvedValue({ id: 'auto-bind', status: 'pending' });
    api.getAnnotationJob.mockResolvedValue({ id: 'auto-bind', status: 'done', outputPayload: JSON.stringify(bound) });
    render(
      <AnnotationWorkspace
        projectId="project-1"
        taskId={unbound.taskId}
        promptText="修复订单筛选问题"
        onPromptCopy={onPromptCopy}
      />,
    );
    await waitFor(() => expect(api.listContainers).toHaveBeenCalledTimes(1));
    api.listContainers.mockResolvedValue([
      { id: 'container-1', name: 'xh05-claude-1', state: 'running', image: 'claude', workspacePath: '/workspace-1' },
      { id: 'container-11', name: 'xh05-claude-11', state: 'running', image: 'claude', workspacePath: '/workspace-11' },
    ]);

    fireEvent.click(screen.getByRole('button', { name: '刷新容器' }));

    await waitFor(() => expect(api.bindContainer).toHaveBeenCalledWith({
      taskId: unbound.taskId,
      containerId: 'container-11',
      repoRelativePath: 'xh-05-bug修复-11',
      copyRepository: true,
    }));
    expect(await screen.findByRole('combobox', { name: '容器' })).toHaveValue('container-11');
    expect(await screen.findByRole('button', { name: '已绑定' })).toBeEnabled();
    expect(await screen.findByRole('button', { name: '提示词已复制' })).toBeEnabled();
    expect(onPromptCopy).toHaveBeenCalledTimes(1);
    expect(api.bindContainer.mock.invocationCallOrder[0]).toBeLessThan(onPromptCopy.mock.invocationCallOrder[0]);
  });

  it('does not fuzzy-match a different task number after refreshing containers', async () => {
    const current = makeCase({
      taskId: 'p1__feat__label-123-1',
      taskName: 'xh-05',
      sourcePath: '/tasks/xh-05-bug修复-1',
      containerId: '',
      containerName: '',
      workspacePath: '',
    });
    const onPromptCopy = vi.fn().mockResolvedValue(undefined);
    api.listCases.mockResolvedValue([current]);
    render(<AnnotationWorkspace projectId="project-1" taskId={current.taskId} promptText="提示词" onPromptCopy={onPromptCopy} />);
    await waitFor(() => expect(api.listContainers).toHaveBeenCalledTimes(1));
    api.listContainers.mockResolvedValue([
      { id: 'container-11', name: 'xh05-claude-11', state: 'running', image: 'claude', workspacePath: '/workspace-11' },
    ]);

    const refreshButton = screen.getByRole('button', { name: '刷新容器' });
    await waitFor(() => expect(refreshButton).toBeEnabled());
    fireEvent.click(refreshButton);

    await waitFor(() => expect(api.listContainers).toHaveBeenCalledTimes(2));
    expect(screen.getByRole('combobox', { name: '容器' })).toHaveValue('');
    expect(screen.getByRole('option', { name: /xh05-claude-11/ })).toBeInTheDocument();
    expect(api.bindContainer).not.toHaveBeenCalled();
    expect(onPromptCopy).not.toHaveBeenCalled();
  });

  it('copies the prompt without copying the repository again when the exact container is already bound', async () => {
    const bound = makeCase({
      taskId: 'p1__feat__label-123-11',
      taskName: 'xh-05',
      sourcePath: '/tasks/xh-05-bug修复-11',
      containerId: 'container-11',
      containerName: 'xh05-claude-11',
      workspacePath: '/workspace-11',
    });
    const onPromptCopy = vi.fn().mockResolvedValue(undefined);
    api.listCases.mockResolvedValue([bound]);
    api.listContainers.mockResolvedValue([
      { id: 'container-11', name: 'xh05-claude-11', state: 'running', image: 'claude', workspacePath: '/workspace-11' },
    ]);
    render(<AnnotationWorkspace projectId="project-1" taskId={bound.taskId} promptText="提示词" onPromptCopy={onPromptCopy} />);
    await waitFor(() => expect(api.listContainers).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByRole('button', { name: '刷新容器' }));

    expect(await screen.findByRole('button', { name: '已绑定' })).toBeEnabled();
    expect(await screen.findByRole('button', { name: '提示词已复制' })).toBeEnabled();
    expect(api.bindContainer).not.toHaveBeenCalled();
    expect(onPromptCopy).toHaveBeenCalledTimes(1);
  });

  it('keeps the successful binding state when automatic prompt copy fails', async () => {
    const unbound = makeCase({
      taskId: 'p1__feat__label-123-11',
      taskName: 'xh-05',
      sourcePath: '/tasks/xh-05-bug修复-11',
      containerId: '',
      containerName: '',
      workspacePath: '',
    });
    const bound = makeCase({ ...unbound, containerId: 'container-11', containerName: 'xh05-claude-11', workspacePath: '/workspace', revision: 2 });
    api.listCases.mockResolvedValue([unbound]);
    api.bindContainer.mockResolvedValue({ id: 'auto-bind', status: 'pending' });
    api.getAnnotationJob.mockResolvedValue({ id: 'auto-bind', status: 'done', outputPayload: JSON.stringify(bound) });
    const onPromptCopy = vi.fn().mockRejectedValue(new Error('剪贴板不可用'));
    render(<AnnotationWorkspace projectId="project-1" taskId={unbound.taskId} promptText="提示词" onPromptCopy={onPromptCopy} />);
    await waitFor(() => expect(api.listContainers).toHaveBeenCalledTimes(1));
    api.listContainers.mockResolvedValue([
      { id: 'container-11', name: 'xh05-claude-11', state: 'running', image: 'claude', workspacePath: '/workspace-11' },
    ]);

    fireEvent.click(screen.getByRole('button', { name: '刷新容器' }));

    expect(await screen.findByRole('button', { name: '已绑定' })).toBeEnabled();
    expect(await screen.findByRole('alert')).toHaveTextContent('绑定成功，但提示词复制失败：剪贴板不可用');
    expect(screen.getByRole('button', { name: '复制提示词' })).toBeEnabled();
  });

  it('keeps bind actions clickable after showing their completed state', async () => {
    const unbound = makeCase({ containerId: '', containerName: '', workspacePath: '', repoRelativePath: 'repo-one' });
    const bound = makeCase({ containerId: 'container-2', containerName: 'xh04-claude-2', workspacePath: '/workspace', repoRelativePath: 'repo-one', revision: 2 });
    api.listCases.mockResolvedValue([unbound]);
    api.listContainers.mockResolvedValue([{ id: 'container-2', name: 'xh04-claude-2', state: 'running', image: 'claude', workspacePath: '/workspace' }]);
    api.bindContainer.mockResolvedValue({ id: 'bind-job', status: 'pending' });
    api.getAnnotationJob.mockResolvedValue({ id: 'bind-job', status: 'done', outputPayload: JSON.stringify(bound) });

    render(<AnnotationWorkspace projectId="project-1" taskId="task-1" />);
    fireEvent.change(await screen.findByRole('combobox', { name: '容器' }), { target: { value: 'container-2' } });
    fireEvent.click(screen.getByRole('button', { name: '复制并绑定' }));

    const completedBind = await screen.findByRole('button', { name: '已绑定' });
    expect(completedBind).toBeEnabled();
    fireEvent.click(completedBind);
    await waitFor(() => expect(api.bindContainer).toHaveBeenCalledTimes(2));
  });

  it('enables prompt copying only after a container is selected and keeps the completed button clickable', async () => {
    const onPromptCopy = vi.fn().mockResolvedValue(undefined);
    api.listCases.mockResolvedValue([
      makeCase({ containerId: '', containerName: '', workspacePath: '' }),
    ]);
    api.listContainers.mockResolvedValue([
      { id: 'container-2', name: 'xh04-claude-2', state: 'running', image: 'claude', workspacePath: '/workspace' },
    ]);
    render(
      <AnnotationWorkspace
        projectId="project-1"
        taskId="task-1"
        promptText="实现订单筛选功能"
        onPromptCopy={onPromptCopy}
      />,
    );

    const copyPrompt = await screen.findByRole('button', { name: '复制提示词' });
    expect(copyPrompt).toBeDisabled();
    fireEvent.change(screen.getByRole('combobox', { name: '容器' }), {
      target: { value: 'container-2' },
    });
    expect(copyPrompt).toBeEnabled();
    fireEvent.click(copyPrompt);
    const completedCopy = await screen.findByRole('button', { name: '提示词已复制' });
    expect(completedCopy).toBeEnabled();
    fireEvent.click(completedCopy);
    await waitFor(() => expect(onPromptCopy).toHaveBeenCalledTimes(2));
  });

  it('resets quick action completion when the detail switches to another task', async () => {
    api.listCases.mockResolvedValue([
      makeCase({ taskId: 'p1__feat__label-123-9', taskName: 'cyc-03', sourcePath: '/tasks/cyc-03-feature迭代-9' }),
      makeCase({ taskId: 'p1__feat__label-123-10', taskName: 'cyc-03', sourcePath: '/tasks/cyc-03-feature迭代-10' }),
    ]);
    const { rerender } = render(<AnnotationWorkspace projectId="project-1" taskId="p1__feat__label-123-9" />);
    const copyCommand = await screen.findByRole('button', { name: '复制容器命令' });
    await waitFor(() => expect(copyCommand).toBeEnabled());
    fireEvent.click(copyCommand);
    expect(await screen.findByRole('button', { name: '容器命令已复制' })).toBeInTheDocument();

    rerender(<AnnotationWorkspace projectId="project-1" taskId="p1__feat__label-123-10" />);

    expect(await screen.findByRole('button', { name: '复制容器命令' })).toBeInTheDocument();
  });

  it('keeps a truthful perfect score but marks it as not collected', async () => {
    const item = makeCase();
    item.rounds[0].evaluations![0].scores = [5, 5, 5, 5, 5];
    api.listCases.mockResolvedValue([item]);
    render(<AnnotationWorkspace projectId="project-1" taskId="task-1" view="review" />);
    expect(await screen.findByText('五维总分 25 · 超过21，不收录')).toBeInTheDocument();
    expect(screen.queryByText('下一轮修复提示词（仅复制）')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '继续准备未完成轮次' })).not.toBeInTheDocument();
  });

  it('shows five-dimensional review in the detail review view and keeps batch export separate', async () => {
    render(<AnnotationWorkspace projectId="project-1" taskId="task-1" view="review" />);
    expect(await screen.findByText('真实轮次与评价')).toBeInTheDocument();
    expect(screen.getByText('1 分')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重新审核' })).toBeInTheDocument();
    expect(screen.queryByText('批次预检与统一导出')).not.toBeInTheDocument();
  });

  it('shows every case and all five score cells without filtering low or missing scores', async () => {
    render(<AnnotationWorkspace projectId="project-1" projectName="标注项目" />);

    expect((await screen.findAllByText('低分也保留的任务')).length).toBeGreaterThan(0);
    expect(screen.getByText('1 分')).toBeInTheDocument();
    expect(screen.getByText('2 分')).toBeInTheDocument();
    expect(screen.getByText('待补证据')).toBeInTheDocument();
    expect(screen.getByText('独立验证输出')).toBeInTheDocument();
    expect(screen.getByText('边界未覆盖')).toBeInTheDocument();
    expect(screen.getByText('补充边界测试并记录结果')).toBeInTheDocument();
    expect(screen.queryByText(/配额|quota/i)).not.toBeInTheDocument();
  });

  it('renders legacy rounds whose evaluations field is null', async () => {
    const legacy = makeCase({
      rounds: [{
        ...makeCase().rounds[0],
        evaluations: null,
      }],
    });
    api.listCases.mockResolvedValue([legacy]);

    render(<AnnotationWorkspace projectId="project-1" />);

    expect((await screen.findAllByText('低分也保留的任务')).length).toBeGreaterThan(0);
    expect(screen.getByText('审核 0/1')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '审核本轮' })).toBeEnabled();
  });

  it('runs prepare through the job queue and reloads the returned case', async () => {
    const unprepared = makeCase({ initialSha: '', rounds: [], containerId: '', containerName: '' });
    const prepared = { ...unprepared, initialSha: 'prepared-sha', revision: 2 };
    api.listCases.mockResolvedValueOnce([unprepared]).mockResolvedValueOnce([prepared]);
    api.prepareCase.mockResolvedValue({ id: 'job-prepare', status: 'pending' });
    api.getAnnotationJob.mockResolvedValue({
      id: 'job-prepare',
      status: 'done',
      outputPayload: JSON.stringify(prepared),
    });

    render(<AnnotationWorkspace projectId="project-1" />);
    fireEvent.click(await screen.findByRole('button', { name: '准备题目' }));

    await waitFor(() => expect(api.prepareCase).toHaveBeenCalledWith('task-1'));
    await waitFor(() => expect(screen.getByText('prepared-sha')).toBeInTheDocument());
    expect(screen.getByText('准备题目已完成')).toBeInTheDocument();
    expect(api.getAnnotationJob).toHaveBeenCalledWith('job-prepare');
  });

  it('ignores a stale case response after the selected project changes', async () => {
    let resolveOld: ((value: AnnotationCase[]) => void) | undefined;
    api.listCases.mockImplementation((projectId: string) => {
      if (projectId === 'project-old') {
        return new Promise<AnnotationCase[]>((resolve) => { resolveOld = resolve; });
      }
      return Promise.resolve([makeCase({ taskId: 'task-new', projectId, taskName: '新项目任务' })]);
    });

    const view = render(<AnnotationWorkspace projectId="project-old" />);
    view.rerender(<AnnotationWorkspace projectId="project-new" />);

    expect((await screen.findAllByText('新项目任务')).length).toBeGreaterThan(0);
    resolveOld?.([makeCase({ taskId: 'task-old', projectId: 'project-old', taskName: '旧项目任务' })]);
    await Promise.resolve();

    expect(screen.queryByText('旧项目任务')).not.toBeInTheDocument();
    expect(screen.getAllByText('新项目任务').length).toBeGreaterThan(0);
  });

  it('hides the legacy preflight and draft or formal export controls', async () => {
    render(<AnnotationWorkspace projectId="project-1" />);

    expect(await screen.findByRole('button', { name: '批量导出 GSB' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '批次预检' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '正式导出' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '草稿导出' })).not.toBeInTheDocument();
  });

  it('accepts a local absolute JSONL path when container discovery has no matching trace', async () => {
    api.listTraces.mockResolvedValue([
      { path: '/home/node/.claude/projects/a.jsonl', sessionId: 'a', size: 10 },
      { path: '/home/node/.claude/projects/b.jsonl', sessionId: 'b', size: 20 },
    ]);
    api.captureAndPrepareTable.mockResolvedValue({ id: 'job-capture', status: 'pending' });
    api.getAnnotationJob.mockResolvedValue({
      id: 'job-capture',
      status: 'done',
      outputPayload: JSON.stringify(makeCase({ tracePath: '/tmp/manual.jsonl' })),
    });

    render(<AnnotationWorkspace projectId="project-1" />);
    const localPath = await screen.findByLabelText('本机 JSONL 绝对路径');
    fireEvent.change(localPath, { target: { value: '/tmp/manual.jsonl' } });
    fireEvent.click(screen.getByRole('button', { name: '采集并准备制表数据' }));

    await waitFor(() => expect(api.captureAndPrepareTable).toHaveBeenCalledWith({
      taskId: 'task-1',
      tracePath: '/tmp/manual.jsonl',
    }));
  });

  it('keeps a running action scoped to its case', async () => {
    api.listCases.mockResolvedValue([
      makeCase({ taskId: 'task-1', taskName: '第一题', initialSha: '', containerId: '', rounds: [] }),
      makeCase({ taskId: 'task-2', taskName: '第二题', initialSha: '', containerId: '', rounds: [] }),
    ]);
    api.prepareCase.mockResolvedValue({ id: 'job-one', status: 'pending' });
    api.getAnnotationJob.mockReturnValue(new Promise(() => {}));

    render(<AnnotationWorkspace projectId="project-1" />);
    fireEvent.click(await screen.findByRole('button', { name: '准备题目' }));
    await waitFor(() => expect(api.prepareCase).toHaveBeenCalledWith('task-1'));
    fireEvent.click(screen.getByRole('button', { name: /第二题/ }));

    expect(screen.getByRole('button', { name: '准备题目' })).toBeEnabled();
  });

  it('does not apply an old settings response after switching projects', async () => {
    let resolveSave: ((value: AnnotationCase) => void) | undefined;
    api.listCases.mockImplementation((projectId: string) => Promise.resolve([
      makeCase({
        taskId: projectId === 'project-old' ? 'task-old' : 'task-new',
        projectId,
        taskName: projectId === 'project-old' ? '旧设置任务' : '新设置任务',
        snapshotUrl: projectId === 'project-old' ? '' : 'https://example.test/new',
      }),
    ]));
    api.saveCaseSettings.mockReturnValue(new Promise<AnnotationCase>((resolve) => { resolveSave = resolve; }));

    const view = render(<AnnotationWorkspace projectId="project-old" />);
    await screen.findAllByText('旧设置任务');
    fireEvent.change(screen.getByLabelText('初始快照 URL'), { target: { value: 'https://example.test/old' } });
    fireEvent.click(screen.getByRole('button', { name: '保存题目设置' }));
    view.rerender(<AnnotationWorkspace projectId="project-new" />);

    expect((await screen.findAllByText('新设置任务')).length).toBeGreaterThan(0);
    resolveSave?.(makeCase({ taskId: 'task-old', projectId: 'project-old', snapshotUrl: 'https://example.test/old' }));
    await waitFor(() => expect(screen.getByLabelText('初始快照 URL')).toHaveValue('https://example.test/new'));
    expect(screen.queryByText('题目设置已保存')).not.toBeInTheDocument();
  });
});
