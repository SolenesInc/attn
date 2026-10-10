package daemon_test

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestRepliesToACrewPartyFollowItsNextSession(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	x, xRun := mailIdleAgent(w, app, "peer")
	day := wakeCrew(t, cli, "Keel", "")
	old := string(day.SessionID)
	run := w.Launched(old)
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, old, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	sendAgentMessage(t, cli, old, x, "the member sent this")
	if got := xRun.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatal(got)
	}
	batch := readInbox(t, cli, x, 0)
	if len(batch.Items) != 1 || batch.Items[0].Sender == nil || batch.Items[0].Sender.Ref != "member:keel" || batch.Items[0].Sender.Name != "Keel" || protocol.Deref(batch.Items[0].ReplyTo) != "Keel" {
		t.Fatalf("member sender: %+v", batch.Items)
	}
	next := crewHandoff(t, cli, old, "Continue the conversation with our peer.", false, protocol.CrewDayCloseNap)
	id := string(protocol.Deref(next.SessionID))
	if id == "" || next.NapError != nil {
		t.Fatalf("nap: %+v", next)
	}
	nextRun := w.Launched(id)
	nextRun.Prompted()
	nextRun.Reply("Ready. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	reply := sendAgentMessage(t, cli, x, protocol.Deref(batch.Items[0].ReplyTo), "reply to the next day")
	if protocol.Deref(reply.TargetSessionID) != protocol.SessionID(id) {
		t.Fatalf("reply: %+v", reply)
	}
	if got := nextRun.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatal(got)
	}
	received := readInbox(t, cli, id, 0)
	if len(received.Items) != 1 || received.Items[0].Content != "reply to the next day" || protocol.Deref(received.Items[0].ReplyTo) != "session:"+x {
		t.Fatalf("plain sender: %+v", received.Items)
	}
	if _, err := cli.AgentClose(x, protocol.SessionID(x), "done"); err != nil {
		t.Fatal(err)
	}
	queued := sendAgentMessage(t, cli, id, protocol.Deref(received.Items[0].ReplyTo), "wait for resume")
	if queued.Status != protocol.AgentMsgStatusQueued || !strings.Contains(queued.Detail, "has ended; waits until it is resumed") {
		t.Fatalf("ended reply: %+v", queued)
	}
}
func TestACrewSessionReadsBothItsSessionAndMemberAddresses(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	sender, _ := mailIdleAgent(w, app, "sender")
	day := wakeCrew(t, cli, "Keel", "")
	id := string(day.SessionID)
	run := w.Launched(id)
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	sendAgentMessage(t, cli, sender, "session:"+id, "session mailbox")
	sendAgentMessage(t, cli, sender, "Keel", "member mailbox")
	batch := readInbox(t, cli, id, 0)
	if len(batch.Items) != 2 || batch.Items[0].Address != protocol.AddressRef("session:"+id) || batch.Items[1].Address != "member:keel" {
		t.Fatalf("dual mailboxes: %+v", batch.Items)
	}
}
func TestUntendedSeedMailStatesWhyItWaits(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	sender, _ := mailIdleAgent(w, app, "sender")
	seed := plantSeedAs(t, cli, sender, "Somebody will pick this up")
	sent := sendAgentMessage(t, cli, sender, seed, "for its next tender")
	if sent.Status != protocol.AgentMsgStatusQueued || !strings.Contains(sent.Detail, "nobody tends "+seed) {
		t.Fatalf("untended seed: %+v", sent)
	}
}
func TestACrewPullRequestWatchFollowsTheNextSessionAndCanBeStoppedThere(t *testing.T) {
	serveWatchedPullRequest(t, watchedPullRequestGitHub{state: "OPEN", reviewDecision: "REVIEW_REQUIRED"})
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	day := wakeCrew(t, cli, "Keel", "")
	old := string(day.SessionID)
	run := w.Launched(old)
	run.Prompted()
	run.Reply("Ready. <!-- attn:state=idle -->")
	watchPullRequestAs(t, cli, old, protocol.PullRequestWatchModeCodex, "")
	awaitWatchedPullRequest(app, old, func(pr protocol.SessionPullRequest) bool { return protocol.Deref(pr.Watching) })
	next := crewHandoff(t, cli, old, "Keep watching the PR.", false, protocol.CrewDayCloseNap)
	id := string(protocol.Deref(next.SessionID))
	w.Launched(id)
	awaitWatchedPullRequest(app, id, func(pr protocol.SessionPullRequest) bool { return protocol.Deref(pr.Watching) })
	if err := cli.UnwatchSessionPullRequest(protocol.SessionID(id), watchedPullRequestURL); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, id, func(s protocol.Session) bool { return len(s.PullRequests) == 0 })
}
func TestCrewAutoModeProposalsRecordTheMember(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()
	day := wakeCrew(t, cli, "Keel", "")
	w.Launched(string(day.SessionID))
	proposed, err := cli.AutoModePropose("host", "", `{"host":"example.com","decision":"allow"}`, day.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if by := proposed.Proposal.ProposedBy; by == nil || by.Ref != "member:keel" || by.Name != "Keel" {
		t.Fatalf("proposal actor: %+v", by)
	}
}

func TestSessionsCarryTheirProfileOnTheWire(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	registerSessions(t, w, cli, "agent-a")
	app := w.App()
	for _, s := range app.Initial.Sessions {
		if s.ID == "agent-a" {
			if s.ProfileID != app.SelectedProfile() {
				t.Fatalf("profile on wire: %+v", s)
			}
			return
		}
	}
	t.Fatalf("agent-a absent: %+v", app.Initial.Sessions)
}

func TestCrewHomePathFailuresDoNotEraseSessionsOrSenderIdentity(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	witness, _ := mailIdleAgent(w, app, "witness")
	day := wakeCrew(t, cli, "Keel", "")
	w.Launched(string(day.SessionID))
	home := crewHome(w, "alder")
	outside := w.Path("relocated-alder")
	if err := os.Rename(home, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, home); err != nil {
		t.Fatal(err)
	}
	fresh := w.App()
	seen := map[protocol.SessionID]bool{}
	for _, s := range fresh.Initial.Sessions {
		seen[s.ID] = true
	}
	if !seen[day.SessionID] || !seen[protocol.SessionID(witness)] {
		t.Fatalf("sessions disappeared after an unrelated home moved: %+v", fresh.Initial.Sessions)
	}
	sent := sendAgentMessage(t, cli, string(day.SessionID), witness, "still from Keel")
	status, err := cli.AgentMsgStatus(sent.MessageID, day.SessionID)
	if err != nil || status.Sender.Ref != "member:keel" {
		t.Fatalf("sender after a home path failure: %+v, %v", status, err)
	}
	if _, err := cli.CrewWake("Alder", "", ""); err == nil {
		t.Fatal("the relocated home passed the wake path fence")
	}
}
