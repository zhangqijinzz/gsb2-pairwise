package codepush

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/blueship581/pinru/internal/github"
	"github.com/blueship581/pinru/internal/gitops"
	"github.com/blueship581/pinru/internal/store"
	"github.com/blueship581/pinru/internal/util"
	"github.com/google/uuid"
)

const (
	mainBranch        = "main"
	statusCommitted   = "committed"
	statusPushed      = "pushed"
	statusNeedsPush   = "needs_push"
	noCommitChangeMsg = "当前项目没有可提交改动"
)

type CodePushService struct {
	store *store.Store
}

func New(store *store.Store) *CodePushService {
	return &CodePushService{store: store}
}

type CommitCodeRequest struct {
	TaskID       string `json:"taskId"`
	ModelRunID   string `json:"modelRunId"`
	SessionID    string `json:"sessionId"`
	SessionIndex int    `json:"sessionIndex"`
}

type RedoCommitRequest struct {
	RecordID string `json:"recordId"`
}

type PushCodeRequest struct {
	RecordID        string `json:"recordId"`
	GitHubAccountID string `json:"githubAccountId"`
	ForceWithLease  bool   `json:"forceWithLease"`
}

func (s *CodePushService) ListCodePushRecords(taskID string) ([]store.CodePushRecord, error) {
	return s.store.ListCodePushRecords(strings.TrimSpace(taskID))
}

func (s *CodePushService) CommitCode(req CommitCodeRequest) (*store.CodePushRecord, error) {
	resolved, err := s.resolveCommitTarget(req.TaskID, req.ModelRunID, req.SessionID, req.SessionIndex)
	if err != nil {
		return nil, err
	}

	sha, err := commitWorkspaceOrReuseHead(resolved.LocalPath, resolved.SessionID)
	if err != nil {
		return nil, err
	}

	record := store.CodePushRecord{
		ID:           uuid.NewString(),
		TaskID:       resolved.TaskID,
		ModelRunID:   resolved.ModelRunID,
		SessionID:    resolved.SessionID,
		SessionIndex: resolved.SessionIndex,
		LocalPath:    resolved.LocalPath,
		RepoName:     resolved.RepoName,
		CommitSHA:    sha,
		Branch:       mainBranch,
		Status:       statusCommitted,
	}
	if err := s.store.UpsertCodePushRecord(record); err != nil {
		return nil, err
	}
	return s.store.GetCodePushRecord(record.ID)
}

func (s *CodePushService) RedoCommit(req RedoCommitRequest) (*store.CodePushRecord, error) {
	recordID := strings.TrimSpace(req.RecordID)
	if recordID == "" {
		return nil, errors.New("提交记录不能为空")
	}
	record, err := s.store.GetCodePushRecord(recordID)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("提交记录不存在：%s", recordID)
	}

	sha, err := commitWorkspace(record.LocalPath, record.SessionID, true)
	if err != nil {
		return nil, err
	}
	record.CommitSHA = sha
	record.CommitURL = ""
	if record.Status == statusPushed {
		record.Status = statusNeedsPush
	} else {
		record.Status = statusCommitted
	}
	record.ErrorMessage = ""
	record.PushedAt = nil
	if err := s.store.UpsertCodePushRecord(*record); err != nil {
		return nil, err
	}
	return s.store.GetCodePushRecord(record.ID)
}

func (s *CodePushService) PushCode(req PushCodeRequest) (*store.CodePushRecord, error) {
	recordID := strings.TrimSpace(req.RecordID)
	if recordID == "" {
		return nil, errors.New("提交记录不能为空")
	}
	record, err := s.store.GetCodePushRecord(recordID)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("提交记录不存在：%s", recordID)
	}
	if strings.TrimSpace(record.CommitSHA) == "" {
		return nil, errors.New("当前记录还没有本地 commit，不能推送")
	}

	account, err := s.resolveGitHubAccount(req.GitHubAccountID)
	if err != nil {
		return nil, s.markPushFailed(record, err)
	}

	targetRepo := strings.TrimSpace(account.Username) + "/" + record.RepoName
	description := ""
	repo, err := github.EnsureRepository(targetRepo, account.Token, &description)
	if err != nil {
		return nil, s.markPushFailed(record, fmt.Errorf("创建或读取 GitHub 仓库失败：%w", err))
	}
	_ = github.UpdateRepositoryDescription(targetRepo, account.Token, "")

	remoteURL := fmt.Sprintf("https://github.com/%s.git", targetRepo)
	if err := ensureRemote(record.LocalPath, remoteURL); err != nil {
		return nil, s.markPushFailed(record, fmt.Errorf("设置 GitHub remote 失败：%w", err))
	}
	if err := gitops.EnsureBranch(record.LocalPath, mainBranch); err != nil {
		return nil, s.markPushFailed(record, fmt.Errorf("切换 main 分支失败：%w", err))
	}
	if err := gitops.PushBranchWithMode(record.LocalPath, mainBranch, account.Username, account.Token, req.ForceWithLease); err != nil {
		return nil, s.markPushFailed(record, fmt.Errorf("推送 GitHub 失败：%w", err))
	}
	_ = github.SetDefaultBranch(targetRepo, mainBranch, account.Token)

	now := time.Now().Unix()
	record.RepoURL = repo.HTMLURL
	record.CommitURL = strings.TrimRight(repo.HTMLURL, "/") + "/commit/" + record.CommitSHA
	record.Status = statusPushed
	record.ErrorMessage = ""
	record.PushedAt = &now
	if err := s.store.UpsertCodePushRecord(*record); err != nil {
		return nil, err
	}
	return s.store.GetCodePushRecord(record.ID)
}

func (s *CodePushService) markPushFailed(record *store.CodePushRecord, cause error) error {
	if record == nil || cause == nil {
		return cause
	}
	record.ErrorMessage = trimErrorMessage(cause.Error())
	if record.Status == statusPushed {
		record.Status = statusNeedsPush
	} else {
		record.Status = statusCommitted
	}
	if err := s.store.UpsertCodePushRecord(*record); err != nil {
		return fmt.Errorf("%w；失败原因写入本地记录也失败：%v", cause, err)
	}
	return cause
}

func trimErrorMessage(message string) string {
	message = strings.TrimSpace(message)
	const maxLen = 2000
	if len(message) <= maxLen {
		return message
	}
	return message[len(message)-maxLen:]
}

type commitTarget struct {
	TaskID       string
	ModelRunID   string
	SessionID    string
	SessionIndex int
	LocalPath    string
	RepoName     string
}

func (s *CodePushService) resolveCommitTarget(taskID, modelRunID, sessionID string, sessionIndex int) (*commitTarget, error) {
	taskID = strings.TrimSpace(taskID)
	modelRunID = strings.TrimSpace(modelRunID)
	sessionID = strings.TrimSpace(sessionID)
	if taskID == "" {
		return nil, errors.New("题卡不能为空")
	}
	if modelRunID == "" {
		return nil, errors.New("模型执行记录不能为空")
	}
	if sessionID == "" {
		return nil, errors.New("sessionId 不能为空")
	}

	run, err := s.store.GetModelRunByID(modelRunID)
	if err != nil {
		return nil, err
	}
	if run == nil || run.TaskID != taskID {
		return nil, fmt.Errorf("模型执行记录不存在：%s", modelRunID)
	}
	if run.LocalPath == nil || strings.TrimSpace(*run.LocalPath) == "" {
		return nil, errors.New("当前模型执行副本没有本地目录")
	}
	localPath := filepath.Clean(util.ExpandTilde(*run.LocalPath))
	info, err := os.Stat(localPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("本地目录不是文件夹：%s", localPath)
	}

	return &commitTarget{
		TaskID:       taskID,
		ModelRunID:   modelRunID,
		SessionID:    sessionID,
		SessionIndex: sessionIndex,
		LocalPath:    localPath,
		RepoName:     deriveRepoName(localPath),
	}, nil
}

func (s *CodePushService) resolveGitHubAccount(accountID string) (*store.GitHubAccount, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID != "" {
		account, err := s.store.GetGitHubAccount(accountID)
		if err != nil {
			return nil, err
		}
		if account == nil {
			return nil, fmt.Errorf("GitHub 账号不存在：%s", accountID)
		}
		return validateGitHubAccount(account)
	}

	accounts, err := s.store.ListGitHubAccounts()
	if err != nil {
		return nil, err
	}
	for index := range accounts {
		if accounts[index].IsDefault {
			return validateGitHubAccount(&accounts[index])
		}
	}
	if len(accounts) > 0 {
		return validateGitHubAccount(&accounts[0])
	}
	return nil, errors.New("还没有配置 GitHub 账号，请先在设置里添加个人账号 username 和 token")
}

func validateGitHubAccount(account *store.GitHubAccount) (*store.GitHubAccount, error) {
	if strings.TrimSpace(account.Username) == "" || strings.TrimSpace(account.Token) == "" {
		return nil, errors.New("GitHub 账号 username 或 token 不完整")
	}
	account.Username = strings.TrimSpace(account.Username)
	account.Token = strings.TrimSpace(account.Token)
	return account, nil
}

func commitWorkspace(path, sessionID string, amend bool) (string, error) {
	if err := ensureGitRepository(path); err != nil {
		return "", err
	}
	if err := gitops.EnsureProjectGitignore(path); err != nil {
		return "", err
	}
	if err := ensureCommitAuthor(path); err != nil {
		return "", err
	}
	if err := gitops.EnsureBranch(path, mainBranch); err != nil {
		return "", err
	}

	hasChanges, err := hasWorkspaceChanges(path)
	if err != nil {
		return "", err
	}
	if !hasChanges {
		return "", errors.New(noCommitChangeMsg)
	}
	if err := runGit(path, "add", "-A"); err != nil {
		return "", err
	}

	if amend && hasHead(path) {
		if err := runGit(path, "commit", "--amend", "--reset-author", "-m", sessionID); err != nil {
			return "", err
		}
	} else {
		if err := runGit(path, "commit", "-m", sessionID); err != nil {
			return "", err
		}
	}
	return gitOutput(path, "rev-parse", "HEAD")
}

func commitWorkspaceOrReuseHead(path, sessionID string) (string, error) {
	sha, err := commitWorkspace(path, sessionID, false)
	if err == nil {
		return sha, nil
	}
	if !strings.Contains(err.Error(), noCommitChangeMsg) || !hasHead(path) {
		return "", err
	}
	return gitOutput(path, "rev-parse", "HEAD")
}

func ensureGitRepository(path string) error {
	if info, err := os.Stat(filepath.Join(path, ".git")); err == nil && info.IsDir() {
		return nil
	}
	if err := runGit(path, "init", "-b", mainBranch); err == nil {
		return nil
	}
	if err := runGit(path, "init"); err != nil {
		return err
	}
	return gitops.EnsureBranch(path, mainBranch)
}

func ensureCommitAuthor(path string) error {
	globalName, _ := gitOutput(path, "config", "--global", "--get", "user.name")
	globalEmail, _ := gitOutput(path, "config", "--global", "--get", "user.email")
	globalName = strings.TrimSpace(globalName)
	globalEmail = strings.TrimSpace(globalEmail)
	if globalName != "" && globalEmail != "" {
		if err := runGit(path, "config", "user.name", globalName); err != nil {
			return err
		}
		if err := runGit(path, "config", "user.email", globalEmail); err != nil {
			return err
		}
		return nil
	}

	name, _ := gitOutput(path, "config", "--get", "user.name")
	email, _ := gitOutput(path, "config", "--get", "user.email")
	if strings.TrimSpace(name) != "" && strings.TrimSpace(email) != "" {
		return nil
	}
	return nil
}

func hasWorkspaceChanges(path string) (bool, error) {
	out, err := gitOutput(path, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func hasHead(path string) bool {
	return runGit(path, "rev-parse", "--verify", "HEAD") == nil
}

func ensureRemote(path, remoteURL string) error {
	current, err := gitOutput(path, "remote", "get-url", "origin")
	if err == nil {
		if strings.TrimSpace(current) == remoteURL {
			return nil
		}
		return runGit(path, "remote", "set-url", "origin", remoteURL)
	}
	return runGit(path, "remote", "add", "origin", remoteURL)
}

func runGit(path string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = path
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gitOutput(path string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

var zwSegmentPattern = regexp.MustCompile(`^zw-?0*([0-9]+)`)
var numericSegmentPattern = regexp.MustCompile(`^[0-9]+$`)

func deriveRepoName(localPath string) string {
	cleaned := filepath.Clean(localPath)
	for {
		base := filepath.Base(cleaned)
		lower := strings.ToLower(base)
		if match := zwSegmentPattern.FindStringSubmatch(lower); len(match) == 2 {
			round := extractTrailingNumber(lower)
			if round != "" {
				return "zw-" + match[1] + "-" + round
			}
			return "zw-" + match[1]
		}
		parent := filepath.Dir(cleaned)
		if parent == cleaned || parent == "." || parent == string(filepath.Separator) {
			break
		}
		cleaned = parent
	}
	return sanitizeRepoName(filepath.Base(localPath))
}

func extractTrailingNumber(value string) string {
	parts := strings.Split(strings.Trim(value, "-_ "), "-")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		if part == "" {
			continue
		}
		if numericSegmentPattern.MatchString(part) {
			return strings.TrimLeft(part, "0")
		}
		break
	}
	return ""
}

func sanitizeRepoName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "pinru-code"
	}
	return result
}
