package annotation

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcli "github.com/blueship581/pinru/app/cli"
	domain "github.com/blueship581/pinru/internal/annotation"
	"github.com/blueship581/pinru/internal/store"
	"github.com/blueship581/pinru/migrations"
)

func annotationFixture(t *testing.T) (*AnnotationService, string, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "app.db"), migrations.All()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	project := "batch"
	if err := st.CreateProject(store.Project{ID: project, Name: "样本批次", Models: "[]", CloneBasePath: dir}); err != nil {
		t.Fatal(err)
	}
	deepSeekURL := "https://api.deepseek.com"
	if err := st.CreateLLMProvider(store.LLMProvider{ID: "deepseek-test", Name: "DeepSeek V4 Flash", ProviderType: "openai_compatible", Model: "deepseek-v4-flash", BaseURL: &deepSeekURL, APIKey: "test-key", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "main.py"), []byte("def add(a,b): return a+b\n"), 0600); err != nil {
		t.Fatal(err)
	}
	prompt := "实现加法功能"
	if err := st.CreateTask(store.Task{ID: "题目-1", ProjectName: "示例题", ProjectConfigID: &project, LocalPath: &source, PromptText: &prompt, TaskType: "0-1代码生成", PromptDifficulty: "困难", Status: "Claimed"}); err != nil {
		t.Fatal(err)
	}
	s := New(st, nil)
	prepared, err := s.PrepareCase(PrepareRequest{TaskID: "题目-1"})
	if err != nil || len(prepared.InitialSHA) != 40 {
		t.Fatalf("prepare: %+v %v", prepared, err)
	}
	trace := filepath.Join(dir, "session.jsonl")
	writeFixtureTrace(t, trace, source, 1)
	return s, trace, source
}

func writeFixtureTrace(t *testing.T, trace, cwd string, count int) {
	t.Helper()
	var data []byte
	for i := 1; i <= count; i++ {
		prompt, id := "实现加法功能", "p1"
		if i == 2 {
			prompt, id = "空值输入没有提示，请补上明确反馈", "p2"
		}
		for _, event := range []map[string]any{
			{"type": "user", "sessionId": "session", "promptId": id, "uuid": id, "cwd": cwd, "version": "2.1.0", "message": map[string]any{"role": "user", "content": prompt}},
			{"type": "assistant", "sessionId": "session", "uuid": "answer-" + id, "parentUuid": id, "message": map[string]any{"role": "assistant", "content": []map[string]string{{"type": "text", "text": "完成本轮实现"}}, "stop_reason": "end_turn"}},
		} {
			raw, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			data = append(data, raw...)
			data = append(data, '\n')
		}
	}
	if err := os.WriteFile(trace, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func fakeReviewCLI(t *testing.T) (*appcli.CliService, string) {
	return fakeReviewCLIWithDelay(t, "")
}

func fakeReviewCLIWithDelay(t *testing.T, delay string) (*appcli.CliService, string) {
	t.Helper()
	eval := map[string]any{
		"status": "ready", "scores": []int{4, 5, 4, 5, 3}, "descriptions": []string{"加法入口能返回结果；在 code/main.py 的 add 函数中，空值反馈不完整，函数只计算 a+b，导致空值输入时没有明确提示。", "按原要求提供了加法入口。", "在第1轮开始实现前，规划拆解不够具体，模型没有列出空值输入的验证步骤，导致空值反馈没有进入交付前检查。", "从输入类型推导处理分支。", "在本轮交付阶段，模型只运行了普通加法用例，没有执行空值输入验证，导致 code/main.py 的空值反馈缺陷在结束前没有被发现。"},
		"descriptionChecks": []any{
			map[string]string{"judgment": "空值反馈不完整", "location": "code/main.py 的 add 函数", "behavior": "函数只计算 a+b", "consequence": "空值输入时没有明确提示"},
			map[string]string{}, map[string]string{"judgment": "规划拆解不够具体", "location": "第1轮开始实现前", "behavior": "模型没有列出空值输入的验证步骤", "consequence": "导致空值反馈没有进入交付前检查"}, map[string]string{}, map[string]string{"judgment": "没有执行空值输入验证", "location": "本轮交付阶段", "behavior": "模型只运行了普通加法用例", "consequence": "导致 code/main.py 的空值反馈缺陷在结束前没有被发现"},
		},
		"taskType": "0-1代码生成", "difficulty": "简单", "language": "Python", "environment": "无外部依赖", "harnessVersion": "2.1.0", "os": "MacOS/Linux",
		"evidence": []string{"原轨迹第 2 行回复；静态检查 code/main.py；审核日志 evaluator.log"}, "missing": []string{},
		"requirementChecks": []map[string]string{{"requirement": "实现加法功能", "status": "completed", "evidence": "静态检查 code/main.py：add(a,b) 返回 a+b"}},
		"issues":            []map[string]string{{"kind": "bug", "description": "空值输入没有反馈", "evidence": "code/main.py 仅返回 a+b"}}, "nextPrompt": "修复空值输入时页面没有反馈的问题，补上明确提示，保留正常加法结果。", "nextPromptType": "Bug修复",
	}
	return fakeReviewCLIWithEvaluation(t, eval, delay)
}

func fakeReviewCLIWithEvaluation(t *testing.T, eval map[string]any, delay string) (*appcli.CliService, string) {
	t.Helper()
	dir := t.TempDir()
	payload := filepath.Join(dir, "response.json")
	count := filepath.Join(dir, "count")
	raw, _ := json.Marshal(eval)
	if err := os.WriteFile(payload, raw, 0600); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	wait := ""
	if delay != "" {
		wait = "sleep " + delay + "\n"
	}
	script := "#!/bin/sh\ncat >/dev/null\n" + wait + "while [ \"$#\" -gt 0 ]; do\nif [ \"$1\" = -o ]; then shift; cp " + quote(payload) + " \"$1\"; fi\nshift\ndone\nprintf 'called\\n' >> " + quote(count) + "\nprintf 'static verification recorded\\n'\n"
	binary := filepath.Join(dir, "codex-fake")
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return appcli.NewWithResolver(func(string) (string, error) { return binary, nil }), count
}

func TestAnnotationLocalWorkflowPreservesGradesAndExportsWholeBatchDraft(t *testing.T) {
	s, trace, source := annotationFixture(t)
	c, err := s.Capture(CaptureRequest{TaskID: "题目-1", TracePath: trace})
	if err != nil || len(c.Rounds) != 1 || c.Rounds[0].CaptureID == "" {
		t.Fatalf("capture %+v %v", c, err)
	}
	initial, revision := c.InitialSHA, c.Revision
	duplicate, err := s.Capture(CaptureRequest{TaskID: "题目-1", TracePath: trace})
	if err != nil || duplicate.Revision != revision {
		t.Fatalf("idempotent capture %+v %v", duplicate, err)
	}
	cli, count := fakeReviewCLI(t)
	s.cli = cli
	c, err = s.Review(ReviewRequest{TaskID: "题目-1", PromptID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	eval := c.Rounds[0].Evaluations[0]
	if eval.Scores[0] == nil || *eval.Scores[0] != 4 || eval.NextPrompt == "" || eval.ReviewPath == "" {
		t.Fatalf("review %+v", eval)
	}
	if _, err = s.Review(ReviewRequest{TaskID: "题目-1", PromptID: "p1"}); err != nil {
		t.Fatal(err)
	}
	runs, _ := os.ReadFile(count)
	if string(runs) != "called\n" {
		t.Fatalf("cache ran reviewer again: %s", runs)
	}
	writeFixtureTrace(t, trace, source, 2)
	c, err = s.Capture(CaptureRequest{TaskID: "题目-1", TracePath: trace})
	if err != nil || len(c.Rounds) != 2 || c.InitialSHA != initial || len(c.Rounds[0].Evaluations) != 1 {
		t.Fatalf("append %+v %v", c, err)
	}
	if _, err = s.PrepareCase(PrepareRequest{TaskID: "题目-1"}); err == nil {
		t.Fatal("replaced initial snapshot after execution")
	}
	if _, err = s.Export(ExportRequest{ProjectID: "batch", Submitter: "标注员", SubmittedAt: "2026-09-12"}); err == nil {
		t.Fatal("incomplete batch formally exported")
	}
	result, err := s.Export(ExportRequest{ProjectID: "batch", Submitter: "标注员", SubmittedAt: "2026-09-12", Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 2 || len(result.Issues) == 0 {
		t.Fatalf("draft %+v", result)
	}
	z, err := zip.OpenReader(result.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	var text strings.Builder
	for _, f := range z.File {
		if strings.HasPrefix(f.Name, "xl/") && strings.HasSuffix(f.Name, ".xml") {
			r, e := f.Open()
			if e != nil {
				t.Fatal(e)
			}
			io.Copy(&text, r)
			r.Close()
		}
	}
	for _, want := range []string{"实现加法功能", "空值输入没有提示", "加法入口能返回结果"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("draft dropped %q", want)
		}
	}
}

func TestReviewRepairsPresentationFormattingWithoutChangingScoresOrTechnicalEvidence(t *testing.T) {
	s, trace, _ := annotationFixture(t)
	eval := map[string]any{
		"status": "ready", "scores": []int{4, 5, 4, 5, 3}, "descriptions": []string{
			"以下是交付结果：`code/main.py` → 空值反馈不完整，函数只计算 a+b，导致空值输入没有提示。",
			"实现内容符合原始需求，没有扩展任务范围。",
			"在第1轮开始实现前，规划拆解不够具体，模型没有列出空值输入的验证步骤，导致该边界没有进入交付前检查。",
			"根据输入类型判断处理分支，结论与代码一致。",
			"执行过程出现环境命令失败，corepack enable 报 symlink '../lib/pnpm.js' -> '/usr/local/bin/pnpm'，随后改用临时目录并完成验证。",
		},
		"descriptionChecks": []any{
			map[string]string{"judgment": "空值反馈不完整", "location": "`code/main.py` →", "behavior": "函数只计算 a+b", "consequence": "空值输入没有提示"},
			map[string]any{}, map[string]string{"judgment": "规划拆解不够具体", "location": "第1轮开始实现前", "behavior": "模型没有列出空值输入的验证步骤", "consequence": "导致该边界没有进入交付前检查"}, map[string]any{},
			map[string]string{"judgment": "执行过程出现环境命令失败", "location": "corepack enable", "behavior": "symlink '../lib/pnpm.js' -> '/usr/local/bin/pnpm'", "consequence": "随后改用临时目录并完成验证"},
		},
		"taskType": "0-1代码生成", "difficulty": "简单", "language": "Python", "environment": "无外部依赖", "harnessVersion": "2.1.0", "os": "MacOS/Linux",
		"evidence": []string{"原轨迹记录 code/main.py 与完整命令输出"}, "missing": []string{},
		"requirementChecks": []map[string]string{{"requirement": "实现加法功能", "status": "completed", "evidence": "code/main.py 中的 add 返回计算结果"}},
		"issues": []map[string]string{
			{"kind": "bug", "description": "空值输入没有反馈", "evidence": "code/main.py 仅返回 a+b"},
			{"kind": "process", "description": "环境命令首次执行失败", "evidence": "corepack enable 返回 EACCES"},
		},
		"nextPrompt": "修复`code/main.py` → 补上空值输入提示。", "nextPromptType": "Bug修复",
	}
	s.cli, _ = fakeReviewCLIWithEvaluation(t, eval, "")
	if _, err := s.Capture(CaptureRequest{TaskID: "题目-1", TracePath: trace}); err != nil {
		t.Fatal(err)
	}

	c, err := s.Review(ReviewRequest{TaskID: "题目-1", PromptID: "p1"})
	if err != nil {
		t.Fatalf("review rejected repairable presentation formatting: %v", err)
	}
	got := c.Rounds[0].Evaluations[0]
	if strings.Contains(got.Descriptions[0], "以下是") || strings.ContainsAny(got.Descriptions[0], "`→") {
		t.Fatalf("description was not normalized to natural prose: %q", got.Descriptions[0])
	}
	if !strings.Contains(got.Descriptions[4], "symlink '../lib/pnpm.js' -> '/usr/local/bin/pnpm'") {
		t.Fatalf("technical error text was changed: %q", got.Descriptions[4])
	}
	wantScores := []int{4, 5, 4, 5, 3}
	for index, score := range got.Scores {
		if score == nil || *score != wantScores[index] {
			t.Fatalf("score %d changed during language normalization: %#v", index+1, score)
		}
	}
	if len(got.Evidence) != 1 || got.Evidence[0] != "原轨迹记录 code/main.py 与完整命令输出" {
		t.Fatalf("evidence changed during language normalization: %#v", got.Evidence)
	}
	if len(got.Issues) != 2 || got.Issues[0].Description != "空值输入没有反馈" || got.NextPrompt != "修复code/main.py，补上空值输入提示。" {
		t.Fatalf("review findings changed during language normalization: issues=%#v nextPrompt=%q", got.Issues, got.NextPrompt)
	}
}

func TestCachedReviewRejectsAlteredCodeAndTraceAttachments(t *testing.T) {
	for _, part := range []string{"code", "traces", "review"} {
		t.Run(part, func(t *testing.T) {
			s, trace, _ := annotationFixture(t)
			s.cli, _ = fakeReviewCLI(t)
			c, err := s.Capture(CaptureRequest{TaskID: "题目-1", TracePath: trace})
			if err != nil {
				t.Fatal(err)
			}
			c, err = s.Review(ReviewRequest{TaskID: "题目-1", PromptID: "p1"})
			if err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(c.Captures[0].Dir, part, "changed.txt")
			if part == "review" {
				p = filepath.Join(c.Rounds[0].Evaluations[0].ReviewPath, "evaluator.log")
			}
			if err := os.WriteFile(p, []byte("changed evidence"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Review(ReviewRequest{TaskID: "题目-1", PromptID: "p1"}); err == nil {
				t.Fatal("altered evidence reused cached score")
			}
		})
	}
}

func TestTraceSourceRejectsDifferentTaskAndAcceptsContainerParentWithKnownPrompt(t *testing.T) {
	c := &domain.Case{ContainerID: "actual-id", RepoRelativePath: "task-repo"}
	r := []domain.Round{{Prompt: "请在 task-repo 中实现加法功能", Cwd: "/workspace", Status: "complete"}}
	if err := validateTraceSource(c, "/host/task-repo", r, "实现加法功能"); err != nil {
		t.Fatal(err)
	}
	if err := validateTraceSource(c, "/host/task-repo", r, "实现登录功能"); err == nil {
		t.Fatal("accepted different task prompt")
	}
	r[0].Cwd = "/workspace/another-repo"
	if err := validateTraceSource(c, "/host/task-repo", r, "实现加法功能"); err == nil {
		t.Fatal("accepted other repository")
	}
}

func TestExistingRepositoryMustContainPreparedBaseline(t *testing.T) {
	s, _, source := annotationFixture(t)
	c, err := s.loadCase("题目-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyInitialAncestry(context.Background(), source, c.InitialSHA); err != nil {
		t.Fatal(err)
	}
	unrelated := t.TempDir()
	os.WriteFile(filepath.Join(unrelated, "other.txt"), []byte("other project"), 0600)
	if _, err := prepareRepository(context.Background(), unrelated); err != nil {
		t.Fatal(err)
	}
	if err := verifyInitialAncestry(context.Background(), unrelated, c.InitialSHA); err == nil {
		t.Fatal("accepted unrelated baseline")
	}
}

func TestAnnotationFormalExportKeepsLowScoresAndIncludesReviewEvidence(t *testing.T) {
	s, trace, _ := annotationFixture(t)
	s.cli, _ = fakeReviewCLI(t)
	c, err := s.Capture(CaptureRequest{TaskID: "题目-1", TracePath: trace})
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.Review(ReviewRequest{TaskID: "题目-1", PromptID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.SaveCaseSettings(SettingsRequest{TaskID: "题目-1", SnapshotURL: "https://github.com/example/project/commit/" + c.InitialSHA, Completed: true})
	if err != nil {
		t.Fatal(err)
	}
	s.verifySnapshot = func(context.Context, string) error { return errors.New("remote unavailable") }
	if _, err = s.Export(ExportRequest{ProjectID: "batch", Submitter: "标注员", SubmittedAt: "2026-09-12"}); err == nil {
		t.Fatal("unreachable snapshot formally exported")
	}
	s.verifySnapshot = func(context.Context, string) error { return nil }
	result, err := s.Export(ExportRequest{ProjectID: "batch", Submitter: "标注员", SubmittedAt: "2026-09-12"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 1 || len(result.Issues) != 0 {
		t.Fatalf("formal export: %+v", result)
	}
	foundLog := false
	if err := filepath.WalkDir(filepath.Join(filepath.Dir(result.OutputPath), "attachments"), func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Name() == "evaluator.log" {
			foundLog = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !foundLog {
		t.Fatal("formal bundle omitted evaluator verification log")
	}
}

func TestBindFreshContainerWithoutProjectsDirectory(t *testing.T) {
	s, _, _ := annotationFixture(t)
	workspace := t.TempDir()
	inspect, _ := json.Marshal(map[string]any{"ID": "real-container-id", "Name": "/claude-fixture", "State": "running", "Image": "fixture", "Mounts": []map[string]string{{"Type": "bind", "Source": workspace, "Destination": "/workspace"}}})
	absenceConfirmed := false
	s.command = func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		if name != "docker" {
			t.Fatalf("unexpected command %s %v", name, args)
		}
		switch args[0] {
		case "inspect":
			return inspect, nil
		case "cp":
			return nil, errors.New("projects does not exist")
		case "exec":
			if strings.Join(args, " ") != "exec real-container-id test ! -e "+containerTraceRoot {
				t.Fatalf("unexpected exec %v", args)
			}
			absenceConfirmed = true
			return nil, nil
		default:
			t.Fatalf("unexpected Docker command %v", args)
			return nil, nil
		}
	}
	c, err := s.BindContainer(BindRequest{TaskID: "题目-1", ContainerID: "claude-fixture", RepoRelativePath: "task-repo", CopyRepository: true})
	if err != nil {
		t.Fatal(err)
	}
	if !absenceConfirmed || c.ContainerID != "real-container-id" {
		t.Fatalf("binding %+v", c)
	}
	if err := verifyInitialAncestry(context.Background(), filepath.Join(workspace, "task-repo"), c.InitialSHA); err != nil {
		t.Fatal(err)
	}
}

func TestBindPairwiseRequiresTwoDifferentContainers(t *testing.T) {
	s, _, _ := annotationFixture(t)
	if _, err := s.EnablePairwise(EnablePairwiseRequest{
		TaskID: "题目-1", Language: "Python", Harness: "Claude Code", HarnessVersion: "1",
		OS: "MacOS/Linux", Validity: domain.PairwiseValidityValid,
	}); err != nil {
		t.Fatal(err)
	}
	workspaces := map[string]string{"container-a": t.TempDir(), "container-b": t.TempDir()}
	s.command = func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		if name != "docker" || len(args) < 4 || args[0] != "inspect" {
			t.Fatalf("unexpected command %s %v", name, args)
		}
		id := args[len(args)-1]
		workspace, ok := workspaces[id]
		if !ok {
			return nil, errors.New("unknown container")
		}
		return json.Marshal(map[string]any{
			"ID": id, "Name": "/" + id, "State": "running", "Image": "fixture",
			"Mounts": []map[string]string{{"Type": "bind", "Source": workspace, "Destination": "/workspace"}},
		})
	}
	bound, err := s.bindPairwiseContainer(context.Background(), PairwiseBindRequest{
		TaskID: "题目-1", Side: domain.PairwiseSideA, ContainerID: "container-a", RepoRelativePath: "task", CopyRepository: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bound.Pairwise.RunA.ContainerID != "container-a" || bound.Pairwise.RunA.PreparedAt == 0 {
		t.Fatalf("A binding = %+v", bound.Pairwise.RunA)
	}
	if branch := pairwiseGitBranch(t, filepath.Join(workspaces["container-a"], "task")); branch != "A" {
		t.Fatalf("A branch after binding = %q", branch)
	}
	if _, err := s.bindPairwiseContainer(context.Background(), PairwiseBindRequest{
		TaskID: "题目-1", Side: domain.PairwiseSideB, ContainerID: "container-a", RepoRelativePath: "task", CopyRepository: true,
	}); err == nil || !strings.Contains(err.Error(), "不同容器") {
		t.Fatalf("same container error = %v", err)
	}
	bound, err = s.bindPairwiseContainer(context.Background(), PairwiseBindRequest{
		TaskID: "题目-1", Side: domain.PairwiseSideB, ContainerID: "container-b", RepoRelativePath: "task", CopyRepository: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bound.Pairwise.RunB.ContainerID != "container-b" || bound.Pairwise.RunB.PreparedAt == 0 || bound.Pairwise.RunA.WorkspacePath == bound.Pairwise.RunB.WorkspacePath {
		t.Fatalf("pairwise bindings = A:%+v B:%+v", bound.Pairwise.RunA, bound.Pairwise.RunB)
	}
	if branch := pairwiseGitBranch(t, filepath.Join(workspaces["container-b"], "task")); branch != "B" {
		t.Fatalf("B branch after binding = %q", branch)
	}
}

func pairwiseGitBranch(t *testing.T, repo string) string {
	t.Helper()
	out, err := runCommand(context.Background(), repo, "git", "branch", "--show-current")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}
