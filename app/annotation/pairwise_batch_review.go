package annotation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	domain "github.com/blueship581/pinru/internal/annotation"
)

type PairwiseBatchReviewRequest struct {
	ProjectID string   `json:"projectId"`
	TaskIDs   []string `json:"taskIds,omitempty"`
	Force     bool     `json:"force"`
}

type PairwiseBatchReviewItem struct {
	TaskID   string `json:"taskId"`
	TaskName string `json:"taskName"`
	Status   string `json:"status"`
	Message  string `json:"message"`
}

type PairwiseBatchReviewResult struct {
	Total    int                       `json:"total"`
	Reviewed int                       `json:"reviewed"`
	Reused   int                       `json:"reused"`
	Skipped  int                       `json:"skipped"`
	Failed   int                       `json:"failed"`
	Items    []PairwiseBatchReviewItem `json:"items"`
}

func (s *AnnotationService) pairwiseBatchCases(req PairwiseBatchReviewRequest) ([]domain.Case, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return nil, errors.New("请选择项目批次")
	}
	all, err := s.ListCases(req.ProjectID)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]struct{}, len(req.TaskIDs))
	for _, id := range req.TaskIDs {
		if id = strings.TrimSpace(id); id != "" {
			selected[id] = struct{}{}
		}
	}
	cases := make([]domain.Case, 0, len(all))
	for _, c := range all {
		if c.Mode != domain.CaseModePairwiseGSB {
			continue
		}
		if len(selected) > 0 {
			if _, ok := selected[c.TaskID]; !ok {
				continue
			}
		}
		cases = append(cases, c)
	}
	return cases, nil
}

func (s *AnnotationService) BatchReviewPairwise(ctx context.Context, req PairwiseBatchReviewRequest) (*PairwiseBatchReviewResult, error) {
	cases, err := s.pairwiseBatchCases(req)
	if err != nil {
		return nil, err
	}
	result := &PairwiseBatchReviewResult{Total: len(cases), Items: make([]PairwiseBatchReviewItem, len(cases))}
	if len(cases) == 0 {
		domain.ReportProgress(ctx, 100, "当前项目没有 Pair-wise GSB 题目")
		return result, nil
	}
	execution, err := s.reviewExecution()
	if err != nil {
		return nil, err
	}
	progresses := make([]int, len(cases))
	var progressMu sync.Mutex
	report := func(index, progress int, message string) {
		progressMu.Lock()
		progresses[index] = progress
		total, completed := 0, 0
		for _, value := range progresses {
			total += value
			if value == 100 {
				completed++
			}
		}
		progressMu.Unlock()
		domain.ReportProgress(ctx, total/len(cases), fmt.Sprintf("已完成 %d/%d 题 · %s", completed, len(cases), message))
	}

	indices := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < s.batchReviewConcurrency(len(cases)); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range indices {
				c := cases[index]
				item := PairwiseBatchReviewItem{TaskID: c.TaskID, TaskName: c.TaskName}
				if issues := domain.ValidatePairwiseCase(c, false); len(issues) > 0 {
					item.Status, item.Message = "skipped", issues[0]
					result.Items[index] = item
					report(index, 100, c.TaskName+"："+item.Message)
					continue
				}
				if current := domain.CurrentPairwiseReview(c); current != nil && current.Model == execution.Label && current.SkillHash == pairwiseReviewSkillHash() && !req.Force {
					item.Status, item.Message = "reused", "已复用与当前 A/B 证据一致的 GSB"
					result.Items[index] = item
					report(index, 100, c.TaskName+"："+item.Message)
					continue
				}
				report(index, 10, c.TaskName+"：正在对比 A/B 轨迹与产物")
				itemCtx := domain.WithProgress(ctx, func(progress int, message string) {
					report(index, progress, c.TaskName+"："+message)
				})
				if _, reviewErr := s.ReviewPairwise(itemCtx, PairwiseReviewRequest{TaskID: c.TaskID, Force: req.Force}); reviewErr != nil {
					item.Status, item.Message = "failed", reviewErr.Error()
				} else {
					item.Status, item.Message = "reviewed", "Pair-wise GSB 已保存"
				}
				result.Items[index] = item
				report(index, 100, c.TaskName+"："+item.Message)
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
		case "reviewed":
			result.Reviewed++
		case "reused":
			result.Reused++
		case "skipped":
			result.Skipped++
		default:
			result.Failed++
		}
	}
	domain.ReportProgress(ctx, 100, fmt.Sprintf("批量 GSB 完成：新审核 %d 题，复用 %d 题，跳过 %d 题，失败 %d 题", result.Reviewed, result.Reused, result.Skipped, result.Failed))
	return result, nil
}
