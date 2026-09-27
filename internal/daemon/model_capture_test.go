package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/modelcapture"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
)

type modelCaptureBackend struct {
	*fakeSpawnBackend
	mu        sync.Mutex
	snapshots map[string]pty.ScreenSnapshotInfo
	calls     map[string]int
}

func (b *modelCaptureBackend) ScreenSnapshot(_ context.Context, sessionID string) (pty.ScreenSnapshotInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls[sessionID]++
	return b.snapshots[sessionID], nil
}

func TestModelCapturePassIsOptInAndCapturesOnlyAgentViewports(t *testing.T) {
	d := NewForTesting(t.TempDir())
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	for _, session := range []*protocol.Session{
		{ID: "codex-session", Agent: "codex", Label: "Codex", State: protocol.SessionStateWorking, StateSince: now.Format(time.RFC3339), StateUpdatedAt: now.Format(time.RFC3339), LastSeen: now.Format(time.RFC3339)},
		{ID: "shell-session", Agent: "shell", Label: "Shell", State: protocol.SessionStateIdle, StateSince: now.Format(time.RFC3339), StateUpdatedAt: now.Format(time.RFC3339), LastSeen: now.Format(time.RFC3339)},
	} {
		if err := d.store.AddChecked(session); err != nil {
			t.Fatalf("add session %s: %v", session.ID, err)
		}
	}
	d.stateReasons().set("codex-session", "heartbeat_busy")
	backend := &modelCaptureBackend{
		fakeSpawnBackend: &fakeSpawnBackend{sessionIDs: []string{"codex-session", "shell-session"}},
		snapshots: map[string]pty.ScreenSnapshotInfo{
			"codex-session": {
				LastSeq: 9,
				Running: true,
				Screen: &pty.ViewportSnapshot{
					Text:    "• Working\n\n› Explain this codebase",
					HasText: true,
					Cols:    80,
					Rows:    24,
				},
			},
			"shell-session": {
				Running: true,
				Screen:  &pty.ViewportSnapshot{Text: "$ ", HasText: true, Cols: 80, Rows: 24},
			},
		},
		calls: make(map[string]int),
	}
	d.ptyBackend = backend
	recorder := modelcapture.New(d.modelCaptureDir())

	d.modelCapturePass(recorder, now)
	if backend.calls["codex-session"] != 0 {
		t.Fatal("capture should be disabled by default")
	}
	if _, err := os.Stat(d.modelCaptureDir()); !os.IsNotExist(err) {
		t.Fatalf("disabled capture should not create a directory, stat err=%v", err)
	}

	d.store.SetSetting(SettingModelCaptureEnabled, "true")
	d.modelCapturePass(recorder, now)
	if backend.calls["codex-session"] != 1 {
		t.Fatalf("codex snapshot calls = %d, want 1", backend.calls["codex-session"])
	}
	if backend.calls["shell-session"] != 0 {
		t.Fatalf("shell snapshot calls = %d, want 0", backend.calls["shell-session"])
	}

	files, err := filepath.Glob(filepath.Join(d.modelCaptureDir(), "observations-*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("capture files = %v, err=%v", files, err)
	}
	records := readCaptureJSONL(t, files[0])
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}
	if got := records[0]["viewport_text"]; got != "• Working\n\n› Explain this codebase" {
		t.Fatalf("viewport_text = %#v", got)
	}
	if got := records[0]["daemon_state"]; got != protocol.StateWorking {
		t.Fatalf("daemon_state = %#v, want working", got)
	}
	if got := records[0]["state_reason"]; got != "heartbeat_busy" {
		t.Fatalf("state_reason = %#v", got)
	}
	if got := records[0]["session_key"]; got == "codex-session" || got == "" {
		t.Fatalf("session_key should be hashed, got %#v", got)
	}

	d.modelCapturePass(recorder, now.Add(defaultModelCaptureIntervalSeconds*time.Second))
	if backend.calls["codex-session"] != 2 {
		t.Fatalf("interval snapshot calls = %d, want 2", backend.calls["codex-session"])
	}
	if got := len(readCaptureJSONL(t, files[0])); got != 1 {
		t.Fatalf("deduplicated record count = %d, want 1", got)
	}
}

func readCaptureJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open capture JSONL: %v", err)
	}
	defer f.Close()
	var records []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode capture JSONL: %v", err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan capture JSONL: %v", err)
	}
	return records
}
