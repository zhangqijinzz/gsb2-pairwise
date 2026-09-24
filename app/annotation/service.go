package annotation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	appcli "github.com/blueship581/pinru/app/cli"
	domain "github.com/blueship581/pinru/internal/annotation"
	"github.com/blueship581/pinru/internal/store"
)

type AnnotationService struct {
	store                   *store.Store
	cli                     *appcli.CliService
	root                    string
	pairwiseVideoDir        string
	pairwiseProjectStateDir string
	locks                   sync.Map
	command                 func(context.Context, string, string, ...string) ([]byte, error)
	verifySnapshot          func(context.Context, string) error
	publishInitial          func(context.Context, string, string, string, store.GitHubAccount) (string, error)
	pushPairwise            func(context.Context, string, string, string) error
	verifyPairwiseRemote    func(context.Context, domain.Case) error
}

func New(st *store.Store, cli *appcli.CliService) *AnnotationService {
	return &AnnotationService{
		store:                   st,
		cli:                     cli,
		root:                    filepath.Join(filepath.Dir(st.DBPath()), "annotation"),
		pairwiseVideoDir:        "/Users/cool/work/self-project/pairwise-videos",
		pairwiseProjectStateDir: defaultPairwiseProjectStateDir,
		command:                 runCommand,
		verifySnapshot:          verifyRemoteSnapshot,
		publishInitial:          publishInitial,
	}
}

type PrepareRequest struct {
	TaskID string `json:"taskId"`
}
type BindRequest struct {
	TaskID           string `json:"taskId"`
	ContainerID      string `json:"containerId"`
	RepoRelativePath string `json:"repoRelativePath"`
	CopyRepository   bool   `json:"copyRepository"`
}
type CaptureRequest struct {
	TaskID    string `json:"taskId"`
	TracePath string `json:"tracePath"`
}
type BatchPrepareRequest struct {
	ProjectID string   `json:"projectId"`
	TaskIDs   []string `json:"taskIds,omitempty"`
}
type BatchPrepareItem struct {
	TaskID   string `json:"taskId"`
	TaskName string `json:"taskName"`
	Status   string `json:"status"`
	Message  string `json:"message"`
}
type BatchPrepareResult struct {
	Total    int                `json:"total"`
	Prepared int                `json:"prepared"`
	Skipped  int                `json:"skipped"`
	Failed   int                `json:"failed"`
	Items    []BatchPrepareItem `json:"items"`
}
type ReviewRequest struct {
	TaskID   string `json:"taskId"`
	PromptID string `json:"promptId"`
	Force    bool   `json:"force"`
}
type SettingsRequest struct {
	TaskID      string `json:"taskId"`
	SnapshotURL string `json:"snapshotUrl"`
	Completed   bool   `json:"completed"`
}
type ExportRequest struct {
	TaskIDs      []string `json:"taskIds,omitempty"`
	TaskID       string   `json:"taskId,omitempty"`
	ReviewedOnly bool     `json:"reviewedOnly,omitempty"`
	ProjectID    string   `json:"projectId"`
	Submitter    string   `json:"submitter"`
	SubmittedAt  string   `json:"submittedAt"`
	Draft        bool     `json:"draft"`
}
type ExportResult struct {
	OutputPath string   `json:"outputPath"`
	ReportPath string   `json:"reportPath"`
	Rows       int      `json:"rows"`
	Issues     []string `json:"issues"`
}

func (s *AnnotationService) lockTask(id string) (func(), error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("请选择题目")
	}
	value, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, errors.New("该题目正在处理，请等待完成")
	}
	return mu.Unlock, nil
}

func stableKey(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:16])
}
func (s *AnnotationService) caseDir(id string) string {
	return filepath.Join(s.root, "cases", stableKey(id))
}

func (s *AnnotationService) loadCase(id string) (*domain.Case, error) {
	task, err := s.store.GetTask(id)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, errors.New("题目不存在")
	}
	c, err := s.store.GetAnnotationCase(id)
	if err != nil {
		return nil, err
	}
	if c != nil {
		c.TaskType = task.TaskType
		c.PromptDifficulty = task.PromptDifficulty
		return c, nil
	}
	project := ""
	if task.ProjectConfigID != nil {
		project = *task.ProjectConfigID
	}
	runs, err := s.store.ListModelRuns(id)
	if err != nil {
		return nil, err
	}
	source := ""
	for _, run := range runs {
		if run.LocalPath != nil && (source == "" || strings.EqualFold(run.ModelName, "ORIGIN")) {
			source = *run.LocalPath
		}
	}
	if source == "" && task.LocalPath != nil {
		source = *task.LocalPath
	}
	return &domain.Case{TaskID: id, ProjectID: project, TaskName: task.ProjectName, TaskType: task.TaskType, PromptDifficulty: task.PromptDifficulty, SourcePath: source, RepoRelativePath: filepath.Base(source), Rounds: []domain.Round{}, Captures: []domain.Capture{}}, nil
}

// ListCases includes every project task, including tasks not yet prepared.
func (s *AnnotationService) ListCases(projectID string) ([]domain.Case, error) {
	if strings.TrimSpace(projectID) == "" {
		return []domain.Case{}, nil
	}
	tasks, err := s.store.ListTasks(&projectID)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Case, 0, len(tasks))
	preparations, err := s.store.LatestAnnotationPreparations(projectID)
	if err != nil {
		return nil, err
	}
	_, skillHash, err := s.reviewSkill(context.Background())
	if err != nil {
		return nil, err
	}
	reviewExecution, modelErr := s.reviewExecution()
	for _, task := range tasks {
		c, err := s.loadCase(task.ID)
		if err != nil {
			return nil, err
		}
		populatePairwiseMetadata(c)
		c.Preparation = preparations[task.ID]
		markEvaluationFreshness(c, skillHash, reviewExecution.Label, modelErr == nil)
		markPairwiseReviewFreshness(c, reviewExecution.Label, modelErr == nil)
		result = append(result, *c)
	}
	return result, nil
}

func markPairwiseReviewFreshness(c *domain.Case, reviewLabel string, requireModel bool) {
	if c.Pairwise == nil {
		return
	}
	for i := range c.Pairwise.Reviews {
		review := &c.Pairwise.Reviews[i]
		current := review.Status == domain.PairwiseReviewReady && domain.PairwiseReviewMatchesRunSources(*review, c.Pairwise.RunA, c.Pairwise.RunB)
		if requireModel {
			current = current && review.Model == reviewLabel
		}
		review.Current = &current
	}
}

func markEvaluationFreshness(c *domain.Case, skillHash, reviewLabel string, requireModel bool) {
	for ri := range c.Rounds {
		r := &c.Rounds[ri]
		var sourceHash string
		for _, cap := range c.Captures {
			if cap.ID == r.CaptureID {
				sourceHash = stableKey(cap.Hash + ":" + cap.TraceHash)
				break
			}
		}
		// Earlier rounds captured together use the same reconstruction evidence
		// as reviewLocked; this does not assert an exact historical code state.
		if sourceHash == "" && len(c.Captures) > 0 {
			cap := c.Captures[len(c.Captures)-1]
			sourceHash = stableKey(cap.Hash + ":" + cap.TraceHash)
		}
		for ei := range r.Evaluations {
			e := &r.Evaluations[ei]
			// Model identity remains part of review-cache freshness even though a
			// completed record keeps its audit metadata after configuration changes.
			current := e.SkillHash == skillHash && e.EvidenceHash == r.EvidenceHash && sourceHash != "" && e.SourceHash == sourceHash
			if requireModel {
				current = current && e.Model == reviewLabel
			}
			e.Current = &current
		}
	}
}

// PrepareCase establishes the initial snapshot before a container is bound.
func (s *AnnotationService) PrepareCase(req PrepareRequest) (*domain.Case, error) {
	return s.prepareCase(context.Background(), req)
}
func (s *AnnotationService) PrepareCaseWithContext(ctx context.Context, req PrepareRequest) (*domain.Case, error) {
	return s.prepareCase(ctx, req)
}

func hasSessionEvidence(sessions []store.TaskSession) bool {
	for _, session := range sessions {
		if strings.TrimSpace(session.SessionID) != "" || strings.TrimSpace(session.UserConversation) != "" || strings.TrimSpace(session.Evaluation) != "" || session.Evidence != nil || session.IsCompleted != nil || session.IsSatisfied != nil {
			return true
		}
	}
	return false
}
func (s *AnnotationService) prepareCase(ctx context.Context, req PrepareRequest) (*domain.Case, error) {
	unlock, err := s.lockTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c, err := s.loadCase(req.TaskID)
	if err != nil {
		return nil, err
	}
	if len(c.Rounds) > 0 || c.ContainerID != "" {
		return nil, errors.New("已绑定或已有轨迹，不能补造初始快照")
	}
	t, err := s.store.GetTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	if t.Status == "ExecutionCompleted" || t.Status == "Submitted" || hasSessionEvidence(t.SessionList) {
		return nil, errors.New("已有执行记录，不能把当前代码登记为首轮前快照")
	}
	runs, err := s.store.ListModelRuns(req.TaskID)
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if hasSessionEvidence(r.SessionList) || (r.SessionID != nil && *r.SessionID != "") {
			return nil, errors.New("已有会话记录，请使用真实首轮前快照")
		}
	}
	sha, err := prepareRepository(ctx, c.SourcePath)
	if err != nil {
		return nil, err
	}
	c.InitialSHA = sha
	baseline := filepath.Join(s.caseDir(c.TaskID), "initial", sha)
	if _, err := os.Stat(baseline); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(baseline), 0700); err != nil {
			return nil, err
		}
		if err := cloneInitialRepository(ctx, c.SourcePath, baseline, sha); err != nil {
			return nil, err
		}
	}
	return s.store.SaveAnnotationCase(*c, c.Revision)
}

// SaveCaseSettings stores the actual snapshot link and explicit completion flag.
func (s *AnnotationService) SaveCaseSettings(req SettingsRequest) (*domain.Case, error) {
	unlock, err := s.lockTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c, err := s.loadCase(req.TaskID)
	if err != nil {
		return nil, err
	}
	url := strings.TrimSpace(req.SnapshotURL)
	if url != "" {
		sha, err := snapshotSHA(url)
		if err != nil {
			return nil, err
		}
		if c.InitialSHA != "" && sha != c.InitialSHA {
			return nil, errors.New("快照链接的提交与登记的初始 SHA 不一致")
		}
	}
	c.SnapshotURL = url
	c.Completed = req.Completed
	return s.store.SaveAnnotationCase(*c, c.Revision)
}

// ExecuteJob connects long operations to the application's existing job queue.
func (s *AnnotationService) ExecuteJob(ctx context.Context, kind, payload string) (any, error) {
	switch kind {
	case "annotation_resume":
		var r PrepareRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.resumeTable(ctx, r.TaskID)
	case "annotation_publish":
		var r PrepareRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.PublishSnapshot(ctx, r)
	case "annotation_prepare":
		var r PrepareRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.prepareCase(ctx, r)
	case "annotation_bind":
		var r BindRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.bindContainer(ctx, r)
	case "annotation_pairwise_enable":
		var r EnablePairwiseRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.EnablePairwise(r)
	case "annotation_pairwise_prepare_side":
		var r PairwiseSideRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.PreparePairwiseSide(ctx, r)
	case "annotation_pairwise_bind":
		var r PairwiseBindRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.bindPairwiseContainer(ctx, r)
	case "annotation_pairwise_clear":
		var r PairwiseSideRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.clearPairwiseContainer(ctx, r)
	case "annotation_pairwise_commit_side":
		var r PairwiseCommitRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.CommitPairwiseSide(ctx, r)
	case "annotation_pairwise_capture":
		var r PairwiseCaptureRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.CaptureAndCommitPairwiseSide(ctx, r)
	case "annotation_pairwise_record_video":
		var r PairwiseSideRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.RecordPairwiseVideo(ctx, r)
	case "annotation_pairwise_recording_guide":
		var r PairwiseSideRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.GeneratePairwiseRecordingGuide(ctx, r)
	case "annotation_pairwise_materials":
		var r PairwiseMaterialsRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.SavePairwiseMaterials(r)
	case "annotation_pairwise_settings":
		var r PairwiseSettingsRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.SavePairwiseSettings(r)
	case "annotation_capture", "annotation_capture_table":
		var r CaptureRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		if kind == "annotation_capture_table" {
			return s.captureAndPrepareTable(ctx, r)
		}
		return s.capture(ctx, r)
	case "annotation_batch_capture_table":
		var r BatchPrepareRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.batchCaptureAndPrepareTable(ctx, r)
	case "annotation_review":
		var r ReviewRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.review(ctx, r)
	case "annotation_pairwise_review":
		var r PairwiseReviewRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.ReviewPairwise(ctx, r)
	case "annotation_pairwise_batch_review":
		var r PairwiseBatchReviewRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.BatchReviewPairwise(ctx, r)
	case "annotation_export":
		var r ExportRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.export(ctx, r)
	case "annotation_pairwise_export":
		var r PairwiseExportRequest
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		return s.exportPairwise(ctx, r)
	default:
		return nil, fmt.Errorf("未知标注操作：%s", kind)
	}
}
