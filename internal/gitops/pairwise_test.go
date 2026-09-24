package gitops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparePairwiseBranchesStartAtSameInitialSHA(t *testing.T) {
	repo, initial := pairwiseTestRepo(t)
	for _, side := range []string{"A", "B"} {
		if err := PreparePairwiseBranch(context.Background(), repo, side, initial); err != nil {
			t.Fatalf("PreparePairwiseBranch(%s) error = %v", side, err)
		}
		if got := pairwiseGit(t, repo, "rev-parse", "HEAD"); got != initial {
			t.Fatalf("%s HEAD = %s, want %s", side, got, initial)
		}
	}
	if got := pairwiseGit(t, repo, "rev-parse", "A"); got != initial {
		t.Fatalf("A = %s, want %s", got, initial)
	}
	if got := pairwiseGit(t, repo, "rev-parse", "B"); got != initial {
		t.Fatalf("B = %s, want %s", got, initial)
	}
}

func TestPreparePairwiseBranchRejectsDirtyWorkspaceAndInvalidSide(t *testing.T) {
	repo, initial := pairwiseTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PreparePairwiseBranch(context.Background(), repo, "A", initial); err == nil || !strings.Contains(err.Error(), "未提交") {
		t.Fatalf("dirty error = %v", err)
	}
	if err := os.Remove(filepath.Join(repo, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
	if err := PreparePairwiseBranch(context.Background(), repo, "C", initial); err == nil {
		t.Fatal("accepted invalid side")
	}
}

func TestCommitPairwiseResultSwitchesDetachedInitialToTargetBranch(t *testing.T) {
	repo, initial := pairwiseTestRepo(t)
	pairwiseGit(t, repo, "checkout", "--detach", initial)
	if err := os.WriteFile(filepath.Join(repo, "result.txt"), []byte("B result"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := CommitPairwiseResult(context.Background(), repo, "B", initial, "session-b")
	if err != nil {
		t.Fatal(err)
	}
	if branch := pairwiseGit(t, repo, "branch", "--show-current"); branch != "B" {
		t.Fatalf("branch = %q, want B", branch)
	}
	if parent := pairwiseGit(t, repo, "rev-parse", got+"^"); parent != initial {
		t.Fatalf("artifact parent = %s, want %s", parent, initial)
	}
}

func TestCommitPairwiseResultRejectsSwitchAfterAnotherCommit(t *testing.T) {
	repo, initial := pairwiseTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "other.txt"), []byte("other work"), 0o600); err != nil {
		t.Fatal(err)
	}
	pairwiseGit(t, repo, "add", "other.txt")
	pairwiseGit(t, repo, "commit", "-m", "unrelated commit")
	if _, err := CommitPairwiseResult(context.Background(), repo, "B", initial, "session-b"); err == nil || !strings.Contains(err.Error(), "初始快照") {
		t.Fatalf("diverged branch error = %v", err)
	}
}

func TestCommitPairwiseNoChangesCreatesArtifactCommitWithInitialParent(t *testing.T) {
	repo, initial := pairwiseTestRepo(t)
	if err := PreparePairwiseBranch(context.Background(), repo, "A", initial); err != nil {
		t.Fatal(err)
	}
	got, err := CommitPairwiseResult(context.Background(), repo, "A", initial, "session-a")
	if err != nil {
		t.Fatal(err)
	}
	if got == initial {
		t.Fatalf("no-change artifact reused initial sha %s", initial)
	}
	if parent := pairwiseGit(t, repo, "rev-parse", got+"^"); parent != initial {
		t.Fatalf("artifact parent = %s, want %s", parent, initial)
	}
}

func TestCommitPairwiseResultCommitsChangesOnSide(t *testing.T) {
	repo, initial := pairwiseTestRepo(t)
	if err := PreparePairwiseBranch(context.Background(), repo, "B", initial); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "result.txt"), []byte("B result"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := CommitPairwiseResult(context.Background(), repo, "B", initial, "session-b")
	if err != nil {
		t.Fatal(err)
	}
	if got == initial || pairwiseGit(t, repo, "branch", "--show-current") != "B" {
		t.Fatalf("commit = %s branch = %s", got, pairwiseGit(t, repo, "branch", "--show-current"))
	}
	cmd := exec.Command("git", "merge-base", "--is-ancestor", initial, got)
	cmd.Dir = repo
	if err := cmd.Run(); err != nil {
		t.Fatalf("initial is not ancestor: %v", err)
	}
}

func pairwiseTestRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	pairwiseGit(t, repo, "init", "-b", "main")
	pairwiseGit(t, repo, "config", "user.name", "Pairwise Test")
	pairwiseGit(t, repo, "config", "user.email", "pairwise@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("initial"), 0o600); err != nil {
		t.Fatal(err)
	}
	pairwiseGit(t, repo, "add", "README.md")
	pairwiseGit(t, repo, "commit", "-m", "initial")
	return repo, pairwiseGit(t, repo, "rev-parse", "HEAD")
}

func pairwiseGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
