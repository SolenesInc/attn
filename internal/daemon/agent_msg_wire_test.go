package daemon_test

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestMailForAnAgentMidTurnStaysSealedUntilItsTurnEndsThenRings(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	recipient, agent := mailIdleAgent(w, app, "shop")
	panes := spawnPanes(w, app, w.Path("sender"), w.Path("bystander"))
	sender, bystander := panes[0].session, panes[1].session
	app.TypeLine(recipient, "rebase onto main")
	agent.Prompted()
	testworld.AwaitSession(app, recipient, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	sent := sendAgentMessage(t, cli, sender, recipient, "when you surface, rebase again")
	if sent.Status != protocol.AgentMsgStatusQueued {
		t.Fatalf("a message mid-turn = %+v, want queued", sent)
	}
	for _, reader := range []string{recipient, bystander} {
		if read, err := cli.AgentInbox(sent.MessageID, protocol.SessionID(reader)); err == nil {
			t.Errorf("%s read the queued message by its ID: %+v", reader, read)
		}
	}
	if _, err := cli.AgentMsgStatus(sent.MessageID, protocol.SessionID(bystander)); client.ErrorCode(err) != "message_not_found" {
		t.Errorf("a bystander asking for the message's status = %v, want message_not_found", err)
	}
	if status, err := cli.AgentMsgStatus(sent.MessageID, protocol.SessionID(sender)); err != nil || status.State != protocol.AgentMessageStateQueued {
		t.Errorf("the sender asking for the message's status = %+v, %v; want queued", status, err)
	}

	agent.Reply("Rebased. <!-- attn:state=idle -->")
	if got := agent.Prompted(); !strings.Contains(got, inboxDoorbell) || strings.Contains(got, "rebase again") {
		t.Fatalf("once its turn ended the agent was prompted with %q, want only the inbox doorbell", got)
	}
	read, err := cli.AgentInbox(sent.MessageID, protocol.SessionID(recipient))
	if err != nil || read.Content != "when you surface, rebase again" {
		t.Fatalf("the rung recipient reads its message = %+v, %v", read, err)
	}
}

func TestABurstOfMailRingsOnceAndKeepsItsBodiesOutOfThePrompt(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	recipient, agent := mailIdleAgent(w, app, "shop")
	panes := spawnPanes(w, app, w.Path("sender"), w.Path("reviewer"))
	sender, reviewer := panes[0].session, panes[1].session

	bodies := []string{"the build is green", "the flaky test is quarantined", "the release notes need a line"}
	sendAgentMessage(t, cli, sender, recipient, bodies[0])
	if got := agent.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("the first message prompted %q, want the inbox doorbell", got)
	}
	sendAgentMessage(t, cli, reviewer, recipient, bodies[1])
	sendAgentMessage(t, cli, sender, recipient, bodies[2])
	agent.Reply("On it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, recipient, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	app.TypeLine(recipient, "what is in your inbox?")
	if got := agent.Prompted(); got != "what is in your inbox?" {
		t.Fatalf("after the burst the agent was prompted with %q, want the user's words and no second doorbell", got)
	}
	if got := inboxContents(readInbox(t, cli, recipient, 0).Items); got != strings.Join(bodies, " ") {
		t.Fatalf("one inbox read = %q, want the whole burst oldest first", got)
	}
	if again := readInbox(t, cli, recipient, 0); len(again.Items) != 0 {
		t.Errorf("a second read returned %q again", inboxContents(again.Items))
	}
}

func TestOneInboxReadReturnsGardenAndPeerMailOldestFirstExactlyOnce(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "watcher", "worker", "sender")
		var seeds []string
		for _, title := range []string{"Ship the release", "Fix the build"} {
			seed := plantSeedAs(t, cli, "worker", title)
			if _, err := cli.SeedWatch("watcher", seed, false); err != nil {
				t.Fatalf("watch %s: %v", seed, err)
			}
			seeds = append(seeds, seed)
		}

		var want []string
		for i, seed := range seeds {
			if _, err := cli.SeedNote("worker", seed, "progress on "+seed, "", "", true, nil); err != nil {
				t.Fatalf("note %s: %v", seed, err)
			}
			w.advance(time.Millisecond)
			body := fmt.Sprintf("peer message %d", i)
			sendAgentMessage(t, cli, "sender", "watcher", body)
			w.advance(time.Millisecond)
			want = append(want, seed+" moved: note", body)
		}

		batch := readInbox(t, cli, "watcher", 0)
		if len(batch.Items) != len(want) || batch.Remaining != 0 {
			t.Fatalf("one read = %q with %d remaining, want %d items", inboxContents(batch.Items), batch.Remaining, len(want))
		}
		for i, item := range batch.Items {
			if !strings.Contains(item.Content, want[i]) || item.ReadAt == "" {
				t.Errorf("item %d = %+v, want %q read", i, item, want[i])
			}
			if strings.Contains(item.Content, "progress on") {
				t.Errorf("the garden update %q carries the note's body", item.Content)
			}
		}
		if again := readInbox(t, cli, "watcher", 0); len(again.Items) != 0 {
			t.Errorf("a second read returned %q again", inboxContents(again.Items))
		}
	})
}

func TestAMessageToAShellPaneWaitsInItsInbox(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	shell := w.Spawn(app, fakeagent.Harness(protocol.AgentShellValue), w.Path("shell"))
	sender := spawnPanes(w, app, w.Path("sender"))[0].session

	sent := sendAgentMessage(t, cli, sender, shell, "a delegate reported")
	if sent.Status != protocol.AgentMsgStatusQueued || !strings.Contains(sent.Detail, "shell pane") {
		t.Fatalf("a message to a shell pane = %+v, want queued naming the shell pane", sent)
	}
	app.TypeLine(shell, "echo mail-checked")
	app.AwaitScreen(shell, "mail-checked")
	peek, err := cli.AgentPeek(shell)
	if err != nil || peek.Screen == nil || strings.Contains(peek.Screen.Text, "inbox") {
		t.Fatalf("the shell's screen = %+v, %v; want nothing typed at it", peek, err)
	}
	if got := inboxContents(readInbox(t, cli, shell, 0).Items); got != "a delegate reported" {
		t.Fatalf("the shell pane's inbox = %q, want the message", got)
	}
}

func TestAgentMessageRefusalsNameTheirReason(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "sender", "target")

		if _, err := cli.AgentMsg("nobody", "sender", "hello"); client.ErrorCode(err) != "session_or_crew_member_not_found" ||
			!strings.Contains(err.Error(), `"nobody"`) || !strings.Contains(err.Error(), "attn agent list") || !strings.Contains(err.Error(), "attn crew list") {
			t.Errorf("a message to an unknown address = %v, want session_or_crew_member_not_found naming both lists", err)
		}
		for _, row := range []struct{ name, target, content, want string }{
			{"an empty message", "target", "   ", "empty"},
			{"a message to yourself", "sender", "note to self", "yourself"},
		} {
			refused, err := cli.AgentMsg(row.target, "sender", row.content)
			if err != nil || refused.Status != protocol.AgentMsgStatusRefused || refused.To != protocol.AddressRef("session:"+row.target) || refused.ToName == "" || !strings.Contains(refused.Detail, row.want) {
				t.Errorf("%s = %+v, %v; want a refusal saying %q", row.name, refused, err, row.want)
			}
		}

		var accepted []string
		for i := range 8 {
			body := fmt.Sprintf("update %d", i)
			sendAgentMessage(t, cli, "sender", "target", body)
			accepted = append(accepted, body)
		}
		repeated, err := cli.AgentMsg("target", "sender", "update 7")
		if err != nil || repeated.Status != protocol.AgentMsgStatusRefused || !strings.Contains(repeated.Detail, "already sent") {
			t.Errorf("a repeat of an unread message past the rate limit = %+v, %v; want a refusal saying it was already sent", repeated, err)
		}
		refused, err := cli.AgentMsg("target", "sender", "update 8")
		if err != nil || refused.Status != protocol.AgentMsgStatusRefused || !strings.Contains(refused.Detail, "8") || !strings.Contains(refused.Detail, "30s") {
			t.Fatalf("a ninth message within 30s = %+v, %v; want a refusal naming 8 and 30s", refused, err)
		}
		w.advance(31 * time.Second)
		sendAgentMessage(t, cli, "sender", "target", "update 9")
		accepted = append(accepted, "update 9")

		var received []string
		for _, item := range readInbox(t, cli, "target", 0).Items {
			received = append(received, item.Content)
		}
		slices.Sort(received)
		if !slices.Equal(received, accepted) {
			t.Errorf("the target's inbox = %q, want only the accepted %q", received, accepted)
		}
		if got := readInbox(t, cli, "sender", 0).Items; len(got) != 0 {
			t.Errorf("the sender's inbox = %q, want the refused note to self kept out", inboxContents(got))
		}
	})
}

func TestTheSocketAnswersOversizeMessagesWithTheirLimits(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	panes := spawnPanes(w, app, w.Path("sender"), w.Path("target"))
	sender, target := panes[0].session, panes[1].session

	refused, err := cli.AgentMsg(target, protocol.SessionID(sender), strings.Repeat("x", 32769))
	if err != nil || refused.Status != protocol.AgentMsgStatusRefused || refused.To != protocol.AddressRef("session:"+target) || refused.ToName == "" || !strings.Contains(refused.Detail, "32769") || !strings.Contains(refused.Detail, "32768") {
		t.Errorf("a message one character over the cap = %+v, %v; want a refusal naming 32769 and 32768", refused, err)
	}
	if got := readInbox(t, cli, target, 0).Items; len(got) != 0 {
		t.Errorf("the refused message reached the inbox: %q", inboxContents(got))
	}

	conn, err := w.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go io.WriteString(conn, `{"cmd":"agent_msg","content":"`+strings.Repeat("x", 64*1024)+`"`)
	var answer protocol.Response
	if err := json.NewDecoder(conn).Decode(&answer); err != nil {
		t.Fatalf("the daemon said nothing about a frame past its limit: %v", err)
	}
	if answer.Ok || !strings.Contains(protocol.Deref(answer.Error), "65536") {
		t.Errorf("a frame past the limit = %+v, want a refusal naming 65536 bytes", answer)
	}
}

func TestMessagingACrewMemberReachesItsDayWakingItIfNeeded(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	sender := spawnPanes(w, app, w.Path("sender"))[0].session

	keel := wakeCrew(t, cli, "keel", "")
	keelDay := w.Launched(string(keel.SessionID))
	keelDay.Prompted()
	keelDay.Reply("Morning. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, string(keel.SessionID), func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	toKeel := sendAgentMessage(t, cli, sender, "Keel", "the garden is ready")
	if toKeel.Status != protocol.AgentMsgStatusNotified || protocol.Deref(toKeel.TargetSessionID) != keel.SessionID || toKeel.Detail != "notified Keel" {
		t.Fatalf("a message to the awake Keel = %+v, want notified on its day %s", toKeel, keel.SessionID)
	}
	if got := keelDay.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("Keel's day was prompted with %q, want the inbox doorbell", got)
	}
	if got := crewSessionCount(t, cli); got != 2 {
		t.Fatalf("messaging an awake member left %d sessions, want the sender and its one day", got)
	}

	first := sendAgentMessage(t, cli, sender, "trellis", "please inspect the broken build")
	if first.Status != protocol.AgentMsgStatusQueued || !strings.Contains(first.Detail, "woke Trellis") || protocol.Deref(first.TargetSessionID) == "" {
		t.Fatalf("a message to the sleeping Trellis = %+v, want it queued on a day it woke", first)
	}
	trellisDay := w.Launched(string(protocol.Deref(first.TargetSessionID)))
	second := sendAgentMessage(t, cli, sender, "trellis", "and the flaky test")
	if second.Status != protocol.AgentMsgStatusQueued || protocol.Deref(second.TargetSessionID) != protocol.Deref(first.TargetSessionID) {
		t.Fatalf("a message while Trellis wakes = %+v, want it queued behind the same day", second)
	}
	wake := trellisDay.Prompted()
	if strings.Contains(wake, "broken build") || strings.Contains(wake, "flaky test") || strings.Contains(wake, "attn agent inbox") {
		t.Fatalf("the woken day was primed with %q, want the wake prompt without the mail", wake)
	}
	trellisDay.Reply("Morning. <!-- attn:state=idle -->")
	if got := trellisDay.Prompted(); !strings.Contains(got, inboxDoorbell) {
		t.Fatalf("after its wake turn Trellis was prompted with %q, want the inbox doorbell", got)
	}
	if got := inboxContents(readInbox(t, cli, string(protocol.Deref(first.TargetSessionID)), 0).Items); got != "please inspect the broken build and the flaky test" {
		t.Fatalf("Trellis's inbox = %q, want both messages in order", got)
	}
	trellisDay.Reply("Looking. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, string(protocol.Deref(first.TargetSessionID)), func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	app.TypeLine(string(protocol.Deref(first.TargetSessionID)), "status?")
	if got := trellisDay.Prompted(); got != "status?" {
		t.Fatalf("Trellis was next prompted with %q, want the user's words and no second doorbell", got)
	}
	if got := crewSessionCount(t, cli); got != 3 {
		t.Errorf("sessions = %d, want the sender and one day per member", got)
	}
}

func mailIdleAgent(w *world, app *testworld.Peer, dir string) (string, *fakeagent.Run) {
	w.T.Helper()
	session := w.Spawn(app, fakeagent.Claude, w.Path(dir))
	agent := w.Launched(session)
	idle := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	app.TypeLine(session, "wait for the others")
	agent.Prompted()
	working := testworld.AwaitStateAfter(app, idle, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	agent.Reply("Waiting. <!-- attn:state=idle -->")
	testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	return session, agent
}

func TestMailForAMemberWhoseLaunchFailsWaitsForItsNextDay(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Pi)
	app, cli := w.App(), w.Client()
	pluginDriverSettings(app, "pi")
	sender := spawnPanes(w, app, w.Path("sender"))[0].session
	setCrew(t, cli, "keel", protocol.CrewSetMessage{Agent: protocol.Ptr("pi")})
	before := paneSessions(w)

	allow := w.RefusePiLaunches("pi could not start: the model provider is unreachable")
	if result, err := cli.AgentMsg("keel", protocol.SessionID(sender), "please wake"); err != nil || result.Status != protocol.AgentMsgStatusQueued || !strings.Contains(result.Detail, "provider is unreachable") {
		t.Fatalf("mail for a Keel that cannot wake = %+v, %v; want the launch failure", result, err)
	}
	if bound := crewRosterMember(t, cli, "keel").BindingSession; bound != nil {
		t.Errorf("after its failed wake Keel is bound to %s, want no day", *bound)
	}
	if after := paneSessions(w); !slices.Equal(after, before) {
		t.Errorf("the failed wake left panes for %q, want only %q", after, before)
	}

	allow()
	woke := wakeCrew(t, cli, "keel", "")
	sendAgentMessage(t, cli, sender, "keel", "the build is green")
	day := w.Launched(string(woke.SessionID))
	day.Prompted()
	day.Reply("Morning. <!-- attn:state=idle -->")
	if got := inboxContents(readInbox(t, cli, string(woke.SessionID), 0).Items); got != "please wake the build is green" {
		t.Errorf("Keel's next day reads %q, want the saved mail across its failed wake", got)
	}
}

func paneSessions(w *world) []string {
	var sessions []string
	for _, desktop := range w.App().Initial.Desktops {
		sessions = append(sessions, delegatePaneSessions(desktop)...)
	}
	slices.Sort(sessions)
	return sessions
}

func TestAMessageToAnotherProfilesSessionOrMemberIsRefusedAsNotFound(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		home := app.SelectedProfile()
		side := createProfile(app, "Side")
		w.advance(time.Second)
		selectProfile(app, side.ID)
		writeCrewHomeFile(t, w, "keel", crew.CharterFileName, "# Keel\n\nA member of Side.\n")
		w.restart()
		cli := w.Client()
		registerSessions(t, w, cli, "side-sender", "side-target")
		app = w.App()
		w.advance(time.Second)
		selectProfile(app, home)
		registerSessions(t, w, cli, "home-sender")

		seed := plantSeedAs(t, cli, "side-sender", "A seed owned by Side")
		for _, target := range []string{"side-target", "side-t", "keel", "member:keel", "session:side-target", seed, "seed:" + seed, "Nobody"} {
			if _, err := cli.AgentMsg(target, "home-sender", "hello"); client.ErrorCode(err) != "session_or_crew_member_not_found" {
				t.Errorf("a message from Default to Side's %s = %v, want session_or_crew_member_not_found", target, err)
			}
			scoped := cli.WithRequester("", "home-sender")
			if _, err := scoped.AgentPeek(target); client.ErrorCode(err) != "session_or_crew_member_not_found" {
				t.Errorf("peek %s: %v", target, err)
			}
			if _, err := cli.AgentClose(target, "home-sender", "done"); client.ErrorCode(err) != "session_or_crew_member_not_found" {
				t.Errorf("close %s: %v", target, err)
			}
		}
		sendAgentMessage(t, cli, "side-sender", "side-target", "hello from Side")
	})
}
