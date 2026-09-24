package annotation

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReviewRuleHashOnlyChangesForIntegrationProfile(t *testing.T) {
	skill := t.TempDir()
	references := filepath.Join(skill, "references")
	if err := os.MkdirAll(references, 0o700); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(references, "integration-review-profile.md")
	if err := os.WriteFile(profile, []byte("rules-v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := reviewRuleHash(skill)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "unrelated-export-guide.md"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := reviewRuleHash(skill)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("unrelated skill documentation invalidated review cache")
	}
	if err := os.WriteFile(profile, []byte("rules-v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := reviewRuleHash(skill)
	if err != nil {
		t.Fatal(err)
	}
	if third == second {
		t.Fatal("review profile change did not invalidate review cache")
	}
}

func TestChangedEvidenceFilesIgnoresGeneratedDependencies(t *testing.T) {
	initial := t.TempDir()
	current := t.TempDir()
	for _, root := range []string{initial, current} {
		if err := os.MkdirAll(filepath.Join(root, "src"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "src", "main.ts"), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(current, "src", "main.ts"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(current, "node_modules", "pkg"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, "node_modules", "pkg", "index.js"), []byte("generated"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := changedEvidenceFiles(context.Background(), initial, current)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"src/main.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("changed files = %#v, want %#v", got, want)
	}
}

func TestCleanReviewGeneratedArtifactsRemovesLargeCachesOnly(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"node_modules/pkg/index.js", ".pytest_cache/result", "src/main.ts"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cleanReviewGeneratedArtifacts(root)
	for _, removed := range []string{"node_modules", ".pytest_cache"} {
		if _, err := os.Stat(filepath.Join(root, removed)); !os.IsNotExist(err) {
			t.Fatalf("generated directory %s was not removed", removed)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "src", "main.ts")); err != nil {
		t.Fatalf("source file was removed: %v", err)
	}
}
