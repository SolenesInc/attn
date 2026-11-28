package daemon

import (
	"fmt"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

const (
	AttentionFinishedTimer stepclock.Name = "attention_finished"

	finishedNoticeKey   = "finished"
	settledWhenFinished = "settled when finished"
)

type finishedAgent struct {
	verdict   attention.LogEntry
	noticedAt time.Time
	silent    time.Time
	deadline  stepclock.Timer
	mark      *protocol.SessionAttention
}

func (f *finishedAgent) stopDeadline() {
	if f.deadline != nil {
		f.deadline.Stop()
	}
}

func (d *Daemon) checkFinished(verdict attention.LogEntry) {
	session := verdict.Session
	a := &d.attention
	a.mu.Lock()
	noticed := a.finished[session] != nil
	a.mu.Unlock()
	if noticed {
		d.markFinished(verdict)
		return
	}
	text := prompts.RenderText("attention", "finished-check", prompts.Values{"reason": verdict.Reason})
	if !d.sendSupervisorNotice(session, session, finishedNoticeKey, text) {
		return
	}
	view, _ := d.monitorViewFor(session)
	agent := &finishedAgent{verdict: verdict, noticedAt: d.clock.Now(), silent: view.LastModelRequestAt.V}
	d.appendAttentionLog(attention.LogEntry{Kind: attention.LogNotice, At: agent.noticedAt, Session: session, To: session, Checkpoint: verdict.Checkpoint,
		VerdictAt: &verdict.At, Verdict: verdict.Verdict, Decider: verdict.Decider, Label: verdict.Label, Reason: verdict.Reason, SilentSince: agent.silent})
	a.mu.Lock()
	defer a.mu.Unlock()
	if previous := a.finished[session]; previous != nil {
		previous.stopDeadline()
	}
	if a.finished == nil {
		a.finished = map[protocol.SessionID]*finishedAgent{}
	}
	a.finished[session] = agent
	d.armFinishedDeadlineLocked(session, agent, attentionNoticeAnswerWindow)
}

func (d *Daemon) armFinishedDeadlineLocked(session protocol.SessionID, agent *finishedAgent, after time.Duration) {
	agent.deadline = d.clock.AfterFunc(AttentionFinishedTimer, after, func() {
		d.life.Do("attentionFinishedNotice", func() {
			a := &d.attention
			a.mu.Lock()
			current := a.finished[session] == agent && agent.mark == nil
			a.mu.Unlock()
			if view, _ := d.monitorViewFor(session); current && view.LastModelRequestAt.V.Equal(agent.silent) {
				d.markFinished(agent.verdict)
			}
		})
	})
}

func (d *Daemon) markFinished(verdict attention.LogEntry) {
	session := verdict.Session
	if !d.on(setting.AttentionSupervisor) {
		d.logf("attention: session=%s would mark it finished: %s", session, verdict.Reason)
		return
	}
	if d.store.Get(session) == nil {
		return
	}
	view, _ := d.monitorViewFor(session)
	if !restingAt(view.State.V.State) {
		return
	}
	mark := sessionAttention(verdict, verdict.At)
	a := &d.attention
	a.mu.Lock()
	agent := a.finished[session]
	if agent == nil {
		agent = &finishedAgent{verdict: verdict}
		if a.finished == nil {
			a.finished = map[protocol.SessionID]*finishedAgent{}
		}
		a.finished[session] = agent
	}
	agent.stopDeadline()
	agent.silent, agent.mark = view.LastModelRequestAt.V, &mark
	a.mu.Unlock()
	d.appendAttentionLog(attention.LogEntry{Kind: attention.LogMarked, At: d.clock.Now(), Session: session, VerdictAt: &verdict.At,
		Verdict: verdict.Verdict, Decider: verdict.Decider, Label: verdict.Label, Reason: verdict.Reason, SilentSince: agent.silent})
	d.broadcastSessionStateChanged(session)
}

func restingAt(state protocol.SessionState) bool {
	return state == protocol.SessionStateIdle || state == protocol.SessionStateWaitingInput
}

func (d *Daemon) observeFinished(session protocol.SessionID, previous, view sessionmonitor.View) {
	if view.LastModelRequestAt.V.Equal(previous.LastModelRequestAt.V) {
		return
	}
	d.unmarkAfterModelRequest(session, view)
}

func (d *Daemon) unmarkAfterModelRequest(session protocol.SessionID, view sessionmonitor.View) {
	a := &d.attention
	a.mu.Lock()
	agent := a.finished[session]
	moved := agent != nil && !view.LastModelRequestAt.V.Equal(agent.silent)
	marked := moved && agent.mark != nil
	if moved {
		agent.stopDeadline()
		agent.mark = nil
	}
	a.mu.Unlock()
	if marked {
		d.appendAttentionLog(attention.LogEntry{Kind: attention.LogUnmarked, At: d.clock.Now(), Session: session})
		d.broadcastSessionStateChanged(session)
	}
}

func (d *Daemon) endFinished(session protocol.SessionID) {
	if agent := d.forgetFinished(session); agent != nil && agent.mark != nil {
		d.broadcastSessionStateChanged(session)
	}
}

func (d *Daemon) forgetFinished(session protocol.SessionID) *finishedAgent {
	a := &d.attention
	a.mu.Lock()
	agent := a.finished[session]
	if agent != nil {
		agent.stopDeadline()
		delete(a.finished, session)
	}
	a.mu.Unlock()
	if agent != nil {
		d.appendAttentionLog(attention.LogEntry{Kind: attention.LogFinishedEnded, At: d.clock.Now(), Session: session})
	}
	return agent
}

func (d *Daemon) resumeFinished(session protocol.SessionID, view sessionmonitor.View) {
	d.unmarkAfterModelRequest(session, view)
	a := &d.attention
	a.mu.Lock()
	defer a.mu.Unlock()
	agent := a.finished[session]
	if agent == nil || agent.mark != nil || !view.LastModelRequestAt.V.Equal(agent.silent) {
		return
	}
	d.armFinishedDeadlineLocked(session, agent, latest(agent.noticedAt.Add(attentionNoticeAnswerWindow), a.startedAt.Add(attentionNoticeHoldAtStart)).Sub(d.clock.Now()))
}

func (d *Daemon) finishedMark(session protocol.SessionID) *protocol.SessionAttention {
	a := &d.attention
	a.mu.Lock()
	var mark *protocol.SessionAttention
	if agent := a.finished[session]; agent != nil && agent.mark != nil {
		copied := *agent.mark
		mark = &copied
	}
	a.mu.Unlock()
	if mark == nil || !d.on(setting.AttentionSupervisor) {
		return nil
	}
	return mark
}

func (d *Daemon) settleClosesFinished(session protocol.SessionID) (bool, error) {
	if d.finishedMark(session) == nil {
		return false, nil
	}
	if view, _ := d.monitorViewFor(session); view.Turn.V.OpenedAt.After(view.Turn.V.SettledAt) {
		return false, nil
	}
	return true, d.closeBySupervisor(session, settledWhenFinished)
}

func (d *Daemon) closeFinishedDelegate(session protocol.SessionID, stake *attention.Seed) {
	reason := fmt.Sprintf("its stake %s is %s", stake.ID, stake.Status)
	if !d.on(setting.AttentionSupervisor) {
		d.logf("attention: session=%s would close it: %s", session, reason)
		return
	}
	if err := d.closeBySupervisor(session, reason); err != nil {
		d.logf("attention: session=%s close: %v", session, err)
	}
}

func (d *Daemon) closeBySupervisor(session protocol.SessionID, reason string) error {
	closing, err := d.beginSessionCloseAsUser(session, store.SessionClose{By: who.Supervisor(), Reason: reason}, nil)
	if err != nil {
		return err
	}
	d.finishSessionClose(session, closing)
	return nil
}
