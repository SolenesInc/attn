# Making a release

## Cut a release

Run from a clean, up-to-date `main`, **inside attn in the profile that holds
attn's seeds** (so `scripts/compile-changelog.sh` can read seed titles/notes
via `Attn-Seed:` trailers):

```sh
./scripts/release.sh vX.Y.Z
```

1. `release.sh` cuts `release/vX.Y.Z` from the newest `main` commit with green
   Acceptance that `reviewed` contains. If it stops because `reviewed` is
   behind, run `.agents/skills/review-main` or wait for review automation.
2. The script compiles `CHANGELOG.md` (with its covered-commits marker), bumps
   `VERSION` and its copies via `cmd/release-train`, and opens a PR to `main`.
3. Merge the PR once `PR gate`, `macOS PR gate` and `App acceptance` are green.
   If automated acceptance cannot cover the commit, record the manual receipt
   printed in the release PR and rerun CI.
4. CI runs a protected full run on the merged release commit and automatically
   tags and publishes once `Acceptance`, `Full macOS run` and `reviewed` all
   include it.
5. **Hotfix:** fix forward on `main`, then release a patch version.
6. Update What's New (`native/crates/attn-app/src/whats_new/`) for milestones.

## Manual dispatches and recovery

- **Signed, notarized macOS build without releasing** (uploads artifact
  `attn-<version>`; signing secrets live only in GitHub Actions):

  ```sh
  gh workflow run release-macos-app.yml -f version=0.16.0
  ```

- **Retry a failed publish** (after fixing the failure; refuses any tag not in
  `reviewed`):

  ```sh
  gh api --method POST repos/SolenesInc/attn/dispatches \
    -f event_type=release -F 'client_payload[tag]=<tag>'
  ```

- If Acceptance or `Full macOS run` fails on the release commit, fix on `main`
  and re-run `release.sh` with the same version (if a macOS job failed only
  because no runner picked it up, rerun that job instead).

## Post-publish check (untestable in CI)

Confirm the release assets include the DMG, `attn_aarch64.app.tar.gz`,
`{attnd,attn,attn-pty-host}-linux-{amd64,arm64}` and `SHA256SUMS-linux-*`.
Then run `brew upgrade --cask solenesinc/attn/attn`, open the app **from
Finder** (the only path that tests macOS Gatekeeper/notarization on first
launch), and confirm the shared PTY host is active and new agents run as its
children:

```sh
attn settings get pty_shared_host_active                 # true
ps -o command= -p "$(ps -o ppid= -p <new agent's pid>)"  # attn-pty-host ...
```

`next` is a workflow-maintained read-only mirror of `main`; never target or
push to it.
