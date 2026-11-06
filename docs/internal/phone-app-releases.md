# Phone app releases

attn's phone app (`mobile/`) reaches testers through TestFlight.
`mobile/scripts/testflight.sh` builds it, signs it for team `VMGP8G85TK` and
uploads it to App Store Connect. The `.github/workflows/testflight.yml` workflow
runs that script on GitHub's Mac runners. Testers install the app as the guide's
[phone app](../guide/reference/phone-app.md) page says.

## One-time setup

Besides the API key under [Secrets](#secrets), App Store Connect needs an app
record before the first upload. Without it, the upload fails with
`Error Downloading App Information`, and the script says the app record is
missing.

1. Run the script once. Xcode's automatic signing registers the App IDs that
   the app's entitlements need, `codes.attn.mobile` and
   `codes.attn.mobile.NotificationService`, under
   [Identifiers](https://developer.apple.com/account/resources/identifiers/list).
   Then the upload fails because the app record is missing.
2. Create the app record in [Apps](https://appstoreconnect.apple.com/apps) with
   **+ → New App**: platform iOS, name `attn.codes` (`attn` is taken),
   primary language English (U.S.), bundle id `codes.attn.mobile`, SKU
   `attn-mobile`, full access. The bundle id of an app record cannot change.
3. Run the script again.

## When a build runs

The workflow builds:

- on every push to `main` that changes the phone: `mobile/**`,
  `internal/protocol/schema/gateway.tsp`, `ghostty-vt.pin`, or a file in `app/src`
  that the phone's bundle includes
- when you start it: **Actions → TestFlight → Run workflow**, or
  `gh workflow run testflight.yml`
- on its weekly schedule, only when the newest upload is over 60 days old, so a
  quiet phone never meets TestFlight's 90-day expiry

One build runs at a time, and at most one waits. A push during a build replaces
the build that waits, so merges that land together become one follow-up build
of the newest commit. Changes to the daemon's gateway code build nothing,
because they do not change the app.

The workflow lists the `app/src` files by name, taken from the bundle's module
graph, because the rest of `app/src` changes far more often and would spend
builds on code the phone does not run. To refresh the list, for example after
the phone starts importing something new, run this and replace the `app/src`
lines under `paths` in the workflow with its output:

```sh
pnpm --dir mobile install --frozen-lockfile
node mobile/scripts/testflight-paths.mjs
```

A stale list only delays a change until the next build, so nothing checks it.

The workflow selects its Xcode with `DEVELOPER_DIR`, because the runner's
default Xcode is too old for Capacitor 8. When GitHub removes that version from
[the runner image](https://github.com/actions/runner-images/blob/main/images/macos/macos-15-arm64-Readme.md),
the build fails and the path needs updating.

A failed build posts to Discord with the reason and the run's link, because the
sheriff watches only the CI workflow. A failed run keeps its `archive.log` and
`upload.log` as the `testflight-logs` artifact for 14 days. The repository is
public, so the script replaces the key id and issuer id in those logs.

Apple publishes no upload limit. When App Store Connect answers an upload with a
409, the alert quotes its answer. `Upload limit reached` means wait a day, then
start the workflow by hand. Any other 409 names what App Store Connect rejected.

## Upload a build from the Mac

The Mac takes no part in the normal flow, but the script still runs there. From
a checkout of the commit you want to ship:

```sh
mobile/scripts/testflight.sh
```

The script:

1. Reads the App Store Connect API key as [Secrets](#secrets) says.
2. Fetches the pinned `ghostty-vt.wasm` the terminal view needs, builds the
   web app, and copies it into the iOS project with `cap sync ios`.
3. Archives the Release build with the build number set to the UTC time, as
   `YYYYMMDD.HHMMSS`, so every upload has a higher number than the last.
   `MARKETING_VERSION` in the Xcode project stays the version people see.
4. Signs the archive for App Store distribution and uploads it.

The archive is signed ad hoc, which keeps its entitlements, and only the export
signs for the App Store. With the API key, Xcode uses a distribution certificate
in the cloud and creates the App Store provisioning profiles, so the machine
needs no signing identity of its own. Automatic signing at archive time would
create a new development certificate on each fresh runner, and Apple caps those.

The archive and the `archive.log` and `upload.log` files land in
`mobile/ios/App/build/testflight-<build>/`. When a step fails, the script prints
that step's `error:` lines and the path of its log.

App Store Connect processes a build before TestFlight offers it. The first
builds took 2 to 4 minutes.

## Export compliance

App Store Connect asks of every build whether the app uses encryption beyond
what Apple's operating system provides, and TestFlight does not offer a build
until it has the answer. The answer is no: the phone's Noise encryption runs
on CryptoKit, Apple's own cryptography. `ITSAppUsesNonExemptEncryption` is
`false` in `mobile/ios/App/App/Info.plist`, so each build carries the answer
and never stops at **Missing Compliance**. If the phone ever ships its own
cryptography, that answer changes: remove the key and answer the questions in
**Apps → attn.codes → TestFlight** for the build.

## Secrets

The script reads three values. In the workflow, they come from the `testflight`
GitHub Environment, which only `main` can use. On the Mac, they come from
`secret get`. The script takes a value from its environment variable when that
is set, and removes the variable before it runs a build tool.

| Environment secret | `secret` name | Value |
|---|---|---|
| `ASC_API_KEY` | `apple/asc-api-key` | The whole `AuthKey_<key id>.p8` file |
| `ASC_KEY_ID` | `apple/asc-key-id` | The key id, such as `2X9R4HXF34` |
| `ASC_ISSUER_ID` | `apple/asc-issuer-id` | The issuer id, a UUID shown above the key list |

The Environment also holds `DISCORD_WEBHOOK`, where failed builds post.

The key is a team key with the **Admin** role, made in
[App Store Connect](https://appstoreconnect.apple.com/access/integrations/api)
under **Users and Access → Integrations → App Store Connect API**. Admin lets
the key create the cloud-managed distribution certificate. Apple offers the
`.p8` file for download once. Store each value from the Mac, then delete the
download:

```sh
secret put apple/asc-api-key < ~/Downloads/AuthKey_<key id>.p8
printf %s '<key id>' | secret put apple/asc-key-id
printf %s '<issuer id>' | secret put apple/asc-issuer-id
rm ~/Downloads/AuthKey_<key id>.p8
```

To put a new key in the Environment, from the Mac:

```sh
secret get apple/asc-api-key | gh secret set ASC_API_KEY --env testflight --repo SolenesInc/attn
secret get apple/asc-key-id | gh secret set ASC_KEY_ID --env testflight --repo SolenesInc/attn
secret get apple/asc-issuer-id | gh secret set ASC_ISSUER_ID --env testflight --repo SolenesInc/attn
```

This key is not the APNs key that Attn Cloud uses to send push notifications.

## Add a tester

Testers in the team are internal testers. They get each build once processing
ends, with no review by Apple.

1. In App Store Connect, open **Users and Access** and invite the person with
   the **Developer** role, or a higher one.
2. Open **Apps → attn.codes → TestFlight**, choose the internal testing
   group, and add the person.

People outside the team are external testers. Apple reviews the first build
that goes to an external group.
