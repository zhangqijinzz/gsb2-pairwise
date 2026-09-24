package config

import (
	"testing"

	"github.com/blueship581/pinru/app/testutil"
)

func TestAnnotationSettingsDefaultToDeepSeekAndKeepContainerKeyMasked(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	service := New(testStore)
	if err := testStore.SetConfig("annotation_container_api_key", "container-secret"); err != nil {
		t.Fatal(err)
	}

	settings, err := service.GetAnnotationSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.ReviewEngine != "deepseek" || !settings.HasContainerAPIKey {
		t.Fatalf("GetAnnotationSettings() = %+v, want DeepSeek with a saved container key", settings)
	}
	if value, err := service.GetConfig("annotation_container_api_key"); err != nil || value != "" {
		t.Fatalf("GetConfig(annotation_container_api_key) = %q, %v; want masked value", value, err)
	}
	if value, err := service.GetAnnotationContainerAPIKey(); err != nil || value != "container-secret" {
		t.Fatalf("GetAnnotationContainerAPIKey() = %q, %v", value, err)
	}
}

func TestSaveAnnotationSettingsValidatesEngineAndPreservesBlankKey(t *testing.T) {
	testStore := testutil.OpenTestStore(t)
	service := New(testStore)

	if err := service.SaveAnnotationSettings("other", "secret"); err == nil {
		t.Fatal("SaveAnnotationSettings() accepted an unsupported review engine")
	}
	if err := service.SaveAnnotationSettings("codex", "first-secret"); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveAnnotationSettings("deepseek", ""); err != nil {
		t.Fatal(err)
	}
	if engine, err := testStore.GetConfig("annotation_review_engine"); err != nil || engine != "deepseek" {
		t.Fatalf("stored engine = %q, %v", engine, err)
	}
	if key, err := testStore.GetConfig("annotation_container_api_key"); err != nil || key != "first-secret" {
		t.Fatalf("stored key = %q, %v; blank update must preserve it", key, err)
	}
}
