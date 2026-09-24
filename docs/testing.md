# Testing

Tests guard the promises attn makes. They are not how an agent checks its own
work. Check work by running it: the daemon over `wsctl`, the CLI, a
non-production instance, a throwaway test. Keep scratch checks out of commits,
the same way spikes stay out.

## Promises

attn keeps its promises at five boundaries:

- **Protocol**: the commands, responses, and events exchanged between the
  daemon and its clients: the app, the CLI, and remote daemons.
- **Durability**: sessions, terminals, and the database survive app, daemon,
  and machine restarts, and upgrades between versions.
- **CLI**: commands, output, and exit codes that users and agents rely on.
- **Screen**: what the user sees and does with the keyboard.
- **App SDK**: the API that apps and their views build against. Its declared
  types must match the protocol shapes they mirror; a check comparing the two
  guards both promises.

The protocol is the main seam. Most behavior is observable there, and both
the daemon and the app are tested against it.

## The rewrite rule

Before committing a test, ask: could everything behind the boundary it drives
be rewritten from scratch, behavior preserved, without editing the test? If
not, the test pins the implementation. Do not commit it.

For wire, stack, and scenario tests:

- They do not call unexported functions, read internal structs, or assert on
  log lines.
- They do not seed state by writing to the store or calling internals. They
  reach a state the way a client would, through the protocol, the CLI, or a
  prior daemon run.
- Only what the table below lists as faked is faked. Everything else behind
  the boundary is real.

A kernel test's boundary is its function, so it builds the function's input
directly.

## Kinds of test

Each kind is defined by where the test enters and what it may fake.

| Kind     | Enters and observes through                                | Real                                         | Faked                                               |
| -------- | ---------------------------------------------------------- | -------------------------------------------- | --------------------------------------------------- |
| Kernel   | A function with a written specification                    | The function                                 | Nothing                                             |
| Wire     | The protocol, from one side                                | Everything on the other side of the protocol | Agent binaries, external services, clock, network   |
| Stack    | The CLI, the protocol, or a browser page, across processes | Daemon, PTY workers and host, CLI, frontend  | Agent binaries, external services such as GitHub    |
| Scenario | Keyboard; observes the screen, the protocol, and the CLI   | Everything attn ships, packaged as shipped   | Agent binaries, external services such as GitHub    |

### Wire

Wire tests are the default. A daemon wire test runs a real daemon with a real
store, bus, and PTY layer, and acts as a protocol client. An app wire test
renders the real app with its real socket client, and acts as the daemon,
sending protocol messages and asserting on what renders and what the app sends
back. Both sides use the generated protocol types, so a protocol change breaks
both at once.

Fake agent binaries follow the real harness's observable contract: its hooks,
transcript files, and terminal output. Time uses `synctest`; network failures
use `newToxiProxy`. No test sleeps or polls.

### Stack

Stack tests cover what only exists between processes: restarts, reconnects,
signals, PTY ownership, CLI behavior, and Linux paths. The browser end-to-end
suite is a stack test: a real daemon serving the frontend in a browser.

### Scenario

Scenarios run the packaged app in CI under Xvfb, per the
[verification requirements](instances.md#verification-requirements) and the
[harness guide](../app/scripts/real-app-harness/AGENTS.md). They cover
rendering, focus, keyboard flow, and whole-product behavior such as queue mode.
Their verdicts come from the screen, the protocol, or the CLI, never from the
daemon's database.

### Kernel

A kernel test is justified only when both hold:

- the function has a specification in domain terms, independent of how it is
  written: the state classifier, turn accounting, parsers, the terminal wire
  rewrite, migrations;
- its input space is too large to cover through the protocol.

Kernel tests are tables, corpora, or `rapid` properties. One hand-picked
example per function is not a kernel test. A migration's input is a database
in an older version's schema and data, built however the test needs.

## Choosing a kind

Use the lowest kind that can observe the behavior:

1. Wire, by default.
2. Stack, when the behavior crosses a process boundary.
3. Scenario, when the behavior is pixels, focus, or keyboard.
4. Kernel, only under the conditions above.

A bug fix adds a regression test at the lowest kind that reproduces what the
user saw.

## Budgets

- A wire test finishes well under a second.
- A stack test finishes in a few seconds.
- Scenarios run in CI; run them locally only to reproduce a failure or
  develop a scenario.

A test that needs more than its budget belongs to a higher kind, or its
harness needs work.

## When a test breaks

If a change preserves behavior and a test breaks, the test pinned the
implementation. Delete it, or replace it with a test at the right kind. Do not
repair it to match the new internals.

## Isolation

Every test that reaches config paths follows the
[test safety contract](maintainer-contracts.md#test-safety).
