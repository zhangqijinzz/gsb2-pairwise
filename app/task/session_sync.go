package task

import (
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/pinru/internal/errs"
	"github.com/blueship581/pinru/internal/store"
)

const defaultSessionSyncTaskType = "未归类"

type SyncTaskSessionsTarget struct {
	ModelName      string `json:"modelName"`
	ModelRunID     string `json:"modelRunId"`
	SessionCount   int    `json:"sessionCount"`
	WorkspacePath  string `json:"workspacePath"`
	MatchedPath    string `json:"matchedPath"`
	UserID         string `json:"userId"`
	Username       string `json:"username"`
	LastActivityAt *int64 `json:"lastActivityAt"`
}

type SyncTaskSessionsResult struct {
	TaskID             string                   `json:"taskId"`
	CandidateCount     int                      `json:"candidateCount"`
	UpdatedTargetCount int                      `json:"updatedTargetCount"`
	Targets            []SyncTaskSessionsTarget `json:"targets"`
}

func (s *TaskService) SyncLatestTaskSessions(taskID string) (*SyncTaskSessionsResult, error) {
	task, err := s.store.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf(errs.FmtCardNotFound, taskID)
	}

	modelRuns, err := s.store.ListModelRuns(taskID)
	if err != nil {
		return nil, err
	}

	extractResult, err := s.ExtractTaskSessions(taskID)
	if err != nil {
		return nil, err
	}

	result := &SyncTaskSessionsResult{
		TaskID:         taskID,
		CandidateCount: len(extractResult.Candidates),
		Targets:        []SyncTaskSessionsTarget{},
	}
	if len(extractResult.Candidates) == 0 {
		return result, nil
	}

	targetRuns, err := s.sessionSyncTargetRuns(task, modelRuns)
	if err != nil {
		return nil, err
	}

	if len(targetRuns) == 0 {
		bestCandidate := selectBestCandidateForPath(extractResult.Candidates, task.LocalPath)
		if bestCandidate == nil {
			return result, nil
		}

		nextSessions := buildTaskSessionsFromCandidate(*bestCandidate, task.SessionList, task.TaskType)
		if len(nextSessions) == 0 {
			return result, nil
		}
		if _, err := s.ensureTaskTypeChangeWithinUpperLimit(taskID, resolvedTaskTypeForSessionList(task.TaskType, nextSessions)); err != nil {
			return nil, err
		}
		if err := s.store.UpdateTaskSessionList(taskID, nextSessions); err != nil {
			return nil, err
		}

		result.UpdatedTargetCount = 1
		result.Targets = append(result.Targets, buildSyncTaskSessionsTarget("", "", *bestCandidate))
		return result, nil
	}

	for _, run := range targetRuns {
		bestCandidate := selectBestCandidateForPath(extractResult.Candidates, run.LocalPath)
		if bestCandidate == nil {
			continue
		}

		nextSessions := buildTaskSessionsFromCandidate(*bestCandidate, run.SessionList, task.TaskType)
		if len(nextSessions) == 0 {
			continue
		}
		if _, err := s.ensureTaskTypeChangeWithinUpperLimit(taskID, resolvedTaskTypeForSessionList(task.TaskType, nextSessions)); err != nil {
			return nil, err
		}
		if err := s.store.UpdateModelRunSessionList(taskID, run.ID, nextSessions); err != nil {
			return nil, err
		}

		result.UpdatedTargetCount += 1
		result.Targets = append(result.Targets, buildSyncTaskSessionsTarget(run.ModelName, run.ID, *bestCandidate))
	}

	return result, nil
}

func (s *TaskService) sessionSyncTargetRuns(task *store.Task, modelRuns []store.ModelRun) ([]store.ModelRun, error) {
	if len(modelRuns) == 0 {
		return nil, nil
	}

	sourceModelName := "ORIGIN"
	if task.ProjectConfigID != nil && strings.TrimSpace(*task.ProjectConfigID) != "" {
		project, err := s.store.GetProject(strings.TrimSpace(*task.ProjectConfigID))
		if err != nil {
			return nil, err
		}
		if project != nil && strings.TrimSpace(project.SourceModelFolder) != "" {
			sourceModelName = strings.TrimSpace(project.SourceModelFolder)
		}
	}

	runsWithPath := make([]store.ModelRun, 0, len(modelRuns))
	executionRuns := make([]store.ModelRun, 0, len(modelRuns))
	for _, run := range modelRuns {
		if run.LocalPath == nil || strings.TrimSpace(*run.LocalPath) == "" {
			continue
		}

		runsWithPath = append(runsWithPath, run)
		if isOriginModelName(run.ModelName) || isSourceModelFolder(run.ModelName, sourceModelName) {
			continue
		}
		executionRuns = append(executionRuns, run)
	}

	if len(executionRuns) > 0 {
		return executionRuns, nil
	}
	return runsWithPath, nil
}

func selectBestCandidateForPath(
	candidates []ExtractTaskSessionCandidate,
	localPath *string,
) *ExtractTaskSessionCandidate {
	if localPath == nil || strings.TrimSpace(*localPath) == "" {
		return nil
	}

	for index := range candidates {
		if candidateMatchesLocalPath(candidates[index], *localPath) {
			return &candidates[index]
		}
	}
	return nil
}

func candidateMatchesLocalPath(candidate ExtractTaskSessionCandidate, localPath string) bool {
	targets := []string{strings.TrimSpace(localPath)}
	if targets[0] == "" {
		return false
	}

	if _, _, _, ok := bestTraeWorkspacePathMatch(candidate.WorkspacePath, targets); ok {
		return true
	}
	if candidate.MatchedPath != "" {
		if _, _, _, ok := bestTraeWorkspacePathMatch(candidate.MatchedPath, targets); ok {
			return true
		}
	}
	return false
}

func buildTaskSessionsFromCandidate(
	candidate ExtractTaskSessionCandidate,
	previousSessions []store.TaskSession,
	fallbackTaskType string,
) []store.TaskSession {
	if len(candidate.Sessions) == 0 && len(previousSessions) == 0 {
		return nil
	}

	normalizedTaskType := strings.TrimSpace(fallbackTaskType)
	if normalizedTaskType == "" {
		normalizedTaskType = defaultSessionSyncTaskType
	}

	extractedAt := time.Now().Unix()
	sessions := make([]store.TaskSession, 0, maxInt(len(candidate.Sessions), len(previousSessions)))
	for _, previousSession := range previousSessions {
		next := previousSession
		next.SessionID = strings.TrimSpace(next.SessionID)
		next.TaskType = strings.TrimSpace(next.TaskType)
		next.Evaluation = strings.TrimSpace(next.Evaluation)
		next.UserConversation = strings.TrimSpace(next.UserConversation)
		next.Evidence = cloneTaskSessionEvidence(next.Evidence)
		sessions = append(sessions, next)
	}

	usedIndexes := make(map[int]struct{})
	canMergeByPosition := len(candidate.Sessions) >= len(previousSessions)
	for detectedIndex, extractedSession := range candidate.Sessions {
		targetIndex := findTaskSessionIndexBySessionID(sessions, extractedSession.SessionID, usedIndexes)
		if targetIndex < 0 && canMergeByPosition && detectedIndex < len(sessions) {
			targetIndex = detectedIndex
		}

		if targetIndex >= 0 {
			sessions[targetIndex] = mergeExtractedSessionIntoTaskSession(
				candidate,
				extractedSession,
				&sessions[targetIndex],
				targetIndex,
				normalizedTaskType,
				extractedAt,
			)
			usedIndexes[targetIndex] = struct{}{}
			continue
		}

		sessions = append(sessions, mergeExtractedSessionIntoTaskSession(
			candidate,
			extractedSession,
			nil,
			len(sessions),
			normalizedTaskType,
			extractedAt,
		))
	}

	for index := range sessions {
		if index == 0 {
			sessions[index].ConsumeQuota = true
		}
	}

	return sessions
}

func findTaskSessionIndexBySessionID(sessions []store.TaskSession, sessionID string, usedIndexes map[int]struct{}) int {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return -1
	}
	for index, session := range sessions {
		if _, used := usedIndexes[index]; used {
			continue
		}
		if strings.TrimSpace(session.SessionID) == trimmed {
			return index
		}
	}
	return -1
}

func mergeExtractedSessionIntoTaskSession(
	candidate ExtractTaskSessionCandidate,
	extractedSession ExtractedTraeSession,
	previousSession *store.TaskSession,
	index int,
	normalizedTaskType string,
	extractedAt int64,
) store.TaskSession {
	taskType := normalizedTaskType
	if previousSession != nil && strings.TrimSpace(previousSession.TaskType) != "" {
		taskType = strings.TrimSpace(previousSession.TaskType)
	}

	isCompleted := true
	if previousSession != nil && previousSession.IsCompleted != nil {
		isCompleted = *previousSession.IsCompleted
	}
	isSatisfied := true
	if previousSession != nil && previousSession.IsSatisfied != nil {
		isSatisfied = *previousSession.IsSatisfied
	}

	consumeQuota := index == 0
	if index > 0 && previousSession != nil {
		consumeQuota = previousSession.ConsumeQuota
	}

	userConversation := strings.TrimSpace(extractedSession.UserConversation)
	if userConversation == "" && previousSession != nil {
		userConversation = strings.TrimSpace(previousSession.UserConversation)
	}

	evaluation := ""
	if previousSession != nil {
		evaluation = strings.TrimSpace(previousSession.Evaluation)
	}

	return store.TaskSession{
		SessionID:        strings.TrimSpace(extractedSession.SessionID),
		TaskType:         taskType,
		ConsumeQuota:     consumeQuota,
		IsCompleted:      boolPtr(isCompleted),
		IsSatisfied:      boolPtr(isSatisfied),
		Evaluation:       evaluation,
		UserConversation: userConversation,
		Evidence:         buildTaskSessionEvidence(candidate, extractedSession, extractedAt),
	}
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func cloneTaskSessionEvidence(evidence *store.TaskSessionEvidence) *store.TaskSessionEvidence {
	if evidence == nil {
		return nil
	}
	next := *evidence
	if evidence.LastActivityAt != nil {
		value := *evidence.LastActivityAt
		next.LastActivityAt = &value
	}
	if evidence.ExtractedAt != nil {
		value := *evidence.ExtractedAt
		next.ExtractedAt = &value
	}
	return &next
}

func buildTaskSessionEvidence(
	candidate ExtractTaskSessionCandidate,
	extractedSession ExtractedTraeSession,
	extractedAt int64,
) *store.TaskSessionEvidence {
	username := strings.TrimSpace(candidate.Username)
	if username == "" {
		username = strings.TrimSpace(candidate.UserID)
	}

	extractedAtCopy := extractedAt
	var lastActivityAt *int64
	if extractedSession.LastActivityAt != nil {
		next := *extractedSession.LastActivityAt
		lastActivityAt = &next
	}

	return &store.TaskSessionEvidence{
		WorkspacePath:  strings.TrimSpace(candidate.WorkspacePath),
		MatchedPath:    strings.TrimSpace(candidate.MatchedPath),
		MatchKind:      strings.TrimSpace(candidate.MatchKind),
		UserID:         strings.TrimSpace(candidate.UserID),
		Username:       username,
		Summary:        strings.TrimSpace(candidate.Summary),
		IsCurrent:      extractedSession.IsCurrent,
		LastActivityAt: lastActivityAt,
		ExtractedAt:    &extractedAtCopy,
	}
}

func buildSyncTaskSessionsTarget(
	modelName string,
	modelRunID string,
	candidate ExtractTaskSessionCandidate,
) SyncTaskSessionsTarget {
	username := strings.TrimSpace(candidate.Username)
	if username == "" {
		username = strings.TrimSpace(candidate.UserID)
	}

	return SyncTaskSessionsTarget{
		ModelName:      strings.TrimSpace(modelName),
		ModelRunID:     strings.TrimSpace(modelRunID),
		SessionCount:   len(candidate.Sessions),
		WorkspacePath:  strings.TrimSpace(candidate.WorkspacePath),
		MatchedPath:    strings.TrimSpace(candidate.MatchedPath),
		UserID:         strings.TrimSpace(candidate.UserID),
		Username:       username,
		LastActivityAt: candidate.LastActivityAt,
	}
}

func isOriginModelName(modelName string) bool {
	return strings.EqualFold(strings.TrimSpace(modelName), "ORIGIN")
}

func isSourceModelFolder(modelName, sourceModelName string) bool {
	return strings.EqualFold(strings.TrimSpace(modelName), strings.TrimSpace(sourceModelName))
}

func boolPtr(value bool) *bool {
	next := value
	return &next
}
