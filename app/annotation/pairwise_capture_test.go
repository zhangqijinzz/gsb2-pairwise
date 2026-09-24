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

func TestCapturePairwiseSideStoresOneTurnWithoutReplacingOtherSide(t *testing.T) {
	s, trace, source := annotationFixture(t)
	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1", Language: "Python", Harness: "Codex CLI", HarnessVersion: "1.0.0", OS: "MacOS/Linux", Validity: domain.PairwiseValidityValid})
	if err != nil {
		t.Fatal(err)
	}
	c.SnapshotURL = "https://github.com/example/repo/commit/" + c.InitialSHA
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}
	s.pushPairwise = func(context.Context, string, string, string) error { return nil }

	if _, err := s.PreparePairwiseSide(context.Background(), PairwiseSideRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	traceA := filepath.Join(filepath.Dir(trace), "session-a.jsonl")
	if err := os.WriteFile(traceA, []byte(strings.ReplaceAll(string(raw), `"session"`, `"session-a"`)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "a.txt"), []byte("A result"), 0o600); err != nil {
		t.Fatal(err)
	}
	capturedA, err := s.CapturePairwiseSide(context.Background(), PairwiseCaptureRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA, TracePath: traceA})
	if err != nil {
		t.Fatal(err)
	}
	aCapture := capturedA.Pairwise.RunA.CaptureID
	if aCapture == "" || capturedA.Pairwise.RunA.SessionID != "session-a" || capturedA.Pairwise.RunA.TurnCount != 1 {
		t.Fatalf("captured A = %#v", capturedA.Pairwise.RunA)
	}
	if _, err := s.CommitPairwiseSide(context.Background(), PairwiseCommitRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA, SessionID: "session-a"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PreparePairwiseSide(context.Background(), PairwiseSideRequest{TaskID: c.TaskID, Side: domain.PairwiseSideB}); err != nil {
		t.Fatal(err)
	}
	traceB := filepath.Join(filepath.Dir(trace), "session-b.jsonl")
	if err := os.WriteFile(traceB, []byte(strings.ReplaceAll(string(raw), `"session"`, `"session-b"`)), 0o600); err != nil {
		t.Fatal(err)
	}
	capturedB, err := s.CapturePairwiseSide(context.Background(), PairwiseCaptureRequest{TaskID: c.TaskID, Side: domain.PairwiseSideB, TracePath: traceB})
	if err != nil {
		t.Fatal(err)
	}
	if capturedB.Pairwise.RunA.CaptureID != aCapture || capturedB.Pairwise.RunB.CaptureID == "" {
		t.Fatalf("captures after B = A:%#v B:%#v", capturedB.Pairwise.RunA, capturedB.Pairwise.RunB)
	}
}

func TestCapturePairwiseSideRejectsDuplicateSessionAndMultipleTurns(t *testing.T) {
	s, trace, source := annotationFixture(t)
	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1", Language: "Python", Harness: "Codex CLI", HarnessVersion: "1", OS: "MacOS/Linux", Validity: domain.PairwiseValidityValid})
	if err != nil {
		t.Fatal(err)
	}
	c.Pairwise.RunA.SessionID = "session"
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CapturePairwiseSide(context.Background(), PairwiseCaptureRequest{TaskID: c.TaskID, Side: domain.PairwiseSideB, TracePath: trace}); err == nil || !strings.Contains(err.Error(), "SessionID") {
		t.Fatalf("duplicate session error = %v", err)
	}
	multi := filepath.Join(filepath.Dir(trace), "multi.jsonl")
	writeFixtureTrace(t, multi, source, 2)
	loaded, _ := s.loadCase(c.TaskID)
	loaded.Pairwise.RunA.SessionID = ""
	if _, err := s.store.SaveAnnotationCase(*loaded, loaded.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CapturePairwiseSide(context.Background(), PairwiseCaptureRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA, TracePath: multi}); err == nil || !strings.Contains(err.Error(), "一轮") {
		t.Fatalf("multi-turn error = %v", err)
	}
}

func TestCaptureAndCommitPairwiseSidePublishesCapturedResult(t *testing.T) {
	s, trace, source := annotationFixture(t)
	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1"})
	if err != nil {
		t.Fatal(err)
	}
	c.SnapshotURL = "https://github.com/example/repo/commit/" + c.InitialSHA
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}
	s.pushPairwise = func(context.Context, string, string, string) error { return nil }
	if _, err := s.PreparePairwiseSide(context.Background(), PairwiseSideRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "result.txt"), []byte("captured result"), 0o600); err != nil {
		t.Fatal(err)
	}

	updated, err := s.CaptureAndCommitPairwiseSide(context.Background(), PairwiseCaptureRequest{
		TaskID: c.TaskID, Side: domain.PairwiseSideA, TracePath: trace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Pairwise.RunA.SessionID != "session" || len(updated.Pairwise.RunA.DeliverableSHA) != 40 || updated.Pairwise.RunA.CommittedAt == 0 {
		t.Fatalf("captured and committed A = %#v", updated.Pairwise.RunA)
	}
}

func TestCaptureAndCommitPairwiseSideKeepsCaptureWhenPushFailsAndCanRetry(t *testing.T) {
	s, trace, source := annotationFixture(t)
	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1"})
	if err != nil {
		t.Fatal(err)
	}
	c.SnapshotURL = "https://github.com/example/repo/commit/" + c.InitialSHA
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreparePairwiseSide(context.Background(), PairwiseSideRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "result.txt"), []byte("captured result"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.pushPairwise = func(context.Context, string, string, string) error { return errors.New("push unavailable") }

	if _, err := s.CaptureAndCommitPairwiseSide(context.Background(), PairwiseCaptureRequest{
		TaskID: c.TaskID, Side: domain.PairwiseSideA, TracePath: trace,
	}); err == nil || !strings.Contains(err.Error(), "push unavailable") {
		t.Fatalf("push error = %v", err)
	}
	failed, err := s.loadCase(c.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Pairwise.RunA.CaptureID == "" || failed.Pairwise.RunA.SessionID != "session" || failed.Pairwise.RunA.DeliverableSHA != "" {
		t.Fatalf("capture after failed push = %#v", failed.Pairwise.RunA)
	}

	s.pushPairwise = func(context.Context, string, string, string) error { return nil }
	retried, err := s.CommitPairwiseSide(context.Background(), PairwiseCommitRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA, SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	if len(retried.Pairwise.RunA.DeliverableSHA) != 40 {
		t.Fatalf("retried commit = %#v", retried.Pairwise.RunA)
	}
}

func TestSelectPairwiseTraceFindsUniqueMatchingSession(t *testing.T) {
	_, trace, _ := annotationFixture(t)
	raw, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	matching := []byte(strings.ReplaceAll(string(raw), filepath.Dir(trace)+"/source", "/workspace/repo"))
	unrelated := []byte(strings.ReplaceAll(string(matching), "实现加法功能", "修改无关功能"))
	files := map[string][]byte{
		"projects/-workspace-repo/session-a.jsonl":  matching,
		"projects/-workspace-other/session-x.jsonl": unrelated,
	}
	binding := &domain.Case{ContainerID: "container-a", RepoRelativePath: "repo"}

	selected, err := selectPairwiseTrace(files, binding, "/host/repo", "实现加法功能")
	if err != nil {
		t.Fatal(err)
	}
	if selected.main != "projects/-workspace-repo/session-a.jsonl" || selected.tracePath != containerTraceRoot+"/-workspace-repo/session-a.jsonl" {
		t.Fatalf("selection = %#v", selected)
	}
	if len(selected.rounds) != 1 || selected.rounds[0].SessionID != "session" {
		t.Fatalf("rounds = %#v", selected.rounds)
	}
}

func TestSelectPairwiseTraceRejectsMissingAndAmbiguousMatches(t *testing.T) {
	_, trace, _ := annotationFixture(t)
	raw, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	matching := []byte(strings.ReplaceAll(string(raw), filepath.Dir(trace)+"/source", "/workspace/repo"))
	binding := &domain.Case{ContainerID: "container-a", RepoRelativePath: "repo"}

	if _, err := selectPairwiseTrace(map[string][]byte{
		"projects/-workspace-repo/unrelated.jsonl": []byte(strings.ReplaceAll(string(matching), "实现加法功能", "修改无关功能")),
	}, binding, "/host/repo", "实现加法功能"); err == nil || !strings.Contains(err.Error(), "未找到") {
		t.Fatalf("missing match error = %v", err)
	}

	second := []byte(strings.ReplaceAll(string(matching), `"session"`, `"session-b"`))
	if _, err := selectPairwiseTrace(map[string][]byte{
		"projects/-workspace-repo/session-a.jsonl": matching,
		"projects/-workspace-repo/session-b.jsonl": second,
	}, binding, "/host/repo", "实现加法功能"); err == nil || !strings.Contains(err.Error(), "多条") {
		t.Fatalf("ambiguous match error = %v", err)
	}
}

func TestNormalizePairwiseVideoSourceKeepsRealApostrophes(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"plain path", "  /tmp/a.mp4 ", "/tmp/a.mp4"},
		{"single quoted path", "'/tmp/a b.mov'", "/tmp/a b.mov"},
		{"double quoted path", `"/tmp/a.mov"`, "/tmp/a.mov"},
		{"quoted url", "'https://example.com/a.mp4'", "https://example.com/a.mp4"},
		{"nested quotes", `"'/tmp/a.mov'"`, "/tmp/a.mov"},
		{"shell escaped apostrophe", `'/tmp/it'\''s.mov'`, "/tmp/it's.mov"},
		{"apostrophe inside name", "/tmp/it's.mov", "/tmp/it's.mov"},
		{"unbalanced quote", "/tmp/it's.mov'", "/tmp/it's.mov'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizePairwiseVideoSource(tc.raw); got != tc.want {
				t.Fatalf("normalize(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestSavePairwiseMaterialsAcceptsQuotedVideoPathAndURL(t *testing.T) {
	s, _, _ := annotationFixture(t)
	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1", Language: "Python", Harness: "Codex CLI", HarnessVersion: "1", OS: "MacOS/Linux", Validity: domain.PairwiseValidityValid})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "it's demo.mov")
	if err := os.WriteFile(video, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	updated, err := s.SavePairwiseMaterials(PairwiseMaterialsRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA, VideoPath: "'" + video + "'"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Pairwise.RunA.VideoStatus != domain.PairwiseVideoReady || updated.Pairwise.RunA.VideoPath != video {
		t.Fatalf("video state = %#v", updated.Pairwise.RunA)
	}
	// A quoted HTTP(S) link arrives in the path field and must still be stored as a URL.
	updated, err = s.SavePairwiseMaterials(PairwiseMaterialsRequest{TaskID: c.TaskID, Side: domain.PairwiseSideB, VideoPath: "'https://example.com/b.mp4'"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Pairwise.RunB.VideoURL != "https://example.com/b.mp4" || updated.Pairwise.RunB.VideoPath != "" {
		t.Fatalf("video state = %#v", updated.Pairwise.RunB)
	}
}

func TestSavePairwiseMaterialsKeepsFileWhoseNameIsWrappedInQuotes(t *testing.T) {
	s, _, _ := annotationFixture(t)
	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1", Language: "Python", Harness: "Codex CLI", HarnessVersion: "1", OS: "MacOS/Linux", Validity: domain.PairwiseValidityValid})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "'wrapped'.mp4")
	if err := os.WriteFile(video, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	updated, err := s.SavePairwiseMaterials(PairwiseMaterialsRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA, VideoPath: video})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Pairwise.RunA.VideoPath != video {
		t.Fatalf("video path = %q, want %q", updated.Pairwise.RunA.VideoPath, video)
	}
}

func TestSavePairwiseMaterialsReportsWhyTheVideoWasRejected(t *testing.T) {
	s, _, _ := annotationFixture(t)
	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1", Language: "Python", Harness: "Codex CLI", HarnessVersion: "1", OS: "MacOS/Linux", Validity: domain.PairwiseValidityValid})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	text := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(text, []byte("not a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePairwiseMaterials(PairwiseMaterialsRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA, VideoPath: filepath.Join(dir, "missing.mov")}); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("missing file error = %v", err)
	}
	if _, err := s.SavePairwiseMaterials(PairwiseMaterialsRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA, VideoPath: text}); err == nil || !strings.Contains(err.Error(), "格式") {
		t.Fatalf("wrong extension error = %v", err)
	}
	missing := filepath.Join(dir, "missing.mov")
	if _, err := s.SavePairwiseMaterials(PairwiseMaterialsRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA, VideoPath: "'" + missing + "'"}); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("quoted missing file error = %v", err)
	}
}

func TestSavePairwiseMaterialsRequiresHTTPVideoURL(t *testing.T) {
	s, _, _ := annotationFixture(t)
	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1", Language: "Python", Harness: "Codex CLI", HarnessVersion: "1", OS: "MacOS/Linux", Validity: domain.PairwiseValidityValid})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePairwiseMaterials(PairwiseMaterialsRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA, VideoURL: "file:///tmp/a.mp4"}); err == nil {
		t.Fatal("accepted local file URL")
	}
	updated, err := s.SavePairwiseMaterials(PairwiseMaterialsRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA, VideoURL: "https://example.com/a.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Pairwise.RunA.VideoStatus != domain.PairwiseVideoReady || updated.Pairwise.RunA.VideoURL != "https://example.com/a.mp4" {
		t.Fatalf("video state = %#v", updated.Pairwise.RunA)
	}
}

func TestSavePairwiseSettingsBackfillsSubmissionFields(t *testing.T) {
	s, _, _ := annotationFixture(t)
	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1", Language: "Python", Harness: "Codex CLI", HarnessVersion: "1", OS: "MacOS/Linux", Validity: domain.PairwiseValidityValid})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.SavePairwiseSettings(PairwiseSettingsRequest{
		TaskID: c.TaskID, Language: "TypeScript / React", Environment: "无外部依赖", Validity: domain.PairwiseValidityOther, Notes: "环境异常",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Pairwise.Language != "TypeScript / React" || updated.Pairwise.Environment != "无外部依赖" || updated.Pairwise.Validity != domain.PairwiseValidityOther || updated.Pairwise.Notes != "环境异常" {
		t.Fatalf("settings = %+v", updated.Pairwise)
	}
}

func TestEnablePairwiseAutomaticallyStoresSubmissionMetadata(t *testing.T) {
	s, _, source := annotationFixture(t)
	if err := os.WriteFile(filepath.Join(source, "package.json"), []byte(`{"dependencies":{"react":"latest"},"devDependencies":{"typescript":"latest"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "tsconfig.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Pairwise.Language, "TypeScript") || !strings.Contains(c.Pairwise.Language, "React") {
		t.Fatalf("language = %q", c.Pairwise.Language)
	}
	if c.Pairwise.Harness != "Claude Code" || c.Pairwise.HarnessVersion == "" || c.Pairwise.OS != "MacOS/Linux" {
		t.Fatalf("fixed metadata = %+v", c.Pairwise)
	}
	if c.Pairwise.Environment != "已容器化，可一键起环境" || c.Pairwise.Validity != domain.PairwiseValidityValid {
		t.Fatalf("defaults = %+v", c.Pairwise)
	}
}
