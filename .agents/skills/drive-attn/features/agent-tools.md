# Agent tools

With the experimental **Agent tools** setting on, the agents attn runs call
attn through MCP tools instead of the `attn` CLI. Each tool is one `attn`
command, such as `seed_note` for `attn seed note`. A call acts as the
session that made it, so a note an agent writes through a tool shows its
session as the author, as the CLI would. Claude Code, shared Codex, plain
Codex, plugin harnesses and Sigma each receive the tools in their own way,
and sessions on an outpost get the tools of the outpost they run on. Each
command's own behavior belongs to its feature's file, such as
[garden.md](garden.md) for the seed tools.

## Sub-features

- `tools-setting`: `agent_tools_enabled`, off by default. It applies to agents started after it changes. While it is off, every call is refused with `setting_off`, including calls from agents that launched with the tools.
- `tools-claude`: Claude Code launches with `--mcp-config` naming attn's `/mcp` as the server `attn`, its credential only in the environment as `ATTN_AGENT_CREDENTIAL`, and `mcp__attn__*` allowed. A call acts as the session that the terminal shows, a subagent's call as its parent's session.
- `tools-codex-shared`: each shared Codex conversation gets two servers, `attn-core` (the core tools) and `attn` (the rest). A call acts as the session that shows the call's conversation ([shared-codex.md](shared-codex.md) `codex-tools`).
- `tools-codex-plain`: a plain Codex gets the same two servers through `-c` overrides and its terminal's credential. Only a session that launched plain still runs plain.
- `tools-plugin-driver`: a plugin driver that declares `agent_tools` gets the endpoint in `driver.spawn` and `driver.resume` ([harnesses.md](harnesses.md) `plugin-driver`).
- `tools-plugin-server`: a plugin server that declares `caller_conversation_header` gets attn's server through `mcp/register`, and each conversation acts as its own session ([harnesses.md](harnesses.md) `plugin-server`).
- `tools-sigma`: a new Sigma conversation starts with `--mcp-server attn=<description>`. A call acts as the session that holds the call's root conversation ([harnesses.md](harnesses.md) `sigma`).
- `tools-outposts`: a session on an outpost calls the outpost's own `/mcp`. A `home` tool runs at the home as that session ([outposts.md](outposts.md) `remote-tools`).
- `tools-teaching`: the server's instructions carry the intent map, a list from what an agent needs to the tools that do it. A launch with the tools leaves the CLI teaching of the moved families to the server.
- `tools-call-log`: each call writes an `agent tools:` line to the daemon log of the machine that served it, naming the tool, the credential and the session it acted as.

## How to get to it

### People

- **Settings → Experimental**, the "Agent tools" switch `settings.feature.agent_tools_enabled` (`native/crates/attn-app/src/settings/controls.rs`), in the group `settings.feature-group.Agents`.
- Palette `feature-agent_tools_enabled` (`native/crates/attn-app/src/palette/commands.rs`), which turns the setting on or off.
- Where people see what agents do: the call's result, such as a note in `attn seed show` authored by the session, and the harness's own screen: Claude Code shows "Called attn", Codex "Called attn-core.seed_note", Sigma "▸ mcp__attn__seed_note". The daemon log's `agent tools:` lines name every call ([diagnostics.md](diagnostics.md)). The app shows no call of its own.
- Their own shell: `attn settings get|set agent_tools_enabled` and `attn settings list --features` ([settings.md](settings.md)).

### Agents

- The tools: every command in `internal/catalog/content/*.yaml` that offers a tool, named `<family>_<verb>`, served over HTTP at `/mcp` on the daemon's port (`internal/daemon/agent_tools.go`, `internal/mcpserver`). Claude Code and Sigma name them `mcp__attn__<tool>`. Codex names them `attn-core.<tool>` or `attn.<tool>`.
- Each harness's launch: `internal/harness/internal/claude/launch.go`, `internal/harness/internal/codex/harness.go`, `internal/harness/internal/codex/shared.go`, `internal/harness/internal/sigma/sigma.go`, `internal/harness/internal/plugin/plugin.go`, and `internal/harness/appserver/server_connection.go`.
- Prompts: the server's description and the intent map (`internal/catalog/content/server.yaml`), which the harness shows as the server's instructions. Sigma also gets the map after its launch instructions. Each tool's description holds the command's rules.
- The CLI stays: each tool's command still runs as `attn <family> <verb>`, and agents started while the setting was off use only the CLI.

## Driving it

The mock agent never calls a tool, so these recipes run the real harnesses with short prompts. Each prompt spends tokens.

Preconditions:

- A daemon-only instance from the baseline, before `./attn daemon ensure`: run `write-env.sh <name>` and the mock GitHub as [../SKILL.md](../SKILL.md) says. For the app's switch, run `make install INSTANCE=<name>` instead of `./attn daemon ensure`, after the steps below. On Linux, run every command that reaches the app inside one `xvfb-run -a -s '-noreset -screen 0 1600x1000x24' bash -c '…'`.
- Make a proof folder with `P=$(mktemp -d /tmp/drive-attn-agent-tools.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- Give the real harnesses scratch homes signed in with copies of your own sign-in, and trust their work folders:

```bash
. /tmp/drive-attn-<name>.env && mkdir -p $P/claude $P/codex $P/work/claude $P/work/codex $P/work/sigma && cp ~/.claude/.credentials.json $P/claude/ && cp ~/.codex/auth.json $P/codex/ && python3 -c 'import json,sys,os; w=sys.argv[2]; t={"hasTrustDialogAccepted":True,"hasCompletedProjectOnboarding":True}; json.dump({"hasCompletedOnboarding":True,"projects":{w:t,os.path.realpath(w):t}},open(sys.argv[1]+"/.claude.json","w"))' $P/claude $P/work/claude && printf '[projects."%s"]\ntrust_level = "trusted"\n' $P/work/codex > $P/codex/config.toml
. /tmp/drive-attn-<name>.env && echo "export ATTN_CLAUDE_EXECUTABLE=$(which claude) ATTN_CODEX_EXECUTABLE=$(which codex) CLAUDE_CONFIG_DIR=$P/claude CODEX_HOME=$P/codex" >> $DRIVE_ENV
```

- The two homes hold copies of your sign-in. After [Cleanup](../SKILL.md#cleanup), remove `<P>/claude` and `<P>/codex`, written out, and keep the rest of the proof folder.
- Then, in a new command, start the daemon with `./attn daemon ensure`. Check its environment before the first launch: `tr '\0' '\n' < /proc/$(cat $(./attn instance resolve --field dataDir)/attn.pid)/environ | grep -E '^(CLAUDE_CONFIG_DIR|CODEX_HOME|SIGMA_HOME)='` prints the first two under `$P` and `SIGMA_HOME` under `$ATTN_TOOL_HOME`, as the env file sets it. The doctor's `tool.` line names the real `claude`, not `app/scripts/real-app-harness/mockAgent.mjs`.
- Plant the seed the agents note on: `S=$(./attn seed plant "Agent tools drive" -m "Each harness notes here through the tools." --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])') && echo "export S=$S" >> $DRIVE_ENV`.
- Write one prompt per harness, each naming the seed written out: `printf 'Use the attn MCP tools, not the shell or the attn CLI. Call seed_show for seed %s, then seed_note on seed %s with the text "%s through the attn tools". Reply with the note id and stop.\n' $S $S Claude > $P/prompt-claude.txt`, and the same with `Codex` into `$P/prompt-codex.txt` and `Sigma` into `$P/prompt-sigma.txt`.
- "The call log" below is `./attn debug daemon-log --since 30m --grep 'agent tools:'`. Re-read it until the line you wait for appears, with a deadline of 3 minutes per call.

Steps:

- **Off by default.** Run `./attn settings get agent_tools_enabled`: it prints an empty line. `./attn settings list --features` lists `agent_tools_enabled` as `off`.
- **Turn it on from the shell.** Run `./attn settings set agent_tools_enabled true`: it prints `agent_tools_enabled = true`, and `./attn settings list --features` lists it `on` with `New agents`.
- **Turn it on in the app.** With the app launched, open Settings with `mod+,` (`mod+shift+,` on Linux), press `settings.nav.experimental`, then `settings.feature.agent_tools_enabled`: `./attn settings get agent_tools_enabled` flips. Choose the palette command `feature-agent_tools_enabled` (`mod+shift+k`, `mod+alt+k` on Linux): it flips back. Leave it on.
- **Claude Code calls a tool.** Run `wsctl add-session --cwd $P/work/claude --agent claude --label tools-claude --id tools-claude --initial-prompt-file $P/prompt-claude.txt`. A fresh Claude Code config asks about Code Mode first: `./attn agent peek tools-claude` shows "No, start without it". Answer with `wsctl input --id tools-claude --enter`, and the prompt runs. The call log shows `agent tools: seed_show by terminal <terminal> (session tools-claude): ok` and the same for `seed_note`. Peek shows "Called attn 2 times" and the note's id. `./attn seed notes $S` shows the note by `tools-claude`.
- **No credential in an argument.** Run `ps -eo args | grep -cE 't\.[0-9a-f-]{36}\.[0-9a-f]{64}'`: it prints `0`. `ps -eo args | grep -- '--mcp-config' | grep -o 'Bearer [$][{][A-Z_]*[}]'` prints `Bearer ${ATTN_AGENT_CREDENTIAL}`, the variable and not its value.
- **Shared Codex calls a tool.** Run `wsctl add-session --cwd $P/work/codex --agent codex --label tools-codex --id tools-codex --initial-prompt-file $P/prompt-codex.txt`. The call log shows `agent tools: seed_note by shared Codex conversation <conversation> (session tools-codex): ok`, and peek shows `Called attn-core.seed_note`.
- **Plain Codex calls a tool.** The harnesses cannot reach this way in: every new Codex terminal launches on the shared app-server, so no fresh instance starts a plain Codex. The step fails. Gotchas says why.
- **A plugin driver calls a tool.** Run `./attn plugin install --path examples/plugins/sample-harness`. Wait until `./attn plugin list` shows `sample-harness` connected and healthy. Run `wsctl add-session --cwd $P/work/claude --agent sample --label tools-sample --id tools-sample --place`, then `wsctl input --id tools-sample --text '/tool seed_note {"seed":"<S>","text":"Sample harness through the attn tools"}' --enter`, with the seed written out. Peek shows `tool seed_note: {"author":{"name":"tools-sample","ref":"session:tools-sample"}`, and the call log shows `seed_note by terminal <terminal> (session tools-sample): ok`. The sample answers its `/tool` line itself, so no model runs. `./scripts/test-go.sh --packages -run 'TestAPluginHarnessThatAsksForAgentTools|TestASharedServerPluginGetsTheTools' ./internal/daemon` checks the spawn field with and without the capability and the setting, resume, `mcp/register`, two conversations and a fork.
- **A plugin server calls a tool.** The harnesses have no plugin server to run live; Sigma is the live shared server, below. Run `./scripts/test-go.sh --packages -run 'TestEachConversationOfTheSampleServerCallsAttnsToolsAsItsOwnSession' ./cmd/attnd`. It installs `examples/plugins/sample-server` on the packed SDK and calls `seed_note` from two conversations, each as its own session.
- **Sigma calls a tool.** Build Sigma from a scratch clone of SolenesInc/sigma's `main`, at `~/projects/victor/sigma` (clone it there if it is missing), run from source by a wrapper: `git clone -q ~/projects/victor/sigma $P/sigma-src && git -C $P/sigma-src checkout -q $(git -C ~/projects/victor/sigma rev-parse origin/main) && (cd $P/sigma-src && bun install) && mkdir -p $P/bin && printf '#!/bin/sh\nexec bun %s/sigma-src/packages/sigma/src/main.ts "$@"\n' $P > $P/bin/sigma && chmod +x $P/bin/sigma`. Run `./attn settings set sigma_executable $P/bin/sigma`, `./attn settings set sigma_enabled true` and `./attn settings set default_model_sigma claude/haiku`. Run `wsctl add-session --cwd $P/work/sigma --agent sigma --label tools-sigma --id tools-sigma --initial-prompt-file $P/prompt-sigma.txt`. The daemon log shows `sigma: started $P/bin/sigma serve for $ATTN_TOOL_HOME/.sigma`, with the paths written out, and the call log shows `seed_note by sigma conversation <conversation> (session tools-sigma): ok`. Peek shows `▸ mcp__attn__seed_note`, and `ps -eo args | grep -o -- '--mcp-server attn=[^-]*' | head -1` shows the server by name and description, with no credential. That ran Sigma's Claude path. For Sigma's own loop without an OpenAI sign-in, put `SIGMA_MODEL_SCRIPT` in the wrapper and set `default_model_sigma` to `script/script-code-mode` (`docs/internal/harnesses/sigma.md`). `TestASigmaSessionStartsWithAttnsToolsWhileAgentToolsAreOn` covers both launch paths with the fake server.
- **Agents on an outpost.** Drive the "Agent tools on an outpost" step of [outposts.md](outposts.md). It needs a second machine enrolled as an outpost, with Claude Code and Codex signed in there. `./scripts/test-go.sh --packages -run 'TestAClaudeOnAnOutpost|TestAnOutpostAgentsCredential|TestTurningTheToolsOff|TestASharedCodexOnAnOutpostRuns' ./cmd/attnd` covers the same calls with the fake agent.
- **Turn it off.** Run `./attn settings set agent_tools_enabled false`, then `wsctl input --id tools-codex --text "Use the attn MCP tool seed_note, not the shell, to note off on seed <S>. Reply done."`, with the seed written out, and `wsctl input --id tools-codex --enter` on its own. The call log shows `seed_note by shared Codex conversation <conversation> (session tools-codex): refused setting_off: Agent tools is off.`, peek shows the refusal with `Fix: Use the attn CLI until the setting is on.`, and the seed gets no note `off`.
- **Proof.** In `$P`, keep the call log, `./attn seed notes $S`, each `./attn agent peek` after its call, the two `ps` counts, each `./attn settings` command with its output and exit code, and with the app, `bridge shot '{"path":"<P>/agent-tools-switch.png"}'` with Experimental open. Name each file for its way in, such as `claude-peek.txt`.

## Contexts

- **Durability across restarts**: a terminal's credential outlives a daemon restart, so the agent's next call after the restart succeeds. Sigma's server registers attn's server again after its own restart.
- **Linux**: `/mcp` is served on the daemon's own port on every machine.
- **Outposts**: a session on an outpost calls the outpost's `/mcp`. A `home` tool runs at the home as that session; a `here` tool runs on the outpost, even with the bridge down. With the bridge down, a `home` tool is refused with `home_unreachable`. The setting is a home setting, so turning it off at the home refuses the next call on every outpost ([outposts.md](outposts.md)).
- **Profiles**: a call acts in its session's profile. Every tool refuses or hides what belongs to another profile.
- **Harnesses**: Claude Code and plain Codex present their terminal's credential, so a call acts as the session the terminal shows, after `/clear` too. Shared Codex and Sigma act as the session that holds the call's conversation, so a conversation open in two terminals acts as its own session. A subagent's call acts as its parent's session on Claude Code, Codex and Sigma. Claude Code's REPL and Codex's code mode can chain tool calls in one script; attn turns neither mode on.
- **Compatibility across versions**: an agent that launched while the setting was off uses only the CLI until it launches again. A plugin built on an SDK before 0.6.0 never declares `agent_tools`, so its agents use the CLI.

## Tests

- Wire: `internal/daemon/agent_tools_wire_test.go`, `internal/daemon/agent_tools_*_wire_test.go`, `internal/daemon/codex_agent_tools_wire_test.go`, `internal/daemon/plugin_agent_tools_wire_test.go`, `internal/daemon/sigma_wire_test.go` (`TestASigmaSessionStartsWithAttnsToolsWhileAgentToolsAreOn`), `internal/daemon/garden_move_reason_wire_test.go`, `internal/daemon/session_reads_profile_wire_test.go`.
- Stack: `cmd/attnd/agent_credential_arguments_stack_test.go`, `cmd/attnd/agent_tools_outpost_stack_test.go`, `cmd/attnd/agent_tools_delegate_outpost_stack_test.go`, `cmd/attnd/agent_tools_terminal_stack_test.go`, `cmd/attnd/plugin_agent_tools_stack_test.go`.
- Kernel: `internal/mcpserver/schema_test.go`, `internal/catalog/mention_test.go`.

## Gotchas

- The daemon reads its environment once, at start. Append the harness homes to the env file first, then start the daemon in a later command: the command that appends still runs on the environment it sourced. Check the daemon's environment as the preconditions say before the first launch.
- Your login shell's `CLAUDE_CONFIG_DIR`, `CODEX_HOME` and `SIGMA_HOME` win over the daemon's own, so a login shell that sets one sends the instance's agents to that home, and the daemon's environment does not show it. The daemon log's `sigma: started … for <home>` line names the home Sigma's server runs in.
- A Sigma TUI started against the wrong home keeps that home's server alive across a daemon restart. Clean the instance to end it.
- A note on the seed does not prove a tool call: an agent that runs `attn seed note` in its shell writes the same note under the same session. Only the `agent tools:` line proves the call. Working but wrong: the agent reports the note's id and the call log has no line for it.
- The credential in the line differs by harness: `by terminal <id>` for Claude Code, plain Codex and plugin drivers, `by shared Codex conversation <id>` and `by sigma conversation <id>` for the shared servers. A call refused before it reaches a tool logs `agent tools: POST /mcp refused: <reason>` instead.
- The setting applies to agents started after it changes. An agent started before you turned it on has no attn server, and turning it on does not change that agent.
- Plain Codex is unreachable on a fresh instance. Only a Codex session that launched plain, before shared Codex, still runs plain, and nothing in a fresh instance starts one.
- Codex takes text and Enter sent in one `wsctl input` as a paste and leaves the text in its composer. Send the Enter in a second `wsctl input`. An initial prompt file needs no Enter.
- Codex may call other tools first, such as `skill`, before the ones the prompt names. Each still logs its own line.
- On the baseline the classifier is off, so a real harness's session can show `unknown` or stay on a state the classifier would have changed. The call log is the signal to wait on, not the session's state.
- Each real call spends tokens on your account. Keep prompts to one or two tool calls.
