# Phones

A paired phone sees the agents of one home profile and keeps that list
current. The phone talks to the home's gateway, an API apart from the app's
`/ws` protocol that only grows, so a phone from the App Store keeps working
across home builds. Phone access is experimental and off until the user turns
it on. Today the gateway has no transport: no phone can reach it yet, and only
the daemon wire tests drive it. The gateway API lives in
`internal/protocol/schema/gateway.tsp`, the gateway in
`internal/daemon/gateway.go`, the phone's decoder in `app/src/gateway/decode.ts`,
and `scripts/check-gateway-compat` refuses a change that is not an addition.

## Sub-features

- `phone-access-setting`: the experimental switch. While it is off, the gateway refuses every hello, and turning it off ends every connected phone's conversation with a refusal.
- `gateway-hello`: the phone says hello with its api version. The home answers welcome, serving min(the phone's api, `GATEWAY_API_VERSION`), with the home's name and the pairing's profile, or refused with a reason.
- `gateway-sessions`: after welcome, and again whenever one changes, every session of the pairing's profile, with its state mapped to the gateway's states. Sessions of other profiles never reach the phone.

## How to get to it

### People

- **Settings → Experimental**, "Phone access" (`app/src/components/SettingsModalSections.tsx`), the home setting `phone_access_enabled`.
- Their own shell: `attn settings set phone_access_enabled true`.

### Agents

- None. The gateway serves phones, not agents.

## Driving it

Preconditions:

- Use the baseline instance with its app.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-phones-proof.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.

- **Off by default.** `./attn settings get phone_access_enabled` prints an empty line, which reads as off.
- **The person turns it on.** Press ⌘, (`bridge native_key '{"key":",","modifiers":["command"]}'`), click `[data-testid="settings-nav-experimental"]` natively, and click `[aria-label="Phone access"]` natively after `dom_native_point` scrolls it into view. `./attn settings get phone_access_enabled` prints `true`. Click it again to turn it off. From the person's shell, `./attn settings set phone_access_enabled true` does the same.
- **The gateway.** It has no live way in until the cloud link lands. Run its wire tests: `./scripts/test-go.sh --packages -v -run TestGateway ./internal/daemon`.
- **Proof.** In `$P`, keep each `./attn settings` command's output, the wire test output, and `bridge capture_native_window_screenshot '{"path":"<P>/phones-switch.png"}'` with the switch on, `<P>` written out.

## Contexts

- **Profiles**: a pairing reaches only its profile. The profile comes from the pairing, never from the phone's hello.
- **Outposts**: an outpost's sessions reach the phone through the home, with `host` set, and `offline` while the outpost is unreachable.
- **Version skew**: a phone decodes a session state it does not know as `unknown` and skips an event it does not know.
- **Performance and idle cost**: with no phone connected, the gateway does no work. A connected phone rebuilds its list on session facts and sends it only when it changed.

## Tests

- Wire: `internal/daemon/gateway_wire_test.go`.
- App: `app/src/gateway/decode.test.ts`, and the switch in `app/src/components/SettingsModal.test.tsx`.
- Script: `scripts/check-gateway-compat_test.sh`.
