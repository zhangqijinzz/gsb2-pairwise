package annotation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	domain "github.com/blueship581/pinru/internal/annotation"
	"github.com/google/uuid"
)

var fullSHA = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

func snapshotSHA(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("快照须为 GitHub HTTPS commit 完整链接")
	}
	p := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(p) != 4 || p[0] == "" || p[1] == "" || p[2] != "commit" || !fullSHA.MatchString(p[3]) {
		return "", errors.New("快照链接必须包含完整 40 位 SHA")
	}
	return strings.ToLower(p[3]), nil
}

func verifyRemoteSnapshot(ctx context.Context, value string) error {
	sha, err := snapshotSHA(value)
	if err != nil {
		return err
	}
	u, _ := url.Parse(value)
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	endpoint := "https://api.github.com/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/commits/" + sha
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || req.URL.Scheme != "https" || req.URL.Host != "api.github.com" {
			return errors.New("快照验证重定向无效")
		}
		return nil
	}}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("快照访问返回 %d（未确认接收方可访问）", response.StatusCode)
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(response.Body).Decode(&commit); err != nil {
		return err
	}
	if commit.SHA != sha {
		return errors.New("远端提交与初始 SHA 不一致")
	}
	return nil
}

// Preflight checks the whole selected project rather than filtering by scores.
func (s *AnnotationService) Preflight(projectID string) (domain.Report, error) {
	cases, err := s.ListCases(projectID)
	if err != nil {
		return domain.Report{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return s.preflight(ctx, cases), nil
}

func (s *AnnotationService) preflight(ctx context.Context, cases []domain.Case) domain.Report {
	report := domain.Preflight(cases)
	if len(cases) == 0 {
		report.Issues = append(report.Issues, "当前批次没有题目")
	}
	_, skillHash, skillErr := s.reviewSkill(ctx)
	if skillErr != nil {
		report.Issues = append(report.Issues, "读取审核技能失败："+skillErr.Error())
	}
	urls := map[string]bool{}
	for _, c := range cases {
		if c.SnapshotURL != "" {
			urls[c.SnapshotURL] = true
		}
		for _, r := range c.Rounds {
			if r.Status == "excluded" {
				continue
			}
			for i := len(r.Evaluations) - 1; i >= 0; i-- {
				e := r.Evaluations[i]
				if e.EvidenceHash == r.EvidenceHash {
					if e.Current != nil && !*e.Current {
						report.Issues = append(report.Issues, fmt.Sprintf("%s 第 %d 轮审核配置或证据已变化，请重新审核", c.TaskName, r.Order))
					}
					var capture *domain.Capture
					for i := range c.Captures {
						if c.Captures[i].ID == r.CaptureID {
							capture = &c.Captures[i]
							break
						}
					}
					if capture == nil && len(c.Captures) > 0 {
						capture = &c.Captures[len(c.Captures)-1]
					}
					if capture == nil || e.SourceHash != stableKey(capture.Hash+":"+capture.TraceHash) {
						report.Issues = append(report.Issues, c.TaskName+"：审核对应的代码或轨迹附件版本已变化，请重新审核")
					}
					if err := verifyReviewArtifacts(ctx, e); err != nil {
						report.Issues = append(report.Issues, c.TaskName+"："+err.Error())
					}
					if e.SkillHash != skillHash {
						report.Issues = append(report.Issues, fmt.Sprintf("%s 第 %d 轮规则版本已变化，请重新审核", c.TaskName, r.Order))
					}
					break
				}
			}
		}
		for _, cap := range c.Captures {
			if err := verifyTraceArtifacts(ctx, cap); err != nil {
				report.Issues = append(report.Issues, c.TaskName+"："+err.Error())
			}
			hash, err := domain.TreeHash(ctx, cap.CodePath)
			if err != nil || hash != cap.Hash {
				report.Issues = append(report.Issues, c.TaskName+"：代码证据缺失或已修改")
			}
			raw, err := os.ReadFile(cap.TracePath)
			if err != nil {
				report.Issues = append(report.Issues, c.TaskName+"：原始轨迹文件缺失")
				continue
			}
			actual, err := domain.ParseTrace(raw)
			if err != nil {
				report.Issues = append(report.Issues, c.TaskName+"：原始轨迹无法解析")
				continue
			}
			for _, r := range c.Rounds {
				if r.CaptureID != cap.ID {
					continue
				}
				match := false
				for _, a := range actual {
					if a.PromptID == r.PromptID && a.SessionID == r.SessionID && a.EvidenceHash == r.EvidenceHash {
						match = true
						break
					}
				}
				if !match {
					report.Issues = append(report.Issues, c.TaskName+"：轮次与原始轨迹不一致")
				}
			}
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, 4)
	for value := range urls {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				report.Issues = append(report.Issues, "快照访问验证超时")
				mu.Unlock()
				return
			}
			defer func() { <-sem }()
			if err := s.verifySnapshot(ctx, value); err != nil {
				mu.Lock()
				report.Issues = append(report.Issues, value+"："+err.Error())
				mu.Unlock()
			}
		}(value)
	}
	wg.Wait()
	if len(report.Issues) > 0 {
		report.Ready = 0
	}
	return report
}

// Export freezes all cases and emits one workbook with original trace attachments.
func (s *AnnotationService) Export(req ExportRequest) (*ExportResult, error) {
	return s.export(context.Background(), req)
}
func (s *AnnotationService) export(ctx context.Context, req ExportRequest) (*ExportResult, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return nil, errors.New("请选择项目批次")
	}
	unlock, err := s.lockTask("export:" + req.ProjectID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	cases, err := s.ListCases(req.ProjectID)
	if err != nil {
		return nil, err
	}
	cases, err = selectExportCases(cases, req)
	if err != nil {
		return nil, err
	}
	report := s.preflight(ctx, cases)
	if report.Ready == 0 && report.NotCollected > 0 && len(report.Issues) == 0 {
		return nil, fmt.Errorf("所选评价五维总分均超过 %d，不符合平台收录规则；评分已按真实结果保留", domain.MaxCollectableScoreTotal)
	}

	if strings.TrimSpace(req.Submitter) == "" {
		report.Issues = append(report.Issues, "提交人未填写")
	}
	if !req.Draft && len(report.Issues) > 0 {
		return nil, fmt.Errorf("本批仍有 %d 项待处理，请先补齐或导出待补材料：%s", len(report.Issues), strings.Join(report.Issues, "；"))
	}
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
	dir := filepath.Join(root, time.Now().Format("20060102-150405")+"-"+uuid.NewString())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	input := struct {
		SeparateTasks bool          `json:"separateTasks"`
		ProjectName   string        `json:"projectName"`
		Submitter     string        `json:"submitter"`
		SubmittedAt   string        `json:"submittedAt"`
		Cases         []domain.Case `json:"cases"`
		Issues        []string      `json:"issues"`
	}{req.ReviewedOnly, project.Name, req.Submitter, req.SubmittedAt, cases, report.Issues}
	raw, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return nil, err
	}
	inputPath := filepath.Join(dir, "batch-input.json")
	if err := os.WriteFile(inputPath, raw, 0600); err != nil {
		return nil, err
	}
	args := []string{filepath.Join(assets, "export_satisfaction.py"), "--input", inputPath, "--output", dir}
	if req.Draft {
		args = append(args, "--draft")
	}
	out, err := s.command(ctx, dir, "python3", args...)
	if err != nil {
		return nil, err
	}
	var result ExportResult
	if err := json.Unmarshal(bytesLastJSON(out), &result); err != nil {
		return nil, fmt.Errorf("读取导出结果失败：%w", err)
	}
	if result.OutputPath == "" {
		return nil, errors.New("导出未生成 Excel 路径")
	}
	if _, err := os.Stat(result.OutputPath); err != nil {
		return nil, err
	}
	return &result, nil
}

func bytesLastJSON(data []byte) []byte {
	if json.Valid(data) {
		return data
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if json.Valid([]byte(lines[i])) {
			return []byte(lines[i])
		}
	}
	return data
}

func selectExportCases(cases []domain.Case, req ExportRequest) ([]domain.Case, error) {
	wanted := make(map[string]bool)
	if req.TaskIDs != nil {
		if len(req.TaskIDs) == 0 || req.TaskID != "" || !req.ReviewedOnly {
			return nil, errors.New("请选择至少一道已制表题目，指定题目导出不能同时指定单题范围")
		}
		for _, id := range req.TaskIDs {
			if strings.TrimSpace(id) == "" {
				return nil, errors.New("导出题目编号不能为空")
			}
			wanted[id] = true
		}
	}
	selected := make([]domain.Case, 0, len(cases))
	found := req.TaskID == ""
	totalOverLimit := 0
	for _, c := range cases {
		if req.TaskIDs != nil && !wanted[c.TaskID] {
			continue
		}
		if req.TaskID != "" && req.TaskID != c.TaskID {
			continue
		}
		found = true
		for _, round := range c.Rounds {
			if domain.IsPureRecoveryRound(round) {
				return nil, fmt.Errorf("题目 %s 仍有旧版拆分的恢复指令，请先重新采集并准备制表数据后再导出", c.TaskName)
			}
		}
		rounds := make([]domain.Round, 0, len(c.Rounds))
		overLimit := 0
		for _, r := range c.Rounds {
			var latest *domain.Evaluation
			for i := range r.Evaluations {
				e := &r.Evaluations[i]
				if latest == nil || e.CreatedAt >= latest.CreatedAt {
					latest = e
				}
			}
			currentReady := r.Status == "complete" && latest != nil && latest.EvidenceHash == r.EvidenceHash && latest.Status == "ready" && (latest.Current == nil || *latest.Current)
			if currentReady {
				if err := domain.ValidateRepairConsistency(*latest); err != nil {
					return nil, fmt.Errorf("题目 %s 第 %d 轮：%w", c.TaskName, r.Order, err)
				}
				if total, complete := domain.EvaluationScoreTotal(*latest); complete && total > domain.MaxCollectableScoreTotal {
					overLimit++
					totalOverLimit++
					continue
				}
			}
			if !req.ReviewedOnly || (currentReady && domain.IsCollectableEvaluation(*latest)) {
				rounds = append(rounds, r)
			}
		}
		if len(rounds) == 0 {
			if overLimit > 0 && (req.TaskID != "" || req.TaskIDs != nil) {
				return nil, fmt.Errorf("题目 %s 的五维总分超过 %d，不符合平台收录规则；评分已按真实结果保留", c.TaskName, domain.MaxCollectableScoreTotal)
			}
			if req.TaskIDs != nil {
				return nil, fmt.Errorf("题目 %s 暂无可导出的制表数据，请刷新后重新选择", c.TaskName)
			}
			continue
		}
		c.Rounds = rounds
		for _, r := range c.Rounds {
			if r.Status == "excluded" {
				continue
			}
			for i := len(r.Evaluations) - 1; i >= 0; i-- {
				e := r.Evaluations[i]
				if e.EvidenceHash != r.EvidenceHash {
					continue
				}
				if err := domain.ValidateRepairConsistency(e); err != nil {
					return nil, fmt.Errorf("题目 %s 第 %d 轮：%w", c.TaskName, r.Order, err)
				}
				break
			}
		}
		selected = append(selected, c)
	}
	if req.TaskIDs != nil && len(selected) != len(wanted) {
		return nil, errors.New("部分所选题目不存在或不属于当前项目，请刷新后重新选择")
	}
	if !found {
		return nil, errors.New("题目不属于当前项目")
	}
	if len(selected) == 0 && totalOverLimit > 0 {
		return nil, fmt.Errorf("所选评价五维总分均超过 %d，不符合平台收录规则；评分已按真实结果保留", domain.MaxCollectableScoreTotal)
	}
	if req.ReviewedOnly && len(selected) == 0 {
		return nil, errors.New("暂无已制表内容，请先完成至少一轮五维审核")
	}
	return selected, nil
}

func (s *AnnotationService) exportDirectory() (string, error) {
	value, err := s.store.GetConfig("annotation_export_directory")
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return filepath.Join(s.root, "exports"), nil
	}
	if !filepath.IsAbs(value) {
		return "", errors.New("请在设置中填写导出目录的本机绝对路径")
	}
	return filepath.Clean(value), nil
}
