import type {
  ChatMessage,
  ChatSession,
  SendMessageRequest,
  SendMessageResponse,
  SessionWithMessages,
} from './chat';
import type { PollOutputResponse, SkillItem, StartClaudeRequest, StartClaudeResponse } from './cli';
import type {
  CodePushRecord,
  CommitCodeRequest,
  PushCodeRequest,
  RedoCommitRequest,
} from './codePush';
import type {
  GitHubAccountConfig,
  GitLabSettings,
  CustomProjectSettings,
  ProjectConfig,
  TraeSettings,
} from './config';
import type {
  DirectoryInspectionResult,
  CustomProjectCandidateScanResult,
  GitLabProject,
  GitLabProjectLookupResult,
  ImportLocalSourcesResult,
  ManagedClaimPathPlan,
  NormalizeManagedSourceFoldersResult,
  QuestionBankItem,
  QuestionBankSyncResult,
} from './git';
import type {
  BackgroundJob,
  JobFilter,
  SubmitJobRequest,
} from './job';
import type {
  AnnotationCase,
  AnnotationContainer,
  AnnotationExportResult,
  AnnotationPreflightReport,
  BindContainerRequest,
  CaptureRequest,
  ExportAnnotationRequest,
  PairwiseProjectState,
  PairwiseSideRequest,
  ReviewRequest,
  SaveCaseSettingsRequest,
  TraceCandidate,
} from './annotation';
import type {
  GeneratePromptRequest,
  GenerateCustomProjectPromptDocumentsRequest,
  GenerateCustomProjectPromptDocumentsResult,
  CustomProjectPromptDocumentDetail,
  LlmProviderConfig,
  PolishTextRequest,
  PolishTextResult,
  PromptGenerationResult,
} from './llm';
import type {
  PublishSourceRepoRequest,
  PublishSourceRepoResult,
  SubmitAllRequest,
  SubmitAllResult,
  SubmitModelRunRequest,
  SubmitModelRunResult,
} from './submit';
import type {
  AiReviewNodeFromDB,
  AiReviewRoundFromDB,
  AddModelRunRequest,
  BatchUpdateResult,
  BatchUpdateTasksRequest,
  CreateTasksFromCustomPromptDocumentsRequest,
  CreateTasksFromCustomPromptDocumentsResult,
  CreateTaskRequest,
  ExtractTaskSessionsResult,
  ModelRunFromDB,
  SoloProjectXlsxExportResult,
  TaskChildDirectory,
  TaskFromDB,
  TaskReadme,
  UpdateModelRunRequest,
  UpdateModelRunSessionRequest,
  UpdateTaskReportFieldsRequest,
  UpdateTaskSessionListRequest,
} from './task';

type ServiceMethod<Args extends unknown[], Result> = {
  args: Args;
  result: Result;
};

export type WailsServiceContract = {
  AnnotationService: {
    ListCases: ServiceMethod<[projectId: string], AnnotationCase[]>;
    ListContainers: ServiceMethod<[], AnnotationContainer[]>;
    PrepareCase: ServiceMethod<[request: { taskId: string }], AnnotationCase>;
    BindContainer: ServiceMethod<[request: BindContainerRequest], AnnotationCase>;
    ListTraces: ServiceMethod<[taskId: string], TraceCandidate[]>;
    Capture: ServiceMethod<[request: CaptureRequest], AnnotationCase>;
    Review: ServiceMethod<[request: ReviewRequest], AnnotationCase>;
    SaveCaseSettings: ServiceMethod<[request: SaveCaseSettingsRequest], AnnotationCase>;
    Preflight: ServiceMethod<[projectId: string], AnnotationPreflightReport>;
    PreflightPairwise: ServiceMethod<[projectId: string], AnnotationPreflightReport>;
    StartPairwiseProject: ServiceMethod<[request: PairwiseSideRequest], PairwiseProjectState>;
    StopPairwiseProject: ServiceMethod<[request: PairwiseSideRequest], void>;
    Export: ServiceMethod<[request: ExportAnnotationRequest], AnnotationExportResult>;
  };
  ChatService: {
    CreateSession: ServiceMethod<[request: { taskId: string; model: string }], ChatSession>;
    ListSessions: ServiceMethod<[taskId: string, model: string], ChatSession[]>;
    GetSessionWithMessages: ServiceMethod<[sessionId: string], SessionWithMessages>;
    RenameSession: ServiceMethod<[sessionId: string, title: string], void>;
    DeleteSession: ServiceMethod<[sessionId: string], void>;
    SendMessage: ServiceMethod<[request: SendMessageRequest], SendMessageResponse>;
    GetMessage: ServiceMethod<[messageId: string], ChatMessage>;
    SaveMessageAsPrompt: ServiceMethod<[taskId: string, messageId: string], void>;
  };
  CliService: {
    CheckCLI: ServiceMethod<[], string>;
    StartClaude: ServiceMethod<[request: StartClaudeRequest], StartClaudeResponse>;
    PollOutput: ServiceMethod<[request: { sessionId: string; offset: number }], PollOutputResponse>;
    CancelSession: ServiceMethod<[sessionId: string], void>;
    ListSkills: ServiceMethod<[], SkillItem[]>;
  };
  ConfigService: {
    GetConfig: ServiceMethod<[key: string], string>;
    SetConfig: ServiceMethod<[key: string, value: string], void>;
    GetAnnotationSettings: ServiceMethod<[], import('./config').AnnotationSettings>;
    SaveAnnotationSettings: ServiceMethod<[
      reviewEngine: import('./config').AnnotationReviewEngine,
      containerApiKey: string,
    ], void>;
    GetAnnotationContainerAPIKey: ServiceMethod<[], string>;
    TestGitLabConnection: ServiceMethod<[url: string, token: string, skipTlsVerify: boolean], boolean>;
    TestGitHubConnection: ServiceMethod<[username: string, token: string], boolean>;
    TestGitHubAccountConnection: ServiceMethod<
      [id: string, username: string, token: string],
      boolean
    >;
    GetGitLabSettings: ServiceMethod<[], GitLabSettings>;
    SaveGitLabSettings: ServiceMethod<[url: string, username: string, token: string, skipTlsVerify: boolean], void>;
    GetCustomProjectSettings: ServiceMethod<[], CustomProjectSettings>;
    SaveCustomProjectSettings: ServiceMethod<[rootPath: string], void>;
    SaveCustomProjectSettingsWithPrefixes: ServiceMethod<[rootPath: string, prefixes: string], void>;
    PickCustomProjectRootDirectory: ServiceMethod<[], string>;
    ListProjects: ServiceMethod<[], ProjectConfig[]>;
    CreateProject: ServiceMethod<[project: ProjectConfig], void>;
    CreateProjectBatch: ServiceMethod<[sourceProjectId: string, project: ProjectConfig], void>;
    UpdateProject: ServiceMethod<[project: ProjectConfig], void>;
    DeleteProject: ServiceMethod<[id: string], void>;
    ConsumeProjectQuota: ServiceMethod<[projectId: string, taskType: string], void>;
    ListLLMProviders: ServiceMethod<[], LlmProviderConfig[]>;
    CreateLLMProvider: ServiceMethod<[provider: LlmProviderConfig], void>;
    UpdateLLMProvider: ServiceMethod<[provider: LlmProviderConfig], void>;
    DeleteLLMProvider: ServiceMethod<[id: string], void>;
    ListGitHubAccounts: ServiceMethod<[], GitHubAccountConfig[]>;
    CreateGitHubAccount: ServiceMethod<[account: GitHubAccountConfig], void>;
    UpdateGitHubAccount: ServiceMethod<[account: GitHubAccountConfig], void>;
    DeleteGitHubAccount: ServiceMethod<[id: string], void>;
    GetTraeSettings: ServiceMethod<[], TraeSettings>;
    SaveTraeSettings: ServiceMethod<[workspaceStoragePath: string, logsPath: string], void>;
  };
  GitService: {
    FetchGitLabProject: ServiceMethod<[projectRef: string, url: string, token: string], GitLabProject>;
    FetchGitLabProjects: ServiceMethod<
      [projectRefs: string[], url: string, token: string],
      GitLabProjectLookupResult[]
    >;
    FetchConfiguredGitLabProjects: ServiceMethod<
      [projectRefs: string[]],
      GitLabProjectLookupResult[]
    >;
    CloneProject: ServiceMethod<
      [cloneUrl: string, path: string, username: string, token: string],
      void
    >;
    CloneConfiguredProject: ServiceMethod<[cloneUrl: string, path: string], void>;
    DownloadGitLabProject: ServiceMethod<
      [projectId: number, url: string, token: string, destination: string, sha: string | null],
      void
    >;
    CopyProjectDirectory: ServiceMethod<[sourcePath: string, destinationPath: string], void>;
    CheckPathsExist: ServiceMethod<[paths: string[]], string[]>;
    InspectDirectory: ServiceMethod<[path: string], DirectoryInspectionResult>;
    PlanManagedClaimPaths: ServiceMethod<
      [
        basePath: string,
        projectName: string,
        projectId: number,
        taskType: string,
        count: number,
        projectConfigId: string,
      ],
      ManagedClaimPathPlan[]
    >;
    NormalizeManagedSourceFolders: ServiceMethod<
      [projectId: string],
      NormalizeManagedSourceFoldersResult
    >;
    ListQuestionBankItems: ServiceMethod<[projectId: string], QuestionBankItem[]>;
    ScanLocalQuestionBank: ServiceMethod<[projectId: string], ImportLocalSourcesResult>;
    ScanCustomProjects: ServiceMethod<[projectId: string], ImportLocalSourcesResult>;
    ScanCustomProjectCandidates: ServiceMethod<[projectId: string], CustomProjectCandidateScanResult>;
    ImportSelectedCustomProjects: ServiceMethod<
      [projectId: string, projectNames: string[]],
      ImportLocalSourcesResult
    >;
    SyncGitLabQuestionBank: ServiceMethod<
      [projectId: string, questionIds: number[]],
      QuestionBankSyncResult
    >;
    RefreshQuestionBankItem: ServiceMethod<
      [projectId: string, questionId: number],
      QuestionBankSyncResult
    >;
    DeleteQuestionBankItem: ServiceMethod<[projectId: string, questionId: number], void>;
    ImportLocalSources: ServiceMethod<[projectId: string], ImportLocalSourcesResult>;
    PickQuestionBankArchives: ServiceMethod<[], string[]>;
    ImportQuestionBankArchives: ServiceMethod<
      [projectId: string, archivePaths: string[]],
      ImportLocalSourcesResult
    >;
  };
  PromptService: {
    TestLLMProvider: ServiceMethod<[provider: LlmProviderConfig], boolean>;
    GenerateTaskPrompt: ServiceMethod<[request: GeneratePromptRequest], PromptGenerationResult>;
    SaveTaskPrompt: ServiceMethod<[taskId: string, promptText: string], void>;
    GenerateCustomProjectPromptDocuments: ServiceMethod<
      [request: GenerateCustomProjectPromptDocumentsRequest],
      GenerateCustomProjectPromptDocumentsResult
    >;
    ReadCustomProjectPromptDocument: ServiceMethod<
      [path: string],
      CustomProjectPromptDocumentDetail
    >;
    SaveCustomProjectPromptDocument: ServiceMethod<
      [request: { path: string; content: string }],
      CustomProjectPromptDocumentDetail
    >;
    PolishText: ServiceMethod<[request: PolishTextRequest], PolishTextResult>;
  };
  SubmitService: {
    PublishSourceRepo: ServiceMethod<[request: PublishSourceRepoRequest], PublishSourceRepoResult>;
    SubmitModelRun: ServiceMethod<[request: SubmitModelRunRequest], SubmitModelRunResult>;
    SubmitAll: ServiceMethod<[request: SubmitAllRequest], SubmitAllResult>;
  };
  CodePushService: {
    ListCodePushRecords: ServiceMethod<[taskId: string], CodePushRecord[]>;
    CommitCode: ServiceMethod<[request: CommitCodeRequest], CodePushRecord>;
    RedoCommit: ServiceMethod<[request: RedoCommitRequest], CodePushRecord>;
    PushCode: ServiceMethod<[request: PushCodeRequest], CodePushRecord>;
  };
  JobService: {
    SubmitJob: ServiceMethod<[request: SubmitJobRequest], BackgroundJob>;
    ListJobs: ServiceMethod<[filter: JobFilter | null], BackgroundJob[]>;
    GetJob: ServiceMethod<[id: string], BackgroundJob | null>;
    RetryJob: ServiceMethod<[id: string], BackgroundJob>;
    CancelJob: ServiceMethod<[id: string], void>;
    DeleteAiReviewJob: ServiceMethod<[id: string], void>;
  };
  TaskService: {
    ListTasks: ServiceMethod<[projectConfigId: string | null], TaskFromDB[]>;
    GetTask: ServiceMethod<[id: string], TaskFromDB | null>;
    ListModelRuns: ServiceMethod<[taskId: string], ModelRunFromDB[]>;
    ListAiReviewNodes: ServiceMethod<[taskId: string], AiReviewNodeFromDB[]>;
    ListAiReviewRounds: ServiceMethod<[taskId: string], AiReviewRoundFromDB[]>;
    ResetTaskAiReview: ServiceMethod<[taskId: string], void>;
    ResetAiReviewRound: ServiceMethod<[roundId: string], void>;
    ListTaskChildDirectories: ServiceMethod<[taskId: string], TaskChildDirectory[]>;
    GetTaskReadme: ServiceMethod<[taskId: string], TaskReadme | null>;
    CreateTask: ServiceMethod<[task: CreateTaskRequest], TaskFromDB>;
    PickCustomPromptDocuments: ServiceMethod<[], string[]>;
    CreateTasksFromCustomPromptDocuments: ServiceMethod<
      [request: CreateTasksFromCustomPromptDocumentsRequest],
      CreateTasksFromCustomPromptDocumentsResult
    >;
    UpdateTaskStatus: ServiceMethod<[id: string, status: string], void>;
    UpdateTaskType: ServiceMethod<[id: string, taskType: string], void>;
    UpdateTaskSessionList: ServiceMethod<[request: UpdateTaskSessionListRequest], void>;
    ExtractTaskSessions: ServiceMethod<[taskId: string], ExtractTaskSessionsResult>;
    UpdateModelRun: ServiceMethod<[request: UpdateModelRunRequest], void>;
    DeleteTask: ServiceMethod<[id: string], void>;
    OpenTaskLocalFolder: ServiceMethod<[id: string], void>;
    UpdateModelRunSessionInfo: ServiceMethod<[request: UpdateModelRunSessionRequest], void>;
    AddModelRun: ServiceMethod<[request: AddModelRunRequest], void>;
    DeleteModelRun: ServiceMethod<[taskId: string, modelName: string], void>;
    UpdateTaskReportFields: ServiceMethod<[request: UpdateTaskReportFieldsRequest], void>;
    ExportSoloProjectXlsx: ServiceMethod<[projectName: string], SoloProjectXlsxExportResult>;
    BatchUpdateTasks: ServiceMethod<[request: BatchUpdateTasksRequest], BatchUpdateResult>;
    BatchDeleteTasks: ServiceMethod<[taskIds: string[]], BatchUpdateResult>;
    SaveAiReviewRoundNotes: ServiceMethod<
      [roundID: string, reviewNotes: string, nextPrompt: string, nextPromptTaskType: string],
      void
    >;
    SaveAiReviewRoundDissatisfactionSummary: ServiceMethod<
      [roundID: string, dissatisfactionSummary: string],
      void
    >;
  };
};
