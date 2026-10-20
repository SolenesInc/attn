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
	launchConversationWindow = 2 * time.Minute
)

const (
	AnnotatableMessageMaxChars = 64 * 1024
	AnnotatableWindowMessages  = 32
	AnnotatableWindowMaxChars  = 256 * 1024
)

type transcriptWatcher struct {
	entry         *Entry
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

func newTranscriptWatcher(entry *Entry, agent protocol.SessionAgent, cwd string, startedAt time.Time, behavior agentdriver.TranscriptWatcherBehavior) *transcriptWatcher {
	return &transcriptWatcher{
		entry:     entry,
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
	e := w.entry
	for _, path := range []string{strings.TrimSpace(w.preferredPath), e.boundConversation().TranscriptPath} {
		if path == "" {
			continue
		}
		path = e.registry.host.LinkTranscriptPath(e, path)
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
	path := FindTranscriptForResume(w.agent, w.entry.boundConversation().NativeID)
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func FindsLaunchConversation(agent protocol.SessionAgent) bool {
	_, ok := agentdriver.GetLaunchTranscriptFinder(agentdriver.Get(string(agent)))
	return ok
}

func (w *transcriptWatcher) awaitsLaunchConversation() bool {
	return FindsLaunchConversation(w.agent) && w.entry.boundConversation().NativeID == ""
}

func (w *transcriptWatcher) bindLaunchConversation() bool {
	driver := agentdriver.Get(string(w.agent))
	finder, ok := agentdriver.GetLaunchTranscriptFinder(driver)
	if !ok {
		return false
	}
	path := strings.TrimSpace(finder.FindLaunchTranscript(w.cwd, w.startedAt, w.entry.claimedElsewhere))
	nativeID := agentdriver.ResumeSessionIDFromTranscriptPath(driver, path)
	if nativeID == "" {
		return false
	}
	if !w.entry.ProposeConversation(ConversationObservation{NativeID: nativeID, TranscriptPath: path}, ConversationGuessedAtLaunch, "") {
		return false
	}
	w.entry.logf("transcript watcher: bound launch conversation session=%s native=%s path=%s", w.entry.Label(), nativeID, path)
	return true
}

func (w *transcriptWatcher) discoveryDeadline(now time.Time) time.Time {
	if strings.TrimSpace(w.preferredPath) != "" || strings.TrimSpace(w.entry.boundConversation().TranscriptPath) != "" {
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

func (e *Entry) EnsureTranscriptWatcher(transcriptPath string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	agent := e.agent
	e.mu.Unlock()
	if !IsTranscriptWatchedAgent(agent) {
		return
	}
	transcriptPath = strings.TrimSpace(transcriptPath)
	e.mu.Lock()
	watcher := e.transcriptWatch
	current := watcher != nil && watcher.agent == agent &&
		strings.TrimSpace(watcher.preferredPath) == transcriptPath && !watcherStopped(watcher)
	e.mu.Unlock()
	if current {
		return
	}
	e.StartTranscriptWatcher(transcriptPath)
}

func transcriptBootstrapBytesForAgent(agent protocol.SessionAgent) int64 {
	if tf, ok := agentdriver.GetTranscriptFinder(agentdriver.Get(string(agent))); ok {
		if n := tf.BootstrapBytes(); n > 0 {
			return n
		}
	}
	return 0
}

func (e *Entry) StartTranscriptWatcher(transcriptPath string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	agent, cwd, startedAt := e.agent, e.directory, e.startedAt
	e.mu.Unlock()
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
	e.mu.Lock()
	if e.usageDraining {
		e.mu.Unlock()
		return
	}
	watcher := newTranscriptWatcher(e, agent, cwd, startedAt, behavior)
	watcher.preferredPath = strings.TrimSpace(transcriptPath)
	watcher.setState(e.state.State)

	// Admitted before it replaces the previous watcher, so a refused start leaves that one to stop's final reconcile.
	if !e.registry.host.Go("runTranscriptWatcher", watcher.run) {
		e.mu.Unlock()
		return
	}
	e.beginUsageRunLocked()
	previous := e.transcriptWatch
	e.transcriptWatch = watcher
	label := e.label
	e.mu.Unlock()
	if previous != nil {
		close(previous.stopCh)
	}
	e.logf("transcript watcher: started session=%s agent=%s cwd=%s", label, agent, cwd)
}

func (e *Entry) UpdateTranscriptState(state protocol.SessionState) {
	if e == nil {
		return
	}
	e.mu.Lock()
	watcher := e.transcriptWatch
	e.mu.Unlock()
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
			w.entry.markCostSource(w.exactPath(), false, true, nil)
			break
		}
	}
}

func (e *Entry) StopTranscriptWatchers() {
	if e == nil {
		return
	}
	e.mu.Lock()
	watcher, plugin := e.transcriptWatch, e.pluginUsageWatch
	e.transcriptWatch, e.pluginUsageWatch = nil, nil
	e.mu.Unlock()
	if plugin != nil {
		close(plugin.stopCh)
	}
	if watcher != nil {
		close(watcher.stopCh)
	}
}

func (r *Registry) StopTranscriptWatchers() {
	entries := r.each()
	for _, e := range entries {
		e.StopTranscriptWatchers()
	}
	for _, e := range entries {
		e.reconcileDeferredUsage()
	}
}

func (e *Entry) deferFinalUsage(reconcile func()) {
	e.mu.Lock()
	e.finalUsage = append(e.finalUsage, reconcile)
	e.mu.Unlock()
}

func (e *Entry) beginUsageRunLocked() {
	e.usageRuns++
	if e.usageIdle == nil {
		e.usageIdle = make(chan struct{})
	}
}

func (e *Entry) endUsageRun() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.usageRuns--
	if e.usageRuns > 0 {
		return
	}
	close(e.usageIdle)
	e.usageIdle = nil
}

func (e *Entry) reconcileDeferredUsage() {
	e.mu.Lock()
	reconciles := e.finalUsage
	e.finalUsage = nil
	e.mu.Unlock()
	for _, reconcile := range reconciles {
		reconcile()
	}
}

func (e *Entry) FinishUsage() func() {
	release, _ := e.finishUsage()
	return release
}

func (e *Entry) finishUsage() (func(), *usageTracker) {
	if e == nil {
		return func() {}, nil
	}
	e.mu.Lock()
	e.usageDraining = true
	watcher, plugin, idle := e.transcriptWatch, e.pluginUsageWatch, e.usageIdle
	e.transcriptWatch, e.pluginUsageWatch = nil, nil
	e.mu.Unlock()
	if plugin != nil {
		close(plugin.stopCh)
	}
	if watcher != nil {
		close(watcher.stopCh)
	}
	if idle != nil {
		<-idle
	}
	e.reconcileDeferredUsage()
	var tracker *usageTracker
	if watcher != nil {
		tracker = watcher.usageTracker
	}
	return func() { e.mu.Lock(); e.usageDraining = false; e.mu.Unlock() }, tracker
}

func (e *Entry) AssistantWindow(agent protocol.SessionAgent) (AssistantWindow, bool) {
	if e == nil {
		return AssistantWindow{}, false
	}
	e.mu.Lock()
	watcher := e.transcriptWatch
	e.mu.Unlock()
	if watcher == nil || watcher.agent != agent {
		return AssistantWindow{}, false
	}
	return watcher.snapshot(), true
}

func (e *Entry) LiveTranscriptPath(agent protocol.SessionAgent) string {
	if e == nil {
		return ""
	}
	e.mu.Lock()
	watcher := e.transcriptWatch
	e.mu.Unlock()
	if watcher == nil || watcher.agent != agent {
		return ""
	}
	return watcher.exactPath()
}

func (e *Entry) transcriptPathFor(path string) string {
	path = strings.TrimSpace(path)
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if bound := strings.TrimSpace(e.boundConversation().TranscriptPath); bound != "" {
		if _, err := os.Stat(bound); err == nil {
			return bound
		}
	}
	e.mu.Lock()
	agent := e.agent
	e.mu.Unlock()
	if watched := e.LiveTranscriptPath(agent); watched != "" {
		return watched
	}
	return path
}

func (e *Entry) ActivityTranscript() string {
	if e == nil {
		return ""
	}
	return e.transcriptPathFor("")
}

func (e *Entry) TranscriptEvents(cursor string) (activity.Window, error) {
	if e == nil {
		return activity.Window{}, errNoActivityTranscript
	}
	path := e.ActivityTranscript()
	if path == "" {
		return activity.Window{}, errNoActivityTranscript
	}
	e.mu.Lock()
	agent := e.agent
	e.mu.Unlock()
	return activity.Read(path, string(agent), cursor)
}

func (e *Entry) TranscriptHead() (string, error) {
	if e == nil {
		return "", errNoActivityTranscript
	}
	path := e.ActivityTranscript()
	if path == "" {
		return "", errNoActivityTranscript
	}
	return activity.SeedCursor(path)
}

var errNoActivityTranscript = errors.New("the session has no live transcript")

func (w *transcriptWatcher) run() {
	e := w.entry
	label := e.Label()
	defer e.endUsageRun()
	defer close(w.doneCh)

	if w.behavior == nil {
		e.logf("transcript watcher: no behavior configured session=%s agent=%s", label, w.agent)
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
			e.logf("transcript watcher: stopped session=%s", label)
			return
		case <-e.registry.host.Done():
			if w.usageTracker != nil {
				e.deferFinalUsage(w.usageTracker.Reconcile)
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
			if transcriptPath == "" && w.awaitsLaunchConversation() {
				if w.bindLaunchConversation() {
					return
				}
				if time.Now().Before(w.startedAt.Add(launchConversationWindow)) {
					continue
				}
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
					e.assistantWindowChanged()
				}
				e.logf("transcript watcher: exact transcript unavailable session=%s agent=%s cwd=%s", label, w.agent, w.cwd)
				return
			}
			info, err := os.Stat(transcriptPath)
			if err != nil {
				e.logf("transcript watcher: transcript stat failed session=%s path=%s err=%v", label, transcriptPath, err)
				w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript could not be opened", false)
				e.assistantWindowChanged()
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
				e.logf("transcript watcher: follower init failed session=%s path=%s err=%v", label, transcriptPath, err)
				w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript could not be read", false)
				e.assistantWindowChanged()
				return
			}
			w.behavior.Reset()
			w.resetSource(protocol.SessionMessageWindowStatusReady, transcriptPath, "", startOffset > 0)
			windowChanged = true
			fallbackAttempted = false
			w.usageTracker = newUsageTracker(w, transcriptPath)
			e.logf("transcript watcher: transcript discovered session=%s path=%s offset=%d", label, transcriptPath, startOffset)
		}

		info, err := os.Stat(transcriptPath)
		if err != nil {
			e.logf("transcript watcher: transcript unavailable, rediscovering session=%s path=%s err=%v", label, transcriptPath, err)
			w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript became unavailable", false)
			e.assistantWindowChanged()
			transcriptPath = ""
			follower = nil
			w.usageTracker = nil
			readFileInfo = nil
			discoveryDeadline = w.discoveryDeadline(time.Now())
			fallbackAttempted = false
			w.behavior.Reset()
			continue
		}

		transcriptMoved := readFileInfo == nil ||
			info.Size() != readFileInfo.Size() ||
			!info.ModTime().Equal(readFileInfo.ModTime()) ||
			!os.SameFile(info, readFileInfo)
		if follower != nil && transcriptMoved {
			batch, readErr := follower.Read()
			if errors.Is(readErr, transcript.ErrCursorMismatch) || errors.Is(readErr, transcript.ErrCursorPastEnd) {
				e.logf("transcript watcher: transcript replaced session=%s path=%s", label, transcriptPath)
				w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript was replaced", false)
				e.assistantWindowChanged()
				return
			}
			if readErr != nil {
				e.logf("transcript watcher: read delta error session=%s path=%s err=%v", label, transcriptPath, readErr)
				w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript could not be read", false)
				e.assistantWindowChanged()
				return
			}

			for _, record := range batch.Records {
				if len(record.Raw) == 0 {
					continue
				}
				now := time.Now()

				lineResult := w.behavior.HandleLine(record.Raw, now, sessionState)
				if lineResult.Log != "" {
					e.logf("%s session=%s", lineResult.Log, label)
				}
				if lineResult.Aborted {
					if skip, reason := staleTranscriptAbort(lineResult.AbortAt, w.startedAt); skip {
						e.logf("transcript watcher: ignoring turn abort session=%s reason=%s abort_at=%s", label, reason, lineResult.AbortAt.Format(time.RFC3339Nano))
					} else {
						e.RecordTurnAborted(lineResult.AbortDetail, lineResult.AbortAt, now)
						classifiedSeq = assistantSeq
					}
				}
				if lineResult.BracketClosed {
					e.recordTurnBracketClosedEvidence(now)
				}
				if lineResult.State != "" && protocol.SessionState(lineResult.State) != sessionState {
					e.recordTranscriptEvidence(lineResult.State, "transcript line", now)
					sessionState = protocol.SessionState(lineResult.State)
				}

				for _, event := range record.Events {
					if event.Kind != transcript.EventKindAssistant || strings.TrimSpace(event.Text) == "" {
						continue
					}
					content := event.Text
					w.behavior.HandleAssistantMessage(now)
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
					e.logf("transcript watcher: assistant event session=%s seq=%d chars=%d preview=%q", label, assistantSeq, len(content), logMsg)
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
			e.assistantWindowChanged()
		}
		if usageSettled && w.usageTracker != nil {
			w.usageTracker.Reconcile()
		}

		tickResult := w.behavior.Tick(time.Now(), sessionState)
		if tickResult.Log != "" {
			e.logf("%s session=%s", tickResult.Log, label)
		}
		if tickResult.State != "" && protocol.SessionState(tickResult.State) != sessionState {
			e.recordTranscriptEvidence(tickResult.State, "watcher tick", time.Now())
		}
		if tickResult.BlockClassification {
			continue
		}

		quietSince := w.behavior.QuietSince(lastAssistantAt)
		if assistantSeq > classifiedSeq &&
			!lastAssistantAt.IsZero() &&
			!quietSince.IsZero() &&
			time.Since(quietSince) >= transcriptQuietWindow {
			e.mu.Lock()
			current := e.state.State
			e.mu.Unlock()
			if skip, reason := w.behavior.SkipClassification(current, e.lastSeen(), time.Now()); skip {
				if strings.TrimSpace(reason) == "" {
					reason = "transcript watcher: skipping classification"
				}
				e.logf("%s session=%s state=%s", reason, label, current)
				continue
			}

			classifiedSeq = assistantSeq
			e.logf(
				"transcript watcher: quiet window reached session=%s seq=%d transcript=%s quiet_since=%s",
				label,
				assistantSeq,
				transcriptPath,
				quietSince.Format(time.RFC3339Nano),
			)
			path := transcriptPath
			e.registry.host.Go("classifySessionState", func() { e.ClassifyStop(path, StopClassification{}) })
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

func (e *Entry) boundConversation() ConversationObservation {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.conversationInput.ConversationObservation
}

func (e *Entry) claimedElsewhere(nativeID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.claimedNatives[nativeID]
}

func (e *Entry) ConversationClaimedElsewhere(nativeID string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.claimedNatives == nil {
		e.claimedNatives = make(map[string]bool)
	}
	e.claimedNatives[nativeID] = true
}

func (e *Entry) lastSeen() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.touchedAt.IsZero() {
		return ""
	}
	return string(protocol.NewTimestamp(e.touchedAt))
}

func (e *Entry) assistantWindowChanged() {
	e.mu.Lock()
	e.windowRevision++
	view := e.viewLocked()
	e.mu.Unlock()
	e.home.Applied(view)
}
