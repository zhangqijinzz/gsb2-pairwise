package annotation

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	domain "github.com/blueship581/pinru/internal/annotation"
	"github.com/blueship581/pinru/internal/gitops"
)

type PairwiseSideRequest struct {
	TaskID string              `json:"taskId"`
	Side   domain.PairwiseSide `json:"side"`
}

type PairwiseCommitRequest struct {
	TaskID    string              `json:"taskId"`
	Side      domain.PairwiseSide `json:"side"`
	SessionID string              `json:"sessionId"`
}

func (s *AnnotationService) PreparePairwiseSide(ctx context.Context, req PairwiseSideRequest) (*domain.Case, error) {
	unlock, err := s.lockTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c, err := s.loadCase(req.TaskID)
	if err != nil {
		return nil, err
	}
	if err := requirePairwiseCase(c); err != nil {
		return nil, err
	}
	run, err := pairwiseRun(c.Pairwise, req.Side)
	if err != nil {
		return nil, err
	}
	source, err := s.verifyPairwiseBinding(ctx, c, run)
	if err != nil {
		return nil, err
	}
	if err := gitops.PreparePairwiseBranch(ctx, source, string(req.Side), c.InitialSHA); err != nil {
		return nil, err
	}
	containerID, containerName := run.ContainerID, run.ContainerName
	workspacePath, repoRelativePath := run.WorkspacePath, run.RepoRelativePath
	*run = domain.PairwiseRun{
		Side:             req.Side,
		Branch:           string(req.Side),
		ContainerID:      containerID,
		ContainerName:    containerName,
		WorkspacePath:    workspacePath,
		RepoRelativePath: repoRelativePath,
		VideoStatus:      domain.PairwiseVideoMissing,
		PreparedAt:       time.Now().Unix(),
	}
	return s.store.SaveAnnotationCase(*c, c.Revision)
}

func (s *AnnotationService) CommitPairwiseSide(ctx context.Context, req PairwiseCommitRequest) (*domain.Case, error) {
	unlock, err := s.lockTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c, err := s.loadCase(req.TaskID)
	if err != nil {
		return nil, err
	}
	if err := requirePairwiseCase(c); err != nil {
		return nil, err
	}
	run, err := pairwiseRun(c.Pairwise, req.Side)
	if err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		return nil, errors.New("SessionID 不能为空")
	}
	if run.CaptureID == "" || run.CaptureHash == "" || run.TraceHash == "" || run.TurnCount != 1 || run.SessionID == "" {
		return nil, errors.New("请先采集该侧完整首轮轨迹和代码证据")
	}
	if run.SessionID != sessionID {
		return nil, errors.New("提交 SessionID 与该侧已采集轨迹不一致")
	}
	capture := pairwiseCaptureByID(c, run.CaptureID)
	if capture == nil || capture.Hash != run.CaptureHash || capture.TraceHash != run.TraceHash {
		return nil, errors.New("该侧采集证据记录不完整，请重新采集")
	}
	frozenHash, err := domain.TreeHash(ctx, capture.CodePath)
	if err != nil || frozenHash != capture.Hash {
		return nil, errors.New("该侧已采集代码证据发生变化，请重新采集")
	}
	source, err := s.verifyPairwiseBinding(ctx, c, run)
	if err != nil {
		return nil, err
	}
	currentHash, err := domain.TreeHash(ctx, source)
	if err != nil {
		return nil, err
	}
	if currentHash != run.CaptureHash {
		return nil, errors.New("当前代码在采集后发生变化，请重新采集再提交")
	}
	other, _ := pairwiseRun(c.Pairwise, oppositePairwiseSide(req.Side))
	if other.SessionID != "" && other.SessionID == sessionID {
		return nil, errors.New("A/B SessionID 必须不同")
	}
	sha, err := gitops.CommitPairwiseResult(ctx, source, string(req.Side), c.InitialSHA, sessionID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.SnapshotURL) == "" {
		return nil, errors.New("请先发布初始快照，再提交 A/B 产物")
	}
	if s.pushPairwise != nil {
		err = s.pushPairwise(ctx, source, c.SnapshotURL, string(req.Side))
	} else {
		err = s.pushPairwiseBranch(ctx, source, c.SnapshotURL, string(req.Side))
	}
	if err != nil {
		return nil, err
	}
	commitURL, err := pairwiseCommitURL(c.SnapshotURL, sha)
	if err != nil {
		return nil, err
	}
	run.SessionID = sessionID
	run.DeliverableSHA = sha
	run.DeliverableURL = commitURL
	run.CommittedAt = time.Now().Unix()
	return s.store.SaveAnnotationCase(*c, c.Revision)
}

func requirePairwiseCase(c *domain.Case) error {
	if c == nil || c.Mode != domain.CaseModePairwiseGSB || c.Pairwise == nil {
		return errors.New("当前题目尚未启用 Pair-wise GSB 模式")
	}
	if len(c.InitialSHA) != 40 {
		return errors.New("缺少完整初始快照")
	}
	return nil
}

func pairwiseRun(data *domain.PairwiseData, side domain.PairwiseSide) (*domain.PairwiseRun, error) {
	if data == nil {
		return nil, errors.New("Pair-wise 数据不存在")
	}
	switch side {
	case domain.PairwiseSideA:
		return &data.RunA, nil
	case domain.PairwiseSideB:
		return &data.RunB, nil
	default:
		return nil, errors.New("Pair-wise 分支名称只能是 A 或 B")
	}
}

func oppositePairwiseSide(side domain.PairwiseSide) domain.PairwiseSide {
	if side == domain.PairwiseSideA {
		return domain.PairwiseSideB
	}
	return domain.PairwiseSideA
}

func pairwiseCommitURL(snapshotURL, sha string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(snapshotURL))
	if err != nil || u.Scheme != "https" || u.Host != "github.com" {
		return "", errors.New("初始快照地址必须是 GitHub HTTPS commit 链接")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "commit" || parts[0] == "" || parts[1] == "" {
		return "", errors.New("初始快照地址格式无效")
	}
	return fmt.Sprintf("https://github.com/%s/%s/commit/%s", parts[0], parts[1], sha), nil
}

func (s *AnnotationService) pushPairwiseBranch(ctx context.Context, path, snapshotURL, side string) error {
	u, err := url.Parse(snapshotURL)
	if err != nil {
		return err
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "commit" {
		return errors.New("初始快照地址格式无效")
	}
	accounts, err := s.store.ListGitHubAccounts()
	if err != nil {
		return err
	}
	var username, token string
	for _, account := range accounts {
		if account.IsDefault || len(accounts) == 1 {
			username, token = strings.TrimSpace(account.Username), strings.TrimSpace(account.Token)
			if account.IsDefault {
				break
			}
		}
	}
	if username == "" || token == "" {
		return errors.New("请先配置默认 GitHub 账号，再推送 A/B 产物")
	}
	remote := fmt.Sprintf("https://github.com/%s/%s.git", parts[0], parts[1])
	if _, err := runCommand(ctx, path, "git", "remote", "get-url", "origin"); err == nil {
		if _, err := runCommand(ctx, path, "git", "remote", "set-url", "origin", remote); err != nil {
			return err
		}
	} else if _, err := runCommand(ctx, path, "git", "remote", "add", "origin", remote); err != nil {
		return err
	}
	return gitops.PushBranchWithMode(path, side, username, token, false)
}
