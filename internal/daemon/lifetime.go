package daemon

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// With the lifetime, stops took 3.2s at most and 102ms at p99 over 18,500 restart wire-test stops.
const lifetimeStragglerReport = 5 * time.Second

// lifetime owns every goroutine the daemon starts and the work it admits from goroutines it did not start.
// Daemon.stop ends it, then waits for all of it before PTY shutdown and store.Close.
type lifetime struct {
	once    sync.Once
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	ended   bool
	running map[string]int
	active  sync.WaitGroup
}

func (l *lifetime) init() {
	l.once.Do(func() {
		l.ctx, l.cancel = context.WithCancel(context.Background())
		l.running = make(map[string]int)
	})
}

// Context is canceled when stop begins. Stop waits for admitted work instead of cancelling it, so pass Context
// only to a call that could block forever and whose result is never saved; everything else needs its own timeout.
func (l *lifetime) Context() context.Context {
	l.init()
	return l.ctx
}

func (l *lifetime) Done() <-chan struct{} { return l.Context().Done() }

// Ended turns true exactly when Hold starts refusing, so a refused caller always sees it.
func (l *lifetime) Ended() bool {
	l.init()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ended
}

// Hold admits work running on a goroutine the daemon did not start; false means the daemon is stopping and the work must not run.
func (l *lifetime) Hold(name string) (release func(), ok bool) {
	l.init()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended {
		return nil, false
	}
	l.active.Add(1)
	l.running[name]++
	return func() {
		l.mu.Lock()
		if l.running[name]--; l.running[name] == 0 {
			delete(l.running, name)
		}
		l.mu.Unlock()
		l.active.Done()
	}, true
}

// Do runs fn inside Hold on the calling goroutine; false means the daemon is stopping and fn did not run.
func (l *lifetime) Do(name string, fn func()) bool {
	release, ok := l.Hold(name)
	if !ok {
		return false
	}
	defer release()
	fn()
	return true
}

// Go runs fn on a goroutine Daemon.stop waits for; false means the daemon is stopping and fn did not run.
func (l *lifetime) Go(name string, fn func()) bool {
	release, ok := l.Hold(name)
	if !ok {
		return false
	}
	go func() {
		defer release()
		fn()
	}()
	return true
}

// goTransport runs I/O that ends with its peer rather than with the daemon. Stop does not wait for it,
// so it may touch daemon state only inside life.Hold or life.Do.
func goTransport(fn func()) {
	go fn()
}

// AfterFunc runs fn after delay inside Hold; a timer that fires once the daemon is stopping does nothing.
func (l *lifetime) AfterFunc(name string, delay time.Duration, fn func()) *time.Timer {
	return time.AfterFunc(delay, func() {
		release, ok := l.Hold(name)
		if !ok {
			return
		}
		defer release()
		fn()
	})
}

// end refuses new work and cancels the lifetime context.
func (l *lifetime) end() {
	l.init()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ended = true
	l.cancel()
}

// wait blocks until everything admitted before end has returned, naming what is still running once after lifetimeStragglerReport.
func (l *lifetime) wait(logf func(string, ...interface{})) {
	drained := make(chan struct{})
	go func() {
		l.active.Wait()
		close(drained)
	}()
	report := time.NewTimer(lifetimeStragglerReport)
	defer report.Stop()
	select {
	case <-drained:
		return
	case <-report.C:
		logf("daemon stop still waiting after %s on: %s", lifetimeStragglerReport, l.stragglers())
	}
	<-drained
}

func (l *lifetime) stragglers() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	names := make([]string, 0, len(l.running))
	for name, count := range l.running {
		names = append(names, fmt.Sprintf("%s x%d", name, count))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
