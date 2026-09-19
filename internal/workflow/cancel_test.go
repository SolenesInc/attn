package workflow

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestCancelWhileAwaitingLiveAgentSettlesAndTearsDownAgent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stub := newBlockingStub(echoPrompt)
		eng := New(Config{Stub: stub, WatchdogTimeout: 30 * time.Second})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		type outcome struct {
			res RunResult
			err error
		}
		done := make(chan outcome, 1)
		go func() {
			r, err := eng.Run(ctx, `return await agent("x");`, nil)
			done <- outcome{r, err}
		}()

		synctest.Wait()
		if got := stub.inFlight.Load(); got != 1 {
			stub.releaseAll()
			<-done
			t.Fatalf("agent never reached in-flight; inFlight=%d", got)
		}

		cancel()

		synctest.Wait()
		select {
		case got := <-done:
			if got.res.Status != StatusInterrupted {
				t.Fatalf("status = %s (err=%v), want interrupted", got.res.Status, got.res.Err)
			}
			var ie *ErrInterrupted
			if !errors.As(got.err, &ie) {
				t.Fatalf("err = %v (%T), want *ErrInterrupted", got.err, got.err)
			}
		default:
			t.Fatal("run did not settle after cancel — event loop stayed parked on <-el.jobs (cancel deadlock regression)")
		}

		if got := stub.inFlight.Load(); got != 0 {
			t.Fatalf("in-flight agent was not torn down after cancel (inFlight=%d); run ctx was not threaded into AgentStub.Run", got)
		}
	})
}
