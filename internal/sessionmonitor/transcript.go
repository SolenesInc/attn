package sessionmonitor

import (
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/activity"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const (
	transcriptPollInterval   = 500 * time.Millisecond
	transcriptQuietWindow    = 1500 * time.Millisecond
	assistantDedupWindow     = 2 * time.Second
	transcriptDiscoveryGrace = 2 * time.Second
)

const (
	AnnotatableMessageMaxChars = 64 * 1024
	AnnotatableWindowMessages  = 32
	AnnotatableWindowMaxChars  = 256 * 1024
)

type transcriptWatcher struct {
	monitor       *Monitor
	agent         protocol.SessionAgent
	cwd           string
	startedAt     time.Time
	preferredPath string
	behavior      agentdriver.TranscriptWatcherBehavior
	usageTracker  *usageTracker
	stopCh        chan struct{}
	doneCh        chan struct{}

	mu             sync.RWMutex
	status         protocol.SessionMessageWindowStatus
	detail         string
	transcriptPath string
	window         *transcript.AssistantWindow
	sessionState   protocol.SessionState
}

type AssistantWindow struct {
	Status   protocol.SessionMessageWindowStatus
	Messages []transcript.AssistantMessage
	Report   transcript.AssistantWindowReport
	Detail   string
}

func newTranscriptWatcher(monitor *Monitor, agent protocol.SessionAgent, cwd string, startedAt time.Time, behavior agentdriver.TranscriptWatcherBehavior) *transcriptWatcher {
	return &transcriptWatcher{
		monitor:   monitor,
		agent:     agent,
		cwd:       cwd,
		startedAt: startedAt,
		behavior:  behavior,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
		status:    protocol.SessionMessageWindowStatusDiscovering,
		window:    newAnnotatableWindow(),
	}
}

func newAnnotatableWindow() *transcript.AssistantWindow {
	return transcript.NewAssistantWindow(transcript.AssistantWindowLimits{
		MaxMessages:     AnnotatableWindowMessages,
		MaxMessageChars: AnnotatableMessageMaxChars,
		MaxTotalChars:   AnnotatableWindowMaxChars,
	})
}

func (w *transcriptWatcher) snapshot() AssistantWindow {
	w.mu.RLock()
	defer w.mu.RUnlock()
	messages, report := w.window.Snapshot()
	return AssistantWindow{Status: w.status, Messages: messages, Report: report, Detail: w.detail}
}

func (w *transcriptWatcher) exactPath() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.status != protocol.SessionMessageWindowStatusReady {
		return ""
	}
	return w.transcriptPath
}

func (w *transcriptWatcher) setStatus(status protocol.SessionMessageWindowStatus, detail string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	changed := w.status != status || w.detail != detail
	w.status = status
	w.detail = detail
	return changed
}

func (w *transcriptWatcher) resetSource(status protocol.SessionMessageWindowStatus, path, detail string, omittedPrefix bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = status
	w.detail = detail
	w.transcriptPath = path
	w.window = newAnnotatableWindow()
	if omittedPrefix {
		w.window.MarkPrefixOmitted()
	}
}

func (w *transcriptWatcher) applyEvents(events []transcript.Event) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.window.Apply(events)
}

func (w *transcriptWatcher) state() protocol.SessionState {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.sessionState
}

func (w *transcriptWatcher) setState(state protocol.SessionState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sessionState = state
}

func isDuplicateAssistantEvent(lastContent string, lastAt time.Time, content string, now time.Time) bool {
	return content == lastContent && !lastAt.IsZero() && now.Sub(lastAt) <= assistantDedupWindow
}

func IsTranscriptWatchedAgent(agent protocol.SessionAgent) bool {
	d := agentdriver.Get(string(agent))
	if d == nil {
		return false
	}
	caps := agentdriver.EffectiveCapabilities(d)
	if !caps.HasTranscript || !caps.HasTranscriptWatcher {
		return false
	}
	if _, ok := agentdriver.GetTranscriptFinder(d); !ok {
		return false
	}
	return true
}

func (w *transcriptWatcher) resolveExactPath() string {
	m := w.monitor
	for _, path := range []string{strings.TrimSpace(w.preferredPath), m.boundConversation().TranscriptPath} {
		if path == "" {
			continue
		}
		path = m.registry.host.LinkTranscriptPath(m.linkKind(), path)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func FindTranscriptForResume(agent protocol.SessionAgent, resumeID string) string {
	resumeID = strings.TrimSpace(resumeID)
	if resumeID == "" {
		return ""
	}
	tf, ok := agentdriver.GetTranscriptFinder(agentdriver.Get(string(agent)))
	if !ok {
		return ""
	}
	return strings.TrimSpace(tf.FindTranscriptForResume(resumeID))
}

func (w *transcriptWatcher) discover() string {
	path := FindTranscriptForResume(w.agent, w.monitor.boundConversation().NativeID)
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func (w *transcriptWatcher) discoveryDeadline(now time.Time) time.Time {
	if strings.TrimSpace(w.preferredPath) != "" || strings.TrimSpace(w.monitor.boundConversation().TranscriptPath) != "" {
		return now.Add(transcriptDiscoveryGrace)
	}
	return now
}

func watcherStopped(w *transcriptWatcher) bool {
	select {
	case <-w.doneCh:
		return true
	default:
		return false
	}
}

func (m *Monitor) EnsureTranscriptWatcher(transcriptPath string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	agent := m.agent
	m.mu.Unlock()
	if !IsTranscriptWatchedAgent(agent) {
		return
	}
	transcriptPath = strings.TrimSpace(transcriptPath)
	m.mu.Lock()
	watcher := m.transcriptWatch
	current := watcher != nil && watcher.agent == agent &&
		strings.TrimSpace(watcher.preferredPath) == transcriptPath && !watcherStopped(watcher)
	m.mu.Unlock()
	if current {
		return
	}
	m.StartTranscriptWatcher(transcriptPath)
}

func transcriptBootstrapBytesForAgent(agent protocol.SessionAgent) int64 {
	if tf, ok := agentdriver.GetTranscriptFinder(agentdriver.Get(string(agent))); ok {
		if n := tf.BootstrapBytes(); n > 0 {
			return n
		}
	}
	return 0
}

func (m *Monitor) StartTranscriptWatcher(transcriptPath string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	agent, cwd, startedAt := m.agent, m.directory, m.startedAt
	m.mu.Unlock()
	if !IsTranscriptWatchedAgent(agent) {
		return
	}
	behavior, ok := agentdriver.GetTranscriptWatcherBehavior(agentdriver.Get(string(agent)))
	if !ok {
		return
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	m.mu.Lock()
	if m.usageDraining {
		m.mu.Unlock()
		return
	}
	watcher := newTranscriptWatcher(m, agent, cwd, startedAt, behavior)
	watcher.preferredPath = strings.TrimSpace(transcriptPath)
	watcher.setState(m.state.State)

	// Admitted before it replaces the previous watcher, so a refused start leaves that one to stop's final reconcile.
	if !m.registry.host.Go("runTranscriptWatcher", watcher.run) {
		m.mu.Unlock()
		return
	}
	m.beginUsageRunLocked()
	previous := m.transcriptWatch
	m.transcriptWatch = watcher
	label := m.label
	m.mu.Unlock()
	if previous != nil {
		close(previous.stopCh)
	}
	m.logf("transcript watcher: started session=%s agent=%s cwd=%s", label, agent, cwd)
}

func (m *Monitor) UpdateTranscriptState(state protocol.SessionState) {
	if m == nil {
		return
	}
	m.mu.Lock()
	watcher := m.transcriptWatch
	m.mu.Unlock()
	if watcher != nil {
		watcher.setState(state)
	}
}

func (w *transcriptWatcher) applyUsageAvailability(batch transcript.FollowBatch) {
	if len(batch.Records) == 0 || transcript.SupportsUsage(string(w.agent)) {
		return
	}
	for _, event := range batch.Events {
		if event.Kind == transcript.EventKindAssistant {
			if w.monitor.markCostSource(w.exactPath(), false, true, nil) {
				w.monitor.publishCost()
			}
			break
		}
	}
}

func (m *Monitor) StopTranscriptWatchers() {
	if m == nil {
		return
	}
	m.mu.Lock()
	watcher, plugin := m.transcriptWatch, m.pluginUsageWatch
	m.transcriptWatch, m.pluginUsageWatch = nil, nil
	m.mu.Unlock()
	if plugin != nil {
		close(plugin.stopCh)
	}
	if watcher != nil {
		close(watcher.stopCh)
	}
}

func (r *Registry) StopTranscriptWatchers() {
	monitors := r.each()
	for _, m := range monitors {
		m.StopTranscriptWatchers()
	}
	for _, m := range monitors {
		m.reconcileDeferredUsage()
	}
}

func (m *Monitor) deferFinalUsage(reconcile func()) {
	m.mu.Lock()
	m.finalUsage = append(m.finalUsage, reconcile)
	m.mu.Unlock()
}

func (m *Monitor) beginUsageRunLocked() {
	m.usageRuns++
	if m.usageIdle == nil {
		m.usageIdle = make(chan struct{})
	}
}

func (m *Monitor) endUsageRun() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usageRuns--
	if m.usageRuns > 0 {
		return
	}
	close(m.usageIdle)
	m.usageIdle = nil
}

func (m *Monitor) reconcileDeferredUsage() {
	m.mu.Lock()
	reconciles := m.finalUsage
	m.finalUsage = nil
	m.mu.Unlock()
	for _, reconcile := range reconciles {
		reconcile()
	}
}

func (m *Monitor) FinishUsage() func() {
	release, _ := m.finishUsage()
	return release
}

func (m *Monitor) finishUsage() (func(), *usageTracker) {
	if m == nil {
		return func() {}, nil
	}
	m.mu.Lock()
	m.usageDraining = true
	watcher, plugin, idle := m.transcriptWatch, m.pluginUsageWatch, m.usageIdle
	m.transcriptWatch, m.pluginUsageWatch = nil, nil
	m.mu.Unlock()
	if plugin != nil {
		close(plugin.stopCh)
	}
	if watcher != nil {
		close(watcher.stopCh)
	}
	if idle != nil {
		<-idle
	}
	m.reconcileDeferredUsage()
	var tracker *usageTracker
	if watcher != nil {
		tracker = watcher.usageTracker
	}
	return func() { m.mu.Lock(); m.usageDraining = false; m.mu.Unlock() }, tracker
}

func (m *Monitor) AssistantWindow(agent protocol.SessionAgent) (AssistantWindow, bool) {
	if m == nil {
		return AssistantWindow{}, false
	}
	m.mu.Lock()
	watcher := m.transcriptWatch
	m.mu.Unlock()
	if watcher == nil || watcher.agent != agent {
		return AssistantWindow{}, false
	}
	return watcher.snapshot(), true
}

func (m *Monitor) LiveTranscriptPath(agent protocol.SessionAgent) string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	watcher := m.transcriptWatch
	m.mu.Unlock()
	if watcher == nil || watcher.agent != agent {
		return ""
	}
	return watcher.exactPath()
}

func (m *Monitor) transcriptPathFor(path string) string {
	path = strings.TrimSpace(path)
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if bound := strings.TrimSpace(m.boundConversation().TranscriptPath); bound != "" {
		if _, err := os.Stat(bound); err == nil {
			return bound
		}
	}
	m.mu.Lock()
	agent := m.agent
	m.mu.Unlock()
	if watched := m.LiveTranscriptPath(agent); watched != "" {
		return watched
	}
	return path
}

func (m *Monitor) ActivityTranscript() string {
	if m == nil {
		return ""
	}
	return m.transcriptPathFor("")
}

func (m *Monitor) TranscriptEvents(cursor string) (activity.Window, error) {
	if m == nil {
		return activity.Window{}, errNoActivityTranscript
	}
	path := m.ActivityTranscript()
	if path == "" {
		return activity.Window{}, errNoActivityTranscript
	}
	m.mu.Lock()
	agent := m.agent
	m.mu.Unlock()
	return activity.Read(path, string(agent), cursor)
}

func (m *Monitor) TranscriptHead() (string, error) {
	if m == nil {
		return "", errNoActivityTranscript
	}
	path := m.ActivityTranscript()
	if path == "" {
		return "", errNoActivityTranscript
	}
	return activity.SeedCursor(path)
}

var errNoActivityTranscript = errors.New("the session has no live transcript")

func (w *transcriptWatcher) run() {
	m := w.monitor
	label := m.Label()
	defer m.endUsageRun()
	defer close(w.doneCh)

	if w.behavior == nil {
		m.logf("transcript watcher: no behavior configured session=%s agent=%s", label, w.agent)
		return
	}

	ticker := time.NewTicker(transcriptPollInterval)
	defer ticker.Stop()

	var (
		transcriptPath string
		follower       *transcript.Follower
		readFileInfo   os.FileInfo

		lastAssistantAt time.Time
		lastAssistant   string
		assistantSeq    int64
		classifiedSeq   int64

		discoveryDeadline = w.discoveryDeadline(time.Now())
		fallbackAttempted bool
		usageState        = w.state()
	)
	defer func() {
		if w.usageTracker != nil {
			w.usageTracker.Reconcile()
		}
	}()

	for {
		select {
		case <-w.stopCh:
			m.logf("transcript watcher: stopped session=%s", label)
			return
		case <-m.registry.host.Done():
			if w.usageTracker != nil {
				m.deferFinalUsage(w.usageTracker.Reconcile)
				w.usageTracker = nil
			}
			return
		case <-ticker.C:
		}

		sessionState := w.state()
		usageSettled := usageState == protocol.SessionStateWorking && sessionState != protocol.SessionStateWorking
		usageState = sessionState
		windowChanged := false

		if transcriptPath == "" {
			transcriptPath = w.resolveExactPath()
			if transcriptPath == "" && time.Now().Before(discoveryDeadline) {
				continue
			}
			if transcriptPath == "" && sessionState == protocol.SessionStateLaunching {
				continue
			}
			if transcriptPath == "" && !fallbackAttempted {
				fallbackAttempted = true
				transcriptPath = w.discover()
			}
			if transcriptPath == "" {
				if w.setStatus(protocol.SessionMessageWindowStatusUnavailable, "no exact transcript is bound to this live session") {
					m.assistantWindowChanged()
				}
				m.logf("transcript watcher: exact transcript unavailable session=%s agent=%s cwd=%s", label, w.agent, w.cwd)
				return
			}
			info, err := os.Stat(transcriptPath)
			if err != nil {
				m.logf("transcript watcher: transcript stat failed session=%s path=%s err=%v", label, transcriptPath, err)
				w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript could not be opened", false)
				m.assistantWindowChanged()
				return
			}
			startOffset := info.Size()
			bootstrapBytes := transcriptBootstrapBytesForAgent(w.agent)
			if bootstrapBytes > 0 && info.Size() > bootstrapBytes {
				startOffset = info.Size() - bootstrapBytes
			} else if bootstrapBytes > 0 {
				startOffset = 0
			}
			follower, err = transcript.NewFollower(transcriptPath, string(w.agent), startOffset)
			if err != nil {
				m.logf("transcript watcher: follower init failed session=%s path=%s err=%v", label, transcriptPath, err)
				w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript could not be read", false)
				m.assistantWindowChanged()
				return
			}
			w.resetSource(protocol.SessionMessageWindowStatusReady, transcriptPath, "", startOffset > 0)
			windowChanged = true
			fallbackAttempted = false
			w.usageTracker = newUsageTracker(w, transcriptPath)
			m.logf("transcript watcher: transcript discovered session=%s path=%s offset=%d", label, transcriptPath, startOffset)
		}

		info, err := os.Stat(transcriptPath)
		if err != nil {
			m.logf("transcript watcher: transcript unavailable, rediscovering session=%s path=%s err=%v", label, transcriptPath, err)
			w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript became unavailable", false)
			m.assistantWindowChanged()
			transcriptPath = ""
			follower = nil
			w.usageTracker = nil
			readFileInfo = nil
			discoveryDeadline = w.discoveryDeadline(time.Now())
			fallbackAttempted = false
			continue
		}

		transcriptMoved := readFileInfo == nil ||
			info.Size() != readFileInfo.Size() ||
			!info.ModTime().Equal(readFileInfo.ModTime()) ||
			!os.SameFile(info, readFileInfo)
		if follower != nil && transcriptMoved {
			batch, readErr := follower.Read()
			if errors.Is(readErr, transcript.ErrCursorMismatch) || errors.Is(readErr, transcript.ErrCursorPastEnd) {
				m.logf("transcript watcher: transcript replaced session=%s path=%s", label, transcriptPath)
				w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript was replaced", false)
				m.assistantWindowChanged()
				return
			}
			if readErr != nil {
				m.logf("transcript watcher: read delta error session=%s path=%s err=%v", label, transcriptPath, readErr)
				w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript could not be read", false)
				m.assistantWindowChanged()
				return
			}

			for _, record := range batch.Records {
				if len(record.Raw) == 0 {
					continue
				}
				now := time.Now()

				lineResult := w.behavior.HandleLine(record.Raw)
				if lineResult.Log != "" {
					m.logf("%s session=%s", lineResult.Log, label)
				}
				if lineResult.Aborted {
					if skip, reason := staleTranscriptAbort(lineResult.AbortAt, w.startedAt); skip {
						m.logf("transcript watcher: ignoring turn abort session=%s reason=%s abort_at=%s", label, reason, lineResult.AbortAt.Format(time.RFC3339Nano))
					} else {
						m.RecordTurnAborted(lineResult.AbortDetail, lineResult.AbortAt, now)
						classifiedSeq = assistantSeq
					}
				}

				for _, event := range record.Events {
					if event.Kind != transcript.EventKindAssistant || strings.TrimSpace(event.Text) == "" {
						continue
					}
					content := event.Text
					if w.behavior.DeduplicateAssistantEvents() &&
						isDuplicateAssistantEvent(lastAssistant, lastAssistantAt, content, now) {
						continue
					}
					assistantSeq++
					lastAssistant = content
					lastAssistantAt = now
					logMsg := content
					if len(logMsg) > 120 {
						logMsg = logMsg[:120] + "..."
					}
					m.logf("transcript watcher: assistant event session=%s seq=%d chars=%d preview=%q", label, assistantSeq, len(content), logMsg)
				}
			}
			windowChanged = w.applyEvents(batch.Events) || windowChanged
			w.applyUsageAvailability(batch)
			if w.usageTracker != nil {
				w.usageTracker.Reconcile()
			}
			readFileInfo = info
		}
		if windowChanged {
			m.assistantWindowChanged()
		}
		if usageSettled && w.usageTracker != nil {
			w.usageTracker.Reconcile()
		}

		quietSince := w.behavior.QuietSince(lastAssistantAt)
		if assistantSeq > classifiedSeq &&
			!lastAssistantAt.IsZero() &&
			!quietSince.IsZero() &&
			time.Since(quietSince) >= transcriptQuietWindow {
			m.mu.Lock()
			current := m.state.State
			m.mu.Unlock()
			if skip, reason := w.behavior.SkipClassification(current, m.lastSeen(), time.Now()); skip {
				if strings.TrimSpace(reason) == "" {
					reason = "transcript watcher: skipping classification"
				}
				m.logf("%s session=%s state=%s", reason, label, current)
				continue
			}

			classifiedSeq = assistantSeq
			m.logf(
				"transcript watcher: quiet window reached session=%s seq=%d transcript=%s quiet_since=%s",
				label,
				assistantSeq,
				transcriptPath,
				quietSince.Format(time.RFC3339Nano),
			)
			path := transcriptPath
			m.registry.host.Go("classifySessionState", func() { m.ClassifyStop(path, StopClassification{}) })
		}
	}
}

func staleTranscriptAbort(abortAt, sessionStartedAt time.Time) (bool, string) {
	if abortAt.IsZero() {
		return true, "undated"
	}
	if !sessionStartedAt.IsZero() && abortAt.Before(sessionStartedAt) {
		return true, "predates session"
	}
	return false, ""
}

func (m *Monitor) boundConversation() ConversationObservation {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.conversationInput.ConversationObservation
}

func (m *Monitor) lastSeen() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.touchedAt.IsZero() {
		return ""
	}
	return string(protocol.NewTimestamp(m.touchedAt))
}

func (m *Monitor) assistantWindowChanged() {
	m.mu.Lock()
	m.windowRevision++
	view := m.viewLocked()
	m.mu.Unlock()
	m.publish(view)
}

func (m *Monitor) linkKind() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.link
}
