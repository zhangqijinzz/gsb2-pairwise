package annotation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	domain "github.com/blueship581/pinru/internal/annotation"
	"github.com/google/uuid"
)

func digestFiles(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		fmt.Fprintf(h, "%d:%s:%d:", len(n), n, len(files[n]))
		h.Write(files[n])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *AnnotationService) traceFiles(ctx context.Context, c *domain.Case, trace string) (map[string][]byte, string, error) {
	if strings.HasPrefix(trace, containerTraceRoot+"/") {
		if c.ContainerID == "" {
			return nil, "", errors.New("容器轨迹需要先绑定题目")
		}
		if path.Clean(trace) != trace || !strings.HasSuffix(trace, ".jsonl") || strings.Contains(trace, "/subagents/") {
			return nil, "", errors.New("请选择完整的主会话 JSONL 文件")
		}
		data, err := s.command(ctx, "", "docker", "cp", c.ContainerID+":"+containerTraceRoot, "-")
		if err != nil {
			return nil, "", err
		}
		all, err := archiveFiles(data)
		if err != nil {
			return nil, "", err
		}
		name := "projects/" + strings.TrimPrefix(trace, containerTraceRoot+"/")
		raw, ok := all[name]
		if !ok {
			return nil, "", errors.New("指定轨迹不在容器中")
		}
		out := map[string][]byte{name: raw}
		prefix := strings.TrimSuffix(name, ".jsonl") + "/"
		for n, b := range all {
			if strings.HasPrefix(n, prefix) {
				out[n] = b
			}
		}
		return out, name, nil
	}
	if !filepath.IsAbs(trace) || !strings.HasSuffix(trace, ".jsonl") {
		return nil, "", errors.New("本地轨迹必须是绝对路径的 JSONL 文件")
	}
	info, err := os.Lstat(trace)
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxTraceBytes {
		return nil, "", errors.New("轨迹文件类型或大小不支持")
	}
	raw, err := os.ReadFile(trace)
	if err != nil {
		return nil, "", err
	}
	name := filepath.Base(trace)
	out := map[string][]byte{name: raw}
	total := int64(len(raw))
	sub := strings.TrimSuffix(trace, ".jsonl")
	if info, err := os.Stat(sub); err == nil && info.IsDir() {
		err = filepath.WalkDir(sub, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("轨迹附件不能包含符号链接")
			}
			if d.IsDir() {
				return nil
			}
			i, e := d.Info()
			if e != nil {
				return e
			}
			if !i.Mode().IsRegular() || i.Size() > maxTraceBytes {
				return errors.New("轨迹附件类型或大小不支持")
			}
			total += i.Size()
			if total > maxTraceBytes {
				return errors.New("轨迹附件总量过大")
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			rel, e := filepath.Rel(filepath.Dir(trace), p)
			if e != nil {
				return e
			}
			out[filepath.ToSlash(rel)] = b
			return nil
		})
		if err != nil {
			return nil, "", err
		}
	}
	return out, name, nil
}

// Capture freezes a selected transcript and checks the code is not changing.
func (s *AnnotationService) Capture(req CaptureRequest) (*domain.Case, error) {
	return s.capture(context.Background(), req)
}
func (s *AnnotationService) capture(ctx context.Context, req CaptureRequest) (*domain.Case, error) {
	unlock, err := s.lockTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.captureLocked(ctx, req)
}

// captureLocked requires the caller to hold the task lock.
func (s *AnnotationService) captureLocked(ctx context.Context, req CaptureRequest) (*domain.Case, error) {
	c, err := s.loadCase(req.TaskID)
	if err != nil {
		return nil, err
	}
	source, err := s.verifyBinding(ctx, c)
	if err != nil {
		return nil, err
	}
	before, err := domain.TreeHash(ctx, source)
	if err != nil {
		return nil, err
	}
	files, main, err := s.traceFiles(ctx, c, req.TracePath)
	if err != nil {
		return nil, err
	}
	rounds, err := domain.ParseTrace(files[main])
	if err != nil {
		return nil, err
	}
	if len(rounds) == 0 {
		return nil, errors.New("轨迹没有可识别的用户输入")
	}
	task, err := s.store.GetTask(c.TaskID)
	if err != nil {
		return nil, err
	}
	prompt := ""
	if task.PromptText != nil {
		prompt = *task.PromptText
	}
	if err := validateTraceSource(c, source, rounds, prompt); err != nil {
		return nil, err
	}
	session := ""
	for _, r := range rounds {
		if r.SessionID != "" {
			if session != "" && session != r.SessionID {
				return nil, errors.New("轨迹包含多个会话，不能合并为同一题")
			}
			session = r.SessionID
		}
	}
	if session == "" {
		return nil, errors.New("轨迹缺少真实 SessionID")
	}
	if c.SessionID != "" && c.SessionID != session {
		return nil, errors.New("该题已关联其他会话，不能覆盖原始轮次")
	}
	// A live copy cannot certify an earlier round's code after later rounds ran.
	id := uuid.NewString()
	dir := filepath.Join(s.caseDir(c.TaskID), "captures", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = os.RemoveAll(dir)
		}
	}()
	code := filepath.Join(dir, "code")
	hash, err := domain.CopyEvidenceTree(ctx, source, code)
	if err != nil {
		return nil, err
	}
	for name, data := range files {
		target := filepath.Join(dir, "traces", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			return nil, err
		}
	}
	after, err := domain.TreeHash(ctx, source)
	if err != nil {
		return nil, err
	}
	filesAfter, _, err := s.traceFiles(ctx, c, req.TracePath)
	if err != nil {
		return nil, err
	}
	if before != hash || hash != after || digestFiles(files) != digestFiles(filesAfter) {
		return nil, errors.New("代码或轨迹仍在变化，请等待 Claude 完成本轮后再采集")
	}
	traceHash, err := domain.TreeHash(ctx, filepath.Join(dir, "traces"))
	if err != nil {
		return nil, err
	}
	merged := domain.MergeRounds(c.Rounds, rounds)
	last := len(merged) - 1
	if last >= 0 && merged[last].Status == "complete" {
		for _, old := range c.Rounds {
			if old.PromptID == merged[last].PromptID && old.EvidenceHash == merged[last].EvidenceHash && old.CaptureID != "" {
				for _, cap := range c.Captures {
					if cap.ID == old.CaptureID {
						if cap.Hash != hash {
							return nil, errors.New("日志未变但代码已变化，不能把会话后的修改计入模型成绩")
						}
						if cap.TraceHash == traceHash {
							if err := verifyTraceArtifacts(ctx, cap); err != nil {
								return nil, err
							}
							storedHash, err := domain.TreeHash(ctx, cap.CodePath)
							if err != nil || storedHash != cap.Hash {
								return nil, errors.New("已保存的代码证据被修改")
							}
							return c, nil
						}
					}
				}
			}
		}
		merged[last].CaptureID = id
	}
	c.SessionID = session
	c.TracePath = req.TracePath
	c.Rounds = merged
	c.Completed = false
	c.Captures = append(c.Captures, domain.Capture{ID: id, Dir: dir, TracePath: filepath.Join(dir, "traces", filepath.FromSlash(main)), CodePath: code, Hash: hash, TraceHash: traceHash, CreatedAt: time.Now().Unix()})
	manifest, err := json.MarshalIndent(c.Rounds, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "rounds.json"), manifest, 0600); err != nil {
		return nil, err
	}
	saved, err := s.store.SaveAnnotationCase(*c, c.Revision)
	if err != nil {
		return nil, err
	}
	accepted = true
	return saved, nil
}
