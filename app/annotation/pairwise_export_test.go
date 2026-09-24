package annotation

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	domain "github.com/blueship581/pinru/internal/annotation"
)

func pairwiseExportCase(t *testing.T, complete bool) (*AnnotationService, *domain.Case) {
	t.Helper()
	s, _, _ := annotationFixture(t)
	s.verifyPairwiseRemote = func(context.Context, domain.Case) error { return nil }
	c, err := s.EnablePairwise(EnablePairwiseRequest{
		TaskID: "题目-1", Language: "Python", Harness: "Codex CLI", HarnessVersion: "1.2.3", OS: "MacOS/Linux", Environment: "无外部依赖", Validity: domain.PairwiseValidityValid,
	})
	if err != nil {
		t.Fatal(err)
	}
	c.Pairwise.Prompt = "实现加法功能"
	c.SnapshotURL = "https://github.com/example/repo/commit/" + c.InitialSHA
	for side, run := range map[domain.PairwiseSide]*domain.PairwiseRun{
		domain.PairwiseSideA: &c.Pairwise.RunA,
		domain.PairwiseSideB: &c.Pairwise.RunB,
	} {
		label := strings.ToLower(string(side))
		run.SessionID = "session-" + label
		run.TracePath = "/evidence/" + label + "/session.jsonl"
		run.TurnCount = 1
		run.CaptureID = "capture-" + label
		run.ContainerID = "container-" + label
		run.ContainerName = "claude-" + label
		run.WorkspacePath = "/workspace-" + label
		run.RepoRelativePath = "repo"
		run.CaptureHash = "code-hash-" + label
		run.TraceHash = "trace-hash-" + label
		run.DeliverableSHA = strings.Repeat(map[domain.PairwiseSide]string{domain.PairwiseSideA: "a", domain.PairwiseSideB: "b"}[side], 40)
		run.DeliverableURL = "https://github.com/example/repo/commit/" + run.DeliverableSHA
		if complete {
			run.VideoStatus = domain.PairwiseVideoReady
			run.VideoURL = "https://example.com/" + label + ".mp4"
		}
	}
	if complete {
		execution, err := s.reviewExecution()
		if err != nil {
			t.Fatal(err)
		}
		c.Pairwise.Reviews = append(c.Pairwise.Reviews, domain.PairwiseReview{
			ID: "review-1", Status: domain.PairwiseReviewReady, Conclusion: domain.PairwiseConclusionA,
			Reason:             "A 保留了加法返回值并覆盖正常输入；B 删除返回值，调用方无法取得计算结果，因此 A 更完整。",
			ACompletenessScore: 5, ACompletenessDescription: "A 已交付加法返回结果，原始需求的关键功能可以使用。",
			BCompletenessScore: 3, BCompletenessDescription: "B 缺少返回结果，原始需求的调用链仍有功能遗漏。",
			Model:       execution.Label,
			SourceHashA: domain.PairwiseRunSourceHash(c.Pairwise.RunA),
			SourceHashB: domain.PairwiseRunSourceHash(c.Pairwise.RunB),
		})
	}
	saved, err := s.store.SaveAnnotationCase(*c, c.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return s, saved
}

func TestPairwisePreflightRequiresRemoteABBranches(t *testing.T) {
	s, _ := pairwiseExportCase(t, true)
	s.verifyPairwiseRemote = func(context.Context, domain.Case) error {
		return errors.New("远端 A 分支未指向登记的 A 产物")
	}
	report, err := s.PreflightPairwise("batch")
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready != 0 || !strings.Contains(strings.Join(report.Issues, "；"), "远端 A 分支") {
		t.Fatalf("report = %#v", report)
	}
}

func TestPairwiseFormalExportBlocksRemoteABMismatch(t *testing.T) {
	s, c := pairwiseExportCase(t, true)
	s.verifyPairwiseRemote = func(context.Context, domain.Case) error {
		return errors.New("远端 B 分支未指向登记的 B 产物")
	}
	_, err := s.ExportPairwise(PairwiseExportRequest{ProjectID: "batch", TaskIDs: []string{c.TaskID}})
	if err == nil || !strings.Contains(err.Error(), "远端 B 分支") {
		t.Fatalf("error = %v", err)
	}
}

func TestPairwisePreflightAllowsMissingVideosButRequiresReview(t *testing.T) {
	s, _ := pairwiseExportCase(t, false)
	report, err := s.PreflightPairwise("batch")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(report.Issues, "；")
	if strings.Contains(joined, "视频") {
		t.Fatalf("issues %q should not require videos", joined)
	}
	if !strings.Contains(joined, "GSB") {
		t.Fatalf("issues %q do not contain GSB", joined)
	}
	if report.Tasks != 1 || report.Ready != 0 {
		t.Fatalf("report = %#v", report)
	}
}

func TestPairwiseExportWritesOnePairPerRow(t *testing.T) {
	s, c := pairwiseExportCase(t, true)
	result, err := s.ExportPairwise(PairwiseExportRequest{
		ProjectID: "batch", TaskIDs: []string{c.TaskID}, Submitter: "标注员", SubmittedAt: "2026-09-17",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 1 || result.OutputPath == "" || result.ReportPath == "" {
		t.Fatalf("result = %#v", result)
	}
	z, err := zip.OpenReader(result.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	var workbook strings.Builder
	for _, file := range z.File {
		if !strings.HasPrefix(file.Name, "xl/") || !strings.HasSuffix(file.Name, ".xml") {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(&workbook, reader)
		_ = reader.Close()
	}
	for _, want := range []string{"session-a", "session-b", "https://example.com/a.mp4", "https://example.com/b.mp4"} {
		if !strings.Contains(workbook.String(), want) {
			t.Errorf("workbook does not contain %q", want)
		}
	}
}

func TestPairwiseExportAllowsReviewedCaseWithoutVideos(t *testing.T) {
	s, c := pairwiseExportCase(t, true)
	c.Pairwise.RunA.VideoStatus = domain.PairwiseVideoMissing
	c.Pairwise.RunA.VideoURL = ""
	c.Pairwise.RunB.VideoStatus = domain.PairwiseVideoMissing
	c.Pairwise.RunB.VideoURL = ""
	review := &c.Pairwise.Reviews[len(c.Pairwise.Reviews)-1]
	review.SourceHashA = domain.PairwiseRunSourceHash(c.Pairwise.RunA)
	review.SourceHashB = domain.PairwiseRunSourceHash(c.Pairwise.RunB)
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}

	result, err := s.ExportPairwise(PairwiseExportRequest{ProjectID: "batch", TaskIDs: []string{c.TaskID}, Submitter: "标注员"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 1 {
		t.Fatalf("rows = %d", result.Rows)
	}
}

func TestPairwiseExportSkipsReviewWithoutReason(t *testing.T) {
	s, c := pairwiseExportCase(t, true)
	c.Pairwise.Reviews[len(c.Pairwise.Reviews)-1].Reason = ""
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}

	_, err := s.ExportPairwise(PairwiseExportRequest{ProjectID: "batch", TaskIDs: []string{c.TaskID}})
	if err == nil || !strings.Contains(err.Error(), "没有已完成 GSB 审核且理由完整的题目") {
		t.Fatalf("error = %v", err)
	}
}

func TestPairwiseExportSkipsReviewWithDecorativeQuoteBrackets(t *testing.T) {
	s, c := pairwiseExportCase(t, true)
	c.Pairwise.Reviews[len(c.Pairwise.Reviews)-1].Reason = "A 完成了『筛选功能』并运行测试，B 的页面仍返回未过滤数据，因此 A 更完整。"
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}

	_, err := s.ExportPairwise(PairwiseExportRequest{ProjectID: "batch", TaskIDs: []string{c.TaskID}})
	if err == nil || !strings.Contains(err.Error(), "装饰引号") {
		t.Fatalf("error = %v", err)
	}
}

func TestPairwiseExportRequiresSelectedTaskIDs(t *testing.T) {
	s, _ := pairwiseExportCase(t, true)

	_, err := s.ExportPairwise(PairwiseExportRequest{ProjectID: "batch"})
	if err == nil || !strings.Contains(err.Error(), "至少选择一道") {
		t.Fatalf("error = %v", err)
	}
}
