# Harnesses

Bring your own harness: attn wraps Claude Code, Codex and Pi as they are, and
plugins add more. Each agent runs its harness's own CLI with the user's
account, settings and conversations, and the user can run the bare CLI at any
time. People pick a harness for each agent, set each one's executable and
default model, and install plugins; Pi itself comes as the bundled `attn-pi`
plugin. The sidebar can mark each agent with its harness's logo.

## Actions and ways in

### Choose and configure a harness

The new-agent picker's "Session agent" choice takes ⌥1–⌥9, or ⌥T for a shell
(`app/src/components/NewSessionDialog/SessionControls.tsx`; [agent-launch.md](agent-launch.md)).
Settings, Agents and models (`agents`, `app/src/components/SettingsModalSections.tsx`) sets "Default
session agent", executable (empty uses PATH), default model and effort, auto-approve, and shows each
harness's capabilities. Settings, Models (`models`, `app/src/components/ModelTierSettings.tsx`) maps
light, standard and deep tiers to models; attn asks each harness which models it offers
(`internal/daemon/harness_models.go`). People and agents can use `attn settings get|set` for
`new_session_agent`, `claude_executable` and `codex_executable` ([settings.md](settings.md)).

### Manage plugins

Settings, Plugins (`plugins`) has "Plugin source", Browse, "Install Plugin", each plugin's version,
Bundled/Installed/Available status, runtime state and health, Install for bundled plugins, and
priority. People and agents can run `attn plugin install --path <dir>`, `link --path <dir>`,
`install-bundled <name>`, `list`, `uninstall <name>` and `remove <name>` (`cmd/attn/plugin.go`),
each returning JSON. The supervisor starts installed plugins, restarts failures and stops them on
uninstall (`internal/daemon/plugin_supervisor.go`); a driver starts and reports its harness's agents
(`internal/daemon/plugin_driver.go`). Pi comes as the bundled `attn-pi` plugin.

### See harness and conversation state

Palette `toggle-harness-logos` ("Hide harness logos in the sidebar" or "Show harness logos in the
sidebar", `app/src/application/useAppCommands.tsx`) and `attn settings get|set sidebar_harness_logos_enabled` control the logo. Sidebar rows label it "<Harness> · <state>"
(`app/src/components/SidebarRows.tsx`); without a logo they show a state dot. Two tiles on one
Claude Code or Pi conversation warn "This conversation is also open in …" until one closes
(`app/src/components/SharedConversationNotice.tsx`).

### Receive guidance

A plugin driver that asks for launch instructions gets the same agent or chief guidance as other
harnesses (`internal/daemon/plugin_launch_instructions.go`). [agent-guidance.md](agent-guidance.md)
owns that guidance; Pi's sandbox and Guardian prompts are in [auto-mode.md](auto-mode.md).

## Driving it

Preconditions:

- The baseline instance with its app (`bridge --launch --wait-ready ping` answers) and What's new dismissed (`bridge dismiss_whats_new`), in desktop flow, the default.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-harnesses.XXXX) && echo "export P=$P" >> $DRIVE_ENV`, and an empty agent folder `$P/codex`.

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
