# attn profiles

A profile gives an attn install its own data directory, socket, WebSocket port,
installed app, and bundle identifier. Agents can run profiles side by side
without collisions.

Set `ATTN_PROFILE` through `profile-env` so the CLI, daemon, builds, and tests
use the same profile. The command clears inherited routing overrides (`ATTN_DATA_DIR`,
`ATTN_SOCKET_PATH`, `ATTN_DB_PATH`, `ATTN_CONFIG_PATH`, `ATTN_PLUGIN_DIR`, and
`ATTN_WS_PORT`) so the selected profile actually wins, including inside an
attn-managed session.

```
attn profile-env agent7 | source     # fish:  set ATTN_PROFILE=agent7
eval "$(attn profile-env agent7)"     # bash/zsh
attn profile-env --unset | source     # back to the default profile
```

## Inspect a profile

```
attn profile            # status of the active profile
attn profile list       # every profile with data and/or an installed app
```

`attn profile status` prints the resolved data dir, socket, port, bundle id,
app path, e2e ports, and whether the daemon socket and app are present. Commands
using a non-default profile print an `[attn profile=… socket=… port=…]` banner.
Check it before any lifecycle command.

`ATTN_DATA_DIR`, when set, overrides every profile-derived data dir outright
(highest precedence, above `ATTN_PROFILE`). Tests and harnesses use it to scope
a process to a temporary directory. `attn profile resolve` and `attn profile
clean` still use the profile's canonical directories, so their paths can differ
from that process's runtime paths.

## Routing conflicts

Because an explicit override outranks `ATTN_PROFILE`, the two can contradict
each other. The profile name may say `agent7` while its resolved paths point to
production. On 2026-08-17 an inherited production
`ATTN_DATA_DIR` let `make install PROFILE=fb2lists` take the production PID
lock and run migrations on the production database.

The CLI and daemon reject `ATTN_PROFILE` alongside another profile's data
directory, socket, database, config, plugin directory, or WebSocket port. The
check runs before opening a database, taking the PID lock, binding a socket,
or stopping a daemon. The error names the conflict and how to clear it.

```
$ ATTN_PROFILE=fb2lists attn daemon ensure
ATTN_PROFILE=fb2lists disagrees with the routing this process resolved.
  profile fb2lists is /Users/victor/.attn-fb2lists (port 27441), but:
    ATTN_DATA_DIR    = /Users/victor/.attn
    ATTN_SOCKET_PATH = /Users/victor/.attn/attn.sock
    ...
  Fix: env -u ATTN_DATA_DIR … ATTN_PROFILE=fb2lists <command>
  Or clear them in your shell: eval "$(attn profile-env fb2lists)"
```

`ATTN_DATA_DIR` without `ATTN_PROFILE` remains valid for test scoping.
`attn profile` commands still run under a routing conflict and print it as a
warning, allowing inspection and cleanup.

## Resolve paths and ports

`internal/config` derives profile resources, and `attn profile resolve` exposes
them. Build and test tooling must read these resolved paths and ports.

```
attn profile resolve --json                 # full resolution as JSON
attn profile resolve --field wsPort          # one value (scripting)
attn profile resolve --profile dev --field bundleId
```

Resolved keys: `profile, label, dataDir, socket, dbPath, wsPort, bundleId,
appName, appPath, appExecutable, appDaemon, deepLinkScheme, e2eDaemonPort,
e2eVitePort`.

## Profile resources

| Resource | default | dev | named (e.g. `agent7`) |
|---|---|---|---|
| Data dir | `~/.attn` | `~/.attn-dev` | `~/.attn-agent7` |
| WS port | 9849 | 29849 | hash → `[20000,29848]` |
| Bundle id | `com.attn.manager` | `…​.dev` | `…​.agent7` |
| App (macOS) | `~/Applications/attn.app` | `attn-dev.app` | `attn-agent7.app` |
| App (Linux) | `~/.local/share/attn` | `attn-dev` | `attn-agent7` |
| Deep-link scheme | `attn` | `attn-dev` | `attn-agent7` |
| e2e daemon port | 19849 | hash → `[30000,30999]` | hash → `[30000,30999]` |
| e2e Vite port | 1421 | hash → `[31000,31999]` | hash → `[31000,31999]` |

Port bands are disjoint by construction: a throwaway e2e daemon never collides
with a *real* daemon of the same profile. Names match `[a-z0-9][a-z0-9-]{0,15}`.

## Build and install

Use the [approval and production-safety rules](../AGENTS.md#you-are-probably-running-inside-attn)
before any lifecycle command. Select a named profile first:

```bash
eval "$(./attn profile-env <name>)"
make install PROFILE=<name>
```

Open the named app with `make run PROFILE=<name>`.

Choose the cheapest build that includes the change:

| Change | Dev profile | Named profile |
| --- | --- | --- |
| Go-only (`cmd/attn`, `internal/**`) | `make install-daemon-dev` | `make install-daemon PROFILE=<name>` |
| App, plugins, protocol, or bundle metadata | `make dev` | `make install PROFILE=<name>` |

A daemon-only install replaces and re-signs the sidecar, then restarts the
daemon. Use a full build when unsure or when the sidecar install does not show
the change. The build tier does not determine the
[verification tier](#verification-requirements).

Full macOS builds and installs run outside the sandbox. Signing needs the
keychain; sandboxed identity lookup can fall back to ad-hoc signing and lose
persistent permissions. `scripts/macos-codesign-identity.sh` selects a stable
identity so permissions persist per bundle id.

## Verification requirements

Every non-trivial PR needs live verification from the branch in a running
non-production app/daemon. Exempt only:

- trivial docs/comments/renames/log strings; or
- an isolated change fully covered by unit tests, with no daemon lifecycle,
  protocol, PTY, background runner, timing, or UI behavior. State the reason.

Choose verification by the behavior users can reach:

- Exercise a self-contained CLI command through the built binary and its unit
  tests. For example, `attn pr wait-ready` only talks to `gh`.
- Verify daemon internals through a running daemon only when they have no path
  to the app.
- Verify app-observable changes in the running app. This includes daemon work
  that affects protocol, persisted state, broadcasts, PTYs, or PR/git flows.

Daemon lifecycle, protocol, PTY, background-runner, and UI changes always need
live verification. Run the selected profile's bundled preflight first. Fix its
tool, path, routing, daemon, or protocol failures before collecting evidence.
If the environment cannot run the required verification, stop and ask; do not
merge on automated tests alone.

## Verify the installed build

Run the selected profile's bundled CLI before live verification:

```bash
profile_app="$(./attn profile resolve --field appPath)"
"$profile_app/Contents/MacOS/attn" preflight
# Include the launch settings used by the scenario when applicable.
"$profile_app/Contents/MacOS/attn" preflight \
  --agent codex --model <model> --effort high --json
```

This checks the installed binary and its environment. An unrelated `attn` on
`PATH` cannot provide that evidence.

## Run tests under a profile

| Suite | Command | Isolation |
|---|---|---|
| Go unit/integration | `make test` | follows the [test-safety setup](maintaining.md#test-safety) |
| Frontend e2e | `make test-e2e` | derives this profile's e2e daemon + Vite ports; the per-run daemon kill is scoped to that port |
| Real-app scenarios | `pnpm --dir app run real-app:…` | follows the [harness profile and serial-run rules](../app/scripts/real-app-harness/AGENTS.md#running-scenarios) |

Go and frontend e2e suites can run concurrently in separate worktrees with
distinct profiles.

## Build isolation

- Bare `make`, `make install`, and `make install-daemon` target production and
  refuse at parse time if `ATTN_PROFILE` is set.
- `make install PROFILE=<name>` requires the shell's `ATTN_PROFILE` to match.
  `make dev` always targets `dev`.
- A profile's `.app` pins `ATTN_PROFILE`/`ATTN_WS_PORT` and strips inherited
  routing env at launch, so
  it can never reach another profile's daemon.
- The daemon refuses to start if its socket root and database would straddle
  two profiles, and restarts when the running
  daemon's profile no longer matches the caller's.

## Clean up

`attn profile stop-app --profile <name>` stops a profile by bundle id on macOS.
On Linux it reads `app.pid` and confirms the process through `/proc`. Nothing
to stop is success. A refusal exits nonzero, and `make install` stops with it.

Clean a temporary profile as soon as its work is done. Its daemon and workers
otherwise keep running after the branch merges:

```bash
attn profile clean <name>
```

The command reaps PTY workers, conversation hosts, and plugin runtimes, stops
the daemon, quits the app, and removes its bundle and data directory.

Workers survive daemon restarts, so `clean` shuts them down before removing the
data directory. It uses each worker's authenticated control socket from the
profile registry. If that socket is unreachable, it signals a PID only when
its argv still identifies the registered worker. Unconfirmed processes remain
running and are reported by PID for inspection with `ps -p <pid>`.

Removing the data directory first would delete the registry needed to find and
adopt surviving workers.

### Find a profile's worktree

`make install PROFILE=<name>` and `make install-daemon PROFILE=<name>` record the
worktree they ran from in `<data-dir>/origin.json`. The build fingerprint alone
cannot identify a checkout with identical source content.

`attn profile list` shows the origin worktree; `attn profile list --json` adds
the branch, whether the daemon is up, and how many workers are live. Cleaning a
profile removes its origin record along with everything else, so provenance never
outlives the profile.

That record is what powers the repository's PR-milestone cleanup hook
(`scripts/claude/attn-profile-nudge.sh`, wired in `.claude/settings.json`): after
an agent creates or merges a PR, it reminds the agent to clean any profile
installed from that worktree, and stays silent otherwise. Record an origin by
hand with `attn profile set-origin <name> [--worktree <dir>]` if you created a
profile outside `make install`.

## UI automation

Named profiles, including `dev`, expose the UI automation bridge. The app
writes a `ui-automation.json` manifest and serves the bridge on localhost with
a token. Production requires an explicit `ATTN_AUTOMATION=1` opt-in
(`profile::automation_enabled`). Bundle metadata comes from
`attn profile tauri-config`, with the profile's port and bundle id baked in.
