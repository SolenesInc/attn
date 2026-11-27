package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

type surfacedTurn struct {
	openedAt  time.Time
	attention protocol.SessionAttention
}

func sessionAttention(entry attention.LogEntry, at time.Time) protocol.SessionAttention {
	verdict := protocol.SessionAttention{Verdict: string(entry.Verdict), Decider: entry.Decider, Reason: entry.Reason, At: at.UTC().Format(time.RFC3339Nano)}
	if entry.Label != "" {
		verdict.Label = protocol.Ptr(string(entry.Label))
	}
	return verdict
}

func (d *Daemon) actOnAttentionVerdict(verdict attention.LogEntry) {
	if verdict.Verdict != attention.Open || !d.on(setting.AttentionSupervisor) {
		return
	}
	result, err := d.monitorCommand(verdict.Session, "surface", time.Time{}, "")
	if err != nil {
		d.logf("attention: session=%s surface refused: %v", verdict.Session, err)
		return
	}
	opened, err := parseMonitorTime(result.OpenedAt)
	if err != nil {
		d.logf("attention: session=%s surface: %v", verdict.Session, err)
		return
	}
	surfaced := attention.LogEntry{Kind: attention.LogSurfaced, At: opened, Session: verdict.Session, VerdictAt: &verdict.At,
		Verdict: verdict.Verdict, Decider: verdict.Decider, Label: verdict.Label, Reason: verdict.Reason}
	d.appendAttentionLog(surfaced)
	d.attention.surface(surfaced)
	d.broadcastSessionStateChanged(verdict.Session)
}

func (a *attentionCheckpoints) surface(entry attention.LogEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.surfaced == nil {
		a.surfaced = map[protocol.SessionID]surfacedTurn{}
	}
	a.surfaced[entry.Session] = surfacedTurn{openedAt: entry.At, attention: sessionAttention(entry, *entry.VerdictAt)}
}

func (d *Daemon) decorateSessionAttention(clone *protocol.Session) {
	clone.Attention = nil
	if !protocol.Deref(clone.TurnOwed) || clone.TurnOpenedAt == nil {
		return
	}
	d.attention.mu.Lock()
	surfaced, ok := d.attention.surfaced[clone.ID]
	d.attention.mu.Unlock()
	if opened, err := time.Parse(time.RFC3339Nano, *clone.TurnOpenedAt); ok && err == nil && opened.Equal(surfaced.openedAt) {
		clone.Attention = &surfaced.attention
	}
}

func (d *Daemon) latestAttentionVerdict(session protocol.SessionID) *protocol.SessionAttention {
	d.attention.mu.Lock()
	defer d.attention.mu.Unlock()
	judged, ok := d.attention.judged[session]
	if !ok {
		return nil
	}
	return &judged.verdict
}
