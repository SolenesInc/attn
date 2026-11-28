package daemon

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/statemarker"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/who"
)

const (
	AttentionNoticeTimer stepclock.Name = "attention_notice"

	// D12 and Q6 on s-4kr3bn set both: an agent or a dispatcher has 5 min to answer a supervisor notice, and
	// an agent a restart wakes gets no notice in the same 5 min.
	attentionNoticeAnswerWindow = 5 * time.Minute
	attentionNoticeHoldAtStart  = 5 * time.Minute

	attentionNoticeSource = "attention-supervisor"
	waitNoticeKey         = "background-wait"
	// The tail of an answer is enough to show the agent what it said; 300 characters is about three lines.
	answerTailMaxChars = 300
)

type surfacedTurn struct {
	openedAt  time.Time
	attention protocol.SessionAttention
}

type supervisedWait struct {
	since     time.Time
	notice    attention.Checkpoint
	noticedAt time.Time
	silent    time.Time
	answered  bool
	deadline  stepclock.Timer
}

func (w *supervisedWait) stopDeadline() {
	if w.deadline != nil {
		w.deadline.Stop()
	}
}

func sessionAttention(entry attention.LogEntry, at time.Time) protocol.SessionAttention {
	verdict := protocol.SessionAttention{Verdict: string(entry.Verdict), Decider: entry.Decider, Reason: entry.Reason, At: at.UTC().Format(time.RFC3339Nano)}
	if entry.Label != "" {
		verdict.Label = protocol.Ptr(string(entry.Label))
	}
	return verdict
}

func (d *Daemon) actOnAttentionVerdict(verdict attention.LogEntry, facts attention.Facts) {
	if facts.TurnOwed || facts.OpenTool != nil {
		return
	}
	if view, _ := d.monitorViewFor(verdict.Session); verdict.Checkpoint.Elapsed() && waitsOnBackground(view) && !onlyTheUserCanAct(verdict) {
		if verdict.Checkpoint != attention.CheckpointElapsed20m || verdict.Verdict == attention.Open {
			d.checkOnWait(verdict, facts, view)
		}
		return
	}
	d.raiseAttention(verdict)
}

func onlyTheUserCanAct(verdict attention.LogEntry) bool {
	return verdict.Verdict == attention.Open && (verdict.Label == attention.LabelAsksYou || verdict.Label == attention.LabelUnreadReport)
}

func (d *Daemon) raiseAttention(verdict attention.LogEntry) {
	if verdict.Verdict != attention.Open && verdict.Verdict != attention.Finished {
		return
	}
	if dispatcher, ok := d.liveDispatcherOf(verdict.Session); ok {
		d.askDispatcher(dispatcher, verdict)
		return
	}
	if verdict.Verdict == attention.Open {
		d.surfaceAttention(verdict)
	}
}

func (d *Daemon) liveDispatcherOf(session protocol.SessionID) (protocol.SessionID, bool) {
	dispatch, delegated := d.gardenDispatch(session)
	if !delegated {
		return "", false
	}
	stake, _ := d.gardenDispatchCrown(session)
	if d.resultRecipient(session, stake, dispatch) != attention.ResultToDispatcher {
		return "", false
	}
	return d.sessionOfActor(dispatch.Dispatcher)
}

func (d *Daemon) surfaceAttention(verdict attention.LogEntry) {
	if !d.on(setting.AttentionSupervisor) {
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

func (d *Daemon) sendSupervisorNotice(about, to protocol.SessionID, key, text string) bool {
	if !d.on(setting.AttentionSupervisor) {
		d.logf("attention: session=%s would send %s this notice: %q", about, to, text)
		return false
	}
	if _, err := d.sendToInbox(inbox.Item{To: who.ToSession(to), Kind: inbox.Notice, Source: attentionNoticeSource, Key: key, Text: text}); err != nil {
		d.logf("attention: session=%s notice to %s: %v", about, to, err)
		return false
	}
	return true
}

func (d *Daemon) checkOnWait(verdict attention.LogEntry, facts attention.Facts, view sessionmonitor.View) {
	session := verdict.Session
	noticedAt := d.clock.Now()
	wait := &supervisedWait{since: elapsedSince(view), notice: verdict.Checkpoint, noticedAt: noticedAt, silent: view.LastModelRequestAt.V}
	answered := false
	a := &d.attention
	a.mu.Lock()
	if previous := a.waits[session]; previous != nil {
		wait.since, answered = previous.since, previous.answered
	}
	a.mu.Unlock()
	answer := ""
	if answered {
		answer = answerTail(facts.LastMessage)
	}
	if !d.sendSupervisorNotice(session, session, waitNoticeKey, waitCheckText(verdict.Checkpoint, facts, noticedAt.Sub(wait.since), answer)) {
		return
	}
	d.appendAttentionLog(attention.LogEntry{Kind: attention.LogNotice, At: noticedAt, Session: session, To: session, Checkpoint: verdict.Checkpoint, WaitSince: wait.since, SilentSince: wait.silent})
	a.mu.Lock()
	defer a.mu.Unlock()
	if previous := a.waits[session]; previous != nil {
		previous.stopDeadline()
	}
	if a.waits == nil {
		a.waits = map[protocol.SessionID]*supervisedWait{}
	}
	a.waits[session] = wait
	d.armWaitDeadlineLocked(session, wait, attentionNoticeAnswerWindow)
}

func (d *Daemon) armWaitDeadlineLocked(session protocol.SessionID, wait *supervisedWait, after time.Duration) {
	silent, notice := wait.silent, wait.notice
	wait.deadline = d.clock.AfterFunc(AttentionNoticeTimer, after, func() {
		d.life.Do("attentionWaitNotice", func() {
			if view, _ := d.monitorViewFor(session); view.LastModelRequestAt.V.Equal(silent) && d.endWait(session, wait) {
				d.raiseAttention(attention.LogEntry{At: d.clock.Now(), Session: session, Verdict: attention.Open, Decider: "rule:notice_unanswered", Label: attention.LabelStuckWait,
					Reason: fmt.Sprintf("no model request within %s of the supervisor's %s check of its background wait", minutesText(attentionNoticeAnswerWindow), checkpointAge(notice))})
			}
		})
	})
}

func (d *Daemon) resumeWait(session protocol.SessionID, view sessionmonitor.View) {
	a := &d.attention
	a.mu.Lock()
	defer a.mu.Unlock()
	wait := a.waits[session]
	if wait == nil {
		return
	}
	if !view.LastModelRequestAt.V.Equal(wait.silent) {
		wait.answered = true
		return
	}
	d.armWaitDeadlineLocked(session, wait, latest(wait.noticedAt.Add(attentionNoticeAnswerWindow), a.startedAt.Add(attentionNoticeHoldAtStart)).Sub(d.clock.Now()))
}

func (d *Daemon) observeWait(session protocol.SessionID, previous, view sessionmonitor.View) {
	a := &d.attention
	a.mu.Lock()
	wait := a.waits[session]
	if wait == nil {
		a.mu.Unlock()
		return
	}
	moved := false
	if !view.LastModelRequestAt.V.Equal(previous.LastModelRequestAt.V) {
		if wait.answered {
			moved = true
		} else {
			wait.answered = true
			wait.stopDeadline()
		}
	}
	resting := !sessionmonitor.RunsIn(view.State.V.State)
	over := resting && !waitsOnBackground(view)
	stopped := resting && view.State.V.Sequence != previous.State.V.Sequence
	keptWaiting := !moved && !over && stopped && wait.answered && wait.notice == attention.CheckpointElapsed4h
	a.mu.Unlock()
	if (moved || over || keptWaiting) && d.endWait(session, wait) && keptWaiting {
		d.raiseAttention(attention.LogEntry{At: d.clock.Now(), Session: session, Verdict: attention.Open, Decider: "rule:kept_waiting", Label: attention.LabelStuckWait,
			Reason: "kept waiting after the supervisor's last check of its background wait"})
	}
}

func (d *Daemon) endWait(session protocol.SessionID, wait *supervisedWait) bool {
	a := &d.attention
	a.mu.Lock()
	ended := a.endWaitLocked(session, wait)
	a.mu.Unlock()
	if ended {
		d.appendAttentionLog(attention.LogEntry{Kind: attention.LogWaitEnded, At: d.clock.Now(), Session: session})
	}
	return ended
}

func (a *attentionCheckpoints) endWaitLocked(session protocol.SessionID, wait *supervisedWait) bool {
	if a.waits[session] != wait {
		return false
	}
	wait.stopDeadline()
	delete(a.waits, session)
	return true
}

func (d *Daemon) askDispatcher(dispatcher protocol.SessionID, verdict attention.LogEntry) {
	delegate := verdict.Session
	record := d.store.Get(delegate)
	if record == nil {
		return
	}
	finished := verdict.Verdict == attention.Finished
	text := prompts.RenderText("attention", "delegate-check", prompts.Values{
		"delegate": sessionDisplayName(record), "session": shortSessionID(delegate), "finished": strconv.FormatBool(finished), "reason": verdict.Reason})
	if !d.sendSupervisorNotice(delegate, dispatcher, "delegate/"+string(delegate), text) {
		return
	}
	asking, _ := d.monitorViewFor(dispatcher)
	notice := attention.LogEntry{Kind: attention.LogNotice, At: d.clock.Now(), Session: delegate, To: dispatcher, Checkpoint: verdict.Checkpoint,
		SilentSince: asking.LastModelRequestAt.V, Verdict: verdict.Verdict, Decider: verdict.Decider, Label: verdict.Label, Reason: verdict.Reason}
	d.appendAttentionLog(notice)
	d.awaitDispatcher(notice)
}

func (d *Daemon) awaitDispatcher(notice attention.LogEntry) {
	delegate, dispatcher, silent := notice.Session, notice.To, notice.SilentSince
	surface := attention.LogEntry{Session: delegate, Verdict: attention.Open, Decider: "rule:dispatcher_silent", Label: notice.Label,
		Reason: fmt.Sprintf("its dispatcher made no model request within %s of the supervisor's notice: %s", minutesText(attentionNoticeAnswerWindow), notice.Reason)}
	a := &d.attention
	a.mu.Lock()
	defer a.mu.Unlock()
	if asked := a.dispatcherAsks[delegate]; asked != nil {
		asked.Stop()
	}
	if a.dispatcherAsks == nil {
		a.dispatcherAsks = map[protocol.SessionID]stepclock.Timer{}
	}
	var asked stepclock.Timer
	asked = d.clock.AfterFunc(AttentionNoticeTimer, notice.At.Add(attentionNoticeAnswerWindow).Sub(d.clock.Now()), func() {
		d.life.Do("attentionDispatcherNotice", func() {
			a.mu.Lock()
			current := a.dispatcherAsks[delegate] == asked
			if current {
				delete(a.dispatcherAsks, delegate)
			}
			a.mu.Unlock()
			if !current {
				return
			}
			if view, _ := d.monitorViewFor(dispatcher); view.LastModelRequestAt.V.Equal(silent) && d.store.Get(delegate) != nil {
				surface.At = d.clock.Now()
				d.surfaceAttention(surface)
			}
			d.appendAttentionLog(attention.LogEntry{Kind: attention.LogNoticeDeadline, At: d.clock.Now(), Session: delegate, To: dispatcher})
		})
	})
	a.dispatcherAsks[delegate] = asked
}

func waitCheckText(checkpoint attention.Checkpoint, facts attention.Facts, waited time.Duration, answer string) string {
	wait, age, silent := "wait", waited, waited
	if task, ok := oldestTask(facts.Background.Tasks); ok {
		wait, age = "task "+taskName(task), time.Duration(task.Age)
	}
	if facts.SinceModelRequest != nil {
		silent = time.Duration(*facts.SinceModelRequest)
	}
	return prompts.RenderText("attention", "wait-check", prompts.Values{
		"wait": wait, "age": minutesText(age), "silent": minutesText(silent),
		"last": strconv.FormatBool(checkpoint == attention.CheckpointElapsed4h), "answer": answer})
}

func oldestTask(tasks []attention.Task) (attention.Task, bool) {
	if len(tasks) == 0 {
		return attention.Task{}, false
	}
	return slices.MaxFunc(tasks, func(a, b attention.Task) int { return int(a.Age - b.Age) }), true
}

func taskName(task attention.Task) string {
	name := task.Description
	for _, alternative := range []string{task.Command, task.Name, task.AgentType, task.Tool, task.Kind} {
		if name == "" {
			name = alternative
		}
	}
	if task.ID == "" {
		return strconv.Quote(name)
	}
	return fmt.Sprintf("%q (%s)", name, task.ID)
}

func answerTail(message string) string {
	answer := []rune(strings.TrimSpace(statemarker.Strip(message)))
	if len(answer) > answerTailMaxChars {
		return "…" + string(answer[len(answer)-answerTailMaxChars:])
	}
	return string(answer)
}

func checkpointAge(checkpoint attention.Checkpoint) string {
	for _, step := range attentionElapsedSteps {
		if step.checkpoint == checkpoint {
			return minutesText(step.after)
		}
	}
	return string(checkpoint)
}

func minutesText(d time.Duration) string {
	minutes := int(d.Round(time.Minute) / time.Minute)
	switch {
	case minutes < 60:
		return fmt.Sprintf("%d min", minutes)
	case minutes%60 == 0:
		return fmt.Sprintf("%d h", minutes/60)
	default:
		return fmt.Sprintf("%d h %d min", minutes/60, minutes%60)
	}
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
