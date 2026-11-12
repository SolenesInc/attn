package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const serverPluginAgent = "other-server-harness"

func serverDriverDeclaration(w *world) map[string]any {
	return map[string]any{
		"protocol": "attn-app-server-v1", "executable": filepath.Join(w.Dir, "bin", "sigma"),
		"serve_args": []string{"serve"}, "home_env": "SIGMA_HOME", "default_home": ".sigma", "socket_name": "sigma.sock",
		"initialize":              map[string]any{"server": "sigma", "protocol": map[string]int{"exact": 1}},
		"caller_conversation_env": "SIGMA_ROOT_CONVERSATION", "on_close": "leave-running", "recovery": "numbered-completions", "targets": true,
	}
}

func registerServerDriver(t *testing.T, w *world, name string, declaration map[string]any) *driverPeer {
	t.Helper()
	d := dialDriver(t, w, name)
	d.agent = serverPluginAgent
	d.mu.Lock()
	d.holdLaunch = true
	d.mu.Unlock()
	if err := d.call("driver.register", map[string]any{"agent": serverPluginAgent, "capabilities": map[string]bool{"server_binding": true, "resume": true}, "server": declaration}, &d.registered); err != nil {
		t.Fatal(err)
	}
	return d
}

func spawnPluginServerView(w *world, app *testworld.Peer, driver *driverPeer, edit ...func(*protocol.SpawnSessionMessage)) (string, *fakeagent.Run) {
	w.T.Helper()
	id := uuid.NewString()
	if err := os.MkdirAll(w.Path("shop"), 0700); err != nil {
		w.T.Fatal(err)
	}
	msg := protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: protocol.SessionID(id), Agent: serverPluginAgent, Cwd: w.Path("shop"), ProfileID: app.SelectedProfile(), Cols: 80, Rows: 24}
	for _, f := range edit {
		f(&msg)
	}
	app.Send(msg)
	var params struct {
		Conversation string `json:"conversation_id"`
		Target       string `json:"execution_target"`
	}
	call := driver.asked("driver.spawn", &params)
	if params.Conversation == "" {
		w.T.Fatal("server view launch has no conversation_id")
	}
	argv := []string{filepath.Join(w.Dir, "bin", "sigma"), "--attach", "--conversation", params.Conversation}
	if params.Target != "" {
		argv = append(argv, "--target", params.Target)
	}
	driver.answer(call, map[string]any{"argv": argv})
	result := testworld.Await(app, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == msg.ID })
	if !result.Success {
		w.T.Fatalf("server view refused: %s", protocol.Deref(result.Error))
	}
	return id, w.Launched(id)
}

func TestAPluginServerBindingUsesTheSharedServerForInputModelsAndTargets(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	driver := registerServerDriver(t, w, "server-example", serverDriverDeclaration(w))
	awaitDriverAvailable(app, serverPluginAgent)
	session, view := spawnPluginServerView(w, app, driver, func(m *protocol.SpawnSessionMessage) { m.ExecutionTarget = protocol.Ptr("local") })
	server := w.AppServer(fakeagent.Sigma)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	delivery := uuid.NewString()
	for range 2 {
		r := submitSessionAnnotationFeedbackAs(app, session, delivery, "check the total")
		if !r.Success || r.Status != "delivered" {
			t.Fatalf("direct input: %+v", r)
		}
	}
	got := server.Prompted(view.ConversationID)
	if got.Text != "check the total" || got.Author == "attn" || got.RequestID == "" {
		t.Fatalf("user input: %+v", got)
	}
	if n := server.Submissions(view.ConversationID); n != 1 {
		t.Fatalf("repeated input admitted %d submissions", n)
	}
	server.Reply(view.ConversationID, "Checked. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	source := w.Spawn(app, shellHarness, w.Path("peer"))
	if _, err := w.Client().AgentMsg(session, protocol.SessionID(source), "review complete"); err != nil {
		t.Fatal(err)
	}
	if got := server.Prompted(view.ConversationID); got.Author != "attn" {
		t.Fatalf("notice author: %+v", got)
	}
	models := testworld.Request(app, protocol.HarnessModelsMessage{Cmd: protocol.CmdHarnessModels, Harness: serverPluginAgent, RequestID: "models"}, protocol.EventHarnessModelsResult, func(r protocol.HarnessModelsResultMessage) bool { return r.RequestID == "models" })
	if !models.Success || len(models.Models) == 0 || models.Models[0].Harness != serverPluginAgent {
		t.Fatalf("models: %+v", models)
	}
	targets := testworld.Request(app, protocol.HarnessTargetsMessage{Cmd: protocol.CmdHarnessTargets, Harness: serverPluginAgent, Cwd: w.Path("shop"), RequestID: "targets"}, protocol.EventHarnessTargetsResult, func(r protocol.HarnessTargetsResultMessage) bool { return r.RequestID == "targets" })
	if !targets.Success || len(targets.Targets) == 0 {
		t.Fatalf("targets: %+v", targets)
	}
	driver.leave(w)
	if r := submitSessionAnnotationFeedbackAs(app, session, uuid.NewString(), "after plugin disconnect"); !r.Success {
		t.Fatalf("disconnected delivery: %+v", r)
	}
	server.Prompted(view.ConversationID)
	if n := w.AppServers(fakeagent.Sigma); n != 1 {
		t.Fatalf("started %d servers", n)
	}
}

func TestAPluginServerDeclarationRejectsUnknownProtocolsCapabilitiesAndOwners(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	d := dialDriver(t, w, "owner")
	d.agent = serverPluginAgent
	declaration := serverDriverDeclaration(w)
	for _, bad := range []struct {
		key   string
		value any
		want  string
	}{
		{"protocol", "another-protocol", "unsupported server binding protocol"},
		{"on_close", "forget", "unsupported server on_close"},
		{"recovery", "guess", "unsupported server recovery"},
	} {
		before := declaration[bad.key]
		declaration[bad.key] = bad.value
		err := d.call("driver.register", map[string]any{"agent": serverPluginAgent, "capabilities": map[string]bool{"server_binding": true}, "server": declaration}, nil)
		if err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Fatalf("%s: %v", bad.key, err)
		}
		declaration[bad.key] = before
	}
	if err := d.call("driver.register", map[string]any{"agent": serverPluginAgent, "capabilities": map[string]bool{"server_binding": true, "message_delivery": true}, "server": declaration}, nil); err == nil {
		t.Fatal("accepted competing delivery handler")
	}
	if err := d.call("driver.register", map[string]any{"agent": serverPluginAgent, "capabilities": map[string]bool{"server_binding": true}, "server": declaration}, nil); err != nil {
		t.Fatal(err)
	}
	awaitDriverAvailable(app, serverPluginAgent)
	d.leave(w)
	rival := dialDriver(t, w, "rival")
	for _, capabilities := range []map[string]bool{{}, {"server_binding": true}} {
		params := map[string]any{"agent": serverPluginAgent, "capabilities": capabilities}
		if capabilities["server_binding"] {
			params["server"] = declaration
		}
		if err := rival.call("driver.register", params, nil); err == nil || !strings.Contains(err.Error(), "belongs to plugin") {
			t.Fatalf("owner collision: %v", err)
		}
	}
}

func TestAPluginServerHandshakeMismatchFailsBeforeLaunchingAView(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	declaration := serverDriverDeclaration(w)
	declaration["initialize"] = map[string]any{"server": "sigma", "protocol": map[string]int{"exact": 99}}
	registerServerDriver(t, w, "wrong-protocol", declaration)
	awaitDriverAvailable(app, serverPluginAgent)
	result := testworld.Request(app, protocol.HarnessModelsMessage{Cmd: protocol.CmdHarnessModels, Harness: serverPluginAgent, RequestID: "wrong"}, protocol.EventHarnessModelsResult, func(r protocol.HarnessModelsResultMessage) bool { return r.RequestID == "wrong" })
	if result.Success || !strings.Contains(protocol.Deref(result.Error), "protocol") {
		t.Fatalf("incompatible server: %+v", result)
	}
}

func TestAnAppServerConversationHasOneHarnessAndProfileOwnerAcrossAliases(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	session, view, _ := appServerSession(t, w, app, fakeagent.Sigma)
	registerServerDriver(t, w, "alias", serverDriverDeclaration(w))
	awaitDriverAvailable(app, serverPluginAgent)
	side := createProfile(app, "Side")
	for _, p := range []*testworld.Peer{app, w.AppOn(side.ID)} {
		refused, _, _ := w.RequestSpawn(p, fakeagent.Harness(serverPluginAgent), w.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.ResumeSessionID = protocol.Ptr(view.ConversationID) })
		if refused.Success {
			t.Fatalf("alias admitted conversation of %s in profile %s", session, p.SelectedProfile())
		}
		if !strings.Contains(protocol.Deref(refused.Error), "conversation "+view.ConversationID) {
			t.Fatalf("alias refusal: %+v", refused)
		}
	}
	if got := queriedSession(t, w.Client(), session); got.Agent != "sigma" || got.ProfileID != app.SelectedProfile() {
		t.Fatalf("alias changed original owner: %+v", got)
	}
}
