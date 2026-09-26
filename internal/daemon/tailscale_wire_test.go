package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

const tailscaleFakeScript = `#!/bin/sh
state="${0%/*}/state"
if [ -f "$state/failure" ]; then read -r failure < "$state/failure"; echo "$failure" >&2; exit 1; fi
case "$*" in
"status --json")
	echo '{"BackendState":"Running","Self":{"DNSName":"macbook.tail1bfe77.ts.net."}}' ;;
"serve status --json")
	if [ -s "$state/root" ]; then
		read -r root < "$state/root"
		printf '{"Web":{"macbook.tail1bfe77.ts.net:443":{"Handlers":{"/":{"Proxy":"%s"}}}}}\n' "$root"
	else
		echo '{}'
	fi ;;
"serve --bg --https=443 --set-path=/ "*)
	echo "http://$5" > "$state/root" ;;
"serve --https=443 --set-path=/ off")
	: > "$state/root" ;;
*)
	echo "unexpected tailscale $*" >&2; exit 2 ;;
esac
`

type tailscaleFake struct {
	t   *testing.T
	bin string
}

func newTailscaleFake(t *testing.T) tailscaleFake {
	t.Helper()
	bin := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bin, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := []string{bin}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, "tailscale")); err != nil {
			path = append(path, dir)
		}
	}
	t.Setenv("PATH", strings.Join(path, string(os.PathListSeparator)))
	t.Setenv("ATTN_WS_PORT", "49849")
	return tailscaleFake{t: t, bin: bin}
}

func (f tailscaleFake) install() {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.bin, "tailscale"), []byte(tailscaleFakeScript), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func (f tailscaleFake) fail(message string) {
	f.t.Helper()
	f.writeState("failure", message)
}

func (f tailscaleFake) recover() {
	f.t.Helper()
	if err := os.Remove(filepath.Join(f.bin, "state", "failure")); err != nil {
		f.t.Fatal(err)
	}
}

func (f tailscaleFake) serveAtRoot(proxy string) {
	f.t.Helper()
	f.writeState("root", proxy)
}

func (f tailscaleFake) writeState(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.bin, "state", name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f tailscaleFake) root() string {
	f.t.Helper()
	root, err := os.ReadFile(filepath.Join(f.bin, "state", "root"))
	if err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
	return strings.TrimSpace(string(root))
}

func TestTailscaleServeFollowsTheSetting(t *testing.T) {
	tailscale := newTailscaleFake(t)
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		w.advance(0)
		toggle := func(enabled string) protocol.RecordString {
			t.Helper()
			if ack := gardenAdvisorSetSetting(app, "tailscale_enabled", enabled); !protocol.Deref(ack.Success) {
				t.Fatalf("set tailscale_enabled = %s refused: %s", enabled, protocol.Deref(ack.Error))
			}
			return w.App().Initial.Settings
		}
		expect := func(what string, settings protocol.RecordString, want map[string]string) {
			t.Helper()
			for key, value := range want {
				if got, _ := settings[key].(string); !strings.Contains(got, value) || (value == "" && got != "") {
					t.Errorf("%s: %s = %q, want %q", what, key, got, value)
				}
			}
		}

		expect("enabled without tailscale installed", toggle("true"), map[string]string{
			"tailscale_status": "unavailable", "tailscale_error": "install Tailscale",
		})

		toggle("false")
		tailscale.install()
		tailscale.fail("backend exploded")
		expect("enabled while tailscale fails", toggle("true"), map[string]string{
			"tailscale_status": "error", "tailscale_error": "backend exploded",
		})

		tailscale.recover()
		toggle("false")
		expect("enabled on a running device", toggle("true"), map[string]string{
			"tailscale_status": "running", "tailscale_domain": "macbook.tail1bfe77.ts.net",
			"tailscale_url": "https://macbook.tail1bfe77.ts.net/", "tailscale_error": "",
		})
		if got := tailscale.root(); got != "http://127.0.0.1:49849" {
			t.Errorf("serve publishes %q at the tailnet root, want attn's http://127.0.0.1:49849", got)
		}

		expect("disabled", toggle("false"), map[string]string{"tailscale_status": "disabled", "tailscale_error": ""})
		if got := tailscale.root(); got != "" {
			t.Errorf("disabling left %q at the tailnet root, want attn's handler cleared", got)
		}

		tailscale.serveAtRoot("http://127.0.0.1:3000")
		expect("enabled over another service", toggle("true"), map[string]string{
			"tailscale_status": "conflict", "tailscale_error": "127.0.0.1:3000",
		})
		expect("disabled over another service", toggle("false"), map[string]string{"tailscale_status": "disabled"})
		if got := tailscale.root(); got != "http://127.0.0.1:3000" {
			t.Errorf("the other service's root became %q, want it left at http://127.0.0.1:3000", got)
		}
	})
}
