package ptybackend

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptyhost"
)

func TestSharedHost_MultipleSessionsAndBackendRecovery(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	root, err := os.MkdirTemp("/tmp", "attn-rust-host-int-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	cfg := WorkerBackendConfig{
		DataRoot:         root,
		DaemonInstanceID: "d-shared-host-integration",
		BinaryPath:       binary,
	}
	backend, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"shared-one", "shared-two"} {
		if err := backend.Spawn(context.Background(), SpawnOptions{
			ID: id, CWD: t.TempDir(), Agent: "shell", Cols: 80, Rows: 24,
		}); err != nil {
			t.Fatalf("Spawn(%s): %v", id, err)
		}
	}
	pids := backend.WorkerPIDs(context.Background())
	if pids["shared-one"] <= 0 || pids["shared-one"] != pids["shared-two"] {
		t.Fatalf("host pids = %v, want the same positive pid", pids)
	}
	hostPID := pids["shared-one"]
	defer func() {
		_ = syscall.Kill(hostPID, syscall.SIGTERM)
		_ = waitForPIDsGone(3*time.Second, hostPID)
	}()

	_, stream, err := backend.Attach(context.Background(), "shared-one", "integration")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := backend.Input(context.Background(), "shared-one", []byte("printf '__RUST_HOST_ONE__\\n'\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(8 * time.Second)
	var output bytes.Buffer
	for !strings.Contains(output.String(), "__RUST_HOST_ONE__") {
		select {
		case event := <-stream.Events():
			if event.Kind == OutputEventKindOutput {
				output.Write(event.Data)
			}
		case <-deadline:
			t.Fatalf("output = %q, want marker", output.String())
		}
	}

	if err := backend.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewSharedHost(cfg)
	if err != nil {
		t.Fatal(err)
	}
	report, err := recovered.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Recovered != 2 {
		t.Fatalf("recovered = %+v, want two sessions", report)
	}
	ids := recovered.SessionIDs(context.Background())
	sort.Strings(ids)
	if !reflect.DeepEqual(ids, []string{"shared-one", "shared-two"}) {
		t.Fatalf("session ids = %v", ids)
	}
	for _, id := range ids {
		if err := recovered.Remove(context.Background(), id); err != nil {
			t.Fatalf("Remove(%s): %v", id, err)
		}
	}
}

func TestSharedHost_InnerShellPromptSurvivesForegroundPolling(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	root, err := os.MkdirTemp("/tmp", "attn-inner-shell-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-inner-shell", BinaryPath: binary,
	})
	if err != nil {
		t.Fatal(err)
	}
	observations := make(chan pty.Observation, 64)
	backend.SetStateHandler(func(_ string, obs pty.Observation) { observations <- obs })
	defer backend.Shutdown(context.Background())
	const id = "inner-shell"
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID: id, CWD: root, Agent: "shell", Cols: 80, Rows: 24,
		ExternalCommand: []string{"/bin/bash", "--noprofile", "--norc", "-i"},
	}); err != nil {
		t.Fatal(err)
	}
	hostPID := backend.WorkerPIDs(context.Background())[id]
	defer func() {
		_ = backend.Remove(context.Background(), id)
		_ = syscall.Kill(hostPID, syscall.SIGTERM)
		_ = waitForPIDsGone(3*time.Second, hostPID)
	}()
	waitFor := func(claim, detail string, rejectBusy bool) {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for {
			select {
			case obs := <-observations:
				if rejectBusy && obs.Claim == "busy" {
					t.Fatalf("foreground poll overwrote the inner prompt: %+v", obs)
				}
				if obs.Claim == claim && obs.Detail == detail {
					return
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s: %s", claim, detail)
			}
		}
	}
	input := func(command string) {
		t.Helper()
		if err := backend.Input(context.Background(), id, []byte(command+"\n")); err != nil {
			t.Fatal(err)
		}
	}
	waitFor("not_busy", "shell at prompt", false)
	input(`/bin/sh -c 'read -r token; printf "\033]133;A\007"; read -r token; printf "\033]133;C\007"; read -r token; printf "\033]133;D;0\007"; read -r token'`)
	waitFor("busy", "foreground command running", false)
	input("prompt")
	waitFor("not_busy", "shell at prompt", false)
	waitFor("not_busy", "inner shell at prompt", true)
	input("start")
	waitFor("busy", "command started", false)
	input("finish")
	waitFor("not_busy", "command exited 0", false)
	waitFor("not_busy", "inner shell at prompt", true)
	input("exit")
	waitFor("not_busy", "shell at prompt", true)
}

func TestSharedHost_ReplacedSubscriberCannotDetachReplacement(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	root, err := os.MkdirTemp("/tmp", "attn-rust-host-reattach-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-host-reattach", BinaryPath: binary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID: "reattach", CWD: root, Agent: "reattach-probe",
		ExternalCommand: []string{"/bin/cat"}, Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatal(err)
	}
	hostPID := backend.WorkerPIDs(context.Background())["reattach"]
	defer func() {
		_ = syscall.Kill(hostPID, syscall.SIGTERM)
		_ = waitForPIDsGone(3*time.Second, hostPID)
	}()

	_, replaced, err := backend.Attach(context.Background(), "reattach", "frontend", AttachOptions{OmitReplay: true})
	if err != nil {
		t.Fatal(err)
	}
	_, current, err := backend.Attach(context.Background(), "reattach", "frontend", AttachOptions{OmitReplay: true})
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	_ = replaced.Close()

	if err := backend.Input(context.Background(), "reattach", []byte("__REATTACH_SURVIVED__\n")); err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, current, "__REATTACH_SURVIVED__")
	if err := backend.Remove(context.Background(), "reattach"); err != nil {
		t.Fatal(err)
	}
}

func TestMigratingHost_RecoversLegacyAndSharedSessionsWithoutMovingEither(t *testing.T) {
	hostBinary := os.Getenv("ATTN_TEST_PTY_HOST")
	if hostBinary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	workerBinary := os.Getenv("ATTN_E2E_BIN")
	if workerBinary == "" {
		workerBinary = buildAttnBinary(t)
	}
	root, err := os.MkdirTemp("/tmp", "attn-mixed-host-recovery-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	const daemonID = "d-mixed-host-recovery"
	cfg := func(binary string) WorkerBackendConfig {
		return WorkerBackendConfig{DataRoot: root, DaemonInstanceID: daemonID, BinaryPath: binary}
	}
	legacy, err := NewWorker(cfg(workerBinary))
	if err != nil {
		t.Fatal(err)
	}
	shared, err := NewSharedHost(cfg(hostBinary))
	if err != nil {
		t.Fatal(err)
	}
	spawn := func(backend Backend, id string) {
		t.Helper()
		if err := backend.Spawn(context.Background(), SpawnOptions{
			ID: id, CWD: root, Agent: "mixed-recovery-probe",
			ExternalCommand: []string{"/bin/cat"}, Cols: 80, Rows: 24,
		}); err != nil {
			t.Fatalf("Spawn(%s): %v", id, err)
		}
	}
	spawn(legacy, "legacy-before-update")
	spawn(shared, "shared-before-update")
	initialPIDs := map[string]int{
		"legacy-before-update": legacy.WorkerPIDs(context.Background())["legacy-before-update"],
		"shared-before-update": shared.WorkerPIDs(context.Background())["shared-before-update"],
	}
	defer func() {
		for _, pid := range initialPIDs {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
		_ = waitForPIDsGone(3*time.Second, initialPIDs["legacy-before-update"], initialPIDs["shared-before-update"])
	}()
	if err := legacy.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := shared.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	legacy, err = NewWorker(cfg(workerBinary))
	if err != nil {
		t.Fatal(err)
	}
	shared, err = NewSharedHost(cfg(hostBinary))
	if err != nil {
		t.Fatal(err)
	}
	migrating, err := NewMigrating(legacy, shared, true)
	if err != nil {
		t.Fatal(err)
	}
	defer migrating.Shutdown(context.Background())
	report, err := migrating.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Recovered != 2 {
		t.Fatalf("Recover() = %+v, want two sessions", report)
	}
	spawn(migrating, "shared-after-update")
	pids := migrating.WorkerPIDs(context.Background())
	if pids["legacy-before-update"] != initialPIDs["legacy-before-update"] {
		t.Fatalf("legacy session moved: pids = %v", pids)
	}
	if pids["shared-before-update"] != initialPIDs["shared-before-update"] || pids["shared-after-update"] != initialPIDs["shared-before-update"] {
		t.Fatalf("shared host ownership changed: pids = %v", pids)
	}
	for _, id := range []string{"legacy-before-update", "shared-before-update", "shared-after-update"} {
		_, stream, err := migrating.Attach(context.Background(), id, "mixed-"+id)
		if err != nil {
			t.Fatalf("Attach(%s): %v", id, err)
		}
		marker := "__ALIVE_" + id + "__"
		if err := migrating.Input(context.Background(), id, []byte(marker+"\n")); err != nil {
			t.Fatalf("Input(%s): %v", id, err)
		}
		waitForStreamText(t, stream, marker)
		_ = stream.Close()
		if err := migrating.Remove(context.Background(), id); err != nil {
			t.Fatalf("Remove(%s): %v", id, err)
		}
	}
}

func TestSharedHost_OneLifecycleStreamCoversMultipleSessions(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	root, err := os.MkdirTemp("/tmp", "attn-rust-host-lifecycle-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-host-lifecycle", BinaryPath: binary,
	})
	if err != nil {
		t.Fatal(err)
	}
	exits := make(chan ExitInfo, 2)
	backend.SetExitHandler(func(info ExitInfo) { exits <- info })

	ids := []string{"lifecycle-one", "lifecycle-two"}
	for _, id := range ids {
		if err := backend.Spawn(context.Background(), SpawnOptions{
			ID: id, CWD: root, Agent: "lifecycle-probe",
			ExternalCommand: []string{"/bin/sh", "-c", "read release"},
			Cols:            80,
			Rows:            24,
		}); err != nil {
			t.Fatalf("Spawn(%s): %v", id, err)
		}
	}
	hostPID := backend.WorkerPIDs(context.Background())[ids[0]]
	defer func() {
		_ = backend.Shutdown(context.Background())
		_ = syscall.Kill(hostPID, syscall.SIGTERM)
		_ = waitForPIDsGone(3*time.Second, hostPID)
	}()

	backend.sharedMonitorMu.Lock()
	monitorCount := len(backend.sharedMonitors)
	fallback := false
	for _, monitor := range backend.sharedMonitors {
		fallback = monitor.fallback
	}
	backend.sharedMonitorMu.Unlock()
	if monitorCount != 1 || fallback {
		t.Fatalf("shared lifecycle monitors = %d, fallback = %t; want one host stream", monitorCount, fallback)
	}
	for _, id := range ids {
		session, err := backend.getSession(id)
		if err != nil {
			t.Fatal(err)
		}
		session.mu.Lock()
		perSessionMonitor := session.monitorStop != nil
		session.mu.Unlock()
		if perSessionMonitor {
			t.Fatalf("session %s has a per-session lifecycle stream", id)
		}
		if err := backend.Input(context.Background(), id, []byte("\n")); err != nil {
			t.Fatalf("Input(%s): %v", id, err)
		}
	}

	got := make(map[string]ExitInfo, len(ids))
	deadline := time.After(8 * time.Second)
	for len(got) < len(ids) {
		select {
		case info := <-exits:
			got[info.ID] = info
		case <-deadline:
			t.Fatalf("exit notifications = %+v, want %v", got, ids)
		}
	}
	for _, id := range ids {
		if got[id].ExitCode != 0 {
			t.Fatalf("exit notification for %s = %+v, want code 0", id, got[id])
		}
		if err := backend.Remove(context.Background(), id); err != nil {
			t.Fatalf("Remove(%s): %v", id, err)
		}
	}
}

func TestSharedHost_ShellCloseUsesHangup(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	root, err := os.MkdirTemp("/tmp", "pty-hangup-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	backend, err := NewSharedHost(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-hangup", BinaryPath: binary})
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan ExitInfo, 1)
	backend.SetExitHandler(func(info ExitInfo) { exited <- info })
	if err := backend.Spawn(context.Background(), SpawnOptions{ID: "shell", CWD: root, Agent: "shell", Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	pid := backend.WorkerPIDs(context.Background())["shell"]
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()); _ = syscall.Kill(pid, syscall.SIGTERM) })
	_, stream, err := backend.Attach(context.Background(), "shell", "hangup")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := backend.Input(context.Background(), "shell", []byte("exec /bin/sh -c 'trap \"exit 23\" TERM; printf \"__REA%s__\\n\" DY; while :; do read line || exit; done'\n")); err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, stream, "__READY__")
	if err := backend.Kill(context.Background(), "shell", syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case info := <-exited:
		if info.Signal != "SIGHUP" {
			t.Fatalf("shell exit = %+v, want SIGHUP without a preceding SIGTERM", info)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing shell exit notification")
	}
}

func TestSharedHost_CrashEvictsEverySessionAndRestartsCleanly(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	root, err := os.MkdirTemp("/tmp", "attn-rust-host-crash-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-host-crash", BinaryPath: binary,
	})
	if err != nil {
		t.Fatal(err)
	}
	exits := make(chan ExitInfo, 4)
	backend.SetExitHandler(func(info ExitInfo) { exits <- info })

	for id, command := range map[string][]string{
		"exited-before-crash": {"/bin/cat"},
		"running-at-crash":    {"/bin/cat"},
	} {
		if err := backend.Spawn(context.Background(), SpawnOptions{
			ID: id, CWD: root, Agent: "crash-probe", ExternalCommand: command, Cols: 80, Rows: 24,
		}); err != nil {
			t.Fatalf("Spawn(%s): %v", id, err)
		}
	}
	oldHostPID := backend.WorkerPIDs(context.Background())["running-at-crash"]
	runningInfo, err := backend.SessionInfo(context.Background(), "running-at-crash")
	if err != nil {
		t.Fatal(err)
	}
	exitedInfo, err := backend.SessionInfo(context.Background(), "exited-before-crash")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = backend.Shutdown(context.Background())
		_ = syscall.Kill(oldHostPID, syscall.SIGKILL)
		_ = syscall.Kill(-runningInfo.PID, syscall.SIGKILL)
		_ = syscall.Kill(-exitedInfo.PID, syscall.SIGKILL)
	}()

	if err := syscall.Kill(-exitedInfo.PID, syscall.SIGTERM); err != nil {
		t.Fatalf("terminate pre-crash child: %v", err)
	}
	waitForSessionExit(t, backend, "exited-before-crash")
	waitForExitInfo(t, exits, "exited-before-crash", func(info ExitInfo) bool {
		return info.Signal == "SIGTERM"
	})
	if err := syscall.Kill(oldHostPID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill shared host: %v", err)
	}
	waitForExitInfo(t, exits, "running-at-crash", func(info ExitInfo) bool {
		return info.Signal == "worker_unreachable"
	})
	waitForSessionIDs(t, backend, nil)

	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID: "after-crash", CWD: root, Agent: "crash-probe",
		ExternalCommand: []string{"/bin/cat"}, Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatalf("Spawn(after-crash): %v", err)
	}
	newHostPID := backend.WorkerPIDs(context.Background())["after-crash"]
	defer func() {
		_ = syscall.Kill(newHostPID, syscall.SIGTERM)
		_ = waitForPIDsGone(3*time.Second, newHostPID)
	}()
	if newHostPID <= 0 || newHostPID == oldHostPID {
		t.Fatalf("restarted host pid = %d, old = %d", newHostPID, oldHostPID)
	}
	_, stream, err := backend.Attach(context.Background(), "after-crash", "crash-restart")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := backend.Input(context.Background(), "after-crash", []byte("__AFTER_CRASH__\n")); err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, stream, "__AFTER_CRASH__")
	if err := backend.Remove(context.Background(), "after-crash"); err != nil {
		t.Fatal(err)
	}
}

func TestSharedHost_FailedExecLeavesOtherSessionsHealthy(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	root, err := os.MkdirTemp("/tmp", "attn-rust-host-exec-failure-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-host-exec-failure", BinaryPath: binary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID: "healthy-before-failure", CWD: root, Agent: "exec-failure-probe",
		ExternalCommand: []string{"/bin/cat"}, Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatal(err)
	}
	hostPID := backend.WorkerPIDs(context.Background())["healthy-before-failure"]
	defer func() {
		_ = backend.Shutdown(context.Background())
		_ = syscall.Kill(hostPID, syscall.SIGTERM)
		_ = waitForPIDsGone(3*time.Second, hostPID)
	}()

	err = backend.Spawn(context.Background(), SpawnOptions{
		ID: "failed-exec", CWD: root, Agent: "exec-failure-probe",
		ExternalCommand: []string{"/definitely/missing/attn-pty-host-test"}, Cols: 80, Rows: 24,
	})
	if err == nil {
		t.Fatal("Spawn(failed-exec) succeeded")
	}
	if ids := backend.SessionIDs(context.Background()); !reflect.DeepEqual(ids, []string{"healthy-before-failure"}) {
		t.Fatalf("session ids after failed exec = %v", ids)
	}
	registryPath := ptyhost.SessionRegistryPath(root, "d-host-exec-failure", "failed-exec")
	if _, statErr := os.Stat(registryPath); !os.IsNotExist(statErr) {
		t.Fatalf("failed session registry stat error = %v, want not exist", statErr)
	}

	_, stream, err := backend.Attach(context.Background(), "healthy-before-failure", "exec-failure-health")
	if err != nil {
		hostLog, _ := os.ReadFile(ptyhost.LogPath(root, "d-host-exec-failure"))
		t.Fatalf("Attach after failed exec: %v\nhost log:\n%s", err, hostLog)
	}
	defer stream.Close()
	if err := backend.Input(context.Background(), "healthy-before-failure", []byte("__SURVIVED_EXEC_FAILURE__\n")); err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, stream, "__SURVIVED_EXEC_FAILURE__")
	if got := backend.WorkerPIDs(context.Background())["healthy-before-failure"]; got != hostPID {
		t.Fatalf("host pid after failed exec = %d, want %d", got, hostPID)
	}
	if err := backend.Remove(context.Background(), "healthy-before-failure"); err != nil {
		t.Fatal(err)
	}
}

func TestSharedHost_BlockedTerminationDoesNotBlockAnotherSession(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	root, err := os.MkdirTemp("/tmp", "attn-rust-host-fairness-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-host-fairness", BinaryPath: binary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID: "slow-stop", CWD: root, Agent: "fairness-probe",
		ExternalCommand: []string{"/bin/sh", "-c", `trap 'printf "__TERM_RECEIVED__\n"; trap "" TERM HUP; while :; do read hold || true; done' TERM; printf "__READY__\n"; while :; do read hold || true; done`},
		Cols:            80,
		Rows:            24,
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID: "responsive", CWD: root, Agent: "fairness-probe",
		ExternalCommand: []string{"/bin/cat"}, Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatal(err)
	}
	hostPID := backend.WorkerPIDs(context.Background())["slow-stop"]
	defer func() {
		_ = backend.Shutdown(context.Background())
		_ = syscall.Kill(hostPID, syscall.SIGTERM)
		_ = waitForPIDsGone(3*time.Second, hostPID)
	}()
	_, slowStream, err := backend.Attach(context.Background(), "slow-stop", "fairness-slow")
	if err != nil {
		t.Fatal(err)
	}
	defer slowStream.Close()
	waitForStreamText(t, slowStream, "__READY__")
	slowInfo, err := backend.SessionInfo(context.Background(), "slow-stop")
	if err != nil {
		t.Fatal(err)
	}
	_, stream, err := backend.Attach(context.Background(), "responsive", "fairness-fast")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	killDone := make(chan error, 1)
	go func() { killDone <- backend.Kill(context.Background(), "slow-stop", syscall.SIGTERM) }()
	waitForStreamText(t, slowStream, "__TERM_RECEIVED__")

	inputCtx, cancelInput := context.WithTimeout(context.Background(), time.Second)
	err = backend.Input(inputCtx, "responsive", []byte("__STILL_RESPONSIVE__\n"))
	cancelInput()
	if err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, stream, "__STILL_RESPONSIVE__")
	if err := syscall.Kill(-slowInfo.PID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill slow test process group: %v", err)
	}
	select {
	case err := <-killDone:
		if err != nil {
			t.Fatalf("termination call: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("termination call did not observe the child exit")
	}
}

func TestSharedHost_BinaryUpgradeLeavesOldSessionsOnOldHost(t *testing.T) {
	original := os.Getenv("ATTN_TEST_PTY_HOST")
	if original == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	root, err := os.MkdirTemp("/tmp", "attn-rust-host-upgrade-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	upgraded := root + "/attn-pty-host-next"
	binary, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(upgraded, append(binary, 0), 0o700); err != nil {
		t.Fatal(err)
	}

	base := WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-host-upgrade"}
	base.BinaryPath = original
	oldBackend, err := NewSharedHost(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := oldBackend.Spawn(context.Background(), SpawnOptions{
		ID: "before-upgrade", CWD: t.TempDir(), Agent: "shell", Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatal(err)
	}
	oldPID := oldBackend.WorkerPIDs(context.Background())["before-upgrade"]
	if err := oldBackend.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	base.BinaryPath = upgraded
	newBackend, err := NewSharedHost(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newBackend.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := newBackend.Spawn(context.Background(), SpawnOptions{
		ID: "after-upgrade", CWD: t.TempDir(), Agent: "shell", Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatal(err)
	}
	pids := newBackend.WorkerPIDs(context.Background())
	newPID := pids["after-upgrade"]
	defer func() {
		_ = syscall.Kill(oldPID, syscall.SIGTERM)
		_ = syscall.Kill(newPID, syscall.SIGTERM)
		_ = waitForPIDsGone(3*time.Second, oldPID, newPID)
	}()
	if oldPID <= 0 || newPID <= 0 || oldPID == newPID {
		t.Fatalf("host pids = %v, want old and new sessions on distinct hosts", pids)
	}
	for _, id := range []string{"before-upgrade", "after-upgrade"} {
		if _, err := newBackend.SessionInfo(context.Background(), id); err != nil {
			t.Fatalf("SessionInfo(%s): %v", id, err)
		}
		if err := newBackend.Remove(context.Background(), id); err != nil {
			t.Fatalf("Remove(%s): %v", id, err)
		}
	}
}

func TestSharedHost_OldGenerationFallsBackToPortableReplay(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	root, err := os.MkdirTemp("/tmp", "attn-rust-host-portable-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-host-portable", BinaryPath: binary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID: "portable", CWD: t.TempDir(), Agent: "shell", Cols: 80, Rows: 24,
	}); err != nil {
		t.Fatal(err)
	}
	hostPID := backend.WorkerPIDs(context.Background())["portable"]
	defer func() {
		_ = syscall.Kill(hostPID, syscall.SIGTERM)
		_ = waitForPIDsGone(3*time.Second, hostPID)
	}()

	_, live, err := backend.Attach(context.Background(), "portable", "portable-live", AttachOptions{OmitReplay: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Input(context.Background(), "portable", []byte("printf '__PORTABLE_REPLAY__\\n'\n")); err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, live, "__PORTABLE_REPLAY__")
	_ = live.Close()

	info, replay, err := backend.Attach(context.Background(), "portable", "portable-replay")
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	if len(info.GhosttySnapshot) != 0 {
		t.Fatalf("native snapshot bytes = %d, want portable replay for mismatched formats", len(info.GhosttySnapshot))
	}
	event := waitForStreamText(t, replay, "__PORTABLE_REPLAY__")
	if !bytes.HasPrefix(event.Data, []byte("\x1bc")) {
		t.Fatalf("portable replay prefix = %q, want terminal reset", event.Data[:min(len(event.Data), 8)])
	}
	if err := backend.Remove(context.Background(), "portable"); err != nil {
		t.Fatal(err)
	}
}

func TestSharedHost_KittyPlacementsAndImagePull(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	t.Setenv("ATTN_KITTY_STORAGE_LIMIT", "16777216")
	root, err := os.MkdirTemp("/tmp", "attn-rust-host-kitty-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-host-kitty", BinaryPath: binary,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := kittyPayloadFile(t, "\x1b[3;1H"+kittyPlaceRGB(90, 16, 32))
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID:              "shared-kitty",
		CWD:             t.TempDir(),
		Agent:           "probe-kitty",
		ExternalCommand: []string{"/bin/sh", "-c", "read release; cat " + payload},
		Cols:            40,
		Rows:            12,
	}); err != nil {
		t.Fatal(err)
	}
	hostPID := backend.WorkerPIDs(context.Background())["shared-kitty"]
	defer func() {
		_ = syscall.Kill(hostPID, syscall.SIGTERM)
		_ = waitForPIDsGone(3*time.Second, hostPID)
	}()

	_, stream, err := backend.Attach(context.Background(), "shared-kitty", "kitty-sub")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	event := releaseAndReadPlacements(t, stream, func() error {
		return backend.Input(context.Background(), "shared-kitty", []byte("\n"))
	})
	if len(event.Placements) != 1 || event.Placements[0].ImageID != 90 {
		t.Fatalf("placements = %+v, want image 90", event.Placements)
	}
	image, err := backend.KittyImage(context.Background(), "shared-kitty", 90)
	if err != nil {
		t.Fatal(err)
	}
	if image.Width != 16 || image.Height != 32 || len(image.Data) != 16*32*3 {
		t.Fatalf("image = %dx%d, %d bytes", image.Width, image.Height, len(image.Data))
	}
	if image.Generation != event.Placements[0].ImageGeneration {
		t.Fatalf("image generation = %d, placement = %d", image.Generation, event.Placements[0].ImageGeneration)
	}
}

func TestSharedHost_TerminalQueriesUseTheHostModel(t *testing.T) {
	if os.Getenv("ATTN_SHARED_QUERY_HELPER") == "1" {
		_, _ = os.Stdout.Write([]byte("\x1b[3;7H\x1b[6n\x1b[0c"))
		reply := make([]byte, len("\x1b[3;7R\x1b[?1;2c"))
		if _, err := io.ReadFull(os.Stdin, reply); err != nil {
			os.Exit(2)
		}
		_, _ = fmt.Fprintf(os.Stdout, "__QUERY_REPLY_%x__", reply)
		os.Exit(0)
	}
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	root, err := os.MkdirTemp("/tmp", "attn-rust-host-query-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-host-query", BinaryPath: binary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID:    "shared-query",
		CWD:   t.TempDir(),
		Agent: "probe-query",
		ExternalCommand: []string{
			"/bin/sh", "-c", "stty raw -echo; exec \"$1\" -test.run=TestSharedHost_TerminalQueriesUseTheHostModel", "query-helper", os.Args[0],
		},
		ExternalEnv: []string{"ATTN_SHARED_QUERY_HELPER=1"},
		Cols:        40,
		Rows:        12,
	}); err != nil {
		t.Fatal(err)
	}
	hostPID := backend.WorkerPIDs(context.Background())["shared-query"]
	defer func() {
		_ = syscall.Kill(hostPID, syscall.SIGTERM)
		_ = waitForPIDsGone(3*time.Second, hostPID)
	}()
	_, stream, err := backend.Attach(context.Background(), "shared-query", "query-sub")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	want := "__QUERY_REPLY_1b5b333b37521b5b3f313b3263__"
	waitForStreamText(t, stream, want)
}

func TestSharedHost_AttachCarriesCommandBlocks(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to an attn-pty-host binary")
	}
	root, err := os.MkdirTemp("/tmp", "attn-rust-host-blocks-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: root, DaemonInstanceID: "d-host-blocks", BinaryPath: binary,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := "\x1b]133;A\x07$ \x1b]133;B\x07echo hi\x1b]133;C;cmdline_url=echo%20hi\x07\r\nhi\r\n\x1b]133;D;0\x07__BLOCK_DONE__"
	path := kittyPayloadFile(t, payload)
	if err := backend.Spawn(context.Background(), SpawnOptions{
		ID:              "shared-blocks",
		CWD:             t.TempDir(),
		Agent:           "probe-blocks",
		ExternalCommand: []string{"/bin/sh", "-c", "read release; cat " + path + "; read hold"},
		Cols:            40,
		Rows:            12,
	}); err != nil {
		t.Fatal(err)
	}
	hostPID := backend.WorkerPIDs(context.Background())["shared-blocks"]
	defer func() {
		_ = syscall.Kill(hostPID, syscall.SIGTERM)
		_ = waitForPIDsGone(3*time.Second, hostPID)
	}()
	_, live, err := backend.Attach(context.Background(), "shared-blocks", "blocks-live")
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Input(context.Background(), "shared-blocks", []byte("\n")); err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, live, "__BLOCK_DONE__")
	_ = live.Close()
	info, replay, err := backend.Attach(context.Background(), "shared-blocks", "blocks-replay")
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	if len(info.GhosttyBlocks) != 1 {
		t.Fatalf("blocks = %+v, want one completed command", info.GhosttyBlocks)
	}
	block := info.GhosttyBlocks[0]
	if block.Command == nil || *block.Command != "echo hi" || block.ExitCode == nil || *block.ExitCode != 0 {
		t.Fatalf("block = %+v, want echo hi with exit 0", block)
	}
}

func waitForStreamText(t *testing.T, stream Stream, want string) OutputEvent {
	t.Helper()
	deadline := time.After(8 * time.Second)
	var output bytes.Buffer
	for {
		select {
		case event := <-stream.Events():
			if event.Kind != OutputEventKindOutput {
				continue
			}
			output.Write(event.Data)
			if strings.Contains(output.String(), want) {
				return event
			}
		case <-deadline:
			t.Fatalf("output = %q, want %q", output.String(), want)
		}
	}
}

func waitForExitInfo(t *testing.T, exits <-chan ExitInfo, id string, matches func(ExitInfo) bool) {
	t.Helper()
	deadline := time.After(8 * time.Second)
	for {
		select {
		case info := <-exits:
			if info.ID == id && matches(info) {
				return
			}
		case <-deadline:
			t.Fatalf("did not receive matching exit for %s", id)
		}
	}
}

func waitForSessionIDs(t *testing.T, backend *WorkerBackend, want []string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		ids := backend.SessionIDs(context.Background())
		sort.Strings(ids)
		if len(ids) == 0 && len(want) == 0 || reflect.DeepEqual(ids, want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session ids = %v, want %v", backend.SessionIDs(context.Background()), want)
}

func waitForSessionExit(t *testing.T, backend *WorkerBackend, id string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		info, err := backend.SessionInfo(context.Background(), id)
		if err == nil && !info.Running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	hostLog, _ := os.ReadFile(ptyhost.LogPath(backend.cfg.DataRoot, backend.cfg.DaemonInstanceID))
	info, infoErr := backend.SessionInfo(context.Background(), id)
	t.Fatalf("session %s did not exit: info=%+v err=%v\nhost log:\n%s", id, info, infoErr, hostLog)
}
