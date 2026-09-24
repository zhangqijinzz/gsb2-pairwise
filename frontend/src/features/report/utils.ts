import type { TaskFromDB, AiReviewRoundFromDB, ModelRunFromDB, TaskSession } from '../../api/task';
import type { ReportRow } from './types';
import { extractTaskClaimSequence, isLocalSyntheticProjectId } from '../../shared/lib/taskId';

function getLatestAiRound(rounds: AiReviewRoundFromDB[]): AiReviewRoundFromDB | null {
  if (rounds.length === 0) return null;
  return rounds.reduce((latest, r) =>
    r.roundNumber > latest.roundNumber ? r : latest,
  );
}

function getAiRoundForSession(
  rounds: AiReviewRoundFromDB[],
  modelRunId: string | null,
  sessionIndex: number,
): AiReviewRoundFromDB | null {
  if (!modelRunId) return null;
  const roundNumber = sessionIndex + 1;
  return (
    rounds.find((round) => round.modelRunId === modelRunId && round.roundNumber === roundNumber) ??
    null
  );
}

function resolveSessionDecision(
  session: TaskSession,
  round: AiReviewRoundFromDB | null,
  field: 'isCompleted' | 'isSatisfied',
): boolean | null {
  if (round && round[field] !== null && round[field] !== undefined) {
    return round[field];
  }
  return session[field] ?? null;
}

function resolveDissatisfactionReason(session: TaskSession, round: AiReviewRoundFromDB | null): string {
  const roundSatisfied = round?.isSatisfied;
  if (round && roundSatisfied === false) {
    return round.dissatisfactionSummary?.trim() || round.reviewNotes?.trim() || session.evaluation || '';
  }
  return session.evaluation ?? '';
}

export function resolveReportRepoId(
  task: Pick<TaskFromDB, 'id' | 'gitlabProjectId' | 'projectName'>,
): string {
  const projectName = task.projectName.trim();
  if (isLocalSyntheticProjectId(task.gitlabProjectId) && projectName) {
    const sequence = extractTaskClaimSequence(task.id);
    return sequence ? `${projectName}-${sequence}` : projectName;
  }
  return String(task.gitlabProjectId);
}

/**
 * Collect all available session data from model runs (excluding ORIGIN/source).
 *
 * Session ID sources (priority order):
 * 1. TaskSession.sessionId inside ModelRun.sessionList (extracted from Trae logs)
 * 2. ModelRun.sessionId (model-run-level, from UpdateModelRunSessionInfo)
 * 3. TaskSession.sessionId inside Task.sessionList (task-level fallback)
 */
type ReportSession = {
  session: TaskSession;
  modelRunId: string | null;
  sessionIndex: number;
};

function collectExecutionData(modelRuns: ModelRunFromDB[]): {
  sessions: ReportSession[];
  modelRunSessionId: string;
} {
  const sessions: ReportSession[] = [];
  let modelRunSessionId = '';

  for (const run of modelRuns) {
    if (run.modelName.trim().toUpperCase() === 'ORIGIN') continue;

    if (!modelRunSessionId && run.sessionId && run.sessionId.trim()) {
      modelRunSessionId = run.sessionId.trim();
    }

    const runSessions = run.sessionList ?? [];
    for (let index = 0; index < runSessions.length; index += 1) {
      const s = runSessions[index];
      if (s.consumeQuota) {
        sessions.push({ session: s, modelRunId: run.id, sessionIndex: index });
      }
    }
  }

  // If we got sessions but still no modelRunSessionId, try from the first session
  if (!modelRunSessionId && sessions.length > 0 && sessions[0].session.sessionId) {
    modelRunSessionId = sessions[0].session.sessionId;
  }

  return { sessions, modelRunSessionId };
}

function formatBoolean(value: boolean | null): string {
  if (value === true) return '是';
  if (value === false) return '否';
  return '未填写';
}

function formatText(value: string | null | undefined): string {
  const text = value?.trim();
  return text ? text : '未填写';
}

function formatSessionValue(
  rows: ReportRow[],
  getValue: (row: ReportRow) => string,
): string {
  const sessionRows = rows.filter((row) => row.sessionIndex >= 0);
  if (sessionRows.length === 0) return '未填写';
  if (sessionRows.length === 1) return getValue(sessionRows[0]);

  return sessionRows
    .map((row, index) => `第${index + 1}次：${getValue(row)}`)
    .join('；');
}

export function buildReportMarkdown(rows: ReportRow[]): string {
  if (rows.length === 0) {
    return '# 报表 Markdown 清单\n\n暂无数据';
  }

  const repoMap = new Map<string, ReportRow[]>();
  for (const row of rows) {
    const repoRows = repoMap.get(row.repoId);
    if (repoRows) {
      repoRows.push(row);
    } else {
      repoMap.set(row.repoId, [row]);
    }
  }

  const repoIds = [...repoMap.keys()].sort((a, b) =>
    a.localeCompare(b, 'zh-CN', { numeric: true, sensitivity: 'base' }),
  );

  const lines: string[] = ['# 报表 Markdown 清单', ''];

  for (const repoId of repoIds) {
    lines.push(`## 项目编号：${repoId}`, '');

    const taskMap = new Map<string, ReportRow[]>();
    for (const row of repoMap.get(repoId) ?? []) {
      const taskRows = taskMap.get(row.taskId);
      if (taskRows) {
        taskRows.push(row);
      } else {
        taskMap.set(row.taskId, [row]);
      }
    }

    let taskIndex = 1;
    for (const taskRows of taskMap.values()) {
      const baseRow = taskRows[0];
      lines.push(`### 任务 ${taskIndex}`, `- 任务类型：${formatText(baseRow.taskType)}`);
      lines.push(`- Prompt：${formatText(baseRow.promptText)}`);
      lines.push(`- 业务领域：${formatText(baseRow.projectType || baseRow.aiProjectType)}`);
      lines.push(`- 修改范围：${formatText(baseRow.changeScope || baseRow.aiChangeScope)}`);
      lines.push(`- 是否完成：${formatSessionValue(taskRows, (row) => formatBoolean(row.isCompleted))}`);
      lines.push(`- 是否满意：${formatSessionValue(taskRows, (row) => formatBoolean(row.isSatisfied))}`);
      lines.push(
        `- 不满意原因/点评：${formatSessionValue(taskRows, (row) => formatText(row.dissatisfactionReason))}`,
        '',
      );
      taskIndex += 1;
    }
  }

  return lines.join('\n').trim();
}

export function assembleReportRows(
  tasks: TaskFromDB[],
  modelRunsByTask: Map<string, ModelRunFromDB[]>,
  aiRoundsByTask: Map<string, AiReviewRoundFromDB[]>,
): ReportRow[] {
  const rows: ReportRow[] = [];

  for (const task of tasks) {
    const rounds = aiRoundsByTask.get(task.id) ?? [];
    const latestRound = getLatestAiRound(rounds);
    const aiProjectType = latestRound?.projectType ?? '';
    const aiChangeScope = latestRound?.changeScope ?? '';
    const repoId = resolveReportRepoId(task);

    const modelRuns = modelRunsByTask.get(task.id) ?? [];
    const { sessions: mrSessions, modelRunSessionId } = collectExecutionData(modelRuns);

    // Prefer model run sessions (consumeQuota=true); fall back to task.sessionList
    const sessions: ReportSession[] = mrSessions.length > 0
      ? mrSessions
      : (task.sessionList ?? [])
          .map((session, index) => ({ session, modelRunId: null, sessionIndex: index }))
          .filter((item) => item.session.consumeQuota);

    // Resolve the best available sessionId for this task:
    // ModelRun.sessionId > first session's sessionId > task.sessionList[0].sessionId
    const fallbackSessionId =
      modelRunSessionId ||
      (sessions.length > 0 ? sessions[0].session.sessionId : '') ||
      ((task.sessionList ?? []).length > 0 ? (task.sessionList ?? [])[0].sessionId : '');

    if (sessions.length === 0) {
      rows.push({
        taskId: task.id,
        repoId,
        sessionId: fallbackSessionId,
        sessionIndex: -1,
        promptText: task.promptText,
        taskType: task.taskType,
        projectType: task.projectType || aiProjectType,
        changeScope: task.changeScope || aiChangeScope,
        isCompleted: null,
        isSatisfied: null,
        dissatisfactionReason: '',
        aiProjectType,
        aiChangeScope,
      });
    } else {
      for (let i = 0; i < sessions.length; i++) {
        const item = sessions[i];
        const session = item.session;
        const round = getAiRoundForSession(rounds, item.modelRunId, item.sessionIndex);
        rows.push({
          taskId: task.id,
          repoId,
          sessionId: session.sessionId || fallbackSessionId,
          sessionIndex: i,
          promptText: task.promptText,
          taskType: task.taskType,
          projectType: task.projectType || aiProjectType,
          changeScope: task.changeScope || aiChangeScope,
          isCompleted: resolveSessionDecision(session, round, 'isCompleted'),
          isSatisfied: resolveSessionDecision(session, round, 'isSatisfied'),
          dissatisfactionReason: resolveDissatisfactionReason(session, round),
          aiProjectType,
          aiChangeScope,
        });
      }
    }
  }

  rows.sort((a, b) =>
    a.repoId.localeCompare(b.repoId, 'zh-CN', { numeric: true, sensitivity: 'base' }),
  );

  return rows;
}
