package workflow

import (
	"context"
	"encoding/json"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

type RunStatus string

const (
	StatusCompleted   RunStatus = "completed"
	StatusErrored     RunStatus = "errored"
	StatusInterrupted RunStatus = "interrupted"
)

const (
	defaultAgentLifetimeCap = 1000
	defaultMaxItemsPerCall  = 4096
	defaultWatchdogTimeout  = 30 * time.Second
)

type Config struct {
	Stub             AgentStub
	Journal          Journal
	AgentLifetimeCap int
	MaxItemsPerCall  int
	ConcurrencyCap   int
	WatchdogTimeout  time.Duration
}

type Engine struct {
	cfg Config
}

func New(cfg Config) *Engine {
	if cfg.AgentLifetimeCap == 0 {
		cfg.AgentLifetimeCap = defaultAgentLifetimeCap
	}
	if cfg.MaxItemsPerCall == 0 {
		cfg.MaxItemsPerCall = defaultMaxItemsPerCall
	}
	if cfg.WatchdogTimeout == 0 {
		cfg.WatchdogTimeout = defaultWatchdogTimeout
	}
	if cfg.ConcurrencyCap == 0 {
		cfg.ConcurrencyCap = defaultConcurrency()
	}
	if cfg.Stub == nil {
		cfg.Stub = DefaultStub{}
	}
	return &Engine{cfg: cfg}
}

func defaultConcurrency() int {
	c := runtime.NumCPU() - 2
	if c < 1 {
		c = 1
	}
	if c > 16 {
		c = 16
	}
	return c
}

type RunResult struct {
	Value       any
	Meta        *Meta
	Status      RunStatus
	Err         error
	CachedCalls int
	LiveCalls   int
	Journal     Journal
}

func (e *Engine) Run(ctx context.Context, script string, args any) (RunResult, error) {
	return e.execute(ctx, script, args)
}

func (e *Engine) Resume(ctx context.Context, script string, args any) (RunResult, error) {
	return e.execute(ctx, script, args)
}

func (e *Engine) execute(ctx context.Context, script string, args any) (RunResult, error) {
	jour := e.cfg.Journal
	if jour == nil {
		jour = NewMemJournal()
	}

	stripped := stripExport(script)
	meta, metaErr := parseMeta(stripped)
	if metaErr != nil {
		return RunResult{Status: StatusErrored, Err: metaErr, Journal: jour}, metaErr
	}

	// The whole run executes on ONE goroutine; the returned error is always res.Err.
	outCh := make(chan RunResult, 1)
	go func() {
		outCh <- e.runOnLoopGoroutine(ctx, stripped, args, meta, jour)
	}()

	res := <-outCh
	return res, res.Err
}

func (e *Engine) runOnLoopGoroutine(ctx context.Context, src string, args any, meta *Meta, jour Journal) RunResult {
	vm := goja.New()

	if err := installDeterminismBans(vm); err != nil {
		return RunResult{Status: StatusErrored, Err: err, Journal: jour, Meta: meta}
	}

	el := newEventLoop()
	rs := &runState{
		vm:               vm,
		el:               el,
		stub:             e.cfg.Stub,
		jour:             jour,
		ctx:              ctx,
		stack:            newPathStack(),
		agentLifetimeCap: e.cfg.AgentLifetimeCap,
		maxItemsPerCall:  e.cfg.MaxItemsPerCall,
		sem:              make(chan struct{}, e.cfg.ConcurrencyCap),
	}
	if err := installHostFns(rs, args); err != nil {
		return RunResult{Status: StatusErrored, Err: err, Journal: jour, Meta: meta}
	}

	// Carrying the structural path across every await/.then boundary is what makes a
	// post-await agent() read its STRUCTURAL ordinal, not a timing-dependent one.
	vm.SetAsyncContextTracker(newPathContextTracker(rs.stack))

	wd := newWatchdog(vm, e.cfg.WatchdogTimeout)
	el.onEnterJS = wd.arm
	el.onLeaveJS = wd.disarm
	stopWatch := wd.start(ctx)
	defer stopWatch()

	wrapped := "(async function __wf__(){\n" + src + "\n})()"

	var topLevel *goja.Promise
	initPanic := el.safeRunJS(func() {
		v, err := vm.RunScript("workflow.js", wrapped)
		if err != nil {
			panic(err)
		}
		p, ok := v.Export().(*goja.Promise)
		if !ok {
			pp, resolve, _ := vm.NewPromise()
			_ = resolve(v)
			topLevel = pp
			return
		}
		topLevel = p
	})

	if initPanic != nil {
		return e.mapPanic(initPanic, rs, jour, meta)
	}

	state, result, pumpPanic := el.pump(ctx, topLevel)
	if pumpPanic != nil {
		return e.mapPanic(pumpPanic, rs, jour, meta)
	}

	switch state {
	case goja.PromiseStateFulfilled:
		return RunResult{
			Value:       exportValue(result),
			Meta:        meta,
			Status:      StatusCompleted,
			CachedCalls: rs.cachedCalls,
			LiveCalls:   rs.liveCalls,
			Journal:     jour,
		}
	case goja.PromiseStateRejected:
		return RunResult{
			Meta:        meta,
			Status:      StatusErrored,
			Err:         &scriptError{text: stringify(result)},
			CachedCalls: rs.cachedCalls,
			LiveCalls:   rs.liveCalls,
			Journal:     jour,
		}
	default:
		return RunResult{Meta: meta, Status: StatusInterrupted, Err: &ErrInterrupted{Reason: "workflow did not settle"}, Journal: jour,
			CachedCalls: rs.cachedCalls, LiveCalls: rs.liveCalls}
	}
}

func (e *Engine) mapPanic(p interface{}, rs *runState, jour Journal, meta *Meta) RunResult {
	base := RunResult{
		Meta:        meta,
		CachedCalls: rs.cachedCalls,
		LiveCalls:   rs.liveCalls,
		Journal:     jour,
	}
	if ie, ok := p.(*goja.InterruptedError); ok {
		reason := "workflow exceeded the watchdog timeout"
		if v := ie.Value(); v != nil {
			if e2, ok := v.(*ErrInterrupted); ok {
				reason = e2.Reason
			} else if s, ok := v.(string); ok {
				reason = s
			}
		}
		base.Status = StatusInterrupted
		base.Err = &ErrInterrupted{Reason: reason}
		return base
	}
	// A cancel arriving while parked on the jobs channel never enters goja, so pump
	// returns a bare *ErrInterrupted, unwrapped by goja.InterruptedError.
	if ie, ok := p.(*ErrInterrupted); ok {
		base.Status = StatusInterrupted
		base.Err = ie
		return base
	}
	if gerr, ok := p.(error); ok {
		base.Status = StatusErrored
		base.Err = gerr
		return base
	}
	base.Status = StatusErrored
	base.Err = &scriptError{text: stringifyAny(p)}
	return base
}

func exportValue(v goja.Value) any {
	if v == nil || goja.IsNull(v) || goja.IsUndefined(v) {
		return nil
	}
	return v.Export()
}

func stringify(v goja.Value) string {
	if v == nil {
		return ""
	}
	return v.String()
}

func stringifyAny(v interface{}) string {
	if gv, ok := v.(goja.Value); ok {
		return gv.String()
	}
	if err, ok := v.(error); ok {
		return err.Error()
	}
	b, _ := json.Marshal(v)
	return string(b)
}

type scriptError struct {
	text string
}

func (e *scriptError) Error() string {
	if e.text != "" {
		return e.text
	}
	return "workflow script error"
}

type watchdog struct {
	vm      *goja.Runtime
	timeout time.Duration

	deadline atomic.Int64
	tripped  atomic.Bool
}

func newWatchdog(vm *goja.Runtime, timeout time.Duration) *watchdog {
	return &watchdog{vm: vm, timeout: timeout}
}

func (w *watchdog) arm() {
	w.deadline.Store(time.Now().Add(w.timeout).UnixNano())
}

func (w *watchdog) disarm() {
	w.deadline.Store(0)
}

func (w *watchdog) start(ctx context.Context) func() {
	stop := make(chan struct{})
	var once sync.Once
	// min(timeout/10, 10ms), floor 1ms: catches a tight loop well inside the timeout
	// without spinning.
	tick := w.timeout / 10
	if tick > 10*time.Millisecond {
		tick = 10 * time.Millisecond
	}
	if tick < time.Millisecond {
		tick = time.Millisecond
	}
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				if w.tripped.CompareAndSwap(false, true) {
					w.vm.Interrupt(&ErrInterrupted{Reason: "workflow cancelled"})
				}
				return
			case <-t.C:
				dl := w.deadline.Load()
				if dl != 0 && time.Now().UnixNano() >= dl {
					if w.tripped.CompareAndSwap(false, true) {
						w.vm.Interrupt(&ErrInterrupted{Reason: "workflow exceeded the watchdog timeout"})
					}
					return
				}
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}
