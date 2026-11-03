# Phone app releases

attn's phone app (`mobile/`) reaches testers through TestFlight.
`mobile/scripts/testflight.sh` builds it, signs it for team `VMGP8G85TK` and
uploads it to App Store Connect. Testers install it as the guide's
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

## Upload a build

Run the script on the Mac, from a checkout of the commit you want to ship:

```sh
mobile/scripts/testflight.sh
```

The script:

1. Reads the App Store Connect API key from the secrets below.
2. Fetches the pinned `ghostty-vt.wasm` the terminal view needs, builds the
   web app, and copies it into the iOS project with `cap sync ios`.
3. Archives the Release build with the build number set to the UTC time, as
   `YYYYMMDD.HHMMSS`, so every upload has a higher number than the last.
   `MARKETING_VERSION` in the Xcode project stays the version people see.
4. Signs the archive for App Store distribution and uploads it.

Signing is automatic. With the API key, Xcode creates the distribution
certificate in the cloud and the App Store provisioning profiles. The Mac needs no signing
identity of its own.

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

The script reads three secrets with `secret get`:

| Secret | Value |
|---|---|
| `apple/asc-api-key` | The whole `AuthKey_<key id>.p8` file |
| `apple/asc-key-id` | The key id, such as `2X9R4HXF34` |
| `apple/asc-issuer-id` | The issuer id, a UUID shown above the key list |

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
