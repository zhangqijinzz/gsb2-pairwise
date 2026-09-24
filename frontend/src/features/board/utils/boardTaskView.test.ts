import { describe, expect, it } from 'vitest';
import type { Task } from '../../../store';
import {
  filterBoardTasks,
  getAvailableExecutionRounds,
  groupBoardTasks,
  sortBoardTasks,
} from './boardTaskView';

function createTask(overrides: Partial<Task>): Task {
  return {
    id: 'task-1',
    projectId: '1001',
    projectName: 'Alpha',
    status: 'Claimed',
    taskType: 'Bug修复',
    sessionList: [],
    promptDifficulty: overrides.promptDifficulty ?? '一般',
    promptGenerationStatus: 'idle',
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
    ...overrides,
  };
}

describe('boardTaskView helpers', () => {
  it('filters tasks by search, type, stage, and round', () => {
    const tasks = [
      createTask({ id: 'task-a', projectId: '1001', projectName: 'Alpha', taskType: 'bugfix' }),
      createTask({ id: 'task-b', projectId: '1002', projectName: 'Beta', status: 'Submitted', executionRounds: 2 }),
    ];

    expect(
      filterBoardTasks(tasks, {
        search: '1001',
        activeTypes: new Set(['Bug修复']),
        activeStages: new Set(['Claimed']),
        activeRounds: new Set([1]),
      }).map((task) => task.id),
    ).toEqual(['task-a']);
  });

  it('filters tasks by AI review status', () => {
    const tasks = [
      createTask({ id: 'task-a', aiReviewStatus: 'none' }),
      createTask({ id: 'task-b', aiReviewStatus: 'warning' }),
      createTask({ id: 'task-c', aiReviewStatus: 'pass' }),
    ];

    expect(
      filterBoardTasks(tasks, {
        search: '',
        activeTypes: new Set(),
        activeStages: new Set(),
        activeRounds: new Set(),
        activeReviewStatuses: new Set(['warning']),
      }).map((task) => task.id),
    ).toEqual(['task-b']);
  });

  it('groups repeated project labels inside each task type and keeps single labels in the normal flow', () => {
    const tasks = [
      createTask({ id: 'task-b', projectId: '1002', projectName: 'Beta 2', createdAt: 2, executionRounds: 2, taskType: 'Feature迭代' }),
      createTask({ id: 'task-a', projectId: '1001', projectName: 'Alpha 10', createdAt: 3, executionRounds: 1, taskType: 'bugfix' }),
      createTask({ id: 'task-c', projectId: '1001', projectName: 'Alpha 10', createdAt: 4, executionRounds: 1, taskType: 'Bug修复' }),
      createTask({ id: 'task-d', projectId: '1003', projectName: 'Alpha 2', createdAt: 5, executionRounds: 1, taskType: 'Bug修复' }),
      createTask({ id: 'task-e', projectId: '1004', projectName: 'Alpha 3', createdAt: 6, executionRounds: 1, taskType: 'Bug修复' }),
    ];

    const sorted = sortBoardTasks(tasks, 'created-asc');
    expect(sorted.map((task) => task.id)).toEqual(['task-b', 'task-a', 'task-c', 'task-d', 'task-e']);

    expect(groupBoardTasks(['Bug修复', 'Feature迭代'], sorted)).toEqual([
      {
        groupKey: 'Bug修复',
        groupLabel: 'Bug修复',
        tasks: [sorted[1], sorted[2], sorted[3], sorted[4]],
        labelGroups: [
          { groupKey: 'Alpha 10', tasks: [sorted[1], sorted[2]] },
          { groupKey: '__loose_0', tasks: [sorted[3], sorted[4]] },
        ],
      },
      {
        groupKey: 'Feature迭代',
        groupLabel: 'Feature迭代',
        tasks: [sorted[0]],
        labelGroups: [{ groupKey: '__loose_0', tasks: [sorted[0]] }],
      },
    ]);
    expect(getAvailableExecutionRounds(tasks)).toEqual([1, 2]);
  });

  it('sorts by project name descending and claim sequence ascending', () => {
    const tasks = [
      createTask({ id: 'project__code__label-02459-3', projectName: 'label-02459', createdAt: 4 }),
      createTask({ id: 'project__code__label-02491-2', projectName: 'label-02491', createdAt: 3 }),
      createTask({ id: 'project__code__label-02451-1', projectName: 'label-02451', createdAt: 2 }),
      createTask({ id: 'project__code__label-02459-1', projectName: 'label-02459', createdAt: 1 }),
      createTask({ id: 'project__code__label-02459-2', projectName: 'label-02459', createdAt: 5 }),
    ];

    expect(sortBoardTasks(tasks, 'project-desc').map((task) => task.id)).toEqual([
      'project__code__label-02491-2',
      'project__code__label-02459-1',
      'project__code__label-02459-2',
      'project__code__label-02459-3',
      'project__code__label-02451-1',
    ]);
  });
});
