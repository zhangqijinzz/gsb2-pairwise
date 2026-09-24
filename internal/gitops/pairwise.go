package gitops

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var pairwiseFullSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func PreparePairwiseBranch(ctx context.Context, path, side, initialSHA string) error {
	side, err := validatePairwiseRef(side, initialSHA)
	if err != nil {
		return err
	}
	if _, err := pairwiseGitOutput(ctx, path, "rev-parse", "--is-inside-work-tree"); err != nil {
		return errors.New("当前路径不是 Git 仓库")
	}
	status, err := pairwiseGitOutput(ctx, path, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return errors.New("工作区存在未提交改动，不能准备 A/B 分支")
	}
	if _, err := pairwiseGitOutput(ctx, path, "cat-file", "-e", initialSHA+"^{commit}"); err != nil {
		return fmt.Errorf("初始快照 %s 在本地仓库中不存在", initialSHA)
	}
	if _, err := pairwiseGitOutput(ctx, path, "switch", "-C", side, initialSHA); err != nil {
		return fmt.Errorf("准备 %s 分支失败：%w", side, err)
	}
	head, err := pairwiseGitOutput(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if !strings.EqualFold(head, initialSHA) {
		return fmt.Errorf("%s 分支未位于登记的初始快照", side)
	}
	return nil
}

func CommitPairwiseResult(ctx context.Context, path, side, initialSHA, message string) (string, error) {
	side, err := validatePairwiseRef(side, initialSHA)
	if err != nil {
		return "", err
	}
	branch, err := pairwiseGitOutput(ctx, path, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	headBefore, err := pairwiseGitOutput(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if branch != side {
		if !strings.EqualFold(headBefore, initialSHA) {
			return "", fmt.Errorf("当前分支是 %s，且 HEAD 已偏离初始快照，不能自动切换到 %s 分支", branch, side)
		}
		if targetHead, targetErr := pairwiseGitOutput(ctx, path, "rev-parse", "--verify", "refs/heads/"+side); targetErr == nil && !strings.EqualFold(targetHead, initialSHA) {
			return "", fmt.Errorf("%s 分支已偏离初始快照，不能自动重置", side)
		}
		if _, err := pairwiseGitOutput(ctx, path, "switch", "-C", side, initialSHA); err != nil {
			return "", fmt.Errorf("自动切换到 %s 分支失败：%w", side, err)
		}
	}
	if _, err := pairwiseGitOutput(ctx, path, "merge-base", "--is-ancestor", initialSHA, "HEAD"); err != nil {
		return "", errors.New("当前分支不是从登记的初始快照派生")
	}
	status, err := pairwiseGitOutput(ctx, path, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(headBefore, initialSHA) {
		parent, parentErr := pairwiseGitOutput(ctx, path, "rev-parse", headBefore+"^")
		if parentErr != nil || !strings.EqualFold(parent, initialSHA) || strings.TrimSpace(status) != "" {
			return "", errors.New("产物提交的直接父提交必须是登记的初始快照")
		}
		return strings.ToLower(headBefore), nil
	}
	if strings.TrimSpace(status) != "" {
		if _, err := pairwiseGitOutput(ctx, path, "add", "-A"); err != nil {
			return "", err
		}
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Pair-wise result " + side
	}
	if _, err := pairwiseGitOutput(ctx, path, "-c", "user.name=PINRU Pairwise", "-c", "user.email=pinru@local", "commit", "--allow-empty", "-m", message); err != nil {
		return "", fmt.Errorf("提交 %s 产物失败：%w", side, err)
	}
	head, err := pairwiseGitOutput(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if _, err := pairwiseGitOutput(ctx, path, "merge-base", "--is-ancestor", initialSHA, head); err != nil {
		return "", errors.New("产物提交没有继承登记的初始快照")
	}
	parent, err := pairwiseGitOutput(ctx, path, "rev-parse", head+"^")
	if err != nil || !strings.EqualFold(parent, initialSHA) {
		return "", errors.New("产物提交的直接父提交不是登记的初始快照")
	}
	return strings.ToLower(head), nil
}

func validatePairwiseRef(side, initialSHA string) (string, error) {
	side = strings.TrimSpace(side)
	if side != "A" && side != "B" {
		return "", errors.New("Pair-wise 分支名称只能是 A 或 B")
	}
	if !pairwiseFullSHA.MatchString(strings.TrimSpace(initialSHA)) {
		return "", errors.New("初始快照必须是完整 40 位 SHA")
	}
	return side, nil
}

func pairwiseGitOutput(ctx context.Context, path string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = path
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return "", errors.New(message)
	}
	return strings.TrimSpace(string(out)), nil
}
