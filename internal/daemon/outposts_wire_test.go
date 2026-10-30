package daemon_test

import (
	"context"
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

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/bridge/memnet"
	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/outpostssh"
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
	failure atomic.Pointer[outpostssh.Error]
	workers sync.WaitGroup
}

func startFarOutpost(t *testing.T, w *world, connected func(*bridge.Conn)) *farOutpost {
	t.Helper()
	o := &farOutpost{network: memnet.Listen()}
	if connected == nil {
		connected = func(*bridge.Conn) {}
	}
	o.port.Store(50000)
	self := bridge.Identity{Instance: "default", DaemonID: "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Build: bridge.Build{Version: buildinfo.Version, SourceFingerprint: buildinfo.SourceFingerprint, ProtocolVersion: protocol.ProtocolVersion}}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- bridge.Serve(ctx, o.network, bridge.ServerConfig{
			Self:        self,
			HostKeyPath: filepath.Join(t.TempDir(), "bridge_host_key"),
			Handlers: bridge.Handlers{daemon.BridgeCallRecovery: func(_ *bridge.Conn, incoming ssh.NewChannel) {
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
	if failure := o.failure.Load(); failure != nil {
		return nil, failure
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

func TestOutpostRedialsBackOffAndStartFreshAfterRecoveryOrAnExplicitRetry(t *testing.T) {
	refusal := &outpostssh.Error{ExitCode: 255, Stderr: "Permission denied (publickey)."}
	for _, tc := range []struct {
		name    string
		state   protocol.OutpostState
		failure *outpostssh.Error
		delays  []int
	}{
		{name: "lost", state: protocol.OutpostStateLost, delays: []int{2, 4, 8, 16, 32, 64, 128, 128}},
		{name: "refused", state: protocol.OutpostStateRefused, failure: refusal, delays: []int{10, 20, 40, 80, 128, 128}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				far := startFarOutpost(t, w, nil)
				far.link.Store(int32(linkRefused))
				far.failure.Store(tc.failure)
				attempts := make(chan time.Time, 1)
				w.daemon.UseBridgeDialer(func(ctx context.Context, network, address string) (net.Conn, error) {
					select {
					case attempts <- time.Now():
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					return far.dial(ctx, network, address)
				})
				app := w.App()
				setSetting(t, app, "outposts_enabled", "true")
				app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "far", Destination: outpostAddress.String()})
				row := testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
					return len(m.Outposts) == 1 && m.Outposts[0].State == tc.state
				}).Outposts[0]
				var last time.Time
				takeAttempt := func(want time.Time) {
					t.Helper()
					select {
					case at := <-attempts:
						if !at.Equal(want) {
							t.Fatalf("redial at %s, want %s", at, want)
						}
						last = at
					default:
						t.Fatalf("no redial at %s", want)
					}
				}
				noAttempt := func() {
					t.Helper()
					select {
					case at := <-attempts:
						t.Fatalf("unexpected redial after %s", at.Sub(last))
					default:
					}
				}
				w.advance(0)
				takeAttempt(time.Now())
				waits := func(seconds ...int) {
					t.Helper()
					for _, seconds := range seconds {
						delay := time.Duration(seconds) * time.Second
						want := last.Add(delay)
						w.advance(delay - time.Nanosecond)
						noAttempt()
						w.advance(time.Nanosecond)
						takeAttempt(want)
					}
				}
				waits(tc.delays...)

				far.link.Store(int32(linkUp))
				far.failure.Store(nil)
				mark := app.EventCount()
				waits(128)
				testworld.AwaitSince(app, mark, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
					return len(m.Outposts) == 1 && m.Outposts[0].State == protocol.OutpostStateConnected
				})
				far.link.Store(int32(linkRefused))
				far.failure.Store(tc.failure)
				far.network.Cut(true)
				mark = app.EventCount()
				w.advance(bridge.LostAfter)
				testworld.AwaitSince(app, mark, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
					return len(m.Outposts) == 1 && m.Outposts[0].State == tc.state
				})
				takeAttempt(time.Now())
				waits(tc.delays[:3]...)

				now := time.Now()
				app.Send(protocol.RepairOutpostMessage{Cmd: protocol.CmdRepairOutpost, ID: row.ID})
				w.advance(0)
				takeAttempt(now)
				waits(tc.delays[:3]...)

				setSetting(t, app, "outposts_enabled", "false")
				w.advance(128 * time.Second)
				noAttempt()
				now = time.Now()
				setSetting(t, app, "outposts_enabled", "true")
				w.advance(0)
				takeAttempt(now)
				waits(tc.delays[:3]...)
				if tc.failure == nil {
					far.failure.Store(refusal)
				} else {
					far.failure.Store(nil)
				}
				waits(tc.delays[3:5]...)
				far.failure.Store(tc.failure)
				waits(tc.delays[5:]...)
				t.Logf("%s waits %v seconds; connection, Re-pair and Outposts on reset them; changing failure kinds preserves backoff", tc.name, tc.delays)
			})
		})
	}
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
			inPreparedBubble(t, prepared, func(t *testing.T, w *world) {
				far := startFarOutpost(t, w, nil)
				app := w.App()
				setSetting(t, app, "outposts_enabled", "true")
				app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "far", Destination: outpostAddress.String()})
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
	inPreparedBubble(t, prepared, func(t *testing.T, w *world) {
		far := startFarOutpost(t, w, nil)
		far.failure.Store(&outpostssh.Error{ExitCode: 255, Stderr: "Permission denied (publickey)."})
		app := w.App()
		setSetting(t, app, "outposts_enabled", "true")
		app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "far", Destination: outpostAddress.String()})
		testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
			return len(m.Outposts) == 1 && m.Outposts[0].State == protocol.OutpostStateRefused && strings.Contains(protocol.Deref(m.Outposts[0].LastError), "Permission denied")
		})
		w.advance(time.Second)
		mark := len(app.Log())
		w.advance(time.Minute)
		if got := outpostUpdates(app, mark); len(got) != 0 {
			t.Fatalf("refusing the same way for a minute published %d updates, want none:\n%s", len(got), describeOutposts(got))
		}

		far.failure.Store(&outpostssh.Error{ExitCode: 255, Stderr: "Host key verification failed."})
		mark = len(app.Log())
		w.advance(time.Minute)
		got := outpostUpdates(app, mark)
		if len(got) != 1 || got[0].State != protocol.OutpostStateRefused || !strings.Contains(protocol.Deref(got[0].LastError), "Host key verification failed") {
			t.Fatalf("a key the outpost does not accept published %d updates, want one naming it:\n%s", len(got), describeOutposts(got))
		}

		far.failure.Store(nil)
		w.advance(128 * time.Second)
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

type refusingInstallSSH struct {
	*outpostssh.Client
	probes, installs int
}

func (r *refusingInstallSSH) EnsureMaster(context.Context) error { return nil }
func (r *refusingInstallSSH) ExitMaster(context.Context) error   { return nil }
func (r *refusingInstallSSH) Remove(context.Context) error       { return nil }
func (r *refusingInstallSSH) Probe(context.Context) (outpostssh.Probe, error) {
	r.probes++
	return outpostssh.Probe{Platform: "linux/amd64", Build: &bridge.Build{Version: "earlier", SourceFingerprint: "git:earlier", ProtocolVersion: protocol.ProtocolVersion}}, nil
}
func (r *refusingInstallSSH) Install(_ context.Context, _ string, _ bridge.Build, phase func(string)) error {
	r.installs++
	phase("verifying")
	return fmt.Errorf("pty_shared_host_enabled is on")
}
func (r *refusingInstallSSH) Bridge(context.Context) (net.Conn, error) {
	return nil, fmt.Errorf("cannot bridge a refused install")
}

func TestAnUnpairedOutpostDoesNotRepeatARefusedInstall(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		t.Setenv("ATTN_HARNESS_DATA_DIR", w.Dir)
		t.Setenv("ATTN_HARNESS_OUTPOST_BINARY", "fixture-build")
		ssh := &refusingInstallSSH{Client: outpostssh.For("fixture", "unused", "default")}
		w.daemon.UseOutpostSSH(ssh)
		app := w.App()
		setSetting(t, app, "outposts_enabled", "true")
		app.Send(protocol.AddOutpostMessage{Cmd: protocol.CmdAddOutpost, Name: "outpost", Destination: "fixture"})
		var row protocol.OutpostInfo
		testworld.Await(app, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
			if len(m.Outposts) != 1 {
				return false
			}
			row = m.Outposts[0]
			return row.State == protocol.OutpostStateRefused && row.Update != nil && row.Update.Phase == protocol.OutpostUpdatePhaseFailed
		})
		if !strings.Contains(protocol.Deref(row.Update.Outcome), "pty_shared_host_enabled") {
			t.Fatalf("refusal reason lost: %+v", row.Update)
		}
		for _, delay := range []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second} {
			w.advance(delay)
		}
		if ssh.installs != 1 || ssh.probes < 4 {
			t.Fatalf("after retries: installs=%d probes=%d, want one install and continued probes", ssh.installs, ssh.probes)
		}
		mark := app.EventCount()
		app.Send(protocol.UpdateOutpostMessage{Cmd: protocol.CmdUpdateOutpost, ID: row.ID})
		testworld.AwaitSince(app, mark, protocol.EventOutpostsUpdated, func(m protocol.OutpostsUpdatedMessage) bool {
			return len(m.Outposts) == 1 && m.Outposts[0].Update != nil && m.Outposts[0].Update.Phase == protocol.OutpostUpdatePhaseFailed
		})
		for _, delay := range []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second} {
			w.advance(delay)
		}
		if ssh.installs != 2 || ssh.probes < 8 {
			t.Fatalf("explicit Update and later retries: installs=%d probes=%d, want two installs and continued probes", ssh.installs, ssh.probes)
		}
		t.Logf("Add refused once, probes continued across retries, and explicit Update attempted once more: installs=%d probes=%d", ssh.installs, ssh.probes)
	})
}
