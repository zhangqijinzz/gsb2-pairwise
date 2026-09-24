package annotation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	domain "github.com/blueship581/pinru/internal/annotation"
)

func TestRandomPairwiseProjectProxyPortStaysFourDigits(t *testing.T) {
	low, err := randomPairwiseProjectProxyPort(bytes.NewReader([]byte{0, 0}))
	if err != nil {
		t.Fatal(err)
	}
	high, err := randomPairwiseProjectProxyPort(bytes.NewReader([]byte{255, 255}))
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range []int{low, high} {
		if port < 1024 || port > 9999 {
			t.Fatalf("port = %d", port)
		}
	}
	if low == high {
		t.Fatalf("ports did not vary: %d", low)
	}
}

func TestParsePairwiseProjectProxyPortRejectsValuesOutsideFourDigitRange(t *testing.T) {
	if port, err := parsePairwiseProjectProxyPort("4821\n"); err != nil || port != 4821 {
		t.Fatalf("valid port = %d, %v", port, err)
	}
	for _, value := range []string{"", "999", "10000", "not-a-port"} {
		if _, err := parsePairwiseProjectProxyPort(value); err == nil {
			t.Fatalf("accepted proxy port %q", value)
		}
	}
}

func TestDetectPairwiseProjectLaunchUsesDeclaredPNPMDevScript(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"scripts":{"dev":"vite"},"devDependencies":{"vite":"1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	launch, err := detectPairwiseProjectLaunch(repo)
	if err != nil {
		t.Fatal(err)
	}
	if launch.Command != "corepack pnpm run dev" || launch.Port != 5173 || launch.Host != "::1" {
		t.Fatalf("launch = %#v", launch)
	}
}

func manualPairwiseProjectCommand(t *testing.T, launch pairwiseProjectLaunch, port int) (string, string, string) {
	t.Helper()
	hostPortFile := filepath.Join(t.TempDir(), "pinru-project-task-a.port")
	containerPidFile := "/tmp/pinru-project-task-a.pid"
	command := pairwiseProjectManualCommand("container-a", "/workspace/project-a", launch, port, hostPortFile, containerPidFile)
	return command, hostPortFile, containerPidFile
}

func TestPairwiseProjectManualCommandRunsForegroundInBoundContainer(t *testing.T) {
	launch := pairwiseProjectLaunch{Command: "npm run dev", Port: 5173, Host: "::1"}
	command, _, _ := manualPairwiseProjectCommand(t, launch, 4821)
	for _, want := range []string{
		"container='container-a'",
		"docker exec -it -w '/workspace/project-a' \"$container\" sh -lc",
		"项目地址：http://127.0.0.1:4821",
		"docker inspect --format",
		"proxy_pid=$!",
		"trap cleanup EXIT HUP INT TERM",
		`s.listen(p,"127.0.0.1")`,
		` 4821 "$target_ip" 4821 & proxy_pid=$!`,
		"exec npm run dev -- --host 0.0.0.0 --port 4821 --strictPort",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("command missing %q: %s", want, command)
		}
	}
	// The startup command must stay a plain foreground exec, never a detached
	// background start like the removed in-app launcher.
	for _, forbidden := range []string{"setsid", "nohup"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("manual command contains %q: %s", forbidden, command)
		}
	}
	if output, err := exec.Command("sh", "-n", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("manual command is not valid shell: %v: %s", err, output)
	}
}

func TestPairwiseProjectManualCommandRecordsRuntimeState(t *testing.T) {
	launch := pairwiseProjectLaunch{Command: "corepack pnpm run dev", Port: 5173, Host: "::1"}
	command, hostPortFile, containerPidFile := manualPairwiseProjectCommand(t, launch, 4821)
	for _, want := range []string{
		// Host state the app reads back: the browser port of the host proxy.
		`printf '%s\n' 4821 > ` + shellQuote(hostPortFile),
		// The app never has to kill the host proxy: the command's trap does it.
		`cleanup() { kill -TERM "$proxy_pid" 2>/dev/null || true; rm -f ` + shellQuote(hostPortFile),
		// The container records its own dev server pid for the stop script. The path
		// is nested inside the quoted dev command, so only the surrounding text is
		// stable enough to assert verbatim.
		"echo $$ > ",
		containerPidFile,
		"2>/dev/null || true; exec corepack pnpm run dev",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("command missing %q: %s", want, command)
		}
	}
	if strings.Index(command, `printf '%s\n' 4821 >`) < strings.Index(command, "proxy_pid=$!") {
		t.Fatalf("the port is recorded before the proxy starts: %s", command)
	}
	if output, err := exec.Command("sh", "-n", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("manual command is not valid shell: %v: %s", err, output)
	}
}

// listenOnFourDigitPort returns a listener on a free four-digit port, which is the
// only range the pairwise proxy accepts.
func listenOnFourDigitPort(t *testing.T) (net.Listener, int) {
	t.Helper()
	for port := 4821; port <= 4900; port++ {
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err == nil {
			return listener, port
		}
	}
	t.Skip("no free four-digit port on 127.0.0.1")
	return nil, 0
}

func TestPairwiseProjectManualCommandOpensBrowserOnceProjectAnswers(t *testing.T) {
	launch := pairwiseProjectLaunch{Command: "npm run dev", Port: 5173, Host: "::1"}
	command, _, _ := manualPairwiseProjectCommand(t, launch, 4821)
	for _, want := range []string{
		"code=$(curl -s -o /dev/null -w '%{http_code}' 'http://127.0.0.1:4821/' 2>/dev/null)",
		`if [ -n "$code" ] && [ "$code" != "000" ]; then break; fi`,
		`kill -0 "$proxy_pid" 2>/dev/null || exit 0`,
		pairwiseProjectBrowserOpener() + " 'http://127.0.0.1:4821/'",
		") >/dev/null 2>&1 &",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("command missing %q: %s", want, command)
		}
	}
	if strings.Contains(command, "s.listen(p,\"127.0.0.1\",()=>console.log") {
		t.Fatalf("proxy still prints the address after the echo was added: %s", command)
	}
	// The auto-open subshell must be detached, otherwise it blocks the foreground docker exec.
	if strings.Index(command, ") >/dev/null 2>&1 &") > strings.Index(command, "docker exec -it") {
		t.Fatalf("auto-open subshell is not started before the foreground command: %s", command)
	}
}

func TestPairwiseProjectAutoOpenSubshellOpensOnceProjectAnswers(t *testing.T) {
	dir := t.TempDir()
	callsPath := filepath.Join(dir, "calls")
	openedPath := filepath.Join(dir, "opened")
	curlPath := filepath.Join(dir, "curl")
	openerPath := filepath.Join(dir, "opener")
	curlStub := "#!/bin/sh\nn=$(cat " + shellQuote(callsPath) + " 2>/dev/null || echo 0)\nn=$((n+1))\necho \"$n\" > " + shellQuote(callsPath) + "\nif [ \"$n\" -ge 3 ]; then echo 200; else echo 000; fi\n"
	openerStub := "#!/bin/sh\necho \"$@\" >> " + shellQuote(openedPath) + "\n"
	if err := os.WriteFile(curlPath, []byte(curlStub), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(openerPath, []byte(openerStub), 0o700); err != nil {
		t.Fatal(err)
	}
	snippet := pairwiseProjectAutoOpenScript("http://127.0.0.1:4821/")
	snippet = strings.ReplaceAll(snippet, "curl", shellQuote(curlPath))
	snippet = strings.ReplaceAll(snippet, pairwiseProjectBrowserOpener(), shellQuote(openerPath))
	snippet = strings.ReplaceAll(snippet, "sleep 1", "sleep 0.1")
	script := "proxy_pid=$$; " + snippet + " for i in 1 2 3 4 5 6 7 8 9 10; do [ -s " + shellQuote(openedPath) + " ] && break; sleep 0.2; done"
	if output, err := exec.Command("sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("auto-open subshell failed: %v: %s", err, output)
	}
	opened, err := os.ReadFile(openedPath)
	if err != nil || strings.TrimSpace(string(opened)) != "http://127.0.0.1:4821/" {
		t.Fatalf("opened = %q, %v", opened, err)
	}
	calls, err := os.ReadFile(callsPath)
	if err != nil || strings.TrimSpace(string(calls)) != "3" {
		t.Fatalf("probe calls = %q, %v (the browser must wait for a real answer)", calls, err)
	}
}

func TestRandomPairwiseProjectAvailablePortSkipsOccupiedPort(t *testing.T) {
	checked := []int{}
	port, err := randomPairwiseProjectAvailablePort(bytes.NewReader([]byte{0, 0, 255, 255}), func(candidate int) bool {
		checked = append(checked, candidate)
		return len(checked) == 2
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(checked) != 2 || port != checked[1] || checked[0] == checked[1] {
		t.Fatalf("port = %d, checked = %#v", port, checked)
	}
}

func TestPairwiseProjectManualCommandUsesNextFlags(t *testing.T) {
	command, _, _ := manualPairwiseProjectCommand(t, pairwiseProjectLaunch{Command: "corepack pnpm run dev", Port: 3000, Host: "127.0.0.1"}, 7351)
	if !strings.Contains(command, "corepack pnpm run dev -- -H 0.0.0.0 -p 7351") {
		t.Fatalf("command = %s", command)
	}
}

func TestValidatePairwiseProjectRevisionRequiresExpectedBranchAndCommit(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=PINRU", "GIT_AUTHOR_EMAIL=pinru@test", "GIT_COMMITTER_NAME=PINRU", "GIT_COMMITTER_EMAIL=pinru@test")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "result.txt"), []byte("A result\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "result.txt")
	runGit("commit", "-qm", "result")
	runGit("branch", "-M", "A")
	sha := runGit("rev-parse", "HEAD")
	if err := validatePairwiseProjectRevision(context.Background(), repo, "A", sha); err != nil {
		t.Fatal(err)
	}
	if err := validatePairwiseProjectRevision(context.Background(), repo, "B", sha); err == nil || !strings.Contains(err.Error(), "B") {
		t.Fatalf("wrong branch error = %v", err)
	}
}

func TestRecordPairwiseVideoStoresAbsolutePathAndMarksVideoReady(t *testing.T) {
	s, _, _ := annotationFixture(t)
	s.pairwiseVideoDir = filepath.Join(t.TempDir(), "pairwise-videos")
	s.pairwiseProjectStateDir = t.TempDir()
	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1"})
	if err != nil {
		t.Fatal(err)
	}
	c.Pairwise.RunA.DeliverableSHA = strings.Repeat("a", 40)
	c.Pairwise.RunA.ContainerID = "container-a"
	if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
		t.Fatal(err)
	}
	// A real listener stands in for the host proxy so the liveness check passes.
	listener, port := listenOnFourDigitPort(t)
	defer listener.Close()
	portFile := s.pairwiseProjectPortFile(c.TaskID, domain.PairwiseSideA)
	if err := os.WriteFile(portFile, []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	expectedURL := pairwiseProjectBrowserURL(port)
	var recordedArgs []string
	var commands []string
	s.command = func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		switch name {
		case "/usr/bin/open":
			return nil, nil
		case "/usr/sbin/screencapture":
			recordedArgs = append([]string(nil), args...)
			output := args[len(args)-1]
			return nil, os.WriteFile(output, []byte("quicktime-video"), 0o600)
		default:
			t.Fatalf("command = %s", name)
			return nil, nil
		}
	}
	updated, err := s.RecordPairwiseVideo(context.Background(), PairwiseSideRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA})
	if err != nil {
		t.Fatal(err)
	}
	run := updated.Pairwise.RunA
	if run.VideoStatus != domain.PairwiseVideoReady || !filepath.IsAbs(run.VideoPath) {
		t.Fatalf("video state = %#v", run)
	}
	if !strings.HasPrefix(run.VideoPath, s.pairwiseVideoDir+string(filepath.Separator)) {
		t.Fatalf("video path = %s", run.VideoPath)
	}
	joined := strings.Join(recordedArgs, " ")
	if !strings.Contains(joined, "-v -V30 -T3 -D1 -k -x") {
		t.Fatalf("screencapture args = %q", joined)
	}
	if len(commands) != 2 || commands[0] != "/usr/bin/open "+expectedURL || !strings.HasPrefix(commands[1], "/usr/sbin/screencapture ") {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestRecordPairwiseVideoRefusesToRecordWithoutARunningProject(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("automatic recording is macOS only")
	}
	t.Run("no startup command has run yet", func(t *testing.T) {
		s, _, _ := annotationFixture(t)
		s.pairwiseProjectStateDir = t.TempDir()
		c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1"})
		if err != nil {
			t.Fatal(err)
		}
		c.Pairwise.RunA.DeliverableSHA = strings.Repeat("a", 40)
		c.Pairwise.RunA.ContainerID = "container-a"
		if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
			t.Fatal(err)
		}
		s.command = func(_ context.Context, _ string, name string, _ ...string) ([]byte, error) {
			t.Fatalf("no command may run without a started project: %s", name)
			return nil, nil
		}
		if _, err := s.RecordPairwiseVideo(context.Background(), PairwiseSideRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA}); err == nil || !strings.Contains(err.Error(), "尚未启动") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("recorded port is gone", func(t *testing.T) {
		s, _, _ := annotationFixture(t)
		s.pairwiseProjectStateDir = t.TempDir()
		c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1"})
		if err != nil {
			t.Fatal(err)
		}
		c.Pairwise.RunA.DeliverableSHA = strings.Repeat("a", 40)
		c.Pairwise.RunA.ContainerID = "container-a"
		if _, err := s.store.SaveAnnotationCase(*c, c.Revision); err != nil {
			t.Fatal(err)
		}
		// Pick a four-digit port that nothing listens on any more.
		listener, port := listenOnFourDigitPort(t)
		_ = listener.Close()
		portFile := s.pairwiseProjectPortFile(c.TaskID, domain.PairwiseSideA)
		if err := os.WriteFile(portFile, []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		s.command = func(_ context.Context, _ string, name string, _ ...string) ([]byte, error) {
			t.Fatalf("no command may run without a live project: %s", name)
			return nil, nil
		}
		if _, err := s.RecordPairwiseVideo(context.Background(), PairwiseSideRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA}); err == nil || !strings.Contains(err.Error(), "尚未在终端运行") {
			t.Fatalf("error = %v", err)
		}
		// The refusal is recorded so the UI can explain the missing recording.
		saved, err := s.store.GetAnnotationCase(c.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if saved.Pairwise.RunA.RecordingError == "" || saved.Pairwise.RunA.VideoStatus != domain.PairwiseVideoManualRequired {
			t.Fatalf("run = %#v", saved.Pairwise.RunA)
		}
	})
}

// stopFixtureCase stores a pairwise case whose container binding passes
// verification without docker: a real local git repo stands in for the mounted
// workspace, so the stop path can be exercised in full.
func stopFixtureCase(t *testing.T, s *AnnotationService) (*domain.Case, string) {
	t.Helper()
	workspace := t.TempDir()
	repo := filepath.Join(workspace, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=PINRU", "GIT_AUTHOR_EMAIL=pinru@test", "GIT_COMMITTER_NAME=PINRU", "GIT_COMMITTER_EMAIL=pinru@test")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "result.txt"), []byte("A result\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "result.txt")
	runGit("commit", "-qm", "initial")
	initialSHA := runGit("rev-parse", "HEAD")

	c, err := s.EnablePairwise(EnablePairwiseRequest{TaskID: "题目-1"})
	if err != nil {
		t.Fatal(err)
	}
	c.InitialSHA = initialSHA
	c.Pairwise.RunA.DeliverableSHA = strings.Repeat("a", 40)
	c.Pairwise.RunA.ContainerID = "container-a"
	c.Pairwise.RunA.WorkspacePath = workspace
	c.Pairwise.RunA.RepoRelativePath = "repo"
	saved, err := s.store.SaveAnnotationCase(*c, c.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return saved, workspace
}

// inspectStub answers the docker inspect call of the binding check for workspace.
func inspectStub(t *testing.T, workspace string, onStop func(containerPIDFile string)) func(context.Context, string, string, ...string) ([]byte, error) {
	t.Helper()
	return func(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
		if name != "docker" {
			t.Fatalf("command = %s", name)
		}
		if len(args) > 1 && args[0] == "inspect" {
			return []byte(fmt.Sprintf(`{"ID":"container-a","Name":"/container-a","State":"running","Image":"claude","Mounts":[{"Type":"bind","Source":%q,"Destination":"/workspace"}]}`, workspace)), nil
		}
		if onStop != nil {
			onStop(args[len(args)-1])
		}
		return nil, nil
	}
}

func TestStopPairwiseProjectNeedsARunningStartupCommand(t *testing.T) {
	s, _, _ := annotationFixture(t)
	s.pairwiseProjectStateDir = t.TempDir()
	c, workspace := stopFixtureCase(t, s)
	var commands []string
	s.command = inspectStub(t, workspace, func(containerPIDFile string) {
		commands = append(commands, containerPIDFile)
	})
	if err := s.StopPairwiseProject(PairwiseSideRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA}); err == nil || !strings.Contains(err.Error(), "无需停止") {
		t.Fatalf("error = %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("a stop script ran without a started project: %#v", commands)
	}
}

func TestStopPairwiseProjectStopsTheContainerDevServerAndClearsPortFile(t *testing.T) {
	s, _, _ := annotationFixture(t)
	s.pairwiseProjectStateDir = t.TempDir()
	c, workspace := stopFixtureCase(t, s)
	portFile := s.pairwiseProjectPortFile(c.TaskID, domain.PairwiseSideA)
	if err := os.WriteFile(portFile, []byte("4821\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var containerStopScript string
	s.command = inspectStub(t, workspace, func(value string) { containerStopScript = value })
	if err := s.StopPairwiseProject(PairwiseSideRequest{TaskID: c.TaskID, Side: domain.PairwiseSideA}); err != nil {
		t.Fatal(err)
	}
	// The stop script targets the in-container pid file the startup command wrote,
	// and the host port file is cleared so the app no longer reports a live project.
	if !strings.Contains(containerStopScript, shellQuote(pairwiseContainerProjectPidFile(c.TaskID, domain.PairwiseSideA))) {
		t.Fatalf("container stop script = %q", containerStopScript)
	}
	if !strings.Contains(containerStopScript, "kill -TERM -- -\"$pid\"") {
		t.Fatalf("container stop script must kill the dev process group: %q", containerStopScript)
	}
	if _, err := os.Stat(portFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("host port file still present: %v", err)
	}
	// The startup command owns the host proxy, so the app must never shell out to ps.
	if strings.Contains(containerStopScript, "ps ") {
		t.Fatalf("container stop script = %q", containerStopScript)
	}
}

// writeExecutableStub writes a helper script used to stand in for docker and npm.
func writeExecutableStub(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

// TestPairwiseProjectCommandLifecycleWritesAndCleansHostState runs the generated
// startup command against a stub docker and verifies the contract the app depends on:
// the host port file exists while the project runs, the container side records its own
// dev server pid, and stopping the command cleans the host state up again.
func TestPairwiseProjectCommandLifecycleWritesAndCleansHostState(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required for the host proxy")
	}
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspace")
	repoDir := filepath.Join(workspace, "repo")
	if err := os.MkdirAll(repoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutableStub(t, filepath.Join(binDir, "docker"), `#!/bin/sh
case "$1" in
  inspect) echo 127.0.0.1; exit 0;;
  exec)
    shift
    work=""
    while [ $# -gt 0 ]; do
      case "$1" in
        -it|-i|-t|-d) shift;;
        -w) work="$2"; shift 2;;
        *) break;;
      esac
    done
    shift
    shift 2
    [ -n "$work" ] && cd "$work"
    exec sh -lc "$1"
    ;;
esac
exit 1
`)
	// The dev script stands in for a real dev server: it stays alive briefly and never
	// answers HTTP, so the background auto-open waiter gives up when the proxy exits.
	writeExecutableStub(t, filepath.Join(binDir, "npm"), "#!/bin/sh\nsleep 2\n")

	listener, port := listenOnFourDigitPort(t)
	_ = listener.Close()

	portFile := filepath.Join(root, "pinru-project-life-a.port")
	containerPidFile := filepath.Join(root, "container-dev.pid")
	command := pairwiseProjectManualCommand(
		"fake-container",
		filepath.ToSlash(repoDir),
		pairwiseProjectLaunch{Command: "npm run dev", Port: 5173, Host: "::1"},
		port,
		portFile,
		containerPidFile,
	)
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(portFile); err == nil && strings.TrimSpace(string(raw)) == strconv.Itoa(port) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw, err := os.ReadFile(portFile)
	if err != nil || strings.TrimSpace(string(raw)) != strconv.Itoa(port) {
		t.Fatalf("host port file = %q, %v", raw, err)
	}
	proxyDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(proxyDeadline) && !pairwiseProjectProxyAlive(port) {
		time.Sleep(20 * time.Millisecond)
	}
	if !pairwiseProjectProxyAlive(port) {
		t.Fatalf("host proxy is not listening on %d while the project runs", port)
	}
	pidRaw, err := os.ReadFile(containerPidFile)
	if err != nil {
		t.Fatalf("the container side did not record its dev server pid: %v", err)
	}
	if _, err := strconv.Atoi(strings.TrimSpace(string(pidRaw))); err != nil {
		t.Fatalf("container dev pid = %q", pidRaw)
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("startup command failed: %v", err)
	}
	finished = true
	if _, err := os.Stat(portFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("host port file was not cleaned up: %v", err)
	}
	closedDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(closedDeadline) && pairwiseProjectProxyAlive(port) {
		time.Sleep(20 * time.Millisecond)
	}
	if pairwiseProjectProxyAlive(port) {
		t.Fatalf("host proxy still listens on %d after the command exited", port)
	}
}

// TestPairwiseProjectStopScriptKillsTheRecordedDevProcessGroup verifies the stop
// contract with a session leader, which is what docker exec starts: the recorded pid
// and its whole process group (npm plus the dev server it spawned) must go away.
func TestPairwiseProjectStopScriptKillsTheRecordedDevProcessGroup(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(dir, "container-dev.pid")
	childPidFile := filepath.Join(dir, "dev-server.pid")
	// Stand in for npm: it spawns the dev server and waits, so a plain kill of the
	// recorded pid would leave the grandchild behind.
	writeExecutableStub(t, filepath.Join(binDir, "npm"), `#!/bin/sh
sleep 300 &
echo $! > `+shellQuote(childPidFile)+`
wait
`)
	listener, _ := listenOnFourDigitPort(t)
	_ = listener.Close()
	command := pairwiseProjectManualCommand(
		"fake-container",
		"/workspace/repo",
		pairwiseProjectLaunch{Command: "npm run dev", Port: 5173, Host: "::1"},
		4821,
		filepath.Join(dir, "pinru-project-stop-a.port"),
		pidFile,
	)
	devCommand := command[strings.Index(command, "sh -lc ")+len("sh -lc "):]
	if len(devCommand) < 3 {
		t.Fatalf("command = %s", command)
	}
	// docker exec -it starts a session leader, so the test does the same.
	dev := exec.Command("sh", "-c", "export PATH="+shellQuote(binDir)+":$PATH; eval "+devCommand)
	dev.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := dev.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dev.Process.Kill() })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidFile); err == nil && strings.TrimSpace(string(raw)) != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	recorded, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("dev server pid was not recorded: %v", err)
	}
	devPID, err := strconv.Atoi(strings.TrimSpace(string(recorded)))
	if err != nil {
		t.Fatalf("dev server pid = %q", recorded)
	}
	childDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(childDeadline) {
		if raw, err := os.ReadFile(childPidFile); err == nil && strings.TrimSpace(string(raw)) != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	childRaw, err := os.ReadFile(childPidFile)
	if err != nil {
		t.Fatalf("dev server child pid was not recorded: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(childRaw)))
	if err != nil {
		t.Fatalf("dev server child pid = %q", childRaw)
	}

	if _, err := exec.Command("sh", "-c", pairwiseProjectStopScript(pidFile)).CombinedOutput(); err != nil {
		t.Fatalf("stop script failed: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- dev.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the recorded dev server was not stopped")
	}
	if err := syscall.Kill(devPID, 0); err == nil {
		t.Fatal("the recorded dev server is still alive")
	}
	waitGone(t, childPID)
	if _, err := os.Stat(pidFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dev server pid file was kept: %v", err)
	}
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d is still alive", pid)
}
