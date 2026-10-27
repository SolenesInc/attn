package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/sessionmonitor"
)

type WireDaemon struct {
	d       *Daemon
	stopped chan error
}

func StartWireDaemonWithTerminals(socketPath string, unix, ws net.Listener, terminals ptybackend.Backend, nativeWatch bool, gardenClock ...func() time.Time) (*WireDaemon, error) {
	d := New(socketPath)
	d.notebookSyncWatch = nativeWatch
	if len(gardenClock) > 0 {
		d.gardenNow = gardenClock[0]
	}
	if terminals != nil {
		d.ptyBackend = terminals
	}
	if terminals != nil || !nativeWatch {
		d.notebookWatch = func(string, func([]string)) (*notebook.Watcher, error) { return nil, nil }
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

const BridgeCallRecovery = bridgeCallRecovery

func (w *WireDaemon) UseBridgeDialer(dial func(ctx context.Context, network, address string) (net.Conn, error)) {
	w.d.outposts.dial = dial
}

func (w *WireDaemon) UseBridgeListener(listen func(string, string) (net.Listener, error)) {
	w.d.outpost.listen = listen
}

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
