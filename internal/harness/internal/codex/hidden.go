package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/transcript"
)

const codexLinkKind = "codex-app-server"

func (r *codexShared) launchedShared(sessionID protocol.SessionID) bool {
	agent, lifetime := r.host.Sessions().Lifetime(sessionID)
	return lifetime == harness.InServer && agent == codexID
}

func (r *codexShared) conversation(sessionID protocol.SessionID) string {
	if !r.launchedShared(sessionID) {
		return ""
	}
	return r.host.Sessions().Conversation(sessionID)
}

func (r *codexShared) holder(profile, conversation string) protocol.SessionID {
	sessionID := r.host.Sessions().Holder(profile, conversation)
	if sessionID == "" || !r.launchedShared(sessionID) {
		return ""
	}
	return sessionID
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

type codexConversation struct {
	shared *codexShared
	facts  harness.SessionFacts
}

func (h codexHarness) Conversation(facts harness.SessionFacts) harness.Conversation {
	if facts.Lifetime != harness.InServer {
		return harness.TypedConversation(errors.New("this Codex session started before Codex sessions ran on the shared app server, so attn has no way to compact it. Close without --compact instead"))
	}
	return codexConversation{shared: h.shared, facts: facts}
}

func (codexConversation) CanCompact() error { return nil }

func (c codexConversation) Deliver(_ context.Context, in harness.Input) harness.Custody {
	if in.PendingApproval {
		return harness.Custody{Outcome: harness.NotMine}
	}
	r, facts := c.shared, c.facts
	conversation := facts.Conversation
	if conversation == "" {
		return harness.Custody{Outcome: harness.NotMine}
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexServerStartLimit)
	defer cancel()
	s, client, err := r.control(ctx, facts.Profile)
	took := harness.Queue
	if err == nil {
		took, err = r.startTurn(ctx, s, client, facts.Session, conversation, in)
	}
	if err == nil && facts.Session != "" {
		s.events.run(r.host, func() {
			r.host.Sessions().EnsureTranscriptWatcher(facts.Session, facts.Path)
		})
	}
	if err != nil {
		return harness.Custody{Reason: fmt.Sprintf("deliver to shared Codex conversation %s: %v", conversation, err)}
	}
	return harness.Custody{Outcome: harness.Taken, At: time.Now(), TookAs: took}
}

func (r *codexShared) startTurn(ctx context.Context, s *codexServer, client *codexshared.Client, sessionID protocol.SessionID, conversation string, in harness.Input) (took harness.TurnUse, err error) {
	pausepoint.At(pausepoint.CodexTurnStarting)
	err = r.withResumed(ctx, client, sessionID, conversation, func(call codexCall, status codexThreadStatus) error {
		params := map[string]any{"threadId": conversation, "input": []any{map[string]any{"type": "text", "text": in.Text, "text_elements": []any{}}}}
		if status.Type == "active" {
			running, err := runningTurn(call, conversation)
			switch {
			case err != nil:
				return err
			case running == "":
			case in.Turn == harness.Interrupt:
				idle := s.awaitIdle(conversation)
				r.interrupting(running)
				err := call("turn/interrupt", map[string]any{"threadId": conversation, "turnId": running}, nil)
				if err == nil {
					err = awaitClosed(ctx, idle, codexServerCallLimit)
				}
				if err != nil {
					s.forgetIdleWait(conversation, idle)
					r.tookOwnInterrupt(running)
					return fmt.Errorf("interrupt turn %s: %w", running, err)
				}
				took = harness.Interrupt
			default:
				params["expectedTurnId"] = running
				took = harness.Steer
				return call("turn/steer", params, nil)
			}
		}
		return call("turn/start", params, nil)
	})
	return took, err
}

func (r *codexShared) interrupting(turn string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ownInterrupts == nil {
		r.ownInterrupts = make(map[string]bool)
	}
	r.ownInterrupts[turn] = true
}

func (r *codexShared) tookOwnInterrupt(turn string) bool {
	if r == nil || turn == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	own := r.ownInterrupts[turn]
	delete(r.ownInterrupts, turn)
	return own
}

func (s *codexServer) forgetIdleWait(conversation string, idle <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if waiting := s.idleWaits[conversation]; waiting != nil && (<-chan struct{})(waiting) == idle {
		delete(s.idleWaits, conversation)
	}
}

func runningTurn(call codexCall, conversation string) (string, error) {
	var turns struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := call("thread/turns/list", map[string]any{"threadId": conversation, "limit": 1, "sortDirection": "desc"}, &turns); err != nil {
		return "", err
	}
	if len(turns.Data) > 0 && turns.Data[0].Status == "inProgress" {
		return turns.Data[0].ID, nil
	}
	return "", nil
}

func awaitClosed(ctx context.Context, closed <-chan struct{}, limit time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	select {
	case <-closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *codexServer) awaitIdle(conversation string) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idleWaits == nil {
		s.idleWaits = make(map[string]chan struct{})
	}
	idle := s.idleWaits[conversation]
	if idle == nil {
		idle = make(chan struct{})
		s.idleWaits[conversation] = idle
	}
	return idle
}

type codexCall func(method string, params, result any) error

func (r *codexShared) withResumed(ctx context.Context, client *codexshared.Client, sessionID protocol.SessionID, conversation string, fn func(call codexCall, status codexThreadStatus) error) error {
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
	info := r.host.Sessions().LaunchInfo(sessionID, codexID)
	config := make(map[string]any)
	r.loadConfig(config, info.ContextCap)
	resume := map[string]any{"threadId": conversation, "excludeTurns": true, "config": config}
	grantCodexApprovals(resume, info.ApprovalRoute)
	if err := call("thread/resume", resume, &resumed); err != nil {
		return err
	}
	defer func() {
		_ = call("thread/unsubscribe", map[string]any{"threadId": conversation}, nil)
	}()
	return fn(call, resumed.Thread.Status)
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
	return 0, false
}

type linkEvents struct {
	mu      sync.Mutex
	queue   []func()
	running bool
}

func (q *linkEvents) run(host harness.Host, f func()) {
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
	if !host.Life().Go("codexServerEvents", drain) {
		q.mu.Lock()
		q.queue, q.running = nil, false
		q.mu.Unlock()
	}
}

func (r *codexShared) observe(sessionID protocol.SessionID, turn harness.Turn) {
	session := r.host.Sessions().Get(sessionID)
	if session == nil {
		return
	}
	if r.host.Sessions().Hidden(sessionID) || r.followsServer(sessionID) {
		r.report(sessionID, session, turn)
		return
	}
	r.host.Sessions().TraceServerStatus(sessionID, turn, time.Now())
	if turn == harness.TurnEnded && r.terminalClaimsWorking(sessionID) {
		r.checkWorkingSoon(sessionID)
	}
}

func (r *codexShared) report(sessionID protocol.SessionID, session *protocol.Session, turn harness.Turn) {
	now := time.Now()
	r.host.Sessions().TraceServerStatus(sessionID, turn, now)
	reporter := harness.FromSession(sessionID)
	if turn == harness.TurnEnded {
		evidence, _ := r.host.Sessions().Evidence(sessionID)
		bracketOpen := evidence.TurnOpen || evidence.ToolOpen
		if !bracketOpen && !inTurn(session.State) {
			return
		}
		if bracketOpen {
			r.host.Events().Turn(reporter, now, harness.TurnEvent{Turn: harness.TurnAborted, Inferred: true, Detail: "the shared Codex app-server ended the turn"})
		}
	}
	r.host.Events().Turn(reporter, now, harness.TurnEvent{Turn: turn, Inferred: true, Status: true})
}

func inTurn(state protocol.SessionState) bool {
	switch state {
	case protocol.SessionStateWorking, protocol.SessionStatePendingApproval, protocol.SessionStateUnknown:
		return true
	}
	return false
}

func (r *codexShared) observeStatus(s *codexServer, m codexshared.Message) {
	var p struct {
		ThreadID string            `json:"threadId"`
		Status   codexThreadStatus `json:"status"`
	}
	if json.Unmarshal(m.Params, &p) != nil || p.ThreadID == "" {
		return
	}
	idle := s.idleWaits[p.ThreadID]
	ownInterruptEnded := idle != nil && p.Status.Type != "active"
	if ownInterruptEnded {
		close(idle)
		delete(s.idleWaits, p.ThreadID)
	}
	if compacted := s.compactionStepLocked(p.ThreadID, p.Status.Type); compacted != nil {
		r.host.Events().Compaction(compacted.session, harness.Compaction{Input: compacted.delivery, Result: harness.Compacted})
	}
	turn, ok := p.Status.turn()
	if !ok {
		return
	}
	s.events.run(r.host, func() {
		holder := r.holder(s.profile, p.ThreadID)
		if !ownInterruptEnded {
			r.observe(holder, turn)
		}
		if turn == harness.TurnEnded {
			r.host.Events().Compaction(holder, harness.Compaction{Result: harness.CompactionReady})
		}
	})
}

func (r *codexShared) hiddenSessions(profile string) []protocol.SessionID {
	var hidden []protocol.SessionID
	for _, session := range r.host.Sessions().List() {
		if session.ProfileID == profile && r.launchedShared(session.ID) && r.host.Sessions().Hidden(session.ID) {
			hidden = append(hidden, session.ID)
		}
	}
	return hidden
}

func (r *codexShared) readHidden(s *codexServer, client *codexshared.Client) {
	for _, sessionID := range r.hiddenSessions(s.profile) {
		ctx, cancel := context.WithTimeout(r.host.Life().Context(), codexServerCallLimit)
		raw, err := client.Call(ctx, "thread/read", map[string]any{"threadId": r.conversation(sessionID)})
		cancel()
		var read struct {
			Thread struct {
				Status codexThreadStatus `json:"status"`
			} `json:"thread"`
		}
		if err != nil || json.Unmarshal(raw, &read) != nil {
			r.host.Logf("shared Codex: reading the turn of hidden session %s: %v", sessionID, err)
			continue
		}
		if turn, ok := read.Thread.Status.turn(); ok {
			r.observe(sessionID, turn)
		}
	}
}

func (r *codexShared) serverGone(profile string) {
	hidden := r.hiddenSessions(profile)
	if len(hidden) == 0 {
		return
	}
	s := r.server(profile)
	s.events.run(r.host, func() {
		for _, sessionID := range hidden {
			r.observe(sessionID, harness.TurnEnded)
		}
	})
}

func (r *codexShared) archiveConversation(profile string, closed protocol.SessionID, conversation string) {
	ctx, cancel := context.WithTimeout(context.Background(), codexServerStartLimit)
	defer cancel()
	_, client, err := r.control(ctx, profile)
	if err == nil {
		callCtx, cancelCall := context.WithTimeout(context.Background(), codexServerCallLimit)
		_, err = client.Call(callCtx, "thread/archive", map[string]any{"threadId": conversation})
		cancelCall()
	}
	if err != nil {
		r.host.Logf("shared Codex: conversation %s of closed session %s stays unarchived: %v", conversation, closed, err)
	}
}

func (r *codexShared) sessionClosed(sessionID protocol.SessionID, profile, released string) {
	r.mu.Lock()
	r.archivingAfterClose[profile]++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.archivingAfterClose[profile]--; r.archivingAfterClose[profile] == 0 {
			delete(r.archivingAfterClose, profile)
		}
		r.mu.Unlock()
		r.profileReleased(profile)
	}()
	if released != "" {
		r.archiveConversation(profile, sessionID, released)
	}
}

func (r *codexShared) restore(profile, conversation string) error {
	if transcript.FindArchivedCodexTranscript(conversation) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexServerStartLimit)
	defer cancel()
	_, client, err := r.control(ctx, profile)
	if err != nil {
		return err
	}
	callCtx, cancelCall := context.WithTimeout(context.Background(), codexServerCallLimit)
	defer cancelCall()
	if _, err := client.Call(callCtx, "thread/unarchive", map[string]any{"threadId": conversation}); err != nil {
		return fmt.Errorf("unarchive Codex conversation %s: %w", conversation, err)
	}
	return nil
}

func (r *codexShared) unarchiveForResume(profile string, m *codexshared.Message) {
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return
	}
	if err := r.restore(profile, p.ThreadID); err != nil {
		r.host.Logf("shared Codex: conversation %s stays archived: %v", p.ThreadID, err)
	}
}

func (r *codexShared) transcriptPath(path string) string {
	return transcript.ResolveCodexRolloutPath(path)
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
	s.events.run(r.host, func() {
		sessionID := r.holder(s.profile, p.ThreadID)
		session := r.host.Sessions().Get(sessionID)
		if session == nil || name == "" {
			return
		}
		r.host.Events().Named(sessionID, name)
	})
}

func (r *codexShared) renamed(sessionID protocol.SessionID, label string) {
	conversation := r.conversation(sessionID)
	session := r.host.Sessions().Get(sessionID)
	if conversation == "" || session == nil {
		return
	}
	r.mu.Lock()
	s := r.servers[session.ProfileID]
	r.mu.Unlock()
	if s == nil {
		return
	}
	s.events.run(r.host, func() {
		client := s.client()
		if client == nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.host.Life().Context(), codexServerCallLimit)
		defer cancel()
		if _, err := client.Call(ctx, "thread/name/set", map[string]any{"threadId": conversation, "name": label}); err != nil {
			r.host.Logf("shared Codex: naming conversation %s %q: %v", conversation, label, err)
		}
	})
}

func (r *codexShared) profileReleased(profile string) {
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
	r.stop(s, codexServerReleaseGrace)
	r.host.Logf("shared Codex: stopped the app-server of profile %s; nothing uses it", profile)
}

func (r *codexShared) needed(profile string) bool {
	r.mu.Lock()
	if r.archivingAfterClose[profile] > 0 {
		r.mu.Unlock()
		return true
	}
	for _, v := range r.views {
		if v.profile == profile {
			r.mu.Unlock()
			return true
		}
	}
	r.mu.Unlock()
	for _, session := range r.host.Sessions().List() {
		if session.ProfileID == profile && session.Agent == codexID && r.launchedShared(session.ID) {
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
		_, held := s.held[conversation]
		held = held && p != profile
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
	r.host.Life().Go("codexServerIdle", func() { r.profileReleased(profile) })
}
