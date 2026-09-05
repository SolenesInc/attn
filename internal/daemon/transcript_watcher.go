package daemon

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

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

type transcriptWatcher struct {
	sessionID     string
	agent         protocol.SessionAgent
	cwd           string
	startedAt     time.Time
	preferredPath string
	behavior      agentdriver.TranscriptWatcherBehavior
	stopCh        chan struct{}
	doneCh        chan struct{}

	mu             sync.RWMutex
	status         protocol.SessionMessageWindowStatus
	detail         string
	transcriptPath string
	window         *transcript.AssistantWindow
	sessionState   protocol.SessionState
}

type assistantWindowSnapshot struct {
	Status   protocol.SessionMessageWindowStatus
	Messages []transcript.AssistantMessage
	Report   transcript.AssistantWindowReport
	Detail   string
}

func newTranscriptWatcher(sessionID string, agent protocol.SessionAgent, cwd string, startedAt time.Time, behavior agentdriver.TranscriptWatcherBehavior) *transcriptWatcher {
	return &transcriptWatcher{
		sessionID: sessionID,
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
		MaxMessages:     annotatableWindowMessages,
		MaxMessageChars: annotatableMessageMaxChars,
		MaxTotalChars:   annotatableWindowMaxChars,
	})
}

func (w *transcriptWatcher) snapshot() assistantWindowSnapshot {
	w.mu.RLock()
	defer w.mu.RUnlock()
	messages, report := w.window.Snapshot()
	return assistantWindowSnapshot{Status: w.status, Messages: messages, Report: report, Detail: w.detail}
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

func isTranscriptWatchedAgent(agent protocol.SessionAgent) bool {
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

func (d *Daemon) resolveExactTranscriptPathForWatcher(w *transcriptWatcher) string {
	binding := d.store.GetSessionConversation(w.sessionID)
	for _, path := range []string{strings.TrimSpace(w.preferredPath), binding.TranscriptPath} {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func (d *Daemon) findTranscriptForResume(agent protocol.SessionAgent, resumeID string) string {
	resumeID = strings.TrimSpace(resumeID)
	if resumeID == "" {
		return ""
	}
	if d.transcriptResumeLookup != nil {
		return strings.TrimSpace(d.transcriptResumeLookup(agent, resumeID))
	}
	driver := agentdriver.Get(string(agent))
	tf, ok := agentdriver.GetTranscriptFinder(driver)
	if !ok {
		return ""
	}
	return strings.TrimSpace(tf.FindTranscriptForResume(resumeID))
}

func (d *Daemon) discoverTranscriptForWatcher(w *transcriptWatcher) string {
	binding := d.store.GetSessionConversation(w.sessionID)
	path := d.findTranscriptForResume(w.agent, binding.NativeID)
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	if _, err := d.store.TransitionSessionConversation(w.sessionID, binding.NativeID, path); err != nil {
		d.logf("transcript watcher: persist discovered path failed session=%s path=%s err=%v", w.sessionID, path, err)
	}
	return path
}

func (d *Daemon) sessionHasBoundTranscriptPath(sessionID string) bool {
	return strings.TrimSpace(d.store.GetSessionTranscriptPath(sessionID)) != ""
}

func (d *Daemon) transcriptDiscoveryDeadline(w *transcriptWatcher, now time.Time) time.Time {
	if strings.TrimSpace(w.preferredPath) != "" || d.sessionHasBoundTranscriptPath(w.sessionID) {
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

func (d *Daemon) ensureTranscriptWatcherAtPath(sessionID string, transcriptPath string) {
	session := d.store.Get(sessionID)
	if session == nil || !isTranscriptWatchedAgent(session.Agent) {
		return
	}
	transcriptPath = strings.TrimSpace(transcriptPath)
	d.watchersMu.Lock()
	watcher := d.transcriptWatch[sessionID]
	current := watcher != nil && watcher.agent == session.Agent &&
		strings.TrimSpace(watcher.preferredPath) == transcriptPath && !watcherStopped(watcher)
	d.watchersMu.Unlock()
	if current {
		return
	}
	d.startTranscriptWatcherAtPath(session.ID, session.Agent, session.Directory, time.Now(), transcriptPath)
}

func (d *Daemon) transcriptBootstrapBytesForAgent(agent protocol.SessionAgent) int64 {
	driver := agentdriver.Get(string(agent))
	if tf, ok := agentdriver.GetTranscriptFinder(driver); ok {
		if n := tf.BootstrapBytes(); n > 0 {
			return n
		}
	}
	return 0
}

func (d *Daemon) startTranscriptWatcher(sessionID string, agent protocol.SessionAgent, cwd string, startedAt time.Time) {
	d.startTranscriptWatcherAtPath(sessionID, agent, cwd, startedAt, "")
}

func (d *Daemon) startTranscriptWatcherAtPath(sessionID string, agent protocol.SessionAgent, cwd string, startedAt time.Time, transcriptPath string) {
	if !isTranscriptWatchedAgent(agent) {
		return
	}

	driver := agentdriver.Get(string(agent))
	behavior, ok := agentdriver.GetTranscriptWatcherBehavior(driver)
	if !ok {
		return
	}
	d.watchersMu.Lock()
	session := d.lookupTranscriptWatcherSession(sessionID)
	if session == nil || session.Agent != agent {
		d.watchersMu.Unlock()
		return
	}

	watcher := newTranscriptWatcher(sessionID, agent, cwd, startedAt, behavior)
	watcher.preferredPath = strings.TrimSpace(transcriptPath)
	watcher.setState(session.State)

	if d.transcriptWatch == nil {
		d.transcriptWatch = make(map[string]*transcriptWatcher)
	}
	previous := d.transcriptWatch[sessionID]
	d.transcriptWatch[sessionID] = watcher
	d.watchersMu.Unlock()
	if previous != nil {
		close(previous.stopCh)
	}

	d.logf("transcript watcher: started session=%s agent=%s cwd=%s", sessionID, agent, cwd)
	go d.runTranscriptWatcher(watcher)
}

func (d *Daemon) lookupTranscriptWatcherSession(sessionID string) *protocol.Session {
	if d.transcriptWatcherSessionLookup != nil {
		return d.transcriptWatcherSessionLookup(sessionID)
	}
	return d.store.Get(sessionID)
}

func (d *Daemon) updateTranscriptWatcherState(sessionID string, state protocol.SessionState) {
	d.watchersMu.Lock()
	watcher := d.transcriptWatch[sessionID]
	d.watchersMu.Unlock()
	if watcher != nil {
		watcher.setState(state)
	}
}

func (d *Daemon) restoreTranscriptWatchers() {
	if d.store == nil || d.ptyBackend == nil {
		return
	}
	live := make(map[string]struct{})
	for _, id := range d.ptyBackend.SessionIDs(context.Background()) {
		live[id] = struct{}{}
	}
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
		d.startTranscriptWatcherAtPath(session.ID, session.Agent, session.Directory, time.Now(), binding.TranscriptPath)
	}
}

func (d *Daemon) applySessionUsageAvailability(w *transcriptWatcher, batch transcript.FollowBatch) error {
	if len(batch.Records) == 0 {
		return nil
	}
	if transcript.SupportsUsage(string(w.agent)) {
		return nil
	}
	changed := false
	for _, event := range batch.Events {
		if event.Kind != transcript.EventKindAssistant {
			continue
		}
		var err error
		changed, err = d.store.MarkSessionCostUsageUnavailable(w.sessionID, "")
		if err != nil {
			return err
		}
		break
	}
	if changed {
		d.publishFact(FactSessionCostChanged, w.sessionID, nil)
	}
	return nil
}

func (d *Daemon) stopTranscriptWatcher(sessionID string) {
	d.watchersMu.Lock()
	watcher, ok := d.transcriptWatch[sessionID]
	if ok {
		delete(d.transcriptWatch, sessionID)
	}
	d.watchersMu.Unlock()
	if ok {
		close(watcher.stopCh)
	}
}

func (d *Daemon) stopAllTranscriptWatchers() {
	d.watchersMu.Lock()
	watchers := make([]*transcriptWatcher, 0, len(d.transcriptWatch))
	for _, watcher := range d.transcriptWatch {
		watchers = append(watchers, watcher)
	}
	d.transcriptWatch = make(map[string]*transcriptWatcher)
	d.watchersMu.Unlock()

	for _, watcher := range watchers {
		close(watcher.stopCh)
	}
}

func (d *Daemon) assistantWindow(sessionID string, agent protocol.SessionAgent) (assistantWindowSnapshot, bool) {
	d.watchersMu.Lock()
	watcher := d.transcriptWatch[sessionID]
	d.watchersMu.Unlock()
	if watcher == nil || watcher.agent != agent {
		return assistantWindowSnapshot{}, false
	}
	return watcher.snapshot(), true
}

func (d *Daemon) liveTranscriptPath(sessionID string, agent protocol.SessionAgent) string {
	d.watchersMu.Lock()
	watcher := d.transcriptWatch[sessionID]
	d.watchersMu.Unlock()
	if watcher == nil || watcher.agent != agent {
		return ""
	}
	return watcher.exactPath()
}

func (d *Daemon) runTranscriptWatcher(w *transcriptWatcher) {
	defer close(w.doneCh)

	if w.behavior == nil {
		d.logf("transcript watcher: no behavior configured session=%s agent=%s", w.sessionID, w.agent)
		return
	}

	ticker := time.NewTicker(transcriptPollInterval)
	defer ticker.Stop()

	var (
		transcriptPath string
		follower       *transcript.Follower
		usageTracker   *sessionUsageTracker
		readFileInfo   os.FileInfo

		lastAssistantAt time.Time
		lastAssistant   string
		assistantSeq    int64
		classifiedSeq   int64

		discoveryDeadline = d.transcriptDiscoveryDeadline(w, time.Now())
		fallbackAttempted bool
		usageState        = w.state()
	)

	for {
		select {
		case <-w.stopCh:
			if usageTracker != nil {
				usageTracker.Reconcile()
			}
			d.logf("transcript watcher: stopped session=%s", w.sessionID)
			return
		case <-ticker.C:
		}

		sessionState := w.state()
		usageSettled := usageState == protocol.SessionStateWorking && sessionState != protocol.SessionStateWorking
		usageState = sessionState
		windowChanged := false

		if transcriptPath == "" {
			transcriptPath = d.resolveExactTranscriptPathForWatcher(w)
			if transcriptPath == "" && time.Now().Before(discoveryDeadline) {
				continue
			}
			if transcriptPath == "" && !fallbackAttempted {
				fallbackAttempted = true
				transcriptPath = d.discoverTranscriptForWatcher(w)
			}
			if transcriptPath == "" {
				if w.setStatus(protocol.SessionMessageWindowStatusUnavailable, "no exact transcript is bound to this live session") {
					d.publishFact(FactSessionAssistantWindowChanged, w.sessionID, nil)
				}
				d.logf("transcript watcher: exact transcript unavailable session=%s agent=%s cwd=%s", w.sessionID, w.agent, w.cwd)
				return
			}
			info, err := os.Stat(transcriptPath)
			if err != nil {
				d.logf("transcript watcher: transcript stat failed session=%s path=%s err=%v", w.sessionID, transcriptPath, err)
				w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript could not be opened", false)
				d.publishFact(FactSessionAssistantWindowChanged, w.sessionID, nil)
				return
			}
			startOffset := info.Size()
			bootstrapBytes := d.transcriptBootstrapBytesForAgent(w.agent)
			if bootstrapBytes > 0 && info.Size() > bootstrapBytes {
				startOffset = info.Size() - bootstrapBytes
			} else if bootstrapBytes > 0 {
				startOffset = 0
			}
			follower, err = transcript.NewFollower(transcriptPath, string(w.agent), startOffset)
			if err != nil {
				d.logf("transcript watcher: follower init failed session=%s path=%s err=%v", w.sessionID, transcriptPath, err)
				w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript could not be read", false)
				d.publishFact(FactSessionAssistantWindowChanged, w.sessionID, nil)
				return
			}
			w.behavior.Reset()
			w.resetSource(protocol.SessionMessageWindowStatusReady, transcriptPath, "", startOffset > 0)
			windowChanged = true
			fallbackAttempted = false
			usageTracker = d.newSessionUsageTracker(w, transcriptPath)
			d.logf("transcript watcher: transcript discovered session=%s path=%s offset=%d", w.sessionID, transcriptPath, startOffset)
		}

		info, err := os.Stat(transcriptPath)
		if err != nil {
			d.logf("transcript watcher: transcript unavailable, rediscovering session=%s path=%s err=%v", w.sessionID, transcriptPath, err)
			w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript became unavailable", false)
			d.publishFact(FactSessionAssistantWindowChanged, w.sessionID, nil)
			transcriptPath = ""
			follower = nil
			usageTracker = nil
			readFileInfo = nil
			discoveryDeadline = d.transcriptDiscoveryDeadline(w, time.Now())
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
				d.logf("transcript watcher: transcript replaced session=%s path=%s", w.sessionID, transcriptPath)
				w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript was replaced", false)
				d.publishFact(FactSessionAssistantWindowChanged, w.sessionID, nil)
				return
			}
			if readErr != nil {
				d.logf("transcript watcher: read delta error session=%s path=%s err=%v", w.sessionID, transcriptPath, readErr)
				w.resetSource(protocol.SessionMessageWindowStatusUnavailable, "", "the exact live transcript could not be read", false)
				d.publishFact(FactSessionAssistantWindowChanged, w.sessionID, nil)
				return
			}

			for _, record := range batch.Records {
				if len(record.Raw) == 0 {
					continue
				}
				now := time.Now()

				lineResult := w.behavior.HandleLine(record.Raw, now, sessionState)
				if lineResult.Log != "" {
					d.logf("%s session=%s", lineResult.Log, w.sessionID)
				}
				if lineResult.Aborted {
					if skip, reason := staleTranscriptAbort(lineResult.AbortAt, w.startedAt); skip {
						d.logf("transcript watcher: ignoring turn abort session=%s reason=%s abort_at=%s", w.sessionID, reason, lineResult.AbortAt.Format(time.RFC3339Nano))
					} else {
						d.recordTurnAbortedEvidence(w.sessionID, lineResult.AbortDetail, lineResult.AbortAt, now)
						classifiedSeq = assistantSeq
					}
				}
				if lineResult.BracketClosed {
					d.recordTurnBracketClosedEvidence(w.sessionID, now)
				}
				if lineResult.State != "" && protocol.SessionState(lineResult.State) != sessionState {
					d.recordTranscriptEvidence(w.sessionID, lineResult.State, "transcript line", now)
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
					d.logf("transcript watcher: assistant event session=%s seq=%d chars=%d preview=%q", w.sessionID, assistantSeq, len(content), logMsg)
				}
			}
			windowChanged = w.applyEvents(batch.Events) || windowChanged
			if err := d.applySessionUsageAvailability(w, batch); err != nil {
				d.logf("transcript watcher: usage availability persist failed session=%s err=%v", w.sessionID, err)
			}
			if usageTracker != nil {
				usageTracker.Reconcile()
			}
			readFileInfo = info
		}
		if windowChanged {
			d.publishFact(FactSessionAssistantWindowChanged, w.sessionID, nil)
		}
		if usageSettled && usageTracker != nil {
			usageTracker.Reconcile()
		}

		tickResult := w.behavior.Tick(time.Now(), sessionState)
		if tickResult.Log != "" {
			d.logf("%s session=%s", tickResult.Log, w.sessionID)
		}
		if tickResult.State != "" && protocol.SessionState(tickResult.State) != sessionState {
			d.recordTranscriptEvidence(w.sessionID, tickResult.State, "watcher tick", time.Now())
		}
		if tickResult.BlockClassification {
			continue
		}

		quietSince := w.behavior.QuietSince(lastAssistantAt)
		if assistantSeq > classifiedSeq &&
			!lastAssistantAt.IsZero() &&
			!quietSince.IsZero() &&
			time.Since(quietSince) >= transcriptQuietWindow {
			if current := d.store.Get(w.sessionID); current != nil {
				if skip, reason := w.behavior.SkipClassification(current.State, current.LastSeen, time.Now()); skip {
					if strings.TrimSpace(reason) == "" {
						reason = "transcript watcher: skipping classification"
					}
					d.logf("%s session=%s state=%s", reason, w.sessionID, current.State)
					continue
				}
			}

			classifiedSeq = assistantSeq
			d.logf(
				"transcript watcher: quiet window reached session=%s seq=%d transcript=%s quiet_since=%s",
				w.sessionID,
				assistantSeq,
				transcriptPath,
				quietSince.Format(time.RFC3339Nano),
			)
			go d.classifySessionState(w.sessionID, transcriptPath)
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
