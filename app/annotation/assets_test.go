package annotation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeAssetsDoesNotReuseEditedSkillDirectory(t *testing.T) {
	root := t.TempDir()
	first, err := MaterializeAssets(root)
	if err != nil {
		t.Fatalf("MaterializeAssets() first call error = %v", err)
	}
	skillPath := filepath.Join(first, "skill", "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("edited after extraction\n"), 0o600); err != nil {
		t.Fatalf("edit extracted skill: %v", err)
	}

	second, err := MaterializeAssets(root)
	if err != nil {
		t.Fatalf("MaterializeAssets() after edit error = %v", err)
	}
	if second == first {
		t.Fatalf("MaterializeAssets() reused modified directory %q", second)
	}
	content, err := os.ReadFile(filepath.Join(second, "skill", "SKILL.md"))
	if err != nil {
		t.Fatalf("read restored skill: %v", err)
	}
	if strings.Contains(string(content), "edited after extraction") {
		t.Fatal("restored skill contains modified content")
	}

	third, err := MaterializeAssets(root)
	if err != nil {
		t.Fatalf("MaterializeAssets() reuse restored directory error = %v", err)
	}
	if third != second {
		t.Fatalf("MaterializeAssets() did not reuse verified restored directory: got %q, want %q", third, second)
	}
}
