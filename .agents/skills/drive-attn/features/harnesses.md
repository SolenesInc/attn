# Harnesses

Bring your own harness: attn wraps Claude Code, Codex and Pi as they are, and
plugins add more. Each agent runs its harness's own CLI with the user's
account, settings and conversations, and the user can run the bare CLI at any
time. People pick a harness for each agent, set each one's executable and
default model, and install plugins; Pi itself comes as the bundled `attn-pi`
plugin. The sidebar can mark each agent with its harness's logo.

## Sub-features

- `harness-choice`: pick the harness when starting an agent; the default comes from Settings.
- `harness-settings`: each harness's executable, default model and effort, and auto-approve, plus what it supports, in Settings.
- `harness-models`: the models each harness offers, and the light, standard and deep tiers mapped to them.
- `plugins`: install a plugin from a folder or a Git repository, link a folder while developing one, install the bundled `attn-pi`, set a plugin's priority, uninstall.
- `plugin-driver`: a plugin's driver starts its harness's agents and reports what they do.
- `harness-logos`: the Claude, Codex or Pi mark beside each agent in the sidebar, or a state dot without it.
- `shared-conversation-warning`: two tiles on one Claude Code or Pi conversation both warn until one closes.

## How to get to it

### People

- The new-agent picker's "Session agent" choice, ⌥1 to ⌥9 and ⌥T for a shell (`app/src/components/NewSessionDialog/SessionControls.tsx`); the picker itself is [agent-launch.md](agent-launch.md).
- Settings, Agents and models (section `agents`, `app/src/components/SettingsModalSections.tsx`): "Default session agent", each harness's executable ("Empty uses the executable on PATH."), default model and effort, "Auto-approve", and what each harness supports.
- Settings, Models (section `models`, `app/src/components/ModelTierSettings.tsx`): the tiers mapped to each harness's models.
- Settings, Plugins (section `plugins`): the "Plugin source" field with Browse and "Install Plugin", a card per plugin with its version, Bundled, Installed or Available, its runtime state and health, an Install button for a bundled one, and a priority field.
- Palette `toggle-harness-logos` ("Hide harness logos in the sidebar" or "Show harness logos in the sidebar", `app/src/application/useAppCommands.tsx`), the setting `sidebar_harness_logos_enabled`.
- Where people see what agents do: the sidebar row's logo, labelled "<Harness> · <state>" (`app/src/components/SidebarRows.tsx`), and the notice above a tile whose conversation another tile also runs, "This conversation is also open in …" (`app/src/components/SharedConversationNotice.tsx`).
- Their own shell: the verbs below.

### Agents

- `attn plugin install --path <dir>`, `link --path <dir>`, `install-bundled <name>`, `list`, `uninstall <name>` and `remove <name>` (`cmd/attn/plugin.go`), each printing JSON.
- `attn settings get|set` for `new_session_agent`, `claude_executable`, `codex_executable` and `sidebar_harness_logos_enabled` ([settings.md](settings.md)).
- Prompts: a plugin driver that asks for launch instructions gets the same agent or chief guidance as the other harnesses (`internal/daemon/plugin_launch_instructions.go`); what attn tells every harness is [agent-guidance.md](agent-guidance.md), and Pi's sandbox and Guardian prompts are [auto-mode.md](auto-mode.md).

### Triggers

- The plugin supervisor starts each installed plugin, restarts it when it fails, and stops it on uninstall (`internal/daemon/plugin_supervisor.go`); its driver launches and reports its harness's agents (`internal/daemon/plugin_driver.go`).
- attn asks each harness which models it offers (`internal/daemon/harness_models.go`).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`), in desktop flow, the default.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-harnesses.XXXX)`, and an empty agent folder `$P/codex`.

`<P>` is the proof folder's path written out.

- **The harnesses on offer.** Press ⌘N with `bridge native_key '{"key":"n","modifiers":["command"]}'` and wait for `[data-testid="location-picker-overlay"]`. `bridge dom_text '{"selector":"[role=\"radiogroup\"][aria-label=\"Session agent\"]"}'` names Terminal, Claude and Codex with their ⌥ keys, ⌥T, ⌥1 and ⌥2. Pi is listed only once the `attn-pi` plugin is installed and running. A harness the machine lacks has no key and is disabled, titled "<Harness> is not available on this machine".
- **Pick with a key.** Run `bridge native_key '{"key":"2","modifiers":["option"]}'`. `bridge dom_text '{"selector":"[role=\"radiogroup\"][aria-label=\"Session agent\"] [aria-checked=\"true\"]"}'` names Codex, and `./attn settings get new_session_agent` prints `codex`: a pick is saved as the default. Press Escape.
- **The default from the shell.** Run `./attn settings set new_session_agent claude`. It prints `new_session_agent = claude`. Press ⌘N: the checked harness is Claude. Press Escape. Open Settings with ⌘, (`bridge native_key '{"key":",","modifiers":["command"]}'`) and click `[data-testid="settings-nav-agents"]` natively: `bridge dom_text '{"selector":"[aria-label=\"Default session agent\"] [aria-pressed=\"true\"]"}'` reads `Claude`. The same section holds each harness's executable, default model and effort, and Auto-approve; `bridge capture_screenshot` records it. Click `[data-testid="settings-nav-models"]` natively: `bridge settings_get_state` shows `activeSection: "models"`.
- **Harness logos.** Run `wsctl add-session --cwd $P/codex --agent codex --label harness-codex --id harness-codex --place`. `bridge dom_wait '{"selector":"[data-testid=\"sidebar-session-harness-codex\"] [role=\"img\"][aria-label^=\"Codex · \"]","timeoutMs":5000}'` matches. Open the palette (⌘⇧K, wait for `[role="combobox"][aria-label="Commands"]` to take focus), type `Hide harness logos` and press Enter. `./attn settings get sidebar_harness_logos_enabled` prints `false`, and `bridge dom_wait '{"selector":".sidebar.sidebar--hide-harness-logos","timeoutMs":5000}'` matches: the row shows its state dot where the logo was. "Show harness logos in the sidebar" puts them back and the selector goes absent.
- **Plugins from the shell.** Run `./attn plugin list`. Its JSON lists `attn-pi` under `plugins`, with `availability` `bundled`. Run `./attn plugin install-bundled attn-pi`: it prints JSON with `"ok": true` and `"name": "attn-pi"`, and `./attn plugin list` shows `installation_state` `installed`. Run `./attn plugin uninstall attn-pi`: the same JSON, and `installation_state` is `available` again.
- **Plugins from Settings.** Open Settings and click `[data-testid="settings-nav-plugins"]` natively. The `attn-pi` card reads Bundled and Available. Click `.plugin-card .plugin-card-header button.settings-action` ("Install") natively: the card reads Installed with its runtime state, and `./attn plugin list` agrees. `./attn plugin remove attn-pi` puts it back to Available, and the card follows.
- **Proof.** In `$P`, keep each `./attn` command with its output and exit code, `bridge dom_text` of the "Session agent" group before and after the key, `./attn settings get new_session_agent` after it, `./attn plugin list` before and after each install, and `bridge capture_screenshot '{"path":"<P>/harness-logos.png"}'` with the logos on and `<P>/harness-plugins.png` with the Plugins section open. Put the way in in each file's name, such as `harnesses-settings-plugins.png`.

## Contexts

- **Durability across restarts**: installed plugins start again with the daemon, and a reconnecting driver is handed its own runs and the auto mode config they run under. Harness settings are kept.
- **Linux**: plugins run where the daemon runs. The Linux real-app runner needs `pi` on the machine and `attn plugin install-bundled attn-pi`.
- **Outposts**: a harness's executable is a machine setting, so each outpost decides its own; the rest of the harness settings come from the home.
- **Profiles**: harness settings and plugins apply across all profiles on the daemon.
- **Performance and idle cost**: a plugin whose socket stalls does not stall the daemon. One shared Codex app-server uses less memory than a Codex per terminal ([shared-codex.md](shared-codex.md)).
- **Harnesses**: Claude Code reports through hooks and its terminal title; Codex through hooks, or its app-server under shared Codex; Pi and any other plugin harness through the plugin's driver. YOLO, auto mode and the chief toggle each show only for the harnesses that support them.
- **Keyboard and native input**: ⌥1 to ⌥9 and ⌥T pick the harness in the picker; the palette toggles the logos.
- **Compatibility across versions**: a plugin names the API it was built for in its `attn-plugin.toml` (`attn_api_version`), and a driver older than the current input rules can still report input it took.

## Tests

- Wire: `internal/daemon/plugin_wire_test.go`, `internal/daemon/plugins_wire_test.go`, `internal/daemon/plugins_install_wire_test.go`, `internal/daemon/plugin_driver_*wire_test.go`, `internal/daemon/plugin_restart_wire_test.go`, `internal/daemon/plugin_stop_owner_wire_test.go`, `internal/daemon/plugin_stalled_socket_wire_test.go`, `internal/daemon/plugin_launch_instructions_wire_test.go`, `internal/daemon/harness_models_claude_wire_test.go`, `internal/daemon/harness_models_plugin_wire_test.go`, `internal/daemon/agent_catalog_wire_test.go`.
- Stack: `cmd/attn/plugin_stack_test.go`, `cmd/attn/plugin_runtime_stack_test.go`, `cmd/attn/plugin_restart_stack_test.go`, `internal/daemon/plugin_driver_teardown_stack_test.go`, `internal/daemon/plugin_launch_conversation_stack_test.go`.
- Scenario: `agent-settings`, `claude-shared-conversation`, `pi-shared-conversation`.
- Kernel: `internal/plugins/*_test.go`, `internal/pty/harness_signals_test.go`, `internal/daemon/plugin_runtime_test.go`.
- App: `app/src/components/Sidebar.harnessIcons.test.tsx`, `app/src/App.modelTierSettings.test.tsx`.

## Gotchas

- The baseline's `ATTN_CLAUDE_EXECUTABLE` and `ATTN_CODEX_EXECUTABLE` win over the executables set in Settings, so changing one there changes nothing on the baseline.
- The mock stands in for `claude` and `codex` only. A Pi agent needs the real `pi` and the `attn-pi` plugin; `pi-shared-conversation` and `automode-environment` run it on purpose. The shared-conversation warning needs two harness processes on one conversation, which the scenarios set up.
- `attn plugin` has no help. With no subcommand it prints its usage and exits 1, and `attn plugin --help` exits 1 with `unknown plugin command: --help`. Every subcommand prints JSON, and `uninstall` and `remove` do the same thing, `remove` adding `plugin_dir` to its reply.
- A plugin that owns running delegated agents refuses to uninstall: `plugin "<name>" owns N active delegated run(s)`.
- `bridge location_picker_get_state` lists the YOLO, auto mode and chief switches in `agents` beside the harnesses, since they share a style. Read the harness from the "Session agent" group.
- Working but wrong: `attn plugin` has no help and always answers in JSON, and its `uninstall` and `remove` are two names for one action.
