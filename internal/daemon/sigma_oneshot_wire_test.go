package daemon_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func sigmaOnlyWorld(t *testing.T) (*world, *testworld.Peer) {
	t.Helper()
	w := newTitlingWorld(t, fakeagent.Sigma)
	app := w.TrustedApp()
	setSetting(t, app, "sigma_enabled", "true")
	return w, app
}

func sigmaTask(t *testing.T, w *world, asks string) *fakeagent.HeadlessTask {
	t.Helper()
	return sigmaTaskOn(t, w, "", asks)
}

func sigmaTaskOn(t *testing.T, w *world, model, asks string) *fakeagent.HeadlessTask {
	t.Helper()
	task := w.HeadlessTask()
	if task.Harness != fakeagent.Sigma || task.Model != model || task.Effort != "" || !strings.Contains(task.Prompt, asks) {
		t.Fatalf("the daemon asked %s %q at effort %q: %q, want %q at the model's own effort asked about %q", task.Harness, task.Model, task.Effort, task.Prompt, model, asks)
	}
	return task
}

var (
	sigmaAnswers       = protocol.Ptr(true)
	sigmaCannotAnswer  = protocol.Ptr(false)
	sigmaChatGPTModels = []fakeagent.ServerModel{{ID: "gpt-6.1-sol", Name: "GPT-6.1 Sol", Efforts: []string{"low", "high"}}, {ID: "gpt-6-luna", Name: "GPT-6 Luna", Efforts: []string{"low", "high"}}}
	sigmaClaudeModels  = []fakeagent.ServerModel{{ID: "opus", Name: "Opus", Efforts: []string{"low", "high"}}, {ID: "haiku", Name: "Haiku", Efforts: []string{}}}
)

func sigmaDictation(t *testing.T, w *world, app *testworld.Peer, input string) *json.Decoder {
	t.Helper()
	token := pairSenderInApp(t, w.Client(), app, "Dictation")
	conn, err := w.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(fakeagent.HangGuard)); err != nil {
		t.Fatal(err)
	}
	call := protocol.SenderModelCallMessage{
		Cmd: protocol.CmdSenderModelCall, Input: input, LimitMs: int(fakeagent.HangGuard.Milliseconds()), Harness: protocol.Ptr("sigma"),
		Purpose: "dictation", SenderToken: token.Token, System: "Clean up the dictated text.", Tier: protocol.Ptr(protocol.ModelTierLight),
	}
	if err := json.NewEncoder(conn).Encode(call); err != nil {
		t.Fatal(err)
	}
	return json.NewDecoder(conn)
}

func TestASigmaModelCallStartsSigmasServerWhenNoneRuns(t *testing.T) {
	w, app := sigmaOnlyWorld(t)
	setSetting(t, app, "model_calls.agent", "sigma")
	if running := w.AppServers(fakeagent.Sigma); running != 0 {
		t.Fatalf("%d Sigma servers run before any session or call", running)
	}
	answers := sigmaDictation(t, w, app, "so um fix the the login bug")
	sigmaTask(t, w, "so um fix the the login bug").Answer("Fix the login bug.")
	var response protocol.Response
	if err := answers.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if answer := response.SenderModelCallResult; !response.Ok || answer == nil || protocol.Deref(answer.Text) != "Fix the login bug." {
		t.Fatalf("the call with no Sigma server running answered %+v", response)
	}
}

func TestWhenTheBundledOpenAIProviderIsSignedOutSigmaTitlesStopsAndLightCallsGoToTheProviderThatIsSignedIn(t *testing.T) {
	for _, home := range []struct {
		name  string
		ready string
		model string
	}{
		{"a named OpenAI instance", "openai-work", "openai-work/gpt-6-luna"},
		{"Claude", "claude", "claude/haiku"},
	} {
		t.Run(home.name, func(t *testing.T) {
			w := newTitlingWorld(t, fakeagent.Sigma)
			status := func(id string) string {
				if id == home.ready {
					return "ready"
				}
				return "signed_out"
			}
			w.ServeProvidersAtNextBoot(
				fakeagent.ServerProvider{ID: "openai", Status: status("openai"), Models: sigmaChatGPTModels, Ephemeral: sigmaAnswers},
				fakeagent.ServerProvider{ID: "claude", Status: status("claude"), Models: sigmaClaudeModels, Ephemeral: sigmaAnswers},
				fakeagent.ServerProvider{ID: "openai-work", Status: status("openai-work"), Models: sigmaChatGPTModels, Ephemeral: sigmaAnswers},
			)
			app := w.TrustedApp()
			setSetting(t, app, "sigma_enabled", "true")
			setSetting(t, app, "model_calls.agent", "sigma")
			session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
			promptUntilWorking(app, session, view, server, "the login form rejects valid passwords")
			sigmaTaskOn(t, w, home.model, "the login form rejects valid passwords").Answer("Fix login flow")
			awaitLabel(app, session, "Fix login flow")

			server.Reply(view.ConversationID, "The fix is ready. Should I deploy it to staging?")
			sigmaTaskOn(t, w, home.model, "Should I deploy it to staging?").Answer(`{"verdict":"WAITING"}`)
			testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })

			answers := sigmaDictation(t, w, app, "so um fix the the login bug")
			sigmaTaskOn(t, w, home.model, "so um fix the the login bug").Answer("Fix the login bug.")
			var response protocol.Response
			if err := answers.Decode(&response); err != nil {
				t.Fatal(err)
			}
			if answer := response.SenderModelCallResult; !response.Ok || answer == nil || protocol.Deref(answer.Text) != "Fix the login bug." || answer.Model != home.model {
				t.Fatalf("the light call answered %+v, want %s's answer", response, home.model)
			}
		})
	}
}

func TestSettingsRefuseASigmaModelWhoseProviderCannotAnswerABackgroundCall(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Sigma)
	w.ServeProvidersAtNextBoot(
		fakeagent.ServerProvider{ID: "agy", Status: "ready", Models: []fakeagent.ServerModel{{ID: "gemini-4-neon", Name: "Gemini 4 Neon", Efforts: []string{}}}, Ephemeral: sigmaCannotAnswer},
		fakeagent.ServerProvider{ID: "openai", Status: "ready", Models: sigmaChatGPTModels, Ephemeral: sigmaAnswers},
	)
	app := w.TrustedApp()
	setSetting(t, app, "sigma_enabled", "true")
	catalog := harnessCatalog(app, "sigma", false)
	oneShot := map[string]protocol.ModelCapabilitySupport{}
	for _, m := range catalog.Models {
		oneShot[m.Provider+"/"+m.ID] = m.OneShot
	}
	if oneShot["agy/gemini-4-neon"] != protocol.ModelCapabilitySupportUnsupported || oneShot["openai/gpt-6-luna"] != protocol.ModelCapabilitySupportSupported {
		t.Fatalf("Sigma's catalog says of background calls %v, want agy unable and openai able", oneShot)
	}
	for _, key := range []string{"activity.config", "garden.advisor"} {
		requestID := uuid.NewString()
		refused := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: key, Value: `{"agent":"sigma","model":"agy/gemini-4-neon"}`, RequestID: protocol.Ptr(requestID)},
			protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
		if protocol.Deref(refused.Success) || !strings.Contains(protocol.Deref(refused.Error), "agy/gemini-4-neon cannot answer a background call") {
			t.Errorf("%s with agy's model = success %v, error %q; want it refused naming the model", key, protocol.Deref(refused.Success), protocol.Deref(refused.Error))
		}
		setSetting(t, app, key, `{"agent":"sigma","model":"openai/gpt-6-luna"}`)
	}
}

func TestAProviderSignedInAfterSigmaListedItsModelsAnswersBackgroundCalls(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Sigma)
	w.ServeProvidersAtNextBoot(
		fakeagent.ServerProvider{ID: "claude", Status: "ready", Models: sigmaClaudeModels, Ephemeral: sigmaAnswers},
		fakeagent.ServerProvider{ID: "openai-work", Status: "signed_out", Models: sigmaChatGPTModels, Ephemeral: sigmaAnswers},
	)
	app := w.TrustedApp()
	setSetting(t, app, "sigma_enabled", "true")
	requestID := uuid.NewString()
	refused := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: "activity.config", Value: `{"agent":"sigma","model":"openai-work/gpt-6-luna"}`, RequestID: protocol.Ptr(requestID)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	if protocol.Deref(refused.Success) {
		t.Fatal("the activity line took a model of a provider that is signed out")
	}

	w.AppServer(fakeagent.Sigma).SignIn("openai-work")
	setSetting(t, app, "activity.config", `{"agent":"sigma","model":"openai-work/gpt-6-luna"}`)
	for _, m := range harnessCatalog(app, "sigma", false).Models {
		if m.Provider == "openai-work" && m.Access == protocol.ModelCapabilitySupportUnsupported {
			t.Fatalf("after signing in, Sigma's catalog still refuses %s/%s", m.Provider, m.ID)
		}
	}
}

func TestRefreshOffersTheModelsOfAProviderSignedInWhileSigmaRuns(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Sigma)
	w.ServeProvidersAtNextBoot(
		fakeagent.ServerProvider{ID: "claude", Status: "signed_out", Models: sigmaClaudeModels, Ephemeral: sigmaAnswers},
		fakeagent.ServerProvider{ID: "openai-work", Status: "ready", Models: sigmaChatGPTModels, Ephemeral: sigmaAnswers},
	)
	app := w.TrustedApp()
	setSetting(t, app, "sigma_enabled", "true")
	claudeAccess := func(refresh bool) []protocol.ModelCapabilitySupport {
		var access []protocol.ModelCapabilitySupport
		for _, m := range harnessCatalog(app, "sigma", refresh).Models {
			if m.Provider == "claude" {
				access = append(access, m.Access)
			}
		}
		return access
	}
	if access := claudeAccess(false); !slices.Contains(access, protocol.ModelCapabilitySupportUnsupported) {
		t.Fatalf("before signing in, Sigma's Claude models have access %v, want them refused", access)
	}

	w.AppServer(fakeagent.Sigma).SignIn("claude")
	if access := claudeAccess(true); len(access) == 0 || slices.Contains(access, protocol.ModelCapabilitySupportUnsupported) {
		t.Fatalf("after signing in and pressing Refresh, Sigma's Claude models have access %v, want them offered", access)
	}
	for _, key := range []string{"activity.config", "garden.advisor"} {
		setSetting(t, app, key, `{"agent":"sigma","model":"claude/haiku"}`)
	}
}

func TestOnAHomeWithOnlySigmaASigmaSessionIsTitledAndItsStopClassifiedBySigma(t *testing.T) {
	w, app := sigmaOnlyWorld(t)
	session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	promptUntilWorking(app, session, view, server, "the login form rejects valid passwords")
	sigmaTask(t, w, "the login form rejects valid passwords").Answer("Fix login flow")
	awaitLabel(app, session, "Fix login flow")

	server.Reply(view.ConversationID, "The fix is ready. Should I deploy it to staging?")
	sigmaTask(t, w, "Should I deploy it to staging?").Answer(`{"verdict":"WAITING"}`)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
}

func TestOnAHomeWithOnlySigmaTheActivityLineIsWrittenBySigma(t *testing.T) {
	w, app := sigmaOnlyWorld(t)
	setSetting(t, app, "activity.enabled", "true")
	setSetting(t, app, "activity.config", `{"agent":"sigma"}`)
	session, view, server := appServerSession(t, w, app, fakeagent.Sigma, func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("checkout")
	})
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	app.Send(protocol.SetClientPresenceMessage{Cmd: protocol.CmdSetClientPresence, Visible: true, DashboardVisible: true})
	serverActivityTurn(t, w, app, view, server, session, "start on the tests", "Starting on the tests.")
	awaitActivityTask(app, session, func(task protocol.Task) bool { return task.State == "done" })

	serverActivityTurn(t, w, app, view, server, session, "run the frontend tests", "The frontend suite is running.")
	sigmaTask(t, w, "The frontend suite is running.").Answer("Running the frontend test suite.")
	awaitActivity(app, session, "Running the frontend test suite")

	serverActivityTurn(t, w, app, view, server, session, "run the whole suite", "The whole suite is running.")
	sigmaTask(t, w, "The whole suite is running.").Fail("not logged in")
	awaitActivityTask(app, session, func(task protocol.Task) bool { return task.State == "failed" })
	if failed := activitySession(t, w.Client(), session); !strings.Contains(protocol.Deref(failed.Error), "authentication failed") || protocol.Deref(failed.Activity) != "Running the frontend test suite" {
		t.Errorf("activity after Sigma failed = error %q beside line %q, want the failure beside the last good line", protocol.Deref(failed.Error), protocol.Deref(failed.Activity))
	}
}

func TestSettingsOfferSigmaForBackgroundCallsAndAnAbandonedDictationStopsItsSigmaRun(t *testing.T) {
	w, app := sigmaOnlyWorld(t)
	if !awaitHarnessOffered(app, "sigma").Options.OneShot {
		t.Fatal("sigma one_shot = false, want Sigma offered for background model calls")
	}
	setSetting(t, app, "activity.config", `{"agent":"sigma"}`)
	setSetting(t, app, "garden.advisor", `{"agent":"sigma"}`)
	setSetting(t, app, "model_calls.agent", "sigma")
	session, _, _ := appServerSession(t, w, app, fakeagent.Sigma, func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("checkout")
	})
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	token := pairSenderInApp(t, w.Client(), app, "Dictation")
	conn, err := w.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(fakeagent.HangGuard)); err != nil {
		t.Fatal(err)
	}
	call := protocol.SenderModelCallMessage{
		Cmd: protocol.CmdSenderModelCall, Input: "so um fix the the login bug", LimitMs: int(fakeagent.HangGuard.Milliseconds()),
		Purpose: "dictation", SenderToken: token.Token, System: "Clean up the dictated text.", Tier: protocol.Ptr(protocol.ModelTierLight),
	}
	if err := json.NewEncoder(conn).Encode(call); err != nil {
		t.Fatal(err)
	}
	sigmaTask(t, w, "so um fix the the login bug").Answer("Fix the login bug.")
	var response protocol.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if answer := response.SenderModelCallResult; !response.Ok || answer == nil || answer.Harness != "sigma" || protocol.Deref(answer.Text) != "Fix the login bug." {
		t.Fatalf("the dictation call answered %+v, want Sigma's refined text", response)
	}

	abandoning, err := w.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	call.Input = "and the uh signup page"
	if err := json.NewEncoder(abandoning).Encode(call); err != nil {
		t.Fatal(err)
	}
	abandoned := sigmaTask(t, w, "and the uh signup page")
	_ = abandoning.Close()
	abandoned.AwaitExited()
}
