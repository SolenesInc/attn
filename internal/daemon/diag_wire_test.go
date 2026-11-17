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
	dial := func(addr string) error {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err == nil {
			conn.Close()
		}
		return err
	}

	defaultAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(config.DefaultPprofPort))
	defaultFree := dial(defaultAddr) != nil
	if !defaultFree {
		t.Logf("something already listens on %s, so the default port cannot show the server stays off", defaultAddr)
	}

	t.Setenv("ATTN_PPROF", "")
	w := newWorld(t, fakeagent.Claude)
	if defaultFree && dial(defaultAddr) == nil {
		t.Fatalf("with ATTN_PPROF unset something answered on %s", defaultAddr)
	}

	w.diagHost = "0.0.0.0"
	w.restart()
	port := w.diagPort
	vars := diagVars(port)
	app := w.App()
	w.Launched(w.Spawn(app, fakeagent.Claude, w.Path("shop")))
	resp, err := http.Get(vars)
	if err != nil {
		t.Fatalf("with ATTN_PPROF asking for every interface, loopback %s did not answer: %v", vars, err)
	}
	defer resp.Body.Close()
	if external := firstNonLoopbackIPv4ForDiag(t); external == nil {
		t.Log("the host has no non-loopback IPv4 address, so the loopback-only bind cannot be shown")
	} else {
		addr := net.JoinHostPort(external.String(), port)
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		var netErr net.Error
		switch {
		case err == nil:
			conn.Close()
			t.Errorf("with ATTN_PPROF asking for every interface, %s accepted a connection; the diagnostics server must bind loopback only", addr)
		case errors.As(err, &netErr) && netErr.Timeout():
			t.Logf("%s timed out instead of refusing, so this host's firewall hides whether the bind is loopback only", addr)
		case !errors.Is(err, syscall.ECONNREFUSED):
			t.Errorf("with ATTN_PPROF asking for every interface, dialing %s failed with %v, not a refusal", addr, err)
		}
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

func diagVars(port string) string {
	return "http://" + net.JoinHostPort("127.0.0.1", port) + "/debug/vars"
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
