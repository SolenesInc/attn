package ptybackend

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		alive := false
		for _, pid := range pids {
			if pidExists(pid) {
				alive = true
				break
			}
		}
		if !alive {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func buildAttnBinary(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	binary := filepath.Join(t.TempDir(), "attn-test-bin")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/attn")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build attn binary: %v\n%s", err, string(output))
	}
	return binary
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
	deadline := time.After(15 * time.Second)
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
