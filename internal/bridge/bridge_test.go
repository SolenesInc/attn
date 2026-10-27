package bridge_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/bridge/memnet"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

var home = bridge.Identity{DaemonID: "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "home", Instance: "default", Build: bridge.Build{Version: "test", SourceFingerprint: "unknown", ProtocolVersion: "test"}}
var outpost = bridge.Identity{DaemonID: "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Name: "outpost", Instance: "default", Build: home.Build}

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func publicKey(t *testing.T, key ed25519.PrivateKey) string {
	t.Helper()
	pub, err := ssh.NewPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
}

type pairWorld struct {
	t       *testing.T
	ctx     context.Context
	cancel  context.CancelFunc
	network *memnet.Listener
	server  chan *bridge.Conn
	served  chan error
	workers sync.WaitGroup
	keyring agent.Agent
	cfg     bridge.ServerConfig
}

func pair(t *testing.T) *pairWorld {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	key := testKey(t)
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: key}); err != nil {
		t.Fatal(err)
	}
	w := &pairWorld{t: t, ctx: ctx, cancel: cancel, network: memnet.Listen(), server: make(chan *bridge.Conn, 1), served: make(chan error, 1), keyring: keyring}
	w.cfg = bridge.ServerConfig{Self: outpost, HostKeyPath: filepath.Join(t.TempDir(), "bridge_host_key"), AuthorizedKeys: func() ([]byte, error) { return []byte(publicKey(t, key)), nil }}
	return w
}

func (w *pairWorld) start() {
	go func() { w.served <- bridge.Serve(w.ctx, w.network, w.cfg, func(c *bridge.Conn) { w.server <- c }) }()
	w.t.Cleanup(func() {
		w.cancel()
		if err := <-w.served; err != nil {
			w.t.Errorf("Serve: %v", err)
		}
		w.workers.Wait()
	})
}

func (w *pairWorld) dial(identity bridge.Identity, pin *bridge.Pin) (*bridge.Conn, *bridge.Pin, error) {
	return w.dialConfig(bridge.DialConfig{Self: identity, Pin: pin})
}

func (w *pairWorld) dialWith(handlers bridge.Handlers) (*bridge.Conn, *bridge.Pin, error) {
	return w.dialConfig(bridge.DialConfig{Self: home, Handlers: handlers})
}

func (w *pairWorld) dialConfig(cfg bridge.DialConfig) (*bridge.Conn, *bridge.Pin, error) {
	cfg.Address, cfg.AgentSocket = "outpost", "agent"
	cfg.DialContext = w.dialContext
	return bridge.Dial(w.ctx, cfg)
}

func (w *pairWorld) dialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	if network == "tcp" {
		return w.network.Dial(ctx)
	}
	a, b := net.Pipe()
	w.workers.Go(func() { defer b.Close(); _ = agent.ServeAgent(w.keyring, b) })
	return a, nil
}

func state(t *testing.T, c *bridge.Conn, want bridge.State) {
	t.Helper()
	if got := <-c.States(); got != want {
		t.Fatalf("state = %q, want %q", got, want)
	}
}

func TestBridgeLiveness(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		t.Run(fmt.Sprintf("recover=%v", recovery), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := pair(t)
				w.start()
				client, pin, err := w.dial(home, nil)
				if err != nil {
					t.Fatal(err)
				}
				server := <-w.server
				if pin.DaemonID != outpost.DaemonID || pin.Fingerprint() == "" {
					t.Fatalf("pin = %+v", pin)
				}
				state(t, client, bridge.StateConnected)
				state(t, server, bridge.StateConnected)
				time.Sleep(20 * time.Second)
				synctest.Wait()
				if client.Peer() != outpost || server.Peer() != home {
					t.Fatal("wrong peer identity")
				}
				w.network.Cut(true)
				time.Sleep(3 * time.Second)
				synctest.Wait()
				state(t, client, bridge.StateStalled)
				state(t, server, bridge.StateStalled)
				if recovery {
					time.Sleep(2 * time.Second)
					w.network.Cut(false)
					time.Sleep(bridge.HeartbeatInterval)
					synctest.Wait()
					state(t, client, bridge.StateConnected)
					state(t, server, bridge.StateConnected)
				} else {
					time.Sleep(7 * time.Second)
					synctest.Wait()
					state(t, client, bridge.StateLost)
					state(t, server, bridge.StateLost)
					if !strings.Contains(client.Err().Error(), "10s") {
						t.Fatal(client.Err())
					}
				}
			})
		})
	}
}

func TestBridgePinsAndBuild(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		w.start()
		client, pin, err := w.dial(home, nil)
		if err != nil {
			t.Fatal(err)
		}
		<-w.server
		_ = client.Close()
		wrongID := *pin
		wrongID.DaemonID = "d-cccccccccccccccccccccccccccccccc"
		wrongKey := *pin
		wrongKey.HostKey = publicKey(t, testKey(t))
		for _, changed := range []*bridge.Pin{&wrongID, &wrongKey} {
			_, _, err := w.dial(home, changed)
			var refusal *bridge.Refusal
			if !errors.As(err, &refusal) || refusal.Reason != "identity" {
				t.Fatalf("pin refusal = %v", err)
			}
		}
		for _, field := range []string{"version", "source", "protocol"} {
			changed := home
			switch field {
			case "version":
				changed.Build.Version = "other"
			case "source":
				changed.Build.SourceFingerprint = "known"
			case "protocol":
				changed.Build.ProtocolVersion = "other"
			}
			_, _, err := w.dial(changed, pin)
			var refusal *bridge.Refusal
			if !errors.As(err, &refusal) || refusal.Reason != "build" || !strings.Contains(err.Error(), home.DaemonID) || !strings.Contains(err.Error(), outpost.DaemonID) {
				t.Fatalf("%s refusal = %v", field, err)
			}
		}
	})
}

func TestAuthorizedKeys(t *testing.T) {
	key := publicKey(t, testKey(t))
	for _, tc := range []struct {
		name, line     string
		keys, warnings int
	}{
		{"plain", key, 1, 0}, {"comment", key + " user@machine", 1, 0}, {"blank", "\n # comment\n", 0, 0},
		{"command", `command="echo hello" ` + key, 0, 1}, {"restrict", "restrict " + key, 0, 1},
		{"from", `from="10.0.0.*",no-pty ` + key, 0, 1}, {"invalid", "ssh-ed25519 invalid", 0, 1},
		{"mixed", "# comment\nrestrict " + key + "\n" + key, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var warnings []string
			keys := bridge.AuthorizedKeys([]byte(tc.line), func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) })
			if len(keys) != tc.keys || len(warnings) != tc.warnings {
				t.Fatalf("keys=%d warnings=%v", len(keys), warnings)
			}
			if tc.name == "mixed" && !strings.Contains(warnings[0], "line 2") {
				t.Fatal(warnings)
			}
		})
	}
}

func TestBridgeCancellationDuringHandshake(t *testing.T) {
	for _, phase := range []string{"ssh", "hello", "connected"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := pair(t)
				admitted := make(chan struct{})
				if phase == "hello" {
					w.cfg.Admit = func(bridge.Identity) *bridge.Refusal { close(admitted); <-w.ctx.Done(); return nil }
				}
				w.start()
				if phase == "ssh" {
					w.network.Cut(true)
				}
				finished := make(chan error, 1)
				go func() {
					c, _, err := w.dial(home, nil)
					if c != nil {
						<-c.Done()
					}
					finished <- err
				}()
				switch phase {
				case "hello":
					<-admitted
				case "connected":
					<-w.server
				case "ssh":
					synctest.Wait()
				}
				w.cancel()
				<-finished
			})
		})
	}
}

func TestBridgeRereadsAuthorizedKeysAndSkipsRestrictedKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		path := filepath.Join(t.TempDir(), "authorized_keys")
		keys, err := w.cfg.AuthorizedKeys()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append([]byte("restrict "), keys...), 0600); err != nil {
			t.Fatal(err)
		}
		w.cfg.AuthorizedKeys = func() ([]byte, error) { return os.ReadFile(path) }
		w.start()
		_, _, err = w.dial(home, nil)
		var refusal *bridge.Refusal
		if !errors.As(err, &refusal) || refusal.Reason != "auth" {
			t.Fatalf("restricted key admitted: %v", err)
		}
		if err := os.WriteFile(path, keys, 0600); err != nil {
			t.Fatal(err)
		}
		c, _, err := w.dial(home, nil)
		if err != nil {
			t.Fatalf("updated authorized_keys not read: %v", err)
		}
		<-w.server
		_ = c.Close()
	})
}

func TestBridgeBuildRefusalRetainsTheAdmittedConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		var admissions []bridge.Identity
		w.cfg.Admit = func(peer bridge.Identity) *bridge.Refusal { admissions = append(admissions, peer); return nil }
		w.start()
		first, _, err := w.dial(home, nil)
		if err != nil {
			t.Fatal(err)
		}
		server := <-w.server
		changed := home
		changed.Build.Version = "other"
		_, _, err = w.dial(changed, nil)
		var refusal *bridge.Refusal
		if !errors.As(err, &refusal) || refusal.Reason != "build" || refusal.Peer == nil {
			t.Fatalf("build refusal: %v", err)
		}
		synctest.Wait()
		if len(admissions) != 2 || admissions[1] != changed {
			t.Fatalf("home not checked before build refusal: %+v", admissions)
		}
		select {
		case <-server.Done():
			t.Fatal("build refusal closed admitted bridge")
		default:
		}
		_ = first.Close()
	})
}
