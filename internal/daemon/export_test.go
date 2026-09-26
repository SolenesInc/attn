package daemon

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/victorarias/attn/internal/ptybackend"
)

type WireDaemon struct {
	d       *Daemon
	stopped chan error
}

func StartWireDaemon(socketPath string, unix, ws net.Listener) (*WireDaemon, error) {
	return startWireDaemon(New(socketPath), unix, ws)
}

func StartWireDaemonHoldingRecovery(socketPath string, unix, ws net.Listener) (*WireDaemon, func(), error) {
	d := New(socketPath)
	held := &recoveryHeldBackend{
		EmbeddedBackend: d.ptyBackend.(*ptybackend.EmbeddedBackend),
		release:         make(chan struct{}),
		daemonDone:      d.done,
	}
	d.ptyBackend = held
	w, err := startWireDaemon(d, unix, ws)
	return w, sync.OnceFunc(func() { close(held.release) }), err
}

func startWireDaemon(d *Daemon, unix, ws net.Listener) (*WireDaemon, error) {
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

type recoveryHeldBackend struct {
	*ptybackend.EmbeddedBackend
	release    chan struct{}
	daemonDone <-chan struct{}
}

func (b *recoveryHeldBackend) Recover(ctx context.Context) (ptybackend.RecoveryReport, error) {
	select {
	case <-b.release:
	case <-b.daemonDone:
	}
	return b.EmbeddedBackend.Recover(ctx)
}
