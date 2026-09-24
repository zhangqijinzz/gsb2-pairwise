package annotation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	domain "github.com/blueship581/pinru/internal/annotation"
	"github.com/blueship581/pinru/internal/github"
	"github.com/blueship581/pinru/internal/gitops"
	"github.com/blueship581/pinru/internal/store"
)

// PublishSnapshot only publishes the frozen initial commit, never the live workspace.
func (s *AnnotationService) PublishSnapshot(ctx context.Context, req PrepareRequest) (*domain.Case, error) {
	unlock, err := s.lockTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c, err := s.loadCase(req.TaskID)
	if err != nil {
		return nil, err
	}
	if c.SnapshotURL != "" {
		return c, nil
	}
	if len(c.InitialSHA) != 40 {
		return nil, errors.New("缺少真实初始快照，请先准备题目")
	}
	repoName, err := initialSnapshotRepositoryName(c)
	if err != nil {
		return nil, err
	}
	accounts, err := s.store.ListGitHubAccounts()
	if err != nil {
		return nil, err
	}
	var account *store.GitHubAccount
	for i := range accounts {
		if accounts[i].IsDefault {
			account = &accounts[i]
			break
		}
	}
	if account == nil && len(accounts) == 1 {
		account = &accounts[0]
	}
	if account == nil {
		return nil, errors.New("请在设置中配置默认 GitHub 账号后重试发布初始快照")
	}
	baseline := filepath.Join(s.caseDir(c.TaskID), "initial", c.InitialSHA)
	url, err := s.publishInitial(ctx, baseline, repoName, c.InitialSHA, *account)
	if err != nil {
		return nil, err
	}
	if sha, err := snapshotSHA(url); err != nil || sha != c.InitialSHA {
		return nil, errors.New("发布结果与初始 SHA 不一致")
	}
	c.SnapshotURL = url
	return s.store.SaveAnnotationCase(*c, c.Revision)
}

// Use the same permanent sequence sources as the container command, never UI order.
func initialSnapshotRepositoryName(c *domain.Case) (string, error) {
	name := strings.TrimSpace(c.TaskName)
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 _-]*$`).MatchString(name) {
		return "", errors.New("初始快照仓库的大题名称需使用英文字母、数字、空格、下划线或连字符")
	}
	idMatch := regexp.MustCompile(`(?:^|__)label-\d+-(\d+)$`).FindStringSubmatch(c.TaskID)
	folder := filepath.Base(strings.TrimRight(strings.ReplaceAll(c.SourcePath, `\`, "/"), "/"))
	var folderMatch []string
	if strings.HasPrefix(strings.ToLower(folder), strings.ToLower(name)+"-") {
		folderMatch = regexp.MustCompile(`-(\d+)$`).FindStringSubmatch(folder)
	}
	parse := func(match []string) int64 {
		if len(match) < 2 {
			return 0
		}
		n, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || n < 1 || n > 9007199254740991 {
			return 0
		}
		return n
	}
	idSequence, folderSequence := parse(idMatch), parse(folderMatch)
	if (len(idMatch) > 0 && idSequence == 0) || (len(folderMatch) > 0 && folderSequence == 0) {
		return "", errors.New("题目固定编号无效，请核对题目记录与目录")
	}
	if idSequence > 0 && folderSequence > 0 && idSequence != folderSequence {
		return "", errors.New("题目编号与目录编号不一致，请先核对题目目录")
	}
	if idSequence == 0 {
		idSequence = folderSequence
	}
	if idSequence == 0 {
		return "", errors.New("尚未找到题目的固定编号，无法生成初始快照仓库名称")
	}
	prefix := regexp.MustCompile(`[ _]+`).ReplaceAllString(strings.ToLower(name), "-")
	repo := fmt.Sprintf("%s-%d", strings.TrimRight(prefix, "-"), idSequence)
	if len(repo) > 100 {
		return "", errors.New("初始快照仓库名称过长，请缩短大题名称")
	}
	return repo, nil
}

func publishInitial(ctx context.Context, baseline, repoName, sha string, account store.GitHubAccount) (string, error) {
	if strings.TrimSpace(account.Token) == "" || strings.TrimSpace(account.Username) == "" {
		return "", errors.New("GitHub 账号配置不完整")
	}
	parent, err := os.MkdirTemp("", "pinru-initial-publish-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(parent)
	work := filepath.Join(parent, "repo")
	if err := ctx.Err(); err != nil {
		return "", err
	}
	target := account.Username + "/" + repoName
	if err := prepareSnapshotPublication(ctx, baseline, work, sha, "https://github.com/"+target+".git"); err != nil {
		return "", err
	}
	repo, err := github.EnsureRepository(target, account.Token, nil)
	if err != nil {
		return "", fmt.Errorf("创建初始快照仓库失败：%w", err)
	}
	if err := gitops.PublishSnapshotCommit(ctx, work, sha, account.Username, account.Token); err != nil {
		return "", err
	}
	return strings.TrimRight(repo.HTMLURL, "/") + "/commit/" + sha, nil
}

// Configure only the disposable publishing clone; frozen evidence stays unchanged.
func prepareSnapshotPublication(ctx context.Context, baseline, work, sha, remote string) error {
	if err := cloneInitialRepository(ctx, baseline, work, sha); err != nil {
		return err
	}
	if err := gitops.EnsureBranch(work, "main"); err != nil {
		return err
	}
	_, err := runCommand(ctx, work, "git", "remote", "add", "origin", remote)
	return err
}
