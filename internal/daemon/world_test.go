package daemon_test

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/testworld"
)

type world struct {
	*testworld.World
	bubbled     bool
	unix        net.Listener
	ws          net.Listener
	daemon      *daemon.WireDaemon
	terms       *testworld.Terminals
	gardenClock func() time.Time
	clock       *stepclock.Clock
	pauses      *testworld.Pauses
}

func newWorld(t testing.TB, agents ...fakeagent.Harness) *world {
	t.Helper()
	w := &world{World: prepareWorld(t, agents...)}
	w.start()
	return w
}

func newWorldInLongestDataDir(t *testing.T, agents ...fakeagent.Harness) *world {
	t.Helper()
	w := &world{World: setWorldEnv(t, testworld.PrepareInLongestDataDir(t, testworld.AttnBinary(t), agents...))}
	w.start()
	return w
}

func inBubble(t *testing.T, script func(t *testing.T, w *world)) {
	t.Helper()
	inPreparedBubble(t, prepareWorld(t), script)
}

// inBubbleHeldAt runs script in a bubbled world whose pause points are armed before its daemon starts.
func inBubbleHeldAt(t *testing.T, points []string, script func(t *testing.T, w *world, held map[string]*testworld.Pause)) {
	t.Helper()
	prepared := prepareWorld(t)
	synctest.Test(t, func(t *testing.T) {
		bubbled := *prepared
		bubbled.T = t
		w := &world{World: &bubbled, bubbled: true}
		held := make(map[string]*testworld.Pause, len(points))
		for _, point := range points {
			held[point] = w.PauseAt(point)
		}
		w.start()
		script(t, w, held)
	})
}

func inCrewBubble(t *testing.T, script func(t *testing.T, w *world)) {
	t.Helper()
	w := &world{World: prepareWorld(t)}
	writeCrewHomes(t, w)
	inPreparedBubble(t, w.World, script)
}

func inPreparedBubble(t *testing.T, prepared *testworld.World, script func(t *testing.T, w *world)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		bubbled := *prepared
		bubbled.T = t
		w := &world{World: &bubbled, bubbled: true}
		w.start()
		script(t, w)
	})
}

func prepareWorld(t testing.TB, agents ...fakeagent.Harness) *testworld.World {
	t.Helper()
	wrapper := ""
	if len(agents) > 0 {
		wrapper = testworld.AttnBinary(t)
	}
	return setWorldEnv(t, testworld.Prepare(t, wrapper, agents...))
}

func setWorldEnv(t testing.TB, prepared *testworld.World) *testworld.World {
	t.Helper()
	for _, pair := range prepared.Vars {
		key, value, _ := strings.Cut(pair, "=")
		t.Setenv(key, value)
	}
	return prepared
}

func (w *world) start() {
	w.T.Helper()
	w.listen()
	var terminals ptybackend.Backend
	if w.terms != nil {
		terminals = w.terms
	}
	if !w.bubbled && w.clock == nil {
		w.clock = stepclock.New()
	}
	if w.pauses != nil {
		w.pauses.Reopen()
	}
	started, err := daemon.StartWireDaemonWithTerminals(w.Socket, w.unix, w.ws, terminals, !w.bubbled, w.clock, w.gardenClock)
	if err != nil {
		w.T.Fatalf("start daemon: %v", err)
	}
	w.daemon = started
	w.T.Cleanup(w.stop)
}

// PauseAt holds the world's daemon the first time each run reaches the pause point name. A bubbled world can arm
// before w.start, inside its bubble.
func (w *world) PauseAt(name string) *testworld.Pause {
	w.T.Helper()
	return w.armPause(name, false)
}

// PauseEvery holds the world's daemon each time it reaches the pause point name.
func (w *world) PauseEvery(name string) *testworld.Pause {
	w.T.Helper()
	return w.armPause(name, true)
}

func (w *world) armPause(name string, every bool) *testworld.Pause {
	w.T.Helper()
	if w.pauses == nil {
		pauses := testworld.NewPauses()
		unroute, err := pausepoint.InProcess(pauses)
		if err != nil {
			w.T.Fatalf("arm pause point %q: %v", name, err)
		}
		w.pauses = pauses
		w.T.Cleanup(func() {
			pauses.Close()
			unroute()
		})
	}
	return w.pauses.Arm(w.T, name, every)
}

func (w *world) listen() {
	w.T.Helper()
	if w.bubbled {
		unix, ws := newPipeListener(), newPipeListener()
		w.unix, w.ws, w.Dial, w.WSAddr = unix, ws, ws.dial, ws.Addr().String()
		w.DialUnix = func() (net.Conn, error) { return unix.dial(context.Background()) }
		return
	}
	unix, err := net.Listen("unix", w.Socket)
	if err != nil {
		w.T.Fatal(err)
	}
	ws, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		w.T.Fatal(err)
	}
	w.T.Setenv("ATTN_WS_PORT", strconv.Itoa(ws.Addr().(*net.TCPAddr).Port))
	w.unix, w.ws, w.WSAddr = unix, ws, ws.Addr().String()
	w.Dial = func(ctx context.Context) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp", ws.Addr().String())
	}
	w.DialUnix = func() (net.Conn, error) { return net.Dial("unix", w.Socket) }
}

func (w *world) stop() {
	if w.daemon == nil {
		return
	}
	if w.pauses != nil {
		w.pauses.Close()
	}
	if w.bubbled {
		// Let calls the agents already have in flight land before their daemon goes away.
		synctest.Wait()
	}
	w.ClosePeers()
	if err := w.stopDaemon(); err != nil {
		w.T.Errorf("stop daemon: %v", err)
	}
	w.daemon = nil
	if w.T.Failed() {
		w.LogDaemonTail()
	}
}

func (w *world) stopDaemon() error {
	if w.bubbled {
		return w.daemon.Stop()
	}
	stopped := make(chan error, 1)
	d := w.daemon
	go func() { stopped <- d.Stop() }()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	select {
	case err := <-stopped:
		return err
	case <-guard.C:
	}
	w.T.Errorf("the daemon did not stop within %s; a timer on its stepped clock may hold it, so the world advances a day to finish", fakeagent.HangGuard)
	w.clock.Advance(24 * time.Hour)
	return <-stopped
}

func (w *world) restart() {
	w.T.Helper()
	w.stop()
	w.start()
}

func (w *world) beginStop(t *testing.T) (finish func()) {
	t.Helper()
	observer := transportConnectRaw(t, w)
	stopped := make(chan error, 1)
	d := w.daemon
	go func() { stopped <- d.Stop() }()
	deadline := time.NewTimer(fakeagent.HangGuard)
	defer deadline.Stop()
	for {
		select {
		case _, open := <-observer.frames:
			if !open {
				return func() {
					w.T.Helper()
					if err := <-stopped; err != nil {
						w.T.Errorf("stop daemon: %v", err)
					}
					w.daemon = nil
					w.ClosePeers()
				}
			}
		case <-deadline.C:
			w.T.Fatal("daemon did not close its transport during stop")
		}
	}
}

func (w *world) advance(d time.Duration) {
	w.T.Helper()
	if !w.bubbled {
		w.clock.Advance(d)
		return
	}
	w.elapse(d)
	synctest.Wait()
}

// closeWatchWindows closes each folder-watch window as soon as it opens, for the rest of the test.
func (w *world) closeWatchWindows() {
	w.T.Helper()
	if w.bubbled {
		w.T.Fatal("a world inBubble cannot watch folders")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, ok := w.clock.AwaitTimer(ctx, notebook.WatchDebounce); !ok {
				return
			}
			w.clock.Advance(notebook.WatchDebounce)
		}
	}()
	w.T.Cleanup(func() {
		cancel()
		<-done
	})
}

// scheduleTickLimit is past the most schedule ticks any automation test waits for one outcome.
const scheduleTickLimit = 10

// tickSchedulesUntil ticks the automation schedule until done reports true after a tick. Each tick waits until the job
// runner is idle, moves the daemon clock one schedule tick and waits until the runner is idle again.
func (w *world) tickSchedulesUntil(what string, done func() bool) {
	w.T.Helper()
	if w.bubbled {
		w.T.Fatal("tickSchedulesUntil steps the daemon clock, which a world inBubble does not have")
	}
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	awaitIdle := func() {
		w.T.Helper()
		if !w.clock.AwaitIdle(ctx) {
			w.T.Fatalf("%s: the job runner was still busy after %s", what, fakeagent.HangGuard)
		}
	}
	for range scheduleTickLimit {
		awaitIdle()
		w.clock.Advance(daemon.AutomationScheduleTick)
		awaitIdle()
		if done() {
			return
		}
	}
	w.T.Fatalf("%s: not after %d schedule ticks (scheduleTickLimit)", what, scheduleTickLimit)
}

// awaitTimer waits for the daemon of a world outside a bubble to arm a timer of d after the last advance or awaitTimer.
func (w *world) awaitTimer(d time.Duration) {
	w.T.Helper()
	if w.bubbled {
		w.T.Fatal("awaitTimer reads the stepped clock, which a world inBubble does not have")
	}
	ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
	defer cancel()
	if seen, ok := w.clock.AwaitTimer(ctx, d); !ok {
		w.T.Fatalf("awaitTimer(%s): no such timer armed within %s; saw timer durations %v", d, fakeagent.HangGuard, seen)
	}
}

// elapse moves the fake clock without draining work held by an external process.
func (w *world) elapse(d time.Duration) {
	w.T.Helper()
	if !w.bubbled {
		w.T.Fatal("advance moves the fake clock, which only a world inBubble has")
	}
	time.Sleep(d)
}

type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return pipeAddr{}
}

func (l *pipeListener) dial(ctx context.Context) (net.Conn, error) {
	server, client := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

func (w *world) RefuseDatabaseCommits() func() {
	return w.daemon.RefuseDatabaseCommits()
}

func (w *world) UseFileStat(stat func(string) (os.FileInfo, error)) {
	w.daemon.UseFileStat(stat)
}
