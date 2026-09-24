package codepush

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveRepoNameFromCustomProjectPath(t *testing.T) {
	tests := map[string]string{
		"/tmp/cotv21/zw-005-Feature迭代-2/source": "zw-5-2",
		"/tmp/zw-10-17":         "zw-10-17",
		"/tmp/zw001-0-1代码生成-03": "zw-1-3",
	}

	for input, want := range tests {
		if got := deriveRepoName(input); got != want {
			t.Fatalf("deriveRepoName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCommitWorkspaceRejectsNoChanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := commitAllForTest(dir, "initial"); err != nil {
		t.Fatal(err)
	}

	_, err := commitWorkspace(dir, "session-1", false)
	if err == nil || !strings.Contains(err.Error(), noCommitChangeMsg) {
		t.Fatalf("expected no-change error, got %v", err)
	}
}

func TestCommitWorkspaceOrReuseHeadUsesExistingCommitWhenClean(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := commitAllForTest(dir, "initial"); err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(gitOutputForTest(t, dir, "rev-parse", "HEAD"))

	sha, err := commitWorkspaceOrReuseHead(dir, "session-2")
	if err != nil {
		t.Fatal(err)
	}
	if sha != head {
		t.Fatalf("sha = %q, want existing HEAD %q", sha, head)
	}
	msg := gitOutputForTest(t, dir, "log", "-1", "--pretty=%B")
	if strings.TrimSpace(msg) != "initial" {
		t.Fatalf("commit message = %q, want original message", msg)
	}
}

func TestCommitWorkspaceCreatesFullSHA(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sha, err := commitWorkspace(dir, "session-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(sha) != 40 {
		t.Fatalf("commit sha length = %d, want 40: %s", len(sha), sha)
	}
	msg := gitOutputForTest(t, dir, "log", "-1", "--pretty=%B")
	if strings.TrimSpace(msg) != "session-1" {
		t.Fatalf("commit message = %q", msg)
	}
}

func TestCommitWorkspaceKeepsConfiguredAuthor(t *testing.T) {
	// Exercise local identity without inheriting the developer's global author.
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	dir := t.TempDir()
	if err := runGit(dir, "init", "-b", mainBranch); err != nil {
		if err := runGit(dir, "init"); err != nil {
			t.Fatal(err)
		}
	}
	if err := runGit(dir, "config", "user.name", "zhouwei"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(dir, "config", "user.email", "zhouwei@holdzone.cn"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := commitWorkspace(dir, "session-1", false); err != nil {
		t.Fatal(err)
	}

	author := gitOutputForTest(t, dir, "log", "-1", "--pretty=%an <%ae>")
	if strings.TrimSpace(author) != "zhouwei <zhouwei@holdzone.cn>" {
		t.Fatalf("author = %q", author)
	}
}

func TestCommitWorkspaceRewritesPinruAuthorFromGlobalConfig(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	if err := runGit(dir, "init", "-b", mainBranch); err != nil {
		if err := runGit(dir, "init"); err != nil {
			t.Fatal(err)
		}
	}
	if err := runGit(dir, "config", "user.name", "PINRU"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(dir, "config", "user.email", "pinru@local"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(dir, "config", "--global", "user.name", "zhouwei"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(dir, "config", "--global", "user.email", "zhouwei@holdzone.cn"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := commitWorkspace(dir, "session-1", false); err != nil {
		t.Fatal(err)
	}

	author := gitOutputForTest(t, dir, "log", "-1", "--pretty=%an <%ae>")
	if strings.TrimSpace(author) != "zhouwei <zhouwei@holdzone.cn>" {
		t.Fatalf("author = %q", author)
	}
}

func commitAllForTest(dir, msg string) error {
	if err := runGit(dir, "init", "-b", mainBranch); err != nil {
		if err := runGit(dir, "init"); err != nil {
			return err
		}
	}
	if err := runGit(dir, "config", "user.name", "test"); err != nil {
		return err
	}
	if err := runGit(dir, "config", "user.email", "test@example.com"); err != nil {
		return err
	}
	if err := runGit(dir, "add", "-A"); err != nil {
		return err
	}
	return runGit(dir, "commit", "-m", msg)
}

func gitOutputForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
