package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func awaitHarnessOffered(app *testworld.Peer, agent string) protocol.Harness {
	app.T.Helper()
	var found protocol.Harness
	testworld.Await(app, protocol.EventHarnessCatalog, func(m protocol.HarnessCatalogMessage) bool {
		h, ok := testworld.FindHarness(m.Harnesses, agent)
		if ok && h.Offered && h.Installation == protocol.HarnessInstallationInstalled {
			found = h
			return true
		}
		return false
	})
	return found
}

func TestAPluginDriverPublishesItsCapabilitiesAndIsRefusedLaunchesBeyondThem(t *testing.T) {
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	plug := awaitHarnessOffered(app, "plug")
	if !plug.Options.InitialPrompt || !plug.Options.Resume || plug.Options.ModelPin || plug.Options.EffortPin {
		t.Errorf("the app sees plug options %+v, want initial_prompt and resume without model or effort pins", plug.Options)
	}

	for _, row := range []struct {
		name    string
		launch  func(*protocol.SpawnSessionMessage)
		refusal []string
	}{
		{name: "pinned-model", launch: func(m *protocol.SpawnSessionMessage) { m.Model = protocol.Ptr("gpt-5") },
			refusal: []string{"does not support model pins"}},
		{name: "pinned-effort", launch: func(m *protocol.SpawnSessionMessage) { m.Effort = protocol.Ptr("low") },
			refusal: []string{"does not support effort pins"}},
	} {
		refused := refuseSpawnLikeTheApp(w, app, fakeagent.Plug, w.Path(row.name), row.launch)
		for _, want := range row.refusal {
			if !strings.Contains(protocol.Deref(refused.Error), want) {
				t.Errorf("%s: the spawn was refused with %q, want it to say %q", row.name, protocol.Deref(refused.Error), want)
			}
		}
	}
}

func TestAPluginDriverWithoutResumeIsRefusedANamedConversation(t *testing.T) {
	t.Setenv(fakeagent.PlugWithoutResumeEnv, "1")
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	if awaitHarnessOffered(app, "plug").Options.Resume {
		t.Fatal("the app sees plug resume = true, want false from a driver registered without resume")
	}
	refused := refuseSpawnLikeTheApp(w, app, fakeagent.Plug, w.Path("named-conversation"), func(m *protocol.SpawnSessionMessage) {
		m.ResumeSessionID = protocol.Ptr("conv-1")
	})
	for _, want := range []string{"conv-1", "does not support resume"} {
		if !strings.Contains(protocol.Deref(refused.Error), want) {
			t.Errorf("the spawn was refused with %q, want it to say %q", protocol.Deref(refused.Error), want)
		}
	}
}

func TestAPluginSessionFollowsItsDriversReportsAndResumesItsConversationAfterItExits(t *testing.T) {
	t.Setenv(fakeagent.PlugCapabilitiesEnv, "resume_availability=false")
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	awaitHarnessOffered(app, "plug")
	cwd := w.Path("shop")
	session := w.Spawn(app, fakeagent.Plug, cwd, func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("price the checkout")
	})
	launched := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return protocol.Deref(s.LastModelRequestAt) != "" })
	first := w.Launched(session)
	if got := first.Prompted(); got != "price the checkout" {
		t.Fatalf("plug received %q", got)
	}
	working := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	if requested, launch := protocol.Timestamp(protocol.Deref(working.LastModelRequestAt)).Time(), protocol.Timestamp(protocol.Deref(launched.LastModelRequestAt)).Time(); !requested.After(launch) {
		t.Errorf("the working report left last_model_request_at at %s, want it after the launch stamp %s", requested, launch)
	}
	first.Reply("Priced at 42. Which currency? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

	first.Exit(7)
	exited := testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == session })
	if exited.ExitCode != 7 {
		t.Errorf("session_exited carries exit code %d, want plug's 7", exited.ExitCode)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	relaunchedBy := w.App()
	w.Spawn(relaunchedBy, fakeagent.Plug, cwd, func(m *protocol.SpawnSessionMessage) { m.ID = protocol.SessionID(session) })
	second := w.Launched(session)
	if !second.Resumed || second.ConversationID != first.ConversationID {
		t.Fatalf("the relaunch ran plug %q, want its driver resuming conversation %s from the metadata it reported", second.Argv, first.ConversationID)
	}
	relaunchedBy.TypeLine(session, "now in euros")
	if got := second.Prompted(); got != "now in euros" {
		t.Fatalf("the relaunched plug received %q", got)
	}
	testworld.AwaitSession(relaunchedBy, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	second.Reply("Priced at 39 euros. <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(relaunchedBy, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}
