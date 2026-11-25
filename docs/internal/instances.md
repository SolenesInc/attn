# Instances

Each named instance has its own data dir, socket, ports, app and bundle id.

```sh
eval "$(attn instance-env <name>)"   # fish: attn instance-env --fish <name> | source
attn instance                        # show active selection
attn instance resolve --field <f>    # read paths (dataDir, wsPort, appDaemon, appCli, appLocalDataDir)
attn instance clean <name>           # stop app/daemon/workers/hosts and remove data (--keep-app keeps app)
eval "$(attn instance-env --unset)"
```

- Always read paths from `attn instance resolve --field <field>`; never derive
  them.
- `ATTN_DATA_DIR` moves only paths without their own override: every attn
  session inherits `ATTN_SOCKET_PATH` and `ATTN_DB_PATH`, which beat
  `ATTN_DATA_DIR`. Select a named instance inside a session, or use
  `config.ScopeTestEnvironment` in tests.
- `attn` (Rust CLI) and `attnd` (Go daemon/workers) install side by side; never
  copy one without the other (`make build` writes both).
- **Install & run:** `make install-daemon INSTANCE=<name>` (Go only),
  `make install INSTANCE=<name>` (full app; `ATTN_APP_CARGO_PROFILE=release`
  for release profile), `make run INSTANCE=<name>` (opens unfocused). Run full
  macOS installs outside the sandbox so signing uses `security find-identity`
  instead of ad-hoc signing (preserving macOS privacy grants).
- **GitHub polling:** named instances disable real GitHub polling by default;
  pass `ATTN_GITHUB_POLLING=on` when testing against real GitHub.
- **UI automation:** enabled automatically on named instances; set
  `ATTN_AUTOMATION=1` on production only when needed.
- **Cleanup:** always run `attn instance clean <name>` when done. Never delete
  an instance data directory by hand—`clean` needs the worker and PTY host
  registries inside it to stop running processes. If `clean` reports a process
  it could not confirm or stop, leave it alone and hand the output to the user.

## Starting a daemon from inside a session

`ATTN_SESSION_HOME` and `ATTN_SESSION_BINARY` guard production against being
replaced or migrated by another build from inside a session (`attn daemon`,
`attn daemon ensure`, `attn _bridge`, `attn db restore`):

| Process routing | Result |
| --- | --- |
| No `ATTN_SESSION_HOME`, or selects a named instance | allowed |
| Data dir, DB and socket are all outside `ATTN_SESSION_HOME` | allowed |
| Only some of the three are inside `ATTN_SESSION_HOME` | refused |
| All three inside `ATTN_SESSION_HOME`, running `ATTN_SESSION_BINARY` | allowed |
| All three inside `ATTN_SESSION_HOME`, running another binary | refused |

Never bypass this with `env -i` and no instance: clearing the environment hides
`ATTN_SESSION_HOME` and lets a test build hit production.

## Other platforms and scripts

- macOS UI/recordings: [macOS VM](macos-vm.md) (`scripts/vm`)
- Linux packaged-app runner: [Local Linux runner](linux-runner.md)
- Windows (WSL2) VM: `scripts/windows-vm --help`
- Chief onboarding rehearsal: `node app/scripts/onboarding-rehearsal/rehearse.mjs`
  (never pass `--fresh-world` for the `upgrader` scenario; it relies on the
  removed chief-of-staff role).
