package annotation

import (
	"context"
	"github.com/blueship581/pinru/internal/gitops"
	"github.com/blueship581/pinru/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishInitialUsesFrozenBaselineAndIsIdempotent(t *testing.T) {
	s, _, source := annotationFixture(t)
	if err := s.store.CreateGitHubAccount(store.GitHubAccount{ID: "account", Username: "owner", Token: "fixture", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.loadCase("题目-1")
	c.TaskName = "cyc-05"
	c.SourcePath = filepath.Join(filepath.Dir(source), "cyc-05-0-1代码生成-1")
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(source, "later.txt"), []byte("uncommitted later output"), 0600)
	calls := 0
	s.publishInitial = func(ctx context.Context, baseline, repo, sha string, account store.GitHubAccount) (string, error) {
		calls++
		if repo != "cyc-05-1" {
			t.Fatalf("repository = %q", repo)
		}
		if baseline == source || sha != c.InitialSHA {
			t.Fatal("not using frozen initial state")
		}
		if _, err := os.Stat(filepath.Join(baseline, "later.txt")); !os.IsNotExist(err) {
			t.Fatal("later code leaked into initial snapshot")
		}
		return "https://github.com/owner/" + repo + "/commit/" + sha, nil
	}
	for i := 0; i < 2; i++ {
		updated, err := s.PublishSnapshot(context.Background(), PrepareRequest{TaskID: c.TaskID})
		if err != nil || !strings.HasSuffix(updated.SnapshotURL, c.InitialSHA) {
			t.Fatalf("publish: %v %v", updated, err)
		}
	}
	if calls != 1 {
		t.Fatalf("published %d times", calls)
	}
}

func TestSnapshotPublicationPreservesExistingRemoteHistory(t *testing.T) {
	s, _, _ := annotationFixture(t)
	c, _ := s.loadCase("题目-1")
	ctx := context.Background()
	remote := filepath.Join(t.TempDir(), "remote.git")
	if _, err := runCommand(ctx, "", "git", "init", "--bare", remote); err != nil {
		t.Fatal(err)
	}
	baseline := filepath.Join(s.caseDir(c.TaskID), "initial", c.InitialSHA)
	work := filepath.Join(t.TempDir(), "publish")
	if err := prepareSnapshotPublication(ctx, baseline, work, c.InitialSHA, remote); err != nil {
		t.Fatal(err)
	}
	// An earlier incarnation of this task already published a different main.
	if _, err := runCommand(ctx, work, "git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "Existing remote history"); err != nil {
		t.Fatal(err)
	}
	old, _ := runCommand(ctx, work, "git", "rev-parse", "HEAD")
	if _, err := runCommand(ctx, work, "git", "push", "origin", "main:main"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(ctx, work, "git", "checkout", "--detach", c.InitialSHA); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(ctx, work, "git", "branch", "-f", "main", c.InitialSHA); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := gitops.PublishSnapshotCommit(ctx, work, c.InitialSHA, "", ""); err != nil {
			t.Fatalf("snapshot retry failed: %v", err)
		}
	}
	got, err := runCommand(ctx, remote, "git", "rev-parse", "refs/heads/main")
	if err != nil || string(got) != string(old) {
		t.Fatal("existing main was changed")
	}
	got, err = runCommand(ctx, remote, "git", "rev-parse", "refs/heads/initial/"+c.InitialSHA)
	if err != nil || strings.TrimSpace(string(got)) != c.InitialSHA {
		t.Fatalf("snapshot not retained: %s %v", got, err)
	}
	// Even a conflicting SHA-named ref must never be moved by a retry.
	ref := "refs/heads/initial/" + c.InitialSHA
	if _, err := runCommand(ctx, remote, "git", "update-ref", ref, strings.TrimSpace(string(old))); err != nil {
		t.Fatal(err)
	}
	if err := gitops.PublishSnapshotCommit(ctx, work, c.InitialSHA, "", ""); err == nil {
		t.Fatal("accepted conflicting snapshot ref")
	}
	got, err = runCommand(ctx, remote, "git", "rev-parse", ref)
	if err != nil || string(got) != string(old) {
		t.Fatal("conflicting snapshot ref was overwritten")
	}
}

func TestSnapshotPublicationPushesInitialCommitWithoutExistingOrigin(t *testing.T) {
	s, _, source := annotationFixture(t)
	c, err := s.loadCase("题目-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	baseline := filepath.Join(s.caseDir(c.TaskID), "initial", c.InitialSHA)
	remote := filepath.Join(t.TempDir(), "remote.git")
	if _, err := runCommand(ctx, "", "git", "init", "--bare", remote); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "later.txt"), []byte("later output"), 0600); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "publish")
	if err := prepareSnapshotPublication(ctx, baseline, work, c.InitialSHA, remote); err != nil {
		t.Fatal(err)
	}
	if err := gitops.PublishSnapshotCommit(ctx, work, c.InitialSHA, "", ""); err != nil {
		t.Fatal(err)
	}
	head, err := runCommand(ctx, remote, "git", "rev-parse", "refs/heads/main")
	if err != nil || strings.TrimSpace(string(head)) != c.InitialSHA {
		t.Fatalf("pushed %s, want %s: %v", head, c.InitialSHA, err)
	}
	if _, err := runCommand(ctx, remote, "git", "cat-file", "-e", "main:later.txt"); err == nil {
		t.Fatal("published later output")
	}
}
