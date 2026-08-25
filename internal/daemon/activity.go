package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/activity"
	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

// sessionActivityTimeout bounds one generation. A tripwire: measured ~5s on
// Codex, ~12s on Claude, worst observed ~19s.
const sessionActivityTimeout = time.Minute

// sessionActivityBudgetUSD caps one run, two orders of magnitude above the measured
// cost ($0.0027 on Codex, $0.011-0.017 on Claude), so only a runaway touches it.
const sessionActivityBudgetUSD = "0.50"

const sessionActivityConcurrency = 3

// activityMaxTurns is 2, not 1: a headless run that exhausts its turn budget
// exits non-zero even when it already produced the text.
const activityMaxTurns = 2

const (
	sessionActivityScanKind     = "session_activity_scan"
	sessionActivityScanInterval = 30 * time.Second
	sessionActivityScanTimeout  = 30 * time.Second
)

type sessionActivityRun struct {
	ObservedAt time.Time
	SpentAt    time.Time
	Err        string
	Transcript string
	ResumeID   string
}

func (d *Daemon) sessionActivityRunRecord(sessionID string) sessionActivityRun {
	d.sessionActivityRunsMu.Lock()
	defer d.sessionActivityRunsMu.Unlock()
	return d.sessionActivityRuns[sessionID]
}

func (d *Daemon) noteSessionActivityRun(sessionID string, mutate func(*sessionActivityRun)) {
	d.sessionActivityRunsMu.Lock()
	defer d.sessionActivityRunsMu.Unlock()
	if d.sessionActivityRuns == nil {
		d.sessionActivityRuns = make(map[string]sessionActivityRun)
	}
	record := d.sessionActivityRuns[sessionID]
	mutate(&record)
	d.sessionActivityRuns[sessionID] = record
}

func (d *Daemon) forgetSessionActivityRuns(live map[string]struct{}) {
	d.sessionActivityRunsMu.Lock()
	defer d.sessionActivityRunsMu.Unlock()
	for id := range d.sessionActivityRuns {
		if _, ok := live[id]; !ok {
			delete(d.sessionActivityRuns, id)
		}
	}
}

func latest(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// Interval and transcript movement are measured against the last PASS, not the last
// stored line: against the line, a failing generation is invisible and re-runs every tick.
func (d *Daemon) sessionActivityScanHandler(context.Context, *jobs.Job) (any, error) {
	if !d.activityEnabled() {
		return nil, nil
	}
	tier := d.PresenceTier()
	interval := d.activityInterval(tier)
	if interval <= 0 {
		return nil, nil
	}
	if _, err := d.activityConfigured(); err != nil {
		d.logf("session_activity: enabled but not runnable: %v", err)
		return nil, nil
	}

	now := time.Now()
	live := make(map[string]struct{})
	for _, session := range d.store.List("") {
		live[session.ID] = struct{}{}
		if !sessionGeneratesActivity(session) {
			continue
		}
		stored := d.store.GetSessionActivity(session.ID)
		run := d.sessionActivityRunRecord(session.ID)
		spent := latest(stored.At, run.SpentAt)
		if !spent.IsZero() && now.Sub(spent) < interval {
			continue
		}
		if !d.transcriptMovedSince(session, latest(stored.At, run.ObservedAt)) {
			continue
		}
		d.enqueueSessionActivity(session.ID)
	}
	d.forgetSessionActivityRuns(live)
	return nil, nil
}

func (d *Daemon) transcriptMovedSince(session *protocol.Session, since time.Time) bool {
	if since.IsZero() {
		return true
	}
	path := d.sessionActivityTranscript(session)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.ModTime().After(since)
}

// Caches the resolved path across ticks: resolving it on Codex measured 235-489ms per
// session. A resume under a new id writes a new file, hence the remembered resume id.
func (d *Daemon) sessionActivityTranscript(session *protocol.Session) string {
	if session == nil {
		return ""
	}
	resumeID := strings.TrimSpace(d.store.GetResumeSessionID(session.ID))
	if cached := d.sessionActivityRunRecord(session.ID); cached.Transcript != "" && cached.ResumeID == resumeID {
		if _, err := os.Stat(cached.Transcript); err == nil {
			return cached.Transcript
		}
	}
	path := strings.TrimSpace(d.resolveTranscriptPathForSession(session, ""))
	d.noteSessionActivityRun(session.ID, func(run *sessionActivityRun) {
		run.Transcript = path
		run.ResumeID = resumeID
	})
	return path
}

type sessionActivityPayload struct {
	Transcript string `json:"transcript,omitempty"`
	ResumeID   string `json:"resume_id,omitempty"`
}

func (d *Daemon) enqueueSessionActivity(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if !d.activityEnabled() {
		return
	}
	if d.PresenceTier() == PresenceAway {
		return
	}
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return
	}
	session := d.store.Get(sessionID)
	if session == nil || !sessionGeneratesActivity(session) {
		return
	}
	resumeID := strings.TrimSpace(d.store.GetResumeSessionID(sessionID))
	transcriptPath := d.sessionActivityTranscript(session)
	if transcriptPath == "" {
		return
	}
	if _, err := runner.Enqueue(sessionActivityKind, jobs.EnqueueOptions{
		UniqueKey: sessionID,
		Payload: sessionActivityPayload{
			Transcript: transcriptPath,
			ResumeID:   resumeID,
		},
	}); err != nil {
		d.logf("session_activity: enqueue %s: %v", sessionID, err)
	}
}

func sessionGeneratesActivity(session *protocol.Session) bool {
	if session == nil {
		return false
	}
	if session.ParentSessionID != nil && strings.TrimSpace(*session.ParentSessionID) != "" {
		return false
	}
	if session.EndpointID != nil && strings.TrimSpace(*session.EndpointID) != "" {
		return false
	}
	driver := agentdriver.Get(string(session.Agent))
	if driver == nil {
		return false
	}
	_, isFinder := agentdriver.GetTranscriptFinder(driver)
	return isFinder
}

func (d *Daemon) sessionActivityHandler(ctx context.Context, job *jobs.Job) (any, error) {
	if !d.activityEnabled() || d.PresenceTier() == PresenceAway {
		return nil, nil
	}

	sessionID := strings.TrimSpace(jobSubject(job))
	if sessionID == "" {
		return nil, errors.New("session_activity requires a session id")
	}
	config, err := d.activityConfigured()
	if err != nil {
		return nil, err
	}

	session := d.store.Get(sessionID)
	if session == nil {
		return nil, nil
	}
	d.noteSessionActivityRun(sessionID, func(run *sessionActivityRun) {
		run.ObservedAt = time.Now()
	})

	var carried sessionActivityPayload
	if err := job.DecodePayload(&carried); err != nil {
		return nil, err
	}
	resumeID := strings.TrimSpace(carried.ResumeID)
	transcriptPath := strings.TrimSpace(carried.Transcript)
	if transcriptPath == "" {
		transcriptPath = d.sessionActivityTranscript(session)
	}
	if transcriptPath == "" {
		return nil, nil
	}

	stored := d.store.GetSessionActivity(sessionID)
	// Cold start, checked before the read: reading from byte 0 succeeds, which is
	// the problem — a full scan summarizing history as if it were now.
	if stored.Cursor == "" {
		return nil, d.reseedSessionActivity(sessionID, resumeID, transcriptPath)
	}
	window, err := activity.Read(transcriptPath, string(session.Agent), stored.Cursor)
	switch {
	case err == nil:
	case errors.Is(err, transcript.ErrCursorMismatch) ||
		errors.Is(err, transcript.ErrCursorPastEnd) ||
		errors.Is(err, transcript.ErrInvalidCursor) ||
		errors.Is(err, activity.ErrDeltaTooLarge):
		return nil, d.reseedSessionActivity(sessionID, resumeID, transcriptPath)
	default:
		return nil, fmt.Errorf("session_activity: read %s: %w", transcriptPath, err)
	}

	if window.Empty() {
		return nil, d.advanceSessionActivityCursor(sessionID, resumeID, stored, window.NextCursor)
	}

	prompt := activity.Baseline().Render(activity.Input{
		State:       string(session.State),
		StateReason: protocol.Deref(session.StateReason),
		Window:      window.Render(),
		Previous:    stored.Line,
	})

	provider, executablePath, err := d.resolveActivityExecutable(config)
	if err != nil {
		return nil, err
	}
	workDir, err := headlessScratchCwd()
	if err != nil {
		return nil, fmt.Errorf("session_activity: resolve scratch cwd: %w", err)
	}

	run := d.sessionActivityExecution
	if run == nil {
		run = func(ctx context.Context, p agentdriver.HeadlessTaskProvider, r agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
			return p.RunHeadlessTask(ctx, r)
		}
	}
	// Stamped before the call: a hanging run would otherwise retry every tick.
	d.noteSessionActivityRun(sessionID, func(record *sessionActivityRun) {
		record.SpentAt = time.Now()
	})
	result, err := run(ctx, provider, agentdriver.HeadlessTaskRequest{
		Executable:      executablePath,
		Model:           config.Model,
		ReasoningEffort: config.Effort,
		Prompt:          prompt.User,
		// Replaces the CLI's own interactive-coding system prompt. Measured: the
		// billed prefix drops from 46,745 tokens to 33,955.
		SystemPrompt: prompt.System,
		WorkDir:      workDir,
		// No OutputSchema: the answer IS the final text, and Codex's tool-free path has none.
		// MaxTurns/MaxBudgetUSD are Claude-only; DisableTools plus the ctx timeout bound both.
		DisableTools: true,
		MaxTurns:     activityMaxTurns,
		MaxBudgetUSD: sessionActivityBudgetUSD,
	})
	if err != nil {
		d.noteSessionActivityRun(sessionID, func(record *sessionActivityRun) {
			record.Err = err.Error()
		})
		return nil, fmt.Errorf("session_activity: run agent: %w (%s)", err, result.Diagnostics)
	}

	line, ok := activity.Sanitize(result.Text)
	if !ok {
		d.logf("session_activity: session=%s produced no usable line (%s)", sessionID, result.Diagnostics)
		d.noteSessionActivityRun(sessionID, func(record *sessionActivityRun) {
			record.Err = "the agent answered with nothing usable"
		})
		return nil, d.advanceSessionActivityCursor(sessionID, resumeID, stored, window.NextCursor)
	}
	d.noteSessionActivityRun(sessionID, func(record *sessionActivityRun) { record.Err = "" })

	if note := window.Report.String(); note != "" {
		d.logf("session_activity: session=%s window truncated: %s", sessionID, note)
	}
	if !d.activityEnabled() {
		return nil, nil
	}
	if !d.store.UpdateSessionActivityForConversation(sessionID, resumeID, line, time.Now(), window.NextCursor) {
		return nil, nil
	}
	d.publishFact(FactSessionActivityChanged, sessionID, nil)
	d.logf("session_activity: session=%s agent=%s model=%s line=%q", sessionID, config.Agent, config.Model, line)
	return nil, nil
}

func (d *Daemon) reseedSessionActivity(sessionID, resumeID, transcriptPath string) error {
	head, err := activity.SeedCursor(transcriptPath)
	if err != nil {
		return fmt.Errorf("session_activity: seed cursor for %s: %w", transcriptPath, err)
	}
	d.store.SetSessionActivityCursorForConversation(sessionID, resumeID, head)
	return nil
}

// An empty cursor here would silently clear the line.
func (d *Daemon) advanceSessionActivityCursor(sessionID, resumeID string, stored store.SessionActivity, next string) error {
	if next == "" || next == stored.Cursor {
		return nil
	}
	d.store.SetSessionActivityCursorForConversation(sessionID, resumeID, next)
	return nil
}

func (d *Daemon) clearAllSessionActivity() {
	for _, session := range d.store.List("") {
		if d.store.GetSessionActivity(session.ID).Line == "" {
			continue
		}
		d.store.UpdateSessionActivity(session.ID, "", time.Time{}, "")
		d.publishFact(FactSessionActivityChanged, session.ID, nil)
	}
}

func (d *Daemon) handleActivityStatus(conn net.Conn, _ *protocol.ActivityStatusMessage) {
	result := protocol.ActivityStatusResult{
		PresenceTier: d.PresenceTier().String(),
		Enabled:      d.activityEnabled(),
		Sessions:     []protocol.ActivityStatusSession{},
	}
	if _, err := d.activityConfigured(); err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	for _, session := range d.store.List("") {
		if !sessionGeneratesActivity(session) {
			continue
		}
		entry := protocol.ActivityStatusSession{ID: session.ID, Label: session.Label}
		if run := d.sessionActivityRunRecord(session.ID); run.Err != "" {
			entry.Error = protocol.Ptr(run.Err)
		}
		if stored := d.store.GetSessionActivity(session.ID); stored.Line != "" {
			entry.Activity = protocol.Ptr(stored.Line)
			if !stored.At.IsZero() {
				entry.ActivityAt = protocol.Ptr(string(protocol.NewTimestamp(stored.At)))
			}
		}
		result.Sessions = append(result.Sessions, entry)
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, ActivityStatusResult: &result})
}

func (d *Daemon) handleClearSessionActivity(conn net.Conn, msg *protocol.ClearSessionActivityMessage) {
	sessionID := strings.TrimSpace(msg.ID)
	if sessionID == "" {
		d.sendError(conn, "clear session activity: id is required")
		return
	}
	if !d.store.UpdateSessionActivity(sessionID, "", time.Time{}, "") {
		d.sendError(conn, "clear session activity: session not found: "+sessionID)
		return
	}
	d.publishFact(FactSessionActivityChanged, sessionID, nil)
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true})
}
