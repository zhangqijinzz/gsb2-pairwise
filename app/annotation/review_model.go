package annotation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	appcli "github.com/blueship581/pinru/app/cli"
	"github.com/blueship581/pinru/internal/store"
)

const deepSeekReviewLabel = "DeepSeek V4 Flash"

type reviewExecutionSelection struct {
	Model    string
	DeepSeek *appcli.DeepSeekCodexConfig
	Label    string
}

func (s *AnnotationService) reviewExecution() (reviewExecutionSelection, error) {
	engine, err := s.store.GetConfig("annotation_review_engine")
	if err != nil {
		return reviewExecutionSelection{}, err
	}
	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "" {
		engine = "deepseek"
	}
	if engine == "codex" {
		model, err := s.store.GetConfig("annotation_review_model")
		if err != nil {
			return reviewExecutionSelection{}, err
		}
		model = strings.TrimSpace(model)
		label := "Codex CLI"
		if model != "" {
			label += " (" + model + ")"
		}
		return reviewExecutionSelection{Model: model, Label: label}, nil
	}
	if engine != "deepseek" {
		return reviewExecutionSelection{}, errors.New("不支持的审核引擎，请在设置中重新选择")
	}

	providers, err := s.store.ListLLMProviders()
	if err != nil {
		return reviewExecutionSelection{}, err
	}
	var selected *store.LLMProvider
	for _, requireDefault := range []bool{true, false} {
		for i := range providers {
			if providers[i].IsDefault == requireDefault && isDeepSeekReviewAPIProvider(providers[i]) {
				selected = &providers[i]
				break
			}
		}
		if selected != nil {
			break
		}
	}
	if selected == nil {
		return reviewExecutionSelection{}, errors.New("请先在设置中添加带 API Key 的 DeepSeek V4 Flash API 提供商，或将审核引擎切换为 Codex CLI")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(*selected.BaseURL), "/")
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	fingerprint := sha256.Sum256([]byte(strings.TrimSpace(selected.Model) + "\n" + reviewPipelineVersion + "\nhigh"))
	label := deepSeekReviewLabel + " [engine:" + hex.EncodeToString(fingerprint[:8]) + "]"
	config := &appcli.DeepSeekCodexConfig{
		Model:           strings.TrimSpace(selected.Model),
		BaseURL:         baseURL,
		APIKey:          strings.TrimSpace(selected.APIKey),
		ReasoningEffort: "high",
	}
	return reviewExecutionSelection{Model: config.Model, DeepSeek: config, Label: label}, nil
}

func isDeepSeekReviewAPIProvider(provider store.LLMProvider) bool {
	if provider.ProviderType != "openai_compatible" || strings.TrimSpace(provider.APIKey) == "" || provider.BaseURL == nil {
		return false
	}
	model := strings.ToLower(strings.TrimSpace(provider.Model))
	if model != "deepseek-v4-flash" && model != "deepseek-flash" {
		return false
	}
	baseURL := strings.ToLower(strings.TrimRight(strings.TrimSpace(*provider.BaseURL), "/"))
	return baseURL == "https://api.deepseek.com" || baseURL == "https://api.deepseek.com/v1"
}
