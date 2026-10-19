package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/store"
)

const exitScreenMaxBytes = 256 * 1024

const exitScreenSnapshotTimeout = 3 * time.Second

func (d *Daemon) sessionExit(session protocol.SessionID) *protocol.TerminalExit {
	if view, ok := d.monitorViewFor(session); ok {
		if exit := view.Exit.V; exit != nil {
			result := &protocol.TerminalExit{Code: exit.Code, At: string(protocol.NewTimestamp(exit.At))}
			if exit.Signal != "" {
				result.Signal = protocol.Ptr(exit.Signal)
			}
			return result
		}
		return nil
	}
	return d.store.GetSessionExit(session)
}

func (d *Daemon) captureExitScreen(sessionID protocol.SessionID, info ptybackend.ExitInfo) {
	if d.store == nil || d.store.Get(sessionID) == nil {
		return
	}
	if entry := d.monitorEntry(sessionID); entry != nil {
		if offered, applied := entry.ReofferExit(info.LifecycleID); offered {
			if !applied {
				d.logf("exit view not applied: session=%s terminal=%s", sessionID, info.ID)
			}
			return
		}
	}
	rec := store.SessionExitScreen{SessionID: sessionID, ExitCode: info.ExitCode, ExitSignal: info.Signal}
	if provider, ok := d.ptyBackend.(ptybackend.ScreenSnapshotProvider); ok {
		ctx, cancel := context.WithTimeout(context.Background(), exitScreenSnapshotTimeout)
		snapshot, err := provider.ScreenSnapshot(ctx, info.ID)
		cancel()
		switch {
		case err != nil:
			d.logf("exit screen unavailable: session=%s err=%v", sessionID, err)
		case snapshot.Screen != nil && snapshot.Screen.HasText:
			rec.Text = clampExitScreenText(snapshot.Screen.Text)
			rec.Cols = int(snapshot.Screen.Cols)
			rec.Rows = int(snapshot.Screen.Rows)
		}
	}
	if entry := d.monitorEntry(sessionID); entry != nil {
		if !entry.ReportExit(sessionmonitor.Exit{LifecycleID: info.LifecycleID, Text: rec.Text, Cols: rec.Cols, Rows: rec.Rows, Code: rec.ExitCode, Signal: rec.ExitSignal, At: time.Now()}) {
			d.logf("exit view not applied: session=%s terminal=%s", sessionID, info.ID)
		}
		return
	}
	d.saveExitScreen(rec, time.Now())
}

func (d *Daemon) saveExitScreen(rec store.SessionExitScreen, at time.Time) bool {
	if err := d.store.SaveSessionExitScreen(rec, at); err != nil {
		d.logf("exit screen not kept: session=%s err=%v", rec.SessionID, err)
		return false
	}
	d.logf("exit screen kept: session=%s code=%d signal=%q text_bytes=%d", rec.SessionID, rec.ExitCode, rec.ExitSignal, len(rec.Text))
	return true
}

func (d *Daemon) clearExitScreenBefore(session protocol.SessionID, before time.Time) {
	if entry := d.monitorEntry(session); entry != nil {
		cleared, applied := entry.ClearExitBefore(before)
		if !cleared || !applied {
			return
		}
	}
	if err := d.store.DeleteSessionExitScreenBefore(session, before); err != nil {
		d.logf("exit screen of the previous process not cleared: session=%s err=%v", session, err)
	}
}

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
