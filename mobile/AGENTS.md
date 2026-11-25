# Phone app (`mobile/`)

## TestFlight builds and traps

`mobile/scripts/testflight.sh` builds, signs for team `VMGP8G85TK` and uploads
to App Store Connect. `.github/workflows/testflight.yml` runs it on `main`
pushes that touch phone inputs, on manual dispatch, and weekly when the newest
upload is over 60 days old (before TestFlight's 90-day expiry).

- **Secrets.** The workflow reads `ASC_API_KEY`, `ASC_KEY_ID`, `ASC_ISSUER_ID`
  and `DISCORD_WEBHOOK` from the `testflight` GitHub Environment (`main` only).
  Locally on macOS, `testflight.sh` reads `apple/asc-api-key`,
  `apple/asc-key-id` and `apple/asc-issuer-id` via `secret get` (a team Admin
  App Store Connect API key, distinct from Attn Cloud's APNs key).
- **Public logs.** The repository is public and failed runs upload
  `archive.log` and `upload.log` for 14 days. Pass API key flags only to the
  export step (`xcodebuild` does not print export flags); never pass them to a
  step whose command line reaches a log.
- **Ad-hoc archive signing.** The archive step signs ad hoc to keep
  entitlements, and only the export step signs for the App Store using cloud
  certificates. Automatic signing during archive creates a new development
  certificate per ephemeral runner and hits Apple's cap.
- **Export compliance.** `ITSAppUsesNonExemptEncryption` is `false` in
  `mobile/ios/App/App/Info.plist` because Noise uses Apple's CryptoKit. If the
  phone ever ships non-OS cryptography, remove that key and answer compliance in
  App Store Connect.
- **Runner Xcode pin.** `testflight.yml` selects Xcode 26.3 via `DEVELOPER_DIR`
  (runner default is 16.4); update that path if GitHub removes it from
  `macos-15-arm64`.
- **First-time app record.** App Store Connect must have an iOS app record with
  bundle id `codes.attn.mobile` (and notification service extension
  `codes.attn.mobile.NotificationService`). A `409 Upload limit reached` means
  wait a day and dispatch again.
