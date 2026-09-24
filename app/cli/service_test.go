package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestHelperProcess is not a real test. It is invoked as a subprocess by other
// tests to provide a cross-platform mock for external CLI binaries.
// Run via createMockCodexExecutable; do not call directly.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_SUBPROCESS") != "1" {
		return
	}
	// Find the "--" separator; everything after it are the forwarded CLI args.
	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}
	switch os.Getenv("GO_TEST_SUBPROCESS_MODE") {
	case "codex_error":
		fmt.Fprintln(os.Stderr, "ERROR: invalid schema")
		fmt.Fprintln(os.Stderr, "ERROR: additionalProperties must be false")
		os.Exit(1)
	case "python_error":
		fmt.Fprintln(os.Stderr, "traceback: collect context failed")
		fmt.Fprintln(os.Stderr, "details: missing dependency")
		os.Exit(1)
	case "python_context_ok":
		fmt.Fprintln(os.Stdout, `{"base_dir":"/tmp","projects":[{"resolved_path":"/tmp/demo","exists":true,"git":{"in_git":true,"changed_files":["main.go"]},"recent_files":[]}]}`)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown GO_TEST_SUBPROCESS_MODE: %s\n", os.Getenv("GO_TEST_SUBPROCESS_MODE"))
		os.Exit(1)
	}
}

// createMockCodexExecutable returns a path to a platform-appropriate executable
// that behaves according to mode when invoked as "codex". On Unix it creates a
// shell script; on Windows a .bat wrapper — both delegate to the test binary
// via the TestHelperProcess helper-process pattern.
func createMockCodexExecutable(t *testing.T, mode string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "codex.bat")
		content := fmt.Sprintf(
			"@echo off\r\nset GO_TEST_SUBPROCESS=1\r\nset GO_TEST_SUBPROCESS_MODE=%s\r\n\"%s\" -test.run=TestHelperProcess -- %%*\r\nexit /b %%errorlevel%%\r\n",
			mode, exe,
		)
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatalf("os.WriteFile(%s) error = %v", path, err)
		}
		return path
	}
	path := filepath.Join(dir, "codex")
	content := fmt.Sprintf(
		"#!/bin/sh\nGO_TEST_SUBPROCESS=1 GO_TEST_SUBPROCESS_MODE=%s exec %q -test.run=TestHelperProcess -- \"$@\"\n",
		mode, exe,
	)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}
	return path
}

func createMockPythonExecutable(t *testing.T, mode string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "python3.bat")
		content := fmt.Sprintf(
			"@echo off\r\nset GO_TEST_SUBPROCESS=1\r\nset GO_TEST_SUBPROCESS_MODE=%s\r\n\"%s\" -test.run=TestHelperProcess -- %%*\r\nexit /b %%errorlevel%%\r\n",
			mode, exe,
		)
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatalf("os.WriteFile(%s) error = %v", path, err)
		}
		return path
	}
	path := filepath.Join(dir, "python3")
	content := fmt.Sprintf(
		"#!/bin/sh\nGO_TEST_SUBPROCESS=1 GO_TEST_SUBPROCESS_MODE=%s exec %q -test.run=TestHelperProcess -- \"$@\"\n",
		mode, exe,
	)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}
	return path
}

func runGitForCliTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s error = %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

func TestValidatePermissionMode(t *testing.T) {
	if err := validatePermissionMode(""); err != nil {
		t.Fatalf("validatePermissionMode(empty) error = %v", err)
	}
	if err := validatePermissionMode("default"); err != nil {
		t.Fatalf("validatePermissionMode(default) error = %v", err)
	}
	if err := validatePermissionMode("acceptEdits"); err != nil {
		t.Fatalf("validatePermissionMode(acceptEdits) error = %v", err)
	}
	if err := validatePermissionMode("yolo"); err != nil {
		t.Fatalf("validatePermissionMode(yolo) error = %v", err)
	}
	if err := validatePermissionMode("bypassPermissions"); err != nil {
		t.Fatalf("validatePermissionMode(bypassPermissions) error = %v", err)
	}
}

func TestBuildClaudeArgsIncludesPermissionModeAndAdditionalDirs(t *testing.T) {
	args, err := buildClaudeArgs(StartClaudeRequest{
		Prompt:         "生成提示词",
		Model:          "claude-sonnet-4-6",
		PermissionMode: "acceptEdits",
		AdditionalDirs: []string{" /tmp/manuals ", "/tmp/manuals", "", "/tmp/skills"},
	})
	if err != nil {
		t.Fatalf("buildClaudeArgs() error = %v", err)
	}

	expected := []string{
		"-p", "生成提示词",
		"--model", "claude-sonnet-4-6",
		"--permission-mode", "acceptEdits",
		"--add-dir", "/tmp/manuals", "/tmp/skills",
	}
	if !reflect.DeepEqual(args, expected) {
		t.Fatalf("buildClaudeArgs() = %#v, want %#v", args, expected)
	}
}

func TestBuildClaudeArgsAddsPlanAllowedTools(t *testing.T) {
	args, err := buildClaudeArgs(StartClaudeRequest{
		Prompt: "规划一下",
		Mode:   "plan",
	})
	if err != nil {
		t.Fatalf("buildClaudeArgs() error = %v", err)
	}

	expectedSuffix := []string{"--allowedTools", "Read,Glob,Grep,WebFetch,WebSearch"}
	if len(args) < len(expectedSuffix) {
		t.Fatalf("buildClaudeArgs() = %#v, want suffix %#v", args, expectedSuffix)
	}
	if !reflect.DeepEqual(args[len(args)-len(expectedSuffix):], expectedSuffix) {
		t.Fatalf("buildClaudeArgs() suffix = %#v, want %#v", args[len(args)-len(expectedSuffix):], expectedSuffix)
	}
}

func TestBuildClaudeArgsDefaultsToDangerousSkipPermissions(t *testing.T) {
	args, err := buildClaudeArgs(StartClaudeRequest{
		Prompt: "生成提示词",
		Model:  "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("buildClaudeArgs() error = %v", err)
	}

	expected := []string{
		"-p", "生成提示词",
		"--model", "claude-sonnet-4-6",
		"--dangerously-skip-permissions",
	}
	if !reflect.DeepEqual(args, expected) {
		t.Fatalf("buildClaudeArgs() = %#v, want %#v", args, expected)
	}
}

func TestBuildClaudeArgsMapsYoloToBypassPermissions(t *testing.T) {
	args, err := buildClaudeArgs(StartClaudeRequest{
		Prompt:         "生成提示词",
		PermissionMode: "yolo",
	})
	if err != nil {
		t.Fatalf("buildClaudeArgs() error = %v", err)
	}

	expected := []string{
		"-p", "生成提示词",
		"--permission-mode", "bypassPermissions",
		"--dangerously-skip-permissions",
	}
	if !reflect.DeepEqual(args, expected) {
		t.Fatalf("buildClaudeArgs() = %#v, want %#v", args, expected)
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	base := []string{
		"PATH=/usr/bin:/bin",
		"ANTHROPIC_MODEL=claude-opus-4-6",
		"HOME=/Users/test",
	}

	// Override ANTHROPIC_MODEL, add a new key
	result := applyEnvOverrides(base, map[string]string{
		"ANTHROPIC_MODEL": "claude-sonnet-4-6",
		"EXTRA_VAR":       "hello",
	})

	env := make(map[string]string, len(result))
	for _, entry := range result {
		idx := len(entry)
		for i, c := range entry {
			if c == '=' {
				idx = i
				break
			}
		}
		env[entry[:idx]] = entry[idx+1:]
	}

	if env["ANTHROPIC_MODEL"] != "claude-sonnet-4-6" {
		t.Errorf("ANTHROPIC_MODEL = %q, want claude-sonnet-4-6", env["ANTHROPIC_MODEL"])
	}
	if env["PATH"] != "/usr/bin:/bin" {
		t.Errorf("PATH = %q, want /usr/bin:/bin", env["PATH"])
	}
	if env["HOME"] != "/Users/test" {
		t.Errorf("HOME = %q, want /Users/test", env["HOME"])
	}
	if env["EXTRA_VAR"] != "hello" {
		t.Errorf("EXTRA_VAR = %q, want hello", env["EXTRA_VAR"])
	}
	// Ensure ANTHROPIC_MODEL appears only once
	count := 0
	for _, entry := range result {
		if len(entry) >= 14 && entry[:14] == "ANTHROPIC_MODE" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("ANTHROPIC_MODEL appears %d times in env, want 1", count)
	}
}

func TestRedactClaudePromptArg(t *testing.T) {
	args := []string{"-p", "secret prompt", "--model", "deepseek-v4-pro"}
	got := redactClaudePromptArg(args)
	want := []string{"-p", "<prompt redacted>", "--model", "deepseek-v4-pro"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("redactClaudePromptArg() = %#v, want %#v", got, want)
	}
	if args[1] != "secret prompt" {
		t.Fatalf("redactClaudePromptArg mutated input args: %#v", args)
	}
}

func TestPgCodeReviewSchemaDisallowsAdditionalProperties(t *testing.T) {
	var schema map[string]interface{}
	if err := json.Unmarshal(pgCodeReviewSchema, &schema); err != nil {
		t.Fatalf("json.Unmarshal(pgCodeReviewSchema) error = %v", err)
	}

	value, ok := schema["additionalProperties"]
	if !ok {
		t.Fatalf("schema missing additionalProperties: %v", schema)
	}
	if allowed, ok := value.(bool); !ok || allowed {
		t.Fatalf("schema additionalProperties = %#v, want false", value)
	}
}

func TestPgCodeReviewSchemaDescriptionsEnforcePromptScopedReview(t *testing.T) {
	var schema map[string]interface{}
	if err := json.Unmarshal(pgCodeReviewSchema, &schema); err != nil {
		t.Fatalf("json.Unmarshal(pgCodeReviewSchema) error = %v", err)
	}

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("schema properties = %#v, want object", schema["properties"])
	}

	reviewNotes, ok := properties["reviewNotes"].(map[string]interface{})
	if !ok {
		t.Fatalf("reviewNotes schema = %#v, want object", properties["reviewNotes"])
	}
	reviewNotesDesc, _ := reviewNotes["description"].(string)
	if !strings.Contains(reviewNotesDesc, "必须回指 original_prompt/current_prompt") {
		t.Fatalf("reviewNotes description = %q, want prompt back-reference rule", reviewNotesDesc)
	}
	if !strings.Contains(reviewNotesDesc, "不能用未写明的扩展点作为主缺口") {
		t.Fatalf("reviewNotes description = %q, want no-expansion rule", reviewNotesDesc)
	}
	if !strings.Contains(reviewNotesDesc, "产物满意但处理过程存在不满意") {
		t.Fatalf("reviewNotes description = %q, want process dissatisfaction pass rule", reviewNotesDesc)
	}
	if !strings.Contains(reviewNotesDesc, "reviewNotes 只写“异常输出”") {
		t.Fatalf("reviewNotes description = %q, want abnormal output rule", reviewNotesDesc)
	}

	nextPrompt, ok := properties["nextPrompt"].(map[string]interface{})
	if !ok {
		t.Fatalf("nextPrompt schema = %#v, want object", properties["nextPrompt"])
	}
	nextPromptDesc, _ := nextPrompt["description"].(string)
	if !strings.Contains(nextPromptDesc, "只允许围绕 reviewNotes 已回指的主缺口") {
		t.Fatalf("nextPrompt description = %q, want focused-fix rule", nextPromptDesc)
	}
	if !strings.Contains(nextPromptDesc, "触发场景、当前异常、修复后的业务结果和必要边界") {
		t.Fatalf("nextPrompt description = %q, want bug prompt shape rule", nextPromptDesc)
	}
	if strings.Contains(nextPromptDesc, "必要验收点") {
		t.Fatalf("nextPrompt description = %q, should not encourage acceptance-check wording", nextPromptDesc)
	}
	if !strings.Contains(nextPromptDesc, "一定不能和 current_prompt/上一轮会话提示词雷同") {
		t.Fatalf("nextPrompt description = %q, want no-similar-previous-prompt rule", nextPromptDesc)
	}
	if !strings.Contains(nextPromptDesc, "自然语言清晰、顺畅、连贯") {
		t.Fatalf("nextPrompt description = %q, want natural language repair prompt rule", nextPromptDesc)
	}
	if !strings.Contains(nextPromptDesc, "不要描述问题原因或代码原因") {
		t.Fatalf("nextPrompt description = %q, want no-cause repair prompt rule", nextPromptDesc)
	}
	if !strings.Contains(nextPromptDesc, "修复提示词尽量不包含代码") {
		t.Fatalf("nextPrompt description = %q, want no-code repair prompt rule", nextPromptDesc)
	}
	if !strings.Contains(nextPromptDesc, "文件、文件名、文件路径") || !strings.Contains(nextPromptDesc, "方法名") {
		t.Fatalf("nextPrompt description = %q, want no file/method repair prompt rule", nextPromptDesc)
	}
	if !strings.Contains(nextPromptDesc, "必须以“修复”两个字开头") {
		t.Fatalf("nextPrompt description = %q, want bug prefix rule", nextPromptDesc)
	}
	if !strings.Contains(nextPromptDesc, "不要写“验收时确认”“补齐链路”“核验闭环”") {
		t.Fatalf("nextPrompt description = %q, want no review-tone rule", nextPromptDesc)
	}
	if !strings.Contains(nextPromptDesc, "不要沿用 current_prompt 的长开头") || !strings.Contains(nextPromptDesc, "通常控制在 2 到 3 句") {
		t.Fatalf("nextPrompt description = %q, want concise non-repeated bug prompt rule", nextPromptDesc)
	}
	if !strings.Contains(nextPromptDesc, "如果 reviewNotes 是“异常输出”，nextPrompt 必须填“无”") {
		t.Fatalf("nextPrompt description = %q, want no next prompt for abnormal output rule", nextPromptDesc)
	}

	issues, ok := properties["issues"].(map[string]interface{})
	if !ok {
		t.Fatalf("issues schema = %#v, want object", properties["issues"])
	}
	issuesDesc, _ := issues["description"].(string)
	if !strings.Contains(issuesDesc, "只能对应 prompt 中明确写出的要求") {
		t.Fatalf("issues description = %q, want prompt-only rule", issuesDesc)
	}

	items, ok := issues["items"].(map[string]interface{})
	if !ok {
		t.Fatalf("issues.items = %#v, want object", issues["items"])
	}
	itemProps, ok := items["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("issues.items.properties = %#v, want object", items["properties"])
	}

	issueReviewNotes, ok := itemProps["reviewNotes"].(map[string]interface{})
	if !ok {
		t.Fatalf("issues.items.reviewNotes = %#v, want object", itemProps["reviewNotes"])
	}
	issueReviewNotesDesc, _ := issueReviewNotes["description"].(string)
	if !strings.Contains(issueReviewNotesDesc, "必须回指 original_prompt/current_prompt") {
		t.Fatalf("issues.items.reviewNotes description = %q, want prompt back-reference rule", issueReviewNotesDesc)
	}
	if !strings.Contains(issueReviewNotesDesc, "只写“异常输出”") {
		t.Fatalf("issues.items.reviewNotes description = %q, want abnormal output rule", issueReviewNotesDesc)
	}

	issueNextPrompt, ok := itemProps["nextPrompt"].(map[string]interface{})
	if !ok {
		t.Fatalf("issues.items.nextPrompt = %#v, want object", itemProps["nextPrompt"])
	}
	issueNextPromptDesc, _ := issueNextPrompt["description"].(string)
	if !strings.Contains(issueNextPromptDesc, "只修复当前子问题对应的主缺口") {
		t.Fatalf("issues.items.nextPrompt description = %q, want focused-fix rule", issueNextPromptDesc)
	}
	if !strings.Contains(issueNextPromptDesc, "不要直接写代码实现步骤") {
		t.Fatalf("issues.items.nextPrompt description = %q, want no implementation-step rule", issueNextPromptDesc)
	}
	if !strings.Contains(issueNextPromptDesc, "不能和 current_prompt/上一轮会话提示词雷同") {
		t.Fatalf("issues.items.nextPrompt description = %q, want no-similar-previous-prompt rule", issueNextPromptDesc)
	}
	if !strings.Contains(issueNextPromptDesc, "修复提示词尽量不包含代码") {
		t.Fatalf("issues.items.nextPrompt description = %q, want no-code repair prompt rule", issueNextPromptDesc)
	}
	if !strings.Contains(issueNextPromptDesc, "文件、文件名、文件路径") || !strings.Contains(issueNextPromptDesc, "方法名") {
		t.Fatalf("issues.items.nextPrompt description = %q, want no file/method repair prompt rule", issueNextPromptDesc)
	}
	if !strings.Contains(issueNextPromptDesc, "必须以“修复”两个字开头") {
		t.Fatalf("issues.items.nextPrompt description = %q, want bug prefix rule", issueNextPromptDesc)
	}
	if !strings.Contains(issueNextPromptDesc, "不要写成“验收时确认”“补齐链路”“核验闭环”") {
		t.Fatalf("issues.items.nextPrompt description = %q, want no review-tone rule", issueNextPromptDesc)
	}
	if !strings.Contains(issueNextPromptDesc, "不要沿用 current_prompt 的长开头") || !strings.Contains(issueNextPromptDesc, "通常控制在 2 到 3 句") {
		t.Fatalf("issues.items.nextPrompt description = %q, want concise non-repeated bug prompt rule", issueNextPromptDesc)
	}
	if !strings.Contains(issueNextPromptDesc, "如果 reviewNotes 是“异常输出”，nextPrompt 必须填“无”") {
		t.Fatalf("issues.items.nextPrompt description = %q, want no next prompt for abnormal output rule", issueNextPromptDesc)
	}
}

func TestRunCodexReviewIncludesRecentCliOutputInError(t *testing.T) {
	repoDir := t.TempDir()
	mockPath := createMockCodexExecutable(t, "codex_error")

	svc := NewWithResolver(func(name string) (string, error) {
		if name != "codex" {
			t.Fatalf("unexpected CLI lookup: %s", name)
		}
		return mockPath, nil
	})
	svc.reviewContextPath = filepath.Join(t.TempDir(), "missing_collect_project_context.py")

	_, err := svc.RunCodexReview(context.Background(), CodexReviewRequest{
		LocalPath:      repoDir,
		OriginalPrompt: "stub prompt",
	}, nil)
	if err == nil {
		t.Fatalf("RunCodexReview() error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "invalid schema") {
		t.Fatalf("RunCodexReview() error = %q, want invalid schema detail", err.Error())
	}
	if !strings.Contains(err.Error(), "additionalProperties must be false") {
		t.Fatalf("RunCodexReview() error = %q, want stderr tail", err.Error())
	}
}

func TestReviewContextScriptPathFallsBackToClaudeSkillsDir(t *testing.T) {
	home := t.TempDir()
	originalHome := os.Getenv("HOME")
	t.Setenv("HOME", home)
	if originalHome != "" {
		defer os.Setenv("HOME", originalHome)
	}

	scriptPath := filepath.Join(home, ".claude", "skills", "pg-code", "scripts", "collect_project_context.py")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("print('ok')\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	svc := New()
	if got := svc.reviewContextScriptPath(); got != scriptPath {
		t.Fatalf("reviewContextScriptPath() = %q, want %q", got, scriptPath)
	}
}

func TestCollectPgCodeReviewContextFallsBackWhenPythonFails(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "deploy.sh"), []byte("#!/bin/sh\necho deploy\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(deploy.sh) error = %v", err)
	}
	scriptPath := filepath.Join(t.TempDir(), "collect_project_context.py")
	if err := os.WriteFile(scriptPath, []byte("print('ignored')\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(scriptPath) error = %v", err)
	}
	pythonPath := createMockPythonExecutable(t, "python_error")

	svc := NewWithResolver(func(name string) (string, error) {
		if name != "python3" {
			t.Fatalf("unexpected CLI lookup: %s", name)
		}
		return pythonPath, nil
	})
	svc.reviewContextPath = scriptPath

	project, err := svc.collectPgCodeReviewContext(context.Background(), repoDir)
	if err != nil {
		t.Fatalf("collectPgCodeReviewContext() error = %v", err)
	}
	if project == nil {
		t.Fatalf("collectPgCodeReviewContext() = nil, want fallback project")
	}
	if !project.Exists {
		t.Fatalf("project.Exists = false, want true")
	}
	if len(project.RecentFiles) == 0 || project.RecentFiles[0].RelativePath != "deploy.sh" {
		t.Fatalf("project.RecentFiles = %#v, want deploy.sh from native fallback", project.RecentFiles)
	}
}

func TestCollectPgCodeReviewContextUsesResolvedPythonPath(t *testing.T) {
	repoDir := t.TempDir()
	scriptPath := filepath.Join(t.TempDir(), "collect_project_context.py")
	if err := os.WriteFile(scriptPath, []byte("print('ignored')\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(scriptPath) error = %v", err)
	}
	pythonPath := createMockPythonExecutable(t, "python_context_ok")

	svc := NewWithResolver(func(name string) (string, error) {
		if name != "python3" {
			t.Fatalf("unexpected CLI lookup: %s", name)
		}
		return pythonPath, nil
	})
	svc.reviewContextPath = scriptPath

	project, err := svc.collectPgCodeReviewContext(context.Background(), repoDir)
	if err != nil {
		t.Fatalf("collectPgCodeReviewContext() error = %v", err)
	}
	if project == nil {
		t.Fatalf("collectPgCodeReviewContext() = nil, want project")
	}
	if !project.Git.InGit {
		t.Fatalf("project.Git.InGit = false, want true")
	}
	if len(project.Git.ChangedFiles) != 1 || project.Git.ChangedFiles[0] != "main.go" {
		t.Fatalf("project.Git.ChangedFiles = %#v, want [main.go]", project.Git.ChangedFiles)
	}
}

func TestAttachReviewCommitContextUsesCommittedDiffWhenWorkingTreeClean(t *testing.T) {
	repoDir := t.TempDir()
	runGitForCliTest(t, repoDir, "init", "-b", "main")
	runGitForCliTest(t, repoDir, "config", "user.name", "PINRU Test")
	runGitForCliTest(t, repoDir, "config", "user.email", "pinru@example.com")

	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(main.go) error = %v", err)
	}
	runGitForCliTest(t, repoDir, "add", "-A")
	runGitForCliTest(t, repoDir, "commit", "-m", "初始化项目")

	if err := os.WriteFile(filepath.Join(repoDir, "feature.go"), []byte("package main\nfunc feature() {}\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(feature.go) error = %v", err)
	}
	runGitForCliTest(t, repoDir, "add", "-A")
	runGitForCliTest(t, repoDir, "commit", "-m", "feat: add feature")
	commitSHA := strings.TrimSpace(runGitForCliTest(t, repoDir, "rev-parse", "HEAD"))

	project, err := collectNativePgCodeReviewContext(context.Background(), repoDir)
	if err != nil {
		t.Fatalf("collectNativePgCodeReviewContext() error = %v", err)
	}
	if got := project.Git.ChangedFiles; len(got) != 0 {
		t.Fatalf("precondition changed files = %#v, want clean working tree", got)
	}

	attachReviewCommitContext(context.Background(), project, repoDir, CodexReviewRequest{
		CommitSHA: commitSHA,
	})

	if project.ReviewCommit == nil || !project.ReviewCommit.Found {
		t.Fatalf("ReviewCommit = %#v, want found commit", project.ReviewCommit)
	}
	if !reflect.DeepEqual(project.Git.ChangedFiles, []string{"feature.go"}) {
		t.Fatalf("Git.ChangedFiles = %#v, want committed feature.go", project.Git.ChangedFiles)
	}
	if !strings.Contains(project.ReviewCommit.Stat, "feature.go") {
		t.Fatalf("ReviewCommit.Stat = %q, want feature.go", project.ReviewCommit.Stat)
	}
}

func TestBuildCodexReviewPromptIncludesEvidenceGuardrails(t *testing.T) {
	prompt := buildCodexReviewPrompt(CodexReviewRequest{
		LocalPath:         "/tmp/demo",
		CommitSHA:         "abc123",
		OriginalPrompt:    "实现每日任务与奖励记录",
		CurrentPrompt:     "修复奖励记录漏记问题",
		ParentReviewNotes: "奖励记录路径缺少空值保护",
		IssueType:         "Bug修复",
		IssueTitle:        "奖励记录异常处理",
		ModelName:         "cotv21-pro",
	}, &pgCodeProjectContext{
		ResolvedPath: "/tmp/demo",
		Git: pgCodeGitContext{
			InGit:        true,
			ChangedFiles: []string{"app/main.go"},
		},
	})

	if !strings.Contains(prompt, "/pg-code") {
		t.Fatalf("prompt = %q, want /pg-code prefix", prompt)
	}
	if !strings.Contains(prompt, "只能基于任务提示词、本轮代码变更、最近更新文件以及你实际读取过的文件下结论") {
		t.Fatalf("prompt missing evidence guardrail: %q", prompt)
	}
	if !strings.Contains(prompt, "必须优先以 review_commit.changed_files") {
		t.Fatalf("prompt missing review commit priority rule: %q", prompt)
	}
	if !strings.Contains(prompt, "不要再用当前工作区 git status/git diff 为空来判断") {
		t.Fatalf("prompt missing clean working tree warning: %q", prompt)
	}
	if !strings.Contains(prompt, "original_prompt/current_prompt 为唯一来源") {
		t.Fatalf("prompt missing db-only prompt guidance: %q", prompt)
	}
	if !strings.Contains(prompt, "只把其中明确写出的要求作为验收标准") {
		t.Fatalf("prompt missing strict prompt-only acceptance rule: %q", prompt)
	}
	if !strings.Contains(prompt, "不要把未写明的扩展点") {
		t.Fatalf("prompt missing no-expansion rule: %q", prompt)
	}
	if !strings.Contains(prompt, "reviewNotes 必须回指 original_prompt/current_prompt") {
		t.Fatalf("prompt missing prompt back-reference rule: %q", prompt)
	}
	if !strings.Contains(prompt, "回指不到的内容不能作为主缺口") {
		t.Fatalf("prompt missing main-gap restriction: %q", prompt)
	}
	if !strings.Contains(prompt, "nextPrompt 只能围绕主缺口补充最小修复指令") {
		t.Fatalf("prompt missing focused nextPrompt rule: %q", prompt)
	}
	if !strings.Contains(prompt, "Bug修复类 nextPrompt 必须像用户可执行的 bug 修复提示词") {
		t.Fatalf("prompt missing bug nextPrompt shape rule: %q", prompt)
	}
	if !strings.Contains(prompt, "不要直接写“把 A 放到 B 前面”") {
		t.Fatalf("prompt missing no implementation-step nextPrompt rule: %q", prompt)
	}
	if !strings.Contains(prompt, "代码理解类可以按文档交付物判断") {
		t.Fatalf("prompt missing code-understanding relaxed rule: %q", prompt)
	}
	if !strings.Contains(prompt, "Feature迭代、0-1代码生成、Bug修复必须收紧") {
		t.Fatalf("prompt missing stricter feature/bug rule: %q", prompt)
	}
	if !strings.Contains(prompt, "未运行页面或接口、仅静态取证") {
		t.Fatalf("prompt missing static-evidence caveat: %q", prompt)
	}
	if !strings.Contains(prompt, "产物已经满足 current_prompt 的主要交付要求，但处理过程存在不满意") {
		t.Fatalf("prompt missing process dissatisfaction pass rule: %q", prompt)
	}
	if !strings.Contains(prompt, "nextPrompt 一定不能和 current_prompt/上一轮会话提示词雷同") {
		t.Fatalf("prompt missing no-similar-previous-prompt rule: %q", prompt)
	}
	if !strings.Contains(prompt, "reviewNotes 只输出“异常输出”四个字") {
		t.Fatalf("prompt missing abnormal output rule: %q", prompt)
	}
	if !strings.Contains(prompt, "不要给修复提示词") {
		t.Fatalf("prompt missing no repair prompt for abnormal output rule: %q", prompt)
	}
	if !strings.Contains(prompt, "不要描述问题原因、代码原因") {
		t.Fatalf("prompt missing no-cause repair prompt rule: %q", prompt)
	}
	if !strings.Contains(prompt, "修复提示词尽量不包含代码") {
		t.Fatalf("prompt missing no-code repair prompt rule: %q", prompt)
	}
	if !strings.Contains(prompt, "文件、文件名、文件路径") || !strings.Contains(prompt, "方法名") {
		t.Fatalf("prompt missing no file/method repair prompt rule: %q", prompt)
	}
	if !strings.Contains(prompt, "对应 nextPrompt 前面一定要加“修复”两个字") {
		t.Fatalf("prompt missing bug prefix repair prompt rule: %q", prompt)
	}
	if strings.Contains(prompt, "prompt_sources") || strings.Contains(prompt, "prompt_candidates") {
		t.Fatalf("prompt should not reference local prompt sources: %q", prompt)
	}
	if !strings.Contains(prompt, "实现每日任务与奖励记录") {
		t.Fatalf("prompt missing original prompt content: %q", prompt)
	}
	if !strings.Contains(prompt, "\"parent_review_notes\": \"奖励记录路径缺少空值保护\"") {
		t.Fatalf("prompt missing parent review notes: %q", prompt)
	}
	if !strings.Contains(prompt, "\"issue_title\": \"奖励记录异常处理\"") {
		t.Fatalf("prompt missing issue title: %q", prompt)
	}
}

func TestRunCodexReviewRejectsWhenPromptsMissing(t *testing.T) {
	svc := New()
	_, err := svc.RunCodexReview(context.Background(), CodexReviewRequest{
		LocalPath:      t.TempDir(),
		OriginalPrompt: "   ",
		CurrentPrompt:  "",
	}, nil)
	if err == nil {
		t.Fatalf("RunCodexReview() error = nil, want rejection when prompts missing")
	}
	if !strings.Contains(err.Error(), "数据库中未保存该轮复审的提示词") {
		t.Fatalf("RunCodexReview() error = %q, want missing-prompt rejection", err.Error())
	}
}

func TestBuildDissatisfactionSummaryPromptAllowsProcessOnlyWhenProductSatisfied(t *testing.T) {
	prompt := buildDissatisfactionSummaryPrompt(DissatisfactionSummaryRequest{
		LocalPath:        "/tmp/demo",
		ModelName:        "cotv21-pro",
		OriginalPrompt:   "新增订单通知",
		CurrentPrompt:    "修复订单通知刷新问题",
		ReviewNotes:      "过程不满意：只看了静态通知列表，没有回到实时刷新场景验证。产物满足本轮要求。",
		ProductSatisfied: true,
	})

	if !strings.Contains(prompt, `"product_satisfied": "true"`) {
		t.Fatalf("prompt missing product_satisfied input: %q", prompt)
	}
	if !strings.Contains(prompt, "产物段固定写 `产物不满意：无`") {
		t.Fatalf("prompt missing product-satisfied process-only rule: %q", prompt)
	}
	if !strings.Contains(prompt, "可以只整理过程不满意，产物段写“无”") {
		t.Fatalf("prompt missing pass-with-process-dissatisfaction rule: %q", prompt)
	}
	if !strings.Contains(prompt, "发生在哪个环节（When）、具体做错或漏掉什么（What）、会造成什么实际影响（Impact）") {
		t.Fatalf("prompt missing dissatisfaction problem-chain rule: %q", prompt)
	}
	if !strings.Contains(prompt, "指令遵循、任务规划、工具使用、幻觉、验证缺失") {
		t.Fatalf("prompt missing dissatisfaction problem-finding checklist: %q", prompt)
	}
	if !strings.Contains(prompt, "不要把“证据不足”当成结论本身") {
		t.Fatalf("prompt missing concrete evidence-insufficiency rule: %q", prompt)
	}
	if !strings.Contains(prompt, "summary 只输出 `异常输出` 四个字") {
		t.Fatalf("prompt missing abnormal dissatisfaction output rule: %q", prompt)
	}
	if !strings.Contains(prompt, "表达要连贯、顺畅、清晰") {
		t.Fatalf("prompt missing natural dissatisfaction writing rule: %q", prompt)
	}
}

func TestNormalizeDissatisfactionSummaryAllowsAbnormalOutput(t *testing.T) {
	got := normalizeDissatisfactionSummary(" \n异常输出\n ")
	if got != "异常输出" {
		t.Fatalf("normalizeDissatisfactionSummary() = %q, want 异常输出", got)
	}
}

func TestCompactPromptForWindowsCommandLine(t *testing.T) {
	raw := "/pg-code\n\n  第一行  \r\n\r\n{\n  \"prompt_candidates\": [\n    \"a.md\"\n  ]\n}\n"
	got := compactPromptForWindowsCommandLine(raw)

	if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
		t.Fatalf("compactPromptForWindowsCommandLine() = %q, want single line", got)
	}
	if !strings.Contains(got, "/pg-code 第一行 {") {
		t.Fatalf("compactPromptForWindowsCommandLine() = %q, want joined content", got)
	}
	if !strings.Contains(got, "\"prompt_candidates\": [") {
		t.Fatalf("compactPromptForWindowsCommandLine() = %q, want JSON content retained", got)
	}
}

func TestApplyCodexReviewEvidenceGuardsDowngradesInvalidKeyLocations(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(main.go) error = %v", err)
	}

	result := CodexReviewResult{
		IsCompleted:  true,
		IsSatisfied:  true,
		ReviewNotes:  "已核验 main.go 的核心改动，主流程要求已覆盖。",
		NextPrompt:   "无",
		KeyLocations: "missing.go:8",
	}

	applyCodexReviewEvidenceGuards(repoDir, &pgCodeProjectContext{
		Exists: true,
		Git: pgCodeGitContext{
			InGit:        true,
			ChangedFiles: []string{"main.go"},
		},
	}, &result)

	// Invalid key locations is now a soft guard: result booleans are preserved.
	if !result.IsCompleted || !result.IsSatisfied {
		t.Fatalf("result = %#v, want booleans preserved (soft guard)", result)
	}
	if !strings.Contains(result.ReviewNotes, "关键代码位置格式无效") {
		t.Fatalf("ReviewNotes = %q, want invalid key location note", result.ReviewNotes)
	}
}

func TestApplyCodexReviewEvidenceGuardsRejectsEmptyPassingReviewNotes(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(main.go) error = %v", err)
	}

	result := CodexReviewResult{
		IsCompleted:        true,
		IsSatisfied:        true,
		ReviewNotes:        "无",
		NextPrompt:         "无",
		NextPromptTaskType: "未归类",
		KeyLocations:       "main.go:1",
	}

	applyCodexReviewEvidenceGuards(repoDir, &pgCodeProjectContext{
		Exists: true,
		Git: pgCodeGitContext{
			InGit:        true,
			ChangedFiles: []string{"main.go"},
		},
	}, &result)

	if !result.IsCompleted {
		t.Fatalf("IsCompleted = false, want preserved true")
	}
	if result.IsSatisfied {
		t.Fatalf("IsSatisfied = true, want downgraded false for empty pass note")
	}
	if !strings.Contains(result.ReviewNotes, "通过依据不足") {
		t.Fatalf("ReviewNotes = %q, want pass evidence guard note", result.ReviewNotes)
	}
	if strings.TrimSpace(result.NextPrompt) == "" || strings.TrimSpace(result.NextPrompt) == "无" {
		t.Fatalf("NextPrompt = %q, want concrete repair prompt", result.NextPrompt)
	}
}

func TestApplyCodexReviewEvidenceGuardsDowngradesHighRiskStaticPassWithPrompt(t *testing.T) {
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "frontend.js"), []byte("console.log('ws')\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(frontend.js) error = %v", err)
	}

	result := CodexReviewResult{
		IsCompleted:        true,
		IsSatisfied:        true,
		ReviewNotes:        "已核验 WebSocket 自动同步相关代码。未运行页面或接口，仅静态取证。",
		NextPrompt:         "无",
		NextPromptTaskType: "未归类",
		KeyLocations:       "frontend.js:1",
	}

	applyCodexReviewEvidenceGuards(repoDir, &pgCodeProjectContext{
		Exists: true,
		Git: pgCodeGitContext{
			InGit:        true,
			ChangedFiles: []string{"frontend.js"},
		},
	}, &result)

	if !result.IsCompleted {
		t.Fatalf("IsCompleted = false, want preserved true")
	}
	if result.IsSatisfied {
		t.Fatalf("IsSatisfied = true, want downgraded false for high-risk static pass")
	}
	if !strings.Contains(result.ReviewNotes, "自动同步链路证据不足") {
		t.Fatalf("ReviewNotes = %q, want concrete high-risk reason", result.ReviewNotes)
	}
	if !strings.Contains(result.NextPrompt, "自动同步不可靠") || !strings.Contains(result.NextPrompt, "WebSocket") {
		t.Fatalf("NextPrompt = %q, want concrete repair prompt", result.NextPrompt)
	}
}

func TestPolishReviewNextPromptTextRemovesReviewTone(t *testing.T) {
	got := polishReviewNextPromptText("请补齐并核验自动同步链路：验收时确认前端收到事件后能回读最新数据。", "", "Bug修复")

	if strings.Contains(got, "请补齐") || strings.Contains(got, "核验") || strings.Contains(got, "链路") || strings.Contains(got, "验收时") {
		t.Fatalf("polishReviewNextPromptText() = %q, still contains review-tone wording", got)
	}
	if !strings.Contains(got, "修复自动同步流程") && !strings.Contains(got, "修复自动同步") {
		t.Fatalf("polishReviewNextPromptText() = %q, want natural repair wording", got)
	}
}

func TestPolishReviewNextPromptTextShortensRepeatedBugOpening(t *testing.T) {
	currentPrompt := "修复面试官进入评价页时已配置模板仍可能无法自动带出的异常：当前面试阶段名称与模板阶段只是大小写、空格或常见写法差异时，评价页应加载已启用模板，并展示对应维度、权重和必填项。"
	nextPrompt := "修复面试官进入评价页时，当前面试阶段与已启用模板阶段属于同一常见阶段写法、但模板阶段本身还带有空格或大小写差异时仍不能自动加载模板的问题；修复后这类阶段差异应稳定加载已启用模板，并在页面展示模板维度、权重和必填项，只有确实没有可用模板时才使用通用评价提示。还需要继续确认阶段匹配规则覆盖常见写法。"

	got := polishReviewNextPromptText(nextPrompt, currentPrompt, "Bug修复")

	if strings.Contains(got, "修复面试官进入评价页时") {
		t.Fatalf("polishReviewNextPromptText() = %q, still repeats previous long opening", got)
	}
	if len(splitChineseSentences(got)) > 3 {
		t.Fatalf("polishReviewNextPromptText() = %q, want at most 3 sentences", got)
	}
	if !strings.HasPrefix(got, "修复") {
		t.Fatalf("polishReviewNextPromptText() = %q, want bug prompt prefix", got)
	}
	if !strings.Contains(got, "已启用模板") || !strings.Contains(got, "通用评价提示") {
		t.Fatalf("polishReviewNextPromptText() = %q, lost key business expectations", got)
	}
}
