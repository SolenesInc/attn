package testworld

import (
	"bufio"
	"errors"
	"net"
	"os"
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
	every  bool
	held   chan arrival
	paused *arrival
}

type arrival struct {
	pid     int
	failed  error
	release func()
}

// Pauses is the set of points a test armed for one stack or wire world. Holds match by point name, so each armed
// point must have one operation of its kind in flight.
type Pauses struct {
	mu          sync.Mutex
	points      map[string]*Pause
	order       []string
	consumed    map[string]bool
	closed      bool
	outstanding []func()
}

func NewPauses() *Pauses {
	return &Pauses{points: map[string]*Pause{}, consumed: map[string]bool{}}
}

func (ps *Pauses) Arm(t testing.TB, name string, every bool) *Pause {
	p := &Pause{t: t, name: name, every: every, held: make(chan arrival, 8)}
	armed := name
	if every {
		armed += pausepoint.Every
	}
	ps.mu.Lock()
	ps.points[name] = p
	ps.order = append(ps.order, armed)
	ps.mu.Unlock()
	return p
}

func (ps *Pauses) Armed() []string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return append([]string(nil), ps.order...)
}

// Hold is the in-process route: it blocks the caller at an armed point until the test releases it. A one-shot point
// holds the first arrival of each daemon run.
func (ps *Pauses) Hold(name string, failed error) {
	released := ps.arrive(name, os.Getpid(), failed, true, func() {})
	if failed == nil && released != nil {
		<-released
	}
}

func (ps *Pauses) arrive(name string, pid int, failed error, oneShot bool, resume func()) <-chan struct{} {
	ps.mu.Lock()
	p := ps.points[name]
	if ps.closed || p == nil || (oneShot && !p.every && ps.consumed[name]) {
		ps.mu.Unlock()
		resume()
		return nil
	}
	ps.consumed[name] = true
	released := make(chan struct{})
	var once sync.Once
	a := arrival{pid: pid, failed: failed, release: func() {
		once.Do(func() {
			close(released)
			resume()
		})
	}}
	ps.outstanding = append(ps.outstanding, a.release)
	ps.mu.Unlock()
	select {
	case p.held <- a:
	case <-released:
	}
	return released
}

// Close admits no new holds and releases every outstanding one, queued, awaited or taken.
func (ps *Pauses) Close() {
	ps.mu.Lock()
	ps.closed = true
	outstanding := ps.outstanding
	ps.outstanding = nil
	ps.mu.Unlock()
	for _, release := range outstanding {
		release()
	}
}

// Reopen admits holds again for a new daemon: each one-shot point holds its first arrival once more, and no arrival
// from the stopped daemon is left to await.
func (ps *Pauses) Reopen() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, p := range ps.points {
		p.paused = nil
		for drained := false; !drained; {
			select {
			case <-p.held:
			default:
				drained = true
			}
		}
	}
	ps.consumed = map[string]bool{}
	ps.closed = false
}

func (s *Stack) PauseAt(name string) *Pause {
	s.T.Helper()
	return s.arm(name, false)
}

// PauseEvery holds a process each time it reaches name, not only the first time.
func (s *Stack) PauseEvery(name string) *Pause {
	s.T.Helper()
	return s.arm(name, true)
}

func (s *Stack) arm(name string, every bool) *Pause {
	s.T.Helper()
	if s.daemon != nil {
		s.T.Fatalf("PauseAt(%q): arm pause points before Start; the daemon and its workers read them once", name)
	}
	return s.pauses.Arm(s.T, name, every)
}

type pauseSocket struct {
	*Pauses
	socket string
}

func (s *Stack) listenForPauses() *pauseSocket {
	socket := filepath.Join(s.Dir, "pause.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		s.T.Fatalf("listen for pause points: %v", err)
	}
	ps := &pauseSocket{Pauses: NewPauses(), socket: socket}
	var admits sync.WaitGroup
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			admits.Add(1)
			go func() {
				defer admits.Done()
				ps.admit(conn)
			}()
		}
	}()
	s.T.Cleanup(func() {
		_ = listener.Close()
		<-accepted
		ps.Close()
		admits.Wait()
	})
	return ps
}

func (ps *pauseSocket) env() []string {
	if ps == nil {
		return nil
	}
	return []string{
		pausepoint.EnvPoints + "=" + strings.Join(ps.Armed(), ","),
		pausepoint.EnvSocket + "=" + ps.socket,
	}
}

// admit reads "name pid" for a hold, or "name pid failed <quoted error>" for a step that failed and does not wait. Each
// process holds a one-shot point once, so admit holds every arrival it is sent.
func (ps *pauseSocket) admit(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(fakeagent.HangGuard))
	line, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		release(conn)
		return
	}
	fields := strings.SplitN(strings.TrimSpace(line), " ", 4)
	pid := 0
	if len(fields) > 1 {
		pid, _ = strconv.Atoi(fields[1])
	}
	var failed error
	if len(fields) == 4 && fields[2] == pausepoint.Failed {
		message, err := strconv.Unquote(fields[3])
		if err != nil {
			message = fields[3]
		}
		failed = errors.New(message)
	}
	ps.arrive(fields[0], pid, failed, false, func() { release(conn) })
}

func (p *Pause) Await() int {
	p.t.Helper()
	if p.paused != nil {
		p.t.Fatalf("Await(%q): release the process already paused there first", p.name)
	}
	paused := p.next()
	p.paused = &paused
	return paused.pid
}

func (p *Pause) next() arrival {
	p.t.Helper()
	select {
	case paused := <-p.held:
		if paused.failed != nil {
			paused.release()
			p.t.Fatalf("pause point %q: the step failed: %v", p.name, paused.failed)
		}
		return paused
	case <-time.After(fakeagent.HangGuard):
		p.t.Fatalf("no process reached pause point %q within %s", p.name, fakeagent.HangGuard)
		return arrival{}
	}
}

func (p *Pause) Release() {
	p.t.Helper()
	if p.paused == nil {
		p.t.Fatalf("Release(%q): no process is paused there; Await it first", p.name)
	}
	p.paused.release()
	p.paused = nil
}

func release(conn net.Conn) {
	_, _ = conn.Write([]byte(pausepoint.Release + "\n"))
	_ = conn.Close()
}

func (p *Pause) Take() func() {
	p.t.Helper()
	paused := p.next()
	p.t.Cleanup(paused.release)
	return paused.release
}
