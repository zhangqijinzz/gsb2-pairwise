package annotation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	domain "github.com/blueship581/pinru/internal/annotation"
	"github.com/google/uuid"
)

type PairwiseExportRequest struct {
	TaskIDs     []string `json:"taskIds"`
	ProjectID   string   `json:"projectId"`
	Submitter   string   `json:"submitter"`
	SubmittedAt string   `json:"submittedAt"`
}

func (s *AnnotationService) PreflightPairwise(projectID string) (domain.Report, error) {
	cases, err := s.ListCases(projectID)
	if err != nil {
		return domain.Report{}, err
	}
	return s.preflightPairwise(context.Background(), cases), nil
}

func (s *AnnotationService) preflightPairwise(ctx context.Context, cases []domain.Case) domain.Report {
	report := preflightPairwiseCases(cases)
	verify := s.pairwiseRemoteVerifier()
	for _, c := range cases {
		if c.Mode != domain.CaseModePairwiseGSB || len(domain.ValidatePairwiseCase(c, true)) > 0 {
			continue
		}
		if err := verify(ctx, c); err != nil {
			report.Ready--
			report.Issues = append(report.Issues, c.TaskName+"："+err.Error())
		}
	}
	return report
}

func preflightPairwiseCases(cases []domain.Case) domain.Report {
	report := domain.Report{Issues: []string{}}
	for _, c := range cases {
		if c.Mode != domain.CaseModePairwiseGSB {
			continue
		}
		report.Tasks++
		report.Rounds++
		issues := domain.ValidatePairwiseCase(c, true)
		if len(issues) == 0 {
			report.Ready++
			continue
		}
		for _, issue := range issues {
			report.Issues = append(report.Issues, c.TaskName+"："+issue)
		}
	}
	if report.Tasks == 0 {
		report.Issues = append(report.Issues, "当前批次没有 Pair-wise GSB 题目")
	}
	return report
}

func (s *AnnotationService) ExportPairwise(req PairwiseExportRequest) (*ExportResult, error) {
	return s.exportPairwise(context.Background(), req)
}

func (s *AnnotationService) exportPairwise(ctx context.Context, req PairwiseExportRequest) (*ExportResult, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return nil, errors.New("请选择项目批次")
	}
	selected := make(map[string]struct{}, len(req.TaskIDs))
	for _, taskID := range req.TaskIDs {
		if taskID = strings.TrimSpace(taskID); taskID != "" {
			selected[taskID] = struct{}{}
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("请至少选择一道要导出的 GSB 题目")
	}
	unlock, err := s.lockTask("pairwise-export:" + req.ProjectID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	all, err := s.ListCases(req.ProjectID)
	if err != nil {
		return nil, err
	}
	cases := make([]domain.Case, 0, len(all))
	skipped := make([]string, 0)
	found := make(map[string]bool, len(selected))
	for _, c := range all {
		if c.Mode != domain.CaseModePairwiseGSB {
			continue
		}
		if _, ok := selected[c.TaskID]; !ok {
			continue
		}
		found[c.TaskID] = true
		copy := c
		populatePairwiseMetadata(&copy)
		current := domain.CurrentPairwiseReview(copy)
		if current == nil || strings.TrimSpace(current.Reason) == "" {
			skipped = append(skipped, copy.TaskName+"：尚无当前有效的 GSB 结论和理由")
			continue
		}
		if domain.PairwiseReasonHasDecorativeBrackets(current.Reason) {
			skipped = append(skipped, copy.TaskName+"：GSB 理由包含"+domain.PairwiseReasonDecorationLabel+"，请重新审核")
			continue
		}
		if issues := domain.ValidatePairwiseCase(copy, false); len(issues) > 0 {
			skipped = append(skipped, copy.TaskName+"："+strings.Join(issues, "；"))
			continue
		}
		if err := s.pairwiseRemoteVerifier()(ctx, copy); err != nil {
			skipped = append(skipped, copy.TaskName+"："+err.Error())
			continue
		}
		pairwise := domain.NewPairwiseData("")
		if copy.Pairwise != nil {
			value := *copy.Pairwise
			pairwise = &value
		}
		pairwise.Reviews = []domain.PairwiseReview{*current}
		copy.Pairwise = pairwise
		cases = append(cases, copy)
	}
	for taskID := range selected {
		if !found[taskID] {
			return nil, fmt.Errorf("所选题目 %s 不属于当前项目或尚未启用 Pair-wise GSB", taskID)
		}
	}
	if len(cases) == 0 {
		message := "当前范围没有已完成 GSB 审核且理由完整的题目"
		if len(skipped) > 0 {
			message += "：" + strings.Join(skipped, "；")
		}
		return nil, errors.New(message)
	}
	report := domain.Report{Tasks: len(cases), Rounds: len(cases), Ready: len(cases), Issues: skipped}

	project, err := s.store.GetProject(req.ProjectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, errors.New("项目不存在")
	}
	assets, err := MaterializeAssets(s.root)
	if err != nil {
		return nil, err
	}
	root, err := s.exportDirectory()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, time.Now().Format("20060102-150405")+"-pairwise-"+uuid.NewString())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	input := struct {
		ProjectName string        `json:"projectName"`
		Submitter   string        `json:"submitter"`
		SubmittedAt string        `json:"submittedAt"`
		Cases       []domain.Case `json:"cases"`
		Issues      []string      `json:"issues"`
	}{project.Name, req.Submitter, req.SubmittedAt, cases, report.Issues}
	raw, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return nil, err
	}
	inputPath := filepath.Join(dir, "pairwise-input.json")
	if err := os.WriteFile(inputPath, raw, 0o600); err != nil {
		return nil, err
	}
	args := []string{filepath.Join(assets, "export_pairwise.py"), "--input", inputPath, "--output", dir}
	out, err := s.command(ctx, dir, "python3", args...)
	if err != nil {
		return nil, err
	}
	var result ExportResult
	if err := json.Unmarshal(bytesLastJSON(out), &result); err != nil {
		return nil, fmt.Errorf("读取 Pair-wise 导出结果失败：%w", err)
	}
	if result.OutputPath == "" {
		return nil, errors.New("Pair-wise 导出未生成 Excel 路径")
	}
	if _, err := os.Stat(result.OutputPath); err != nil {
		return nil, err
	}
	return &result, nil
}
