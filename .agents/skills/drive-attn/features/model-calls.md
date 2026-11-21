# Model calls

A paired program calls a model through attn: it sends its own instructions
and input, and gets the model's text answer, with no tool that reaches outside
the call and no memory of other calls. attn supplies the harness's sign-in and,
for what the call leaves out, the harness and model the user chose. While model calls are in use, attn
keeps one `claude` started and waiting, the model call spare, so a call waits
only for the model. Attn Lens refines dictation this way.

## Sub-features

- `model-call`: `attn model call --token-stdin` reads the token, then one JSON request, and prints one JSON line with `harness`, `model`, `text` and `model_ms`.
- `model-call-target`: a call may name a `harness`, and a `tier` or a `model`. What it leaves out comes from `model_calls.agent` (empty: `new_session_agent`) and `model_calls.model` (empty: the standard tier). A tier resolves through the harness's model tiers, with a fallback per tier when discovery fails.
- `model-call-limit`: `limit_ms` is required. Past it, the line has `error` and `limit_ms`, and the command exits 1. Stopping the command ends the call and its process.
- `model-call-refusals`: tier and model both named, an unknown tier, an empty `input`, a harness that is not installed, a ninth call while eight run, headless tasks off, and a token attn does not know, each one line on stderr and exit 1.
- `model-call-spare`: one `claude` with stream-json input and no tools, started while a sender is paired, headless tasks are on, the default harness is Claude, and the user was present or a call arrived within 4 hours. A call takes it, and attn starts the next one at once.
- `model-call-codex`: a Codex call runs as one ephemeral, tool-free conversation on the sender's profile's shared Codex app-server while that server runs, and cold through `codex exec` otherwise. attn starts no Codex process for it.
- `model-call-log`: one daemon log line per call, `model call: sender=... purpose=... harness=... model=... spare=... took=... model_ms=... tokens_in=... tokens_out=... cost_usd=... outcome=...`, with no instructions, input or answer.
- `model-log`: `attn model log [--sender <name>] [--profile <name|id>] [--json]` lists the profile's calls from the `model_calls` table, newest first: `at`, `sender`, `purpose`, `harness`, `model`, `took_ms`, `model_ms` (absent when not reported), `tokens_in`, `tokens_out`, `cost_usd`, `outcome` (`answered`, `failed`, `limit`, `stopped`). One row per call that ran, written when it ends; refused calls have none. Rows older than 30 days are pruned hourly (`model_call_log_prune`), and deleting the profile deletes its rows.

## How to get to it

### People

- A program the user paired with `attn send pair` (Attn Lens, a script), allowed in the app's approval dialog, as [Quick capture](quick-capture.md) describes.
- Settings: `model_calls.agent` and `model_calls.model`, through `attn settings set` or the settings dialog.

### Agents

- None. Agents in sessions use `attn vision-check` for their own model questions.

## Driving it

Preconditions:

- An instance (`make install INSTANCE=<name>`), launched with `bridge --launch ping`.
- The real harnesses and headless tasks on, appended to the env file before the launch, so the daemon starts with them: `echo "export ATTN_HEADLESS_TASKS=on ATTN_CLAUDE_EXECUTABLE=$(which claude) ATTN_CODEX_EXECUTABLE=$(which codex) CODEX_HOME=$HOME/.codex" >> $DRIVE_ENV`. Each call spends real tokens; keep the text synthetic.
- Pair: `./attn send pair --name Probe --json > $P/pair.out &`, then `bridge wait '{"target":{"key":"sender-pairing-dialog"}}'` and `bridge press '{"target":{"key":"sender-pairing.allow"}}'`. The token is `token` on the second line of `$P/pair.out`.
- The daemon's pid is in `~/.attn-<name>/attn.pid`. The spare is its child whose command has `--input-format stream-json`.

Recipes:

- **The spare.** Within a few seconds of pairing, with the app open, `ps -ax -o pid=,ppid=,stat=,time=,command=` shows a `claude --print ... --disallowedTools * ... --input-format stream-json` under the daemon, with no `--model` and no `--system-prompt`. Its state is `T`, stopped, and its CPU time does not grow. With `ANTHROPIC_API_KEY` in the env file it runs with `--bare` and stops the same way.
- **A call on the defaults.** `{ echo "$TOKEN"; echo '{"purpose":"drive","system":"Answer with one word.","input":"What color is a clear daytime sky?","limit_ms":60000}'; } | ./attn model call --token-stdin` prints `{"harness":"claude","model":"sonnet","model_ms":<n>,"text":"Blue"}` and exits 0. `grep 'model call: sender' ~/.attn-<name>/daemon.log | tail -1` shows `spare=true` and none of the call's text. A new spare is running.
- **Codex cold.** With no Codex session open, add `"harness":"codex","tier":"light"`: the line names `codex` and `gpt-6-luna` and has no `model_ms`. The log shows `spare=false`, and the claude spare keeps running. While it runs, `ps` shows `codex exec ... -c instructions="Answer with one word." ... -` with no input in its arguments.
- **Codex on the shared app-server.** Open a Codex session (`go run ./scripts/wsctl add-session --cwd <dir> --agent codex`) and wait for `shared Codex: started the app-server` in the daemon log. The same call answers, the log shows `spare=false`, `ps` shows no Codex process besides the shared app-server, and `go run ./scripts/wsctl list` shows no session for the call. A call with `"limit_ms":3000` and a long essay to write ends at the limit with `limit`.
- **A large input.** An `input` of 1,048,576 characters, built into the request JSON by a script (`"x"*1048576` in Python), answers on Claude and on Codex, cold or on the shared app-server. On Codex one character more fails with `headless agent input is longer than the harness accepts`.
- **Settings.** `./attn settings set model_calls.agent codex` ends the spare (`model call spare: ending the claude spare: codex cannot start ahead`). With `model_calls.model light`, a call naming nothing answers from codex's light model. Set both back to empty: a spare starts again.
- **Limit.** `"system":"Write a 2000-word essay.","limit_ms":1500` prints `{"error":"the model did not answer within 1.5s","harness":"claude","limit_ms":1500,"model":"sonnet"}` and exits 1; no claude is left running for it.
- **Refusals.** `"tier":"light","model":"opus"` gives `model call: name a tier or a model, not both`; `"harness":"sigma"` gives `model call: "sigma" does not answer model calls; model calls are answered by claude or codex`, and a name attn does not know, such as `gemini`, gets the same line with that name. With `new_session_agent` set to `sigma` and `model_calls.agent` empty, a call naming nothing gives `model call: model calls default to the new-session agent "sigma", which does not answer model calls; set model_calls.agent to claude or codex`; `"input":""` gives `model call: input must not be empty; send the text the model answers`.
- **Headless tasks off.** Replace `ATTN_HEADLESS_TASKS=on` with `off` in the env file, quit the app with `./attn instance stop-app`, stop the daemon with `"$(./attn instance resolve --field appDaemon)" daemon stop`, and launch again. The spare does not start, and a call prints `model call: ` and the headless-tasks refusal, exit 1.
- **The log.** After the calls above, `./attn model log` prints a table with `TIME SENDER PURPOSE HARNESS MODEL TOOK MODEL TIME TOKENS IN TOKENS OUT COST OUTCOME`, newest first: the limit call says `limit`, a call whose command you stopped with ctrl-c says `stopped`, and a refused call has no row. `--json` prints the same rows as one array; `grep` it for the call's input text to see it absent. `--sender Probe` keeps that sender's rows.
- **Proof.** Keep each command, its output and exit code, and the log lines.

## Contexts

- **Linux**: the daemon and CLI run there the same way; pairing needs the app, which runs on macOS. `TestTheNativeAppAllowsAndDeniesAProgramThatAsksToPair` answers a pairing in the headless native app on Linux.
- **Outposts**: model calls run on the home, where the sender is paired; an outpost's harnesses do not answer them.
- **Performance and idle cost**: the spare holds about 200 to 300 MB and uses no CPU while it waits: once Claude answers attn's `mcp_status` request, attn stops the spare with SIGSTOP until a call takes it. That holds under `--bare`, which Claude runs with when the environment has an API key or a cloud provider. The spare ends 4 hours after the last presence report or call, when the last sender is unpaired, when headless tasks go off, or when the default harness cannot start ahead.
- **Harnesses**: Claude answers from the spare. Codex answers on the shared app-server while it runs and through `codex exec` otherwise; both declare `exec` and `wait`, a V8 isolate with no file system or network, and on gpt-6.2 models also `send_message_to_user_async`, whose message stays in the call's output (`docs/internal/harnesses/codex.md`, Headless runs). On the shared app-server the conversation keeps the user's `config.toml` settings, such as `developer_instructions`, and every Codex call sends `AGENTS.md` from `CODEX_HOME` (`docs/internal/harnesses/codex-app-server.md`). Sigma and plugin harnesses do not answer model calls.

## Tests

- Stack: `cmd/attnd/model_call_stack_test.go`.

## Gotchas

- The spare starts only after the app reports the user present, or after a call. Right after a daemon restart there is none until then; the first call runs cold.
- `ATTN_HEADLESS_TASKS` in the daemon's environment beats the `headless_tasks.enabled` setting, so turn tasks off in the env file, not with `attn settings set`.
- `tokens_in` counts cached input too, as Claude reports it in `usage`.
- `attn model log` outside a session with two profiles needs `--profile`, as `attn send senders` does.
- Working but wrong: a caller's text in the daemon log or the model log, or a spare that holds a second call's conversation. Each spare answers once and exits.
