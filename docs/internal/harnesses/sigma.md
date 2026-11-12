# Sigma harness behavior

How Sigma behaves where attn meets it. The rules for adding a claim are in
[Harness behavior](../harnesses.md).

Read in the source of the Sigma fork of Pi at 8b5e9f24d
(`packages/sigma/README.md`, `src/protocol.ts`, `src/cli.ts`), execution targets
at 3b851359c, and probed with those builds on Nov 12, 2026.

- One `sigma serve` per Sigma home runs every conversation of the home in one
  Pi Durable harness on one SQLite database. Clients reach it over
  `SIGMA_HOME/sigma.sock`. A second server refuses a home whose owner is alive.
- The terminal view starts `sigma serve` when none answers, unless it runs with
  `--attach`, which waits for a running server.
- `initialize` answers `{server: "sigma", protocol: 1, version, instance}`.
  The framing and the method names are the ones Nisse uses, so the same
  control client talks to both.
- `sigma --conversation <id>` opens that conversation by its launch key, and
  creates it on first use. A retry after a lost reply finds the same
  conversation. The cwd, execution target, approval mode and instructions keep
  their creation values. A later open with a model or effort changes the pin.
- `conversation/submit` stores the input before it replies. A retry with the
  same `requestId` returns the same submission, also after a server restart.
- A completion is the settlement of one input, numbered by its submission.
  Numbers ascend within a conversation. `ended` carries the number as
  `completion`.
- `status/watch {after: {conversation: completion}}` opens with the busy
  conversations, then sends, for each listed idle conversation with a newer
  completion, an idle status with the newest completion. It then sends one
  status per change: `working`, `waiting` (a call waits for approval) or
  `idle` with the completion that ended the run.
- `bash`, `write` and `edit` wait for an approval unless the conversation
  opened with `--auto`. A waiting call survives a server restart.
- Every shell tool runs in the conversation's cwd with
  `SIGMA_ROOT_CONVERSATION` set to the launch key.
- `targets/list {cwd}` answers `[{id, name, available, detail}]`: `local` and
  `devvm`, the folder's DevVM project VM. `detail` says what an unavailable
  target needs, such as running `devvm` in the folder.
- `sigma --target ID` and `conversation/open {target}` pick the target of a new
  conversation; the default is `local`. An open of an existing conversation
  ignores the target. An open that cannot reach its target fails with the tag
  `TargetUnavailable` and creates nothing; the terminal view prints
  `execution target unavailable: <reason>` and exits. Sigma never runs a
  conversation's tools on another target.
- The terminal view prints the target, and for DevVM where edits land, when it
  opens and when it reattaches.
