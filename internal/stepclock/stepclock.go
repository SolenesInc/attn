package stepclock

import (
	"bufio"
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

var load = sync.OnceValue(func() *clock {
	socket := os.Getenv(EnvSocket)
	if socket == "" {
		return nil
	}
	return &clock{socket: socket, now: time.Now(), timers: make(map[*timer]struct{})}
})

func NewTimer(d time.Duration) Timer {
	if c := load(); c != nil {
		return c.newTimer(d, nil)
	}
	return realTimer{time.NewTimer(d)}
}

func AfterFunc(d time.Duration, f func()) Timer {
	if c := load(); c != nil {
		return c.newTimer(d, f)
	}
	return realTimer{time.AfterFunc(d, f)}
}

type clock struct {
	socket   string
	start    sync.Once
	mu       sync.Mutex
	now      time.Time
	timers   map[*timer]struct{}
	revision uint64
	observed uint64
	changed  chan struct{}
}

type timer struct {
	clock    *clock
	deadline time.Time
	duration time.Duration
	revision uint64
	channel  chan time.Time
	callback func()
}

func (c *clock) newTimer(d time.Duration, f func()) *timer {
	t := &timer{clock: c, callback: f}
	if f == nil {
		t.channel = make(chan time.Time, 1)
	}
	t.Reset(d)
	c.start.Do(func() { go c.serve() })
	return t
}

func (t *timer) C() <-chan time.Time { return t.channel }

func (t *timer) Reset(d time.Duration) bool {
	c := t.clock
	c.mu.Lock()
	active := t.stopLocked()
	t.deadline = c.now.Add(d)
	t.duration = d
	c.revision++
	t.revision = c.revision
	if d > 0 {
		c.timers[t] = struct{}{}
	} else if t.callback == nil {
		t.channel <- c.now
	}
	c.notifyLocked()
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
	_, active := t.clock.timers[t]
	delete(t.clock.timers, t)
	select {
	case <-t.channel:
		active = true
	default:
	}
	return active
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.observed = c.revision
	c.now = c.now.Add(d)
	var callbacks []func()
	for t := range c.timers {
		if t.deadline.After(c.now) {
			continue
		}
		delete(c.timers, t)
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

func (c *clock) notifyLocked() {
	if c.changed != nil {
		close(c.changed)
		c.changed = nil
	}
}

func (c *clock) awaitTimer(conn net.Conn, d time.Duration) error {
	seen := make(map[time.Duration]bool)
	for {
		c.mu.Lock()
		found := false
		var fresh []time.Duration
		for t := range c.timers {
			if t.revision <= c.observed {
				continue
			}
			found = found || t.duration == d
			if !seen[t.duration] {
				seen[t.duration] = true
				fresh = append(fresh, t.duration)
			}
		}
		if found {
			c.observed = c.revision
		} else if c.changed == nil {
			c.changed = make(chan struct{})
		}
		changed := c.changed
		c.mu.Unlock()
		for _, duration := range fresh {
			if _, err := fmt.Fprintf(conn, "seen %s\n", duration); err != nil {
				return err
			}
		}
		if found {
			return nil
		}
		<-changed
	}
}

func (c *clock) serve() {
	conn, err := net.Dial("unix", c.socket)
	if err != nil {
		panic(fmt.Sprintf("stepclock connect %s: %v", c.socket, err))
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "ready %d\n", os.Getpid()); err != nil {
		panic(fmt.Sprintf("stepclock ready: %v", err))
	}
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			panic(fmt.Sprintf("stepclock read: %v", err))
		}
		command, value, _ := strings.Cut(strings.TrimSpace(line), " ")
		d, err := time.ParseDuration(value)
		if (command != "advance" && command != "await") || err != nil || d < 0 {
			panic(fmt.Sprintf("stepclock invalid command %q", strings.TrimSpace(line)))
		}
		if command == "advance" {
			c.advance(d)
		} else if err := c.awaitTimer(conn, d); err != nil {
			panic(fmt.Sprintf("stepclock await timer %s: %v", d, err))
		}
		if _, err := fmt.Fprintln(conn, "ok"); err != nil {
			panic(fmt.Sprintf("stepclock acknowledge %s %s: %v", command, d, err))
		}
	}
}
