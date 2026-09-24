package annotation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	domain "github.com/blueship581/pinru/internal/annotation"
)

func TestPairwiseRunFolderOnlyAcceptsRunWorkspaceLayout(t *testing.T) {
	for _, test := range []struct {
		workspace string
		want      string
	}{
		{"/Users/demo/cycgsb06-claude-runs/run-11-a/workspace", "/Users/demo/cycgsb06-claude-runs/run-11-a"},
		{"/workspace", ""},
		{"/Users/demo/workspace", ""},
		{"/Users/demo/run-11-a/repo", ""},
		{"/Users/demo/run-11-a/workspace/nested", ""},
		{"relative/workspace", ""},
		{"", ""},
		{"/Users/demo/run-11-a/run-12-a/workspace", "/Users/demo/run-11-a/run-12-a"},
	} {
		if got := pairwiseRunFolder(test.workspace); got != test.want {
			t.Fatalf("pairwiseRunFolder(%q) = %q, want %q", test.workspace, got, test.want)
		}
	}
}

func TestClearPairwiseContainerRemovesContainerAndRunFolder(t *testing.T) {
	s, _, _ := annotationFixture(t)
	c, err := s.EnablePairwise(EnablePairwiseRequest{
		TaskID: "题目-1", Language: "Go", Harness: "Claude Code", HarnessVersion: "1.0.0",
		OS: "MacOS/Linux", Environment: "无外部依赖", Validity: domain.PairwiseValidityValid,
	})
	if err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(t.TempDir(), "cycgsb06-claude-runs", "run-11-a")
	workspace := filepath.Join(runDir, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	c.Pairwise.RunA.ContainerID = "container-a"
	c.Pairwise.RunA.ContainerName = "cycgsb06-claude-11-a"
	c.Pairwise.RunA.WorkspacePath = workspace
	c.Pairwise.RunA.RepoRelativePath = "repo"
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}

	var commands []string
	s.command = func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		commands = append(commands, strings.Join(append([]string{name}, args...), " "))
		return nil, nil
	}

	updated, err := s.clearPairwiseContainer(context.Background(), PairwiseSideRequest{TaskID: "题目-1", Side: domain.PairwiseSideA})
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || commands[0] != "docker rm -f cycgsb06-claude-11-a" {
		t.Fatalf("commands = %v", commands)
	}
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatalf("run folder still exists: %v", err)
	}
	if !updated.Pairwise.RunA.ContainerCleared {
		t.Fatal("cleared side was not marked as cleared")
	}
	// 绑定信息保留给制表和追溯，不能被清除动作抹掉。
	if updated.Pairwise.RunA.ContainerID != "container-a" || updated.Pairwise.RunA.WorkspacePath != workspace || updated.Pairwise.RunA.RepoRelativePath != "repo" {
		t.Fatalf("binding record was wiped: %+v", updated.Pairwise.RunA)
	}
	if issues := domain.ValidatePairwiseCase(*updated, true); containsIssue(issues, "A 独立容器尚未绑定") {
		t.Fatalf("formal validation lost the container record: %v", issues)
	}

	// 容器和目录都已经不存在时，重复清除仍然成功（对应手工删过容器的场景）。
	if _, err := s.clearPairwiseContainer(context.Background(), PairwiseSideRequest{TaskID: "题目-1", Side: domain.PairwiseSideA}); err != nil {
		t.Fatalf("second clear failed: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("second clear should still call docker rm, commands = %v", commands)
	}
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatalf("run folder reappeared: %v", err)
	}
}

func TestClearPairwiseContainerToleratesMissingContainer(t *testing.T) {
	s, _, _ := annotationFixture(t)
	c, err := s.EnablePairwise(EnablePairwiseRequest{
		TaskID: "题目-1", Language: "Go", Harness: "Claude Code", HarnessVersion: "1.0.0",
		OS: "MacOS/Linux", Environment: "无外部依赖", Validity: domain.PairwiseValidityValid,
	})
	if err != nil {
		t.Fatal(err)
	}
	c.Pairwise.RunB.ContainerID = "container-b"
	c.Pairwise.RunB.ContainerName = "cycgsb06-claude-11-b"
	c.Pairwise.RunB.WorkspacePath = ""
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}
	s.command = func(context.Context, string, string, ...string) ([]byte, error) {
		return nil, errors.New("Error response from daemon: No such container: cycgsb06-claude-11-b")
	}

	updated, err := s.clearPairwiseContainer(context.Background(), PairwiseSideRequest{TaskID: "题目-1", Side: domain.PairwiseSideB})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Pairwise.RunB.ContainerCleared {
		t.Fatal("missing container should still clear the side")
	}
}

func TestClearPairwiseContainerRejectsSideWithoutTargets(t *testing.T) {
	s, _, _ := annotationFixture(t)
	if _, err := s.EnablePairwise(EnablePairwiseRequest{
		TaskID: "题目-1", Language: "Go", Harness: "Claude Code", HarnessVersion: "1.0.0",
		OS: "MacOS/Linux", Environment: "无外部依赖", Validity: domain.PairwiseValidityValid,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.clearPairwiseContainer(context.Background(), PairwiseSideRequest{TaskID: "题目-1", Side: domain.PairwiseSideA}); err == nil {
		t.Fatal("clearing an unbound side should fail")
	}
}

func containsIssue(issues []string, want string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, want) {
			return true
		}
	}
	return false
}
