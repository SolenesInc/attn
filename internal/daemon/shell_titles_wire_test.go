package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEmbeddedShellPublishesItsTitleAndKeepsDeliveringOutput(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	shell := w.Spawn(app, shellHarness, w.Path("shop"))
	testworld.AwaitSession(app, shell, func(s protocol.Session) bool { return protocol.Deref(s.Title) == "shop" })
	app.TypeLine(shell, `printf '\033]0;embedded prompt\007'; printf 'title output %s\n' delivered`)
	testworld.AwaitSession(app, shell, func(s protocol.Session) bool { return protocol.Deref(s.Title) == "embedded prompt" })
	app.AwaitScreen(shell, "title output delivered")
}
