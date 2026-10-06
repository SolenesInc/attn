package daemon

import (
	"bytes"
	"time"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) noteUserInput(terminal harness.TerminalID, sessionID protocol.SessionID, source string, data []byte) bool {
	if sessionID == "" || !isComposerKeystroke(source, data) {
		return false
	}
	now := time.Now()
	previous, placed := d.terminals().noteKey(terminal, now)
	d.lastInputMu.Lock()
	if d.lastAutoSettleActivityAt == nil {
		d.lastAutoSettleActivityAt = make(map[protocol.SessionID]time.Time)
	}
	d.lastAutoSettleActivityAt[sessionID] = now
	d.lastInputMu.Unlock()
	if placed && quietRemaining(previous, sessionInputQuietWindow) == 0 {
		d.kickSessionInboxAddresses(sessionID)
	}
	return true
}

func (d *Daemon) noteAutoSettleActivity(sessionID protocol.SessionID) bool {
	if sessionID == "" {
		return false
	}
	d.lastInputMu.Lock()
	if d.lastAutoSettleActivityAt == nil {
		d.lastAutoSettleActivityAt = make(map[protocol.SessionID]time.Time)
	}
	d.lastAutoSettleActivityAt[sessionID] = time.Now()
	d.lastInputMu.Unlock()
	return true
}

func (d *Daemon) forgetUserInput(sessionID protocol.SessionID) {
	for _, terminal := range d.terminals().Of(sessionID) {
		d.terminals().forgetKey(terminal)
	}
}

func (d *Daemon) userInputQuietRemaining(sessionID protocol.SessionID, within time.Duration) time.Duration {
	return quietRemaining(d.terminals().lastKeyOf(d.primaryTerminal(sessionID)), within)
}

func quietRemaining(last time.Time, within time.Duration) time.Duration {
	if last.IsZero() {
		return 0
	}
	remaining := within - time.Since(last)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (d *Daemon) autoSettleActivityQuietRemaining(sessionID protocol.SessionID, within time.Duration) time.Duration {
	d.lastInputMu.Lock()
	defer d.lastInputMu.Unlock()
	return d.autoSettleActivityQuietRemainingLocked(sessionID, within)
}

func (d *Daemon) autoSettleActivityQuietRemainingLocked(sessionID protocol.SessionID, within time.Duration) time.Duration {
	last, ok := d.lastAutoSettleActivityAt[sessionID]
	if !ok {
		return 0
	}
	remaining := within - time.Since(last)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (d *Daemon) settleIfAutoSettleQuiet(sessionID protocol.SessionID, within time.Duration) (quiet time.Duration, settled bool) {
	d.lastInputMu.Lock()
	defer d.lastInputMu.Unlock()
	if remaining := d.autoSettleActivityQuietRemainingLocked(sessionID, within); remaining > 0 {
		return remaining, false
	}
	return 0, d.store.SettleTurn(sessionID, time.Now())
}

func isUserKeystrokeSource(source string) bool {
	switch source {
	case "automation", "attach_replay", "pointer", "response":
		return false
	default:
		return true
	}
}

func isComposerKeystroke(source string, data []byte) bool {
	return isUserKeystrokeSource(source) && userInputEditsComposer(data)
}

func userInputEditsComposer(data []byte) bool {
	rest := data
	for len(rest) > 0 {
		switch {
		case bytes.HasPrefix(rest, []byte("\x1b[<")):
			end := bytes.IndexAny(rest, "Mm")
			if end < 0 {
				return true
			}
			rest = rest[end+1:]
		case bytes.HasPrefix(rest, []byte("\x1b[I")), bytes.HasPrefix(rest, []byte("\x1b[O")):
			rest = rest[3:]
		default:
			return true
		}
	}
	return false
}
