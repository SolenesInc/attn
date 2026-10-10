package daemon_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestUnreadableCrewBindingsAffectOnlyTheirMemberAndRecoverAfterRepair(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	witness, _ := mailIdleAgent(w, app, "witness")
	day := wakeCrew(t, cli, "Keel", "")
	run := w.Launched(string(day.SessionID))
	run.Prompted()
	healthy := wakeCrew(t, cli, "Trellis", "")
	w.Launched(string(healthy.SessionID))
	seed := gardenReviewPlantTended(t, cli, string(day.SessionID), "Keep the bad member's mailbox")
	sendAgentMessage(t, cli, witness, seed, "leave this for Keel")
	original, err := cli.DocGet(crew.Namespace, crew.CollectionMembers, "keel")
	if err != nil || original.Document == nil {
		t.Fatalf("original member: %+v, %v", original, err)
	}
	if _, err := cli.DocPut(crew.Namespace, crew.CollectionMembers, "keel", fmt.Sprintf(`{"binding_session":%q,"awareness_dirs":1}`, day.SessionID), nil); err != nil {
		t.Fatal(err)
	}
	listed, err := cli.Query("")
	if err != nil {
		t.Fatal(err)
	}
	for surface, sessions := range map[string][]protocol.Session{"CLI": listed, "app": w.App().Initial.Sessions} {
		seen := map[protocol.SessionID]bool{}
		for _, s := range sessions {
			seen[s.ID] = true
			if s.ID == day.SessionID && s.CrewMember != nil {
				t.Fatalf("%s decorated an unreadable member: %+v", surface, s)
			}
			if s.ID == healthy.SessionID && protocol.Deref(s.CrewMember) != "trellis" {
				t.Fatalf("%s lost healthy identity: %+v", surface, s)
			}
		}
		if !seen[day.SessionID] || !seen[healthy.SessionID] || !seen[protocol.SessionID(witness)] {
			t.Fatalf("%s hid sessions: %+v", surface, sessions)
		}
	}
	if _, err := cli.AgentMsg(witness, day.SessionID, "keep my identity"); err == nil || !strings.Contains(err.Error(), "awareness_dirs") || !strings.Contains(err.Error(), "Keel") {
		t.Fatalf("unreadable member attribution: %v", err)
	}
	if _, err := cli.AutoModePropose("host", "", `{"host":"example.com","decision":"allow"}`, day.SessionID); err == nil || !strings.Contains(err.Error(), "awareness_dirs") {
		t.Fatalf("unreadable proposer: %v", err)
	}
	if _, err := cli.WithRequester("", healthy.SessionID).AgentPeek("Keel"); err == nil || !strings.Contains(err.Error(), "awareness_dirs") {
		t.Fatalf("unreadable target: %v", err)
	}
	sendAgentMessage(t, cli, string(healthy.SessionID), witness, "the other member still works")
	proposed, err := cli.AutoModePropose("host", "", `{"host":"example.org","decision":"allow"}`, healthy.SessionID)
	if err != nil || proposed.Proposal.ProposedBy.Ref != "member:trellis" {
		t.Fatalf("healthy proposer: %+v, %v", proposed, err)
	}
	readInbox(t, cli, string(healthy.SessionID), 0)
	readInbox(t, cli, witness, 0)
	if _, err := cli.AgentClose(witness, protocol.SessionID(witness), "ordinary work finished"); err != nil {
		t.Fatalf("healthy close: %v", err)
	}
	if _, err := cli.DocPut(crew.Namespace, crew.CollectionMembers, "keel", `{"awareness_dirs":1}`, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.AutoModePropose("host", "", `{"host":"missing-binding.example.org","decision":"allow"}`, day.SessionID); err == nil || !strings.Contains(err.Error(), "awareness_dirs") || !strings.Contains(err.Error(), "Keel") {
		t.Fatalf("unreadable member with no binding header was demoted: %v", err)
	}
	if _, err := cli.DocPut(crew.Namespace, crew.CollectionMembers, "keel", original.Document.Body, nil); err != nil {
		t.Fatal(err)
	}
	proposed, err = cli.AutoModePropose("host", "", `{"host":"repaired.example.org","decision":"allow"}`, day.SessionID)
	if err != nil || proposed.Proposal.ProposedBy.Ref != "member:keel" {
		t.Fatalf("repaired proposer: %+v, %v", proposed, err)
	}
}

func TestACrewAgentsCleanExitRecordsTheMemberBeforeReleasingItsBinding(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	day := wakeCrew(t, cli, "Keel", "")
	run := w.Launched(string(day.SessionID))
	run.Prompted()
	run.Exit(0)
	closed := awaitClosed(app, string(day.SessionID))
	if by := closed.ClosedBy; by == nil || by.Ref != "member:keel" || by.Name != "Keel" {
		t.Fatalf("clean-exit actor: %+v", by)
	}
}

func TestAnAutomaticCloseFailureReachesOnlyTheAffectedMembersNotification(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	ordinary, ordinaryRun := mailIdleAgent(w, app, "shop")
	day := wakeCrew(t, cli, "Keel", "")
	id := string(day.SessionID)
	run := w.Launched(id)
	run.Prompted()
	if _, err := cli.DocPut(crew.Namespace, crew.CollectionMembers, "keel", fmt.Sprintf(`{"binding_session":%q,"awareness_dirs":1}`, day.SessionID), nil); err != nil {
		t.Fatal(err)
	}
	ordinaryRun.Exit(0)
	awaitClosed(app, ordinary)
	run.Exit(0)
	testworld.Await(app, protocol.EventNotificationsUpdated, func(m protocol.NotificationsUpdatedMessage) bool { return m.UnreadCount == 1 })
	feed := listNotifications(app)
	if len(feed.Notifications) != 1 {
		t.Fatalf("close failure notifications: %+v", feed)
	}
	n := feed.Notifications[0]
	if n.Kind != "session_close_failed" || n.SourceID != id || !strings.Contains(n.Detail, "awareness_dirs") || !strings.Contains(n.Detail, "Keel") || len(n.Actions) != 1 || n.Actions[0].Kind != "open_session" || n.Actions[0].TargetID != id {
		t.Fatalf("close failure: %+v", n)
	}
	if entry := showSession(t, cli, id); entry.ClosedAt != nil || entry.ClosedBy != nil {
		t.Fatalf("close failure wrote an actor or closed the session: %+v", entry)
	}
}

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

func TestAClosedSessionActorKeepsItsDisplayName(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	id := w.Spawn(app, fakeagent.Claude, w.Path("actor"), func(m *protocol.SpawnSessionMessage) { m.Label = protocol.Ptr("Verifier work") })
	w.Launched(id)
	if _, err := cli.AgentClose(id, protocol.SessionID(id), "finished"); err != nil {
		t.Fatal(err)
	}
	entry := showSession(t, cli, id)
	if by := entry.ClosedBy; by == nil || by.Ref != protocol.ActorRef("session:"+id) || by.Name != "Verifier work" {
		t.Fatalf("closed actor: %+v", by)
	}
}
