package annotation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyEvidenceTreePreservesSourceAndInternalSymlinksButExcludesJunk(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	dst := filepath.Join(root, "capture")
	mustWriteFile(t, filepath.Join(src, "cmd", "main.go"), "package main\n", 0755)
	mustWriteFile(t, filepath.Join(src, "untracked.txt"), "untracked\n", 0640)
	mustWriteFile(t, filepath.Join(src, ".git", "HEAD"), "ref: main\n", 0644)
	mustWriteFile(t, filepath.Join(src, "node_modules", "dep", "index.js"), "junk\n", 0644)
	mustWriteFile(t, filepath.Join(src, "dist", "bundle.js"), "built\n", 0644)
	if err := os.Symlink(filepath.Join("cmd", "main.go"), filepath.Join(src, "main-link")); err != nil {
		t.Fatal(err)
	}

	hash, err := CopyEvidenceTree(context.Background(), src, dst)
	if err != nil {
		t.Fatalf("CopyEvidenceTree() error = %v", err)
	}
	if len(hash) != 64 {
		t.Fatalf("hash = %q, want sha256", hash)
	}
	for _, path := range []string{"cmd/main.go", "untracked.txt", "main-link", "dist/bundle.js"} {
		if _, err := os.Lstat(filepath.Join(dst, filepath.FromSlash(path))); err != nil {
			t.Fatalf("copied %s: %v", path, err)
		}
	}
	for _, path := range []string{".git", "node_modules"} {
		if _, err := os.Lstat(filepath.Join(dst, path)); !os.IsNotExist(err) {
			t.Fatalf("excluded %s exists or returned unexpected error: %v", path, err)
		}
	}
	target, err := os.Readlink(filepath.Join(dst, "main-link"))
	if err != nil || target != filepath.Join("cmd", "main.go") {
		t.Fatalf("copied symlink target = %q, err = %v", target, err)
	}
	copiedHash, err := TreeHash(context.Background(), dst)
	if err != nil {
		t.Fatalf("TreeHash(copy) error = %v", err)
	}
	if copiedHash != hash {
		t.Fatalf("copy hash = %q, source hash = %q", copiedHash, hash)
	}
}

func TestTreeHashChangesForContentAndExecutablePermission(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "run.sh")
	mustWriteFile(t, path, "echo one\n", 0644)
	first, err := TreeHash(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, path, "echo two\n", 0644)
	second, err := TreeHash(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("hash did not change with file content")
	}
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	third, err := TreeHash(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if second == third {
		t.Fatal("hash did not change with executable permission")
	}
}

func TestCopyEvidenceTreePreservesExactPermissionBitsAndGitWorktreeFileIsExcluded(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	dst := filepath.Join(root, "capture")
	mustWriteFile(t, filepath.Join(src, ".git"), "gitdir: /outside/worktree\n", 0644)
	mustWriteFile(t, filepath.Join(src, "open", "run.sh"), "#!/bin/sh\n", 0777)
	if err := os.Chmod(filepath.Join(src, "open"), 0777); err != nil {
		t.Fatal(err)
	}

	if _, err := CopyEvidenceTree(context.Background(), src, dst); err != nil {
		t.Fatalf("CopyEvidenceTree() error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git worktree metadata was copied: %v", err)
	}
	for _, path := range []string{"open", filepath.Join("open", "run.sh")} {
		sourceInfo, err := os.Lstat(filepath.Join(src, path))
		if err != nil {
			t.Fatal(err)
		}
		copyInfo, err := os.Lstat(filepath.Join(dst, path))
		if err != nil {
			t.Fatal(err)
		}
		if sourceInfo.Mode().Perm() != copyInfo.Mode().Perm() {
			t.Fatalf("%s permission = %o, want %o", path, copyInfo.Mode().Perm(), sourceInfo.Mode().Perm())
		}
	}
}

func TestEvidenceTreeRejectsSymlinkOutsideRootAndSpecialFiles(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.txt")
	mustWriteFile(t, outside, "secret", 0644)
	if err := os.Symlink(outside, filepath.Join(src, "outside-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := TreeHash(context.Background(), src); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("TreeHash() error = %v, want unsafe symlink error", err)
	}
	if _, err := CopyEvidenceTree(context.Background(), src, filepath.Join(root, "capture")); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("CopyEvidenceTree() error = %v, want unsafe symlink error", err)
	}
}

func TestCopyEvidenceTreeRefusesExistingDestinationAndCancelledContext(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	mustWriteFile(t, filepath.Join(src, "source.txt"), "source", 0644)
	dst := filepath.Join(root, "capture")
	mustWriteFile(t, filepath.Join(dst, "keep.txt"), "keep", 0644)

	if _, err := CopyEvidenceTree(context.Background(), src, dst); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("existing destination error = %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(dst, "keep.txt"))
	if err != nil || string(contents) != "keep" {
		t.Fatalf("existing destination was changed: %q, %v", contents, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CopyEvidenceTree(ctx, src, filepath.Join(root, "cancelled")); err == nil {
		t.Fatal("CopyEvidenceTree() error = nil, want context cancellation")
	}
}

func mustWriteFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
