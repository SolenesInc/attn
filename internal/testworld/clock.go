package testworld

import (
	"bufio"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/stepclock"
)

type stackClock struct {
	socket   string
	listener net.Listener
	accepted chan struct{}
	closing  chan struct{}
	workers  sync.WaitGroup
	mu       sync.Mutex
	current  *clockPeer
	peers    map[*clockPeer]struct{}
	changed  chan struct{}
	request  sync.Mutex
}

type clockPeer struct {
	conn    net.Conn
	pid     int
	replies chan string
	done    chan struct{}
	err     error
}

func (s *Stack) listenForClock() *stackClock {
	s.T.Helper()
	socket := filepath.Join(s.Dir, "clock.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		s.T.Fatalf("listen for stepped clock: %v", err)
	}
	c := &stackClock{socket: socket, listener: listener, accepted: make(chan struct{}), closing: make(chan struct{}), changed: make(chan struct{}), peers: make(map[*clockPeer]struct{})}
	go func() {
		defer close(c.accepted)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			p := &clockPeer{conn: conn, replies: make(chan string, 1), done: make(chan struct{})}
			c.mu.Lock()
			c.peers[p] = struct{}{}
			c.mu.Unlock()
			c.workers.Go(func() { c.admit(p) })
		}
	}()
	s.T.Cleanup(c.close)
	return c
}

func (c *stackClock) env() string { return stepclock.EnvSocket + "=" + c.socket }

func (c *stackClock) notifyLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *stackClock) admit(p *clockPeer) {
	defer func() {
		p.conn.Close()
		c.mu.Lock()
		delete(c.peers, p)
		if c.current == p {
			c.current = nil
		}
		close(p.done)
		c.notifyLocked()
		c.mu.Unlock()
	}()
	reader := bufio.NewReader(p.conn)
	_ = p.conn.SetReadDeadline(time.Now().Add(fakeagent.HangGuard))
	line, err := reader.ReadString('\n')
	_ = p.conn.SetReadDeadline(time.Time{})
	command, pid, _ := strings.Cut(strings.TrimSpace(line), " ")
	p.pid, p.err = strconv.Atoi(pid)
	if err != nil {
		p.err = err
		return
	}
	if command != "ready" || p.err != nil {
		p.err = fmt.Errorf("invalid stepped clock greeting %q", strings.TrimSpace(line))
		return
	}
	c.mu.Lock()
	c.current = p
	c.notifyLocked()
	c.mu.Unlock()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			p.err = err
			return
		}
		select {
		case p.replies <- strings.TrimSpace(line):
		case <-c.closing:
			return
		}
	}
}

func (c *stackClock) close() {
	close(c.closing)
	_ = c.listener.Close()
	<-c.accepted
	c.mu.Lock()
	for p := range c.peers {
		_ = p.conn.Close()
	}
	c.mu.Unlock()
	c.workers.Wait()
}

func (s *Stack) Advance(d time.Duration) {
	s.T.Helper()
	s.clockCommand("advance", d)
}

func (s *Stack) AwaitTimer(d time.Duration) {
	s.T.Helper()
	s.clockCommand("await", d)
}

func (s *Stack) clockCommand(command string, d time.Duration) {
	s.T.Helper()
	name := "Advance"
	if command == "await" {
		name = "AwaitTimer"
	}
	if d < 0 {
		s.T.Fatalf("%s duration %s is negative", name, d)
	}
	c := s.clock
	c.request.Lock()
	defer c.request.Unlock()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	var p *clockPeer
	for p == nil {
		c.mu.Lock()
		p = c.current
		changed := c.changed
		c.mu.Unlock()
		if p != nil {
			if s.daemon == nil || p.pid == s.daemon.Pid {
				break
			}
			p = nil
		}
		select {
		case <-changed:
		case <-guard.C:
			s.T.Fatalf("%s(%s): no daemon clock connected within %s; saw no timer durations", name, d, fakeagent.HangGuard)
		}
	}
	_ = p.conn.SetWriteDeadline(time.Now().Add(fakeagent.HangGuard))
	_, err := fmt.Fprintf(p.conn, "%s %s\n", command, d)
	_ = p.conn.SetWriteDeadline(time.Time{})
	if err != nil {
		s.T.Fatalf("%s(%s) to daemon %d: %v", name, d, p.pid, err)
	}
	var seen []string
	for {
		select {
		case reply := <-p.replies:
			if reply == "ok" {
				return
			}
			if duration, ok := strings.CutPrefix(reply, "seen "); ok && command == "await" {
				seen = append(seen, duration)
				continue
			}
			s.T.Fatalf("%s(%s): daemon %d answered %q", name, d, p.pid, reply)
		case <-p.done:
			s.T.Fatalf("%s(%s): daemon %d disconnected: %v", name, d, p.pid, p.err)
		case <-guard.C:
			s.T.Fatalf("%s(%s): daemon %d did not acknowledge within %s; saw timer durations [%s]", name, d, p.pid, fakeagent.HangGuard, strings.Join(seen, ", "))
		}
	}
}
