package ptybackend

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/procwait"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func pidExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func waitForPIDsGone(timeout time.Duration, pids ...int) bool {
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		if exited, err := procwait.Exited(pid, timeout); err != nil || !exited {
			return false
		}
	}
	return true
}

func awaitSharedHostRemoval(t *testing.T, backend *WorkerBackend, id harness.TerminalID, cwd string) {
	t.Helper()
	if err := backend.Spawn(context.Background(), SpawnOptions{ID: id, CWD: cwd, Agent: "removal-probe", Cols: 80, Rows: 24, AgentArgv: []string{"/bin/cat"}}); err != nil {
		t.Fatalf("spawn that waits for the removal to finish: %v", err)
	}
}

func kittyPlaceRGB(id uint32, w, h int) string {
	pix := make([]byte, w*h*3)
	for i := range pix {
		pix[i] = byte((i*7 + 13) % 251)
	}
	return fmt.Sprintf("\x1b_Ga=T,i=%d,f=24,t=d,s=%d,v=%d;%s\x1b\\",
		id, w, h, base64.StdEncoding.EncodeToString(pix))
}

func kittyPayloadFile(t *testing.T, payload string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	return path
}

func releaseAndReadPlacements(t *testing.T, stream Stream, release func() error) OutputEvent {
	t.Helper()
	if err := release(); err != nil {
		t.Fatalf("release the child: %v", err)
	}
	deadline := time.After(fakeagent.HangGuard)
	for {
		select {
		case evt, ok := <-stream.Events():
			if !ok {
				t.Fatal("the stream closed before any placement arrived")
			}
			if evt.Kind == OutputEventKindPlacements {
				return evt
			}
		case <-deadline:
			t.Fatal("timed out waiting for a placement event")
		}
	}
}
