# Harnesses

Bring your own harness: attn wraps Claude Code and Codex as they are, and
plugins add more. Each agent runs its harness's own CLI with the user's
account, settings and conversations, and the user can run the bare CLI at any
time. People pick a harness for each agent, set each harness's executable and
default model, and install plugins. The sidebar can mark each agent with its
harness's logo.

## Sub-features

- `harness-choice`: pick the harness when starting an agent. The default comes from Settings.
- `harness-settings`: each harness's executable, default model and effort, and auto-approve, plus what it supports, in Settings.
- `harness-models`: the models each harness offers, and the light, standard and deep tiers mapped to them.
- `execution-targets`: where a Sigma conversation runs its tools, `local` or the folder's DevVM VM, chosen at launch and kept through resume. An unavailable target ends the view with its reason and never falls back to local.
- `plugins`: install a plugin from a folder or a Git repository, link a folder while developing one, set a plugin's priority, uninstall.
- `plugin-driver`: a plugin's driver starts its harness's agents and reports what they do. A run keeps its terminal or direct input route across disconnects and restarts. Capable drivers receive attributed input, accept text during approval without answering it, and compact before closing.
- `plugin-server`: an installed plugin declares a shared app-server, prepares native views and exposes models and optional targets. Open conversations retain direct input and ownership without a view.
- `plugin-authoring`: an author outside the checkout installs `@victorarias/attn-plugin`, copies `examples/plugins/sample-harness`, changes its harness ID and installs it with `attn plugin install --path`. The ID appears in the picker and launches the harness. A capability the author leaves out is unavailable.
- `harness-logos`: the Claude or Codex mark beside each agent in the sidebar, or a state dot without it.
- `shared-conversation-warning`: two tiles on one Claude Code conversation both warn until one closes.

## How to get to it

### People

- The new-agent picker's "Session agent" choice, ⌥1 to ⌥9 and ⌥T for a shell (`app/src/components/NewSessionDialog/SessionControls.tsx`). See [agent-launch.md](agent-launch.md) for the picker itself.
- **Settings → Agents and models** (section `agents`, `app/src/components/SettingsModalSections.tsx`): "Default session agent", each harness's executable ("Empty uses the executable on PATH."), default model and effort, "Auto-approve", what each harness supports, and the "Seeds on standby" block (`app/src/components/SeedsOnStandbySettings.tsx`): the choice of `garden.tender_resume` with its explainer of which mail resumes a closed agent ([garden.md](garden.md)), and the "Ring on my own PR comments" switch ([pull-requests.md](pull-requests.md)).
- **Settings → Models** (section `models`, `app/src/components/ModelTierSettings.tsx`): the tiers mapped to each harness's models.
- **Settings → Plugins** (section `plugins`): the "Plugin source" field with Browse and "Install Plugin", a card per plugin with its version, a Linked mark for a linked plugin, its runtime state and health, a Remove button, and a priority field.
- Palette `toggle-harness-logos` ("Hide harness logos in the sidebar" or "Show harness logos in the sidebar", `app/src/application/useAppCommands.tsx`), the setting `sidebar_harness_logos_enabled`.
- Where people see what agents do: the sidebar row's logo, labelled "<Harness> · <state>" (`app/src/components/SidebarRows.tsx`), and the notice above a tile whose conversation another tile also runs, "This conversation is also open in …" (`app/src/components/SharedConversationNotice.tsx`).
- Their own shell: the verbs below.

### Agents

- `attn plugin install --path <dir>`, `link --path <dir>`, `list`, `uninstall <name>` and `remove <name>` (`cmd/attnd/plugin.go`), each printing JSON.
- `attn settings get|set` for `new_session_agent`, `claude_executable`, `codex_executable` and `sidebar_harness_logos_enabled` ([settings.md](settings.md)).
- Prompts: a plugin driver that asks for launch instructions gets the same agent or chief guidance as the other harnesses (`internal/daemon/plugin_launch_instructions.go`). See [agent-guidance.md](agent-guidance.md) for what attn tells every harness.

### Triggers

- The plugin supervisor starts each installed plugin, restarts it when it fails, and stops it on uninstall (`internal/daemon/plugin_supervisor.go`). The plugin's driver launches and reports its harness's agents (`internal/daemon/plugin_driver.go`).
- attn asks each harness which models it offers (`internal/daemon/harness_models.go`).
- A protocol client lists a folder's targets with `harness_targets {harness, cwd}` and launches on one with `spawn_session`'s `execution_target` (`internal/daemon/harness_targets.go`). No app picker offers it yet.

## Driving it

Preconditions:

- Use the baseline instance with its app. Check that `bridge --launch --wait-ready ping` answers. Run `bridge dismiss_whats_new` to dismiss What's new, and stay in desktop flow, the default.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-harnesses.XXXX) && echo "export P=$P" >> $DRIVE_ENV`. Make an empty agent folder `$P/codex`.

`<P>` is the proof folder's path written out.

- **The harnesses on offer.** Press ⌘N with `bridge native_key '{"key":"n","modifiers":["command"]}'` and wait for `[data-testid="location-picker-overlay"]`. `bridge dom_text '{"selector":"[role=\"radiogroup\"][aria-label=\"Session agent\"]"}'` names Terminal, Claude and Codex with their ⌥ keys, ⌥T, ⌥1 and ⌥2. A plugin harness is listed once its plugin is installed and running. A harness the machine lacks has no key and is disabled, titled "<Harness> is not available on this machine". While the daemon checks which harnesses are installed, a harness stays selectable, titled "Checking for <Harness> on this machine", and carries the static mark `.harness-checking-mark`, named "checking". Settings marks its "Default agent" buttons the same way. With an outpost as the target, Launcher 2.0 leaves off the title and the mark.
- **Pick with a key.** Run `bridge native_key '{"key":"2","modifiers":["option"]}'`. `bridge dom_text '{"selector":"[role=\"radiogroup\"][aria-label=\"Session agent\"] [aria-checked=\"true\"]"}'` names Codex, and `./attn settings get new_session_agent` prints `codex`: a pick is saved as the default. ⌥1 checks Claude and saves `claude`. ⌥T checks Terminal and leaves the saved default alone. Press Escape.
- **The default from the shell.** Run `./attn settings set new_session_agent claude`. It prints `new_session_agent = claude`. Press ⌘N: the checked harness is Claude. Press Escape. Open Settings with ⌘, (`bridge native_key '{"key":",","modifiers":["command"]}'`) and click `[data-testid="settings-nav-agents"]` natively: `bridge dom_text '{"selector":"[aria-label=\"Default session agent\"] [aria-pressed=\"true\"]"}'` reads `Claude`. Click the Codex button in `[aria-label="Default session agent"]` natively: `new_session_agent` reads `codex`. The chosen agent moves first in the group, so read the buttons' text before clicking Claude back. Click `[aria-label="Auto-approve"]` natively: `./attn settings get auto_approve_enabled` flips, and a second click flips it back. The same section holds each harness's executable, default model and effort, and `bridge capture_screenshot` records it. Click `[data-testid="settings-nav-models"]` natively: `bridge settings_get_state` shows `activeSection: "models"`, and the Codex block lists `Mock agent 1`, which the mock reports. Click the first label in `[aria-label="Codex models"] [aria-label="Tier for Mock agent 1"]` natively: `./attn settings get model_tier_overrides` holds `"tier":"light"` for `mock-agent-1`. Click `[aria-label="Reset tier for Mock agent 1"]` natively: the value is empty again. Press Escape to close Settings before the palette steps below.
- **Harness logos.** Run `wsctl add-session --cwd $P/codex --agent codex --label harness-codex --id harness-codex --place`. `bridge dom_wait '{"selector":"[data-testid=\"sidebar-session-harness-codex\"] [role=\"img\"][aria-label^=\"Codex · \"]","timeoutMs":5000}'` matches. Open the palette (⌘⇧K, wait for `[role="combobox"][aria-label="Commands"]` to take focus), type `Hide harness logos` and press Enter. `./attn settings get sidebar_harness_logos_enabled` prints `false`, and `bridge dom_wait '{"selector":".sidebar.sidebar--hide-harness-logos","timeoutMs":5000}'` matches: the row shows its state dot where the logo was. Choose "Show harness logos in the sidebar" in the palette: the logos return and the selector goes absent.
- **A plugin from a folder.** Make a folder `$P/audit-probe` holding `attn-plugin.toml` (`name = "audit-probe"`, `version = "0.0.1"`, `attn_api_version = 7`, and `[plugin]` with `kind = "executable"` and `path = "probe.sh"`), a `package.json` of `{"name":"audit-probe","version":"0.0.1","private":true}` (install runs `bun install`, which needs one) and an executable `probe.sh` that runs `exec sleep 300`. `./attn plugin install --path $P/audit-probe` prints `"ok": true`, and `./attn plugin list` lists it. Run `./attn plugin uninstall audit-probe`. Then run `./attn plugin link --path $P/audit-probe` and `./attn plugin remove audit-probe` to do the same for a linked copy. The supervisor starts the probe and stops it on removal. Once the probe fails, the plugin shows `degraded` with the probe's last exit.
- **Plugins from Settings.** Open Settings and click `[data-testid="settings-nav-plugins"]` natively. Click `[aria-label="Plugin source"]`, type `$P/audit-probe` written out, and click `.plugin-form button.settings-action:last-child` ("Install Plugin") natively: a card for `audit-probe` appears and `./attn plugin list` agrees. Type `5` in `[aria-label="audit-probe priority"]` and press Enter: `priority` is 5. Click the card's "Remove" natively: the card goes and `./attn plugin list` no longer holds `audit-probe`. Browse opens the system file dialog, which you cannot drive on a shared Mac.
- **Plugin input.** Run `scripts/verify-plugin-run-input.sh` for daemon wire and CLI stack proof on isolated data. The checks cover arbitrary harness IDs, both voices, permission-safe delivery, compaction outcomes, old API 7 payloads, terminal-only input, disconnects, daemon restarts, both PTY runtimes, retired reports, and a database from before input routes were recorded. Human keystrokes still reach the terminal. A direct run whose driver is unavailable defers automated input.
- **A plugin author's harness.** Run `scripts/verify-plugin-author-sdk.sh`. It packs the SDK into a tarball, copies `examples/plugins/sample-harness` to a clean folder with a new harness ID, installs it with `attn plugin install --path`, starts it in the picker's place, sends annotation and `attn agent msg` input, restarts the daemon, reloads the session and checks that models, which the sample leaves out, are unavailable. The guide page is `docs/guide/reference/writing-harness-plugins.md`.
- **A shared-server plugin.** Run `scripts/verify-plugin-server.sh`. It packs SDK 0.3.0, installs `examples/plugins/sample-server` with an arbitrary ID in an isolated stack, reuses an external server, discovers models and targets, checks both voices and duplicate IDs, resolves shell tools through the Go and Rust CLIs, and restarts the plugin and daemon with live and zero-view sessions. Missing registrations defer direct input. Active owners refuse uninstall.
- **Proof.** In `$P`, keep each `./attn` command with its output and exit code, `bridge dom_text` of the "Session agent" group before and after the key, `./attn settings get new_session_agent` after it, `./attn plugin list` before and after each install, and `bridge capture_screenshot '{"path":"<P>/harness-logos.png"}'` with the logos on and `<P>/harness-plugins.png` with the Plugins section open. Name each file for its way in, such as `harnesses-settings-plugins.png`.

## Contexts

- **Durability across restarts**: installed plugins start again with the daemon, and the daemon hands a reconnecting driver its own runs. A run keeps its input route even when its plugin is absent. Older runs wait for their owning driver to register before choosing a route. Harness settings are kept.
- **Linux**: plugins run where the daemon runs.
- **Outposts**: a harness's executable is a machine setting, so each outpost decides its own. The rest of the harness settings come from the home.
- **Profiles**: harness settings and plugins apply across all profiles on the daemon.
- **Performance and idle cost**: a plugin whose socket stalls does not stall the daemon. One shared Codex app-server uses less memory than a Codex per terminal ([shared-codex.md](shared-codex.md)).
- **Harnesses**: Claude Code reports through hooks and its terminal title. Codex reports through hooks, or through its app-server under shared Codex. A plugin harness reports through its plugin's driver. YOLO and the chief toggle each show only for the harnesses that support them.
- **Keyboard and native input**: ⌥1 to ⌥9 and ⌥T pick the harness in the picker. The palette toggles the logos.
- **Compatibility across versions**: a plugin names the API it was built for in its `attn-plugin.toml` (`attn_api_version`), and a driver older than the current input rules can still report input it took.

## Tests

- Wire: `internal/daemon/plugin_server_wire_test.go`, `internal/daemon/plugin_wire_test.go`, `internal/daemon/plugins_wire_test.go`, `internal/daemon/plugins_install_wire_test.go`, `internal/daemon/plugin_driver_*wire_test.go`, `internal/daemon/plugin_run_input_wire_test.go`, `internal/daemon/plugin_restart_wire_test.go`, `internal/daemon/plugin_stop_owner_wire_test.go`, `internal/daemon/plugin_stalled_socket_wire_test.go`, `internal/daemon/plugin_launch_instructions_wire_test.go`, `internal/daemon/harness_models_claude_wire_test.go`, `internal/daemon/harness_models_plugin_wire_test.go`, `internal/daemon/agent_catalog_wire_test.go`, `internal/daemon/app_server_wire_test.go`, `internal/daemon/sigma_wire_test.go`.
- Stack: `cmd/attnd/plugin_server_stack_test.go`, `scripts/verify-plugin-server.sh`, `cmd/attnd/plugin_author_stack_test.go`, `cmd/attnd/plugin_stack_test.go`, `cmd/attnd/plugin_runtime_stack_test.go`, `cmd/attnd/plugin_restart_stack_test.go`, `internal/daemon/plugin_driver_teardown_stack_test.go`, `internal/daemon/plugin_launch_conversation_stack_test.go`, `internal/daemon/plugin_run_input_stack_test.go`, `scripts/verify-plugin-run-input.sh`.
- Scenario: `agent-settings`, `claude-shared-conversation`, `sigma` (macOS with the Sigma fork build in `ATTN_SCENARIO_SIGMA_EXECUTABLE`), `sigma-targets` (also needs `devvm` on PATH and a folder with a DevVM VM in `ATTN_SCENARIO_SIGMA_DEVVM_PROJECT`).
- Kernel: `sdk/plugin/test/*.test.ts` (run by `TestThePluginSDKPassesItsOwnTests`), `internal/plugins/*_test.go`, `internal/pty/harness_signals_test.go`, `internal/daemon/plugin_runtime_test.go`.
- App: `app/src/components/Sidebar.harnessIcons.test.tsx`, `app/src/App.modelTierSettings.test.tsx`.

## Gotchas

- Plugin cards sort by priority, so `.plugin-card` and `:nth-child` pick a different plugin after a priority change. Read the card's text before clicking its button.
- `auto_approve_enabled` and `sidebar_harness_logos_enabled` cannot be set back to empty: `""` is refused as an invalid boolean. Set `true`, their default.
- Close Settings before opening the palette: while Settings is open, the palette shortcut leaves its command field unfocused.
- The baseline's `ATTN_CLAUDE_EXECUTABLE` and `ATTN_CODEX_EXECUTABLE` win over the executables set in Settings, so changing one there changes nothing on the baseline.
- The mock stands in for `claude` and `codex` only. The shared-conversation warning needs two harness processes on one conversation, which the scenarios set up.
- `attn plugin` has no help. With no subcommand it prints its usage and exits 1, and `attn plugin --help` exits 1 with `unknown plugin command: --help`. Every subcommand prints JSON, and `uninstall` and `remove` do the same thing, `remove` adding `plugin_dir` to its reply.
- A shared-server plugin refuses uninstall while it owns any open server session, including a session with no view. Its declaration stays usable through a plugin disconnect; after a daemon restart, the session waits for the same owner to register again.
- A plugin that owns running delegated agents refuses to uninstall: `plugin "<name>" owns N active delegated run(s)`.
- `bridge location_picker_get_state` lists the YOLO and chief switches in `agents` beside the harnesses, since they share a style. Read the harness from the "Session agent" group.
- Working but wrong: `attn plugin` has no help and always answers in JSON, and its `uninstall` and `remove` are two names for one action.
