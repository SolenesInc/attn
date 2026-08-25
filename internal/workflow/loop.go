package workflow

import (
	"context"

	"github.com/dop251/goja"
)

// The goja *Runtime is created and touched ONLY by the goroutine running run(); a worker
// posts a closure on `jobs` instead. vm.Interrupt is the only call permitted off it.
type eventLoop struct {
	jobs chan func()

	// onEnterJS / onLeaveJS arm/disarm the watchdog around any segment that
	// re-enters the bytecode interpreter.
	onEnterJS func()
	onLeaveJS func()
}

func newEventLoop() *eventLoop {
	return &eventLoop{
		jobs: make(chan func(), 4096),
	}
}

// post is safe from any goroutine.
func (el *eventLoop) post(fn func()) {
	el.jobs <- fn
}

func (el *eventLoop) runJS(fn func()) {
	if el.onEnterJS != nil {
		el.onEnterJS()
	}
	defer func() {
		if el.onLeaveJS != nil {
			el.onLeaveJS()
		}
	}()
	fn()
}

// Waking on ctx cancellation is load-bearing: parked on `await agent(...)` the loop blocks
// on a Go channel, not in goja, so the watchdog's vm.Interrupt cannot reach it.
func (el *eventLoop) pump(ctx context.Context, topLevel *goja.Promise) (state goja.PromiseState, result goja.Value, panicVal interface{}) {
	for topLevel.State() == goja.PromiseStatePending {
		select {
		case job := <-el.jobs:
			caught := el.safeRunJS(job)
			if caught != nil {
				return topLevel.State(), topLevel.Result(), caught
			}
		case <-ctx.Done():
			return topLevel.State(), topLevel.Result(), &ErrInterrupted{Reason: "workflow cancelled"}
		}
	}
	return topLevel.State(), topLevel.Result(), nil
}

func (el *eventLoop) safeRunJS(job func()) (caught interface{}) {
	defer func() {
		if r := recover(); r != nil {
			caught = r
		}
	}()
	el.runJS(job)
	return nil
}
