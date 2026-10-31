The garden holds the work. Syntax: `attn seed <verb> --help`. The catalog is `attn seed --help`.

WRITING A BODY

A seed body is a prompt for the agent who will do the work. A new delegation
stores its brief as the body. A delegation at an existing seed names that seed,
and the agent reads the seed with `attn seed show`. Handover adds a short
next-step note and transfers ownership before the successor starts.
Write every body, including the bodies of plot children, for someone without this conversation.
Do this even when you plan to do the work yourself.

- Lead with the task and outcome: what to do and what done looks like.
- Give starting context: the problem, relevant paths or evidence, and constraints.
  Point to shared context and say what the reader needs from it.
- Define scope: what this seed owns, what it may change and what it must leave
  alone, which decisions need the user, and which actions wait for the user, such
  as a deployment outside the authorized delivery step. Follow the
  `attn-workflow` skill's Delivery reference for repository delivery.
- Specify completion: the checks that verify the result, defined on the seed as
  CHECKS below describes and cited in the body by id, where to leave the
  deliverable, and what the report back contains.

Include an agreed design when there is one. Leave implementation choices open
where they are still for the tender to make. Scale the detail to the task. A few
sentences can be a complete prompt.

Keep the agreed task current in the body. Also write what a spike or
investigation established in the body, as findings below the task, because later
work builds on those findings. Put progress, other evidence, and handoffs in the
log. When the task changes, rewrite the body so it reads as the current task,
and use `--note` on the edit to record what changed.

EDITING A BODY

Export the body, edit the file with any tool, then submit it:

    attn seed export s-7k3f9m
    # edit s-7k3f9m.md and keep its first line
    attn seed edit s-7k3f9m -m - --note "what changed" < s-7k3f9m.md

The first line of the file is a stamp that names the seed and its body hash. If
another edit changed the body, export again and reapply your change. attn
refuses an empty body. Each edit saves an undoable body version and one log
entry, and `--note` adds your explanation to that entry.
Read the entries with `attn seed notes s-7k3f9m`. To restore the previous
version, run `attn seed rollback s-7k3f9m`. To restore a named version, run
`attn seed rollback s-7k3f9m 2`. Rollback saves a new version too.

WRITING A PLOT

A plot holds the shared plan. Its children are units of work, and a tender can
pick up each one on its own. A child can itself be a plot. Keep shared decisions
in the parent. In each child, state the child's own assignment and name each
parent section, sibling result, or artifact the child needs to read, with a
reason. Those references save repetition, but a delegation does not include
their text automatically.
A child's `show` lists the parent's headings and the item ids under each, and
`attn seed show <plot> --section <heading|D3>` reads one section. Name parent
sections by heading and decisions or findings by id.

Set `after` on a child only when the child needs a sibling's result. Otherwise
leave the children parallel. The children's states record progress, so the plan
needs no checklist.

Write the parent for an implementer who starts fresh and a user who reviews the
direction. Include the goal, the decisions the user might change, what spikes
and investigation established, the design, and how the children together
complete the goal. In the design, show ownership, interfaces, behavior, and
state, each as the smallest picture that explains it rather than prose alone.
The Planning reference in the `attn-workflow` skill gives the required form of
each part, with examples. Follow it when that skill is installed.

Example: move session search to the daemon while preserving its behavior.
The tender of the endpoint child records a contract that the app child needs
before the app child starts.

    attn seed plot -f plan.json

```json
{
  "title": "Search moves to the daemon",
  "body": "Move session search from the app to the daemon, so every client gets the same results from one ranking. Out of scope: changing what matches or how results rank.\n\n## Search behavior\nPreserve the current matching, ordering and keyboard behavior in app/src.\n\n## Decisions\nD1. Ranking moves to the daemon with matching, so the app renders results in the order it receives them. Open: whether search also covers ended sessions. The endpoint tender may decide, and nothing waits on it.\n\n## Tradeoffs accepted\nWe accept two search implementations between the two delivery steps, in exchange for each child landing on its own. The client index stays until the app uses daemon results.\n\n## Findings\nRead at a1b2c3d.\nF1. The app breaks match ties by recency (app/src/search/rank.ts). An experiment that ranked by match score alone reordered equal matches. The endpoint keeps recency as the tie-breaker. Dropping recency looks harmless and changes results.\n\n## Design\nOwnership:\n\n    internal/daemon/search.go          matches and orders sessions\n    internal/protocol/schema/main.tsp  SearchSessions message and its generated types\n    app/src/search/                    sends queries and renders results\n\nInterfaces:\n\n    SearchSessions{query_id, query} -> {query_id, sessions: [{id, title, rank}]}\n\nA new wire message needs an edit to main.tsp and a run of make generate-types. Commit the generated types and the protocol schema hash.\n\nBehavior:\n\n    app    -> daemon: SearchSessions{query_id, query}\n    daemon -> app:    ranked sessions for that query_id\n\nState:\n\n    latest query_id and keyboard selection (app, search store)\n      set by:      each query, and the keyboard moves the selection\n      on reply:    the app drops a reply for an older query_id\n      on failure:  the query and selection stay, and the results show the error until the next query\n      on restart:  starts empty and persists nothing\n    daemon: none added, and each query reads the live session set\n\n## Execution\nLand the endpoint child first, and record the contract on its seed. Land the app child afterwards, in its own delivery step.\n\n## Completion\nThe checks are on the seeds. Attach the recording for C2 to the app child, and cite it in that child's report.\n\n## Alternatives rejected\nD1. The daemon matches and the app keeps ranking: each client would rank on its own, and removing per-client ranking is the point of this plot.",
  "checks": [
    {"id": "C3", "shared": true, "text": "The child is delivered as the repository delivers changes, with that step's own checks passing. The report links the pull request or the landed commits."}
  ],
  "children": [
    {
      "title": "Daemon search endpoint",
      "body": "Add session search in internal/daemon. Read the parent plot's Search behavior, Decisions, Findings (F1) and Design sections. They give the SearchSessions contract, the ranking decision and its tie-break, and the protocol files. Trace the existing search in app/src to preserve its behavior. Follow repository protocol guidance. Test empty queries, matching, ordering and session changes (C1). Record the query and response contract on this seed for the app child. App integration belongs to that child.",
      "checks": [{"id": "C1", "text": "The endpoint's tests pass for empty queries, matching, ordering and session changes. Test run on this seed."}]
    },
    {
      "title": "App calls the endpoint",
      "body": "Route session search in app/src through the daemon, then remove the obsolete client index. Read the parent plot's Search behavior and Design sections and the Daemon search endpoint seed's recorded API contract. Preserve keyboard selection and prevent stale results when queries overlap. Verify search, rapid query changes and keyboard navigation in the running app (C2).",
      "checks": [{"id": "C2", "text": "In the running app, search, rapid query changes and keyboard navigation behave as before. Recording on this seed."}],
      "after": ["daemon-search-endpoint"]
    }
  ]
}
```

The plot's C3 is shared, so each child proves it with its own report. A
child's `after` names the siblings the child waits for, by slug or full title.
To plant the plot inside another plot, add `"part_of": "<plot>"`. To record the
seed whose work led to this plot, add `"discovered_from": "<seed>"`. Both take
an id or a slug. To read the payload from stdin, pass `-` instead of a file.

    attn seed plant "..." --part-of <plot>   add another child
    attn open <plot>                        read the rendered plan

Before you plant or delegate, read each body and its named references as a
fresh agent would. Can you tell what to do, where to start, which constraints
apply, and how to establish completion? Fill any gaps before you hand the work
off. A part you cannot fill means the work is not scoped yet. Read referenced
seed bodies with `attn seed show <id>`.

When an agreed decision changes, rewrite every part of the shared plan that
depends on that decision, and note why. Plant deferred work under the plot or
beside it so the work stays tracked.

CHECKS

A check is a numbered statement (C1) that a seed must prove. The statement
names the evidence that proves it. attn keeps each check on the seed with its
latest report and verdict, so define checks as data, not as bullets in the body:
use `checks` in plot JSON, `--check "<statement>"` on plant, or
`attn seed check add <id> -m "<statement>"` later. `plant` and `add` number each
check with the next free id in the plot tree and print it. In plot JSON, you
write the ids yourself. In the body, cite checks by id and keep what a check
cannot hold, such as an exemption or where long evidence goes. Ids are unique
across a plot tree and never reused. A removed check keeps its id, and restore
brings it back.

On a plot, `"shared": true` puts a check on every seed below the plot that has
no children. Each of those seeds proves the check with its own report, and the
plot rolls up how many are confirmed. Put a check that only one child needs on
that child. A plot check without `shared` is the plot's own.

    attn seed check report <id> <C#> -m "<summary line>" --evidence <file|artifact|url> --commit <rev>
    attn seed check confirm <id> <C#> [-m "<how you verified it>"]
    attn seed check reject <id> <C#> -m "<what would prove it>"

If you do the work, report each check as you prove it, in one call. The
first line of `-m` is the summary line that `attn seed show` prints, at most 400
characters. The lines after it are detail, and attn keeps them in the report's
check note.
`--evidence` takes a local file, which attn copies onto the seed, an artifact
already on the seed, or an http(s) URL. A later run is a new report. It
replaces the last report and clears the verdict. Citing a changed file under the
same name replaces the old file. `attn seed show` marks a report whose cited
file changed after the report, until the next report. For a check that does not
apply, report why and cite what shows it. If you judge the work, confirm or
reject each report. You may also confirm a check that has no report when you
verified it yourself. Checks never block a move, but harvest names the ones
still open or rejected.

WHAT DONE IS, BY DELIVERABLE

Adapt the assignment and its completion check to the work:

    code       The requested behavior exists and required verification passes.
               Give the starting points, constraints and any agreed design.
    bug fix    The cause is found and fixed, with a regression test. Give the
               symptom, reproduction and known evidence. Leave an unproven fix open.
    research   A sourced answer supports a decision. State the question, the
               decision it informs and the expected report.
    docs       The intended audience can understand or act on the result.
               Name what it must explain and which existing text it replaces.
    refactor   The named code issue is gone and behavior is preserved.
               Identify the issue and the checks that establish preservation.
    spike      The result answers a design question or lets the user judge the
               experience. Write what it taught as findings in the plan it
               informs, or below the task in the spike's own body until a plan
               exists. State what it should teach. The spike's code is
               disposable unless the user decides to keep it. A prototype is a
               spike whose result is options for the user to choose between.

Harvest when the outcome and required verification in the body are complete.
If you repeat a move to the same state, the command succeeds without rewriting
the seed or ringing anyone. If you give a different reason, attn keeps it as a
quiet note. To change a harvested seed to withered, or a withered seed to
harvested, replant it first. Moves print who attn rang.

A plot closes after its direct children. Harvest and wither refuse while any
child is planted, growing, or dormant, including with `--force` or
`--when-merged`. When your part of a plot is done, replant the plot to hand it
back. Otherwise, close the plot's children or move follow-ups out first. When
the last child closes, attn leaves a ringing note on the plot and names it in
the output. The plot keeps its state. A plot's tender hears updates from the
plot's descendants without a watch, until the claim ends.

When the only thing left is a pull request merging, say so once and let attn
close it:

    attn seed harvest <id> --when-merged [<pr-url>] -m "<outcome and verification>"
                                                  with no url, this session's
                                                  single open pull request
    attn seed harvest <id> --when-merged --clear      take the arming back

If you were growing the seed, it goes dormant and attn releases your claim.
Either way, the seed points at the pull request, and attn harvests the seed
with your reason when the merge lands. Without `-m`, the reason is
"PR #N merged: <title>". The command merges nothing. If the pull request closes
without merging, attn clears the arming instead. Use the command only when the
merge is genuinely the last step. Review notes to answer, docs to write, and
evidence to attach are work, and work belongs to a tender.

ARTIFACTS

Bring a local file into durable seed ownership with an explicit Move or Copy:

    attn seed attach <id> --path <file> --move
    attn seed attach <id> --path <file> --copy
    attn seed detach <id> --path <filename> --to <destination>

Move is recommended, because the seed then owns the one canonical editable
file. Copy leaves the source alone. attn refuses Move for a tracked Git file, so use Copy. If you
attach a name the seed already has, attn replaces that file, whether the new
file comes from the same source or another one, and does not keep the old
bytes. Re-attaching an unchanged file changes nothing. Detach never overwrites
its destination. The seed-owned file stays after the source worktree, the
session, or the seed closes.
Every visible regular file directly under the seed's artifact directory belongs
to the seed, so ordinary filesystem edits and renames appear in the Garden.

Repository, Notebook and URL documents remain linked where they already live:

    attn seed attach <id> --path <file.md> --repo <repository>
    attn seed attach <id> --notebook <document-id>
    attn seed attach <id> --url <url>
    attn seed detach <id> --path <file.md> --reference

The Garden labels old path associations as linked files. To bring one into seed
ownership, use its explicit Move or Copy action. The link disappears only after
the transfer succeeds.

HANDOFFS AND STEERING

    attn seed note <id> -m "…" --handoff   for the next tender. attn seed tend prints the
                                            handoff on the claim. attn seed show lists the
                                            handoff among the newest entries, or points at it
                                            when the handoff is older
    attn agent msg <seed-id> "…"            reaches the seed's current or next tender,
                                            and waits when none is reachable
    attn seed show <id> --since <N>         only what was saved after bookmark N.
                                            show and notes print the bookmark to use next

Leave a handoff whenever you park a seed, or stop partway through the work and
do not intend to continue. State the outcome, the evidence, and the next action.
Put long reasoning in an attached artifact, not in the note. Noting does not end
your session, so keep working unless you are blocked or done.
