package daemon_test

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAPluginThatStopsReadingItsSocketIsDroppedAtTheWriteDeadlineWithoutStallingTheDaemon(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		installPluginTheTestPlays(t, w, "stalled-provider")
		conn, err := w.DialUnix()
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		hello, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "hello",
			"params": pluginHelloParams("stalled-provider", pluginWireAPIVersion, 1, "worktree.create")})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write(append(hello, '\n')); err != nil {
			t.Fatal(err)
		}
		if answer, err := bufio.NewReader(conn).ReadString('\n'); err != nil || !strings.Contains(answer, `"ok":true`) {
			t.Fatalf("the hello was answered %q (%v), want ok", answer, err)
		}
		awaitPluginShown(app, "stalled-provider", func(p protocol.PluginInfo) bool { return p.Connected })

		w.advance(2 * time.Second)
		if listed, ok := pluginNamed(listPlugins(app).Plugins, "stalled-provider"); !ok || !listed.Connected {
			t.Fatalf("while the daemon's health check sat unread the plugins listed %+v, want the daemon answering and the plugin still connected", listed)
		}
		w.advance(time.Second)
		awaitPluginShown(app, "stalled-provider", func(p protocol.PluginInfo) bool { return !p.Connected })
	})
}
