package main_test

import (
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func pairPhone(t *testing.T, app *testworld.Peer, cloud *testworld.Cloud, profileID, name string) *testworld.Phone {
	t.Helper()
	qr := testworld.ParsePhoneQR(t, beginPairing(t, app, profileID, "pair-"+name).Qr)
	phone := testworld.NewPhone(t, cloud, name)
	socket, err := phone.Dial(qr)
	if err != nil {
		t.Fatal(err)
	}
	if err := socket.Pair(qr); err != nil {
		t.Fatal(err)
	}
	socket.Hello()
	socket.RegisterForPush()
	return phone
}

type openedPush struct {
	phone string
	note  testworld.PushNote
}

func awaitPushesUntil(t *testing.T, cloud *testworld.Cloud, phones []*testworld.Phone, last func(openedPush) bool) []openedPush {
	t.Helper()
	var opened []openedPush
	guard := time.After(fakeagent.HangGuard)
	for {
		var push testworld.Push
		select {
		case push = <-cloud.Pushes():
		case <-guard:
			t.Fatalf("the awaited push did not reach the relay within fakeagent.HangGuard=%s; pushes so far %+v", fakeagent.HangGuard, opened)
		}
		i := slices.IndexFunc(phones, func(p *testworld.Phone) bool { return p.SignPub() == push.Device })
		if i < 0 {
			t.Fatalf("a push went to device %s, which no test phone owns", push.Device)
		}
		note, err := phones[i].OpenPush(push)
		if err != nil {
			t.Fatalf("%s could not open its push: %v", phones[i].DeviceID(), err)
		}
		got := openedPush{phone: phones[i].DeviceID(), note: note}
		opened = append(opened, got)
		if last(got) {
			return opened
		}
	}
}

func TestAgentsOwedATurnAndAsksPushToEveryPhoneOfTheirProfileOnly(t *testing.T) {
	t.Parallel()
	cloud := testworld.StartCloud(t)
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "ATTN_CLOUD_URL="+cloud.HomeURL)
	s.Start()
	app := s.App()
	home := app.SelectedProfile()
	enableAsks(t, app)
	setPhoneAccess(t, app, true)
	testworld.Await(app, protocol.EventPhonesUpdated, linkConnected)
	created := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, Name: "Work", RequestID: "work"},
		protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == "work" })
	if created.Profile == nil {
		t.Fatalf("profile_create: %s", protocol.Deref(created.Error))
	}
	work := created.Profile.ID

	phones := []*testworld.Phone{
		pairPhone(t, app, cloud, home, "iPhone"),
		pairPhone(t, app, cloud, home, "iPad"),
		pairPhone(t, app, cloud, work, "Work phone"),
	}

	chief := s.Spawn(app, fakeagent.Claude, s.Path("chief"), labelled("chief"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) })
	chiefAgent := s.Launched(chief)
	app.TypeLine(chief, "triage the inbox")
	chiefAgent.Prompted()
	chiefAgent.Reply("Triaged. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, chief, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })

	builder := s.Spawn(app, fakeagent.Claude, s.Path("shop"), labelled("builder"))
	claude := s.Launched(builder)
	app.TypeLine(builder, "land the discount field")
	claude.Prompted()
	claude.Reply("Tests pass.\n\nShould I land this? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, builder, func(x protocol.Session) bool { return x.State == protocol.SessionStateWaitingInput })

	selected := testworld.Request(app, protocol.ProfileSelectMessage{Cmd: protocol.CmdProfileSelect, ProfileID: work, RequestID: "select-work"},
		protocol.EventProfileActionResult, func(m protocol.ProfileActionResultMessage) bool { return m.RequestID == "select-work" })
	if selected.Error != nil {
		t.Fatalf("profile_select: %s", *selected.Error)
	}
	reviewer := s.Spawn(app, fakeagent.Claude, s.Path("docs"), labelled("reviewer"), func(m *protocol.SpawnSessionMessage) { m.ProfileID = work })
	other := s.Launched(reviewer)
	app.TypeLine(reviewer, "review the docs")
	other.Prompted()
	other.Reply("Reviewed. <!-- attn:state=waiting_input -->")

	opened := awaitPushesUntil(t, cloud, phones, func(p openedPush) bool { return p.note.SessionID == reviewer })
	want := []openedPush{
		{phone: phones[0].DeviceID(), note: testworld.PushNote{SessionID: builder, ProfileID: home, Title: "builder", Kind: "waiting", Text: "Should I land this?"}},
		{phone: phones[1].DeviceID(), note: testworld.PushNote{SessionID: builder, ProfileID: home, Title: "builder", Kind: "waiting", Text: "Should I land this?"}},
		{phone: phones[2].DeviceID(), note: testworld.PushNote{SessionID: reviewer, ProfileID: work, Title: "reviewer", Kind: "waiting", Text: "Reviewed."}},
	}
	got := make([]openedPush, len(opened))
	for i, p := range opened {
		got[i] = openedPush{phone: p.phone, note: testworld.PushNote{SessionID: p.note.SessionID, ProfileID: p.note.ProfileID, Title: p.note.Title, Kind: p.note.Kind, Text: p.note.Text}}
		if p.note.V != 1 || p.note.HomeID == "" || p.note.HomeName == "" || p.note.ProfileName == "" {
			t.Errorf("push %d names no home or profile: %+v", i, p.note)
		}
	}
	slices.SortStableFunc(got[:len(got)-1], func(a, b openedPush) int { return cmpPhone(phones, a.phone, b.phone) })
	if !slices.Equal(got, want) {
		t.Fatalf("pushes = %+v\nwant one per phone of each session's profile, in order, and none for the chief, who is never owed a turn: %+v", got, want)
	}

	asked := s.Run(testworld.Invocation{Args: []string{"ask"}, Session: builder, Stdin: ":::question\nWhich store keeps the cache?\n- [ ] Postgres\n- [ ] Redis\n:::\n"})
	askID := regexp.MustCompile(`^Asked (a-[0-9a-z]{6})`).FindStringSubmatch(asked.Stdout)
	if asked.Code != 0 || askID == nil {
		t.Fatalf("attn ask exited %d with stdout %q and stderr %q", asked.Code, asked.Stdout, asked.Stderr)
	}
	seen := 0
	asks := awaitPushesUntil(t, cloud, phones, func(openedPush) bool { seen++; return seen == 2 })
	for _, p := range asks {
		if wantAsk := (testworld.PushNote{V: 1, HomeID: p.note.HomeID, HomeName: p.note.HomeName, ProfileID: home, ProfileName: p.note.ProfileName, SessionID: builder, Title: "builder", Kind: "ask", Text: "Which store keeps the cache?", AskID: askID[1]}); p.note != wantAsk || p.phone == phones[2].DeviceID() {
			t.Errorf("ask push to %s = %+v, want %+v to a phone of the asking session's profile", p.phone, p.note, wantAsk)
		}
	}
	if asks[0].phone == asks[1].phone {
		t.Errorf("both ask pushes went to %s, want one to each phone of the profile", asks[0].phone)
	}
}

func cmpPhone(phones []*testworld.Phone, a, b string) int {
	index := func(id string) int {
		return slices.IndexFunc(phones, func(p *testworld.Phone) bool { return p.DeviceID() == id })
	}
	return index(a) - index(b)
}

func TestEveryApprovalAnAgentNeedsPushesOnItsOwn(t *testing.T) {
	t.Parallel()
	cloud := testworld.StartCloud(t)
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Vars = append(s.Vars, "ATTN_CLOUD_URL="+cloud.HomeURL)
	s.Start()
	app := s.App()
	setSetting(t, app, "auto_approve_enabled", "false")
	setPhoneAccess(t, app, true)
	testworld.Await(app, protocol.EventPhonesUpdated, linkConnected)
	phones := []*testworld.Phone{pairPhone(t, app, cloud, app.SelectedProfile(), "iPhone")}
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"), labelled("migrator"))
	codex := s.Launched(session)
	next := func() testworld.PushNote {
		t.Helper()
		return awaitPushesUntil(t, cloud, phones, func(openedPush) bool { return true })[0].note
	}

	app.TypeLine(session, "plan the release")
	codex.Prompted()
	codex.Reply("Should I ship version one? <!-- attn:state=waiting_input -->")
	if note := next(); note.Kind != "waiting" || note.Text != "Should I ship version one?" {
		t.Fatalf("the question pushed %+v", note)
	}

	app.TypeLine(session, "run the migration")
	codex.Prompted()
	codex.AskApproval()
	if note := next(); note.Kind != "needs_approval" || note.Text != "" {
		t.Fatalf("the first approval pushed %+v, want needs_approval without the earlier question", note)
	}
	app.AwaitScreen(session, "Allow the command to run?")
	answered := len(app.Log())
	app.TypeLine(session, "1")
	if codex.Answered() == "" {
		t.Fatal("the first approval took no answer")
	}
	testworld.AwaitSessionSince(app, answered, session, func(x protocol.Session) bool { return x.State != protocol.SessionStatePendingApproval })
	codex.AskApproval()
	if note := next(); note.Kind != "needs_approval" || note.SessionID != session {
		t.Fatalf("the second approval in the same turn pushed %+v, want its own needs_approval push", note)
	}
}

func startPhoneHome(t *testing.T) (*testworld.Stack, *testworld.Peer, *testworld.Cloud, []*testworld.Phone, string) {
	t.Helper()
	cloud := testworld.StartCloud(t)
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Vars = append(s.Vars, "ATTN_CLOUD_URL="+cloud.HomeURL)
	s.Start()
	app := s.App()
	enableAsks(t, app)
	setPhoneAccess(t, app, true)
	testworld.Await(app, protocol.EventPhonesUpdated, linkConnected)
	phones := []*testworld.Phone{pairPhone(t, app, cloud, app.SelectedProfile(), "iPhone")}
	session := s.Spawn(app, fakeagent.Claude, s.Path("shop"), labelled("builder"))
	s.Launched(session)
	return s, app, cloud, phones, session
}

func openAsk(t *testing.T, s *testworld.Stack, session, question string) string {
	t.Helper()
	asked := s.Run(testworld.Invocation{Args: []string{"ask"}, Session: session, Stdin: ":::question\n" + question + "\n- [ ] Yes\n- [ ] No\n:::\n"})
	askID := regexp.MustCompile(`^Asked (a-[0-9a-z]{6})`).FindStringSubmatch(asked.Stdout)
	if asked.Code != 0 || askID == nil {
		t.Fatalf("attn ask exited %d with stdout %q and stderr %q", asked.Code, asked.Stdout, asked.Stderr)
	}
	return askID[1]
}

func awaitLinkDown(t *testing.T, app *testworld.Peer, since int) {
	t.Helper()
	testworld.AwaitSince(app, since, protocol.EventPhonesUpdated, func(m protocol.PhonesUpdatedMessage) bool {
		return m.Link.State != protocol.PhoneLinkStateConnected
	})
}

func TestAnAskOpenedWhileTheLinkIsDownPushesWhenItReturns(t *testing.T) {
	t.Parallel()
	s, app, cloud, phones, session := startPhoneHome(t)

	before := len(app.Log())
	restore := cloud.Outage()
	awaitLinkDown(t, app, before)
	askID := openAsk(t, s, session, "Ship during the gap?")
	restore()

	note := awaitPushesUntil(t, cloud, phones, func(openedPush) bool { return true })[0].note
	if note.Kind != "ask" || note.AskID != askID {
		t.Fatalf("the push after the link returned = %+v, want ask %s", note, askID)
	}
}

func TestAnAskThatOutlivesTheFreshnessLimitDuringAnOutageNeverPushes(t *testing.T) {
	t.Parallel()
	s, app, cloud, phones, session := startPhoneHome(t)

	before := len(app.Log())
	restore := cloud.Outage()
	awaitLinkDown(t, app, before)
	openAsk(t, s, session, "Ship during the gap?")
	s.AwaitTimer(daemon.PhonePushLinkWaitTimer)
	s.Advance(2 * time.Minute)

	back := len(app.Log())
	restore()
	testworld.AwaitSince(app, back, protocol.EventPhonesUpdated, linkConnected)
	later := openAsk(t, s, session, "Ship after the gap?")
	if note := awaitPushesUntil(t, cloud, phones, func(openedPush) bool { return true })[0].note; note.AskID != later {
		t.Fatalf("the first push after the outage = %+v, want only the later ask %s", note, later)
	}
}

func TestAnAskAnsweredDuringAnOutageNeverPushes(t *testing.T) {
	t.Parallel()
	s, app, cloud, phones, session := startPhoneHome(t)

	before := len(app.Log())
	restore := cloud.Outage()
	awaitLinkDown(t, app, before)
	answeredID := openAsk(t, s, session, "Ship during the gap?")
	s.AwaitTimer(daemon.PhonePushLinkWaitTimer)
	answered := testworld.Request(app, protocol.AskAnswerMessage{
		Cmd: protocol.CmdAskAnswer, RequestID: "answer", AskID: answeredID, Answers: []protocol.AskAnswer{{Key: "q1", Picked: []string{"Yes"}}},
	}, protocol.EventAskAnswerResult, func(r protocol.AskAnswerResultMessage) bool { return r.RequestID == "answer" })
	if answered.Error != nil {
		t.Fatalf("ask_answer: %s", *answered.Error)
	}

	restore()
	later := openAsk(t, s, session, "Ship after the gap?")
	if note := awaitPushesUntil(t, cloud, phones, func(openedPush) bool { return true })[0].note; note.AskID != later {
		t.Fatalf("the first push after the outage = %+v, want only the unanswered ask %s", note, later)
	}
}

func TestADaemonKilledDuringAnOutageKeepsThePendingPush(t *testing.T) {
	t.Parallel()
	s, app, cloud, phones, session := startPhoneHome(t)

	before := len(app.Log())
	restore := cloud.Outage()
	awaitLinkDown(t, app, before)
	askID := openAsk(t, s, session, "Ship during the gap?")
	s.AwaitTimer(daemon.PhonePushLinkWaitTimer)

	s.Stop()
	s.Start()
	restore()

	note := awaitPushesUntil(t, cloud, phones, func(openedPush) bool { return true })[0].note
	if note.Kind != "ask" || note.AskID != askID {
		t.Fatalf("the push after the kill and restart = %+v, want ask %s", note, askID)
	}
}
