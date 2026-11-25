# Script runner

- Never give script text to the binding's `Context.Eval`. It may compile text
  that holds `import`, `export` or `await` a second time, in a runtime
  without the limits, and evaluate it as a module. The prelude compiles the
  script with the engine's own `eval`.
- QuickJS throws `null` when no memory is left for an error object.
- A stopped loop carries the line of the last call or property read before it,
  which can be the wrong line, so a limit names no line.
- `EvalAwait` throws the first promise rejection that nothing handled, such as
  a refused command inside `forEach(async …)`. Nothing else in the binding
  shows one.
- The compute limit reads the CPU time of the script's locked OS thread, so
  waiting for an answer costs a script nothing.
