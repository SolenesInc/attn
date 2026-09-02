package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
)

// Receipt: an 80x24 pi launch error renders to 198 bytes of viewport text and a
// fully painted 400x120 viewport of 3-byte glyphs to 142,920. 256KiB is a tripwire.
const exitScreenMaxBytes = 256 * 1024

const exitScreenSnapshotTimeout = modelCaptureSnapshotTimeout

// Runs before the worker is removed: that removal is what discards the screen.
func (d *Daemon) captureExitScreen(info ptybackend.ExitInfo) {
	if d.store == nil || d.store.Get(info.ID) == nil {
		return
	}
	rec := store.SessionExitScreen{SessionID: info.ID, ExitCode: info.ExitCode, ExitSignal: info.Signal}
	if provider, ok := d.ptyBackend.(ptybackend.ScreenSnapshotProvider); ok {
		ctx, cancel := context.WithTimeout(context.Background(), exitScreenSnapshotTimeout)
		snapshot, err := provider.ScreenSnapshot(ctx, info.ID)
		cancel()
		switch {
		case err != nil:
			d.logf("exit screen unavailable: session=%s err=%v", info.ID, err)
		case snapshot.Screen != nil && snapshot.Screen.HasText:
			rec.Text = clampExitScreenText(snapshot.Screen.Text)
			rec.Cols = int(snapshot.Screen.Cols)
			rec.Rows = int(snapshot.Screen.Rows)
		}
	}
	if err := d.store.SaveSessionExitScreen(rec, time.Now()); err != nil {
		d.logf("exit screen not kept: session=%s err=%v", info.ID, err)
		return
	}
	d.logf("exit screen kept: session=%s code=%d signal=%q text_bytes=%d", info.ID, info.ExitCode, info.Signal, len(rec.Text))
}

// The error that killed a process is at the bottom, so the tail survives.
func clampExitScreenText(text string) string {
	if len(text) <= exitScreenMaxBytes {
		return text
	}
	tail := text[len(text)-exitScreenMaxBytes:]
	if cut := strings.IndexByte(tail, '\n'); cut >= 0 {
		tail = tail[cut+1:]
	}
	return fmt.Sprintf("[exit screen truncated: %d bytes rendered, attn keeps the last %d]\n%s", len(text), exitScreenMaxBytes, tail)
}

// A failed respawn leaves the dead process as the last one, so its receipt
// stays unless the attempt itself produced a newer exit.
func (d *Daemon) restoreExitScreen(sessionID string, prior *store.SessionExitScreen) {
	if prior == nil || d.store.GetSessionExitScreen(sessionID) != nil {
		return
	}
	exitedAt, err := time.Parse(time.RFC3339Nano, prior.ExitedAt)
	if err != nil {
		exitedAt = time.Now()
	}
	if err := d.store.SaveSessionExitScreen(*prior, exitedAt); err != nil {
		d.logf("exit screen of the previous process not restored: session=%s err=%v", sessionID, err)
	}
}
