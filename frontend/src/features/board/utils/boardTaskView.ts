import type { ReviewStatus } from '../../../api/task';
import type { Task, TaskStatus, TaskType } from '../../../store';
import { extractTaskClaimSequence } from '../../../shared/lib/taskId';
import { normalizeTaskTypeName } from '../../../shared/lib/taskTypes';

export type BoardSortOption =
  | 'project-desc'
  | 'created-desc'
  | 'created-asc'
  | 'round-desc'
  | 'round-asc';

export function getAvailableExecutionRounds(tasks: Task[]) {
  return Array.from(new Set(tasks.map((task) => task.executionRounds))).sort((left, right) => left - right);
}

export function filterBoardTasks(
  tasks: Task[],
  {
    search,
    activeTypes,
    activeStages,
    activeRounds,
    activeReviewStatuses,
  }: {
    search: string;
    activeTypes: Set<TaskType>;
    activeStages: Set<TaskStatus>;
    activeRounds: Set<number>;
    activeReviewStatuses?: Set<ReviewStatus>;
  },
) {
  const normalizedSearch = search.trim().toLowerCase();

  return tasks.filter((task) => {
    const matchSearch =
      !normalizedSearch ||
      task.projectName.toLowerCase().includes(normalizedSearch) ||
      task.projectId.includes(search) ||
      task.id.toLowerCase().includes(normalizedSearch);
    const matchType = activeTypes.size === 0 || activeTypes.has(normalizeTaskTypeName(task.taskType));
    const matchStage = activeStages.size === 0 || activeStages.has(task.status);
    const matchRound = activeRounds.size === 0 || activeRounds.has(task.executionRounds);
    const matchReviewStatus =
      !activeReviewStatuses ||
      activeReviewStatuses.size === 0 ||
      activeReviewStatuses.has(task.aiReviewStatus);

    return matchSearch && matchType && matchStage && matchRound && matchReviewStatus;
  });
}

export function sortBoardTasks(tasks: Task[], sortBy: BoardSortOption) {
  const next = [...tasks];
  const compareByName = (left: Task, right: Task) =>
    left.projectName.localeCompare(right.projectName, 'zh-CN', { numeric: true, sensitivity: 'base' });
  const compareByNameDesc = (left: Task, right: Task) => compareByName(right, left);
  const compareByClaimSequence = (left: Task, right: Task) =>
    (extractTaskClaimSequence(left.id) ?? Number.MAX_SAFE_INTEGER) -
    (extractTaskClaimSequence(right.id) ?? Number.MAX_SAFE_INTEGER);

  next.sort((left, right) => {
    if (sortBy === 'project-desc') {
      return compareByNameDesc(left, right) || compareByClaimSequence(left, right) || right.createdAt - left.createdAt;
    }
    if (sortBy === 'created-asc') {
      return left.createdAt - right.createdAt || right.executionRounds - left.executionRounds || compareByName(left, right);
    }
    if (sortBy === 'round-desc') {
      return right.executionRounds - left.executionRounds || right.createdAt - left.createdAt || compareByName(left, right);
    }
    if (sortBy === 'round-asc') {
      return left.executionRounds - right.executionRounds || right.createdAt - left.createdAt || compareByName(left, right);
    }
    return right.createdAt - left.createdAt || right.executionRounds - left.executionRounds || compareByName(left, right);
  });

  return next;
}

export type BoardTaskGroup = {
  groupKey: string;
  groupLabel: string;
  tasks: Task[];
  labelGroups: Array<{
    groupKey: string;
    tasks: Task[];
  }>;
};

function groupTasksByProjectLabel(tasks: Task[]) {
  const labelCounts = new Map<string, number>();
  const labelTasks = new Map<string, Task[]>();

  for (const task of tasks) {
    const groupKey = task.projectName.trim() || task.projectId || task.id;
    labelCounts.set(groupKey, (labelCounts.get(groupKey) ?? 0) + 1);
    labelTasks.set(groupKey, [...(labelTasks.get(groupKey) ?? []), task]);
  }

  const groups: Array<{ groupKey: string; tasks: Task[] }> = [];
  const handledLabels = new Set<string>();
  let looseTasks: Task[] = [];
  let looseGroupIndex = 0;
  const flushLooseTasks = () => {
    if (looseTasks.length === 0) return;
    groups.push({ groupKey: `__loose_${looseGroupIndex}`, tasks: looseTasks });
    looseGroupIndex += 1;
    looseTasks = [];
  };

  for (const task of tasks) {
    const groupKey = task.projectName.trim() || task.projectId || task.id;
    const shouldGroupByLabel = (labelCounts.get(groupKey) ?? 0) > 1;
    if (!shouldGroupByLabel) {
      looseTasks.push(task);
      continue;
    }
    flushLooseTasks();
    if (handledLabels.has(groupKey)) continue;
    groups.push({ groupKey, tasks: labelTasks.get(groupKey) ?? [task] });
    handledLabels.add(groupKey);
  }

  flushLooseTasks();
  return groups;
}

export function groupBoardTasks(availableTaskTypes: string[], tasks: Task[]): BoardTaskGroup[] {
  const grouped = new Map<string, Task[]>();

  for (const taskType of availableTaskTypes) {
    grouped.set(taskType, []);
  }

  for (const task of tasks) {
    const normalizedTaskType = normalizeTaskTypeName(task.taskType) || task.taskType;
    const existingTasks = grouped.get(normalizedTaskType);
    if (existingTasks) {
      existingTasks.push(task);
      continue;
    }
    grouped.set(normalizedTaskType, [task]);
  }

  return Array.from(grouped.entries())
    .map(([taskType, groupedTasks]) => ({
      groupKey: taskType,
      groupLabel: taskType,
      tasks: groupedTasks,
      labelGroups: groupTasksByProjectLabel(groupedTasks),
    }))
    .filter((group) => group.tasks.length > 0);
}
