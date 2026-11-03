package daemon_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestHealthReportsCanonicalRoutingPathsForRelativeSymlinkedOverrides(t *testing.T) {
	w := newWorld(t)
	canonicalDataDir, err := filepath.EvalSymlinks(w.Dir)
	if err != nil {
		t.Fatal(err)
	}
	workingDir := t.TempDir()
	if err := os.Symlink(w.Dir, filepath.Join(workingDir, "linked")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workingDir)
	t.Setenv("ATTN_DATA_DIR", "linked")
	t.Setenv("ATTN_SOCKET_PATH", filepath.Join("linked", "attn.sock"))
	w.restart()

	resp, err := http.Get("http://" + w.WSAddr + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var health struct {
		DataDir          string `json:"data_dir"`
		SocketPath       string `json:"socket_path"`
		RoutingPathError string `json:"routing_path_error"`
		DaemonInstanceID string `json:"daemon_instance_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(canonicalDataDir, "attn.sock"); health.DataDir != canonicalDataDir || health.SocketPath != want || health.RoutingPathError != "" {
		t.Errorf("health routes to data_dir %q and socket_path %q (error %q), want %q and %q", health.DataDir, health.SocketPath, health.RoutingPathError, canonicalDataDir, want)
	}
	if got := protocol.Deref(w.App().Initial.DaemonInstanceID); got == "" || got != health.DaemonInstanceID {
		t.Errorf("the app is told it reached daemon instance %q, want the running one %q", got, health.DaemonInstanceID)
	}
}
