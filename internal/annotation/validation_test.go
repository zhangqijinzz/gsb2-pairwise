package annotation

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidateEvaluationAcceptsFiveIndependentReadyScores(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "evidence-hash"}
	evaluation := validEvaluation("evidence-hash")
	if err := ValidateEvaluation(round, evaluation); err != nil {
		t.Fatalf("ValidateEvaluation() error = %v", err)
	}
}

func TestValidateEvaluationRejectsReadyScoresOutsideCollectionBand(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "evidence-hash"}

	belowMinimum := validEvaluation("evidence-hash")
	two := 2
	belowMinimum.Scores[1] = &two
	if err := ValidateEvaluation(round, belowMinimum); err == nil || !strings.Contains(err.Error(), "3 to 5") {
		t.Fatalf("score below three error = %v, want 3 to 5 rejection", err)
	}

	overLimit := validEvaluation("evidence-hash")
	five := 5
	overLimit.Scores = [5]*int{&five, &five, &five, &five, &five}
	if err := ValidateEvaluation(round, overLimit); err == nil || !strings.Contains(err.Error(), "exceeds 21") {
		t.Fatalf("score total above 21 error = %v, want score ceiling rejection", err)
	}
}

func TestValidateEvaluationAcceptsLegacyRecordWithoutRequirementChecks(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "evidence-hash"}
	evaluation := validEvaluation("evidence-hash")
	if err := ValidateEvaluation(round, evaluation); err != nil {
		t.Fatalf("legacy evaluation rejected: %v", err)
	}
}

func TestValidateEvaluationChecksRequirementCheckFieldsAndStatus(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "evidence-hash"}
	tests := []struct {
		name  string
		check RequirementCheck
		want  string
	}{
		{"empty requirement", RequirementCheck{Status: "completed", Evidence: "service.go:10"}, "requirement"},
		{"invalid status", RequirementCheck{Requirement: "保存数据", Status: "unknown", Evidence: "service.go:10"}, "status"},
		{"empty evidence", RequirementCheck{Requirement: "保存数据", Status: "completed"}, "evidence"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluation := validEvaluation("evidence-hash")
			evaluation.RequirementChecks = []RequirementCheck{test.check}
			if err := ValidateEvaluation(round, evaluation); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateEvaluation() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateEvaluationRequiresBugForFailedRequirement(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "evidence-hash"}
	evaluation := validEvaluation("evidence-hash")
	evaluation.RequirementChecks = []RequirementCheck{{Requirement: "保存数据", Status: "failed", Evidence: "service.go:10 未实现写入"}}
	if err := ValidateEvaluation(round, evaluation); err == nil || !strings.Contains(err.Error(), "bug") {
		t.Fatalf("ValidateEvaluation() error = %v, want bug consistency error", err)
	}

	four := 4
	evaluation.Scores[0] = &four
	evaluation.Descriptions[0] = "保存功能未完成。"
	evaluation.DescriptionChecks[0] = DescriptionCheck{Judgment: "保存功能未完成", Location: "service.go:10", Behavior: "没有写入数据", Consequence: "重新打开后无法读取"}
	evaluation.Descriptions[0] = "在 service.go:10 的保存步骤，保存功能未完成：没有写入数据，导致重新打开后无法读取。"
	evaluation.Issues = []Issue{{Description: "未保存数据", Evidence: "service.go:10", Kind: "bug"}}
	evaluation.NextPrompt = "修复保存数据未落盘的问题，触发保存后应能重新读取"
	evaluation.NextPromptType = "Bug修复"
	if err := ValidateEvaluation(round, evaluation); err != nil {
		t.Fatalf("failed requirement with bug rejected: %v", err)
	}
}

func TestValidateEvaluationRequiresConcreteDescriptionChecksBelowFive(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "evidence-hash"}
	four := 4
	evaluation := validEvaluation("evidence-hash")
	evaluation.Scores[2] = &four
	evaluation.QualityVersion = 2
	evaluation.DescriptionChecks[2] = DescriptionCheck{}
	evaluation.Descriptions[2] = "规划阶段存在遗漏。"
	if err := ValidateEvaluation(round, evaluation); err == nil || !strings.Contains(err.Error(), "description check 3") {
		t.Fatalf("ValidateEvaluation() error = %v, want structured description evidence error", err)
	}
	evaluation.DescriptionChecks[2] = DescriptionCheck{
		Judgment:    "规划阶段遗漏了前置检查",
		Location:    "第1轮执行 npm run build 前",
		Behavior:    "未先确认 package.json 中的构建脚本",
		Consequence: "首次构建使用了不存在的脚本并退出 1",
	}
	evaluation.Descriptions[2] = "第1轮执行 npm run build 前，规划阶段遗漏了前置检查，未先确认 package.json 中的构建脚本，首次构建使用了不存在的脚本并退出 1。"
	if err := ValidateEvaluation(round, evaluation); err != nil {
		t.Fatalf("concrete non-perfect description rejected: %v", err)
	}
}

func TestValidateEvaluationRequiresMissingEvidenceForUnverifiedRequirement(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "evidence-hash"}
	evaluation := validEvaluation("evidence-hash")
	evaluation.RequirementChecks = []RequirementCheck{{Requirement: "浏览器交互", Status: "unverified", Evidence: "静态检查无法确认运行时交互"}}
	if err := ValidateEvaluation(round, evaluation); err == nil || !strings.Contains(err.Error(), "needs_evidence") {
		t.Fatalf("ValidateEvaluation() error = %v, want needs_evidence consistency error", err)
	}

	evaluation.Status = "needs_evidence"
	evaluation.Missing = []string{"浏览器交互：缺少可定位的运行记录"}
	if err := ValidateEvaluation(round, evaluation); err == nil || !strings.Contains(err.Error(), "delivery score") {
		t.Fatalf("ValidateEvaluation() error = %v, want unverified delivery score error", err)
	}

	evaluation.Scores[0] = nil
	evaluation.Descriptions[0] = ""
	if err := ValidateEvaluation(round, evaluation); err != nil {
		t.Fatalf("unverified requirement with missing evidence rejected: %v", err)
	}
}

func TestValidateEvaluationAllowsFailedAndUnverifiedRequirementsTogether(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "evidence-hash"}
	evaluation := validEvaluation("evidence-hash")
	four := 4
	evaluation.Status = "needs_evidence"
	evaluation.Scores[0] = &four
	evaluation.Descriptions[0] = "保存功能未完成；浏览器交互缺少运行证据。"
	evaluation.Missing = []string{"浏览器交互：缺少可定位的运行记录"}
	evaluation.RequirementChecks = []RequirementCheck{
		{Requirement: "保存数据", Status: "failed", Evidence: "service.go:10 未实现写入"},
		{Requirement: "浏览器交互", Status: "unverified", Evidence: "静态检查无法确认运行时交互"},
	}
	evaluation.Issues = []Issue{{Description: "未保存数据", Evidence: "service.go:10", Kind: "bug"}}
	evaluation.NextPrompt = "修复保存数据未落盘的问题，触发保存后应能重新读取"
	evaluation.NextPromptType = "Bug修复"
	if err := ValidateEvaluation(round, evaluation); err != nil {
		t.Fatalf("mixed failed and unverified requirements rejected: %v", err)
	}
}

func TestValidateEvaluationAcceptsOnlySpecificMissingEvidenceForNullableScores(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "evidence-hash"}
	evaluation := validEvaluation("evidence-hash")
	evaluation.Status = "needs_evidence"
	evaluation.Scores[2] = nil
	evaluation.Descriptions[2] = ""
	evaluation.Missing = []string{"任务规划：缺少本轮工具调用与计划事件"}
	if err := ValidateEvaluation(round, evaluation); err != nil {
		t.Fatalf("ValidateEvaluation() error = %v", err)
	}

	evaluation.Missing = []string{"  "}
	if err := ValidateEvaluation(round, evaluation); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("ValidateEvaluation() error = %v, want specific missing evidence error", err)
	}
}

func TestValidateEvaluationNeedsEvidenceCanKeepAllSupportedScores(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "evidence-hash"}
	evaluation := validEvaluation("evidence-hash")
	evaluation.Status = "needs_evidence"
	evaluation.HarnessVersion = ""
	evaluation.Missing = []string{"Harness 版本：轨迹事件未记录 version"}
	if err := ValidateEvaluation(round, evaluation); err != nil {
		t.Fatalf("ValidateEvaluation() error = %v", err)
	}
}

func TestValidateEvaluationRejectsBadEnumsScoreAndEvidenceMismatch(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "round-hash"}
	tests := []struct {
		name   string
		mutate func(*Evaluation)
		want   string
	}{
		{"old difficulty enum", func(e *Evaluation) { e.Difficulty = "一般" }, "difficulty"},
		{"task type case mismatch", func(e *Evaluation) { e.TaskType = "Feature迭代" }, "taskType"},
		{"environment enum", func(e *Evaluation) { e.Environment = "docker" }, "environment"},
		{"os enum", func(e *Evaluation) { e.OS = "Linux" }, "os"},
		{"score zero", func(e *Evaluation) { zero := 0; e.Scores[1] = &zero }, "score"},
		{"missing description", func(e *Evaluation) { e.Descriptions[4] = "" }, "description"},
		{"wrong evidence", func(e *Evaluation) { e.EvidenceHash = "old-hash" }, "evidenceHash"},
		{"bad issue kind", func(e *Evaluation) { e.Issues = []Issue{{Description: "x", Evidence: "event 2", Kind: "quality"}} }, "kind"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluation := validEvaluation("round-hash")
			test.mutate(&evaluation)
			if err := ValidateEvaluation(round, evaluation); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateEvaluation() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateEvaluationRejectsSimpleScoreDescriptionContradictions(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "round-hash"}
	evaluation := validEvaluation("round-hash")
	evaluation.Descriptions[0] = "核心流程已经交付，但真实浏览器交互尚未验证。"
	if err := ValidateEvaluation(round, evaluation); err == nil || !strings.Contains(err.Error(), "contradict") {
		t.Fatalf("score 5 evidence-boundary contradiction error = %v", err)
	}

	evaluation = validEvaluation("round-hash")
	five := 5
	four := 4
	evaluation.Scores[0] = &four
	evaluation.Scores[4] = &five
	evaluation.Descriptions[4] = "执行过程中遗漏了一个边界，因此扣1分。"
	if err := ValidateEvaluation(round, evaluation); err == nil || !strings.Contains(err.Error(), "contradict") {
		t.Fatalf("score 5 contradiction error = %v", err)
	}

	evaluation = validEvaluation("round-hash")
	evaluation.Scores[0] = &four
	evaluation.Descriptions[0] = "所有交付均完整，无任何问题。"
	if err := ValidateEvaluation(round, evaluation); err == nil || !strings.Contains(err.Error(), "contradict") {
		t.Fatalf("score 4 contradiction error = %v", err)
	}

	evaluation = validEvaluation("round-hash")
	evaluation.Descriptions[0] = "核心流程已经交付，没有发现功能遗漏，关键验收结果均可使用。"
	if err := ValidateEvaluation(round, evaluation); err != nil {
		t.Fatalf("positive score 5 description rejected: %v", err)
	}
}

func TestValidateEvaluationDoesNotTreatNegatedContradictionPhraseAsAClaim(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "round-hash"}
	evaluation := validEvaluation("round-hash")
	evaluation.Descriptions[4] = "已核对执行证据，没有扣1分的情形。"
	if err := ValidateEvaluation(round, evaluation); err != nil {
		t.Fatalf("negated deduction rejected: %v", err)
	}

	four := 4
	evaluation = validEvaluation("round-hash")
	evaluation.Scores[0] = &four
	evaluation.Descriptions[0] = "并非无任何问题，产物仍缺少关键实现。"
	if err := ValidateEvaluation(round, evaluation); err != nil {
		t.Fatalf("negated perfect claim rejected: %v", err)
	}
}

func TestValidateEvaluationAcceptsNaturalDescriptionsWithTechnicalReferences(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "round-hash"}
	evaluation := validEvaluation("round-hash")
	evaluation.Descriptions[0] = "本轮修改了 internal/annotation/validation.go 中的 ValidateEvaluation，并通过 go test ./internal/annotation 验证了状态校验。文件名、函数名和命令都能直接定位到本轮交付。"
	if err := ValidateEvaluation(round, evaluation); err != nil {
		t.Fatalf("technical references in natural prose were rejected: %v", err)
	}
}

func TestPlatformInterruptionDeductionDoesNotTreatNumericFileNameAsRateLimit(t *testing.T) {
	evaluation := validEvaluation("round-hash")
	four := 4
	evaluation.Scores[0] = &four
	evaluation.Descriptions[0] = "交付仍有遗漏，src/429_handler.go 只实现了错误类型，原提示词要求的导出入口尚未接入。"
	if reason := PlatformInterruptionDeduction(evaluation); reason != "" {
		t.Fatalf("numeric file name was treated as a platform interruption: %s", reason)
	}

	evaluation.Descriptions[0] = "本轮因 429 RateLimitError 中断，所以交付不完整。"
	if reason := PlatformInterruptionDeduction(evaluation); reason == "" {
		t.Fatal("rate-limit deduction was accepted")
	}
}

func TestValidateEvaluationDoesNotRejectNaturalCausalWording(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "round-hash"}
	evaluation := validEvaluation("round-hash")
	evaluation.Descriptions[0] = "validation.go 会检查空参数，因此给调用方返回明确错误。这个处理与本轮要求一致。"
	if err := ValidateEvaluation(round, evaluation); err != nil {
		t.Fatalf("natural causal wording was rejected: %v", err)
	}
}

func TestValidateEvaluationRejectsMachineFormattedDescriptions(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "round-hash"}
	tests := []struct {
		name        string
		description string
		want        string
	}{
		{"markdown backticks", "修改了 `validation.go`，并执行了 `go test ./...`。", "反引号"},
		{"arrow chain", "修改 validation.go → 执行测试 → 完成交付。", "箭头"},
		{"template labels", "触发节点：保存时。实际行为：写入失败。业务影响：数据丢失。", "固定标签"},
		{"markdown list", "- 修改 validation.go\n- 执行 go test ./...", "单段"},
		{"decorative emoji", "✅ validation.go 已修改，测试已经通过。", "装饰符号"},
		{"decorative bullet", "● validation.go 已修改，测试已经通过。", "装饰符号"},
		{"stock conclusion", "validation.go 已修改并完成测试，综上所述，因此给5分。", "套话"},
		{"ai preface", "作为一个AI，我将从以下几个方面说明 validation.go 的修改结果。", "AI套话"},
		{"unfinished sentence", "validation.go 已完成修改，同时还需要", "语句不完整"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluation := validEvaluation("round-hash")
			evaluation.Descriptions[0] = test.description
			err := ValidateEvaluation(round, evaluation)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateEvaluation() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateEvaluationRejectsHighlyRepeatedDescriptions(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "round-hash"}
	evaluation := validEvaluation("round-hash")
	evaluation.Descriptions[0] = "模型在最终验证阶段执行 go test ./internal/annotation，命令返回成功，但没有检查导出文件中的五项文字是否完整。"
	evaluation.Descriptions[1] = "模型在最终验证阶段执行 go test ./internal/annotation，命令返回成功，但没有检查导出文件中的五项文字是否通顺。"

	err := ValidateEvaluation(round, evaluation)
	if err == nil || !strings.Contains(err.Error(), "重复比例") {
		t.Fatalf("ValidateEvaluation() error = %v, want high repetition rejection", err)
	}
}

func TestValidateEvaluationRejectsIdenticalDescriptions(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "round-hash"}
	evaluation := validEvaluation("round-hash")
	evaluation.Descriptions[0] = "修改 validation.go 后执行 go test ./internal/annotation，相关校验全部通过。"
	evaluation.Descriptions[1] = evaluation.Descriptions[0]

	err := ValidateEvaluation(round, evaluation)
	if err == nil || !strings.Contains(err.Error(), "内容重复") {
		t.Fatalf("ValidateEvaluation() error = %v, want identical description rejection", err)
	}
}

func TestValidateEvaluationRejectsMachineWrittenRepairPrompt(t *testing.T) {
	round := Round{PromptID: "p-1", EvidenceHash: "round-hash"}
	evaluation := validEvaluation("round-hash")
	four := 4
	evaluation.Scores[0] = &four
	evaluation.Descriptions[0] = "订单保存后没有写入最新状态，重新打开详情时仍显示旧数据。"
	evaluation.Issues = []Issue{{Kind: "bug", Description: "订单状态没有保存", Evidence: "service.go 的 SaveOrder"}}
	evaluation.NextPrompt = "修复以下是为你整理的问题：请修改 service.go 中的 SaveOrder。"
	evaluation.NextPromptType = "Bug修复"

	err := ValidateEvaluation(round, evaluation)
	if err == nil || !strings.Contains(err.Error(), "修复提示词文案") {
		t.Fatalf("ValidateEvaluation() error = %v, want repair-prompt writing rejection", err)
	}
}

func TestPreflightCountsMinimumScoredReadyRoundsAndBlocksIncompleteMaterial(t *testing.T) {
	sha := strings.Repeat("a", 40)
	evaluation := validEvaluation("trace-hash")
	three := 3
	evaluation.Scores[0] = &three
	evaluation.Descriptions[0] = "交付缺少关键实现，证据见产物差异。"
	cases := []Case{{
		TaskID: "task-1", ProjectID: "project-1", Completed: true,
		InitialSHA: sha, SnapshotURL: "https://github.com/acme/repo/commit/" + sha,
		TracePath: "/home/node/.claude/projects/-workspace/session.jsonl",
		Rounds:    []Round{{PromptID: "p-1", SessionID: "s-1", Order: 1, Status: "complete", EvidenceHash: "trace-hash", Evaluations: []Evaluation{evaluation}}},
	}}
	report := Preflight(cases)
	if report.Tasks != 1 || report.Rounds != 1 || report.Ready != 1 || len(report.Issues) != 0 {
		t.Fatalf("Preflight() = %#v", report)
	}

	cases[0].Completed = false
	cases[0].Rounds[0].Status = "pending"
	report = Preflight(cases)
	if report.Ready != 0 || len(report.Issues) < 2 {
		t.Fatalf("blocked Preflight() = %#v", report)
	}
}

func TestPreflightIgnoresLegacyStandaloneRecoveryRounds(t *testing.T) {
	sha := strings.Repeat("a", 40)
	evaluation := validEvaluation("trace-hash")
	cases := []Case{{
		TaskID: "task-1", Completed: true, InitialSHA: sha,
		SnapshotURL: "https://github.com/acme/repo/commit/" + sha,
		Rounds: []Round{
			{PromptID: "p-1", SessionID: "s-1", Prompt: "实现订单筛选功能", Order: 1, Status: "complete", EvidenceHash: "trace-hash", Evaluations: []Evaluation{evaluation}},
			{PromptID: "p-continue-1", SessionID: "s-1", Prompt: "继续", Order: 2, Status: "complete"},
			{PromptID: "p-continue-2", SessionID: "s-1", Prompt: "请继续。", Order: 3, Status: "complete"},
		},
	}}

	report := Preflight(cases)
	if report.Rounds != 1 || report.Ready != 1 || len(report.Issues) != 0 {
		t.Fatalf("Preflight() = %#v, want only the original prompt round", report)
	}
}

func TestPreflightCollectsTwentyOneAndRejectsLegacyTwentyTwo(t *testing.T) {
	sha := strings.Repeat("a", 40)
	makeCase := func(taskID string, scores [5]int) Case {
		evaluation := validEvaluation("trace-hash-" + taskID)
		for index := range scores {
			score := scores[index]
			evaluation.Scores[index] = &score
		}
		return Case{
			TaskID: taskID, ProjectID: "project-1", Completed: true,
			InitialSHA: sha, SnapshotURL: "https://github.com/acme/repo/commit/" + sha,
			Rounds: []Round{{PromptID: "p-" + taskID, SessionID: "s-" + taskID, Order: 1, Status: "complete", EvidenceHash: evaluation.EvidenceHash, Evaluations: []Evaluation{evaluation}}},
		}
	}
	cases := []Case{
		makeCase("score-21", [5]int{5, 4, 4, 4, 4}),
		makeCase("score-22", [5]int{5, 5, 4, 4, 4}),
	}

	report := Preflight(cases)

	if report.Ready != 1 || report.NotCollected != 0 || !containsIssue(report.Issues, "exceeds 21") {
		t.Fatalf("Preflight() = %#v, want one collectable and one invalid legacy over-limit evaluation", report)
	}
	got := make([]int, 0, 5)
	for _, score := range cases[1].Rounds[0].Evaluations[0].Scores {
		got = append(got, *score)
	}
	if !reflect.DeepEqual(got, []int{5, 5, 4, 4, 4}) {
		t.Fatalf("Preflight changed truthful scores: %v", got)
	}
}

func TestPreflightRequiresFullMatchingSnapshotAndLatestMatchingEvaluation(t *testing.T) {
	sha := strings.Repeat("b", 40)
	old := validEvaluation("old-hash")
	currentNeedsEvidence := validEvaluation("new-hash")
	currentNeedsEvidence.Status = "needs_evidence"
	currentNeedsEvidence.Scores[0] = nil
	currentNeedsEvidence.Descriptions[0] = ""
	currentNeedsEvidence.Missing = []string{"交付完整性：缺本轮代码快照"}
	c := Case{
		TaskID: "task-1", Completed: true, InitialSHA: sha,
		SnapshotURL: "https://github.com/acme/repo/commit/" + strings.Repeat("c", 40),
		Rounds:      []Round{{PromptID: "p-1", SessionID: "s-1", Status: "complete", EvidenceHash: "new-hash", Evaluations: []Evaluation{old, currentNeedsEvidence}}},
	}
	report := Preflight([]Case{c})
	if report.Ready != 0 || !containsIssue(report.Issues, "snapshot") || !containsIssue(report.Issues, "ready") {
		t.Fatalf("Preflight() = %#v", report)
	}
}

func TestPreflightBlocksMoreThanTenRealRoundsWithoutDroppingThem(t *testing.T) {
	sha := strings.Repeat("d", 40)
	rounds := make([]Round, 11)
	for index := range rounds {
		hash := "hash-" + string(rune('a'+index))
		rounds[index] = Round{PromptID: "prompt-" + string(rune('a'+index)), SessionID: "s", Order: index + 1, Status: "complete", EvidenceHash: hash, Evaluations: []Evaluation{validEvaluation(hash)}}
	}
	report := Preflight([]Case{{TaskID: "task", Completed: true, InitialSHA: sha, SnapshotURL: "https://github.com/acme/repo/commit/" + sha, Rounds: rounds}})
	if report.Rounds != 11 || report.Ready != 0 || !containsIssue(report.Issues, "10") {
		t.Fatalf("Preflight() = %#v", report)
	}
}

func TestPreflightRequiresFrozenCaptureDirectoryAndArtifacts(t *testing.T) {
	root := t.TempDir()
	tracePath := filepath.Join(root, "trace.jsonl")
	codePath := filepath.Join(root, "code")
	mustWriteFile(t, tracePath, "{}\n", 0644)
	if err := os.Mkdir(codePath, 0755); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("e", 40)
	report := Preflight([]Case{{
		TaskID: "task", Completed: true, InitialSHA: sha,
		SnapshotURL: "https://github.com/acme/repo/commit/" + sha,
		Captures:    []Capture{{ID: "capture", Dir: filepath.Join(root, "missing-capture"), TracePath: tracePath, CodePath: codePath, Hash: "code-hash"}},
		Rounds:      []Round{{PromptID: "prompt", SessionID: "session", Status: "complete", EvidenceHash: "trace-hash", CaptureID: "capture", Evaluations: []Evaluation{validEvaluation("trace-hash")}}},
	}})
	if report.Ready != 0 || !containsIssue(report.Issues, "capture directory") {
		t.Fatalf("Preflight() = %#v", report)
	}
}

func validEvaluation(evidenceHash string) Evaluation {
	five := 5
	four := 4
	return Evaluation{
		ID: "eval-1", CreatedAt: 10, SkillHash: "skill-hash", Model: "model",
		EvidenceHash: evidenceHash, Status: "ready",
		Scores: [5]*int{&five, &four, &four, &four, &four},
		Descriptions: [5]string{
			"交付完整。",
			"在第1轮核对用户约束时，指令覆盖仍有轻微遗漏，模型没有逐项说明边界条件，导致约束对应关系不够清晰。",
			"在第1轮开始实现前，规划拆解不够具体，模型没有列出验证步骤，导致完成路径缺少明确检查节点。",
			"在第1轮分析实现条件时，推理覆盖存在轻微不足，模型没有说明次要边界，导致部分判断依据未被明确记录。",
			"在第1轮交付前的验证环节，执行覆盖存在轻微不足，模型只完成主要检查，导致次要场景没有留下验证记录。",
		},
		DescriptionChecks: [5]DescriptionCheck{
			{},
			{Judgment: "指令覆盖仍有轻微遗漏", Location: "第1轮核对用户约束时", Behavior: "模型没有逐项说明边界条件", Consequence: "导致约束对应关系不够清晰"},
			{Judgment: "规划拆解不够具体", Location: "第1轮开始实现前", Behavior: "模型没有列出验证步骤", Consequence: "导致完成路径缺少明确检查节点"},
			{Judgment: "推理覆盖存在轻微不足", Location: "第1轮分析实现条件时", Behavior: "模型没有说明次要边界", Consequence: "导致部分判断依据未被明确记录"},
			{Judgment: "执行覆盖存在轻微不足", Location: "第1轮交付前的验证环节", Behavior: "模型只完成主要检查", Consequence: "导致次要场景没有留下验证记录"},
		},
		TaskType: "feature迭代", Difficulty: "中等", Language: "Go",
		Environment: "无外部依赖", HarnessVersion: "2.1.0", OS: "MacOS/Linux",
		Evidence: []string{"trace.jsonl:1-4", "code/tree"},
	}
}

func containsIssue(issues []string, fragment string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, fragment) {
			return true
		}
	}
	return false
}
