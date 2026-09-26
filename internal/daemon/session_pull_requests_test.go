package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/protocol"
)

func registerSessionForPRTest(t *testing.T, d *Daemon, id string) {
	t.Helper()
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        "workspace-" + id,
		Title:     "workspace-" + id,
		Directory: t.TempDir(),
	})

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	go func() {
		d.handleRegister(serverConn, &protocol.RegisterMessage{
			ID:          id,
			Label:       protocol.Ptr(id),
			Dir:         t.TempDir(),
			Agent:       protocol.Ptr(protocol.SessionAgentClaude),
			WorkspaceID: "workspace-" + id,
		})
		_ = serverConn.Close()
	}()
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("register response = %+v", resp)
	}
}

func sendPRCommand(t *testing.T, d *Daemon, msg any) protocol.Response {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()
	if err := json.NewEncoder(clientConn).Encode(msg); err != nil {
		t.Fatalf("send command: %v", err)
	}
	var resp protocol.Response
	if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	<-done
	return resp
}

func newPRDaemonForTest(t *testing.T, sessionID string) *Daemon {
	t.Helper()
	d := newDaemonForTest(t)
	stopDaemonBackground(t, d)
	registerSessionForPRTest(t, d, sessionID)
	return d
}

func sessionUpdates(cap *broadcastCapture, sessionID string) []protocol.WebSocketEvent {
	var out []protocol.WebSocketEvent
	for _, event := range cap.snapshot() {
		if event.Event == protocol.EventSessionStateChanged && event.Session != nil && event.Session.ID == sessionID {
			out = append(out, event)
		}
	}
	return out
}

func sessionPullRequests(t *testing.T, d *Daemon, sessionID string) []protocol.SessionPullRequest {
	t.Helper()
	session := d.sessionForBroadcast(d.store.Get(sessionID))
	if session == nil {
		t.Fatalf("session %s missing from the store", sessionID)
	}
	return session.PullRequests
}

func TestPullRequestMutationsTravelToTheSessionOwner(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	d.hubManager = hub.NewManager(d.store, nil, nil, nil, nil, nil)
	endpoint, err := d.hubManager.AddEndpoint("remote", "remote.example.test", "")
	if err != nil {
		t.Fatalf("add endpoint: %v", err)
	}
	d.hubManager.ReservePendingSessionRoute(endpoint.ID, "s-remote")

	for _, msg := range []any{
		protocol.PullRequestCreatedMessage{
			Cmd: protocol.CmdPullRequestCreated, ID: "s-remote", URL: "https://github.com/victorarias/attn/pull/71",
		},
		protocol.PullRequestForgetMessage{
			Cmd: protocol.CmdPullRequestForget, ID: "s-remote", URL: "https://github.com/victorarias/attn/pull/71",
		},
	} {
		resp := sendPRCommand(t, d, msg)
		if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "endpoint owning session s-remote") {
			t.Fatalf("response = %+v, want it routed to the owning endpoint", resp)
		}
	}
	if prs := sessionPullRequests(t, d, "s1"); len(prs) != 0 {
		t.Fatalf("local pull requests = %+v, want the hub's own store untouched", prs)
	}
}

func TestPullRequestReportedByAPluginDriverLandsOnTheSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"state_reporting": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-pr", Label: "driver work", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if !d.store.BeginAgentDriverRun("pi-pr", "pi-plugin", "run-1") {
		t.Fatal("failed to begin the test plugin run")
	}

	sendPluginMethod(t, client, 3, "session.report_pull_request", pluginReportPullRequestParams{
		SessionID: "pi-pr",
		RunID:     "run-1",
		URL:       "https://github.com/victorarias/attn/pull/90",
	})

	prs := sessionPullRequests(t, d, "pi-pr")
	if len(prs) != 1 {
		t.Fatalf("pull requests = %+v, want the reported one", prs)
	}
	if prs[0].Repository != "github.com/victorarias/attn" || prs[0].Number != 90 || prs[0].State != "open" {
		t.Errorf("entry = %+v, want github.com/victorarias/attn#90 open", prs[0])
	}

	sendPluginMethod(t, client, 4, "session.report_pull_request", pluginReportPullRequestParams{
		SessionID: "pi-pr",
		RunID:     "run-1",
		URL:       "https://github.com/victorarias/attn/pull/90",
	})
	if prs := sessionPullRequests(t, d, "pi-pr"); len(prs) != 1 {
		t.Fatalf("pull requests after the repeat = %+v, want still one", prs)
	}
	if published := docFacts(t, d, FactSessionPullRequestChanged); len(published) != 1 {
		t.Fatalf("facts = %+v, want one: the suite retries a report the relay dropped", published)
	}
}

func TestPullRequestReportedForARunThePluginDoesNotOwnIsRefused(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client, done := startPluginPipe(t, d, "pi-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "pi", map[string]bool{"state_reporting": true})

	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "pi-pr", Label: "driver work", Agent: "pi", Directory: t.TempDir(),
		State: protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if !d.store.BeginAgentDriverRun("pi-pr", "pi-plugin", "run-1") {
		t.Fatal("failed to begin the test plugin run")
	}

	refusals := []struct {
		name   string
		params pluginReportPullRequestParams
	}{
		{"another run", pluginReportPullRequestParams{SessionID: "pi-pr", RunID: "run-other", URL: "https://github.com/victorarias/attn/pull/90"}},
		{"an unknown session", pluginReportPullRequestParams{SessionID: "nobody", RunID: "run-1", URL: "https://github.com/victorarias/attn/pull/90"}},
		{"a url that is not a pull request", pluginReportPullRequestParams{SessionID: "pi-pr", RunID: "run-1", URL: "https://github.com/victorarias/attn"}},
	}
	for i, refusal := range refusals {
		response := sendPluginMethodResponse(t, client, 3+i, "session.report_pull_request", refusal.params)
		if response.Error == nil {
			t.Errorf("%s was accepted", refusal.name)
		}
	}

	if prs := sessionPullRequests(t, d, "pi-pr"); len(prs) != 0 {
		t.Fatalf("refused reports reached the session: %+v", prs)
	}
}
