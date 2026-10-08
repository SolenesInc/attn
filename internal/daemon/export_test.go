package daemon

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/ptybackend"
)

type WireDaemon struct {
	d       *Daemon
	stopped chan error
}

func StartWireDaemonWithTerminals(socketPath string, unix, ws net.Listener, terminals ptybackend.Backend, gardenClock ...func() time.Time) (*WireDaemon, error) {
	d := New(socketPath)
	if len(gardenClock) > 0 {
		d.gardenNow = gardenClock[0]
	}
	if terminals != nil {
		d.ptyBackend = terminals
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

func UseShippedPasteGap(t testing.TB) {
	sessionInputSubmitDelay = shippedSessionInputSubmitDelay
	t.Cleanup(func() { sessionInputSubmitDelay = 0 })
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
