package daemon

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/store"

	"github.com/victorarias/attn/internal/attention"
	"github.com/victorarias/attn/internal/classifier"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

const (
	defaultAutoSettleArmSeconds       = 30
	defaultAutoSettleCountdownSeconds = 15

	autoSettleArmMinSeconds       = 5
	autoSettleArmMaxSeconds       = 3600
	autoSettleCountdownMinSeconds = 3
	autoSettleCountdownMaxSeconds = 600
)

func (d *Daemon) autoSettleSettings() sessionmonitor.AutoSettleSettings {
	if d.store == nil {
		return sessionmonitor.AutoSettleSettings{}
	}
	return sessionmonitor.AutoSettleSettings{
		Enabled:   parseBooleanSetting(d.daemonSetting(SettingAutoSettleEnabled)),
		Arm:       resolveAutoSettleSeconds(d.daemonSetting(SettingAutoSettleArmSeconds), defaultAutoSettleArmSeconds),
		Countdown: resolveAutoSettleSeconds(d.daemonSetting(SettingAutoSettleCountdownSeconds), defaultAutoSettleCountdownSeconds),
	}
}

func resolveAutoSettleSeconds(stored string, fallbackSeconds int) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(stored)); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return time.Duration(fallbackSeconds) * time.Second
}

func (d *Daemon) answerAutoSettleByUser(sessionID protocol.SessionID) bool {
	if !d.autoSettleAppliesTo(d.decoratedSession(sessionID)) {
		return false
	}
	answered, hadPending, armed := d.monitorEntry(sessionID).AnswerCountdown()
	if answered && d.debugLogging {
		d.logf("auto-settle answered by user: session=%s had_pending=%v armed=%v", sessionID, hadPending, armed)
	}
	return answered
}

func (d *Daemon) decorateSessionWithAutoSettle(clone *protocol.Session) {
	if clone == nil {
		return
	}
	firesAt := ""
	held, armed := false, false
	if view, ok := d.monitorViewFor(clone.ID); ok {
		auto := view.AutoSettle.V
		if !auto.FiresAt.IsZero() {
			firesAt = auto.FiresAt.UTC().Format(time.RFC3339Nano)
		}
		held, armed = auto.Held, auto.DismissArmed
	}
	clone.AutoSettleDismissArmed = nil
	if armed {
		clone.AutoSettleDismissArmed = protocol.Ptr(true)
	}
	clone.AutoSettleFiresAt = nil
	if firesAt != "" {
		clone.AutoSettleFiresAt = protocol.Ptr(firesAt)
	}
	clone.AutoSettleHeld = nil
	if held {
		clone.AutoSettleHeld = protocol.Ptr(true)
	}
}

func (d *Daemon) autoSettleAppliesTo(session *protocol.Session) bool {
	return session != nil && d.autoSettleSettings().Enabled && !attention.Excluded(d.attentionInputFor(session))
}

func (d *Daemon) decoratedSession(sessionID protocol.SessionID) *protocol.Session {
	if d.store == nil {
		return nil
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return nil
	}
	return d.sessionForBroadcast(session)
}

func (d *Daemon) cancelAllAutoSettle() {
	for _, e := range d.monitors().each() {
		e.ClearAutoSettle()
	}
}

func (d *Daemon) stopAutoSettleTimers() {
	for _, e := range d.monitors().each() {
		e.StopAutoSettle()
	}
}

func (d *Daemon) armAutoSettleForRunningSessions() {
	if d.store == nil {
		return
	}
	for _, session := range d.store.List("") {
		if session == nil || string(session.State) != protocol.StateWorking {
			continue
		}
		d.monitorEntry(session.ID).ArmAutoSettle()
	}
}

func (d *Daemon) stateReason(sessionID protocol.SessionID) string {
	if view, ok := d.monitorViewFor(sessionID); ok {
		return view.State.V.Reason
	}
	return ""
}

func (d *Daemon) decorateSessionWithStateReason(clone *protocol.Session) {
	if clone == nil {
		return
	}
	clone.StateReason = nil
	if !sessionmonitor.ResolverOwnedStates[clone.State] {
		return
	}
	if reason := d.stateReason(clone.ID); reason != "" {
		clone.StateReason = protocol.Ptr(reason)
	}
}

func (d *Daemon) monitorViewFor(session protocol.SessionID) (sessionmonitor.View, bool) {
	r := d.monitors()
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	binding := r.sessions[session]
	if binding == nil {
		return sessionmonitor.View{}, false
	}
	return binding.applied, true
}

func (d *Daemon) runClassifier(ctx context.Context, agent protocol.SessionAgent, directory, text string, timeout time.Duration) (string, error) {
	if agent != "" {
		driver := agentdriver.Get(string(agent))
		if state, err, ok := agentdriver.ClassifyWithDriver(
			ctx,
			driver,
			text,
			d.store.GetSetting(executableSettingKey(string(agent))),
			directory,
			d.classifierModel(ctx, string(agent)),
			timeout,
		); ok {
			return state, err
		}
	}
	claude := agentdriver.Get("claude")
	if state, err, ok := agentdriver.ClassifyWithDriver(
		ctx,
		claude,
		text,
		d.store.GetSetting(canonicalExecutableSettingKey("claude")),
		"",
		d.classifierModel(ctx, "claude"),
		timeout,
	); ok {
		return state, err
	}
	return protocol.StateUnknown, errors.New("no classifier backend available")
}

func (d *Daemon) classifierModel(ctx context.Context, harness string) string {
	fallback := ""
	switch harness {
	case "claude":
		fallback = classifier.ClaudeClassifierModel()
	case "codex":
		fallback = classifier.CodexClassifierModel()
	default:
		return ""
	}
	explicit := os.Getenv("ATTN_" + strings.ToUpper(harness) + "_CLASSIFIER_MODEL")
	return d.resolveTierModel(ctx, harness, "", modeltiers.Light, explicit, fallback)
}

func (d *Daemon) updateEvidence(sessionID protocol.SessionID, unchanged func(*sessionmonitor.Evidence) bool, mutate func(*sessionmonitor.Evidence)) bool {
	if d.store == nil || d.store.Get(sessionID) == nil {
		return false
	}
	return d.monitorEntry(sessionID).UpdateEvidence(unchanged, mutate)
}

func (d *Daemon) restoreTranscriptWatchers() {
	if d.store == nil || d.ptyBackend == nil {
		return
	}
	live := d.liveSessions(context.Background())
	for _, session := range d.store.List("") {
		if session == nil || session.Agent == protocol.SessionAgentShell {
			continue
		}
		if _, ok := live[session.ID]; !ok {
			continue
		}
		binding := d.store.GetSessionConversation(session.ID)
		if binding.NativeID == "" {
			continue
		}
		d.monitorEntry(session.ID).StartTranscriptWatcher(binding.TranscriptPath)
	}
	d.restorePluginUsageWatchers()
}

func (d *Daemon) restorePluginUsageWatchers() {
	if d.store == nil {
		return
	}
	for _, run := range d.store.ListActiveAgentDriverRuns() {
		if strings.TrimSpace(run.TranscriptPath) == "" || d.store.Get(run.SessionID) == nil {
			continue
		}
		d.monitorEntry(run.SessionID).WatchDriverUsage(run.TranscriptPath)
	}
}

func (d *Daemon) armPluginDriverSilenceWatch(pluginName string) {
	if d.store == nil {
		return
	}
	for _, run := range d.store.ListAgentDriverRuns(pluginName) {
		d.monitorEntry(run.SessionID).ArmDriverSilence(run.RunID, pluginName)
	}
}

func (d *Daemon) armPluginDriverSilenceWatchForEveryRun() {
	if d.store == nil {
		return
	}
	for _, run := range d.store.ListActiveAgentDriverRuns() {
		d.monitorEntry(run.SessionID).ArmDriverSilence(run.RunID, run.PluginName)
	}
}

func (d *Daemon) notePluginDriverReport(sessionID protocol.SessionID) {
	if d.monitorEntry(sessionID).NoteDriverReport() {
		d.logf("plugin driver silence cleared: session=%s", sessionID)
	}
}

func (d *Daemon) observePromptSubmitted(sessionID protocol.SessionID, at time.Time) bool {
	if entry := d.monitorEntry(sessionID); entry != nil {
		return entry.ObservePromptSubmitted(at)
	}
	d.markModelRequestStarted(sessionID, at)
	return false
}

func (d *Daemon) markModelRequestStarted(sessionID protocol.SessionID, at time.Time) bool {
	if d.store == nil {
		return false
	}
	return d.monitorEntry(sessionID).MarkModelRequestStarted(at)
}

func (d *Daemon) writeSessionPTY(sessionID protocol.SessionID, data []byte, source string) error {
	return d.monitorEntry(sessionID).WriteInput(context.Background(), d.primaryTerminal(sessionID), data, strings.TrimSpace(source))
}

func (d *Daemon) writeTerminalPTY(ctx context.Context, terminal harness.TerminalID, data []byte, source string) error {
	sessionID, shown := d.shownIn(terminal)
	if !shown {
		if d.ptyBackend == nil {
			return errNoPTYBackend
		}
		return d.ptyBackend.Input(ctx, terminal, data)
	}
	return d.monitorEntry(sessionID).WriteInput(ctx, terminal, data, strings.TrimSpace(source))
}

var errNoPTYBackend = errors.New("session has no PTY backend")

func (d *Daemon) beginAgentDriverRun(session protocol.SessionID, plugin, run string) bool {
	return d.monitorEntry(session).BeginDriverRun(plugin, run)
}

func (d *Daemon) endAgentDriverRun(session protocol.SessionID) store.AgentDriverReportCursor {
	ended := d.monitorEntry(session).EndDriverRun()
	return store.AgentDriverReportCursor{PluginName: ended.Plugin, RunID: ended.Run, Seq: ended.Sequence}
}

func (d *Daemon) classifyPluginStop(session *protocol.Session, text string) (string, error) {
	if !headless.Enabled() {
		d.logf("headless task refused (classifier)")
		label := ""
		if session != nil {
			label = string(session.ID)
		}
		return sessionmonitor.ClassifyFromMarker(d.logf, label, text)
	}
	timeout := 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var agent protocol.SessionAgent
	var directory string
	if session != nil {
		agent, directory = session.Agent, session.Directory
	}
	return d.runClassifier(ctx, agent, directory, text, timeout)
}

func (d *Daemon) quietNotifications() bool {
	return parseBooleanSetting(d.daemonSetting(SettingQueueQuietNotifications)) && d.queueFlowOn()
}

func (d *Daemon) queueFlowOn() bool {
	return parseBooleanSetting(d.daemonSetting(SettingQueueModeEnabled))
}

func (d *Daemon) touchSession(sessionID protocol.SessionID) {
	d.monitorEntry(sessionID).Touch(time.Now())
}

func (d *Daemon) syncMonitorConversation(sessionID protocol.SessionID) {
	binding := d.store.GetSessionConversation(sessionID)
	d.monitorEntry(sessionID).SetConversation(sessionmonitor.ConversationObservation{NativeID: binding.NativeID, TranscriptPath: binding.TranscriptPath})
}
