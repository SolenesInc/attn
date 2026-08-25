package workflow

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func ordinalResultMap(t *testing.T, stub AgentStub, script string) map[string]string {
	t.Helper()
	eng := New(Config{Stub: stub, WatchdogTimeout: 5 * time.Second})
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

func deterministicResult(call AgentCall) (json.RawMessage, error) {
	b, _ := json.Marshal("R:" + call.Prompt)
	return b, nil
}

func scriptedDeterministicResult(ordinal OrdinalPath, prompt string) (json.RawMessage, error) {
	return deterministicResult(AgentCall{Ordinal: ordinal, Prompt: prompt})
}

func TestPipelineOrdinalStabilityUnderReorder(t *testing.T) {
	script := `
		const out = await pipeline(["X", "Y"],
			(v, item, i) => agent("s1:" + item),
			(v, item, i) => agent("s2:" + v));
		return out;
	`

	all := NewScriptedStub(scriptedDeterministicResult)
	all.ReleaseAll()
	baseline := ordinalResultMap(t, all, script)
	if len(baseline) != 4 {
		t.Fatalf("expected 4 calls, got %d: %v", len(baseline), baseline)
	}

	var st1 []string
	for ord := range baseline {
		if containsSeg(ord, "st1") {
			st1 = append(st1, ord)
		}
	}
	sort.Strings(st1)
	if len(st1) != 2 {
		t.Fatalf("expected 2 stage-1 ordinals, got %v (all=%v)", st1, baseline)
	}

	runWithReleaseOrder := func(order []string) map[string]string {
		out := map[string]string{}
		synctest.Test(t, func(t *testing.T) {
			stub := NewScriptedStub(scriptedDeterministicResult)
			eng := New(Config{Stub: stub, WatchdogTimeout: 5 * time.Second})
			done := make(chan RunResult, 1)
			go func() {
				r, _ := eng.Run(context.Background(), script, nil)
				done <- r
			}()
			for ord := range baseline {
				if !containsSeg(ord, "st1") {
					stub.Release(ord)
				}
			}
			for _, ord := range order {
				synctest.Wait()
				stub.Release(ord)
			}
			stub.ReleaseAll()
			r := <-done
			if r.Status != StatusCompleted {
				t.Fatalf("status=%s err=%v", r.Status, r.Err)
			}
			for _, e := range r.Journal.Entries() {
				var s string
				_ = json.Unmarshal(e.Result, &s)
				out[e.Ordinal] = s
			}
		})
		return out
	}

	forward := runWithReleaseOrder([]string{st1[0], st1[1]})
	reverse := runWithReleaseOrder([]string{st1[1], st1[0]})

	if !reflect.DeepEqual(forward, reverse) {
		t.Fatalf("ordinal->result mapping differs under reordered resolution:\n forward=%v\n reverse=%v", forward, reverse)
	}
	if !reflect.DeepEqual(forward, baseline) {
		t.Fatalf("reordered runs disagree with the baseline:\n baseline=%v\n got=%v", baseline, forward)
	}

	for ord, val := range forward {
		if containsSeg(ord, "pi0") && containsSeg(ord, "st1") {
			if val != "R:s2:R:s1:X" {
				t.Errorf("item0/stage1 ordinal %s has result %q, want R:s2:R:s1:X (binding leaked)", ord, val)
			}
		}
		if containsSeg(ord, "pi1") && containsSeg(ord, "st1") {
			if val != "R:s2:R:s1:Y" {
				t.Errorf("item1/stage1 ordinal %s has result %q, want R:s2:R:s1:Y (binding leaked)", ord, val)
			}
		}
	}
}

func releaseOrderedMap(t *testing.T, script string, firstWave, secondWave []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	synctest.Test(t, func(t *testing.T) {
		stub := NewScriptedStub(scriptedDeterministicResult)
		eng := New(Config{Stub: stub, WatchdogTimeout: 5 * time.Second})
		done := make(chan RunResult, 1)
		go func() {
			r, _ := eng.Run(context.Background(), script, nil)
			done <- r
		}()
		for _, ord := range firstWave {
			stub.Release(ord)
		}
		for _, ord := range secondWave {
			synctest.Wait()
			stub.Release(ord)
		}
		stub.ReleaseAll()
		r := <-done
		if r.Status != StatusCompleted {
			t.Fatalf("status=%s err=%v", r.Status, r.Err)
		}
		for _, e := range r.Journal.Entries() {
			var s string
			_ = json.Unmarshal(e.Result, &s)
			out[e.Ordinal] = s
		}
	})
	return out
}

func TestPipelinePostAwaitOrdinalStability(t *testing.T) {
	script := `
		const out = await pipeline(["X", "Y"], async (v, item, i) => {
			const r1 = await agent("a:" + item);
			return await agent("b:" + r1);
		});
		return out;
	`
	all := NewScriptedStub(scriptedDeterministicResult)
	all.ReleaseAll()
	baseline := ordinalResultMap(t, all, script)
	if len(baseline) != 4 {
		t.Fatalf("expected 4 calls, got %d: %v", len(baseline), baseline)
	}

	var firstWave, secondWave []string
	for ord, v := range baseline {
		if strings.HasPrefix(v, "R:b:") {
			secondWave = append(secondWave, ord)
		} else {
			firstWave = append(firstWave, ord)
		}
	}
	sort.Strings(secondWave)
	if len(secondWave) != 2 {
		t.Fatalf("expected 2 post-await calls, got %v (all=%v)", secondWave, baseline)
	}

	forward := releaseOrderedMap(t, script, firstWave, []string{secondWave[0], secondWave[1]})
	reverse := releaseOrderedMap(t, script, firstWave, []string{secondWave[1], secondWave[0]})

	if !reflect.DeepEqual(forward, reverse) {
		t.Fatalf("post-await ordinal mapping differs under reorder:\n forward=%v\n reverse=%v", forward, reverse)
	}
	if !reflect.DeepEqual(forward, baseline) {
		t.Fatalf("reordered runs disagree with baseline:\n baseline=%v\n got=%v", baseline, forward)
	}

	for ord, val := range forward {
		if !strings.HasPrefix(val, "R:b:") {
			continue
		}
		if !containsSeg(ord, "st0") {
			t.Errorf("post-await ordinal %s lost its structural prefix (temporal leak): %q", ord, val)
		}
		switch {
		case containsSeg(ord, "pi0") && val != "R:b:R:a:X":
			t.Errorf("pi0 post-await %s = %q, want R:b:R:a:X (binding leaked)", ord, val)
		case containsSeg(ord, "pi1") && val != "R:b:R:a:Y":
			t.Errorf("pi1 post-await %s = %q, want R:b:R:a:Y (binding leaked)", ord, val)
		}
	}
}

func TestParallelPostAwaitOrdinalStability(t *testing.T) {
	script := `
		const mk = async (n) => {
			const r = await agent("x:" + n);
			return await agent("y:" + r);
		};
		const out = await parallel([ () => mk("0"), () => mk("1"), () => mk("2") ]);
		return out;
	`
	all := NewScriptedStub(scriptedDeterministicResult)
	all.ReleaseAll()
	baseline := ordinalResultMap(t, all, script)
	if len(baseline) != 6 {
		t.Fatalf("expected 6 calls, got %d: %v", len(baseline), baseline)
	}

	var firstWave, secondWave []string
	for ord, v := range baseline {
		if strings.HasPrefix(v, "R:y:") {
			secondWave = append(secondWave, ord)
		} else {
			firstWave = append(firstWave, ord)
		}
	}
	sort.Strings(secondWave)

	forward := releaseOrderedMap(t, script, firstWave, secondWave)
	rev := append([]string(nil), secondWave...)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	reverse := releaseOrderedMap(t, script, firstWave, rev)

	if !reflect.DeepEqual(forward, reverse) {
		t.Fatalf("parallel post-await ordinals differ under reorder:\n fwd=%v\n rev=%v", forward, reverse)
	}
	for ord, val := range forward {
		if !strings.HasPrefix(val, "R:y:") {
			continue
		}
		switch {
		case containsSeg(ord, "ps0") && val != "R:y:R:x:0":
			t.Errorf("slot0 post-await %s = %q, want R:y:R:x:0", ord, val)
		case containsSeg(ord, "ps1") && val != "R:y:R:x:1":
			t.Errorf("slot1 post-await %s = %q, want R:y:R:x:1", ord, val)
		case containsSeg(ord, "ps2") && val != "R:y:R:x:2":
			t.Errorf("slot2 post-await %s = %q, want R:y:R:x:2", ord, val)
		case !containsSeg(ord, "ps0") && !containsSeg(ord, "ps1") && !containsSeg(ord, "ps2"):
			t.Errorf("post-await ordinal %s lost its slot prefix (temporal leak): %q", ord, val)
		}
	}
}

func TestParallelOrdinalStabilityUnderReorder(t *testing.T) {
	script := `
		const out = await parallel([
			() => agent("a"),
			() => agent("b"),
			() => agent("c"),
		]);
		return out;
	`
	all := NewScriptedStub(scriptedDeterministicResult)
	all.ReleaseAll()
	baseline := ordinalResultMap(t, all, script)
	if len(baseline) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(baseline))
	}

	ords := make([]string, 0, 3)
	for ord := range baseline {
		ords = append(ords, ord)
	}
	sort.Strings(ords)

	runWithReleaseOrder := func(order []string) map[string]string {
		out := map[string]string{}
		synctest.Test(t, func(t *testing.T) {
			stub := NewScriptedStub(scriptedDeterministicResult)
			eng := New(Config{Stub: stub, WatchdogTimeout: 5 * time.Second})
			done := make(chan RunResult, 1)
			go func() {
				r, _ := eng.Run(context.Background(), script, nil)
				done <- r
			}()
			for _, ord := range order {
				synctest.Wait()
				stub.Release(ord)
			}
			stub.ReleaseAll()
			r := <-done
			for _, e := range r.Journal.Entries() {
				var s string
				_ = json.Unmarshal(e.Result, &s)
				out[e.Ordinal] = s
			}
		})
		return out
	}

	forward := runWithReleaseOrder([]string{ords[0], ords[1], ords[2]})
	reverse := runWithReleaseOrder([]string{ords[2], ords[1], ords[0]})

	if !reflect.DeepEqual(forward, reverse) {
		t.Fatalf("parallel ordinals differ under reorder:\n fwd=%v\n rev=%v", forward, reverse)
	}
	for ord, val := range forward {
		switch {
		case containsSeg(ord, "ps0") && val != "R:a":
			t.Errorf("slot0 %s = %q, want R:a", ord, val)
		case containsSeg(ord, "ps1") && val != "R:b":
			t.Errorf("slot1 %s = %q, want R:b", ord, val)
		case containsSeg(ord, "ps2") && val != "R:c":
			t.Errorf("slot2 %s = %q, want R:c", ord, val)
		}
	}
}

func TestLoopCounterOrdinals(t *testing.T) {
	script := `
		const out = [];
		for (let i = 0; i < 4; i++) {
			out.push(await agent("call-" + i));
		}
		return out;
	`
	stub := StubFunc(deterministicResult)
	m := ordinalResultMap(t, stub, script)
	if len(m) != 4 {
		t.Fatalf("expected 4 calls, got %d: %v", len(m), m)
	}
	wantByCounter := map[int]string{0: "R:call-0", 1: "R:call-1", 2: "R:call-2", 3: "R:call-3"}
	seenCounters := map[int]bool{}
	for ord, val := range m {
		c := counterOf(t, ord)
		seenCounters[c] = true
		if val != wantByCounter[c] {
			t.Errorf("counter #%d ordinal %s = %q, want %q", c, ord, val, wantByCounter[c])
		}
	}
	for i := 0; i < 4; i++ {
		if !seenCounters[i] {
			t.Errorf("missing loop counter #%d", i)
		}
	}
}

func TestPhaseRenameKeepsCacheHits(t *testing.T) {
	scriptA := `phase("planning"); const a = await agent("p"); return a;`
	scriptB := `phase("BUILDING"); const a = await agent("p"); return a;`

	jour := NewMemJournal()
	eng := New(Config{Journal: jour, Stub: StubFunc(deterministicResult), WatchdogTimeout: 5 * time.Second})
	r1, err := eng.Run(context.Background(), scriptA, nil)
	if err != nil {
		t.Fatalf("run A: %v", err)
	}
	if r1.LiveCalls != 1 {
		t.Fatalf("run A LiveCalls=%d want 1", r1.LiveCalls)
	}

	eng2 := New(Config{Journal: jour, Stub: StubFunc(deterministicResult), WatchdogTimeout: 5 * time.Second})
	r2, err := eng2.Resume(context.Background(), scriptB, nil)
	if err != nil {
		t.Fatalf("resume B: %v", err)
	}
	if r2.LiveCalls != 0 || r2.CachedCalls != 1 {
		t.Fatalf("phase rename should be 100%% cache hit: live=%d cached=%d", r2.LiveCalls, r2.CachedCalls)
	}
}

func containsSeg(ordinal, seg string) bool {
	for _, part := range splitOrdinal(ordinal) {
		if part == seg {
			return true
		}
	}
	return false
}

func splitOrdinal(ordinal string) []string {
	return strings.Split(ordinal, "/")
}

func counterOf(t *testing.T, ordinal string) int {
	t.Helper()
	parts := splitOrdinal(ordinal)
	last := parts[len(parts)-1]
	// last looks like "cs@file:line:col#N"
	hashIdx := -1
	for i := len(last) - 1; i >= 0; i-- {
		if last[i] == '#' {
			hashIdx = i
			break
		}
	}
	if hashIdx < 0 {
		t.Fatalf("ordinal %s has no counter segment", ordinal)
	}
	n := 0
	for _, c := range last[hashIdx+1:] {
		n = n*10 + int(c-'0')
	}
	return n
}
