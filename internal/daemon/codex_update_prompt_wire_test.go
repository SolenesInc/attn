package daemon_test

import (
	"bytes"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
)

func TestASharedCodexTerminalOpensPastCodexsOwnUpdatePromptWhenANewerCodexIsOut(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	w.SetCodexVersion("0.162.1")
	w.SetCodexLatest("0.163.0")
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	app.TypeLine(session, "add a discount field")
	if got := codex.Prompted(); got != "add a discount field" {
		t.Fatalf("the shared Codex took %q, want the prompt typed into it", got)
	}
	app.AwaitScreen(session, "add a discount field")
	if screen := app.Screen(session); bytes.Contains(screen, []byte("Update available")) {
		t.Errorf("the shared Codex terminal showed Codex's own update prompt: %q", screen)
	}
}
