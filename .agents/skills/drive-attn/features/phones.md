# Phones

A paired phone sees the agents, asks and Garden of one home profile and keeps
them current. The home dials out to its relay in Attn Cloud and phones dial in, so
neither side needs an open port. Home and phone talk Noise end to end inside
the relay's frames, so the Cloud sees only ciphertext. The phone talks to the
home's gateway, an API apart from the app's `/ws` protocol that only grows, so
a phone from the App Store keeps working across home builds. Phone access is
experimental and off until the user turns it on.

The Cloud wire contract is `docs/protocol.md` in `SolenesInc/attn-cloud`. The
cloud link is `internal/cloudlink` (home keys in the data directory as
`cloud_sign_key`, `cloud_noise_key` and `cloud_home_id`), pairings are the
`phone_pairings` table, the daemon side is `internal/daemon/phones.go`, the
gateway is `internal/daemon/gateway.go`, `gateway_asks.go`, `gateway_terminal.go` and `gateway_garden.go`, the phone
app is `mobile/`, the gateway API is
`internal/protocol/schema/gateway.tsp`, and `scripts/check-gateway-compat`
refuses a gateway change that is not an addition. The phone's Noise framing is
pinned by `internal/cloudlink/testdata/noise_vectors.json`.

## Sub-features

- `phone-access-setting`: the experimental switch. While it is off, the daemon holds no connection to Attn Cloud, and the gateway refuses every hello. Turning it off closes the link and ends every phone's conversation.
- `cloud-link`: while phone access is on, the home registers once, keeps one socket to its relay with a ping every 15 s, and reconnects after a loss from 100 ms backing off to 10 s. Settings → Phones shows connected, connecting, or the last error.
- `pairing`: Settings → Phones shows a QR code per profile. Its secret works once, for 10 minutes, and the relay admits unknown devices only while a code is live. The first message the phone sends after the handshake writes the pairing.
- `revoke`: removes the pairing, sends the relay the new allowlist, and closes the phone's conversation with that profile.
- `gateway-hello`: the phone says hello with its api version. The home answers welcome, serving min(the phone's api, `GATEWAY_API_VERSION`), with the home's name and the pairing's profile, or refused with a reason.
- `gateway-sessions`: after welcome, and again whenever one changes, every session of the pairing's profile, with its state mapped to the gateway's states and its main terminal. Sessions of other profiles never reach the phone.
- `phone-terminal`: tapping a live session on the phone opens its terminal, fitted to the phone's width with rows for the screen with the keyboard closed. The phone asks for that size and becomes the size owner (`size-owner` in terminals.md); the desktop then shows the terminal scaled until someone uses it there. The home answers `terminal_screen` (the visible screen as ANSI), then output frames, coalesced every 16 ms and sent only while the phone's credit allows. A phone that falls more than 1 MiB behind gets a fresh screen instead of the backlog. The terminal attaches again after every reconnect, including when the app returns to the foreground on a new connection. A phone that reconnects while the home is starting is welcomed only after the home has its terminals back. Opening the keyboard pans the terminal so its bottom rows, where the agent asks and the cursor waits, stay above the keys bar; it never resizes the PTY. Dragging the terminal down first pans the rows the keyboard hides, then scrolls back. A program that scrolls itself, such as Claude Code's fullscreen interface, gets mouse wheel reports as the desktop's wheel sends them; otherwise the phone scrolls through the output it has received since its latest `terminal_screen`, which each attach and each fresh screen starts over. A double tap returns to the bottom.
- `phone-keys-bar`: one row under the terminal: Esc, ↑, ↓, ⏎, 1, 2, 3, Tab, ^C, ⇧Tab, led by Hide while the keyboard is open, which closes it in one tap. A key sends its bytes as the person's keystrokes and keeps the keyboard open. While the home is not connected, the session screen says so and the keys are off.
- `gateway-garden`: from api 6, after welcome and on every garden change, the open seeds of the pairing's profile without their bodies, packed into `garden_seeds` frames that fit one relay frame, then `garden` with every open seed's id. `seed_get` answers a seed's body, checks, children and the newest log entries that fit a frame. `seed_note` and `seed_move` (park, harvest, wither, replant, with text) act as the user through the desktop's note and transition paths, moving a seed an agent tends as the board does. A seed of another profile reads as absent.
- `phone-garden`: the Garden tab lists plots with their progress, then seeds growing, ready, blocked and parked. A pairing that is not online, or has not sent its garden yet, gets a line at the top naming it; its last seeds stay listed, greyed and labelled with the connection state, and the tab says "Nothing is planted." only once every pairing that serves a garden has sent one and none lists a seed. A seed's screen shows its status and tender, the move buttons its state allows, its children, its markdown body (mermaid renders; images say they open on the computer), checks, a note box and the log. A move opens a form with a required reason. After a note or a move, and whenever the home sends a changed copy of the open seed, the phone reads the seed again.
- `phone-composer`: a text field with autocorrect and dictation. Send asks the home to `submit_prompt`: the session monitor pastes the text and presses Enter when the agent is at a turn boundary. Each session keeps its own draft. The text stays until the home says it was delivered; a refusal shows the home's reason. While the home is not connected, Send is off.

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
- For the phone app in a browser at a phone's size, run `pnpm --dir mobile exec vite --port 5180` with a Vite config that resolves `./appleCrypto` to `src/test/nobleCrypto.ts`, because the app encrypts only with CryptoKit and a browser has no CryptoKit. Pair it by pasting the QR code's text in Pairings. A browser reports no keyboard, so check keyboard behavior on an iPhone.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-phones-proof.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.

- **Off by default.** `./attn settings get phone_access_enabled` prints an empty line, which reads as off, and `ss -tnp` shows no connection from the daemon's pid to the relay.
- **The person turns it on.** Press ⌘, (`bridge native_key '{"key":",","modifiers":["command"]}'`), click `[data-testid="settings-nav-experimental"]` natively, and click `[aria-label="Phone access"]` natively after `dom_native_point` scrolls it into view. `./attn settings get phone_access_enabled` prints `true`, and the daemon holds one connection to the relay. From the person's shell, `./attn settings set phone_access_enabled true` does the same.
- **Pairing and revoke.** Click `[data-testid="settings-nav-phones"]` natively, then "Pair a phone" in the profile's section. A phone is the test device in `internal/testworld/phone.go`. The stack test drives it end to end: `./scripts/test-go.sh --packages -v -run 'Phone|TestDeletingAProfile|TestOnlyTheHome|TestUnreadableCloudKeys' ./cmd/attn`, against a Go stand-in for the relay, or against a real relay with `ATTN_TEST_CLOUD_URL=http://127.0.0.1:<port>`.
- **The gateway.** `./scripts/test-go.sh --packages -v -run 'TestGateway|TestAPhone' ./internal/daemon`.
- **The phone terminal.** Spawn a Claude Code session in the paired profile and tap it in the phone app. The terminal shows the agent at the phone's width, and `attn debug daemon-log --grep pty_resize` shows a resize to the phone's size. Answer a numbered menu with the keys bar, and send a prompt from the composer. After a long reply, swipe down on the terminal and read text from well above the screen; a double tap returns to the bottom. In the desktop app, the tile shows the terminal scaled; clicking it takes the size back.
- **The phone Garden.** Plant a plot in the paired profile with `./attn seed plot -f <file> --profile <id>`, a markdown body with a mermaid block and two children where one waits for the other. In the phone app, open Garden: the plot lists with its progress. Open the plot, then a child; add a note and park the child with a reason. `./attn seed notes <child>` shows the note by the user and the park's comment, and `./attn seed show <child>` shows it parked.
- **Proof.** In `$P`, keep each `./attn settings` command's output, the `ss` lines, the test output, and `bridge capture_native_window_screenshot '{"path":"<P>/phones.png"}'` of Settings → Phones, `<P>` written out.

## Contexts

- **Profiles**: a pairing reaches only its profile. The profile comes from the pairing, never from the phone's hello. A phone pairs with each profile separately, with one QR code each. Deleting a profile removes its pairings and disconnects its phones.
- **Outposts**: an outpost's sessions reach the phone through the home, with `host` set, and `offline` while the outpost is unreachable. A phone terminal of an outpost session survives the outpost going away: the phone says the terminal is lost until the outpost answers again, and then gets a fresh screen without reopening it. Only the home connects to Attn Cloud: an outpost that copies phone access from its home holds no link, and its Settings → Phones says why.
- **Restarts**: the home's keys and pairings survive a restart. A live QR code does not; show a new one. Keys attn cannot read stay in place; Settings → Phones names the file and how to start over.
- **Desktop reconnects**: Settings → Phones reads the phones again on every new daemon connection.
- **Relay deploys and network loss**: the relay drops busy sockets without a close frame. The home reconnects and every phone runs a new handshake.
- **Version skew**: a phone decodes a session state it does not know as `unknown` and skips an event it does not know. A home older than gateway api 4 cannot open terminals; the phone says so instead of showing one. From api 5 a phone asks for an ask's HTML image by its source with every character reference decoded once, and the home reads exactly that image. A home before api 6 sends no garden; the phone's Garden tab names it as too old. A phone before api 6 gets no garden frames. A phone before api 5 leaves named references other than `&amp;`, `&lt;`, `&gt;`, `&quot;` and `&apos;` undecoded; the home reads the decoded source when only that one is an image of the ask, and answers with an error naming both images when the source and its decoding each are.
- **Several clients on one terminal**: the phone, the desktop and other phones each ask for their size; the last one the person used owns it, as `size-owner` in terminals.md describes. A phone that disconnects or closes the terminal hands the size on.
- **Performance and idle cost**: with phone access off, nothing runs. With it on and no phone connected, the home sends one ping every 15 s. An open phone terminal that is quiet runs no timer on the home.

## Tests

- Stack: `cmd/attn/phones_stack_test.go` (pair, reconnect after a cut, a second use of a code, revoke, a revoke between handshake and hello; a deleted profile's phones; unreadable keys; only the home connects; no connection while off; a phone that reconnects while the home starts; an outpost terminal that comes back after its outpost drops).
- Kernel: `internal/cloudlink/noise_vectors_test.go`, the vectors the phone's initiator checks too.
- Wire: `internal/daemon/gateway_wire_test.go`, `internal/daemon/gateway_terminal_wire_test.go` (screen then output, focus ownership and hand-back, submit_prompt, output within credit during a flood).
- Wire: `internal/daemon/gateway_garden_wire_test.go` (listing, detail, note, park and harvest, profile scope, a phone before api 6).
- Phone: `mobile/src/main.scene.test.ts` (queue, session screen, composer), `mobile/src/terminal/scroll.test.ts` (what a drag does: pan, scrollback rows, wheel lines), `mobile/src/garden/garden.scene.test.ts` (Garden sections, seed screen, note, move, unreachable and reconnecting homes), `mobile/src/link.test.ts` (heartbeat, terminal attach and re-attach, the garden window).
- App: `app/src/components/PhonesSettings.pairing.test.tsx`, `app/src/gateway/decode.test.ts`, and the switch in `app/src/components/SettingsModal.test.tsx`.
- Script: `scripts/check-gateway-compat_test.sh`.
