# attn

attn (Attention) wraps Claude Code, Codex, Copilot, and Pi with durable sessions,
terminals, and a keyboard-driven queue for agents waiting for the user. Harnesses
keep their native experience and remain usable as bare CLIs; plugins add more.
The Garden tracks work, visible delegations let users steer agents, crew members
have permanent charters, and automations start sessions on schedules or events.
Users can annotate terminal text and rendered markdown and send comments to a session.
Queue mode separates busy agents from those waiting for the user and advances
after each prompt. State classification and turn accounting serve that flow.

Sessions and terminals must survive app, daemon, and machine restarts. The Tauri
app runs on macOS and Linux; the daemon also serves remote hosts over SSH.
`cmd/attn` and `internal/**` must build and run on Linux. The daemon owns
application state; the app owns rendering.
Linux CI exercises the packaged application under Xvfb.

attn is Victor's most loved and most used piece of software. Its small user base
is intentional. Frictionless interaction, keyboard access, and performance matter
throughout: it runs all day, so idle CPU use and creeping memory are defects.

## Note from Victor

I love ambitious ideas and strive for simple, elegant systems. Refactoring
first so a new feature weaves in gracefully is the norm here, not an exception
to argue for. I work iteratively, crafting boutique software, not IKEA
software. Nothing wrong with IKEA; it just doesn't spark passion in me.

## You are probably running inside attn

- Never kill by name, pattern, or worktree path. Kill only a PID captured at
  spawn, or a port/socket owner confirmed by working directory.
- Production `~/.attn` is read-only. Copy data out; never run a test daemon
  against it, open it read-write, or clean it.
- Non-production builds, installs, launches, and restarts are pre-authorized.
  Production `make`, `make install`, and `make install-daemon` need Victor's
  explicit approval. Check the `[attn profile=…]` banner first.
- Never restart the daemon hosting this session.
- Never redirect `HOME` or resolve test config paths to production `~/.attn`.
  Use the [test isolation rules](docs/maintainer-contracts.md#test-safety)
  when adding or changing tests that reach config paths.

## Working rules

- Run tests when making changes. If relevant, install a non-production profile
  for additional verification.
- Make protocol bumps and DB migrations as needed by the changes.
- Diagnose before fixing. If the cause is unknown, propose instrumentation.
- Do not commit spikes.
- Do not add prose comments. Prefer self-explanatory code over comments.
- Product prompts address the user, never "Victor". Distinguish the agent
  changing attn from the agents it runs.
- Quote globs passed to shell commands; zsh rejects unmatched bare globs before
  commands such as `rg` can handle them.
- Do not ignore Reactor Doctor warnings and errors. Don't dismiss them as irrelevant.
  The bar to assume they are not applicable must be very high. Ask the user for approval
  to ignore them. Do not silently bypass it.

## Commands and verification

| Task                    | Command                  |
| ----------------------- | ------------------------ |
| Go tests                | `make test`              |
| Frontend tests          | `make test-frontend`     |
| Browser tests           | `make test-e2e`          |
| Go + frontend           | `make test-all`          |
| Go + frontend + browser | `make test-harness`      |
| Frontend dev server     | `pnpm --dir app run dev` |
| Lint                    | `make lint`              |

These targets fetch the native VT library and install `app/node_modules` as needed.
Prefer fast integration tests; do not copy production code into tests or test
compile-time guarantees. Use the [test contracts](docs/maintainer-contracts.md#test-safety)
when choosing time, property, or network-failure test helpers.

Choose checks for affected CLI, daemon, app, protocol, and Linux paths using
[verification requirements](docs/profiles.md#verification-requirements), including
any exemption. App-observable changes need the running app; visible changes need
a recording. Rendering changes must avoid continuous repainting; check idle CPU
and memory. If required verification is unavailable, ask before merging.

### Experience testing

Test feel with Victor early in spikes and at the end of substantial PR arcs.
Prepare a running profile from the branch, realistic data, and a short list
covering changed behavior, latency, and keyboard flow.

Before requesting approval or merging, remeasure every receipt in the final PR
description and verify each value independently against the exact head.

## Task-specific guidance

Read the relevant entry when the task touches its subject; unrelated entries
need no up-front reading.

| When changing or working on                                                            | Read                                                                                                                                                                                                           |
| -------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| State ownership, PTYs, store, jobs, Garden/crew, apps, or auto mode                    | [Ownership](docs/maintainer-contracts.md#ownership)                                                                                                                                                            |
| Command, event, or message shapes                                                      | [Protocol generation and versioning](docs/maintainer-contracts.md#protocol)                                                                                                                                    |
| `sdk/attn-app/src` or SDK consumers                                                    | [SDK generation and shared React](docs/maintainer-contracts.md#the-app-sdk)                                                                                                                                    |
| Event publishing, projections, consumers, or retention                                 | [Event bus](docs/maintainer-contracts.md#event-bus)                                                                                                                                                            |
| Native VT builds, ABI, or pin updates                                                  | [Native VT library](docs/maintainer-contracts.md#native-vt-library)                                                                                                                                            |
| Agent-facing content in `internal/prompts/content/**`, its Go definitions, or CLI help | [Prompt authoring](docs/prompt-authoring.md): run `go run ./cmd/prompt-editor context EVENT_OR_SOURCE --json` and read complete affected compositions before and after edits; `refresh` reloads Go definitions |
| Domain names or rules                                                                  | [Glossary](docs/glossary.md); update definitions with implementation                                                                                                                                           |
| Branches, PRs, merges, or waiting on reviews                                           | [Working with next](docs/working-with-next.md)                                                                                                                                                                 |
| Changelog fragments, releases, hotfixes, or syncing `main` into `next`                 | [Making a release](docs/making-a-release.md)                                                                                                                                                                   |
| Installing, launching, or verifying profiles                                           | [Profiles](docs/profiles.md)                                                                                                                                                                                   |
| Frontend code or shortcuts                                                             | [Frontend guidance](app/AGENTS.md)                                                                                                                                                                             |
| Packaged-app scenarios or recording/publishing evidence                                | [Harness guidance](app/scripts/real-app-harness/AGENTS.md)                                                                                                                                                     |
| Pi driver or auto-mode permissions                                                     | [Pi guidance](plugins/attn-pi/AGENTS.md)                                                                                                                                                                       |

## Diagnostics

- Daemon: `<data-dir>/daemon.log`.
- Dedicated PTY worker: `<data-dir>/workers/<daemon-instance>/log/<session>.log`.
- Shared PTY host: `<data-dir>/pty-hosts/<daemon-instance>/log/host.log`.
- Daemon code uses `d.logf(...)` or injected `LogFunc`; background stderr is lost.
- To debug an isolated daemon, quit its app, then `DEBUG=debug attn daemon ensure`.
