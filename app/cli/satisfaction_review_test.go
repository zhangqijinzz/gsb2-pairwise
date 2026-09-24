package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	annotation "github.com/blueship581/pinru/internal/annotation"
)

func TestSatisfactionPromptKeepsFiveDimensionRulesAndEvidenceBoundaries(t *testing.T) {
	p := buildSatisfactionPrompt(SatisfactionReviewRequest{InputPath: "/review/input.json", SkillDir: "/review/skill"})
	for _, want := range []string{"integration-review-profile.md", "input.json", "evidence-index.json", "round-trace.jsonl", "五维", "副本", "语义高度相似", "未写完的句子", "必要技术引用", "ASCII 箭头", "修复提示词也"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"90 分", "过程不满意：", "最多三句"} {
		if strings.Contains(p, bad) {
			t.Errorf("legacy rule %q leaked", bad)
		}
	}
}

func TestSatisfactionSchemaRequiresExactScoreAndDescriptionCounts(t *testing.T) {
	schema := satisfactionSchema()
	props := schema["properties"].(map[string]any)
	for _, name := range []string{"scores", "descriptions"} {
		p := props[name].(map[string]any)
		if p["minItems"] != 5 || p["maxItems"] != 5 {
			t.Fatalf("%s not exactly five", name)
		}
	}
	checks := props["descriptionChecks"].(map[string]any)
	if checks["minItems"] != 5 || checks["maxItems"] != 5 {
		t.Fatalf("descriptionChecks not exactly five")
	}
	for _, name := range []string{"taskType", "difficulty", "environment", "os"} {
		if _, ok := props[name].(map[string]any)["enum"]; !ok {
			t.Fatalf("%s must use a strict enum", name)
		}
	}
	requirementChecks := props["requirementChecks"].(map[string]any)
	if requirementChecks["minItems"] != 1 {
		t.Fatalf("requirementChecks minItems = %#v, want 1", requirementChecks["minItems"])
	}
}

func TestSatisfactionSchemaRestrictsScoresToThreeThroughFive(t *testing.T) {
	schema := satisfactionSchema()
	props := schema["properties"].(map[string]any)
	scores := props["scores"].(map[string]any)
	items := scores["items"].(map[string]any)
	if items["minimum"] != 3 || items["maximum"] != 5 {
		t.Fatalf("score bounds = %#v..%#v, want 3..5", items["minimum"], items["maximum"])
	}
}

func TestRunSatisfactionReviewAutomaticallyRechecksScoreTotalAboveTwentyOne(t *testing.T) {
	workDir := t.TempDir()
	first := reviewJSONWithScores(t, [5]int{5, 5, 5, 5, 5})
	second := reviewJSONWithScores(t, [5]int{5, 4, 4, 4, 4})
	counter := filepath.Join(t.TempDir(), "attempts")
	binary := writeFakeCodex(t, fakeCodexWritesReviewSequence(t, counter, first, second))
	service := NewWithResolver(func(string) (string, error) { return binary, nil })

	evaluation, err := service.RunSatisfactionReview(context.Background(), SatisfactionReviewRequest{
		WorkDir: workDir, SkillDir: filepath.Join(workDir, "skill"), InputPath: filepath.Join(workDir, "input.json"),
	}, nil)
	if err != nil {
		t.Fatalf("RunSatisfactionReview() error = %v", err)
	}
	total, complete := annotationScoreTotal(evaluation)
	if !complete || total != 21 {
		t.Fatalf("corrected score total = %d, complete=%v, want 21", total, complete)
	}
	attempts, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(attempts)) != "2" {
		t.Fatalf("attempt count = %q, want 2", attempts)
	}
}

func TestRunSatisfactionReviewRewritesPlatformRateLimitDeduction(t *testing.T) {
	workDir := t.TempDir()
	first := reviewJSONWithDescription(t, 0, "本轮因 429 RateLimitError 中断，最后只有领域类型和一行导出，所以交付不完整。")
	second := reviewJSONWithDescription(t, 0, "轮末只有领域类型和一行导出，原提示词要求的完整导出流程仍未实现，现有产物无法完成用户要求的数据交付。")
	counter := filepath.Join(t.TempDir(), "attempts")
	binary := writeFakeCodex(t, fakeCodexWritesReviewSequence(t, counter, first, second))
	service := NewWithResolver(func(string) (string, error) { return binary, nil })

	evaluation, err := service.RunSatisfactionReview(context.Background(), SatisfactionReviewRequest{
		WorkDir: workDir, SkillDir: filepath.Join(workDir, "skill"), InputPath: filepath.Join(workDir, "input.json"),
	}, nil)
	if err != nil {
		t.Fatalf("RunSatisfactionReview() error = %v", err)
	}
	if strings.Contains(evaluation.Descriptions[0], "429") || !strings.Contains(evaluation.Descriptions[0], "完整导出流程仍未实现") {
		t.Fatalf("corrected delivery description = %q", evaluation.Descriptions[0])
	}
	attempts, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(attempts)) != "2" {
		t.Fatalf("attempt count = %q, want 2", attempts)
	}
}

func TestSatisfactionSchemaRestrictsOperatingSystemToExportValues(t *testing.T) {
	schema := satisfactionSchema()
	props := schema["properties"].(map[string]any)
	osSchema := props["os"].(map[string]any)
	values, ok := osSchema["enum"].([]string)
	if !ok {
		t.Fatalf("os enum = %#v, want []string", osSchema["enum"])
	}
	want := []string{"", "MacOS/Linux", "Windows"}
	if strings.Join(values, "|") != strings.Join(want, "|") {
		t.Fatalf("os enum = %#v, want %#v", values, want)
	}
}

func TestRunSatisfactionReviewRejectsEmptyRequirementChecks(t *testing.T) {
	workDir := t.TempDir()
	payload := validReviewJSON(t, 5)
	var document map[string]any
	if err := json.Unmarshal([]byte(payload), &document); err != nil {
		t.Fatal(err)
	}
	document["requirementChecks"] = []any{}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	binary := writeFakeCodex(t, fakeCodexWritesReview(t, string(raw), "", ""))
	service := NewWithResolver(func(string) (string, error) { return binary, nil })
	if evaluation, err := service.RunSatisfactionReview(context.Background(), SatisfactionReviewRequest{
		WorkDir: workDir, SkillDir: filepath.Join(workDir, "skill"), InputPath: filepath.Join(workDir, "input.json"),
	}, nil); err == nil || !strings.Contains(err.Error(), "逐项需求核验") {
		t.Fatalf("RunSatisfactionReview() = %#v, %v; want empty requirement checks rejection", evaluation, err)
	}
}

func TestRunSatisfactionReviewAcceptsExactlyFiveItemsAndRejectsFourOrSix(t *testing.T) {
	for _, count := range []int{5, 4, 6} {
		t.Run(fmt.Sprintf("count_%d", count), func(t *testing.T) {
			workDir := t.TempDir()
			binary := writeFakeCodex(t, fakeCodexWritesReview(t, validReviewJSON(t, count), "", ""))
			service := NewWithResolver(func(string) (string, error) { return binary, nil })

			evaluation, err := service.RunSatisfactionReview(context.Background(), SatisfactionReviewRequest{
				WorkDir: workDir, SkillDir: filepath.Join(workDir, "skill"), InputPath: filepath.Join(workDir, "input.json"),
			}, nil)
			if count == 5 {
				if err != nil {
					t.Fatalf("RunSatisfactionReview() error = %v", err)
				}
				if evaluation == nil || evaluation.Scores[4] == nil || *evaluation.Scores[4] != 4 || evaluation.Descriptions[4] != "dimension 5" {
					t.Fatalf("evaluation = %#v", evaluation)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "恰好包含五项") {
				t.Fatalf("RunSatisfactionReview() error = %v, want exact-count rejection", err)
			}
		})
	}
}

func TestRunSatisfactionReviewCancellationReturnsWithoutHanging(t *testing.T) {
	workDir := t.TempDir()
	binary := writeFakeCodex(t, "exec sleep 30\n")
	service := NewWithResolver(func(string) (string, error) { return binary, nil })
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	started := time.Now()
	_, err := service.RunSatisfactionReview(ctx, SatisfactionReviewRequest{
		WorkDir: workDir, SkillDir: filepath.Join(workDir, "skill"), InputPath: filepath.Join(workDir, "input.json"),
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunSatisfactionReview() error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("cancellation took %v, runner appears hung", elapsed)
	}
}

func TestRunSatisfactionReviewPreservesEvaluatorToolAndTestOutput(t *testing.T) {
	workDir := t.TempDir()
	binary := writeFakeCodex(t, fakeCodexWritesReview(
		t, validReviewJSON(t, 5),
		`{"type":"item.completed","item":{"type":"command_execution","command":"go test ./...","aggregated_output":"PASS"}}`,
		"verification stderr: integration test passed",
	))
	service := NewWithResolver(func(string) (string, error) { return binary, nil })
	if _, err := service.RunSatisfactionReview(context.Background(), SatisfactionReviewRequest{
		WorkDir: workDir, SkillDir: filepath.Join(workDir, "skill"), InputPath: filepath.Join(workDir, "input.json"),
	}, nil); err != nil {
		t.Fatalf("RunSatisfactionReview() error = %v", err)
	}
	logData, err := os.ReadFile(filepath.Join(workDir, "evaluator.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"command_execution", "go test ./...", "PASS", "verification stderr", "integration test passed"} {
		if !strings.Contains(string(logData), want) {
			t.Fatalf("evaluator.log = %q, missing %q", logData, want)
		}
	}
}

func TestRunSatisfactionReviewRejectsTrailingDocumentAfterJSON(t *testing.T) {
	workDir := t.TempDir()
	payload := validReviewJSON(t, 5) + "\n# evaluator notes must not be accepted\n"
	binary := writeFakeCodex(t, fakeCodexWritesReview(t, payload, "", ""))
	service := NewWithResolver(func(string) (string, error) { return binary, nil })
	if evaluation, err := service.RunSatisfactionReview(context.Background(), SatisfactionReviewRequest{
		WorkDir: workDir, SkillDir: filepath.Join(workDir, "skill"), InputPath: filepath.Join(workDir, "input.json"),
	}, nil); err == nil {
		t.Fatalf("RunSatisfactionReview() = %#v, nil; want trailing-content rejection", evaluation)
	}
}

func TestRunSatisfactionReviewAcceptsSingleJSONCodeFence(t *testing.T) {
	workDir := t.TempDir()
	payload := "```json\n" + validReviewJSON(t, 5) + "\n```\n"
	binary := writeFakeCodex(t, fakeCodexWritesReview(t, payload, "", ""))
	service := NewWithResolver(func(string) (string, error) { return binary, nil })
	evaluation, err := service.RunSatisfactionReview(context.Background(), SatisfactionReviewRequest{
		WorkDir: workDir, SkillDir: filepath.Join(workDir, "skill"), InputPath: filepath.Join(workDir, "input.json"),
	}, nil)
	if err != nil {
		t.Fatalf("RunSatisfactionReview() error = %v", err)
	}
	if evaluation == nil || evaluation.Status != "ready" || evaluation.Scores[4] == nil || *evaluation.Scores[4] != 4 {
		t.Fatalf("evaluation = %#v", evaluation)
	}
}

func TestRunSatisfactionReviewReportsMalformedJSONBeforeShapeErrors(t *testing.T) {
	workDir := t.TempDir()
	binary := writeFakeCodex(t, fakeCodexWritesReview(t, "```json\n{broken}\n```\n", "", ""))
	service := NewWithResolver(func(string) (string, error) { return binary, nil })
	_, err := service.RunSatisfactionReview(context.Background(), SatisfactionReviewRequest{
		WorkDir: workDir, SkillDir: filepath.Join(workDir, "skill"), InputPath: filepath.Join(workDir, "input.json"),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "评分 JSON 无效") || strings.Contains(err.Error(), "恰好包含五项") {
		t.Fatalf("RunSatisfactionReview() error = %v, want an accurate JSON parse error", err)
	}
}

func TestDeepSeekCodexConfigUsesIsolatedHomeAndDoesNotExposeKeyInArgs(t *testing.T) {
	home, cleanup, err := prepareDeepSeekCodexHome(DeepSeekCodexConfig{
		Model: "deepseek-v4-flash", BaseURL: "https://api.deepseek.com", APIKey: "secret-key", ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	config, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	models, err := os.ReadFile(filepath.Join(home, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`model = "deepseek-v4-flash"`, `model_provider = "deepseek"`, `wire_api = "responses"`, `model_reasoning_effort = "high"`, `experimental_bearer_token = "secret-key"`} {
		if !strings.Contains(string(config), want) {
			t.Fatalf("config.toml missing %q: %s", want, config)
		}
	}
	if !strings.Contains(string(models), `"slug": "deepseek-v4-flash"`) {
		t.Fatalf("models.json = %s", models)
	}
}

func TestDeepSeekCodexConfigIsAcceptedByInstalledCodex(t *testing.T) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex CLI is not installed")
	}
	requestSeen := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case requestSeen <- struct{}{}:
		default:
		}
		http.Error(w, `{"error":{"message":"test endpoint"}}`, http.StatusUnauthorized)
	}))
	defer server.Close()
	home, cleanup, err := prepareDeepSeekCodexHome(DeepSeekCodexConfig{Model: "deepseek-v4-flash", BaseURL: server.URL, APIKey: "test-key", ReasoningEffort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "exec", "-", "--skip-git-repo-check", "--ephemeral", "--json")
	cmd.Dir = t.TempDir()
	cmd.Env = applyEnvOverrides(os.Environ(), map[string]string{"CODEX_HOME": home})
	cmd.Stdin = strings.NewReader("reply with ok")
	out, _ := cmd.CombinedOutput()
	select {
	case <-requestSeen:
	case <-ctx.Done():
		t.Fatalf("Codex did not reach the configured endpoint: %s", out)
	}
	for _, bad := range []string{"model catalog", "models.json", "config.toml parse", "unknown field"} {
		if strings.Contains(strings.ToLower(string(out)), strings.ToLower(bad)) {
			t.Fatalf("Codex rejected generated configuration: %s", out)
		}
	}
}

func validReviewJSON(t *testing.T, count int) string {
	t.Helper()
	scores := make([]int, count)
	descriptions := make([]string, count)
	for index := 0; index < count; index++ {
		scores[index] = 4
		descriptions[index] = fmt.Sprintf("dimension %d", index+1)
	}
	if count > 0 {
		scores[0] = 5
	}
	payload := map[string]any{
		"status": "ready", "scores": scores, "descriptions": descriptions,
		"descriptionChecks": []any{map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}},
		"taskType":          "feature迭代", "difficulty": "中等", "language": "Go",
		"environment": "无外部依赖", "harnessVersion": "2.1.0", "os": "MacOS/Linux",
		"evidence": []string{"trace.jsonl:1-3"}, "missing": []string{}, "issues": []any{},
		"requirementChecks": []any{map[string]any{"requirement": "完成用户要求", "status": "completed", "evidence": "code/service.go:10; go test ./... PASS"}},
		"nextPrompt":        "", "nextPromptType": "",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func reviewJSONWithScores(t *testing.T, scores [5]int) string {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(validReviewJSON(t, 5)), &document); err != nil {
		t.Fatal(err)
	}
	document["scores"] = []int{scores[0], scores[1], scores[2], scores[3], scores[4]}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func reviewJSONWithDescription(t *testing.T, index int, description string) string {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(validReviewJSON(t, 5)), &document); err != nil {
		t.Fatal(err)
	}
	descriptions := document["descriptions"].([]any)
	descriptions[index] = description
	document["descriptions"] = descriptions
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func annotationScoreTotal(evaluation *annotation.Evaluation) (int, bool) {
	if evaluation == nil {
		return 0, false
	}
	total := 0
	for _, score := range evaluation.Scores {
		if score == nil {
			return 0, false
		}
		total += *score
	}
	return total, true
}

func fakeCodexWritesReview(t *testing.T, payload, stdout, stderr string) string {
	t.Helper()
	fixture := filepath.Join(t.TempDir(), "evaluation.json")
	if err := os.WriteFile(fixture, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	return strings.Join([]string{
		`out=""`,
		`while [ "$#" -gt 0 ]; do`,
		`  if [ "$1" = "-o" ]; then shift; out="$1"; fi`,
		`  shift`,
		`done`,
		`cp ` + shellSingleQuote(fixture) + ` "$out"`,
		`printf '%s\n' ` + shellSingleQuote(stdout),
		`printf '%s\n' ` + shellSingleQuote(stderr) + ` >&2`,
	}, "\n") + "\n"
}

func fakeCodexWritesReviewSequence(t *testing.T, counter string, payloads ...string) string {
	t.Helper()
	fixtures := make([]string, len(payloads))
	for index, payload := range payloads {
		fixture := filepath.Join(t.TempDir(), fmt.Sprintf("evaluation-%d.json", index+1))
		if err := os.WriteFile(fixture, []byte(payload), 0600); err != nil {
			t.Fatal(err)
		}
		fixtures[index] = fixture
	}
	lines := []string{
		`out=""`,
		`while [ "$#" -gt 0 ]; do`,
		`  if [ "$1" = "-o" ]; then shift; out="$1"; fi`,
		`  shift`,
		`done`,
		`attempt=1`,
		`if [ -f ` + shellSingleQuote(counter) + ` ]; then attempt=$(($(cat ` + shellSingleQuote(counter) + `) + 1)); fi`,
		`printf '%s\n' "$attempt" > ` + shellSingleQuote(counter),
	}
	for index, fixture := range fixtures {
		keyword := "elif"
		if index == 0 {
			keyword = "if"
		}
		lines = append(lines, fmt.Sprintf(`%s [ "$attempt" -eq %d ]; then cp %s "$out"`, keyword, index+1, shellSingleQuote(fixture)))
	}
	lines = append(lines, `else cp `+shellSingleQuote(fixtures[len(fixtures)-1])+` "$out"`, `fi`)
	return strings.Join(lines, "\n") + "\n"
}

func writeFakeCodex(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
