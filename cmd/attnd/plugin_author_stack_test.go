package main_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const exampleHarnessID = "sample"

// packSDK builds the SDK the way an author consumes it, from a tarball outside the checkout.
func packSDK(t *testing.T) string {
	t.Helper()
	out := t.TempDir()
	cmd := exec.Command("bun", "pm", "pack", "--destination", out)
	cmd.Dir = filepath.Join("..", "..", "sdk", "plugin")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bun pm pack: %v\n%s", err, output)
	}
	tarballs, _ := filepath.Glob(filepath.Join(out, "*.tgz"))
	if len(tarballs) != 1 {
		t.Fatalf("packed %v, want one tarball", tarballs)
	}
	return tarballs[0]
}

func TestAnAuthorsHarnessTakesQueuedInputAfterRecovery(t *testing.T) {
	t.Parallel()
	harnessID := "author_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	plugin := authorsPlugin(t, harnessID, packSDK(t))
	root, err := os.MkdirTemp("", "attn-recovery-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socketPath := filepath.Join(root, "recovery.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	entry := filepath.Join(plugin, "src", "index.ts")
	source, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	recoverHandler := fmt.Sprintf(`  recover: async (runs) => {
    if (runs.length === 0) return;
    await runs[0].reportMetadata(runs[0].metadata);
    const socket = createConnection({ path: %q });
    await new Promise<void>((resolve, reject) => {
      socket.once("error", reject);
      socket.once("connect", () => socket.write(JSON.stringify(runs[0].metadata) + "\n"));
      socket.once("data", () => { socket.end(); resolve(); });
    });
  },
`, socketPath)
	changed := strings.Replace(string(source), "client.driver({\n", "client.driver({\n"+recoverHandler, 1)
	if changed == string(source) {
		t.Fatal("sample no longer registers its driver")
	}
	if err := os.WriteFile(entry, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}

	s := testworld.NewStack(t)
	s.Start()
	app := s.App()
	if installed := s.Attn("plugin", "install", "--path", plugin); installed.Code != 0 {
		t.Fatalf("install: %s%s", installed.Stdout, installed.Stderr)
	}
	agent := fakeagent.Harness(harnessID)
	awaitAgentAvailable(app, agent)
	session := s.Spawn(app, agent, s.Path("shop"))
	app.AwaitScreen(session, "sample harness started conversation")
	peer := s.Spawn(app, agent, s.Path("peer"))
	app.AwaitScreen(peer, "sample harness started conversation")

	s.Stop()
	s.Start()
	app = s.App()
	if err := listener.SetDeadline(time.Now().Add(fakeagent.HangGuard)); err != nil {
		t.Fatal(err)
	}
	conn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatalf("plugin recovery did not start: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		ConversationID string `json:"conversationId"`
	}
	if err := json.Unmarshal(line, &metadata); err != nil || metadata.ConversationID == "" {
		t.Fatalf("recovered metadata %q: %v", line, err)
	}
	message := s.Attn("agent", "msg", session, "queued during recovery", "--source-session", peer)
	if message.Code != 0 || !strings.Contains(message.Stdout, "queued") {
		t.Fatalf("message during recovery: exit %d, %s%s", message.Code, message.Stdout, message.Stderr)
	}
	t.Logf("agent msg while recovering: %s", strings.TrimSpace(message.Stdout))
	if _, err := conn.Write([]byte("ready\n")); err != nil {
		t.Fatal(err)
	}
	awaitAgentAvailable(app, agent)
	app.AwaitScreen(session, "You have unread items in your attn inbox")
	inbox := s.Run(testworld.Invocation{Args: []string{"agent", "inbox"}, Session: session})
	if inbox.Code != 0 || !strings.Contains(inbox.Stdout, "queued during recovery") {
		t.Fatalf("inbox after recovery: exit %d, %s%s", inbox.Code, inbox.Stdout, inbox.Stderr)
	}
	t.Logf("agent inbox after recovery: %s", strings.TrimSpace(inbox.Stdout))
}

// authorsPlugin copies the example to a clean directory, gives it harnessID and points it at the packed SDK.
func authorsPlugin(t *testing.T, harnessID, tarball string) string {
	t.Helper()
	source := filepath.Join("..", "..", "examples", "plugins", "sample-harness")
	dir := filepath.Join(t.TempDir(), "my-harness")
	for _, file := range []string{"attn-plugin.toml", "package.json", "src/index.ts", "src/harness.ts", "src/state.ts"} {
		data, err := os.ReadFile(filepath.Join(source, file))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		switch file {
		case "package.json":
			text = strings.Replace(text, `"0.3.0"`, `"file:`+tarball+`"`, 1)
		case "src/index.ts":
			text = strings.Replace(text, `const harnessId = "`+exampleHarnessID+`"`, `const harnessId = "`+harnessID+`"`, 1)
			if !strings.Contains(text, harnessID) {
				t.Fatal("the example no longer declares its harness ID as a constant")
			}
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, file)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, file), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func submitDirectInput(app *testworld.Peer, session, text string) protocol.SessionAnnotationsSubmitResultMessage {
	app.T.Helper()
	requestID := uuid.NewString()
	app.Send(protocol.SessionAnnotationsSubmitMessage{Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: requestID, SessionID: protocol.SessionID(session), Text: text})
	return testworld.Await(app, protocol.EventSessionAnnotationsSubmitResult, func(r protocol.SessionAnnotationsSubmitResultMessage) bool { return r.RequestID == requestID })
}

func TestAnAuthorsInstalledHarnessLaunchesTakesInputAndSurvivesADaemonRestart(t *testing.T) {
	t.Parallel()
	harnessID := "author_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	plugin := authorsPlugin(t, harnessID, packSDK(t))
	s := testworld.NewStack(t)
	s.Start()
	app := s.App()

	installed := s.Attn("plugin", "install", "--path", plugin)
	if installed.Code != 0 {
		t.Fatalf("attn plugin install: %s%s", installed.Stdout, installed.Stderr)
	}
	agent := fakeagent.Harness(harnessID)
	awaitAgentAvailable(app, agent)
	session := s.Spawn(app, agent, s.Path("shop"))
	app.AwaitScreen(session, "sample harness started conversation")

	if got := submitDirectInput(app, session, "check the rounding"); !got.Success {
		t.Fatalf("direct input refused: %+v", got)
	}
	app.AwaitScreen(session, "[user] check the rounding")
	peer := s.Spawn(app, agent, s.Path("peer"))
	notice := s.Attn("agent", "msg", session, "the build is green", "--source-session", peer)
	if notice.Code != 0 {
		t.Fatalf("attn agent msg: %s%s", notice.Stdout, notice.Stderr)
	}
	app.AwaitScreen(session, "[attn]")

	models := testworld.Request(app, protocol.HarnessModelsMessage{Cmd: protocol.CmdHarnessModels, Harness: harnessID, RequestID: "models"},
		protocol.EventHarnessModelsResult, func(r protocol.HarnessModelsResultMessage) bool { return r.RequestID == "models" })
	if len(models.Models) != 0 || !strings.Contains(models.Detail, "does not expose model discovery") {
		t.Fatalf("a harness that omits models offers %+v, want none and the explanation", models)
	}

	s.Stop()
	s.Start()
	app = s.App()
	awaitAgentAvailable(app, agent)
	if got := submitDirectInput(app, session, "after the restart"); !got.Success {
		t.Fatalf("direct input after the daemon restart refused: %+v", got)
	}
	app.AwaitScreen(session, "[user] after the restart")

	terminal := app.Terminal(session)
	reloaded := s.Attn("agent", "reload", session, "--source-session", session)
	if reloaded.Code != 0 {
		t.Fatalf("attn agent reload: %s%s", reloaded.Stdout, reloaded.Stderr)
	}
	testworld.Await(app, protocol.EventRuntimeRespawned, func(e protocol.WebSocketEvent) bool { return protocol.Deref(e.ID) == terminal })
	app.AwaitScreen(session, "sample harness resumed conversation")
}

func TestThePluginSDKPassesItsOwnTests(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("bun", "test")
	cmd.Dir = filepath.Join("..", "..", "sdk", "plugin")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bun test in sdk/plugin: %v\n%s", err, output)
	}
}
