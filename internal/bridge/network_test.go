package bridge_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"time"
)

type networkGate struct {
	mu      sync.Mutex
	held    bool
	changed chan struct{}
}

func (g *networkGate) Store(held bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.held = held
	if g.changed != nil {
		close(g.changed)
	}
	g.changed = make(chan struct{})
}
func (g *networkGate) wait(closed <-chan struct{}) error {
	for {
		g.mu.Lock()
		held, changed := g.held, g.changed
		g.mu.Unlock()
		if !held {
			return nil
		}
		select {
		case <-changed:
		case <-closed:
			return net.ErrClosed
		}
	}
}

type networkStream struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	closed  bool
	changed chan struct{}
}

func stream() *networkStream     { return &networkStream{changed: make(chan struct{})} }
func (s *networkStream) signal() { close(s.changed); s.changed = make(chan struct{}) }
func (s *networkStream) close()  { s.mu.Lock(); defer s.mu.Unlock(); s.closed = true; s.signal() }

type memoryConn struct {
	read, write *networkStream
	gate        *networkGate
	closed      chan struct{}
	once        sync.Once
	mu          sync.Mutex
	deadline    *time.Timer
}

func (c *memoryConn) Read(data []byte) (int, error) {
	for {
		if err := c.gate.wait(c.closed); err != nil {
			return 0, err
		}
		c.read.mu.Lock()
		if c.read.buffer.Len() > 0 {
			n, err := c.read.buffer.Read(data)
			c.read.mu.Unlock()
			return n, err
		}
		closed, changed := c.read.closed, c.read.changed
		c.read.mu.Unlock()
		if closed {
			return 0, io.EOF
		}
		select {
		case <-changed:
		case <-c.closed:
			return 0, net.ErrClosed
		}
	}
}
func (c *memoryConn) Write(data []byte) (int, error) {
	c.write.mu.Lock()
	defer c.write.mu.Unlock()
	if c.write.closed {
		return 0, net.ErrClosed
	}
	n, err := c.write.buffer.Write(data)
	c.write.signal()
	return n, err
}
func (c *memoryConn) Close() error {
	c.once.Do(func() {
		close(c.closed)
		c.read.close()
		c.write.close()
		c.mu.Lock()
		if c.deadline != nil {
			c.deadline.Stop()
		}
		c.mu.Unlock()
	})
	return nil
}
func (c *memoryConn) LocalAddr() net.Addr  { return memoryAddr{} }
func (c *memoryConn) RemoteAddr() net.Addr { return memoryAddr{} }
func (c *memoryConn) SetDeadline(at time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deadline != nil {
		c.deadline.Stop()
	}
	if !at.IsZero() {
		c.deadline = time.AfterFunc(time.Until(at), func() { _ = c.Close() })
	}
	return nil
}
func (c *memoryConn) SetReadDeadline(at time.Time) error  { return c.SetDeadline(at) }
func (c *memoryConn) SetWriteDeadline(at time.Time) error { return c.SetDeadline(at) }

type memoryListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
	cut    networkGate
}

func memoryNetwork() *memoryListener {
	return &memoryListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}
func (l *memoryListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *memoryListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *memoryListener) Addr() net.Addr { return memoryAddr{} }
func (l *memoryListener) dial(ctx context.Context) (net.Conn, error) {
	a, b := stream(), stream()
	client := &memoryConn{read: a, write: b, gate: &l.cut, closed: make(chan struct{})}
	server := &memoryConn{read: b, write: a, gate: &l.cut, closed: make(chan struct{})}
	select {
	case l.conns <- server:
		return client, nil
	case <-ctx.Done():
		_ = client.Close()
		_ = server.Close()
		return nil, ctx.Err()
	case <-l.closed:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	}
}

type memoryAddr struct{}

func (memoryAddr) Network() string { return "memory" }
func (memoryAddr) String() string  { return "memory" }
