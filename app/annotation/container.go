package annotation

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	domain "github.com/blueship581/pinru/internal/annotation"
	"github.com/blueship581/pinru/internal/gitops"
)

const containerTraceRoot = "/home/node/.claude/projects"

type Container struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	State         string `json:"state"`
	Image         string `json:"image"`
	WorkspacePath string `json:"workspacePath"`
}

type PairwiseBindRequest struct {
	TaskID           string              `json:"taskId"`
	Side             domain.PairwiseSide `json:"side"`
	ContainerID      string              `json:"containerId"`
	RepoRelativePath string              `json:"repoRelativePath"`
	CopyRepository   bool                `json:"copyRepository"`
}
type TraceCandidate struct {
	Path      string `json:"path"`
	SessionID string `json:"sessionId"`
	Size      int64  `json:"size"`
}
type dockerInfo struct {
	ID, Name, State, Image string
	Mounts                 []struct{ Type, Source, Destination string }
}

func (s *AnnotationService) inspectContainer(ctx context.Context, id string) (Container, error) {
	if id == "" || strings.HasPrefix(id, "-") {
		return Container{}, errors.New("请选择容器")
	}
	format := `{"ID":{{json .Id}},"Name":{{json .Name}},"State":{{json .State.Status}},"Image":{{json .Image}},"Mounts":{{json .Mounts}}}`
	b, err := s.command(ctx, "", "docker", "inspect", "--format", format, id)
	if err != nil {
		return Container{}, err
	}
	var d dockerInfo
	if err := json.Unmarshal(bytes.TrimSpace(b), &d); err != nil {
		return Container{}, err
	}
	c := Container{ID: d.ID, Name: strings.TrimPrefix(d.Name, "/"), State: d.State, Image: d.Image}
	for _, m := range d.Mounts {
		if m.Type == "bind" && m.Destination == "/workspace" {
			c.WorkspacePath = m.Source
		}
	}
	return c, nil
}

// ListContainers returns only non-sensitive metadata for bind-mounted workspaces.
func (s *AnnotationService) ListContainers() ([]Container, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30_000_000_000)
	defer cancel()
	b, err := s.command(ctx, "", "docker", "ps", "-aq")
	if err != nil {
		return nil, err
	}
	result := []Container{}
	for _, id := range strings.Fields(string(b)) {
		c, err := s.inspectContainer(ctx, id)
		if err != nil {
			return nil, err
		}
		if c.WorkspacePath != "" {
			result = append(result, c)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// BindContainer associates an existing container; it never starts or stops it.
func (s *AnnotationService) BindContainer(req BindRequest) (*domain.Case, error) {
	return s.bindContainer(context.Background(), req)
}

func (s *AnnotationService) bindPairwiseContainer(ctx context.Context, req PairwiseBindRequest) (*domain.Case, error) {
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
	if run.SessionID != "" || run.CaptureID != "" {
		return nil, errors.New("该侧已有采集证据，不能更换容器")
	}
	d, err := s.inspectContainer(ctx, req.ContainerID)
	if err != nil {
		return nil, err
	}
	if d.WorkspacePath == "" {
		return nil, errors.New("容器没有映射到本机的 /workspace")
	}
	other, _ := pairwiseRun(c.Pairwise, oppositePairwiseSide(req.Side))
	if other.ContainerID != "" && other.ContainerID == d.ID {
		return nil, errors.New("A/B 必须绑定不同容器")
	}
	all, err := s.store.ListAnnotationCases("")
	if err != nil {
		return nil, err
	}
	for _, candidate := range all {
		if candidate.TaskID == c.TaskID {
			continue
		}
		used := candidate.ContainerID == d.ID
		if candidate.Pairwise != nil {
			used = used || candidate.Pairwise.RunA.ContainerID == d.ID || candidate.Pairwise.RunB.ContainerID == d.ID
		}
		if used {
			return nil, errors.New("该容器已绑定其他题目")
		}
	}
	target, err := repositoryPath(d.WorkspacePath, req.RepoRelativePath)
	if err != nil {
		return nil, err
	}
	if req.CopyRepository {
		if d.State != "running" {
			return nil, errors.New("请先启动对应容器并停留在 Claude 输入界面")
		}
		if err := cloneInitialRepository(ctx, c.SourcePath, target, c.InitialSHA); err != nil {
			return nil, err
		}
	} else if err := verifyInitialAncestry(ctx, target, c.InitialSHA); err != nil {
		return nil, err
	}
	if err := gitops.PreparePairwiseBranch(ctx, target, string(req.Side), c.InitialSHA); err != nil {
		return nil, err
	}
	run.ContainerID = d.ID
	run.ContainerName = d.Name
	run.WorkspacePath = d.WorkspacePath
	run.RepoRelativePath = filepath.Clean(req.RepoRelativePath)
	run.ContainerCleared = false
	run.PreparedAt = time.Now().Unix()
	return s.store.SaveAnnotationCase(*c, c.Revision)
}
func (s *AnnotationService) bindContainer(ctx context.Context, req BindRequest) (*domain.Case, error) {
	unlock, err := s.lockTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c, err := s.loadCase(req.TaskID)
	if err != nil {
		return nil, err
	}
	d, err := s.inspectContainer(ctx, req.ContainerID)
	if err != nil {
		return nil, err
	}
	if d.WorkspacePath == "" {
		return nil, errors.New("容器没有映射到本机的 /workspace")
	}
	if c.ContainerID != "" && c.ContainerID != d.ID && len(c.Rounds) > 0 {
		return nil, errors.New("本题已有会话，不能关联到另一个容器")
	}
	if len(c.Rounds) > 0 && filepath.Clean(req.RepoRelativePath) != c.RepoRelativePath {
		return nil, errors.New("本题已有会话，不能更换执行仓库")
	}
	all, err := s.store.ListAnnotationCases("")
	if err != nil {
		return nil, err
	}
	for _, other := range all {
		if other.TaskID != c.TaskID && other.ContainerID == d.ID {
			return nil, errors.New("该容器已绑定其他题目")
		}
	}
	target, err := repositoryPath(d.WorkspacePath, req.RepoRelativePath)
	if err != nil {
		return nil, err
	}
	if req.CopyRepository {
		if d.State != "running" {
			return nil, errors.New("请先启动新容器并停留在 Claude 输入界面")
		}
		if len(c.Rounds) > 0 || c.InitialSHA == "" {
			return nil, errors.New("请先准备首轮前快照；已有轮次不能覆盖仓库")
		}
		archive, err := s.command(ctx, "", "docker", "cp", d.ID+":"+containerTraceRoot, "-")
		if err != nil {
			// A brand-new CLI may not create projects until its first input.
			// Only an explicit read-only absence check can turn a cp error into
			// an empty history; permissions/daemon errors still block the copy.
			if _, absentErr := s.command(ctx, "", "docker", "exec", d.ID, "test", "!", "-e", containerTraceRoot); absentErr != nil {
				return nil, err
			}
			archive = nil
		}
		entries, err := archiveFiles(archive)
		if err != nil {
			return nil, err
		}
		for name, data := range entries {
			if strings.HasSuffix(name, ".jsonl") && !strings.Contains(name, "/subagents/") {
				rounds, err := domain.ParseTrace(data)
				if err != nil {
					return nil, fmt.Errorf("核对启动状态：%w", err)
				}
				if len(rounds) > 0 {
					return nil, errors.New("容器已有用户输入，不能再登记首轮前导入")
				}
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return nil, err
		}
		if err := cloneInitialRepository(ctx, c.SourcePath, target, c.InitialSHA); err != nil {
			return nil, err
		}
	} else {
		info, err := os.Stat(target)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, errors.New("容器仓库不存在")
		}
		if err := verifyInitialAncestry(ctx, target, c.InitialSHA); err != nil {
			return nil, err
		}
	}
	c.ContainerID = d.ID
	c.ContainerName = d.Name
	c.WorkspacePath = d.WorkspacePath
	c.RepoRelativePath = filepath.Clean(req.RepoRelativePath)
	c.Completed = false
	return s.store.SaveAnnotationCase(*c, c.Revision)
}

func archiveFiles(data []byte) (map[string][]byte, error) {
	result := map[string][]byte{}
	tr := tar.NewReader(bytes.NewReader(data))
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := path.Clean(h.Name)
		if path.IsAbs(h.Name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") {
			return nil, errors.New("轨迹归档路径无效")
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return nil, errors.New("轨迹归档包含不支持的链接")
		}
		total += h.Size
		if h.Size < 0 || total > maxTraceBytes {
			return nil, errors.New("轨迹过大")
		}
		if _, ok := result[name]; ok {
			return nil, errors.New("重复轨迹路径")
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxTraceBytes+1))
		if err != nil {
			return nil, err
		}
		result[name] = b
	}
	return result, nil
}

func containerPathForArchive(name string) string {
	name = path.Clean(name)
	if name == "projects" {
		return containerTraceRoot
	}
	return containerTraceRoot + "/" + strings.TrimPrefix(name, "projects/")
}

// ListTraces lists candidate top-level sessions without guessing the target.
func (s *AnnotationService) ListTraces(taskID string) ([]TraceCandidate, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30_000_000_000)
	defer cancel()
	return s.listTraces(ctx, taskID)
}

func (s *AnnotationService) listTraces(ctx context.Context, taskID string) ([]TraceCandidate, error) {
	c, err := s.loadCase(taskID)
	if err != nil {
		return nil, err
	}
	if c.ContainerID == "" {
		return []TraceCandidate{}, nil
	}
	if _, err := s.verifyBinding(ctx, c); err != nil {
		return nil, err
	}
	b, err := s.command(ctx, "", "docker", "cp", c.ContainerID+":"+containerTraceRoot, "-")
	if err != nil {
		return nil, err
	}
	files, err := archiveFiles(b)
	if err != nil {
		return nil, err
	}
	out := []TraceCandidate{}
	for name, data := range files {
		if !strings.HasSuffix(name, ".jsonl") || strings.Contains(name, "/subagents/") {
			continue
		}
		out = append(out, TraceCandidate{Path: containerPathForArchive(name), SessionID: strings.TrimSuffix(path.Base(name), ".jsonl"), Size: int64(len(data))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (s *AnnotationService) verifyBinding(ctx context.Context, c *domain.Case) (string, error) {
	if c.ContainerID == "" {
		return c.SourcePath, nil
	}
	d, err := s.inspectContainer(ctx, c.ContainerID)
	if err != nil {
		return "", err
	}
	if d.ID != c.ContainerID || d.WorkspacePath != c.WorkspacePath {
		return "", errors.New("容器或挂载已变化，请重新核对绑定")
	}
	repo, err := repositoryPath(c.WorkspacePath, c.RepoRelativePath)
	if err != nil {
		return "", err
	}
	if err := verifyInitialAncestry(ctx, repo, c.InitialSHA); err != nil {
		return "", err
	}
	return repo, nil
}

func (s *AnnotationService) verifyPairwiseBinding(ctx context.Context, c *domain.Case, run *domain.PairwiseRun) (string, error) {
	if run.ContainerCleared {
		return "", errors.New("该侧容器已清除，请重新绑定容器")
	}
	if run.ContainerID == "" {
		return c.SourcePath, nil
	}
	binding := *c
	binding.ContainerID = run.ContainerID
	binding.ContainerName = run.ContainerName
	binding.WorkspacePath = run.WorkspacePath
	binding.RepoRelativePath = run.RepoRelativePath
	return s.verifyBinding(ctx, &binding)
}

func verifyInitialAncestry(ctx context.Context, repo, sha string) error {
	if sha == "" {
		return nil
	} // Historical imports remain incomplete at preflight.
	if !fullSHA.MatchString(sha) {
		return errors.New("初始 SHA 无效")
	}
	if _, err := runCommand(ctx, repo, "git", "merge-base", "--is-ancestor", sha, "HEAD"); err != nil {
		return errors.New("执行仓库不包含登记的初始提交，请核对题目与仓库的对应关系")
	}
	return nil
}
