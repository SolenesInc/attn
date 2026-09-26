package daemon_test

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
)

func TestTheDiagnosticsServerIsOptInAndLoopbackOnly(t *testing.T) {
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(reserved.Addr().(*net.TCPAddr).Port)
	if err := reserved.Close(); err != nil {
		t.Fatal(err)
	}
	loopback := net.JoinHostPort("127.0.0.1", port)
	vars := "http://" + loopback + "/debug/vars"
	dial := func(addr string) error {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err == nil {
			conn.Close()
		}
		return err
	}

	unsetProbes := []string{loopback}
	defaultAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(config.DefaultPprofPort))
	if dial(defaultAddr) == nil {
		t.Logf("something already listens on %s, so the default port cannot show the server stays off", defaultAddr)
	} else {
		unsetProbes = append(unsetProbes, defaultAddr)
	}

	t.Setenv("ATTN_PPROF", "")
	w := newWorld(t, fakeagent.Codex)
	for _, addr := range unsetProbes {
		if dial(addr) == nil {
			t.Fatalf("with ATTN_PPROF unset something answered on %s", addr)
		}
	}

	t.Setenv("ATTN_PPROF", net.JoinHostPort("0.0.0.0", port))
	w.restart()
	app := w.App()
	w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("shop")))
	resp, err := http.Get(vars)
	if err != nil {
		t.Fatalf("with ATTN_PPROF asking for every interface, loopback %s did not answer: %v", vars, err)
	}
	defer resp.Body.Close()
	if external := firstNonLoopbackIPv4ForDiag(t); external == nil {
		t.Log("the host has no non-loopback IPv4 address, so the loopback-only bind cannot be shown")
	} else if addr := net.JoinHostPort(external.String(), port); !errors.Is(dial(addr), syscall.ECONNREFUSED) {
		t.Errorf("with ATTN_PPROF asking for every interface, %s was not refused; the diagnostics server must bind loopback only", addr)
	}
	var reported struct {
		PtyBackend string `json:"pty_backend"`
		Sessions   int    `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reported); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("%s answered %s with a body that is not the vars JSON: %v", vars, resp.Status, err)
	}
	if backend := app.Initial.Settings["pty_backend_mode"]; reported.PtyBackend != backend || reported.Sessions != 1 {
		t.Errorf("/debug/vars reports backend %q with %d sessions, want the app's %v with the one spawned session", reported.PtyBackend, reported.Sessions, backend)
	}
}

func firstNonLoopbackIPv4ForDiag(t *testing.T) net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			return ipNet.IP.To4()
		}
	}
	return nil
}
