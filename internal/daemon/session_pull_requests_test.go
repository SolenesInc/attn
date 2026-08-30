package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

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

// Registration broadcasts land on their own goroutine, so only this session's
// state updates count here.
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

func TestPullRequestCreatedLandsOnTheSessionAndBroadcastsOnce(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	cap := captureBroadcasts(d)

	resp := sendPRCommand(t, d, protocol.PullRequestCreatedMessage{
		Cmd: protocol.CmdPullRequestCreated,
		ID:  "s1",
		URL: "https://github.com/victorarias/attn/pull/71",
	})
	if !resp.Ok {
		t.Fatalf("record response = %+v", resp)
	}

	prs := sessionPullRequests(t, d, "s1")
	if len(prs) != 1 {
		t.Fatalf("pull requests = %+v, want the reported one", prs)
	}
	if prs[0].Repository != "github.com/victorarias/attn" || prs[0].Number != 71 {
		t.Errorf("identity = %s#%d, want github.com/victorarias/attn#71", prs[0].Repository, prs[0].Number)
	}
	if prs[0].State != "open" {
		t.Errorf("state = %q, want open", prs[0].State)
	}
	if prs[0].CIStatus != nil || prs[0].ReviewStatus != nil || prs[0].StatusFetchedAt != nil {
		t.Errorf("entry = %+v, want no status before the refresh job fetches it", prs[0])
	}

	events := sessionUpdates(cap, "s1")
	if len(events) != 1 {
		t.Fatalf("session updates = %d, want one", len(events))
	}
	if len(events[0].Session.PullRequests) != 1 {
		t.Fatalf("broadcast carried %+v, want the session with its pull request", events[0].Session.PullRequests)
	}

	// The hook and a manual `attn pr record` both firing is the normal case.
	if resp := sendPRCommand(t, d, protocol.PullRequestCreatedMessage{
		Cmd: protocol.CmdPullRequestCreated,
		ID:  "s1",
		URL: "https://github.com/victorarias/attn/pull/71",
	}); !resp.Ok {
		t.Fatalf("second record response = %+v", resp)
	}
	if prs := sessionPullRequests(t, d, "s1"); len(prs) != 1 {
		t.Fatalf("pull requests after the repeat = %+v, want still one", prs)
	}
	if events := sessionUpdates(cap, "s1"); len(events) != 1 {
		t.Fatalf("session updates after the repeat = %d, want no second one", len(events))
	}
}

func TestPullRequestCreatedNamesWhatItRejected(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")

	tests := []struct {
		name    string
		session string
		url     string
		wants   []string
	}{
		{
			name:    "a url that is not a pull request",
			session: "s1",
			url:     "https://github.com/victorarias/attn/issues/12",
			wants:   []string{"pull request url"},
		},
		{
			name:    "a session the daemon never heard of",
			session: "typo",
			url:     "https://github.com/victorarias/attn/pull/71",
			wants:   []string{"unknown session", "typo"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := sendPRCommand(t, d, protocol.PullRequestCreatedMessage{
				Cmd: protocol.CmdPullRequestCreated,
				ID:  tc.session,
				URL: tc.url,
			})
			if resp.Ok {
				t.Fatalf("response = %+v, want a rejection", resp)
			}
			for _, want := range tc.wants {
				if !strings.Contains(protocol.Deref(resp.Error), want) {
					t.Errorf("error = %q, want it to name %q", protocol.Deref(resp.Error), want)
				}
			}
		})
	}
	if prs := sessionPullRequests(t, d, "s1"); len(prs) != 0 {
		t.Fatalf("pull requests = %+v, want nothing recorded", prs)
	}
}

// The hook fires once and never retries, so a host attn has no client for — an
// enterprise one, or any host during the seconds before gh discovery lands — still counts.
func TestPullRequestCreatedRecordsHostsAttnCannotPoll(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")

	if hosts := d.gitHubHosts(); len(hosts) != 0 {
		t.Fatalf("hosts = %v, want a registry that has discovered nothing yet", hosts)
	}
	if resp := sendPRCommand(t, d, protocol.PullRequestCreatedMessage{
		Cmd: protocol.CmdPullRequestCreated, ID: "s1", URL: "https://ghe.example.test/acme/widget/pull/12",
	}); !resp.Ok {
		t.Fatalf("record response = %+v, want it accepted", resp)
	}
	prs := sessionPullRequests(t, d, "s1")
	if len(prs) != 1 || prs[0].Repository != "ghe.example.test/acme/widget" {
		t.Fatalf("pull requests = %+v, want the enterprise one recorded", prs)
	}
}

func TestPullRequestForgetIsTheWayOut(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	url := "https://github.com/victorarias/attn/pull/71"
	if resp := sendPRCommand(t, d, protocol.PullRequestCreatedMessage{
		Cmd: protocol.CmdPullRequestCreated, ID: "s1", URL: url,
	}); !resp.Ok {
		t.Fatalf("record response = %+v", resp)
	}

	cap := captureBroadcasts(d)
	if resp := sendPRCommand(t, d, protocol.PullRequestForgetMessage{
		Cmd: protocol.CmdPullRequestForget, ID: "s1", URL: url,
	}); !resp.Ok {
		t.Fatalf("forget response = %+v", resp)
	}
	if prs := sessionPullRequests(t, d, "s1"); len(prs) != 0 {
		t.Fatalf("pull requests = %+v, want the entry gone", prs)
	}
	if events := sessionUpdates(cap, "s1"); len(events) != 1 {
		t.Fatalf("session updates = %d, want one after forgetting", len(events))
	}

	resp := sendPRCommand(t, d, protocol.PullRequestForgetMessage{
		Cmd: protocol.CmdPullRequestForget, ID: "s1", URL: url,
	})
	if resp.Ok || !strings.Contains(protocol.Deref(resp.Error), "no pull request") {
		t.Fatalf("forgetting twice = %+v, want it to say the session has no such entry", resp)
	}
}

func TestSessionsForBroadcastCarryTheirPullRequests(t *testing.T) {
	d := newPRDaemonForTest(t, "s1")
	registerSessionForPRTest(t, d, "s2")
	if resp := sendPRCommand(t, d, protocol.PullRequestCreatedMessage{
		Cmd: protocol.CmdPullRequestCreated, ID: "s2", URL: "https://github.com/victorarias/attn/pull/71",
	}); !resp.Ok {
		t.Fatalf("record response = %+v", resp)
	}

	for _, session := range d.sessionsForBroadcast(d.store.List("")) {
		switch session.ID {
		case "s1":
			if len(session.PullRequests) != 0 {
				t.Errorf("s1 pull requests = %+v, want none", session.PullRequests)
			}
		case "s2":
			if len(session.PullRequests) != 1 || session.PullRequests[0].Number != 71 {
				t.Errorf("s2 pull requests = %+v, want the recorded one", session.PullRequests)
			}
		}
	}
}
