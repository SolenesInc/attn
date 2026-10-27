package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestOutpostSocketCommandsReachHome(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t)
	for _, stack := range []*testworld.Stack{home, outpost} {
		stack.Vars = append(stack.Vars, "SHELL=/bin/bash")
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	requireStdout(t, home.Attn("settings", "set", "analytics.enabled", "true"), "true")
	requireStdout(t, outpost.Attn("settings", "set", "analytics.enabled", "true"), "true")
	requireStdout(t, outpost.Attn("seed", "plant", "A seed planted from the outpost", "-m", "Forward this to the home"), "seed-planted-outpost")
	requireStdout(t, home.Attn("seed", "ls"), "A seed planted from the outpost")
	requireStdout(t, outpost.Attn("seed", "ls"), "A seed planted from the outpost")
	id := home.Spawn(pair.App, fakeagent.Harness(protocol.SessionAgentShell), outpost.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	pair.App.TypeLine(id, "attn seed plant 'A seed from the outpost shell' -m 'Home garden'; attn seed ls; printf '__garden_%s\\n' done")
	pair.App.AwaitScreen(id, "__garden_done")
	requireStdout(t, home.Attn("seed", "ls"), "A seed from the outpost shell")
	terminal := protocol.TerminalID(pair.App.Terminal(id))
	_, caller, err := outpost.Client().QueryAs(terminal)
	if err != nil || caller != protocol.SessionID(id) {
		t.Fatalf("forwarded caller: %+v, %v", caller, err)
	}
	if err := outpost.Client().UpdateState(terminal, protocol.StateWorking); err != nil {
		t.Fatal(err)
	}
	if err := outpost.Client().OpenSentFilesFromTerminal(terminal, nil); err != nil {
		t.Fatal(err)
	}
	if err := outpost.Client().RecordPullRequestCreatedFromTerminal(terminal, "https://github.com/example/project/pull/1"); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(pair.App, id, func(s protocol.Session) bool {
		return len(s.PullRequests) == 1 && s.PullRequests[0].Number == 1
	})
	hasCommand := func(app *testworld.Peer, name string) bool {
		for _, record := range cliRecords(t, app) {
			if record.Name == name {
				return true
			}
		}
		return false
	}
	for _, name := range []string{"state", "open_sent_files", "pull_request_created"} {
		if hasCommand(pair.App, name) {
			t.Fatalf("%s hook was dispatched at the home", name)
		}
		if !hasCommand(outpost.App(), name) {
			t.Fatalf("%s hook was not dispatched at the outpost", name)
		}
	}
	t.Log("plain shell and outpost shell use the home garden; query preserves caller; hooks stay on the outpost and the monitor's PR report reaches the home through Call")
}

func TestOutpostForwardsDocumentSubscriptions(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	requireStdout(t, home.Attn("doc", "define", "tests/router", "tasks", "title:string"), "tasks")
	watch := outpost.Launch(testworld.Invocation{Args: []string{"doc", "watch", "tests/router", "tasks", "--json"}})
	watch.AwaitStdout(`"documents": []`)
	requireStdout(t, home.Attn("doc", "put", "tests/router", "tasks", "one", `{"title":"streamed from home"}`), "wrote")
	watch.AwaitStdout("streamed from home")
	requireStdout(t, home.Attn("doc", "undefine", "tests/router", "tasks"), "removed")
	if result := watch.Wait(); result.Code == 0 || !strings.Contains(result.Stderr, "collection") {
		t.Fatalf("subscription must report its end: %+v", result)
	}
	t.Log("forward streamed the initial window, mutation and subscription end")
}

func TestOutpostForwardPreservesHalfCloseAndReportsBridgeLoss(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	conn, err := outpost.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(fakeagent.HangGuard)); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(conn).Encode(protocol.QueryMessage{Cmd: protocol.CmdQuery}); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*net.UnixConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var response protocol.Response
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&response); err != nil || !response.Ok {
		t.Fatalf("response after caller half-close: %+v, %v", response, err)
	}
	if err := decoder.Decode(&response); !errors.Is(err, io.EOF) {
		t.Fatalf("response after half-close did not end: ok=%v error=%q read=%v", response.Ok, protocol.Deref(response.Error), err)
	}
	requireStdout(t, home.Attn("doc", "define", "tests/router", "tasks"), "tasks")
	watch := outpost.Launch(testworld.Invocation{Args: []string{"doc", "watch", "tests/router", "tasks", "--json"}})
	watch.AwaitStdout(`"documents": []`)
	requireStdout(t, outpost.Attn("settings", "set", "outpost_listen", ""))
	result := watch.Wait()
	name, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if result.Code == 0 || !strings.Contains(result.Stderr, "lost "+name+" during this call; it may have run") {
		t.Fatalf("lost bridge: %+v", result)
	}
	t.Log(strings.TrimSpace(result.Stderr))
}

func TestOutpostForwardCallerHangupCancelsTerminalWaitAtHome(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	waiting := outpost.PauseAt(pausepoint.PtyWaitBlock)
	outpost.Vars = append(outpost.Vars, "SHELL=/bin/bash")
	pair.Start()
	id := home.Spawn(pair.App, fakeagent.Harness(protocol.SessionAgentShell), outpost.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	fifo := makeFifo(t, outpost.Path("hold.fifo"))
	pair.App.TypeLine(id, "printf '__blocking_%s\\n' ready; read line < "+fifo)
	pair.App.AwaitScreen(id, "__blocking_ready")
	conn, err := outpost.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(protocol.TerminalWaitMessage{Cmd: protocol.CmdTerminalWait, CallerSessionID: protocol.SessionID(id), Target: pair.App.Terminal(id), Until: protocol.TerminalWaitUntilPrompt}); err != nil {
		t.Fatal(err)
	}
	waiting.Await()
	if err := conn.(*net.UnixConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	waiting.Release()
	var response protocol.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil || !strings.Contains(protocol.Deref(response.Error), "context canceled") {
		t.Fatalf("wait did not end when caller hung up: %+v, %v", response, err)
	}
	writeFifo(t, fifo, "done\n")
	t.Logf("wait at the home ended on caller EOF: %s", protocol.Deref(response.Error))
}

func TestLaunchingLocalShellDoesNotKeepForwardedSocketOpen(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	created := home.PauseAt(pausepoint.ForwardSocketCreated)
	pair.Start()
	conn, err := outpost.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(fakeagent.HangGuard)); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(conn).Encode(protocol.QueryMessage{Cmd: protocol.CmdQuery}); err != nil {
		t.Fatal(err)
	}
	created.Await()
	id := home.Spawn(pair.App, fakeagent.Harness(protocol.SessionAgentShell), home.Path("local-shell"))
	created.Release()
	decoder := json.NewDecoder(conn)
	var response protocol.Response
	if err := decoder.Decode(&response); err != nil || !response.Ok {
		t.Fatalf("forwarded response: %+v, %v", response, err)
	}
	if err := decoder.Decode(&response); !errors.Is(err, io.EOF) {
		t.Fatalf("forwarded connection stayed open after its response: ok=%v error=%q read=%v", response.Ok, protocol.Deref(response.Error), err)
	}
	pair.App.TypeLine(id, "printf '__still_%s\\n' live")
	pair.App.AwaitScreen(id, "__still_live")
	t.Log("forwarded connection ended while the shell launched during socket creation remained live")
}

func TestOutpostForwardRefusalsNameTheHostAndHome(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	if err := outpost.Client().OpenSentFiles("", nil); err == nil || !strings.Contains(err.Error(), "outpost") {
		t.Fatalf("id-form sent files must refuse outpost paths: %v", err)
	}
	for _, args := range [][]string{{"open", "/tmp/outpost.md"}, {"browser", "open", "https://example.com"}} {
		result := outpost.Attn(args...)
		if result.Code == 0 || !strings.Contains(result.Stderr, "outpost") {
			t.Fatalf("refuse %v naming outpost: %+v", args, result)
		}
		t.Logf("%v: %s", args, strings.TrimSpace(result.Stderr))
	}
	conn, err := home.DialUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(protocol.ClientHelloMessage{Cmd: protocol.CmdClientHello}); err != nil {
		t.Fatal(err)
	}
	var refusal protocol.Response
	if err := json.NewDecoder(conn).Decode(&refusal); err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(refusal.Error) != "client_hello declares no placement" {
		t.Fatalf("undeclared placement: %+v", refusal)
	}
	t.Log(protocol.Deref(refusal.Error))
	home.Stop()
	for _, restart := range []bool{false, true} {
		if restart {
			outpost.Stop()
			outpost.Start()
		}
		name, err := os.Hostname()
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		result := outpost.Attn("seed", "ls")
		if result.Code == 0 || !strings.Contains(result.Stderr, name+" is unreachable") {
			t.Fatalf("offline home: %+v", result)
		}
		t.Logf("outpost restarted=%v: %s (%s)", restart, strings.TrimSpace(result.Stderr), time.Since(started))
	}
}

func TestOutpostRefusesAnotherInstanceBeforeEnrollment(t *testing.T) {
	outpost := testworld.NewStack(t)
	agent := testworld.NewSSHAgent(t)
	agent.Authorize(outpost.World)
	outpost.Start()
	address := bridgeAddress(t)
	requireStdout(t, outpost.Attn("settings", "set", "outpost_listen", address), address)
	cfg := bridge.DialConfig{
		Address: address, AgentSocket: agent.Socket,
		Self: bridge.Identity{DaemonID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "another home", Instance: "another-instance", Build: bridge.Build{Version: buildinfo.Version, SourceFingerprint: buildinfo.SourceFingerprint, ProtocolVersion: protocol.ProtocolVersion}},
	}
	for _, update := range []bool{false, true} {
		var refused error
		if update {
			conn, _, err := bridge.DialUpdate(context.Background(), cfg)
			refused = err
			if conn != nil {
				_ = conn.Close()
			}
		} else {
			conn, _, err := bridge.Dial(context.Background(), cfg)
			refused = err
			if conn != nil {
				_ = conn.Close()
			}
		}
		if refused == nil || !strings.Contains(refused.Error(), "another-instance") || !strings.Contains(refused.Error(), "default") {
			t.Fatalf("instance refusal for update=%v: %v", update, refused)
		}
		t.Logf("update=%v: %v", update, refused)
		status, err := enrollment.Load(outpost.Dir)
		if err != nil || !status.IsHome() {
			t.Fatalf("refused instance enrolled the outpost: %+v %v", status, err)
		}
	}
}

func TestOutpostArtifactTransfersNeverInterpretPathsOnTheHome(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	seed, err := home.Client().SeedPlant("", "Home artifacts", "Keep home files on the home", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	guide, err := home.Client().NotebookGuide("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(guide.Root, 0700); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(home.Path("reports"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"move", "copy"} {
		source := home.Path("reports", operation+".md")
		if err := os.WriteFile(source, []byte("a file on the home"), 0600); err != nil {
			t.Fatal(err)
		}
		result := outpost.Attn("seed", "attach", seed.Seed.ID, "--path", source, "--"+operation)
		if result.Code == 0 || !strings.Contains(result.Stderr, "outpost") {
			t.Errorf("%s must refuse outpost paths: %+v", operation, result)
		}
		if data, err := os.ReadFile(source); err != nil || string(data) != "a file on the home" {
			t.Errorf("%s changed the home's source: %q, %v", operation, data, err)
		}
	}
	shown, err := home.Client().SeedShow("", seed.Seed.ID)
	if err != nil || len(shown.Artifacts) != 0 {
		t.Fatalf("refused attaches changed home artifacts: %+v, %v", shown, err)
	}
	source := home.Path("reports", "keep.md")
	if err := os.WriteFile(source, []byte("keep the home artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := home.Client().SeedArtifactTransfer("", seed.Seed.ID, "copy", source, "", "", nil); err != nil {
		t.Fatal(err)
	}
	destination := home.Path("reports", "detached.md")
	result := outpost.Attn("seed", "detach", seed.Seed.ID, "--path", filepath.Base(source), "--to", destination)
	if result.Code == 0 || !strings.Contains(result.Stderr, "outpost") {
		t.Errorf("detach must refuse outpost paths: %+v", result)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Errorf("refused detach wrote the home destination: %v", err)
	}
	shown, err = home.Client().SeedShow("", seed.Seed.ID)
	if err != nil || len(shown.Artifacts) != 1 || shown.Artifacts[0].Filename != "keep.md" {
		t.Fatalf("refused detach changed home artifacts: %+v, %v", shown, err)
	}
}
