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

const sampleServerCallerHeader = "Sample-Server-Root-Conversation"

type pluginAgentTools struct {
	URL         string `json:"url"`
	Credential  string `json:"credential"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func launchAgentTools(t *testing.T, launch driverLaunch) *pluginAgentTools {
	t.Helper()
	return launch.AgentTools
}

func TestAPluginHarnessThatAsksForAgentToolsLaunchesWithItsTerminalsEndpoint(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	seed := plantForTools(t, w)
	asking := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"agent_tools": true, "resume": true})
	plain := connectDriver(t, w, "plain-plugin", "plain", map[string]bool{"resume": true})
	awaitDriverAvailable(app, "snipe")
	awaitDriverAvailable(app, "plain")

	_, off := spawnDriven(w, app, asking, w.Path("shop"))
	if tools := launchAgentTools(t, off); tools != nil {
		t.Errorf("with agent_tools_enabled off the launch carried agent_tools %+v, want none", tools)
	}

	setSetting(t, app, "agent_tools_enabled", "true")
	_, unasked := spawnDriven(w, app, plain, w.Path("shop"))
	if tools := launchAgentTools(t, unasked); tools != nil {
		t.Errorf("a driver that did not declare agent_tools was given %+v, want none", tools)
	}
	session, spawned := spawnDriven(w, app, asking, w.Path("shop"))
	tools := launchAgentTools(t, spawned)
	if tools == nil || tools.URL != "http://"+w.WSAddr+"/mcp" || tools.Credential == "" || tools.Name != "" || tools.Description == "" || strings.Contains(tools.Description, "\n") {
		t.Fatalf("driver.spawn carried agent_tools %+v, want this daemon's /mcp, a credential and a one-line description", tools)
	}
	agent := agentToolsCaller{w: w, bearer: tools.Credential}
	agent.mustCall(t, "seed_note", map[string]any{"seed": seed, "text": "From the plugin harness."})
	if author, _ := newestNoteAuthor(t, w, seed); author != session {
		t.Errorf("the plugin harness's note is by %s, want %s, its session", author, session)
	}

	if reloaded := reloadPlug(app, session); !reloaded.Success {
		t.Fatalf("reload: %s", protocol.Deref(reloaded.Error))
	}
	resumed := asking.launched()
	if again := launchAgentTools(t, resumed); resumed.Method != "driver.resume" || again == nil || *again != *tools {
		t.Errorf("%s carried agent_tools %+v, want the terminal's endpoint %+v again", resumed.Method, again, *tools)
	}
}

func toolsServerDeclaration(w *world) map[string]any {
	declaration := serverDriverDeclaration(w)
	declaration["caller_conversation_header"] = sampleServerCallerHeader
	return declaration
}

func registerToolsServerDriver(t *testing.T, w *world, extra ...string) *driverPeer {
	t.Helper()
	d := dialDriver(t, w, "tools-server")
	d.agent = serverPluginAgent
	d.mu.Lock()
	d.holdLaunch = true
	d.mu.Unlock()
	capabilities := map[string]bool{"server_binding": true, "resume": true, "agent_tools": true}
	for _, capability := range extra {
		capabilities[capability] = true
	}
	if err := d.call("driver.register", map[string]any{"agent": serverPluginAgent, "capabilities": capabilities, "server": toolsServerDeclaration(w)}, &d.registered); err != nil {
		t.Fatal(err)
	}
	return d
}

type toolsServerView struct {
	session      string
	conversation string
	tools        *pluginAgentTools
	instructions string
}

func spawnToolsServerView(w *world, app *testworld.Peer, driver *driverPeer, edit ...func(*protocol.SpawnSessionMessage)) toolsServerView {
	w.T.Helper()
	if err := os.MkdirAll(w.Path("shop"), 0700); err != nil {
		w.T.Fatal(err)
	}
	id := uuid.NewString()
	msg := protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: protocol.SessionID(id), Agent: serverPluginAgent, Cwd: w.Path("shop"), ProfileID: app.SelectedProfile(), Cols: 80, Rows: 24}
	for _, f := range edit {
		f(&msg)
	}
	app.Send(msg)
	var params struct {
		Conversation string            `json:"conversation_id"`
		AgentTools   *pluginAgentTools `json:"agent_tools"`
		Instructions *struct {
			Content string `json:"content"`
		} `json:"instructions"`
	}
	method := "driver.spawn"
	if msg.ResumeSessionID != nil {
		method = "driver.resume"
	}
	call := driver.asked(method, &params)
	argv := []string{filepath.Join(w.Dir, "bin", "sample-server"), "--attach", "--conversation", params.Conversation}
	if params.AgentTools != nil {
		argv = append(argv, "--mcp-server", params.AgentTools.Name, "--mcp-description", params.AgentTools.Description)
	}
	driver.answer(call, map[string]any{"argv": argv})
	result := testworld.Await(app, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == msg.ID })
	if !result.Success {
		w.T.Fatalf("server view refused: %s", protocol.Deref(result.Error))
	}
	w.Launched(id)
	view := toolsServerView{session: id, conversation: params.Conversation, tools: params.AgentTools}
	if params.Instructions != nil {
		view.instructions = params.Instructions.Content
	}
	return view
}

func TestASharedServerPluginGetsTheToolsAndEachConversationActsAsItsOwnSession(t *testing.T) {
	w := newWorld(t, fakeagent.SampleServer)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	seed := plantForTools(t, w)
	driver := registerToolsServerDriver(t, w)
	awaitDriverAvailable(app, serverPluginAgent)

	first := spawnToolsServerView(w, app, driver)
	second := spawnToolsServerView(w, app, driver)
	server := w.AppServer(fakeagent.SampleServer)
	for _, view := range []toolsServerView{first, second} {
		if view.tools == nil || view.tools.Name != "attn" || view.tools.Description == "" || view.tools.URL != "" || view.tools.Credential != "" {
			t.Fatalf("the view's driver.spawn carried agent_tools %+v, want only the server's name and description, no secret", view.tools)
		}
	}
	registered := server.MCPRegistered()["attn"]
	if registered.URL != "http://"+w.WSAddr+"/mcp" || strings.Join(registered.Headers, ",") != "Authorization" {
		t.Fatalf("the server holds the attn MCP server as %+v, want this daemon's /mcp with an Authorization header", registered)
	}

	for _, view := range []toolsServerView{first, second} {
		called := server.CallTool(view.conversation, "attn_do", noteScript(seed, "From conversation "+view.conversation))
		if called.Refused != "" || called.IsError {
			t.Fatalf("conversation %s calling seed_note: refused %q, %s", view.conversation, called.Refused, called.Text())
		}
		if author, body := newestNoteAuthor(t, w, seed); author != view.session {
			t.Errorf("conversation %s wrote %q as %s, want its own session %s", view.conversation, body, author, view.session)
		}
	}

	fork := "fork-of-" + first.conversation
	server.Fork(first.conversation, fork)
	if refused := server.CallTool(fork, "attn_do", noteScript(seed, "from the fork")); !strings.Contains(refused.Refused, "401") || !strings.Contains(refused.Refused, "no session of "+serverPluginAgent+" holds conversation "+fork) {
		t.Fatalf("a conversation no session holds was answered %+v, want 401 naming conversation %s", refused, fork)
	}
	forked := spawnToolsServerView(w, app, driver, func(m *protocol.SpawnSessionMessage) { m.ResumeSessionID = protocol.Ptr(fork) })
	server.CallTool(fork, "attn_do", noteScript(seed, "from the fork"))
	if author, _ := newestNoteAuthor(t, w, seed); author != forked.session || forked.session == first.session {
		t.Errorf("the opened fork wrote as %s, want its own session %s, not %s", author, forked.session, first.session)
	}

	setSetting(t, app, "agent_tools_enabled", "false")
	server.AwaitMCPRegistration("attn", false)
	if off := spawnToolsServerView(w, app, driver); off.tools != nil {
		t.Errorf("with agent_tools_enabled off a view's driver.spawn carried agent_tools %+v, want none", off.tools)
	}
	setSetting(t, app, "agent_tools_enabled", "true")
	server.AwaitMCPRegistration("attn", true)
}

func registerServerAlias(t *testing.T, w *world, app *testworld.Peer, plugin, agent string, tools bool) *driverPeer {
	t.Helper()
	d := dialDriver(t, w, plugin)
	d.agent = agent
	d.mu.Lock()
	d.holdLaunch = true
	d.mu.Unlock()
	declaration := serverDriverDeclaration(w)
	if tools {
		declaration = toolsServerDeclaration(w)
	}
	if err := d.call("driver.register", map[string]any{"agent": agent, "server": declaration, "capabilities": map[string]bool{"server_binding": true, "resume": true, "agent_tools": tools}}, &d.registered); err != nil {
		t.Fatal(err)
	}
	awaitDriverAvailable(app, agent)
	return d
}

func spawnAliasView(w *world, app *testworld.Peer, driver *driverPeer, edit ...func(*protocol.SpawnSessionMessage)) toolsServerView {
	w.T.Helper()
	return spawnToolsServerView(w, app, driver, append([]func(*protocol.SpawnSessionMessage){func(m *protocol.SpawnSessionMessage) { m.Agent = driver.agent }}, edit...)...)
}

func uninstallServerAlias(t *testing.T, w *world, app *testworld.Peer, plugin string, views ...toolsServerView) {
	t.Helper()
	for _, view := range views {
		if closed := closeFromApp(app, view.session); !closed.Accepted {
			t.Fatal(protocol.Deref(closed.Error))
		}
		awaitClosed(app, view.session)
	}
	writePluginManifest(t, filepath.Join(w.Dir, "plugins", plugin), plugin, "exit 0")
	pluginsAct(t, app, protocol.UninstallPluginMessage{Cmd: protocol.CmdUninstallPlugin, Name: plugin}, "uninstall", plugin)
}

func TestASharedServerKeepsTheToolsForItsOtherAliasesWhenOneIsUninstalled(t *testing.T) {
	w := newWorld(t, fakeagent.SampleServer)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	seed := plantForTools(t, w)
	first := registerServerAlias(t, w, app, "first-plugin", "aaa-first", true)
	second := registerServerAlias(t, w, app, "second-plugin", "bbb-second", true)
	firstView := spawnAliasView(w, app, first)
	secondView := spawnAliasView(w, app, second)
	server := w.AppServer(fakeagent.SampleServer)
	if firstView.tools == nil || secondView.tools == nil {
		t.Fatalf("both aliases' views should carry agent_tools: %+v and %+v", firstView.tools, secondView.tools)
	}

	uninstallServerAlias(t, w, app, "first-plugin", firstView)

	called := server.CallTool(secondView.conversation, "attn_do", noteScript(seed, "after the other alias left"))
	if called.Refused != "" || called.IsError {
		t.Fatalf("the remaining alias's conversation calling seed_note: refused %q, %s", called.Refused, called.Text())
	}
	if author, _ := newestNoteAuthor(t, w, seed); author != secondView.session {
		t.Errorf("the remaining alias's note is by %s, want its session %s", author, secondView.session)
	}
	if again := spawnAliasView(w, app, second); again.tools == nil || again.tools.Name != "attn" {
		t.Errorf("a new view of the remaining alias carried agent_tools %+v, want the server's name and description", again.tools)
	}
	if initializes, _ := server.ControlCounts(); initializes != 1 {
		t.Errorf("attn connected to the server %d times, want one connection throughout", initializes)
	}
}

func TestAnOptedInAliasJoiningAConnectedServerGetsTheToolsUntilItLeaves(t *testing.T) {
	w := newWorld(t, fakeagent.SampleServer)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	seed := plantForTools(t, w)
	plain := registerServerAlias(t, w, app, "plain-plugin", "aaa-plain", false)
	plainView := spawnAliasView(w, app, plain)
	server := w.AppServer(fakeagent.SampleServer)
	if plainView.tools != nil || len(server.MCPRegistered()) != 0 {
		t.Fatalf("an alias without agent_tools gave its view %+v and the server %+v, want neither", plainView.tools, server.MCPRegistered())
	}

	late := registerServerAlias(t, w, app, "late-plugin", "bbb-late", true)
	spawned := spawnAliasView(w, app, late)
	if spawned.tools == nil || spawned.tools.Name != "attn" {
		t.Fatalf("the joining alias's driver.spawn carried agent_tools %+v, want the server's name and description", spawned.tools)
	}
	called := server.CallTool(spawned.conversation, "attn_do", noteScript(seed, "from the joining alias"))
	if called.Refused != "" || called.IsError {
		t.Fatalf("the joining alias's conversation calling seed_note: refused %q, %s", called.Refused, called.Text())
	}
	if author, _ := newestNoteAuthor(t, w, seed); author != spawned.session {
		t.Errorf("the joining alias's note is by %s, want its session %s", author, spawned.session)
	}
	fork := "fork-of-" + spawned.conversation
	server.Fork(spawned.conversation, fork)
	resumed := spawnAliasView(w, app, late, func(m *protocol.SpawnSessionMessage) { m.ResumeSessionID = protocol.Ptr(fork) })
	if resumed.tools == nil || resumed.tools.Name != "attn" {
		t.Errorf("the joining alias's driver.resume carried agent_tools %+v, want the server's name and description", resumed.tools)
	}
	if initializes, _ := server.ControlCounts(); initializes != 1 {
		t.Errorf("attn connected to the server %d times, want one connection throughout", initializes)
	}

	uninstallServerAlias(t, w, app, "late-plugin", spawned, resumed)
	server.AwaitMCPRegistration("attn", false)
}
