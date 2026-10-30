package stepclock

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const EnvSocket = "ATTN_STEP_CLOCK_SOCK"

type Timer interface {
	C() <-chan time.Time
	Reset(time.Duration) bool
	Stop() bool
}

type realTimer struct{ timer *time.Timer }

func (t realTimer) C() <-chan time.Time        { return t.timer.C }
func (t realTimer) Reset(d time.Duration) bool { return t.timer.Reset(d) }
func (t realTimer) Stop() bool                 { return t.timer.Stop() }

// A nil *Clock reads real time.
type Clock struct {
	mu      sync.Mutex
	now     time.Time
	pending pending[*timer]
	report  func(event string)
}

func New() *Clock {
	return &Clock{now: time.Now(), pending: newPending[*timer]()}
}

var process = sync.OnceValue(func() *Clock {
	socket := os.Getenv(EnvSocket)
	if socket == "" {
		return nil
	}
	c := New()
	c.serve(socket)
	return c
})

// Process is the clock ATTN_STEP_CLOCK_SOCK selects: stepped by the hub listening there, or real time when unset.
func Process() *Clock { return process() }

func NewTimer(d time.Duration) Timer { return Process().NewTimer(d) }

func AfterFunc(d time.Duration, f func()) Timer { return Process().AfterFunc(d, f) }

func (c *Clock) NewTimer(d time.Duration) Timer {
	if c == nil {
		return realTimer{time.NewTimer(d)}
	}
	return c.newTimer(d, nil)
}

func (c *Clock) AfterFunc(d time.Duration, f func()) Timer {
	if c == nil {
		return realTimer{time.AfterFunc(d, f)}
	}
	return c.newTimer(d, f)
}

func (c *Clock) Now() time.Time {
	if c == nil {
		return time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

type timer struct {
	clock    *Clock
	deadline time.Time
	channel  chan time.Time
	callback func()
}

func (c *Clock) newTimer(d time.Duration, f func()) *timer {
	t := &timer{clock: c, callback: f}
	if f == nil {
		t.channel = make(chan time.Time, 1)
	}
	t.Reset(d)
	return t
}

func (t *timer) C() <-chan time.Time { return t.channel }

func (t *timer) Reset(d time.Duration) bool {
	c := t.clock
	c.mu.Lock()
	active := t.stopLocked()
	t.deadline = c.now.Add(d)
	if d > 0 {
		c.pending.arm(t, d)
		c.reportLocked("armed %p %s", t, d)
	} else if t.callback == nil {
		t.channel <- c.now
	}
	c.mu.Unlock()
	if d <= 0 && t.callback != nil {
		go t.callback()
	}
	return active
}

func (t *timer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	return t.stopLocked()
}

func (t *timer) stopLocked() bool {
	active := t.clock.pending.disarm(t)
	if active {
		t.clock.reportLocked("disarmed %p", t)
	}
	select {
	case <-t.channel:
		active = true
	default:
	}
	return active
}

func (c *Clock) reportLocked(format string, args ...any) {
	if c.report != nil {
		c.report(fmt.Sprintf(format, args...))
	}
}

// Advance fires every timer due by now+d and returns once their callbacks have run.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.pending.mark()
	c.now = c.now.Add(d)
	var callbacks []func()
	for _, t := range c.pending.armed() {
		if t.deadline.After(c.now) {
			continue
		}
		c.pending.disarm(t)
		c.reportLocked("disarmed %p", t)
		if t.callback != nil {
			callbacks = append(callbacks, t.callback)
		} else {
			t.channel <- t.deadline
		}
	}
	c.mu.Unlock()
	var running sync.WaitGroup
	for _, f := range callbacks {
		running.Go(f)
	}
	running.Wait()
}

// AwaitTimer waits until a timer of duration d, armed or reset after the last Advance or AwaitTimer, is pending.
// When ctx ends first it returns false with the durations of the timers pending then.
func (c *Clock) AwaitTimer(ctx context.Context, d time.Duration) ([]time.Duration, bool) {
	return c.pending.await(ctx, &c.mu, d)
}

// pending tracks armed timers by key, so a waiter can tell a timer armed after its mark from an older one.
type pending[K comparable] struct {
	timers   map[K]armedTimer
	revision uint64
	marked   uint64
	changed  chan struct{}
}

type armedTimer struct {
	duration time.Duration
	revision uint64
}

func newPending[K comparable]() pending[K] {
	return pending[K]{timers: make(map[K]armedTimer), changed: make(chan struct{})}
}

func (p *pending[K]) arm(key K, d time.Duration) {
	p.revision++
	p.timers[key] = armedTimer{duration: d, revision: p.revision}
	p.notify()
}

func (p *pending[K]) disarm(key K) bool {
	_, ok := p.timers[key]
	delete(p.timers, key)
	return ok
}

func (p *pending[K]) armed() []K {
	keys := make([]K, 0, len(p.timers))
	for key := range p.timers {
		keys = append(keys, key)
	}
	return keys
}

func (p *pending[K]) mark() { p.marked = p.revision }

func (p *pending[K]) notify() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *pending[K]) await(ctx context.Context, mu *sync.Mutex, d time.Duration) ([]time.Duration, bool) {
	for {
		mu.Lock()
		var durations []time.Duration
		for _, t := range p.timers {
			if t.duration == d && t.revision > p.marked {
				p.mark()
				mu.Unlock()
				return nil, true
			}
			durations = append(durations, t.duration)
		}
		changed := p.changed
		mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return durations, false
		}
	}
}

func (c *Clock) serve(socket string) {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		panic(fmt.Sprintf("stepclock connect %s: %v", socket, err))
	}
	var writing sync.Mutex
	send := func(line string) {
		writing.Lock()
		defer writing.Unlock()
		if _, err := fmt.Fprintln(conn, line); err != nil {
			panic(fmt.Sprintf("stepclock send %q: %v", line, err))
		}
	}
	send(fmt.Sprintf("ready %d", os.Getpid()))
	c.report = send
	go func() {
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				panic(fmt.Sprintf("stepclock read: %v", err))
			}
			command, value, _ := strings.Cut(strings.TrimSpace(line), " ")
			d, err := time.ParseDuration(value)
			if command != "advance" || err != nil || d < 0 {
				panic(fmt.Sprintf("stepclock invalid command %q", strings.TrimSpace(line)))
			}
			c.Advance(d)
			send("advanced " + d.String())
		}
	}()
}
