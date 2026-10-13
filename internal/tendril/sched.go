package tendril

import (
	"context"
	"errors"
	"go/token"
	"sync"
)

var (
	ErrDeadlock   = errors.New("all goroutines are asleep - deadlock!")
	ErrGoroutines = errors.New("too many goroutines")
	errExited     = errors.New("main returned")
)

const goroutineBytes = 4096

type gstate uint8

const (
	gRunning gstate = iota
	gRunnable
	gParked
	gInHost
	gDead
)

type gor struct {
	id        int
	wake      chan struct{}
	state     gstate
	depth     int
	pos       token.Pos
	panicking *panicState
}

type scheduler struct {
	mu     sync.Mutex
	runq   []*gor
	live   []*gor
	inHost int
	free   bool
	nextID int
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

func (m *machine) ensureSched() *scheduler {
	if m.sched == nil {
		ctx, cancel := context.WithCancel(m.ctx)
		m.ctx = ctx
		m.hctx = context.WithValue(ctx, machineKey{}, m)
		m.sched = &scheduler{cancel: cancel}
		m.main = &gor{wake: make(chan struct{}, 1), state: gRunning}
		m.g = m.main
	}
	return m.sched
}

func (m *machine) save() {
	g := m.g
	g.depth, g.pos, g.panicking = m.depth, m.pos, m.panicking
}

func (m *machine) restore(g *gor) {
	m.g = g
	m.depth, m.pos, m.panicking = g.depth, g.pos, g.panicking
}

func (s *scheduler) popRunnable() *gor {
	if len(s.runq) == 0 {
		return nil
	}
	g := s.runq[0]
	s.runq = s.runq[1:]
	return g
}

func (s *scheduler) remove(g *gor) {
	for i, r := range s.runq {
		if r == g {
			s.runq = append(s.runq[:i], s.runq[i+1:]...)
			return
		}
	}
}

func (m *machine) ready(g *gor) {
	s := m.sched
	s.mu.Lock()
	g.state = gRunnable
	s.runq = append(s.runq, g)
	s.mu.Unlock()
}

func (m *machine) yield() {
	if m.sched != nil {
		m.handoff(gRunnable)
	}
}

func (m *machine) park() { m.handoff(gParked) }

func (m *machine) handoff(next gstate) {
	s := m.ensureSched()
	self := m.g
	m.save()
	s.mu.Lock()
	self.state = next
	if next == gRunnable {
		s.runq = append(s.runq, self)
	}
	to := m.pickNext(s)
	s.mu.Unlock()
	if to == nil && next == gParked && m.halted != nil && self == m.main {
		m.restore(self)
		panic(halt{m.halted})
	}
	if to != self {
		if to != nil {
			to.wake <- struct{}{}
		}
		<-self.wake
	}
	m.restore(self)
	if m.halted != nil {
		panic(halt{m.halted})
	}
}

func (m *machine) pickNext(s *scheduler) *gor {
	var to *gor
	switch {
	case m.exited:
		to = m.main
	case m.halted != nil && m.main.state == gParked:
		to = m.main
		s.remove(to)
	default:
		to = s.popRunnable()
	}
	if to == nil && s.inHost == 0 {
		if m.halted == nil {
			m.halted = ErrDeadlock
		}
		if m.main.state == gParked {
			to = m.main
		}
	}
	if to == nil {
		s.free = true
		return nil
	}
	to.state = gRunning
	return to
}

func (m *machine) spawn(run func(*frame)) {
	s := m.ensureSched()
	if len(s.live) >= m.limits.Goroutines {
		m.stop(ErrGoroutines)
	}
	m.alloc(goroutineBytes)
	s.mu.Lock()
	s.nextID++
	g := &gor{id: s.nextID, wake: make(chan struct{}, 1), state: gRunnable, pos: m.pos}
	s.live = append(s.live, g)
	s.runq = append(s.runq, g)
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		<-g.wake
		m.restore(g)
		if m.halted == nil {
			if err := m.run(func() { run(&frame{m: m}) }); err != nil && (m.halted == nil || m.haltedBy == g) {
				m.halted, m.haltedBy = err, g
			}
		}
		m.exit(g)
	}()
}

func (m *machine) exit(g *gor) {
	s := m.sched
	s.mu.Lock()
	g.state = gDead
	for i, l := range s.live {
		if l == g {
			s.live = append(s.live[:i], s.live[i+1:]...)
			break
		}
	}
	to := m.pickNext(s)
	s.mu.Unlock()
	if to != nil {
		to.wake <- struct{}{}
	}
}

func (m *machine) releaseForHost() bool {
	s := m.sched
	if s == nil {
		return false
	}
	self := m.g
	m.save()
	s.mu.Lock()
	self.state = gInHost
	s.inHost++
	to := s.popRunnable()
	if to == nil {
		s.free = true
	} else {
		to.state = gRunning
	}
	s.mu.Unlock()
	if to != nil {
		to.wake <- struct{}{}
	}
	return true
}

func (m *machine) reacquire(self *gor) {
	s := m.sched
	s.mu.Lock()
	s.inHost--
	if s.free {
		s.free = false
		self.state = gRunning
		s.mu.Unlock()
	} else {
		self.state = gRunnable
		s.runq = append(s.runq, self)
		s.mu.Unlock()
		<-self.wake
	}
	m.restore(self)
}

func (m *machine) shutdown() {
	s := m.sched
	if s == nil {
		return
	}
	m.exited = true
	if m.halted == nil {
		m.halted = errExited
	}
	s.cancel()
	m.save()
	for {
		s.mu.Lock()
		if len(s.live) == 0 {
			s.mu.Unlock()
			break
		}
		var to *gor
		for _, g := range s.live {
			if g.state == gParked || g.state == gRunnable {
				to = g
				break
			}
		}
		if to == nil {
			s.free = true
		} else {
			s.remove(to)
			to.state = gRunning
		}
		s.mu.Unlock()
		if to != nil {
			to.wake <- struct{}{}
		}
		<-m.main.wake
	}
	s.wg.Wait()
	m.restore(m.main)
}
