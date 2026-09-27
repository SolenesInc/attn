package daemon_test

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestClosingAPaneEndsItsProcessWhateverSignalsItIgnores(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	cwd := w.Path("shop")
	programs := map[string]string{
		"ignores SIGTERM":                 `trap "" TERM`,
		"loses the first SIGHUP":          `hups=0; trap "hups=\$((hups+1)); [ \$hups -ge 2 ] && echo cleanly >&3 && exit 0" HUP`,
		"ignores both SIGTERM and SIGHUP": `trap "" TERM HUP`,
	}
	type ended struct {
		said []byte
		err  error
	}
	gone := map[string]chan ended{}
	panes := map[string]string{}
	var ws string
	for name, traps := range programs {
		result, workspace, pane := w.RequestSpawn(app, workspaceShell, cwd)
		ws, panes[name] = workspace, pane
		if !result.Success {
			t.Fatalf("spawn for %q failed: %s", name, protocol.Deref(result.Error))
		}
		held := filepath.Join(w.Dir, "held-"+pane)
		if err := syscall.Mkfifo(held, 0o600); err != nil {
			t.Fatal(err)
		}
		gone[name] = make(chan ended, 1)
		go func(done chan<- ended) {
			f, err := os.Open(held)
			if err != nil {
				done <- ended{err: err}
				return
			}
			said, err := io.ReadAll(f)
			_ = f.Close()
			done <- ended{said: said, err: err}
		}(gone[name])
		app.TypeLine(result.ID, `exec bash -c '`+traps+`; echo held-$((6*7)); while :; do sleep 1; done' 3>`+held)
		app.AwaitScreen(result.ID, "held-42")
	}
	for name, pane := range panes {
		closed := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{
			Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: ws, PaneID: pane,
		}, protocol.CmdWorkspaceLayoutClosePane, ws)
		if !closed.Success {
			t.Fatalf("closing the pane that %s failed: %s", name, protocol.Deref(closed.Error))
		}
	}
	for name, done := range gone {
		select {
		case end := <-done:
			if end.err != nil {
				t.Errorf("watching the program that %s: %v", name, end.err)
			}
			if clean := name == "loses the first SIGHUP"; clean != (string(end.said) == "cleanly\n") {
				t.Errorf("the program that %s said %q on its way out, want a clean exit only from the one that heeds SIGHUP", name, end.said)
			}
		case <-time.After(2 * fakeagent.HangGuard):
			t.Errorf("the program that %s still runs %s after its pane closed", name, 2*fakeagent.HangGuard)
		}
	}
}
