package annotation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	analysis "github.com/blueship581/pinru/internal/analysis"
	domain "github.com/blueship581/pinru/internal/annotation"
	"github.com/google/uuid"
)

const (
	defaultPairwiseHarness        = "Claude Code"
	defaultPairwiseHarnessVersion = "2.1.197"
	defaultPairwiseOS             = "MacOS/Linux"
	defaultPairwiseEnvironment    = "已容器化，可一键起环境"
)

type EnablePairwiseRequest struct {
	TaskID         string `json:"taskId"`
	Language       string `json:"language"`
	Harness        string `json:"harness"`
	HarnessVersion string `json:"harnessVersion"`
	OS             string `json:"os"`
	Environment    string `json:"environment"`
	Validity       string `json:"validity"`
}

type PairwiseSettingsRequest struct {
	TaskID      string `json:"taskId"`
	Language    string `json:"language"`
	Environment string `json:"environment"`
	Validity    string `json:"validity"`
	Notes       string `json:"notes"`
}

type PairwiseCaptureRequest struct {
	TaskID    string              `json:"taskId"`
	Side      domain.PairwiseSide `json:"side"`
	TracePath string              `json:"tracePath"`
}

type PairwiseMaterialsRequest struct {
	TaskID         string              `json:"taskId"`
	Side           domain.PairwiseSide `json:"side"`
	VideoURL       string              `json:"videoUrl"`
	VideoPath      string              `json:"videoPath"`
	RecordingError string              `json:"recordingError"`
}

type pairwiseTraceSelection struct {
	files     map[string][]byte
	main      string
	tracePath string
	rounds    []domain.Round
}

func selectPairwiseTrace(all map[string][]byte, binding *domain.Case, source, prompt string) (*pairwiseTraceSelection, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("题目提示词为空，无法自动匹配轨迹")
	}
	matches := make([]*pairwiseTraceSelection, 0, 1)
	for name, raw := range all {
		if !strings.HasSuffix(name, ".jsonl") || strings.Contains(name, "/subagents/") || len(raw) > 27*1024*1024 {
			continue
		}
		rounds, err := domain.ParseTrace(raw)
		if err != nil || len(rounds) != 1 || rounds[0].Status != "complete" || strings.TrimSpace(rounds[0].SessionID) == "" {
			continue
		}
		if err := validateTraceSource(binding, source, rounds, prompt); err != nil {
			continue
		}
		selectedFiles := map[string][]byte{name: raw}
		prefix := strings.TrimSuffix(name, ".jsonl") + "/"
		for attachmentName, data := range all {
			if strings.HasPrefix(attachmentName, prefix) {
				selectedFiles[attachmentName] = data
			}
		}
		matches = append(matches, &pairwiseTraceSelection{
			files: selectedFiles, main: name, tracePath: containerPathForArchive(name), rounds: rounds,
		})
	}
	if len(matches) == 0 {
		return nil, errors.New("未找到与本题提示词、仓库匹配且已完成的单轮轨迹")
	}
	if len(matches) > 1 {
		return nil, errors.New("找到多条与本题匹配的完整轨迹，无法自动确认 Session；请清理重复会话后重试")
	}
	return matches[0], nil
}

func (s *AnnotationService) EnablePairwise(req EnablePairwiseRequest) (*domain.Case, error) {
	unlock, err := s.lockTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c, err := s.loadCase(req.TaskID)
	if err != nil {
		return nil, err
	}
	if c.Mode == domain.CaseModeLegacy && (len(c.Rounds) > 0 || len(c.Captures) > 0 || c.SessionID != "") {
		return nil, errors.New("当前题目已有旧版标注证据，不能直接切换 Pair-wise 模式")
	}
	if c.TaskType == "代码理解" {
		return nil, errors.New("Pair-wise 模式暂不支持代码理解题")
	}
	task, err := s.store.GetTask(c.TaskID)
	if err != nil {
		return nil, err
	}
	prompt := ""
	if task != nil && task.PromptText != nil {
		prompt = *task.PromptText
	}
	if c.Pairwise == nil {
		c.Pairwise = domain.NewPairwiseData(prompt)
	}
	c.Mode = domain.CaseModePairwiseGSB
	c.Pairwise.Prompt = prompt
	c.Pairwise.Language = strings.TrimSpace(req.Language)
	c.Pairwise.Harness = strings.TrimSpace(req.Harness)
	c.Pairwise.HarnessVersion = strings.TrimSpace(req.HarnessVersion)
	c.Pairwise.OS = strings.TrimSpace(req.OS)
	c.Pairwise.Environment = strings.TrimSpace(req.Environment)
	c.Pairwise.Validity = strings.TrimSpace(req.Validity)
	populatePairwiseMetadata(c)
	return s.store.SaveAnnotationCase(*c, c.Revision)
}

func populatePairwiseMetadata(c *domain.Case) bool {
	if c == nil || c.Pairwise == nil {
		return false
	}
	changed := false
	if strings.TrimSpace(c.Pairwise.Language) == "" {
		c.Pairwise.Language = detectPairwiseLanguage(c.SourcePath)
		changed = true
	}
	if strings.TrimSpace(c.Pairwise.Harness) == "" {
		c.Pairwise.Harness = defaultPairwiseHarness
		changed = true
	}
	if strings.TrimSpace(c.Pairwise.HarnessVersion) == "" {
		c.Pairwise.HarnessVersion = defaultPairwiseHarnessVersion
		changed = true
	}
	if strings.TrimSpace(c.Pairwise.OS) == "" {
		c.Pairwise.OS = defaultPairwiseOS
		changed = true
	}
	if strings.TrimSpace(c.Pairwise.Environment) == "" {
		c.Pairwise.Environment = defaultPairwiseEnvironment
		changed = true
	}
	if strings.TrimSpace(c.Pairwise.Validity) == "" {
		c.Pairwise.Validity = domain.PairwiseValidityValid
		changed = true
	}
	return changed
}

func detectPairwiseLanguage(sourcePath string) string {
	summary, err := analysis.AnalyzeRepository(sourcePath)
	if err != nil {
		return "其他（自动识别失败）"
	}
	stack := make([]string, 0, len(summary.DetectedStack)+2)
	seen := map[string]bool{}
	add := func(value string) {
		if value != "" && value != "JavaScript 包管理" && value != "Docker" && value != "待识别项目" && !seen[value] {
			seen[value] = true
			stack = append(stack, value)
		}
	}
	for _, value := range summary.DetectedStack {
		add(value)
	}
	var manifest struct {
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	}
	if raw, readErr := os.ReadFile(filepath.Join(summary.RepoPath, "package.json")); readErr == nil && json.Unmarshal(raw, &manifest) == nil {
		dependencies := make(map[string]json.RawMessage, len(manifest.Dependencies)+len(manifest.DevDependencies))
		for name, version := range manifest.Dependencies {
			dependencies[strings.ToLower(name)] = version
		}
		for name, version := range manifest.DevDependencies {
			dependencies[strings.ToLower(name)] = version
		}
		for name, label := range map[string]string{"react": "React", "vue": "Vue", "next": "Next.js", "svelte": "Svelte"} {
			if _, ok := dependencies[name]; ok {
				add(label)
			}
		}
	}
	if len(stack) == 0 {
		return "其他（自动识别）"
	}
	return strings.Join(stack, ", ")
}

func (s *AnnotationService) SavePairwiseSettings(req PairwiseSettingsRequest) (*domain.Case, error) {
	unlock, err := s.lockTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c, err := s.loadCase(req.TaskID)
	if err != nil {
		return nil, err
	}
	if err := requirePairwiseCase(c); err != nil {
		return nil, err
	}
	c.Pairwise.Language = strings.TrimSpace(req.Language)
	c.Pairwise.Environment = strings.TrimSpace(req.Environment)
	c.Pairwise.Validity = strings.TrimSpace(req.Validity)
	c.Pairwise.Notes = strings.TrimSpace(req.Notes)
	return s.store.SaveAnnotationCase(*c, c.Revision)
}

func (s *AnnotationService) CapturePairwiseSide(ctx context.Context, req PairwiseCaptureRequest) (*domain.Case, error) {
	unlock, err := s.lockTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c, err := s.loadCase(req.TaskID)
	if err != nil {
		return nil, err
	}
	if err := requirePairwiseCase(c); err != nil {
		return nil, err
	}
	run, err := pairwiseRun(c.Pairwise, req.Side)
	if err != nil {
		return nil, err
	}
	source, err := s.verifyPairwiseBinding(ctx, c, run)
	if err != nil {
		return nil, err
	}
	before, err := domain.TreeHash(ctx, source)
	if err != nil {
		return nil, err
	}
	binding := *c
	binding.ContainerID = run.ContainerID
	binding.ContainerName = run.ContainerName
	binding.WorkspacePath = run.WorkspacePath
	binding.RepoRelativePath = run.RepoRelativePath
	tracePath := strings.TrimSpace(req.TracePath)
	var files map[string][]byte
	var main string
	var rounds []domain.Round
	if tracePath == "" {
		if binding.ContainerID == "" {
			return nil, errors.New("请先绑定该侧容器，再自动采集轨迹")
		}
		archive, err := s.command(ctx, "", "docker", "cp", binding.ContainerID+":"+containerTraceRoot, "-")
		if err != nil {
			return nil, err
		}
		all, err := archiveFiles(archive)
		if err != nil {
			return nil, err
		}
		selected, err := selectPairwiseTrace(all, &binding, source, c.Pairwise.Prompt)
		if err != nil {
			return nil, err
		}
		files, main, rounds, tracePath = selected.files, selected.main, selected.rounds, selected.tracePath
	} else {
		files, main, err = s.traceFiles(ctx, &binding, tracePath)
		if err != nil {
			return nil, err
		}
		if len(files[main]) > 27*1024*1024 {
			return nil, errors.New("Pair-wise 轨迹文件不能超过 27 MB")
		}
		rounds, err = domain.ParseTrace(files[main])
		if err != nil {
			return nil, err
		}
	}
	if len(rounds) != 1 || rounds[0].Status == "excluded" {
		return nil, errors.New("Pair-wise 每个 Session 必须且只能包含一轮有效交互")
	}
	if rounds[0].Status != "complete" {
		return nil, errors.New("该 Session 首轮尚未完整结束")
	}
	if err := validateTraceSource(&binding, source, rounds, c.Pairwise.Prompt); err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(rounds[0].SessionID)
	if sessionID == "" {
		return nil, errors.New("轨迹缺少真实 SessionID")
	}
	other, _ := pairwiseRun(c.Pairwise, oppositePairwiseSide(req.Side))
	if other.SessionID != "" && other.SessionID == sessionID {
		return nil, errors.New("A/B SessionID 必须不同")
	}
	id := uuid.NewString()
	dir := filepath.Join(s.caseDir(c.TaskID), "pairwise-captures", strings.ToLower(string(req.Side)), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = os.RemoveAll(dir)
		}
	}()
	codePath := filepath.Join(dir, "code")
	captureHash, err := domain.CopyEvidenceTree(ctx, source, codePath)
	if err != nil {
		return nil, err
	}
	for name, data := range files {
		target := filepath.Join(dir, "traces", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return nil, err
		}
	}
	after, err := domain.TreeHash(ctx, source)
	if err != nil {
		return nil, err
	}
	filesAfter, _, err := s.traceFiles(ctx, &binding, tracePath)
	if err != nil {
		return nil, err
	}
	if before != captureHash || captureHash != after || digestFiles(files) != digestFiles(filesAfter) {
		return nil, errors.New("代码或轨迹仍在变化，请等待模型完成后再采集")
	}
	traceRoot := filepath.Join(dir, "traces")
	traceHash, err := domain.TreeHash(ctx, traceRoot)
	if err != nil {
		return nil, err
	}
	manifest, err := json.MarshalIndent(rounds, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "rounds.json"), manifest, 0o600); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	run.SessionID = sessionID
	run.TracePath = tracePath
	run.TurnCount = 1
	run.CaptureID = id
	run.CaptureHash = captureHash
	run.TraceHash = traceHash
	run.RecordingGuide = nil
	run.RecordingGuideHash = ""
	run.RecordingGuideGeneratedAt = 0
	run.CapturedAt = now
	c.Captures = append(c.Captures, domain.Capture{
		ID: id, Dir: dir, TracePath: filepath.Join(traceRoot, filepath.FromSlash(main)),
		CodePath: codePath, Hash: captureHash, TraceHash: traceHash, CreatedAt: now,
	})
	saved, err := s.store.SaveAnnotationCase(*c, c.Revision)
	if err != nil {
		return nil, err
	}
	accepted = true
	return saved, nil
}

func (s *AnnotationService) CaptureAndCommitPairwiseSide(ctx context.Context, req PairwiseCaptureRequest) (*domain.Case, error) {
	captured, err := s.CapturePairwiseSide(ctx, req)
	if err != nil {
		return nil, err
	}
	run, err := pairwiseRun(captured.Pairwise, req.Side)
	if err != nil {
		return nil, err
	}
	return s.CommitPairwiseSide(ctx, PairwiseCommitRequest{
		TaskID: req.TaskID, Side: req.Side, SessionID: run.SessionID,
	})
}

const pairwiseVideoMaxBytes = 500 * 1024 * 1024

var pairwiseVideoExtensions = map[string]bool{".mp4": true, ".mov": true, ".webm": true, ".m4v": true}

// normalizePairwiseVideoSource strips wrappers that were pasted together with the
// value, such as the shell quotes around a dragged-in path, or a shell-escaped
// apostrophe. Only matching outer wrappers are removed, so a path that
// legitimately contains an apostrophe is preserved.
func normalizePairwiseVideoSource(raw string) string {
	value := strings.TrimSpace(raw)
	for pass := 0; pass < 3; pass++ {
		next := strings.TrimSpace(strings.ReplaceAll(value, `'\''`, `'`))
		for _, quote := range []string{"'", `"`, "`"} {
			if len(next) > 1 && strings.HasPrefix(next, quote) && strings.HasSuffix(next, quote) {
				next = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(next, quote), quote))
			}
		}
		if next == value {
			break
		}
		value = next
	}
	return value
}

func isPairwiseVideoURL(value string) bool {
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

func validatePairwiseVideoURL(raw string) (string, error) {
	value := normalizePairwiseVideoSource(raw)
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", errors.New("运行视频必须填写可访问的 HTTP(S) 链接")
	}
	return value, nil
}

func validatePairwiseVideoPath(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("运行录屏文件不存在或不是本机可访问的普通文件：" + path)
	}
	if ext := strings.ToLower(filepath.Ext(path)); !pairwiseVideoExtensions[ext] {
		return "", errors.New("运行录屏文件必须是 mp4、mov、webm 或 m4v 格式：" + path)
	}
	if info.Size() > pairwiseVideoMaxBytes {
		return "", fmt.Errorf("运行录屏文件不能超过 500 MB（当前 %.0f MB）：%s", float64(info.Size())/(1024*1024), path)
	}
	return path, nil
}

// resolvePairwiseVideoPath validates the unwrapped value first and falls back to
// the raw input, so a file whose real name is wrapped in quotes still works.
func resolvePairwiseVideoPath(raw string) (string, error) {
	candidates := make([]string, 0, 2)
	for _, candidate := range []string{normalizePairwiseVideoSource(raw), strings.TrimSpace(raw)} {
		if candidate == "" {
			continue
		}
		known := false
		for _, existing := range candidates {
			if existing == candidate {
				known = true
				break
			}
		}
		if !known {
			candidates = append(candidates, candidate)
		}
	}
	var lastErr error
	for _, candidate := range candidates {
		path, err := validatePairwiseVideoPath(candidate)
		if err == nil {
			return path, nil
		}
		lastErr = err
	}
	if len(candidates) > 1 {
		return "", fmt.Errorf("%v（原始输入：%s）", lastErr, candidates[1])
	}
	return "", lastErr
}

func (s *AnnotationService) SavePairwiseMaterials(req PairwiseMaterialsRequest) (*domain.Case, error) {
	unlock, err := s.lockTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c, err := s.loadCase(req.TaskID)
	if err != nil {
		return nil, err
	}
	if err := requirePairwiseCase(c); err != nil {
		return nil, err
	}
	run, err := pairwiseRun(c.Pairwise, req.Side)
	if err != nil {
		return nil, err
	}
	currentReview := domain.CurrentPairwiseReview(*c)
	videoURL := strings.TrimSpace(req.VideoURL)
	videoPath := strings.TrimSpace(req.VideoPath)
	if videoURL == "" && isPairwiseVideoURL(normalizePairwiseVideoSource(videoPath)) {
		// A quoted HTTP(S) link arrives in the path field; classify it after unwrapping.
		videoURL, videoPath = videoPath, ""
	}
	if videoURL != "" {
		value, err := validatePairwiseVideoURL(videoURL)
		if err != nil {
			return nil, err
		}
		videoURL, videoPath = value, ""
		run.VideoStatus = domain.PairwiseVideoReady
	} else if videoPath != "" {
		value, err := resolvePairwiseVideoPath(videoPath)
		if err != nil {
			return nil, err
		}
		videoPath, videoURL = value, ""
		run.VideoStatus = domain.PairwiseVideoReady
	} else if strings.TrimSpace(req.RecordingError) != "" {
		run.VideoStatus = domain.PairwiseVideoManualRequired
	} else {
		run.VideoStatus = domain.PairwiseVideoMissing
	}
	run.VideoURL = videoURL
	run.VideoPath = videoPath
	run.RecordingError = strings.TrimSpace(req.RecordingError)
	if currentReview != nil {
		currentReview.SourceHashA = domain.PairwiseRunSourceHash(c.Pairwise.RunA)
		currentReview.SourceHashB = domain.PairwiseRunSourceHash(c.Pairwise.RunB)
	}
	return s.store.SaveAnnotationCase(*c, c.Revision)
}
