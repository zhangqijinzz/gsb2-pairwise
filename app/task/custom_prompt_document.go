package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	appannotation "github.com/blueship581/pinru/app/annotation"
	appprompt "github.com/blueship581/pinru/app/prompt"
	annotation "github.com/blueship581/pinru/internal/annotation"
	"github.com/blueship581/pinru/internal/errs"
	internalprompt "github.com/blueship581/pinru/internal/prompt"
	"github.com/blueship581/pinru/internal/store"
	"github.com/blueship581/pinru/internal/util"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type CreateTasksFromCustomPromptDocumentsRequest struct {
	ProjectID     string   `json:"projectId"`
	DocumentPaths []string `json:"documentPaths"`
}

type CustomPromptDocumentTaskDetail struct {
	ProjectName      string `json:"projectName"`
	TaskID           string `json:"taskId"`
	QuestionID       int64  `json:"questionId"`
	TaskType         string `json:"taskType"`
	PromptDifficulty string `json:"promptDifficulty"`
	ClaimSequence    int    `json:"claimSequence"`
	LocalPath        string `json:"localPath"`
	Status           string `json:"status"`
	Message          string `json:"message"`
}

type CustomPromptDocumentCreateDetail struct {
	DocumentPath string                           `json:"documentPath"`
	ProjectName  string                           `json:"projectName"`
	ParsedCount  int                              `json:"parsedCount"`
	CreatedCount int                              `json:"createdCount"`
	ErrorCount   int                              `json:"errorCount"`
	Status       string                           `json:"status"`
	Message      string                           `json:"message"`
	Tasks        []CustomPromptDocumentTaskDetail `json:"tasks"`
}

type CreateTasksFromCustomPromptDocumentsResult struct {
	ProjectID     string                             `json:"projectId"`
	CreatedCount  int                                `json:"createdCount"`
	ErrorCount    int                                `json:"errorCount"`
	DocumentCount int                                `json:"documentCount"`
	Details       []CustomPromptDocumentCreateDetail `json:"details"`
}

type PrepareCustomPromptTaskJobsResult struct {
	ProjectID     string                             `json:"projectId"`
	DocumentCount int                                `json:"documentCount"`
	PreparedCount int                                `json:"preparedCount"`
	ErrorCount    int                                `json:"errorCount"`
	Details       []CustomPromptDocumentCreateDetail `json:"details"`
	Jobs          []CustomPromptTaskJobPayload       `json:"jobs"`
}

type CustomPromptTaskJobPayload struct {
	ProjectID        string `json:"projectId"`
	QuestionID       int64  `json:"questionId"`
	ProjectName      string `json:"projectName"`
	SourcePath       string `json:"sourcePath"`
	TargetSourcePath string `json:"targetSourcePath"`
	TaskType         string `json:"taskType"`
	PromptDifficulty string `json:"promptDifficulty"`
	PromptText       string `json:"promptText"`
	ClaimSequence    int    `json:"claimSequence"`
	LocalPath        string `json:"localPath"`
	SourceModelName  string `json:"sourceModelName"`
}

type customPromptEntry struct {
	TaskType         string
	PromptDifficulty string
	PromptText       string
}

type managedClaimPlan struct {
	Sequence   int
	TaskPath   string
	SourcePath string
}

var (
	customPromptHeadingPattern = regexp.MustCompile(`^\s{0,3}(?:#{1,6}\s*)?(?:\*\*)?\s*(0-1代码生成|Feature迭代|代码理解|Bug修复|代码重构|工程化|代码测试|未归类)\s*(?:\*\*)?\s*$`)
	customPromptItemPattern    = regexp.MustCompile(`^\s*(?:[-*]\s+|\d+[.、)]\s+)(.*)$`)
	customPromptDifficultyPat  = regexp.MustCompile(`^【([^】]+)】\s*(.*)$`)
)

func (s *TaskService) PickCustomPromptDocuments() ([]string, error) {
	app := application.Get()
	if app == nil {
		return nil, errors.New("wails 运行时未就绪")
	}
	paths, err := app.Dialog.OpenFile().
		SetTitle("选择自定义项目提示词文档").
		CanChooseFiles(true).
		CanChooseDirectories(false).
		AddFilter("Markdown", "*.md").
		PromptForMultipleSelection()
	if err != nil {
		return nil, err
	}
	return paths, nil
}

func (s *TaskService) CreateTasksFromCustomPromptDocuments(req CreateTasksFromCustomPromptDocumentsRequest) (*CreateTasksFromCustomPromptDocumentsResult, error) {
	return s.CreateTasksFromCustomPromptDocumentsWithContext(context.Background(), req)
}

func (s *TaskService) CreateTasksFromCustomPromptDocumentsWithContext(ctx context.Context, req CreateTasksFromCustomPromptDocumentsRequest) (*CreateTasksFromCustomPromptDocumentsResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prepared, err := s.PrepareCustomPromptTaskJobs(req)
	if err != nil {
		return nil, err
	}
	result := &CreateTasksFromCustomPromptDocumentsResult{
		ProjectID:     prepared.ProjectID,
		DocumentCount: prepared.DocumentCount,
		Details:       prepared.Details,
		ErrorCount:    prepared.ErrorCount,
	}

	for _, payload := range prepared.Jobs {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		taskDetail := s.CreateCustomPromptTaskFromPayload(ctx, payload)
		result.CreatedCount++
		if taskDetail.Status != "created" {
			result.ErrorCount++
		}
		for detailIndex := range result.Details {
			if result.Details[detailIndex].ProjectName != taskDetail.ProjectName {
				continue
			}
			for taskIndex := range result.Details[detailIndex].Tasks {
				if result.Details[detailIndex].Tasks[taskIndex].TaskType == taskDetail.TaskType &&
					result.Details[detailIndex].Tasks[taskIndex].ClaimSequence == taskDetail.ClaimSequence {
					result.Details[detailIndex].Tasks[taskIndex] = taskDetail
					break
				}
			}
			break
		}
	}

	return result, nil
}

func (s *TaskService) PrepareCustomPromptTaskJobs(req CreateTasksFromCustomPromptDocumentsRequest) (*PrepareCustomPromptTaskJobsResult, error) {
	projectID := strings.TrimSpace(req.ProjectID)
	if projectID == "" {
		return nil, errors.New(errs.MsgProjectRequired)
	}
	if len(req.DocumentPaths) == 0 {
		return nil, errors.New("未选择提示词文档")
	}

	project, err := s.store.GetProject(projectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, fmt.Errorf(errs.FmtStoreProjectNotFound, projectID)
	}

	items, err := s.store.ListQuestionBankItems(projectID)
	if err != nil {
		return nil, err
	}
	itemByName := make(map[string]store.QuestionBankItem, len(items))
	for _, item := range items {
		if !isCustomPromptQuestionBankItem(item) {
			continue
		}
		itemByName[strings.ToLower(strings.TrimSpace(item.DisplayName))] = item
	}

	models := parseTaskProjectModels(project.Models, project.SourceModelFolder)
	sourceModelName := models[0]
	result := &PrepareCustomPromptTaskJobsResult{
		ProjectID:     projectID,
		DocumentCount: len(req.DocumentPaths),
		Details:       make([]CustomPromptDocumentCreateDetail, 0, len(req.DocumentPaths)),
		Jobs:          make([]CustomPromptTaskJobPayload, 0),
	}

	for _, rawPath := range req.DocumentPaths {
		detail, jobs := s.prepareCustomPromptTaskJobsFromSingleDocument(*project, itemByName, sourceModelName, rawPath)
		result.PreparedCount += len(jobs)
		if detail.ErrorCount > 0 {
			result.ErrorCount += detail.ErrorCount
		} else if detail.Status == "error" {
			result.ErrorCount++
		}
		result.Details = append(result.Details, detail)
		result.Jobs = append(result.Jobs, jobs...)
	}

	return result, nil
}

func (s *TaskService) prepareCustomPromptTaskJobsFromSingleDocument(
	project store.Project,
	itemByName map[string]store.QuestionBankItem,
	sourceModelName string,
	rawPath string,
) (CustomPromptDocumentCreateDetail, []CustomPromptTaskJobPayload) {
	documentPath := util.NormalizePath(rawPath)
	detail := CustomPromptDocumentCreateDetail{
		DocumentPath: documentPath,
		ProjectName:  inferCustomPromptDocumentProjectName(documentPath),
		Status:       "created",
		Tasks:        []CustomPromptDocumentTaskDetail{},
	}

	if detail.ProjectName == "" {
		detail.Status = "error"
		detail.Message = "无法从文档文件名识别项目名"
		return detail, nil
	}
	item, ok := itemByName[strings.ToLower(detail.ProjectName)]
	if !ok {
		detail.Status = "error"
		detail.Message = fmt.Sprintf("题库中未找到自定义项目：%s", detail.ProjectName)
		return detail, nil
	}

	content, err := os.ReadFile(util.ExpandTilde(documentPath))
	if err != nil {
		detail.Status = "error"
		detail.Message = err.Error()
		return detail, nil
	}
	entries := parseCustomPromptDocumentEntries(string(content))
	detail.ParsedCount = len(entries)
	if len(entries) == 0 {
		detail.Status = "error"
		detail.Message = "文档里没有解析到提示词条目"
		return detail, nil
	}
	if err := validateCustomPromptDocumentBatch(entries); err != nil {
		detail.Status = "error"
		detail.Message = err.Error()
		return detail, nil
	}

	plansByType, err := s.planCustomPromptDocumentClaims(project, item, entries)
	if err != nil {
		detail.Status = "error"
		detail.Message = err.Error()
		return detail, nil
	}

	jobs := make([]CustomPromptTaskJobPayload, 0, len(entries))
	for _, entry := range entries {
		plan := plansByType[entry.TaskType][0]
		plansByType[entry.TaskType] = plansByType[entry.TaskType][1:]
		taskDetail := CustomPromptDocumentTaskDetail{
			ProjectName:      detail.ProjectName,
			QuestionID:       item.QuestionID,
			TaskType:         entry.TaskType,
			PromptDifficulty: entry.PromptDifficulty,
			ClaimSequence:    plan.Sequence,
			LocalPath:        plan.TaskPath,
			Status:           "prepared",
			Message:          "等待后台任务创建",
		}
		detail.Tasks = append(detail.Tasks, taskDetail)
		jobs = append(jobs, CustomPromptTaskJobPayload{
			ProjectID:        project.ID,
			QuestionID:       item.QuestionID,
			ProjectName:      item.DisplayName,
			SourcePath:       item.SourcePath,
			TargetSourcePath: plan.SourcePath,
			TaskType:         entry.TaskType,
			PromptDifficulty: entry.PromptDifficulty,
			PromptText:       entry.PromptText,
			ClaimSequence:    plan.Sequence,
			LocalPath:        plan.TaskPath,
			SourceModelName:  sourceModelName,
		})
	}
	detail.CreatedCount = len(jobs)
	return detail, jobs
}

func (s *TaskService) CreateCustomPromptTaskFromPayload(ctx context.Context, payload CustomPromptTaskJobPayload) CustomPromptDocumentTaskDetail {
	entry := customPromptEntry{
		TaskType:         internalprompt.NormalizeTaskType(payload.TaskType),
		PromptDifficulty: payload.PromptDifficulty,
		PromptText:       payload.PromptText,
	}
	item := store.QuestionBankItem{
		ProjectConfigID: payload.ProjectID,
		QuestionID:      payload.QuestionID,
		DisplayName:     payload.ProjectName,
		SourcePath:      payload.SourcePath,
	}
	project := store.Project{ID: payload.ProjectID}
	plan := managedClaimPlan{
		Sequence:   payload.ClaimSequence,
		TaskPath:   payload.LocalPath,
		SourcePath: payload.TargetSourcePath,
	}
	if strings.TrimSpace(plan.SourcePath) == "" {
		plan.SourcePath = filepath.Join(plan.TaskPath, filepath.Base(plan.TaskPath))
	}
	return s.createSingleCustomPromptTask(ctx, project, item, payload.SourceModelName, entry, plan)
}

func (s *TaskService) createSingleCustomPromptTask(
	ctx context.Context,
	project store.Project,
	item store.QuestionBankItem,
	sourceModelName string,
	entry customPromptEntry,
	plan managedClaimPlan,
) CustomPromptDocumentTaskDetail {
	detail := CustomPromptDocumentTaskDetail{
		ProjectName:      item.DisplayName,
		QuestionID:       item.QuestionID,
		TaskType:         entry.TaskType,
		PromptDifficulty: entry.PromptDifficulty,
		ClaimSequence:    plan.Sequence,
		LocalPath:        plan.TaskPath,
		Status:           "created",
	}

	if strings.TrimSpace(sourceModelName) == "" {
		sourceModelName = "ORIGIN"
	}
	targetPaths := []string{plan.TaskPath}
	if err := ensureCustomPromptTaskTargetsAvailable(targetPaths); err != nil {
		detail.Status = "error"
		detail.Message = err.Error()
		return detail
	}

	if _, err := annotation.CopyEvidenceTree(ctx, item.SourcePath, plan.SourcePath); err != nil {
		_ = cleanupCustomPromptTaskTargets(targetPaths)
		detail.Status = "error"
		detail.Message = err.Error()
		return detail
	}
	successfulModels := []string{sourceModelName}

	projectConfigID := project.ID
	localPath := plan.TaskPath
	sourcePath := plan.SourcePath
	created, err := s.CreateTask(CreateTaskRequest{
		GitLabProjectID: item.QuestionID,
		ProjectName:     item.DisplayName,
		TaskType:        entry.TaskType,
		ClaimSequence:   &plan.Sequence,
		LocalPath:       &localPath,
		SourceModelName: &sourceModelName,
		SourceLocalPath: &sourcePath,
		Models:          successfulModels,
		ProjectConfigID: &projectConfigID,
	})
	if err != nil {
		_ = cleanupCustomPromptTaskTargets(targetPaths)
		detail.Status = "error"
		detail.Message = err.Error()
		return detail
	}
	detail.TaskID = created.ID

	startedAt := time.Now().Unix()
	if err := s.store.CompleteTaskPromptGenerationWithDifficulty(created.ID, entry.PromptText, entry.PromptDifficulty, startedAt); err != nil {
		detail.Status = "error"
		detail.Message = err.Error()
		return detail
	}
	if err := appprompt.SyncPromptArtifact(created.LocalPath, entry.PromptText); err != nil {
		detail.Status = "error"
		detail.Message = err.Error()
		return detail
	}
	if _, err := appannotation.New(s.store, nil).PrepareCaseWithContext(ctx, appannotation.PrepareRequest{TaskID: created.ID}); err != nil {
		detail.Status = "error"
		detail.Message = "初始快照准备失败：" + err.Error()
		if statusErr := s.UpdateTaskStatus(created.ID, "Error"); statusErr != nil {
			detail.Message += "；状态保存失败：" + statusErr.Error()
		}
		return detail
	}
	if _, err := appannotation.New(s.store, nil).PublishSnapshot(ctx, appannotation.PrepareRequest{TaskID: created.ID}); err != nil {
		detail.Message = "题目及本地初始快照已创建；GitHub 初始快照待发布：" + err.Error()
	}
	if detail.Message == "" {
		detail.Message = "已创建并发布 GitHub 初始快照"
	}
	return detail
}

func (s *TaskService) planCustomPromptDocumentClaims(project store.Project, item store.QuestionBankItem, entries []customPromptEntry) (map[string][]managedClaimPlan, error) {
	countsByType := make(map[string]int)
	for _, entry := range entries {
		countsByType[entry.TaskType]++
	}

	taskTypes := make([]string, 0, len(countsByType))
	for taskType := range countsByType {
		taskTypes = append(taskTypes, taskType)
	}
	sort.Strings(taskTypes)

	folderSequences, err := collectCustomPromptManagedFolderSequences(project.CloneBasePath, item.DisplayName)
	if err != nil {
		return nil, err
	}
	usedSequences := make(map[int]struct{}, len(folderSequences))
	for seq := range folderSequences {
		usedSequences[seq] = struct{}{}
	}

	result := make(map[string][]managedClaimPlan, len(taskTypes))
	for _, taskType := range taskTypes {
		taskSequences, err := s.collectCustomPromptTaskSequences(project.ID, item.QuestionID, taskType)
		if err != nil {
			return nil, err
		}
		for seq := range taskSequences {
			usedSequences[seq] = struct{}{}
		}
		sequences := resolveCustomPromptClaimSequences(usedSequences, countsByType[taskType])
		plans := make([]managedClaimPlan, 0, len(sequences))
		for _, sequence := range sequences {
			usedSequences[sequence] = struct{}{}
			taskPath := util.BuildManagedTaskFolderPathWithSequence(project.CloneBasePath, item.DisplayName, taskType, sequence)
			sourcePath := filepath.Join(taskPath, filepath.Base(taskPath))
			plans = append(plans, managedClaimPlan{
				Sequence:   sequence,
				TaskPath:   taskPath,
				SourcePath: sourcePath,
			})
		}
		result[taskType] = plans
	}
	return result, nil
}

func parseCustomPromptDocumentEntries(content string) []customPromptEntry {
	currentType := ""
	var entries []customPromptEntry
	var current *customPromptEntry

	flush := func() {
		if current == nil {
			return
		}
		current.PromptText = strings.TrimSpace(current.PromptText)
		if current.PromptText != "" {
			entries = append(entries, *current)
		}
		current = nil
	}

	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if matches := customPromptHeadingPattern.FindStringSubmatch(trimmed); len(matches) == 2 {
			flush()
			currentType = internalprompt.NormalizeTaskType(matches[1])
			continue
		}
		if currentType == "" {
			continue
		}
		if matches := customPromptItemPattern.FindStringSubmatch(line); len(matches) == 2 {
			flush()
			difficulty, promptText := splitCustomPromptDifficulty(matches[1])
			current = &customPromptEntry{
				TaskType:         currentType,
				PromptDifficulty: difficulty,
				PromptText:       promptText,
			}
			continue
		}
		if current != nil {
			current.PromptText = strings.TrimSpace(current.PromptText + "\n" + trimmed)
		}
	}
	flush()
	return entries
}

func splitCustomPromptDifficulty(value string) (string, string) {
	trimmed := strings.TrimSpace(value)
	if matches := customPromptDifficultyPat.FindStringSubmatch(trimmed); len(matches) == 3 {
		return strings.TrimSpace(matches[1]), strings.TrimSpace(matches[2])
	}
	return store.DefaultPromptDifficulty, trimmed
}

func validateCustomPromptDocumentBatch(entries []customPromptEntry) error {
	if len(entries) == 0 {
		return errors.New("提示词文档至少需要一条有效提示词")
	}
	counts := map[string]int{}
	for index, entry := range entries {
		kind := internalprompt.NormalizeTaskType(entry.TaskType)
		switch kind {
		case "0-1代码生成", "Feature迭代", "Bug修复", "代码理解", "工程化", "代码测试", "代码重构":
		default:
			return fmt.Errorf("不支持的题型：%s", kind)
		}
		switch strings.TrimSpace(entry.PromptDifficulty) {
		case "困难", "地狱":
		case "简单", "一般":
			return fmt.Errorf("第 %d 条 %s 题难度为“%s”，低于困难下限，简单和一般难度不再接收", index+1, kind, strings.TrimSpace(entry.PromptDifficulty))
		default:
			return fmt.Errorf("第 %d 条 %s 题难度不支持：%s", index+1, kind, entry.PromptDifficulty)
		}
		if kind == "代码理解" && !strings.Contains(strings.ToLower(entry.PromptText), "readme") {
			return errors.New("代码理解题必须明确要求生成 README 文档")
		}
		counts[kind]++
		switch kind {
		case "代码理解", "工程化", "代码测试", "代码重构":
			if counts[kind] > 1 {
				return fmt.Errorf("%s 每份文档最多 1 条", kind)
			}
		}
	}
	return nil
}

func ensureCustomPromptSourceDependencies(ctx context.Context, sourcePath string) error {
	expandedSource := util.ExpandTilde(util.NormalizePath(sourcePath))
	if !isNodeFrontendProject(expandedSource) {
		return nil
	}
	if pathExists(filepath.Join(expandedSource, "node_modules")) {
		return nil
	}

	manager, args, err := resolveNodeInstallCommand(expandedSource)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, manager, args...)
	cmd.Dir = expandedSource
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("源项目前端依赖安装失败：%w", err)
	}
	return nil
}

func isNodeFrontendProject(path string) bool {
	return pathExists(filepath.Join(path, "package.json"))
}

func resolveNodeInstallCommand(projectPath string) (string, []string, error) {
	type candidate struct {
		lockFile string
		binary   string
		args     []string
	}
	candidates := []candidate{
		{lockFile: "pnpm-lock.yaml", binary: "pnpm", args: []string{"install", "--frozen-lockfile"}},
		{lockFile: "yarn.lock", binary: "yarn", args: []string{"install", "--frozen-lockfile"}},
		{lockFile: "package-lock.json", binary: "npm", args: []string{"ci"}},
	}
	for _, current := range candidates {
		if !pathExists(filepath.Join(projectPath, current.lockFile)) {
			continue
		}
		bin, err := util.ResolveCLI(current.binary)
		if err != nil {
			return "", nil, fmt.Errorf("源项目存在 %s，但未找到 %s，无法自动安装依赖：%w", current.lockFile, current.binary, err)
		}
		return bin, current.args, nil
	}
	bin, err := util.ResolveCLI("npm")
	if err != nil {
		return "", nil, fmt.Errorf("源项目是前端项目但未找到 npm，无法自动安装依赖：%w", err)
	}
	return bin, []string{"install"}, nil
}

func inferCustomPromptDocumentProjectName(path string) string {
	base := strings.TrimSuffix(filepath.Base(strings.TrimSpace(path)), filepath.Ext(path))
	marker := "_提示词_"
	if idx := strings.LastIndex(base, marker); idx > 0 {
		return strings.TrimSpace(base[:idx])
	}
	return strings.TrimSpace(base)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func parseTaskProjectModels(rawModels, sourceModelFolder string) []string {
	sourceModelName := strings.TrimSpace(sourceModelFolder)
	if sourceModelName == "" {
		sourceModelName = "ORIGIN"
	}
	seen := map[string]struct{}{strings.ToLower(sourceModelName): {}}
	models := []string{sourceModelName}
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n", ",", "\n").Replace(rawModels)
	for _, segment := range strings.Split(normalized, "\n") {
		modelName := strings.TrimSpace(segment)
		if modelName == "" {
			continue
		}
		key := strings.ToLower(modelName)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		models = append(models, modelName)
	}
	return models
}

func isCustomPromptQuestionBankItem(item store.QuestionBankItem) bool {
	return strings.EqualFold(strings.TrimSpace(item.SourceKind), "local_directory") &&
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(item.OriginRef)), "custom:")
}

func collectCustomPromptManagedFolderSequences(basePath, projectName string) (map[int]struct{}, error) {
	entries, err := os.ReadDir(util.ExpandTilde(basePath))
	if err != nil {
		if os.IsNotExist(err) {
			return map[int]struct{}{}, nil
		}
		return nil, err
	}
	prefix := util.NormalizeManagedProjectFolderName(projectName) + "-"
	sequences := make(map[int]struct{})
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		lastDash := strings.LastIndex(name, "-")
		if lastDash <= len(prefix)-1 {
			sequences[1] = struct{}{}
			continue
		}
		seq, err := strconv.Atoi(name[lastDash+1:])
		if err != nil || seq <= 0 {
			sequences[1] = struct{}{}
			continue
		}
		sequences[seq] = struct{}{}
	}
	return sequences, nil
}

func (s *TaskService) collectCustomPromptTaskSequences(projectConfigID string, questionID int64, taskType string) (map[int]struct{}, error) {
	tasks, err := s.store.ListTasks(&projectConfigID)
	if err != nil {
		return nil, err
	}
	normalizedType := internalprompt.NormalizeTaskType(taskType)
	sequences := make(map[int]struct{})
	for _, task := range tasks {
		if task.GitLabProjectID != questionID {
			continue
		}
		if internalprompt.NormalizeTaskType(task.TaskType) != normalizedType {
			continue
		}
		seq := 1
		if task.LocalPath != nil {
			if parsed, ok := parseManagedTaskClaimSequence(*task.LocalPath, task.ProjectName, task.TaskType); ok {
				seq = parsed
			}
		}
		if seq <= 1 && strings.TrimSpace(task.ID) != "" {
			if parsed, ok := parseCustomPromptClaimSequenceFromTaskID(task.ID); ok {
				seq = parsed
			}
		}
		sequences[seq] = struct{}{}
	}
	return sequences, nil
}

func parseCustomPromptClaimSequenceFromTaskID(taskID string) (int, bool) {
	trimmed := strings.TrimSpace(taskID)
	if trimmed == "" {
		return 0, false
	}
	if idx := strings.LastIndex(trimmed, "__"); idx >= 0 {
		trimmed = trimmed[idx+2:]
	}
	parts := strings.Split(trimmed, "-")
	if len(parts) < 2 || !strings.EqualFold(parts[0], "label") {
		return 0, false
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		return 0, false
	}
	if len(parts) == 2 {
		return 1, true
	}
	seq, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || seq <= 0 {
		return 0, false
	}
	return seq, true
}

func resolveCustomPromptClaimSequences(used map[int]struct{}, count int) []int {
	result := make([]int, 0, count)
	for seq := 1; len(result) < count; seq++ {
		if _, exists := used[seq]; exists {
			continue
		}
		result = append(result, seq)
	}
	return result
}

func ensureCustomPromptTaskTargetsAvailable(paths []string) error {
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if _, err := os.Stat(util.ExpandTilde(trimmed)); err == nil {
			return fmt.Errorf(errs.FmtTargetDirExists, filepath.Base(trimmed))
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func cleanupCustomPromptTaskTargets(paths []string) error {
	var errsJoined error
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if err := os.RemoveAll(util.ExpandTilde(path)); err != nil {
			errsJoined = errors.Join(errsJoined, err)
		}
	}
	return errsJoined
}
