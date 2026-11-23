---
name: verify-harness
description: Use before landing or opening a pull request whose changes touch internal/harness, harness Facts or Options, the harness catalog, model catalogs or model ids, launch options, CLI hooks, or code in the daemon, session monitor, store, CLI, PTY host, native app, or phone app that reads data a harness supplies or branches on harness behavior.
---

# Verify the harness contract

Generic code works with every harness through `internal/harness` and the
pushed `harness_catalog`, and never knows which harness it has. Read
[the harness contract](../../../docs/internal/harness-contract.md) first.
`make lint-harness` enforces the mechanical boundaries that doc lists, but it
cannot catch generic code that knows one harness's formats, lifecycle, or
habits without naming the harness. This skill covers both. Report findings. Do
not edit.

## Run the lint

1. Run `make lint-harness`. It checks Go code and `pty-host/`, `native/`,
   `cli/`, and `mobile/`.
2. Report each failure as a finding. Never add a line to
   `scripts/harnesslint/allow.txt` or `scripts/harnesslint/violations.txt` to
   pass the lint: the allow list needs the user's approval, and the violation
   list only shrinks.

## Read the change

1. List the changes: `git diff --stat "$(git merge-base HEAD origin/main)"`
   and untracked files with `git ls-files --others --exclude-standard`. That
   diff includes uncommitted edits. When someone else is still editing the
   checkout, judge the commits alone and say so in the report. To review
   commits that already landed, use `git show --stat <rev>`, read changed
   functions at that revision with `git show <rev>:<path>`, and say that the
   lint ran on the checkout, not on those commits.
2. Read each changed function outside `internal/harness/internal/` whole,
   skipping generated files.
   - For `internal/harness/appserver/`, judge against the `attn-app-server-v2`
     protocol it implements for Sigma and every server-backed plugin.
     Wire-protocol work, such as building `provider/id` from `models/list`,
     belongs there. Behavior specific to Sigma belongs in
     `internal/harness/internal/sigma/`. Shared server behavior belongs in
     `appserver/` only when
     [Writing a harness plugin](../../../docs/guide/reference/writing-harness-plugins.md)
     documents it and, for anything beyond the required calls, a declared
     ability gates it.
   - For per-harness hook adapters in `cli/attn/src/hooks/` (`claude.rs`,
     `codex.rs`), check that they translate harness payloads into the existing
     neutral evidence messages. Shared files in `cli/attn/src/hooks/` are
     generic code.
   - For all other code, ask what it knows about a harness: a name, a value
     format, a file, a default, a vocabulary, or a behavior that only one
     harness has.
3. Look in the diff for the places harness knowledge usually hides. Each one
   is a lead, not a finding. Read the code around it.
   - String handling of a value a harness supplies: splitting, joining,
     prefix checks, regular expressions, or JSON field reads on a model id, a
     transcript line, a hook payload, a screen snapshot, a notification kind,
     or a stop reason.
   - A sentinel or special value stored in a slot a harness owns, such as an
     opaque cursor, conversation id, model id, or run id, that an adapter can
     receive or a real harness value can collide with.
   - Harness-owned paths and names in generic code: a dot folder in the home,
     a transcript or project layout, a config file, an executable, an
     environment variable, or a model name.
   - A fallback harness when none was named, a hardcoded harness list or
     order, or an assumption that every harness stays installed, offered, and
     registered on every machine.
   - A harness's name, executable, environment variable, model name, or tool
     name in a prompt, a skill, CLI help, or a doc that every harness's agents
     read.
   - A field harnesses fill, or an optional part, that the change starts to
     read, stops reading, or reads with a new meaning.
   - A generic test that drives only one harness's fake from
     `internal/fakeagent/`.
4. For each field harnesses fill that the change adds, removes, or gives a new
   meaning, list the adapters that set it. Search
   `internal/harness/internal/` and `internal/harness/appserver/`. When only
   one adapter sets the field, generic code that depends on it is a
   single-implementer part, which the contract allows only with the user's
   approval.
5. For each new or changed contract member, check test and plugin-doc coverage:
   - When the change touches `Facts`, `Health`, `OneShot`, or unreached
     `Conversation` handles, confirm that
     `TestEveryHarnessAnswersTheContractWithItsFake`
     (`internal/daemon/harness_conformance_wire_test.go`) exercises it,
     including any changed meaning, across Claude Code, Codex, Sigma, and a
     terminal plugin (`house`). Because that test has no server-backed plugin
     row, also check wire test coverage for server-backed plugins when the
     member applies to them.
   - For any other part of the contract, name the wire tests that cover each
     kind of harness (including `eachAppServer` for Sigma and server-backed
     plugins), and report any kind none of them covers.
   - When plugins supply or rely on the member, check that
     [Writing a harness plugin](../../../docs/guide/reference/writing-harness-plugins.md)
     matches the code.

## Test each lead against every harness

For each lead in generic code, work out what the code does for five harnesses:
Claude Code, Codex, Sigma, a plugin harness that runs in a terminal, and a
server-backed plugin. Start with the `Facts` field or optional part that
decides which harnesses reach the code at all. Then read each adapter's side
instead of assuming it. All four Go adapters live in
`internal/harness/internal/` (`claude`, `codex`, `sigma`, and `plugin`), and
Sigma and server-backed plugins share `internal/harness/appserver/`. For the
plugin process itself, read its checkout when you have one, and otherwise read
the fake harnesses in `internal/fakeagent/`. Code whose rule holds for all
five without knowing which harness it has is not a leak. Display-only
formatting that degrades cleanly for any value shape, such as omitting a
repeated provider label from the start of an id, is not a leak either. Code
that is right for some harnesses and wrong for others is a finding, even when
no test fails today.

The five differ most in how attn learns what they do and how they live across
machines. Claude Code and Codex report through CLI hooks and a transcript file,
and shared Codex also runs a server. Sigma and server-backed plugins report
over `attn-app-server-v2` and keep conversations in the server. A terminal
plugin may report nothing but its screen. Any harness can have a missing
executable, and a plugin harness can be unregistered after a restart or owned
by another plugin on an outpost. Code that reads state, delivers input, caches
catalogs, or recovers a session usually holds for one of these and not the
others.

If you wrote the change and it adds a contract member or a field that harnesses
fill, or gives one a new meaning, ask one fresh reader that has not seen your
reasoning to do the same check. Give it the diff without tests and generated
files, this skill, the contract doc, and any recorded user decision from the
seed, not your conclusions. Tell it to skip `make lint-harness` and spawning
another reader. Merge its findings into your report, and say which ones it
found too. If you are already running as an independent reviewer, skip spawning
another reader and say so.

## Judge

The approved leaks in `docs/internal/harness-contract.md` (Leak 1, Leak 2, and
the CLI hook adapters) are not findings, and neither is a single-implementer
exception or migration trade-off the user already approved on the seed. A new
use of a Leak 1 part outside the caller files in `scripts/harnesslint/allow.txt`
is a finding.

`make lint-harness` enforces the mechanical checks from the contract doc, and
where the last column names `harnesslint`, it guards only that narrow path.
Apply every rule below to the changed code anyway.

### Values, files, and vocabulary

| Do not | Example | What else guards it |
| --- | --- | --- |
| Build or split a value a harness owns, or put a colliding sentinel in a slot a harness fills | The daemon added `provider + "/"` in front of the model at launch, so attn-snipe launched `gemini/gemini/gemini-4-carbon` and exited three times (seed `s-yj43kg`). Earlier code recognized server terminals by a `"-server"` suffix until `adapter_terminals` replaced it (d423713cf) | This review |
| Parse a harness's value to classify it | Tier rules matched the part of a model id after its last `/`. The harness now reports `family` to classify (seed `s-6kdg2r`) | This review |
| Read one harness's transcript or screen format in generic code | `agent_peek.go` matched Claude Code's composer-suggestion cells with a regular expression on the screen snapshot. `harness.ScreenReader` now answers `ShowsComposerSuggestion` (cfcb2614a) | This review |
| Read one harness's vocabulary in the daemon | The daemon mapped Claude Code's `permission_prompt` and `idle_prompt` notification kinds, and read its `background_tasks` and `session_crons` stop fields. Each hook file in `cli/attn/src/hooks/` now sends neutral evidence (f3389d54b) | This review |
| Know or name a harness's file layout, executables, environment variables, or model names in generic code | `conversation_files.go` kept only `.claude/projects/…/<id>.jsonl` and `.claude/file-history/<id>` in a kept conversation. `harness.Keeper.ClassifyFile` now says which files travel with it (fb0056a04) | `harnesslint` matches built-in ids only, not paths or model names |

### State, lifecycle, and delivery

| Do not | Example | What else guards it |
| --- | --- | --- |
| Fit state or recovery logic to hook-based or server-based harnesses only | After a restart, recovered state trusted the terminal's last waiting or approval claim unless an adapter overrode it. A harness that forgets that claim now states `Signals.ForgetsRecoveredClaim` (cfcb2614a) | This review |
| Treat one harness's delivery outcomes as all of them | Code that counts only `Taken` as delivered treats a `Held` input as lost or retries a `Rejected` input. Handle every `Outcome` a `Custody` can carry, and `Custody.Uncertain` | This review |
| Pick a default harness or give generic behavior a default only one harness needs | Empty agent fields defaulted to Claude Code or Codex (71ee65bea). Session activity set effort `low` for Codex and the garden advisor accepted only `claude` and `codex` (b92f5c7cf). Empty harnesses are now refused, `Facts.Tiers` holds each harness's Light choice, and any `harness.OneShot` qualifies | `harnesslint` for built-in ids, this review otherwise |
| Assume every harness is installed, offered, or owned by the same plugin on every machine | Cached model catalogs outlived a missing executable or unregistered plugin (93986a6af), and an outpost could launch an agent under another plugin until `Facts.Owner` traveled with the launch (d9e455f06) | This review |

### Boundaries, migrations, and the app

| Do not | Example | What else guards it |
| --- | --- | --- |
| Branch on a harness id in code, a migration, or an app view | A migration that joins `provider/model` for Sigma but not for plugins | `harnesslint` for built-in ids, this review for plugin names and migrations |
| Gate generic behavior on an option or fact only one kind of harness sets | `Options.Provider` marked the harnesses that took a separate provider, and attn joined it to the model for them | This review. A single-implementer part needs the user's approval |
| Rewrite stored data a harness owns in a migration that assumes one shape for it | A migration cannot read catalogs. Its rewrite is safe when, for every harness, the new value is what attn launched before. Joining a saved provider and model gives Sigma's old launch name, and a wrong name for a plugin whose ids do not start with the provider | This review |
| Work out in the app what the catalog already pushes, or read the catalog before it loads | The app split saved models at the first `/` to find a provider (`settings/route.rs`), and deep links acted before `state.facts.harnesses_loaded` was true (eadc651ef) | `harnesslint` for literals, this review otherwise |
| Put Sigma-only or undocumented protocol behavior into `internal/harness/appserver/`, or skip an ability gate for optional behavior | Every server-backed plugin shares `appserver`. Uncontracted event kinds, fields, or defaults break other `attn-app-server-v2` servers or drift from `writing-harness-plugins.md` | `harnesslint` for `"sigma"` literals, this review otherwise |

### Prompts, docs, and tests

| Do not | Example | What else guards it |
| --- | --- | --- |
| Write a prompt, skill, or doc for every agent that assumes one harness | A product prompt that tells every agent to use a tool, file, or skill directory only one harness has | This review |
| Test generic behavior with one harness's fake | A wire test for delivery that drives only the Claude Code fake proves nothing for server-backed harnesses. Drive a fake of each kind the behavior differs for (`eachAppServer`, plugin fakes), or use the conformance test | This review |

## Report

Report only P0 and P1 findings, as the PR posture in `AGENTS.md` says. A bug
the change leaves in place counts when the change touches the code that holds
it. For each finding, give:

- the `file:line` and the rule it breaks (table row or lint check)
- the harnesses it is wrong for, and what goes wrong for each
- the change: move the knowledge into the adapter, gate it on a declared
  ability or `Facts` field every adapter states, or align the plugin doc, and
  say why behavior stays the same for the other harnesses

If no change has a finding, say so in one line, and name the lint runs.
