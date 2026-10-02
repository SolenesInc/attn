package daemon

import (
	"bytes"
	"time"
)

func (d *Daemon) noteUserInput(sessionID, source string, data []byte) bool {
	if sessionID == "" || !isComposerKeystroke(source, data) {
		return false
	}
	now := time.Now()
	d.lastInputMu.Lock()
	if d.lastUserInputAt == nil {
		d.lastUserInputAt = make(map[string]time.Time)
	}
	if d.lastAutoSettleActivityAt == nil {
		d.lastAutoSettleActivityAt = make(map[string]time.Time)
	}
	wasQuiet := d.userInputQuietRemainingLocked(sessionID, sessionInputQuietWindow) == 0
	d.lastUserInputAt[sessionID] = now
	d.lastAutoSettleActivityAt[sessionID] = now
	d.lastInputMu.Unlock()
	if wasQuiet {
		d.kickSessionInboxAddresses(sessionID)
	}
	return true
}

func (d *Daemon) noteAutoSettleActivity(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	d.lastInputMu.Lock()
	if d.lastAutoSettleActivityAt == nil {
		d.lastAutoSettleActivityAt = make(map[string]time.Time)
	}
	d.lastAutoSettleActivityAt[sessionID] = time.Now()
	d.lastInputMu.Unlock()
	return true
}

func (d *Daemon) forgetUserInput(sessionID string) {
	d.lastInputMu.Lock()
	delete(d.lastUserInputAt, sessionID)
	d.lastInputMu.Unlock()
}

func (d *Daemon) userInputQuietRemaining(sessionID string, within time.Duration) time.Duration {
	d.lastInputMu.Lock()
	defer d.lastInputMu.Unlock()
	return d.userInputQuietRemainingLocked(sessionID, within)
}

func (d *Daemon) userInputQuietRemainingLocked(sessionID string, within time.Duration) time.Duration {
	last, ok := d.lastUserInputAt[sessionID]
	if !ok {
		return 0
	}
	remaining := within - time.Since(last)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (d *Daemon) autoSettleActivityQuietRemaining(sessionID string, within time.Duration) time.Duration {
	d.lastInputMu.Lock()
	defer d.lastInputMu.Unlock()
	return d.autoSettleActivityQuietRemainingLocked(sessionID, within)
}

func (d *Daemon) autoSettleActivityQuietRemainingLocked(sessionID string, within time.Duration) time.Duration {
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

func (d *Daemon) settleIfAutoSettleQuiet(sessionID string, within time.Duration) (quiet time.Duration, settled bool) {
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
