# Phones

A paired phone sees the agents of one home profile and keeps that list
current. The home dials out to its relay in Attn Cloud and phones dial in, so
neither side needs an open port. Home and phone talk Noise end to end inside
the relay's frames, so the Cloud sees only ciphertext. The phone talks to the
home's gateway, an API apart from the app's `/ws` protocol that only grows, so
a phone from the App Store keeps working across home builds. Phone access is
experimental and off until the user turns it on.

The Cloud wire contract is `docs/protocol.md` in `SolenesInc/attn-cloud`. The
cloud link is `internal/cloudlink` (home keys in the data directory as
`cloud_sign_key`, `cloud_noise_key` and `cloud_home_id`), pairings are the
`phone_pairings` table, the daemon side is `internal/daemon/phones.go`, the
gateway is `internal/daemon/gateway.go`, the gateway API is
`internal/protocol/schema/gateway.tsp`, and `scripts/check-gateway-compat`
refuses a gateway change that is not an addition. The phone's Noise framing is
pinned by `internal/cloudlink/testdata/noise_vectors.json`.

## Sub-features

- `phone-access-setting`: the experimental switch. While it is off, the daemon holds no connection to Attn Cloud, and the gateway refuses every hello. Turning it off closes the link and ends every phone's conversation.
- `cloud-link`: while phone access is on, the home registers once, keeps one socket to its relay with a ping every 15 s, and reconnects after a loss from 100 ms backing off to 10 s. Settings → Phones shows connected, connecting, or the last error.
- `pairing`: Settings → Phones shows a QR code per profile. Its secret works once, for 10 minutes, and the relay admits unknown devices only while a code is live. The first message the phone sends after the handshake writes the pairing.
- `revoke`: removes the pairing, sends the relay the new allowlist, and closes the phone's conversation with that profile.
- `gateway-hello`: the phone says hello with its api version. The home answers welcome, serving min(the phone's api, `GATEWAY_API_VERSION`), with the home's name and the pairing's profile, or refused with a reason.
- `gateway-sessions`: after welcome, and again whenever one changes, every session of the pairing's profile, with its state mapped to the gateway's states. Sessions of other profiles never reach the phone.

## How to get to it

### People

- **Settings → Experimental**, "Phone access" (`app/src/components/SettingsModalSections.tsx`), the home setting `phone_access_enabled`.
- **Settings → Phones** (`app/src/components/PhonesSettings.tsx`), listed under Connectivity while phone access is on: the link, "Pair a phone" per profile, the paired phones with Revoke.
- Their own shell: `attn settings set phone_access_enabled true`.

### Agents

- None. Phones are paired and revoked by the person.

## Driving it

Preconditions:

- A relay. Run the relay of a checkout of `SolenesInc/attn-cloud` under workerd, as its `test/relay.test.ts` does, and add `export ATTN_CLOUD_URL=http://127.0.0.1:<port>` to `$DRIVE_ENV` before starting the daemon. Without `ATTN_CLOUD_URL` the daemon dials `https://cloud.attn.codes`.
- Use the baseline instance with its app, or the daemon alone for the link and the protocol.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-phones-proof.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.

- **Off by default.** `./attn settings get phone_access_enabled` prints an empty line, which reads as off, and `ss -tnp` shows no connection from the daemon's pid to the relay.
- **The person turns it on.** Press ⌘, (`bridge native_key '{"key":",","modifiers":["command"]}'`), click `[data-testid="settings-nav-experimental"]` natively, and click `[aria-label="Phone access"]` natively after `dom_native_point` scrolls it into view. `./attn settings get phone_access_enabled` prints `true`, and the daemon holds one connection to the relay. From the person's shell, `./attn settings set phone_access_enabled true` does the same.
- **Pairing and revoke.** Click `[data-testid="settings-nav-phones"]` natively, then "Pair a phone" in the profile's section. A phone is the test device in `internal/testworld/phone.go`. The stack test drives it end to end: `./scripts/test-go.sh --packages -v -run 'Phone|TestDeletingAProfile|TestOnlyTheHome|TestUnreadableCloudKeys' ./cmd/attn`, against a Go stand-in for the relay, or against a real relay with `ATTN_TEST_CLOUD_URL=http://127.0.0.1:<port>`.
- **The gateway.** `./scripts/test-go.sh --packages -v -run TestGateway ./internal/daemon`.
- **Proof.** In `$P`, keep each `./attn settings` command's output, the `ss` lines, the test output, and `bridge capture_native_window_screenshot '{"path":"<P>/phones.png"}'` of Settings → Phones, `<P>` written out.

## Contexts

- **Profiles**: a pairing reaches only its profile. The profile comes from the pairing, never from the phone's hello. A phone pairs with each profile separately, with one QR code each. Deleting a profile removes its pairings and disconnects its phones.
- **Outposts**: an outpost's sessions reach the phone through the home, with `host` set, and `offline` while the outpost is unreachable. Only the home connects to Attn Cloud: an outpost that copies phone access from its home holds no link, and its Settings → Phones says why.
- **Restarts**: the home's keys and pairings survive a restart. A live QR code does not; show a new one. Keys attn cannot read stay in place; Settings → Phones names the file and how to start over.
- **Desktop reconnects**: Settings → Phones reads the phones again on every new daemon connection.
- **Relay deploys and network loss**: the relay drops busy sockets without a close frame. The home reconnects and every phone runs a new handshake.
- **Version skew**: a phone decodes a session state it does not know as `unknown` and skips an event it does not know.
- **Performance and idle cost**: with phone access off, nothing runs. With it on and no phone connected, the home sends one ping every 15 s.

## Tests

- Stack: `cmd/attn/phones_stack_test.go` (pair, reconnect after a cut, a second use of a code, revoke, a revoke between handshake and hello; a deleted profile's phones; unreadable keys; only the home connects; no connection while off).
- Kernel: `internal/cloudlink/noise_vectors_test.go`, the vectors the phone's initiator checks too.
- Wire: `internal/daemon/gateway_wire_test.go`.
- App: `app/src/components/PhonesSettings.pairing.test.tsx`, `app/src/gateway/decode.test.ts`, and the switch in `app/src/components/SettingsModal.test.tsx`.
- Script: `scripts/check-gateway-compat_test.sh`.
