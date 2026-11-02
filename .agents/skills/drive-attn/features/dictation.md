# Dictation

Experimental and off by default, on Apple silicon Macs. Turning dictation on
downloads the pinned English speech model and starts one private
`attn-speech` helper per app. The production app keeps the model in
`~/Library/Caches/attn/speech-models`. A named instance keeps its own copy in
`<data dir>/speech-models`. Driving a named instance never touches production's
model, and `attn instance clean` removes the copy. The helper keeps the model ready while the app
runs and exits when dictation turns off or the app quits. After transcribing,
the daemon asks a harness's light model, with tools off, to fix punctuation,
fillers and explicit self-corrections without changing what was said. The
original stays available. The dictation shortcut (Control+Shift+Space by
default) starts and stops a recording at the cursor in an attn window: an input or
textarea in the main window, Quick Capture or a browser tile's page (in any of
its frames), a markdown editor, or a terminal. The app owns the microphone only
while recording.

## Sub-features

- `dictation-switch`: the Settings switch, setting `dictation_enabled`.
- `dictation-setup`: the model's download, verification and load, shown as one status line under the switch.
- `dictation-retry`: after a failed setup or a helper that stopped, the status names the cause and offers Retry.
- `dictation-remove`: while dictation is off, Remove model deletes the downloaded model.
- `dictation-refine`: the daemon cleans up one transcript for the requesting app and answers once.
- `dictation-refine-limit`: each refinement must answer within 15 s plus a quarter of the recording's duration, or it fails naming both and the original is kept.
- `dictation-refine-cancel`: the app cancels a pending refinement, or closes its connection, and the harness process stops.
- `dictation-refine-harness`: the `dictation.refinement_agent` setting picks Claude (the default) or Codex. Refinement uses that harness's light model and respects `headless_tasks.enabled`.
- `dictation-record`: the shortcut toggles a recording for the focused text field, browser page field, markdown editor or terminal; the pill shows Starting the microphone…, Listening with a clock, Transcribing… and Refining…, with Stop and Cancel while recording; Escape cancels too, before any other Escape handler. A recording keeps going when the person switches windows or leaves attn; while it records, the shortcut works in every app, so it stops from anywhere. Without a writable target the pill says why and nothing records.
- `dictation-microphone`: the first recording asks macOS for microphone access. When access is off, the pill names it and offers Open Privacy Settings. A microphone that changes or disconnects mid-recording ends the recording and transcribes what was recorded.
- `dictation-insert`: Insert at cursor (setting `dictation.interaction` = `insert_at_cursor`, the default) types the transcript where the recording started, through the field's own editing, then replaces only that text with the refined version as its own undo step. Edits elsewhere, the caret and the focused field are kept. Editing, deleting or undoing the dictated text turns the refined version into an offer card. Markdown editors do the same through their own history, and ask for markdown-aware refinement.
- `dictation-browser`: in a browser tile, the press captures the field focused in the page or in one of its frames, and the same two interactions apply there through the page's own editing. A page that navigated or replaced the frame gets the offer card instead.
- `dictation-terminal`: in a terminal, Insert at cursor waits for the refined text and pastes it once through the terminal's ordinary paste, bracketed when the program enabled bracketed paste. While it waits, the pill offers Paste original, and Enter in that terminal pastes the original instead of reaching the program. Enter anywhere else keeps its meaning. Nothing is pasted when the terminal's runtime was replaced, exited or became unreachable before the paste; the card offers the text instead. No submit key is added, and a multiline paste without bracketed paste can still run shell lines, as any paste does.
- `dictation-compare`: Compare first (`compare_first`) shows the original and the refined text with their differences, and inserts only the version chosen.
- `dictation-offer`: a card with Use original / Use refined (or Insert at cursor), Copy and Discard, shown when the field changed, was replaced or closed before the text could go in.
- `dictation-refine-failure`: a failed or timed-out refinement shows the daemon's text, which ends "Original kept", with Retry and Dismiss (in a terminal, Paste original, Retry and Discard); Keep original cancels a pending refinement.
- `dictation-shortcut`: Keyboard Shortcuts lists "Dictation · in attn windows" with Change, Reset and Turn off; setting `dictation.shortcut`, where empty turns it off.

## How to get to it

### People

- **Settings → Experimental** (`app/src/components/SettingsModalSections.tsx`, `DictationSetting`): the "Dictation" switch, the status `[data-testid="settings-dictation-status"]`, `[data-testid="settings-dictation-retry"]` and `[data-testid="settings-dictation-remove"]`. Only a packaged macOS app shows the row. While dictation is on, "When dictation finishes" picks Insert at cursor or Compare first.
- **Keyboard Shortcuts** (`app/src/components/ShortcutEditorModal.tsx`, `NativeShortcutEditor`): "Dictation · in attn windows".
- **The dictation shortcut** in any attn text field, markdown editor or terminal: press to start, press again to stop. The pill (`[data-testid="dictation-pill"]`) has Stop (`dictation-stop`), Cancel (`dictation-cancel`), Keep original (`dictation-keep-original`), Paste original (`dictation-paste-original`, terminals) and Retry (`dictation-retry`); the card (`[data-testid="dictation-card"]`) has `dictation-use-original`, `dictation-use-refined` and `dictation-discard`. Card and pill buttons never take focus from the field.
- Their own shell: `attn settings set dictation_enabled true|false`, `attn settings set dictation.refinement_agent claude|codex` (empty restores Claude), `attn settings set dictation.interaction insert_at_cursor|compare_first` and `attn settings set dictation.shortcut <chord>`.

### Agents

- None. Agents do not read or change dictation. They receive dictated text only as text typed into their terminal.

### Triggers

- An app that starts with `dictation_enabled` on starts the helper and prepares the model (`app/src/dictation/speechHelper.ts`, `useSpeechHelperLifecycle`).
- When the app exits, it closes the helper's stdin and waits for the helper to exit (`app/src-tauri/core/src/speech.rs`, `shutdown`).
- The trusted app's `dictation_refine` and `dictation_refine_cancel` WebSocket commands, answered by `dictation_refine_result` (`internal/protocol/schema/main.tsp`, `internal/daemon/dictation.go`). The main window sends them for its own fields and relays them for Quick Capture (`app/src/dictation/refiner.ts`). The refinement prompt is the `dictation-refiner` recipient (`internal/prompts/dictation.go`).
- The native recorder (`app/src-tauri/core/src/dictation.rs`): a press while idle asks the key window for its target (`dictation-request`); the window starts the recording (`dictation_start`); a press while recording stops it natively. Updates reach the owning window as the `dictation` event: listening, stopped, transcript (with its timeline), failed, canceled. When the press came from a browser tile, the main window captures and edits the page field through `browser_host_dictation` (`app/src-tauri/core/src/browser_host.rs`), which runs the browser runtime's dictation actions in the isolated world of the frame that last took focus.

## Driving it

Preconditions: the baseline instance with its app (`bridge --launch --wait-ready ping` answers). The instance's model cache is `$(./attn instance resolve --field dataDir)/speech-models`. Without it, the first step downloads about 730 MB. To skip the download, copy a cache you already have into that folder (`cp -cR <cache> "$(./attn instance resolve --field dataDir)/speech-models"`). Never point the instance at production's cache.

- **Turn it on.** Open Settings with `bridge native_key '{"key":",","modifiers":["command"]}'`, click `[data-testid="settings-nav-experimental"]`, then turn the "Dictation" switch on (or run `./attn settings set dictation_enabled true`). The status reads `Downloading Parakeet Unified English: … of 731 MB`, then `Verifying …`, `Loading the speech model…` and `Ready.`. `pgrep -P "$(cat "$(./attn instance resolve --field dataDir)/app.pid")" -x attn-speech` prints one helper, running `<app>/Contents/MacOS/attn-speech serve --cache <data dir>/speech-models`.
- **Turn it off.** Turn the "Dictation" switch off (or run `./attn settings set dictation_enabled false`). The helper exits before the status changes, and the status reads `Speech model downloaded (733 MB).` with Remove model.
- **On again from the cache.** Turn the switch on again. The status reaches `Ready.` within seconds, without a download.
- **A helper that stops.** End the helper (it is a child of this instance's app): the status names the exit, `The speech helper stopped unexpectedly (signal: 9 (SIGKILL)). Retry to start it again.`, and the app keeps answering `bridge ping`. Click Retry: the status reaches `Ready.`.
- **Quit and start with it on.** Run `./attn instance stop-app`: the helper ends with the app. Run `bridge --launch --wait-ready ping` to start the app again. The app starts a new helper, and the status reaches `Ready.` without any action.
- **Remove the model.** Turn dictation off and click Remove model: the status line disappears and the instance's `speech-models` folder is gone.
- **The helper alone.** `python3 -I scripts/verify-speech-helper.py check <app>/Contents/MacOS/attn-speech <workdir> <clips>` runs the helper's process checks: download, resume, cancel, crash, lease exit and fixture dictation. The `idle` mode of the same script measures the helper's idle CPU, wakeups and memory, and the first dictation after an idle period. `python3 -I scripts/measure-speech-latency.py dictate <helper> <cache> <clip>` replays 5, 10, 20 and 30 s recordings in real time and prints stop-to-text with the load average. The `idle` mode of `measure-speech-latency.py` times dictations after an idle period, with or without `--warm-first`.
- **Proof.** Keep each status text with the command that produced it, the helper's `ps` line, and `bridge capture_screenshot` at Ready and after a stop.

### Recording

Preconditions: the baseline instance with its app, dictation on and Ready, and a text field focused with `bridge dom_focus '{"selector":"..."}'`. A shared Mac cannot press the Carbon shortcut, so press it through `bridge hotkey_press '{"window":"main"}'`, which enters the same dispatch as the Carbon handler (the `dictation-routing` scenario proves the Carbon side on a dedicated display).

- **Repeatable audio.** `bridge dictation_clip '{"path":"<clip>.f32"}'` makes the next recordings stream a 16 kHz mono f32 clip in real time instead of opening the microphone (`ffmpeg -i in.aiff -ac 1 -ar 16000 -f f32le out.f32`); Add `"stopAtEnd":true` to stop the recording at the clip's last sample, as a person who stops right after the last word. `{"path":null}` returns to the microphone. Automation instances only.
- **One dictation.** `hotkey_press`, wait for Listening, wait the clip's length, `hotkey_press` again. The field shows the transcript, then the refined text. `bridge dictation_get_state` returns the pending dictations and the receipts: `stopToTranscriptMs` (the helper returned the text), `stopToRawInDomMs` (the window put it in the field or card), `stopToRawPaintMs` (the next painted frame), `pressToListeningMs`, the helper's `asrMs`/`finishMs`/`segments` and the recorded `audioMs`.
- **Latency.** `scripts/measure-dictation-paint.sh` runs the clips through the packaged app and prints stop-to-raw-paint beside the helper's stop-to-text and the load average.
- **The real microphone.** Without `dictation_clip`, the first recording asks macOS for access (a person clicks Allow once per bundle id). Play speech near the Mac with `say` and check that the first word survives.

### Refinement

Preconditions: the baseline instance, daemon only, and a proof folder, `P=$(mktemp -d /tmp/drive-attn-dictation.XXXX) && echo "export P=$P" >> $DRIVE_ENV`. Refinement needs real harnesses with headless tasks on, so append to the env file, then stop and start the daemon:

```bash
echo "export ATTN_CLAUDE_EXECUTABLE=$(command -v claude) ATTN_CODEX_EXECUTABLE=$(command -v codex) ATTN_HEADLESS_TASKS=on" >> $DRIVE_ENV
mkdir -p $ATTN_TOOL_HOME/.codex && cp ~/.codex/auth.json $ATTN_TOOL_HOME/.codex/
```

Codex reads its login from the scratch tool home, so copy the credential in. Claude uses the user's own login.

- **Corpus.** `go run ./scripts/dictationrefineprobe -providers claude,codex -rounds 2 -out $P/refine.jsonl` connects as the trusted app, sets `dictation.refinement_agent` for each provider and sends every corpus case: hedges, negations, identifiers, quantities, names, self-corrections, instruction-shaped text, and passages spoken twice and three times. Each line prints PASS or FAIL with the wall time, and a FAIL names the phrase that was lost, changed or kept too often. The JSONL keeps each input and output for review. `-case <id>` runs one case. The probe refuses to run without a named instance.
- **Limit.** Stop the daemon and start it with every proxy pointed at a dead port: `X=http://127.0.0.1:9; HTTPS_PROXY=$X HTTP_PROXY=$X ALL_PROXY=$X https_proxy=$X http_proxy=$X all_proxy=$X ./attn daemon ensure`. Run the corpus probe with `-case hedges` for each provider, in the background as the driving conventions describe. After 16.1 s each answers `refinement did not answer within 16.1 s for this 4.4 s recording. Original kept` with `limit_ms` 16109. While it waits, `pgrep -P $(cat $ATTN_DATA_DIR/attn.pid)` lists the harness process; after the answer, it lists none. Start the daemon again without the proxies.
- **Proof.** Keep the probe's JSONL and printed lines, and the `pgrep` listings before and after the limit.

## Contexts

- **Durability across restarts**: the helper belongs to one app process. An app restart starts a new helper from the cache. A daemon restart does not touch it. A daemon restart or a closed connection ends pending refinements. The app keeps the original and starts a new request to retry.
- **Profiles**: the settings are home-wide. Each named instance has its own model cache. The production app's cache is the Mac's. A refinement request names the profile the app shows and is refused for any other. A profile change discards that window's pending dictations; text already inserted stays in its field.
- **Harnesses**: refinement runs on Claude or Codex, the harnesses with tool-free headless tasks. Their light tier comes from model discovery, falling back to `haiku` and `gpt-6-luna`.
- **Performance and idle cost**: an idle prepared helper uses about 1 ms of CPU a minute and wakes about once every 3 seconds. macOS reclaims the model's pages while idle, so the first dictation after a long idle period takes extra time to bring them back. While a recording streams in, the helper transcribes each segment that ends in a pause. When the recording stops, the helper transcribes only the speech since the last pause. Machine load stretches that time.
- **Linux**: no app dictation. The row is hidden and the helper is not bundled. The daemon's refinement builds and runs on Linux.
- **Microphone**: setup never opens the microphone. The helper links no audio framework. The app opens the microphone at the press and closes it at stop, cancel, failure, disable and quit.
- **Windows**: the main window and Quick Capture each run their own dictation for their own fields. One recording at a time across the app; earlier dictations keep refining while a new one records.

## Tests

- App: `app/src/App.dictationSetup.test.tsx`.
- Fields: `app/e2e/dictation-fields.spec.ts` drives real controlled fields in WebKit with fake native and refinement ports (`app/test-harness/harnesses/DictationFieldsHarness.tsx`), and browser page fields through the browser runtime's dictation actions behind a fake browser host.
- Editors and terminals: `app/e2e/dictation-targets.spec.ts` drives a real markdown editor and a real terminal pane over the mock PTY, and asserts the exact bytes written (`app/test-harness/harnesses/DictationTargetsHarness.tsx`).
- Helper: `cargo test --manifest-path app/speech/Cargo.toml` (the stdio protocol before prepare) and `scripts/verify-speech-helper.py check` (download, resume, verification, dictation, cancel, crash and lease exit against the real model).
- Wire: `internal/daemon/dictation_wire_test.go` (refinement).
- Live corpus: `scripts/dictationrefineprobe`, run by hand against real harnesses.

## Gotchas

- `bridge` calls take about a second each, so times measured through `bridge` include it. Use the helper's own `load_ms` and `warm_ms` (the `check` and `idle` output) for preparation receipts, and `measure-speech-latency.py` for stop-to-text.
- The baseline instance refuses headless tasks and launches the mock agent, so refinement fails with `headless tasks are off` until the refinement preconditions are applied.
- Model output varies run to run. Run the corpus more than once before trusting a prompt change. Codex drops one copy of the passage spoken three times in about 1 of 6 to 8 runs. The probe reports that case for Codex as a known residual.
- Offline, Claude retries for about three minutes and Codex for over fifteen. Only the limit ends them.
