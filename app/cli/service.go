package cli

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blueship581/pinru/internal/errs"
	"github.com/blueship581/pinru/internal/util"
	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed manuals/*
var manualFS embed.FS

//go:embed schemas/pg_code_review.json
var pgCodeReviewSchema []byte

//go:embed schemas/dissatisfaction_summary.json
var dissatisfactionSummarySchema []byte

//go:embed prompts/dissatisfaction_summary.md
var dissatisfactionSummaryPromptTemplate string

// Service executes the local claude CLI and streams output back via polling.
type CliService struct {
	mu                sync.Mutex
	sessions          map[string]*cliSession
	resolveCLI        func(name string) (string, error) // nil → util.ResolveCLI
	reviewContextPath string
}

func defaultPgCodeContextScriptPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".codex", "skills", "pg-code", "scripts", "collect_project_context.py"),
		filepath.Join(home, ".claude", "skills", "pg-code", "scripts", "collect_project_context.py"),
	}
}

type cliSession struct {
	mu       sync.Mutex
	lines    []string
	done     bool
	exitErr  string
	cancel   context.CancelFunc
	lastUsed time.Time
}

func (s *cliSession) append(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, line)
	s.lastUsed = time.Now()
}

func (s *cliSession) finish(errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	s.exitErr = errMsg
	s.lastUsed = time.Now()
}

func (s *cliSession) poll(offset int) (lines []string, done bool, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if offset < len(s.lines) {
		lines = s.lines[offset:]
	}
	return lines, s.done, s.exitErr
}

// NewService creates a new CLI service with background session cleanup.
func New() *CliService {
	svc := &CliService{
		sessions: make(map[string]*cliSession),
	}
	go svc.cleanupLoop()
	return svc
}

// NewWithResolver creates a CliService with a custom binary resolver. Use this
// in tests to simulate a missing CLI without touching the system PATH.
func NewWithResolver(fn func(name string) (string, error)) *CliService {
	svc := &CliService{
		sessions:   make(map[string]*cliSession),
		resolveCLI: fn,
	}
	go svc.cleanupLoop()
	return svc
}

// lookupCLI resolves the named binary using the configured resolver, or falls
// back to util.ResolveCLI when none is set.
func (s *CliService) lookupCLI(name string) (string, error) {
	if s.resolveCLI != nil {
		return s.resolveCLI(name)
	}
	return util.ResolveCLI(name)
}

func (s *CliService) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for id, sess := range s.sessions {
			sess.mu.Lock()
			if sess.done && sess.lastUsed.Before(cutoff) {
				delete(s.sessions, id)
			}
			sess.mu.Unlock()
		}
		s.mu.Unlock()
	}
}

// ─── Request / Response types ───────────────────────────────────────────────

// StartClaudeRequest describes a claude CLI invocation.
type StartClaudeRequest struct {
	// WorkDir is the repository directory to run claude in.
	WorkDir string `json:"workDir"`
	// Prompt is the user's prompt / task description.
	Prompt string `json:"prompt"`
	// Model e.g. "claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5"
	Model string `json:"model"`
	// ThinkingDepth: "", "think", "think harder", "ultrathink"
	ThinkingDepth string `json:"thinkingDepth"`
	// Mode: "agent" or "plan"
	Mode string `json:"mode"`
	// PermissionMode currently only supports the default guarded mode.
	PermissionMode string `json:"permissionMode"`
	// AdditionalDirs grants Claude access to paths outside WorkDir when needed.
	AdditionalDirs []string `json:"additionalDirs"`
	// EnvOverrides sets additional environment variables for the claude process.
	// These are applied on top of the current process environment.
	// Use this instead of --model to bypass CLI argument normalization (e.g. 4-6 → 4.6).
	EnvOverrides map[string]string `json:"envOverrides,omitempty"`
}

// StartClaudeResponse holds the session ID for output polling.
type StartClaudeResponse struct {
	SessionID string `json:"sessionId"`
}

// PollOutputRequest specifies which session and line offset to read from.
type PollOutputRequest struct {
	SessionID string `json:"sessionId"`
	Offset    int    `json:"offset"`
}

// PollOutputResponse contains new lines and completion state.
type PollOutputResponse struct {
	Lines  []string `json:"lines"`
	Done   bool     `json:"done"`
	ErrMsg string   `json:"errMsg"`
}

// SkillItem represents a single skill entry.
type SkillItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ─── Public methods ──────────────────────────────────────────────────────────

// CheckCLI returns the resolved path to the claude binary, or an error.
func (s *CliService) CheckCLI() (string, error) {
	path, err := s.lookupCLI("claude")
	if err != nil {
		return "", fmt.Errorf(errs.MsgClaudeCliMissing)
	}
	return path, nil
}

// StartClaude launches a claude CLI session and returns a session ID for polling.
func (s *CliService) StartClaude(req StartClaudeRequest) (*StartClaudeResponse, error) {
	if strings.TrimSpace(req.WorkDir) == "" {
		return nil, fmt.Errorf(errs.MsgWorkDirRequired)
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf(errs.MsgPromptRequired)
	}

	claudePath, err := s.lookupCLI("claude")
	if err != nil {
		return nil, fmt.Errorf(errs.MsgClaudeCliMissing)
	}

	args, err := buildClaudeArgs(req)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)

	cmd := exec.CommandContext(ctx, claudePath, args...)
	cmd.Dir = req.WorkDir
	if len(req.EnvOverrides) > 0 {
		cmd.Env = applyEnvOverrides(os.Environ(), req.EnvOverrides)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf(errs.FmtStdoutPipeFail, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf(errs.FmtStderrPipeFail, err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf(errs.FmtClaudeStartClassicFail, err)
	}

	sessionID := uuid.New().String()
	sess := &cliSession{
		cancel:   cancel,
		lastUsed: time.Now(),
	}

	s.mu.Lock()
	s.sessions[sessionID] = sess
	s.mu.Unlock()

	// Get the Wails application instance for event emission.
	app := application.Get()

	// Stream stdout and stderr concurrently
	var wg sync.WaitGroup
	wg.Add(2)
	var recentMu sync.Mutex
	recentOutput := make([]string, 0, 8)

	streamReader := func(r io.Reader, prefix string) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if prefix != "" {
				line = prefix + line
			}
			appendRecentCodexOutput(&recentMu, &recentOutput, line)
			sess.append(line)
			// Emit each line as a real-time event so the frontend can display
			// output without polling.
			app.Event.Emit("cli:line:"+sessionID, line)
		}
	}

	go streamReader(stdoutPipe, "")
	go streamReader(stderrPipe, "")

	go func() {
		wg.Wait()
		waitErr := cmd.Wait()
		cancel()
		var errMsg string
		if waitErr != nil {
			if ctx.Err() == context.DeadlineExceeded {
				errMsg = "执行超时（10 分钟）"
			} else {
				errMsg = waitErr.Error()
				if summary := formatRecentCodexOutput(recentOutput); summary != "" {
					errMsg += "：" + summary
				}
				if logPath := writeClaudeFailureLog(req, args, sessionID, waitErr, recentOutput); logPath != "" {
					errMsg += "；日志：" + logPath
				}
			}
		}
		// Emit done event before marking session finished so listeners receive
		// the terminal signal.
		app.Event.Emit("cli:done:"+sessionID, errMsg)
		sess.finish(errMsg)
	}()

	return &StartClaudeResponse{SessionID: sessionID}, nil
}

func writeClaudeFailureLog(req StartClaudeRequest, args []string, sessionID string, err error, lines []string) string {
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		return ""
	}
	logDir := filepath.Join(home, ".pinru", "logs", "claude-failures")
	if mkdirErr := os.MkdirAll(logDir, 0o755); mkdirErr != nil {
		return ""
	}
	timestamp := time.Now().Format("20060102-150405")
	logPath := filepath.Join(logDir, fmt.Sprintf("%s-%s.log", timestamp, sessionID))

	var sb strings.Builder
	sb.WriteString("time: ")
	sb.WriteString(time.Now().Format(time.RFC3339))
	sb.WriteString("\n")
	sb.WriteString("session_id: ")
	sb.WriteString(sessionID)
	sb.WriteString("\n")
	sb.WriteString("work_dir: ")
	sb.WriteString(req.WorkDir)
	sb.WriteString("\n")
	sb.WriteString("model: ")
	sb.WriteString(req.Model)
	sb.WriteString("\n")
	sb.WriteString("permission_mode: ")
	sb.WriteString(req.PermissionMode)
	sb.WriteString("\n")
	sb.WriteString("error: ")
	sb.WriteString(strings.TrimSpace(fmt.Sprint(err)))
	sb.WriteString("\n")
	sb.WriteString("args: ")
	sb.WriteString(strings.Join(redactClaudePromptArg(args), " "))
	sb.WriteString("\n\n")
	sb.WriteString("recent_output:\n")
	for _, line := range lines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	if writeErr := os.WriteFile(logPath, []byte(sb.String()), 0o644); writeErr != nil {
		return ""
	}
	return logPath
}

func redactClaudePromptArg(args []string) []string {
	result := make([]string, len(args))
	copy(result, args)
	for i := 0; i < len(result)-1; i++ {
		if result[i] == "-p" {
			result[i+1] = "<prompt redacted>"
			i++
		}
	}
	return result
}

// PollOutput returns new output lines since the given offset.
func (s *CliService) PollOutput(req PollOutputRequest) (*PollOutputResponse, error) {
	s.mu.Lock()
	sess, ok := s.sessions[req.SessionID]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf(errs.FmtSessionNotFound, req.SessionID)
	}

	lines, done, errMsg := sess.poll(req.Offset)
	if lines == nil {
		lines = []string{}
	}
	return &PollOutputResponse{
		Lines:  lines,
		Done:   done,
		ErrMsg: errMsg,
	}, nil
}

// CancelSession terminates a running claude session.
func (s *CliService) CancelSession(sessionID string) error {
	s.mu.Lock()
	sess, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return nil // already gone or never existed — treat as success
	}
	sess.cancel()
	return nil
}

// ListSkills scans ~/.claude/skills/, reads SKILL.md frontmatter from each
// subdirectory, and returns a sorted list of SkillItem.
func (s *CliService) ListSkills() ([]SkillItem, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf(errs.FmtUserDirFail, err)
	}
	skillsDir := filepath.Join(home, ".claude", "skills")

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SkillItem{}, nil
		}
		return nil, fmt.Errorf(errs.FmtReadSkillDirFail, err)
	}

	var skills []SkillItem
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillMD := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(skillMD)
		if err != nil {
			continue
		}
		name, desc := parseSkillFrontmatter(string(data), entry.Name())
		skills = append(skills, SkillItem{Name: name, Description: desc})
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	return skills, nil
}

// parseSkillFrontmatter extracts name and description from YAML frontmatter.
// Falls back to dirName for name and empty string for description.
func parseSkillFrontmatter(content, dirName string) (name, description string) {
	name = dirName
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return
	}
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "---" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "name":
			if val != "" {
				name = val
			}
		case "description":
			if val != "" {
				description = val
			}
		}
	}
	return
}

// InstallBuiltinSkills writes all skills bundled with PINRU to ~/.claude/skills/.
// Always overwrites to keep the installed version in sync with the binary.
// Manual dir placeholders ({{MANUAL_DIR}}) in skill content are replaced with
// the platform-appropriate path before writing.
func (s *CliService) InstallBuiltinSkills() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	manualDir := util.PinruManualDir()
	for dirName, content := range builtinSkills {
		skillDir := filepath.Join(home, ".claude", "skills", dirName)
		skillFile := filepath.Join(skillDir, "SKILL.md")
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			continue
		}
		resolved := strings.ReplaceAll(content, "{{MANUAL_DIR}}", manualDir)
		_ = os.WriteFile(skillFile, []byte(resolved), 0o644)
	}
}

// InstallBuiltinManuals extracts the bundled execution manuals to the
// platform data directory (~/.pinru/manuals/). Always overwrites to keep
// the installed version in sync with the binary.
func (s *CliService) InstallBuiltinManuals() {
	destDir := util.PinruManualDir()
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return
	}
	entries, err := manualFS.ReadDir("manuals")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := manualFS.ReadFile("manuals/" + entry.Name())
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(destDir, entry.Name()), data, 0o644)
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func validatePermissionMode(mode string) error {
	trimmed := strings.TrimSpace(mode)
	if trimmed == "" || trimmed == "default" {
		return nil
	}
	switch trimmed {
	case "acceptEdits", "auto", "dontAsk", "plan":
		return nil
	case "yolo", "bypassPermissions":
		return nil
	}
	return fmt.Errorf(errs.FmtUnsupportedPermission, trimmed)
}

func buildClaudeArgs(req StartClaudeRequest) ([]string, error) {
	// Build the final prompt with thinking depth prefix and mode annotation
	finalPrompt := buildPrompt(req.Prompt, req.ThinkingDepth, req.Mode)
	if err := validatePermissionMode(req.PermissionMode); err != nil {
		return nil, err
	}

	args := []string{"-p", finalPrompt}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	permissionMode := normalizePermissionMode(req.PermissionMode)
	if permissionMode != "" {
		args = append(args, "--permission-mode", permissionMode)
	}

	if shouldSkipPermissions(req.PermissionMode) {
		args = append(args, "--dangerously-skip-permissions")
	}

	additionalDirs := uniqueNonEmptyStrings(req.AdditionalDirs)
	if len(additionalDirs) > 0 {
		args = append(args, "--add-dir")
		args = append(args, additionalDirs...)
	}

	if req.Mode == "plan" {
		// Plan mode: restrict to read-only tools so claude only plans, doesn't execute.
		args = append(args, "--allowedTools", "Read,Glob,Grep,WebFetch,WebSearch")
	}

	return args, nil
}

func normalizePermissionMode(mode string) string {
	trimmed := strings.TrimSpace(mode)
	switch trimmed {
	case "", "default":
		return ""
	case "yolo":
		return "bypassPermissions"
	default:
		return trimmed
	}
}

func shouldSkipPermissions(mode string) bool {
	trimmed := strings.TrimSpace(mode)
	return trimmed == "" || trimmed == "default" || trimmed == "yolo" || trimmed == "bypassPermissions"
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))

	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}

	return result
}

// applyEnvOverrides merges overrides into base environment slice.
// Each entry in the returned slice has the form "KEY=VALUE".
// Override keys replace any existing entries for the same key.
func applyEnvOverrides(base []string, overrides map[string]string) []string {
	// Build a set of keys to override so we can skip duplicates from base.
	skip := make(map[string]struct{}, len(overrides))
	for k := range overrides {
		skip[strings.ToUpper(k)] = struct{}{}
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key := entry
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			key = entry[:idx]
		}
		if _, shouldOverride := skip[strings.ToUpper(key)]; !shouldOverride {
			result = append(result, entry)
		}
	}
	for k, v := range overrides {
		result = append(result, k+"="+v)
	}
	return result
}

// ─── Codex Review ────────────────────────────────────────────────────────────

type CodexReviewIssue struct {
	Title        string `json:"title"`
	IssueType    string `json:"issueType"`
	ReviewNotes  string `json:"reviewNotes"`
	NextPrompt   string `json:"nextPrompt"`
	KeyLocations string `json:"keyLocations"`
}

// CodexReviewResult is the structured output from the pg-code review skill.
type CodexReviewResult struct {
	IsCompleted        bool               `json:"isCompleted"`
	IsSatisfied        bool               `json:"isSatisfied"`
	ProjectType        string             `json:"projectType"`
	ChangeScope        string             `json:"changeScope"`
	ReviewNotes        string             `json:"reviewNotes"`
	NextPrompt         string             `json:"nextPrompt"`
	NextPromptTaskType string             `json:"nextPromptTaskType"`
	KeyLocations       string             `json:"keyLocations"`
	Issues             []CodexReviewIssue `json:"issues"`
}

type CodexReviewRequest struct {
	LocalPath         string               `json:"localPath"`
	TaskID            string               `json:"taskId"`
	ModelRunID        string               `json:"modelRunId"`
	ReviewRound       int                  `json:"reviewRound"`
	CommitSHA         string               `json:"commitSha"`
	CommitURL         string               `json:"commitUrl"`
	RepoURL           string               `json:"repoUrl"`
	OriginalPrompt    string               `json:"originalPrompt"`
	CurrentPrompt     string               `json:"currentPrompt"`
	ParentReviewNotes string               `json:"parentReviewNotes"`
	IssueType         string               `json:"issueType"`
	IssueTitle        string               `json:"issueTitle"`
	ModelName         string               `json:"modelName"`
	DeepSeek          *DeepSeekCodexConfig `json:"-"`
}

type DissatisfactionSummaryRequest struct {
	LocalPath        string               `json:"localPath"`
	ModelName        string               `json:"modelName"`
	OriginalPrompt   string               `json:"originalPrompt"`
	CurrentPrompt    string               `json:"currentPrompt"`
	ReviewNotes      string               `json:"reviewNotes"`
	ProjectType      string               `json:"projectType"`
	ChangeScope      string               `json:"changeScope"`
	KeyLocations     string               `json:"keyLocations"`
	ProductSatisfied bool                 `json:"productSatisfied"`
	DeepSeek         *DeepSeekCodexConfig `json:"-"`
}

type DissatisfactionSummaryResult struct {
	Summary string `json:"summary"`
}

type pgCodeContextEnvelope struct {
	BaseDir  string                 `json:"base_dir"`
	Projects []pgCodeProjectContext `json:"projects"`
}

type pgCodeProjectContext struct {
	InputPath      string               `json:"input_path"`
	ResolvedPath   string               `json:"resolved_path"`
	Exists         bool                 `json:"exists"`
	ProjectIDGuess string               `json:"project_id_guess"`
	Git            pgCodeGitContext     `json:"git"`
	ReviewCommit   *pgCodeCommitContext `json:"review_commit,omitempty"`
	RecentFiles    []pgCodeRecentFile   `json:"recent_files"`
	FileEvidence   []pgCodeFileEvidence `json:"file_evidence,omitempty"`
	Summary        pgCodeProjectSummary `json:"summary"`
}

type pgCodeGitContext struct {
	InGit           bool     `json:"in_git"`
	RepoRoot        *string  `json:"repo_root"`
	StatusLines     []string `json:"status_lines"`
	ChangedFiles    []string `json:"changed_files"`
	ChangedFilesRaw []string `json:"changed_files_repo_relative"`
}

type pgCodeCommitContext struct {
	CommitSHA       string   `json:"commit_sha"`
	CommitURL       string   `json:"commit_url,omitempty"`
	RepoURL         string   `json:"repo_url,omitempty"`
	Found           bool     `json:"found"`
	RepoRoot        *string  `json:"repo_root,omitempty"`
	Subject         string   `json:"subject,omitempty"`
	ChangedFiles    []string `json:"changed_files"`
	ChangedFilesRaw []string `json:"changed_files_repo_relative"`
	NameStatusLines []string `json:"name_status_lines"`
	Stat            string   `json:"stat,omitempty"`
	Error           string   `json:"error,omitempty"`
}

type pgCodeRecentFile struct {
	Path         string `json:"path"`
	RelativePath string `json:"relative_path"`
	MTime        string `json:"mtime"`
}

type pgCodeFileEvidence struct {
	RelativePath string `json:"relative_path"`
	Source       string `json:"source"`
	Snippet      string `json:"snippet"`
}

type pgCodeProjectSummary struct {
	TopLevelEntries []string       `json:"top_level_entries"`
	Extensions      map[string]int `json:"extensions"`
}

// RunCodexReview executes the codex pg-code skill non-interactively on the given
// localPath, streaming each output line to onLine (may be nil), and returns the
// structured review result parsed from the --output-schema JSON file.
func (s *CliService) RunCodexReview(ctx context.Context, req CodexReviewRequest, onLine func(string)) (*CodexReviewResult, error) {
	codexPath, err := s.lookupCLI("codex")
	if err != nil {
		return nil, fmt.Errorf(errs.MsgCodexCliMissing)
	}
	localPath := strings.TrimSpace(req.LocalPath)
	if localPath == "" {
		return nil, fmt.Errorf(errs.MsgLocalPathRequired)
	}
	if strings.TrimSpace(req.OriginalPrompt) == "" && strings.TrimSpace(req.CurrentPrompt) == "" {
		return nil, fmt.Errorf(errs.MsgReviewPromptMissing)
	}

	reviewContext, ctxErr := s.collectPgCodeReviewContext(ctx, localPath)
	if ctxErr != nil {
		slog.Warn("collectPgCodeReviewContext failed", "localPath", localPath, "error", ctxErr)
	}
	if reviewContext != nil {
		attachReviewCommitContext(ctx, reviewContext, localPath, req)
	}
	reviewPrompt := buildCodexReviewPrompt(req, reviewContext)
	if runtime.GOOS == "windows" {
		reviewPrompt = compactPromptForWindowsCommandLine(reviewPrompt)
	}

	// Write bundled schema to a temp file.
	schemaFile, err := os.CreateTemp("", "pinru-review-schema-*.json")
	if err != nil {
		return nil, fmt.Errorf(errs.FmtSchemaTempFileFail, err)
	}
	schemaPath := schemaFile.Name()
	defer os.Remove(schemaPath)
	if _, err := schemaFile.Write(pgCodeReviewSchema); err != nil {
		schemaFile.Close()
		return nil, fmt.Errorf(errs.FmtWriteSchemaFail, err)
	}
	schemaFile.Close()

	// Temp file for the last-message output.
	outFile, err := os.CreateTemp("", "pinru-review-out-*.json")
	if err != nil {
		return nil, fmt.Errorf(errs.FmtOutputTempFileFail, err)
	}
	outPath := outFile.Name()
	outFile.Close()
	defer os.Remove(outPath)

	args := []string{
		"exec", reviewPrompt,
		"-C", localPath,
		"--dangerously-bypass-approvals-and-sandbox",
		"--output-schema", schemaPath,
		"-o", outPath,
		"--ephemeral",
	}

	cmd := exec.CommandContext(ctx, codexPath, args...)
	cmd.Dir = localPath
	envOverrides := map[string]string{
		"PINRU_CODEX_REVIEW_OUTPUT_PATH": outPath,
	}
	if req.DeepSeek != nil {
		codexHome, cleanup, err := prepareDeepSeekCodexHome(*req.DeepSeek)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		envOverrides["CODEX_HOME"] = codexHome
	}
	cmd.Env = applyEnvOverrides(os.Environ(), envOverrides)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf(errs.FmtStdoutPipeWrap, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf(errs.FmtStderrPipeWrap, err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf(errs.FmtCodexStartFail, err)
	}

	// Stream stdout and stderr, forwarding each line to the caller.
	var wg sync.WaitGroup
	wg.Add(2)
	var recentOutputMu sync.Mutex
	recentOutput := make([]string, 0, 8)
	streamPipe := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			appendRecentCodexOutput(&recentOutputMu, &recentOutput, line)
			if onLine != nil {
				onLine(line)
			}
		}
	}
	go streamPipe(stdoutPipe)
	go streamPipe(stderrPipe)
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if summary := formatRecentCodexOutput(recentOutput); summary != "" {
			return nil, fmt.Errorf(errs.FmtCodexRunFailWithSummary, err, summary)
		}
		return nil, fmt.Errorf(errs.FmtCodexRunFail, err)
	}

	// Parse the structured output file.
	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf(errs.FmtCodexReadOutputFail, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf(errs.MsgCodexNoStructuredOutput)
	}

	var result CodexReviewResult
	if err := json.Unmarshal(data, &result); err != nil {
		slog.Error("codex 输出 JSON 解析失败", "err", err, "raw", string(data))
		return nil, fmt.Errorf(errs.FmtCodexParseJSONFail, err)
	}
	applyCodexReviewEvidenceGuards(localPath, reviewContext, &result)
	polishReviewNextPrompt(&result, req)
	return &result, nil
}

func (s *CliService) RunCodexDissatisfactionSummary(ctx context.Context, req DissatisfactionSummaryRequest, onLine func(string)) (*DissatisfactionSummaryResult, error) {
	codexPath, err := s.lookupCLI("codex")
	if err != nil {
		return nil, fmt.Errorf(errs.MsgCodexCliMissing)
	}
	localPath := strings.TrimSpace(req.LocalPath)
	if localPath == "" {
		return nil, fmt.Errorf(errs.MsgLocalPathRequired)
	}
	if strings.TrimSpace(req.ReviewNotes) == "" {
		return nil, fmt.Errorf("复审点评为空，无法整理不满意原因")
	}

	prompt := buildDissatisfactionSummaryPrompt(req)
	if runtime.GOOS == "windows" {
		prompt = compactPromptForWindowsCommandLine(prompt)
	}

	schemaFile, err := os.CreateTemp("", "pinru-dissatisfaction-schema-*.json")
	if err != nil {
		return nil, fmt.Errorf(errs.FmtSchemaTempFileFail, err)
	}
	schemaPath := schemaFile.Name()
	defer os.Remove(schemaPath)
	if _, err := schemaFile.Write(dissatisfactionSummarySchema); err != nil {
		schemaFile.Close()
		return nil, fmt.Errorf(errs.FmtWriteSchemaFail, err)
	}
	schemaFile.Close()

	outFile, err := os.CreateTemp("", "pinru-dissatisfaction-out-*.json")
	if err != nil {
		return nil, fmt.Errorf(errs.FmtOutputTempFileFail, err)
	}
	outPath := outFile.Name()
	outFile.Close()
	defer os.Remove(outPath)

	args := []string{
		"exec", prompt,
		"-C", localPath,
		"--dangerously-bypass-approvals-and-sandbox",
		"--output-schema", schemaPath,
		"-o", outPath,
		"--ephemeral",
	}

	cmd := exec.CommandContext(ctx, codexPath, args...)
	cmd.Dir = localPath
	envOverrides := map[string]string{
		"PINRU_CODEX_DISSATISFACTION_OUTPUT_PATH": outPath,
	}
	if req.DeepSeek != nil {
		codexHome, cleanup, err := prepareDeepSeekCodexHome(*req.DeepSeek)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		envOverrides["CODEX_HOME"] = codexHome
	}
	cmd.Env = applyEnvOverrides(os.Environ(), envOverrides)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf(errs.FmtStdoutPipeWrap, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf(errs.FmtStderrPipeWrap, err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf(errs.FmtCodexStartFail, err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	var recentOutputMu sync.Mutex
	recentOutput := make([]string, 0, 8)
	streamPipe := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			appendRecentCodexOutput(&recentOutputMu, &recentOutput, line)
			if onLine != nil {
				onLine(line)
			}
		}
	}
	go streamPipe(stdoutPipe)
	go streamPipe(stderrPipe)
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if summary := formatRecentCodexOutput(recentOutput); summary != "" {
			return nil, fmt.Errorf(errs.FmtCodexRunFailWithSummary, err, summary)
		}
		return nil, fmt.Errorf(errs.FmtCodexRunFail, err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf(errs.FmtCodexReadOutputFail, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf(errs.MsgCodexNoStructuredOutput)
	}

	var result DissatisfactionSummaryResult
	if err := json.Unmarshal(data, &result); err != nil {
		slog.Error("codex 不满意原因总结 JSON 解析失败", "err", err, "raw", string(data))
		return nil, fmt.Errorf(errs.FmtCodexParseJSONFail, err)
	}
	result.Summary = normalizeDissatisfactionSummary(result.Summary)
	if result.Summary == "" {
		return nil, fmt.Errorf("不满意原因总结为空")
	}
	if result.Summary == "异常输出" {
		return &result, nil
	}
	if !strings.Contains(result.Summary, "过程不满意：") || !strings.Contains(result.Summary, "产物不满意：") {
		return nil, fmt.Errorf("不满意原因总结缺少过程或产物段")
	}
	return &result, nil
}

func appendRecentCodexOutput(mu *sync.Mutex, lines *[]string, line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	if len(*lines) == 8 {
		copy((*lines)[0:], (*lines)[1:])
		*lines = (*lines)[:7]
	}
	*lines = append(*lines, trimmed)
}

func formatRecentCodexOutput(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, " | ")
}

func (s *CliService) reviewContextScriptPath() string {
	if strings.TrimSpace(s.reviewContextPath) != "" {
		return s.reviewContextPath
	}
	candidates := defaultPgCodeContextScriptPaths()
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func (s *CliService) collectPgCodeReviewContext(ctx context.Context, localPath string) (*pgCodeProjectContext, error) {
	scriptPath := strings.TrimSpace(s.reviewContextScriptPath())
	if scriptPath == "" {
		return collectNativePgCodeReviewContext(ctx, localPath)
	}
	if _, err := os.Stat(scriptPath); err != nil {
		slog.Warn("pg-code context script unavailable, using native collector", "scriptPath", scriptPath, "error", err)
		return collectNativePgCodeReviewContext(ctx, localPath)
	}

	pythonPath, err := s.lookupCLI("python3")
	if err != nil {
		slog.Warn("python3 unavailable for pg-code context script, using native collector", "error", err)
		return collectNativePgCodeReviewContext(ctx, localPath)
	}

	cmd := exec.CommandContext(ctx, pythonPath, scriptPath, localPath)
	cmd.Dir = localPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		trimmed := strings.TrimSpace(string(output))
		if trimmed != "" {
			slog.Warn("pg-code context script failed, using native collector", "error", err, "output", trimmed)
			return collectNativePgCodeReviewContext(ctx, localPath)
		}
		slog.Warn("pg-code context script failed, using native collector", "error", err)
		return collectNativePgCodeReviewContext(ctx, localPath)
	}

	var envelope pgCodeContextEnvelope
	if err := json.Unmarshal(output, &envelope); err != nil {
		slog.Warn("pg-code context script output invalid, using native collector", "error", err)
		return collectNativePgCodeReviewContext(ctx, localPath)
	}
	if len(envelope.Projects) == 0 {
		slog.Warn("pg-code context script returned no projects, using native collector", "scriptPath", scriptPath)
		return collectNativePgCodeReviewContext(ctx, localPath)
	}
	return &envelope.Projects[0], nil
}

var nativeContextIgnoredDirs = map[string]struct{}{
	".git": {}, ".hg": {}, ".svn": {}, "node_modules": {}, "dist": {}, "build": {},
	"coverage": {}, ".next": {}, ".nuxt": {}, ".idea": {}, ".vscode": {}, "__pycache__": {},
}

var nativeContextIgnoredSuffixes = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {}, ".svg": {},
	".ico": {}, ".mp3": {}, ".wav": {}, ".ogg": {}, ".mp4": {}, ".mov": {},
	".pdf": {}, ".zip": {}, ".tar": {}, ".gz": {}, ".lock": {},
}

var nativeContextIgnoredNames = map[string]struct{}{
	".DS_Store": {},
}

type nativeCommandResult struct {
	code   int
	stdout string
	stderr string
}

func collectNativePgCodeReviewContext(ctx context.Context, localPath string) (*pgCodeProjectContext, error) {
	inputPath := strings.TrimSpace(localPath)
	if inputPath == "" {
		return nil, nil
	}

	resolvedPath, err := filepath.Abs(inputPath)
	if err != nil {
		resolvedPath = filepath.Clean(inputPath)
	}
	if realPath, err := filepath.EvalSymlinks(resolvedPath); err == nil {
		resolvedPath = realPath
	}

	project := &pgCodeProjectContext{
		InputPath:      inputPath,
		ResolvedPath:   resolvedPath,
		Exists:         false,
		ProjectIDGuess: guessNativeProjectID(resolvedPath),
		Git: pgCodeGitContext{
			InGit:           false,
			RepoRoot:        nil,
			StatusLines:     []string{},
			ChangedFiles:    []string{},
			ChangedFilesRaw: []string{},
		},
		RecentFiles:  []pgCodeRecentFile{},
		FileEvidence: []pgCodeFileEvidence{},
		Summary: pgCodeProjectSummary{
			TopLevelEntries: []string{},
			Extensions:      map[string]int{},
		},
	}

	info, statErr := os.Stat(resolvedPath)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return project, nil
		}
		return project, statErr
	}
	project.Exists = true

	root := resolvedPath
	if !info.IsDir() {
		root = filepath.Dir(resolvedPath)
	}
	project.Git = collectNativeGitContext(ctx, root, 40)
	project.RecentFiles = collectNativeRecentFiles(ctx, root, 12)
	project.FileEvidence = collectPgCodeFileEvidence(ctx, root, project.Git.ChangedFiles, project.RecentFiles, 8)
	project.Summary = summarizeNativeProjectFiles(root, project.Git.ChangedFiles, project.RecentFiles)
	return project, nil
}

func runNativeCommand(ctx context.Context, args ...string) nativeCommandResult {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		code = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
	}
	return nativeCommandResult{
		code:   code,
		stdout: strings.TrimRight(stdout.String(), "\n"),
		stderr: strings.TrimRight(stderr.String(), "\n"),
	}
}

func collectNativeGitContext(ctx context.Context, scope string, changedLimit int) pgCodeGitContext {
	context := pgCodeGitContext{
		InGit:           false,
		RepoRoot:        nil,
		StatusLines:     []string{},
		ChangedFiles:    []string{},
		ChangedFilesRaw: []string{},
	}
	if ctx.Err() != nil {
		return context
	}

	rootResult := runNativeCommand(ctx, "git", "-C", scope, "rev-parse", "--show-toplevel")
	if rootResult.code != 0 || strings.TrimSpace(rootResult.stdout) == "" {
		return context
	}

	repoRoot := strings.TrimSpace(rootResult.stdout)
	if absRoot, err := filepath.Abs(repoRoot); err == nil {
		repoRoot = absRoot
	}
	context.InGit = true
	context.RepoRoot = &repoRoot

	relScope, err := filepath.Rel(repoRoot, scope)
	if err != nil || relScope == "" {
		relScope = "."
	}
	relScope = filepath.ToSlash(relScope)

	statusResult := runNativeCommand(ctx, "git", "-C", repoRoot, "status", "--porcelain", "--untracked-files=all", "--", relScope)
	if statusResult.code == 0 {
		context.StatusLines = limitStrings(nonEmptyLines(statusResult.stdout), changedLimit)
	}

	changedRaw := make([]string, 0, len(context.StatusLines))
	for _, line := range context.StatusLines {
		if len(line) < 4 {
			continue
		}
		changedRaw = append(changedRaw, normalizeNativeStatusPath(line[3:]))
	}
	if len(changedRaw) == 0 {
		diffResult := runNativeCommand(ctx, "git", "-C", repoRoot, "diff", "--name-only", "HEAD", "--", relScope)
		if diffResult.code == 0 {
			changedRaw = nonEmptyLines(diffResult.stdout)
		}
	}

	changedRaw = limitStrings(uniqueStrings(changedRaw), changedLimit)
	context.ChangedFilesRaw = changedRaw
	context.ChangedFiles = repoRelativeToProjectRelative(repoRoot, scope, changedRaw)
	return context
}

func attachReviewCommitContext(ctx context.Context, project *pgCodeProjectContext, localPath string, req CodexReviewRequest) {
	commitSHA := strings.TrimSpace(req.CommitSHA)
	if project == nil || commitSHA == "" || ctx.Err() != nil {
		return
	}
	scope := strings.TrimSpace(localPath)
	if scope == "" {
		scope = project.ResolvedPath
	}
	if scope == "" {
		scope = project.InputPath
	}
	if scope == "" {
		return
	}

	commitContext := collectReviewCommitContext(ctx, scope, commitSHA, strings.TrimSpace(req.CommitURL), strings.TrimSpace(req.RepoURL), 80)
	project.ReviewCommit = &commitContext
	if !commitContext.Found {
		return
	}

	project.Git.ChangedFilesRaw = limitStrings(uniqueStrings(commitContext.ChangedFilesRaw), 80)
	project.Git.ChangedFiles = limitStrings(uniqueStrings(commitContext.ChangedFiles), 80)
	if project.Git.RepoRoot == nil && commitContext.RepoRoot != nil {
		project.Git.RepoRoot = commitContext.RepoRoot
	}
	project.Git.InGit = true
	project.FileEvidence = collectPgCodeFileEvidence(ctx, scope, project.Git.ChangedFiles, project.RecentFiles, 8)
	project.Summary = summarizeNativeProjectFiles(scope, project.Git.ChangedFiles, project.RecentFiles)
}

func collectReviewCommitContext(ctx context.Context, scope, commitSHA, commitURL, repoURL string, changedLimit int) pgCodeCommitContext {
	result := pgCodeCommitContext{
		CommitSHA:       commitSHA,
		CommitURL:       commitURL,
		RepoURL:         repoURL,
		Found:           false,
		ChangedFiles:    []string{},
		ChangedFilesRaw: []string{},
		NameStatusLines: []string{},
	}
	if ctx.Err() != nil {
		return result
	}

	rootResult := runNativeCommand(ctx, "git", "-C", scope, "rev-parse", "--show-toplevel")
	if rootResult.code != 0 || strings.TrimSpace(rootResult.stdout) == "" {
		result.Error = strings.TrimSpace(rootResult.stderr)
		if result.Error == "" {
			result.Error = "当前目录不是 Git 仓库，无法读取本轮 commit"
		}
		return result
	}
	repoRoot := strings.TrimSpace(rootResult.stdout)
	if absRoot, err := filepath.Abs(repoRoot); err == nil {
		repoRoot = absRoot
	}
	result.RepoRoot = &repoRoot

	verifyResult := runNativeCommand(ctx, "git", "-C", repoRoot, "rev-parse", "--verify", commitSHA+"^{commit}")
	if verifyResult.code != 0 || strings.TrimSpace(verifyResult.stdout) == "" {
		result.Error = strings.TrimSpace(verifyResult.stderr)
		if result.Error == "" {
			result.Error = "本地仓库中找不到该 commit，无法读取本轮改动"
		}
		return result
	}
	result.CommitSHA = strings.TrimSpace(verifyResult.stdout)
	result.Found = true

	subjectResult := runNativeCommand(ctx, "git", "-C", repoRoot, "show", "-s", "--format=%s", result.CommitSHA)
	if subjectResult.code == 0 {
		result.Subject = strings.TrimSpace(subjectResult.stdout)
	}

	nameStatusResult := runNativeCommand(ctx, "git", "-C", repoRoot, "show", "--name-status", "--format=", result.CommitSHA)
	if nameStatusResult.code == 0 {
		result.NameStatusLines = limitStrings(nonEmptyLines(nameStatusResult.stdout), changedLimit)
		result.ChangedFilesRaw = limitStrings(uniqueStrings(commitNameStatusPaths(result.NameStatusLines)), changedLimit)
		result.ChangedFiles = repoRelativeToProjectRelative(repoRoot, scope, result.ChangedFilesRaw)
	} else {
		result.Error = strings.TrimSpace(nameStatusResult.stderr)
	}

	statResult := runNativeCommand(ctx, "git", "-C", repoRoot, "show", "--stat", "--oneline", "--no-renames", "--format=short", result.CommitSHA)
	if statResult.code == 0 {
		result.Stat = limitText(statResult.stdout, 6000)
	}

	return result
}

func commitNameStatusPaths(lines []string) []string {
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) >= 3 && (strings.HasPrefix(fields[0], "R") || strings.HasPrefix(fields[0], "C")) {
			paths = append(paths, normalizeNativeStatusPath(fields[len(fields)-1]))
			continue
		}
		if len(fields) >= 2 {
			paths = append(paths, normalizeNativeStatusPath(fields[len(fields)-1]))
		}
	}
	return paths
}

func normalizeNativeStatusPath(raw string) string {
	if strings.Contains(raw, " -> ") {
		parts := strings.Split(raw, " -> ")
		raw = parts[len(parts)-1]
	}
	return strings.TrimSpace(raw)
}

func repoRelativeToProjectRelative(repoRoot, scope string, repoRelativePaths []string) []string {
	result := make([]string, 0, len(repoRelativePaths))
	for _, repoRelative := range repoRelativePaths {
		absolute := filepath.Join(repoRoot, filepath.FromSlash(repoRelative))
		if rel, err := filepath.Rel(scope, absolute); err == nil && !strings.HasPrefix(rel, "..") {
			result = append(result, filepath.ToSlash(rel))
			continue
		}
		result = append(result, filepath.ToSlash(repoRelative))
	}
	return result
}

func collectNativeRecentFiles(ctx context.Context, root string, limit int) []pgCodeRecentFile {
	type fileEntry struct {
		mtime time.Time
		path  string
	}
	entries := make([]fileEntry, 0, limit)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if _, ignored := nativeContextIgnoredDirs[name]; ignored {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ignored := nativeContextIgnoredNames[name]; ignored {
			return nil
		}
		if _, ignored := nativeContextIgnoredSuffixes[strings.ToLower(filepath.Ext(name))]; ignored {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return nil
		}
		entries = append(entries, fileEntry{mtime: info.ModTime(), path: path})
		return nil
	})

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].mtime.Equal(entries[j].mtime) {
			return entries[i].path < entries[j].path
		}
		return entries[i].mtime.After(entries[j].mtime)
	})

	if len(entries) > limit {
		entries = entries[:limit]
	}
	result := make([]pgCodeRecentFile, 0, len(entries))
	for _, entry := range entries {
		rel, err := filepath.Rel(root, entry.path)
		if err != nil {
			rel = entry.path
		}
		result = append(result, pgCodeRecentFile{
			Path:         entry.path,
			RelativePath: filepath.ToSlash(rel),
			MTime:        entry.mtime.Format("2006-01-02T15:04:05"),
		})
	}
	return result
}

func collectPgCodeFileEvidence(ctx context.Context, root string, changedFiles []string, recentFiles []pgCodeRecentFile, limit int) []pgCodeFileEvidence {
	candidates := make([]pgCodeFileEvidence, 0, limit)
	seen := make(map[string]struct{}, limit)

	appendFile := func(relPath, source string) {
		if ctx.Err() != nil || len(candidates) >= limit {
			return
		}
		relPath = filepath.ToSlash(strings.TrimSpace(relPath))
		if relPath == "" {
			return
		}
		if _, ok := seen[relPath]; ok {
			return
		}
		snippet := readProjectFileEvidence(root, relPath)
		if snippet == "" {
			return
		}
		seen[relPath] = struct{}{}
		candidates = append(candidates, pgCodeFileEvidence{
			RelativePath: relPath,
			Source:       source,
			Snippet:      snippet,
		})
	}

	for _, relPath := range changedFiles {
		appendFile(relPath, "changed_file")
	}
	for _, file := range recentFiles {
		appendFile(file.RelativePath, "recent_file")
	}
	return candidates
}

func readProjectFileEvidence(root, relPath string) string {
	absolute := filepath.Join(root, filepath.FromSlash(relPath))
	info, err := os.Stat(absolute)
	if err != nil || info.IsDir() {
		return ""
	}
	name := info.Name()
	if _, ignored := nativeContextIgnoredNames[name]; ignored {
		return ""
	}
	if _, ignored := nativeContextIgnoredSuffixes[strings.ToLower(filepath.Ext(name))]; ignored {
		return ""
	}

	data, err := os.ReadFile(absolute)
	if err != nil || len(data) == 0 {
		return ""
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return ""
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	collected := make([]string, 0, 24)
	nonEmpty := 0
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		collected = append(collected, fmt.Sprintf("%d: %s", idx+1, limitText(trimmed, 180)))
		nonEmpty++
		if nonEmpty >= 24 {
			break
		}
	}
	if len(collected) == 0 {
		return ""
	}
	return limitText(strings.Join(collected, "\n"), 2400)
}

func summarizeNativeProjectFiles(root string, changedFiles []string, recentFiles []pgCodeRecentFile) pgCodeProjectSummary {
	sourcePaths := make([]string, 0, len(changedFiles)+len(recentFiles))
	for _, rel := range changedFiles {
		sourcePaths = append(sourcePaths, filepath.Join(root, filepath.FromSlash(rel)))
	}
	if len(sourcePaths) == 0 {
		for _, file := range recentFiles {
			sourcePaths = append(sourcePaths, filepath.Join(root, filepath.FromSlash(file.RelativePath)))
		}
	}

	topCounts := make(map[string]int)
	extCounts := make(map[string]int)
	for _, path := range sourcePaths {
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) > 0 && parts[0] != "" {
			topCounts[parts[0]]++
		} else {
			topCounts["."]++
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == "" {
			ext = "<no-ext>"
		}
		extCounts[ext]++
	}

	return pgCodeProjectSummary{
		TopLevelEntries: topNamesByCount(topCounts, 6),
		Extensions:      topMapByCount(extCounts, 8),
	}
}

func guessNativeProjectID(path string) string {
	for _, part := range pathPartsFromLeaf(path, 4) {
		if strings.Contains(part, "label-") {
			return part
		}
	}
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) {
		return path
	}
	return base
}

func pathPartsFromLeaf(path string, limit int) []string {
	parts := make([]string, 0, limit)
	current := filepath.Clean(path)
	for len(parts) < limit {
		base := filepath.Base(current)
		if base == "." || base == string(filepath.Separator) || base == "" {
			break
		}
		parts = append(parts, base)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return parts
}

func nonEmptyLines(text string) []string {
	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func limitStrings(values []string, limit int) []string {
	if limit >= 0 && len(values) > limit {
		return values[:limit]
	}
	return values
}

func limitText(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "\n...<truncated>"
}

func topNamesByCount(counts map[string]int, limit int) []string {
	items := sortCountKeys(counts)
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func topMapByCount(counts map[string]int, limit int) map[string]int {
	items := sortCountKeys(counts)
	if len(items) > limit {
		items = items[:limit]
	}
	result := make(map[string]int, len(items))
	for _, key := range items {
		result[key] = counts[key]
	}
	return result
}

func sortCountKeys(counts map[string]int) []string {
	items := make([]string, 0, len(counts))
	for key := range counts {
		items = append(items, key)
	}
	sort.Slice(items, func(i, j int) bool {
		if counts[items[i]] == counts[items[j]] {
			return items[i] < items[j]
		}
		return counts[items[i]] > counts[items[j]]
	})
	return items
}

func buildCodexReviewPrompt(req CodexReviewRequest, project *pgCodeProjectContext) string {
	var parts []string
	parts = append(parts, "/pg-code")
	parts = append(parts, strings.TrimSpace(`
补充规则：
1. 【严格限制】只能基于任务提示词、本轮代码变更、最近更新文件以及你实际读取过的文件下结论。若项目上下文包含 review_commit，说明本轮代码已先提交，必须优先以 review_commit.changed_files / review_commit.name_status_lines / review_commit.stat 作为本轮变更依据；不要再用当前工作区 git status/git diff 为空来判断“未发现变更”。没有 review_commit 时，才使用 git status / git diff 列出的文件。
1.1 如果项目上下文里提供了 file_evidence，优先把这些片段当作已读取代码的直接证据；判断完成度、满意度、keyLocations 和 reviewNotes 时，应先引用这些片段，再决定是否继续读取对应文件的其他位置。只有 file_evidence 和你后续实际读取到的代码，才能算“已读取过的文件内容”。
2. 严禁猜测运行效果、页面视觉、接口返回、测试结果或用户体验。
3. keyLocations 只能填写 git 变更文件或最近更新文件中 1 到 3 个你实际核验过的代码位置，写不出时可留空。
4. isCompleted 和 isSatisfied 必须分开判断：核心交付物出现、主流程大体落地时，isCompleted 可填 true；只有主要求覆盖接近 90 分、没有明确主链路缺口、关键边界不影响验收时，isSatisfied 才能填 true。80% 左右只能算“完成但不满意”，不能直接满意通过。
5. 找不到任务提示词或有效改动时，reviewNotes 注明”依据不足”，isCompleted 和 isSatisfied 均填 false；但当 review_commit.found=true 且 changed_files 不为空时，不得把当前工作区 clean 当作无有效改动。
6. projectType 和 changeScope 按最符合实际情况的选项填写。
7. 任务提示词以“当前复核节点上下文”里的 original_prompt/current_prompt 为唯一来源，只把其中明确写出的要求作为验收标准；不要再去读取本地提示词文件，也不要把未写明的扩展点、常识性联想、顺手优化项记为未完成或不满意。parent_review_notes 仅作辅助上下文，不能替代任务提示词本身。
8. 当 isCompleted=false 或 isSatisfied=false 时，reviewNotes 必须回指 original_prompt/current_prompt 中对应的具体句子、短语或明确要求；若拆分到 issues，则每条 issues[*].reviewNotes 也必须分别回指对应 prompt 语句。回指不到的内容不能作为主缺口，不得据此判定未完成或不满意。
9. nextPrompt 只能围绕主缺口补充最小修复指令，必须与已回指的 prompt 要求直接对应，不得扩展额外需求；若拆分到 issues，则每条 issues[*].nextPrompt 也遵守同样规则。Bug修复类 nextPrompt 必须像用户可执行的 bug 修复提示词：写清触发场景、当前异常、修复后的业务结果和必要边界；不要直接写“把 A 放到 B 前面”“修改某函数”“调整某字段”“在某文件里...”这类实现方案，也不要写“验收时确认”“补齐链路”“核验闭环”这类复审口吻，除非 current_prompt 本身就是代码级修复要求。Bug修复类 nextPrompt 不能直接沿用 current_prompt 的长开头，先改写成短的问题名，再写修复后的表现，通常控制在 2 到 3 句。
10. 当本轮发现多个独立问题时，必须通过 issues 数组分别列出；不要把多个问题揉成一条。
11. nextPromptTaskType 根据 nextPrompt 的任务性质填写，只能在“Bug修复、Feature迭代、0-1代码生成、代码理解、代码重构、工程化、代码测试、未归类”中选择；满意且 nextPrompt 为“无”时填“未归类”。
12. issues[*].issueType 默认填“Bug修复”，除非证据明确表明是其他类型。
13. 代码理解类可以按文档交付物判断，通过标准相对宽松：只要新增/修改的说明文档覆盖 prompt 要求的核心链路、关键节点和最终回答问题，可判定满意；不要求运行页面、接口或测试。
14. Feature迭代、0-1代码生成、Bug修复必须收紧：通过不能只建立在“看到相关代码改动”上。需要沿 prompt 的用户触发场景核到入口、数据来源/接口返回、状态或持久化变化、前端展示/用户反馈这些主链路；纯前端任务也要核到状态更新与可见反馈。任一主链路缺口、关键字段未返回、状态分支未覆盖、接口/页面衔接不清，isSatisfied 必须为 false。
15. Bug修复类必须说明原 bug 的触发条件、修复后对应条件为什么不会再复现，以及相邻边界是否覆盖；如果只看到相关文件被修改，但无法证明原触发条件被闭环处理，isCompleted 可为 true，但 isSatisfied 必须为 false。
16. 高风险场景默认加严：权限/角色隔离、金额/优惠/计费、状态流转、导出下载、文件上传、WebSocket/通知、路由匹配、异步刷新、并发或库存容量。只要没有把请求到响应、状态落库到页面回显、异常边界到用户反馈核清楚，就不能判定满意。
17. “未运行页面或接口、仅静态取证”不是自动失败，但对 Feature/Bug/0-1 的跨文件或跨前后端主流程，只能在代码证据已经完整闭环且无关键边界缺口时满意；否则默认完成但不满意，并在 reviewNotes 说明缺少哪段链路证据。
18. 若 isSatisfied=false，reviewNotes 必须给出可核验的具体不满意原因，nextPrompt 必须给出围绕该缺口的最小修复词；不能只写“证据不足”“测试不足”“未运行页面”这类泛化结论。nextPrompt 要描述要修复的用户可感知问题和修复后的业务结果，不要把复审里的代码根因原样改写成代码操作步骤，也不要用复审报告口吻。
18.0 特殊异常分支：如果不满意的原因是没有读取到实际代码变动、没有拿到本轮有效改动或无法确认本轮真实代码变化，reviewNotes 只输出“异常输出”四个字，nextPrompt 填“无”，nextPromptTaskType 填“未归类”，不要给修复提示词，也不要输出“过程不满意/产物不满意”。
18.1 当 review_commit.found=true 或 file_evidence 非空时，不要写“未实际读取这些文件内容”这类和上下文矛盾的表述；只有在 file_evidence 为空且你也没有继续读取文件正文时，才能说明具体缺少哪些代码证据。
19. 若本轮已通过，issues 返回空数组，但 reviewNotes 不能只填“无”；必须用一两句话说明已经核验哪些核心要求、关键代码位置和主链路闭环依据，作为通过依据。
20. 如果产物已经满足 current_prompt 的主要交付要求，但处理过程存在不满意，可以保持 isSatisfied=true、nextPrompt=“无”、nextPromptTaskType=“未归类”，并在 reviewNotes 中明确写出“过程不满意：...”；过程不满意只描述处理过程漏掉的验证、拆分或确认动作，不要伪造成产物缺陷。
21. 红线：只要 isSatisfied=false，nextPrompt 一定不能和 current_prompt/上一轮会话提示词雷同，不能复述上一轮提示词、照抄原句或只替换少量词；必须基于本轮 reviewNotes 中的问题现状重新组织成新的修复提示词。尤其不要连续几轮都用同一个“修复xxx时...”开头。
22. nextPrompt 必须使用自然语言清晰、顺畅、连贯地描述问题现状和修复后验收结果；不要写废话，不要描述问题原因、代码原因或“为什么会这样”，直接描述当前哪里不对、用户或业务会遇到什么、修复后应达到什么状态。reviewNotes 和后续不满意原因也要像人工质检反馈，表达自然、连贯、清楚，不要写成 AI 复审摘要、审计报告或提示词复述，少用“核验”“主链路”“闭环”“可核验”等 AI 味明显的词。
22.1 修复提示词尽量不包含代码，不写代码片段、文件、文件名、文件路径、类名、方法名、变量名或命令等代码细节；除非 current_prompt 本身就是代码级修复要求，否则要把代码细节改写成用户可感知的问题现状和验收结果。
22.2 Bug修复类 nextPrompt 要简洁，避免把复审结论里的证据、代码位置、原因分析和所有边界完整搬进去；同一对象如“评价页”“已启用模板”“维度、权重和必填项”出现一次即可，后面用“对应模板内容”等自然指代。
23. 如果 nextPromptTaskType 或 issues[*].issueType 是 Bug修复，对应 nextPrompt 前面一定要加“修复”两个字。
`))

	reviewInput := map[string]string{
		"issue_title":         strings.TrimSpace(req.IssueTitle),
		"issue_type":          strings.TrimSpace(req.IssueType),
		"model_name":          strings.TrimSpace(req.ModelName),
		"original_prompt":     strings.TrimSpace(req.OriginalPrompt),
		"current_prompt":      strings.TrimSpace(req.CurrentPrompt),
		"parent_review_notes": strings.TrimSpace(req.ParentReviewNotes),
	}
	if contextJSON, err := json.MarshalIndent(reviewInput, "", "  "); err == nil {
		parts = append(parts, "当前复核节点上下文如下，请明确区分“原始任务提示词”“当前节点提示词”“父节点不满意结论”：\n"+string(contextJSON))
	}

	if project != nil {
		contextJSON, err := json.MarshalIndent(project, "", "  ")
		if err == nil {
			parts = append(parts, "下面是预采集到的项目上下文，请优先据此取证，不要忽略证据缺口：\n"+string(contextJSON))
		}
	}

	return strings.Join(parts, "\n\n")
}

func buildDissatisfactionSummaryPrompt(req DissatisfactionSummaryRequest) string {
	var parts []string
	parts = append(parts, strings.TrimSpace(dissatisfactionSummaryPromptTemplate))
	summaryInput := map[string]string{
		"model_name":        strings.TrimSpace(req.ModelName),
		"original_prompt":   strings.TrimSpace(req.OriginalPrompt),
		"current_prompt":    strings.TrimSpace(req.CurrentPrompt),
		"review_notes":      strings.TrimSpace(req.ReviewNotes),
		"project_type":      strings.TrimSpace(req.ProjectType),
		"change_scope":      strings.TrimSpace(req.ChangeScope),
		"key_locations":     strings.TrimSpace(req.KeyLocations),
		"product_satisfied": strconv.FormatBool(req.ProductSatisfied),
	}
	if inputJSON, err := json.MarshalIndent(summaryInput, "", "  "); err == nil {
		parts = append(parts, "现有复审证据如下，只能基于这些内容整理：\n"+string(inputJSON))
	}
	parts = append(parts, "请只输出符合 schema 的 JSON，其中 summary 字段直接填最终可导出的不满意原因。")
	return strings.Join(parts, "\n\n")
}

func normalizeDissatisfactionSummary(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	text = strings.Join(parts, "")
	text = strings.ReplaceAll(text, "过程不满意 :", "过程不满意：")
	text = strings.ReplaceAll(text, "产物不满意 :", "产物不满意：")
	text = strings.ReplaceAll(text, "结果不满意：", "产物不满意：")
	text = strings.ReplaceAll(text, "结果不满意:", "产物不满意：")
	return strings.TrimSpace(text)
}

func compactPromptForWindowsCommandLine(prompt string) string {
	replacer := strings.NewReplacer("\r\n", "\n", "\r", "\n")
	lines := strings.Split(replacer.Replace(prompt), "\n")
	compacted := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			compacted = append(compacted, trimmed)
		}
	}
	return strings.Join(compacted, " ")
}

func applyCodexReviewEvidenceGuards(localPath string, project *pgCodeProjectContext, result *CodexReviewResult) {
	if result == nil {
		return
	}

	// Hard guards: project must exist and have some verifiable changes.
	var hardReasons []string
	if project == nil {
		hardReasons = append(hardReasons, "未采集到复审上下文")
	} else {
		if !project.Exists {
			hardReasons = append(hardReasons, "项目目录不存在")
		}
		if len(project.Git.ChangedFiles) == 0 && len(project.RecentFiles) == 0 {
			hardReasons = append(hardReasons, "缺少可核验的改动或最近文件")
		}
	}

	if len(hardReasons) > 0 {
		result.IsCompleted = false
		result.IsSatisfied = false
		guardNote := "依据不足：" + strings.Join(hardReasons, "；")
		if note := strings.TrimSpace(result.ReviewNotes); note == "" || note == "无" {
			result.ReviewNotes = guardNote
		} else if !strings.Contains(note, "依据不足") {
			result.ReviewNotes = note + "；" + guardNote
		}
		if prompt := strings.TrimSpace(result.NextPrompt); prompt == "" || prompt == "无" {
			result.NextPrompt = "重新处理这轮任务前，先确认任务提示词和本轮改动位置都能被正常读取，否则复审无法判断这次改动是否真的覆盖了需求。"
		}
		return
	}

	if result.IsCompleted && result.IsSatisfied && isEmptyPassingReviewNote(result.ReviewNotes) {
		downgradeCodexReviewWithGuard(
			result,
			"通过依据不足：isSatisfied=true 时 reviewNotes 不能只填“无”，需要说明已核验的核心要求和关键代码证据；当前无法确认主链路已被充分复审。",
			"重新检查本轮改动时，需要说明已经覆盖了哪些核心要求和关键代码位置；如果入口、接口、状态或页面反馈还有缺口，就按实际缺口继续修复。",
			"未归类",
		)
	}

	if result.IsCompleted && result.IsSatisfied {
		if guard := strictCodexReviewPassGuard(result); guard != nil {
			downgradeCodexReviewWithGuard(result, guard.reviewNotes, guard.nextPrompt, guard.nextPromptTaskType)
		}
	}

	// Soft guard: invalid key locations are noted but do not override the
	// AI's pass/fail judgment.
	if countValidKeyLocations(localPath, result.KeyLocations) == 0 && strings.TrimSpace(result.KeyLocations) != "" {
		note := "注：关键代码位置格式无效"
		if existing := strings.TrimSpace(result.ReviewNotes); existing == "" || existing == "无" {
			result.ReviewNotes = note
		} else {
			result.ReviewNotes = existing + "；" + note
		}
	}
}

type codexReviewPassGuard struct {
	reviewNotes        string
	nextPrompt         string
	nextPromptTaskType string
}

func downgradeCodexReviewWithGuard(result *CodexReviewResult, reviewNotes, nextPrompt, nextPromptTaskType string) {
	result.IsSatisfied = false
	result.ReviewNotes = mergeGuardReviewNotes(result.ReviewNotes, reviewNotes)
	if isEmptyNextPrompt(result.NextPrompt) {
		result.NextPrompt = nextPrompt
	}
	if strings.TrimSpace(result.NextPromptTaskType) == "" || strings.TrimSpace(result.NextPromptTaskType) == "未归类" {
		result.NextPromptTaskType = nextPromptTaskType
	}
}

func mergeGuardReviewNotes(existing, guard string) string {
	existing = strings.TrimSpace(existing)
	guard = strings.TrimSpace(guard)
	if existing == "" || existing == "无" {
		return guard
	}
	if guard == "" || strings.Contains(existing, guard) {
		return existing
	}
	return existing + "；" + guard
}

func isEmptyNextPrompt(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == "" || trimmed == "无" || strings.EqualFold(trimmed, "none") || strings.EqualFold(trimmed, "n/a")
}

func strictCodexReviewPassGuard(result *CodexReviewResult) *codexReviewPassGuard {
	notes := strings.TrimSpace(result.ReviewNotes)
	taskType := normalizeReviewTaskType(result.NextPromptTaskType)
	if taskType == "代码理解" {
		return nil
	}
	combined := notes + "\n" + result.NextPrompt + "\n" + result.KeyLocations
	if hasAnyKeyword(combined, "未运行页面或接口", "仅静态取证", "未运行接口或页面", "未运行页面", "未运行接口", "未运行测试") {
		if hasAnyKeyword(combined, "WebSocket", "websocket", "通知", "异步刷新", "自动同步") &&
			!hasAnyKeyword(combined, "连接地址", "代理", "订阅", "重新拉取", "回读", "接口回读", "收到事件") {
			return &codexReviewPassGuard{
				reviewNotes:        "自动同步链路证据不足：当前结论承认未运行页面或接口，但没有把 WebSocket/通知的连接地址、代理配置、事件订阅和页面刷新或回读链路核清楚；高风险异步刷新场景不能判定满意。",
				nextPrompt:         "修复自动同步不可靠的问题：页面收到 WebSocket 或通知事件后，应能重新拉取最新数据并更新展示，用户不用手动刷新也能看到最新状态。",
				nextPromptTaskType: "Bug修复",
			}
		}
		if hasAnyKeyword(combined, "权限", "角色", "鉴权", "隔离") &&
			!hasAnyKeyword(combined, "按钮", "页面", "接口调用", "用户维度", "角色级", "后端接口") {
			return &codexReviewPassGuard{
				reviewNotes:        "权限链路证据不足：当前结论承认未运行页面或接口，但没有同时核清前端入口/按钮/接口调用和后端角色校验是否一致；权限或角色隔离场景不能只凭局部代码改动判定满意。",
				nextPrompt:         "修复角色权限不一致的问题：不同角色进入页面、点击按钮或调用接口时，只能看到并执行自己被允许的操作，前端入口和后端校验要保持一致。",
				nextPromptTaskType: "Bug修复",
			}
		}
		if hasAnyKeyword(combined, "金额", "优惠", "计费", "实付", "价格", "退款", "余额") &&
			!hasAnyKeyword(combined, "边界", "不能超过", "最小", "最大", "负数", "Decimal", "实付金额") {
			return &codexReviewPassGuard{
				reviewNotes:        "金额链路证据不足：当前结论承认未运行页面或接口，但没有核清优惠/计费边界和实付金额是否始终有效；金额类高风险场景不能在边界缺口未说明时判定满意。",
				nextPrompt:         "修复金额边界处理不完整的问题：优惠、计费和实付金额在最小值、最大值、超额抵扣或异常输入下都要给出正确结果，页面展示和最终提交金额不能互相冲突。",
				nextPromptTaskType: "Bug修复",
			}
		}
	}
	return nil
}

func polishReviewNextPrompt(result *CodexReviewResult, req CodexReviewRequest) {
	if result == nil {
		return
	}
	result.NextPrompt = polishReviewNextPromptText(result.NextPrompt, req.CurrentPrompt, result.NextPromptTaskType)
	for i := range result.Issues {
		result.Issues[i].NextPrompt = polishReviewNextPromptText(result.Issues[i].NextPrompt, req.CurrentPrompt, result.Issues[i].IssueType)
	}
}

func polishReviewNextPromptText(value string, currentPrompt string, taskType ...string) string {
	text := strings.TrimSpace(value)
	if text == "" || text == "无" {
		return text
	}
	replacer := strings.NewReplacer(
		"请补齐并核验", "修复",
		"请补齐", "修复",
		"补齐并核验", "修复",
		"补齐", "处理",
		"核验", "确认",
		"核清", "确认",
		"验通", "确认能正常使用",
		"闭环", "完整流程",
		"主链路", "主流程",
		"链路", "流程",
		"验收时需确认", "需要确认",
		"验收时确认", "需要确认",
		"验收时", "需要",
		"验证时需确认", "需要确认",
		"验证时确认", "需要确认",
		"验证时", "需要",
	)
	text = replacer.Replace(text)
	text = strings.ReplaceAll(text, "：需要确认", "：确认")
	text = strings.ReplaceAll(text, "；需要确认", "；确认")
	text = strings.ReplaceAll(text, "，需要确认", "，确认")
	text = strings.ReplaceAll(text, "需要需要", "需要")
	text = strings.TrimSpace(text)

	if isBugFixTaskType(taskType...) {
		text = polishBugFixNextPromptText(text, currentPrompt)
	}
	return strings.TrimSpace(text)
}

func isBugFixTaskType(taskType ...string) bool {
	for _, value := range taskType {
		if strings.TrimSpace(value) == "Bug修复" {
			return true
		}
	}
	return false
}

var (
	nextPromptWhitespaceRe = regexp.MustCompile(`\s+`)
	nextPromptPrefixRe     = regexp.MustCompile(`^修复[^：:，,。；;]{8,42}(?:时|中|后|的问题|异常|缺口)?[：:，,。；;]`)
)

func polishBugFixNextPromptText(text, currentPrompt string) string {
	text = normalizePromptWhitespace(text)
	text = removeRepeatedCurrentPromptPrefix(text, currentPrompt)
	text = shortenBugFixOpening(text)
	text = compactBugFixSentences(text, 3)
	if text != "" && !strings.HasPrefix(text, "修复") {
		text = "修复" + text
	}
	return text
}

func normalizePromptWhitespace(value string) string {
	text := strings.TrimSpace(value)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = nextPromptWhitespaceRe.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

func removeRepeatedCurrentPromptPrefix(text, currentPrompt string) string {
	currentPrefix := normalizedComparablePrefix(currentPrompt, 40)
	if currentPrefix == "" {
		return text
	}
	for {
		textPrefix := normalizedComparablePrefix(text, 40)
		if textPrefix == "" || commonPrefixRuneLength(textPrefix, currentPrefix) < 10 {
			return text
		}
		cut := nextPromptPrefixRe.FindStringIndex(text)
		if cut == nil {
			return text
		}
		text = "修复" + strings.TrimSpace(text[cut[1]:])
	}
}

func normalizedComparablePrefix(value string, limit int) string {
	text := normalizePromptWhitespace(value)
	text = strings.TrimPrefix(text, "请")
	text = strings.TrimPrefix(text, "继续")
	runes := []rune(text)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	normalized := strings.NewReplacer(
		" ", "",
		"：", "",
		":", "",
		"，", "",
		",", "",
		"。", "",
		"；", "",
		";", "",
	).Replace(string(runes))
	return strings.TrimSpace(normalized)
}

func shortenBugFixOpening(text string) string {
	if !strings.HasPrefix(text, "修复") {
		return text
	}
	if loc := nextPromptPrefixRe.FindStringIndex(text); loc != nil {
		opening := text[:loc[1]]
		if runeLength(opening) > 24 {
			remainder := strings.TrimSpace(text[loc[1]:])
			if remainder != "" {
				return "修复" + remainder
			}
		}
	}
	return text
}

func compactBugFixSentences(text string, limit int) string {
	sentences := splitChineseSentences(text)
	if len(sentences) <= limit {
		return text
	}
	return strings.Join(sentences[:limit], "")
}

func splitChineseSentences(text string) []string {
	var sentences []string
	start := 0
	runes := []rune(text)
	for i, r := range runes {
		if strings.ContainsRune("。！？；", r) {
			sentence := strings.TrimSpace(string(runes[start : i+1]))
			if sentence != "" {
				sentences = append(sentences, sentence)
			}
			start = i + 1
		}
	}
	if start < len(runes) {
		sentence := strings.TrimSpace(string(runes[start:]))
		if sentence != "" {
			sentences = append(sentences, sentence)
		}
	}
	return sentences
}

func runeLength(value string) int {
	return len([]rune(value))
}

func commonPrefixRuneLength(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	limit := len(ar)
	if len(br) < limit {
		limit = len(br)
	}
	for i := 0; i < limit; i++ {
		if ar[i] != br[i] {
			return i
		}
	}
	return limit
}

func normalizeReviewTaskType(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "未归类"
	}
	return trimmed
}

func hasAnyKeyword(value string, keywords ...string) bool {
	for _, keyword := range keywords {
		if strings.Contains(value, keyword) {
			return true
		}
	}
	return false
}

func isEmptyPassingReviewNote(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == "" || trimmed == "无" || strings.EqualFold(trimmed, "none") || strings.EqualFold(trimmed, "n/a")
}

func countValidKeyLocations(localPath, raw string) int {
	entries := splitKeyLocations(raw)
	valid := 0
	for _, entry := range entries {
		if isValidKeyLocation(localPath, entry) {
			valid++
		}
	}
	return valid
}

func splitKeyLocations(raw string) []string {
	replacer := strings.NewReplacer("；", ";", "，", ";", ",", ";")
	normalized := replacer.Replace(raw)
	parts := strings.Split(normalized, ";")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func isValidKeyLocation(localPath, entry string) bool {
	idx := strings.LastIndex(entry, ":")
	if idx <= 0 || idx >= len(entry)-1 {
		return false
	}

	relativePath := strings.TrimSpace(entry[:idx])
	lineText := strings.TrimSpace(entry[idx+1:])
	lineNumber, err := strconv.Atoi(lineText)
	if err != nil || lineNumber <= 0 {
		return false
	}

	filePath := filepath.Join(localPath, relativePath)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}

	lineCount := bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lineCount++
	}
	return lineCount >= lineNumber
}

func buildPrompt(userPrompt, thinkingDepth, mode string) string {
	var sb strings.Builder

	// Thinking depth prefix
	switch strings.ToLower(thinkingDepth) {
	case "think":
		sb.WriteString("think\n\n")
	case "think harder":
		sb.WriteString("think harder\n\n")
	case "ultrathink":
		sb.WriteString("ultrathink\n\n")
	}

	// Mode annotation
	if mode == "plan" {
		sb.WriteString("【规划模式】仅输出实施计划，不执行任何文件修改或命令，不使用 Write/Edit/Bash 工具。\n\n")
	}

	sb.WriteString(userPrompt)
	return sb.String()
}
