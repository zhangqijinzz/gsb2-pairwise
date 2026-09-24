package annotation

import (
	"os"
	"strings"
	"testing"

	"github.com/blueship581/pinru/internal/store"
)

func TestDeepSeekReviewProviderChangeKeepsCacheUntilReviewIsForced(t *testing.T) {
	s, trace, _ := annotationFixture(t)
	cli, calls := fakeReviewCLI(t)
	s.cli = cli
	if _, err := s.Capture(CaptureRequest{TaskID: "题目-1", TracePath: trace}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Review(ReviewRequest{TaskID: "题目-1", PromptID: "p1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Review(ReviewRequest{TaskID: "题目-1", PromptID: "p1"}); err != nil {
		t.Fatal(err)
	}
	assertReviewCallCount(t, calls, 1)

	provider, err := s.store.GetLLMProvider("deepseek-test")
	if err != nil || provider == nil {
		t.Fatalf("GetLLMProvider() = %+v, %v", provider, err)
	}
	provider.APIKey = "rotated-test-key"
	if err := s.store.UpdateLLMProvider(*provider); err != nil {
		t.Fatal(err)
	}
	cases, err := s.ListCases("batch")
	if err != nil {
		t.Fatal(err)
	}
	if current := cases[0].Rounds[0].Evaluations[0].Current; current == nil || !*current {
		t.Fatalf("saved table data was discarded after API key rotation: current = %v", current)
	}
	if _, err := s.Review(ReviewRequest{TaskID: "题目-1", PromptID: "p1", Force: true}); err != nil {
		t.Fatal(err)
	}
	assertReviewCallCount(t, calls, 2)
}

func TestReviewProviderUsesDefaultDeepSeekAPI(t *testing.T) {
	s, _, _ := annotationFixture(t)
	selection, err := s.reviewExecution()
	if err != nil {
		t.Fatal(err)
	}
	provider, label := selection.DeepSeek, selection.Label
	if provider == nil {
		t.Fatal("review execution did not select DeepSeek")
	}
	if provider.Model != "deepseek-v4-flash" || provider.APIKey != "test-key" || provider.BaseURL != "https://api.deepseek.com" || provider.ReasoningEffort != "high" {
		t.Fatalf("review provider = %+v", provider)
	}
	if !strings.Contains(label, "DeepSeek V4 Flash") || strings.Contains(label, "test-key") {
		t.Fatalf("review label = %q", label)
	}
}

func TestReviewProviderPrefersDefaultDeepSeekAPI(t *testing.T) {
	s, _, _ := annotationFixture(t)
	url := "https://api.deepseek.com/v1"
	if err := s.store.CreateLLMProvider(store.LLMProvider{ID: "deepseek-other", Name: "Other Flash", ProviderType: "openai_compatible", Model: "deepseek-flash", BaseURL: &url, APIKey: "other-key"}); err != nil {
		t.Fatal(err)
	}
	selection, err := s.reviewExecution()
	if err != nil {
		t.Fatal(err)
	}
	provider := selection.DeepSeek
	if provider == nil {
		t.Fatal("review execution did not select DeepSeek")
	}
	if provider.APIKey != "test-key" {
		t.Fatalf("selected API key identifies the wrong provider")
	}
}

func TestReviewProviderRejectsMissingDeepSeekAPIConfiguration(t *testing.T) {
	s, _, _ := annotationFixture(t)
	if err := s.store.DeleteLLMProvider("deepseek-test"); err != nil {
		t.Fatal(err)
	}
	_, err := s.reviewExecution()
	if err == nil || !strings.Contains(err.Error(), "DeepSeek V4 Flash API") {
		t.Fatalf("reviewProvider error = %v", err)
	}
}

func TestReviewExecutionUsesLocalCodexWhenGloballySelected(t *testing.T) {
	s, _, _ := annotationFixture(t)
	if err := s.store.SetConfig("annotation_review_engine", "codex"); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetConfig("annotation_review_model", "gpt-5.5"); err != nil {
		t.Fatal(err)
	}

	selection, err := s.reviewExecution()
	if err != nil {
		t.Fatal(err)
	}
	if selection.DeepSeek != nil || selection.Model != "gpt-5.5" || !strings.Contains(selection.Label, "Codex CLI") {
		t.Fatalf("review execution = %+v, want local Codex CLI", selection)
	}
}

func assertReviewCallCount(t *testing.T, path string, want int) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, value := range raw {
		if value == '\n' {
			count++
		}
	}
	if count != want {
		t.Fatalf("review calls = %d, want %d", count, want)
	}
}
