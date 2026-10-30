The garden holds the work. Syntax: `attn seed <verb> --help`. The catalog is `attn seed --help`.

WRITING A BODY

A seed body is a prompt for the agent who will do the work. A new delegation
stores its brief as the body. A delegation at an existing seed names that seed;
the agent reads its current body and notes. Handover adds a short next-step note
and transfers ownership before the successor starts.
Write every body, including plot children, for someone without this conversation.
Do this even when you plan to do the work yourself.

- Lead with the task and outcome: what to do and what done looks like.
- Give starting context: the problem, relevant paths or evidence, and constraints.
  Point to shared context and say what the reader needs from it.
- Define scope: what this seed owns, what it may change and what it must leave
  alone, which decisions need the user, and which actions wait for them, such
  as a deployment outside the authorized delivery step. Follow the
  `attn-workflow` skill's Delivery reference for repository delivery.
- Specify completion: the checks that verify the result, defined on the seed as
  CHECKS below describes and cited in the body by id, where to leave the
  deliverable, and what the report back contains.

Include an agreed design when there is one. Leave implementation choices open
where they are still for the tender to make. Scale the detail to the task; a few
sentences can be a complete prompt.

Keep the agreed task current in the body. What a spike or investigation
established goes in the body too, as findings below the task, because later
work builds on it. Progress, other evidence and handoffs go in the log. When the
task changes, rewrite the body so it reads as the current task. Use `--note`
on the edit to record what changed.

EDITING A BODY

Export the body, edit the file with any tool, then submit it:

    attn seed export s-7k3f9m
    # edit s-7k3f9m.md; keep its first line
    attn seed edit s-7k3f9m -m - --note "what changed" < s-7k3f9m.md

The stamp names the seed and its body hash. If another edit changed the body,
export again and reapply your change. Empty bodies are refused. Each edit saves
an undoable body version and one log entry; --note adds your explanation there.
Read the entries with `attn seed notes s-7k3f9m`. Restore the previous version
with `attn seed rollback s-7k3f9m`, or a named one with
`attn seed rollback s-7k3f9m 2`. Rollback saves a new version too.

WRITING A PLOT

A plot holds the shared plan. Its children are units of work that can each be
tended on their own; a child can itself be a plot. Keep shared decisions in the
parent. Each child states its own assignment and names any parent section,
sibling result or artifact it needs to read, with a reason. Those references
save repetition, but their text is not included automatically in a delegation.
A child's `show` lists the parent's headings and the item ids under each, and
`attn seed show <plot> --section <heading|D3>` reads one, so name parent
sections by heading and decisions or findings by id.

Use `blocks` only when one child needs another's result. Otherwise leave them
parallel. The children's states record progress, so the plan needs no checklist.

Write the parent for an implementer starting fresh and a user reviewing the
direction: the goal, the decisions the user might change, what spikes and
investigation established, the design, and how the children together complete it. The design shows ownership, interfaces,
behavior and state, each as the smallest picture that explains it rather than
prose alone. The `attn-workflow` skill's Planning reference gives each part's
required form with examples; follow it when that skill is installed.

Example: move session search to the daemon while preserving its behavior.
The endpoint child records a contract that the app child needs before starting.

    attn seed plot -f plan.json

```json
{
  "title": "Search moves to the daemon",
  "body": "Move session search from the app to the daemon, so every client gets the same results from one ranking. Out of scope: changing what matches or how results rank.\n\n## Search behavior\nPreserve the current matching, ordering and keyboard behavior in app/src.\n\n## Decisions\nD1. Ranking moves to the daemon with matching, so the app renders results in the order it receives them. Open: whether search also covers ended sessions. The endpoint tender may decide, and nothing waits on it.\n\n## Tradeoffs accepted\nWe accept two search implementations between the two delivery steps, in exchange for each child landing on its own. The client index stays until the app uses daemon results.\n\n## Findings\nRead at a1b2c3d.\nF1. The app breaks match ties by recency (app/src/search/rank.ts). A quick check ranking by match score alone reordered equal matches. The endpoint keeps recency as the tie-breaker. Dropping it looks harmless and changes results.\n\n## Design\nOwnership:\n\n    internal/daemon/search.go          matches and orders sessions\n    internal/protocol/schema/main.tsp  SearchSessions message; generated types follow\n    app/src/search/                    sends queries and renders results\n\nInterfaces:\n\n    SearchSessions{query_id, query} -> {query_id, sessions: [{id, title, rank}]}\n\nA new wire message: edit main.tsp and run make generate-types; commit the generated types and protocol schema hash.\n\nBehavior:\n\n    app    -> daemon: SearchSessions{query_id, query}\n    daemon -> app:    ranked sessions for that query_id\n\nState:\n\n    latest query_id and keyboard selection (app, search store)\n      set by:      each query; selection moves with the keyboard\n      on reply:    a reply for an older query_id is dropped\n      on failure:  the query and selection stay; the results show the error until the next query\n      on restart:  starts empty; nothing persisted\n    daemon: none added; each query reads the live session set\n\n## Execution\nThe endpoint child lands first and records the contract. The app child follows in its own delivery step.\n\n## Completion\nThe checks are on the seeds. C2's recording is attached to the app child, and its report cites it.\n\n## Alternatives rejected\nD1. The daemon matches and the app keeps ranking: each client would rank on its own, and that is what this plot removes.",
  "checks": [
    {"id": "C3", "shared": true, "text": "The child is delivered as the repository delivers changes, with that step's own checks passing. The report links the pull request or the landed commits."}
  ],
  "children": [
    {
      "title": "Daemon search endpoint",
      "body": "Add session search in internal/daemon. Read the parent plot's Search behavior, Decisions, Findings (F1) and Design sections for the SearchSessions contract, the ranking decision and its tie-break, and the protocol files, and trace the existing search in app/src to preserve its behavior. Follow repository protocol guidance. Test empty queries, matching, ordering and session changes (C1). Record the query and response contract on this seed for the app child. App integration belongs to that child.",
      "checks": [{"id": "C1", "text": "The endpoint's tests pass for empty queries, matching, ordering and session changes. Test run on this seed."}],
      "blocks": ["app-calls-endpoint"]
    },
    {
      "title": "App calls the endpoint",
      "body": "Route session search in app/src through the daemon, then remove the obsolete client index. Read the parent plot's Search behavior and Design sections and the Daemon search endpoint seed's recorded API contract. Preserve keyboard selection and prevent stale results when queries overlap. Verify search, rapid query changes and keyboard navigation in the running app (C2).",
      "checks": [{"id": "C2", "text": "In the running app, search, rapid query changes and keyboard navigation behave as before. Recording on this seed."}]
    }
  ]
}
```

The plot's C3 is shared, so each child proves it with its own report. Each child's `blocks` array names the siblings that wait for it. Use the sibling's
slug or full title. Pass `-` instead of a file to read the payload from stdin.

    attn seed plant "..." --part-of <plot>   add another child
    attn open <plot>                        read the rendered plan

Before planting or delegating, read each body with its named references as a
fresh agent. Can you tell what to do, where to start, which constraints apply,
and how to establish completion? Fill any gaps before handing it off; a part you
cannot fill means the work is not scoped yet. Read
referenced seed bodies with `attn seed show <id>`.

When an agreed decision changes, rewrite every part of the shared plan that
depends on it, and note why. Plant deferred work under the plot or beside it so
it remains tracked.

CHECKS

A check is a numbered statement (C1) that a seed must prove, naming the
evidence that proves it. attn keeps each check on the seed with its latest
report and verdict, so define checks as data, not as bullets in the body:
`checks` in plot JSON, `--check "C1=<statement>"` on plant, or `attn seed check
add` later. The body cites checks by id and keeps what a check cannot hold,
such as an exemption or where long evidence goes. Ids are unique across a plot
tree and never reused: a removed check keeps its id, and restore brings it back.

On a plot, `"shared": true` puts a check on every seed below it that has no
children; each proves it with its own report, and the plot rolls up how many
are confirmed. A check only one child needs goes on that child. A plot check
without `shared` is the plot's own.

    attn seed check report <id> <C#> -m "<summary>" --evidence <artifact|url> --commit <rev>
    attn seed check confirm <id> <C#> [-m "<how you verified it>"]
    attn seed check reject <id> <C#> -m "<what would prove it>"

Whoever does the work reports each check as they prove it, with evidence
attached to the seed or an http(s) URL. A later run is a new report: it
replaces the last one and clears its verdict. Attach the new evidence under
the same name to replace the old file; `attn seed show` marks a report whose
cited file changed after it, until the next report. A check that does not apply gets a report saying why and citing what
shows it. Whoever judges the work confirms or rejects each report, and may
confirm a check with no report that they verified themselves. Checks never
block a move; harvest names the ones still open or rejected.

WHAT DONE IS, BY DELIVERABLE

Adapt the assignment and its completion check to the work:

    code       The requested behavior exists and required verification passes.
               Give the starting points, constraints and any agreed design.
    bug fix    The cause is found and fixed, with a regression test. Give the
               symptom, reproduction and known evidence; leave an unproven fix open.
    research   A sourced answer supports a decision. State the question, the
               decision it informs and the expected report.
    docs       The intended audience can understand or act on the result.
               Name what it must explain and which existing text it replaces.
    refactor   The named code issue is gone and behavior is preserved.
               Identify the issue and the checks that establish preservation.
    spike      The result answers a design question or lets the user judge the
               experience. What it taught is written as findings into the plan
               it informs, or below the task in its own body until a plan
               exists. State what it should teach; its code is disposable
               unless the user decides to keep it. A prototype is a spike
               whose result is options for the user to choose between.

Harvest when the outcome and required verification in the body are complete.
Repeating a move to the same state succeeds without rewriting it or ringing
anyone. A different reason is kept as a quiet note. To change a harvested seed
to withered, or a withered seed to harvested, replant it first. Moves print who
they rang.

A plot closes after its direct children. Harvest and wither refuse while any
child is planted, growing or dormant, including with --force or --when-merged.
Replant hands back a plot when your part is done; otherwise close its children
or move follow-ups out first. The last child closing leaves a ringing note on
the plot and names it in the output; the plot keeps its state. A plot's tender
hears its descendants' updates without a watch, until the claim ends.

When the only thing left is a pull request merging, say so once and let attn
close it:

    attn seed harvest <id> --when-merged [<pr-url>] -m "<outcome and verification>"
                                                  with no url, this session's
                                                  single open pull request
    attn seed harvest <id> --when-merged --clear      take the arming back

A seed you were growing goes dormant and lets go of your claim; either way it
points at the pull request and harvests itself with your reason when the merge
lands. Without -m, the reason is "PR #N merged: <title>". The command merges
nothing. A pull request closed without merging clears the arming instead.
Reach for it only when the merge is genuinely the last step: review notes to
answer, docs to write, or evidence to attach are work, and work belongs to a
tender.

ARTIFACTS

Bring a local file into durable seed ownership with an explicit Move or Copy:

    attn seed attach <id> --path <file> --move
    attn seed attach <id> --path <file> --copy
    attn seed detach <id> --path <filename> --to <destination>

Move is recommended: the seed owns the one canonical editable file. Copy leaves
the source alone. A tracked Git file must use Copy. Attaching a name the seed
already has replaces that file, from the same source or another one, and the
old bytes are not kept; re-attaching an unchanged file changes nothing. Detach
never overwrites its destination. The seed-owned file survives the source
worktree, session and seed closing.
Direct visible regular files under the seed's artifact directory are membership,
so ordinary filesystem edits and renames appear in the Garden.

Repository, Notebook and URL documents remain linked where they already live:

    attn seed attach <id> --path <file.md> --repo <repository>
    attn seed attach <id> --notebook <document-id>
    attn seed attach <id> --url <url>
    attn seed detach <id> --path <file.md> --reference

The Garden labels old path associations as linked files. Bring one into seed
ownership with its explicit Move or Copy action; the link disappears only after
the transfer succeeds.

HANDOFFS AND STEERING

    attn seed note <id> -m "…" --handoff   for the next tender; attn seed tend prints it
                                            on the claim; attn seed show lists it among the
                                            newest entries, or points at it when it is older
    attn agent msg <seed-id> "…"            reaches its current or next tender;
                                            waits when none is reachable

Leave a handoff whenever you park a seed, or stop mid-thread and do not
intend to continue: outcome, evidence, next action. Long reasoning goes in an
attached artifact, not in the note. Noting does not end your session; keep
working unless blocked or done.
