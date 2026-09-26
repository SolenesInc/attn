package daemon

import (
	"context"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
)

type snapshotBackend struct {
	*fakeSpawnBackend
	snapshot pty.ScreenSnapshotInfo
}

func (b *snapshotBackend) ScreenSnapshot(context.Context, string) (pty.ScreenSnapshotInfo, error) {
	return b.snapshot, nil
}

func TestHandleGetScreenSnapshotLeavesObserverUnseededWithoutViewport(t *testing.T) {
	d := NewForTesting(t.TempDir())
	d.ptyBackend = &snapshotBackend{
		fakeSpawnBackend: &fakeSpawnBackend{},
		snapshot:         pty.ScreenSnapshotInfo{LastSeq: 42, Cols: 100, Rows: 30, Running: true},
	}
	client := &wsClient{send: make(chan outboundMessage, 1)}

	d.handleGetScreenSnapshot(client, &protocol.GetScreenSnapshotMessage{ID: "session-1"})

	var result protocol.GetScreenSnapshotResultMessage
	readNotebookWSEvent(t, client.send, &result)
	if !result.Success {
		t.Fatalf("snapshot result failed: %v", result.Error)
	}
	if result.ScreenSnapshot != nil || result.ScreenCols != nil || result.ScreenRows != nil {
		t.Fatalf("expected unseeded observer result, got %+v", result)
	}
}
