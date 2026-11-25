package script

import (
	"cmp"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/buke/quickjs-go"
	"golang.org/x/sys/unix"
)

//go:embed prelude.js
var prelude string

type engine struct {
	job     Job
	host    func(line []byte) Answer
	rt      *quickjs.Runtime
	ctx     *quickjs.Context
	began   time.Duration
	sent    int
	logs    []string
	logged  int
	dropped int
	stopped string
}

func executeOnLockedThread(job Job, host func(line []byte) Answer) Output {
	e := &engine{job: job, host: host}
	e.rt = quickjs.NewRuntime(
		quickjs.WithCanBlock(false),
		quickjs.WithMemoryLimit(uint64(job.Limits.Memory)),
		quickjs.WithMaxStackSize(uint64(job.Limits.Stack)),
	)
	if err := limitHeap(e.rt, job.Limits.Memory); err != nil {
		return Output{Failed: fmt.Sprintf("the script's runner did not start: %v", err)}
	}
	e.ctx = e.rt.NewBareContext()
	names, _ := json.Marshal(job.Functions)
	start := e.ctx.Eval(prelude, quickjs.EvalFileName("attn"))
	if !start.IsException() {
		start = start.Execute(e.ctx.NewUndefined(), e.ctx.NewFunction(e.call), e.ctx.NewFunction(e.log), e.ctx.ParseJSON(string(names)))
	}
	if start.IsException() {
		return Output{Failed: fmt.Sprintf("the script's runner did not start: %v", e.ctx.Exception())}
	}

	e.began = threadCPU()
	e.rt.SetInterruptHandler(e.interrupt)
	returned := start.Execute(e.ctx.NewUndefined(), e.ctx.NewString(job.Source))
	if !returned.IsException() {
		e.ctx.Loop()
	}
	raiseHeapLimit(2 * job.Limits.Memory)
	out := e.outcome(returned)
	e.stopAtHeapLimit()
	if e.stopped != "" {
		out = Output{Failed: stoppedBy(e.stopped)}
	}
	out.Failed = cutAndSaySo(out.Failed, failedText)
	out.Logs = e.logs
	out.Dropped += e.dropped
	return out
}

func (e *engine) stopAtHeapLimit() {
	if e.stopped == "" && heapWasRefused() {
		e.stopped = e.memoryLimit()
	}
}

func (e *engine) outcome(returned *quickjs.Value) Output {
	switch {
	case e.ctx.HasException():
		return e.failure(e.ctx.Exception())
	case returned.PromiseState() == quickjs.PromiseRejected:
		return e.threw("the script threw ", returned.PromiseResult())
	}
	rejection := e.rejectionNothingHandled()
	if rejection.IsException() {
		return e.failure(e.ctx.Exception())
	}
	if rejection.IsArray() {
		return e.threw("the script rejected a promise that nothing handles with ", rejection.GetIdx(0))
	}
	if returned.PromiseState() == quickjs.PromisePending {
		return Output{Failed: "the script did not finish: it waits for a promise that nothing resolves"}
	}
	var out Output
	if result := returned.PromiseResult(); !result.IsUndefined() {
		out.Has = json.Unmarshal([]byte(result.ToString()), &out.Returned) == nil
		out.Returned, out.Dropped = cut(out.Returned, ResultText)
	}
	return out
}

func (e *engine) threw(what string, reason *quickjs.Value) Output {
	if reason.IsError() {
		return e.failure(reason.ToError())
	}
	return Output{Failed: what + reason.JSONStringify()}
}

func (e *engine) rejectionNothingHandled() *quickjs.Value {
	caught := e.ctx.Eval("(wait) => { try { wait() } catch (reason) { return [reason] } }")
	return caught.Execute(e.ctx.NewUndefined(), e.ctx.NewFunction(func(c *quickjs.Context, _ *quickjs.Value, _ []*quickjs.Value) *quickjs.Value {
		return c.Eval("Promise.resolve()", quickjs.EvalAwait(true))
	}))
}

func (e *engine) call(c *quickjs.Context, _ *quickjs.Value, args []*quickjs.Value) *quickjs.Value {
	if e.interrupt() != 0 {
		return c.ThrowInternalError("%s", stoppedBy(e.stopped))
	}
	if len(args) != 2 || !args[0].IsString() {
		return c.ThrowTypeError("a command is called with its name and its arguments")
	}
	name, sent := args[0].ToString(), ""
	if args[1].IsString() {
		sent = args[1].ToString()
	}
	if c.HasException() {
		return c.ThrowInternalError(outOfMemory)
	}
	if !json.Valid([]byte(sent)) {
		return c.ThrowTypeError("%s takes one object of named arguments", name)
	}
	line := encodeLine(message{Call: &call{Name: name, Args: json.RawMessage(sent)}})
	if e.sent += len(line); e.sent > Arguments {
		e.stopped = "limit of " + size(Arguments) + " of arguments to commands"
		return c.ThrowInternalError("%s", stoppedBy(e.stopped))
	}
	answer, _ := json.Marshal(e.host(line))
	return c.NewString(string(answer))
}

func (e *engine) log(c *quickjs.Context, _ *quickjs.Value, args []*quickjs.Value) *quickjs.Value {
	if e.interrupt() != 0 {
		return c.ThrowInternalError("%s", stoppedBy(e.stopped))
	}
	if len(args) != 1 || !args[0].IsString() {
		return c.ThrowTypeError("log takes one JSON string")
	}
	encoded := args[0].ToString()
	if c.HasException() {
		return c.ThrowInternalError(outOfMemory)
	}
	var line string
	if json.Unmarshal([]byte(encoded), &line) != nil {
		return c.ThrowTypeError("log takes one JSON string")
	}
	line += "\n"
	kept, dropped := cut(line, ResultText-e.logged)
	e.logged += utf8.RuneCountInString(kept)
	e.dropped += dropped
	if kept != "" {
		e.logs = append(e.logs, strings.TrimSuffix(kept, "\n"))
	}
	return c.NewUndefined()
}

func cut(text string, limit int) (kept string, dropped int) {
	dropped = utf8.RuneCountInString(text) - limit
	if dropped <= 0 {
		return text, 0
	}
	runes := 0
	for at := range text {
		if runes == limit {
			return text[:at], dropped
		}
		runes++
	}
	return text, 0
}

func cutAndSaySo(text string, limit int) string {
	kept, dropped := cut(text, limit)
	if dropped == 0 {
		return text
	}
	return fmt.Sprintf("%s\n…attn left out the last %d characters of this error.", kept, dropped)
}

func (e *engine) interrupt() int {
	e.stopAtHeapLimit()
	if e.stopped == "" && threadCPU()-e.began > e.job.Limits.Compute {
		e.stopped = fmt.Sprintf("compute limit of %s of its own work", e.job.Limits.Compute)
	}
	if e.stopped != "" {
		return 1
	}
	return 0
}

func threadCPU() time.Duration {
	var now unix.Timespec
	_ = unix.ClockGettime(unix.CLOCK_THREAD_CPUTIME_ID, &now)
	return time.Duration(now.Nano())
}

const (
	outOfMemory   = "out of memory"
	stackOverflow = "Maximum call stack size exceeded"
)

func stoppedBy(limit string) string {
	return "the script stopped: it reached its " + limit
}

func (e *engine) memoryLimit() string {
	return "memory limit of " + size(e.job.Limits.Memory)
}

func (e *engine) failure(thrown error) Output {
	var err *quickjs.Error
	if !errors.As(thrown, &err) {
		return Output{Failed: fmt.Sprintf("the script ended with an error that could not be read: %v", thrown)}
	}
	if err.Name == "RangeError" && err.Message == stackOverflow {
		return Output{Failed: stoppedBy("stack limit of " + size(e.job.Limits.Stack))}
	}
	line, at := e.line(err.Stack), ""
	if line > 0 {
		at = fmt.Sprintf(" at line %d", line)
	}
	var refusal Refusal
	if err.Name == "Refusal" && json.Unmarshal([]byte(err.JSONString), &refusal) == nil {
		failed := fmt.Sprintf("%s refused%s: %s: %s", refusal.Command, at, refusal.Code, err.Message)
		if refusal.Fix != "" {
			failed += "\nFix: " + refusal.Fix
		}
		return Output{Line: line, Failed: failed}
	}
	failed := fmt.Sprintf("%s%s: %s", err.Name, at, err.Message)
	if name, ok := strings.CutSuffix(err.Message, " is not defined"); ok && err.Name == "ReferenceError" {
		if near := nearest(name, e.job.Functions); len(near) > 0 {
			failed += "; near it: " + strings.Join(near, ", ")
		}
	}
	return Output{Line: line, Failed: failed}
}

func (e *engine) line(stack string) int {
	_, frame, found := strings.Cut(stack, "<input>:")
	if !found {
		return 0
	}
	digits := strings.IndexFunc(frame+"\n", func(r rune) bool { return r < '0' || r > '9' })
	line, _ := strconv.Atoi(frame[:digits])
	return min(line, 1+strings.Count(e.job.Source, "\n"))
}

func nearest(name string, functions []string) []string {
	words := strings.Split(strings.ToLower(name), "_")
	shared := func(function string) int {
		return len(slices.DeleteFunc(strings.Split(function, "_"), func(word string) bool { return !slices.Contains(words, word) }))
	}
	prefix := func(function string) int {
		n := 0
		for n < min(len(name), len(function)) && name[n] == function[n] {
			n++
		}
		return n
	}
	near := slices.DeleteFunc(slices.Clone(functions), func(function string) bool { return shared(function) == 0 })
	slices.SortStableFunc(near, func(a, b string) int {
		return cmp.Or(shared(b)-shared(a), prefix(b)-prefix(a))
	})
	return near[:min(len(near), 3)]
}

func size(bytes int) string {
	if bytes%(1<<20) == 0 {
		return fmt.Sprintf("%d MB", bytes>>20)
	}
	return fmt.Sprintf("%d KB", bytes>>10)
}
