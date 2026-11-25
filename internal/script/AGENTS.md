# Script runner

- Never give script text to the binding's `Context.Eval`. It may compile text
  that holds `import`, `export` or `await` a second time, in a runtime
  without the limits, and evaluate it as a module. The prelude compiles the
  script with the engine's own `eval`.
- QuickJS throws `null`, which a script can catch, when no memory is left for
  an error object, and keeps no record of it. `heap.go` limits the heap in the
  runtime's allocator to see it. Before a new version of the binding lands, read
  `JSMallocFunctions`, `JSMallocState`, the first fields of `JSRuntime` and the
  limit checks in `js_malloc_rt`, `js_calloc_rt` and `js_realloc_rt` again, and
  update the version `heap.go` names. A moved layout makes the runner refuse to
  start, which fails `TestAScriptRunsCommandsAndEndsAsTheAgentIsTold`.
- A stopped loop carries the line of the last call or property read before it,
  which can be the wrong line, so a limit names no line.
- `EvalAwait` throws the first promise rejection that nothing handled, such as
  a refused command inside `forEach(async …)`. Nothing else in the binding
  shows one, and `Context.Exception` loses a thrown value that is not an Error,
  so a `catch` in JavaScript reads it.
- The compute limit reads the CPU time of the script's locked OS thread, so
  waiting for an answer costs a script nothing. QuickJS asks the interrupt
  handler once in 10,000 branches and calls and never inside a built-in, so a
  command and `console.log` ask it too.
- A script can take the prelude's host functions out of a stack trace
  (`Error.prepareStackTrace`, `CallSite.getFunction`) and call them with
  anything. The daemon's side of the pipe checks each call.
