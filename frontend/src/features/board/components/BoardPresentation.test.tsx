import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Task } from '../../../store';
import { TaskCard } from './BoardPresentation';

function createTask(overrides: Partial<Task> = {}): Task {
  return {
    id: overrides.id ?? 'task-1',
    projectId: overrides.projectId ?? '1849',
    projectName: overrides.projectName ?? 'label-01849',
    status: overrides.status ?? 'PromptReady',
    taskType: overrides.taskType ?? 'Bug修复',
    sessionList: overrides.sessionList ?? [],
    promptDifficulty: overrides.promptDifficulty ?? '一般',
    promptGenerationStatus: overrides.promptGenerationStatus ?? 'done',
    promptGenerationError: overrides.promptGenerationError ?? null,
    createdAt: overrides.createdAt ?? 1,
    executionRounds: overrides.executionRounds ?? 1,
    aiReviewRounds: overrides.aiReviewRounds ?? 0,
    aiReviewStatus: overrides.aiReviewStatus ?? 'none',
    hasCollectedPairwiseGsb: overrides.hasCollectedPairwiseGsb ?? false,
    hasGeneratedGsb: overrides.hasGeneratedGsb ?? false,
    progress: overrides.progress ?? 0,
    totalModels: overrides.totalModels ?? 0,
    runningModels: overrides.runningModels ?? 0,
  };
}

describe('TaskCard', () => {
  it.each(['sm', 'four', 'md', 'lg'] as const)(
    'shows container quick actions without opening the %s card',
    (size) => {
      const onClick = vi.fn();
      const onCopyContainerCommand = vi.fn();
      const onBindContainerAndCopyPrompt = vi.fn();
      render(
        <TaskCard
          task={createTask()}
          size={size}
          onClick={onClick}
          onContextMenu={vi.fn()}
          onDelete={vi.fn()}
          onCopyContainerCommand={onCopyContainerCommand}
          onBindContainerAndCopyPrompt={onBindContainerAndCopyPrompt}
        />,
      );

      fireEvent.click(screen.getByRole('button', { name: '复制容器启动命令' }));
      fireEvent.click(screen.getByRole('button', { name: '刷新并绑定容器，同时复制提示词' }));

      expect(onCopyContainerCommand).toHaveBeenCalledTimes(1);
      expect(onBindContainerAndCopyPrompt).toHaveBeenCalledTimes(1);
      expect(onClick).not.toHaveBeenCalled();
    },
  );

  it('keeps successful container quick actions blue and clickable', () => {
    render(
      <TaskCard
        task={createTask()}
        size="md"
        onClick={vi.fn()}
        onContextMenu={vi.fn()}
        onDelete={vi.fn()}
        containerActionState={{
          commandCopied: true,
          bindPromptCompleted: true,
          busyAction: null,
          busyLabel: '',
        }}
        onCopyContainerCommand={vi.fn()}
        onBindContainerAndCopyPrompt={vi.fn()}
      />,
    );

    const commandButton = screen.getByRole('button', { name: '容器命令已复制，可再次复制' });
    const bindButton = screen.getByRole('button', { name: '容器已绑定且提示词已复制，可再次执行' });
    expect(commandButton).toBeEnabled();
    expect(bindButton).toBeEnabled();
    expect(commandButton).toHaveClass('text-blue-600');
    expect(bindButton).toHaveClass('text-blue-600');
  });

  it('shows AI review warning rounds on the task card', () => {
    render(
      <TaskCard
        task={createTask({ aiReviewRounds: 3, aiReviewStatus: 'warning' })}
        size="md"
        onClick={vi.fn()}
        onContextMenu={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    expect(screen.getByText('复审未过 · 第 3 轮')).toBeInTheDocument();
  });

  it('shows AI review pass rounds on the task card', () => {
    render(
      <TaskCard
        task={createTask({ aiReviewRounds: 2, aiReviewStatus: 'pass' })}
        size="md"
        onClick={vi.fn()}
        onContextMenu={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    expect(screen.getByText('复审通过 · 第 2 轮')).toBeInTheDocument();
  });

  it('shows the task sequence on the task card', () => {
    render(
      <TaskCard
        task={createTask({ id: 'p1780327557517__feat__label-8232846794780805-17' })}
        size="md"
        onClick={vi.fn()}
        onContextMenu={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    expect(screen.getByText('#17')).toBeInTheDocument();
  });

  it.each(['sm', 'four', 'md', 'lg'] as const)(
    'shows reviewed GSB badge on the %s task card',
    (size) => {
      render(
        <TaskCard
          task={createTask({ hasGeneratedGsb: true })}
          size={size}
          onClick={vi.fn()}
          onContextMenu={vi.fn()}
          onDelete={vi.fn()}
        />,
      );

      expect(screen.getByText('GSB已审核')).toBeInTheDocument();
    },
  );

  it('shows collected status before GSB review', () => {
    render(
      <TaskCard
        task={createTask({ hasCollectedPairwiseGsb: true })}
        size="md"
        onClick={vi.fn()}
        onContextMenu={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    expect(screen.getByText('已采集')).toBeInTheDocument();
    expect(screen.queryByText('GSB已审核')).not.toBeInTheDocument();
  });

  it('prioritizes reviewed status when a collected task has been reviewed', () => {
    render(
      <TaskCard
        task={createTask({ hasCollectedPairwiseGsb: true, hasGeneratedGsb: true })}
        size="md"
        onClick={vi.fn()}
        onContextMenu={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    expect(screen.getByText('GSB已审核')).toBeInTheDocument();
    expect(screen.queryByText('已采集')).not.toBeInTheDocument();
  });
});
