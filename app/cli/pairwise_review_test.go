package cli

import (
	"strings"
	"testing"
)

func TestDecodePairwiseReviewAcceptsConcreteComparison(t *testing.T) {
	raw := []byte(`{"status":"ready","conclusion":"A_better","reason":"A 在 internal/filter.go 中实现了服务端筛选并通过 go test ./...；B 只修改 frontend/src/List.tsx，接口仍返回未过滤数据，因此 A 的交付更完整。"}`)
	result, err := decodePairwiseReview(raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.Conclusion != "A_better" || result.Status != "ready" {
		t.Fatalf("result = %#v", result)
	}
}

func TestDecodePairwiseReviewRejectsNegativeScoreFiveCompletenessDescription(t *testing.T) {
	raw := []byte(`{"status":"ready","conclusion":"A_better","aCompletenessScore":5,"aCompletenessDescription":"核心流程已交付，但真实浏览器交互尚未验证。","bCompletenessScore":4,"bCompletenessDescription":"主体流程已交付，但次要边界仍有遗漏。","reason":"A 在 internal/filter.go 中实现了服务端筛选并通过 go test ./...；B 只修改 frontend/src/List.tsx，接口仍返回未过滤数据，因此 A 的交付更完整。"}`)
	if result, err := decodePairwiseReview(raw); err == nil {
		t.Fatalf("accepted negative score-5 completeness description: %#v", result)
	}
}

func TestDecodePairwiseReviewAcceptsPositiveScoreFiveCompletenessDescription(t *testing.T) {
	raw := []byte(`{"status":"ready","conclusion":"A_better","aCompletenessScore":5,"aCompletenessDescription":"核心流程已经交付，关键验收结果均可使用，没有发现功能遗漏。","bCompletenessScore":4,"bCompletenessDescription":"主体流程已经交付，但次要边界仍有遗漏。","reason":"A 在 internal/filter.go 中实现了服务端筛选并通过 go test ./...；B 只修改 frontend/src/List.tsx，接口仍返回未过滤数据，因此 A 的交付更完整。"}`)
	result, err := decodePairwiseReview(raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.ACompletenessScore != 5 || result.BCompletenessScore != 4 {
		t.Fatalf("result = %#v", result)
	}
}

func TestDecodePairwiseReviewRejectsGenericOrOneSidedReason(t *testing.T) {
	for _, reason := range []string{
		"A 更好，整体完成度更高。",
		"A 在 internal/filter.go 中完成了筛选并通过测试，因此表现很好。",
		strings.Repeat("A 很好，B 也很好。", 8),
	} {
		raw := []byte(`{"status":"ready","conclusion":"A_better","reason":` + quotePairwiseJSON(reason) + `}`)
		if result, err := decodePairwiseReview(raw); err == nil {
			t.Fatalf("accepted generic review %#v for %q", result, reason)
		}
	}
}

func TestDecodePairwiseReviewRejectsSpeculationAndOffstageEvidence(t *testing.T) {
	for _, reason := range []string{
		"A 在 internal/filter.go 中完成筛选并通过轨迹记录的测试；B 可能已经完成同样修改但看起来没有展示出来。这道题更看重稳定性，因此 A 更可靠。",
		"A 在页面实现筛选并完成提交；B 的录屏显示按钮可用，但代码产物没有对应实现。这道题更看重可维护性，因此 A 更值得选择。",
		"A 在 internal/filter.go 中实现筛选；B 用未提交到 Git 的临时脚本做了本地代码测试，结果不能作为交付依据。这道题更看重提交产物，因此 A 更完整。",
	} {
		raw := []byte(`{"status":"ready","conclusion":"A_better","reason":` + quotePairwiseJSON(reason) + `}`)
		if result, err := decodePairwiseReview(raw); err == nil {
			t.Fatalf("accepted speculation or offstage evidence %#v", result)
		}
	}
}

func TestDecodePairwiseReviewRequiresPortalLengthAndDetailedSameReason(t *testing.T) {
	short := []byte(`{"status":"ready","conclusion":"A_better","reason":"A 修改 main.go，B 未修改 main.go，所以 A 更好。"}`)
	if _, err := decodePairwiseReview(short); err == nil {
		t.Fatal("expected reason shorter than 60 Chinese characters to fail")
	}
	same := []byte(`{"status":"ready","conclusion":"same","reason":"A 在 main.go 完成实现并通过测试，B 在 main.go 也完成实现并通过测试；两边表现都很好，最终选择 Same。"}`)
	if _, err := decodePairwiseReview(same); err == nil {
		t.Fatal("expected Same without equivalence or offsetting trade-offs to fail")
	}
	agreement := []byte(`{"status":"ready","conclusion":"same","reason":"A 和 B 都在 viewModel.ts 完成拖拽收尾，并分别覆盖取消与提交场景；实现细节虽有差异，但用户结果一致，均不会残留预览或误加障碍，因此判 same。"}`)
	if _, err := decodePairwiseReview(agreement); err != nil {
		t.Fatalf("accepted Same reason with explicit outcome agreement: %v", err)
	}
}

func TestDecodePairwiseReviewRejectsMetricInventoryWithoutARealTradeoff(t *testing.T) {
	reason := "A 执行 npm test 的退出码为 0，共有 48 条断言通过，产物哈希为 8f45d1a09bc73125，第 126 行也完成修改；B 使用 3.2.1 版本构建，记录了 1920×1080 像素和 16 个轮廓坐标。两边基本等价，优缺点相互抵消。"
	raw := []byte(`{"status":"ready","conclusion":"same","reason":` + quotePairwiseJSON(reason) + `}`)
	if result, err := decodePairwiseReview(raw); err == nil {
		t.Fatalf("accepted metric inventory without a decision basis: %#v", result)
	}
}

func TestDecodePairwiseReviewRejectsMarkdownOrChecklistFormatting(t *testing.T) {
	reason := "- A：在页面实现了筛选功能并运行测试。\n- B：也实现了筛选功能，但没有覆盖空数据。\n- 结论：这道题更看重异常场景下是否仍能正常使用，所以选择 A。"
	raw := []byte(`{"status":"ready","conclusion":"A_better","reason":` + quotePairwiseJSON(reason) + `}`)
	if result, err := decodePairwiseReview(raw); err == nil {
		t.Fatalf("accepted checklist-form review: %#v", result)
	}
}

func TestDecodePairwiseReviewStripsDecorativeQuoteBrackets(t *testing.T) {
	for _, marks := range []string{"『筛选功能』", "「筛选功能」", "【筛选功能】", "《筛选功能》", "〔筛选功能〕", "〈筛选功能〉"} {
		reason := "A 在 src/filter.ts 完成了" + marks + "并运行 npm test 确认空数据也有反馈；B 只读取 src/filter.ts 实现常规流程，异常输入仍会中断操作。这道题更看重用户遇到异常时能否继续使用，因此 A 更可靠。"
		raw := []byte(`{"status":"ready","conclusion":"A_better","reason":` + quotePairwiseJSON(reason) + `}`)
		result, err := decodePairwiseReview(raw)
		if err != nil {
			t.Fatalf("rejected %q after stripping brackets: %v", marks, err)
		}
		if strings.ContainsAny(result.Reason, "『』「」【】《》〔〕〖〗〘〙〚〛〈〉") {
			t.Fatalf("reason still contains decorative brackets after %q: %q", marks, result.Reason)
		}
		if !strings.Contains(result.Reason, "A 在 src/filter.ts 完成了筛选功能并运行 npm test") {
			t.Fatalf("stripping %q changed the sentence beyond removing brackets: %q", marks, result.Reason)
		}
	}
}

func TestDecodePairwiseReviewAcceptsNaturalComparisonWithDecisionBasis(t *testing.T) {
	reason := "A 先读取 src/filter.ts，修改后页面在空数据和重复提交时都能正常反馈，并运行 npm test 走完用户操作；B 只查看 src/filter.ts 并验证常规流程，空数据时仍会留下一块没有说明的空白区域。这道题更看重用户遇到异常输入时能不能继续操作，因此 A 更可靠。"
	result, err := decodePairwiseReview([]byte(`{"status":"ready","conclusion":"A_better","reason":` + quotePairwiseJSON(reason) + `}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Reason != reason {
		t.Fatalf("reason = %q", result.Reason)
	}
}

func TestDecodePairwiseReviewRejectsResultOnlyComparison(t *testing.T) {
	reason := "两边都实现了灵感批量生成、按能量或色相方向约束候选、载入并撤销，运行后发现 B 的候选行在点收起或选中候选后仍占据版面，A 的候选数为零但低能量和暖色标签不自洽。这道题最看重候选可收起，因此 A 更好。"
	raw := []byte(`{"status":"ready","conclusion":"A_better","reason":` + quotePairwiseJSON(reason) + `}`)
	if result, err := decodePairwiseReview(raw); err == nil {
		t.Fatalf("accepted result-only comparison: %#v", result)
	}
}

func TestDecodePairwiseReviewRequiresProcessAnchorsForBothSides(t *testing.T) {
	reason := "A 先读取 src/main.ts 并修改候选区，执行 npm run build；B 也完成了候选功能，页面结果可以使用，但没有说明它读取、修改或验证了什么。两边产物都能生成候选并载入撤销，这道题更看重过程可核验和收起后的实际交互，因此 A 更好。"
	raw := []byte(`{"status":"ready","conclusion":"A_better","reason":` + quotePairwiseJSON(reason) + `}`)
	if result, err := decodePairwiseReview(raw); err == nil {
		t.Fatalf("accepted one-sided process anchors: %#v", result)
	}
}

func TestDecodePairwiseReviewAcceptsProcessAndProductCoverage(t *testing.T) {
	reason := "A 先读取 src/main.ts 和 src/ui/styles.css，修改候选生成与收起处理，并执行 npm run build；B 读取相同入口后新增 src/core/inspiration.ts 和 src/core/history.ts，先修正约束测试失败，再重跑测试和 npm run build。产物上两边都能按能量和色相生成候选、载入并撤销，但 A 的标签不自洽，B 的候选网格覆盖 hidden 导致收起后仍占版面。这道题最看重候选可收起且不干扰创作，因此 A 更好。"
	result, err := decodePairwiseReview([]byte(`{"status":"ready","conclusion":"A_better","reason":` + quotePairwiseJSON(reason) + `}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Conclusion != "A_better" {
		t.Fatalf("result = %#v", result)
	}
}

func TestDecodePairwiseReviewAcceptsConditionDrivenProductCoverage(t *testing.T) {
	reason := "两侧都新增推荐服务。A 新增 StrategyRanker，自测网格里五个难度在同一周期得到相同推荐，难度只改分数不改结论；产物上用户可选 2、7、21、35 天周期，分别推荐集中复习、间隔复习、主动回忆、主动回忆，可手动切换查看。B 重写记忆模型让难度影响提取成功率，新增 StrategyAdvisor，验证脚本出现高难度长周期该选间隔复习却选中主动回忆的断言失败，收紧平局容差后全部难度与周期组合自洽；产物上考察日预设 2 天到五周，7 天低难度默认主动回忆，难度 3 以上及长周期高难度落到间隔复习。两边时间表、评分卡、洞察都跟随推荐且可手动切换，差别在难度能否改变推荐结论，题目要的正是难度与周期共同决定，故 B 更好。"
	result, err := decodePairwiseReview([]byte(`{"status":"ready","conclusion":"B_better","reason":` + quotePairwiseJSON(reason) + `}`))
	if err != nil {
		t.Fatalf("rejected condition-driven product coverage: %v", err)
	}
	if result.Conclusion != "B_better" {
		t.Fatalf("result = %#v", result)
	}
}

func TestDecodePairwiseReviewRejectsInactionWithoutTriggerNode(t *testing.T) {
	reason := "A 真正把复制与分享拆成两条路径：复制按钮只写剪贴板，分享按钮才走系统分享，并按真实结果更新提示，还补了回归测试且构建通过。B 全程停在读代码和空想阶段，没有改动任何文件，交付仍是有缺陷的原实现，复制按钮照旧弹出分享面板。本题更看重按钮行为与提示是否一致，因此 A 明显更好。"
	raw := []byte(`{"status":"ready","conclusion":"A_better","reason":` + quotePairwiseJSON(reason) + `}`)
	if result, err := decodePairwiseReview(raw); err == nil {
		t.Fatalf("accepted inaction criticism without a trigger node: %#v", result)
	}
}

func TestDecodePairwiseReviewAcceptsInactionWithTriggerNode(t *testing.T) {
	reason := "A 修改 ClipboardShareAdapter.ts 和 CyberCardView.ts，拆开复制与分享：复制按钮只写剪贴板，分享按钮按真实结果反馈，取消不再误报成功，并补充 npm test；B 读完上述文件并定位到复制按钮误用 shareSlip 后，在拆分两条调用路径这一步反复重读，没有执行修改；还从 /workspace 运行 npm run build，因找不到 package.json 失败，提交仍保留原缺陷，产物上两边都有复制和分享按钮但行为不同。本题更看重按钮行为与提示是否一致，因此 A 明显更好。"
	result, err := decodePairwiseReview([]byte(`{"status":"ready","conclusion":"A_better","reason":` + quotePairwiseJSON(reason) + `}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Reason != reason {
		t.Fatalf("reason = %q", result.Reason)
	}
}

func TestBuildPairwiseReviewPromptRequiresTriggerNodeForInaction(t *testing.T) {
	prompt := buildPairwiseReviewPrompt("/tmp/evidence.json", "", "")
	if !strings.Contains(prompt, "触发节点") || !strings.Contains(prompt, "只读未改") {
		t.Fatalf("prompt does not require a trigger node for inaction: %s", prompt)
	}
	for _, phrase := range []string{"轨迹文件明确记录", "与轨迹冲突", "不得猜测、补全或虚构", "禁止写录屏", "未提交到 Git", "5 分描述只能写已经交付的正向依据", "验证范围、未覆盖的检查和证据边界不属于完整性描述"} {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("prompt does not contain hard GSB evidence constraint %q: %s", phrase, prompt)
		}
	}
	for _, phrase := range []string{"已提交的代码产物", "冻结代码树", "临时、未提交或审核助手额外生成的脚本仍不可引用"} {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("prompt does not distinguish committed scripts from offstage evidence %q: %s", phrase, prompt)
		}
	}
	for _, symbol := range []string{"『』", "「」", "【】", "《》", "〔〕", "〈〉"} {
		if !strings.Contains(prompt, symbol) {
			t.Fatalf("prompt does not forbid decorative bracket family %q: %s", symbol, prompt)
		}
	}
}

func TestDecodePairwiseReviewKeepsTriggerNodeAfterStrippingBrackets(t *testing.T) {
	reason := "A 修改 ClipboardShareAdapter.ts 和 CyberCardView.ts，拆开『复制』与【分享】：复制按钮只写剪贴板，分享按钮按真实结果反馈，取消不再误报成功，并补充 npm test；B 读完上述文件并定位到复制按钮误用 shareSlip 后，在拆分《两条调用路径》这一步反复重读，没有执行修改；还从 /workspace 运行 npm run build，因找不到 package.json 失败，提交仍保留原缺陷，产物上两边都有复制和分享按钮但行为不同。本题最看重按钮行为与提示是否一致，因此 A 明显更好。"
	result, err := decodePairwiseReview([]byte(`{"status":"ready","conclusion":"A_better","reason":` + quotePairwiseJSON(reason) + `}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(result.Reason, "『』「」【】《》〔〕〖〗〈〉") {
		t.Fatalf("reason still contains decorative brackets: %q", result.Reason)
	}
	if !strings.Contains(result.Reason, "拆开复制与分享") || !strings.Contains(result.Reason, "拆分两条调用路径") {
		t.Fatalf("stripping changed the sentence beyond removing brackets: %q", result.Reason)
	}
}

func TestDecodePairwiseReviewRejectsReasonOverThreeHundredTwentyCharacters(t *testing.T) {
	prefix := "A 完成了核心功能并实际运行了页面，B 也完成实现但异常流程仍会中断用户操作，这道题更看重实际使用是否可靠，因此 A 更值得选择。"
	reason := prefix + strings.Repeat("补", 321-len([]rune(prefix)))
	if got := len([]rune(reason)); got != 321 {
		t.Fatalf("test reason length = %d", got)
	}
	raw := []byte(`{"status":"ready","conclusion":"A_better","reason":` + quotePairwiseJSON(reason) + `}`)
	if result, err := decodePairwiseReview(raw); err == nil {
		t.Fatalf("accepted 321-character review: %#v", result)
	}
}

func TestPairwiseReviewSchemaLimitsReasonToThreeHundredTwentyCharacters(t *testing.T) {
	properties := pairwiseReviewSchema()["properties"].(map[string]any)
	reason := properties["reason"].(map[string]any)
	if reason["maxLength"] != 320 {
		t.Fatalf("reason maxLength = %#v", reason["maxLength"])
	}
}

func quotePairwiseJSON(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
