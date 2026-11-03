package daemon_test

import (
	"net"
	"net/http"
	"testing"
)

func TestTheDaemonAnswersOnlyRequestsAddressedToLocalhostOrAnIPAddress(t *testing.T) {
	w := newWorld(t)
	_, port, err := net.SplitHostPort(w.WSAddr)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		path   string
		host   string
		origin string
		want   int
	}{
		{"a page whose name was rebound to loopback", "/ws", "rebound.example:" + port, "http://rebound.example:" + port, http.StatusForbidden},
		{"a rebound page reading health", "/health", "rebound.example:" + port, "", http.StatusForbidden},
		{"a page on a name that only starts like localhost", "/ws", "127.0.0.1:" + port, "http://localhost.rebound.example", http.StatusForbidden},
		{"the packaged app", "/ws", "127.0.0.1:" + port, "tauri://localhost", http.StatusSwitchingProtocols},
		{"the dev app", "/ws", "localhost:" + port, "http://localhost:1420", http.StatusSwitchingProtocols},
		{"a local client that sends no Origin", "/ws", "127.0.0.1:" + port, "", http.StatusSwitchingProtocols},
		{"a client on the IPv6 loopback", "/ws", "[::1]:" + port, "", http.StatusSwitchingProtocols},
		{"health on loopback", "/health", "127.0.0.1:" + port, "", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "http://"+w.WSAddr+c.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = c.host
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			if c.path == "/ws" {
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Upgrade", "websocket")
				req.Header.Set("Sec-WebSocket-Version", "13")
				req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != c.want {
				t.Fatalf("GET %s with Host %q and Origin %q = %d, want %d", c.path, c.host, c.origin, resp.StatusCode, c.want)
			}
		})
	}
}
