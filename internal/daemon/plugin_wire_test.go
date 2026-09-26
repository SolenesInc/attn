package daemon_test

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const pluginWireAPIVersion = 6

type pluginWireMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *pluginWireError `json:"error,omitempty"`
}

type pluginWireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type pluginHealth struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type pluginPeer struct {
	t             *testing.T
	name          string
	conn          net.Conn
	answersHealth bool
	writing       sync.Mutex
	inbox         chan pluginWireMessage
}

func dialPlugin(t *testing.T, w *world, name string, answersHealth bool) *pluginPeer {
	t.Helper()
	conn, err := w.DialUnix()
	if err != nil {
		t.Fatalf("plugin %s dials the daemon: %v", name, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	p := &pluginPeer{t: t, name: name, conn: conn, answersHealth: answersHealth, inbox: make(chan pluginWireMessage, 64)}
	go p.listen()
	return p
}

func connectPlugin(t *testing.T, w *world, name string, surfaces ...string) *pluginPeer {
	t.Helper()
	p := dialPlugin(t, w, name, true)
	if answer := p.hello(pluginHelloParams(name, pluginWireAPIVersion, 1, surfaces...)); answer.Error != nil {
		t.Fatalf("the daemon refused plugin %s: %s", name, answer.Error.Message)
	}
	return p
}

func pluginHelloParams(name string, apiVersion int, generation uint64, surfaces ...string) map[string]any {
	params := map[string]any{"name": name, "version": "0.1.0", "attn_api_version": apiVersion, "surfaces": surfaces}
	if generation > 0 {
		params["generation"] = generation
	}
	return params
}

func (p *pluginPeer) listen() {
	defer close(p.inbox)
	decoder := json.NewDecoder(p.conn)
	for {
		var message pluginWireMessage
		if err := decoder.Decode(&message); err != nil {
			return
		}
		if message.Method == "attn.health" && p.answersHealth {
			_ = p.transmit(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": pluginHealth{OK: true}})
			continue
		}
		p.inbox <- message
	}
}

func (p *pluginPeer) transmit(message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return p.writeAll(append(payload, '\n'))
}

func (p *pluginPeer) writeAll(payload []byte) error {
	p.writing.Lock()
	defer p.writing.Unlock()
	_, err := p.conn.Write(payload)
	return err
}

func (p *pluginPeer) hello(params map[string]any) pluginWireMessage {
	p.t.Helper()
	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "hello", "params": params})
	return p.read()
}

func (p *pluginPeer) write(payload []byte) {
	p.t.Helper()
	if err := p.writeAll(payload); err != nil {
		p.t.Fatalf("plugin %s writes: %v", p.name, err)
	}
}

func (p *pluginPeer) send(message any) {
	p.t.Helper()
	if err := p.transmit(message); err != nil {
		p.t.Fatalf("plugin %s writes: %v", p.name, err)
	}
}

func (p *pluginPeer) read() pluginWireMessage {
	p.t.Helper()
	select {
	case message, open := <-p.inbox:
		if !open {
			p.t.Fatalf("the daemon hung up on plugin %s", p.name)
		}
		return message
	case <-time.After(fakeagent.HangGuard):
		p.t.Fatalf("plugin %s heard nothing from the daemon within %s", p.name, fakeagent.HangGuard)
		return pluginWireMessage{}
	}
}

func (p *pluginPeer) awaitHangUp() {
	p.t.Helper()
	select {
	case message, open := <-p.inbox:
		if open {
			p.t.Fatalf("plugin %s read %+v, want the daemon to hang up", p.name, message)
		}
	case <-time.After(fakeagent.HangGuard):
		p.t.Fatalf("the daemon did not hang up on plugin %s within %s", p.name, fakeagent.HangGuard)
	}
}

func (p *pluginPeer) expect(method string, params any) json.RawMessage {
	p.t.Helper()
	for {
		message := p.read()
		switch message.Method {
		case method:
			if params != nil {
				if err := json.Unmarshal(message.Params, params); err != nil {
					p.t.Fatalf("plugin %s decodes %s params %s: %v", p.name, method, message.Params, err)
				}
			}
			return message.ID
		case "":
		default:
			p.t.Fatalf("plugin %s was asked %s %s while awaiting %s", p.name, message.Method, message.Params, method)
		}
	}
}

func (p *pluginPeer) answer(id json.RawMessage, result any) {
	p.t.Helper()
	p.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (p *pluginPeer) fail(id json.RawMessage, message string) {
	p.t.Helper()
	p.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": pluginWireError{Code: -32603, Message: message}})
}

func (p *pluginPeer) hangUp() {
	_ = p.conn.Close()
}

func writePluginManifest(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name = \"" + name + "\"\nversion = \"0.1.0\"\nattn_api_version = 6\n\n[plugin]\nkind = \"executable\"\npath = \"run\"\n"
	if err := os.WriteFile(filepath.Join(dir, "attn-plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func listPlugins(app *testworld.Peer) protocol.PluginsUpdatedMessage {
	app.T.Helper()
	return testworld.Request(app, protocol.ListPluginsMessage{Cmd: protocol.CmdListPlugins}, protocol.EventPluginsUpdated,
		func(protocol.PluginsUpdatedMessage) bool { return true })
}

func pluginNamed(plugins []protocol.PluginInfo, name string) (protocol.PluginInfo, bool) {
	for _, plugin := range plugins {
		if plugin.Name == name {
			return plugin, true
		}
	}
	return protocol.PluginInfo{}, false
}

func awaitPluginShown(app *testworld.Peer, name string, match func(protocol.PluginInfo) bool) protocol.PluginInfo {
	app.T.Helper()
	var shown protocol.PluginInfo
	testworld.Await(app, protocol.EventPluginsUpdated, func(m protocol.PluginsUpdatedMessage) bool {
		plugin, ok := pluginNamed(m.Plugins, name)
		shown = plugin
		return ok && match(plugin)
	})
	return shown
}

func pluginUpdatesShowing(app *testworld.Peer, name string) int {
	count := 0
	for _, received := range app.Received() {
		if received.Event != protocol.EventPluginsUpdated {
			continue
		}
		if _, ok := pluginNamed(received.Plugins, name); ok {
			count++
		}
	}
	return count
}

func TestAPluginHelloConnectsItUntilItHangsUp(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	for _, name := range []string{"worktree-provider", "old-provider", "unnumbered-provider", "typo-provider"} {
		writePluginManifest(t, filepath.Join(w.Dir, "plugins", name), name, "exit 0")
	}

	for _, row := range []struct {
		name, refusal string
		hello         map[string]any
	}{
		{name: "old-provider", refusal: "unsupported attn_api_version 5",
			hello: pluginHelloParams("old-provider", pluginWireAPIVersion-1, 1)},
		{name: "unnumbered-provider", refusal: "hello params.generation is required",
			hello: pluginHelloParams("unnumbered-provider", pluginWireAPIVersion, 0)},
		{name: "typo-provider", refusal: `unsupported plugin surface "worktree.cretae"`,
			hello: pluginHelloParams("typo-provider", pluginWireAPIVersion, 1, "worktree.cretae")},
	} {
		refused := dialPlugin(t, w, row.name, true)
		if answer := refused.hello(row.hello); answer.Error == nil || !strings.Contains(answer.Error.Message, row.refusal) {
			t.Errorf("%s's hello was answered %+v, want a refusal saying %q", row.name, answer, row.refusal)
		}
		refused.awaitHangUp()
	}

	plugin := dialPlugin(t, w, "worktree-provider", true)
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "hello",
		"params": pluginHelloParams("worktree-provider", pluginWireAPIVersion, 1, "worktree.create")})
	if err != nil {
		t.Fatal(err)
	}
	half := len(payload) / 2
	plugin.write(payload[:half])
	plugin.write(append(payload[half:], '\n'))
	var accepted struct {
		OK bool `json:"ok"`
	}
	answer := plugin.read()
	if err := json.Unmarshal(answer.Result, &accepted); err != nil || answer.Error != nil || !accepted.OK {
		t.Fatalf("the hello written in two halves was answered %+v, want ok", answer)
	}
	connected := awaitPluginShown(app, "worktree-provider", func(p protocol.PluginInfo) bool { return p.Connected })
	if protocol.Deref(connected.RuntimePhase) != "connected" || connected.RuntimeState != "connected" {
		t.Errorf("the app shows the connected plugin as phase %q, state %q; want connected", protocol.Deref(connected.RuntimePhase), connected.RuntimeState)
	}
	for _, received := range app.Received() {
		for _, refused := range []string{"old-provider", "unnumbered-provider", "typo-provider"} {
			if shown, ok := pluginNamed(received.Plugins, refused); ok && shown.Connected {
				t.Errorf("the app saw the refused plugin %s connected", refused)
			}
		}
	}

	plugin.hangUp()
	awaitPluginShown(app, "worktree-provider", func(p protocol.PluginInfo) bool { return !p.Connected })
}

func TestPluginHealthReachesTheAppOnlyWhenItChanges(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		writePluginManifest(t, filepath.Join(w.Dir, "plugins", "health-provider"), "health-provider", "exit 0")
		plugin := dialPlugin(t, w, "health-provider", false)
		if answer := plugin.hello(pluginHelloParams("health-provider", pluginWireAPIVersion, 1, "worktree.create")); answer.Error != nil {
			t.Fatalf("the daemon refused the health provider: %s", answer.Error.Message)
		}
		awaitPluginShown(app, "health-provider", func(p protocol.PluginInfo) bool { return p.Connected })

		poll := func(answer pluginHealth) {
			t.Helper()
			plugin.answer(plugin.expect("attn.health", nil), answer)
			synctest.Wait()
		}
		poll(pluginHealth{OK: true, Message: "ready"})
		healthy := awaitPluginShown(app, "health-provider", func(p protocol.PluginInfo) bool {
			return protocol.Deref(p.HealthStatus) == "healthy"
		})
		if protocol.Deref(healthy.HealthMessage) != "ready" || protocol.Deref(healthy.LastHealthAt) == "" || healthy.RuntimeState != "connected" {
			t.Errorf("the healthy plugin shows %+v, want its message, a check time, and connected", healthy)
		}

		for _, step := range []struct {
			answer pluginHealth
			pushes int
			status string
		}{
			{answer: pluginHealth{OK: true, Message: "ready"}, pushes: 0, status: "healthy"},
			{answer: pluginHealth{OK: true, Message: "ready"}, pushes: 0, status: "healthy"},
			{answer: pluginHealth{Message: "provider down"}, pushes: 1, status: "unhealthy"},
			{answer: pluginHealth{Message: "provider down"}, pushes: 0, status: "unhealthy"},
			{answer: pluginHealth{Message: "worktree provider timed out"}, pushes: 1, status: "unhealthy"},
			{answer: pluginHealth{OK: true, Message: "ready"}, pushes: 1, status: "healthy"},
		} {
			before := pluginUpdatesShowing(app, "health-provider")
			w.advance(15 * time.Second)
			poll(step.answer)
			if pushed := pluginUpdatesShowing(app, "health-provider") - before; pushed != step.pushes {
				t.Fatalf("answering %+v pushed %d plugin updates, want %d", step.answer, pushed, step.pushes)
			}
			if step.pushes == 0 {
				continue
			}
			shown := awaitPluginShown(app, "health-provider", func(p protocol.PluginInfo) bool {
				return protocol.Deref(p.HealthStatus) == step.status && protocol.Deref(p.HealthMessage) == step.answer.Message
			})
			wantState := "connected"
			if step.status == "unhealthy" {
				wantState = "degraded"
			}
			if shown.RuntimeState != wantState {
				t.Errorf("a %s plugin shows state %q, want %q", step.status, shown.RuntimeState, wantState)
			}
		}
	})
}

func TestTheAppSeesBrokenPluginManifestsBesideThePluginsItAccepts(t *testing.T) {
	w := newWorld(t)
	if err := os.MkdirAll(filepath.Join(w.Dir, "plugins", "bad-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	badManifest := filepath.Join(w.Dir, "plugins", "bad-plugin", "attn-plugin.toml")
	if err := os.WriteFile(badManifest, []byte("name = \"bad-plugin\"\nversion = \"0.1.0\"\nattn_api_version = 6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writePluginManifest(t, filepath.Join(w.Dir, "plugins", "manual-provider"), "manual/provider", "exit 0")
	w.restart()
	app := w.App()

	listed := listPlugins(app)
	if len(listed.Issues) != 1 || !strings.Contains(listed.Issues[0].Path, "bad-plugin") || listed.Issues[0].Error == "" {
		t.Errorf("the app sees manifest issues %+v, want only the one without [plugin] reported", listed.Issues)
	}
	if _, ok := pluginNamed(listed.Plugins, "bad-plugin"); ok {
		t.Error("the app lists the broken manifest as a plugin")
	}
	if manual, ok := pluginNamed(listed.Plugins, "manual/provider"); !ok || manual.InstallationState != "installed" {
		t.Errorf("the app sees manual/provider as %+v (listed %v), want it installed", manual, ok)
	}
	connectPlugin(t, w, "manual/provider", "worktree.create")
	awaitPluginShown(app, "manual/provider", func(p protocol.PluginInfo) bool { return p.Connected })
}

func TestWorktreeCreateGoesToTheHighestPriorityProvider(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	repo := newRepo(t, "shop")
	writePluginManifest(t, filepath.Join(w.Dir, "plugins", "beta-provider"), "beta-provider", "exit 0")
	alpha := connectPlugin(t, w, "alpha-provider", "worktree.create")
	beta := connectPlugin(t, w, "beta-provider", "worktree.create")

	createThrough := func(provider *pluginPeer, branch string) {
		t.Helper()
		app.Send(protocol.CreateWorktreeMessage{Cmd: protocol.CmdCreateWorktree, MainRepo: repo, Branch: branch})
		var asked pluginWorktreeCreate
		id := provider.expect("worktree.create", &asked)
		if asked.MainRepo != repo || asked.Branch != branch {
			t.Errorf("%s was asked %+v, want %s on %s", provider.name, asked, repo, branch)
		}
		path := filepath.Join(filepath.Dir(repo), provider.name+"-"+branch)
		runGit(t, repo, "worktree", "add", "-q", "-b", branch, path)
		provider.answer(id, pluginWorktreeAnswer{Status: "handled", Path: path, Branch: branch})
		result := testworld.Await(app, protocol.EventCreateWorktreeResult, func(protocol.CreateWorktreeResultMessage) bool { return true })
		if !result.Success || protocol.Deref(result.Path) != path {
			t.Errorf("creating %s answered %+v (%s), want %s's %s", branch, result, protocol.Deref(result.Error), provider.name, path)
		}
	}

	createThrough(alpha, "feat-tied")
	ranked := testworld.Request(app, protocol.SetPluginPriorityMessage{Cmd: protocol.CmdSetPluginPriority, Name: "beta-provider", Priority: 10},
		protocol.EventPluginActionResult, func(r protocol.PluginActionResultMessage) bool { return r.Action == "set_priority" })
	if !ranked.Success {
		t.Fatalf("ranking beta-provider first: %s", protocol.Deref(ranked.Error))
	}
	createThrough(beta, "feat-ranked")
	beta.hangUp()
	awaitPluginShown(app, "beta-provider", func(p protocol.PluginInfo) bool { return !p.Connected })
	createThrough(alpha, "feat-after-hang-up")
}
