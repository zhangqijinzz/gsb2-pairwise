package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallBuiltinPromptSkillUsesVerifiableNaturalTaskRules(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	New().InstallBuiltinSkills()
	content, err := os.ReadFile(filepath.Join(home, ".claude", "skills", "评审项目提示词生成", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{
		"150-300 个字",
		"可核查的交付结果",
		"至少一个真实边界",
		"不得出现五维评分",
		"不能故意制造失败",
		"困难题准入门槛",
		"不能拆成互不影响的局部小修",
		"文字截断与完整名称提示",
		"本地存储失败提示",
		"上传文件类型或大小校验",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("installed prompt skill missing %q", want)
		}
	}
	if strings.Contains(text, "不超过 80 字") {
		t.Fatal("installed prompt skill still contains the legacy 80-character limit")
	}
}
