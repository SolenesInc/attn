package main_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASnoozeStillWakesWhenANewerStateArrivesBeforeItsReply(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	committed := s.PauseAt(pausepoint.SessionSnoozeCommitted)
	s.Start()
	app, cli := s.App(), s.Client()
	register(t, s, "waiting", "waiting")
	terminal := protocol.TerminalID(app.Terminal("waiting"))
	if err := cli.UpdateState(terminal, protocol.StateWaitingInput); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, "waiting", func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	deadline := time.Now().UTC().Add(time.Second)
	until := deadline.Format(time.RFC3339Nano)
	answered := make(chan error, 1)
	go func() { answered <- s.Client().SnoozeSession("", "waiting", deadline) }()
	committed.Await()
	if err := cli.UpdateState(terminal, protocol.StateWorking); err != nil {
		t.Fatal(err)
	}
	working := testworld.AwaitSession(app, "waiting", func(x protocol.Session) bool {
		return x.State == protocol.SessionStateWorking && protocol.Deref(x.TurnSnoozedUntil) == until
	})
	workedAt, err := time.Parse(time.RFC3339Nano, working.StateUpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.UpdateState(terminal, protocol.StateWaitingInput); err != nil {
		t.Fatal(err)
	}
	newest := testworld.AwaitSession(app, "waiting", func(x protocol.Session) bool {
		updated, err := time.Parse(time.RFC3339Nano, x.StateUpdatedAt)
		return err == nil && updated.After(workedAt) && x.State == protocol.SessionStateWaitingInput && protocol.Deref(x.TurnSnoozedUntil) == until
	})
	committed.Release()
	if err := <-answered; err != nil {
		t.Fatalf("the accepted snooze failed: %v", err)
	}
	listed, err := cli.Query("")
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range listed {
		if session.ID == "waiting" && session.StateUpdatedAt != newest.StateUpdatedAt {
			t.Fatalf("the delayed snooze replaced the newer state: updated_at=%s, want %s", session.StateUpdatedAt, newest.StateUpdatedAt)
		}
	}
	testworld.AwaitSession(app, "waiting", func(x protocol.Session) bool {
		return protocol.Deref(x.TurnOwed) && protocol.Deref(x.TurnSnoozedUntil) == "" && protocol.Deref(x.TurnOpenedAt) == until
	})
}

func TestAgentsThatDieAfterADaemonRestartBreakThroughTheirSnooze(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.App()
	agents := map[string]*fakeagent.Run{}
	for _, repo := range []string{"cart", "checkout", "invoices"} {
		id := s.Spawn(app, fakeagent.Claude, s.Path(repo))
		agent := s.Launched(id)
		agents[id] = agent
		app.TypeLine(id, "add a discount field")
		agent.Prompted()
		agent.Reply("Before or after tax? <!-- attn:state=waiting_input -->")
		testworld.AwaitSession(app, id, func(x protocol.Session) bool {
			return x.State == protocol.SessionStateWaitingInput && protocol.Deref(x.TurnOwed)
		})
		until := time.Now().Add(time.Hour).Format(time.RFC3339Nano)
		app.Send(protocol.SnoozeTurnMessage{Cmd: protocol.CmdSnoozeTurn, SessionID: protocol.SessionID(id), Until: until})
		testworld.AwaitSession(app, id, func(x protocol.Session) bool { return protocol.Deref(x.TurnSnoozedUntil) != "" })
	}

	s.Stop()
	s.Start()
	app = s.App()
	for id, agent := range agents {
		agent.Exit(1)
		testworld.AwaitSession(app, id, func(x protocol.Session) bool {
			return protocol.Deref(x.TurnOwed) && protocol.Deref(x.TurnSnoozedUntil) == ""
		})
	}
}

func TestASnoozeTheDaemonCrashedWhileWakingWakesAfterTheRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.StartCrashingAt("snooze-wake-running")
	app := s.App()
	id := s.Spawn(app, fakeagent.Claude, s.Path("cart"))
	agent := s.Launched(id)
	app.TypeLine(id, "add a discount field")
	agent.Prompted()
	agent.Reply("Before or after tax? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, id, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	snooze(app, id, 100*time.Millisecond)
	s.AwaitCrash()

	s.Start()
	app = s.App()
	var came protocol.Session
	for _, x := range app.Initial.Sessions {
		if string(x.ID) == id {
			came = x
		}
	}
	if !protocol.Deref(came.TurnOwed) {
		came = testworld.AwaitSession(app, id, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	}
	if came.State != protocol.SessionStateWaitingInput || protocol.Deref(came.TurnSnoozedUntil) != "" {
		t.Errorf("after the restart the session is %s, snoozed until %q; want it waiting with its lapsed snooze woken", came.State, protocol.Deref(came.TurnSnoozedUntil))
	}
}

func TestASnoozeWakesOnTimeWhileGitHubHangs(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	asked, release := make(chan struct{}, 1), make(chan struct{})
	github := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		select {
		case asked <- struct{}{}:
		default:
		}
		<-release
		rw.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(github.Close)
	t.Cleanup(func() { close(release) })
	s.Vars = append(s.Vars, "ATTN_MOCK_GH_URL="+github.URL, "ATTN_MOCK_GH_TOKEN=test-token", "ATTN_MOCK_GH_HOST=github.test")
	s.Start()
	app := s.App()

	register(t, s, "shipper", "shipper")
	s.Run(testworld.Invocation{Args: []string{"pr", "watch", "https://github.test/acme/shop/pull/71"}, Session: "shipper"})
	select {
	case <-asked:
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("the daemon never asked GitHub about the watched pull request within %s", fakeagent.HangGuard)
	}

	register(t, s, "waiting", "waiting")
	if err := s.Client().UpdateState(protocol.TerminalID(app.Terminal("waiting")), protocol.StateWaitingInput); err != nil {
		t.Fatalf("waiting reports waiting: %v", err)
	}
	testworld.AwaitSession(app, "waiting", func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	snooze(app, "waiting", 200*time.Millisecond)
	testworld.AwaitSession(app, "waiting", func(x protocol.Session) bool { return protocol.Deref(x.TurnSnoozedUntil) != "" })
	testworld.AwaitSession(app, "waiting", func(x protocol.Session) bool {
		return protocol.Deref(x.TurnOwed) && protocol.Deref(x.TurnSnoozedUntil) == ""
	})
}

func snooze(app *testworld.Peer, id string, lasting time.Duration) {
	until := time.Now().Add(lasting).Format(time.RFC3339Nano)
	app.Send(protocol.SnoozeTurnMessage{Cmd: protocol.CmdSnoozeTurn, SessionID: protocol.SessionID(id), Until: until})
}
