package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/codexshared"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/sessionstate"
	"github.com/victorarias/attn/internal/transcript"
)

const codexLinkKind = "codex-app-server"

func (r *codexShared) launchedShared(sessionID protocol.SessionID) bool {
	return r.d.store.SessionLink(sessionID) == codexLinkKind
}

func (r *codexShared) conversation(sessionID protocol.SessionID) string {
	if !r.launchedShared(sessionID) {
		return ""
	}
	return r.d.store.GetSessionConversation(sessionID).NativeID
}

func (r *codexShared) holder(profile, conversation string) protocol.SessionID {
	sessionID := r.d.store.OpenSessionHolding(profile, conversation)
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

func (r *codexShared) Voices() []harness.Voice {
	return []harness.Voice{harness.VoiceUser, harness.VoiceAttn}
}

type codexMonitorLink struct {
	shared  *codexShared
	monitor *sessionmonitor.Monitor
	target  sessionmonitor.LinkTarget
}

func (l codexMonitorLink) Voices() []harness.Voice { return l.shared.Voices() }

func (r *codexShared) Deliver(ctx context.Context, in harness.Input) harness.Custody {
	monitor := r.d.sessionMonitor(in.Session)
	if monitor == nil {
		return harness.Custody{Reason: fmt.Sprintf("session %s has no monitor", in.Session)}
	}
	return (codexMonitorLink{shared: r, monitor: monitor, target: monitor.LinkTarget()}).Deliver(ctx, in)
}

func (l codexMonitorLink) Deliver(_ context.Context, in harness.Input) harness.Custody {
	r, target := l.shared, l.target
	conversation := target.Conversation.NativeID
	if conversation == "" {
		return harness.Custody{Reason: "shared Codex monitor has no conversation yet"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexServerStartLimit)
	defer cancel()
	s, client, err := r.control(ctx, target.Profile)
	if err == nil {
		err = r.startTurn(ctx, client, target.Session, conversation, in.Text)
	}
	if err == nil && target.Session != "" {
		s.events.run(r.d, func() { l.monitor.EnsureTranscriptWatcher(target.Conversation.TranscriptPath) })
	}
	if err != nil {
		return harness.Custody{Reason: fmt.Sprintf("deliver to shared Codex conversation %s: %v", conversation, err)}
	}
	return harness.Custody{Taken: true, At: time.Now()}
}

func (r *codexShared) startTurn(ctx context.Context, client *codexshared.Client, sessionID protocol.SessionID, conversation, text string) error {
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
	config := make(map[string]any)
	r.capContext(config, sessionID)
	resume := map[string]any{"threadId": conversation, "excludeTurns": true, "config": config}
	r.grantApprovals(resume, sessionID)
	if err := call("thread/resume", resume, &resumed); err != nil {
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

func (st codexThreadStatus) programStatus() (string, bool) {
	switch st.Type {
	case "idle", "notLoaded":
		return pty.ProgramIdle, true
	case "active":
		switch {
		case slices.Contains(st.ActiveFlags, "waitingOnApproval"):
			return pty.ProgramBlockedPermission, true
		case slices.Contains(st.ActiveFlags, "waitingOnUserInput"):
			return pty.ProgramBlockedQuestion, true
		}
		return pty.ProgramWorking, true
	}
	return "", false
}

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

func (r *codexShared) observe(sessionID protocol.SessionID, status string) {
	session := r.d.store.Get(sessionID)
	if session == nil {
		return
	}
	now := time.Now()
	r.d.traceStateEvidence(sessionID, sessionmonitor.Origin{Source: string(sessionstate.SourceAppServer), ObservedAt: now}, status)
	if !r.d.hidden(sessionID) {
		return
	}
	if status == pty.ProgramIdle {
		evidence, _ := r.d.sessionMonitor(sessionID).Evidence()
		bracketOpen := evidence.TurnOpen || evidence.ToolOpen
		if !bracketOpen && !inTurn(session.State) {
			return
		}
		if bracketOpen {
			r.d.sessionMonitor(sessionID).RecordTurnAborted("the shared Codex app-server ended the turn", now, now)
		}
	}
	r.d.sessionMonitor(sessionID).RecordServerStatus(status, now)
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
	status, ok := p.Status.programStatus()
	if !ok {
		return
	}
	s.events.run(r.d, func() { r.observe(r.holder(s.profile, p.ThreadID), status) })
}

func (r *codexShared) hiddenSessions(profile string) []protocol.SessionID {
	var hidden []protocol.SessionID
	for _, session := range r.d.store.List("") {
		if session.ProfileID == profile && r.launchedShared(session.ID) && r.d.hidden(session.ID) {
			hidden = append(hidden, session.ID)
		}
	}
	return hidden
}

func (r *codexShared) readHidden(s *codexServer, client *codexshared.Client) {
	for _, sessionID := range r.hiddenSessions(s.profile) {
		ctx, cancel := context.WithTimeout(r.d.life.Context(), codexServerCallLimit)
		raw, err := client.Call(ctx, "thread/read", map[string]any{"threadId": r.conversation(sessionID)})
		cancel()
		var read struct {
			Thread struct {
				Status codexThreadStatus `json:"status"`
			} `json:"thread"`
		}
		if err != nil || json.Unmarshal(raw, &read) != nil {
			r.d.logf("shared Codex: reading the turn of hidden session %s: %v", sessionID, err)
			continue
		}
		if status, ok := read.Thread.Status.programStatus(); ok {
			r.observe(sessionID, status)
		}
	}
}

func (r *codexShared) serverGone(profile string) {
	hidden := r.hiddenSessions(profile)
	if len(hidden) == 0 {
		return
	}
	s := r.server(profile)
	s.events.run(r.d, func() {
		for _, sessionID := range hidden {
			r.observe(sessionID, pty.ProgramIdle)
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
		r.d.logf("shared Codex: conversation %s of closed session %s stays unarchived: %v", conversation, closed, err)
	}
}

func (r *codexShared) setAsideAtClose(sessionID protocol.SessionID, conversation string) bool {
	return conversation != "" && r.launchedShared(sessionID) && transcript.FindArchivedCodexTranscript(conversation) != ""
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
	s.events.run(r.d, func() {
		sessionID := r.holder(s.profile, p.ThreadID)
		session := r.d.store.Get(sessionID)
		if session == nil || name == "" {
			return
		}
		if monitor := r.d.sessionMonitor(sessionID); monitor != nil {
			monitor.ReportHarnessTitle(name)
		}
	})
}

func (r *codexShared) renamed(sessionID protocol.SessionID, label string) {
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
	r.d.logf("shared Codex: stopped the app-server of profile %s; nothing uses it", profile)
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
	r.d.life.Go("codexServerIdle", func() { r.profileReleased(profile) })
}
