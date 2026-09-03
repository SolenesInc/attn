# Release guide

This is the short maintainer runbook. [Making a release](making-a-release.md)
explains the branch model, changelog fragments, hotfixes, and main-to-next sync
in detail.

## Prepare the candidate

Start from a clean, current `next` branch:

```bash
git switch next
git pull --ff-only origin next
make release VERSION_TAG=v0.12.0
```

The command requires green exact-SHA `Acceptance` on `next`. It refuses an
existing tag, an open candidate, stale local state, or a `main` commit that has
not been synced into `next`.

It then:

1. Creates `release/v0.12.0` at the accepted `next` SHA.
2. Compiles and consumes the frozen changelog fragments.
3. Updates the app version and lockfiles.
4. Writes `.github/release-candidate.yml` with the source and `main` baseline.
5. Validates that the candidate contains release-only changes.
6. Pushes the branch and opens a ready PR to `main`.

It does not merge the PR, create a tag, or start the release workflow.
The compiled section carries a hidden SHA-256 receipt over the frozen fragment
paths and Git blobs, which candidate validation checks before merge.

## Accept the candidate

Review the generated changelog and the automated `App acceptance` job. It builds
the packaged Linux app from the exact candidate head, runs the real-app serial
matrix under Xvfb, and uploads its digest, pane text, and screenshots.

If Linux CI cannot cover the candidate, use the ready-to-fill command in the PR
to record a manual override from protected `main`:

```bash
gh workflow run app-acceptance.yml \
  --ref main \
  -f candidate_sha=<full candidate SHA> \
  -f profile=<profile> \
  -f scenarios='<scenarios run>' \
  -f evidence='<recording URL or concise receipt>' \
  -f outcome=passed
```

The override loads its workflow and receipt script from protected `main`, then
checks out the exact `candidate_sha` separately. Any candidate edit needs a new
automated or manual receipt. A manual dispatch does not restart candidate CI, so
rerun it after recording the override. Merge only when `PR gate` and `App
acceptance` are green for the same candidate head.

## Release preflight

The `Release Preflight` workflow runs in branch and PR context with no release
side effects. It builds and validates the Linux daemon and app runtime host on
`ubuntu-24.04` (`amd64`) and `ubuntu-24.04-arm` (`arm64`), then uploads the
artifacts for inspection.

## Release artifacts

The release workflow builds and publishes the signed and notarized macOS app,
the Homebrew DMG, and standalone Linux daemon binaries for `amd64` and `arm64`.
The macOS job requires these GitHub Actions secrets:

- `APPLE_CERTIFICATE`
- `APPLE_CERTIFICATE_PASSWORD`
- `KEYCHAIN_PASSWORD`
- `APPLE_API_ISSUER`
- `APPLE_API_KEY`
- `APPLE_API_KEY_P8`

After publication, confirm the release contains the versioned DMG,
`attn_aarch64.dmg`, `attn-linux-amd64`, and `attn-linux-arm64`. Then verify the
Homebrew path with `brew upgrade --cask victorarias/attn/attn`.

If a release workflow needs to be rerun for an existing tag, dispatch it with:

```bash
gh api --method POST repos/victorarias/attn/dispatches \
  -f event_type=release -F 'client_payload[tag]=v0.12.0'
```

The repository event loads the workflow from protected `main`, so an older tag
cannot select an older release workflow. The validation job never executes
gate code from the tag under inspection. The workflow rebuilds only after it
proves the tag is the exact current `main`,
its exact SHA has green `Acceptance` and `App acceptance` from `ci.yml`, or its
originating candidate has a green exact-head automated or protected-main manual
receipt, and its manifest and committed versions still agree.
Every build checks out the validated commit SHA. Publication stops if the
remote tag no longer resolves to that SHA. Publishing an older build after a
newer version has completed does not move GitHub's latest release or the
Homebrew stable-download path backward. Publication asks GitHub to select the
latest release atomically with its stable semantic-version policy.
