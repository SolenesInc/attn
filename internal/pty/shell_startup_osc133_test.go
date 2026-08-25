package pty

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	creackpty "github.com/creack/pty"
)

func runShellIntegrationScenario(t *testing.T, shellPath string, env []string, commands string) ([]Observation, []byte) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping real PTY spawn in short mode")
	}
	if _, err := os.Stat(shellPath); err != nil {
		t.Skipf("%s unavailable: %v", shellPath, err)
	}

	launch, err := prepareShellPaneLaunch(shellPath, env)
	if err != nil {
		t.Fatalf("prepare shell pane launch: %v", err)
	}
	t.Cleanup(func() { removeShellOverlay(launch.overlayDir) })
	launch.command.Env = launch.env
	launch.command.Dir = t.TempDir()

	ptmx, err := creackpty.StartWithSize(launch.command, &creackpty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("start %s in PTY: %v", shellPath, err)
	}
	t.Cleanup(func() {
		_ = ptmx.Close()
		if launch.command.Process != nil {
			_ = launch.command.Process.Kill()
		}
		_, _ = launch.command.Process.Wait()
	})

	arbiter := newShellSignalArbiter(launch.command.Process.Pid)
	var mu sync.Mutex
	var observations []Observation
	var stream []byte
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				obs := arbiter.ObserveOutput(buf[:n], time.Now())
				mu.Lock()
				observations = append(observations, obs...)
				stream = append(stream, buf[:n]...)
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	if _, err := ptmx.Write([]byte(commands)); err != nil {
		t.Fatalf("write commands: %v", err)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("shell did not exit; integration output never completed")
	}

	mu.Lock()
	defer mu.Unlock()
	return append([]Observation(nil), observations...), append([]byte(nil), stream...)
}

const integrationProbeCommands = "/bin/echo attn-integration-probe\rfalse\rexit\r"

func requireObservation(t *testing.T, observations []Observation, claim, detail string) {
	t.Helper()
	for _, obs := range observations {
		if obs.Claim == claim && obs.Detail == detail {
			return
		}
	}
	t.Fatalf("no %s/%q observation in %+v", claim, detail, observations)
}

func blocksFromStream(t *testing.T, stream []byte) []AttachBlockData {
	t.Helper()
	freed := 0
	row := 0
	table := newBlockTable()
	t.Cleanup(table.Close)
	seg := &feedSegmenter{}
	seg.Feed(stream, func(e feedSegment) {
		if e.Marker == nil {
			return
		}
		row++
		table.ApplyMarker(*e.Marker, &fakeBlockRef{x: 0, y: row, freed: &freed}, false)
	})
	return table.SnapshotBlocks()
}

func requireCompletedBlock(t *testing.T, blocks []AttachBlockData, command string, exitCode int32) {
	t.Helper()
	for _, b := range blocks {
		if b.Pending || b.Command == nil || b.ExitCode == nil {
			continue
		}
		if *b.Command == command && *b.ExitCode == exitCode {
			return
		}
	}
	t.Fatalf("no completed block command=%q exit=%d in %+v", command, exitCode, blocks)
}

func TestZshIntegrationEmitsCommandEdgesAndBlocks(t *testing.T) {
	root := t.TempDir()
	userZdotdir := filepath.Join(root, "user-zdotdir")
	if err := os.MkdirAll(userZdotdir, 0o755); err != nil {
		t.Fatalf("create user zdotdir: %v", err)
	}
	observations, stream := runShellIntegrationScenario(t, "/bin/zsh", []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + root,
		"ZDOTDIR=" + userZdotdir,
		"TERM=xterm-256color",
	}, integrationProbeCommands)

	requireObservation(t, observations, claimBusy, "command started: /bin/echo attn-integration-probe")
	requireObservation(t, observations, claimBusy, "command started: false")
	requireObservation(t, observations, claimNotBusy, "command exited 0")
	requireObservation(t, observations, claimNotBusy, "command exited 1")

	blocks := blocksFromStream(t, stream)
	requireCompletedBlock(t, blocks, "/bin/echo attn-integration-probe", 0)
	requireCompletedBlock(t, blocks, "false", 1)
}

func TestBashIntegrationEmitsCommandEdgesAndBlocks(t *testing.T) {
	root := t.TempDir()
	userHome := filepath.Join(root, "home")
	if err := os.MkdirAll(userHome, 0o755); err != nil {
		t.Fatalf("create user home: %v", err)
	}
	observations, stream := runShellIntegrationScenario(t, "/bin/bash", []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + userHome,
		"TERM=xterm-256color",
	}, integrationProbeCommands)

	requireObservation(t, observations, claimBusy, "command started: /bin/echo attn-integration-probe")
	requireObservation(t, observations, claimBusy, "command started: false")
	requireObservation(t, observations, claimNotBusy, "command exited 0")
	requireObservation(t, observations, claimNotBusy, "command exited 1")

	blocks := blocksFromStream(t, stream)
	requireCompletedBlock(t, blocks, "/bin/echo attn-integration-probe", 0)
	requireCompletedBlock(t, blocks, "false", 1)
}

func TestBashIntegrationLeavesAUserDebugTrapAlone(t *testing.T) {
	root := t.TempDir()
	userHome := filepath.Join(root, "home")
	if err := os.MkdirAll(userHome, 0o755); err != nil {
		t.Fatalf("create user home: %v", err)
	}
	trapLog := filepath.Join(root, "trap-log")
	profile := "trap 'echo fired >> \"$ATTN_TEST_TRAP_LOG\"' DEBUG\n"
	if err := os.WriteFile(filepath.Join(userHome, ".bash_profile"), []byte(profile), 0o600); err != nil {
		t.Fatalf("write user bash profile: %v", err)
	}

	observations, _ := runShellIntegrationScenario(t, "/bin/bash", []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + userHome,
		"TERM=xterm-256color",
		"ATTN_TEST_TRAP_LOG=" + trapLog,
	}, integrationProbeCommands)

	logged, err := os.ReadFile(trapLog)
	if err != nil || len(logged) == 0 {
		t.Fatalf("user DEBUG trap did not survive integration: err=%v log=%q", err, logged)
	}
	for _, obs := range observations {
		if strings.HasPrefix(obs.Detail, "command started: ") {
			t.Fatalf("cmdline mark emitted despite user-owned DEBUG trap: %+v", obs)
		}
	}
	requireObservation(t, observations, claimNotBusy, "command exited 1")
}

func TestShellIntegrationOptOutEmitsNoMarks(t *testing.T) {
	root := t.TempDir()
	userZdotdir := filepath.Join(root, "user-zdotdir")
	if err := os.MkdirAll(userZdotdir, 0o755); err != nil {
		t.Fatalf("create user zdotdir: %v", err)
	}
	observations, _ := runShellIntegrationScenario(t, "/bin/zsh", []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + root,
		"ZDOTDIR=" + userZdotdir,
		"TERM=xterm-256color",
		"ATTN_NO_SHELL_INTEGRATION=1",
	}, integrationProbeCommands)

	for _, obs := range observations {
		if strings.HasPrefix(obs.Detail, "command ") {
			t.Fatalf("opt-out still emitted %+v", obs)
		}
	}
}
