package daemon_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAWatcherThatStopsReadingNeverHoldsUpAnApp(t *testing.T) {
	t.Setenv("ATTN_APP_RUNTIME_HOST", filepath.Join(t.TempDir(), "not-installed"))
	inBubble(t, func(t *testing.T, w *world) {
		cli, app := w.Client(), w.App()
		applyRunningApp(t, cli, appbuild.Manifest{Name: "reviewer", Commands: []appbuild.Command{{Name: "approve"}}}, "export default {}\n")
		conn, err := w.DialUnix()
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if err := json.NewEncoder(conn).Encode(protocol.AppWatchMessage{Cmd: protocol.CmdAppWatch, Name: "reviewer"}); err != nil {
			t.Fatal(err)
		}
		var ack protocol.Response
		if err := json.NewDecoder(conn).Decode(&ack); err != nil || !ack.Ok {
			t.Fatalf("watch reviewer = %+v, %v", ack, err)
		}

		for range 100 {
			requireAppCommandRefused(t, "a command with no runtime", requestAppCommand(app, "reviewer", "approve", ""), "ATTN_APP_RUNTIME_HOST")
		}
		if recorded := appStatus(t, cli, "reviewer").Invocations; recorded != 100 {
			t.Errorf("reviewer recorded %d invocations past a watcher that stopped reading, want all 100", recorded)
		}
	})
}
