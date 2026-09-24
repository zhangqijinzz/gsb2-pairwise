package prompt

import (
	"fmt"
	"strings"
	"testing"
)

func TestDocumentCountsValidation(t *testing.T) {
	for _, counts := range []DocumentCounts{{}, {CodeGen: 3, Feature: 2, BugFix: 4, Difficult: 5, Hell: 4}, DefaultDocumentCounts()} {
		if err := counts.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, counts := range []DocumentCounts{{CodeGen: -1}, {Feature: -1}, {BugFix: -1}, {Difficult: -1}, {Hell: -1}, {CodeGen: 9007199254740991}, {CodeGen: 1, Difficult: 1, Hell: 1}} {
		if err := counts.Validate(); err == nil {
			t.Fatalf("accepted invalid counts: %+v", counts)
		}
	}
}

func TestDocumentCountsNormalizesMissingDifficultyAllocation(t *testing.T) {
	counts := (DocumentCounts{Feature: 2, BugFix: 3}).NormalizeDifficultyAllocation()
	if counts.Difficult != 5 || counts.Hell != 0 {
		t.Fatalf("difficulty allocation = %+v, want 5 difficult and 0 hell", counts)
	}
	defaults := DefaultDocumentCounts()
	if defaults.CodeGen != 10 || defaults.Feature != 10 || defaults.BugFix != 2 || defaults.Difficult != 20 || defaults.Hell != 2 || defaults.Total() != 22 {
		t.Fatalf("default difficulty allocation = %+v", defaults)
	}
	for _, taskType := range []string{"代码理解", "代码测试", "代码重构", "工程化"} {
		if defaults.ByType()[taskType] != 0 {
			t.Fatalf("default %s count = %d, want 0", taskType, defaults.ByType()[taskType])
		}
	}
}

func TestDocumentCountsCheckActualOutput(t *testing.T) {
	counts := DocumentCounts{Feature: 2, BugFix: 3, Difficult: 3, Hell: 2}
	promptTexts := []string{
		"在现有订单列表中补充配送方式筛选，并保持翻页、返回和刷新后的筛选状态一致；筛选条件变化后，列表、汇总数量和导出结果必须同时更新，任何一处失败都要给出可恢复的提示。",
		"会员取消预约后需要同步释放名额，并让候补用户及时收到可预约通知；如果通知发送失败，名额释放结果不能回滚，重试后也不能重复通知同一位用户。",
		"修复商品下架后仍出现在搜索结果中的问题，并刷新相关缓存；缓存刷新失败时列表要回退到数据库结果，不能出现前台可售、后台已下架的冲突。",
		"修复重复提交退款申请时生成两条记录的问题，保留首次处理结果；并保证退款状态、订单金额和财务明细在并发提交与失败重试下仍保持一致，重复请求只能生效一次。",
		"修复课程改期后学员端仍显示原上课时间的问题，确保通知内容、课表统计和教师端名单同步更新，历史已签到记录不能被改期覆盖，批量改期时也不能出现部分成功部分回滚的情况。",
	}
	var doc strings.Builder
	itemIndex := 0
	for _, kind := range []string{"Feature迭代", "Bug修复"} {
		fmt.Fprintf(&doc, "**%s**\n", kind)
		for i := 1; i <= counts.ByType()[kind]; i++ {
			difficulty := "困难"
			if itemIndex >= counts.Difficult {
				difficulty = "地狱"
			}
			fmt.Fprintf(&doc, "%d. 【%s】%s\n", i, difficulty, promptTexts[itemIndex])
			itemIndex++
		}
	}
	if err := counts.ValidateDocument(doc.String()); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		strings.Replace(doc.String(), "1. 【困难】在现有订单列表中补充配送方式筛选，并保持翻页、返回和刷新后的筛选状态一致；筛选条件变化后，列表、汇总数量和导出结果必须同时更新，任何一处失败都要给出可恢复的提示。\n", "", 1),
		doc.String() + "6. 【困难】补充一条与统计口径无关的额外记录，用于核对数量不匹配时的拒绝行为是否正确。\n",
		strings.Replace(doc.String(), "【困难】", "【一般】", 1),
		strings.Replace(doc.String(), "【困难】", "【简单】", 1),
		strings.Replace(doc.String(), promptTexts[0], "在现有订单列表中补充配送方式筛选。", 1),
	} {
		if err := counts.ValidateDocument(output); err == nil {
			t.Fatal("accepted output with wrong counts")
		}
	}
}

func TestDocumentCountsValidateDocumentRejectsRepeatedPromptBodies(t *testing.T) {
	counts := DocumentCounts{CodeGen: 1, Feature: 1, Difficult: 1, Hell: 1}
	document := strings.Join([]string{
		"**0-1代码生成**",
		"1. 【困难】会员提交预约之后，管理员可以确认到店状态，并在列表中查看最新的处理结果，同时把到店时间写入预约记录，方便后续统计到店率。",
		"**Feature迭代**",
		"1. 【地狱】会员提交预约之后，管理员可以确认离店状态，并在列表中查看最新的处理结果，同时把离店时间写入预约记录，方便后续统计到店率。",
	}, "\n")

	err := counts.ValidateDocument(document)
	if err == nil || !strings.Contains(err.Error(), "重复比例") {
		t.Fatalf("ValidateDocument() error = %v, want repeated prompt rejection", err)
	}
}
