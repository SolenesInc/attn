package memnet

import (
	"context"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type Option func(*Listener)

// Rate delivers each direction of a connection at most bytesPerSecond, on the clock the caller runs under.
func Rate(bytesPerSecond float64) Option {
	return func(l *Listener) { l.rate = bytesPerSecond }
}

func Delay(oneWay time.Duration) Option {
	return func(l *Listener) { l.delay.Store(int64(oneWay)) }
}

type Listener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
	cut    gate
	rate   float64
	delay  atomic.Int64
}

func Listen(options ...Option) *Listener {
	l := &Listener{conns: make(chan net.Conn), closed: make(chan struct{})}
	for _, option := range options {
		option(l)
	}
	return l
}

func (l *Listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *Listener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *Listener) Addr() net.Addr { return addr{} }

// Cut holds every byte in flight on every connection until Cut(false).
func (l *Listener) Cut(held bool) { l.cut.store(held) }

func (l *Listener) SetDelay(oneWay time.Duration) { l.delay.Store(int64(oneWay)) }

func (l *Listener) Dial(ctx context.Context) (net.Conn, error) {
	a, b := newStream(l.rate), newStream(l.rate)
	a.delay, b.delay = &l.delay, &l.delay
	client := &conn{read: a, write: b, gate: &l.cut, closed: make(chan struct{})}
	server := &conn{read: b, write: a, gate: &l.cut, closed: make(chan struct{})}
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

type gate struct {
	mu      sync.Mutex
	held    bool
	changed chan struct{}
}

func (g *gate) store(held bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.held = held
	if g.changed != nil {
		close(g.changed)
	}
	g.changed = make(chan struct{})
}

func (g *gate) wait(closed, expired <-chan struct{}) error {
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
		case <-expired:
			return os.ErrDeadlineExceeded
		}
	}
}

type chunk struct {
	data []byte
	due  time.Time
}

type stream struct {
	mu        sync.Mutex
	rate      float64
	chunks    []chunk
	busyUntil time.Time
	closed    bool
	changed   chan struct{}
	delay     *atomic.Int64
}

func newStream(rate float64) *stream { return &stream{rate: rate, changed: make(chan struct{})} }

func (s *stream) signal() { close(s.changed); s.changed = make(chan struct{}) }
func (s *stream) close()  { s.mu.Lock(); defer s.mu.Unlock(); s.closed = true; s.signal() }

func (s *stream) push(data []byte) {
	now := time.Now()
	due := now
	if s.rate > 0 {
		if s.busyUntil.After(now) {
			due = s.busyUntil
		}
		due = due.Add(time.Duration(float64(len(data)) / s.rate * float64(time.Second)))
		s.busyUntil = due
	}
	if s.delay != nil {
		due = due.Add(time.Duration(s.delay.Load()))
	}
	if len(s.chunks) > 0 && s.chunks[len(s.chunks)-1].due.After(due) {
		due = s.chunks[len(s.chunks)-1].due
	}
	s.chunks = append(s.chunks, chunk{data: append([]byte(nil), data...), due: due})
	s.signal()
}

func (s *stream) take(into []byte) int {
	n := 0
	now := time.Now()
	for n < len(into) && len(s.chunks) > 0 && !s.chunks[0].due.After(now) {
		copied := copy(into[n:], s.chunks[0].data)
		n += copied
		if copied == len(s.chunks[0].data) {
			s.chunks = s.chunks[1:]
		} else {
			s.chunks[0].data = s.chunks[0].data[copied:]
		}
	}
	return n
}

type conn struct {
	read, write *stream
	gate        *gate
	closed      chan struct{}
	once        sync.Once
	mu          sync.Mutex
	deadline    *time.Timer
	expired     chan struct{}
}

func (c *conn) Read(data []byte) (int, error) {
	for {
		expired := c.deadlineExpired()
		select {
		case <-expired:
			return 0, os.ErrDeadlineExceeded
		default:
		}
		if err := c.gate.wait(c.closed, expired); err != nil {
			return 0, err
		}
		c.read.mu.Lock()
		if n := c.read.take(data); n > 0 {
			c.read.mu.Unlock()
			return n, nil
		}
		var due <-chan time.Time
		if len(c.read.chunks) > 0 {
			due = time.After(time.Until(c.read.chunks[0].due))
		}
		closed, changed := c.read.closed && len(c.read.chunks) == 0, c.read.changed
		c.read.mu.Unlock()
		if closed {
			return 0, io.EOF
		}
		select {
		case <-changed:
		case <-due:
		case <-c.closed:
			return 0, net.ErrClosed
		case <-expired:
			return 0, os.ErrDeadlineExceeded
		}
	}
}

func (c *conn) Write(data []byte) (int, error) {
	select {
	case <-c.deadlineExpired():
		return 0, os.ErrDeadlineExceeded
	default:
	}
	c.write.mu.Lock()
	defer c.write.mu.Unlock()
	if c.write.closed {
		return 0, net.ErrClosed
	}
	c.write.push(data)
	return len(data), nil
}

func (c *conn) Close() error {
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

func (c *conn) LocalAddr() net.Addr  { return addr{} }
func (c *conn) RemoteAddr() net.Addr { return addr{} }

func (c *conn) SetDeadline(at time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deadline != nil {
		c.deadline.Stop()
	}
	expired := make(chan struct{})
	c.expired = expired
	if !at.IsZero() {
		c.deadline = time.AfterFunc(time.Until(at), func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.expired == expired {
				close(expired)
			}
		})
	}
	return nil
}

func (c *conn) deadlineExpired() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.expired
}

func (c *conn) SetReadDeadline(at time.Time) error  { return c.SetDeadline(at) }
func (c *conn) SetWriteDeadline(at time.Time) error { return c.SetDeadline(at) }

type addr struct{}

func (addr) Network() string { return "memory" }
func (addr) String() string  { return "memory" }
