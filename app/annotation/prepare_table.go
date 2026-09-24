package annotation

import (
	"context"
	"errors"
	"fmt"

	domain "github.com/blueship581/pinru/internal/annotation"
)

// captureAndPrepareTable persists each result separately so a cancelled or failed
// review can resume from the saved capture and evaluations. It never runs a prompt.
func (s *AnnotationService) captureAndPrepareTable(ctx context.Context, req CaptureRequest) (*domain.Case, error) {
	unlock, err := s.lockTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	domain.ReportProgress(ctx, 5, "正在采集轨迹与代码快照")
	c, err := s.captureLocked(ctx, req)
	if err != nil {
		return nil, err
	}
	return s.prepareCapturedRounds(ctx, c)
}

// Resume uses frozen evidence, so a retry never requires a running container.
func (s *AnnotationService) resumeTable(ctx context.Context, taskID string) (*domain.Case, error) {
	unlock, err := s.lockTask(taskID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c, err := s.loadCase(taskID)
	if err != nil {
		return nil, err
	}
	return s.prepareCapturedRounds(ctx, c)
}

func (s *AnnotationService) prepareCapturedRounds(ctx context.Context, c *domain.Case) (*domain.Case, error) {
	var err error
	var rounds []domain.Round
	for _, r := range c.Rounds {
		if r.Status == "complete" && !domain.IsPureRecoveryRound(r) {
			rounds = append(rounds, r)
		}
	}
	if len(rounds) == 0 {
		return nil, errors.New("采集已保存，但没有已完成的有效轮次可供准备制表数据")
	}
	for i, r := range rounds {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		domain.ReportProgress(ctx, 15+80*i/len(rounds), fmt.Sprintf("轨迹已采集，正在使用 skill 准备制表数据：第 %d 轮（%d/%d）", r.Order, i+1, len(rounds)))
		roundCtx := domain.WithProgress(ctx, func(percent int, message string) {
			domain.ReportProgress(ctx, 15+(80*i+80*percent/100)/len(rounds), fmt.Sprintf("第 %d 轮（%d/%d）：%s", r.Order, i+1, len(rounds), message))
		})
		c, err = s.reviewLocked(roundCtx, ReviewRequest{TaskID: c.TaskID, PromptID: r.PromptID})
		if err != nil {
			return nil, fmt.Errorf("采集已保存，第 %d 轮制表数据准备失败（已完成的评分保留）：%w", r.Order, err)
		}
	}
	return c, nil
}
