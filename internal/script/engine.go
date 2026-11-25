package script

import (
	"cmp"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/buke/quickjs-go"
	"golang.org/x/sys/unix"
)

//go:embed prelude.js
var prelude string

type engine struct {
	job     Job
	host    func(name string, args json.RawMessage) Answer
	rt      *quickjs.Runtime
	ctx     *quickjs.Context
	began   time.Duration
	logs    []string
	logged  int
	stopped string
}

func executeOnLockedThread(job Job, host func(name string, args json.RawMessage) Answer) Output {
	e := &engine{job: job, host: host}
	e.rt = quickjs.NewRuntime(
		quickjs.WithCanBlock(false),
		quickjs.WithMemoryLimit(uint64(job.Limits.Memory)),
		quickjs.WithMaxStackSize(uint64(job.Limits.Stack)),
	)
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
	e.makeRoomToReadWhatWasThrown()
	out := e.outcome(returned)
	if e.stopped != "" {
		out = Output{Failed: stoppedBy(e.stopped)}
	}
	out.Logs = e.logs
	return out
}

func (e *engine) makeRoomToReadWhatWasThrown() {
	e.rt.SetMemoryLimit(2 * uint64(e.job.Limits.Memory))
}

func (e *engine) outcome(returned *quickjs.Value) Output {
	switch {
	case e.ctx.HasException():
		return e.failure(e.ctx.Exception())
	case returned.PromiseState() == quickjs.PromiseRejected:
		reason := returned.PromiseResult()
		if reason.IsError() || reason.IsNull() {
			return e.failure(reason.ToError())
		}
		return Output{Failed: "the script threw " + reason.JSONStringify()}
	}
	if rejection := e.rejectionNothingHandled(); rejection != nil {
		return e.failure(rejection)
	}
	if returned.PromiseState() == quickjs.PromisePending {
		return Output{Failed: "the script did not finish: it waits for a promise that nothing resolves"}
	}
	var out Output
	if result := returned.PromiseResult(); !result.IsUndefined() {
		out.Has = json.Unmarshal([]byte(result.ToString()), &out.Returned) == nil
	}
	return out
}

func (e *engine) rejectionNothingHandled() error {
	e.ctx.Eval("Promise.resolve()", quickjs.EvalAwait(true))
	return e.ctx.Exception()
}

func (e *engine) call(c *quickjs.Context, _ *quickjs.Value, args []*quickjs.Value) *quickjs.Value {
	name, sent := args[0].ToString(), args[1].ToString()
	if c.HasException() {
		return c.ThrowInternalError(outOfMemory)
	}
	if !json.Valid([]byte(sent)) {
		return c.ThrowTypeError("%s takes one object of named arguments", name)
	}
	answer, _ := json.Marshal(e.host(name, json.RawMessage(sent)))
	return c.NewString(string(answer))
}

func (e *engine) log(c *quickjs.Context, _ *quickjs.Value, args []*quickjs.Value) *quickjs.Value {
	var line string
	if err := json.Unmarshal([]byte(args[0].ToString()), &line); err != nil || c.HasException() {
		return c.ThrowInternalError(outOfMemory)
	}
	if e.logged += len(line); e.logged > e.job.Limits.Memory {
		e.stopped = e.memoryLimit()
		return c.ThrowInternalError(outOfMemory)
	}
	e.logs = append(e.logs, line)
	return c.NewUndefined()
}

func (e *engine) interrupt() int {
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

var outOfMemoryThrownAsNull = &quickjs.Error{Name: "InternalError", Message: outOfMemory}

func stoppedBy(limit string) string {
	return "the script stopped: it reached its " + limit
}

func (e *engine) memoryLimit() string {
	return "memory limit of " + size(e.job.Limits.Memory)
}

func (e *engine) failure(thrown error) Output {
	var err *quickjs.Error
	if !errors.As(thrown, &err) {
		err = outOfMemoryThrownAsNull
	}
	switch {
	case err.Name == "InternalError" && err.Message == outOfMemory:
		return Output{Failed: stoppedBy(e.memoryLimit())}
	case err.Name == "RangeError" && err.Message == stackOverflow:
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

var scriptFrame = regexp.MustCompile(`<input>:(\d+)`)

func (e *engine) line(stack string) int {
	frame := scriptFrame.FindStringSubmatch(stack)
	if frame == nil {
		return 0
	}
	line, _ := strconv.Atoi(frame[1])
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
