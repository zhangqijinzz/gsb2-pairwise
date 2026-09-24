package job

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/blueship581/pinru/app/testutil"
	domain "github.com/blueship581/pinru/internal/annotation"
)

func TestAnnotationJobUsesRegisteredHandlerAndPersistsOutput(t *testing.T) {
	for _, kind := range []string{"annotation_export", "annotation_capture_table", "annotation_batch_capture_table", "annotation_pairwise_capture", "annotation_pairwise_recording_guide", "annotation_pairwise_record_video", "annotation_pairwise_materials", "annotation_pairwise_batch_review", "annotation_pairwise_export"} {
		t.Run(kind, func(t *testing.T) { testAnnotationJobHandler(t, kind) })
	}
}

func testAnnotationJobHandler(t *testing.T, kind string) {
	st := testutil.OpenTestStore(t)
	svc := New(st, nil, nil, nil, nil, nil, func(ctx context.Context, kind, payload string) (any, error) {
		domain.ReportProgress(ctx, 15, "正在使用 skill 准备制表数据")
		return map[string]string{"result": "frozen", "kind": kind}, nil
	})
	j, err := svc.SubmitJob(SubmitJobRequest{JobType: kind, InputPayload: `{"projectId":"p"}`, MaxRetries: 1, TimeoutSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := st.GetBackgroundJob(j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == "done" {
			if got.OutputPayload == nil || !strings.Contains(*got.OutputPayload, "frozen") {
				t.Fatalf("missing output: %+v", got)
			}
			return
		}
		if got.Status == "error" {
			t.Fatalf("job failed %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish")
}
