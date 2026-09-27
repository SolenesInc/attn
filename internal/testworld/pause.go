package testworld

import (
	"bufio"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
)

type Pause struct {
	t      testing.TB
	name   string
	held   chan pausedAt
	paused *pausedAt
}

type pausedAt struct {
	conn net.Conn
	pid  int
}

type pauses struct {
	socket   string
	listener net.Listener
	mu       sync.Mutex
	points   map[string]*Pause
	order    []string
	admits   sync.WaitGroup
}

func (s *Stack) PauseAt(name string) *Pause {
	s.T.Helper()
	if s.daemon != nil {
		s.T.Fatalf("PauseAt(%q): arm pause points before Start; the daemon and its workers read them once", name)
	}
	if s.pauses == nil {
		s.pauses = s.listenForPauses()
	}
	p := &Pause{t: s.T, name: name, held: make(chan pausedAt, 8)}
	s.pauses.mu.Lock()
	s.pauses.points[name] = p
	s.pauses.order = append(s.pauses.order, name)
	s.pauses.mu.Unlock()
	return p
}

func (s *Stack) listenForPauses() *pauses {
	socket := filepath.Join(s.Dir, "pause.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		s.T.Fatalf("listen for pause points: %v", err)
	}
	ps := &pauses{socket: socket, listener: listener, points: map[string]*Pause{}}
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			ps.admits.Add(1)
			go ps.admit(conn)
		}
	}()
	s.T.Cleanup(func() {
		_ = listener.Close()
		<-accepted
		ps.admits.Wait()
		ps.mu.Lock()
		defer ps.mu.Unlock()
		for _, p := range ps.points {
			p.releaseAll()
		}
	})
	return ps
}

func (ps *pauses) env() []string {
	if ps == nil {
		return nil
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return []string{
		pausepoint.EnvPoints + "=" + strings.Join(ps.order, ","),
		pausepoint.EnvSocket + "=" + ps.socket,
	}
}

func (ps *pauses) admit(conn net.Conn) {
	defer ps.admits.Done()
	_ = conn.SetReadDeadline(time.Now().Add(fakeagent.HangGuard))
	line, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.SetReadDeadline(time.Time{})
	name, pidText, _ := strings.Cut(strings.TrimSpace(line), " ")
	pid, _ := strconv.Atoi(pidText)
	ps.mu.Lock()
	p := ps.points[name]
	ps.mu.Unlock()
	if err != nil || p == nil {
		release(conn)
		return
	}
	p.held <- pausedAt{conn: conn, pid: pid}
}

func (p *Pause) Await() int {
	p.t.Helper()
	if p.paused != nil {
		p.t.Fatalf("Await(%q): release the process already paused there first", p.name)
	}
	select {
	case paused := <-p.held:
		p.paused = &paused
		return paused.pid
	case <-time.After(fakeagent.HangGuard):
		p.t.Fatalf("no process reached pause point %q within %s", p.name, fakeagent.HangGuard)
		return 0
	}
}

func (p *Pause) Release() {
	p.t.Helper()
	if p.paused == nil {
		p.t.Fatalf("Release(%q): no process is paused there; Await it first", p.name)
	}
	release(p.paused.conn)
	p.paused = nil
}

func (p *Pause) releaseAll() {
	if p.paused != nil {
		release(p.paused.conn)
		p.paused = nil
	}
	for {
		select {
		case paused := <-p.held:
			release(paused.conn)
		default:
			return
		}
	}
}

func release(conn net.Conn) {
	_, _ = conn.Write([]byte(pausepoint.Release + "\n"))
	_ = conn.Close()
}
