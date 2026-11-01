# Dictation

Experimental and off by default, on Apple silicon Macs. Turning dictation on
downloads the pinned English speech model and starts one private
`attn-speech` helper per app. The production app keeps the model in
`~/Library/Caches/attn/speech-models`. A named instance keeps its own copy in
`<data dir>/speech-models`. Driving a named instance never touches production's
model, and `attn instance clean` removes the copy. The helper keeps the model ready while the app
runs and exits when dictation turns off or the app quits. Recording and
inserting text come in later slices of the dictation plan. This file covers
setup.

## Sub-features

- `dictation-switch`: the Settings switch, setting `dictation_enabled`.
- `dictation-setup`: the model's download, verification and load, shown as one status line under the switch.
- `dictation-retry`: after a failed setup or a helper that stopped, the status names the cause and offers Retry.
- `dictation-remove`: while dictation is off, Remove model deletes the downloaded model.

## How to get to it

### People

- **Settings → Experimental** (`app/src/components/SettingsModalSections.tsx`, `DictationSetting`): the "Dictation" switch, the status `[data-testid="settings-dictation-status"]`, `[data-testid="settings-dictation-retry"]` and `[data-testid="settings-dictation-remove"]`. Only a packaged macOS app shows the row.
- Their own shell: `attn settings set dictation_enabled true|false`.

### Agents

- None. Agents do not read or change dictation.

### Triggers

- An app that starts with `dictation_enabled` on starts the helper and prepares the model (`app/src/dictation/speechHelper.ts`, `useSpeechHelperLifecycle`).
- When the app exits, it closes the helper's stdin and waits for the helper to exit (`app/src-tauri/core/src/speech.rs`, `shutdown`).

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

## Contexts

- **Durability across restarts**: the helper belongs to one app process. An app restart starts a new helper from the cache. A daemon restart does not touch it.
- **Profiles**: the setting is home-wide. Each named instance has its own model cache. The production app's cache is the Mac's.
- **Performance and idle cost**: an idle prepared helper uses about 1 ms of CPU a minute and wakes about once every 3 seconds. macOS reclaims the model's pages while idle, so the first dictation after a long idle period takes extra time to bring them back. While a recording streams in, the helper transcribes each segment that ends in a pause. When the recording stops, the helper transcribes only the speech since the last pause. Machine load stretches that time.
- **Linux**: no app dictation. The row is hidden and the helper is not bundled.
- **Microphone**: setup never opens the microphone. The helper links no audio framework.

## Tests

- App: `app/src/App.dictationSetup.test.tsx`.
- Helper: `cargo test --manifest-path app/speech/Cargo.toml` (the stdio protocol before prepare) and `scripts/verify-speech-helper.py check` (download, resume, verification, dictation, cancel, crash and lease exit against the real model).

## Gotchas

- `bridge` calls take about a second each, so times measured through `bridge` include it. Use the helper's own `load_ms` and `warm_ms` (the `check` and `idle` output) for preparation receipts, and `measure-speech-latency.py` for stop-to-text.
