package gitops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// PublishSnapshotCommit preserves existing remote history. A reused repository
// keeps each distinct initial commit under initial/<sha>; an empty one uses main.
func PublishSnapshotCommit(ctx context.Context, path, sha, username, token string) error {
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(sha) {
		return fmt.Errorf("初始提交 SHA 无效")
	}
	origin, err := runGitOutput(path, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = path
		cmd.Env = append(os.Environ(), buildGitAuthEnv(origin, username, token, false)...)
		cmd.WaitDelay = 5 * time.Second
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", formatGitCommandError(err, out, username, token)
		}
		return strings.TrimSpace(string(out)), nil
	}
	head, err := run("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != sha {
		return fmt.Errorf("发布副本 HEAD 与登记的初始提交不一致")
	}
	readRef := func(ref string) (string, error) {
		out, err := run("ls-remote", "--heads", "origin", ref)
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == ref {
				return fields[0], nil
			}
		}
		return "", nil
	}
	mainRef := "refs/heads/main"
	mainSHA, err := readRef(mainRef)
	if err != nil {
		return fmt.Errorf("读取初始快照仓库失败：%w", err)
	}
	if mainSHA == sha {
		return nil
	}
	if mainSHA == "" {
		// An empty lease is a create-only condition, never permission to overwrite.
		if _, pushErr := run("push", "--force-with-lease="+mainRef+":", "origin", sha+":"+mainRef); pushErr == nil {
			return nil
		} else {
			// Another publisher may have created main after ls-remote.
			mainSHA, err = readRef(mainRef)
			if err != nil || mainSHA == "" {
				return fmt.Errorf("发布初始快照失败：%w", pushErr)
			}
			if mainSHA == sha {
				return nil
			}
		}
	}
	ref := "refs/heads/initial/" + sha
	existing, err := readRef(ref)
	if err != nil {
		return err
	}
	if existing == sha {
		return nil
	}
	if existing != "" {
		return fmt.Errorf("初始快照分支 %s 已指向其他提交，已停止发布，请核对仓库", ref)
	}
	if _, err := run("push", "--force-with-lease="+ref+":", "origin", sha+":"+ref); err != nil {
		if current, readErr := readRef(ref); readErr == nil && current == sha {
			return nil
		}
		return fmt.Errorf("保留原仓库历史并发布初始快照失败：%w", err)
	}
	return nil
}
