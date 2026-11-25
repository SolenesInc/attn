# Agent bench

The bench runs real agents on short tasks and judges each run by its end state, with pass counts, turns, tokens and cost per cell. It has two drivers:

- `instance/` starts real Claude Code, Codex and Sigma sessions on a non-production attn instance. With `agent_tools_enabled` on, the agents reach attn's real `/mcp`. With it off, they reach the real CLI. This driver runs the task grid of the plot `attn-tools-over-mcp-agents-attn` (`s-ct56cj`, check C6).
- `container/` drives Claude Code through the Claude Agent SDK, with its own tools off, Sigma's shell and file tools on a throwaway container, and a mocked attn. It measures a limited agent, such as a crew member that reaches attn only through tools.

Both spend model tokens on your subscriptions, so CI never runs them. A person runs them.

## Run the instance driver

You need `make build` done in this checkout, bun, Go, cargo for the ask tasks, bash 5.1 or newer, `mkfifo`, and signed-in `claude` and `codex`, all on `PATH`. macOS ships bash 3.2, so put a newer one ahead of it. Run from the repository root:

```sh
make build
bun scripts/agent-bench/instance/bench.ts --instance bench --tasks standby --harness claude,codex --tools on,off
```

The driver starts the instance's daemon with headless model tasks off, runs every cell, prints the table, and ends with `attn instance clean <name>`. Ctrl-C and SIGTERM clean the instance the same way and write no table or `results.json`. Pass `--keep` to leave the instance running for inspection.

`--instance` must name a new instance, because the run changes the instance's settings and cleans it. The driver asks attn for the canonical name, so `Bench` and ` bench ` both mean `bench`. Before it runs anything, it refuses the default and `dev` instances and any instance that `attn instance list` already shows. To run again on a name you kept, clean it first with `attn instance clean <name>`.

Each run gets its own profile and Notebook folder, so seeds, slugs and journal entries never collide between runs, and its own folder under the system temp folder. The driver starts the session through the daemon as the app does (`spawn_session` with `scripts/wsctl`). It waits for the prompt, answering a trust screen on the way, and sets up the task, acting as the user or as the agent's own session. A dialog that ends in `· Esc to cancel`, such as Claude Code's offer to set up auto mode, would take the prompt's Enter, so the driver presses Esc. Then it types the prompt into the terminal and waits until the session is back at its prompt, waits on the user in its harness's own question or approval prompt, or has closed itself. Before judging, it reads the garden, the asks and the inboxes through the instance's CLI.

The numbers come from attn itself:

- The models, tokens and cost come from `attn session show --json` for the session the prompt went to, less what the session had used before the prompt. Uncached input, cache reads, cache writes and output are kept apart, and an approval reviewer's tokens apart from the agent's. Cost is at API prices. A model attn has no price for, such as Codex's, shows as `+ unpriced`. A run whose usage attn could not measure whole, or that shows no model, is marked `usage incomplete` and left out of the token and cost figures. Other sessions a run starts, such as the delegate in `msg-delegate`, are stopped once the run is judged and not counted.
- Turns and searches come from `attn session transcript --json`, from the prompt on. `--help` reads, and skill and guide reads, are counted from the attn calls below. A run with `--tools on` that used the CLI is marked `used the CLI`.
- At the home, attn calls are executions, counted alike on both surfaces. A tool call is a line of the daemon's log (`agent tools: …`), with its refusal. A CLI call is one run of the `attn` the session's shell finds: the driver starts the daemon from a folder where `attn` is a recorder that notes each command's arguments and exit code and then runs the real CLI. The driver refuses to start when the recorder records nothing. Hook calls (`attn _…`) pass through unrecorded. Exit 2 is a usage refusal. Any other non-zero exit is a refusal, except of a command that asks for `--help`, that lost its pipe's reader (exit 141) or that the sandbox blocked (`blocked` below), so a command that answers with its exit code, such as `presence --seed-ids` without seeds, counts as refused. The transcript supplies a tool call's arguments, matched by order. Commands that ran for certain but that the recorder missed are named as `unrecorded` in the table, and a run whose commands were all missed has a fault. A Codex rollout lists each shell command that ran. For the other harnesses, the transcript's shell calls that the harness did not refuse count. All the commands of a script count when they follow each other with nothing between, or form one `&&` chain that succeeded. Any other script, such as one with `||`, a loop or an `if`, counts only its first command, because a branch may not have run. A code-mode script outside Codex counts none, because the transcript does not say which of its commands ran.
- On an outpost, nothing records the calls, so they are read from the transcript: a tool of attn's server, a tool named in a code-mode script, or an `attn` command in the agent's shell, unless Claude Code refused the call (`denied` below). A loop there counts once, a command in a branch counts whether or not it ran, and a command shows as refused only when it is the whole shell command and its result is an error, so a failure inside a longer line or a script that runs several commands does not show. Compare an outpost's counts only with another outpost's.
- `blocked` counts, at the home only, the commands that fail with attn's own message that the sandbox blocks its socket, as Codex's does. attn refused nothing, so they are not CLI calls, refusals or failures, and a probe does not pass on one. A command that fails for another reason, such as a parse error, is a CLI failure even inside a sandbox. Codex usually reruns such a command outside the sandbox, so each adds time. How many there are depends on the rules in the harness home's `.codex/rules`, where attn's own message suggests allowing the prefix.
- `denied` counts, at the home and on an outpost, the calls that Claude Code refused to run and that hold an `attn` command or one of attn's tools: a shell command, a code-mode script or a tool call. The driver reads that from Claude Code's answer in the transcript, `Permission for this action was denied by the Claude Code auto mode classifier` or another of its permission refusals, so a refusal by Codex or Sigma does not show. The call reached no attn, so like `blocked` it is not an attn call or refusal, and it counts once however many commands it holds. The run is marked `Claude Code denied <n> attn calls`, because such a run often fails for the refusal and not for attn.
- `usage` counts calls refused because their arguments did not fit, on either surface, such as a call of a deferred tool before the agent loaded its schema. It does not fail a run.
- `turn s` runs from the prompt's Enter until the session settles. Launch and setup are not in it. When the provider refuses a Codex request, for example with `429 Too Many Requests`, Codex waits and sends it again. At the home, the driver reads those waits from the retry warnings that Codex writes to `logs_*.sqlite` in its home, for the conversation the prompt went to. Such a run is marked `waited <n>s for the provider` and left out of the `turn s` median. `retryMs` in its `result.json` is the wait Codex announced, at most the turn. Claude Code's and Sigma's waits, and Codex's on an outpost, are not read.

A run has a fault when one of these holds:

- The harness's own record, in Claude Code's transcript attachments or Codex's rollout, shows that the session did not have its cell's tools setting. With no such record the setting is unknown, which is not a fault.
- A model is pinned, and attn reports another model or none.
- A `claude` run used code mode.
- The prompt never reached the agent.
- A Codex run waited for the provider and did not settle before the deadline.
- The session ran `attn` commands, and the recorder saw none of them.

A faulted run fails, still counts in its cell's row, and is left out of the D22 comparison.

Each run keeps `transcript.jsonl`, `calls.json`, `records.json` (at the home only: the session's tool calls and CLI commands as recorded), `asks.json` (the session's open asks), `screen.txt` and `result.json` in `instance/runs/<stamp>/<task>/<cell>/<n>/`, beside `results.json` and `table.md` for the whole run.

### Harness homes

By default, the agents run with your own `~/.claude`, `~/.codex` and `~/.sigma`. Your global `CLAUDE.md`, plugins and MCP servers then add to every Claude Code prompt, which raises input tokens, and Claude Code adds an entry for each run's folder to your `~/.claude.json`, as it does for any folder it runs in. Codex keeps its trust answer for each run's folder in your `~/.codex/config.toml` until the run ends and the driver removes it. `msg-delegate` would write a further entry there, so the driver refuses a Codex cell of it without `--tool-home`. To measure attn alone, make a bench home once and pass `--tool-home <dir>`. The driver then starts the daemon with `CLAUDE_CONFIG_DIR=<dir>/.claude`, `CODEX_HOME=<dir>/.codex`, `SIGMA_HOME=<dir>/.sigma` and `ATTN_TOOL_HOME=<dir>`. To make the home, sign each harness in:

```sh
CLAUDE_CONFIG_DIR=~/attn-agent-bench-home/.claude claude    # then /login
CODEX_HOME=~/attn-agent-bench-home/.codex codex login
SIGMA_HOME=~/attn-agent-bench-home/.sigma sigma login openai    # for Sigma's own loop
```

A bench home needs a sign-in for each harness and nothing else that the driver cannot make. Claude Code's first-run screens for theme and onboarding do not block a run when the home has a `.claude/.claude.json` with `{"hasCompletedOnboarding": true, "theme": "dark"}`. Claude Code and Codex ask once per folder whether to trust it. The driver answers that screen for the folders it makes, and for `msg-delegate` saves Codex's answer for the delegate's folder, and at the end it removes the Codex entries for those folders from the home's `config.toml`.

Some things stay shared with your `HOME` even with a bench home. Codex reads `~/.agents/skills` whatever `CODEX_HOME` says, so the wrapper below switches each of those skills off. Claude Code's shells load your shell profile, and every agent's shell sees your `PATH`, git configuration and credentials, so its commands can read any file you can.

A harness can refresh its sign-in. If the bench home's sign-in is a copy of another home's, the refresh can sign that other home out, so run on a copied sign-in only while its access token stays valid for the whole run.

The `codex` cells run through a wrapper in `codex_executable` that adds `allow_login_shell=false`, so a profile cannot put an installed `attn` before the one under test, and one disabled entry for each skill under `~/.agents/skills`. The `claude` cell runs with code mode off. The driver gives `claude_executable` a wrapper that adds `"CLAUDE_CODE_EXPERIMENTAL_CODE_MODE": "0"` to the settings attn passes on the command line, because a user's settings `env` overrides the process environment.

### Cells

A cell is a harness, a setting and a place:

| Flag | Values | Default |
|---|---|---|
| `--harness` | `claude`, `claude-code-mode`, `codex`, `sigma`, each with an optional `@<model>` that sets `default_model_<harness>`, and `:<effort>` after it that sets `default_effort_<harness>` | `claude,codex` |
| `--tools` | `on`, `off`: `agent_tools_enabled` | `on`; with `--grid`, `on,off` |
| `--outpost` | `<outpost name>=<ssh destination>`, repeatable as a comma list | none: the home only |
| `--tasks` | task and probe ids, `harder` for the four harder tasks, `asks` for the five ask tasks, `probes` for every probe, or `grid` for the grid's set | the seven tasks, or `grid` with `--grid` |
| `--runs` | runs per cell | 1; with `--grid`, 3 for a task and 1 for a probe |
| `--parallel` | runs of one cell at a time | 2 |

`claude-code-mode` restarts the instance's daemon with `CLAUDE_CODE_EXPERIMENTAL_CODE_MODE=1`, so the driver runs those cells last. Claude Code asks for consent the first time code mode starts in a harness home. Answer it once in that home before you run these cells.

`sigma` turns `sigma_enabled` on, and needs `sigma` on `PATH` or `sigma_executable` set. Its model picks its path: an OpenAI model runs Sigma's own loop, a Claude model runs its Claude path. Sigma's TUI drops what is typed before its composer shows, while attn already reports the session idle, so the driver also waits for Sigma's composer on screen before it sets up the task. Remove that wait once `s-rc8kfp` keeps Sigma's typeahead.

When a run has a Sigma cell beside a Claude Code or Codex cell, the table ends with the D22 verdict: whether Sigma passes as often as the other harness. For each setting and place, and apart for the tasks (the seven and the harder), the ask tasks and the probes, it compares Sigma's pass rate with each other cell, paired by task, with a bootstrapped 95% interval. It prints `FAILS` when the whole interval is below zero and `holds` otherwise, and no verdict on fewer than five pairs, which could not show that Sigma is worse even if all of them were. `RERUN` marks a task where Sigma passed at most one of three runs and the other cell passed all three, so it needs three runs of that task.

For each `--outpost`, the driver turns `outposts_enabled` on, adds the outpost to the new instance and waits for it to connect, so the destination must take ssh without a prompt. Before it cleans the instance, it removes those outposts, which stops the instance's daemon on each outpost and leaves its pairing; with `--keep` they stay, so remove them in the instance's Settings before you clean it. On an outpost, the driver makes each run's folder over SSH and runs Claude Code, Codex and Sigma there. It skips code mode, and the driver refuses to start with a task that stages files on this machine: `coding-tests`, `present-change` and the ask tasks that copy files.

### Tasks

The seven tasks are the prototype's (`s-fnb9wr`), judged as its checks did, on the real garden:

| id | the agent is asked to | passes when |
|---|---|---|
| `progress` | note progress on its seed and report C1 with `retry-test.log` | a note, C1 reported with the log, C2 and the seed open |
| `plot-notes` | note the owner file on each open child of a plot | each open child names its file, the closed children have no note |
| `ask` | ask the user Postgres or SQLite, recommending Postgres | an open ask offers both and recommends Postgres |
| `msg-delegate` | tell `api-review` to start, then delegate the migration guide into `docs/` | `api-review`'s inbox names api-v2, one session runs in `docs/` past its folder-trust question, and it tends a seed that briefs the guide |
| `standby` | wait for review of PR #482 without keeping the session open | the seed is on standby with a note to merge #482, the standby did not keep the session open, and the agent never ran `agent close` |
| `coding` | write and run `fizzbuzz.py` | the script prints 1 to 15 right, and the agent made no attn call |
| `fail` | note a seed by a slug that two seeds share | the Safari seed has the note and the Chrome seed has none |

The four harder tasks need several steps, tools an agent must load first, or recovery from a refusal. Their prompts name no attn command or tool. Each passes on the end state in the garden, the asks or the folder, and `open-ask-refusal` and `coding-tests` also read the attn calls of the run:

| id | the agent is asked to | passes when |
|---|---|---|
| `seed-lifecycle` | plant a seed with two checks, take it on, attach `runbook.md`, report both checks with `rotation.log` and close the seed | one seed has the title, the session tended it, both checks are reported with the log, the runbook is on the seed and still in the folder, and it is harvested with a reason |
| `plot-order` | plan a search rewrite as three pieces, one waiting for another | one plot whose body holds the plan has three child seeds, each with a sentence of its own, only the ranking one is blocked, by the indexing one, and none is started |
| `open-ask-refusal` | close three shipped seeds, one of which has an open ask of the agent's own that shipping made moot, and leave `import-customers` alone | all three are harvested with their reasons, that ask is withdrawn, `import-customers` is still growing with its ask open, no close was forced, and attn refused at most three closes for a reason other than usage |
| `coding-tests` | fix code across several files until 16 tests pass | the 16 tests pass, the tests are unchanged, and the agent made no attn call |

In `open-ask-refusal`, attn refuses the harvest of a seed with an open ask, and names the ask, the command that reads it and the command that withdraws it. An agent that asks the user instead, or that its harness stops from withdrawing the ask, fails.

`msg-delegate` saves `docs/` as trusted in the Codex home before the prompt, because Codex trusts exactly the folder it was asked about, so the delegate starts healthily. It needs `--tool-home` for Codex, as [Harness homes](#harness-homes) says. `msg-delegate-stalled` leaves `docs/` untrusted, for Codex cells only (Claude Code trusts `docs/` through its parent): it passes when, besides, the delegate is still at its folder-trust question and the agent's answer or an ask names that question. It is outside the grid and the groups: run it with `--tasks msg-delegate-stalled --harness codex`.

`ask-still-matters` is the opposite of `open-ask-refusal`, outside the grid and the groups: run it with `--tasks ask-still-matters`. The agent is asked to close two seeds, one of which has an ask whose answer the work still needs, so it passes when the other seed is harvested and this one is still growing with its ask open, with no forced close.

The five ask tasks (`instance/asks.ts`) put a decision to the agent without naming attn, asks or any kind of picture, so they measure whether the agent opens an ask on its own and whether the ask shows what the decision turns on. Each copies its files from `instance/fixtures/asks/` into the run's folder. A run with no open ask fails, and every verdict names the asks opened and the visuals they show. A question offers the options when each matches a different choice. A Mermaid fence counts only when the app draws it whole: before a run with an ask task, the driver builds `native/crates/attn-app/examples/mermaid_check.rs` with cargo, which reads a source with the app's renderer.

| id | the agent is asked to | passes when an open ask |
|---|---|---|
| `ask-flow` | get the user's choice between two retry designs in `design-notes.md`, with a recommendation | shows a Mermaid diagram the app draws whole, and a question offers both designs with one recommended |
| `ask-layout` | get the user's pick between the two layouts captured in `shots/` | shows both PNGs as markdown images, and a question offers both layouts |
| `ask-tradeoff` | get the user's call among the three cache strategies in `options.md` | shows a table that compares the three (three or more rows, or two rows across four or more columns), or a diff, and a question offers all three |
| `ask-simple-control` | get the user's pick of 30 or 90 days' audit-log retention, recommending 30 | offers both and recommends 30 days, with no Mermaid diagram and no image |
| `ask-svg-guard` | get the user's decision on splitting the worker shown in `arch.svg` | shows the architecture as a Mermaid diagram the app draws whole or a raster image, embeds no SVG, remote image or other type attn does not copy, and asks a question with choices |

The probes are the former `scripts/agent-eval.mjs`, each a plain request. A probe passes when a call of the right command succeeds, through the CLI or the tool of the same name, and the end state is what the request asked for. `callout` needs the app on screen, so a refusal other than for usage passes. Probes that name a seed or an agent find it staged. A probe or task that changes home-wide state (`roles`, `chief-roles`, `change-setting`) runs one at a time and the driver restores the state after each run. The `chief-*` probes start the profile's Chief with `attn crew start chief`. `present-change` turns `code_review_enabled` on, clones attn with a real change uncommitted, and passes when the agent read the review reference and opened a document that `attn review check` passes. Add a probe to `instance/probes.ts` when you add a capability to the intent map or the launch prompt.

## Run the grid

The grid is check C6: Claude Code and Codex at the home, with the tools on and off. Run it in one command:

```sh
bun scripts/agent-bench/instance/bench.ts --instance bench --grid --tool-home ~/attn-agent-bench-home \
  --harness claude@<model>:<effort>,codex@<model>:<effort>
```

`--grid` sets `--tools on,off` and the tasks: the seven, the harder and the ask tasks at three runs each, and the 13 probes in `GRID_PROBE_IDS` (`instance/probes.ts`) at one run each. The set leaves out probes whose family a task or another probe in the set covers, and every `chief-*` probe but `chief-journal`. Over the two harnesses and two settings, that is 16 tasks at 12 runs each and 13 probes at 4 each: 244 runs. Flags given beside `--grid` override it, so one cell is the same command with one `--harness` and one `--tools`. Sigma and code mode are outside the grid; add them with `--harness`. The grid's staged tasks keep it on the home. The cells of one task run one after another, in an order that reverses from task to task, so a drift in the provider hits them alike.

What the grid costs, from 29 runs on 2026-11-25 (the harder tasks, `progress` and `ask-tradeoff`, with Sonnet 5.5 and GPT-6.2 Sol at medium effort): the prompt's turn took 8 to 24 s on Claude Code and 14 to 45 s on Codex, and a run took about 26 s on average with its launch, setup and judging. A Claude Code run used 126k to 365k input tokens, mostly cache reads, and cost $0.09 to $0.16 at API prices, $0.12 on average. A Codex run used 76k to 378k input tokens. attn has no price for that model. The grid has 122 Claude Code runs, so on Sonnet it costs about $15 at API prices if its other tasks and probes cost what these did. With two runs of a cell at a time, the grid is 180 rounds one after another, about an hour and a half with the cargo build and slower tasks such as `msg-delegate`. On subscriptions it spends that share of your plan's limits.

## Run the container driver

You need Docker, bun, a Claude sign-in, and a Sigma checkout with `bun install` done: `SIGMA_REPO`, by default `~/projects/victor/sigma`. Its packages serve the tools, and its `node_modules` hold the Agent SDK.

```sh
cd scripts/agent-bench/container
bun bench.ts                                  # 5 tasks x 2 models x 5 runs, 3 at a time
bun bench.ts --tasks chore --models claude-sonnet-5-5 --runs 1
bun bench.ts --role bare                      # the spikes' instructions: no role that makes delegating the agent's job
```

`--role member`, the default, appends a crew member's sentence that makes delegating the agent's job. `--role bare` leaves it out. Without that role, the delegation reference stops most runs before they delegate.

Each run's Claude Code state lives in that run's own config folder, so nothing is written to `~/.claude/projects`. On macOS the keychain signs that folder in. On Linux the sign-in lives inside the config folder, so export `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`, or pass `--claude-config ~/.claude` to run in your own config with no user settings loaded.

The first run builds the image `sigma-bench-computer:1` from `Dockerfile`. Each run starts its own container with no network, mounts the checkout read-only, and removes the container by the id it captured. Results land in `container/runs/<stamp>/`: one folder per run with `transcript.jsonl` (every SDK message), `attn.json` (every attn call and its reply) and `result.json`, then `results.json` and `table.md`.

| File | What it is |
|---|---|
| `bench.ts` | the runner: containers, the Agent SDK options, usage totals, the table |
| `computer-mcp.ts` | Sigma's tools (`read`, `grep`, `glob`, `write`, `edit`, `apply_patch`, `bash`) over MCP on stdio, run inside the container with `docker exec -i` |
| `attn-mock.ts` | the attn tool: frozen help, `delegate --repo` with read grants, `ask` recorded, the rest canned |
| `tasks.ts` | the five tasks, each a setup, one prompt per process and a check |
| `fixture-*.txt` | frozen `attn --help`, `attn delegate --help` with `--repo`, `attn ask --help`, and the delegation reference |

| id | prompt, in short | passes when |
|---|---|---|
| `delegate-fix` | "In acme/calc, `add_all` drops the last number. Get it fixed." | a delegation resolves to `github.com/acme/calc` |
| `missing-grant` | the same for private `acme/billing`, with no `read_repo` grant | an `attn ask` names `read_repo` and `acme/billing` |
| `two-repos` | add a field in `acme/api` and show it in `acme/web` | delegations to both repositories |
| `chore` | top three ERROR codes across six logs into `~/report.txt`, clean logs into `~/logs/clean/` | the report and both folders are exact |
| `resume` | process 1 is told a code word; process 2 resumes the session and writes it to `~/codeword.txt` | the file holds the word and nothing else does |
