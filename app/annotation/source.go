package annotation

import (
	"errors"
	"path"
	"path/filepath"
	"strings"

	domain "github.com/blueship581/pinru/internal/annotation"
)

// A selected file alone is not evidence that its session belongs to this task.
// Claude starts at /workspace in the supplied image, so that parent cwd needs
// corroboration from the imported prompt or an explicit repository reference.
func validateTraceSource(c *domain.Case, source string, rounds []domain.Round, expectedPrompt string) error {
	compact := func(s string) string { return strings.Join(strings.Fields(s), "") }
	first := ""
	for _, r := range rounds {
		if r.Status != "excluded" {
			first = r.Prompt
			break
		}
	}
	knownPrompt := strings.TrimSpace(expectedPrompt) != ""
	if knownPrompt && !strings.Contains(compact(first), compact(expectedPrompt)) {
		return errors.New("所选会话的首轮输入与本题导入提示词不一致，请核对是否选错轨迹")
	}
	expected := source
	if c.ContainerID != "" {
		expected = path.Join("/workspace", filepath.ToSlash(c.RepoRelativePath))
	} else if real, err := filepath.EvalSymlinks(source); err == nil {
		expected = real
	}
	for _, r := range rounds {
		if r.Status == "excluded" {
			continue
		}
		cwd := path.Clean(r.Cwd)
		if c.ContainerID == "" {
			if real, err := filepath.EvalSymlinks(cwd); err == nil {
				cwd = real
			}
		}
		if cwd == expected || strings.HasPrefix(cwd, expected+"/") {
			continue
		}
		if c.ContainerID != "" && cwd == "/workspace" && (knownPrompt || strings.Contains(first, filepath.ToSlash(c.RepoRelativePath))) {
			continue
		}
		return errors.New("轨迹工作目录与本题仓库不匹配或缺失，无法确认材料来源")
	}
	return nil
}
