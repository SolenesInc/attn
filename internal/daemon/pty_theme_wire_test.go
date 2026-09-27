package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

const askTerminalBackground = `bash -c 'printf "\033]11;?\007"; IFS= read -rs -n 25 r; printf "bg=%s=\n" "${r:5:18}"'`

func TestTheTerminalThemeTheAppSetsAnswersColorQueriesInLiveAndLaterSessions(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	shell := fakeagent.Harness(protocol.AgentShellValue)
	live := w.Spawn(app, shell, w.Path("live"))

	app.Send(protocol.SetTerminalThemeMessage{Cmd: protocol.CmdSetTerminalTheme, Foreground: "red", Background: "#001122", Cursor: "not-a-color"})
	later := w.Spawn(app, shell, w.Path("later"))

	for _, session := range []string{live, later} {
		app.TypeLine(session, askTerminalBackground)
		app.AwaitScreen(session, "bg=rgb:0000/1111/2222=")
	}
}
