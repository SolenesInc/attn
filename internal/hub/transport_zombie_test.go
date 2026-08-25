//go:build !windows

package hub

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The leak was cmd.Process.Kill with no matching cmd.Wait, leaving a <defunct>
// child per failed dial until the per-user process limit was hit.
func TestConnectViaSSHOnceReapsChildOnDialFailure(t *testing.T) {
	shimDir := t.TempDir()
	shim := filepath.Join(shimDir, "ssh")
	script := "#!/bin/sh\nprintf 'HTTP/1.1 502 Bad Gateway\\r\\nContent-Length: 0\\r\\n\\r\\n'\nsleep 10\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	ws, cmd, err := connectViaSSHOnce(ctx, "fake-target", "", "")
	cancel()
	if err == nil {
		if ws != nil {
			_ = ws.CloseNow()
		}
		killAndReap(cmd)
		t.Fatal("expected dial failure via shim, got success")
	}

	zombies := zombieChildrenOf(t, os.Getpid())
	if zombies > 0 {
		t.Fatalf("after failed dial: found %d zombie child ssh processes (expected 0)", zombies)
	}
}

// `ps` without -A scopes to the controlling tty and misses detached children in CI.
func zombieChildrenOf(t *testing.T, parent int) int {
	t.Helper()
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,stat=").Output()
	if err != nil {
		t.Fatalf("ps failed: %v", err)
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 {
			continue
		}
		var pid, ppid int
		if _, err := fmt.Sscanf(fields[0], "%d", &pid); err != nil {
			continue
		}
		if _, err := fmt.Sscanf(fields[1], "%d", &ppid); err != nil {
			continue
		}
		if ppid == parent && strings.HasPrefix(fields[2], "Z") {
			count++
		}
	}
	return count
}
