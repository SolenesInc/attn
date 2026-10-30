Make the Garden easy for agents to read and write: finding the right content, reading a plot as a whole, editing bodies, handling checks, and closing work. This plot holds the transcript study behind that goal, the agreed sequence, and the eval approach. Its children are the steps.

## Evidence base

Every `attn seed`/`attn ask` call in the user's Claude Code and Codex transcripts (Aug–Oct 2026, mostly October): 18.8k calls, 758 sessions. Six themed studies read them: reading, writing, plots, checks, lifecycle and user corrections, errors and harness differences. Their reports are attached as `findings-*.md`, with counts, quotes (episode file names) and eval-case candidates. `extract.py`, `episodes.py` and `stats.py` rebuild the corpus from `~/.claude/projects` and `~/.codex/{sessions,archived_sessions}`. Numbers below are measured unless marked.

## Findings by root cause

F1. Silent wrong results (bugs, still present at 961a26579):
- Seeds older than the newest 1000 lose their edges in `show`, plot progress and `ready` (`gardenSnapshotLimit` in internal/daemon/garden.go). `link` and `search` read the whole garden. Verified live: `s-4jrfbx` stores part-of `s-2mgjx9`, `show --json` returns no relations. One agent planted a duplicate to work around it (vent v-qefa70).
- `edit -m -` with empty stdin wipes the body (cmd/attn/seed.go:915), and edits keep no history. 3 confirmed wipes from failed pipelines.
- One shared flag set for every verb (`newSeedFlags`): `plant -f` plants an empty body (15 calls, `s-q743zh`), `ls --plot` is ignored (verified), wrong-verb flags pass silently.
- The 64 KiB socket frame (`maxInitialSocketFrameBytes`) is the real body and plot limit, and its error names the transport. Plans get trimmed to fit (`s-fqkbfa`, `s-fs1txb`). Largest body today: 62.9 KB.
- Codex: read verbs fail with "terminal … is not showing a session" and `--profile` doesn't help (44 failures, 27 never recovered).

F2. Help and errors. 739 help lookups, mostly proactive. Verb help prints the whole 10.7 KB catalog and exits 2. Usage errors print the catalog after the error line, so `| tail` hides the error; 80% of Claude-side attn errors sat in Bash calls that exited 0. Errors that name the limit, the value and the next command recover in one step >85% of the time; the rest mostly don't.

F3. Reading is not incremental. `show` and `notes` are ~90% of bytes read. 55% of `show` calls re-read a seed the session already read, because the bell says "read it with attn seed show". Body is 69% of show's bytes on busy seeds. The "freshest handoff" leads show even when 20–161 notes old, and the Claude harness's 2 KB preview of large outputs often holds only that handoff. The delegation opening says "show, including its notes", giving 1,008 show-then-notes pairs. A grep over notes missed an answer the user had given ("i answered already").

F4. No plot-level view. 727 `for s in …; show|notes` loops; top orchestrators poll 110–130 times per session. Reading one 9-child plot costs 21 calls, ~178 KB. 68% of child sessions (Codex 91%) open by reading the parent, median 18 KB, up to 68 KB, and cut sections by hand. Slugs are refused as ids. Plot payloads can't nest under a plot or record discovered-from, so plots are followed by fix-up edits and links. `blocks` gets written backwards because plans think in "waits for".

F5. Editing is a script every time. 74% of 592 body edits are partial changes resent as the whole body, via `show --json` plus python with hand-written exactly-once asserts. 45 seeds were edited from more than one session, and the last writer silently wins. Builders put findings in notes instead of bodies (Codex 70:1 notes to edits; seeds with 150 notes and a stale 4 KB body).

F6. Checks. Check ids are unique per plot tree and nothing shows which are taken, so agents dump the 5 MB garden and jump to C101+. The 400-char summary cap is discovered by failing (12% of reports). Attach-then-report is one intent in two commands, and re-attaching changed evidence is refused (37 times, leaving `-final`/`-v2` copies). Builders invent checks on their own seed because the dispatch says "Report each of the seed's checks" unconditionally (internal/prompts/content/delegation/seed.md).

F7. Lifecycle: who closes. 88 pre-emptive `--force` harvests: orchestrators close children their builders still hold. Re-harvest errors and points to replant, the wrong way out. `--when-merged` rejects `-m` (21% of uses). Closing a planning seed that became a plot succeeds, then warns. Tending a plot doesn't bring child updates without `watch`. The user asked "can I close you / is your context captured?" in 28 sessions, and agents often found something not yet durable.

F8. Search is substring-based (`strings.Contains`), so "port" matches "report". `fix ci` matches 1,156 seeds, unranked, closed seeds included.

What works and must survive: limit errors as a template; refusals that hand over the exact next command; search-before-plant (95%); heredoc `-m -` (quoting failures ~0.1%); plot progress lines; plot payload validation.

## Sequence

D1. Fix the interface before tuning any guidance. Hillclimbing text now would tune it around `show --json` parsing, full re-reads and other workarounds this work removes.

1. Fix F1 and F2 directly, each with a regression test that reproduces what agents hit. No eval: these are wrong results and deterministic output. Planned as its own plot under this seed.
2. Design body editing (F5) by building options and measuring them with small real-agent evals. A prototype child of this seed.
3. Reshape the rest (F3, F4, F6, F7, F8) the same way: options judged on small evals.
4. Last, build the full eval below and hillclimb the text surfaces: the attn skill's garden.md, internal/prompts/content/garden.md, delegation/seed.md, `attn seed guide` and help text, bell text. Objective: cost (calls, bytes read, errors) at equal task success.

D2. Small evals run four arms: Claude Sonnet at medium effort, Claude Opus at medium (fewer runs than Sonnet), Codex gpt-6.1-sol at medium, Codex gpt-6.1-terra at high. The newest models often change text with scripts instead of their harness's edit tools, so no design may rely on a harness tool's behavior for correctness.

## Eval sketch

- Cases from the attached reports' "Eval cases" sections (~36 candidates), each traceable to a transcript. Train/test split.
- Runner: a non-production instance (docs/instances.md). Fixture gardens are built through the CLI from copies of real seeds, never the production garden. Headless `claude -p` and `codex exec` as separate arms, because the two harnesses behave very differently.
- Graders: programmatic first, from final garden state over `--json` (body diff, edges, checks, states, artifacts) plus counts from the agent transcript (attn calls, failed calls, help lookups, bytes read). A judge on a different model only for answer-type cases (plot status, "what's decided").
- Lesson from the crew-memory eval (`s-wxcdtv`, runner removed in #860): it grew a user simulator, a sandbox and digest approvals, cost ~$25, and found no headroom. Keep this one single-turn with no simulator, pilot the cost per case first, and get the run budget approved.

## Related seeds

`s-q743zh` (plant ignores unknown flags), `s-fqkbfa` (plot socket frame), `s-fs1txb` (vent frame), `s-vaqk22` (find attachments; mostly fixed by #851, can be harvested).
