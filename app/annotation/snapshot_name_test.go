package annotation

import (
	"context"
	domain "github.com/blueship581/pinru/internal/annotation"
	"testing"
)

func TestInitialSnapshotRepositoryName(t *testing.T) {
	for _, tc := range []struct{ name, id, path, want string }{
		{"cyc-05", "batch__label-123-1", "/tasks/cyc-05-0-1代码生成-1", "cyc-05-1"},
		{"cyc-05", "batch__label-123-9", "/moved/repo", "cyc-05-9"},
		{"CYC-05", "legacy", "/tasks/CYC-05-Bug修复-02/", "cyc-05-2"},
		{"cyc-05", "batch__label-123-2", "/tasks/cyc-05-代码测试-3", ""},
		{"cyc-05", "legacy", "/tasks/unrelated-8", ""},
		{"cyc-05", "batch__label-123-0", "/repo", ""},
		{"cyc-05", "label-123-999999999999999999999", "/repo", ""},
		{"../cyc-05", "label-123-1", "/repo", ""},
		{"cyc 05", "label-123-1", "/repo", "cyc-05-1"},
	} {
		t.Run(tc.name+tc.id+tc.path, func(t *testing.T) {
			got, err := initialSnapshotRepositoryName(&domain.Case{TaskName: tc.name, TaskID: tc.id, SourcePath: tc.path})
			if tc.want == "" {
				if err == nil {
					t.Fatalf("accepted invalid identity: %s", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestPublishedSnapshotKeepsExistingURL(t *testing.T) {
	s, _, _ := annotationFixture(t)
	c, _ := s.loadCase("题目-1")
	c.SnapshotURL = "https://github.com/owner/pinru-initial-old/commit/" + c.InitialSHA
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}
	got, err := s.PublishSnapshot(context.Background(), PrepareRequest{TaskID: c.TaskID})
	if err != nil || got.SnapshotURL != c.SnapshotURL {
		t.Fatalf("existing snapshot changed: %v %v", got, err)
	}
}
