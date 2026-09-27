package daemon

import (
	"errors"
	"net"

	"github.com/victorarias/attn/internal/ptybackend"
)

type WireDaemon struct {
	d       *Daemon
	stopped chan error
}

func StartWireDaemon(socketPath string, unix, ws net.Listener) (*WireDaemon, error) {
	return StartWireDaemonWithTerminals(socketPath, unix, ws, nil)
}

func StartWireDaemonWithTerminals(socketPath string, unix, ws net.Listener, terminals ptybackend.Backend) (*WireDaemon, error) {
	d := New(socketPath)
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
		return nil, errors.Join(err, d.store.Close())
	}
}

func (w *WireDaemon) Stop() error {
	w.d.Stop()
	return errors.Join(<-w.stopped, w.d.store.Close())
}
