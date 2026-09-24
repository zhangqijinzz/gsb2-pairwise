package annotation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPairwiseCaseJSONRoundTripAndLegacyDefault(t *testing.T) {
	c := Case{
		TaskID:     "task-pair",
		Mode:       CaseModePairwiseGSB,
		InitialSHA: strings.Repeat("a", 40),
		Pairwise: &PairwiseData{
			Prompt:  "实现筛选功能",
			RunA:    PairwiseRun{Side: PairwiseSideA, Branch: "A", SessionID: "session-a"},
			RunB:    PairwiseRun{Side: PairwiseSideB, Branch: "B", SessionID: "session-b"},
			Reviews: []PairwiseReview{},
		},
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Case
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	NormalizeCase(&decoded)
	if decoded.Mode != CaseModePairwiseGSB || decoded.Pairwise == nil || decoded.Pairwise.RunA.Branch != "A" {
		t.Fatalf("decoded pairwise case = %#v", decoded)
	}

	legacy := Case{TaskID: "legacy"}
	NormalizeCase(&legacy)
	if legacy.Mode != CaseModeLegacy {
		t.Fatalf("legacy mode = %q, want %q", legacy.Mode, CaseModeLegacy)
	}
}

func TestValidatePairwiseCaseRequiresDistinctSingleTurnSides(t *testing.T) {
	c := completePairwiseCase()
	c.Pairwise.RunB.SessionID = c.Pairwise.RunA.SessionID
	c.Pairwise.RunB.TurnCount = 2
	issues := ValidatePairwiseCase(c, false)
	assertPairwiseIssueContains(t, issues, "SessionID")
	assertPairwiseIssueContains(t, issues, "一轮")
}

func TestValidatePairwiseCaseRequiresPortalFields(t *testing.T) {
	c := completePairwiseCase()
	c.Pairwise.Language = ""
	c.Pairwise.Validity = ""
	c.PromptDifficulty = "中等"
	issues := ValidatePairwiseCase(c, true)
	assertPairwiseIssueContains(t, issues, "语言/框架")
	assertPairwiseIssueContains(t, issues, "有效性")
	assertPairwiseIssueContains(t, issues, "困难或地狱")
}

func TestValidatePairwiseFormalRequiresCurrentReviewButAllowsMissingVideos(t *testing.T) {
	c := completePairwiseCase()
	c.Pairwise.RunA.VideoStatus = PairwiseVideoMissing
	c.Pairwise.RunA.VideoURL = ""
	c.Pairwise.Reviews = nil

	draftIssues := ValidatePairwiseCase(c, false)
	if containsPairwiseIssue(draftIssues, "视频") || containsPairwiseIssue(draftIssues, "GSB") {
		t.Fatalf("draft issues = %#v, should not require final materials", draftIssues)
	}
	formalIssues := ValidatePairwiseCase(c, true)
	if containsPairwiseIssue(formalIssues, "视频") {
		t.Fatalf("formal issues = %#v, should allow missing videos", formalIssues)
	}
	assertPairwiseIssueContains(t, formalIssues, "GSB")
}

func TestCurrentPairwiseReviewRequiresMatchingSourceHashes(t *testing.T) {
	c := completePairwiseCase()
	if review := CurrentPairwiseReview(c); review == nil {
		t.Fatal("expected current review")
	}
	c.Pairwise.RunB.CaptureHash = "changed"
	if review := CurrentPairwiseReview(c); review != nil {
		t.Fatalf("stale review = %#v, want nil", review)
	}
}

func TestCurrentPairwiseReviewIgnoresVideoChanges(t *testing.T) {
	c := completePairwiseCase()
	c.Pairwise.RunA.VideoStatus = PairwiseVideoManualRequired
	c.Pairwise.RunA.VideoURL = ""
	c.Pairwise.RunA.VideoPath = ""
	c.Pairwise.RunB.VideoURL = "https://example.com/replaced-b.mp4"

	if review := CurrentPairwiseReview(c); review == nil {
		t.Fatal("video-only changes should not invalidate the current review")
	}
}

func TestCurrentPairwiseReviewAcceptsLegacyVideoAwareHashes(t *testing.T) {
	c := completePairwiseCase()
	c.Pairwise.Reviews[0].SourceHashA = legacyPairwiseRunSourceHash(c.Pairwise.RunA)
	c.Pairwise.Reviews[0].SourceHashB = legacyPairwiseRunSourceHash(c.Pairwise.RunB)

	if review := CurrentPairwiseReview(c); review == nil {
		t.Fatal("legacy review hashes should remain current after upgrade")
	}
}

func TestPairwiseReasonDecorativeBracketDetectionAndStripping(t *testing.T) {
	for _, symbol := range []string{"『", "』", "「", "」", "【", "】", "《", "》", "〔", "〕", "〖", "〗", "〘", "〙", "〚", "〛", "〈", "〉", "｢", "｣"} {
		reason := "A 修好" + symbol + "筛选" + symbol + "，B 未改动，本题最看重结果一致，判同级。"
		if !PairwiseReasonHasDecorativeBrackets(reason) {
			t.Fatalf("decorative bracket %q was not detected", symbol)
		}
		stripped := StripPairwiseReasonDecorations(reason)
		if PairwiseReasonHasDecorativeBrackets(stripped) {
			t.Fatalf("decorative bracket %q survived stripping: %q", symbol, stripped)
		}
		if stripped != "A 修好筛选，B 未改动，本题最看重结果一致，判同级。" {
			t.Fatalf("stripping %q changed more than the brackets: %q", symbol, stripped)
		}
	}

	clean := "A 修好筛选，B 未改动，本题最看重结果一致，判同级。"
	if PairwiseReasonHasDecorativeBrackets(clean) {
		t.Fatal("clean reason was flagged as containing decorative brackets")
	}
	if StripPairwiseReasonDecorations(clean) != clean {
		t.Fatal("stripping rewrote a reason without decorative brackets")
	}
}

func TestValidatePairwiseCompletenessRejectsNegativeFiveDescription(t *testing.T) {
	for _, description := range []string{
		"已完成核心流程，但真实浏览器交互尚未验证。",
		"主要功能已交付，仍有一个关键场景未覆盖。",
		"实现了主流程，但交付结果存在功能遗漏。",
		"保存失败后仍无法恢复，用户重新打开仍看到示例布局。",
		"异常场景尚未覆盖，主流程仍有缺陷。",
	} {
		if err := ValidatePairwiseCompletenessScoreDescription(5, description); err == nil {
			t.Fatalf("accepted negative score-5 description %q", description)
		}
	}
	for _, description := range []string{
		"已完成核心流程，未发现功能遗漏，关键验收结果均可使用。",
		"原始需求的主要功能已经交付，没有发现影响使用的问题。",
		"保存失败后再次成功时只清除保存提示，阻断提示继续保留，连续失败和新编辑场景均已覆盖。",
		"主库记录无法迁移时改从备份恢复，结果写回主库并同步备份，旧数据可以继续使用。",
		"主库写入异常时仍保留备份，用户重新打开可以恢复原方案，保存与恢复场景均已覆盖。",
		"保存失败后继续编辑只更新阻断提示，不抹掉尚未恢复的保存警告，阻断未解除时保存恢复成功只清保存提示。",
		"主库读取抛出异常时以备份作为数据来源，主库有效时直接沿用并同步备份，保存失败回退备份仍然写入成功。",
		"新增测试覆盖多次失败后成功、失败后继续编辑与两类提示独立性，重新构建的 dist 随修复一起提交。",
		"启动恢复失败单独成一路，保存失败只写保存提示，编辑只更新阻断提示，三类提示互不串扰。",
		"缺少后加字段的旧记录补上默认网格尺寸与空障碍数组并标记是否发生改动，读取异常时改走备份恢复。",
		"完成未完成拖拽的收尾清理，尺寸不足与草稿缺失各分支都先清空拖拽起点，正常拖完的一笔仍作为一个障碍提交。",
		"缺少拖拽起点或草稿以及尺寸不足时同样走取消逻辑，切换工具与指针取消后不会新增障碍。",
		"旧格式记录缺少网格尺寸按默认值 24 填充，障碍数组缺失时给空列表，主库记录无法识别成方案时不再当成没有数据，转为从备份取回；主库打不开或读取报错时同样落到备份恢复。",
		"保存时主库与备份一起刷新，避免两份长期不一致；src/viewModel.test.ts 另外补上切换工具、指针取消与正常提交三条回归用例，正常拖完的一笔仍会提交并持久化。",
	} {
		if err := ValidatePairwiseCompletenessScoreDescription(5, description); err != nil {
			t.Fatalf("rejected positive score-5 description %q: %v", description, err)
		}
	}
	if err := ValidatePairwiseCompletenessScoreDescription(4, "主体流程已交付，但次要边界仍有遗漏。"); err != nil {
		t.Fatalf("score 4 should allow a bounded shortcoming: %v", err)
	}
}

func TestValidatePairwiseCaseBlocksNegativeScoreFiveDescription(t *testing.T) {
	c := completePairwiseCase()
	c.Pairwise.Reviews[0].ACompletenessScore = 5
	c.Pairwise.Reviews[0].ACompletenessDescription = "核心流程已经交付，但真实浏览器交互尚未验证。"
	c.Pairwise.Reviews[0].BCompletenessScore = 4
	c.Pairwise.Reviews[0].BCompletenessDescription = "主体流程已交付，但次要边界仍有遗漏。"
	issues := ValidatePairwiseCase(c, false)
	assertPairwiseIssueContains(t, issues, "A：交付完整性为 5 分时")
}

func completePairwiseCase() Case {
	sha := strings.Repeat("a", 40)
	c := Case{
		TaskID:           "task-pair",
		TaskName:         "Pair",
		PromptDifficulty: "困难",
		Mode:             CaseModePairwiseGSB,
		InitialSHA:       sha,
		SnapshotURL:      "https://github.com/example/repo/commit/" + sha,
		Pairwise: &PairwiseData{
			Prompt:         "实现筛选功能",
			Language:       "Go",
			Harness:        "Codex CLI",
			HarnessVersion: "1.0.0",
			OS:             "MacOS/Linux",
			Validity:       PairwiseValidityValid,
			RunA: PairwiseRun{
				Side: PairwiseSideA, Branch: "A", ContainerID: "container-a", WorkspacePath: "/workspace-a", RepoRelativePath: "repo", SessionID: "session-a", TurnCount: 1,
				CaptureID: "capture-a", CaptureHash: "capture-hash-a", TraceHash: "trace-hash-a",
				DeliverableSHA: strings.Repeat("b", 40), DeliverableURL: "https://github.com/example/repo/commit/" + strings.Repeat("b", 40),
				VideoStatus: PairwiseVideoReady, VideoURL: "https://example.com/a.mp4",
			},
			RunB: PairwiseRun{
				Side: PairwiseSideB, Branch: "B", ContainerID: "container-b", WorkspacePath: "/workspace-b", RepoRelativePath: "repo", SessionID: "session-b", TurnCount: 1,
				CaptureID: "capture-b", CaptureHash: "capture-hash-b", TraceHash: "trace-hash-b",
				DeliverableSHA: strings.Repeat("c", 40), DeliverableURL: "https://github.com/example/repo/commit/" + strings.Repeat("c", 40),
				VideoStatus: PairwiseVideoReady, VideoURL: "https://example.com/b.mp4",
			},
		},
	}
	c.Pairwise.Reviews = []PairwiseReview{{
		ID: "review-1", Status: PairwiseReviewReady, Conclusion: PairwiseConclusionA,
		Reason:      "A 在 internal/filter.go 完成筛选并通过测试；B 只修改了界面，缺少服务端过滤。",
		SourceHashA: PairwiseRunSourceHash(c.Pairwise.RunA),
		SourceHashB: PairwiseRunSourceHash(c.Pairwise.RunB),
	}}
	return c
}

func assertPairwiseIssueContains(t *testing.T, issues []string, part string) {
	t.Helper()
	if !containsPairwiseIssue(issues, part) {
		t.Fatalf("issues = %#v, want substring %q", issues, part)
	}
}

func containsPairwiseIssue(issues []string, part string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, part) {
			return true
		}
	}
	return false
}
