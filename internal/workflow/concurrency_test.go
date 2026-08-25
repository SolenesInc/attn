package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type blockingStub struct {
	resultFor func(prompt string) json.RawMessage

	release chan struct{}

	inFlight    atomic.Int64
	maxInFlight atomic.Int64
	calls       atomic.Int64
}

func newBlockingStub(resultFor func(prompt string) json.RawMessage) *blockingStub {
	return &blockingStub{
		resultFor: resultFor,
		release:   make(chan struct{}),
	}
}

func (s *blockingStub) Run(ctx context.Context, call AgentCall) (json.RawMessage, error) {
	s.calls.Add(1)
	cur := s.inFlight.Add(1)
	for {
		hw := s.maxInFlight.Load()
		if cur <= hw || s.maxInFlight.CompareAndSwap(hw, cur) {
			break
		}
	}
	defer s.inFlight.Add(-1)
	select {
	case <-s.release:
		return s.resultFor(call.Prompt), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *blockingStub) releaseAll() { close(s.release) }

func echoPrompt(prompt string) json.RawMessage {
	b, _ := json.Marshal("R:" + prompt)
	return b
}

func boomOrEchoStub() AgentStub {
	return StubFunc(func(call AgentCall) (json.RawMessage, error) {
		runtime.Gosched()
		if strings.Contains(call.Prompt, "boom") {
			return nil, fmt.Errorf("subagent crashed on %q", call.Prompt)
		}
		return echoPrompt(call.Prompt), nil
	})
}

// In a synctest bubble synctest.Wait returns when every goroutine is durably blocked, so
// the reading is "exactly capN, and no more are coming" rather than a poll.
func assertCapSaturatedAndBounded(t *testing.T, script string, capN, wantLive int) RunResult {
	t.Helper()
	var result RunResult
	synctest.Test(t, func(t *testing.T) {
		stub := newBlockingStub(echoPrompt)
		eng := New(Config{
			Stub:            stub,
			ConcurrencyCap:  capN,
			WatchdogTimeout: 10 * time.Second,
		})

		done := make(chan RunResult, 1)
		go func() {
			r, _ := eng.Run(context.Background(), script, nil)
			done <- r
		}()

		synctest.Wait()
		if got := stub.inFlight.Load(); got != int64(capN) {
			stub.releaseAll()
			<-done
			t.Fatalf("cap never saturated: inFlight settled at %d, want %d (semaphore not admitting up to the cap?)",
				got, capN)
		}

		stub.releaseAll()
		r := <-done

		if r.Status != StatusCompleted {
			t.Fatalf("status=%s err=%v", r.Status, r.Err)
		}
		if got := stub.maxInFlight.Load(); got != int64(capN) {
			t.Fatalf("max in-flight = %d, want exactly the cap %d (cap was %s)",
				got, capN, capVerdict(got, int64(capN)))
		}
		if int(stub.calls.Load()) != wantLive {
			t.Fatalf("total live Run calls = %d, want %d (some agents never dispatched)", stub.calls.Load(), wantLive)
		}
		if r.LiveCalls != wantLive {
			t.Fatalf("LiveCalls = %d, want %d", r.LiveCalls, wantLive)
		}
		result = r
	})
	return result
}

func capVerdict(got, capN int64) string {
	if got > capN {
		return "EXCEEDED (semaphore failed to bound dispatch)"
	}
	return "not reached (cap under-utilized)"
}

func TestParallelConcurrencyReachesCapNeverExceeds(t *testing.T) {
	const n, capN = 12, 3
	script := fmt.Sprintf(`
		const thunks = [];
		for (let i = 0; i < %d; i++) {
			const k = i;
			thunks.push(() => agent("p" + k));
		}
		return await parallel(thunks);
	`, n)

	r := assertCapSaturatedAndBounded(t, script, capN, n)

	out, ok := r.Value.([]interface{})
	if !ok || len(out) != n {
		t.Fatalf("parallel result = %#v, want %d-element slice", r.Value, n)
	}
	for i, v := range out {
		want := "R:p" + fmt.Sprint(i)
		if v != want {
			t.Errorf("slot %d = %v, want %q", i, v, want)
		}
	}
}

func TestPipelineConcurrencyReachesCapNeverExceeds(t *testing.T) {
	const n, capN = 12, 3
	script := fmt.Sprintf(`
		const items = [];
		for (let i = 0; i < %d; i++) items.push(i);
		return await pipeline(items, (prev, item, i) => agent("s" + item));
	`, n)

	r := assertCapSaturatedAndBounded(t, script, capN, n)

	out, ok := r.Value.([]interface{})
	if !ok || len(out) != n {
		t.Fatalf("pipeline result = %#v, want %d-element slice", r.Value, n)
	}
	for i, v := range out {
		want := "R:s" + fmt.Sprint(i)
		if v != want {
			t.Errorf("item %d = %v, want %q", i, v, want)
		}
	}
}

type raceStub struct {
	resultFor func(prompt string) json.RawMessage
}

func (s *raceStub) Run(_ context.Context, call AgentCall) (json.RawMessage, error) {
	runtime.Gosched()
	return s.resultFor(call.Prompt), nil
}

func ordinalMapUnderRace(t *testing.T, script string, capN int) map[string]string {
	t.Helper()
	stub := &raceStub{resultFor: echoPrompt}
	eng := New(Config{Stub: stub, ConcurrencyCap: capN, WatchdogTimeout: 10 * time.Second})
	res, err := eng.Run(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("run error: %v (status=%s)", err, res.Status)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("status=%s err=%v", res.Status, res.Err)
	}
	out := map[string]string{}
	for _, e := range res.Journal.Entries() {
		var s string
		_ = json.Unmarshal(e.Result, &s)
		out[e.Ordinal] = s
	}
	return out
}

func TestOrdinalStabilityUnderGenuineConcurrency(t *testing.T) {
	script := `
		const mk = async (n) => {
			const r = await agent("x:" + n);
			return await agent("y:" + r);
		};
		const p = await parallel([ () => mk("0"), () => mk("1"), () => mk("2"), () => mk("3") ]);
		const q = await pipeline(["A", "B", "C"], async (v, item, i) => {
			const r1 = await agent("a:" + item);
			return await agent("b:" + r1);
		});
		return [p, q];
	`

	const capN = 8
	baseline := ordinalMapUnderRace(t, script, capN)
	if len(baseline) == 0 {
		t.Fatalf("baseline produced no journaled calls")
	}

	const repeats = 40
	for i := 0; i < repeats; i++ {
		got := ordinalMapUnderRace(t, script, capN)
		if !reflect.DeepEqual(got, baseline) {
			t.Fatalf("ordinal->result map diverged on run %d under genuine concurrency:\n baseline=%s\n got=%s",
				i, dumpSorted(baseline), dumpSorted(got))
		}
	}
}

func dumpSorted(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b []byte
	for _, k := range keys {
		b = append(b, fmt.Sprintf("  %s -> %s\n", k, m[k])...)
	}
	return string(b)
}

func TestParallelNeverRejectsNullSlotUnderConcurrency(t *testing.T) {
	boomStub := boomOrEchoStub()
	script := `
		const out = await parallel([
			() => agent("ok0"),
			() => { throw new Error("thunk threw"); },
			() => agent("boom2"),
			() => agent("ok3"),
			() => agent("boom4"),
			() => agent("ok5"),
		]);
		return out;
	`
	eng := New(Config{Stub: boomStub, ConcurrencyCap: 8, WatchdogTimeout: 10 * time.Second})
	res, err := eng.Run(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("parallel rejected under concurrency (must never reject): %v", err)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("status=%s err=%v", res.Status, res.Err)
	}
	out, ok := res.Value.([]interface{})
	if !ok || len(out) != 6 {
		t.Fatalf("result = %#v, want 6-element slice", res.Value)
	}
	want := []interface{}{"R:ok0", nil, nil, "R:ok3", nil, "R:ok5"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("slots = %#v, want %#v (throwing thunk + errored agents must be null slots)", out, want)
	}
}

func TestPipelineNeverRejectsNullItemUnderConcurrency(t *testing.T) {
	boomStub := boomOrEchoStub()
	script := `
		const out = await pipeline(["keep", "throw", "boom"],
			(prev, item, i) => {
				if (item === "throw") throw new Error("stage threw for " + item);
				return agent("s0:" + item);
			},
			(prev, item, i) => agent("s1:" + prev));
		return out;
	`
	eng := New(Config{Stub: boomStub, ConcurrencyCap: 8, WatchdogTimeout: 10 * time.Second})
	res, err := eng.Run(context.Background(), script, nil)
	if err != nil {
		t.Fatalf("pipeline rejected under concurrency (must never reject): %v", err)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("status=%s err=%v", res.Status, res.Err)
	}
	out, ok := res.Value.([]interface{})
	if !ok || len(out) != 3 {
		t.Fatalf("result = %#v, want 3-element slice", res.Value)
	}
	want := []interface{}{"R:s1:R:s0:keep", nil, nil}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("items = %#v, want %#v", out, want)
	}
}
