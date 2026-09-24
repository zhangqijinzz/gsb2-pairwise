package annotation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	domain "github.com/blueship581/pinru/internal/annotation"
)

// ClearPairwiseContainer 清除该侧绑定的容器和宿主机运行目录，例如
//
//	docker rm -f cycgsb06-claude-11-a
//	rm -rf /Users/<user>/cycgsb06-claude-runs/run-11-a
//
// 采集到的轨迹、代码证据和制表需要的容器绑定信息都保留，只把该侧恢复成待绑定状态。
func (s *AnnotationService) ClearPairwiseContainer(req PairwiseSideRequest) (*domain.Case, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return s.clearPairwiseContainer(ctx, req)
}

func (s *AnnotationService) clearPairwiseContainer(ctx context.Context, req PairwiseSideRequest) (*domain.Case, error) {
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

	// 容器名优先，缺失时退回容器 ID，两者 docker rm -f 都接受。
	container := strings.TrimSpace(run.ContainerName)
	if container == "" {
		container = strings.TrimSpace(run.ContainerID)
	}
	folder := pairwiseRunFolder(run.WorkspacePath)
	if container == "" && folder == "" {
		return nil, errors.New("该侧没有可清除的容器或运行目录")
	}

	if err := s.removePairwiseContainer(ctx, container); err != nil {
		return nil, err
	}
	if err := removePairwiseRunFolder(folder); err != nil {
		return nil, err
	}

	run.ContainerCleared = true
	return s.store.SaveAnnotationCase(*c, c.Revision)
}

// pairwiseRunFolder 由容器里的 /workspace 绑定路径推出本次运行的宿主机目录。
// 只接受 <任意>/run-xxx-<side>/workspace 这种由启动命令创建的固定结构，
// 结构不匹配时返回空串，宁可少删也不误删其他目录。
func pairwiseRunFolder(workspacePath string) string {
	clean := filepath.Clean(strings.TrimSpace(workspacePath))
	if clean == "" || clean == "." || !filepath.IsAbs(clean) {
		return ""
	}
	if filepath.Base(clean) != "workspace" {
		return ""
	}
	folder := filepath.Dir(clean)
	if folder == string(filepath.Separator) || !strings.HasPrefix(filepath.Base(folder), "run-") {
		return ""
	}
	if len(strings.Split(strings.Trim(folder, string(filepath.Separator)), string(filepath.Separator))) < 3 {
		return ""
	}
	return folder
}

// removePairwiseContainer 删除容器；容器已经不存在时视为清理完成。
func (s *AnnotationService) removePairwiseContainer(ctx context.Context, container string) error {
	if container == "" {
		return nil
	}
	if _, err := s.command(ctx, "", "docker", "rm", "-f", container); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such container") {
			return nil
		}
		return fmt.Errorf("删除容器 %s 失败：%w", container, err)
	}
	return nil
}

// removePairwiseRunFolder 删除宿主机上的运行目录；目录已经不存在时视为清理完成。
func removePairwiseRunFolder(folder string) error {
	if folder == "" {
		return nil
	}
	if _, err := os.Stat(folder); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := os.RemoveAll(folder); err != nil {
		return fmt.Errorf("删除运行目录 %s 失败：%w", folder, err)
	}
	return nil
}
