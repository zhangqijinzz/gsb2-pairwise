package annotation

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryPathRejectsEscapeAndSymlink(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"../outside", "/absolute", ".", "", "a/../../b"} {
		if _, err := repositoryPath(root, rel); err == nil {
			t.Errorf("accepted unsafe path %q", rel)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := repositoryPath(root, "link/repo"); err == nil {
		t.Fatal("accepted symlink ancestor")
	}
	got, err := repositoryPath(root, "题目/repo")
	realRoot, _ := filepath.EvalSymlinks(root)
	if err != nil || got != filepath.Join(realRoot, "题目/repo") {
		t.Fatalf("%q %v", got, err)
	}
}

func TestTraceArchiveRejectsTraversal(t *testing.T) {
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	w.WriteHeader(&tar.Header{Name: "../escape.jsonl", Mode: 0600, Size: 2})
	w.Write([]byte("{}"))
	w.Close()
	if _, err := unpackTraces(b.Bytes(), t.TempDir()); err == nil {
		t.Fatal("accepted archive traversal")
	}
}

func TestTraceArchivePreservesSessionAndSubagentFiles(t *testing.T) {
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, name := range []string{"projects/-workspace/s.jsonl", "projects/-workspace/s/subagents/agent-a.jsonl"} {
		w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: 2})
		w.Write([]byte("{}"))
	}
	w.Close()
	dir := t.TempDir()
	files, err := unpackTraces(b.Bytes(), dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("files %v error %v", files, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "projects/-workspace/s/subagents/agent-a.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotRepositoryPreservesInitialCommit(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "main.txt"), []byte("initial"), 0600)
	sha, err := prepareRepository(context.Background(), src)
	if err != nil || len(sha) != 40 {
		t.Fatalf("prepare %q %v", sha, err)
	}
	dst := filepath.Join(t.TempDir(), "repo")
	if err := cloneInitialRepository(context.Background(), src, dst, sha); err != nil {
		t.Fatal(err)
	}
	b, err := runCommand(context.Background(), dst, "git", "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(string(b)) != sha {
		t.Fatalf("changed snapshot %s %v", b, err)
	}
	os.WriteFile(filepath.Join(src, "main.txt"), []byte("changed"), 0600)
	if err := cloneInitialRepository(context.Background(), src, filepath.Join(t.TempDir(), "repo"), sha); err == nil {
		t.Fatal("accepted uncommitted source changes")
	}
}
