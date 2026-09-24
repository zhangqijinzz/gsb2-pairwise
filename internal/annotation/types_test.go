package annotation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCaseJSONUsesPublicCamelCaseContract(t *testing.T) {
	score := 4
	input := Case{
		TaskID: "task-1", ProjectID: "project-1", TaskName: "Demo",
		InitialSHA: strings.Repeat("a", 40), Revision: 3,
		Captures: []Capture{{ID: "capture-1", TraceHash: "trace-tree-hash"}},
		Rounds: []Round{{PromptID: "prompt-1", SourceStart: 1, SourceEnd: 3, Attachments: []string{"attachment:image sha256=abc"},
			Evaluations: []Evaluation{{Scores: [5]*int{&score}, SourceHash: "source-hash", ReviewPath: "/evidence/review", ReviewHash: "review-hash",
				RequirementChecks: []RequirementCheck{{Requirement: "支持导出", Status: "completed", Evidence: "export.go:42"}}}}}},
	}

	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, fragment := range []string{
		`"taskId":"task-1"`, `"projectId":"project-1"`, `"taskName":"Demo"`,
		`"initialSha":"` + strings.Repeat("a", 40) + `"`, `"promptId":"prompt-1"`,
		`"sourceStart":1`, `"sourceEnd":3`, `"revision":3`,
		`"attachments":["attachment:image sha256=abc"]`,
		`"traceHash":"trace-tree-hash"`, `"sourceHash":"source-hash"`,
		`"reviewPath":"/evidence/review"`, `"reviewHash":"review-hash"`,
		`"requirementChecks":[{"requirement":"支持导出","status":"completed","evidence":"export.go:42"}]`,
	} {
		if !strings.Contains(string(payload), fragment) {
			t.Fatalf("JSON %s does not contain %s", payload, fragment)
		}
	}
}

func TestNewAndMergedRoundsMarshalEvaluationsAsArray(t *testing.T) {
	trace := []byte(`{"type":"user","promptId":"p-1","message":{"role":"user","content":"prompt"}}` + "\n" +
		`{"type":"system","subtype":"turn_duration"}`)
	rounds, err := ParseTrace(trace)
	if err != nil {
		t.Fatal(err)
	}
	merged := MergeRounds(nil, rounds)
	payload, err := json.Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"evaluations":null`) || !strings.Contains(string(payload), `"evaluations":[]`) {
		t.Fatalf("round JSON = %s, want evaluations array", payload)
	}
	if strings.Contains(string(payload), `"attachments":null`) || !strings.Contains(string(payload), `"attachments":[]`) {
		t.Fatalf("round JSON = %s, want attachments array", payload)
	}
}
