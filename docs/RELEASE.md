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
6. Pushes the branch and opens a draft PR to `main`.

It does not merge the PR, create a tag, or start the release workflow.

## Accept the candidate

Review the generated changelog, then install and exercise the packaged app from
the exact candidate head. The draft PR contains a ready-to-fill command for the
manual receipt:

```bash
gh workflow run app-acceptance.yml \
  --ref release/v0.12.0 \
  -f candidate_sha=<full candidate SHA> \
  -f profile=<profile> \
  -f scenarios='<scenarios run>' \
  -f evidence='<recording URL or concise receipt>' \
  -f outcome=passed
```

`App acceptance` fails if the workflow ref resolves to a different SHA. Any
candidate edit needs a new receipt. Make the PR ready only when `PR gate` and
`App acceptance` are green on the same head.

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
gh workflow run release.yml --ref v0.12.0 -f tag=v0.12.0
```

The workflow rebuilds only after it proves the tag is on `main`, its exact SHA
has green `Acceptance`, its originating candidate has green exact-head
`App acceptance`, and its manifest and committed versions still agree.
