# attn

attn stands for Attention: an interface friendly to both human and agent
brains, built as a harness augmenter. What it does today, and what each part
asks of you as a maintainer:

- Sessions and terminals survive app, daemon, and machine restarts.
- Native Claude Code, Codex, Copilot, and Pi experiences; plugins add harnesses.
- Queue mode advances through agents waiting for user attention.
- Daemon features work over SSH on Linux remotes.
- The Garden tracks work; delegations are visible, steerable sessions.
- Crew members have permanent identities and charters.
- Automations launch agents on schedules or events.
- Terminal and markdown annotations go to a session as one message.

The app (the Tauri UI) is mac only for now; Linux support is being worked upon.
The daemon runs on Linux, so daemon code stays portable.

The things we can never compromise on: frictionless experience, keyboard
friendliness, and performance. They go hand in hand. And attn runs all day,
every day: memory that creeps or CPU burned while idle is a defect.

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

## Working rules

- The daemon owns application state; the app owns rendering.
- Diagnose before fixing. If the cause is unknown, propose instrumentation.
- Prefer fast integration tests. Do not copy production code into tests or
  test compile-time guarantees.
- Avoid continuous repainting. Check idle CPU and memory.
- New actions need reversal and inspection: snooze/unsnooze, create/clean.
- Check affected CLI, daemon, app, protocol, and Linux paths before finishing.
  `cmd/attn` and `internal/**` must cross-compile and run on Linux.
- Plant a Garden plot for non-trivial work; put the plan in its body, pieces
  in children, and ordering in `blocks` edges. Offer parallel delegations
  for independent pieces. Small changes can go straight to a PR.
- Do not commit spikes; Victor decides what follows.
- Protocol bumps and DB migrations are routine. Make and verify them.
- Comments explain directives, measured limits, or hidden traps. Maximum two
  lines per block, enforced by `make lint`. Delete unclear compressed comments.
- Product prompts address the user, never "Victor". Distinguish the agent
  changing attn from the agents it runs.

## Verification

Choose each PR's verification and any exemption from
[profiles.md](docs/profiles.md#verification-requirements). App-observable changes
need the running app; visible changes need a recording. If required verification
is unavailable, ask before merging.

### Experience testing

Test feel with Victor early in spikes and at the end of substantial PR arcs.
Prepare a running profile from the branch, realistic data, and a short list
covering changed behavior, latency, and keyboard flow.

## Guidance

@docs/maintaining.md

- Read [glossary.md](docs/glossary.md) before naming domain concepts; update
  definitions and implementation together.
- Read [working-with-next.md](docs/working-with-next.md) before creating
  branches, opening or merging PRs, or waiting on reviews.
- Read [making-a-release.md](docs/making-a-release.md) before adding changelog
  fragments, preparing releases or hotfixes, or syncing `main` into `next`.
- Read [profiles.md](docs/profiles.md) before installing, launching, or
  verifying a profile, and when choosing a PR's verification requirements.
- Read [app/AGENTS.md](app/AGENTS.md) before changing frontend code or shortcuts.
- Read [harness guidance](app/scripts/real-app-harness/AGENTS.md) before
  writing/running packaged-app scenarios or recording/publishing evidence.
- Read [pi guidance](plugins/attn-pi/AGENTS.md) before changing pi/nisse drivers
  or auto-mode permissions.
