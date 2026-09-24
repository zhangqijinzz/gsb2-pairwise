import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import type { AnnotationCase } from '../../api/annotation';
import { PairwiseWorkspace } from './PairwiseWorkspace';

const api = vi.hoisted(() => ({
  capturePairwiseSide: vi.fn(),
  commitPairwiseSide: vi.fn(),
  preparePairwiseSide: vi.fn(),
  reviewPairwise: vi.fn(),
  savePairwiseMaterials: vi.fn(),
  savePairwiseSettings: vi.fn(),
}));

vi.mock('../../api/annotation', async () => ({
  ...await vi.importActual<typeof import('../../api/annotation')>('../../api/annotation'),
  ...api,
}));

const pairwiseCase = {
  taskId: 'task-1', projectId: 'project-1', taskName: 'Pair test', taskType: 'Bug修复',
  sourcePath: '/repo', initialSha: 'a'.repeat(40), snapshotUrl: `https://github.com/u/r/commit/${'a'.repeat(40)}`,
  containerId: '', containerName: '', workspacePath: '', repoRelativePath: 'repo', sessionId: '', tracePath: '',
  completed: false, rounds: [], captures: [], revision: 1, updatedAt: 1, mode: 'pairwise_gsb',
  pairwise: {
    prompt: '修复筛选', language: 'TypeScript', harness: 'Codex CLI', harnessVersion: '1.0', os: 'MacOS/Linux', environment: '', notes: '', validity: '有效', autoRecordEnabled: false,
    runA: { side: 'A', branch: 'A', containerId: 'container-a', containerName: 'claude-a', workspacePath: '/workspace-a', repoRelativePath: 'repo', sessionId: 'session-a', tracePath: '/a.jsonl', turnCount: 1, captureId: 'ca', captureHash: 'ha', traceHash: 'ta', deliverableSha: 'b'.repeat(40), deliverableUrl: 'https://github.com/u/r/commit/a', videoStatus: 'ready', videoPath: '', videoUrl: 'https://example.com/a.mp4', recordingError: '', preparedAt: 1, capturedAt: 2, committedAt: 3 },
    runB: { side: 'B', branch: 'B', containerId: 'container-b', containerName: 'claude-b', workspacePath: '/workspace-b', repoRelativePath: 'repo', sessionId: '', tracePath: '', turnCount: 0, captureId: '', captureHash: '', traceHash: '', deliverableSha: '', deliverableUrl: '', videoStatus: 'missing', videoPath: '', videoUrl: '', recordingError: '', preparedAt: 0, capturedAt: 0, committedAt: 0 },
    reviews: [],
  },
} as AnnotationCase;

beforeEach(() => {
  vi.clearAllMocks();
  Object.values(api).forEach((fn) => fn.mockResolvedValue({ id: 'job-1', status: 'pending' }));
});

it('renders independent A and B evidence states', () => {
  render(<PairwiseWorkspace annotationCase={pairwiseCase} disabled={false} runJob={vi.fn()} />);
  const sideA = screen.getByRole('region', { name: '运行 A' });
  const sideB = screen.getByRole('region', { name: '运行 B' });
  expect(within(sideA).queryByRole('button', { name: '准备 A' })).not.toBeInTheDocument();
  expect(within(sideB).queryByRole('button', { name: '准备 B' })).not.toBeInTheDocument();
  expect(within(sideA).getByText('session-a')).toBeInTheDocument();
  expect(within(sideA).getByText('视频已就绪')).toBeInTheDocument();
  expect(within(sideA).getByRole('button', { name: '已提交 A' })).toBeEnabled();
  expect(within(sideB).getByText('尚未采集')).toBeInTheDocument();
  expect(within(sideB).getByRole('button', { name: '提交 B 产物' })).toBeDisabled();
});

it('offers one shared refresh and one shared prompt copy action', async () => {
  const onRefreshContainers = vi.fn().mockResolvedValue(undefined);
  const onBindContainer = vi.fn().mockResolvedValue(true);
  const onCopyPrompt = vi.fn().mockResolvedValue(undefined);
  render(<PairwiseWorkspace
    annotationCase={pairwiseCase}
    containers={[{ id: 'container-a', name: 'pair-claude-1-a', state: 'running', image: 'claude', workspacePath: '/workspace-a' }]}
    disabled={false}
    runJob={vi.fn()}
    onRefreshContainers={onRefreshContainers}
    onBindContainer={onBindContainer}
    onCopyPrompt={onCopyPrompt}
    promptCopied={false}
  />);
  const sideA = screen.getByRole('region', { name: '运行 A' });
  expect(within(sideA).queryByRole('button', { name: '刷新容器' })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '刷新并绑定 A/B' }));
  await waitFor(() => expect(onRefreshContainers).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole('button', { name: '复制提示词' }));
  await waitFor(() => expect(onCopyPrompt).toHaveBeenCalledTimes(1));
  expect(onBindContainer).not.toHaveBeenCalled();
});

it('lets a bound side clear its container, choose another one, and refresh only that side', async () => {
  const onRefreshContainer = vi.fn().mockResolvedValue(undefined);
  const onBindContainer = vi.fn().mockResolvedValue(true);
  render(<PairwiseWorkspace
    annotationCase={pairwiseCase}
    containers={[
      { id: 'container-a', name: 'pair-claude-1-a', state: 'running', image: 'claude', workspacePath: '/workspace-a' },
      { id: 'container-a2', name: 'pair-claude-2-a', state: 'running', image: 'claude', workspacePath: '/workspace-a2' },
    ]}
    disabled={false}
    runJob={vi.fn()}
    onBindContainer={onBindContainer}
    onRefreshContainer={onRefreshContainer}
  />);

  const sideA = screen.getByRole('region', { name: '运行 A' });
  expect(within(sideA).getByText('已绑定 claude-a')).toBeInTheDocument();
  fireEvent.click(within(sideA).getByRole('button', { name: '刷新容器 A' }));
  await waitFor(() => expect(onRefreshContainer).toHaveBeenCalledWith('A'));

  fireEvent.click(within(sideA).getByRole('button', { name: '清除当前容器 A' }));
  const select = within(sideA).getByLabelText('A 容器');
  expect(select).toHaveValue('');
  expect(within(sideA).getByRole('button', { name: '采集 A' })).toBeDisabled();
  fireEvent.change(select, { target: { value: 'container-a2' } });
  fireEvent.click(within(sideA).getByRole('button', { name: '绑定 A' }));
  await waitFor(() => expect(onBindContainer).toHaveBeenCalledWith('A', 'container-a2'));
});

it('asks the backend to remove the container and run folder when clearing a bound side', async () => {
  const onClearContainer = vi.fn().mockResolvedValue(undefined);
  render(<PairwiseWorkspace
    annotationCase={pairwiseCase}
    disabled={false}
    runJob={vi.fn()}
    onClearContainer={onClearContainer}
  />);

  const sideA = screen.getByRole('region', { name: '运行 A' });
  fireEvent.click(within(sideA).getByRole('button', { name: '清除当前容器 A' }));
  await waitFor(() => expect(onClearContainer).toHaveBeenCalledWith('A'));
});

it('keeps a side that was already cleared unbound after reload', () => {
  const clearedCase = {
    ...pairwiseCase,
    pairwise: {
      ...pairwiseCase.pairwise!,
      runA: { ...pairwiseCase.pairwise!.runA, containerCleared: true },
    },
  } as AnnotationCase;
  render(<PairwiseWorkspace annotationCase={clearedCase} disabled={false} runJob={vi.fn()} />);

  const sideA = screen.getByRole('region', { name: '运行 A' });
  expect(within(sideA).queryByRole('button', { name: '清除当前容器 A' })).not.toBeInTheDocument();
  expect(within(sideA).getByLabelText('A 容器')).toHaveValue('');
  expect(within(sideA).getByRole('button', { name: '采集 A' })).toBeDisabled();
});

it('marks A and B container commands as copied independently and keeps them clickable', async () => {
  const onCopyContainerCommand = vi.fn().mockResolvedValue(undefined);
  render(<PairwiseWorkspace
    annotationCase={pairwiseCase}
    disabled={false}
    runJob={vi.fn()}
    onCopyContainerCommand={onCopyContainerCommand}
  />);

  const copyA = screen.getByRole('button', { name: '复制 A 容器命令' });
  fireEvent.click(copyA);
  const copiedA = await screen.findByRole('button', { name: 'A 容器命令已复制' });
  expect(copiedA).toBeEnabled();
  expect(copiedA).toHaveClass('text-emerald-700');
  expect(screen.getByRole('button', { name: '复制 B 容器命令' })).toBeInTheDocument();

  fireEvent.click(copiedA);
  await waitFor(() => expect(onCopyContainerCommand).toHaveBeenCalledTimes(2));
  fireEvent.click(screen.getByRole('button', { name: '复制 B 容器命令' }));
  expect(await screen.findByRole('button', { name: 'B 容器命令已复制' })).toBeEnabled();
});

it('submits side-specific capture and video actions', async () => {
  const runJob = vi.fn(async (_label: string, submit: () => Promise<unknown>) => { await submit(); return true; });
  render(<PairwiseWorkspace annotationCase={pairwiseCase} disabled={false} runJob={runJob} />);
  const sideB = screen.getByRole('region', { name: '运行 B' });
  expect(within(sideB).queryByLabelText('B 轨迹路径')).not.toBeInTheDocument();
  fireEvent.click(within(sideB).getByRole('button', { name: '采集 B' }));
  await waitFor(() => expect(api.capturePairwiseSide).toHaveBeenCalledWith({ taskId: 'task-1', side: 'B' }));
  expect(runJob).toHaveBeenCalledTimes(1);

  fireEvent.change(within(sideB).getByLabelText('B 视频链接'), { target: { value: 'https://example.com/b.mp4' } });
  fireEvent.click(within(sideB).getByRole('button', { name: '保存 B 视频' }));
  await waitFor(() => expect(api.savePairwiseMaterials).toHaveBeenCalledWith({ taskId: 'task-1', side: 'B', videoUrl: 'https://example.com/b.mp4', videoPath: '', recordingError: '' }));
});

it('copies a manual startup command without presenting automatic runtime controls', async () => {
  const onStartProject = vi.fn().mockResolvedValue({ side: 'A', running: false, url: 'http://192.168.1.2:4821', command: "docker exec -it 'container-a' sh -lc 'pnpm dev'" });
  render(<PairwiseWorkspace annotationCase={pairwiseCase} disabled={false} runJob={vi.fn()} onStartProject={onStartProject} />);
  const sideA = screen.getByRole('region', { name: '运行 A' });
  fireEvent.click(within(sideA).getByRole('button', { name: '复制启动命令 A' }));
  await waitFor(() => expect(onStartProject).toHaveBeenCalledWith('A'));
  expect(within(sideA).getByText('启动命令已复制')).toBeInTheDocument();
  expect(within(sideA).getByRole('link', { name: '打开 A 项目' })).toHaveAttribute('href', 'http://192.168.1.2:4821');
  expect(within(sideA).queryByRole('button', { name: '停止项目 A' })).not.toBeInTheDocument();
  expect(within(sideA).queryByText('项目运行中')).not.toBeInTheDocument();
});

it('does not expose automatic recording buttons', () => {
  render(<PairwiseWorkspace annotationCase={pairwiseCase} disabled={false} runJob={vi.fn()} />);
  const sideA = screen.getByRole('region', { name: '运行 A' });
  const sideB = screen.getByRole('region', { name: '运行 B' });
  expect(within(sideA).queryByRole('button', { name: '录制 A 视频' })).not.toBeInTheDocument();
  expect(within(sideB).queryByRole('button', { name: '录制 B 视频' })).not.toBeInTheDocument();
});

it('does not expose recording guide generation or copied guide content', () => {
  const withGuide = structuredClone(pairwiseCase);
  withGuide.pairwise!.runA.recordingGuide = ['打开项目首页', '点击皱眉榜', '打开第一条记录查看详情'];
  render(<PairwiseWorkspace annotationCase={withGuide} disabled={false} runJob={vi.fn()} />);
  const sideA = screen.getByRole('region', { name: '运行 A' });
  expect(within(sideA).getByText('轨迹已采集')).toBeInTheDocument();
  expect(within(sideA).getByText('产物已提交')).toBeInTheDocument();
  expect(within(sideA).queryByText('30 秒操作链路')).not.toBeInTheDocument();
  expect(within(sideA).queryByText('点击皱眉榜')).not.toBeInTheDocument();
  expect(within(sideA).queryByRole('button', { name: /录制指引/ })).not.toBeInTheDocument();
  expect(within(sideA).queryByRole('button', { name: '复制指引' })).not.toBeInTheDocument();
});

it('does not make GSB review provisional when videos are missing', () => {
  render(<PairwiseWorkspace annotationCase={pairwiseCase} disabled={false} runJob={vi.fn()} />);
  expect(screen.queryByText(/临时状态/)).not.toBeInTheDocument();
});

it('saves one of the three environment reproducibility levels', async () => {
  const runJob = vi.fn(async (_label: string, submit: () => Promise<unknown>) => { await submit(); return true; });
  render(<PairwiseWorkspace annotationCase={pairwiseCase} disabled={false} runJob={runJob} />);
  expect(screen.queryByLabelText('语言/框架')).not.toBeInTheDocument();
  expect(screen.queryByLabelText('有效性')).not.toBeInTheDocument();
  expect(screen.queryByText('Harness')).not.toBeInTheDocument();
  const environment = screen.getByLabelText('环境可复现等级');
  expect(Array.from(environment.querySelectorAll('option')).map((option) => option.textContent)).toEqual([
    '已容器化，可一键起环境', '无外部依赖', '有外部依赖，未容器化',
  ]);
  fireEvent.change(environment, { target: { value: '无外部依赖' } });
  await waitFor(() => expect(api.savePairwiseSettings).toHaveBeenCalledWith(expect.objectContaining({ taskId: 'task-1', environment: '无外部依赖' })));
});

it('keeps review and export actions out of the task detail', () => {
  render(<PairwiseWorkspace annotationCase={pairwiseCase} disabled={false} runJob={vi.fn()} />);
  expect(screen.getByRole('button', { name: '单题 GSB 审核保存' })).toBeDisabled();
  expect(screen.queryByRole('button', { name: /导出/ })).not.toBeInTheDocument();
});

it('reviews and saves one GSB after both sides have been captured', async () => {
  const captured = structuredClone(pairwiseCase);
  captured.pairwise!.runB = {
    ...captured.pairwise!.runB,
    sessionId: 'session-b', tracePath: '/b.jsonl', turnCount: 1,
    captureId: 'cb', captureHash: 'hb', traceHash: 'tb',
    deliverableSha: 'c'.repeat(40), deliverableUrl: 'https://github.com/u/r/commit/b',
    capturedAt: 2, committedAt: 3,
  };
  const runJob = vi.fn(async (_label: string, submit: () => Promise<unknown>) => { await submit(); return true; });
  render(<PairwiseWorkspace annotationCase={captured} disabled={false} runJob={runJob} />);

  const review = screen.getByRole('button', { name: '单题 GSB 审核保存' });
  expect(review).toBeEnabled();
  fireEvent.click(review);

  await waitFor(() => expect(api.reviewPairwise).toHaveBeenCalledWith({ taskId: 'task-1', force: false }));
  expect(runJob).toHaveBeenCalledWith('单题 GSB 审核保存', expect.any(Function));
});

it('offers one shared A/B capture action and disables it without a bound container', async () => {
  const onCaptureBoth = vi.fn().mockResolvedValue(undefined);
  const { unmount } = render(<PairwiseWorkspace annotationCase={pairwiseCase} disabled={false} runJob={vi.fn()} onCaptureBoth={onCaptureBoth} />);
  fireEvent.click(screen.getByRole('button', { name: '一键采集 A/B' }));
  await waitFor(() => expect(onCaptureBoth).toHaveBeenCalledTimes(1));
  unmount();

  const unbound = structuredClone(pairwiseCase);
  unbound.pairwise!.runA.containerId = '';
  unbound.pairwise!.runB.containerId = '';
  render(<PairwiseWorkspace annotationCase={unbound} disabled={false} runJob={vi.fn()} onCaptureBoth={onCaptureBoth} />);
  expect(screen.getByRole('button', { name: '一键采集 A/B' })).toBeDisabled();
});

it('strips pasted quotes from the recording path and keeps the real apostrophe', async () => {
  const runJob = vi.fn(async (_label: string, submit: () => Promise<unknown>) => { await submit(); return true; });
  render(<PairwiseWorkspace annotationCase={pairwiseCase} disabled={false} runJob={runJob} />);
  const sideB = screen.getByRole('region', { name: '运行 B' });
  const input = within(sideB).getByLabelText('B 视频链接');
  fireEvent.change(input, { target: { value: `'/tmp/it's demo.mov'` } });
  fireEvent.click(within(sideB).getByRole('button', { name: '保存 B 视频' }));
  await waitFor(() => expect(api.savePairwiseMaterials).toHaveBeenCalledWith({ taskId: 'task-1', side: 'B', videoUrl: '', videoPath: "/tmp/it's demo.mov", recordingError: '' }));
  expect(input).toHaveValue("/tmp/it's demo.mov");
});

it('classifies a quoted HTTP link as a URL after unwrapping it', async () => {
  const runJob = vi.fn(async (_label: string, submit: () => Promise<unknown>) => { await submit(); return true; });
  render(<PairwiseWorkspace annotationCase={pairwiseCase} disabled={false} runJob={runJob} />);
  const sideB = screen.getByRole('region', { name: '运行 B' });
  fireEvent.change(within(sideB).getByLabelText('B 视频链接'), { target: { value: "'https://example.com/b.mp4'" } });
  fireEvent.click(within(sideB).getByRole('button', { name: '保存 B 视频' }));
  await waitFor(() => expect(api.savePairwiseMaterials).toHaveBeenCalledWith({ taskId: 'task-1', side: 'B', videoUrl: 'https://example.com/b.mp4', videoPath: '', recordingError: '' }));
});
