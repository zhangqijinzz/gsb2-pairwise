package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	appcli "github.com/blueship581/pinru/app/cli"
	appgit "github.com/blueship581/pinru/app/git"
	appprompt "github.com/blueship581/pinru/app/prompt"
	appsubmit "github.com/blueship581/pinru/app/submit"
	apptask "github.com/blueship581/pinru/app/task"
	annotationdomain "github.com/blueship581/pinru/internal/annotation"
	"github.com/blueship581/pinru/internal/errs"
	"github.com/blueship581/pinru/internal/store"
	"github.com/blueship581/pinru/internal/util"
	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	gitCloneConcurrencyLimit       = 3
	promptGenerateConcurrencyLimit = 1
	gitCloneRetryAttempts          = 3
	gitCloneRetryBackoff           = 2 * time.Second
	gitCloneIdleTimeout            = 30 * time.Second
	msgAiReviewCommitRequired      = "请先提交代码，再发起 AI 复审"
	msgDeepSeekReviewProvider      = "请先在设置中添加带 API Key 的 DeepSeek V4 Flash API 提供商，或将审核引擎切换为 Codex CLI"
)

var errGitCloneIdleTimeout = fmt.Errorf(errs.FmtJobGitCloneIdleTimeout, gitCloneIdleTimeout)

type AnnotationHandler func(context.Context, string, string) (any, error)

type JobService struct {
	store             *store.Store
	promptSvc         *appprompt.PromptService
	gitSvc            *appgit.GitService
	submitSvc         *appsubmit.SubmitService
	taskSvc           *apptask.TaskService
	cliSvc            *appcli.CliService
	mu                sync.Mutex
	running           map[string]context.CancelFunc
	cloneSem          chan struct{}
	promptGenerateSem chan struct{}
	annotationHandler AnnotationHandler
}

func New(
	st *store.Store,
	promptSvc *appprompt.PromptService,
	gitSvc *appgit.GitService,
	submitSvc *appsubmit.SubmitService,
	taskSvc *apptask.TaskService,
	cliSvc *appcli.CliService,
	annotationHandlers ...AnnotationHandler,
) *JobService {
	s := &JobService{
		store:             st,
		promptSvc:         promptSvc,
		gitSvc:            gitSvc,
		submitSvc:         submitSvc,
		taskSvc:           taskSvc,
		cliSvc:            cliSvc,
		running:           make(map[string]context.CancelFunc),
		cloneSem:          make(chan struct{}, gitCloneConcurrencyLimit),
		promptGenerateSem: make(chan struct{}, promptGenerateConcurrencyLimit),
	}
	if len(annotationHandlers) > 0 {
		s.annotationHandler = annotationHandlers[0]
	}
	return s
}

type reviewExecutionSelection struct {
	DeepSeek *appcli.DeepSeekCodexConfig
	Label    string
}

func (s *JobService) reviewExecutionConfig() (reviewExecutionSelection, error) {
	if s.store == nil {
		return reviewExecutionSelection{}, errors.New(msgDeepSeekReviewProvider)
	}
	engine, err := s.store.GetConfig("annotation_review_engine")
	if err != nil {
		return reviewExecutionSelection{}, err
	}
	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "" {
		engine = "deepseek"
	}
	if engine == "codex" {
		return reviewExecutionSelection{Label: "Codex CLI"}, nil
	}
	if engine != "deepseek" {
		return reviewExecutionSelection{}, errors.New("不支持的审核引擎，请在设置中重新选择")
	}
	providers, err := s.store.ListLLMProviders()
	if err != nil {
		return reviewExecutionSelection{}, err
	}
	var selected *store.LLMProvider
	for _, requireDefault := range []bool{true, false} {
		for i := range providers {
			if providers[i].IsDefault == requireDefault && isDeepSeekReviewProvider(providers[i]) {
				selected = &providers[i]
				break
			}
		}
		if selected != nil {
			break
		}
	}
	if selected == nil {
		return reviewExecutionSelection{}, errors.New(msgDeepSeekReviewProvider)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(*selected.BaseURL), "/")
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	config := &appcli.DeepSeekCodexConfig{
		Model:           strings.TrimSpace(selected.Model),
		BaseURL:         baseURL,
		APIKey:          strings.TrimSpace(selected.APIKey),
		ReasoningEffort: "high",
	}
	return reviewExecutionSelection{DeepSeek: config, Label: "DeepSeek V4 Flash"}, nil
}

func isDeepSeekReviewProvider(provider store.LLMProvider) bool {
	if provider.ProviderType != "openai_compatible" || strings.TrimSpace(provider.APIKey) == "" || provider.BaseURL == nil {
		return false
	}
	model := strings.ToLower(strings.TrimSpace(provider.Model))
	if model != "deepseek-v4-flash" && model != "deepseek-flash" {
		return false
	}
	baseURL := strings.ToLower(strings.TrimRight(strings.TrimSpace(*provider.BaseURL), "/"))
	return baseURL == "https://api.deepseek.com" || baseURL == "https://api.deepseek.com/v1"
}

type SubmitJobRequest struct {
	JobType        string `json:"jobType"`
	TaskID         string `json:"taskId"`
	InputPayload   string `json:"inputPayload"`
	MaxRetries     int    `json:"maxRetries"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

type JobProgressEvent struct {
	ID              string  `json:"id"`
	JobType         string  `json:"jobType"`
	TaskID          *string `json:"taskId"`
	Status          string  `json:"status"`
	Progress        int     `json:"progress"`
	ProgressMessage *string `json:"progressMessage"`
	ErrorMessage    *string `json:"errorMessage"`
}

type jobExecutionResult struct {
	outputPayload *string
	finalMessage  *string
}

func (s *JobService) SubmitJob(req SubmitJobRequest) (*store.BackgroundJob, error) {
	if req.MaxRetries <= 0 {
		req.MaxRetries = 3
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 300
	}
	if req.JobType == "ai_review" {
		payload, ok := parseAiReviewPayloadForDedup(req.InputPayload)
		if !ok {
			return nil, errors.New(errs.MsgJobAiReviewParseFail)
		}
		s.mu.Lock()
		existing, err := s.findActiveJobLocked(req)
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		if existing != nil {
			s.mu.Unlock()
			return existing, nil
		}
		s.mu.Unlock()
		preparedPayload, err := s.prepareAiReviewPayload(req.TaskID, payload)
		if err != nil {
			return nil, err
		}
		payloadJSON, err := json.Marshal(preparedPayload)
		if err != nil {
			return nil, fmt.Errorf(errs.FmtJobSerializeAiReview, err)
		}
		req.InputPayload = string(payloadJSON)
	}

	id := uuid.New().String()
	now := time.Now().Unix()
	taskID := &req.TaskID
	if req.TaskID == "" {
		taskID = nil
	}

	job := store.BackgroundJob{
		ID:             id,
		JobType:        req.JobType,
		TaskID:         taskID,
		Status:         "pending",
		Progress:       0,
		InputPayload:   req.InputPayload,
		MaxRetries:     req.MaxRetries,
		TimeoutSeconds: req.TimeoutSeconds,
		CreatedAt:      now,
	}

	s.mu.Lock()
	existing, err := s.findActiveJobLocked(req)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if existing != nil {
		s.mu.Unlock()
		return existing, nil
	}
	if err := s.store.CreateBackgroundJob(job); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf(errs.FmtJobCreateFail, err)
	}
	s.mu.Unlock()

	go s.executeJob(id, req)

	created, _ := s.store.GetBackgroundJob(id)
	if created != nil {
		return created, nil
	}
	return &job, nil
}

func (s *JobService) findActiveJobLocked(req SubmitJobRequest) (*store.BackgroundJob, error) {
	if strings.HasPrefix(req.JobType, "annotation_") {
		jobs, err := s.store.ListBackgroundJobs(nil)
		if err != nil {
			return nil, err
		}
		for i := range jobs {
			j := &jobs[i]
			if j.JobType == req.JobType && j.InputPayload == req.InputPayload &&
				(j.Status == "pending" || j.Status == "running") {
				return j, nil
			}
		}
		return nil, nil
	}
	if req.JobType != "ai_review" || strings.TrimSpace(req.TaskID) == "" {
		return nil, nil
	}

	currentPayload, ok := parseAiReviewPayloadForDedup(req.InputPayload)
	if !ok {
		return nil, nil
	}
	if len(aiReviewTargetKeys(currentPayload)) == 0 {
		return nil, nil
	}

	filter := &store.JobFilter{TaskID: &req.TaskID}
	jobs, err := s.store.ListBackgroundJobs(filter)
	if err != nil {
		return nil, fmt.Errorf(errs.FmtJobQueryFail, err)
	}

	for _, job := range jobs {
		if job.JobType != "ai_review" {
			continue
		}
		if job.Status != "pending" && job.Status != "running" {
			continue
		}
		payload, ok := parseAiReviewPayloadForDedup(job.InputPayload)
		if !ok {
			continue
		}
		if sameAiReviewTarget(currentPayload, payload) {
			existing := job
			return &existing, nil
		}
	}

	return nil, nil
}

func (s *JobService) ListJobs(filter *store.JobFilter) ([]store.BackgroundJob, error) {
	jobs, err := s.store.ListBackgroundJobs(filter)
	if err != nil {
		return nil, err
	}
	if jobs == nil {
		return []store.BackgroundJob{}, nil
	}
	return jobs, nil
}

func (s *JobService) GetJob(id string) (*store.BackgroundJob, error) {
	return s.store.GetBackgroundJob(id)
}

func (s *JobService) RetryJob(id string) (*store.BackgroundJob, error) {
	job, err := s.store.GetBackgroundJob(id)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, fmt.Errorf(errs.FmtTaskNotFound, id)
	}
	if job.Status != "error" {
		return nil, errors.New(errs.MsgJobRetryOnlyFailed)
	}
	if job.RetryCount >= job.MaxRetries {
		return nil, fmt.Errorf(errs.FmtJobMaxRetryReached, job.MaxRetries)
	}

	if err := s.store.IncrementBackgroundJobRetry(id); err != nil {
		return nil, err
	}

	taskID := ""
	if job.TaskID != nil {
		taskID = *job.TaskID
	}
	go s.executeJob(id, SubmitJobRequest{
		JobType:        job.JobType,
		TaskID:         taskID,
		InputPayload:   job.InputPayload,
		TimeoutSeconds: job.TimeoutSeconds,
	})

	return s.store.GetBackgroundJob(id)
}

func (s *JobService) CancelJob(id string) error {
	job, err := s.store.GetBackgroundJob(id)
	if err != nil {
		return err
	}
	if job == nil {
		return fmt.Errorf(errs.FmtTaskNotFound, id)
	}

	s.mu.Lock()
	cancel, ok := s.running[id]
	s.mu.Unlock()

	if ok {
		cancel()
	}
	if err := s.store.CancelBackgroundJob(id); err != nil {
		return err
	}
	if err := s.restoreAiReviewRoundAfterCancellation(job); err != nil {
		return err
	}

	taskID := ""
	if job.TaskID != nil {
		taskID = *job.TaskID
	}
	s.emitProgress(id, job.JobType, taskID, "cancelled", job.Progress, strPtr("已取消"), nil)
	return nil
}

func (s *JobService) DeleteAiReviewJob(id string) error {
	job, err := s.store.GetBackgroundJob(id)
	if err != nil {
		return err
	}
	if job == nil {
		return fmt.Errorf(errs.FmtTaskNotFound, id)
	}
	if job.JobType != "ai_review" {
		return errors.New(errs.MsgJobDeleteOnlyReview)
	}
	if job.Status == "pending" || job.Status == "running" {
		return errors.New(errs.MsgJobReviewStillRunning)
	}
	if err := s.deleteAiReviewRoundLinkedToJob(job); err != nil {
		return err
	}
	if err := s.store.DeleteBackgroundJob(id); err != nil {
		return err
	}

	return nil
}

func (s *JobService) deleteAiReviewRoundLinkedToJob(job *store.BackgroundJob) error {
	if job == nil {
		return nil
	}

	var payload AiReviewPayload
	if err := json.Unmarshal([]byte(job.InputPayload), &payload); err != nil {
		return nil
	}

	roundID := ""
	if payload.ReviewRoundID != nil {
		roundID = strings.TrimSpace(*payload.ReviewRoundID)
	}
	if roundID == "" && payload.ReviewNodeID != nil {
		roundID = strings.TrimSpace(*payload.ReviewNodeID)
	}
	if roundID == "" && job.OutputPayload != nil && strings.TrimSpace(*job.OutputPayload) != "" {
		var result AiReviewResult
		if err := json.Unmarshal([]byte(*job.OutputPayload), &result); err == nil {
			roundID = strings.TrimSpace(result.ReviewRoundID)
		}
	}
	if roundID == "" {
		return nil
	}

	round, err := s.store.DeleteAiReviewRound(roundID)
	if err != nil {
		return err
	}
	if round != nil && round.ModelRunID != nil && strings.TrimSpace(*round.ModelRunID) != "" {
		return s.syncModelRunAiReviewSummaryFromRounds(strings.TrimSpace(*round.ModelRunID))
	}
	return nil
}

func (s *JobService) restoreAiReviewRoundAfterCancellation(job *store.BackgroundJob) error {
	if job == nil || job.JobType != "ai_review" || job.TaskID == nil {
		return nil
	}

	payload, ok := parseAiReviewPayloadForDedup(job.InputPayload)
	if !ok || payload.ReviewRoundID == nil || strings.TrimSpace(*payload.ReviewRoundID) == "" {
		return nil
	}

	round, err := s.store.GetAiReviewRound(strings.TrimSpace(*payload.ReviewRoundID))
	if err != nil || round == nil {
		return err
	}
	if round.JobID == nil || strings.TrimSpace(*round.JobID) != job.ID {
		return nil
	}

	// 取消时恢复到 none 状态
	previousStatus := "none"
	if payload.RoundSnapshot != nil {
		previousStatus = firstNonEmpty(payload.RoundSnapshot.Status, "none")
	}
	if err := s.store.UpdateAiReviewRoundStatus(round.ID, previousStatus, nil); err != nil {
		return err
	}

	if round.ModelRunID != nil && strings.TrimSpace(*round.ModelRunID) != "" {
		return s.syncModelRunAiReviewSummaryFromRounds(strings.TrimSpace(*round.ModelRunID))
	}
	return nil
}

func (s *JobService) executeJob(id string, req SubmitJobRequest) {
	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(req.TimeoutSeconds)*time.Second)
	defer cancel()

	s.mu.Lock()
	s.running[id] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, id)
		s.mu.Unlock()
	}()

	if s.isJobCancelled(id) {
		slog.Info("job skipped because it was already cancelled",
			"job_id", id,
			"job_type", req.JobType,
			"task_id", req.TaskID,
		)
		return
	}

	if err := s.store.StartBackgroundJob(id); err != nil {
		slog.Error("failed to mark job as started", "job_id", id, "error", err)
	}
	// 尝试获取项目名称用于日志标识
	jobLabel := ""
	if req.TaskID != "" {
		if task, err := s.store.GetTask(req.TaskID); err == nil && task != nil {
			jobLabel = task.ProjectName
		}
	}

	slog.Info("job started",
		"job_id", id,
		"job_type", req.JobType,
		"project", jobLabel,
		"task_id", req.TaskID,
		"timeout_s", req.TimeoutSeconds,
	)

	if jobLabel != "" {
		s.emitProgress(id, req.JobType, req.TaskID, "running", 0, strPtr(fmt.Sprintf("[%s] 准备中…", jobLabel)), nil)
	} else {
		s.emitProgress(id, req.JobType, req.TaskID, "running", 0, strPtr("准备中…"), nil)
	}

	var (
		execErr    error
		execResult jobExecutionResult
	)
	switch req.JobType {
	case "prompt_generate":
		execResult, execErr = s.executePromptGenerate(ctx, id, req)
	case "session_sync":
		execResult, execErr = s.executeSessionSync(ctx, id, req)
	case "git_clone":
		execResult, execErr = s.executeGitClone(ctx, id, req)
	case "question_bank_materialize":
		execResult, execErr = s.executeQuestionBankMaterialize(ctx, id, req)
	case "custom_prompt_document_generate":
		execResult, execErr = s.executeCustomPromptDocumentGenerate(ctx, id, req)
	case "custom_prompt_task_create":
		execResult, execErr = s.executeCustomPromptTaskCreate(ctx, id, req)
	case "pr_submit":
		execResult, execErr = s.executePrSubmit(ctx, id, req)
	case "ai_review":
		execResult, execErr = s.executeAiReview(ctx, id, req)
	case "annotation_publish", "annotation_prepare", "annotation_bind", "annotation_capture", "annotation_capture_table", "annotation_batch_capture_table", "annotation_resume", "annotation_review", "annotation_export",
		"annotation_pairwise_enable", "annotation_pairwise_prepare_side", "annotation_pairwise_bind", "annotation_pairwise_clear", "annotation_pairwise_commit_side", "annotation_pairwise_capture", "annotation_pairwise_recording_guide", "annotation_pairwise_record_video", "annotation_pairwise_materials", "annotation_pairwise_settings", "annotation_pairwise_review", "annotation_pairwise_batch_review", "annotation_pairwise_export":
		if s.annotationHandler == nil {
			execErr = errors.New("容器标注服务尚未注册")
		} else {
			var output any
			annotationCtx := annotationdomain.WithProgress(ctx, func(progress int, message string) {
				s.emitProgress(id, req.JobType, req.TaskID, "running", progress, strPtr(message), nil)
			})
			output, execErr = s.annotationHandler(annotationCtx, req.JobType, req.InputPayload)
			if execErr == nil {
				var raw []byte
				raw, execErr = json.Marshal(output)
				if execErr == nil {
					execResult.outputPayload = strPtr(string(raw))
					execResult.finalMessage = strPtr("标注操作完成")
				}
			}
		}
	default:
		execErr = fmt.Errorf(errs.FmtJobUnknownType, req.JobType)
	}

	if ctx.Err() == context.DeadlineExceeded {
		if s.isJobCancelled(id) {
			return
		}
		errMsg := "执行超时"
		if err := s.store.FailBackgroundJob(id, errMsg); err != nil {
			slog.Error("failed to mark job as failed (timeout)", "job_id", id, "error", err)
		}
		if !s.isJobCancelled(id) {
			s.emitProgress(id, req.JobType, req.TaskID, "error", 0, nil, &errMsg)
		}
		slog.Error("job timeout",
			"job_id", id,
			"job_type", req.JobType,
			"project", jobLabel,
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
		return
	}
	if ctx.Err() == context.Canceled {
		slog.Info("job cancelled",
			"job_id", id,
			"job_type", req.JobType,
			"project", jobLabel,
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
		return
	}

	if execErr != nil {
		if s.isJobCancelled(id) {
			return
		}
		errMsg := execErr.Error()
		if err := s.store.FailBackgroundJob(id, errMsg); err != nil {
			slog.Error("failed to mark job as failed", "job_id", id, "error", err)
		}
		if !s.isJobCancelled(id) {
			s.emitProgress(id, req.JobType, req.TaskID, "error", 0, nil, &errMsg)
		}
		slog.Error("job failed",
			"job_id", id,
			"job_type", req.JobType,
			"project", jobLabel,
			"error", errMsg,
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
		return
	}

	if s.isJobCancelled(id) {
		return
	}
	finalMessage := execResult.finalMessage
	if finalMessage == nil {
		finalMessage = strPtr("已完成")
	}
	if err := s.store.CompleteBackgroundJobWithMessage(id, execResult.outputPayload, finalMessage); err != nil {
		slog.Error("failed to mark job as complete", "job_id", id, "error", err)
	}
	s.emitProgress(id, req.JobType, req.TaskID, "done", 100, finalMessage, nil)
	slog.Info("job completed",
		"job_id", id,
		"job_type", req.JobType,
		"project", jobLabel,
		"elapsed", time.Since(start).Round(time.Millisecond),
	)
}

func (s *JobService) executePromptGenerate(
	ctx context.Context,
	jobID string,
	req SubmitJobRequest,
) (jobExecutionResult, error) {
	start := time.Now()

	var promptReq appprompt.GeneratePromptRequest
	if err := json.Unmarshal([]byte(req.InputPayload), &promptReq); err != nil {
		return jobExecutionResult{}, fmt.Errorf(errs.FmtJobParsePromptGenParam, err)
	}

	// 获取项目名称用于日志标识
	projectLabel := req.TaskID
	if task, err := s.store.GetTask(req.TaskID); err == nil && task != nil {
		projectLabel = task.ProjectName
	}

	slog.Info("prompt generation started",
		"project", projectLabel,
		"task_type", promptReq.TaskType,
	)
	s.emitProgress(jobID, req.JobType, req.TaskID, "running", 10, strPtr(fmt.Sprintf("[%s] 等待提示词生成队列…", projectLabel)), nil)

	if err := s.acquirePromptGenerateSlot(ctx, jobID, req.TaskID, projectLabel); err != nil {
		return jobExecutionResult{}, err
	}
	defer func() { <-s.promptGenerateSem }()

	s.emitProgress(jobID, req.JobType, req.TaskID, "running", 20, strPtr(fmt.Sprintf("[%s] 正在生成提示词…", projectLabel)), nil)

	res, err := s.promptSvc.GenerateTaskPromptWithContext(ctx, promptReq)
	if err != nil {
		slog.Error("prompt generation failed",
			"project", projectLabel,
			"error", err,
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
		return jobExecutionResult{}, err
	}

	slog.Info("prompt generation completed",
		"project", projectLabel,
		"model", res.Model,
		"elapsed", time.Since(start).Round(time.Millisecond),
	)
	outputJSON, _ := json.Marshal(res)
	outputStr := string(outputJSON)
	return jobExecutionResult{
		outputPayload: &outputStr,
		finalMessage:  strPtr(fmt.Sprintf("[%s] 提示词已生成", projectLabel)),
	}, nil
}

func (s *JobService) executeCustomPromptDocumentGenerate(
	ctx context.Context,
	jobID string,
	req SubmitJobRequest,
) (jobExecutionResult, error) {
	if s.promptSvc == nil {
		return jobExecutionResult{}, errors.New("提示词服务未初始化")
	}

	var promptReq appprompt.GenerateCustomProjectPromptDocumentsRequest
	if err := json.Unmarshal([]byte(req.InputPayload), &promptReq); err != nil {
		return jobExecutionResult{}, fmt.Errorf("解析自定义项目提示词生成参数失败：%w", err)
	}
	projectCount := len(promptReq.ProjectNames)
	s.emitProgress(jobID, req.JobType, req.TaskID, "running", 10, strPtr(fmt.Sprintf("准备生成 %d 个项目的提示词文档…", projectCount)), nil)

	progressOptions := &appprompt.GenerateCustomProjectPromptDocumentsOptions{
		OnProgress: func(progress appprompt.CustomProjectPromptDocumentProgress) {
			value, message := customPromptDocumentProgressView(progress)
			s.emitProgress(jobID, req.JobType, req.TaskID, "running", value, strPtr(message), nil)
		},
	}

	res, err := s.promptSvc.GenerateCustomProjectPromptDocumentsWithOptions(ctx, promptReq, progressOptions)
	if err != nil {
		return jobExecutionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return jobExecutionResult{}, err
	}

	outputJSON, _ := json.Marshal(res)
	outputStr := string(outputJSON)
	return jobExecutionResult{
		outputPayload: &outputStr,
		finalMessage:  strPtr(fmt.Sprintf("提示词文档生成完成：成功 %d，失败 %d", res.GeneratedCount, res.ErrorCount)),
	}, nil
}

func customPromptDocumentProgressView(progress appprompt.CustomProjectPromptDocumentProgress) (int, string) {
	total := progress.Total
	if total <= 0 {
		total = 1
	}
	index := progress.Index
	if index <= 0 {
		index = 1
	}
	if index > total {
		index = total
	}
	projectName := strings.TrimSpace(progress.ProjectName)
	if projectName == "" {
		projectName = "当前项目"
	}

	base := 10
	span := 80
	step := span / total
	if step < 1 {
		step = 1
	}
	start := base + (index-1)*step
	stageOffset := 0
	stageText := "准备生成"
	switch progress.Stage {
	case "generating":
		stageOffset = maxInt(1, step/5)
		stageText = "正在生成"
	case "writing":
		stageOffset = maxInt(1, step*4/5)
		stageText = "正在写入"
	case "done":
		stageOffset = step
		stageText = "已生成"
	case "error":
		stageOffset = step
		stageText = "生成失败"
	}
	value := start + stageOffset
	if value > 95 {
		value = 95
	}
	if progress.Stage == "done" && index == total {
		value = 95
	}
	return value, fmt.Sprintf("%s第 %d/%d 个：%s", stageText, index, total, projectName)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *JobService) executeCustomPromptTaskCreate(
	ctx context.Context,
	jobID string,
	req SubmitJobRequest,
) (jobExecutionResult, error) {
	if s.taskSvc == nil {
		return jobExecutionResult{}, errors.New("任务服务未初始化")
	}

	var taskReq apptask.CreateTasksFromCustomPromptDocumentsRequest
	if err := json.Unmarshal([]byte(req.InputPayload), &taskReq); err != nil {
		return jobExecutionResult{}, fmt.Errorf("解析自定义提示词创建任务参数失败：%w", err)
	}
	docCount := len(taskReq.DocumentPaths)
	s.emitProgress(jobID, req.JobType, req.TaskID, "running", 10, strPtr(fmt.Sprintf("准备从 %d 个提示词文档创建任务…", docCount)), nil)
	s.emitProgress(jobID, req.JobType, req.TaskID, "running", 25, strPtr("正在复制源码并写入提示词…"), nil)

	res, err := s.taskSvc.CreateTasksFromCustomPromptDocumentsWithContext(ctx, taskReq)
	if err != nil {
		return jobExecutionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return jobExecutionResult{}, err
	}

	outputJSON, _ := json.Marshal(res)
	outputStr := string(outputJSON)
	return jobExecutionResult{
		outputPayload: &outputStr,
		finalMessage:  strPtr(fmt.Sprintf("自定义任务创建完成：成功 %d，失败 %d", res.CreatedCount-res.ErrorCount, res.ErrorCount)),
	}, nil
}

func (s *JobService) acquirePromptGenerateSlot(ctx context.Context, jobID, taskID, projectLabel string) error {
	select {
	case s.promptGenerateSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		if s.isJobCancelled(jobID) {
			return ctx.Err()
		}
		s.emitProgress(jobID, "prompt_generate", taskID, "running", 0, strPtr(fmt.Sprintf("[%s] 提示词生成排队超时", projectLabel)), nil)
		return ctx.Err()
	}
}

func (s *JobService) executeSessionSync(
	ctx context.Context,
	jobID string,
	req SubmitJobRequest,
) (jobExecutionResult, error) {
	if strings.TrimSpace(req.TaskID) == "" {
		return jobExecutionResult{}, errors.New(errs.MsgJobSessionSyncNoTask)
	}
	if s.taskSvc == nil {
		return jobExecutionResult{}, errors.New(errs.MsgJobSessionSyncNoService)
	}

	projectLabel := req.TaskID
	if task, err := s.store.GetTask(req.TaskID); err == nil && task != nil {
		projectLabel = task.ProjectName
	}

	s.emitProgress(jobID, req.JobType, req.TaskID, "running", 20, strPtr(fmt.Sprintf("[%s] 正在同步最新 Session…", projectLabel)), nil)

	type result struct {
		payload *apptask.SyncTaskSessionsResult
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		payload, err := s.taskSvc.SyncLatestTaskSessions(req.TaskID)
		ch <- result{payload: payload, err: err}
	}()

	select {
	case <-ctx.Done():
		return jobExecutionResult{}, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return jobExecutionResult{}, r.err
		}

		if r.payload != nil {
			outputJSON, _ := json.Marshal(r.payload)
			outputStr := string(outputJSON)
			if r.payload.UpdatedTargetCount == 0 {
				return jobExecutionResult{
					outputPayload: &outputStr,
					finalMessage:  strPtr(fmt.Sprintf("[%s] 未找到可同步的 Session", projectLabel)),
				}, nil
			}
			return jobExecutionResult{
				outputPayload: &outputStr,
				finalMessage: strPtr(
					fmt.Sprintf("[%s] 已同步 %d 组 Session", projectLabel, r.payload.UpdatedTargetCount),
				),
			}, nil
		}

		return jobExecutionResult{finalMessage: strPtr(fmt.Sprintf("[%s] Session 同步完成", projectLabel))}, nil
	}
}

func (s *JobService) emitProgress(id, jobType, taskID, status string, progress int, message, errMsg *string) {
	if status == "running" && s.isJobCancelled(id) {
		return
	}

	if status == "running" {
		msg := ""
		if message != nil {
			msg = *message
		}
		if s.store != nil {
			if err := s.store.UpdateBackgroundJobProgress(id, progress, msg); err != nil {
				slog.Error("failed to update job progress", "job_id", id, "error", err)
			}
		}
	}

	app := application.Get()
	if app == nil {
		return
	}
	var taskIDPtr *string
	if taskID != "" {
		taskIDPtr = &taskID
	}
	app.Event.Emit("job:progress", JobProgressEvent{
		ID:              id,
		JobType:         jobType,
		TaskID:          taskIDPtr,
		Status:          status,
		Progress:        progress,
		ProgressMessage: message,
		ErrorMessage:    errMsg,
	})
}

func (s *JobService) isJobCancelled(id string) bool {
	if s.store == nil {
		return false
	}
	job, err := s.store.GetBackgroundJob(id)
	if err != nil || job == nil {
		return false
	}
	return job.Status == "cancelled"
}

// GitClonePayload 描述一次 git_clone 任务的参数。
type GitClonePayload struct {
	CloneURL      string               `json:"cloneUrl"`
	SourcePath    string               `json:"sourcePath"`
	SourceModelID string               `json:"sourceModelId"`
	CopyTargets   []GitCloneCopyTarget `json:"copyTargets"`
}

type GitCloneCopyTarget struct {
	ModelID string `json:"modelId"`
	Path    string `json:"path"`
}

type GitCloneFailure struct {
	ModelID string `json:"modelId"`
	Message string `json:"message"`
}

type GitCloneResult struct {
	SourcePath       string            `json:"sourcePath"`
	SuccessfulModels []string          `json:"successfulModels"`
	FailedModels     []GitCloneFailure `json:"failedModels"`
}

type QuestionBankMaterializePayload struct {
	BankSourcePath   string               `json:"bankSourcePath"`
	TargetSourcePath string               `json:"targetSourcePath"`
	SourceModelID    string               `json:"sourceModelId"`
	CopyTargets      []GitCloneCopyTarget `json:"copyTargets"`
}

func (s *JobService) executeGitClone(
	ctx context.Context,
	jobID string,
	req SubmitJobRequest,
) (jobExecutionResult, error) {
	start := time.Now()

	var payload GitClonePayload
	if err := json.Unmarshal([]byte(req.InputPayload), &payload); err != nil {
		return jobExecutionResult{}, fmt.Errorf(errs.FmtJobParseGitCloneParam, err)
	}

	sourceModelID := payload.SourceModelID
	if sourceModelID == "" {
		sourceModelID = "ORIGIN"
	}

	slog.Info("git clone started",
		"clone_url", payload.CloneURL,
		"source_model", sourceModelID,
		"copy_targets", len(payload.CopyTargets),
	)
	if err := s.ensureGitCloneTargetsAvailable(payload); err != nil {
		return jobExecutionResult{}, err
	}

	s.emitProgress(jobID, req.JobType, req.TaskID, "running", 5, strPtr("等待空闲拉取槽位…"), nil)
	release, err := s.acquireGitCloneSlot(ctx)
	if err != nil {
		return jobExecutionResult{}, err
	}
	defer release()

	var lastErr error
	for attempt := 1; attempt <= gitCloneRetryAttempts; attempt++ {
		result, err := s.executeGitCloneAttempt(ctx, jobID, req, payload, sourceModelID, attempt, start)
		if err == nil {
			return result, nil
		}
		if contextErr := gitCloneContextErr(ctx); contextErr != nil {
			return jobExecutionResult{}, cleanupGitCloneTargetsAfterAbort(payload, contextErr)
		}
		lastErr = err
		if attempt == gitCloneRetryAttempts {
			break
		}

		retryMsg := fmt.Sprintf(
			"第 %d/%d 次拉取失败：%s；%s后重试",
			attempt,
			gitCloneRetryAttempts,
			summarizeGitCloneError(err),
			gitCloneRetryBackoff,
		)
		s.emitProgress(jobID, req.JobType, req.TaskID, "running", 12, &retryMsg, nil)
		if cleanupErr := cleanupGitCloneTargets(payload); cleanupErr != nil {
			return jobExecutionResult{}, fmt.Errorf(errs.FmtJobCleanRetryDirFail, cleanupErr)
		}
		select {
		case <-ctx.Done():
			return jobExecutionResult{}, gitCloneContextErr(ctx)
		case <-time.After(gitCloneRetryBackoff):
		}
	}

	if lastErr == nil {
		lastErr = errors.New(errs.MsgGitCloneFailed)
	}
	if cleanupErr := cleanupGitCloneTargets(payload); cleanupErr != nil {
		lastErr = errors.Join(lastErr, fmt.Errorf(errs.FmtJobCleanFailedDirFail, cleanupErr))
	}
	return jobExecutionResult{}, fmt.Errorf(errs.FmtJobGitCloneRetriesFailed, gitCloneRetryAttempts, lastErr)
}

func (s *JobService) executeQuestionBankMaterialize(
	ctx context.Context,
	jobID string,
	req SubmitJobRequest,
) (jobExecutionResult, error) {
	var payload QuestionBankMaterializePayload
	if err := json.Unmarshal([]byte(req.InputPayload), &payload); err != nil {
		return jobExecutionResult{}, fmt.Errorf(errs.FmtJobParseGitCloneParam, err)
	}

	sourceModelID := payload.SourceModelID
	if sourceModelID == "" {
		sourceModelID = "ORIGIN"
	}

	targetPaths := targetPathsFromSourceAndCopies(payload.TargetSourcePath, payload.CopyTargets)
	if err := s.ensureTargetPathsAvailable(targetPaths); err != nil {
		return jobExecutionResult{}, err
	}

	s.emitProgress(jobID, req.JobType, req.TaskID, "running", 5, strPtr("等待空闲复制槽位…"), nil)
	release, err := s.acquireGitCloneSlot(ctx)
	if err != nil {
		return jobExecutionResult{}, err
	}
	defer release()

	s.emitProgress(jobID, req.JobType, req.TaskID, "running", 20, strPtr("正在从 question_bank 复制源码…"), nil)
	if err := s.gitSvc.CopyProjectDirectory(ctx, payload.BankSourcePath, payload.TargetSourcePath); err != nil {
		if cleanupErr := cleanupTargetPaths(targetPaths); cleanupErr != nil {
			return jobExecutionResult{}, errors.Join(err, fmt.Errorf(errs.FmtJobCleanFailedDirFail, cleanupErr))
		}
		return jobExecutionResult{}, err
	}

	resultPayload := GitCloneResult{
		SourcePath:       payload.TargetSourcePath,
		SuccessfulModels: []string{sourceModelID},
		FailedModels:     make([]GitCloneFailure, 0),
	}

	total := len(payload.CopyTargets)
	for i, target := range payload.CopyTargets {
		if contextErr := gitCloneContextErr(ctx); contextErr != nil {
			return jobExecutionResult{}, cleanupTargetPathsAfterAbort(targetPaths, contextErr)
		}
		progress := 55 + (i+1)*40/(total+1)
		s.emitProgress(
			jobID,
			req.JobType,
			req.TaskID,
			"running",
			progress,
			strPtr(fmt.Sprintf("正在复制到 %s（%d/%d）…", target.ModelID, i+1, total)),
			nil,
		)
		if err := s.gitSvc.CopyProjectDirectory(ctx, payload.TargetSourcePath, target.Path); err != nil {
			resultPayload.FailedModels = append(resultPayload.FailedModels, GitCloneFailure{
				ModelID: target.ModelID,
				Message: err.Error(),
			})
			continue
		}
		resultPayload.SuccessfulModels = append(resultPayload.SuccessfulModels, target.ModelID)
	}

	outputJSON, _ := json.Marshal(resultPayload)
	outputStr := string(outputJSON)
	totalModels := len(payload.CopyTargets) + 1
	if len(resultPayload.FailedModels) > 0 {
		return jobExecutionResult{
			outputPayload: &outputStr,
			finalMessage: strPtr(
				fmt.Sprintf("部分完成：成功 %d/%d", len(resultPayload.SuccessfulModels), totalModels),
			),
		}, nil
	}
	return jobExecutionResult{
		outputPayload: &outputStr,
		finalMessage:  strPtr(fmt.Sprintf("题库复制完成：共 %d 个副本", totalModels)),
	}, nil
}

// PrSubmitPayload 描述一次 pr_submit 任务的参数，与 appsubmit.SubmitAllRequest 对应。
type PrSubmitPayload struct {
	GitHubAccountID string   `json:"githubAccountId"`
	TaskID          string   `json:"taskId"`
	Models          []string `json:"models"`
	TargetRepo      string   `json:"targetRepo"`
	SourceModelName string   `json:"sourceModelName"`
	GitHubUsername  string   `json:"githubUsername"`
	GitHubToken     string   `json:"githubToken"`
}

func (s *JobService) executePrSubmit(
	ctx context.Context,
	jobID string,
	req SubmitJobRequest,
) (jobExecutionResult, error) {
	start := time.Now()

	var payload PrSubmitPayload
	if err := json.Unmarshal([]byte(req.InputPayload), &payload); err != nil {
		return jobExecutionResult{}, fmt.Errorf(errs.FmtJobParsePrSubmitParam, err)
	}

	slog.Info("pr submit started",
		"task_id", payload.TaskID,
		"target_repo", payload.TargetRepo,
		"models", payload.Models,
	)
	s.emitProgress(jobID, req.JobType, req.TaskID, "running", 20, strPtr("正在上传源码到 GitHub…"), nil)

	type result struct {
		res *appsubmit.SubmitAllResult
		err error
	}
	ch := make(chan result, 1)
	go func() {
		res, err := s.submitSvc.SubmitAll(appsubmit.SubmitAllRequest{
			GitHubAccountID: payload.GitHubAccountID,
			TaskID:          payload.TaskID,
			Models:          payload.Models,
			TargetRepo:      payload.TargetRepo,
			SourceModelName: payload.SourceModelName,
			GitHubUsername:  payload.GitHubUsername,
			GitHubToken:     payload.GitHubToken,
		})
		ch <- result{res, err}
	}()

	select {
	case <-ctx.Done():
		return jobExecutionResult{}, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			slog.Error("pr submit failed",
				"task_id", payload.TaskID,
				"target_repo", payload.TargetRepo,
				"error", r.err,
				"elapsed", time.Since(start).Round(time.Millisecond),
			)
			return jobExecutionResult{}, r.err
		}
		if r.res != nil && r.res.RepoError != "" {
			err := fmt.Errorf(errs.FmtJobSourceUploadFail, r.res.RepoError)
			slog.Error("pr submit failed",
				"task_id", payload.TaskID,
				"target_repo", payload.TargetRepo,
				"error", err,
				"elapsed", time.Since(start).Round(time.Millisecond),
			)
			return jobExecutionResult{}, err
		}
		s.emitProgress(jobID, req.JobType, req.TaskID, "running", 80, strPtr("源码已上传，正在创建模型 PR…"), nil)

		slog.Info("pr submit completed",
			"task_id", payload.TaskID,
			"target_repo", payload.TargetRepo,
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
		if r.res != nil {
			outputJSON, _ := json.Marshal(r.res)
			outputStr := string(outputJSON)
			return jobExecutionResult{
				outputPayload: &outputStr,
				finalMessage:  strPtr("PR 提交完成"),
			}, nil
		}
		return jobExecutionResult{finalMessage: strPtr("PR 提交完成")}, nil
	}
}

// AiReviewPayload 描述一次 ai_review 任务的参数。
type AiReviewPayload struct {
	ReviewRoundID      *string                `json:"reviewRoundId,omitempty"`
	ModelRunID         *string                `json:"modelRunId"`
	ModelName          string                 `json:"modelName"`
	LocalPath          string                 `json:"localPath"`
	NextPromptOverride string                 `json:"nextPromptOverride,omitempty"`
	RoundSnapshot      *AiReviewRoundSnapshot `json:"roundSnapshot,omitempty"`

	// Deprecated: 兼容旧版前端，映射到 ReviewRoundID
	ReviewNodeID *string `json:"reviewNodeId,omitempty"`
}

type AiReviewRoundSnapshot struct {
	Status      string `json:"status"`
	RoundNumber int    `json:"roundNumber"`
}

// AiReviewResult 记录一次 ai_review 任务的输出。
type AiReviewResult struct {
	ReviewRoundID          string `json:"reviewRoundId"`
	ModelRunID             string `json:"modelRunId"`
	ModelName              string `json:"modelName"`
	PromptDifficulty       string `json:"promptDifficulty"`
	ReviewStatus           string `json:"reviewStatus"`
	ReviewRound            int    `json:"reviewRound"`
	ReviewNotes            string `json:"reviewNotes"`
	DissatisfactionSummary string `json:"dissatisfactionSummary"`
	NextPrompt             string `json:"nextPrompt"`
	NextPromptTaskType     string `json:"nextPromptTaskType"`
	IsCompleted            bool   `json:"isCompleted"`
	IsSatisfied            bool   `json:"isSatisfied"`
	ProjectType            string `json:"projectType"`
	ChangeScope            string `json:"changeScope"`
	KeyLocations           string `json:"keyLocations"`
}

func (s *JobService) executeAiReview(
	ctx context.Context,
	jobID string,
	req SubmitJobRequest,
) (jobExecutionResult, error) {
	var payload AiReviewPayload
	if err := json.Unmarshal([]byte(req.InputPayload), &payload); err != nil {
		return jobExecutionResult{}, fmt.Errorf("%s：%w", errs.MsgJobAiReviewParseFail, err)
	}
	payload, err := s.prepareAiReviewPayload(req.TaskID, payload)
	if err != nil {
		return jobExecutionResult{}, err
	}
	if s.cliSvc == nil {
		return jobExecutionResult{}, errors.New(errs.MsgJobCliUninitialized)
	}
	if payload.ReviewRoundID == nil || strings.TrimSpace(*payload.ReviewRoundID) == "" {
		return jobExecutionResult{}, errors.New(errs.MsgJobAiReviewNoRound)
	}
	reviewExecution, err := s.reviewExecutionConfig()
	if err != nil {
		return jobExecutionResult{}, err
	}

	round, err := s.store.GetAiReviewRound(strings.TrimSpace(*payload.ReviewRoundID))
	if err != nil {
		return jobExecutionResult{}, fmt.Errorf(errs.FmtJobReadReviewRound, err)
	}
	if round == nil {
		return jobExecutionResult{}, fmt.Errorf(errs.FmtJobReviewRoundNotFound, strings.TrimSpace(*payload.ReviewRoundID))
	}

	modelRunID := ""
	if round.ModelRunID != nil {
		modelRunID = strings.TrimSpace(*round.ModelRunID)
	}

	label := normalizeAiReviewRoundLabel(*round)
	roundNumber := round.RoundNumber

	// 标记为 running
	if err := s.store.UpdateAiReviewRoundStatus(round.ID, "running", strPtr(jobID)); err != nil {
		return jobExecutionResult{}, fmt.Errorf(errs.FmtJobUpdateReviewRoundFail, err)
	}
	if modelRunID != "" {
		if err := s.syncModelRunAiReviewSummaryFromRounds(modelRunID); err != nil {
			slog.Error("failed to sync model run review summary", "model_run_id", modelRunID, "error", err)
		}
	}

	attemptLabel := fmt.Sprintf("第 %d 轮", roundNumber)
	slog.Info("ai review round started",
		"job_id", jobID,
		"review_round_id", round.ID,
		"review_label", label,
		"round_number", roundNumber,
		"review_engine", reviewExecution.Label,
	)
	s.emitProgress(jobID, req.JobType, req.TaskID, "running", 10,
		strPtr(fmt.Sprintf("[%s] 复核%s…", label, attemptLabel)),
		nil,
	)

	type reviewOut struct {
		result *appcli.CodexReviewResult
		err    error
	}
	ch := make(chan reviewOut, 1)

	var reviewCommit *store.CodePushRecord
	if modelRunID != "" {
		record, err := s.store.FindCodePushRecordForReview(req.TaskID, modelRunID, roundNumber-1)
		if err != nil {
			slog.Warn("failed to load code push record for ai review",
				"task_id", req.TaskID,
				"model_run_id", modelRunID,
				"round_number", roundNumber,
				"error", err,
			)
		} else {
			reviewCommit = record
		}
	}

	go func() {
		commitSHA := ""
		commitURL := ""
		repoURL := ""
		if reviewCommit != nil {
			commitSHA = strings.TrimSpace(reviewCommit.CommitSHA)
			commitURL = strings.TrimSpace(reviewCommit.CommitURL)
			repoURL = strings.TrimSpace(reviewCommit.RepoURL)
		}
		res, err := s.cliSvc.RunCodexReview(ctx, appcli.CodexReviewRequest{
			LocalPath:         payload.LocalPath,
			TaskID:            req.TaskID,
			ModelRunID:        modelRunID,
			ReviewRound:       roundNumber,
			CommitSHA:         commitSHA,
			CommitURL:         commitURL,
			RepoURL:           repoURL,
			OriginalPrompt:    strings.TrimSpace(round.OriginalPrompt),
			CurrentPrompt:     strings.TrimSpace(round.PromptText),
			ParentReviewNotes: "", // 线性模型不再有父节点
			IssueType:         "",
			IssueTitle:        "",
			ModelName:         strings.TrimSpace(round.ModelName),
			DeepSeek:          reviewExecution.DeepSeek,
		}, func(line string) {
			if isStructuredAiReviewLine(line) {
				return
			}
			s.emitProgress(jobID, req.JobType, req.TaskID, "running", 30,
				strPtr(fmt.Sprintf("[%s] %s", label, line)),
				nil,
			)
		})
		ch <- reviewOut{res, err}
	}()

	var lastResult *appcli.CodexReviewResult
	select {
	case <-ctx.Done():
		return jobExecutionResult{}, ctx.Err()
	case out := <-ch:
		if out.err != nil {
			slog.Error("ai review round failed",
				"job_id", jobID,
				"review_round_id", round.ID,
				"round_number", roundNumber,
				"error", out.err,
			)
			reviewNotes := formatAiReviewExecutionFailure(out.err)
			if err := s.store.FinalizeAiReviewRound(round.ID, "warning", boolPtr(false), boolPtr(false), reviewNotes, "", "", "", "", "", ""); err != nil {
				slog.Error("failed to persist ai review round error state", "review_round_id", round.ID, "error", err)
			}
			if modelRunID != "" {
				if err := s.syncModelRunAiReviewSummaryFromRounds(modelRunID); err != nil {
					slog.Error("failed to sync model run review summary", "model_run_id", modelRunID, "error", err)
				}
			}
			return jobExecutionResult{}, out.err
		}
		lastResult = out.result
	}

	passed := lastResult.IsCompleted && lastResult.IsSatisfied
	slog.Info("ai review round completed",
		"job_id", jobID,
		"review_round_id", round.ID,
		"round_number", roundNumber,
		"is_completed", lastResult.IsCompleted,
		"is_satisfied", lastResult.IsSatisfied,
		"passed", passed,
	)

	finalStatus := "warning"
	if passed {
		finalStatus = "pass"
	}
	finalIsCompleted := lastResult.IsCompleted
	if !passed {
		finalIsCompleted = false
	}

	nextPromptTaskType := resolveNextPromptTaskType(lastResult.NextPrompt, lastResult.NextPromptTaskType)
	nextPrompt := ensureBugFixRepairPromptPrefix(lastResult.NextPrompt, nextPromptTaskType)

	dissatisfactionSummary := ""

	if err := s.store.FinalizeAiReviewRound(
		round.ID,
		finalStatus,
		boolPtr(finalIsCompleted),
		boolPtr(lastResult.IsSatisfied),
		strings.TrimSpace(lastResult.ReviewNotes),
		dissatisfactionSummary,
		nextPrompt,
		nextPromptTaskType,
		strings.TrimSpace(lastResult.ProjectType),
		strings.TrimSpace(lastResult.ChangeScope),
		strings.TrimSpace(lastResult.KeyLocations),
	); err != nil {
		return jobExecutionResult{}, fmt.Errorf(errs.FmtJobSaveReviewResultFail, err)
	}
	if modelRunID != "" {
		if err := s.syncModelRunAiReviewSummaryFromRounds(modelRunID); err != nil {
			slog.Error("failed to sync model run review summary", "model_run_id", modelRunID, "error", err)
		}
	}

	// Sync projectType/changeScope from AI review to task level when task fields are empty.
	if req.TaskID != "" {
		aiPT := strings.TrimSpace(lastResult.ProjectType)
		aiCS := strings.TrimSpace(lastResult.ChangeScope)
		if aiPT != "" || aiCS != "" {
			if task, err := s.store.GetTask(req.TaskID); err == nil && task != nil {
				needSync := false
				pt := task.ProjectType
				cs := task.ChangeScope
				if pt == "" && aiPT != "" {
					pt = aiPT
					needSync = true
				}
				if cs == "" && aiCS != "" {
					cs = aiCS
					needSync = true
				}
				if needSync {
					if err := s.store.UpdateTaskReportFields(req.TaskID, pt, cs); err != nil {
						slog.Error("failed to sync ai review fields to task", "task_id", req.TaskID, "error", err)
					}
				}
			}
		}
	}

	result := AiReviewResult{
		ReviewRoundID:          round.ID,
		ModelRunID:             modelRunID,
		ModelName:              payload.ModelName,
		PromptDifficulty:       strings.TrimSpace(round.PromptDifficulty),
		ReviewStatus:           finalStatus,
		ReviewRound:            roundNumber,
		ReviewNotes:            strings.TrimSpace(lastResult.ReviewNotes),
		DissatisfactionSummary: dissatisfactionSummary,
		NextPrompt:             nextPrompt,
		NextPromptTaskType:     nextPromptTaskType,
		IsCompleted:            finalIsCompleted,
		IsSatisfied:            lastResult.IsSatisfied,
		ProjectType:            strings.TrimSpace(lastResult.ProjectType),
		ChangeScope:            strings.TrimSpace(lastResult.ChangeScope),
		KeyLocations:           strings.TrimSpace(lastResult.KeyLocations),
	}
	outputJSON, _ := json.Marshal(result)
	outputStr := string(outputJSON)
	return jobExecutionResult{
		outputPayload: &outputStr,
		finalMessage:  strPtr(fmt.Sprintf("[%s] 复核%s（第 %d 轮）", label, ternaryAiReviewResultText(passed), roundNumber)),
	}, nil
}

func formatAiReviewExecutionFailure(err error) string {
	msg := strings.TrimSpace(fmt.Sprint(err))
	if msg == "" {
		return "复审执行失败"
	}
	return "复审执行失败：" + msg
}

func (s *JobService) prepareAiReviewPayload(taskID string, payload AiReviewPayload) (AiReviewPayload, error) {
	payload.ModelName = strings.TrimSpace(payload.ModelName)
	payload.LocalPath = normalizeAiReviewPath(payload.LocalPath)
	if payload.ModelRunID != nil {
		trimmed := strings.TrimSpace(*payload.ModelRunID)
		if trimmed == "" {
			payload.ModelRunID = nil
		} else {
			payload.ModelRunID = &trimmed
		}
	}
	if payload.ModelRunID == nil {
		modelRunID, err := s.resolveAiReviewModelRunID(taskID, payload.LocalPath)
		if err != nil {
			return AiReviewPayload{}, err
		}
		payload.ModelRunID = modelRunID
	}
	// 兼容旧版前端: reviewNodeId → reviewRoundId
	if payload.ReviewRoundID == nil && payload.ReviewNodeID != nil {
		payload.ReviewRoundID = payload.ReviewNodeID
	}
	if payload.ReviewRoundID != nil {
		trimmed := strings.TrimSpace(*payload.ReviewRoundID)
		if trimmed == "" {
			payload.ReviewRoundID = nil
		} else {
			payload.ReviewRoundID = &trimmed
		}
	}
	if payload.ReviewRoundID == nil {
		if payload.LocalPath == "" {
			return AiReviewPayload{}, errors.New(errs.MsgJobAiReviewNoLocalPath)
		}
		if _, err := s.requireCommittedCodeForAiReviewTarget(taskID, payload.ModelRunID, payload.LocalPath); err != nil {
			return AiReviewPayload{}, err
		}
	}

	round, err := s.ensureAiReviewRound(taskID, payload)
	if err != nil {
		return AiReviewPayload{}, err
	}
	if _, err := s.requireCommittedCodeForAiReviewRound(taskID, round); err != nil {
		return AiReviewPayload{}, err
	}

	payload.ReviewRoundID = &round.ID
	payload.ModelName = strings.TrimSpace(round.ModelName)
	payload.LocalPath = normalizeAiReviewPath(round.LocalPath)
	if round.ModelRunID != nil {
		modelRunID := strings.TrimSpace(*round.ModelRunID)
		payload.ModelRunID = &modelRunID
	} else {
		payload.ModelRunID = nil
	}
	snapshot := AiReviewRoundSnapshot{
		Status:      round.Status,
		RoundNumber: round.RoundNumber,
	}
	payload.RoundSnapshot = &snapshot
	return payload, nil
}

func (s *JobService) resolveAiReviewModelRunID(taskID, localPath string) (*string, error) {
	normalizedTaskID := strings.TrimSpace(taskID)
	normalizedPath := normalizeAiReviewPath(localPath)
	if normalizedTaskID == "" || normalizedPath == "" {
		return nil, nil
	}

	runs, err := s.store.ListModelRuns(normalizedTaskID)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if run.LocalPath == nil {
			continue
		}
		if normalizeAiReviewPath(*run.LocalPath) != normalizedPath {
			continue
		}
		modelRunID := strings.TrimSpace(run.ID)
		if modelRunID == "" {
			return nil, nil
		}
		return &modelRunID, nil
	}
	return nil, nil
}

func (s *JobService) requireCommittedCodeForAiReviewTarget(taskID string, modelRunID *string, localPath string) (*store.CodePushRecord, error) {
	roundNumber, err := s.store.GetNextRoundNumber(modelRunID, localPath)
	if err != nil {
		return nil, fmt.Errorf(errs.FmtJobNextRoundFail, err)
	}
	return s.findCommittedCodeForAiReview(taskID, modelRunID, roundNumber)
}

func (s *JobService) requireCommittedCodeForAiReviewRound(taskID string, round *store.AiReviewRound) (*store.CodePushRecord, error) {
	if round == nil {
		return nil, errors.New(errs.MsgJobAiReviewNoRound)
	}
	return s.findCommittedCodeForAiReview(taskID, round.ModelRunID, round.RoundNumber)
}

func (s *JobService) findCommittedCodeForAiReview(taskID string, modelRunID *string, roundNumber int) (*store.CodePushRecord, error) {
	if modelRunID == nil || strings.TrimSpace(*modelRunID) == "" {
		return nil, errors.New(msgAiReviewCommitRequired)
	}
	normalizedTaskID := strings.TrimSpace(taskID)
	if normalizedTaskID == "" {
		return nil, errors.New(errs.MsgJobAiReviewNoTask)
	}
	normalizedModelRunID := strings.TrimSpace(*modelRunID)
	record, err := s.store.FindCodePushRecordForReview(normalizedTaskID, normalizedModelRunID, roundNumber-1)
	if err != nil {
		return nil, fmt.Errorf("读取代码提交记录失败：%w", err)
	}
	if record == nil || strings.TrimSpace(record.CommitSHA) == "" {
		return nil, errors.New(msgAiReviewCommitRequired)
	}
	return record, nil
}

func parseAiReviewPayloadForDedup(raw string) (AiReviewPayload, bool) {
	var payload AiReviewPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return AiReviewPayload{}, false
	}

	payload.ModelName = strings.TrimSpace(payload.ModelName)
	payload.LocalPath = normalizeAiReviewPath(payload.LocalPath)
	// 兼容旧版
	if payload.ReviewRoundID == nil && payload.ReviewNodeID != nil {
		payload.ReviewRoundID = payload.ReviewNodeID
	}
	if payload.ReviewRoundID != nil {
		trimmed := strings.TrimSpace(*payload.ReviewRoundID)
		if trimmed == "" {
			payload.ReviewRoundID = nil
		} else {
			payload.ReviewRoundID = &trimmed
		}
	}
	if payload.ModelRunID != nil {
		trimmed := strings.TrimSpace(*payload.ModelRunID)
		if trimmed == "" {
			payload.ModelRunID = nil
		} else {
			payload.ModelRunID = &trimmed
		}
	}

	return payload, true
}

func buildAiReviewTargetKey(reviewRoundID, modelRunID *string, localPath string) string {
	if reviewRoundID != nil {
		if trimmed := strings.TrimSpace(*reviewRoundID); trimmed != "" {
			return "round:" + trimmed
		}
	}

	if modelRunID != nil {
		if trimmed := strings.TrimSpace(*modelRunID); trimmed != "" {
			return "run:" + trimmed
		}
	}

	if normalizedPath := normalizeAiReviewPath(localPath); normalizedPath != "" {
		return "path:" + normalizedPath
	}

	return ""
}

func aiReviewTargetKeys(payload AiReviewPayload) []string {
	keys := make([]string, 0, 3)
	if key := buildAiReviewTargetKey(payload.ReviewRoundID, nil, ""); key != "" {
		keys = append(keys, key)
	}
	if key := buildAiReviewTargetKey(nil, payload.ModelRunID, ""); key != "" {
		keys = append(keys, key)
	}
	if key := buildAiReviewTargetKey(nil, nil, payload.LocalPath); key != "" {
		keys = append(keys, key)
	}
	return keys
}

func sameAiReviewTarget(left, right AiReviewPayload) bool {
	leftKeys := aiReviewTargetKeys(left)
	rightKeys := aiReviewTargetKeys(right)
	if len(leftKeys) == 0 || len(rightKeys) == 0 {
		return false
	}

	rightSet := make(map[string]struct{}, len(rightKeys))
	for _, key := range rightKeys {
		rightSet[key] = struct{}{}
	}
	for _, key := range leftKeys {
		if _, ok := rightSet[key]; ok {
			return true
		}
	}
	return false
}

func (s *JobService) ensureAiReviewRound(taskID string, payload AiReviewPayload) (*store.AiReviewRound, error) {
	normalizedTaskID := strings.TrimSpace(taskID)
	if normalizedTaskID == "" {
		return nil, errors.New(errs.MsgJobAiReviewNoTask)
	}

	// 如果指定了具体的 round ID，直接返回
	if payload.ReviewRoundID != nil && strings.TrimSpace(*payload.ReviewRoundID) != "" {
		round, err := s.store.GetAiReviewRound(strings.TrimSpace(*payload.ReviewRoundID))
		if err != nil {
			return nil, fmt.Errorf(errs.FmtJobReadReviewRound, err)
		}
		if round == nil {
			return nil, fmt.Errorf(errs.FmtJobReviewRoundNotFound, strings.TrimSpace(*payload.ReviewRoundID))
		}
		return round, nil
	}

	if payload.LocalPath == "" {
		return nil, errors.New(errs.MsgJobAiReviewNoLocalPath)
	}

	// 获取任务的原始提示词
	task, err := s.store.GetTask(normalizedTaskID)
	if err != nil {
		return nil, fmt.Errorf("读取任务失败：%w", err)
	}
	originalPrompt := ""
	promptDifficulty := store.DefaultPromptDifficulty
	if task != nil && task.PromptText != nil {
		originalPrompt = strings.TrimSpace(*task.PromptText)
	}
	if task != nil {
		promptDifficulty = strings.TrimSpace(task.PromptDifficulty)
	}

	// 确定 round_number 和本轮使用的提示词
	nextRound, err := s.store.GetNextRoundNumber(payload.ModelRunID, payload.LocalPath)
	if err != nil {
		return nil, fmt.Errorf(errs.FmtJobNextRoundFail, err)
	}

	promptText := originalPrompt
	if strings.TrimSpace(payload.NextPromptOverride) != "" {
		promptText = strings.TrimSpace(payload.NextPromptOverride)
	} else if nextRound > 1 {
		// 后续轮次：使用上一轮的 next_prompt，如果没有则用原始提示词
		prev, err := s.store.GetLatestAiReviewRound(payload.ModelRunID, payload.LocalPath)
		if err != nil {
			return nil, fmt.Errorf(errs.FmtJobReadPrevReviewFail, err)
		}
		if prev != nil {
			suggested := strings.TrimSpace(prev.NextPrompt)
			if suggested != "" && suggested != "无" {
				promptText = suggested
			}
		}
	}

	roundID := uuid.New().String()
	now := time.Now().Unix()
	round := store.AiReviewRound{
		ID:               roundID,
		TaskID:           normalizedTaskID,
		ModelRunID:       payload.ModelRunID,
		LocalPath:        payload.LocalPath,
		ModelName:        firstNonEmpty(strings.TrimSpace(payload.ModelName), filepath.Base(payload.LocalPath)),
		RoundNumber:      nextRound,
		OriginalPrompt:   originalPrompt,
		PromptText:       promptText,
		PromptDifficulty: promptDifficulty,
		Status:           "none",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.store.CreateAiReviewRound(round); err != nil {
		return nil, fmt.Errorf(errs.FmtJobCreateReviewRoundFail, err)
	}
	return s.store.GetAiReviewRound(roundID)
}

func (s *JobService) syncModelRunAiReviewSummaryFromRounds(modelRunID string) error {
	rounds, err := s.store.ListAiReviewRoundsByModelRun(modelRunID)
	if err != nil {
		return err
	}
	status, round, notes := store.SummarizeAiReviewRounds(rounds)
	return s.store.UpdateModelRunReview(modelRunID, status, round, notes)
}

func normalizeAiReviewRoundLabel(round store.AiReviewRound) string {
	modelName := strings.TrimSpace(round.ModelName)
	if modelName != "" {
		return modelName
	}
	if pathBase := filepath.Base(strings.TrimSpace(round.LocalPath)); pathBase != "" && pathBase != "." {
		return pathBase
	}
	return round.ID
}

func resolveNextPromptTaskType(nextPrompt, explicitTaskType string) string {
	if normalized := normalizeReviewTaskType(explicitTaskType); normalized != "" {
		return normalized
	}
	return inferReviewTaskTypeFromPrompt(nextPrompt)
}

func ensureBugFixRepairPromptPrefix(nextPrompt, nextPromptTaskType string) string {
	trimmed := strings.TrimSpace(nextPrompt)
	if trimmed == "" || trimmed == "无" {
		return trimmed
	}
	if normalizeReviewTaskType(nextPromptTaskType) != "Bug修复" {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "修复") {
		return trimmed
	}
	return "修复" + strings.TrimLeft(trimmed, " ：:，,。.")
}

func inferReviewTaskTypeFromPrompt(prompt string) string {
	text := strings.ToLower(strings.TrimSpace(prompt))
	if text == "" || text == "无" {
		return "未归类"
	}
	switch {
	case strings.Contains(text, "测试") || strings.Contains(text, "用例") || strings.Contains(text, "覆盖率") || strings.Contains(text, "断言"):
		return "代码测试"
	case strings.Contains(text, "重构") || strings.Contains(text, "拆分") || strings.Contains(text, "抽取") || strings.Contains(text, "简化结构"):
		return "代码重构"
	case strings.Contains(text, "工程化") || strings.Contains(text, "构建") || strings.Contains(text, "脚手架") || strings.Contains(text, "ci") || strings.Contains(text, "配置"):
		return "工程化"
	case strings.Contains(text, "理解") || strings.Contains(text, "说明") || strings.Contains(text, "梳理") || strings.Contains(text, "文档"):
		return "代码理解"
	case strings.Contains(text, "从零") || strings.Contains(text, "0-1") || strings.Contains(text, "全新") || strings.Contains(text, "完整"):
		return "0-1代码生成"
	case strings.Contains(text, "新增") || strings.Contains(text, "增加") || strings.Contains(text, "支持") || strings.Contains(text, "补充") || strings.Contains(text, "补齐"):
		return "Feature迭代"
	case strings.Contains(text, "修复") || strings.Contains(text, "问题") || strings.Contains(text, "错误") || strings.Contains(text, "异常") || strings.Contains(text, "不正确") || strings.Contains(text, "失败"):
		return "Bug修复"
	default:
		return "Bug修复"
	}
}

func normalizeReviewTaskType(taskType string) string {
	trimmed := strings.TrimSpace(taskType)
	if trimmed == "" {
		return ""
	}
	switch strings.ToLower(strings.ReplaceAll(trimmed, " ", "")) {
	case "bugfix", "bug修复", "缺陷修复":
		return "Bug修复"
	case "feature", "feature迭代", "功能开发":
		return "Feature迭代"
	case "代码生成", "0-1代码生成", "0-1", "0到1", "从0到1", "从零到一":
		return "0-1代码生成"
	case "代码理解":
		return "代码理解"
	case "refactor", "代码重构":
		return "代码重构"
	case "工程化":
		return "工程化"
	case "test", "测试", "测试补全", "代码测试":
		return "代码测试"
	case "未分类", "未归类", "uncategorized", "unclassified":
		return "未归类"
	default:
		return trimmed
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func ternaryAiReviewResultText(passed bool) string {
	if passed {
		return "通过"
	}
	return "未通过"
}

func normalizeAiReviewPath(localPath string) string {
	trimmed := strings.TrimSpace(localPath)
	if trimmed == "" {
		return ""
	}
	return filepath.Clean(trimmed)
}

func isStructuredAiReviewLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return false
	}

	var result appcli.CodexReviewResult
	return json.Unmarshal([]byte(trimmed), &result) == nil
}

func strPtr(s string) *string {
	return &s
}

func boolPtr(value bool) *bool {
	return &value
}

func (s *JobService) cloneSemaphore() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cloneSem == nil {
		s.cloneSem = make(chan struct{}, gitCloneConcurrencyLimit)
	}
	return s.cloneSem
}

func (s *JobService) acquireGitCloneSlot(ctx context.Context) (func(), error) {
	sem := s.cloneSemaphore()
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, gitCloneContextErr(ctx)
	}
}

func (s *JobService) executeGitCloneAttempt(
	ctx context.Context,
	jobID string,
	req SubmitJobRequest,
	payload GitClonePayload,
	sourceModelID string,
	attempt int,
	start time.Time,
) (jobExecutionResult, error) {
	attemptMessage := fmt.Sprintf("准备拉取源码（第 %d/%d 次）…", attempt, gitCloneRetryAttempts)
	s.emitProgress(jobID, req.JobType, req.TaskID, "running", 10, &attemptMessage, nil)

	cloneCtx, stopCloneWatch, heartbeat := newGitCloneProgressContext(ctx, gitCloneIdleTimeout)
	defer stopCloneWatch()

	if err := s.gitSvc.CloneConfiguredProjectWithContext(
		cloneCtx,
		payload.CloneURL,
		payload.SourcePath,
		func(msg string) {
			heartbeat()
			progressMsg := msg
			s.emitProgress(jobID, req.JobType, req.TaskID, "running", 20, &progressMsg, nil)
		},
	); err != nil {
		return jobExecutionResult{}, err
	}

	resultPayload := GitCloneResult{
		SourcePath:       payload.SourcePath,
		SuccessfulModels: []string{sourceModelID},
		FailedModels:     make([]GitCloneFailure, 0),
	}

	total := len(payload.CopyTargets)
	for i, target := range payload.CopyTargets {
		if contextErr := gitCloneContextErr(ctx); contextErr != nil {
			return jobExecutionResult{}, contextErr
		}
		progress := 50 + (i+1)*45/(total+1)
		s.emitProgress(
			jobID,
			req.JobType,
			req.TaskID,
			"running",
			progress,
			strPtr(fmt.Sprintf("正在复制到 %s（%d/%d）…", target.ModelID, i+1, total)),
			nil,
		)
		if err := s.gitSvc.CopyProjectDirectory(ctx, payload.SourcePath, target.Path); err != nil {
			resultPayload.FailedModels = append(resultPayload.FailedModels, GitCloneFailure{
				ModelID: target.ModelID,
				Message: err.Error(),
			})
			continue
		}
		resultPayload.SuccessfulModels = append(resultPayload.SuccessfulModels, target.ModelID)
	}

	outputJSON, _ := json.Marshal(resultPayload)
	outputStr := string(outputJSON)
	totalModels := len(payload.CopyTargets) + 1
	slog.Info("git clone completed",
		"clone_url", payload.CloneURL,
		"success_count", len(resultPayload.SuccessfulModels),
		"failed_count", len(resultPayload.FailedModels),
		"total", totalModels,
		"attempt", attempt,
		"elapsed", time.Since(start).Round(time.Millisecond),
	)
	if len(resultPayload.FailedModels) > 0 {
		return jobExecutionResult{
			outputPayload: &outputStr,
			finalMessage: strPtr(
				fmt.Sprintf("部分完成：成功 %d/%d", len(resultPayload.SuccessfulModels), totalModels),
			),
		}, nil
	}
	return jobExecutionResult{
		outputPayload: &outputStr,
		finalMessage:  strPtr(fmt.Sprintf("拉取完成：共 %d 个副本", totalModels)),
	}, nil
}

func newGitCloneProgressContext(
	parent context.Context,
	idleTimeout time.Duration,
) (context.Context, func(), func()) {
	ctx, cancel := context.WithCancelCause(parent)
	heartbeatCh := make(chan struct{}, 1)

	go func() {
		timer := time.NewTimer(idleTimeout)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeatCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(idleTimeout)
			case <-timer.C:
				cancel(errGitCloneIdleTimeout)
				return
			}
		}
	}()

	heartbeat := func() {
		select {
		case heartbeatCh <- struct{}{}:
		default:
		}
	}

	stop := func() {
		cancel(nil)
	}
	return ctx, stop, heartbeat
}

func (s *JobService) ensureGitCloneTargetsAvailable(payload GitClonePayload) error {
	return s.ensureTargetPathsAvailable(gitCloneTargetPaths(payload))
}

func (s *JobService) ensureTargetPathsAvailable(paths []string) error {
	if len(paths) == 0 {
		return errors.New(errs.MsgJobMissingCloneTarget)
	}
	existing := s.gitSvc.CheckPathsExist(paths)
	if len(existing) == 0 {
		return nil
	}

	names := make([]string, 0, len(existing))
	for _, path := range existing {
		names = append(names, filepath.Base(util.NormalizePath(path)))
	}
	sort.Strings(names)
	return fmt.Errorf(errs.FmtJobDirConflict, strings.Join(names, ", "))
}

func cleanupGitCloneTargets(payload GitClonePayload) error {
	return cleanupTargetPaths(gitCloneTargetPaths(payload))
}

func cleanupTargetPaths(paths []string) error {
	sort.Slice(paths, func(i, j int) bool {
		return len(paths[i]) > len(paths[j])
	})
	for _, path := range paths {
		normalized := util.NormalizePath(path)
		if !isSafeGitCloneCleanupPath(normalized) {
			return fmt.Errorf(errs.FmtJobRefuseUnsafeClean, path)
		}
		if err := os.RemoveAll(normalized); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func cleanupGitCloneTargetsAfterAbort(payload GitClonePayload, abortErr error) error {
	if err := cleanupTargetPaths(gitCloneTargetPaths(payload)); err != nil {
		slog.Error("git clone cleanup after abort failed",
			"source_path", payload.SourcePath,
			"error", err,
		)
		return errors.Join(abortErr, fmt.Errorf(errs.FmtJobCleanAbortResidualFail, err))
	}
	return abortErr
}

func gitCloneTargetPaths(payload GitClonePayload) []string {
	return targetPathsFromSourceAndCopies(payload.SourcePath, payload.CopyTargets)
}

func cleanupTargetPathsAfterAbort(paths []string, abortErr error) error {
	if err := cleanupTargetPaths(paths); err != nil {
		return errors.Join(abortErr, fmt.Errorf(errs.FmtJobCleanAbortResidualFail, err))
	}
	return abortErr
}

func targetPathsFromSourceAndCopies(sourcePath string, copyTargets []GitCloneCopyTarget) []string {
	seen := make(map[string]struct{}, len(copyTargets)+1)
	paths := make([]string, 0, len(copyTargets)+1)
	appendPath := func(path string) {
		normalized := util.NormalizePath(path)
		if normalized == "" {
			return
		}
		if _, ok := seen[normalized]; ok {
			return
		}
		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}

	appendPath(sourcePath)
	for _, target := range copyTargets {
		appendPath(target.Path)
	}
	return paths
}

func isSafeGitCloneCleanupPath(path string) bool {
	if path == "" || path == string(os.PathSeparator) || path == "." {
		return false
	}
	clean := filepath.Clean(path)
	if clean == string(os.PathSeparator) || clean == "." {
		return false
	}
	home, err := os.UserHomeDir()
	if err == nil && util.SamePath(clean, home) {
		return false
	}
	return len(strings.Split(clean, string(os.PathSeparator))) >= 4
}

func summarizeGitCloneError(err error) string {
	if err == nil {
		return "未知错误"
	}
	msg := strings.TrimSpace(err.Error())
	if len([]rune(msg)) <= 80 {
		return msg
	}
	runes := []rune(msg)
	return string(runes[:80]) + "..."
}

func gitCloneContextErr(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return ctx.Err()
}
