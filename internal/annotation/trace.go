package annotation

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

type traceMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	StopReason string          `json:"stop_reason"`
	PromptID   string          `json:"promptId"`
}

type traceEvent struct {
	Type             string       `json:"type"`
	Subtype          string       `json:"subtype"`
	UUID             string       `json:"uuid"`
	ParentUUID       string       `json:"parentUuid"`
	PromptID         string       `json:"promptId"`
	PromptIDAlt      string       `json:"prompt_id"`
	SessionID        string       `json:"sessionId"`
	Version          string       `json:"version"`
	Cwd              string       `json:"cwd"`
	IsSidechain      bool         `json:"isSidechain"`
	IsMeta           bool         `json:"isMeta"`
	IsCompactSummary bool         `json:"isCompactSummary"`
	Message          traceMessage `json:"message"`
}

type parsedTraceLine struct {
	number      int
	raw         []byte
	event       traceEvent
	human       bool
	prompt      string
	attachments []string
	conflict    string
}

// ParseTrace extracts real top-level human turns from a Claude Code JSONL trace.
// A standalone recovery command such as "继续" extends the preceding prompt's
// evidence range instead of creating a separately exportable annotation round.
// It returns an error for any malformed non-empty line so callers never persist a
// plausible-looking prefix of a trace that was still being written.
func ParseTrace(data []byte) ([]Round, error) {
	lines, err := decodeTraceLines(data)
	if err != nil {
		return nil, err
	}

	rounds := make([]Round, 0)
	var current *Round
	prefixRaw := make([][]byte, 0, len(lines))
	currentComplete := false
	currentConflict := ""
	currentUserUUID := ""
	currentParentUUID := ""

	finish := func(end int) {
		if current == nil {
			return
		}
		current.SourceEnd = end
		current.EvidenceHash = hashTraceLines(prefixRaw)
		if current.PromptID == "" {
			current.Status = "conflict"
			current.Reason = "human prompt is missing promptId"
		} else if currentConflict != "" {
			current.Status = "conflict"
			current.Reason = currentConflict
		} else if currentComplete {
			current.Status = "complete"
		} else {
			current.Status = "pending"
		}
		rounds = append(rounds, *current)
		current = nil
		currentComplete = false
		currentConflict = ""
		currentUserUUID = ""
		currentParentUUID = ""
	}

	for _, line := range lines {
		if line.human {
			promptID := firstNonEmpty(line.event.PromptID, line.event.PromptIDAlt, line.event.Message.PromptID)
			exactReplay := current != nil && current.PromptID == promptID && current.Prompt == line.prompt &&
				slices.Equal(current.Attachments, line.attachments) &&
				(current.SessionID == "" || line.event.SessionID == "" || current.SessionID == line.event.SessionID) &&
				currentUserUUID != "" && currentUserUUID == line.event.UUID && currentParentUUID == line.event.ParentUUID
			if exactReplay {
				// Claude can repeat a user envelope while continuing the same event
				// chain. A matching UUID and parent prove this is the same event.
				fillRoundMetadata(current, line.event)
				continue
			}
			continuingCurrent := current != nil && isContinuationPrompt(line.prompt, line.attachments) &&
				(current.SessionID == "" || line.event.SessionID == "" || current.SessionID == line.event.SessionID)
			if continuingCurrent {
				// The user is only asking the interrupted model to resume the same
				// task. Keep the original prompt identity, but include this event and
				// everything after it in the evidence reviewed for that task.
				fillRoundMetadata(current, line.event)
				currentComplete = false
				prefixRaw = append(prefixRaw, line.raw)
				continue
			}
			finish(line.number - 1)
			current = &Round{
				PromptID:    promptID,
				SessionID:   line.event.SessionID,
				Prompt:      line.prompt,
				Order:       len(rounds) + 1,
				SourceStart: line.number,
				Version:     line.event.Version,
				Cwd:         line.event.Cwd,
				Attachments: append(make([]string, 0, len(line.attachments)), line.attachments...),
				Evaluations: make([]Evaluation, 0),
			}
			currentConflict = line.conflict
			currentUserUUID = line.event.UUID
			currentParentUUID = line.event.ParentUUID
			prefixRaw = append(prefixRaw, line.raw)
			continue
		}

		prefixRaw = append(prefixRaw, line.raw)
		if current == nil {
			continue
		}
		fillRoundMetadata(current, line.event)
		if isTrustworthyTurnEnd(line.event) {
			currentComplete = true
		}
	}
	if current != nil {
		finish(lines[len(lines)-1].number)
	}

	markPromptIDConflicts(rounds)
	return rounds, nil
}

func isContinuationPrompt(prompt string, attachments []string) bool {
	if len(attachments) > 0 {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	normalized = strings.TrimSpace(strings.Trim(normalized, "。.!！?？"))
	switch normalized {
	case "继续", "请继续", "继续吧", "请继续吧", "接着做", "请接着做", "继续完成", "请继续完成", "continue", "please continue", "go on", "please go on":
		return true
	default:
		return false
	}
}

// IsPureRecoveryRound reports whether a legacy persisted round contains only a
// command to resume the preceding task. Such rows belong to the original
// prompt's evidence chain and must never be reviewed or exported separately.
func IsPureRecoveryRound(round Round) bool {
	return isContinuationPrompt(round.Prompt, round.Attachments)
}

func decodeTraceLines(data []byte) ([]parsedTraceLine, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Claude tool results can be large; keep a bounded but practical line size.
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	lines := make([]parsedTraceLine, 0)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		raw := bytes.TrimSuffix(scanner.Bytes(), []byte{'\r'})
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var event traceEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, fmt.Errorf("parse Claude trace line %d: %w", lineNumber, err)
		}
		prompt, attachments, human, conflict := humanPrompt(event)
		lines = append(lines, parsedTraceLine{
			number:      lineNumber,
			raw:         bytes.Clone(raw),
			event:       event,
			human:       human,
			prompt:      prompt,
			attachments: attachments,
			conflict:    conflict,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read Claude trace: %w", err)
	}
	return lines, nil
}

func humanPrompt(event traceEvent) (string, []string, bool, string) {
	if event.Type != "user" || event.IsSidechain || event.IsMeta || event.IsCompactSummary || event.Message.Role != "user" {
		return "", nil, false, ""
	}
	var text string
	if err := json.Unmarshal(event.Message.Content, &text); err == nil {
		return text, make([]string, 0), true, ""
	}

	var blocks []json.RawMessage
	if err := json.Unmarshal(event.Message.Content, &blocks); err != nil || len(blocks) == 0 {
		return "", nil, false, ""
	}
	var joined strings.Builder
	attachments := make([]string, 0)
	for _, rawBlock := range blocks {
		var block struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			return "", nil, false, ""
		}
		if block.Type == "tool_result" {
			return "", nil, false, ""
		}
		if block.Type == "text" {
			joined.WriteString(block.Text)
			continue
		}
		attachments = append(attachments, attachmentReference(rawBlock, block.Type))
	}
	prompt := joined.String()
	if joined.Len() == 0 && len(attachments) > 0 {
		return prompt, attachments, true, "human prompt contains only non-text attachment content"
	}
	return prompt, attachments, true, ""
}

func attachmentReference(raw json.RawMessage, blockType string) string {
	if blockType == "" {
		blockType = "unknown"
	}
	var metadata struct {
		Name     string `json:"name"`
		FileName string `json:"file_name"`
		Source   struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			URL       string `json:"url"`
			Path      string `json:"path"`
			FileID    string `json:"file_id"`
		} `json:"source"`
	}
	_ = json.Unmarshal(raw, &metadata)
	parts := []string{"attachment:" + blockType}
	for _, value := range []string{metadata.Name, metadata.FileName, metadata.Source.Type, metadata.Source.MediaType, metadata.Source.URL, metadata.Source.Path, metadata.Source.FileID} {
		if value != "" {
			parts = append(parts, value)
		}
	}
	digest := sha256.Sum256(raw)
	parts = append(parts, "sha256="+hex.EncodeToString(digest[:]))
	return "[" + strings.Join(parts, " ") + "]"
}

func isTrustworthyTurnEnd(event traceEvent) bool {
	if event.IsSidechain || event.IsMeta {
		return false
	}
	if event.Type == "system" && event.Subtype == "turn_duration" {
		return true
	}
	if event.Type != "assistant" || event.Message.Role != "assistant" || event.Message.StopReason != "end_turn" {
		return false
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(event.Message.Content, &blocks); err != nil {
		return false
	}
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			return true
		}
	}
	return false
}

func hashTraceLines(lines [][]byte) string {
	hash := sha256.New()
	for _, line := range lines {
		hash.Write(line)
		hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func markPromptIDConflicts(rounds []Round) {
	type promptIdentity struct {
		sessionID string
		promptID  string
	}
	texts := make(map[promptIdentity]string)
	counts := make(map[promptIdentity]int)
	conflicts := make(map[promptIdentity]bool)
	for _, round := range rounds {
		if round.PromptID == "" {
			continue
		}
		key := promptIdentity{round.SessionID, round.PromptID}
		counts[key]++
		if previous, ok := texts[key]; ok && previous != round.Prompt {
			conflicts[key] = true
		} else if !ok {
			texts[key] = round.Prompt
		}
	}
	for index := range rounds {
		key := promptIdentity{rounds[index].SessionID, rounds[index].PromptID}
		if conflicts[key] || counts[key] > 1 {
			rounds[index].Status = "conflict"
			if conflicts[key] {
				rounds[index].Reason = "same promptId has conflicting human prompt content"
			} else {
				rounds[index].Reason = "promptId is reused in separate human event chains"
			}
		}
	}
}

func fillRoundMetadata(round *Round, event traceEvent) {
	if round.SessionID == "" {
		round.SessionID = event.SessionID
	}
	if round.Version == "" {
		round.Version = event.Version
	}
	if round.Cwd == "" {
		round.Cwd = event.Cwd
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// MergeRounds combines a newly parsed trace with persisted annotation state.
// Evaluation versions remain as history when evidence changes, while the active
// capture link is retained only for byte-identical evidence.
func MergeRounds(previous, incoming []Round) []Round {
	result := cloneRounds(incoming)
	type identity struct {
		sessionID string
		promptID  string
		prompt    string
	}
	keyFor := func(round Round) identity {
		key := identity{sessionID: round.SessionID, promptID: round.PromptID}
		if round.PromptID == "" {
			key.prompt = round.Prompt
		}
		return key
	}
	available := make(map[identity][]int)
	for index, round := range previous {
		available[keyFor(round)] = append(available[keyFor(round)], index)
	}
	used := make([]bool, len(previous))

	for index := range result {
		key := keyFor(result[index])
		candidates := available[key]
		if len(candidates) == 0 {
			continue
		}
		previousIndex := candidates[0]
		available[key] = candidates[1:]
		used[previousIndex] = true
		old := previous[previousIndex]
		result[index].Evaluations = mergeEvaluations(old.Evaluations, result[index].Evaluations)
		if old.EvidenceHash == result[index].EvidenceHash && result[index].CaptureID == "" {
			result[index].CaptureID = old.CaptureID
		}
	}

	for index, old := range previous {
		if used[index] {
			continue
		}
		if isContinuationPrompt(old.Prompt, old.Attachments) {
			// Older parser versions stored recovery commands as independent
			// rounds. A fresh capture folds that evidence into the prior prompt,
			// so the legacy row must not survive as a conflict/export row.
			continue
		}
		missing := old
		missing.Attachments = append(make([]string, 0, len(old.Attachments)), old.Attachments...)
		missing.Order = len(result) + 1
		missing.Status = "conflict"
		missing.Reason = "previously seen turn is missing from the current trace"
		missing.CaptureID = ""
		result = append(result, missing)
	}
	return result
}

func cloneRounds(rounds []Round) []Round {
	cloned := make([]Round, len(rounds))
	for index, round := range rounds {
		cloned[index] = round
		cloned[index].Attachments = append(make([]string, 0, len(round.Attachments)), round.Attachments...)
		cloned[index].Evaluations = append(make([]Evaluation, 0, len(round.Evaluations)), round.Evaluations...)
	}
	return cloned
}

func mergeEvaluations(previous, incoming []Evaluation) []Evaluation {
	merged := append(make([]Evaluation, 0, len(previous)+len(incoming)), previous...)
	seen := make(map[string]bool, len(merged))
	for _, evaluation := range merged {
		if evaluation.ID != "" {
			seen[evaluation.ID] = true
		}
	}
	for _, evaluation := range incoming {
		if evaluation.ID != "" && seen[evaluation.ID] {
			continue
		}
		merged = append(merged, evaluation)
		if evaluation.ID != "" {
			seen[evaluation.ID] = true
		}
	}
	return merged
}
