---
name: verify-harness
description: Use before landing or opening a pull request whose changes touch internal/harness, harness Facts or Options, the harness catalog, model catalogs or model ids, launch options, or code in the daemon, store, CLI or native app that reads data a harness supplies.
---

# Verify the harness contract

Generic code works with every harness through `internal/harness` and the
pushed `harness_catalog`, and never knows which harness it has.
[The harness contract](../../../docs/internal/harness-contract.md) defines that
boundary. Read it first. `harnesslint` catches harness names and adapter
imports. It can't catch generic code that knows one harness's formats or
habits without naming the harness. This skill covers both. Report findings. Do
not edit.

## Run the lint

1. Run `make lint-harness`. It checks the Go code and `pty-host/`, `native/`,
   `cli/` and `mobile/`.
2. Report each failure as a finding. Never add a line to
   `scripts/harnesslint/allow.txt` or `scripts/harnesslint/violations.txt` to
   pass the lint: the allow list needs the user's approval, and the violation
   list only shrinks.

## Read the change

1. List the changes: `git diff --stat "$(git merge-base HEAD origin/main)"`.
   This includes uncommitted edits. When someone else is still editing the
   checkout, judge the commits alone and say so in the report.
2. Read each changed function outside `internal/harness/internal/` whole. Ask
   what it knows about a harness: a name, a value format, a file, a default,
   or a behavior that only one harness has.
3. Search the added lines for the places this knowledge usually hides:

   ```sh
   git diff -U0 "$(git merge-base HEAD origin/main)" -- . \
     ':!internal/harness/internal' ':!*_test.go' \
     | grep -E '^\+' \
     | grep -n -E '"/"|strings\.(Cut|Split|LastIndex|HasPrefix|TrimPrefix)|split_once|\.claude|\.codex|snipe|opencode|xirp'
   ```

   Each hit is a lead, not a finding. Read the code around it.
4. For each field of `harness.Facts`, `harness.Options`, `HarnessModel` or the
   catalog that the change adds, removes or gives a new meaning, list the
   adapters that set it. Search
   `internal/harness/internal/` and `internal/harness/appserver/`. When only
   one adapter sets the field, generic code that depends on it is a
   single-implementer part, which the contract allows only with the user's
   approval.
5. For each new or changed contract member, check that
   `TestEveryHarnessAnswersTheContractWithItsFake` exercises it. For a changed
   meaning, the test checks the behavior for every fake, not only that the
   field is present. When the conformance test has no row for that part of
   the contract, such as the model catalog, name the wire tests that cover it
   for each kind of harness, and report a kind none of them covers. When
   plugins supply it, check that
   [Writing a harness plugin](../../../docs/guide/reference/writing-harness-plugins.md)
   says what a plugin sends.

## Test each lead against every harness

For each lead, work out what the code does for five harnesses: Claude Code,
Codex, Sigma, a plugin harness such as attn-snipe, and a server-backed plugin.
Read each adapter's side, don't assume it. A built-in adapter lives in
`internal/harness/internal/`. For a plugin, read its checkout when you have
it, such as a sibling `attn-snipe` checkout, and otherwise the
fake harnesses in `internal/fakeagent/`. Code whose rule holds for all five
without knowing which one it has is not a leak. Code that is right for some
and wrong for others is a finding, even when no test fails today.

When the change adds or changes a contract member, or a field that harnesses
fill, ask one reader that has not seen your reasoning to do the same check.
Give it the diff without tests and generated files, this skill and the
contract doc, not your conclusions. Merge its findings into your report, and
say which ones it found too.

## Judge

| Do not | Example | What else guards it |
| --- | --- | --- |
| Build or split a value a harness owns, such as a model id, in generic code | The daemon added `provider + "/"` in front of the model at launch. attn-snipe's ids already held the provider, so the Builder role's Copywriter choice launched `gemini/gemini/gemini-4-carbon` and exited within 5 seconds, three times (seed `s-yj43kg`) | This review |
| Parse a harness's value to classify it | Tier rules matched the part of a model id after its last `/`. The harness now reports the name to classify (seed `s-6kdg2r`) | This review |
| Branch on a harness id, in code, a migration, or an app view | A migration that joins `provider/model` for Sigma but not for plugins | `harnesslint` for built-in ids, this review for plugin names and migrations |
| Rewrite stored data a harness owns in a migration that assumes one shape for it | A migration can't read catalogs. Its rewrite is safe when, for every harness, the new value is what attn launched before. Joining a saved provider and model gives Sigma's old launch name, and a wrong name for a plugin whose ids don't start with the provider | This review |
| Gate generic behavior on an option or fact only one kind of harness sets | `Options.Provider` marked the harnesses that took a separate provider, and attn joined it to the model for them | This review. A single-implementer part needs the user's approval |
| Name a harness's files, executables, environment variables or model names in generic code | A daemon path that reads `~/.claude/` | `harnesslint` matches built-in ids only, not paths or model names |
| Import an adapter package, or use an adapter symbol, outside `internal/harness/builtin` and `internal/harness/conformance` | | `harnesslint` |
| Add a `harness.Host` member that names a harness, or a `harness.As[T]` call for a single-implementer part outside `allow.txt` | | `harnesslint` |
| Work out in the native app what the catalog already pushes | The app split saved models at the first `/` to find a provider (`settings/route.rs`) | `harnesslint` for literals, this review otherwise |

The lint guards one shape of leak. Apply each rule to the changed code anyway.

## Report

Report only P0 and P1 findings, as the PR posture in `AGENTS.md` says. A
bug the change leaves in place counts when the change touches the code that
holds it. For each finding, give:

- the `file:line` and the row of the table it breaks
- the harnesses it is wrong for, and what goes wrong for each
- the change: move the knowledge into the adapter, or into a field every
  adapter states, and say why behavior stays the same for the other harnesses

If no change has a finding, say so in one line, and name the lint runs.
