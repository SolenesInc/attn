---
name: promote-or-drop-experimental
description: Use when promoting an experimental attn feature out of experimental, making one always on or on by default, or dropping, removing or killing one.
---

# Promote or drop an experimental feature

An experimental feature is a `feature(...)` entry in
`internal/setting/settings.go` with `Stage: Experimental`. Promoting takes it
out of **Settings → Experimental**. Dropping deletes it. Pick the outcome, then
walk [every surface](#walk-every-surface) so nothing is left half done.

## Decide the outcome

### Promote, with no off switch

Everyone has the feature on. Whether a user can turn it off is a product
decision for the user. If the request did not say, ask with `attn ask`.

1. Delete the `feature(...)` entry.
2. Delete the off path. Every read of the setting becomes the on branch, and
   the code only the off branch reached goes too.
3. [Delete the stored row](#delete-the-stored-row). A user who had set `false`
   gets the feature like everyone else.
4. If any code has to stay for a release, such as for sessions or terminals
   already running on the old path, tell the user exactly what stays and until
   when, so they can track it. Plant a seed for its removal.

### Promote, with a switch

1. In the `feature(...)` entry, set `Stage: OnByDefault` and `Where` to the
   Settings page that holds the switch. The feature then leaves the
   Experimental page, and a stored `false` keeps working.
2. Render a `FeatureSwitch` on that page, as Asks does in
   `SettingsModalSections.tsx`. A feature with its own page in
   `useSettingsModalState.ts` keeps that page and its switch.
3. Remove "experimental" and "off by default" from `Description`, `Summary` and
   the docs.

`Stage` has no value for a promoted feature that stays off by default, and
`Refusal()` would send people to the Experimental page. If the user wants that,
change the `Stage` model first.

### Drop

Delete the setting and every line of code that exists for the feature. Keep no
compatibility, and leave nothing that says the feature existed, as
[Leave no trace](#leave-no-trace-of-a-drop) describes. Do not ask whether to
keep a fallback.

## Delete the stored row

A row for an unknown key does not fail anything: `GetAllSettings` keeps
returning it, and `setting.ClassOf` treats it as a home setting, so it travels
to outposts. Remove it with a migration, in the same commit as the setting:

```sh
go run ./cmd/db-migrations new retire_<key>_setting
```

```sql
DELETE FROM settings WHERE key = '<key>';
```

The statement also matches the copies outposts keep, which carry `copied_from`.
Then run `make generate-schema`, and follow
`docs/internal/db-migrations.md`. A switch that stays needs no migration.

## Leave no trace of a drop

Remove the feature's name from code, comments, docs, skills, prompts, tests and
fixtures. Do not write "removed", "formerly", "no longer" or a tombstone
anywhere, and do not add a `changelog.d` fragment.

Two kinds of text keep the name, because the repository's rules protect them:

- The migration above, which is the only new trace. `make check-migrations`
  refuses any edit or deletion of a merged migration, so it stays for good.
- A frozen migration or legacy writer that reads the setting. If deleting the
  variable breaks one, move only the key to `internal/setting/retired.go`, and
  say so in your report. Frozen code must keep behaving as it did.

Git history and the existing sections of `CHANGELOG.md` are records. Leave them.

## Walk every surface

Search for the key, the Go and TypeScript identifiers, and the display name.
This example is for `new_session_launcher_enabled`, displayed as Launcher 2.0. The first
search matches `new_session_launcher_enabled`, `NewSessionLauncherEnabled` and `newSessionLauncherEnabled`:

```sh
git grep -il -E 'new_?session_?launcher_?enabled'
git grep -il 'launcher 2.0' -- docs website .agents internal/prompts mobile
```

Stop when the first search returns only files you meant to keep. Check each
place below; for a switch that stays, change only what says "experimental" or
assumes off.

| Surface | Where |
|---|---|
| Registry | `internal/setting/settings.go`; `internal/prompttest/testdata/settings-registry.json` and `settings-help.json` |
| Generated | `make generate-types` rewrites `app/src/types/settings.generated.ts`, `cli/protocol/src/go_values.rs` and `cli/testdata/` |
| Daemon | Reads in `internal/daemon` and `internal/store`, `Refusal()` errors, and the session launch environment in `cmd/attn` and `internal/config` |
| App | `BoolSetting.<name>` and `isOn` callers, its page or nav entry in `useSettingsModalState.ts`, `SettingsModalSections.tsx`, and the palette commands that `useAppCommands.tsx` builds from `FEATURES` |
| CLI | `attn settings`, including `list --features`, and any command that refuses while the feature is off |
| Agent guidance | `FlagField` in `internal/prompts/session.go`, content under `internal/prompts/content`, and scenarios in `internal/prompts/scenarios`; then `make generate-prompts` |
| Outposts | Home settings copy to outposts, so check for code that reads the copy or a remote that sets it |
| Phone | `mobile/`, and the daemon's phone gateway |
| User docs | `docs/guide/README.md`, whose rows say "Experimental:", `docs/guide/reference/`, and `website/src/content/features/` |
| Maintainer docs | `docs/internal/`, and `.agents/skills/drive-attn/features/`, both the feature's file and its entry in `README.md` |
| Tests | Go wire and stack tests, app tests including `SettingsModal.experimental.test.tsx`, and `app/scripts/real-app-harness` scenarios and `scenarioCatalog.mjs`, and `app/scripts/onboarding-rehearsal` |

Removing a feature with its own files means deleting those files, the scenarios
that only cover it, and its feature map file.

## Find code the removal left unused

Deleting the reads strands the helpers, types, events and CSS behind them. The
compiler catches the first layer only, so keep going until a pass finds nothing:

1. Save the Go report before you start, after building the native library that
   the report needs:

   ```sh
   scripts/build-libghostty-vt.sh
   go run golang.org/x/tools/cmd/deadcode@latest ./cmd/attn | sort > /tmp/deadcode-<key>-before
   ```

2. After the removal, run it again and read `comm -13` of the two reports.
   Every new line is either code to delete or something only tests call. Delete
   tests that only covered the removed path.
3. For the app, `pnpm --dir app run typecheck` reports unused locals. Exports
   are not covered, so search for each export of the files you changed. Also
   search for CSS classes the removed components used.
4. Check what the removal orphans outside the type system: settings read only
   by this feature, protocol fields, bus events, database tables and columns,
   and environment variables. A protocol or bus change follows `AGENTS.md`.

## Verify

Run the feature's entries in its `drive-attn` file on a named instance, with a
database copied from real data or one that holds a stored value for the key.
Show these results in your report:

- `attn settings list --all` and `attn settings get <key>` agree with the
  outcome: the key is gone, or it is on with its switch outside Experimental.
- For a removal, the row is gone after the migration runs:
  `SELECT * FROM settings WHERE key = '<key>'` returns nothing.
- For a promotion, the feature works on a fresh database and on one that stored
  `false`, which stays off only when the switch remains.

Land with the `land-on-main` skill. Write commit messages that say what users
observe, because the release notes come from them.
