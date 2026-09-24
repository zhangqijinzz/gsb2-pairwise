package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	appcli "github.com/blueship581/pinru/app/cli"
	"github.com/blueship581/pinru/internal/errs"
	"github.com/blueship581/pinru/internal/llm"
	internalprompt "github.com/blueship581/pinru/internal/prompt"
	"github.com/blueship581/pinru/internal/store"
	"github.com/blueship581/pinru/internal/util"
)

// Service handles prompt generation and storage for tasks.
type PromptService struct {
	store                   *store.Store
	cliSvc                  *appcli.CliService
	promptGenerator         func(context.Context, string, string, string) (generatedPromptResult, error)
	promptHumanizer         func(context.Context, string, string, string) (string, error)
	duplicateJudge          func(context.Context, string, string, string) (semanticDuplicateDecision, error)
	requirementDocGenerator func(context.Context, string, string, string) (string, error)
}

// NewService creates a new prompt service.
func New(store *store.Store, cliSvc *appcli.CliService) *PromptService {
	return &PromptService{store: store, cliSvc: cliSvc}
}

// GeneratePromptRequest carries parameters for prompt generation.
type GeneratePromptRequest struct {
	TaskID          string   `json:"taskId"`
	ProviderID      *string  `json:"providerId"`
	TaskType        string   `json:"taskType"`
	Scopes          []string `json:"scopes"`
	Constraints     []string `json:"constraints"`
	AdditionalNotes *string  `json:"additionalNotes"`
	ThinkingBudget  string   `json:"thinkingBudget"`
}

// PromptGenerationResult is returned after a successful generation.
type PromptGenerationResult struct {
	PromptText       string `json:"promptText"`
	PromptDifficulty string `json:"promptDifficulty"`
	ProviderName     string `json:"providerName"`
	Model            string `json:"model"`
	Status           string `json:"status"`
}

type GenerateCustomProjectPromptDocumentsRequest struct {
	Counts       *internalprompt.DocumentCounts `json:"counts,omitempty"`
	ProjectID    string                         `json:"projectId"`
	ProjectNames []string                       `json:"projectNames"`
	ProviderID   *string                        `json:"providerId"`
}

type CustomProjectPromptDocumentDetail struct {
	ProjectName string `json:"projectName"`
	SourcePath  string `json:"sourcePath"`
	OutputPath  string `json:"outputPath"`
	Content     string `json:"content"`
	Status      string `json:"status"`
	Message     string `json:"message"`
}

type GenerateCustomProjectPromptDocumentsResult struct {
	ProjectID      string                              `json:"projectId"`
	RootPath       string                              `json:"rootPath"`
	ProviderName   string                              `json:"providerName"`
	Model          string                              `json:"model"`
	GeneratedCount int                                 `json:"generatedCount"`
	ErrorCount     int                                 `json:"errorCount"`
	Details        []CustomProjectPromptDocumentDetail `json:"details"`
}

type CustomProjectPromptDocumentProgress struct {
	ProjectName string
	Index       int
	Total       int
	Stage       string
}

type GenerateCustomProjectPromptDocumentsOptions struct {
	OnProgress func(CustomProjectPromptDocumentProgress)
}

type CustomProjectPromptDocumentRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

const (
	defaultPromptGenerationModel = "deepseek-v4-flash"
	deepSeekFlashModel           = "deepseek-v4-flash"
	deepSeekFlashCanonicalModel  = "deepseek-flash"
	deepSeekAPIBaseURL           = "https://api.deepseek.com"
	promptDuplicateRetryLimit    = 2
	promptQualityRetryLimit      = 2
)

func (s *PromptService) GenerateTaskPrompt(req GeneratePromptRequest) (*PromptGenerationResult, error) {
	return s.GenerateTaskPromptWithContext(context.Background(), req)
}

func (s *PromptService) TestLLMProvider(provider store.LLMProvider) (bool, error) {
	resolved, err := s.resolveProviderForTest(provider)
	if err != nil {
		return false, err
	}

	client, err := llm.BuildProvider(llm.Config{
		ID:           resolved.ID,
		Name:         resolved.Name,
		ProviderType: resolved.ProviderType,
		Model:        resolved.Model,
		BaseURL:      resolved.BaseURL,
		APIKey:       resolved.APIKey,
		IsDefault:    resolved.IsDefault,
	})
	if err != nil {
		return false, err
	}

	if err := client.TestConnection(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *PromptService) GenerateTaskPromptWithContext(ctx context.Context, req GeneratePromptRequest) (*PromptGenerationResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(req.TaskID) == "" {
		return nil, errors.New(errs.MsgTaskRequired)
	}
	if strings.TrimSpace(req.TaskType) == "" {
		return nil, errors.New(errs.MsgTaskTypeRequired)
	}

	task, err := s.store.GetTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf(errs.FmtTaskNotFound, req.TaskID)
	}
	if task.LocalPath == nil {
		return nil, errors.New(errs.MsgTaskMissingWorkDir)
	}

	if _, err := s.cliSvc.CheckCLI(); err != nil {
		return nil, errors.New(errs.MsgClaudeCodeCliNotInstalledInstallGuide)
	}

	selection, err := resolveProviderForPromptGeneration(s.store, req.ProviderID)
	if err != nil {
		return nil, err
	}

	// Every saved task prompt participates in de-duplication, including tasks
	// from other projects and earlier batches. Same-project prompts stay first
	// in the bounded generation context; the post-generation check uses all.
	allTasks, err := s.store.ListTasks(nil)
	if err != nil {
		return nil, fmt.Errorf("读取题库历史提示词失败：%w", err)
	}
	existingPrompts := collectPromptDedupSources(task, collectOtherTaskPrompts(task, allTasks))

	startedAt := time.Now().Unix()
	if err := s.store.StartTaskPromptGeneration(task.ID, startedAt); err != nil {
		return nil, err
	}

	// Build the [PINRU] skill prompt. Keep generation context small; semantic duplicate
	// checks run after generation against a broader candidate set.
	generationPrompts := promptGenerationContext(existingPrompts)
	workDir := util.NormalizePath(*task.LocalPath)
	projectProfile, err := s.resolveProjectProfile(ctx, workDir)
	if err != nil {
		slog.Warn("project profile cache unavailable, falling back to live repository reading",
			"task_id", task.ID,
			"project", task.ProjectName,
			"error", err,
		)
	}
	skillPrompt := buildSkillPrompt(req, generationPrompts, projectProfile)

	// Execute CLI Agent with one automatic retry on failure
	slog.Info("CLI prompt generation started",
		"project", task.ProjectName,
		"task_type", req.TaskType,
		"model", selection.Model,
		"provider", selection.Name,
		"profile", projectProfile.summaryForLog(),
	)
	cliStart := time.Now()
	generated, err := s.generatePromptWithRetry(ctx, workDir, skillPrompt, selection.Model, 1, selection.EnvOverrides)
	if err != nil {
		slog.Error("CLI prompt generation failed",
			"project", task.ProjectName,
			"model", selection.Model,
			"elapsed", time.Since(cliStart).Round(time.Millisecond),
			"error", err,
		)
		errMsg := normalizePromptGenerationError(err)
		if failErr := s.store.FailTaskPromptGeneration(task.ID, errMsg, startedAt); failErr != nil {
			return nil, fmt.Errorf(errs.FmtPromptStatusBack, errMsg, failErr)
		}
		return nil, errors.New(errMsg)
	}
	promptText := generated.PromptText
	modelDifficulty := generated.PromptDifficulty
	for duplicateAttempt := 0; ; duplicateAttempt++ {
		duplicate, duplicated, duplicateErr := s.findDuplicatePromptMatch(ctx, workDir, promptText, existingPrompts, selection.Model, selection.EnvOverrides)
		if duplicateErr != nil {
			errMsg := normalizePromptGenerationError(duplicateErr)
			if failErr := s.store.FailTaskPromptGeneration(task.ID, errMsg, startedAt); failErr != nil {
				return nil, fmt.Errorf(errs.FmtPromptStatusBack, errMsg, failErr)
			}
			return nil, errors.New(errMsg)
		}
		if !duplicated {
			break
		}
		slog.Warn("generated prompt duplicated existing prompt, regenerating",
			"task_id", task.ID,
			"project", task.ProjectName,
			"model", selection.Model,
			"duplicate_task_id", duplicate.TaskID,
			"duplicate_reason", duplicate.Reason,
			"duplicate_confidence", duplicate.Confidence,
		)
		if duplicateAttempt >= promptDuplicateRetryLimit {
			errMsg := fmt.Sprintf("生成的提示词与题卡 %s 雷同，已重试 %d 次仍未通过判重，结果未保存", duplicate.TaskID, promptDuplicateRetryLimit)
			if failErr := s.store.FailTaskPromptGeneration(task.ID, errMsg, startedAt); failErr != nil {
				return nil, fmt.Errorf(errs.FmtPromptStatusBack, errMsg, failErr)
			}
			return nil, errors.New(errMsg)
		}
		regeneratePrompt := buildDuplicateRegenerationPrompt(req, generationPrompts, promptText, duplicate, projectProfile)
		generated, err = s.generatePromptWithRetry(ctx, workDir, regeneratePrompt, selection.Model, 1, selection.EnvOverrides)
		if err != nil {
			slog.Error("CLI prompt regeneration failed",
				"project", task.ProjectName,
				"model", selection.Model,
				"elapsed", time.Since(cliStart).Round(time.Millisecond),
				"error", err,
			)
			errMsg := normalizePromptGenerationError(err)
			if failErr := s.store.FailTaskPromptGeneration(task.ID, errMsg, startedAt); failErr != nil {
				return nil, fmt.Errorf(errs.FmtPromptStatusBack, errMsg, failErr)
			}
			return nil, errors.New(errMsg)
		}
		promptText = generated.PromptText
		modelDifficulty = generated.PromptDifficulty
	}
	slog.Info("CLI prompt generation completed",
		"project", task.ProjectName,
		"model", selection.Model,
		"elapsed", time.Since(cliStart).Round(time.Millisecond),
	)

	rawGeneratedPrompt := strings.TrimSpace(promptText)
	promptText = s.bestEffortPolishPrompt(ctx, workDir, promptText, selection)
	if promptText != rawGeneratedPrompt {
		duplicate, duplicated, duplicateErr := s.findDuplicatePromptMatch(ctx, workDir, promptText, existingPrompts, selection.Model, selection.EnvOverrides)
		if duplicateErr != nil || duplicated {
			slog.Warn("polished prompt did not pass global duplicate check, keeping verified raw prompt",
				"task_id", task.ID,
				"project", task.ProjectName,
				"model", selection.Model,
				"duplicate_task_id", duplicate.TaskID,
				"error", duplicateErr,
			)
			promptText = rawGeneratedPrompt
		}
	}
	qualityErr := generatedPromptQualityError(promptText, modelDifficulty)
	for qualityAttempt := 0; qualityErr != nil && qualityAttempt < promptQualityRetryLimit; qualityAttempt++ {
		slog.Warn("generated prompt failed quality precheck, regenerating",
			"task_id", task.ID,
			"project", task.ProjectName,
			"model", selection.Model,
			"attempt", qualityAttempt+1,
			"reason", qualityErr,
		)
		regeneratePrompt := buildQualityRegenerationPrompt(req, generationPrompts, promptText, qualityErr, projectProfile)
		regenerated, regenerateErr := s.generatePromptWithRetry(ctx, workDir, regeneratePrompt, selection.Model, 1, selection.EnvOverrides)
		if regenerateErr != nil {
			errMsg := normalizePromptGenerationError(regenerateErr)
			if failErr := s.store.FailTaskPromptGeneration(task.ID, errMsg, startedAt); failErr != nil {
				return nil, fmt.Errorf(errs.FmtPromptStatusBack, errMsg, failErr)
			}
			return nil, errors.New(errMsg)
		}

		promptText = strings.TrimSpace(regenerated.PromptText)
		modelDifficulty = regenerated.PromptDifficulty
		qualityErr = generatedPromptQualityError(promptText, modelDifficulty)
		if qualityErr != nil {
			continue
		}
		duplicate, duplicated, duplicateErr := s.findDuplicatePromptMatch(ctx, workDir, promptText, existingPrompts, selection.Model, selection.EnvOverrides)
		if duplicateErr != nil {
			errMsg := normalizePromptGenerationError(duplicateErr)
			if failErr := s.store.FailTaskPromptGeneration(task.ID, errMsg, startedAt); failErr != nil {
				return nil, fmt.Errorf(errs.FmtPromptStatusBack, errMsg, failErr)
			}
			return nil, errors.New(errMsg)
		}
		if duplicated {
			qualityErr = fmt.Errorf("与题卡 %s 雷同：%s", duplicate.TaskID, duplicate.Reason)
		}
	}
	if qualityErr != nil {
		errMsg := "生成的提示词质量预检未通过：" + qualityErr.Error()
		if failErr := s.store.FailTaskPromptGeneration(task.ID, errMsg, startedAt); failErr != nil {
			return nil, fmt.Errorf(errs.FmtPromptStatusBack, errMsg, failErr)
		}
		return nil, errors.New(errMsg)
	}
	promptDifficulty := modelDifficulty
	if promptDifficulty == "" {
		promptDifficulty = estimatePromptDifficulty(req, promptText)
	}

	if err := s.store.CompleteTaskPromptGenerationWithDifficulty(task.ID, promptText, promptDifficulty, startedAt); err != nil {
		return nil, err
	}
	BestEffortSyncTaskPromptArtifact(task, promptText)

	return &PromptGenerationResult{
		PromptText:       promptText,
		PromptDifficulty: promptDifficulty,
		ProviderName:     selection.Name,
		Model:            selection.Model,
		Status:           "PromptReady",
	}, nil
}

func (s *PromptService) SaveTaskPrompt(taskID, promptText string) error {
	if strings.TrimSpace(taskID) == "" {
		return errors.New(errs.MsgTaskRequired)
	}
	if strings.TrimSpace(promptText) == "" {
		return errors.New(errs.MsgPromptContentRequired)
	}

	task, err := LoadTaskForPromptSync(s.store, taskID)
	if err != nil {
		return err
	}

	if err := s.store.UpdateTaskPrompt(taskID, promptText); err != nil {
		return err
	}

	BestEffortSyncTaskPromptArtifact(task, promptText)
	return nil
}

func (s *PromptService) GenerateCustomProjectPromptDocuments(req GenerateCustomProjectPromptDocumentsRequest) (*GenerateCustomProjectPromptDocumentsResult, error) {
	return s.GenerateCustomProjectPromptDocumentsWithContext(context.Background(), req)
}

func (s *PromptService) GenerateCustomProjectPromptDocumentsWithContext(ctx context.Context, req GenerateCustomProjectPromptDocumentsRequest) (*GenerateCustomProjectPromptDocumentsResult, error) {
	return s.GenerateCustomProjectPromptDocumentsWithOptions(ctx, req, nil)
}

func (s *PromptService) GenerateCustomProjectPromptDocumentsWithOptions(
	ctx context.Context,
	req GenerateCustomProjectPromptDocumentsRequest,
	options *GenerateCustomProjectPromptDocumentsOptions,
) (*GenerateCustomProjectPromptDocumentsResult, error) {
	counts := internalprompt.DefaultDocumentCounts()
	if req.Counts != nil {
		counts = *req.Counts
	}
	if err := counts.Validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectID := strings.TrimSpace(req.ProjectID)
	if projectID == "" {
		return nil, errors.New(errs.MsgProjectRequired)
	}
	projectNames := normalizeCustomProjectDocumentNames(req.ProjectNames)
	if len(projectNames) == 0 {
		return nil, errors.New("未选择任何自定义项目")
	}
	if _, err := s.cliSvc.CheckCLI(); err != nil {
		return nil, errors.New(errs.MsgClaudeCodeCliNotInstalledInstallGuide)
	}
	selection, err := resolveProviderForPromptGeneration(s.store, req.ProviderID)
	if err != nil {
		return nil, err
	}

	project, err := s.store.GetProject(projectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, fmt.Errorf(errs.FmtStoreProjectNotFound, projectID)
	}

	rootPath, err := s.store.GetConfig("custom_project_root_path")
	if err != nil {
		rootPath = ""
	}
	rootPath = util.NormalizePath(rootPath)
	if rootPath == "" {
		return nil, errors.New("请先在设置中配置自定义项目根目录")
	}
	if err := os.MkdirAll(util.ExpandTilde(rootPath), 0o755); err != nil {
		return nil, err
	}

	items, err := s.store.ListQuestionBankItems(projectID)
	if err != nil {
		return nil, err
	}
	itemByName := make(map[string]store.QuestionBankItem, len(items))
	for _, item := range items {
		if !isCustomQuestionBankItem(item) {
			continue
		}
		itemByName[strings.ToLower(strings.TrimSpace(item.DisplayName))] = item
	}

	result := &GenerateCustomProjectPromptDocumentsResult{
		ProjectID:    project.ID,
		RootPath:     rootPath,
		ProviderName: selection.Name,
		Model:        selection.Model,
		Details:      make([]CustomProjectPromptDocumentDetail, 0, len(projectNames)),
	}

	emitProgress := func(projectName string, index int, stage string) {
		if options != nil && options.OnProgress != nil {
			options.OnProgress(CustomProjectPromptDocumentProgress{
				ProjectName: projectName,
				Index:       index,
				Total:       len(projectNames),
				Stage:       stage,
			})
		}
	}

	for index, projectName := range projectNames {
		progressIndex := index + 1
		emitProgress(projectName, progressIndex, "start")
		detail := CustomProjectPromptDocumentDetail{
			ProjectName: projectName,
			Status:      "error",
		}
		item, ok := itemByName[strings.ToLower(projectName)]
		if !ok {
			detail.Message = "未找到已导入的自定义项目题库记录"
			result.Details = append(result.Details, detail)
			result.ErrorCount++
			emitProgress(projectName, progressIndex, "error")
			continue
		}
		sourcePath := util.NormalizePath(item.SourcePath)
		detail.SourcePath = sourcePath
		outputPath := buildCustomProjectPromptDocumentPath(rootPath, item.DisplayName, time.Now())
		detail.OutputPath = outputPath

		emitProgress(projectName, progressIndex, "generating")
		content, genErr := s.generateCustomProjectPromptDocumentWithProvider(ctx, sourcePath, item.DisplayName, selection, counts)
		if genErr != nil {
			detail.Message = genErr.Error()
			result.Details = append(result.Details, detail)
			result.ErrorCount++
			emitProgress(projectName, progressIndex, "error")
			continue
		}
		emitProgress(projectName, progressIndex, "writing")
		if err := os.WriteFile(util.ExpandTilde(outputPath), []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
			detail.Message = err.Error()
			result.Details = append(result.Details, detail)
			result.ErrorCount++
			emitProgress(projectName, progressIndex, "error")
			continue
		}
		detail.Content = strings.TrimSpace(content)
		detail.Status = "generated"
		detail.Message = "已生成提示词文档"
		result.Details = append(result.Details, detail)
		result.GeneratedCount++
		emitProgress(projectName, progressIndex, "done")
	}

	return result, nil
}

func (s *PromptService) ReadCustomProjectPromptDocument(path string) (*CustomProjectPromptDocumentDetail, error) {
	documentPath := util.NormalizePath(path)
	if strings.TrimSpace(documentPath) == "" {
		return nil, errors.New("提示词文档路径不能为空")
	}
	content, err := os.ReadFile(util.ExpandTilde(documentPath))
	if err != nil {
		return nil, err
	}
	return &CustomProjectPromptDocumentDetail{
		ProjectName: inferCustomProjectNameFromPromptDocumentPath(documentPath),
		OutputPath:  documentPath,
		Content:     strings.TrimSpace(string(content)),
		Status:      "loaded",
		Message:     "已读取提示词文档",
	}, nil
}

func (s *PromptService) SaveCustomProjectPromptDocument(req CustomProjectPromptDocumentRequest) (*CustomProjectPromptDocumentDetail, error) {
	documentPath := util.NormalizePath(req.Path)
	if strings.TrimSpace(documentPath) == "" {
		return nil, errors.New("提示词文档路径不能为空")
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, errors.New("提示词文档内容不能为空")
	}
	if err := os.WriteFile(util.ExpandTilde(documentPath), []byte(content+"\n"), 0o644); err != nil {
		return nil, err
	}
	return &CustomProjectPromptDocumentDetail{
		ProjectName: inferCustomProjectNameFromPromptDocumentPath(documentPath),
		OutputPath:  documentPath,
		Content:     content,
		Status:      "saved",
		Message:     "已保存提示词文档",
	}, nil
}

// ── CLI Agent 执行 ──────────────────────────────────────────────────────────

type generatedPromptResult struct {
	PromptText       string
	PromptDifficulty string
}

func (s *PromptService) generatePromptWithRetry(ctx context.Context, workDir, prompt, model string, maxRetries int, envOverrides ...map[string]string) (generatedPromptResult, error) {
	if s.promptGenerator != nil {
		return s.promptGenerator(ctx, workDir, prompt, model)
	}
	return s.executeCliWithRetry(ctx, workDir, prompt, model, maxRetries, envOverrides...)
}

func (s *PromptService) runPromptHumanizer(ctx context.Context, workDir, prompt, model string) (string, error) {
	if s.promptHumanizer != nil {
		return s.promptHumanizer(ctx, workDir, prompt, model)
	}
	return "", nil
}

func (s *PromptService) generateCustomProjectPromptDocument(ctx context.Context, workDir, projectName, model string, requested ...internalprompt.DocumentCounts) (string, error) {
	return s.generateCustomProjectPromptDocumentWithProvider(ctx, workDir, projectName, promptProviderSelection{Model: model}, requested...)
}

func (s *PromptService) generateCustomProjectPromptDocumentWithProvider(ctx context.Context, workDir, projectName string, selection promptProviderSelection, requested ...internalprompt.DocumentCounts) (string, error) {
	counts := internalprompt.DefaultDocumentCounts()
	if len(requested) > 0 {
		counts = requested[0]
	}
	var output string
	var err error
	if s.requirementDocGenerator != nil {
		output, err = s.requirementDocGenerator(ctx, workDir, projectName, selection.Model)
	} else {
		projectProfile, profileErr := s.resolveProjectProfile(ctx, workDir)
		if profileErr != nil {
			slog.Warn("custom project profile cache unavailable, falling back to live repository reading",
				"project", projectName,
				"error", profileErr,
			)
		}
		prompt := buildCustomProjectPromptDocumentPrompt(projectName, projectProfile, counts)
		output, err = s.executeCliRaw(ctx, workDir, prompt, selection.Model, selection.EnvOverrides)
	}
	if err != nil {
		return "", err
	}
	content := cleanCustomProjectPromptDocument(output)
	if strings.TrimSpace(content) == "" {
		trimmedOutput := strings.TrimSpace(output)
		if trimmedOutput == "" {
			return "", errors.New("模型未返回可写入的提示词文档")
		}
		return "", fmt.Errorf("模型未返回有效的提示词需求文档: %s", trimmedOutput)
	}
	if err := counts.ValidateDocument(content); err != nil {
		return "", err
	}
	return content, nil
}

func (s *PromptService) bestEffortPolishPrompt(ctx context.Context, workDir, promptText string, selection promptProviderSelection) string {
	original := strings.TrimSpace(promptText)
	if original == "" {
		return original
	}
	if s.promptHumanizer == nil {
		return original
	}

	polishPrompt := buildPolishSkillPrompt(original)
	start := time.Now()
	polished, err := s.runPromptHumanizer(ctx, workDir, polishPrompt, selection.Model)
	if err != nil {
		slog.Warn("prompt polish failed, using original",
			"model", selection.Model,
			"provider", selection.Name,
			"elapsed", time.Since(start).Round(time.Millisecond),
			"error", err,
		)
		return original
	}

	polished = strings.TrimSpace(polished)
	if polished == "" {
		slog.Warn("prompt polish returned empty text, using original",
			"model", selection.Model,
			"provider", selection.Name,
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
		return original
	}

	slog.Info("prompt polish completed",
		"model", selection.Model,
		"provider", selection.Name,
		"elapsed", time.Since(start).Round(time.Millisecond),
		"changed", polished != original,
	)
	return polished
}

// executeCliWithRetry 执行 CLI Agent 生成提示词，失败时自动重试指定次数。
func (s *PromptService) executeCliWithRetry(ctx context.Context, workDir, prompt, model string, maxRetries int, envOverrides ...map[string]string) (generatedPromptResult, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			slog.Warn("retrying CLI prompt generation",
				"attempt", attempt,
				"max_retries", maxRetries,
				"last_error", lastErr,
			)
		}
		result, err := s.executeCliPromptGeneration(ctx, workDir, prompt, model, envOverrides...)
		if err == nil {
			return result, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return generatedPromptResult{}, err
		}
		lastErr = err
	}
	return generatedPromptResult{}, fmt.Errorf(errs.FmtPromptRetryFailed, maxRetries, lastErr)
}

// executeCliPromptGeneration 启动一次 CLI Agent 执行并从输出中提取提示词。
func (s *PromptService) executeCliPromptGeneration(ctx context.Context, workDir, prompt, model string, envOverrides ...map[string]string) (generatedPromptResult, error) {
	output, err := s.executeCliRaw(ctx, workDir, prompt, model, envOverrides...)
	if err != nil {
		return generatedPromptResult{}, err
	}

	result, err := ExtractPromptResultFromCLIOutput(output)
	if err != nil {
		return generatedPromptResult{}, fmt.Errorf(errs.FmtPromptExtractFail, err)
	}

	return generatedPromptResult{
		PromptText:       result.PromptText,
		PromptDifficulty: result.PromptDifficulty,
	}, nil
}

func (s *PromptService) executeCliHumanizer(ctx context.Context, workDir, prompt, model string, envOverrides ...map[string]string) (string, error) {
	output, err := s.executeCliRaw(ctx, workDir, prompt, model, envOverrides...)
	if err != nil {
		return "", err
	}

	humanizedText, err := ExtractHumanizedTextFromCLIOutput(output)
	if err != nil {
		return "", fmt.Errorf(errs.FmtPolishExtractFail, err)
	}

	return humanizedText, nil
}

func (s *PromptService) executeCliRaw(ctx context.Context, workDir, prompt, model string, envOverrides ...map[string]string) (string, error) {
	additionalDirs := cliAdditionalDirs()
	env := map[string]string(nil)
	if len(envOverrides) > 0 {
		env = envOverrides[0]
	}

	resp, err := s.cliSvc.StartClaude(appcli.StartClaudeRequest{
		WorkDir:        workDir,
		Prompt:         prompt,
		Model:          claudeCLIModel(model, env),
		PermissionMode: "bypassPermissions",
		AdditionalDirs: additionalDirs,
		EnvOverrides:   env,
	})
	if err != nil {
		return "", fmt.Errorf(errs.FmtClaudeStartFail, err)
	}

	output, err := s.waitForCliCompletion(ctx, resp.SessionID)
	if err != nil {
		return "", err
	}
	return output, nil
}

func claudeCLIModel(model string, envOverrides map[string]string) string {
	if strings.TrimSpace(envOverrides["ANTHROPIC_MODEL"]) != "" {
		return ""
	}
	return model
}

// waitForCliCompletion 同步轮询等待 CLI 执行完成，返回完整输出。
func (s *PromptService) waitForCliCompletion(ctx context.Context, sessionID string) (string, error) {
	var lines []string
	offset := 0

	for {
		select {
		case <-ctx.Done():
			_ = s.cliSvc.CancelSession(sessionID)
			return "", ctx.Err()
		default:
		}

		poll, err := s.cliSvc.PollOutput(appcli.PollOutputRequest{
			SessionID: sessionID,
			Offset:    offset,
		})
		if err != nil {
			return "", fmt.Errorf(errs.FmtCliPollFail, err)
		}

		lines = append(lines, poll.Lines...)
		offset += len(poll.Lines)

		if poll.Done {
			if poll.ErrMsg != "" {
				return "", fmt.Errorf(errs.FmtClaudeRunErr, poll.ErrMsg)
			}
			combined := strings.Join(lines, "\n")
			if strings.Contains(combined, "No available accounts") || strings.Contains(combined, "no available accounts") {
				return "", errors.New(errs.MsgClaudeCodeAcpBusy)
			}
			return combined, nil
		}

		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = s.cliSvc.CancelSession(sessionID)
			return "", ctx.Err()
		case <-timer.C:
		}
	}
}

// ── 辅助函数 ────────────────────────────────────────────────────────────────

// buildSkillPrompt 构建 Claude Code 技能调用格式的消息。
// 使用 "/技能名 参数" 格式，这是 claude -p 模式下唯一能正确触发
// skill 加载（展开 SKILL.md 完整内容到上下文）的方式。
// XML 标签格式（<command-name>）在非交互模式下不会触发 skill 展开。
func buildSkillPrompt(req GeneratePromptRequest, siblingPrompts []siblingPrompt, projectProfile *promptProjectProfile) string {
	const skillName = "评审项目提示词生成"
	var sb strings.Builder
	sb.WriteString("/")
	sb.WriteString(skillName)
	sb.WriteString(" [PINRU]\ntaskType: ")
	sb.WriteString(internalprompt.NormalizeTaskType(req.TaskType))
	sb.WriteString("\n")

	if len(req.Constraints) > 0 {
		sb.WriteString("constraints: ")
		sb.WriteString(strings.Join(req.Constraints, ","))
		sb.WriteString("\n")
	} else {
		sb.WriteString("constraints: 无约束\n")
	}

	if len(req.Scopes) > 0 {
		sb.WriteString("scope: ")
		sb.WriteString(strings.Join(req.Scopes, ","))
		sb.WriteString("\n")
	}

	if req.AdditionalNotes != nil && strings.TrimSpace(*req.AdditionalNotes) != "" {
		sb.WriteString("notes: ")
		sb.WriteString(strings.TrimSpace(*req.AdditionalNotes))
		sb.WriteString("\n")
	}

	appendProjectRequirementGenerationRules(&sb, req)
	appendTaskSpecificGenerationRules(&sb, req)
	appendProjectProfilePrompt(&sb, projectProfile)

	sb.WriteString("\n输出要求：请返回结构化 JSON，不要包裹 Markdown 代码块。字段必须包含 version、prompt、promptDifficulty。")
	sb.WriteString("promptDifficulty 只能取 困难 或 地狱，难度下限为困难，不允许输出简单或一般。")
	sb.WriteString("难度按最终 prompt 的真实实现成本判断：跨模块/跨系统多文件、多约束、需要联动验证为困难；只有高耦合、高不确定、验证成本极高才用地狱。")
	sb.WriteString("如果按题目真实成本达不到困难，必须换一个有真实联动链路的题目重新出题，不得降级输出简单或一般，也不得靠堆砌无关边界硬贴难度。\n")

	if len(siblingPrompts) > 0 {
		sb.WriteString("\n---\n")
		sb.WriteString("题库已有提示词（优先列出同一代码仓库，其余来自其他题卡）：\n")
		sb.WriteString(`要求：新生成的提示词必须在"考察点、切入角度、改动范围、描述措辞"上都与下列已有提示词明显不同，不得出现跨项目、跨批次的题目雷同或换皮重复；如果已有提示词覆盖了当前最直接的考察方向，请改从其他真实切入点出题。`)
		sb.WriteString("\n\n")
		for i, sp := range siblingPrompts {
			fmt.Fprintf(&sb, "【已有提示词 %d】taskId=%s taskType=%s\n", i+1, sp.TaskID, sp.TaskType)
			sb.WriteString(sp.PromptText)
			sb.WriteString("\n\n")
		}
	}

	return sb.String()
}

func appendProjectRequirementGenerationRules(sb *strings.Builder, req GeneratePromptRequest) {
	normalizedTaskType := internalprompt.NormalizeTaskType(req.TaskType)
	sb.WriteString("\n通用出题规则：必须先基于当前仓库的真实项目结构、已有功能、主要用户路径、状态流和工程约束生成提示词，不要写泛泛产品想法。提示词要像真实项目排期里的研发任务，包含业务背景、触发场景、用户可感知行为、影响范围和交付边界，但不要出现代码片段、文件路径、类名、方法名、接口名、变量名或具体实现步骤；不要在末尾固定追加“验收时...”“验证时...”这类模板句。\n")
	sb.WriteString("可验收性规则：正文必须自然写清当前情况、触发场景、目标行为和可核查的交付结果，并明确至少一个真实边界、异常场景或旧行为兼容要求。验收结果要能从页面反馈、状态变化、数据结果、接口行为或测试产物中确认，不能只写“优化体验”“完善逻辑”“增强稳定性”。\n")
	sb.WriteString("审核隔离规则：题目不得出现五维评分、21分收录门槛、审核通过率、压分或扣分暗示，也不能故意制造失败、保留缺陷、设置不可完成条件或用模糊要求诱导模型出错。题目只描述项目真实需要解决的问题。\n")
	sb.WriteString("文案红线：直接写需求正文，不加“以下是”“作为 AI”等AI式前言，不写总结式收尾，不使用箭头、Emoji、反引号或装饰符号。语句完整通顺，避免重复句式、同义词堆叠和只替换少量名词的语义换皮；必要事实不得为了调整文风而删减或改名。\n")
	sb.WriteString("任务类型边界：0-1代码生成必须是此前不存在、且必须与现有角色、数据、状态或流程深度衔接的完整模块、新子系统或新主流程，不接受空白项目脚手架或只有增删改查的孤立实现；Feature迭代必须是在已有功能基础上的规则增强、流程延展或能力补齐，并牵动上下游数据、状态或校验链路；Bug修复必须是当前项目中真实存在或由代码迹象支撑的缺陷，写清触发条件、异常表现和业务后果；代码理解聚焦梳理链路和风险；代码重构强调业务结果不变；工程化聚焦构建、依赖、发布或协作稳定性；代码测试围绕高风险流程、边界和回归风险补验证。\n")
	sb.WriteString("质量自检：最终提示词不能只写“优化体验”“完善逻辑”“增强稳定性”这类空话；如果任务涉及导出、统计、预约、状态流转、权限、缓存、异步或跨页面流程，要优先体现数据一致性、异常恢复、边界值、前后端契约或验证链路。难度按理解成本、决策成本和约束复杂度判断，不按文件数量机械判断。\n")
	sb.WriteString("难度下限：本批只接受困难与地狱。严禁生成简单需求：单文件局部改动、单个判断或字段调整、单个按钮或文案调整、只补一个校验或提示、只做样式微调这类题目一律不得输出；如果某个切入点只能做成这类小修，必须放弃并改选有真实联动链路的题目，不得靠堆砌无关边界把小题硬贴成难题。\n")
	appendDifficultPromptEligibilityRules(sb, false)

	switch normalizedTaskType {
	case internalprompt.TaskTypeCodeGen:
		sb.WriteString("0-1代码生成额外规则：不要把普通新增按钮、局部配置或小范围能力写成 0-1；必须体现目标用户、关键闭环、状态变化和与现有系统的衔接，并证明它牵动多个协作部分、不能拆成互不影响的局部小修，同一业务模块内的多文件真实联动也可以。\n")
	case internalprompt.TaskTypeFeature:
		sb.WriteString("Feature迭代额外规则：必须说明现有流程哪里不够、扩展后解决什么摩擦，并体现兼容旧行为、上下游影响或多角色协作；不允许只加一个筛选、一个字段或一处提示。\n")
	case internalprompt.TaskTypeTesting:
		sb.WriteString("代码测试额外规则：不要只写补覆盖率，要明确测试对象、正常和异常路径、边界输入、异步时序或回归风险。\n")
	}
}

func appendDifficultPromptEligibilityRules(sb *strings.Builder, batch bool) {
	sb.WriteString("困难题准入门槛：题目必须存在一条不能拆成互不影响的局部小修的联动链路，并至少命中一类真实复杂度：跨模块或跨层的数据与状态传递；状态机、异步时序、并发或失败恢复；多入口、持久化与展示之间的一致性；兼容旧数据或旧行为且需要成组回归验证。同一业务模块内多个协作部分的真实联动也可以构成困难题，不强求跨模块。独立的样式、提示或输入校验不构成困难题，不能因为边界条件写得多就判为困难。文字截断与完整名称提示、本地存储失败提示、上传文件类型或大小校验都属于典型局部小修；把几项互不关联的小修拼在一起，也不能抬成困难题。")
	sb.WriteString("本期 Pair-wise GSB 改动量门槛：每道困难或地狱题都必须让两次独立实现各自产生至少 10 行有效源码改动，按产物快照相对初始快照的差异计算，每个新增行和删除行各计一行；依赖锁文件、node_modules 等依赖目录、构建产物和纯文档不计入。题目应要求多个相互协作的实现点，避免一侧几行微修即可完成、另一侧却需要完整重构的极端失衡，否则没有两份分量相当的产物可比。这是内部选题和自检门槛，不要把改动行数、GSB、审核或收录规则写进最终业务提示词。")
	if batch {
		sb.WriteString("批量生成需要补足困难题名额时，必须换题，不能硬贴【困难】标签。题目难度只能标为困难或地狱；换题后仍达不到困难门槛就继续换题，不得降级为简单或一般。\n")
		return
	}
	sb.WriteString("达不到门槛时必须换一个有真实联动链路的题目，不得降级输出简单或一般，也不得为了满足预期难度虚构链路。\n")
}

func appendTaskSpecificGenerationRules(sb *strings.Builder, req GeneratePromptRequest) {
	if internalprompt.NormalizeTaskType(req.TaskType) != internalprompt.TaskTypeBugFix {
		return
	}

	switch promptScopeDifficultyLevel(req.Scopes) {
	case 0, 1:
		sb.WriteString("\nBug修复出题规则：当前范围允许单文件或局部修复，但仍必须基于代码中真实存在、用户可观察的缺陷，不能写成新增功能或泛泛优化。\n")
	case 2:
		sb.WriteString("\nBug修复出题规则：当前范围是模块内多文件，必须选择需要同一业务模块内多个协作部分一起修复的真实缺陷，例如页面交互、状态计算、数据读写、校验或展示链路不一致。不要生成只改一个判断、一个字段、一个按钮状态或一处文案就能解决的简单 bug。\n")
	default:
		sb.WriteString("\nBug修复出题规则：当前范围是跨模块或跨系统多文件，必须选择需要多文件、多层链路联动修复的真实缺陷。优先围绕前后端契约、列表与详情、统计与导出、权限与操作入口、状态流转与库存/订单/通知、缓存与数据刷新等会造成跨模块不一致的场景出题。题目要描述用户看到的异常和修复后的业务结果，但不要点名文件、类、方法或字段。不要生成只改一个判断、一个字段、一个按钮状态或一处文案就能解决的简单 bug。\n")
	}
}

func appendProjectProfilePrompt(sb *strings.Builder, projectProfile *promptProjectProfile) {
	if projectProfile == nil || strings.TrimSpace(projectProfile.ProfileText) == "" {
		sb.WriteString("\n项目分析方式：当前没有可用项目画像缓存，请先快速阅读项目结构和关键文件，再生成提示词。只在必要时深入读取源码，不要无目的全量扫描。\n")
		return
	}

	sb.WriteString("\n---\n")
	sb.WriteString("项目画像缓存（优先使用）：\n")
	sb.WriteString(strings.TrimSpace(projectProfile.ProfileText))
	sb.WriteString("\n\n")
	sb.WriteString("项目分析方式：请优先基于上面的项目画像、任务类型和已有提示词生成任务。只有画像信息不足以支撑真实业务判断时，才补充读取少量相关源码；不要每次从零开始全量扫描项目。\n")
}

func buildCustomProjectPromptDocumentPrompt(projectName string, projectProfile *promptProjectProfile, requested ...internalprompt.DocumentCounts) string {
	counts := internalprompt.DefaultDocumentCounts()
	if len(requested) > 0 {
		counts = requested[0].NormalizeDifficultyAllocation()
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "项目名称：%s\n", strings.TrimSpace(projectName))
	sb.WriteString("角色要求：请以有实际研发排期经验的产品经理视角生成提示词，同时理解工程实现约束。输出要像真实业务交付任务，复杂度以项目真实链路为准，不要写成概念 PRD、营销文案或课堂作业。\n")
	sb.WriteString("请基于当前项目一次性生成提示词需求文档，不要逐条调用单题出题逻辑。\n")
	fmt.Fprintf(&sb, "数量要求：只生成 %d 条，其中 0-1代码生成 %d 条，Feature迭代 %d 条，Bug修复 %d 条。仅允许生成这三类题，数量为 0 的分类不生成。严格按数量生成，不擅自增减。\n", counts.Total(), counts.CodeGen, counts.Feature, counts.BugFix)
	fmt.Fprintf(&sb, "难度数量：整批严格生成【困难】%d 条、【地狱】%d 条，只允许使用【困难】和【地狱】两种标签，不能输出简单或一般。题型数量与难度数量是两套独立约束，由你结合每条任务的真实实现工作量把难度名额分配到各题型，但两项合计必须与总题数完全一致。\n", counts.Difficult, counts.Hell)
	appendDifficultPromptEligibilityRules(&sb, true)
	sb.WriteString("严禁简单需求：单文件局部改动、单个判断或字段调整、单个按钮或文案调整、只补一个校验或提示、只做样式微调这类题目一律不得出现；如果某个切入点只能做成这类小修，必须放弃并改选有真实联动链路的题目。每条提示词必须存在一条不能拆成互不影响的局部小修的联动链路，并至少命中一类真实复杂度：跨模块或跨层的数据与状态传递；状态机、异步时序、并发或失败恢复；多入口、持久化与展示之间的一致性；兼容旧数据或旧行为且需要成组回归验证。\n")
	sb.WriteString("复杂度必须以代码事实为依据：先结合项目画像和必要的源码检查确认现有能力边界，再指出当前流程真实缺什么、改动会牵动哪些角色、数据或状态；不能编造 Bug、虚构链路或无依据堆叠复杂度，也不能为了抬高难度堆砌与项目无关的权限、事务、异步或多角色要求。\n")
	sb.WriteString("去重要求：所有提示词之间不得重复或换皮，也要避免对项目已经具备的功能重复出题。先结合项目画像和必要的源码检查确认能力边界，不得只替换对象名、页面名、状态名后复用同一类需求。每条必须在业务目标、用户路径、状态链路、数据对象、交付边界中至少有两个维度明显不同，语义和句式都要明显不同。输出前逐条交叉检查，发现文字重复比例偏高、语义相近或同义改写时，必须换成真实的不同切入点。\n")
	sb.WriteString("可验收性要求：每条都要自然写清当前情况、触发场景、目标行为和可核查的交付结果，并明确至少一个真实边界、异常场景或旧行为兼容要求；单条正文不少于 80 字。结果应能从页面反馈、状态变化、数据结果、接口行为或测试产物中确认，不能只写抽象目标。\n")
	sb.WriteString("审核隔离要求：题目不得出现五维评分、21分收录门槛、审核通过率、压分或扣分暗示，也不能故意制造失败、保留缺陷、设置不可完成条件或用模糊要求诱导模型出错。\n")
	sb.WriteString("Bug修复须基于当前代码中真实存在的缺陷，写清触发条件、异常表现和修复后的结果，不能为凑数量编造问题。\n")
	sb.WriteString("内容要求：提示词必须像真实项目排期里的研发任务，包含背景、触发场景、用户可感知行为和交付边界；不要靠空泛措辞抬难度，也不要为了显得复杂而堆砌与项目无关的模块。不要出现代码片段、文件路径、类名、方法名、接口名、变量名、命令或具体实现步骤。\n")
	sb.WriteString("文风要求：参考 PINRU 历史提示词的自然写法，每条像真实领题描述的一段中文。严禁模板化表达、AI式前言、机械总结、同义词堆叠、语病和未写完的句子；不要使用箭头、Emoji、反引号或装饰符号。不要固定写成“小标题：正文”，不要每条都用冒号切分，也不要先起一个功能名再解释；可以自然使用“当前...”“现在...”“希望...”“新增...”“需要...”等开头，但整批不要同一种句式。不要在每条末尾固定追加“验收时...”“验证时...”“需要确保...”这类验收句；如果必须表达交付结果，要自然融入业务描述里。\n")
	sb.WriteString("0-1代码生成要求：应是此前不存在、且必须与现有角色、页面、数据流或业务链路深度衔接的完整模块、新页面组或新主流程；写明目标用户、关键闭环、状态变化和与现有系统的衔接，不允许写成孤立脚手架、空白项目的从零搭建或只有增删改查的简单模块。\n")
	sb.WriteString("Feature迭代要求：在已有功能基础上扩展规则、延展流程或补齐能力，说明现有流程哪里不够、扩展后解决什么摩擦，以及新旧行为如何共存；改动必须牵动上下游数据、状态或校验链路，不允许只加一个筛选、一个字段或一处提示。\n")
	sb.WriteString("输出格式：只返回 Markdown 正文，不要包裹代码块，不要解释生成过程。分类标题按顺序只使用 **0-1代码生成**、**Feature迭代**、**Bug修复**，数量为 0 的分类省略。每条格式只保留序号、难度标签和自然正文，建议每条 100-220 字且不少于 80 字。例如：1. 【困难】当前会员预约后到场情况不清楚，管理员无法知道实际到课率。需要补一个签到核销入口，把预约状态和到场结果记录下来，并在取消、迟到和重复核销时给出清楚反馈，方便后续查看课程运营情况。不要输出成“会员签到核销子系统：...”这类固定标题格式。\n")

	if projectProfile == nil || strings.TrimSpace(projectProfile.ProfileText) == "" {
		sb.WriteString("\n项目分析方式：当前没有可用项目画像缓存，请先快速阅读项目结构和关键文件，再生成文档；只在必要时读取源码，不要无目的全量扫描。\n")
		return sb.String()
	}

	sb.WriteString("\n---\n")
	sb.WriteString("项目画像缓存（优先使用）：\n")
	sb.WriteString(strings.TrimSpace(projectProfile.ProfileText))
	sb.WriteString("\n\n项目分析方式：优先基于上面的项目画像生成文档。只有画像信息不足以支撑真实业务判断时，才补充读取少量相关源码。\n")
	return sb.String()
}

func cleanCustomProjectPromptDocument(output string) string {
	trimmed := stripMarkdownFence(strings.TrimSpace(output))
	heading := regexp.MustCompile(`(?m)^\s{0,3}(?:#{1,6}\s*)?(?:\*\*)?\s*(?:0-1代码生成|Feature迭代|Bug修复|代码理解|工程化|代码测试|代码重构)\s*(?:\*\*)?\s*$`)
	if loc := heading.FindStringIndex(trimmed); loc != nil {
		trimmed = trimmed[loc[0]:]
	}
	return strings.TrimSpace(trimmed)
}

func stripMarkdownFence(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) >= 2 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		lines = lines[1:]
		if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			lines = lines[:len(lines)-1]
		}
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	return trimmed
}

type siblingPrompt struct {
	TaskID     string
	TaskType   string
	PromptText string
}

type duplicatePromptMatch struct {
	TaskID     string
	TaskType   string
	PromptText string
	Reason     string
	Confidence float64
}

type semanticDuplicateDecision struct {
	IsDuplicate bool    `json:"isDuplicate"`
	Confidence  float64 `json:"confidence"`
	TaskID      string  `json:"taskId"`
	Reason      string  `json:"reason"`
}

const (
	semanticDuplicateFullCandidateLimit    = 6
	semanticDuplicateExcerptCandidateLimit = 12
	semanticDuplicateExcerptRuneLimit      = 120
	semanticDuplicateLocalJudgeThreshold   = 0.015
	semanticDuplicateConfidenceThreshold   = 0.65
	semanticDuplicateJudgeTimeout          = 90 * time.Second
	generationContextPromptLimit           = 8
	generationContextExcerptRuneLimit      = 120
)

func collectPromptDedupSources(task *store.Task, siblings []siblingPrompt) []siblingPrompt {
	result := make([]siblingPrompt, 0, len(siblings)+1)
	if task != nil && task.PromptText != nil {
		if text := strings.TrimSpace(*task.PromptText); text != "" {
			result = append(result, siblingPrompt{
				TaskID:     task.ID,
				TaskType:   task.TaskType,
				PromptText: text,
			})
		}
	}
	result = append(result, siblings...)
	return result
}

func collectOtherTaskPrompts(task *store.Task, tasks []store.Task) []siblingPrompt {
	sameProject := make([]siblingPrompt, 0, len(tasks))
	otherProjects := make([]siblingPrompt, 0, len(tasks))
	for _, t := range tasks {
		if (task != nil && t.ID == task.ID) || t.PromptText == nil {
			continue
		}
		text := strings.TrimSpace(*t.PromptText)
		if text == "" {
			continue
		}
		item := siblingPrompt{
			TaskID:     t.ID,
			TaskType:   t.TaskType,
			PromptText: text,
		}
		if task != nil && t.GitLabProjectID == task.GitLabProjectID {
			sameProject = append(sameProject, item)
		} else {
			otherProjects = append(otherProjects, item)
		}
	}
	return append(sameProject, otherProjects...)
}

func promptGenerationContext(existingPrompts []siblingPrompt) []siblingPrompt {
	if len(existingPrompts) == 0 {
		return nil
	}
	limit := generationContextPromptLimit
	if len(existingPrompts) < limit {
		limit = len(existingPrompts)
	}
	result := make([]siblingPrompt, 0, limit)
	for _, prompt := range existingPrompts[:limit] {
		result = append(result, siblingPrompt{
			TaskID:     prompt.TaskID,
			TaskType:   prompt.TaskType,
			PromptText: truncateRunes(prompt.PromptText, generationContextExcerptRuneLimit),
		})
	}
	return result
}

func normalizePromptForDuplicateCheck(prompt string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(prompt)), " ")
}

func findExactDuplicatePromptMatch(promptText string, existingPrompts []siblingPrompt) (duplicatePromptMatch, bool) {
	normalized := normalizePromptForDuplicateCheck(promptText)
	if normalized == "" {
		return duplicatePromptMatch{}, false
	}
	for _, existing := range existingPrompts {
		if normalized == normalizePromptForDuplicateCheck(existing.PromptText) {
			return duplicatePromptMatch{
				TaskID:     existing.TaskID,
				TaskType:   existing.TaskType,
				PromptText: existing.PromptText,
				Reason:     "文本完全重复",
				Confidence: 1,
			}, true
		}
	}
	return duplicatePromptMatch{}, false
}

func (s *PromptService) findDuplicatePromptMatch(ctx context.Context, workDir, promptText string, existingPrompts []siblingPrompt, model string, envOverrides ...map[string]string) (duplicatePromptMatch, bool, error) {
	if match, ok := findExactDuplicatePromptMatch(promptText, existingPrompts); ok {
		return match, true, nil
	}
	candidates := semanticDuplicateCandidates(promptText, existingPrompts, semanticDuplicateExcerptCandidateLimit)
	if len(candidates) == 0 {
		return duplicatePromptMatch{}, false, nil
	}
	if candidates[0].Confidence < semanticDuplicateLocalJudgeThreshold {
		slog.Info("semantic duplicate judge skipped for low local similarity",
			"candidate_count", len(candidates),
			"top_score", candidates[0].Confidence,
			"threshold", semanticDuplicateLocalJudgeThreshold,
		)
		return duplicatePromptMatch{}, false, nil
	}
	decision, err := s.judgeSemanticDuplicatePrompt(ctx, workDir, promptText, candidates, model, envOverrides...)
	if err != nil {
		return duplicatePromptMatch{}, false, fmt.Errorf("提示词语义判重失败：%w", err)
	}
	if !decision.IsDuplicate || decision.Confidence < semanticDuplicateConfidenceThreshold {
		return duplicatePromptMatch{}, false, nil
	}
	match := candidates[0]
	if strings.TrimSpace(decision.TaskID) != "" {
		for _, candidate := range candidates {
			if strings.TrimSpace(candidate.TaskID) == strings.TrimSpace(decision.TaskID) {
				match = candidate
				break
			}
		}
	}
	match.Reason = strings.TrimSpace(decision.Reason)
	if match.Reason == "" {
		match.Reason = "模型判定语义重复"
	}
	match.Confidence = decision.Confidence
	return match, true, nil
}

func semanticDuplicateCandidates(promptText string, existingPrompts []siblingPrompt, limit int) []duplicatePromptMatch {
	if limit <= 0 {
		limit = len(existingPrompts)
	}
	newTerms := extractPromptSimilarityTerms(promptText)
	candidates := make([]duplicatePromptMatch, 0, len(existingPrompts))

	for _, existing := range existingPrompts {
		oldTerms := extractPromptSimilarityTerms(existing.PromptText)
		score := 0.0
		if len(newTerms) > 0 && len(oldTerms) > 0 {
			score = jaccardTermSimilarity(newTerms, oldTerms)
		}
		candidates = append(candidates, duplicatePromptMatch{
			TaskID:     existing.TaskID,
			TaskType:   existing.TaskType,
			PromptText: existing.PromptText,
			Reason:     fmt.Sprintf("本地语义候选相似度 %.2f", score),
			Confidence: score,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Confidence > candidates[j].Confidence
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

func extractPromptSimilarityTerms(prompt string) map[string]struct{} {
	text := normalizePromptSimilarityText(prompt)
	terms := make(map[string]struct{})
	for _, field := range strings.Fields(text) {
		addPromptTerm(terms, field)
	}

	runes := []rune(strings.ReplaceAll(text, " ", ""))
	for n := 2; n <= 4; n++ {
		if len(runes) < n {
			continue
		}
		for i := 0; i <= len(runes)-n; i++ {
			terms[string(runes[i:i+n])] = struct{}{}
		}
	}
	return terms
}

func normalizePromptSimilarityText(prompt string) string {
	text := strings.ToLower(strings.TrimSpace(prompt))
	replacer := strings.NewReplacer(
		"，", " ", "。", " ", "；", " ", "：", " ", "、", " ", "（", " ", "）", " ",
		"“", " ", "”", " ", "\"", " ", "'", " ", "\n", " ", "\t", " ", "！", " ",
		"？", " ", ":", " ", ",", " ", ".", " ", ";", " ", "!", " ", "?", " ",
		"(", " ", ")", " ", "[", " ", "]", " ", "{", " ", "}", " ",
	)
	return strings.Join(strings.Fields(replacer.Replace(text)), " ")
}

func addPromptTerm(terms map[string]struct{}, field string) {
	field = strings.TrimSpace(field)
	if len([]rune(field)) < 2 {
		return
	}
	terms[field] = struct{}{}
}

func countTermOverlap(left, right map[string]struct{}) int {
	count := 0
	for term := range left {
		if _, ok := right[term]; ok {
			count++
		}
	}
	return count
}

func jaccardTermSimilarity(left, right map[string]struct{}) float64 {
	intersection := countTermOverlap(left, right)
	if intersection == 0 {
		return 0
	}
	union := len(left) + len(right) - intersection
	if union <= 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func (s *PromptService) judgeSemanticDuplicatePrompt(ctx context.Context, workDir, promptText string, candidates []duplicatePromptMatch, model string, envOverrides ...map[string]string) (semanticDuplicateDecision, error) {
	judgePrompt := buildSemanticDuplicateJudgePrompt(promptText, candidates)
	var output string
	var err error
	if s.duplicateJudge != nil {
		return s.duplicateJudge(ctx, workDir, judgePrompt, model)
	}
	judgeCtx, cancel := context.WithTimeout(ctx, semanticDuplicateJudgeTimeout)
	defer cancel()
	output, err = s.executeCliRaw(judgeCtx, workDir, judgePrompt, model, envOverrides...)
	if err != nil {
		return semanticDuplicateDecision{}, err
	}
	return extractSemanticDuplicateDecision(output)
}

func buildSemanticDuplicateJudgePrompt(promptText string, candidates []duplicatePromptMatch) string {
	var sb strings.Builder
	sb.WriteString("你是提示词语义去重审核器。请判断新提示词和历史提示词是否属于同一业务能力或同一考察点的换皮重复。\n")
	sb.WriteString("判重标准：只要核心业务对象、目标能力、主要数据链路和验收重点高度一致，就算重复；即使措辞不同、补充了少量细节也算重复。若只是同一系统里的不同功能，不算重复。\n")
	sb.WriteString("历史候选已按本地通用相似度排序。完整候选用于精判，摘要候选用于兜底识别明显重复。\n")
	sb.WriteString("请从给定历史提示词中找最相似的一条进行判断。只输出 JSON，不要 Markdown。格式：{\"isDuplicate\":true|false,\"confidence\":0到1,\"taskId\":\"命中的历史 taskId，非重复时为空\",\"reason\":\"一句中文原因\"}\n\n")
	fullCandidates, excerptCandidates := splitSemanticDuplicateJudgeCandidates(candidates)
	sb.WriteString("完整历史候选：\n")
	for i, candidate := range fullCandidates {
		fmt.Fprintf(&sb, "【完整 %d】taskId=%s taskType=%s score=%.2f\n", i+1, candidate.TaskID, candidate.TaskType, candidate.Confidence)
		sb.WriteString(strings.TrimSpace(candidate.PromptText))
		sb.WriteString("\n\n")
	}
	if len(excerptCandidates) > 0 {
		sb.WriteString("摘要历史候选：\n")
		for i, candidate := range excerptCandidates {
			fmt.Fprintf(&sb, "【摘要 %d】taskId=%s taskType=%s score=%.2f\n", i+1, candidate.TaskID, candidate.TaskType, candidate.Confidence)
			sb.WriteString(truncateRunes(strings.TrimSpace(candidate.PromptText), semanticDuplicateExcerptRuneLimit))
			sb.WriteString("\n\n")
		}
	}
	sb.WriteString("\n\n新提示词：\n")
	sb.WriteString(strings.TrimSpace(promptText))
	sb.WriteString("\n")
	return sb.String()
}

func splitSemanticDuplicateJudgeCandidates(candidates []duplicatePromptMatch) ([]duplicatePromptMatch, []duplicatePromptMatch) {
	if len(candidates) <= semanticDuplicateFullCandidateLimit {
		return candidates, nil
	}
	full := candidates[:semanticDuplicateFullCandidateLimit]
	excerpt := candidates[semanticDuplicateFullCandidateLimit:]
	return full, excerpt
}

func truncateRunes(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

func extractSemanticDuplicateDecision(output string) (semanticDuplicateDecision, error) {
	for _, candidate := range extractAllJSONObjects(output) {
		var decision semanticDuplicateDecision
		if err := json.Unmarshal([]byte(candidate), &decision); err == nil {
			return decision, nil
		}
	}
	trimmed := strings.TrimSpace(TrimPromptCodeFence(output))
	var decision semanticDuplicateDecision
	if err := json.Unmarshal([]byte(trimmed), &decision); err != nil {
		return semanticDuplicateDecision{}, err
	}
	return decision, nil
}

func buildDuplicateRegenerationPrompt(req GeneratePromptRequest, existingPrompts []siblingPrompt, duplicatePrompt string, match duplicatePromptMatch, projectProfile *promptProjectProfile) string {
	base := buildSkillPrompt(req, existingPrompts, projectProfile)
	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n---\n")
	sb.WriteString("自动重生成要求：上一轮生成结果与已有提示词重复或语义高度相似，必须重新生成一条不同的提示词。\n")
	sb.WriteString("新提示词不得只替换少量同义词，必须更换业务切入点、验收重点和改动边界。\n")
	if strings.TrimSpace(match.TaskID) != "" {
		fmt.Fprintf(&sb, "重复命中的历史提示词：taskId=%s taskType=%s\n", match.TaskID, match.TaskType)
	}
	if strings.TrimSpace(match.Reason) != "" {
		sb.WriteString("重复原因：")
		sb.WriteString(strings.TrimSpace(match.Reason))
		sb.WriteString("\n")
	}
	if strings.TrimSpace(match.PromptText) != "" {
		sb.WriteString("雷同的历史提示词：\n")
		sb.WriteString(strings.TrimSpace(match.PromptText))
		sb.WriteString("\n")
	}
	sb.WriteString("上一轮重复结果：\n")
	sb.WriteString(strings.TrimSpace(duplicatePrompt))
	sb.WriteString("\n")
	return sb.String()
}

func buildQualityRegenerationPrompt(req GeneratePromptRequest, existingPrompts []siblingPrompt, rejectedPrompt string, qualityErr error, projectProfile *promptProjectProfile) string {
	var sb strings.Builder
	sb.WriteString(buildSkillPrompt(req, existingPrompts, projectProfile))
	sb.WriteString("\n---\n")
	sb.WriteString("质量预检未通过，必须重新生成完整提示词，不能只删除触发规则的词语。\n")
	sb.WriteString("保留真实业务意图，重新写清当前情况、触发场景、目标行为、可核查结果和至少一个真实边界；不要加入评分、收录或诱导失败等审核规则。\n")
	sb.WriteString("如果失败原因是难度低于困难，必须换成有真实联动链路的题目，不得靠堆砌无关边界硬贴难度，也不得输出简单或一般难度。\n")
	sb.WriteString("重新选题时还要检查两次独立实现是否都需要至少 10 行有效源码改动，并且改动规模大致可比；不能让一侧靠几行微修完成、另一侧才需要完整重构。该检查只用于内部选题，不要写入最终业务提示词。\n")
	if qualityErr != nil {
		sb.WriteString("失败原因：")
		sb.WriteString(qualityErr.Error())
		sb.WriteString("\n")
	}
	sb.WriteString("上一轮未通过的结果：\n")
	sb.WriteString(strings.TrimSpace(rejectedPrompt))
	sb.WriteString("\n")
	return sb.String()
}

// generatedPromptQualityError 汇总单题生成的质量门禁：文案质量加上难度下限。
// 难度低于困难时按质量不通过处理，触发重新生成而不是保存简单需求。
func generatedPromptQualityError(promptText, difficulty string) error {
	if err := internalprompt.ValidatePromptWritingQuality(promptText); err != nil {
		return err
	}
	if !meetsGeneratedPromptDifficultyFloor(difficulty) {
		return fmt.Errorf("生成难度为“%s”，低于困难下限，禁止输出简单或一般需求，请换一个有真实联动链路的题目重新生成", strings.TrimSpace(difficulty))
	}
	return nil
}

// meetsGeneratedPromptDifficultyFloor 判断模型给出的难度是否达到困难下限。
// 空值表示模型未返回难度，后续按困难兜底，因此同样视为通过。
func meetsGeneratedPromptDifficultyFloor(difficulty string) bool {
	switch strings.TrimSpace(difficulty) {
	case "", "困难", "地狱":
		return true
	default:
		return false
	}
}

// estimatePromptDifficulty 是模型未返回难度时的兜底估算，下限固定为困难。
func estimatePromptDifficulty(req GeneratePromptRequest, promptText string) string {
	scopeLevel := promptScopeDifficultyLevel(req.Scopes)
	meaningfulConstraints := countMeaningfulPromptConstraints(req.Constraints)
	hasNotes := req.AdditionalNotes != nil && strings.TrimSpace(*req.AdditionalNotes) != ""
	length := len([]rune(strings.TrimSpace(promptText)))

	if scopeLevel >= 3 && (meaningfulConstraints >= 3 || (hasNotes && length > 180) || length > 300) {
		return "地狱"
	}
	return store.DefaultPromptDifficulty
}

func promptScopeDifficultyLevel(scopes []string) int {
	level := 0
	for _, scope := range scopes {
		switch strings.TrimSpace(scope) {
		case "单文件":
			if level < 1 {
				level = 1
			}
		case "模块内多文件":
			if level < 2 {
				level = 2
			}
		case "跨模块多文件":
			if level < 3 {
				level = 3
			}
		case "跨系统多模块":
			if level < 4 {
				level = 4
			}
		}
	}
	return level
}

func countMeaningfulPromptConstraints(constraints []string) int {
	count := 0
	for _, constraint := range constraints {
		trimmed := strings.TrimSpace(constraint)
		if trimmed != "" && trimmed != "无约束" {
			count++
		}
	}
	return count
}

func NormalizePromptDifficultyLabel(value string) string {
	switch strings.TrimSpace(value) {
	case "简单":
		return "简单"
	case "一般":
		// 保留原始标签，让难度下限校验能识别并触发重新生成。
		return "一般"
	case "困难":
		return "困难"
	case "地狱":
		return "地狱"
	default:
		return ""
	}
}

type promptProviderSelection struct {
	Name         string
	Model        string
	ProviderType string
	EnvOverrides map[string]string
}

func buildPolishSkillPrompt(text string) string {
	trimmed := strings.TrimSpace(text)
	return strings.Join([]string{
		"/humanizer-zh",
		"",
		"请把下面内容改成更自然、更口语化的业务描述。",
		"不要出现代码片段、伪代码、命令、路径、变量名或技术实现细节。",
		"重点保留业务现象、用户感知、场景变化和需要补齐的业务处理。",
		"只返回润色后的正文。",
		"",
		trimmed,
	}, "\n")
}

func defaultPolishWorkDir() string {
	workDir, err := os.Getwd()
	if err != nil || strings.TrimSpace(workDir) == "" {
		return "."
	}
	return workDir
}

// resolveProviderForPromptGeneration resolves a DeepSeek Flash provider. The
// local Claude Code process remains the tool harness for repository access.
func resolveProviderForPromptGeneration(st *store.Store, requestedID *string) (promptProviderSelection, error) {
	providers, err := st.ListLLMProviders()
	if err != nil {
		return promptProviderSelection{}, err
	}
	if len(providers) == 0 {
		return promptProviderSelection{
			Name:         "DeepSeek V4 Flash（Claude Code）",
			Model:        defaultPromptGenerationModel,
			ProviderType: "claude_code_acp",
		}, nil
	}

	if requestedID != nil && strings.TrimSpace(*requestedID) != "" {
		selected := selectProvider(providers, requestedID)
		if selected == nil {
			return promptProviderSelection{}, errors.New("未找到所选的 DeepSeek V4 Flash 提供商")
		}
		if !isDeepSeekPromptProvider(*selected) {
			return promptProviderSelection{}, errors.New("提示词生成和润色只支持 DeepSeek V4 Flash")
		}
		return buildPromptProviderSelection(*selected), nil
	}

	for _, requireDefault := range []bool{true, false} {
		for i := range providers {
			if providers[i].IsDefault == requireDefault && isDeepSeekAPIProvider(providers[i]) {
				return buildPromptProviderSelection(providers[i]), nil
			}
		}
	}
	for _, requireDefault := range []bool{true, false} {
		for i := range providers {
			if providers[i].IsDefault == requireDefault && isDeepSeekACPProvider(providers[i]) {
				return buildPromptProviderSelection(providers[i]), nil
			}
		}
	}

	return promptProviderSelection{}, errors.New("请先在设置中配置 DeepSeek V4 Flash 提供商")
}

func buildPromptProviderSelection(provider store.LLMProvider) promptProviderSelection {
	name := strings.TrimSpace(provider.Name)
	if name == "" {
		name = "Claude Code CLI"
	}
	model := strings.TrimSpace(provider.Model)
	if model == "" {
		model = defaultPromptGenerationModel
	}
	return promptProviderSelection{
		Name:         name,
		Model:        model,
		ProviderType: provider.ProviderType,
		EnvOverrides: deepSeekClaudeEnvironment(provider),
	}
}

func resolveProviderForPolish(st *store.Store, requestedID *string) (promptProviderSelection, error) {
	return resolveProviderForPromptGeneration(st, requestedID)
}

func isDeepSeekFlashModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case deepSeekFlashModel, deepSeekFlashCanonicalModel:
		return true
	default:
		return false
	}
}

func isDeepSeekAPIProvider(provider store.LLMProvider) bool {
	if provider.ProviderType != "openai_compatible" || !isDeepSeekFlashModel(provider.Model) || strings.TrimSpace(provider.APIKey) == "" {
		return false
	}
	if provider.BaseURL == nil {
		return false
	}
	baseURL := strings.ToLower(strings.TrimRight(strings.TrimSpace(*provider.BaseURL), "/"))
	return baseURL == deepSeekAPIBaseURL || baseURL == deepSeekAPIBaseURL+"/v1"
}

func isDeepSeekACPProvider(provider store.LLMProvider) bool {
	return provider.ProviderType == "claude_code_acp" && isDeepSeekFlashModel(provider.Model)
}

func isDeepSeekPromptProvider(provider store.LLMProvider) bool {
	return isDeepSeekAPIProvider(provider) || isDeepSeekACPProvider(provider)
}

func deepSeekClaudeEnvironment(provider store.LLMProvider) map[string]string {
	if !isDeepSeekAPIProvider(provider) {
		return nil
	}
	model := strings.TrimSpace(provider.Model)
	return map[string]string{
		"ANTHROPIC_BASE_URL":              deepSeekAPIBaseURL + "/anthropic",
		"ANTHROPIC_AUTH_TOKEN":            strings.TrimSpace(provider.APIKey),
		"ANTHROPIC_API_KEY":               strings.TrimSpace(provider.APIKey),
		"ANTHROPIC_MODEL":                 model,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":    model,
		"ANTHROPIC_DEFAULT_SONNET_MODEL":  model,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":   model,
		"CLAUDE_CODE_SUBAGENT_MODEL":      model,
		"CLAUDE_CODE_EFFORT_LEVEL":        "high",
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "786432",
	}
}

func normalizePromptGenerationError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "提示词生成超时，请稍后重试"
	case errors.Is(err, context.Canceled):
		return "提示词生成已取消"
	default:
		msg := err.Error()
		if strings.Contains(msg, "No available accounts") || strings.Contains(msg, "no available accounts") {
			return "Claude Code ACP 账号池暂时耗尽（503），请稍后重试或检查 ACP 配置"
		}
		return msg
	}
}

func selectProvider(providers []store.LLMProvider, requestedID *string) *store.LLMProvider {
	if requestedID != nil && strings.TrimSpace(*requestedID) != "" {
		for i := range providers {
			if providers[i].ID == *requestedID {
				return &providers[i]
			}
		}
		return nil
	}
	for i := range providers {
		if providers[i].IsDefault {
			return &providers[i]
		}
	}
	if len(providers) > 0 {
		return &providers[0]
	}
	return nil
}

// cliAdditionalDirs 返回 CLI Agent 需要访问的额外目录（执行手册目录）。
func cliAdditionalDirs() []string {
	dir := internalprompt.DefaultManualDir()
	if dir == "" {
		return nil
	}
	return []string{dir}
}

func isCustomQuestionBankItem(item store.QuestionBankItem) bool {
	return strings.EqualFold(strings.TrimSpace(item.SourceKind), "local_directory") &&
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(item.OriginRef)), "custom:")
}

func normalizeCustomProjectDocumentNames(projectNames []string) []string {
	seen := make(map[string]struct{}, len(projectNames))
	result := make([]string, 0, len(projectNames))
	for _, name := range projectNames {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func buildCustomProjectPromptDocumentPath(rootPath, projectName string, now time.Time) string {
	fileName := fmt.Sprintf("%s_提示词_%s.md", sanitizePromptDocumentFileName(projectName), now.Format("0102"))
	return util.NormalizePath(filepath.Join(rootPath, fileName))
}

func inferCustomProjectNameFromPromptDocumentPath(path string) string {
	base := strings.TrimSuffix(filepath.Base(strings.TrimSpace(path)), filepath.Ext(path))
	if idx := strings.LastIndex(base, "_提示词_"); idx > 0 {
		return strings.TrimSpace(base[:idx])
	}
	return strings.TrimSpace(base)
}

func sanitizePromptDocumentFileName(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "custom_project"
	}
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		"*", "-",
		"?", "-",
		"\"", "",
		"<", "-",
		">", "-",
		"|", "-",
	)
	cleaned := strings.TrimSpace(replacer.Replace(trimmed))
	cleaned = strings.Join(strings.Fields(cleaned), "")
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return "custom_project"
	}
	return cleaned
}

func (s *PromptService) resolveProviderForTest(provider store.LLMProvider) (store.LLMProvider, error) {
	provider.ID = strings.TrimSpace(provider.ID)
	provider.Name = strings.TrimSpace(provider.Name)
	provider.ProviderType = strings.TrimSpace(provider.ProviderType)
	provider.Model = strings.TrimSpace(provider.Model)

	if provider.ID != "" && !llm.IsACPProvider(provider.ProviderType) && strings.TrimSpace(provider.APIKey) == "" {
		storedProvider, err := s.store.GetLLMProvider(provider.ID)
		if err != nil {
			return store.LLMProvider{}, err
		}
		if storedProvider != nil && strings.TrimSpace(storedProvider.APIKey) != "" {
			provider.APIKey = storedProvider.APIKey
		}
	}

	if provider.ProviderType == "" {
		return store.LLMProvider{}, errors.New(errs.MsgProviderTypeRequired)
	}
	if provider.Model == "" {
		return store.LLMProvider{}, errors.New(errs.MsgModelNameRequired)
	}

	baseURL := provider.BaseURL
	if baseURL != nil {
		trimmed := strings.TrimSpace(*baseURL)
		if trimmed == "" {
			baseURL = nil
		} else {
			baseURL = &trimmed
		}
	}
	provider.BaseURL = baseURL

	return provider, nil
}

// ── 润色文本 ────────────────────────────────────────────────────────────────

// PolishTextRequest 润色请求。
type PolishTextRequest struct {
	Text       string  `json:"text"`
	ProviderID *string `json:"providerId"`
}

// PolishTextResult 润色结果。
type PolishTextResult struct {
	PolishedText string `json:"polishedText"`
	ProviderName string `json:"providerName"`
	Model        string `json:"model"`
}

// PolishText 使用 Claude Code CLI 执行 /humanizer-zh 并返回输出正文。
func (s *PromptService) PolishText(req PolishTextRequest) (*PolishTextResult, error) {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return nil, errors.New(errs.MsgPolishTextRequired)
	}

	if _, err := s.cliSvc.CheckCLI(); err != nil {
		return nil, errors.New(errs.MsgClaudeCodeCliNotInstalledInstallGuide)
	}

	selection, err := resolveProviderForPolish(s.store, req.ProviderID)
	if err != nil {
		return nil, err
	}

	workDir := defaultPolishWorkDir()
	skillPrompt := buildPolishSkillPrompt(text)
	slog.Info("PolishText started", "model", selection.Model, "provider", selection.Name, "textLen", len(text))
	polished, err := s.executeCliHumanizer(context.Background(), workDir, skillPrompt, selection.Model, selection.EnvOverrides)
	if err != nil {
		return nil, fmt.Errorf(errs.FmtPolishFailed, err)
	}

	polished = strings.TrimSpace(polished)
	if polished == "" {
		return nil, errors.New(errs.MsgHumanizerEmpty)
	}

	slog.Info("PolishText completed", "model", selection.Model, "resultLen", len(polished))
	return &PolishTextResult{
		PolishedText: polished,
		ProviderName: selection.Name,
		Model:        selection.Model,
	}, nil
}
