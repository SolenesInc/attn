package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestASpawnWhoseWorkerDiesBeforeReadyIsRefusedPromptlyAndLeavesNothing(t *testing.T) {
	dying := filepath.Join(t.TempDir(), "dying-worker")
	if err := os.WriteFile(dying, []byte("#!/bin/sh\nexit 17\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATTN_PTY_BACKEND", "worker")
	t.Setenv("ATTN_PTY_WORKER_BINARY", dying)
	w := newWorld(t)
	app := w.App()
	refused := refuseSpawnLikeTheApp(w, app, shellHarness, w.Path("shop"))
	if !strings.Contains(protocol.Deref(refused.Error), "worker exited before ready") {
		t.Errorf("the spawn was refused with %q, want it to say the worker exited before ready", protocol.Deref(refused.Error))
	}
}
