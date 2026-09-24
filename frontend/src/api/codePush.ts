import { callService } from './wails';

export type CodePushStatus = 'committed' | 'pushed' | 'needs_push' | string;

export interface CodePushRecord {
  id: string;
  taskId: string;
  modelRunId: string;
  sessionId: string;
  sessionIndex: number;
  localPath: string;
  repoName: string;
  repoUrl: string;
  commitSha: string;
  commitUrl: string;
  branch: string;
  status: CodePushStatus;
  errorMessage: string;
  pushedAt: number | null;
  createdAt: number;
  updatedAt: number;
}

export interface CommitCodeRequest {
  taskId: string;
  modelRunId: string;
  sessionId: string;
  sessionIndex: number;
}

export interface RedoCommitRequest {
  recordId: string;
}

export interface PushCodeRequest {
  recordId: string;
  githubAccountId?: string;
  forceWithLease?: boolean;
}

export async function listCodePushRecords(taskId: string): Promise<CodePushRecord[]> {
  return callService('CodePushService', 'ListCodePushRecords', taskId);
}

export async function commitCode(request: CommitCodeRequest): Promise<CodePushRecord> {
  return callService('CodePushService', 'CommitCode', request);
}

export async function redoCommit(request: RedoCommitRequest): Promise<CodePushRecord> {
  return callService('CodePushService', 'RedoCommit', request);
}

export async function pushCode(request: PushCodeRequest): Promise<CodePushRecord> {
  return callService('CodePushService', 'PushCode', request);
}
