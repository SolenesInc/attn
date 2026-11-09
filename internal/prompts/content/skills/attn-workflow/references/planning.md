# Planning

Principles this process relies on: [Refactor first](../principles/refactor-first.md), [Redesign around the new requirement](../principles/redesign-around-the-new-requirement.md), [Model the domain first](../principles/model-the-domain-first.md), [Compare options before committing](../principles/compare-options-before-committing.md), [Subtract before you add](../principles/subtract-before-you-add.md), [Make operations idempotent](../principles/make-operations-idempotent.md), [Separate before serializing shared state](../principles/separate-before-serializing-shared-state.md), [Converge on the outcome](../principles/converge-on-the-outcome.md), [Sequence verifiable units](../principles/sequence-verifiable-units.md), [Experience first](../principles/experience-first.md), [Prove it works](../principles/prove-it-works.md), [Build the lever](../principles/build-the-lever.md). Read them before you start. [The skill's index](../SKILL.md#principles) lists the rest.

Develop a concrete implementation approach that the user can review and another agent can execute without this conversation. Be thorough in your analysis and bold in your recommendations. Put the plan in the body of a garden seed or plot. Do not create a standalone plan file.

Read `attn skill --reference garden` and run `attn seed guide` for seed and plot mechanics, child briefs and lifecycle rules. This reference says what the plan itself must contain. If the garden is unavailable, report what is missing and do not fall back to a file. Write the plan, the child briefs, and the questions as [Technical writing](technical-writing.md) says. Read it before the first draft.

## Workflow

1. **Find the work.** Read the relevant implementation seed or plot, the notes on it, and any children. Reuse the existing work and keep the scope and decisions it already has. If the work has no implementation seed, plant one to hold the plan.
2. **Investigate the approach.** Start from the user's request, the relevant conversation, and any vision that the work references or the user supplies. Read vision seeds with `attn seed show <id>`. Read the results of any spikes and investigation for the work, including their code, notes, and reports. Read enough code to identify the components, entry points, state, interfaces, and ownership involved. Apply [Refactor first](../principles/refactor-first.md): list the existing paths that do the same job, with how each is addressed, triggered, and recovered, and record the list as a finding. Trace production and test paths where they differ. When the change moves ownership or layering, find out why the current shape exists, from its history, its pull requests, and the decisions recorded in the garden. Record the reason as a finding. A reason that still holds is a constraint. A reason that no longer holds is what the plan removes.
3. **Design it twice.** When the change adds or reshapes a boundary other code depends on, or moves ownership, sketch at least two structurally different shapes, even when the first looks sufficient. Make them whole designs, not variations inside one design. Compare them with [Design](design.md). Other changes need one shape.

   When the choice is contested and costly to reverse, suggest [prototypes](prototype.md). Prototypes are your shapes built as options. When the delegation roles offer more than one model for the decision, you can suggest independent candidates instead. Each candidate designs from the same brief without seeing your shapes, then joins the comparison. In the suggestion, say whether you propose options or candidates, because running candidates starts one session per model. The user decides whether to run them, as for any spike.

   The chosen shape becomes the Design. When the Design combines candidates, state the base, each graft, and the candidate each graft came from. Put the shapes and candidates that lost under Alternatives rejected. Record what a candidate revealed, such as a realistic case that broke it, as a finding. Ask about choices or assumptions that could change the plan.
4. **Write the plan.** Fill in the template below and put it in the body of the seed or plot. If there is a vision seed, reference its ID. Do not overwrite the vision seed's body with the implementation plan.
5. **Organize execution.** Use a plot when the work has distinct pieces to scope and track separately, even within one delivery step. Use a single seed for one coherent task. Add or reuse a child seed for each unit of work. Give each child an outcome, a scope, and its own checks, and refer to the parent plan without repeating it. Group changes into delivery units as Delivery (`attn skill --reference delivery`) describes. Order children with `after` only for actual prerequisites. Leave all other children independent.

## The plan body

Write for an implementer starting fresh and a user reviewing the direction. The body states the current design. It is not a record of the conversation. Put who decided what, when, through which question, and what the user authorized in seed notes. A body that reads as meeting minutes is not a plan. What you learned is not history. It is the evidence the design rests on, and it belongs in the body.

A plan is one document that needs several modes, so it keeps one mode per section instead of the one mode per document that technical writing asks for. Terms and Design are reference. Task and outcome, Decisions, Tradeoffs accepted, Findings, and Alternatives rejected are explanation, so a finding can say what it requires and what it rules out. Execution and Completion are how-to. Keep the section names below as written, even where technical writing would have a heading carry its point. Orchestrators and the authors of child briefs look up a plan's parts by these names.

The body has these sections in this order. Each design part is required. If a part does not apply, write one line with the reason in its place.

- **Task and outcome**: at most ten lines. State what changes, why, what done looks like, and what the work deliberately leaves out. Give the why as the one reason every decision traces back to.
- **Terms**: one line for each name the plan introduces or narrows. Use that name, and no other, for the thing everywhere in the plan and in the children. Omit the section when the plan introduces no names.
- **Decisions**: the choices the user might still change.
  - Number them (D1, D2) so other sections can cite them.
  - State each choice as it stands now, with its reason, in a sentence or two.
  - Put the decisions that shape the system first and the tactics an implementer could change after.
  - Put rejected options under Alternatives rejected, and who decided in a note.
  - Then list the open questions. Write each so the user can answer it without reading anything else. For each question, say what it decides and which child or Builder waits on it. Give the options and your recommendation with its reason. Say what happens if the question stays unanswered, and whether it blocks work or the implementer may decide it.
  - When a refactor would let the change fit the existing code, show the user both orders, refactor first or build beside the existing code, with the smallest refactor that would work. Replacing a whole subsystem is not the only alternative.
- **Tradeoffs accepted**: one line each, in the form "we accept X in exchange for Y". Cite the decision it comes from (D2) when there is one, instead of restating the decision. Name every choice a later reader could mistake for an oversight: a duplicated write, a cost paid for a reason stated elsewhere, a behavior change, a simplification that looks premature. If a cost has no stated exchange, the next agent may remove the cost.
- **Findings**: what spikes and investigation established that the design depends on, written as described under Findings below. When nothing was learned beyond what the code shows, write one line saying so.
- **Design**, in four parts:
  - **Ownership**: show each file or package that changes as a shallow file tree, with one comment per entry that says what that file or package does.
  - **Interfaces**: sketch the types, signatures, wire messages, and persisted data that the change adds or alters, in the codebase's language, with the schema, generation, and migration steps that a wire or storage change requires. For an interface the change adds or reshapes, write a representative call site first and derive the types from it. If the call site and the types disagree, change the types. Check both with [Design](design.md). Name the invariants a later change could silently break, and say what enforces each invariant: a type, a check at the boundary, or the test of the promise the invariant protects.
  - **Behavior**: show how control and data flow through the changed parts: a sequence for messages between processes, a call tree within one process, or pseudocode for a rule.
  - **State**: for each piece of state, give its one owner, what sets it, what reads it, what happens on failure and on restart, and, for state that a retried or interruptible step writes, what a second or half-finished run leaves. In-memory state counts as state and gets the same entry. State with two writers is a design problem to resolve, as Design describes, not an entry to record.
- **Execution**: for a plot, list the children with their delivery units and order, as Delivery (`attn skill --reference delivery`) describes. List an agreed refactor as its own behavior-preserving change that blocks the work built on it. For a single seed, write one line naming its delivery step. Put a detail that only one child needs, such as the cases its tests cover or a mechanism inside its scope, in that child's body.
- **Completion**: what the checks cannot state, citing the checks by id. The checks themselves are seed checks, defined as data on the seeds that must prove them, as Checks below describes. In this section, write any documented exemption, say where long evidence goes, and give any order in which the checks must be proven. When nothing is left over, write one line saying the checks are on the seeds.
- **Alternatives rejected**: write one line each. Cite the decision the option lost to (D1), or name the shape that lost when you designed twice, and say why it lost. Omit the section when nothing was rejected.

## Checks

A check is one numbered statement (C1, C2) that a seed must prove. A check names the evidence that proves it, such as a test run, a recording, a measurement, or a commit, and says where that evidence goes. attn keeps each check on the seed with the check's latest report and verdict. Define checks with `checks` in the plot JSON, or with `attn seed check add` later, never as bullets in the body. Include the checks the repository's verification guidance requires for the affected surfaces, or the documented exemption when one applies. State each check at the level at which a reviewer judges the outcome.

Put each check on the seed that proves it. Put a check that every child must prove on the plot once, with `"shared": true`, and have each child prove it with its own report. Put a check that only one child owns on that child. Put a check that the plot proves as a whole, such as a recording after every child delivers, on the plot without `shared`. Ids are unique across the plot tree, so C5 names one statement wherever a body, note, or report cites it. In each child's body, cite the child's checks by id and give the detail behind them, such as the cases its tests cover.

## Findings

A plan built on a spike contains what the spike showed. Spike code, branches, transcripts, reports, and notes are evidence behind the plan, not a substitute for it. An implementer works from the plan, and a learning recorded only in an artifact gets relearned, often wrongly. Link the artifacts as supporting evidence and write the learnings themselves into the body. The same holds for consequential discoveries from investigation.

Write each finding so an implementer can act on it without the spike:

- the fact, concretely: the behavior, mechanism, ordering, or limit observed
- the evidence: the function, type or file involved, a command and its output, a measurement, or the snippet that worked, quoted, because spike code is disposable
- what it requires of the design
- what it rules out: the approach that looks simpler or more natural, and why it fails

Name code by symbol and file. State once, at the top of Findings, the commit that the findings describe. Do not cite line numbers. A line number becomes wrong with the next edit, and the body is then wrong. Do not survey code the implementer can read. A finding is something the design depends on.

Number findings (F1, F2) and cite them where the design depends on them and in the child briefs whose work they constrain, so each child brief names the findings that apply without repeating them. When implementation overturns a finding, correct it in the body and record the reason in a note.

## Showing the design

Show each design part instead of describing it. A part with prose alone is incomplete.

When the shape already exists in the codebase, show its change as a diff of that shape: `+` and `-` lines on the file tree, the type, the call tree or the sequence. The showing reference below says when to show the whole shape instead.

Scale each picture to the change. Give it enough detail that the reader can judge the design without reconstructing it from the code, and no more. Use names from the codebase. Put each picture beside the short explanation it supports, in a fenced code block, or in a Mermaid block for component interaction and data flow, because the Garden renders both. Keep pictures narrow, and split a wide picture into smaller views. Show separate production and test wiring when the distinction matters.

Choose the smallest view that explains the point. `attn skill --reference showing` gives the forms for logic, call trees, component trees, file trees, and diffs. A plan also shows:

- component interaction or data flow as a Mermaid diagram
- messages between processes as a Mermaid sequence diagram
- a UI layout or state comparison as an ASCII wireframe

## Template

Copy the template and replace everything in angle brackets. The bracketed content is one example plan, a snooze that ends when a seed closes. It shows each part's form. It uses a diff where the shape already exists and a whole block where the shape is new.

````markdown
<Let the user snooze an agent until a seed is harvested or withered, as well
as until a time. Today a snooze ends only at an instant, so a user who waits on
a delegation's seed guesses a time and is handed the agent back too early or
too late. The time stays, as a cap, and the seed can end the snooze sooner.
Done when an agent snoozed on a seed stays out of the queue until the seed is
harvested, gets its turn back within one bus delivery of the harvest, and keeps
the seed snooze across a daemon restart. Out of scope: snoozing on a pull
request, which has its own watch.>

## Terms

- <seed snooze: a snooze that names a seed and ends when that seed closes, or
  at its time, whichever comes first.>
- <closed: harvested or withered, as `garden.Closed` reports it.>

## Decisions

- <D1. A seed snooze keeps its time as a cap, because a seed that stalls must
  not hide an agent for good. The menu offers the seed with the existing times.>
- <D2. The seed belongs to the snooze, stored beside its time in the turn
  stamps. Unsnooze and a new snooze clear it with no separate record.>

Open: <whether parking the seed ends the snooze (the daemon child may decide;
recommend no, since a parked seed can resume)> · <the default cap in the menu
(the app tender decides)>

## Tradeoffs accepted

- <D2. We accept a new column on `session_runtime` in exchange for one owner of
  the snooze and no subscription table to keep in step with it.>
- <F2. We accept a durable bus consumer that pins retention while any seed
  snooze is pending, in exchange for a harvest during a daemon restart still
  ending the snooze.>

## Findings

<From the spike on branch spike/seed-snooze at 7c41e0d. Its code is not kept.>

- <F1. The session monitor owns a snooze. `handleSnoozeTurn` in
  `internal/daemon/snooze.go` sends `d.monitorCommand(sessionID, "snooze",
  until, "")`, and the monitor writes `store.TurnStamps`. The seed must travel
  in the same command. The spike's daemon wrote the seed to the store beside the
  monitor, and an unsnooze left a stale seed that woke the agent a day later.>
- <F2. A `bus.Subscribe` callback misses facts published while the daemon is
  down. The spike harvested a seed during a restart, and the agent stayed
  snoozed until its cap. A consumer registered with `d.eventBus.Register`
  for `garden.seed.harvested` and `garden.seed.withered` resumes from its
  cursor and saw the harvest.>

## Design

Ownership:

    <internal/protocol/schema/main.tsp       new until_seed on SnoozeTurnMessage and turn_snoozed_for_seed on Session
     internal/store/turn.go                  new SnoozedForSeed in TurnStamps, persisted in session_runtime
     internal/sessionmonitor/commands.go     snooze stores the seed with the time; unsnooze clears both (F1)
     internal/daemon/snooze.go               checks the seed is open before the snooze
     internal/daemon/snooze_seed.go          new: durable consumer that unsnoozes sessions when their seed closes (F2)
     app/src/components/SnoozeMenu.tsx       "Until a seed closes" choice with a seed picker>

Interfaces:

<```go
for _, id := range d.store.SessionsSnoozedForSeed(ev.Subject) {
    if _, err := d.monitorCommand(id, "unsnooze", time.Time{}, ""); err != nil {
        d.logf("seed unsnooze %s: %v", id, err)
    }
}
```

```go
func (s *Store) SessionsSnoozedForSeed(seedID string) []protocol.SessionID // new
```

```diff
 model SnoozeTurnMessage {
   cmd: "snooze_turn";
   caller_session_id?: SessionID;
   session_id: SessionID;
   until: string;
+  until_seed?: string;
 }
```

New wire field: edit main.tsp and run make generate-types, then commit the
generated types and protocol schema hash. The new column follows
docs/internal/db-migrations.md. Invariant: a session's snooze names a seed only
while the snooze holds. C1's wire test enforces it.>

Behavior:

    <user    -> app:     picks "Until a seed closes" and a seed in SnoozeMenu
     app     -> daemon:  SnoozeTurnMessage{until, until_seed}
     daemon  -> monitor: snooze(until, seed)
     monitor -> store:   TurnStamps{SnoozedUntil, SnoozedForSeed}>

<```diff
 handleSnoozeTurn
   time.Parse(until)
+  readSeed(until_seed)       refuses a closed or unknown seed
   monitorCommand("snooze")
+    with the seed            the monitor stores both (F1)
```>

    <garden  -> bus:     garden.seed.harvested{seed}
     bus     -> daemon:  snooze_seed consumer, from its cursor (F2)
     daemon  -> monitor: unsnooze, for each session snoozed for the seed>

State:

    <seed choice (app, SnoozeMenu)
       set by:      the seed picker; starts empty
       read by:     the snooze message on choose
       on failure:  a refused snooze shows the daemon's error; the menu closes
       on restart:  starts empty; nothing persisted

     snoozed-for seed (monitor, store.TurnStamps.SnoozedForSeed)
       set by:      the snooze command, with SnoozedUntil
       read by:     the snooze_seed consumer; the session's wire record
       on failure:  a refused snooze leaves the prior stamps unchanged
       twice:       a redelivered harvest unsnoozes a session that is not snoozed, which changes nothing
       on restart:  reloaded from session_runtime; the consumer replays from its cursor>

## Execution

<Three children, delivered separately in this order: the
monitor and store (the seed in the turn stamps and its column), then the daemon
and protocol (the wire field and the consumer), then the app menu. The daemon
child waits for the store child's stamps. The app child waits for the daemon
child's wire field.>

## Completion

<The checks are on the seeds. C4's recording and scenario output go on the app
child as one attachment, and the child's report cites that attachment. The app
child changes no daemon code, so the Linux build the verification guidance asks
of daemon changes does not apply to it.>

## Alternatives rejected

- <D1. A seed snooze with no time: a seed that is never closed would hide the
  agent until the user looks for it.>
- <D2. A subscription table keyed by seed: it is a second record of the snooze
  that unsnooze, a new snooze and closing the session would each have to clear.>
````

The same plan planted with its checks. The plot holds the check every child proves, and each child holds its own:

```json
{
  "title": "Seed snooze",
  "body": "<the plan body above>",
  "checks": [
    {"id": "C5", "shared": true, "text": "The child is delivered as the repository delivers changes, with that step's own checks passing. The report links the pull request or the landed commits."}
  ],
  "children": [
    {"title": "Seed in the turn stamps", "body": "…",
     "checks": [{"id": "C1", "text": "A store test snoozes a session on a seed, unsnoozes it, reopens the store and finds neither time nor seed. Output on this seed."}]},
    {"title": "Daemon seed snooze and consumer", "body": "…", "after": ["seed-turn-stamps"],
     "checks": [{"id": "C2", "text": "A daemon wire test snoozes a session on a seed, restarts the daemon, harvests the seed and sees the session's snooze end. A wsctl run against a non-production daemon shows the same. Both outputs on this seed."}]},
    {"title": "App seed snooze choice", "body": "…", "after": ["daemon-seed-snooze-consumer"],
     "checks": [
       {"id": "C3", "text": "An app wire test picks a seed in the snooze menu and sees the snooze message carry its id. Output on this seed."},
       {"id": "C4", "text": "The real-app harness scenario snoozes an agent on a seed, harvests the seed from the CLI and sees the agent's turn open in the queue. Recording on this seed."}
     ]}
  ]
}
```

## Tracking and handoff

Record progress in seed states and notes. Plant deferred work as seeds. Do not keep a task checklist or activity log in the plan body.

When the approach changes, rewrite every section of the body that depends on the old approach, so the body reads as if the new decision had been the first decision. A new approach usually changes the design, the tradeoffs, the completion checks, and the alternatives rejected, not only the decision. Do not leave amendments, "superseded" markers, or revision numbers in the body. Record what changed and why in a note. Keep the D and F numbers. When you remove a decision or finding, leave its number unused, so a note or brief that cites the number still points at the same thing. The ids of checks follow the same rule: `attn seed check remove` keeps the id, and `restore` brings the check back with its result. `attn seed check edit` keeps a check's report and verdict, so reopen a check whose evidence no longer proves its new statement. When a term comes to mean something else, rename the term or rewrite each use.

Rewrite the briefs of children that are not yet harvested in the same way. Leave a harvested child's body as the record of what the child delivered, and add the work the change brings as a new child. A tender who is already working does not see a body edit, so tell the tender of each growing child whose brief changed, with `attn agent msg <seed-id>`.

Read back the saved plan, children, and dependency links as a fresh agent. Can you tell what to build, where to start, which constraints apply, and how to establish completion? Would an implementer who never saw the spikes avoid every trap that the spikes hit? Can a reader who doubts a decision find the reason for the decision, and the cost it accepts, without opening a note or a transcript? Check that every design part is present or explicitly waived, that `attn seed show` lists each check on the seed that must prove it, and that each learning from a spike is in the body rather than only in the spike's artifacts. Then cut. Delete each line an implementer would not act on and a reviewer would not question, and delete every statement made twice. Show the user the proposed plan, its delivery units and their order, as Delivery (`attn skill --reference delivery`) describes. Name the seed or plot so the user can review it.

Recommend how to execute the plan. Recommend an Orchestrator when the plan requires coordinated or reviewed Builder work, or benefits from mixing harnesses or models between the coordinating agent and its Builders. Otherwise, recommend a single Builder. Explain the recommendation briefly.

With the plan, reflect your understanding of the work, the decisions you made while writing the plan, and the assumptions the implementer will rely on, so the user can correct them in the same answer. That reflection is the [Align](align.md) check before dispatch. Ask whether the user wants to review or adjust the plan, or dispatch, and wait for their answer. This checkpoint applies even when the user's earlier request included execution. Dispatch only in one of two cases. Either the user chooses dispatch after seeing the proposed plan and handoff, or the user said explicitly, before the plan existed, to dispatch without reviewing it. A request that includes execution is not an instruction to dispatch without review. When the user authorizes dispatch, do three things in order. Record the reflection above and the user's instruction as notes on the plan seed. Dispatch. Then name the plan seed in your reply so the user can read it later. Keep execution seeds open when only the plan is complete.

For an authorized handover, keep the plan in the plan seed, record the next assignment in a handoff note, and pass the user's execution authorization with `--authorization`. Use `attn skill --reference delegation` to choose the explicit folder and checkout. Give the next agent the plan seed, code location, agreed scope and completion checks. The successor reads the plan and handoff from the seed, and its opening delivers the authorization.
