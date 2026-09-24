import { cancelJob, getJob, submitJob, type BackgroundJob } from './job';
import { callService } from './wails';

export type AnnotationRoundStatus = 'complete' | 'pending' | 'conflict' | 'excluded';

export interface AnnotationIssue {
  description: string;
  evidence: string;
  kind: string;
}

export interface AnnotationEvaluation {
	current?: boolean;
  requirementChecks?: Array<{requirement:string; status:'completed'|'failed'|'unverified'; evidence:string}>;
  id: string;
  createdAt: number;
  skillHash: string;
  model: string;
  evidenceHash: string;
  status: string;
  sourceHash?: string;
  reviewPath?: string;
  reviewHash?: string;
  scores: Array<number | null>;
  descriptions: string[];
  taskType: string;
  difficulty: string;
  language: string;
  environment: string;
  harnessVersion: string;
  os: string;
  evidence: string[];
  missing: string[];
  limitations?: string[];
  issues: AnnotationIssue[];
  nextPrompt: string;
  nextPromptType: string;
}

export interface AnnotationRound {
  promptId: string;
  sessionId: string;
  prompt: string;
  order: number;
  status: AnnotationRoundStatus;
  reason: string;
  evidenceHash: string;
  sourceStart: number;
  sourceEnd: number;
  version: string;
  cwd: string;
  captureId: string;
  evaluations: AnnotationEvaluation[] | null;
}

export interface AnnotationCapture {
  id: string;
  dir: string;
  tracePath: string;
  codePath: string;
  hash: string;
  traceHash?: string;
  createdAt: number;
}

export type PairwiseSide = 'A' | 'B';
export type PairwiseConclusion = 'A_better' | 'same' | 'B_better';

export interface PairwiseRun {
  side: PairwiseSide;
  branch: string;
  containerId: string;
  containerName: string;
  workspacePath: string;
  repoRelativePath: string;
  sessionId: string;
  tracePath: string;
  turnCount: number;
  captureId: string;
  captureHash: string;
  traceHash: string;
  deliverableSha: string;
  deliverableUrl: string;
  videoStatus: 'missing' | 'recording' | 'ready' | 'manual_required' | 'failed' | string;
  videoPath: string;
  videoUrl: string;
  recordingError: string;
  recordingGuide?: string[];
  recordingGuideHash?: string;
  recordingGuideGeneratedAt?: number;
  preparedAt: number;
  capturedAt: number;
  committedAt: number;
  containerCleared?: boolean;
}

export interface PairwiseReview {
  current?: boolean;
  id: string;
  status: 'ready' | 'needs_evidence' | string;
  conclusion: PairwiseConclusion;
  reason: string;
  aCompletenessScore?: number;
  aCompletenessDescription?: string;
  bCompletenessScore?: number;
  bCompletenessDescription?: string;
  model: string;
  skillHash: string;
  sourceHashA: string;
  sourceHashB: string;
  reviewPath: string;
  reviewHash: string;
  createdAt: number;
}

export interface PairwiseData {
  prompt: string;
  language: string;
  harness: string;
  harnessVersion: string;
  os: string;
  environment: string;
  runA: PairwiseRun;
  runB: PairwiseRun;
  reviews: PairwiseReview[];
  notes: string;
  validity: string;
  autoRecordEnabled: boolean;
}

export interface AnnotationCase {
		preparation?: AnnotationPreparation;
  mode?: 'legacy' | 'pairwise_gsb';
  pairwise?: PairwiseData;
  taskId: string;
  projectId: string;
  taskName: string;
  sourcePath: string;
  initialSha: string;
  snapshotUrl: string;
  containerId: string;
  containerName: string;
  workspacePath: string;
  repoRelativePath: string;
  sessionId: string;
  tracePath: string;
  completed: boolean;
  rounds: AnnotationRound[];
  captures: AnnotationCapture[];
  revision: number;
  updatedAt: number;
}

export interface AnnotationPreparation {
  jobId: string;
  status: string;
  progress: number;
  message: string;
  error: string;
  startedAt: number;
  finishedAt: number;
  lastActivityAt: number;
}

export interface AnnotationContainer {
  id: string;
  name: string;
  state: string;
  image: string;
  workspacePath: string;
}

export interface TraceCandidate {
  path: string;
  sessionId: string;
  size: number;
}

export interface AnnotationPreflightReport {
  tasks: number;
  rounds: number;
  ready: number;
  notCollected: number;
  issues: string[];
}

export interface AnnotationExportResult {
  outputPath: string;
  reportPath: string;
  rows: number;
  issues: string[];
}

export interface AnnotationBatchPrepareItem {
  taskId: string;
  taskName: string;
  status: 'prepared' | 'skipped' | 'failed';
  message: string;
}

export interface AnnotationBatchPrepareResult {
  total: number;
  prepared: number;
  skipped: number;
  failed: number;
  items: AnnotationBatchPrepareItem[];
}

export interface BindContainerRequest {
  taskId: string;
  containerId: string;
  repoRelativePath: string;
  copyRepository: boolean;
}

export interface CaptureRequest {
  taskId: string;
  tracePath: string;
}

export interface ReviewRequest {
  taskId: string;
  promptId: string;
  force: boolean;
}

export interface EnablePairwiseRequest {
  taskId: string;
  language?: string;
  harness?: string;
  harnessVersion?: string;
  os?: string;
  environment?: string;
  validity?: string;
}

export interface PairwiseSettingsRequest {
  taskId: string;
  language: string;
  environment: string;
  validity: string;
  notes: string;
}

export interface PairwiseSideRequest {
  taskId: string;
  side: PairwiseSide;
}

export interface PairwiseProjectState {
  side: PairwiseSide;
  running: boolean;
  url: string;
  command: string;
}

export interface PairwiseBindRequest extends PairwiseSideRequest {
  containerId: string;
  repoRelativePath: string;
  copyRepository: boolean;
}

export interface PairwiseCaptureRequest extends PairwiseSideRequest {
  tracePath?: string;
}

export interface PairwiseCommitRequest extends PairwiseSideRequest {
  sessionId: string;
}

export interface PairwiseMaterialsRequest extends PairwiseSideRequest {
  videoUrl: string;
  videoPath?: string;
  recordingError: string;
}

export interface PairwiseReviewRequest {
  taskId: string;
  force: boolean;
}

export interface PairwiseBatchReviewRequest {
  projectId: string;
  taskIds?: string[];
  force: boolean;
}

export interface PairwiseBatchReviewItem {
  taskId: string;
  taskName: string;
  status: 'reviewed' | 'reused' | 'skipped' | 'failed';
  message: string;
}

export interface PairwiseBatchReviewResult {
  total: number;
  reviewed: number;
  reused: number;
  skipped: number;
  failed: number;
  items: PairwiseBatchReviewItem[];
}

export interface SaveCaseSettingsRequest {
  taskId: string;
  snapshotUrl: string;
  completed: boolean;
}

export interface ExportAnnotationRequest {
  taskIds?: string[];
  taskId?: string;
  reviewedOnly?: boolean;
  projectId: string;
  submitter: string;
  submittedAt: string;
  draft: boolean;
}

export interface PairwiseExportRequest {
  taskIds: string[];
  projectId: string;
  submitter: string;
  submittedAt: string;
}

const JOB_OPTIONS = { maxRetries: 1, timeoutSeconds: 1800 } as const;

function submitAnnotationJob(jobType: string, taskId: string, input: unknown) {
  return submitJob({
    jobType,
    taskId,
    inputPayload: JSON.stringify(input),
    ...JOB_OPTIONS,
  });
}

export function listCases(projectId: string): Promise<AnnotationCase[]> {
  return callService('AnnotationService', 'ListCases', projectId);
}

export function listContainers(): Promise<AnnotationContainer[]> {
  return callService('AnnotationService', 'ListContainers');
}

export function listTraces(taskId: string): Promise<TraceCandidate[]> {
  return callService('AnnotationService', 'ListTraces', taskId);
}

export function saveCaseSettings(request: SaveCaseSettingsRequest): Promise<AnnotationCase> {
  return callService('AnnotationService', 'SaveCaseSettings', request);
}

export function preflight(projectId: string): Promise<AnnotationPreflightReport> {
  return callService('AnnotationService', 'Preflight', projectId);
}

export function preflightPairwise(projectId: string): Promise<AnnotationPreflightReport> {
  return callService('AnnotationService', 'PreflightPairwise', projectId);
}

export function prepareCase(taskId: string): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_prepare', taskId, { taskId });
}

export function captureAndPrepareTable(request: CaptureRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_capture_table', request.taskId, request);
}

export function batchCaptureAndPrepareTable(taskIds: string[]): Promise<BackgroundJob> {
  return submitJob({
    jobType: 'annotation_batch_capture_table',
    taskId: '',
    inputPayload: JSON.stringify({ taskIds }),
    maxRetries: 1,
    timeoutSeconds: 21600,
  });
}

export function resumeTable(taskId: string): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_resume', taskId, { taskId });
}

export function bindContainer(request: BindContainerRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_bind', request.taskId, request);
}

export function captureCase(request: CaptureRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_capture', request.taskId, request);
}

export function reviewRound(request: ReviewRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_review', request.taskId, request);
}

export function enablePairwise(request: EnablePairwiseRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_pairwise_enable', request.taskId, request);
}

export function preparePairwiseSide(request: PairwiseSideRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_pairwise_prepare_side', request.taskId, request);
}

export function bindPairwiseContainer(request: PairwiseBindRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_pairwise_bind', request.taskId, request);
}

// 清除该侧容器时一并删除 docker 容器和宿主机运行目录。
export function clearPairwiseContainer(request: PairwiseSideRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_pairwise_clear', request.taskId, request);
}

export function startPairwiseProject(request: PairwiseSideRequest): Promise<PairwiseProjectState> {
  return callService('AnnotationService', 'StartPairwiseProject', request);
}

export function stopPairwiseProject(request: PairwiseSideRequest): Promise<void> {
  return callService('AnnotationService', 'StopPairwiseProject', request);
}

export function capturePairwiseSide(request: PairwiseCaptureRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_pairwise_capture', request.taskId, request);
}

export function recordPairwiseVideo(request: PairwiseSideRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_pairwise_record_video', request.taskId, request);
}

export function commitPairwiseSide(request: PairwiseCommitRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_pairwise_commit_side', request.taskId, request);
}

export function savePairwiseMaterials(request: PairwiseMaterialsRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_pairwise_materials', request.taskId, request);
}

export function savePairwiseSettings(request: PairwiseSettingsRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_pairwise_settings', request.taskId, request);
}

export function reviewPairwise(request: PairwiseReviewRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_pairwise_review', request.taskId, request);
}

export function batchReviewPairwise(request: PairwiseBatchReviewRequest): Promise<BackgroundJob> {
  return submitJob({
    jobType: 'annotation_pairwise_batch_review',
    taskId: '',
    inputPayload: JSON.stringify(request),
    maxRetries: 1,
    timeoutSeconds: 21600,
  });
}

export function exportCases(request: ExportAnnotationRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_export', '', request);
}

export function exportPairwise(request: PairwiseExportRequest): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_pairwise_export', '', request);
}

export const getAnnotationJob = getJob;
export const cancelAnnotationJob = cancelJob;

export function publishSnapshot(taskId: string): Promise<BackgroundJob> {
  return submitAnnotationJob('annotation_publish', taskId, { taskId });
}
