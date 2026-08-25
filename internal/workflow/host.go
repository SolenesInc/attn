package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dop251/goja"
)

// Display-only; NEVER part of the cache identity.
func nowRFC3339Nano() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// runState lives on the loop goroutine; workers touch only the semaphore and
// mutate counters back on the loop goroutine inside posted closures.
type runState struct {
	vm    *goja.Runtime
	el    *eventLoop
	stub  AgentStub
	jour  Journal
	stack *pathStack

	ctx context.Context

	agentLifetimeCap int
	maxItemsPerCall  int

	liveAgentCount int
	cachedCalls    int
	liveCalls      int

	// diverged latches on the first cache miss, so no cached call ever has a
	// live-run ancestor.
	diverged bool

	sem chan struct{}

	resolveThen goja.Callable
	promiseAll  goja.Callable

	nullValue goja.Value
}

// MUST be called synchronously on the VM goroutine, while the call stack is intact.
func (rs *runState) callsiteKey() string {
	var frames [4]goja.StackFrame
	captured := rs.vm.CaptureCallStack(4, frames[:0])
	for _, f := range captured {
		pos := f.Position()
		if pos.Filename != "" && pos.Line > 0 {
			return fmt.Sprintf("%s:%d:%d", pos.Filename, pos.Line, pos.Column)
		}
	}
	return "<unknown>"
}

func installHostFns(rs *runState, args any) error {
	vm := rs.vm
	rs.nullValue = goja.Null()

	thenV, err := vm.RunString(`(function(p, onF, onR){ return Promise.resolve(p).then(onF, onR); })`)
	if err != nil {
		return err
	}
	if c, ok := goja.AssertFunction(thenV); ok {
		rs.resolveThen = c
	} else {
		return fmt.Errorf("internal: then helper is not callable")
	}
	allV, err := vm.RunString(`(function(arr){ return Promise.all(arr); })`)
	if err != nil {
		return err
	}
	if c, ok := goja.AssertFunction(allV); ok {
		rs.promiseAll = c
	} else {
		return fmt.Errorf("internal: all helper is not callable")
	}

	if err := vm.Set("args", vm.ToValue(args)); err != nil {
		return err
	}
	if err := vm.Set("log", func(goja.FunctionCall) goja.Value { return goja.Undefined() }); err != nil {
		return err
	}
	if err := vm.Set("phase", func(call goja.FunctionCall) goja.Value {
		title := ""
		if len(call.Arguments) > 0 {
			title = call.Argument(0).String()
		}
		rs.stack.setPhase(title)
		return goja.Undefined()
	}); err != nil {
		return err
	}
	if err := vm.Set("workflow", func(goja.FunctionCall) goja.Value {
		panic(vm.ToValue((&ErrWorkflowNotImpl{}).Error()))
	}); err != nil {
		return err
	}
	if err := vm.Set("agent", rs.makeAgentFn()); err != nil {
		return err
	}
	if err := vm.Set("parallel", rs.makeParallelFn()); err != nil {
		return err
	}
	if err := vm.Set("pipeline", rs.makePipelineFn()); err != nil {
		return err
	}
	return nil
}

// agent() MUST read the structural ordinal synchronously, before any async boundary.
func (rs *runState) makeAgentFn() func(goja.FunctionCall) goja.Value {
	vm := rs.vm
	return func(call goja.FunctionCall) goja.Value {
		prompt := ""
		if len(call.Arguments) > 0 {
			prompt = call.Arguments[0].String()
		}
		schema := extractAgentSchema(call.Argument(1))
		// NOT part of the cache identity, which stays ordinal+prompt_hash+schema_hash.
		isolation := validateIsolation(extractAgentString(call.Argument(1), "isolation"))
		model := extractAgentString(call.Argument(1), "model")
		agentType := extractAgentString(call.Argument(1), "agentType")
		label := extractAgentString(call.Argument(1), "label")

		site := rs.callsiteKey()
		ordinal := rs.stack.ordinalFor(site)
		ordKey := ordinal.String()
		promptHash := hashPrompt(prompt)
		schemaHash := hashSchema(schema)
		phaseTitle := rs.stack.currentPhase()

		p, resolve, _ := vm.NewPromise()

		if !rs.diverged {
			if entry, ok := rs.jour.Lookup(ordKey); ok && IsCacheHit(entry, ordKey, promptHash, schemaHash) {
				rs.cachedCalls++
				val := rs.resultToValue(entry.Result)
				mustResolve(resolve, val)
				return vm.ToValue(p)
			}
			rs.diverged = true
		}

		rs.liveAgentCount++
		if rs.liveAgentCount > rs.agentLifetimeCap {
			panic(vm.ToValue((&ErrAgentCap{Cap: rs.agentLifetimeCap}).Error()))
		}

		startedAt := nowRFC3339Nano()
		rs.jour.Upsert(JournalEntry{
			Ordinal: ordKey, PromptHash: promptHash, SchemaHash: schemaHash,
			Status: "running", Label: label, Phase: phaseTitle, Model: model,
			StartedAt: startedAt,
		})

		ordSnapshot := ordinal.clone()
		go func() {
			rs.sem <- struct{}{}
			res, runErr := rs.stub.Run(rs.ctx, AgentCall{
				Ordinal:   ordSnapshot,
				Prompt:    prompt,
				Schema:    schema,
				Isolation: isolation,
				Model:     model,
				AgentType: agentType,
			})
			<-rs.sem

			rs.el.post(func() {
				rs.liveCalls++
				if runErr != nil {
					rs.jour.Upsert(JournalEntry{
						Ordinal: ordKey, PromptHash: promptHash, SchemaHash: schemaHash,
						Result: nil, Status: "errored", Err: runErr.Error(),
						Label: label, Phase: phaseTitle, Model: model,
						StartedAt: startedAt, CompletedAt: nowRFC3339Nano(),
					})
					mustResolve(resolve, rs.nullValue)
					return
				}
				rs.jour.Upsert(JournalEntry{
					Ordinal: ordKey, PromptHash: promptHash, SchemaHash: schemaHash,
					Result: res, Status: "ok",
					Label: label, Phase: phaseTitle, Model: model,
					StartedAt: startedAt, CompletedAt: nowRFC3339Nano(),
				})
				mustResolve(resolve, rs.resultToValue(res))
			})
		}()

		return vm.ToValue(p)
	}
}

func extractAgentSchema(optsVal goja.Value) json.RawMessage {
	if optsVal == nil || goja.IsUndefined(optsVal) || goja.IsNull(optsVal) {
		return nil
	}
	obj, ok := optsVal.(*goja.Object)
	if !ok {
		return nil
	}
	schemaVal := obj.Get("schema")
	if schemaVal == nil || goja.IsUndefined(schemaVal) || goja.IsNull(schemaVal) {
		return nil
	}
	exported := schemaVal.Export()
	if exported == nil {
		return nil
	}
	raw, err := json.Marshal(exported)
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.RawMessage(raw)
}

func extractAgentString(optsVal goja.Value, key string) string {
	if optsVal == nil || goja.IsUndefined(optsVal) || goja.IsNull(optsVal) {
		return ""
	}
	obj, ok := optsVal.(*goja.Object)
	if !ok {
		return ""
	}
	v := obj.Get(key)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	s, ok := v.Export().(string)
	if !ok {
		return ""
	}
	return s
}

func validateIsolation(s string) string {
	if s == "worktree" {
		return "worktree"
	}
	return ""
}

// Re-panics on goja's uncatchable *InterruptedError; swallowing it stalls the loop.
func mustResolve(resolve func(interface{}) error, v goja.Value) {
	if err := resolve(v); err != nil {
		panic(err)
	}
}

func (rs *runState) resultToValue(raw json.RawMessage) goja.Value {
	if len(raw) == 0 {
		return rs.nullValue
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return rs.vm.ToValue(string(raw))
	}
	return rs.vm.ToValue(v)
}

func (rs *runState) makeParallelFn() func(goja.FunctionCall) goja.Value {
	vm := rs.vm
	return func(call goja.FunctionCall) goja.Value {
		thunks := toSlice(vm, call.Argument(0))
		if len(thunks) > rs.maxItemsPerCall {
			panic(vm.ToValue((&ErrTooManyItems{Construct: "parallel", Count: len(thunks), Max: rs.maxItemsPerCall}).Error()))
		}

		childPromises := make([]goja.Value, len(thunks))
		for i, thunkV := range thunks {
			thunk, ok := goja.AssertFunction(thunkV)
			if !ok {
				childPromises[i] = rs.settledNull()
				continue
			}
			pop := rs.stack.push(segParallelSlot, i)
			slotPath := rs.stack.snapshot()
			childPromises[i] = rs.invokeNullable(thunk, slotPath)
			pop()
		}

		arr := vm.NewArray(toIfaceSlice(childPromises)...)
		out, err := rs.promiseAll(goja.Undefined(), arr)
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
		return out
	}
}

func (rs *runState) makePipelineFn() func(goja.FunctionCall) goja.Value {
	vm := rs.vm
	return func(call goja.FunctionCall) goja.Value {
		items := toSlice(vm, call.Argument(0))
		if len(items) > rs.maxItemsPerCall {
			panic(vm.ToValue((&ErrTooManyItems{Construct: "pipeline", Count: len(items), Max: rs.maxItemsPerCall}).Error()))
		}
		var stages []goja.Callable
		for _, a := range call.Arguments[1:] {
			if fn, ok := goja.AssertFunction(a); ok {
				stages = append(stages, fn)
			} else {
				stages = append(stages, nil)
			}
		}

		itemResults := make([]goja.Value, len(items))
		for j, item := range items {
			itemResults[j] = rs.buildPipelineItem(j, item, stages)
		}

		arr := vm.NewArray(toIfaceSlice(itemResults)...)
		out, err := rs.promiseAll(goja.Undefined(), arr)
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
		return out
	}
}

// Each stage's agent() fires at resolution time, hence the captured path
// re-established before every callback.
func (rs *runState) buildPipelineItem(j int, item goja.Value, stages []goja.Callable) goja.Value {
	vm := rs.vm
	popItem := rs.stack.push(segPipelineItem, j)
	defer popItem()

	prev := rs.settledValue(item)
	for s, stage := range stages {
		popStage := rs.stack.push(segStage, s)
		stagePath := rs.stack.snapshot()

		if stage == nil {
			prev = rs.settledNull()
			popStage()
			continue
		}

		stageFn := stage
		idxVal := vm.ToValue(j)
		origItem := item

		onFulfilled := func(fcall goja.FunctionCall) goja.Value {
			prevResult := fcall.Argument(0)
			if goja.IsNull(prevResult) || goja.IsUndefined(prevResult) {
				return rs.nullValue
			}
			restore := rs.stack.replace(stagePath)
			defer restore()
			v, err := stageFn(goja.Undefined(), prevResult, origItem, idxVal)
			if err != nil {
				return rs.nullValue
			}
			return v
		}
		onRejected := func(goja.FunctionCall) goja.Value { return rs.nullValue }

		next, err := rs.resolveThen(goja.Undefined(), prev, vm.ToValue(onFulfilled), vm.ToValue(onRejected))
		if err != nil {
			next = rs.settledNull()
		}
		prev = next
		popStage()
	}
	return prev
}

func (rs *runState) invokeNullable(fn goja.Callable, capturedPath []segment) goja.Value {
	vm := rs.vm
	// Matters for agent() issued after an await; synchronous calls see the push.
	restore := rs.stack.replace(capturedPath)
	var result goja.Value
	var thrown bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				thrown = true
			}
		}()
		v, err := fn(goja.Undefined())
		if err != nil {
			thrown = true
			return
		}
		result = v
	}()
	restore()
	if thrown {
		return rs.settledNull()
	}
	onRejected := func(goja.FunctionCall) goja.Value { return rs.nullValue }
	onFulfilled := func(fcall goja.FunctionCall) goja.Value {
		restore := rs.stack.replace(capturedPath)
		defer restore()
		return fcall.Argument(0)
	}
	wrapped, err := rs.resolveThen(goja.Undefined(), result, vm.ToValue(onFulfilled), vm.ToValue(onRejected))
	if err != nil {
		return rs.settledNull()
	}
	return wrapped
}

func (rs *runState) settledValue(v goja.Value) goja.Value {
	p, resolve, _ := rs.vm.NewPromise()
	mustResolve(resolve, v)
	return rs.vm.ToValue(p)
}

func (rs *runState) settledNull() goja.Value {
	return rs.settledValue(rs.nullValue)
}

func toSlice(vm *goja.Runtime, v goja.Value) []goja.Value {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	obj, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	lenV := obj.Get("length")
	if lenV == nil {
		return nil
	}
	n := int(lenV.ToInteger())
	out := make([]goja.Value, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, obj.Get(fmt.Sprintf("%d", i)))
	}
	return out
}

func toIfaceSlice(vs []goja.Value) []interface{} {
	out := make([]interface{}, len(vs))
	for i, v := range vs {
		out[i] = v
	}
	return out
}
