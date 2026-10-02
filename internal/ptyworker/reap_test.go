package ptyworker

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/procreap"
)

type helloAnswer int

const (
	acceptHello helloAnswer = iota
	rejectHello
	exitOnRemove
)

type fakeWorker struct {
	listener  net.Listener
	gotHello  chan HelloParams
	gotRemove chan struct{}
	answer    helloAnswer
	proc      *exec.Cmd
}

func startFakeWorker(t *testing.T, dir string, answer helloAnswer) *fakeWorker {
	t.Helper()
	sockDir, err := os.MkdirTemp("", "reap")
	if err != nil {
		t.Fatalf("temp sock dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })

	ln, err := net.Listen("unix", filepath.Join(sockDir, "w.sock"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	w := &fakeWorker{
		listener:  ln,
		gotHello:  make(chan HelloParams, 1),
		gotRemove: make(chan struct{}, 1),
		answer:    answer,
		proc:      spawnSleeper(t, "fake-worker-"+filepath.Base(sockDir)),
	}
	t.Cleanup(func() { _ = ln.Close() })
	go w.serve()
	return w
}

func (w *fakeWorker) addr() string { return w.listener.Addr().String() }

func (w *fakeWorker) serve() {
	for {
		conn, err := w.listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = conn.Close() }()
			dec := json.NewDecoder(conn)
			enc := json.NewEncoder(conn)
			for {
				var req RequestEnvelope
				if err := dec.Decode(&req); err != nil {
					return
				}
				switch req.Method {
				case MethodHello:
					var hp HelloParams
					_ = json.Unmarshal(req.Params, &hp)
					select {
					case w.gotHello <- hp:
					default:
					}
					if w.answer == rejectHello {
						_ = enc.Encode(ResponseEnvelope{
							Type: "res", ID: req.ID, OK: false,
							Error: &RPCError{Code: ErrUnauthorized, Message: "bad token"},
						})
						return
					}
					_ = enc.Encode(ResponseEnvelope{Type: "res", ID: req.ID, OK: true})
				case MethodRemove:
					select {
					case w.gotRemove <- struct{}{}:
					default:
					}
					if w.answer == exitOnRemove {
						_ = w.proc.Process.Kill()
						return
					}
					_ = enc.Encode(ResponseEnvelope{Type: "res", ID: req.ID, OK: true})
					_ = w.proc.Process.Signal(syscall.SIGTERM)
				default:
					_ = enc.Encode(ResponseEnvelope{
						Type: "res", ID: req.ID, OK: false,
						Error: &RPCError{Code: ErrBadRequest, Message: "unknown method"},
					})
				}
			}
		}()
	}
}

func spawnSleeper(t *testing.T, marker string) *exec.Cmd {
	t.Helper()
	stdout, ready, err := os.Pipe()
	if err != nil {
		t.Fatalf("sleeper readiness pipe: %v", err)
	}
	cmd := exec.Command("sh", "-c", "echo ready; sleep 60; :", marker)
	cmd.Stdout = ready
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = ready.Close()
		t.Fatalf("start sleeper: %v", err)
	}
	_ = ready.Close()
	t.Cleanup(func() {
		_ = stdout.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	if err := stdout.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatalf("sleeper readiness deadline: %v", err)
	}
	if _, err := stdout.Read(make([]byte, len("ready\n"))); err != nil {
		t.Fatalf("sleeper never signalled readiness (marker %q): %v", marker, err)
	}
	return cmd
}

func writeEntry(t *testing.T, dataDir, sessionID string, entry RegistryEntry) string {
	t.Helper()
	path := filepath.Join(dataDir, "workers", "d-1", "registry", sessionID+".json")
	if err := WriteRegistryAtomic(path, entry); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	return path
}

func TestReapDataDirRemovesViaControlSocket(t *testing.T) {
	dataDir := t.TempDir()
	worker := startFakeWorker(t, dataDir, acceptHello)

	writeEntry(t, dataDir, "sess-1", RegistryEntry{
		Version:          1,
		DaemonInstanceID: "d-1",
		SessionID:        "sess-1",
		WorkerPID:        worker.proc.Process.Pid,
		SocketPath:       worker.addr(),
		ControlToken:     "tok-abc",
	})

	results := ReapDataDir(dataDir)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Outcome != ReapRemoved {
		t.Fatalf("outcome = %s (err=%v), want %s", results[0].Outcome, results[0].Err, ReapRemoved)
	}
	if procreap.ProcessAlive(worker.proc.Process.Pid) {
		t.Error("worker still alive after accepting remove")
	}

	select {
	case hp := <-worker.gotHello:
		if hp.ControlToken != "tok-abc" {
			t.Errorf("control token = %q, want %q", hp.ControlToken, "tok-abc")
		}
		if hp.DaemonInstanceID != "d-1" {
			t.Errorf("daemon instance = %q, want %q", hp.DaemonInstanceID, "d-1")
		}
	default:
		t.Fatal("worker never received hello")
	}
	select {
	case <-worker.gotRemove:
	default:
		t.Fatal("worker never received remove")
	}
}

func TestReapDataDirReportsAlreadyGone(t *testing.T) {
	dataDir := t.TempDir()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}
	writeEntry(t, dataDir, "sess-dead", RegistryEntry{
		Version: 1, SessionID: "sess-dead", WorkerPID: cmd.Process.Pid,
	})

	results := ReapDataDir(dataDir)
	if len(results) != 1 || results[0].Outcome != ReapAlreadyGone {
		t.Fatalf("outcome = %+v, want %s", results, ReapAlreadyGone)
	}
}

func TestReapDataDirRemovesTheHandoffOfADeadWorker(t *testing.T) {
	dataDir := t.TempDir()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}
	registryPath := writeEntry(t, dataDir, "sess-dead", RegistryEntry{
		Version: 1, SessionID: "sess-dead", WorkerPID: cmd.Process.Pid,
	})
	jsonPath, dumpPath := HandoffPaths(registryPath, "sess-dead")
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0700); err != nil {
		t.Fatalf("mkdir handoff: %v", err)
	}
	for _, path := range []string{jsonPath, dumpPath} {
		if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}

	ReapDataDir(dataDir)

	for _, path := range []string{jsonPath, dumpPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived the reap (err=%v)", path, err)
		}
	}
}

func TestReapDataDirPreservesIdentifiedWorkerWhenSocketUnreachable(t *testing.T) {
	dataDir := t.TempDir()
	registryPath := filepath.Join(dataDir, "workers", "d-1", "registry", "sess-wedged.json")
	cmd := spawnSleeper(t, registryPath)

	writeEntry(t, dataDir, "sess-wedged", RegistryEntry{
		Version:    1,
		SessionID:  "sess-wedged",
		WorkerPID:  cmd.Process.Pid,
		SocketPath: filepath.Join(dataDir, "nonexistent.sock"),
	})

	results := ReapDataDir(dataDir)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Outcome != ReapFailed {
		t.Fatalf("outcome = %s (err=%v), want %s", results[0].Outcome, results[0].Err, ReapFailed)
	}
	if !procreap.ProcessAlive(cmd.Process.Pid) {
		t.Fatal("unreachable worker was killed before child removal was confirmed")
	}
}

func TestReapDataDirRefusesToSignalUnidentifiedProcess(t *testing.T) {
	dataDir := t.TempDir()
	cmd := spawnSleeper(t, "unrelated-process-marker")

	writeEntry(t, dataDir, "sess-reused", RegistryEntry{
		Version:    1,
		SessionID:  "sess-reused",
		WorkerPID:  cmd.Process.Pid,
		SocketPath: filepath.Join(dataDir, "nonexistent.sock"),
	})

	results := ReapDataDir(dataDir)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Outcome != ReapUnidentified {
		t.Fatalf("outcome = %s, want %s", results[0].Outcome, ReapUnidentified)
	}
	if !procreap.ProcessAlive(cmd.Process.Pid) {
		t.Fatal("reap signalled a process it could not identify")
	}
}

func TestReapDataDirCountsAWorkerThatExitsDuringTheRemoveAsGone(t *testing.T) {
	dataDir := t.TempDir()
	worker := startFakeWorker(t, dataDir, exitOnRemove)
	writeEntry(t, dataDir, "sess-exiting", RegistryEntry{
		Version:    1,
		SessionID:  "sess-exiting",
		WorkerPID:  worker.proc.Process.Pid,
		SocketPath: worker.addr(),
	})

	results := ReapDataDir(dataDir)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Outcome != ReapAlreadyGone || results[0].Err != nil {
		t.Fatalf("outcome = %s (err=%v), want %s", results[0].Outcome, results[0].Err, ReapAlreadyGone)
	}
}

func TestReapDataDirDoesNotSignalOnAuthFailure(t *testing.T) {
	dataDir := t.TempDir()
	worker := startFakeWorker(t, dataDir, rejectHello)
	cmd := worker.proc

	writeEntry(t, dataDir, "sess-auth", RegistryEntry{
		Version:          1,
		DaemonInstanceID: "d-1",
		SessionID:        "sess-auth",
		WorkerPID:        cmd.Process.Pid,
		SocketPath:       worker.addr(),
		ControlToken:     "wrong",
	})

	results := ReapDataDir(dataDir)
	if len(results) != 1 || results[0].Outcome != ReapUnidentified {
		t.Fatalf("outcome = %+v, want %s", results, ReapUnidentified)
	}
	if results[0].Err == nil {
		t.Error("expected the auth rejection to be reported as the reason")
	}
	if !procreap.ProcessAlive(cmd.Process.Pid) {
		t.Fatal("reap signalled a worker that merely rejected auth")
	}
}

func TestReapDataDirVisitsEveryInstance(t *testing.T) {
	dataDir := t.TempDir()
	for _, inst := range []string{"d-old", "d-new"} {
		path := filepath.Join(dataDir, "workers", inst, "registry", "s.json")
		if err := WriteRegistryAtomic(path, RegistryEntry{Version: 1, SessionID: inst, WorkerPID: -1}); err != nil {
			t.Fatalf("write registry: %v", err)
		}
	}
	if got := len(ReapDataDir(dataDir)); got != 2 {
		t.Fatalf("results = %d, want 2 (one per daemon instance)", got)
	}
}
