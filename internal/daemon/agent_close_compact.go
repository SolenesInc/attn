package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

const (
	CompactOutcomeTripwireTimer stepclock.Name = "compact-outcome-tripwire"
	compactOutcomeTripwire                     = 10 * time.Minute
)

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
	tripwire   stepclock.Timer
}

func (d *Daemon) compactPathRefusal(target *protocol.Session) *agentCloseRefusal {
	if d.codexShared().launchedShared(target.ID) {
		return nil
	}
	switch target.Agent {
	case string(protocol.SessionAgentCodex):
		return &agentCloseRefusal{"compact_unavailable", fmt.Sprintf(
			"this Codex session does not run on the shared Codex app server, so attn has no way to compact it; the user turns on %s (attn settings set %s true), and Codex sessions launched after that can close with --compact. Close without --compact instead",
			setting.CodexSharedEnabled, setting.CodexSharedEnabled)}
	case string(protocol.SessionAgentClaude):
		if d.claudeModReaches(target.ID) {
			return nil
		}
		return &agentCloseRefusal{"compact_unavailable", fmt.Sprintf(
			"this Claude session does not run with attn's Claude mod, so attn has no way to compact it; the user turns on %s (attn settings set %s true), and Claude sessions launched after that can close with --compact. Close without --compact instead",
			setting.ClaudeModEnabled, setting.ClaudeModEnabled)}
	}
	return &agentCloseRefusal{"compact_unavailable", fmt.Sprintf(
		"attn has no way to compact a %s session; close without --compact", target.Agent)}
}

func (d *Daemon) beginCompactingClose(_ context.Context, msg protocol.CompactingCloseBeginMessage) (struct{}, error) {
	session := msg.SessionID
	monitor := d.sessionMonitor(session)
	if monitor == nil {
		return struct{}{}, fmt.Errorf("session %s: no monitor runs it", session)
	}
	p := &pendingCompactClose{
		session:    session,
		monitor:    monitor,
		reason:     msg.Reason,
		userInputs: monitor.UserInputs(),
		onStandby:  msg.OnStandby,
		key:        fmt.Sprintf("%s-%d", session, d.clock.Now().UnixNano()),
	}
	l := &d.compactions
	l.mu.Lock()
	if l.pending == nil {
		l.pending = make(map[protocol.SessionID]*pendingCompactClose)
	}
	if current := l.pending[session]; current != nil && current.monitor == monitor {
		l.mu.Unlock()
		d.logf("compacting close: session %s already closes after its compact", session)
		return struct{}{}, nil
	}
	l.pending[session] = p
	p.placing = true
	l.mu.Unlock()
	d.logf("compacting close: session %s closes after this turn, once its conversation is compacted: %s", session, msg.Reason)
	d.placeCompactingClose(p)
	return struct{}{}, nil
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
				d.reopenCompactingCloseInput(p)
				return
			}
			p.placed = true
			if p.outcome == nil {
				p.tripwire = d.clock.AfterFunc(CompactOutcomeTripwireTimer, compactOutcomeTripwire, func() {
					d.life.Go("compactOutcomeTripwire", func() { d.compactOutcomeMissing(p) })
				})
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
	d.reopenCompactingCloseInput(p)
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

func (d *Daemon) reopenCompactingCloseInput(p *pendingCompactClose) {
	p.monitor.ReopenInput()
	d.claudeMod().report(protocol.SessionInputReopenedMessage{Cmd: protocol.CmdSessionInputReopened, SessionID: p.session})
}

func (d *Daemon) compactingCloseCanceled(p *pendingCompactClose) string {
	verdict, err := callHome[protocol.CompactingCloseVerdict](d, protocol.CompactingCloseCheckMessage{Cmd: protocol.CmdCompactingCloseCheck, SessionID: p.session, OnStandby: p.onStandby})
	if err != nil {
		return err.Error()
	}
	if why := protocol.Deref(verdict.StaysOpen); why != "" {
		return why
	}
	return d.compactingCloseCanceledHere(p)
}

func (d *Daemon) compactingCloseCanceledHere(p *pendingCompactClose) string {
	if d.sessionMonitor(p.session) != p.monitor {
		return "the session it was asked in is gone"
	}
	return userInputCancelsClose(p.monitor, p.userInputs)
}

func userInputCancelsClose(monitor *sessionmonitor.Monitor, userInputsWhenAsked uint64) string {
	switch {
	case monitor.UserInputs() != userInputsWhenAsked:
		return "the user gave it input after the close was asked"
	case monitor.HasUserDraft():
		return "the user has an unsent draft in its terminal"
	}
	return ""
}

func (d *Daemon) compactingCloseCanceledAtHome(session protocol.SessionID, onStandby []string) string {
	if d.store.Get(session) == nil {
		return "the session it was asked in is gone"
	}
	for _, seedID := range onStandby {
		if standby, err := d.store.SeedStandby(seedID); err != nil || standby == nil || standby.Tender != who.Session(session) {
			return fmt.Sprintf("%s left standby after the close was asked", seedID)
		}
	}
	return ""
}

func (d *Daemon) checkCompactingClose(ctx context.Context, msg protocol.CompactingCloseCheckMessage) (protocol.CompactingCloseVerdict, error) {
	if origin, remote := OriginOf(ctx); remote && d.sessionHost(msg.SessionID) != origin.Host {
		return protocol.CompactingCloseVerdict{}, fmt.Errorf("session %s does not run on %s", msg.SessionID, origin.Name)
	}
	return compactingCloseVerdict(d.compactingCloseCanceledAtHome(msg.SessionID, msg.OnStandby)), nil
}

func compactingCloseVerdict(staysOpen string) protocol.CompactingCloseVerdict {
	if staysOpen == "" {
		return protocol.CompactingCloseVerdict{}
	}
	return protocol.CompactingCloseVerdict{StaysOpen: &staysOpen}
}

func (d *Daemon) closeAfterCompact(p *pendingCompactClose) {
	why := d.compactingCloseCanceledHere(p)
	if why == "" {
		reason := p.reason
		if p.outcome.failed != "" {
			reason = garden.TrimReason(fmt.Sprintf("%s (the compact did not run: %s)", reason, p.outcome.failed))
		}
		verdict, err := callHome[protocol.CompactingCloseVerdict](d, protocol.CompactingCloseFinishMessage{Cmd: protocol.CmdCompactingCloseFinish, SessionID: p.session, Reason: reason, OnStandby: p.onStandby, UserInputs: int(p.userInputs)})
		switch {
		case errors.Is(err, errDaemonStopping):
			return
		case err != nil:
			why = err.Error()
		default:
			why = protocol.Deref(verdict.StaysOpen)
		}
	}
	if why != "" {
		d.logf("compacting close: session %s stays open after its compact: %s", p.session, why)
		d.reopenCompactingCloseInput(p)
	}
}

func (d *Daemon) finishCompactingClose(ctx context.Context, msg protocol.CompactingCloseFinishMessage) (protocol.CompactingCloseVerdict, error) {
	if origin, remote := OriginOf(ctx); remote && d.sessionHost(msg.SessionID) != origin.Host {
		return protocol.CompactingCloseVerdict{}, fmt.Errorf("session %s does not run on %s", msg.SessionID, origin.Name)
	}
	if why := d.compactingCloseCanceledAtHome(msg.SessionID, msg.OnStandby); why != "" {
		return compactingCloseVerdict(why), nil
	}
	target := d.store.Get(msg.SessionID)
	pausepoint.At(pausepoint.CompactingCloseFinishing)
	teardown, err := d.prepareSessionTeardown(target.ID, &msg.UserInputs)
	if err != nil {
		return compactingCloseVerdict(fmt.Sprintf("it is still running: %v", err)), nil
	}
	closing, err := d.commitSessionClose(target.ID, store.SessionClose{By: who.Session(target.ID).Actor(), Reason: msg.Reason}, nil, teardown)
	if err != nil {
		return compactingCloseVerdict(fmt.Sprintf("it is still running: %v", err)), nil
	}
	d.logf("agent close: session %s closes itself after its compact: %s", target.ID, msg.Reason)
	d.noteCloseOnTendedSeeds(target, target, protocol.AgentCloseRuleSelf, msg.Reason)
	d.finishSessionClose(target.ID, closing)
	return protocol.CompactingCloseVerdict{}, nil
}

func (d *Daemon) receiveSessionInputReopened(_ context.Context, msg protocol.SessionInputReopenedMessage) (struct{}, error) {
	d.kickSessionInboxAddresses(msg.SessionID)
	return struct{}{}, nil
}
