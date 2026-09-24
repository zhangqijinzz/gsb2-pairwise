package prompt

import (
	"strings"
	"testing"

	"github.com/blueship581/pinru/internal/analysis"
)

func TestSplitPromptSectionsSeparatesConstraintLines(t *testing.T) {
	raw := "筛选条件切换后列表偶尔还停留在上一轮结果，需要保证页面只展示最后一次筛选结果。\n业务逻辑约束：已下架商品不能重新出现在可售列表。\n非代码回复约束：只描述用户看到的结果。"

	body, constraints := SplitPromptSections(raw)

	if body != "筛选条件切换后列表偶尔还停留在上一轮结果，需要保证页面只展示最后一次筛选结果。" {
		t.Fatalf("body = %q", body)
	}
	if len(constraints) != 2 {
		t.Fatalf("len(constraints) = %d, want 2", len(constraints))
	}
}

func TestPromptBodyRuneCountIgnoresConstraintLinesAndWhitespace(t *testing.T) {
	raw := "  登录后首页偶尔还是游客状态，需要保证刷新后立刻展示会员身份。 \n\n业务逻辑约束：游客缓存不能覆盖登录态。\n"

	if got := PromptBodyRuneCount(raw); got != 30 {
		t.Fatalf("PromptBodyRuneCount() = %d, want 30", got)
	}
}

func TestNormalizeTaskTypeSupportsZeroToOneAliases(t *testing.T) {
	tests := []string{"代码生成", "0-1代码生成", "0-1", "从零到一", "从0到1", "0到1"}
	for _, input := range tests {
		if got := NormalizeTaskType(input); got != TaskTypeCodeGen {
			t.Fatalf("NormalizeTaskType(%q) = %q, want %q", input, got, TaskTypeCodeGen)
		}
	}
}

func TestBuildSystemPromptUsesExpandedLengthGuidance(t *testing.T) {
	prompt := BuildSystemPrompt()
	if !strings.Contains(prompt, "150-300 个字") {
		t.Fatalf("BuildSystemPrompt() missing expanded length range: %q", prompt)
	}
	if strings.Contains(prompt, "80 个字") {
		t.Fatalf("BuildSystemPrompt() still contains legacy 80-char limit: %q", prompt)
	}
}

func TestBuildSystemPromptCarriesNaturalWritingRedLines(t *testing.T) {
	prompt := BuildSystemPrompt()
	for _, want := range []string{"模板化表达", "语义换皮", "语句必须通顺完整", "必要的文件名"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("BuildSystemPrompt() missing natural-writing rule %q: %q", want, prompt)
		}
	}
}

func TestBuildSystemPromptIncludesPairwiseChangeVolumeGate(t *testing.T) {
	prompt := BuildSystemPrompt()
	for _, want := range []string{
		"困难或地狱题必须让两次独立实现都需要至少 10 行有效源码改动",
		"改动规模大致可比",
		"依赖锁文件、依赖目录、构建产物和纯文档不计入",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("BuildSystemPrompt() missing pairwise gate %q: %q", want, prompt)
		}
	}
}

func TestValidatePromptWritingQualityRejectsMachineWriting(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"ai preface", "以下是为你生成的需求：会员到店后无法确认预约状态，需要补齐签到记录。", "模板化"},
		{"decorative chain", "用户提交预约 → 管理员确认 → 系统更新状态。", "装饰符号"},
		{"unfinished sentence", "会员到店后无法确认预约状态，同时还需要", "语句不完整"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidatePromptWritingQuality(test.text)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidatePromptWritingQuality() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidatePromptWritingQualityRejectsAuditRuleLeakageAndEmptyGoals(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "score threshold leakage",
			text: "新增订单导出能力，并让五维评分不超过21分，方便后续收录。",
			want: "审核规则",
		},
		{
			name: "induced failure leakage",
			text: "调整会员列表，并故意保留一个问题让模型容易出错，方便扣分。",
			want: "审核规则",
		},
		{
			name: "vague goal only",
			text: "优化体验，完善逻辑，增强稳定性。",
			want: "过于空泛",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidatePromptWritingQuality(test.text)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidatePromptWritingQuality() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidatePromptWritingQualityAcceptsNaturalTechnicalReferences(t *testing.T) {
	text := "导入项目后，页面没有及时刷新已生成的任务。请保留 project.json 的现有字段，并确保重新打开页面时能看到最新结果。"
	if err := ValidatePromptWritingQuality(text); err != nil {
		t.Fatalf("ValidatePromptWritingQuality() rejected natural prompt with filename: %v", err)
	}
}

func TestBuildUserPromptUsesZeroToOneTaskTypeAndNewLimit(t *testing.T) {
	got := BuildUserPrompt(
		TaskInfo{ProjectName: "PINRU"},
		PromptRequest{TaskType: "从零到一"},
		analysis.Summary{DetectedStack: []string{"Go"}, TotalFiles: 12},
		"",
	)

	for _, want := range []string{
		"任务类型：0-1代码生成",
		"基于现有系统补齐完整新模块/新能力",
		"150-300 个字之间",
		"最多不超过 300 个字",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("BuildUserPrompt() missing %q in: %q", want, got)
		}
	}
}

func TestPromptBodyExceedsLimitUsesThreeHundredRuneCap(t *testing.T) {
	if PromptBodyExceedsLimit(strings.Repeat("好", MaxPromptBodyRunes)) {
		t.Fatalf("PromptBodyExceedsLimit() should allow exactly %d runes", MaxPromptBodyRunes)
	}
	if !PromptBodyExceedsLimit(strings.Repeat("好", MaxPromptBodyRunes+1)) {
		t.Fatalf("PromptBodyExceedsLimit() should reject more than %d runes", MaxPromptBodyRunes)
	}
}
