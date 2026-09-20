Source: https://github.com/dmmulroy/anti-slop

Revision: `c44ef22ca116d0ba62a3ff663a0bd13a3f3fa40b` (2026-09-10).
Previous source matched `6d538555cb151d4121ed51a27db81890eacf8ae9`.

Production source and licenses are vendored unchanged; upstream test suites remain
in the source repository. Rule selection is owned by attn in `.oxlintrc.json`.

Attn enables chained-assertion, widen-then-assert, unknown-alias, Reflect.get,
Reflect.apply, and reducer-copy checks, plus native `oxc/no-accumulating-spread`.
Safety-comment enforcement is off under attn's no-explanatory-comments policy.
The remaining generic rules and the Effect plugin are not enabled: attn retains
boundary validation, object identity tokens, module mocking, and existing formatting.
