# Phones

A paired phone sees the agents, asks and Garden of one home profile and keeps
them current. The home dials out to its relay in Attn Cloud and phones dial in, so
neither side needs an open port. Home and phone talk Noise end to end inside
the relay's frames, so the Cloud sees only ciphertext. The phone talks to the
home's gateway, an API apart from the app's `/ws` protocol that only grows, so
a phone from the App Store keeps working across home builds. Phone access is
experimental and off until the user turns it on.

The Cloud Relay contract is `docs/internal/relay-contract.md`, and
`internal/cloudlink/relaytest` holds any relay to it. The
cloud link is `internal/cloudlink` (home keys in the data directory as
`cloud_sign_key`, `cloud_noise_key` and `cloud_home_id`), pairings are the
`phone_pairings` table, the daemon side is `internal/daemon/phones.go`, the
gateway is `internal/daemon/gateway.go`, `gateway_asks.go`, `gateway_terminal.go` and `gateway_garden.go`, the phone
app is `mobile/`, the gateway API is
`internal/protocol/schema/gateway.tsp`, and `scripts/check-gateway-compat`
refuses a gateway change that is not an addition. The phone's Noise framing is
pinned by `internal/cloudlink/testdata/noise_vectors.json`, and the relay frames
and signatures of both clients by `internal/cloudlink/testdata/relay_vectors.json`.

## Sub-features

- `phone-access-setting`: the experimental switch. While it is off, the daemon holds no connection to Attn Cloud, and the gateway refuses every hello. Turning it off closes the link and ends every phone's conversation.
- `cloud-link`: while phone access is on, the home registers once, again if its relay was deleted, keeps one socket to its relay with a ping every 15 s, and reconnects after a loss from 100 ms backing off to 10 s. Settings → Phones shows connected, connecting, or the last error, such as the relay's limit on new relays in a day.
- `pairing`: Settings → Phones shows a QR code per profile. Its secret works once, for 10 minutes. The relay admits an unknown device only with the relay token derived from a live code's secret, sent in the device's `auth` frame while it pairs, and the home hears nothing of any other device. The first message the phone sends after the handshake writes the pairing.
- `pairing-window`: a code's window ends when it is used, in the same allowlist frame that adds the device, when the person clicks Cancel or leaves Settings → Phones, when the app that showed it quits or loses its daemon connection, when a new code replaces it, when its profile is deleted, and when it expires. Each sends the relay a new allowlist, so a socket from the lapsed window closes with `4403`. Each profile's window closes alone, and a client that goes away ends only the codes it still holds, never a code another client replaced it with or a pairing already made.
- `revoke`: removes the pairing, sends the relay the new allowlist, and closes the phone's conversation with that profile.
- `gateway-hello`: the phone says hello with its api version. The home answers welcome, serving min(the phone's api, `GATEWAY_API_VERSION`), with the home's name and the pairing's profile, or refused with a reason.
- `gateway-sessions`: after welcome, and again whenever one changes, every session of the pairing's profile, with its state mapped to the gateway's states and its main terminal. Sessions of other profiles never reach the phone.
- `phone-terminal`: tapping a live session on the phone opens its terminal, fitted to the phone's width with rows for the screen with the keyboard closed. The phone asks for that size and becomes the size owner (`size-owner` in terminals.md); the desktop then shows the terminal scaled until someone uses it there. The home answers `terminal_screen` (the visible screen as ANSI), then output frames, coalesced every 16 ms and sent only while the phone's credit allows. A phone that falls more than 1 MiB behind gets a fresh screen instead of the backlog. The terminal attaches again after every reconnect, including when the app returns to the foreground on a new connection. A phone that reconnects while the home is starting is welcomed only after the home has its terminals back. Opening the keyboard pans the terminal so its bottom rows, where the agent asks and the cursor waits, stay above the keys bar; it never resizes the PTY. Dragging the terminal down first pans the rows the keyboard hides, then scrolls back. A program that scrolls itself, such as Claude Code's fullscreen interface, gets mouse wheel reports as the desktop's wheel sends them; otherwise the phone scrolls through the output it has received since its latest `terminal_screen`, which each attach and each fresh screen starts over. A double tap returns to the latest output; a program that still tracks the mouse gets as many wheel-down reports as the phone sent it net wheel-up.
- `phone-keys-bar`: one row under the terminal: Esc, ↑, ↓, ⏎, 1, 2, 3, Tab, ^C, ⇧Tab, led by Hide while the keyboard is open, which closes it in one tap. A tap on the terminal closes it too; a drag scrolls instead. A key sends its bytes as the person's keystrokes and keeps the keyboard open. While the home is not connected, the session screen says so and the keys are off.
- `gateway-garden`: from api 6, after welcome and on every garden change, the open seeds of the pairing's profile without their bodies, packed into `garden_seeds` frames that fit one relay frame, then `garden` with every open seed's id. `seed_get` answers a seed's body, checks, children and the newest log entries that fit a frame. From api 7 the home sends `seed_changed` with a seed's id whenever a `garden.seed.*` fact names a seed of the pairing's profile, so a change that leaves the seed's summary as it was, such as a note or a body edit, still reaches the phone. `seed_note` and `seed_move` (park, harvest, wither, replant, with text) act as the user through the desktop's note and transition paths, moving a seed an agent tends as the board does. A seed of another profile reads as absent.
- `phone-garden`: the Garden tab lists plots with their progress, then seeds growing, ready, blocked and parked. A pairing that is not online, or has not sent its garden yet, gets a line at the top naming it; its last seeds stay listed, greyed and labelled with the connection state, and the tab says "Nothing is planted." only once every pairing that serves a garden has sent one and none lists a seed. A seed's screen shows its status and tender, the move buttons its state allows, its children, its markdown body (mermaid renders; images say they open on the computer), checks, a note box and the log. A move opens a form with a required reason. After a note or a move, whenever the home sends a changed copy of the open seed or `seed_changed` for it, and on every welcome from its home, the phone reads the open seed again.
- `phone-push`: a sealed notification through the relay when an agent's stop opens a turn in the waiting state, when it needs an approval, and when an ask opens. A waiting stop that a notification started sends nothing in any flow. Such a stop pushes, also when idle, for a turn the agent opened with `attn turn open`, once per turn. A stop whose run failed pushes in any flow as `run_failed`, with no text, and the phone shows "run failed". While the link is down, a push waits for the next connect. A push older than 2 minutes is dropped, with the reason in the daemon log, and one wait pushes once per daemon run.
- `phone-composer`: a text field with autocorrect and dictation. Send asks the home to `submit_prompt`: the session monitor pastes the text and presses Enter when the agent is at a turn boundary. Each session keeps its own draft. The text stays until the home says it was delivered; a refusal shows the home's reason. While the home is not connected, Send is off.

## How to get to it

### People

- **Settings → Experimental** and the top of **Settings → Phones**, "Phone access" (`app/src/components/SettingsModalSections.tsx`), the home setting `phone_access_enabled`.
- **Settings → Phones** (`app/src/components/PhonesSettings.tsx`), listed under Connectivity, dimmed and marked off until phone access is on: the link, "Pair a phone" per profile with Cancel beside a shown code, the paired phones with Revoke.
- Their own shell: `attn settings set phone_access_enabled true`.

### Agents

- None. Phones are paired and revoked by the person.

## Driving it

Preconditions:

- A relay of your own that passes the contract. Add `export ATTN_CLOUD_URL=http://127.0.0.1:<port>` to `$DRIVE_ENV` before starting the daemon. Without `ATTN_CLOUD_URL` the daemon dials `https://cloud.attn.codes`.
- Use the baseline instance with its app, or the daemon alone for the link and the protocol.
- For the phone app in a browser at a phone's size, run `pnpm --dir mobile exec vite --port 5180` with a Vite config that resolves `./appleCrypto` to `src/test/nobleCrypto.ts`, because the app encrypts only with CryptoKit and a browser has no CryptoKit. Pair it by pasting the QR code's text in Pairings. A browser reports no keyboard, so check keyboard behavior on an iPhone.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-phones-proof.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.

- **Off by default.** `./attn settings get phone_access_enabled` prints an empty line, which reads as off, and `ss -tnp` shows no connection from the daemon's pid to the relay.
- **The person turns it on.** Press ⌘, (`bridge native_key '{"key":",","modifiers":["command"]}'`), click `[data-testid="settings-nav-experimental"]` natively, and click `[aria-label="Phone access"]` natively after `dom_native_point` scrolls it into view. `./attn settings get phone_access_enabled` prints `true`, and the daemon holds one connection to the relay. From the person's shell, `./attn settings set phone_access_enabled true` does the same.
- **Pairing and revoke.** Click `[data-testid="settings-nav-phones"]` natively, then "Pair a phone" in the profile's section. Click Cancel beside the code to end it, or leave the page, which does the same. To prove the relay token with a real phone, revoke it first, because the relay does not check the token of a device on the allowlist. A phone is the test device in `internal/testworld/phone.go`. The stack test drives it end to end: `./scripts/test-go.sh --packages -v -run 'Phone|Pairing|TestEachProfile|TestDeletingAProfile|TestOnlyTheHome|TestUnreadableCloudKeys' ./cmd/attn`, against a Go stand-in for the relay, or against a real relay with `ATTN_TEST_CLOUD_URL=http://127.0.0.1:<port>`.
- **The gateway.** `./scripts/test-go.sh --packages -v -run 'TestGateway|TestAPhone' ./internal/daemon`.
- **The phone terminal.** Spawn a Claude Code session in the paired profile and tap it in the phone app. The terminal shows the agent at the phone's width, and `attn debug daemon-log --grep pty_resize` shows a resize to the phone's size. Answer a numbered menu with the keys bar, and send a prompt from the composer. After a long reply, swipe down on the terminal and read text from well above the screen; a double tap returns to the bottom. In the desktop app, the tile shows the terminal scaled; clicking it takes the size back.
- **The phone Garden.** Plant a plot in the paired profile with `./attn seed plot -f <file> --profile <id>`, a markdown body with a mermaid block and two children where one waits for the other. In the phone app, open Garden: the plot lists with its progress. Open the plot, then a child; add a note and park the child with a reason. `./attn seed notes <child>` shows the note by the user and the park's comment, and `./attn seed show <child>` shows it parked.
- **Proof.** In `$P`, keep each `./attn settings` command's output, the `ss` lines, the test output, and `bridge capture_native_window_screenshot '{"path":"<P>/phones.png"}'` of Settings → Phones, `<P>` written out.

## Contexts

- **Profiles**: a pairing reaches only its profile. The profile comes from the pairing, never from the phone's hello. A phone pairs with each profile separately, with one QR code each. Deleting a profile removes its pairings and disconnects its phones.
- **Outposts**: an outpost's sessions reach the phone through the home, with `host` set, and `offline` while the outpost is unreachable. A phone terminal of an outpost session survives the outpost going away: the phone says the terminal is lost until the outpost answers again, and then gets a fresh screen without reopening it. Only the home connects to Attn Cloud: an outpost that copies phone access from its home holds no link, and its Settings → Phones says why.
- **Restarts**: the home's keys and pairings survive a restart. A live QR code does not; show a new one. Keys attn cannot read stay in place; Settings → Phones names the file and how to start over.
- **Desktop reconnects**: Settings → Phones reads the phones again on every new daemon connection. A code shown before the connection dropped has ended, so the person shows a new one.
- **Relay deploys and network loss**: the relay drops busy sockets without a close frame. The home reconnects, a waiting push goes out, and every phone runs a new handshake.
- **A deleted relay**: a relay with no home for 180 days deletes itself and its push tokens. The home registers again by itself. A phone registers its token again the next time it connects to the home, so until the person opens the app with the home online, the phone's pushes fail, and only the daemon log shows it.
- **Version skew**: a phone from before relay tokens that scans a code gets `4403` from the relay and shows its old "no longer allows this phone" line, because it does not read the close reason; a phone already paired keeps working. A phone decodes a session state it does not know as `unknown` and skips an event it does not know. A home older than gateway api 4 cannot open terminals; the phone says so instead of showing one. From api 5 a phone asks for an ask's HTML image by its source with every character reference decoded once, and the home reads exactly that image. A home before api 6 sends no garden; the phone's Garden tab names it as too old. A phone before api 6 gets no garden frames. A phone before api 7 gets no `seed_changed`; it still rereads the open seed on welcome. A phone before api 5 leaves named references other than `&amp;`, `&lt;`, `&gt;`, `&quot;` and `&apos;` undecoded; the home reads the decoded source when only that one is an image of the ask, and answers with an error naming both images when the source and its decoding each are.
- **Several clients on one terminal**: the phone, the desktop and other phones each ask for their size; the last one the person used owns it, as `size-owner` in terminals.md describes. A phone that disconnects or closes the terminal hands the size on.
- **Performance and idle cost**: with phone access off, nothing runs. With it on and no phone connected, the home sends one ping every 15 s. An open phone terminal that is quiet runs no timer on the home.

## Tests

- Stack: `cmd/attn/phones_stack_test.go` (pair, reconnect after a cut, a second use of a code, revoke, a revoke between handshake and hello; a deleted profile's phones; unreadable keys; only the home connects; no connection while off; a phone that reconnects while the home starts; an outpost terminal that comes back after its outpost drops).
- Stack: `cmd/attn/phone_pairing_window_stack_test.go` (a window closed by cancel, expiry, a new code, use, a deleted profile and the app quitting; two profiles and two clients at once; a phone with no token). `ATTN_PAIRING_LIFETIME` shortens a code's life in a test.
- Stack: `cmd/attn/phone_push_stack_test.go` (pushes reach every phone of the session's profile, each approval pushes, a push during an outage: sent when the link returns, dropped past the limit, dropped when answered, kept through a killed daemon).
- Wire: `internal/daemon/phone_push_wire_test.go` (which stops push, in each flow).
- Kernel: `internal/cloudlink/noise_vectors_test.go`, the vectors the phone's initiator checks too.
- Conformance: `internal/cloudlink/relaytest` drives a relay through the contract: the stand-in in `internal/testworld/standin_relay_contract_test.go`, any other with `ATTN_RELAY_URL=<url> go test ./internal/cloudlink/relaytest`.
- Kernel: `internal/cloudlink/relay_vectors_test.go` and `mobile/src/relay.vectors.test.ts`, the home, the phone and the phone's stand-in against one set of relay frames.
- Wire: `internal/daemon/gateway_old_phone_wire_test.go` replays the frozen conversation of a phone of every gateway api in `internal/daemon/testdata/old_phones`. A new gateway api fails it until recorded.
- Wire: `internal/daemon/gateway_wire_test.go`, `internal/daemon/gateway_terminal_wire_test.go` (screen then output, focus ownership and hand-back, submit_prompt, output within credit during a flood).
- Wire: `internal/daemon/gateway_garden_wire_test.go` (listing, detail, note, park and harvest, profile scope, a phone before api 6, `seed_changed` on another actor's note and body edit).
- Phone: `mobile/src/main.scene.test.ts` (queue, session screen, composer, closing the keyboard), `mobile/src/terminal/scroll.test.ts` (what a drag and a double tap do: pan, scrollback rows, wheel lines), `mobile/src/garden/garden.scene.test.ts` (Garden sections, seed screen, note, move, unreachable and reconnecting homes, an open seed refreshed live and after a reconnect), `mobile/src/link.test.ts` (heartbeat, terminal attach and re-attach, the garden window).
- App: `app/src/components/PhonesSettings.pairing.test.tsx`, `app/src/gateway/decode.test.ts`, and the switch in `app/src/components/SettingsModal.test.tsx`.
- Script: `scripts/check-gateway-compat_test.sh`, `scripts/check-relay-privacy_test.sh`.
