package daemon_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/bridge/memnet"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

var outpostAddress = &net.TCPAddr{IP: net.IPv4(100, 64, 0, 7), Port: 19428}

type outpostLink int

const (
	linkUp outpostLink = iota
	linkResets
	linkRefused
)

type farOutpost struct {
	network *memnet.Listener
	link    atomic.Int32
	port    atomic.Int32
	keyring agent.Agent
	key     ed25519.PrivateKey
	workers sync.WaitGroup
}

func startFarOutpost(t *testing.T, w *world, connected func(*bridge.Conn)) *farOutpost {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	o := &farOutpost{network: memnet.Listen(), keyring: agent.NewKeyring(), key: key}
	if connected == nil {
		connected = func(*bridge.Conn) {}
	}
	o.port.Store(50000)
	if err := o.keyring.Add(agent.AddedKey{PrivateKey: key}); err != nil {
		t.Fatal(err)
	}
	self := bridge.Identity{Instance: "default", DaemonID: "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Build: bridge.Build{Version: buildinfo.Version, SourceFingerprint: buildinfo.SourceFingerprint, ProtocolVersion: protocol.ProtocolVersion}}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- bridge.Serve(ctx, o.network, bridge.ServerConfig{
			Self:           self,
			HostKeyPath:    filepath.Join(t.TempDir(), "bridge_host_key"),
			AuthorizedKeys: func() ([]byte, error) { return ssh.MarshalAuthorizedKey(signer.PublicKey()), nil },
			Handlers: bridge.Handlers{"notebook-configure": func(_ *bridge.Conn, incoming ssh.NewChannel) {
				bridge.Answer(incoming, func(context.Context, []byte) (any, error) { return "", nil })
			}, daemon.BridgeCallRecovery: func(_ *bridge.Conn, incoming ssh.NewChannel) {
				bridge.Answer(incoming, func(context.Context, []byte) (any, error) { return struct{}{}, nil })
			}},
		}, connected)
	}()
	t.Cleanup(func() {
		cancel()
		<-served
		o.workers.Wait()
	})
	w.daemon.UseBridgeDialer(o.dial)
	return o
}

func (o *farOutpost) dial(ctx context.Context, network, _ string) (net.Conn, error) {
	if network == "unix" {
		home, keys := net.Pipe()
		o.workers.Go(func() { defer keys.Close(); _ = agent.ServeAgent(o.keyring, keys) })
		return home, nil
	}
	local := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(o.port.Add(1))}
	switch outpostLink(o.link.Load()) {
	case linkRefused:
		return nil, &net.OpError{Op: "dial", Net: "tcp", Addr: outpostAddress, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	case linkResets:
		home, _ := net.Pipe()
		return &tcpConn{Conn: home, local: local, reset: true}, nil
	}
	conn, err := o.network.Dial(ctx)
	if err != nil {
		return nil, err
	}
	return &tcpConn{Conn: conn, local: local}, nil
}

type tcpConn struct {
	net.Conn
	local  *net.TCPAddr
	remote *net.TCPAddr
	reset  bool
}

func (c *tcpConn) LocalAddr() net.Addr { return c.local }
func (c *tcpConn) RemoteAddr() net.Addr {
	if c.remote != nil {
		return c.remote
	}
	return outpostAddress
}

func (c *tcpConn) Read(data []byte) (int, error) {
	if c.reset {
		return 0, c.fail("read", os.NewSyscallError("read", syscall.ECONNRESET))
	}
	n, err := c.Conn.Read(data)
	return n, c.fail("read", err)
}

func (c *tcpConn) Write(data []byte) (int, error) {
	if c.reset {
		return 0, c.fail("write", os.NewSyscallError("write", syscall.ECONNRESET))
	}
	n, err := c.Conn.Write(data)
	return n, c.fail("write", err)
}

func (c *tcpConn) fail(op string, err error) error {
	if err == nil || err == io.EOF {
		return err
	}
	return &net.OpError{Op: op, Net: "tcp", Source: c.LocalAddr(), Addr: c.RemoteAddr(), Err: err}
}

type inboundBridgeListener struct {
	*memnet.Listener
	port int
}

func (l *inboundBridgeListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.port++
	return &tcpConn{Conn: conn, local: outpostAddress, remote: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: l.port}}, nil
}

func TestAnOutpostListenerPublishesOnlyWhenItsFailureChanges(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		network := &inboundBridgeListener{Listener: memnet.Listen(), port: 50000}
		w.daemon.UseBridgeListener(func(string, string) (net.Listener, error) { return network, nil })
		app := w.App()
		setSetting(t, app, "outpost_listen", outpostAddress.String())
		w.advance(0)
		mark := len(app.Log())
		for _, failure := range []string{"timeout", "closed", "timeout"} {
			for range 3 {
				conn, err := network.Dial(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				w.advance(0)
				if failure == "timeout" {
					w.advance(bridge.LostAfter)
				}
				_ = conn.Close()
				w.advance(0)
			}
		}
		var errors []string
		for _, raw := range app.Log()[mark:] {
			var m protocol.SettingsUpdatedMessage
			if json.Unmarshal(raw, &m) == nil && m.Event == protocol.EventSettingsUpdated && protocol.Deref(m.ChangedKey) == "outpost_last_error" {
				errors = append(errors, fmt.Sprint(m.Settings["outpost_last_error"]))
			}
		}
		t.Logf("outpost_last_error updates: %d\n%s", len(errors), strings.Join(errors, "\n"))
		if len(errors) != 3 || !strings.Contains(errors[0], "timeout") || !strings.Contains(errors[1], "EOF") || !strings.Contains(errors[2], "timeout") {
			t.Fatalf("repeated inbound handshake failures published %d errors, want timeout, closed, timeout", len(errors))
		}
	})
}

func outpostUpdates(app *testworld.Peer, from int) []protocol.OutpostInfo {
	var rows []protocol.OutpostInfo
	for _, raw := range app.Log()[from:] {
		var m protocol.OutpostsUpdatedMessage
		if json.Unmarshal(raw, &m) != nil || m.Event != protocol.EventOutpostsUpdated {
			continue
		}
		if len(m.Outposts) != 1 {
			continue
		}
		rows = append(rows, m.Outposts[0])
	}
	return rows
}

func TestAnUnreachableOutpostPublishesOnlyWhenItsFailureChanges(t *testing.T) {
	for _, tc := range []struct {
		name    string
		link    outpostLink
		failing []string
	}{
		{name: "accepts and never speaks SSH", link: linkUp, failing: []string{"", "no frame for 10s"}},
		{name: "resets every connection", link: linkResets, failing: []string{"", "no frame for 10s", "connection reset by peer"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prepared := prepareWorld(t)
			t.Setenv("SSH_AUTH_SOCK", "agent")
			inPreparedBubble(t, prepared, func(t *testing.T, w *world) {
				far := startFarOutpost(t, w, nil)
				app := w.App()
				setSetting(t, app, "outposts_enabled", "true")
				app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "far", Address: outpostAddress.String()})
				testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
					return len(m.Outposts) == 1 && m.Outposts[0].State == protocol.OutpostStateConnected
				})
				w.advance(time.Second)

				mark := len(app.Log())
				far.network.Cut(true)
				far.link.Store(int32(tc.link))
				w.advance(time.Minute)
				got := outpostUpdates(app, mark)
				if len(got) != len(tc.failing) {
					t.Fatalf("a minute unreachable published %d outpost updates, want %d:\n%s", len(got), len(tc.failing), describeOutposts(got))
				}
				for i, want := range tc.failing {
					wantState := protocol.OutpostStateLost
					if i == 0 {
						wantState = protocol.OutpostStateStalled
					}
					if got[i].State != wantState || !strings.Contains(protocol.Deref(got[i].LastError), want) {
						t.Fatalf("update %d is %s %q, want %s naming %q:\n%s", i, got[i].State, protocol.Deref(got[i].LastError), wantState, want, describeOutposts(got))
					}
				}

				mark = len(app.Log())
				far.link.Store(int32(linkRefused))
				w.advance(time.Minute)
				got = outpostUpdates(app, mark)
				if len(got) != 1 || got[0].State != protocol.OutpostStateLost || !strings.Contains(protocol.Deref(got[0].LastError), "connection refused") {
					t.Fatalf("a refused redial published %d updates, want one naming the refusal:\n%s", len(got), describeOutposts(got))
				}
			})
		})
	}
}

func TestARefusedOutpostPublishesWhenItsRefusalChanges(t *testing.T) {
	prepared := prepareWorld(t)
	t.Setenv("SSH_AUTH_SOCK", "agent")
	inPreparedBubble(t, prepared, func(t *testing.T, w *world) {
		far := startFarOutpost(t, w, nil)
		if err := far.keyring.RemoveAll(); err != nil {
			t.Fatal(err)
		}
		app := w.App()
		setSetting(t, app, "outposts_enabled", "true")
		app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "far", Address: outpostAddress.String()})
		testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
			return len(m.Outposts) == 1 && m.Outposts[0].State == protocol.OutpostStateRefused && strings.Contains(protocol.Deref(m.Outposts[0].LastError), "agent has no keys")
		})
		w.advance(time.Second)
		mark := len(app.Log())
		w.advance(time.Minute)
		if got := outpostUpdates(app, mark); len(got) != 0 {
			t.Fatalf("refusing the same way for a minute published %d updates, want none:\n%s", len(got), describeOutposts(got))
		}

		_, stranger, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := far.keyring.Add(agent.AddedKey{PrivateKey: stranger}); err != nil {
			t.Fatal(err)
		}
		mark = len(app.Log())
		w.advance(time.Minute)
		got := outpostUpdates(app, mark)
		if len(got) != 1 || got[0].State != protocol.OutpostStateRefused || !strings.Contains(protocol.Deref(got[0].LastError), "no key accepted") {
			t.Fatalf("a key the outpost does not accept published %d updates, want one naming it:\n%s", len(got), describeOutposts(got))
		}

		if err := far.keyring.Add(agent.AddedKey{PrivateKey: far.key}); err != nil {
			t.Fatal(err)
		}
		w.advance(bridge.LostAfter)
		testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
			return len(m.Outposts) == 1 && m.Outposts[0].State == protocol.OutpostStateConnected && m.Outposts[0].LastError == nil
		})
	})
}

func describeOutposts(rows []protocol.OutpostInfo) string {
	var lines []string
	for _, row := range rows {
		lines = append(lines, string(row.State)+" "+protocol.Deref(row.LastError))
	}
	return strings.Join(lines, "\n")
}
