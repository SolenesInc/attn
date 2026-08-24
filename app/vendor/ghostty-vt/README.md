# Ghostty VT WASM

`ghostty-vt.wasm` is ghostty-org's own prebuilt browser module. attn no longer
builds it: `app/src/ghostty` is a first-party binding over libghostty-vt's
current C API.

- Ghostty source: `ghostty-vt.pin`
- Provenance: `ghostty-vt-wasm.lock` (pin + SHA-256 of the mirrored bytes)
- Build target: `wasm32-freestanding`, `ReleaseFast` — upstream's, not ours

## Shared source pin

The browser module and the native worker library both read `ghostty-vt.pin`, so
the worker and the browser parse VT with one implementation. Moving the pin
moves both, and the browser half only exists for the commit upstream's rolling
`tip` release was last built from — so a pin bump and a re-mirror happen in the
same sitting.

## Mirroring

Upstream publishes the module on `tip`, which is rebuilt on every commit to
main: its assets cannot be pinned by hash, and the bytes for an older commit are
simply gone. So we mirror the exact upstream bytes for the pinned commit onto
our own keyed release as `ghostty-vt-<pin>.wasm`, and lock their SHA-256.

```bash
make publish-ghostty-vt-wasm     # maintainer: mirror the pin's module, rewrite the lock
```

`app/scripts/ensure-ghostty-vt-wasm.sh` runs ahead of every frontend
dev/build/test. It downloads and verifies, fail-closed; it never builds. A pin
that has not been mirrored stops the build rather than letting a stale terminal
core reach a bundle. The binary itself is gitignored — the lock is the record.

## The binding

`app/src/ghostty` holds it: `abi.ts` (C ABI signatures and struct offsets),
`callback.ts` (a hand-assembled wasm shim that installs a JS callback in the
function table, because JavaScriptCore has no `WebAssembly.Function`),
`terminal.ts` (the model the renderers read), `keyEncoder.ts` (the key ABI and
encoder), `input.ts` (the browser input controller), and `index.ts`. Binding
tests exercise the exact vendored module.

Viewport reads go through the render state API. Scrollback reads go through grid
references, which upstream warns are not render-loop material: measured at
0.72ms for a full 200x50 scrolled-back viewport against 0.53ms for the same
volume through the render state, so a scrolled pane stays inside a frame.

The key binding reads enum values by name from the exact module's type manifest
instead of copying ordinal values. `keyInput.binding.test.ts` drives real DOM
arrow events through the first-party controller against the pinned module in
normal and application-cursor modes.

Ghostty is MIT licensed; the license text is included beside the binary.
