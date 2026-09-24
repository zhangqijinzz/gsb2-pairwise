package cli

import "testing"

func TestDecodePairwiseRecordingGuideRequiresThreeToFiveConcreteSteps(t *testing.T) {
	result, err := decodePairwiseRecordingGuide([]byte(`{"steps":["打开首页并确认页面加载完成","点击皱眉榜切换排行榜","打开第一条记录查看详情","关闭详情返回列表"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Steps) != 4 || result.Steps[1] != "点击皱眉榜切换排行榜" {
		t.Fatalf("result = %#v", result)
	}

	if _, err := decodePairwiseRecordingGuide([]byte(`{"steps":["打开页面","点击按钮"]}`)); err == nil {
		t.Fatal("two steps should be rejected")
	}
}
