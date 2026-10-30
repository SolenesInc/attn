package bridge_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/bridge/memnet"
	"golang.org/x/crypto/ssh"
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
	cfg     bridge.ServerConfig
}

func pair(t *testing.T) *pairWorld {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	w := &pairWorld{t: t, ctx: ctx, cancel: cancel, network: memnet.Listen(), server: make(chan *bridge.Conn, 1), served: make(chan error, 1)}
	w.cfg = bridge.ServerConfig{Self: outpost, HostKeyPath: filepath.Join(t.TempDir(), "bridge_host_key")}
	return w
}

func (w *pairWorld) start() {
	w.startOn(w.network)
}

func (w *pairWorld) startOn(listener net.Listener) {
	go func() { w.served <- bridge.Serve(w.ctx, listener, w.cfg, func(c *bridge.Conn) { w.server <- c }) }()
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
	var err error
	cfg.Conn, err = w.network.Dial(w.ctx)
	if err != nil {
		return nil, nil, err
	}
	return bridge.Dial(w.ctx, cfg)
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

func dialPipe(t *testing.T, w *pairWorld) net.Conn {
	t.Helper()
	conn, err := w.network.Dial(w.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}
