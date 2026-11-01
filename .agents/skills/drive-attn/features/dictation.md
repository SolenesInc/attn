# Dictation

Experimental and off by default, on Apple silicon Macs. Turning dictation on
downloads the pinned English speech model into a cache shared by every attn on
the Mac, `~/Library/Caches/attn/speech-models`, and starts one private
`attn-speech` helper per app. The helper keeps the model ready while the app
runs and exits when dictation turns off or the app quits. Recording and
inserting text arrive in later slices of the dictation plan; this file covers
setup.

## Sub-features

- `dictation-switch`: the Settings switch, setting `dictation_enabled`.
- `dictation-setup`: the model's download, verification and load, shown as one status line under the switch.
- `dictation-retry`: a failed setup or a helper that stopped names the cause and offers Retry.
- `dictation-remove`: while dictation is off, Remove model deletes the downloaded model.

## How to get to it

### People

- Settings, Experimental (`app/src/components/SettingsModalSections.tsx`, `DictationSetting`): the "Dictation" switch, the status `[data-testid="settings-dictation-status"]`, `[data-testid="settings-dictation-retry"]` and `[data-testid="settings-dictation-remove"]`. Only a packaged macOS app shows the row.
- Their own shell: `attn settings set dictation_enabled true|false`.

### Agents

- None. Agents do not read or change dictation.

### Triggers

- An app that starts with `dictation_enabled` on starts the helper and prepares the model (`app/src/dictation/speechHelper.ts`, `useSpeechHelperLifecycle`).
- App exit closes the helper's stdin and waits for it to exit (`app/src-tauri/core/src/speech.rs`, `shutdown`).

## Driving it

Preconditions: the baseline instance with its app (`bridge --launch --wait-ready ping` answers). A first run downloads about 730 MB. Note what `~/Library/Caches/attn/speech-models` holds before the run, since Remove model deletes it for every attn on the Mac.

- **Turn it on.** Open Settings with `bridge native_key '{"key":",","modifiers":["command"]}'`, click `[data-testid="settings-nav-experimental"]`, then turn the "Dictation" switch on (or run `./attn settings set dictation_enabled true`). The status reads `Downloading Parakeet Unified English: … of 731 MB`, then `Verifying …`, `Loading the speech model…` and `Ready.`. `pgrep -P "$(cat "$(./attn instance resolve --field dataDir)/app.pid")" -x attn-speech` prints one helper, running `<app>/Contents/MacOS/attn-speech serve --cache ~/Library/Caches/attn/speech-models`.
- **Turn it off.** The helper exits, and the status reads `Speech model downloaded (733 MB).` with Remove model.
- **On again from the cache.** The status reaches `Ready.` within seconds, without a download.
- **A helper that stops.** End the helper (it is a child of this instance's app): the status names the exit, `The speech helper stopped unexpectedly (signal: 9 (SIGKILL)). Retry to start it again.`, and the app keeps answering `bridge ping`. Retry reaches `Ready.`.
- **Quit and start with it on.** `./attn instance stop-app` ends the helper with the app. `bridge --launch --wait-ready ping` starts a new helper that reaches `Ready.` without any action.
- **Remove the model.** Turn dictation off and click Remove model: the status line goes and the cache folder is gone.
- **The helper alone.** `python3 -I scripts/verify-speech-helper.py check <app>/Contents/MacOS/attn-speech <workdir> <clips>` runs the helper's process checks: download, resume, cancel, crash, lease exit and fixture transcription. `idle` measures the helper's idle CPU, wakeups and memory, and the first request after an idle period.
- **Proof.** Keep each status text with the command that produced it, the helper's `ps` line, and `bridge capture_screenshot` at Ready and after a stop.

## Contexts

- **Durability across restarts**: the helper belongs to one app process. An app restart starts a new helper from the cache. A daemon restart does not touch it.
- **Profiles**: the setting is home-wide, and the model cache is shared by every instance on the Mac.
- **Performance and idle cost**: an idle prepared helper uses about 1 ms of CPU a minute and wakes about once every 3 seconds. macOS reclaims the model's pages while idle, so the first request after a long idle pays to bring them back.
- **Linux**: no app dictation. The row is hidden and the helper is not bundled.
- **Microphone**: setup never opens the microphone. The helper links no audio framework.

## Tests

- App: `app/src/App.dictationSetup.test.tsx`.
- Helper: `cargo test --manifest-path app/speech/Cargo.toml` (cache download, resume and verification, audio decoding, and the stdio protocol).

## Gotchas

- `bridge` calls take about a second each, so times measured through `bridge` include it. Use the helper's own `load_ms` and `warm_ms` (the `check` and `idle` output) for preparation receipts.
- Remove model deletes the shared cache that other attn instances on the Mac also use.
