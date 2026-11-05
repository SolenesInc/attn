package daemon

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

var compactOutcomeTripwire = 10 * time.Minute

type compactOutcome struct {
	failed string
}

type compactionLedger struct {
	mu      sync.Mutex
	pending map[protocol.SessionID]*pendingCompactClose
}

type pendingCompactClose struct {
	session    protocol.SessionID
	monitor    *sessionmonitor.Monitor
	reason     string
	userInputs uint64
	onStandby  []string
	key        string
	busyTimes  int
	placing    bool
	again      bool
	placed     bool
	busy       bool
	outcome    *compactOutcome
	tripwire   *time.Timer
}

func (d *Daemon) compactPathRefusal(target *protocol.Session) *agentCloseRefusal {
	if d.codexShared().launchedShared(target.ID) {
		return nil
	}
	switch target.Agent {
	case string(protocol.SessionAgentCodex):
		return &agentCloseRefusal{"compact_unavailable", fmt.Sprintf(
			"this Codex session does not run on the shared Codex app server, so attn has no way to compact it; the user turns on %s (attn settings set %s true), and Codex sessions launched after that can close with --compact. Close without --compact instead",
			SettingCodexSharedEnabled, SettingCodexSharedEnabled)}
	case string(protocol.SessionAgentClaude):
		if d.claudeMod().reachable(d.sessionMonitor(target.ID).LinkTarget().Terminal) {
			return nil
		}
		return &agentCloseRefusal{"compact_unavailable", fmt.Sprintf(
			"this Claude session does not run with attn's Claude mod, so attn has no way to compact it; the user turns on %s (attn settings set %s true), and Claude sessions launched after that can close with --compact. Close without --compact instead",
			SettingClaudeModEnabled, SettingClaudeModEnabled)}
	}
	return &agentCloseRefusal{"compact_unavailable", fmt.Sprintf(
		"attn has no way to compact a %s session; close without --compact", target.Agent)}
}

func (d *Daemon) registerCompactingClose(target *protocol.Session, reason string, onStandby []string) {
	monitor := d.sessionMonitor(target.ID)
	p := &pendingCompactClose{
		session:    target.ID,
		monitor:    monitor,
		reason:     reason,
		userInputs: monitor.UserInputs(),
		onStandby:  onStandby,
		key:        fmt.Sprintf("%s-%d", target.ID, d.clock.Now().UnixNano()),
	}
	l := &d.compactions
	l.mu.Lock()
	if l.pending == nil {
		l.pending = make(map[protocol.SessionID]*pendingCompactClose)
	}
	if current := l.pending[target.ID]; current != nil && current.monitor == monitor {
		l.mu.Unlock()
		d.logf("compacting close: session %s already closes after its compact", target.ID)
		return
	}
	l.pending[target.ID] = p
	p.placing = true
	l.mu.Unlock()
	d.logf("compacting close: session %s closes after this turn, once its conversation is compacted: %s", target.ID, reason)
	d.placeCompactingClose(p)
}

func (l *compactionLedger) forget(session protocol.SessionID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if p := l.pending[session]; p != nil {
		l.takeLocked(p)
	}
}

func (d *Daemon) kickCompactingClose(session protocol.SessionID) {
	l := &d.compactions
	l.mu.Lock()
	p := l.pending[session]
	switch {
	case p == nil || p.placed:
		l.mu.Unlock()
		return
	case p.placing:
		p.again = true
		l.mu.Unlock()
		return
	}
	p.placing = true
	l.mu.Unlock()
	if !d.life.Go("compactingClose", func() { d.placeCompactingClose(p) }) {
		l.mu.Lock()
		p.placing = false
		l.mu.Unlock()
	}
}

func (d *Daemon) placeCompactingClose(p *pendingCompactClose) {
	l := &d.compactions
	for {
		if why := d.compactingCloseCanceled(p); why != "" {
			l.mu.Lock()
			l.takeLocked(p)
			l.mu.Unlock()
			d.logf("compacting close: session %s stays open: %s", p.session, why)
			return
		}
		pausepoint.At(pausepoint.CompactingClosePlacing)
		attempt := d.placeDelivery(context.Background(), p.session, p.compact())
		l.mu.Lock()
		if l.pending[p.session] != p {
			l.mu.Unlock()
			return
		}
		switch attempt.Stage {
		case sessionmonitor.Deferred:
			if p.again {
				p.again = false
				l.mu.Unlock()
				continue
			}
			p.placing = false
			l.mu.Unlock()
			d.logf("compacting close: the compact of session %s waits: %v", p.session, attempt.Err)
			return
		case sessionmonitor.Failed:
			p.placed = true
			if p.outcome == nil {
				p.outcome = &compactOutcome{failed: attempt.Err.Error()}
			}
		case sessionmonitor.Placed:
			if p.busy {
				p.busy, p.again, p.placing = false, false, false
				p.busyTimes++
				l.mu.Unlock()
				p.monitor.ReopenInput()
				d.kickSessionInboxAddresses(p.session)
				return
			}
			p.placed = true
			if p.outcome == nil {
				p.tripwire = d.life.AfterFunc("compactOutcomeTripwire", compactOutcomeTripwire, func() { d.compactOutcomeMissing(p) })
			}
		}
		ready := p.outcome != nil && l.takeLocked(p)
		l.mu.Unlock()
		if ready {
			d.closeAfterCompact(p)
		}
		return
	}
}

func (l *compactionLedger) report(d *Daemon, session protocol.SessionID, delivery string, outcome compactOutcome) {
	l.mu.Lock()
	p := l.pending[session]
	if p == nil || p.outcome != nil || p.compact().ID.String() != delivery {
		l.mu.Unlock()
		return
	}
	p.outcome = &outcome
	ready := p.placed && l.takeLocked(p)
	l.mu.Unlock()
	if ready {
		d.life.Go("compactingClose", func() { d.closeAfterCompact(p) })
	}
}

func (p *pendingCompactClose) compact() sessionmonitor.Delivery {
	return sessionmonitor.CompactInput(fmt.Sprintf("%s-%d", p.key, p.busyTimes))
}

func (l *compactionLedger) retryAfterBusy(d *Daemon, session protocol.SessionID, delivery string, why string) {
	l.mu.Lock()
	p := l.pending[session]
	if p == nil || p.outcome != nil || p.compact().ID.String() != delivery {
		l.mu.Unlock()
		return
	}
	d.logf("compacting close: the compact of session %s met a running turn and waits for its end: %s", session, why)
	if !p.placed {
		p.busy = true
		l.mu.Unlock()
		return
	}
	p.placed, p.placing, p.again = false, false, false
	p.busyTimes++
	if p.tripwire != nil {
		p.tripwire.Stop()
		p.tripwire = nil
	}
	l.mu.Unlock()
	p.monitor.ReopenInput()
	d.kickSessionInboxAddresses(session)
}

func (d *Daemon) compactOutcomeMissing(p *pendingCompactClose) {
	l := &d.compactions
	l.mu.Lock()
	if p.outcome != nil || !l.takeLocked(p) {
		l.mu.Unlock()
		return
	}
	p.outcome = &compactOutcome{failed: fmt.Sprintf("compact_outcome_tripwire=%s passed with no outcome", compactOutcomeTripwire)}
	l.mu.Unlock()
	d.closeAfterCompact(p)
}

func (l *compactionLedger) takeLocked(p *pendingCompactClose) bool {
	if l.pending[p.session] != p {
		return false
	}
	delete(l.pending, p.session)
	if p.tripwire != nil {
		p.tripwire.Stop()
	}
	return true
}

func (d *Daemon) compactingCloseCanceled(p *pendingCompactClose) string {
	target := d.store.Get(p.session)
	switch {
	case target == nil || d.sessionMonitor(p.session) != p.monitor:
		return "the session it was asked in is gone"
	case p.monitor.UserInputs() != p.userInputs:
		return "the user gave it input after the close was asked"
	case p.monitor.HasUserDraft():
		return "the user has an unsent draft in its terminal"
	}
	for _, seedID := range p.onStandby {
		if standby, err := d.store.SeedStandby(seedID); err != nil || standby == nil || standby.Tender != who.Session(p.session) {
			return fmt.Sprintf("%s left standby after the close was asked", seedID)
		}
	}
	return ""
}

func (d *Daemon) closeAfterCompact(p *pendingCompactClose) {
	if why := d.compactingCloseCanceled(p); why != "" {
		d.logf("compacting close: session %s stays open after its compact: %s", p.session, why)
		p.monitor.ReopenInput()
		d.kickSessionInboxAddresses(p.session)
		return
	}
	reason := p.reason
	if p.outcome.failed != "" {
		reason = garden.TrimReason(fmt.Sprintf("%s (the compact did not run: %s)", reason, p.outcome.failed))
	}
	target := d.store.Get(p.session)
	d.logf("agent close: session %s closes itself after its compact: %s", target.ID, reason)
	closing, err := d.beginSessionClose(target.ID, store.SessionClose{By: who.Session(target.ID).Actor(), Reason: reason}, nil)
	if err != nil {
		d.logf("compacting close: session %s is still running: %v", target.ID, err)
		p.monitor.ReopenInput()
		d.kickSessionInboxAddresses(p.session)
		return
	}
	d.noteCloseOnTendedSeeds(target, target, protocol.AgentCloseRuleSelf, reason)
	d.finishSessionClose(target.ID, closing)
}
