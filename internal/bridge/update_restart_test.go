package bridge_test

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/victorarias/attn/internal/bridge"
)

func updatePair(t *testing.T, restart func()) (*pairWorld, string) {
	t.Helper()
	w := pair(t)
	w.cfg.Update = &bridge.UpdateServer{
		Target: bridge.UpdateTarget{Platform: "linux/amd64"},
		Receive: func(_ context.Context, _ bridge.UpdateHeader, body io.Reader, verifying func()) error {
			_, err := io.Copy(io.Discard, body)
			verifying()
			return err
		},
		Restart: restart,
	}
	binary := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(binary, []byte("next build"), 0600); err != nil {
		t.Fatal(err)
	}
	return w, binary
}

func TestOutpostUpdateRetiresItsListenerBeforeRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		restarting, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		resume := func() { once.Do(func() { close(release) }) }
		w, binary := updatePair(t, func() { close(restarting); <-release })
		w.start()
		t.Cleanup(resume)
		conn, _, err := bridge.DialUpdate(w.ctx, bridge.DialConfig{Conn: dialPipe(t, w), Self: home})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		done := make(chan error, 1)
		go func() { done <- conn.Send(w.ctx, home.Build, binary, func(string) {}) }()
		<-restarting
		old, err := w.network.Dial(w.ctx)
		if old != nil {
			_ = old.Close()
		}
		resume()
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("restarting outpost still accepts connections on its old build: %v", err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

type heldDisconnectListener struct {
	net.Listener
	closing chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (l *heldDisconnectListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &heldDisconnectConn{Conn: conn, listener: l}, nil
}

type heldDisconnectConn struct {
	net.Conn
	listener *heldDisconnectListener
}

func (c *heldDisconnectConn) Close() error {
	l := c.listener
	l.once.Do(func() { close(l.closing) })
	<-l.release
	return c.Conn.Close()
}

func TestOutpostUpdateWaitsForTheRestartingConnectionToClose(t *testing.T) {
	for _, cancelSend := range []bool{false, true} {
		name := "disconnect"
		if cancelSend {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w, binary := updatePair(t, func() {})
				release := make(chan struct{})
				var once sync.Once
				resume := func() { once.Do(func() { close(release) }) }
				listener := &heldDisconnectListener{Listener: w.network, closing: make(chan struct{}), release: release}
				w.startOn(listener)
				t.Cleanup(resume)
				ctx, cancel := context.WithCancel(w.ctx)
				defer cancel()
				conn, _, err := bridge.DialUpdate(ctx, bridge.DialConfig{Conn: dialPipe(t, w), Self: home})
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				done := make(chan error, 1)
				restarting := make(chan struct{})
				go func() {
					done <- conn.Send(ctx, home.Build, binary, func(phase string) {
						if phase == "restarting" {
							close(restarting)
						}
					})
				}()
				<-restarting
				<-listener.closing
				synctest.Wait()
				select {
				case err := <-done:
					resume()
					t.Fatalf("update returned before the old connection closed: %v", err)
				default:
				}
				if cancelSend {
					cancel()
				} else {
					resume()
				}
				err = <-done
				resume()
				if cancelSend && !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled update: %v", err)
				}
				if !cancelSend && err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
