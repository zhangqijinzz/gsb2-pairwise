package annotation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	domain "github.com/blueship581/pinru/internal/annotation"
	"github.com/blueship581/pinru/internal/store"
	"github.com/google/uuid"
)

const batchItemTimeout = 30 * time.Minute
const defaultBatchReviewConcurrency = 4

func (s *AnnotationService) batchReviewConcurrency(total int) int {
	limit := defaultBatchReviewConcurrency
	if raw, err := s.store.GetConfig("annotation_review_concurrency"); err == nil {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(raw)); parseErr == nil {
			limit = parsed
		}
	}
	if limit < 2 {
		limit = 2
	}
	if limit > 6 {
		limit = 6
	}
	if total > 0 && limit > total {
		limit = total
	}
	return limit
}

func latestReadyEvaluation(round domain.Round) *domain.Evaluation {
	if len(round.Evaluations) == 0 {
		return nil
	}
	e := &round.Evaluations[len(round.Evaluations)-1]
	if round.Status != "complete" || e.EvidenceHash != round.EvidenceHash || (e.Current != nil && !*e.Current) || !domain.IsCollectableEvaluation(*e) {
		return nil
	}
	return e
}

func readyRoundCount(c domain.Case) int {
	count := 0
	for _, round := range c.Rounds {
		if round.Status == "excluded" || domain.IsPureRecoveryRound(round) {
			continue
		}
		if latestReadyEvaluation(round) != nil {
			count++
		}
	}
	return count
}

func fullyPreparedWithoutRepair(c domain.Case) bool {
	total := 0
	for _, round := range c.Rounds {
		if domain.IsPureRecoveryRound(round) {
			// Legacy parsers persisted resume commands as independent rounds. The
			// original round must be recaptured so its evidence reaches the final
			// resumed state before it can be considered prepared.
			return false
		}
		if round.Status == "excluded" {
			continue
		}
		total++
		e := latestReadyEvaluation(round)
		if e == nil || strings.TrimSpace(e.NextPrompt) != "" {
			return false
		}
	}
	return total > 0
}

func (s *AnnotationService) activePreparation(taskID string) (bool, error) {
	jobs, err := s.store.ListBackgroundJobs(&store.JobFilter{TaskID: &taskID})
	if err != nil {
		return false, err
	}
	for _, job := range jobs {
		if (job.Status == "pending" || job.Status == "running") &&
			(job.JobType == "annotation_capture_table" || job.JobType == "annotation_resume" || job.JobType == "annotation_review") {
			return true, nil
		}
	}
	return false, nil
}

func (s *AnnotationService) createBatchChild(taskID string) (string, error) {
	id := uuid.NewString()
	message := "等待批量制表"
	payload, _ := json.Marshal(CaptureRequest{TaskID: taskID})
	err := s.store.CreateBackgroundJob(store.BackgroundJob{
		ID: id, JobType: "annotation_capture_table", TaskID: &taskID, Status: "pending",
		Progress: 0, ProgressMessage: &message, InputPayload: string(payload), MaxRetries: 1,
		TimeoutSeconds: int(batchItemTimeout.Seconds()), CreatedAt: time.Now().Unix(),
	})
	return id, err
}

func (s *AnnotationService) recordBatchUnavailable(taskID, reason string) error {
	id, err := s.createBatchChild(taskID)
	if err != nil {
		return err
	}
	if err := s.store.StartBackgroundJob(id); err != nil {
		return err
	}
	return s.store.FailBackgroundJob(id, reason)
}

func (s *AnnotationService) recordBatchSkipped(c domain.Case, message string) error {
	id, err := s.createBatchChild(c.TaskID)
	if err != nil {
		return err
	}
	if err := s.store.StartBackgroundJob(id); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	payload := string(raw)
	return s.store.CompleteBackgroundJobWithMessage(id, &payload, &message)
}

func (s *AnnotationService) batchInput(ctx context.Context, c domain.Case) (string, bool, error) {
	// A repair prompt means the last review found a code defect. Re-read the live
	// trace so a newly completed repair round can be discovered.
	needsLiveCapture := false
	for _, round := range c.Rounds {
		if domain.IsPureRecoveryRound(round) {
			needsLiveCapture = true
			continue
		}
		if e := latestReadyEvaluation(round); e != nil && strings.TrimSpace(e.NextPrompt) != "" {
			needsLiveCapture = true
		}
	}
	if len(c.Captures) > 0 && !needsLiveCapture {
		return "", true, nil
	}
	if strings.TrimSpace(c.TracePath) != "" {
		return c.TracePath, false, nil
	}
	if c.ContainerID == "" {
		return "", false, errors.New("尚未绑定容器，且没有已保存的轨迹证据")
	}
	traces, err := s.listTraces(ctx, c.TaskID)
	if err != nil {
		return "", false, err
	}
	if len(traces) == 0 {
		return "", false, errors.New("容器中没有可采集的 Claude Code JSONL 轨迹")
	}
	if len(traces) > 1 {
		return "", false, fmt.Errorf("容器中发现 %d 个候选轨迹，请先在题目详情选择正确轨迹", len(traces))
	}
	return traces[0].Path, false, nil
}

func (s *AnnotationService) prepareBatchCase(ctx context.Context, c domain.Case, report func(int, string)) BatchPrepareItem {
	item := BatchPrepareItem{TaskID: c.TaskID, TaskName: c.TaskName}
	fail := func(err error) BatchPrepareItem {
		item.Status, item.Message = "failed", err.Error()
		return item
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	report(0, "正在检查")
	if fullyPreparedWithoutRepair(c) {
		item.Status, item.Message = "skipped", "已有完整且有效的制表数据"
		if err := s.recordBatchSkipped(c, item.Message); err != nil {
			return fail(err)
		}
		report(100, item.Message)
		return item
	}
	active, err := s.activePreparation(c.TaskID)
	if err != nil {
		return fail(err)
	}
	if active {
		item.Status, item.Message = "skipped", "该题已有制表任务正在运行"
		report(100, item.Message)
		return item
	}
	tracePath, resume, err := s.batchInput(ctx, c)
	if err != nil {
		item.Status, item.Message = "skipped", err.Error()
		if childErr := s.recordBatchUnavailable(c.TaskID, item.Message); childErr != nil {
			return fail(childErr)
		}
		report(100, item.Message)
		return item
	}

	childID, err := s.createBatchChild(c.TaskID)
	if err != nil {
		return fail(err)
	}
	if err := s.store.StartBackgroundJob(childID); err != nil {
		return fail(err)
	}
	itemCtx, cancel := context.WithTimeout(ctx, batchItemTimeout)
	defer cancel()
	itemCtx = domain.WithProgress(itemCtx, func(progress int, message string) {
		_ = s.store.UpdateBackgroundJobProgress(childID, progress, message)
		report(progress, message)
	})
	before := readyRoundCount(c)
	var updated *domain.Case
	if resume {
		updated, err = s.resumeTable(itemCtx, c.TaskID)
	} else {
		updated, err = s.captureAndPrepareTable(itemCtx, CaptureRequest{TaskID: c.TaskID, TracePath: tracePath})
	}
	if err != nil {
		if ctx.Err() != nil {
			_ = s.store.CancelBackgroundJob(childID)
		} else {
			_ = s.store.FailBackgroundJob(childID, err.Error())
		}
		return fail(err)
	}
	raw, err := json.Marshal(updated)
	if err != nil {
		_ = s.store.FailBackgroundJob(childID, err.Error())
		return fail(err)
	}
	payload := string(raw)
	message := "制表数据已准备"
	if err := s.store.CompleteBackgroundJobWithMessage(childID, &payload, &message); err != nil {
		return fail(err)
	}
	if readyRoundCount(*updated) > before {
		item.Status, item.Message = "prepared", message
	} else {
		item.Status, item.Message = "skipped", "轨迹没有新增有效轮次，已复用现有评分"
	}
	report(100, item.Message)
	return item
}

func (s *AnnotationService) batchPrepareCases(req BatchPrepareRequest) ([]domain.Case, error) {
	if len(req.TaskIDs) > 0 {
		_, skillHash, err := s.reviewSkill(context.Background())
		if err != nil {
			return nil, err
		}
		reviewExecution, modelErr := s.reviewExecution()
		seen := make(map[string]struct{}, len(req.TaskIDs))
		cases := make([]domain.Case, 0, len(req.TaskIDs))
		for _, rawID := range req.TaskIDs {
			taskID := strings.TrimSpace(rawID)
			if taskID == "" {
				continue
			}
			if _, exists := seen[taskID]; exists {
				continue
			}
			seen[taskID] = struct{}{}
			current, err := s.loadCase(taskID)
			if err != nil {
				return nil, fmt.Errorf("加载题目 %s 失败：%w", taskID, err)
			}
			markEvaluationFreshness(current, skillHash, reviewExecution.Label, modelErr == nil)
			cases = append(cases, *current)
		}
		if len(cases) == 0 {
			return nil, errors.New("请选择至少一道题目")
		}
		return cases, nil
	}

	if strings.TrimSpace(req.ProjectID) == "" {
		return nil, errors.New("请选择至少一道题目")
	}
	return s.ListCases(req.ProjectID)
}

func (s *AnnotationService) batchCaptureAndPrepareTable(ctx context.Context, req BatchPrepareRequest) (*BatchPrepareResult, error) {
	cases, err := s.batchPrepareCases(req)
	if err != nil {
		return nil, err
	}
	result := &BatchPrepareResult{Total: len(cases), Items: make([]BatchPrepareItem, len(cases))}
	if len(cases) == 0 {
		domain.ReportProgress(ctx, 100, "当前项目没有可审核的题目")
		return result, nil
	}

	progresses := make([]int, len(cases))
	var progressMu sync.Mutex
	report := func(index int, c domain.Case, progress int, message string) {
		progressMu.Lock()
		progresses[index] = progress
		totalProgress := 0
		completed := 0
		for _, value := range progresses {
			totalProgress += value
			if value == 100 {
				completed++
			}
		}
		progressMu.Unlock()
		domain.ReportProgress(ctx, totalProgress/len(cases), fmt.Sprintf("已完成 %d/%d 题 · %s：%s", completed, len(cases), c.TaskName, message))
	}

	var wg sync.WaitGroup
	indices := make(chan int)
	workers := s.batchReviewConcurrency(len(cases))
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range indices {
				current := cases[index]
				result.Items[index] = s.prepareBatchCase(ctx, current, func(progress int, message string) {
					report(index, current, progress, message)
				})
			}
		}()
	}
	for index := range cases {
		select {
		case <-ctx.Done():
			close(indices)
			wg.Wait()
			return result, ctx.Err()
		case indices <- index:
		}
	}
	close(indices)
	wg.Wait()
	for _, item := range result.Items {
		switch item.Status {
		case "prepared":
			result.Prepared++
		case "skipped":
			result.Skipped++
		default:
			result.Failed++
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	domain.ReportProgress(ctx, 100, fmt.Sprintf("批量制表完成：新准备 %d 题，跳过 %d 题，失败 %d 题", result.Prepared, result.Skipped, result.Failed))
	return result, nil
}
