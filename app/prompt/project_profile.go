package prompt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blueship581/pinru/internal/analysis"
	"github.com/blueship581/pinru/internal/util"
)

const (
	projectProfileCommitFallback = "no-git"
	projectProfileMaxRunes       = 6500
	projectProfileCommandTimeout = 2 * time.Second
)

type promptProjectProfile struct {
	RepoPath    string
	CommitHash  string
	ProfileText string
	FromCache   bool
}

func (s *PromptService) resolveProjectProfile(ctx context.Context, workDir string) (*promptProjectProfile, error) {
	summary, err := analysis.AnalyzeRepository(workDir)
	if err != nil {
		return nil, err
	}
	repoPath := util.NormalizePath(summary.RepoPath)
	commitHash := resolveProjectCommitHash(ctx, repoPath)

	if cached, err := s.store.GetProjectProfile(repoPath, commitHash); err == nil && cached != nil {
		return &promptProjectProfile{
			RepoPath:    cached.RepoPath,
			CommitHash:  cached.CommitHash,
			ProfileText: cached.ProfileText,
			FromCache:   true,
		}, nil
	} else if err != nil {
		return nil, err
	}

	profileText := buildProjectProfileText(summary, commitHash)
	if err := s.store.UpsertProjectProfile(repoPath, commitHash, profileText); err != nil {
		return nil, err
	}
	return &promptProjectProfile{
		RepoPath:    repoPath,
		CommitHash:  commitHash,
		ProfileText: profileText,
		FromCache:   false,
	}, nil
}

func resolveProjectCommitHash(ctx context.Context, repoPath string) string {
	if strings.TrimSpace(repoPath) == "" {
		return projectProfileCommitFallback
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
		return projectProfileCommitFallback
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cmdCtx, cancel := context.WithTimeout(ctx, projectProfileCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "git", "-C", repoPath, "rev-parse", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return projectProfileCommitFallback
	}
	hash := strings.TrimSpace(out.String())
	if hash == "" {
		return projectProfileCommitFallback
	}
	return hash
}

func buildProjectProfileText(summary *analysis.Summary, commitHash string) string {
	if summary == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("项目画像缓存：\n")
	fmt.Fprintf(&sb, "- 仓库根目录：%s\n", strings.TrimSpace(summary.RepoPath))
	fmt.Fprintf(&sb, "- 代码版本：%s\n", strings.TrimSpace(commitHash))
	if len(summary.DetectedStack) > 0 {
		fmt.Fprintf(&sb, "- 技术栈线索：%s\n", strings.Join(summary.DetectedStack, "、"))
	}
	fmt.Fprintf(&sb, "- 可分析文本文件数：%d\n", summary.TotalFiles)

	if len(summary.FileTree) > 0 {
		sb.WriteString("\n目录与关键文件抽样：\n")
		for _, line := range summary.FileTree {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}

	features := inferProjectProfileFeatures(summary)
	if len(features) > 0 {
		sb.WriteString("\n可能的业务/工程切入点：\n")
		for _, feature := range features {
			fmt.Fprintf(&sb, "- %s\n", feature)
		}
	}

	if len(summary.KeyFiles) > 0 {
		sb.WriteString("\n关键文件片段：\n")
		for _, file := range summary.KeyFiles {
			if strings.TrimSpace(file.Snippet) == "" {
				continue
			}
			fmt.Fprintf(&sb, "### %s\n", file.Path)
			sb.WriteString(strings.TrimSpace(file.Snippet))
			sb.WriteString("\n\n")
		}
	}

	return truncateRunes(sb.String(), projectProfileMaxRunes)
}

func inferProjectProfileFeatures(summary *analysis.Summary) []string {
	if summary == nil {
		return nil
	}
	paths := make([]string, 0, len(summary.FileTree)+len(summary.KeyFiles))
	for _, line := range summary.FileTree {
		paths = append(paths, strings.ToLower(line))
	}
	for _, file := range summary.KeyFiles {
		paths = append(paths, strings.ToLower(file.Path))
	}
	joined := strings.Join(paths, "\n")

	type signal struct {
		terms []string
		text  string
	}
	signals := []signal{
		{[]string{"order", "订单", "payment", "pay"}, "订单/支付/状态流转相关能力"},
		{[]string{"user", "student", "member", "用户", "学生"}, "用户、学生或成员资料相关能力"},
		{[]string{"comment", "reply", "评论", "回复"}, "评论、回复或互动关系相关能力"},
		{[]string{"export", "csv", "excel", "导出"}, "导出、筛选和文件下载相关能力"},
		{[]string{"statistics", "stat", "dashboard", "chart", "统计"}, "统计面板、报表或数据口径相关能力"},
		{[]string{"auth", "permission", "role", "权限"}, "权限、角色和操作边界相关能力"},
		{[]string{"inventory", "stock", "库存"}, "库存、容量或数量约束相关能力"},
		{[]string{"reservation", "booking", "预约"}, "预约、锁定和释放时序相关能力"},
		{[]string{"notification", "message", "通知", "消息"}, "通知、消息和异步提醒相关能力"},
		{[]string{"test", "spec", "测试"}, "测试、回归和工程质量相关能力"},
	}

	var result []string
	seen := make(map[string]struct{})
	for _, sig := range signals {
		for _, term := range sig.terms {
			if strings.Contains(joined, strings.ToLower(term)) {
				if _, ok := seen[sig.text]; !ok {
					result = append(result, sig.text)
					seen[sig.text] = struct{}{}
				}
				break
			}
		}
	}
	sort.Strings(result)
	return result
}

func (p *promptProjectProfile) summaryForLog() string {
	if p == nil {
		return ""
	}
	source := "fresh"
	if p.FromCache {
		source = "cache"
	}
	return fmt.Sprintf("%s:%s", source, p.CommitHash)
}

func isProjectProfileUnavailable(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
