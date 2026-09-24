package annotation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	domain "github.com/blueship581/pinru/internal/annotation"
)

const reviewPipelineVersion = "review-v5-recovery-attribution"

func annotationDifficulty(value string) string {
	switch strings.TrimSpace(value) {
	case "简单":
		return "简单"
	case "一般", "中等":
		return "中等"
	case "困难":
		return "困难"
	case "地狱":
		return "地狱"
	default:
		return ""
	}
}

type reviewEvidenceIndex struct {
	PromptID           string   `json:"promptId"`
	SessionID          string   `json:"sessionId"`
	Round              int      `json:"round"`
	SourceLines        string   `json:"sourceLines"`
	RoundTracePath     string   `json:"roundTracePath"`
	FullTracePath      string   `json:"fullTracePath"`
	ChangedFiles       []string `json:"changedFiles"`
	VerificationPolicy string   `json:"verificationPolicy"`
}

func writeReviewEvidenceIndex(ctx context.Context, work, fullTrace, codePath, initialPath string, round domain.Round) (string, string, error) {
	raw, err := os.ReadFile(fullTrace)
	if err != nil {
		return "", "", err
	}
	sliced, err := domain.SliceTraceRound(raw, round)
	if err != nil {
		return "", "", err
	}
	roundTrace := filepath.Join(work, "round-trace.jsonl")
	if err := os.WriteFile(roundTrace, sliced, 0o600); err != nil {
		return "", "", err
	}
	changed, err := changedEvidenceFiles(ctx, initialPath, codePath)
	if err != nil {
		return "", "", err
	}
	index := reviewEvidenceIndex{
		PromptID: round.PromptID, SessionID: round.SessionID, Round: round.Order,
		SourceLines:    fmt.Sprintf("%d-%d", round.SourceStart, round.SourceEnd),
		RoundTracePath: roundTrace, FullTracePath: fullTrace, ChangedFiles: changed,
		VerificationPolicy: "优先使用本轮轨迹中的相关成功验证；只有关键需求仍无法判断时才安装依赖或补充运行。",
	}
	encoded, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return "", "", err
	}
	indexPath := filepath.Join(work, "evidence-index.json")
	if err := os.WriteFile(indexPath, encoded, 0o600); err != nil {
		return "", "", err
	}
	return indexPath, roundTrace, nil
}

func changedEvidenceFiles(ctx context.Context, initialPath, codePath string) ([]string, error) {
	current, err := evidenceFileDigests(ctx, codePath)
	if err != nil {
		return nil, err
	}
	initial := map[string]string{}
	if strings.TrimSpace(initialPath) != "" {
		initial, err = evidenceFileDigests(ctx, initialPath)
		if err != nil {
			return nil, err
		}
	}
	set := map[string]struct{}{}
	for name, hash := range current {
		if initial[name] != hash {
			set[name] = struct{}{}
		}
	}
	for name := range initial {
		if _, ok := current[name]; !ok {
			set[name] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for name := range set {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func evidenceFileDigests(ctx context.Context, root string) (map[string]string, error) {
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == ".cache" || entry.Name() == "__pycache__") {
			if path != root {
				return filepath.SkipDir
			}
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		result[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	return result, err
}

func reviewRuleHash(skillDir string) (string, error) {
	content, err := os.ReadFile(filepath.Join(skillDir, "references", "integration-review-profile.md"))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(reviewPipelineVersion+"\x00"), content...))
	return hex.EncodeToString(sum[:]), nil
}

func cleanReviewGeneratedArtifacts(root string) {
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() || path == root {
			return nil
		}
		switch entry.Name() {
		case "node_modules", ".cache", "__pycache__", ".pytest_cache", ".next", ".vite":
			_ = os.RemoveAll(path)
			return filepath.SkipDir
		}
		return nil
	})
}
