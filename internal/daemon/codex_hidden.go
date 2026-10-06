package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/transcript"
)

// Control-connection turn reports arrive in order, so no driver run fences them.
const codexLinkEpochPrefix = "codex-app-server:"

func (r *codexShared) launchedShared(sessionID protocol.SessionID) bool {
	intent, ok := r.d.store.LaunchIntent(sessionID)
	return ok && intent.CodexShared
}

func (r *codexShared) conversation(sessionID protocol.SessionID) string {
	if !r.launchedShared(sessionID) {
		return ""
	}
	return r.d.store.GetSessionConversation(sessionID).NativeID
}

func (r *codexShared) hidden(sessionID protocol.SessionID) bool {
	if len(r.d.terminals().Of(harness.SessionID(sessionID))) > 0 {
		return false
	}
	return r.d.store.Get(sessionID) != nil && r.conversation(sessionID) != ""
}

func (r *codexShared) keepsWhenLeft(sessionID protocol.SessionID) bool {
	return r.conversation(sessionID) != ""
}

func (r *codexShared) holder(profile, conversation string) protocol.SessionID {
	sessionID := r.d.store.OpenSessionHolding(profile, conversation)
	if sessionID == "" || !r.launchedShared(sessionID) {
		return ""
	}
	return sessionID
}

func (d *Daemon) decorateSessionHidden(clone *protocol.Session) {
	clone.Hidden = nil
	if clone.Agent == protocol.SessionAgentCodex && d.codexShared().hidden(clone.ID) {
		clone.Hidden = protocol.Ptr(true)
	}
}

func (d *Daemon) decorateLedgerEntriesHidden(entries []protocol.SessionLedgerEntry) {
	for i := range entries {
		d.decorateLedgerEntryHidden(&entries[i])
	}
}

func (d *Daemon) decorateLedgerEntryHidden(entry *protocol.SessionLedgerEntry) {
	if protocol.Deref(entry.ClosedAt) == "" && entry.Agent == string(protocol.SessionAgentCodex) && d.codexShared().hidden(entry.ID) {
		entry.Hidden = protocol.Ptr(true)
	}
}

func (r *codexShared) shownThread(sessionID protocol.SessionID) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.d.terminals().Of(harness.SessionID(sessionID)) {
		if v := r.views[t]; v != nil && v.thread != "" {
			return v.thread
		}
	}
	return ""
}

func (r *codexShared) control(ctx context.Context, profile string) (*codexServer, *codexshared.Client, error) {
	s, err := r.ensureServer(ctx, profile, "")
	if err != nil {
		return nil, nil, err
	}
	client := s.client()
	if client == nil {
		return nil, nil, fmt.Errorf("the shared Codex app-server of profile %s dropped its connection", profile)
	}
	return s, client, nil
}

type codexLink struct{ r *codexShared }

func (codexLink) Voices() []harness.Voice {
	return []harness.Voice{harness.VoiceUser, harness.VoiceAttn}
}

// Deliver runs on its own deadline and ignores ctx, so daemon shutdown does not cut a delivery short.
func (l codexLink) Deliver(_ context.Context, in harness.Input) harness.Custody {
	r := l.r
	session := r.d.store.Get(in.Session)
	if session == nil {
		return harness.Custody{Reason: fmt.Sprintf("session %s is gone", in.Session)}
	}
	conversation := r.d.store.GetSessionConversation(session.ID).NativeID
	if conversation == "" {
		conversation = r.shownThread(session.ID)
	}
	if conversation == "" {
		return harness.Custody{Reason: fmt.Sprintf("shared Codex session %s shows no conversation yet", session.ID)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexServerStartLimit)
	defer cancel()
	_, client, err := r.control(ctx, session.ProfileID)
	if err == nil {
		err = r.startTurn(ctx, client, conversation, in.Text)
	}
	if err != nil {
		return harness.Custody{Reason: fmt.Sprintf("deliver to shared Codex conversation %s: %v", conversation, err)}
	}
	return harness.Custody{Taken: true, At: time.Now()}
}

func (r *codexShared) startTurn(ctx context.Context, client *codexshared.Client, conversation, text string) error {
	call := func(method string, params, result any) error {
		callCtx, cancel := context.WithTimeout(ctx, codexServerCallLimit)
		defer cancel()
		raw, err := client.Call(callCtx, method, params)
		if err != nil || result == nil {
			return err
		}
		return json.Unmarshal(raw, result)
	}
	var resumed struct {
		Thread struct {
			Status codexThreadStatus `json:"status"`
		} `json:"thread"`
	}
	if err := call("thread/resume", map[string]any{"threadId": conversation, "excludeTurns": true}, &resumed); err != nil {
		return err
	}
	defer func() {
		_ = call("thread/unsubscribe", map[string]any{"threadId": conversation}, nil)
	}()
	params := map[string]any{"threadId": conversation, "input": []any{map[string]any{"type": "text", "text": text, "text_elements": []any{}}}}
	if resumed.Thread.Status.Type == "active" {
		var turns struct {
			Data []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"data"`
		}
		if err := call("thread/turns/list", map[string]any{"threadId": conversation, "limit": 1, "sortDirection": "desc"}, &turns); err != nil {
			return err
		}
		if len(turns.Data) > 0 && turns.Data[0].Status == "inProgress" {
			params["expectedTurnId"] = turns.Data[0].ID
			return call("turn/steer", params, nil)
		}
	}
	return call("turn/start", params, nil)
}

type codexThreadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags"`
}

func (st codexThreadStatus) turn() (harness.Turn, bool) {
	switch st.Type {
	case "idle", "notLoaded":
		return harness.TurnEnded, true
	case "active":
		switch {
		case slices.Contains(st.ActiveFlags, "waitingOnApproval"):
			return harness.TurnApproval, true
		case slices.Contains(st.ActiveFlags, "waitingOnUserInput"):
			return harness.TurnQuestion, true
		}
		return harness.TurnRunning, true
	}
	return harness.TurnUnknown, false
}

// Notifications run in arrival order, off the connection's reader.
type codexEvents struct {
	mu      sync.Mutex
	queue   []func()
	running bool
}

func (q *codexEvents) run(d *Daemon, f func()) {
	q.mu.Lock()
	q.queue = append(q.queue, f)
	if q.running {
		q.mu.Unlock()
		return
	}
	q.running = true
	q.mu.Unlock()
	drain := func() {
		for {
			q.mu.Lock()
			if len(q.queue) == 0 {
				q.running = false
				q.mu.Unlock()
				return
			}
			next := q.queue[0]
			q.queue = q.queue[1:]
			q.mu.Unlock()
			next()
		}
	}
	if !d.life.Go("codexServerEvents", drain) {
		q.mu.Lock()
		q.queue, q.running = nil, false
		q.mu.Unlock()
	}
}

func (r *codexShared) report(s *codexServer, sessionID protocol.SessionID, turn harness.Turn, restated bool) {
	s.mu.Lock()
	epoch := s.epoch
	s.mu.Unlock()
	r.d.linkEvents().Turn(harness.SessionID(sessionID), time.Now(), harness.TurnEvent{Turn: turn, Epoch: epoch, Seq: s.seq.Add(1), Restated: restated})
}

func (r *codexShared) observeStatus(s *codexServer, m codexshared.Message) {
	var p struct {
		ThreadID string            `json:"threadId"`
		Status   codexThreadStatus `json:"status"`
	}
	if json.Unmarshal(m.Params, &p) != nil || p.ThreadID == "" {
		return
	}
	s.events.run(r.d, func() {
		sessionID := r.holder(s.profile, p.ThreadID)
		session := r.d.store.Get(sessionID)
		if session == nil {
			return
		}
		turn, _ := p.Status.turn()
		blocked := session.State == protocol.SessionStatePendingApproval || session.State == protocol.SessionStateWaitingInput
		switch {
		case turn == harness.TurnApproval && session.State != protocol.SessionStatePendingApproval,
			turn == harness.TurnQuestion && session.State != protocol.SessionStateWaitingInput,
			turn == harness.TurnRunning && blocked:
			r.report(s, sessionID, turn, false)
		}
	})
}

func (r *codexShared) restateHiddenStates(s *codexServer, client *codexshared.Client) {
	for _, session := range r.d.store.List("") {
		stale := session.State == protocol.SessionStateUnknown || session.State == protocol.SessionStateWorking ||
			session.State == protocol.SessionStatePendingApproval
		if session.ProfileID != s.profile || !stale || !r.hidden(session.ID) {
			continue
		}
		ctx, cancel := context.WithTimeout(r.d.life.Context(), codexServerCallLimit)
		raw, err := client.Call(ctx, "thread/read", map[string]any{"threadId": r.conversation(session.ID)})
		cancel()
		var read struct {
			Thread struct {
				Status codexThreadStatus `json:"status"`
			} `json:"thread"`
		}
		if err != nil || json.Unmarshal(raw, &read) != nil {
			r.d.logf("shared Codex: restating session %s: %v", session.ID, err)
			continue
		}
		if turn, ok := read.Thread.Status.turn(); ok {
			r.report(s, session.ID, turn, true)
		}
	}
}

// The rollout moves to archived_sessions; read its usage to the end after archiving.
func (r *codexShared) archive(sessionID protocol.SessionID) {
	conversation := r.conversation(sessionID)
	session := r.d.store.Get(sessionID)
	if conversation == "" || session == nil {
		return
	}
	defer r.d.drainTranscriptWatcher(sessionID)()
	defer r.settleUsage(sessionID)
	if other := r.d.store.OtherOpenSessionHolding(conversation, sessionID); other != "" {
		r.d.logf("shared Codex: conversation %s of closed session %s stays unarchived: session %s still holds it", conversation, sessionID, other)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexServerStartLimit)
	defer cancel()
	_, client, err := r.control(ctx, session.ProfileID)
	if err == nil {
		callCtx, cancelCall := context.WithTimeout(ctx, codexServerCallLimit)
		_, err = client.Call(callCtx, "thread/archive", map[string]any{"threadId": conversation})
		cancelCall()
	}
	if err != nil {
		r.d.logf("shared Codex: conversation %s of closed session %s stays unarchived: %v", conversation, sessionID, err)
	}
}

// A close can come before the watcher's first read.
func (r *codexShared) settleUsage(sessionID protocol.SessionID) {
	path := r.d.store.GetSessionConversation(sessionID).TranscriptPath
	if path == "" {
		return
	}
	w := &transcriptWatcher{sessionID: sessionID, agent: protocol.SessionAgentCodex}
	if tracker := r.d.newSessionUsageTracker(w, path); tracker != nil {
		tracker.Reconcile()
	}
}

func (r *codexShared) archivedByClose(sessionID protocol.SessionID, conversation string) bool {
	return conversation != "" && r.launchedShared(sessionID) && transcript.FindArchivedCodexTranscript(conversation) != ""
}

func (r *codexShared) unarchive(profile, conversation string) error {
	if transcript.FindArchivedCodexTranscript(conversation) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexServerStartLimit)
	defer cancel()
	_, client, err := r.control(ctx, profile)
	if err != nil {
		return err
	}
	callCtx, cancelCall := context.WithTimeout(ctx, codexServerCallLimit)
	defer cancelCall()
	if _, err := client.Call(callCtx, "thread/unarchive", map[string]any{"threadId": conversation}); err != nil {
		return fmt.Errorf("unarchive Codex conversation %s: %w", conversation, err)
	}
	return nil
}

func (r *codexShared) showSession(sessionID protocol.SessionID) error {
	session := r.d.store.Get(sessionID)
	intent, ok := r.d.store.LaunchIntent(sessionID)
	conversation := r.conversation(sessionID)
	if session == nil || !ok || conversation == "" {
		return fmt.Errorf("session %s holds no shared Codex conversation to show", sessionID)
	}
	spawn, policy := buildStoredIntentSpawn(session, intent, 80, 24)
	spawn.ResumeSessionID = protocol.Ptr(conversation)
	policy.launchPlacement = &launchPlacement{direction: layouttree.DirectionVertical}
	policy.userStarted = true
	client := newInternalWSClient()
	r.d.handleSpawnSessionWithPolicy(client, spawn, policy)
	if _, err := readInternalActionResult(client); err != nil {
		return fmt.Errorf("show session %s: %w", sessionID, err)
	}
	return nil
}

func (r *codexShared) movedOn(sessionID protocol.SessionID, t harness.TerminalID) bool {
	r.mu.Lock()
	v := r.views[t]
	thread := ""
	if v != nil {
		thread = v.thread
	}
	r.mu.Unlock()
	if thread == "" {
		return false
	}
	conversation := r.conversation(sessionID)
	return conversation != "" && conversation != thread
}

func (d *Daemon) hide(sessionID protocol.SessionID, t harness.TerminalID) {
	lifecycle := d.sessionLifecycleLockFor(sessionID)
	lifecycle.Lock()
	unlock := d.lockTerminalEnds(sessionID)
	if err := d.ptyBackend.Kill(context.Background(), t, syscall.SIGTERM); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
		d.logf("stopping terminal %s: %v", t, err)
	}
	d.dropTerminal(t)
	unlock()
	lifecycle.Unlock()
	d.publishFact(FactSessionReregistered, string(sessionID), nil)
}

func (r *codexShared) observeName(s *codexServer, m codexshared.Message) {
	var p struct {
		ThreadID   string  `json:"threadId"`
		ThreadName *string `json:"threadName"`
	}
	if json.Unmarshal(m.Params, &p) != nil || p.ThreadID == "" || p.ThreadName == nil {
		return
	}
	name := strings.TrimSpace(*p.ThreadName)
	s.events.run(r.d, func() {
		sessionID := r.holder(s.profile, p.ThreadID)
		session := r.d.store.Get(sessionID)
		if session == nil || name == "" || session.Label == name {
			return
		}
		r.d.store.UpdateSessionLabel(sessionID, name)
		r.d.publishFact(FactSessionRenamed, string(sessionID), nil)
	})
}

// attn's label is the one that counts.
func (r *codexShared) mirrorName(sessionID protocol.SessionID, label string) {
	conversation := r.conversation(sessionID)
	session := r.d.store.Get(sessionID)
	if conversation == "" || session == nil {
		return
	}
	r.mu.Lock()
	s := r.servers[session.ProfileID]
	r.mu.Unlock()
	if s == nil {
		return
	}
	s.events.run(r.d, func() {
		client := s.client()
		if client == nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.d.life.Context(), codexServerCallLimit)
		defer cancel()
		if _, err := client.Call(ctx, "thread/name/set", map[string]any{"threadId": conversation, "name": label}); err != nil {
			r.d.logf("shared Codex: naming conversation %s %q: %v", conversation, label, err)
		}
	})
}

func (r *codexShared) stopServerIfUnused(profile string) {
	r.mu.Lock()
	s := r.servers[profile]
	r.mu.Unlock()
	if s == nil {
		return
	}
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	if r.needed(profile) || !r.serverRunning(context.Background(), s.terminal) {
		return
	}
	if client := s.client(); client != nil {
		client.Close()
	}
	if err := r.d.ptyBackend.Kill(context.Background(), s.terminal, syscall.SIGTERM); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
		r.d.logf("shared Codex: stopping the idle app-server of profile %s: %v", profile, err)
		return
	}
	r.d.logf("shared Codex: stopped the app-server of profile %s; nothing uses it", profile)
	deadline := time.Now().Add(codexServerCallLimit)
	for r.serverRunning(context.Background(), s.terminal) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
}

func (r *codexShared) needed(profile string) bool {
	r.mu.Lock()
	for _, v := range r.views {
		if v.profile == profile {
			r.mu.Unlock()
			return true
		}
	}
	r.mu.Unlock()
	for _, session := range r.d.store.List("") {
		if session.ProfileID == profile && session.Agent == protocol.SessionAgentCodex && r.launchedShared(session.ID) {
			return true
		}
	}
	return false
}

func (r *codexShared) loadedElsewhere(profile, conversation string) string {
	r.mu.Lock()
	servers := make(map[string]*codexServer, len(r.servers))
	for p, s := range r.servers {
		servers[p] = s
	}
	r.mu.Unlock()
	for p, s := range servers {
		s.mu.Lock()
		held := p != profile && s.held[conversation]
		s.mu.Unlock()
		if held {
			return p
		}
	}
	return ""
}

func (r *codexShared) idleSoon(profile string) {
	if profile == "" {
		return
	}
	r.d.life.Go("codexServerIdle", func() { r.stopServerIfUnused(profile) })
}
