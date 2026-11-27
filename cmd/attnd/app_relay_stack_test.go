package main_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/diag"
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

	requireStdout(t, w.home.Attn("settings", "set", "remote_home_enabled", "false"), "false")
	w.awaitCopiedSetting("remote_home_enabled", "false")
	refused := awaitHomeDial(t, w.laptop, "refused")
	if told := lastHomeUnreachable(t, refused); !strings.Contains(protocol.Deref(told.Reason), "does not serve apps on other machines") {
		t.Fatalf("an app the home refuses while the machine is bridged: %+v, want refused with the home's reason", told)
	}
	requireStdout(t, w.home.Attn("settings", "set", "remote_home_enabled", "true"), "true")
	if closed := refused.Closed(); closed.Code != websocket.StatusTryAgainLater {
		t.Fatalf("the refused app's connection closed with %v once the home served apps again, want %v", closed, websocket.StatusTryAgainLater)
	}
	if back := w.laptop.App(); protocol.Deref(back.Initial.DaemonInstanceID) != home {
		t.Fatalf("after the home served apps again the laptop's app got initial_state from %s, want the home's %s", protocol.Deref(back.Initial.DaemonInstanceID), home)
	}
	t.Log("a home that stops serving other machines' apps refuses the relay and the app is told so; once it serves them again the app connects by itself")
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

func TestAnAppOnAMachineThatDialsItsHomeMovesSeedFilesInAndOutOfItsOwnDiskAndOpensThemThere(t *testing.T) {
	w := newReverseWorld(t)
	row := w.join()
	if health := healthOf(t, w.laptop); health["home"] == nil || health["home"] == "" || health["machine_id"] != row.ID {
		t.Fatalf("/health on the laptop: %v; want the name of the home it dials and its machine id %s", health, row.ID)
	}
	if health := healthOf(t, w.home); health["home"] != nil {
		t.Fatalf("/health on the home names a home elsewhere: %v", health["home"])
	}
	relayed := w.laptop.TrustedApp()
	seed := plant(t, w.home, "Laptop files", "-m", "Files the laptop's app brings in and takes out")
	transfer := func(msg protocol.SeedArtifactTransferMessage) protocol.SeedArtifactTransferResultMessage {
		t.Helper()
		id := uuid.NewString()
		msg.Cmd, msg.RequestID, msg.SeedID, msg.Host = protocol.CmdSeedArtifactTransfer, &id, seed.ID, &row.ID
		answer := testworld.Request(relayed, msg, protocol.EventSeedArtifactTransferResult, func(r protocol.SeedArtifactTransferResultMessage) bool { return r.RequestID == id })
		if !answer.Success {
			t.Fatalf("%s from the relayed app on the laptop's disk: %s", msg.Operation, protocol.Deref(answer.Error))
		}
		return answer
	}
	source := filepath.Join(w.laptop.Dir, "from-laptop.md")
	writeFileAt(t, source, "written on the laptop")
	copied := transfer(protocol.SeedArtifactTransferMessage{Operation: "copy", SourcePath: &source})
	if into := copied.Result.DestinationPath; !strings.HasPrefix(into, w.laptop.Dir+string(filepath.Separator)) {
		t.Fatalf("the copy ran into %s, want the laptop's copy of the Notebook under %s", into, w.laptop.Dir)
	}
	kept := notebookPath(w.home, "seeds", seed.ID, "from-laptop.md")
	if data, err := os.ReadFile(kept); err != nil || string(data) != "written on the laptop" {
		t.Fatalf("the home's seed folder after the copy returned: %q, %v", data, err)
	}
	targetID := uuid.NewString()
	located := testworld.Request(relayed, protocol.SeedArtifactTargetMessage{
		Cmd: protocol.CmdSeedArtifactTarget, RequestID: targetID, SeedID: seed.ID, RelativeTarget: "from-laptop.md", Purpose: "artifact", Host: &row.ID,
	}, protocol.EventSeedArtifactTargetResult, func(r protocol.SeedArtifactTargetResultMessage) bool { return r.RequestID == targetID })
	if !located.Success {
		t.Fatalf("where the seed's file is on the laptop: %s", protocol.Deref(located.Error))
	}
	opens := protocol.Deref(located.Result.Path)
	if data, err := os.ReadFile(opens); !strings.HasPrefix(opens, w.laptop.Dir+string(filepath.Separator)) || err != nil || string(data) != "written on the laptop" {
		t.Fatalf("the app on the laptop opens %s (%q, %v), want the file in the laptop's copy of the Notebook under %s", opens, data, err, w.laptop.Dir)
	}
	out := filepath.Join(w.laptop.Dir, "out", "from-laptop.md")
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		t.Fatal(err)
	}
	detached := transfer(protocol.SeedArtifactTransferMessage{Operation: "detach", Filename: protocol.Ptr("from-laptop.md"), DestinationPath: &out})
	if from := detached.Result.SourcePath; !strings.HasPrefix(from, w.laptop.Dir+string(filepath.Separator)) {
		t.Fatalf("the detach ran from %s, want the laptop's copy of the Notebook under %s", from, w.laptop.Dir)
	}
	if data, err := os.ReadFile(out); err != nil || string(data) != "written on the laptop" {
		t.Fatalf("the laptop's file after the detach returned: %q, %v", data, err)
	}
	if _, err := os.Stat(kept); !os.IsNotExist(err) {
		t.Fatalf("the home still keeps %s after the detach returned: %v", kept, err)
	}
	t.Logf("copy read %s on the laptop into %s at the home; detach wrote %s on the laptop and left the home's seed without it", source, kept, out)
}

func TestTheHomeSamplesNoProcessForTheAppOnAMachineThatDialsIt(t *testing.T) {
	w := newReverseWorld(t)
	w.join()
	enableAnalytics(w.app)
	elsewhere := exec.Command("sleep", "600")
	if err := elsewhere.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = elsewhere.Process.Kill(); _ = elsewhere.Wait() })
	relayed := w.laptop.TrustedAppSaying(func(hello *protocol.ClientHelloMessage) { hello.AppPid = protocol.Ptr(elsewhere.Process.Pid) })
	local := w.home.TrustedAppSaying(func(hello *protocol.ClientHelloMessage) { hello.AppPid = protocol.Ptr(os.Getpid()) })
	defer local.Close()

	for passed := time.Duration(0); passed <= 20*time.Minute; passed += bridge.LostAfter - time.Second {
		w.home.Advance(bridge.LostAfter - time.Second)
		w.home.AwaitTimer(bridge.LostTimer)
	}
	w.home.AwaitJobsSettled()

	var apps []diag.RoleMemory
	for _, record := range sampleRecords(t, relayed) {
		if record.Name != "process" {
			continue
		}
		var props struct {
			Processes []diag.RoleMemory `json:"processes"`
		}
		if err := json.Unmarshal(record.Props, &props); err != nil {
			t.Fatal(err)
		}
		apps = slices.DeleteFunc(props.Processes, func(p diag.RoleMemory) bool { return p.Role != "app" })
	}
	if len(apps) != 1 || apps[0].Count != 1 {
		t.Fatalf("the home's last sample counts app processes %+v, want only the app on its own machine, pid %d, not the relayed app's pid %d", apps, os.Getpid(), elsewhere.Process.Pid)
	}
}

func TestTheMachineThatDialsItsHomeKeepsItsOwnEditorAndCaptureShortcut(t *testing.T) {
	w := newReverseWorld(t)
	w.join()
	setSetting(t, w.app, "editor_executable", "sh -c home")
	setSetting(t, w.app, "capture.shortcut", "Control+Alt+KeyH")

	machine := w.laptop.MachineApp()
	ours := func(p *testworld.Peer) [2]any {
		return [2]any{p.Initial.Settings["editor_executable"], p.Initial.Settings["capture.shortcut"]}
	}
	for _, key := range []string{"editor_executable", "capture.shortcut"} {
		if got, _ := machine.Initial.Settings[key].(string); got != "" {
			t.Fatalf("the laptop's own daemon gives %s = %q, the home's value; want none of its own yet", key, got)
		}
	}
	setSetting(t, machine, "editor_executable", "sh -c laptop")
	setSetting(t, machine, "capture.shortcut", "Control+Alt+KeyL")

	again := w.laptop.MachineApp()
	if got := ours(again); got != [2]any{"sh -c laptop", "Control+Alt+KeyL"} {
		t.Fatalf("the laptop's own daemon keeps %q, want the editor and shortcut set on it", got)
	}
	home := w.home.TrustedApp()
	if got := ours(home); got != [2]any{"sh -c home", "Control+Alt+KeyH"} {
		t.Fatalf("the home keeps %q after the laptop set its own, want the home's values", got)
	}
}
