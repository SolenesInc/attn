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
original stays available. Recording and inserting text come in later slices of
the dictation plan. This file covers setup and refinement.

## Sub-features

- `dictation-switch`: the Settings switch, setting `dictation_enabled`.
- `dictation-setup`: the model's download, verification and load, shown as one status line under the switch.
- `dictation-retry`: after a failed setup or a helper that stopped, the status names the cause and offers Retry.
- `dictation-remove`: while dictation is off, Remove model deletes the downloaded model.
- `dictation-refine`: the daemon cleans up one transcript for the requesting app and answers once.
- `dictation-refine-limit`: each refinement must answer within 15 s plus a quarter of the recording's duration, or it fails naming both and the original is kept.
- `dictation-refine-cancel`: the app cancels a pending refinement, or closes its connection, and the harness process stops.
- `dictation-refine-harness`: the `dictation.refinement_agent` setting picks Claude (the default) or Codex. Refinement uses that harness's light model and respects `headless_tasks.enabled`.

## How to get to it

### People

- **Settings → Experimental** (`app/src/components/SettingsModalSections.tsx`, `DictationSetting`): the "Dictation" switch, the status `[data-testid="settings-dictation-status"]`, `[data-testid="settings-dictation-retry"]` and `[data-testid="settings-dictation-remove"]`. Only a packaged macOS app shows the row.
- Their own shell: `attn settings set dictation_enabled true|false`, and `attn settings set dictation.refinement_agent claude|codex`, where empty restores Claude.

### Agents

- None. Agents do not read or change dictation. They receive dictated text only as text typed into their terminal.

### Triggers

- An app that starts with `dictation_enabled` on starts the helper and prepares the model (`app/src/dictation/speechHelper.ts`, `useSpeechHelperLifecycle`).
- When the app exits, it closes the helper's stdin and waits for the helper to exit (`app/src-tauri/core/src/speech.rs`, `shutdown`).
- The trusted app's `dictation_refine` and `dictation_refine_cancel` WebSocket commands, answered by `dictation_refine_result` (`internal/protocol/schema/main.tsp`, `internal/daemon/dictation.go`). No app code sends them yet. The refinement prompt is the `dictation-refiner` recipient (`internal/prompts/dictation.go`).

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
- **Profiles**: the settings are home-wide. Each named instance has its own model cache. The production app's cache is the Mac's. A refinement request names the profile the app shows and is refused for any other.
- **Harnesses**: refinement runs on Claude or Codex, the harnesses with tool-free headless tasks. Their light tier comes from model discovery, falling back to `haiku` and `gpt-6-luna`.
- **Performance and idle cost**: an idle prepared helper uses about 1 ms of CPU a minute and wakes about once every 3 seconds. macOS reclaims the model's pages while idle, so the first dictation after a long idle period takes extra time to bring them back. While a recording streams in, the helper transcribes each segment that ends in a pause. When the recording stops, the helper transcribes only the speech since the last pause. Machine load stretches that time.
- **Linux**: no app dictation. The row is hidden and the helper is not bundled. The daemon's refinement builds and runs on Linux.
- **Microphone**: setup never opens the microphone. The helper links no audio framework.

## Tests

- App: `app/src/App.dictationSetup.test.tsx`.
- Helper: `cargo test --manifest-path app/speech/Cargo.toml` (the stdio protocol before prepare) and `scripts/verify-speech-helper.py check` (download, resume, verification, dictation, cancel, crash and lease exit against the real model).
- Wire: `internal/daemon/dictation_wire_test.go` (refinement).
- Live corpus: `scripts/dictationrefineprobe`, run by hand against real harnesses.

## Gotchas

- `bridge` calls take about a second each, so times measured through `bridge` include it. Use the helper's own `load_ms` and `warm_ms` (the `check` and `idle` output) for preparation receipts, and `measure-speech-latency.py` for stop-to-text.
- The baseline instance refuses headless tasks and launches the mock agent, so refinement fails with `headless tasks are off` until the refinement preconditions are applied.
- Model output varies run to run. Run the corpus more than once before trusting a prompt change. Codex drops one copy of the passage spoken three times in about 1 of 6 to 8 runs. The probe reports that case for Codex as a known residual.
- Offline, Claude retries for about three minutes and Codex for over fifteen. Only the limit ends them.
