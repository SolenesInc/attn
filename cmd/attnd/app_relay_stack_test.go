package main_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func hasSession(sessions []protocol.Session, id string) bool {
	return slices.ContainsFunc(sessions, func(s protocol.Session) bool { return string(s.ID) == id })
}

func healthOf(t *testing.T, s *testworld.Stack) map[string]any {
	t.Helper()
	response, err := http.Get("http://" + s.WSAddr + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var health map[string]any
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	return health
}

func TestAnAppOnAMachineThatDialsItsHomeWorksAgainstTheHome(t *testing.T) {
	w := newReverseWorld(t, testworld.WithAgents(fakeagent.Claude))
	box := testworld.NewStack(t)
	box.Vars = append(box.Vars, "SHELL=/bin/bash")
	box.Start()
	w.ssh.Route("box", box)
	beforeJoin := w.laptop.App()
	row := w.join()
	if closed := beforeJoin.Closed(); closed.Code != websocket.StatusTryAgainLater {
		t.Fatalf("an app the laptop served itself before it joined was closed with %v, want %v so it connects through the relay", closed, websocket.StatusTryAgainLater)
	}
	w.app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "box", Destination: "box"})
	boxRow := outpostState(w.app, "box", protocol.OutpostStateConnected)
	home := protocol.Deref(w.app.Initial.DaemonInstanceID)
	atHome := w.home.Spawn(w.app, fakeagent.Harness(protocol.SessionAgentShell), w.home.Path("at-home"))
	onBox := w.home.Spawn(w.app, fakeagent.Harness(protocol.SessionAgentShell), box.Path("on-box"), func(m *protocol.SpawnSessionMessage) { m.Host = &boxRow.ID })

	relayed := w.laptop.TrustedApp()
	if got := protocol.Deref(relayed.Initial.DaemonInstanceID); got != home {
		t.Fatalf("the app on the laptop's port got initial_state from daemon %s, want the home's %s", got, home)
	}
	if !hasSession(relayed.Initial.Sessions, atHome) || !hasSession(relayed.Initial.Sessions, onBox) {
		t.Fatalf("the relayed app's initial_state lacks the home's sessions %s and %s: %+v", atHome, onBox, relayed.Initial.Sessions)
	}
	fromHome := w.home.Spawn(w.app, fakeagent.Harness(protocol.SessionAgentShell), w.home.Path("from-home"))
	testworld.AwaitSession(relayed, fromHome, func(protocol.Session) bool { return true })
	fromLaptop := w.home.Spawn(relayed, fakeagent.Harness(protocol.SessionAgentShell), w.home.Path("from-laptop"))
	testworld.AwaitSession(w.app, fromLaptop, func(protocol.Session) bool { return true })
	relayed.TypeLine(atHome, "echo home-$((1+1))")
	relayed.AwaitScreen(atHome, "home-2")
	relayed.TypeLine(onBox, "echo box-$((2+2))")
	relayed.AwaitScreen(onBox, "box-4")
	t.Log("(1) the relayed app sees the home's state and changes, its own changes reach the home's app, and it types into a shell on the home and on the home's ssh outpost")

	root := t.TempDir()
	list := func(p *testworld.Peer, id string) protocol.FsListResultMessage {
		return testworld.Request(p, protocol.FsListMessage{Cmd: protocol.CmdFsList, Root: protocol.Ptr(root), RequestID: protocol.Ptr(id)}, protocol.EventFsListResult, func(r protocol.FsListResultMessage) bool { return r.RequestID == id })
	}
	put := func(p *testworld.Peer) error {
		_, err := p.Attachment(protocol.AttachmentPutMessage{Cmd: protocol.CmdAttachmentPut, Owner: ownedBy(uuid.NewString()), AttachmentID: uuid.NewString(), Name: "note.txt", DataBase64: base64.StdEncoding.EncodeToString([]byte("relayed")), Final: true})
		return err
	}
	if listed := list(relayed, "trusted"); !listed.Success {
		t.Fatalf("fs_list with a root from the relayed app with the laptop's credential: %q", protocol.Deref(listed.Error))
	}
	if err := put(relayed); err != nil {
		t.Fatalf("attachment_put from the relayed app with the laptop's credential: %v", err)
	}
	plain := w.laptop.App()
	if listed := list(plain, "untrusted"); listed.Success {
		t.Fatal("fs_list with a root succeeded for a client on the laptop's port without the laptop's browser-host-token")
	}
	if err := put(plain); err == nil {
		t.Fatal("attachment_put succeeded for a client on the laptop's port without the laptop's browser-host-token")
	}
	forged := w.laptop.Connect(protocol.ClientHelloMessage{Cmd: protocol.CmdClientHello, ClientKind: protocol.ClientKindNativeApp, Version: "protocol-" + protocol.ProtocolVersion, ClientToken: protocol.Ptr("not-the-laptops")}, nil)
	if refused := testworld.Refused(forged); protocol.Deref(refused.ErrorCode) != protocol.ErrorCodeUnauthorizedClient {
		t.Fatalf("a hello with a wrong client token through the laptop: %+v", refused)
	}
	t.Log("(2) commands gated on the trusted app run for the relayed app and are refused without the laptop's credentials")

	health := healthOf(t, w.laptop)
	if health["daemon_instance_id"] == home || health["machine_id"] != row.ID {
		t.Fatalf("/health on the laptop's port: %v; want the laptop's daemon and machine id %s", health, row.ID)
	}
	requireStdout(t, w.laptop.Attn("enrollment", "status"), "dial:       home (bridged)")
	requireStdout(t, w.home.Attn("settings", "set", "agent_tools_enabled", "true"), "true")
	agent := w.home.Spawn(w.app, fakeagent.Claude, w.home.Path("agent"))
	tools := claudeToolEndpoint(t, w.home.LaunchedCarrying(w.app.Terminal(agent)))
	if answer := tools.call(t, "presence", map[string]any{}); answer.IsError || !strings.Contains(answer.Text, agent) {
		t.Fatalf("presence from the home's agent on the home's /mcp: %+v", answer)
	}
	if status, reply := tools.at(w.laptop).post(t, "attn_record", map[string]any{"script": "return presence()"}); status != http.StatusUnauthorized {
		t.Fatalf("the home's agent credential on the laptop's /mcp answered %d %s, want the laptop's own refusal", status, reply)
	}
	t.Log("(3) /health, the CLI and /mcp on the laptop are answered by the laptop: the home's agent credential does not verify there")
}

func TestAnAppTheHomeDropsIsClosedOnTheMachineThatRelaysIt(t *testing.T) {
	w := newReverseWorld(t)
	w.join()
	noisy := w.home.Spawn(w.app, fakeagent.Harness(protocol.SessionAgentShell), w.home.Path("noisy"))
	terminal := protocol.TerminalID(w.app.Terminal(noisy))

	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	stalled, _, err := websocket.Dial(ctx, "ws://"+w.laptop.WSAddr+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.CloseNow()
	token, err := os.ReadFile(filepath.Join(w.laptop.Dir, "client-token"))
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []any{
		protocol.ClientHelloMessage{Cmd: protocol.CmdClientHello, ClientKind: protocol.ClientKindNativeApp, Version: "protocol-" + protocol.ProtocolVersion, Capabilities: []string{protocol.CapabilityBinaryPtyOutput}, ClientToken: protocol.Ptr(strings.TrimSpace(string(token)))},
		protocol.AttachSessionMessage{Cmd: protocol.CmdAttachSession, ID: terminal},
	} {
		payload, _ := json.Marshal(message)
		if err := stalled.Write(ctx, websocket.MessageText, payload); err != nil {
			t.Fatal(err)
		}
	}
	w.app.TypeLine(noisy, "yes relayed-flood-0123456789-0123456789-0123456789-0123456789")
	for {
		if err := stalled.Write(ctx, websocket.MessageText, []byte(`{"cmd":"ping"}`)); err != nil {
			if ctx.Err() != nil {
				t.Fatal("the laptop kept the connection of an app that stopped reading open after the home dropped it")
			}
			t.Logf("the laptop closed the stalled app's connection after the home dropped it: %v", err)
			break
		}
	}
	w.app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: terminal, Data: "\x03"})
	again := w.laptop.App()
	again.TypeLine(noisy, "echo still-$((3+3))")
	again.AwaitScreen(noisy, "still-6")
	t.Log("(4) a new app through the laptop works after the home dropped the stalled one")
}

func TestAnAppWhoseHomeIsUnreachableIsToldWhyAndConnectsWhenItReturns(t *testing.T) {
	w := newReverseWorld(t)
	w.join()
	w.laptop.App()
	w.home.Stop()
	w.laptop.AwaitTimer(bridge.RetryTimer)

	away := awaitHomeDial(t, w.laptop, "dialing")
	told := lastHomeUnreachable(t, away)
	if told.Home != w.name || protocol.Deref(told.Reason) == "" {
		t.Fatalf("home_unreachable while the home is down: %+v, want home %s, state dialing and a reason", told, w.name)
	}
	t.Logf("with the home down the laptop answers its app: %+v (%s)", told, protocol.Deref(told.Reason))
	away.Send(protocol.RetryHomeMessage{Cmd: protocol.CmdRetryHome})
	if closed := away.Closed(); closed.Code != websocket.StatusTryAgainLater {
		t.Fatalf("retry_home closed the connection with %v, want %v", closed, websocket.StatusTryAgainLater)
	}

	waiting := awaitHomeDial(t, w.laptop, "dialing")
	w.home.Start()
	w.app = w.home.App()
	stopRetries := w.laptop.FireTimers(bridge.RetryTimer)
	defer stopRetries()
	if closed := waiting.Closed(); closed.Code != websocket.StatusTryAgainLater {
		t.Fatalf("the waiting app's connection closed with %v once the home returned, want %v", closed, websocket.StatusTryAgainLater)
	}
	back := w.laptop.App()
	if got, want := protocol.Deref(back.Initial.DaemonInstanceID), protocol.Deref(w.app.Initial.DaemonInstanceID); got != want {
		t.Fatalf("after the home returned, the laptop's app got initial_state from %s, want the home's %s", got, want)
	}
	t.Log("(5) the laptop told its app the home was unreachable and why, closed it when the home returned, and relayed the next connection")
}
