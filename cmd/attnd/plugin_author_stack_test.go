package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
			text = strings.Replace(text, `"0.2.0"`, `"file:`+tarball+`"`, 1)
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

	reloaded := s.Attn("agent", "reload", session, "--source-session", session)
	if reloaded.Code != 0 {
		t.Fatalf("attn agent reload: %s%s", reloaded.Stdout, reloaded.Stderr)
	}
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
