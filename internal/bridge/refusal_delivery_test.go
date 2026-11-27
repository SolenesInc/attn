package bridge_test

import (
	"errors"
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/bridge"
)

// sshChild models the ssh process a machine that dials its home writes through: it forwards what is written once the
// writer is idle, and a Close kills it, dropping what it has not forwarded yet.
type sshChild struct {
	net.Conn
	mu      sync.Mutex
	pending [][]byte
	killed  bool
	queued  chan struct{}
	ended   chan struct{}
}

func newSSHChild(conn net.Conn) *sshChild {
	c := &sshChild{Conn: conn, queued: make(chan struct{}, 1), ended: make(chan struct{})}
	go c.forward()
	return c
}

func (c *sshChild) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.killed {
		return 0, net.ErrClosed
	}
	c.pending = append(c.pending, append([]byte(nil), data...))
	select {
	case c.queued <- struct{}{}:
	default:
	}
	return len(data), nil
}

func (c *sshChild) forward() {
	defer close(c.ended)
	for range c.queued {
		synctest.Wait()
		c.mu.Lock()
		batch := c.pending
		c.pending = nil
		killed := c.killed
		c.mu.Unlock()
		if killed {
			return
		}
		for _, data := range batch {
			if _, err := c.Conn.Write(data); err != nil {
				return
			}
		}
	}
}

func (c *sshChild) Close() error {
	c.mu.Lock()
	if !c.killed {
		c.killed, c.pending = true, nil
		close(c.queued)
	}
	c.mu.Unlock()
	return c.Conn.Close()
}

func TestARefusalReachesTheDialerThroughAPipeThatDropsWhatItHoldsWhenClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := pair(t)
		served := make(chan error, 1)
		var returned time.Time
		children := make(chan *sshChild, 1)
		go func() {
			raw, err := w.network.Accept()
			if err != nil {
				served <- err
				return
			}
			child := newSSHChild(raw)
			children <- child
			err = bridge.ServeConn(w.ctx, child, w.cfg, func(*bridge.Conn) {})
			returned = time.Now()
			served <- err
		}()
		started := time.Now()
		other := home
		other.Build.Version = "other"
		_, _, err := w.dial(other, nil)
		var refusal *bridge.Refusal
		if !errors.As(err, &refusal) || refusal.Reason != "build" {
			t.Fatalf("the dialer read %v, want the server's build refusal", err)
		}
		if err := <-served; !errors.As(err, &refusal) || refusal.Reason != "build" {
			t.Fatalf("ServeConn returned %v, want its build refusal", err)
		}
		if waited := returned.Sub(started); waited >= bridge.AnswerWithin {
			t.Fatalf("the server held a refused bridge %s, until its deadline, though the dialer hung up on reading the refusal", waited)
		}
		<-(<-children).ended
	})
}
