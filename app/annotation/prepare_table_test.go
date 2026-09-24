package annotation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domain "github.com/blueship581/pinru/internal/annotation"
	"github.com/blueship581/pinru/internal/store"
)

func TestBatchCaptureAndTableStartsIndependentReviewsTogether(t *testing.T) {
	s, trace, _ := annotationFixture(t)
	if _, err := s.Capture(CaptureRequest{TaskID: "题目-1", TracePath: trace}); err != nil {
		t.Fatal(err)
	}

	sourceTwo := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceTwo, "main.py"), []byte("def add(a,b): return a+b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	taskTwo, err := s.store.GetTask("题目-1")
	if err != nil {
		t.Fatal(err)
	}
	taskTwo.ID = "题目-2"
	taskTwo.ProjectName = "示例题二"
	taskTwo.LocalPath = &sourceTwo
	if err := s.store.CreateTask(*taskTwo); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareCase(PrepareRequest{TaskID: taskTwo.ID}); err != nil {
		t.Fatal(err)
	}
	traceTwo := filepath.Join(t.TempDir(), "session.jsonl")
	writeFixtureTrace(t, traceTwo, sourceTwo, 1)
	if _, err := s.Capture(CaptureRequest{TaskID: taskTwo.ID, TracePath: traceTwo}); err != nil {
		t.Fatal(err)
	}

	s.cli, _ = fakeReviewCLIWithDelay(t, "1")
	payload, _ := json.Marshal(map[string]string{"projectId": "batch"})
	started := time.Now()
	output, err := s.ExecuteJob(context.Background(), "annotation_batch_capture_table", string(payload))
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	result := output.(*BatchPrepareResult)
	if result.Prepared != 2 || result.Failed != 0 {
		t.Fatalf("batch = %+v", result)
	}
	if elapsed >= 1800*time.Millisecond {
		t.Fatalf("reviews ran one after another: elapsed %s", elapsed)
	}
}

func TestBatchReviewConcurrencyUsesConfiguredBound(t *testing.T) {
	s, _, _ := annotationFixture(t)
	if got := s.batchReviewConcurrency(20); got != 4 {
		t.Fatalf("default concurrency = %d, want 4", got)
	}
	if err := s.store.SetConfig("annotation_review_concurrency", "6"); err != nil {
		t.Fatal(err)
	}
	if got := s.batchReviewConcurrency(20); got != 6 {
		t.Fatalf("configured concurrency = %d, want 6", got)
	}
	if err := s.store.SetConfig("annotation_review_concurrency", "99"); err != nil {
		t.Fatal(err)
	}
	if got := s.batchReviewConcurrency(3); got != 3 {
		t.Fatalf("clamped concurrency = %d, want 3", got)
	}
}

func TestBatchCaptureAndTableSelectsTasksAcrossProjects(t *testing.T) {
	s, trace, _ := annotationFixture(t)
	if _, err := s.Capture(CaptureRequest{TaskID: "题目-1", TracePath: trace}); err != nil {
		t.Fatal(err)
	}

	secondProject := "batch-two"
	if err := s.store.CreateProject(store.Project{ID: secondProject, Name: "第二批次", Models: "[]", CloneBasePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	original, err := s.store.GetTask("题目-1")
	if err != nil {
		t.Fatal(err)
	}
	secondSource := t.TempDir()
	if err := os.WriteFile(filepath.Join(secondSource, "main.py"), []byte("def sub(a,b): return a-b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := *original
	second.ID = "题目-2"
	second.ProjectConfigID = &secondProject
	second.ProjectName = "第二项目题目"
	second.LocalPath = &secondSource
	if err := s.store.CreateTask(second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareCase(PrepareRequest{TaskID: second.ID}); err != nil {
		t.Fatal(err)
	}
	secondTrace := filepath.Join(t.TempDir(), "session.jsonl")
	writeFixtureTrace(t, secondTrace, secondSource, 1)
	if _, err := s.Capture(CaptureRequest{TaskID: second.ID, TracePath: secondTrace}); err != nil {
		t.Fatal(err)
	}

	unselected := *original
	unselected.ID = "题目-3"
	unselected.ProjectName = "未选择题目"
	if err := s.store.CreateTask(unselected); err != nil {
		t.Fatal(err)
	}

	s.cli, _ = fakeReviewCLI(t)
	payload, _ := json.Marshal(BatchPrepareRequest{TaskIDs: []string{"题目-2", "题目-1", "题目-2"}})
	output, err := s.ExecuteJob(context.Background(), "annotation_batch_capture_table", string(payload))
	if err != nil {
		t.Fatal(err)
	}
	result := output.(*BatchPrepareResult)
	if result.Total != 2 || result.Prepared != 2 || result.Failed != 0 {
		t.Fatalf("selected cross-project batch = %+v", result)
	}
	for _, item := range result.Items {
		if item.TaskID == unselected.ID {
			t.Fatalf("unselected task was reviewed: %+v", item)
		}
	}
}

func TestBatchCaptureAndTablePreparesThenSkipsValidSavedData(t *testing.T) {
	s, trace, _ := annotationFixture(t)
	if _, err := s.Capture(CaptureRequest{TaskID: "题目-1", TracePath: trace}); err != nil {
		t.Fatal(err)
	}
	cli, count := fakeReviewCLI(t)
	s.cli = cli
	payload, _ := json.Marshal(map[string]string{"projectId": "batch"})

	output, err := s.ExecuteJob(context.Background(), "annotation_batch_capture_table", string(payload))
	if err != nil {
		t.Fatal(err)
	}
	var first struct {
		Total    int `json:"total"`
		Prepared int `json:"prepared"`
		Failed   int `json:"failed"`
	}
	raw, _ := json.Marshal(output)
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatal(err)
	}
	if first.Total != 1 || first.Prepared != 1 || first.Failed != 0 {
		t.Fatalf("first batch = %+v", first)
	}

	// A functionally complete low-score review without a repair prompt is final
	// table data and must not spend another evaluator call in the next batch.
	c, err := s.store.GetAnnotationCase("题目-1")
	if err != nil {
		t.Fatal(err)
	}
	c.Rounds[0].Evaluations[0].NextPrompt = ""
	c.Rounds[0].Evaluations[0].NextPromptType = ""
	c.Rounds[0].Evaluations[0].Issues = nil
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}

	output, err = s.ExecuteJob(context.Background(), "annotation_batch_capture_table", string(payload))
	if err != nil {
		t.Fatal(err)
	}
	var second struct {
		Skipped int `json:"skipped"`
	}
	raw, _ = json.Marshal(output)
	if err := json.Unmarshal(raw, &second); err != nil {
		t.Fatal(err)
	}
	if second.Skipped != 1 {
		t.Fatalf("second batch = %+v", second)
	}
	runs, _ := os.ReadFile(count)
	if string(runs) != "called\n" {
		t.Fatalf("batch reran valid saved review: %q", runs)
	}
}

func TestBatchSelectedTasksRechecksLegacyOverLimitEvaluation(t *testing.T) {
	s, trace, _ := annotationFixture(t)
	if _, err := s.Capture(CaptureRequest{TaskID: "题目-1", TracePath: trace}); err != nil {
		t.Fatal(err)
	}
	cli, count := fakeReviewCLI(t)
	s.cli = cli
	if _, err := s.Review(ReviewRequest{TaskID: "题目-1", PromptID: "p1"}); err != nil {
		t.Fatal(err)
	}

	c, err := s.store.GetAnnotationCase("题目-1")
	if err != nil {
		t.Fatal(err)
	}
	five, four := 5, 4
	c.Rounds[0].Evaluations[0].Scores = [5]*int{&five, &five, &five, &five, &four}
	c.Rounds[0].Evaluations[0].NextPrompt = ""
	c.Rounds[0].Evaluations[0].NextPromptType = ""
	c.Rounds[0].Evaluations[0].Issues = nil
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(BatchPrepareRequest{TaskIDs: []string{"题目-1"}})
	if _, err := s.ExecuteJob(context.Background(), "annotation_batch_capture_table", string(payload)); err != nil {
		t.Fatal(err)
	}
	stored, err := s.store.GetAnnotationCase("题目-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Rounds[0].Evaluations) != 2 {
		t.Fatalf("legacy over-limit evaluation was reused: %+v", stored.Rounds[0].Evaluations)
	}
	if total, ok := domain.EvaluationScoreTotal(stored.Rounds[0].Evaluations[1]); !ok || total > 21 {
		t.Fatalf("replacement evaluation total = %d, complete = %v", total, ok)
	}
	runs, _ := os.ReadFile(count)
	if string(runs) != "called\ncalled\n" {
		t.Fatalf("legacy over-limit evaluation did not trigger a new review: %q", runs)
	}
}

func TestBatchSelectedTasksRechecksEvaluationFromStaleRules(t *testing.T) {
	s, trace, _ := annotationFixture(t)
	if _, err := s.Capture(CaptureRequest{TaskID: "题目-1", TracePath: trace}); err != nil {
		t.Fatal(err)
	}
	cli, count := fakeReviewCLI(t)
	s.cli = cli
	if _, err := s.Review(ReviewRequest{TaskID: "题目-1", PromptID: "p1"}); err != nil {
		t.Fatal(err)
	}

	c, err := s.store.GetAnnotationCase("题目-1")
	if err != nil {
		t.Fatal(err)
	}
	c.Rounds[0].Evaluations[0].SkillHash = "legacy-review-rules"
	c.Rounds[0].Evaluations[0].NextPrompt = ""
	c.Rounds[0].Evaluations[0].NextPromptType = ""
	c.Rounds[0].Evaluations[0].Issues = nil
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(BatchPrepareRequest{TaskIDs: []string{"题目-1"}})
	if _, err := s.ExecuteJob(context.Background(), "annotation_batch_capture_table", string(payload)); err != nil {
		t.Fatal(err)
	}
	stored, err := s.store.GetAnnotationCase("题目-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Rounds[0].Evaluations) != 2 {
		t.Fatalf("stale evaluation was reused: %+v", stored.Rounds[0].Evaluations)
	}
	runs, _ := os.ReadFile(count)
	if string(runs) != "called\ncalled\n" {
		t.Fatalf("stale evaluation did not trigger a new review: %q", runs)
	}
}

func TestCaptureAndTableCachesGradesWithoutRequiringAnotherRound(t *testing.T) {
	s, trace, source := annotationFixture(t)
	cli, count := fakeReviewCLI(t)
	s.cli = cli
	payload, _ := json.Marshal(CaptureRequest{TaskID: "题目-1", TracePath: trace})
	for attempt := 0; attempt < 2; attempt++ {
		output, err := s.ExecuteJob(context.Background(), "annotation_capture_table", string(payload))
		if err != nil {
			t.Fatal(err)
		}
		c := output.(*domain.Case)
		if len(c.Rounds) != 1 || len(c.Rounds[0].Evaluations) != 1 || *c.Rounds[0].Evaluations[0].Scores[0] != 4 {
			t.Fatalf("did not retain single-round low-score table: %+v", c)
		}
	}
	runs, _ := os.ReadFile(count)
	if string(runs) != "called\n" {
		t.Fatalf("reran cached review: %s", runs)
	}
	// A later real round is analyzed separately and preserves the first review.
	writeFixtureTrace(t, trace, source, 2)
	output, err := s.ExecuteJob(context.Background(), "annotation_capture_table", string(payload))
	if err != nil {
		t.Fatal(err)
	}
	c := output.(*domain.Case)
	if len(c.Rounds) != 2 || len(c.Rounds[0].Evaluations) != 1 || len(c.Rounds[1].Evaluations) != 1 {
		t.Fatalf("rounds %+v", c.Rounds)
	}
	runs, _ = os.ReadFile(count)
	if string(runs) != "called\ncalled\n" {
		t.Fatalf("unexpected reviews: %s", runs)
	}
}

func TestCaptureAndTablePreservesNonBlockingReadyGapsAsLimitations(t *testing.T) {
	s, trace, _ := annotationFixture(t)
	eval := map[string]any{
		"status": "ready", "scores": []int{5, 4, 4, 4, 4}, "descriptions": []string{"交付完整。", "在第1轮核对约束时，指令说明存在轻微遗漏，模型没有逐项说明边界条件，导致约束对应关系不够清晰。", "在第1轮开始实现前，规划拆解不够具体，模型没有列出验证步骤，导致完成路径缺少检查节点。", "在第1轮分析输入时，推理覆盖存在轻微不足，模型没有说明次要边界，导致部分判断依据未被记录。", "执行阶段出现了一次多余调用，这项操作没有帮助完成任务，增加了无效步骤。"},
		"descriptionChecks": []any{map[string]any{}, map[string]string{"judgment": "指令说明存在轻微遗漏", "location": "第1轮核对约束时", "behavior": "模型没有逐项说明边界条件", "consequence": "导致约束对应关系不够清晰"}, map[string]string{"judgment": "规划拆解不够具体", "location": "第1轮开始实现前", "behavior": "模型没有列出验证步骤", "consequence": "导致完成路径缺少检查节点"}, map[string]string{"judgment": "推理覆盖存在轻微不足", "location": "第1轮分析输入时", "behavior": "模型没有说明次要边界", "consequence": "导致部分判断依据未被记录"}, map[string]string{
			"judgment": "出现了一次多余调用", "location": "执行阶段", "behavior": "这项操作没有帮助完成任务", "consequence": "增加了无效步骤",
		}},
		"taskType": "0-1代码生成", "difficulty": "简单", "language": "Python", "environment": "无外部依赖", "harnessVersion": "2.1.0", "os": "MacOS/Linux",
		"evidence": []string{"代码和构建结果均已核验"}, "missing": []string{"未进行真实浏览器交互验证"},
		"requirementChecks": []map[string]string{{"requirement": "实现加法功能", "status": "completed", "evidence": "静态检查和构建均通过"}},
		"issues":            []map[string]string{}, "nextPrompt": "", "nextPromptType": "",
	}
	s.cli, _ = fakeReviewCLIWithEvaluation(t, eval, "")
	payload, _ := json.Marshal(CaptureRequest{TaskID: "题目-1", TracePath: trace})
	output, err := s.ExecuteJob(context.Background(), "annotation_capture_table", string(payload))
	if err != nil {
		t.Fatal(err)
	}
	result := output.(*domain.Case).Rounds[0].Evaluations[0]
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if result.Status != "ready" || len(result.Missing) != 0 {
		t.Fatalf("ready result still blocked: %+v", result)
	}
	limitations, ok := fields["limitations"].([]any)
	if !ok || len(limitations) != 1 || limitations[0] != "未进行真实浏览器交互验证" {
		t.Fatalf("limitations = %#v", fields["limitations"])
	}
}

func TestCaptureAndTableUsesContainerExecutionOSInsteadOfEvaluatorCommentary(t *testing.T) {
	s, trace, _ := annotationFixture(t)
	if _, err := s.Capture(CaptureRequest{TaskID: "题目-1", TracePath: trace}); err != nil {
		t.Fatal(err)
	}
	c, err := s.store.GetAnnotationCase("题目-1")
	if err != nil {
		t.Fatal(err)
	}
	c.ContainerID = "container-123"
	c.ContainerName = "fixture-claude-1"
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}
	eval := map[string]any{
		"status": "ready", "scores": []int{5, 4, 4, 4, 4}, "descriptions": []string{"交付完整。", "在第1轮核对约束时，指令说明存在轻微遗漏，模型没有逐项说明边界条件，导致约束对应关系不够清晰。", "在第1轮开始实现前，规划拆解不够具体，模型没有列出验证步骤，导致完成路径缺少检查节点。", "在第1轮分析输入时，推理覆盖存在轻微不足，模型没有说明次要边界，导致部分判断依据未被记录。", "在第1轮交付前，执行覆盖存在轻微不足，模型只完成主要检查，导致次要场景没有留下验证记录。"},
		"descriptionChecks": []any{map[string]any{}, map[string]string{"judgment": "指令说明存在轻微遗漏", "location": "第1轮核对约束时", "behavior": "模型没有逐项说明边界条件", "consequence": "导致约束对应关系不够清晰"}, map[string]string{"judgment": "规划拆解不够具体", "location": "第1轮开始实现前", "behavior": "模型没有列出验证步骤", "consequence": "导致完成路径缺少检查节点"}, map[string]string{"judgment": "推理覆盖存在轻微不足", "location": "第1轮分析输入时", "behavior": "模型没有说明次要边界", "consequence": "导致部分判断依据未被记录"}, map[string]string{"judgment": "执行覆盖存在轻微不足", "location": "第1轮交付前", "behavior": "模型只完成主要检查", "consequence": "导致次要场景没有留下验证记录"}},
		"taskType":          "0-1代码生成", "difficulty": "简单", "language": "Python", "environment": "无外部依赖", "harnessVersion": "2.1.0",
		"os":       "待补：被测轨迹未记录操作系统，报错格式偏向 Linux，仅为静态推断",
		"evidence": []string{"代码和构建结果均已核验"}, "missing": []string{}, "limitations": []string{"未做浏览器实机验证"},
		"requirementChecks": []map[string]string{{"requirement": "实现加法功能", "status": "completed", "evidence": "静态检查和构建均通过"}},
		"issues":            []map[string]string{}, "nextPrompt": "", "nextPromptType": "",
	}
	s.cli, _ = fakeReviewCLIWithEvaluation(t, eval, "")
	output, err := s.resumeTable(context.Background(), "题目-1")
	if err != nil {
		t.Fatal(err)
	}
	result := output.Rounds[0].Evaluations[0]
	if result.OS != "MacOS/Linux" {
		t.Fatalf("OS = %q, want container execution category MacOS/Linux", result.OS)
	}
}

func TestCaptureAndTableKeepsCaptureOnReviewFailure(t *testing.T) {
	s, trace, _ := annotationFixture(t)
	payload, _ := json.Marshal(CaptureRequest{TaskID: "题目-1", TracePath: trace})
	_, err := s.ExecuteJob(context.Background(), "annotation_capture_table", string(payload))
	if err == nil || !strings.Contains(err.Error(), "采集已保存") {
		t.Fatalf("unclear error: %v", err)
	}
	c, err := s.store.GetAnnotationCase("题目-1")
	if err != nil || len(c.Rounds) != 1 || len(c.Captures) != 1 {
		t.Fatalf("lost capture: %+v %v", c, err)
	}
}

func TestResumeUsesFrozenEvidenceAndKeepsCompletedRounds(t *testing.T) {
	s, trace, source := annotationFixture(t)
	s.cli, _ = fakeReviewCLI(t)
	if _, err := s.captureAndPrepareTable(context.Background(), CaptureRequest{TaskID: "题目-1", TracePath: trace}); err != nil {
		t.Fatal(err)
	}
	writeFixtureTrace(t, trace, source, 2)
	if _, err := s.Capture(CaptureRequest{TaskID: "题目-1", TracePath: trace}); err != nil {
		t.Fatal(err)
	}
	s.cli = nil
	if _, err := s.resumeTable(context.Background(), "题目-1"); err == nil {
		t.Fatal("expected missing evaluator failure")
	}
	cli, count := fakeReviewCLI(t)
	s.cli = cli
	// The original source can disappear after collection; retries use only saved evidence.
	if err := os.Rename(trace, filepath.Join(filepath.Dir(trace), "moved.jsonl")); err != nil {
		t.Fatal(err)
	}
	c, err := s.resumeTable(context.Background(), "题目-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Rounds) != 2 || len(c.Rounds[0].Evaluations) != 1 || len(c.Rounds[1].Evaluations) != 1 {
		t.Fatalf("lost rounds: %+v", c.Rounds)
	}
	data, _ := os.ReadFile(count)
	if string(data) != "called\n" {
		t.Fatalf("reran completed round: %q", data)
	}
}
