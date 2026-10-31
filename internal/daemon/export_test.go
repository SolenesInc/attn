package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bridge"
	"github.com/victorarias/attn/internal/bridge/memnet"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/outpostssh"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/store"
)

type WireDaemon struct {
	d       *Daemon
	stopped chan error
}

func StartWireDaemonWithTerminals(socketPath string, unix, ws net.Listener, terminals ptybackend.Backend, nativeWatch bool, clock *stepclock.Clock, gardenClock func() time.Time) (*WireDaemon, error) {
	d := New(socketPath)
	d.outpost.listen = func(string, string) (net.Listener, error) { return memnet.Listen(), nil }
	d.notebookSyncWatch = nativeWatch
	d.clock = clock
	if gardenClock != nil {
		d.gardenNow = gardenClock
	}
	if terminals != nil {
		d.ptyBackend = terminals
	} else {
		d.ptyBackend = ptybackend.NewEmbedded(pty.NewManagerWithClock(d.logf, clock))
	}
	if terminals != nil || !nativeWatch {
		d.rootWatch = func(*stepclock.Clock, string, func([]string)) (*notebook.Watcher, error) { return nil, nil }
	}
	d.listener = unix
	d.httpListener = ws
	w := &WireDaemon{d: d, stopped: make(chan error, 1)}
	go func() { w.stopped <- d.Start() }()
	select {
	case <-d.Started():
		return w, nil
	case err := <-w.stopped:
		if d.store == nil {
			return nil, err
		}
		return nil, errors.Join(err, d.store.Close())
	}
}

func (w *WireDaemon) Stop() error {
	w.d.Stop()
	return errors.Join(<-w.stopped, w.d.store.Close())
}

func (w *WireDaemon) RefuseDatabaseCommits() func() {
	return w.d.store.RefuseCommits()
}

func (w *WireDaemon) UseFileStat(stat func(string) (os.FileInfo, error)) {
	w.d.recentFileStat = stat
}

func (w *WireDaemon) UseOutpostSSH(remote outpostRemote) {
	w.d.outposts.remote = func(store.Outpost) outpostRemote { return remote }
}

const BridgeCallRecovery = bridgeCallRecovery

func (w *WireDaemon) UseBridgeDialer(dial func(ctx context.Context, network, address string) (net.Conn, error)) {
	w.d.outposts.remote = func(row store.Outpost) outpostRemote {
		return &wireOutpostSSH{Client: outpostssh.For(row.Destination, "", "default"), dial: dial, build: w.d.bridgeIdentity().Build}
	}
}

type wireOutpostSSH struct {
	*outpostssh.Client
	dial  func(context.Context, string, string) (net.Conn, error)
	build bridge.Build
}

func (s *wireOutpostSSH) EnsureMaster(context.Context) error { return nil }
func (s *wireOutpostSSH) ExitMaster(context.Context) error   { return nil }
func (s *wireOutpostSSH) Probe(context.Context) (outpostssh.Probe, error) {
	return outpostssh.Probe{Platform: "linux/amd64", Build: &s.build}, nil
}
func (s *wireOutpostSSH) Bridge(ctx context.Context) (net.Conn, error) {
	return s.dial(ctx, "ssh", s.Destination)
}
func (s *wireOutpostSSH) Install(context.Context, string, bridge.Build, func(string)) error {
	return nil
}
func (s *wireOutpostSSH) Remove(context.Context, string) error { return nil }

func UseShippedPasteGap(t testing.TB) {
	sessionmonitor.SubmitDelay = shippedSessionInputSubmitDelay
	t.Cleanup(func() { sessionmonitor.SubmitDelay = 0 })
}

// PauseNextHeldAction blocks the next profile action after it ran, while its client's broadcasts are held.
func (w *WireDaemon) PauseNextHeldAction(paused chan<- struct{}, resume <-chan struct{}) {
	var once sync.Once
	pause := func() {
		once.Do(func() {
			paused <- struct{}{}
			<-resume
		})
	}
	w.d.heldActionRan.Store(&pause)
}
