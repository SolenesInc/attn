# Making a release

## Changelog fragments

Each ordinary PR adds a uniquely named `changelog.d/*.yaml`:

```yaml
kind: fixed # added | changed | fixed | removed | internal
area: queue
change: Auto-settle advances to the next agent with an outstanding turn.
```

Describe what users observe; invisible work uses `kind: internal`. Optional
fields: `symptom`, `notes`. CI validates fragments;
`go run ./cmd/changelog-check` does the same locally.

## Release

From a clean, current `next` with green Acceptance:

```bash
./scripts/release.sh vX.Y.Z          # add --hold to promote without publishing
```

This opens a frozen `release/vX.Y.Z` PR to `main`. Merge it once `PR gate` and
`App acceptance` are green. `main` then earns Acceptance, gets tagged, and
publishes. A `--hold` candidate stops after Acceptance: no tag, no release, and
that version is never published. If `main` moves before merging, prepare the
candidate again.

If the automated app acceptance cannot cover the candidate, record a manual
receipt with the command printed in the candidate PR.

## Hotfix

Branch `hotfix/*` from `main`, commit the fix and fragment, then run
`make release-hotfix VERSION_TAG=vX.Y.Z`. If `main` Acceptance fails before
publication, repair it the same way, editing `CHANGELOG.md` directly.

## Sync main into next

After every `main` change, run `./scripts/sync-main-to-next.sh` and merge its PR
with a merge commit.

## After publishing

Confirm the release has the versioned DMG, `attn_aarch64.dmg`,
`attn-linux-amd64`, and `attn-linux-arm64`, then check
`brew upgrade --cask victorarias/attn/attn`. A failed publication leaves a draft
and a `Release health` issue; after fixing, retry while the tag still names `main`:

```bash
gh api --method POST repos/victorarias/attn/dispatches \
  -f event_type=release -F 'client_payload[tag]=<tag>'
```

Update the What's New modal (`app/src/components/WhatsNewModal.tsx`) for milestones.
