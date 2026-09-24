package annotation

import (
	"strings"
	"testing"
)

func TestParseTraceExtractsOnlyHumanPromptsAndCompletion(t *testing.T) {
	trace := strings.Join([]string{
		`{"type":"user","promptId":"p-1","sessionId":"s-1","version":"2.1.0","cwd":"/workspace/repo","message":{"role":"user","content":"  Keep my prompt exactly.\nSecond line  "}}`,
		`{"type":"assistant","sessionId":"s-1","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-1","name":"Read"}],"stop_reason":"tool_use"}}`,
		`{"type":"user","promptId":"tool-event","sessionId":"s-1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"done"}]}}`,
		`{"type":"assistant","sessionId":"s-1","message":{"role":"assistant","content":[{"type":"text","text":"Finished."}],"stop_reason":"end_turn"}}`,
		`{"type":"system","subtype":"turn_duration","sessionId":"s-1","durationMs":1234}`,
	}, "\n")

	rounds, err := ParseTrace([]byte(trace))
	if err != nil {
		t.Fatalf("ParseTrace() error = %v", err)
	}
	if len(rounds) != 1 {
		t.Fatalf("len(rounds) = %d, want 1: %#v", len(rounds), rounds)
	}
	round := rounds[0]
	if round.Prompt != "  Keep my prompt exactly.\nSecond line  " {
		t.Fatalf("Prompt = %q, raw prompt was changed", round.Prompt)
	}
	if round.PromptID != "p-1" || round.SessionID != "s-1" || round.Version != "2.1.0" || round.Cwd != "/workspace/repo" {
		t.Fatalf("round metadata = %#v", round)
	}
	if round.Status != "complete" || round.Order != 1 || round.SourceStart != 1 || round.SourceEnd != 5 {
		t.Fatalf("round boundary/status = %#v", round)
	}
	if len(round.EvidenceHash) != 64 {
		t.Fatalf("EvidenceHash = %q, want sha256", round.EvidenceHash)
	}
}

func TestParseTraceMergesStandaloneContinueIntoPreviousPromptChain(t *testing.T) {
	trace := strings.Join([]string{
		`{"type":"user","uuid":"u-1","promptId":"p-1","sessionId":"s-1","message":{"role":"user","content":"实现订单筛选功能"}}`,
		`{"type":"assistant","sessionId":"s-1","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit"}],"stop_reason":"tool_use"}}`,
		`{"type":"user","uuid":"u-2","promptId":"p-continue","sessionId":"s-1","message":{"role":"user","content":"继续"}}`,
		`{"type":"assistant","sessionId":"s-1","message":{"role":"assistant","content":[{"type":"text","text":"已完成并验证。"}],"stop_reason":"end_turn"}}`,
		`{"type":"system","subtype":"turn_duration","sessionId":"s-1"}`,
	}, "\n")

	rounds, err := ParseTrace([]byte(trace))
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 1 {
		t.Fatalf("len(rounds) = %d, want one merged prompt chain: %#v", len(rounds), rounds)
	}
	round := rounds[0]
	if round.Prompt != "实现订单筛选功能" || round.PromptID != "p-1" {
		t.Fatalf("merged round replaced the original prompt identity: %#v", round)
	}
	if round.Status != "complete" || round.SourceStart != 1 || round.SourceEnd != 5 {
		t.Fatalf("merged round boundary/status = %#v", round)
	}
}

func TestParseTraceMergesEveryStandaloneContinueIntoOriginalPromptChain(t *testing.T) {
	trace := strings.Join([]string{
		`{"type":"user","uuid":"u-1","promptId":"p-1","sessionId":"s-1","message":{"role":"user","content":"实现订单筛选功能"}}`,
		`{"type":"assistant","sessionId":"s-1","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit"}],"stop_reason":"tool_use"}}`,
		`{"type":"assistant","sessionId":"s-1","message":{"role":"assistant","content":[{"type":"text","text":"429 RateLimitError"}],"stop_reason":"error"}}`,
		`{"type":"system","subtype":"turn_duration","sessionId":"s-1"}`,
		`{"type":"user","uuid":"u-2","promptId":"p-continue-1","sessionId":"s-1","message":{"role":"user","content":"继续"}}`,
		`{"type":"assistant","sessionId":"s-1","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash"}],"stop_reason":"tool_use"}}`,
		`{"type":"assistant","sessionId":"s-1","message":{"role":"assistant","content":[{"type":"text","text":"429 RateLimitError"}],"stop_reason":"error"}}`,
		`{"type":"system","subtype":"turn_duration","sessionId":"s-1"}`,
		`{"type":"user","uuid":"u-3","promptId":"p-continue-2","sessionId":"s-1","message":{"role":"user","content":"请继续"}}`,
		`{"type":"assistant","sessionId":"s-1","message":{"role":"assistant","content":[{"type":"text","text":"已完成并验证。"}],"stop_reason":"end_turn"}}`,
		`{"type":"system","subtype":"turn_duration","sessionId":"s-1"}`,
	}, "\n")

	rounds, err := ParseTrace([]byte(trace))
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 1 {
		t.Fatalf("len(rounds) = %d, want one original prompt round: %#v", len(rounds), rounds)
	}
	round := rounds[0]
	if round.Prompt != "实现订单筛选功能" || round.PromptID != "p-1" {
		t.Fatalf("recovery commands replaced the original prompt identity: %#v", round)
	}
	if round.Status != "complete" || round.SourceStart != 1 || round.SourceEnd != 11 {
		t.Fatalf("merged recovery chain boundary/status = %#v", round)
	}
}

func TestParseTraceKeepsSubstantivePromptBeginningWithContinueAsNewRound(t *testing.T) {
	trace := strings.Join([]string{
		`{"type":"user","promptId":"p-1","sessionId":"s","message":{"role":"user","content":"实现订单筛选功能"}}`,
		`{"type":"system","subtype":"turn_duration","sessionId":"s"}`,
		`{"type":"user","promptId":"p-2","sessionId":"s","message":{"role":"user","content":"继续完善筛选条件，并增加日期范围查询"}}`,
		`{"type":"system","subtype":"turn_duration","sessionId":"s"}`,
	}, "\n")

	rounds, err := ParseTrace([]byte(trace))
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 2 || rounds[1].PromptID != "p-2" {
		t.Fatalf("substantive follow-up was merged as a recovery command: %#v", rounds)
	}
}

func TestMergeRoundsDropsLegacyStandaloneContinueRoundAfterRecapture(t *testing.T) {
	previous := []Round{
		{PromptID: "p-1", SessionID: "s", Prompt: "实现订单筛选功能", EvidenceHash: "old"},
		{PromptID: "p-continue", SessionID: "s", Prompt: "继续", EvidenceHash: "old-continue"},
	}
	incoming := []Round{{PromptID: "p-1", SessionID: "s", Prompt: "实现订单筛选功能", EvidenceHash: "merged"}}
	merged := MergeRounds(previous, incoming)
	if len(merged) != 1 || merged[0].EvidenceHash != "merged" {
		t.Fatalf("legacy continuation remained as an exportable round: %#v", merged)
	}
}

func TestParseTraceTreatsSidechainsMetaAndToolResultsAsNonHuman(t *testing.T) {
	trace := strings.Join([]string{
		`{"type":"user","isSidechain":true,"promptId":"sub","message":{"role":"user","content":"subagent prompt"}}`,
		`{"type":"user","isMeta":true,"promptId":"meta","message":{"role":"user","content":"meta prompt"}}`,
		`{"type":"user","isCompactSummary":true,"promptId":"summary","message":{"role":"user","content":"compressed conversation summary"}}`,
		`{"type":"user","promptId":"tools","message":{"role":"user","content":[{"type":"tool_result","content":"result"},{"type":"text","text":"metadata"}]}}`,
	}, "\n")

	rounds, err := ParseTrace([]byte(trace))
	if err != nil {
		t.Fatalf("ParseTrace() error = %v", err)
	}
	if len(rounds) != 0 {
		t.Fatalf("rounds = %#v, want none", rounds)
	}
}

func TestParseTraceNormalizesDuplicatePromptChainButFlagsConflictingID(t *testing.T) {
	trace := strings.Join([]string{
		`{"type":"user","uuid":"user-1","parentUuid":"parent-1","promptId":"p-1","sessionId":"s","message":{"role":"user","content":"same prompt"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}}`,
		`{"type":"user","uuid":"user-1","parentUuid":"parent-1","promptId":"p-1","sessionId":"s","message":{"role":"user","content":"same prompt"}}`,
		`{"type":"system","subtype":"turn_duration"}`,
		`{"type":"user","promptId":"p-1","sessionId":"s","message":{"role":"user","content":"different prompt"}}`,
		`{"type":"system","subtype":"turn_duration"}`,
	}, "\n")

	rounds, err := ParseTrace([]byte(trace))
	if err != nil {
		t.Fatalf("ParseTrace() error = %v", err)
	}
	if len(rounds) != 2 {
		t.Fatalf("len(rounds) = %d, want 2: %#v", len(rounds), rounds)
	}
	if rounds[0].Status != "conflict" || rounds[1].Status != "conflict" {
		t.Fatalf("conflicting promptId statuses = %q, %q", rounds[0].Status, rounds[1].Status)
	}
	if rounds[0].SourceStart != 1 || rounds[0].SourceEnd != 4 {
		t.Fatalf("duplicate chain boundary = %d..%d, want 1..4", rounds[0].SourceStart, rounds[0].SourceEnd)
	}
}

func TestParseTraceKeepsMixedAttachmentPromptAndPureAttachmentConflict(t *testing.T) {
	trace := strings.Join([]string{
		`{"type":"user","uuid":"u-1","promptId":"p-1","sessionId":"s","message":{"role":"user","content":[{"type":"text","text":"inspect this screenshot"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc"}}]}}`,
		`{"type":"system","subtype":"turn_duration"}`,
		`{"type":"user","uuid":"u-2","promptId":"p-2","sessionId":"s","message":{"role":"user","content":[{"type":"document","source":{"type":"file","path":"spec.pdf"}}]}}`,
		`{"type":"system","subtype":"turn_duration"}`,
	}, "\n")
	rounds, err := ParseTrace([]byte(trace))
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 2 {
		t.Fatalf("len(rounds) = %d, want 2: %#v", len(rounds), rounds)
	}
	if rounds[0].Prompt != "inspect this screenshot" || len(rounds[0].Attachments) != 1 || !strings.Contains(rounds[0].Attachments[0], "attachment:image") || rounds[0].Status != "complete" {
		t.Fatalf("mixed attachment round = %#v", rounds[0])
	}
	if rounds[1].Prompt != "" || len(rounds[1].Attachments) != 1 || !strings.Contains(rounds[1].Attachments[0], "attachment:document") || rounds[1].Status != "conflict" || !strings.Contains(rounds[1].Reason, "non-text") {
		t.Fatalf("pure attachment round = %#v", rounds[1])
	}
}

func TestMergeRoundsPreservesIncomingAttachmentsWithoutAliasing(t *testing.T) {
	incoming := []Round{{PromptID: "p", SessionID: "s", EvidenceHash: "h", Attachments: []string{"attachment:image sha256=abc"}}}
	merged := MergeRounds(nil, incoming)
	if len(merged[0].Attachments) != 1 || merged[0].Attachments[0] != incoming[0].Attachments[0] {
		t.Fatalf("merged attachments = %#v", merged[0].Attachments)
	}
	merged[0].Attachments[0] = "changed"
	if incoming[0].Attachments[0] != "attachment:image sha256=abc" {
		t.Fatal("MergeRounds aliased incoming attachments")
	}
}

func TestParseTraceDoesNotMergeUnidentifiedDuplicateOrInheritCompletion(t *testing.T) {
	trace := strings.Join([]string{
		`{"type":"user","promptId":"p-1","sessionId":"s","message":{"role":"user","content":"retry me"}}`,
		`{"type":"system","subtype":"turn_duration"}`,
		`{"type":"user","promptId":"p-1","sessionId":"s","message":{"role":"user","content":"retry me"}}`,
	}, "\n")
	rounds, err := ParseTrace([]byte(trace))
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 2 {
		t.Fatalf("len(rounds) = %d, want 2", len(rounds))
	}
	if rounds[1].Status != "conflict" || rounds[1].SourceStart != 3 {
		t.Fatalf("unidentified retry inherited completion or was merged: %#v", rounds[1])
	}
}

func TestParseTracePreservesMissingPromptIDAsConflictAndIncompleteAsPending(t *testing.T) {
	trace := strings.Join([]string{
		`{"type":"user","sessionId":"s","message":{"role":"user","content":"missing id"}}`,
		`{"type":"system","subtype":"turn_duration"}`,
		`{"type":"user","promptId":"p-2","sessionId":"s","message":{"role":"user","content":"still running"}}`,
	}, "\n")

	rounds, err := ParseTrace([]byte(trace))
	if err != nil {
		t.Fatalf("ParseTrace() error = %v", err)
	}
	if len(rounds) != 2 {
		t.Fatalf("len(rounds) = %d, want 2", len(rounds))
	}
	if rounds[0].PromptID != "" || rounds[0].Status != "conflict" || !strings.Contains(rounds[0].Reason, "promptId") {
		t.Fatalf("missing promptId round = %#v", rounds[0])
	}
	if rounds[1].Status != "pending" {
		t.Fatalf("incomplete round status = %q, want pending", rounds[1].Status)
	}
}

func TestParseTraceUsesActualMetadataFromTheRoundEventChain(t *testing.T) {
	trace := strings.Join([]string{
		`{"type":"user","promptId":"p-1","message":{"role":"user","content":"prompt"}}`,
		`{"type":"assistant","sessionId":"session-real","version":"2.2.0","cwd":"/workspace/real","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}}`,
	}, "\n")
	rounds, err := ParseTrace([]byte(trace))
	if err != nil {
		t.Fatal(err)
	}
	if rounds[0].SessionID != "session-real" || rounds[0].Version != "2.2.0" || rounds[0].Cwd != "/workspace/real" {
		t.Fatalf("round metadata = %#v", rounds[0])
	}
}

func TestParseTraceFlagsPromptIDReusedAfterAnotherHumanTurn(t *testing.T) {
	trace := strings.Join([]string{
		`{"type":"user","promptId":"p-1","sessionId":"s","message":{"role":"user","content":"same"}}`,
		`{"type":"system","subtype":"turn_duration"}`,
		`{"type":"user","promptId":"p-2","sessionId":"s","message":{"role":"user","content":"other"}}`,
		`{"type":"system","subtype":"turn_duration"}`,
		`{"type":"user","promptId":"p-1","sessionId":"s","message":{"role":"user","content":"same"}}`,
		`{"type":"system","subtype":"turn_duration"}`,
	}, "\n")
	rounds, err := ParseTrace([]byte(trace))
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 3 || rounds[0].Status != "conflict" || rounds[2].Status != "conflict" {
		t.Fatalf("reused promptId rounds = %#v", rounds)
	}
}

func TestParseTraceRejectsMalformedOrTruncatedJSONL(t *testing.T) {
	_, err := ParseTrace([]byte("{\"type\":\"user\"}\n{\"type\":"))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("ParseTrace() error = %v, want line 2 parse error", err)
	}
}

func TestParseTraceRoundHashStopsBeforeNextHumanPrompt(t *testing.T) {
	first := strings.Join([]string{
		`{"type":"user","promptId":"p-1","message":{"role":"user","content":"one"}}`,
		`{"type":"system","subtype":"turn_duration"}`,
	}, "\n")
	oneRound, err := ParseTrace([]byte(first))
	if err != nil {
		t.Fatal(err)
	}
	twoRounds, err := ParseTrace([]byte(first + "\n" + strings.Join([]string{
		`{"type":"user","promptId":"p-2","message":{"role":"user","content":"two"}}`,
		`{"type":"system","subtype":"turn_duration"}`,
	}, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if oneRound[0].EvidenceHash != twoRounds[0].EvidenceHash {
		t.Fatalf("first hash changed after append: %q != %q", oneRound[0].EvidenceHash, twoRounds[0].EvidenceHash)
	}
	changed, err := ParseTrace([]byte(strings.Replace(first, `"subtype":"turn_duration"`, `"subtype":"turn_duration","durationMs":99`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if oneRound[0].EvidenceHash == changed[0].EvidenceHash {
		t.Fatal("evidence hash did not change after event content changed")
	}
	changedFirstTrace := strings.Replace(first, `"content":"one"`, `"content":"one changed"`, 1) + "\n" + strings.Join([]string{
		`{"type":"user","promptId":"p-2","message":{"role":"user","content":"two"}}`,
		`{"type":"system","subtype":"turn_duration"}`,
	}, "\n")
	changedFirst, err := ParseTrace([]byte(changedFirstTrace))
	if err != nil {
		t.Fatal(err)
	}
	if twoRounds[1].EvidenceHash == changedFirst[1].EvidenceHash {
		t.Fatal("later round prefix hash did not change after earlier context changed")
	}
}

func TestMergeRoundsKeepsMatchingEvaluationAndInvalidatesChangedCapture(t *testing.T) {
	oldEval := Evaluation{ID: "eval-old", EvidenceHash: "old-hash", Status: "ready"}
	previous := []Round{{PromptID: "p", SessionID: "s", Prompt: "prompt", EvidenceHash: "old-hash", CaptureID: "capture-old", Evaluations: []Evaluation{oldEval}}}

	matching := MergeRounds(previous, []Round{{PromptID: "p", SessionID: "s", Prompt: "prompt", EvidenceHash: "old-hash", Status: "complete"}})
	if matching[0].CaptureID != "capture-old" || len(matching[0].Evaluations) != 1 {
		t.Fatalf("matching merge = %#v", matching[0])
	}

	changed := MergeRounds(previous, []Round{{PromptID: "p", SessionID: "s", Prompt: "prompt", EvidenceHash: "new-hash", Status: "complete"}})
	if changed[0].CaptureID != "" {
		t.Fatalf("changed CaptureID = %q, want invalidated", changed[0].CaptureID)
	}
	if len(changed[0].Evaluations) != 1 || changed[0].Evaluations[0].ID != "eval-old" {
		t.Fatalf("changed evaluation history = %#v, want preserved", changed[0].Evaluations)
	}
}

func TestMergeRoundsMarksPreviouslySeenMissingRoundAsConflict(t *testing.T) {
	previous := []Round{
		{PromptID: "p-1", SessionID: "s", Prompt: "one", Order: 1, Status: "complete", EvidenceHash: "h1"},
		{PromptID: "p-2", SessionID: "s", Prompt: "two", Order: 2, Status: "complete", EvidenceHash: "h2"},
	}
	merged := MergeRounds(previous, []Round{{PromptID: "p-2", SessionID: "s", Prompt: "two", Order: 1, Status: "complete", EvidenceHash: "h2"}})
	if len(merged) != 2 || merged[1].PromptID != "p-1" || merged[1].Status != "conflict" || !strings.Contains(merged[1].Reason, "missing") {
		t.Fatalf("merged = %#v", merged)
	}
}
