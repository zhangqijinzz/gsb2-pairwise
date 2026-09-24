package annotation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	domain "github.com/blueship581/pinru/internal/annotation"
)

func TestReviewPairwiseStoresReviewBoundToBothEvidenceSources(t *testing.T) {
	s, _, source := annotationFixture(t)
	payload := map[string]any{
		"status": "ready", "conclusion": "A_better",
		"reason":             "A 先读取 code/main.py，保留 add(a,b) 并通过静态核验；B 读取 code/main.py 后删除返回值，静态核验暴露调用方拿不到结果。产物上 A 能正常返回加法结果，B 仍无法返回调用结果，因此 A 更完整。",
		"aCompletenessScore": 5, "aCompletenessDescription": "A 已交付加法返回结果，原始需求的关键功能可以使用。",
		"bCompletenessScore": 3, "bCompletenessDescription": "B 缺少返回结果，原始需求的调用链仍有功能遗漏。",
	}
	cli, _ := fakeReviewCLIWithEvaluation(t, payload, "")
	s.cli = cli
	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1", Language: "Python", Harness: "Codex CLI", HarnessVersion: "1", OS: "MacOS/Linux", Validity: domain.PairwiseValidityValid})
	if err != nil {
		t.Fatal(err)
	}
	c.Pairwise.Prompt = "实现加法功能"
	c.SnapshotURL = "https://github.com/example/repo/commit/" + c.InitialSHA
	for _, side := range []domain.PairwiseSide{domain.PairwiseSideA, domain.PairwiseSideB} {
		captureID := "capture-" + strings.ToLower(string(side))
		dir := filepath.Join(s.caseDir(c.TaskID), "pairwise-review-fixture", captureID)
		code := filepath.Join(dir, "code")
		traces := filepath.Join(dir, "traces")
		if err := os.MkdirAll(code, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(traces, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(code, "main.py"), []byte("def add(a,b): return a+b\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(traces, "session.jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		codeHash, _ := domain.TreeHash(context.Background(), code)
		traceHash, _ := domain.TreeHash(context.Background(), traces)
		capture := domain.Capture{ID: captureID, Dir: dir, CodePath: code, TracePath: filepath.Join(traces, "session.jsonl"), Hash: codeHash, TraceHash: traceHash}
		c.Captures = append(c.Captures, capture)
		run, _ := pairwiseRun(c.Pairwise, side)
		run.SessionID = "session-" + strings.ToLower(string(side))
		run.TurnCount = 1
		run.CaptureID = captureID
		run.CaptureHash = codeHash
		run.TraceHash = traceHash
		run.DeliverableSHA = strings.Repeat(map[domain.PairwiseSide]string{domain.PairwiseSideA: "b", domain.PairwiseSideB: "c"}[side], 40)
		run.DeliverableURL = "https://github.com/example/repo/commit/" + run.DeliverableSHA
	}
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}
	result, err := s.ReviewPairwise(context.Background(), PairwiseReviewRequest{TaskID: c.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	review := domain.CurrentPairwiseReview(*result)
	if review == nil || review.Current == nil || !*review.Current || review.Conclusion != domain.PairwiseConclusionA || review.SourceHashA == review.SourceHashB {
		t.Fatalf("review = %#v", review)
	}
	if review.SkillHash != pairwiseReviewSkillHash() {
		t.Fatalf("skill hash = %q", review.SkillHash)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal(err)
	}
}

func TestBatchReviewPairwiseReviewsThenReusesCurrentResult(t *testing.T) {
	s, _, _ := annotationFixture(t)
	payload := map[string]any{
		"status": "ready", "conclusion": "A_better",
		"reason":             "A 先读取 code/main.py，保留 add(a,b) 并完成静态核验；B 读取同一文件后删除返回值，静态核验发现调用方无法获得结果。产物上 A 能返回加法结果，B 的调用结果为空，因此 A 的过程和产物都更完整。",
		"aCompletenessScore": 5, "aCompletenessDescription": "A 已交付加法返回结果，原始需求的关键功能可以使用。",
		"bCompletenessScore": 3, "bCompletenessDescription": "B 缺少返回结果，原始需求的调用链仍有功能遗漏。",
	}
	cli, _ := fakeReviewCLIWithEvaluation(t, payload, "")
	s.cli = cli
	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1", Language: "Python", Harness: "Codex CLI", HarnessVersion: "1", OS: "MacOS/Linux", Validity: domain.PairwiseValidityValid})
	if err != nil {
		t.Fatal(err)
	}
	c.Pairwise.Language = ""
	c.Pairwise.Harness = ""
	c.Pairwise.HarnessVersion = ""
	c.Pairwise.OS = ""
	c.Pairwise.Validity = ""
	c.Pairwise.Prompt = "实现加法功能"
	c.SnapshotURL = "https://github.com/example/repo/commit/" + c.InitialSHA
	for _, side := range []domain.PairwiseSide{domain.PairwiseSideA, domain.PairwiseSideB} {
		captureID := "batch-capture-" + strings.ToLower(string(side))
		dir := filepath.Join(s.caseDir(c.TaskID), "pairwise-batch-fixture", captureID)
		code, traces := filepath.Join(dir, "code"), filepath.Join(dir, "traces")
		if err := os.MkdirAll(code, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(traces, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(code, "main.py"), []byte("def add(a,b): return a+b\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(traces, "session.jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		codeHash, _ := domain.TreeHash(context.Background(), code)
		traceHash, _ := domain.TreeHash(context.Background(), traces)
		c.Captures = append(c.Captures, domain.Capture{ID: captureID, Dir: dir, CodePath: code, TracePath: filepath.Join(traces, "session.jsonl"), Hash: codeHash, TraceHash: traceHash})
		run, _ := pairwiseRun(c.Pairwise, side)
		run.SessionID, run.TurnCount, run.CaptureID, run.CaptureHash, run.TraceHash = "batch-session-"+strings.ToLower(string(side)), 1, captureID, codeHash, traceHash
		run.DeliverableSHA = strings.Repeat(map[domain.PairwiseSide]string{domain.PairwiseSideA: "b", domain.PairwiseSideB: "c"}[side], 40)
		run.DeliverableURL = "https://github.com/example/repo/commit/" + run.DeliverableSHA
	}
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}

	first, err := s.BatchReviewPairwise(context.Background(), PairwiseBatchReviewRequest{ProjectID: "batch"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Reviewed != 1 || first.Reused != 0 || first.Failed != 0 {
		t.Fatalf("first = %#v", first)
	}
	persisted, err := s.loadCase(c.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Pairwise.Language == "" || persisted.Pairwise.Harness != defaultPairwiseHarness || persisted.Pairwise.HarnessVersion != defaultPairwiseHarnessVersion || persisted.Pairwise.OS != defaultPairwiseOS || persisted.Pairwise.Validity != domain.PairwiseValidityValid {
		t.Fatalf("backfilled metadata = %#v", persisted.Pairwise)
	}
	second, err := s.BatchReviewPairwise(context.Background(), PairwiseBatchReviewRequest{ProjectID: "batch"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Reviewed != 0 || second.Reused != 1 || second.Failed != 0 {
		t.Fatalf("second = %#v", second)
	}
	stale, err := s.loadCase(c.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	current := domain.CurrentPairwiseReview(*stale)
	current.SkillHash = stableKey("pairwise-gsb-v3-20260917")
	if _, err := s.store.SaveAnnotationCase(*stale, stale.Revision); err != nil {
		t.Fatal(err)
	}
	third, err := s.BatchReviewPairwise(context.Background(), PairwiseBatchReviewRequest{ProjectID: "batch"})
	if err != nil {
		t.Fatal(err)
	}
	if third.Reviewed != 1 || third.Reused != 0 || third.Failed != 0 {
		t.Fatalf("stale skill review = %#v", third)
	}
}
