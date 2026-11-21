# Agent bench

The bench runs real agents on short tasks and judges each run by its end state, with pass counts, turns, tokens and cost per cell. It has two drivers:

- `instance/` starts real Claude Code, Codex and Sigma sessions on a non-production attn instance. With `agent_tools_enabled` on, the agents reach attn's real `/mcp`. With it off, they reach the real CLI. This driver runs the task grid of the plot `attn-tools-over-mcp-agents-attn` (`s-ct56cj`, check C6).
- `container/` drives Claude Code through the Claude Agent SDK, with its own tools off, Sigma's shell and file tools on a throwaway container, and a mocked attn. It measures a limited agent, such as a crew member that reaches attn only through tools.

Both spend model tokens on your subscriptions, so CI never runs them. A person runs them.

## Run the instance driver

You need `make build` done in this checkout, bun, Go, and signed-in `claude` and `codex` on `PATH`. Run from the repository root:

```sh
make build
bun scripts/agent-bench/instance/bench.ts --instance bench --tasks standby --harness claude,codex --tools on,off
```

The driver starts the instance's daemon with headless model tasks off, runs every cell, prints the table, and ends with `attn instance clean <name>`. Pass `--keep` to leave the instance running for inspection.

`--instance` must name a new instance, because the run changes the instance's settings and cleans it. The driver asks attn for the canonical name, so `Bench` and ` bench ` both mean `bench`. Before it runs anything, it refuses the default and `dev` instances and any instance that `attn instance list` already shows. To run again on a name you kept, clean it first with `attn instance clean <name>`.

Each run gets its own profile, so seeds and slugs never collide between runs, and its own folder under the system temp folder. The driver starts the session through the daemon as the app does (`spawn_session` with `scripts/wsctl`). It waits for the prompt and sets up the task, acting as the user or as the agent's own session. Then it types the prompt into the terminal and waits until the session is back at its prompt or has closed itself. Before judging, it reads the garden, the asks and the inboxes through the instance's CLI.

The numbers come from attn itself:

- Input and output tokens and cost come from `attn session show --json` for the session the prompt went to. Cost is at API prices. A model attn has no price for, such as Codex's, shows as `+ unpriced`. Other sessions a run starts, such as the delegate in `msg-delegate`, are stopped once the run is judged and not counted.
- Turns, attn calls, searches and help reads come from `attn session transcript --json`. An attn call is a tool of attn's server, a tool named inside a code-mode script, or an `attn` command the agent ran in its shell.

Each run keeps `transcript.jsonl`, `calls.json`, `screen.txt` and `result.json` in `instance/runs/<stamp>/<task>/<cell>/<n>/`, beside `results.json` and `table.md` for the whole run.

### Harness homes

By default, the agents run with your own `~/.claude` and `~/.codex`. Your global `CLAUDE.md`, plugins and MCP servers then add to every Claude Code prompt, which raises input tokens, and Claude Code adds an entry for each run's folder to your `~/.claude.json`, as it does for any folder it runs in. To measure attn alone, make a bench home once and pass `--tool-home <dir>`. The driver then starts the daemon with `CLAUDE_CONFIG_DIR=<dir>/.claude`, `CODEX_HOME=<dir>/.codex` and `ATTN_TOOL_HOME=<dir>`. To make the home, sign each harness in:

```sh
CLAUDE_CONFIG_DIR=~/attn-agent-bench-home/.claude claude    # then /login
CODEX_HOME=~/attn-agent-bench-home/.codex codex login
```

Codex asks once per folder whether to trust it. The driver answers that screen for the folders it makes, and at the end it removes those folders' entries from the home's `config.toml`.

### Cells

A cell is a harness, a setting and a place:

| Flag | Values | Default |
|---|---|---|
| `--harness` | `claude`, `claude-code-mode`, `codex`, `sigma`, each with an optional `@<model>` that sets `default_model_<harness>` | `claude,codex` |
| `--tools` | `on`, `off`: `agent_tools_enabled` | `on` |
| `--outpost` | `<outpost name>=<ssh destination>`, repeatable as a comma list | none: the home only |
| `--tasks` | task and probe ids, or `probes` for every probe | the seven tasks |
| `--runs` | runs per cell | 1 |
| `--parallel` | runs of one cell at a time | 2 |

`claude-code-mode` restarts the instance's daemon with `CLAUDE_CODE_EXPERIMENTAL_CODE_MODE=1`, so the driver runs those cells last. Claude Code asks for consent the first time code mode starts in a harness home. Answer it once in that home before you run these cells.

`sigma` turns `sigma_enabled` on, and needs `sigma` on `PATH` or `sigma_executable` set. Its model picks its path: an OpenAI model runs Sigma's own loop, a Claude model runs its Claude path.

For each `--outpost`, the driver turns `outposts_enabled` on, adds the outpost to the new instance and waits for it to connect, so the destination must take ssh without a prompt. Before it cleans the instance, it removes those outposts, which stops the instance's daemon on each outpost and leaves its pairing; with `--keep` they stay, so remove them in the instance's Settings before you clean it. On an outpost, the driver makes each run's folder over SSH and runs Claude Code, Codex and Sigma there. It skips code mode, and refuses `present-change`, whose clone is made on this machine.

### Tasks

The seven tasks are the prototype's (`s-fnb9wr`), judged as its checks did, on the real garden:

| id | the agent is asked to | passes when |
|---|---|---|
| `progress` | note progress on its seed and report C1 with `retry-test.log` | a note, C1 reported with the log, C2 and the seed open |
| `plot-notes` | note the owner file on each open child of a plot | each open child names its file, the closed children have no note |
| `ask` | ask the user Postgres or SQLite, recommending Postgres | an open ask offers both and recommends Postgres |
| `msg-delegate` | tell `api-review` to start, then delegate the migration guide into `docs/` | `api-review`'s inbox names api-v2, one session runs in `docs/`, and a seed briefs the guide |
| `standby` | wait for review of PR #482 without keeping the session open | the seed is on standby with a note to merge #482, and the agent never ran `agent close` |
| `coding` | write and run `fizzbuzz.py` | the script prints 1 to 15 right, and the agent made no attn call |
| `fail` | note a seed by a slug that two seeds share | the Safari seed has the note and the Chrome seed has none |

The probes are the former `scripts/agent-eval.mjs`, each a plain request that passes when the agent reaches the right command, through the CLI or the tool of the same name. The command runs for real, so a refusal still counts: a probe proves what the agent finds, not that the command works. The `chief-*` probes start the profile's Chief with `attn crew start chief`. `present-change` turns `code_review_enabled` on, clones attn with a real change uncommitted, and passes when the agent read the review reference and opened a document that `attn review check` passes. Add a probe to `instance/probes.ts` when you add a capability to the intent map or the launch prompt.

## Run the grid

The grid is check C6: every task on Claude Code with code mode off and on, Codex, and Sigma on both paths, at the home, plus Claude Code, Codex and Sigma on an outpost, each with the setting on and off. Run it in one command:

```sh
bun scripts/agent-bench/instance/bench.ts --instance bench --grid --tool-home ~/attn-agent-bench-home \
  --harness claude,claude-code-mode,codex,sigma@<OpenAI model>,sigma@<Claude model> --outpost <outpost>=<ssh destination>
```

`--grid` sets `--tools on,off`, the seven tasks and `--runs 2`. That is 16 cells, 112 runs a pass, 224 runs in all. One cell is the same command with one `--harness`, one `--tools` and no `--outpost`.

The table ends with the D22 verdict: for each task, setting and place, Sigma's pass rate against the best of Claude Code's and Codex's. A task where Sigma falls short is fixed and rerun, not explained.

What the grid costs, from the proof runs of 2026-11-21: a Claude Code run with Sonnet 5.5 took 30 to 60 s, about 175k to 230k input tokens (mostly cache reads) and $0.13 to $0.15 at API prices. An Opus 5.5 run cost $0.51. Codex took 113k to 128k input tokens a run. About half the grid's runs are Claude Code or Sigma's Claude path, so a grid on Sonnet costs about $15 to $20 at API prices, and about three times that on Opus. On subscriptions it spends that share of your plan's limits. With two runs at a time, the grid takes about two hours.

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
