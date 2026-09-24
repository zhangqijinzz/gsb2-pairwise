package annotation

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/blueship581/pinru/internal/util"
)

const maxTraceBytes = 256 << 20

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, fmt.Errorf("命令输出超过 %d 字节限制", b.limit)
	}
	return b.Buffer.Write(p)
}

func runCommand(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	binary, err := util.ResolveCLI(name)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 5_000_000_000
	out := &boundedBuffer{limit: maxTraceBytes}
	errout := &boundedBuffer{limit: 16 << 10}
	cmd.Stdout = out
	cmd.Stderr = errout
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		detail := strings.TrimSpace(errout.String())
		if detail == "" {
			detail = strings.TrimSpace(out.String())
			if len(detail) > 16<<10 {
				detail = detail[:16<<10]
			}
		}
		return nil, fmt.Errorf("%s 执行失败：%w：%s", name, err, detail)
	}
	return out.Bytes(), nil
}

func repositoryPath(root, rel string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", errors.New("工作目录必须是绝对路径")
	}
	clean := filepath.Clean(rel)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("仓库路径必须是工作目录内的子目录")
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(realRoot, clean)
	check := realRoot
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		check = filepath.Join(check, part)
		info, err := os.Lstat(check)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("仓库路径不能经过符号链接")
		}
		if !info.IsDir() {
			return "", errors.New("仓库路径经过非目录文件")
		}
	}
	return target, nil
}

// unpackTraces keeps the Docker archive's directory structure and rejects links
// and path escapes. It never extracts arbitrary special files from containers.
func unpackTraces(data []byte, dest string) ([]string, error) {
	tr := tar.NewReader(bytes.NewReader(data))
	files := []string{}
	seen := map[string]bool{}
	var size int64
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
			return nil, errors.New("轨迹归档包含非法路径")
		}
		if name == "." {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return nil, err
			}
		case tar.TypeReg, tar.TypeRegA:
			if seen[name] {
				return nil, errors.New("轨迹归档包含重复文件")
			}
			seen[name] = true
			size += h.Size
			if h.Size < 0 || size > maxTraceBytes {
				return nil, errors.New("轨迹归档过大")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return nil, err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return nil, err
			}
			_, copyErr := io.CopyN(f, tr, h.Size)
			closeErr := f.Close()
			if copyErr != nil {
				return nil, copyErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
			files = append(files, target)
		default:
			return nil, errors.New("轨迹归档包含链接或特殊文件，未提取")
		}
	}
	return files, nil
}

func prepareRepository(ctx context.Context, dir string) (string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("题目路径不是目录")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		if _, err := runCommand(ctx, dir, "git", "init"); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	root, err := runCommand(ctx, dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	if filepath.Clean(strings.TrimSpace(string(root))) != real {
		return "", errors.New("题目必须使用独立 Git 仓库")
	}
	ignore := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(ignore)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	const marker = "# PINRU annotation baseline"
	if !strings.Contains(string(data), marker) {
		data = append(data, []byte("\n"+marker+"\nnode_modules/\n.DS_Store\n.env\n.env.*\n!.env.example\n!.env.sample\n")...)
		if err := os.WriteFile(ignore, data, 0644); err != nil {
			return "", err
		}
	}
	if _, err := runCommand(ctx, dir, "git", "add", "-A"); err != nil {
		return "", err
	}
	dirty, err := runCommand(ctx, dir, "git", "status", "--porcelain")
	if err != nil {
		return "", err
	}
	_, headErr := runCommand(ctx, dir, "git", "rev-parse", "--verify", "HEAD")
	if headErr != nil || len(bytes.TrimSpace(dirty)) > 0 {
		if _, err := runCommand(ctx, dir, "git", "-c", "user.name=PINRU Local", "-c", "user.email=pinru@local", "commit", "--allow-empty", "-m", "chore: initial annotation snapshot"); err != nil {
			return "", err
		}
	}
	b, err := runCommand(ctx, dir, "git", "rev-parse", "HEAD")
	return strings.TrimSpace(string(b)), err
}

func cloneInitialRepository(ctx context.Context, src, dst, sha string) error {
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		return errors.New("目标已存在，不能覆盖题目仓库")
	}
	status, err := runCommand(ctx, src, "git", "status", "--porcelain")
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(status)) != 0 {
		return errors.New("源仓库有未提交修改，请在首轮前重新准备初始快照")
	}
	head, err := runCommand(ctx, src, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(head)) != sha {
		return errors.New("源仓库 HEAD 与登记的初始快照不一致")
	}
	if _, err := runCommand(ctx, "", "git", "clone", "--no-hardlinks", "--no-checkout", "--", src, dst); err != nil {
		return err
	}
	// Keep a failed copy for inspection; never delete an existing user directory.
	if _, err := runCommand(ctx, dst, "git", "checkout", "--detach", sha); err != nil {
		return err
	}
	// Prevent a local origin path from pointing inside the author's machine.
	if _, err := runCommand(ctx, dst, "git", "remote", "remove", "origin"); err != nil {
		return err
	}
	b, err := runCommand(ctx, dst, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) != sha {
		return errors.New("复制后的初始快照不一致")
	}
	return nil
}
