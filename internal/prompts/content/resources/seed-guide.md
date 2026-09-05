The garden holds the work. Syntax: `attn seed --help`.

WRITING A BODY

A seed body is a prompt for the agent who will do the work. A new delegation
stores its brief as the body; handover sends the stored body to the next agent.
Write every body, including plot children, for someone without this conversation.
Do this even when you plan to do the work yourself.

- Lead with the task and outcome: what to do and what done looks like.
- Give starting context: the problem, relevant paths or evidence, and constraints.
  Point to shared context and say what the reader needs from it.
- Define scope: what this seed owns and which decisions need the user.
- Specify completion: how to verify the result and where to leave the deliverable
  and evidence.

Include an agreed design when there is one. Leave implementation choices open
where they are still for the tender to make. Scale the detail to the task; a few
sentences can be a complete prompt.

Keep the agreed task current in the body. Progress, evidence and handoffs go in
the log. When the task changes, update the body and note what changed.

WRITING A PLOT

A plot holds the shared plan. Its children are units of work that can each be
tended on their own; a child can itself be a plot. Keep shared decisions in the
parent. Each child states its own assignment and names any parent section,
sibling result or artifact it needs to read, with a reason. Those references
save repetition, but their text is not included automatically in a delegation.

Use `blocks` only when one child needs another's result. Otherwise leave them
parallel. The children's states record progress, so the plan needs no checklist.

Write the parent for an implementer starting fresh and a user reviewing the
direction. Put the choices the user might change first. Include what the work
needs:

- The goal and how the children together complete it.
- The proposed shape in repository terms. Show ownership, interfaces and data
  when they cross boundaries.
- Shared decisions and their reasons, constraints, and unresolved questions.
  Say which questions block work and which the tender can decide.

Use short prose and the smallest picture that explains the design: a call tree
for control flow, a file tree for ownership, or a sequence diagram for messages
between processes. Show changed code structure as a diff when that is clearer.
Put each picture next to the explanation; show separate production and test
wiring when the distinction matters.

Example: move session search to the daemon while preserving its behavior.
The endpoint child records a contract that the app child needs before starting.

    attn seed plot -f plan.json

```json
{
  "title": "Search moves to the daemon",
  "body": "Move session search from the app to the daemon.\n\n## Search behavior\nPreserve the current matching, ordering and keyboard behavior in app/src.\n\n## Ownership\ninternal/daemon owns searching session data. app/src sends queries and renders results. Keep the client index until the app uses daemon results.\n\n## Completion\nBoth children are complete, with endpoint tests and running-app evidence on their logs.",
  "children": [
    {
      "title": "Daemon search endpoint",
      "body": "Add session search in internal/daemon. Read the parent plot's Search behavior section and trace the existing search in app/src to preserve its behavior. Follow repository protocol guidance. Test empty queries, matching, ordering and session changes. Record the query and response contract and test results on this seed for the app child. App integration belongs to that child.",
      "blocks": ["app-calls-endpoint"]
    },
    {
      "title": "App calls the endpoint",
      "body": "Route session search in app/src through the daemon, then remove the obsolete client index. Read the parent plot's Search behavior and Ownership sections and the Daemon search endpoint seed's recorded API contract. Preserve keyboard selection and prevent stale results when queries overlap. Verify search, rapid query changes and keyboard navigation in the running app; record the results and a recording on this seed."
    }
  ]
}
```

Each child's `blocks` array names the siblings that wait for it. Use the sibling's
slug or full title. Pass `-` instead of a file to read the payload from stdin.

    attn seed plant "..." --part-of <plot>   add another child
    attn seed edit <id> -m "..."             update the agreed task; note what changed
    attn open <plot>                        read the rendered plan

Before planting or delegating, read each body with its named references as a
fresh agent. Can you tell what to do, where to start, which constraints apply,
and how to establish completion? Fill any gaps before handing it off. Read
referenced seed bodies with `attn seed show <id>`.

Update the shared plan when an agreed decision changes, and note why. Plant
deferred work under the plot or beside it so it remains tracked.

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
    prototype  The result answers a design question or lets the user judge the
               experience. State what to demonstrate; tests may be optional.

Harvest when the outcome and required verification in the body are complete.
When the only thing left is a pull request merging, say so once and let attn
close it:

    attn seed harvest <id> --when-merged [<pr-url>]   with no url, this session's
                                                      single open pull request
    attn seed harvest <id> --when-merged --clear      take the arming back

A seed you were growing goes dormant and lets go of your claim; either way it
points at the pull request and harvests itself, with its own reason, when the
merge lands. A pull request closed without merging clears the arming instead.
Reach for it only when the merge is genuinely the last step: review notes to
answer, docs to write, or evidence to attach are work, and work belongs to a
tender.

ARTIFACTS

Bring a local file into durable seed ownership with an explicit Move or Copy:

    attn seed attach <id> --path <file> --move
    attn seed attach <id> --path <file> --copy
    attn seed detach <id> --path <filename> --to <destination>

Move is recommended: the seed owns the one canonical editable file. Copy leaves
the source alone. A tracked Git file must use Copy. Transfers never overwrite,
and the seed-owned file survives the source worktree, session and seed closing.
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

    attn seed note <id> -m "…" --handoff   for the next tender; attn seed show prints
                                            it first, attn seed tend prints it on the claim
    attn agent msg <seed-id> "…"            reaches whoever tends it now; an
                                            untended seed refuses by name

Leave a handoff whenever you park a seed, or stop mid-thread and do not
intend to continue: outcome, evidence, next action. Long reasoning goes in an
attached artifact, not in the note. Noting does not end your session; keep
working unless blocked or done.
