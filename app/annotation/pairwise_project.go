package annotation

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	domain "github.com/blueship581/pinru/internal/annotation"
)

const (
	pairwiseProjectProxyPortMin = 1024
	pairwiseProjectProxyPortMax = 9999
	// defaultPairwiseProjectStateDir holds the host-side runtime files of the
	// copied startup command: the host proxy port and its pid.
	defaultPairwiseProjectStateDir = "/tmp"
)

var vitePortPattern = regexp.MustCompile(`(?m)\bport\s*:\s*([0-9]{2,5})`)

type pairwiseProjectLaunch struct {
	Command string
	Port    int
	Host    string
}

type PairwiseProjectState struct {
	Side    domain.PairwiseSide `json:"side"`
	Running bool                `json:"running"`
	URL     string              `json:"url"`
	Command string              `json:"command"`
}

func detectPairwiseProjectLaunch(repo string) (pairwiseProjectLaunch, error) {
	raw, err := os.ReadFile(filepath.Join(repo, "package.json"))
	if err != nil {
		return pairwiseProjectLaunch{}, errors.New("暂只支持 package.json 中声明了 dev 脚本的项目")
	}
	var manifest struct {
		Scripts         map[string]string          `json:"scripts"`
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return pairwiseProjectLaunch{}, errors.New("package.json 无法解析")
	}
	if strings.TrimSpace(manifest.Scripts["dev"]) == "" {
		return pairwiseProjectLaunch{}, errors.New("package.json 没有 dev 启动脚本")
	}
	manager := "npm run dev"
	if _, err := os.Stat(filepath.Join(repo, "pnpm-lock.yaml")); err == nil {
		manager = "corepack pnpm run dev"
	} else if _, err := os.Stat(filepath.Join(repo, "yarn.lock")); err == nil {
		manager = "corepack yarn dev"
	}
	port := 0
	viteDetected := false
	_ = filepath.WalkDir(repo, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || port != 0 {
			return nil
		}
		if entry.IsDir() && path != repo {
			name := entry.Name()
			if name == ".git" || name == "node_modules" || name == "dist" || name == "build" {
				return filepath.SkipDir
			}
		}
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "vite.config.") {
			return nil
		}
		viteDetected = true
		config, readErr := os.ReadFile(path)
		if readErr == nil {
			if match := vitePortPattern.FindSubmatch(config); len(match) == 2 {
				_, _ = fmt.Sscanf(string(match[1]), "%d", &port)
			}
			if port == 0 {
				port = 5173
			}
		}
		return nil
	})
	if port == 0 {
		if _, ok := manifest.Dependencies["next"]; ok {
			port = 3000
		} else if _, ok := manifest.DevDependencies["next"]; ok {
			port = 3000
		} else if _, ok := manifest.Dependencies["vite"]; ok {
			port = 5173
			viteDetected = true
		} else if _, ok := manifest.DevDependencies["vite"]; ok {
			port = 5173
			viteDetected = true
		} else {
			return pairwiseProjectLaunch{}, errors.New("未识别到 Vite 或 Next.js 的网页启动端口")
		}
	}
	host := "127.0.0.1"
	if viteDetected {
		host = "::1"
	}
	return pairwiseProjectLaunch{Command: manager, Port: port, Host: host}, nil
}

func validatePairwiseProjectRevision(ctx context.Context, repo, side, deliverableSHA string) error {
	branch, err := runCommand(ctx, repo, "git", "branch", "--show-current")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(branch)) != side {
		return fmt.Errorf("当前仓库不是 %s 分支，已停止启动", side)
	}
	head, err := runCommand(ctx, repo, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(string(head)), strings.TrimSpace(deliverableSHA)) {
		return fmt.Errorf("%s 分支当前提交与已登记产物不一致，请重新采集并提交", side)
	}
	status, err := runCommand(ctx, repo, "git", "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(status)) != "" {
		return fmt.Errorf("%s 分支存在未提交改动，不能作为最终产物录屏", side)
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func (s *AnnotationService) pairwiseProjectStateDirPath() string {
	dir := strings.TrimSpace(s.pairwiseProjectStateDir)
	if dir == "" {
		return defaultPairwiseProjectStateDir
	}
	return dir
}

// pairwiseProjectPortFile is the host-side file written by the copied startup
// command with the host proxy port. It lives on this host because the command runs in
// the user's terminal here, and the app reads the port back to locate the running
// project. The command's own exit trap owns the host proxy and removes this file.
func (s *AnnotationService) pairwiseProjectPortFile(taskID string, side domain.PairwiseSide) string {
	return filepath.Join(s.pairwiseProjectStateDirPath(), pairwiseProjectStateBase(taskID, side)+".port")
}

// pairwiseContainerProjectPidFile is the in-container path written by the startup
// command. The stop script runs inside the container and reads the same path, so it
// describes the container dev server that the foreground docker exec started.
func pairwiseContainerProjectPidFile(taskID string, side domain.PairwiseSide) string {
	return "/tmp/" + pairwiseProjectStateBase(taskID, side) + ".pid"
}

func pairwiseProjectStateBase(taskID string, side domain.PairwiseSide) string {
	return "pinru-project-" + stableKey(taskID) + "-" + strings.ToLower(string(side))
}

func pairwiseProjectStopScript(pidFile string) string {
	return "if [ -f " + shellQuote(pidFile) + " ]; then " +
		"while IFS= read -r pid; do case \"$pid\" in ''|*[!0-9]*) continue;; esac; " +
		"pkill -TERM -s \"$pid\" 2>/dev/null || kill -TERM -- -\"$pid\" 2>/dev/null || kill -TERM \"$pid\" 2>/dev/null || true; done < " + shellQuote(pidFile) + "; " +
		"fi; rm -f " + shellQuote(pidFile)
}

func pairwiseProjectManualCommand(containerID, containerRepo string, launch pairwiseProjectLaunch, port int, hostPortFile, containerPidFile string) string {
	args := fmt.Sprintf(" -- --host 0.0.0.0 --port %d --strictPort", port)
	if launch.Host != "::1" {
		args = fmt.Sprintf(" -- -H 0.0.0.0 -p %d", port)
	}
	proxyScript := `const n=require("net"),p=Number(process.argv[1]),h=process.argv[2],tp=Number(process.argv[3]);const s=n.createServer(c=>{const u=n.connect(tp,h);c.pipe(u);u.pipe(c);c.on("error",()=>u.destroy());u.on("error",()=>c.destroy())});s.listen(p,"127.0.0.1")`
	containerVar := "container=" + shellQuote(containerID)
	inspect := `target_ip=$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$container")`
	checkIP := `if [ -z "$target_ip" ]; then echo '无法读取容器访问地址' >&2; exit 1; fi`
	startProxy := "node -e " + shellQuote(proxyScript) + fmt.Sprintf(" %d \"$target_ip\" %d & proxy_pid=$!", port, port)
	cleanup := "cleanup() { kill -TERM \"$proxy_pid\" 2>/dev/null || true; rm -f " + shellQuote(hostPortFile) + "; }"
	trap := `trap cleanup EXIT HUP INT TERM`
	recordPort := fmt.Sprintf("printf '%%s\\n' %d > %s", port, shellQuote(hostPortFile))
	url := pairwiseProjectBrowserURL(port) + "/"
	announce := "echo " + shellQuote("项目地址："+url+"（容器内项目就绪后自动打开浏览器）")
	autoOpen := pairwiseProjectAutoOpenScript(url)
	// The container side records its own dev server pid, which the app's stop script
	// kills as a process group; a read-only container /tmp must not break the start.
	devCommand := "echo $$ > " + shellQuote(containerPidFile) + " 2>/dev/null || true; exec " + launch.Command + args
	runProject := "docker exec -it -w " + shellQuote(containerRepo) + ` "$container" sh -lc ` + shellQuote(devCommand)
	// The auto-open subshell is backgrounded, so it must be separated from the
	// foreground command by a space instead of a semicolon.
	return strings.Join([]string{containerVar, inspect, checkIP, startProxy, cleanup, trap, recordPort, announce, autoOpen}, "; ") + " " + runProject
}

func randomPairwiseProjectProxyPort(reader io.Reader) (int, error) {
	var raw [2]byte
	if _, err := io.ReadFull(reader, raw[:]); err != nil {
		return 0, err
	}
	span := pairwiseProjectProxyPortMax - pairwiseProjectProxyPortMin + 1
	return pairwiseProjectProxyPortMin + int(binary.BigEndian.Uint16(raw[:]))%span, nil
}

func randomPairwiseProjectAvailablePort(reader io.Reader, available func(int) bool) (int, error) {
	for attempt := 0; attempt < 32; attempt++ {
		port, err := randomPairwiseProjectProxyPort(reader)
		if err != nil {
			return 0, err
		}
		if available(port) {
			return port, nil
		}
	}
	return 0, errors.New("未找到可用的四位数项目端口")
}

func parsePairwiseProjectProxyPort(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < pairwiseProjectProxyPortMin || port > pairwiseProjectProxyPortMax {
		return 0, errors.New("项目代理端口无效，请重新启动项目")
	}
	return port, nil
}

func pairwiseProjectBrowserOpener() string {
	if runtime.GOOS == "darwin" {
		return "/usr/bin/open"
	}
	return "xdg-open"
}

// pairwiseProjectAutoOpenScript waits until the container dev server answers
// through the host proxy and then opens the browser once. It runs as a detached
// subshell so the foreground docker exec keeps the terminal, and it gives up as
// soon as the proxy process is gone, which is when the user stops the project.
func pairwiseProjectAutoOpenScript(url string) string {
	target := shellQuote(url)
	probe := `code=$(curl -s -o /dev/null -w '%{http_code}' ` + target + ` 2>/dev/null)`
	return `( i=0; while [ "$i" -lt 60 ]; do kill -0 "$proxy_pid" 2>/dev/null || exit 0; ` + probe +
		`; if [ -n "$code" ] && [ "$code" != "000" ]; then break; fi; i=$((i+1)); sleep 1; done; ` +
		`kill -0 "$proxy_pid" 2>/dev/null && ` + pairwiseProjectBrowserOpener() + ` ` + target + ` ) >/dev/null 2>&1 &`
}

func pairwiseProjectBrowserURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func (s *AnnotationService) pairwiseProjectTarget(ctx context.Context, req PairwiseSideRequest, validateRevision bool) (*domain.PairwiseRun, string, error) {
	c, err := s.loadCase(req.TaskID)
	if err != nil {
		return nil, "", err
	}
	if err := requirePairwiseCase(c); err != nil {
		return nil, "", err
	}
	run, err := pairwiseRun(c.Pairwise, req.Side)
	if err != nil {
		return nil, "", err
	}
	if run.ContainerID == "" {
		return nil, "", errors.New("请先绑定该侧容器")
	}
	if strings.TrimSpace(run.DeliverableSHA) == "" {
		return nil, "", errors.New("请先采集并提交该侧最终产物")
	}
	repo, err := s.verifyPairwiseBinding(ctx, c, run)
	if err != nil {
		return nil, "", err
	}
	if validateRevision {
		if err := validatePairwiseProjectRevision(ctx, repo, string(req.Side), run.DeliverableSHA); err != nil {
			return nil, "", err
		}
	}
	return run, repo, nil
}

func (s *AnnotationService) StartPairwiseProject(req PairwiseSideRequest) (*PairwiseProjectState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	unlock, err := s.lockTask(req.TaskID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	run, repo, err := s.pairwiseProjectTarget(ctx, req, true)
	if err != nil {
		return nil, err
	}
	launch, err := detectPairwiseProjectLaunch(repo)
	if err != nil {
		return nil, err
	}
	containerRepo := filepath.ToSlash(filepath.Join("/workspace", run.RepoRelativePath))
	proxyPort, err := randomPairwiseProjectAvailablePort(cryptorand.Reader, func(port int) bool {
		const probe = `const n=require("net"),s=n.createServer();s.once("error",()=>process.exit(1));s.listen(Number(process.argv[1]),"0.0.0.0",()=>s.close(()=>process.exit(0)))`
		const hostProbe = `const n=require("net"),s=n.createServer();s.once("error",()=>process.exit(1));s.listen(Number(process.argv[1]),"127.0.0.1",()=>s.close(()=>process.exit(0)))`
		if _, probeErr := s.command(ctx, "", "node", "-e", hostProbe, strconv.Itoa(port)); probeErr != nil {
			return false
		}
		_, probeErr := s.command(ctx, "", "docker", "exec", run.ContainerID, "node", "-e", probe, strconv.Itoa(port))
		return probeErr == nil
	})
	if err != nil {
		return nil, errors.New("无法生成项目代理端口")
	}
	url := pairwiseProjectBrowserURL(proxyPort)
	command := pairwiseProjectManualCommand(run.ContainerID, containerRepo, launch, proxyPort, s.pairwiseProjectPortFile(req.TaskID, req.Side), pairwiseContainerProjectPidFile(req.TaskID, req.Side))
	return &PairwiseProjectState{Side: req.Side, Running: false, URL: url, Command: command}, nil
}

func (s *AnnotationService) stopPairwiseProject(ctx context.Context, containerID, containerPidFile string) error {
	_, err := s.command(ctx, "", "docker", "exec", containerID, "sh", "-lc", pairwiseProjectStopScript(containerPidFile))
	return err
}

// pairwiseProjectPort reads the host proxy port that the copied startup command
// recorded, so the app knows where the running project is served.
func (s *AnnotationService) pairwiseProjectPort(taskID string, side domain.PairwiseSide) (int, error) {
	raw, err := os.ReadFile(s.pairwiseProjectPortFile(taskID, side))
	if err != nil {
		return 0, errors.New("项目尚未启动，请先在终端粘贴执行启动命令")
	}
	port, err := parsePairwiseProjectProxyPort(string(raw))
	if err != nil {
		return 0, err
	}
	return port, nil
}

func pairwiseProjectProxyAlive(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// StopPairwiseProject stops the dev server inside the container, which is what
// releases the foreground docker exec. The startup command's own exit trap then
// terminates the host proxy and removes the port file this app read.
func (s *AnnotationService) StopPairwiseProject(req PairwiseSideRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	unlock, err := s.lockTask(req.TaskID)
	if err != nil {
		return err
	}
	defer unlock()
	run, _, err := s.pairwiseProjectTarget(ctx, req, false)
	if err != nil {
		return err
	}
	portFile := s.pairwiseProjectPortFile(req.TaskID, req.Side)
	if _, statErr := os.Stat(portFile); statErr != nil {
		return errors.New("项目尚未通过启动命令运行，无需停止")
	}
	if err := s.stopPairwiseProject(ctx, run.ContainerID, pairwiseContainerProjectPidFile(req.TaskID, req.Side)); err != nil {
		return fmt.Errorf("容器内项目未确认停止：%w", err)
	}
	_ = os.Remove(portFile)
	return nil
}

func (s *AnnotationService) RecordPairwiseVideo(ctx context.Context, req PairwiseSideRequest) (*domain.Case, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("自动录屏当前仅支持 macOS")
	}
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
	if strings.TrimSpace(run.DeliverableSHA) == "" {
		return nil, errors.New("请先采集并提交该侧最终产物")
	}
	dir := s.pairwiseVideoDir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建录屏目录失败：%w", err)
	}
	output := filepath.Join(dir, strings.ToLower(string(req.Side))+"-"+time.Now().Format("20060102-150405.000")+".mov")
	port, err := s.pairwiseProjectPort(req.TaskID, req.Side)
	if err != nil {
		_, _ = s.SavePairwiseMaterials(PairwiseMaterialsRequest{TaskID: req.TaskID, Side: req.Side, RecordingError: err.Error()})
		return nil, err
	}
	if !pairwiseProjectProxyAlive(port) {
		message := "项目启动命令尚未在终端运行，或项目已停止：请先粘贴执行启动命令"
		_, _ = s.SavePairwiseMaterials(PairwiseMaterialsRequest{TaskID: req.TaskID, Side: req.Side, RecordingError: message})
		return nil, errors.New(message)
	}
	projectURL := pairwiseProjectBrowserURL(port)
	if _, err := s.command(ctx, "", "/usr/bin/open", projectURL); err != nil {
		message := "自动打开项目页面失败：" + err.Error()
		_, _ = s.SavePairwiseMaterials(PairwiseMaterialsRequest{TaskID: req.TaskID, Side: req.Side, RecordingError: message})
		return nil, errors.New(message)
	}
	domain.ReportProgress(ctx, 10, "项目已打开，3 秒后开始录制，请完成一次目标功能操作")
	_, recordErr := s.command(ctx, "", "/usr/sbin/screencapture", "-v", "-V30", "-T3", "-D1", "-k", "-x", output)
	if recordErr != nil {
		_ = os.Remove(output)
		message := "自动录屏失败：" + recordErr.Error()
		_, _ = s.SavePairwiseMaterials(PairwiseMaterialsRequest{TaskID: req.TaskID, Side: req.Side, RecordingError: message})
		return nil, errors.New(message)
	}
	domain.ReportProgress(ctx, 90, "录制完成，正在回填本机视频路径")
	updated, err := s.SavePairwiseMaterials(PairwiseMaterialsRequest{TaskID: req.TaskID, Side: req.Side, VideoPath: output})
	if err != nil {
		return nil, err
	}
	domain.ReportProgress(ctx, 100, "视频路径已回填")
	return updated, nil
}
