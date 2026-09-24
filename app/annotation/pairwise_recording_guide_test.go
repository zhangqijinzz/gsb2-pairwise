package annotation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	domain "github.com/blueship581/pinru/internal/annotation"
)

func TestGeneratePairwiseRecordingGuideStoresSideSpecificSteps(t *testing.T) {
	s, _, _ := annotationFixture(t)
	cli, _ := fakeReviewCLIWithEvaluation(t, map[string]any{
		"steps": []string{"打开项目首页并等待内容出现", "点击皱眉榜切换排行榜", "打开第一条记录查看详情", "关闭详情并返回列表"},
	}, "")
	s.cli = cli
	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1"})
	if err != nil {
		t.Fatal(err)
	}
	c.Pairwise.Prompt = "增加皱眉榜并支持查看记录详情"
	dir := filepath.Join(s.caseDir(c.TaskID), "guide-fixture")
	code, traces := filepath.Join(dir, "code"), filepath.Join(dir, "traces")
	if err := os.MkdirAll(code, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(traces, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(code, "App.tsx"), []byte("export const App = () => '皱眉榜'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(traces, "session.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	codeHash, _ := domain.TreeHash(context.Background(), code)
	traceHash, _ := domain.TreeHash(context.Background(), traces)
	capture := domain.Capture{ID: "capture-a", Dir: dir, CodePath: code, TracePath: filepath.Join(traces, "session.jsonl"), Hash: codeHash, TraceHash: traceHash}
	c.Captures = append(c.Captures, capture)
	c.Pairwise.RunA.CaptureID = capture.ID
	c.Pairwise.RunA.CaptureHash = codeHash
	c.Pairwise.RunA.TraceHash = traceHash
	c.Pairwise.RunA.DeliverableSHA = strings.Repeat("a", 40)
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}

	updated, err := s.GeneratePairwiseRecordingGuide(context.Background(), PairwiseSideRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Pairwise.RunA.RecordingGuide) != 4 || updated.Pairwise.RunA.RecordingGuide[1] != "点击皱眉榜切换排行榜" {
		t.Fatalf("guide = %#v", updated.Pairwise.RunA.RecordingGuide)
	}
	if updated.Pairwise.RunA.RecordingGuideHash == "" || updated.Pairwise.RunA.RecordingGuideGeneratedAt == 0 {
		t.Fatalf("guide metadata = %#v", updated.Pairwise.RunA)
	}
}
