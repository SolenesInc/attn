# Planning

Principles this process relies on: [Refactor first](../principles/refactor-first.md), [Redesign around the new requirement](../principles/redesign-around-the-new-requirement.md), [Model the domain first](../principles/model-the-domain-first.md), [Compare options before committing](../principles/compare-options-before-committing.md), [Subtract before you add](../principles/subtract-before-you-add.md), [Make operations idempotent](../principles/make-operations-idempotent.md), [Separate before serializing shared state](../principles/separate-before-serializing-shared-state.md), [Converge on the outcome](../principles/converge-on-the-outcome.md), [Sequence verifiable units](../principles/sequence-verifiable-units.md), [Experience first](../principles/experience-first.md), [Prove it works](../principles/prove-it-works.md), [Build the lever](../principles/build-the-lever.md). Read them before you start. [The skill's index](../SKILL.md#principles) lists the rest.

Develop a concrete implementation approach that the user can review and another agent can execute without this conversation. Put the plan in the body of a garden seed or plot. Do not create a standalone plan file.

Read the `attn` skill's garden guidance and run `attn seed guide` for seed and plot mechanics, child briefs and lifecycle rules. This reference says what the plan itself must contain. If the garden is unavailable, report what is missing and do not fall back to a file. Write the plan, the child briefs, and the questions as [Technical writing](technical-writing.md) says. Read it before the first draft.

## Workflow

1. **Find the work.** Read the relevant implementation seed or plot, the notes on it, and any children. Reuse the existing work and keep the scope and decisions it already has. If the work has no implementation seed, plant one to hold the plan.
2. **Investigate the approach.** Start from the user's request, the relevant conversation, and any vision that the work references or the user supplies. Read vision seeds with `attn seed show <id>`. Read the results of any spikes and investigation for the work, including their code, notes, and reports. Read enough code to identify the components, entry points, state, interfaces, and ownership involved. Apply [Refactor first](../principles/refactor-first.md): list the existing paths that do the same job, with how each is addressed, triggered, and recovered, and record the list as a finding. Trace production and test paths where they differ. When the change moves ownership or layering, find out why the current shape exists, from its history, its pull requests, and the decisions recorded in the garden. Record the reason as a finding. A reason that still holds is a constraint. A reason that no longer holds is what the plan removes.
3. **Design it twice.** When the change adds or reshapes a boundary other code depends on, or moves ownership, sketch at least two structurally different shapes, even when the first looks sufficient. Make them whole designs, not variations inside one design, and compare them with [Design](design.md). Other changes need one shape. When the choice is contested and costly to reverse, suggest [prototypes](prototype.md). The prototypes are your shapes built as options or, when the delegation roles offer more than one model for it, independent candidates. Each candidate designs from the same brief without seeing your shapes, and then joins the comparison. Say which you suggest, since candidates start one session per model. The user decides whether to run them, as for any spike. The chosen shape becomes the Design. When the Design combines candidates, it states the base, each graft, and the candidate each graft came from. The shapes and candidates that lost go under Alternatives rejected. What a candidate revealed, such as a realistic case that broke the candidate, becomes a finding. Ask about choices or assumptions that could change the plan.
4. **Write the plan.** Fill in the template below and put it in the body of the seed or plot. If there is a vision seed, reference its ID. Do not overwrite the vision seed's body with the implementation plan.
5. **Organize execution.** Use a plot when the work has distinct pieces to scope and track separately, even within one delivery step. Use a single seed for one coherent task. Add or reuse a child seed for each unit of work. Give each child an outcome, a scope, and its own checks, and refer to the parent plan without repeating it. Group changes into delivery units as [Delivery](delivery.md) describes. Order children with `after` only for actual prerequisites. Leave all other children independent.

## The plan body

Write for an implementer starting fresh and a user reviewing the direction. The body states the current design. It is not a record of the conversation. Put who decided what, when, through which question, and what the user authorized in seed notes. A body that reads as meeting minutes is not a plan. What you learned is not history. It is the evidence the design rests on, and it belongs in the body.

A plan is one document that needs several modes, so it keeps one mode per section instead of the one mode per document that technical writing asks for. Terms and Design are reference. Task and outcome, Decisions, Tradeoffs accepted, Findings, and Alternatives rejected are explanation, so a finding can say what it requires and what it rules out. Execution and Completion are how-to. Keep the section names below as written, even where technical writing would have a heading carry its point. Orchestrators and the authors of child briefs look up a plan's parts by these names.

The body has these sections in this order. Each design part is required. If a part does not apply, write one line with the reason in its place.

- **Task and outcome**: at most ten lines. State what changes, why, what done looks like, and what the work deliberately leaves out. Give the why as the one reason every decision traces back to.
- **Terms**: one line for each name the plan introduces or narrows. Use that name, and no other, for the thing everywhere in the plan and in the children. Omit the section when the plan introduces no names.
- **Decisions**: the choices the user might still change. Number them (D1, D2) so other sections can cite them. State each choice as it stands now, with its reason, in a sentence or two. Put the decisions that shape the system first and the tactics an implementer could change after. Put rejected options under Alternatives rejected, and put who decided in a note. Then list the open questions, and write each so the user can answer it without reading anything else. For each question, say what it decides and which child or Builder waits on it, give the options and your recommendation with its reason, say what happens if the question stays unanswered, and say whether it blocks work or the implementer may decide it. When a refactor would let the change fit the existing code, show the user both orders, refactor first or build beside it, with the smallest refactor that would do. Replacing a whole subsystem is not the only alternative.
- **Tradeoffs accepted**: one line each, in the form "we accept X in exchange for Y". Cite the decision it comes from (D2) when there is one, instead of restating the decision. Name every choice a later reader could mistake for an oversight: a duplicated write, a cost paid for a reason stated elsewhere, a behavior change, a simplification that looks premature. If a cost has no stated exchange, the next agent may remove the cost.
- **Findings**: what spikes and investigation established that the design depends on, written as described under Findings below. When nothing was learned beyond what the code shows, write one line saying so.
- **Design**, in four parts:
  - **Ownership**: show each file or package that changes as a shallow file tree, with one comment per entry that says what that file or package does.
  - **Interfaces**: sketch the types, signatures, wire messages, and persisted data that the change adds or alters, in the codebase's language, with the schema, generation, and migration steps that a wire or storage change requires. For an interface the change adds or reshapes, write a representative call site first and derive the types from it. If the call site and the types disagree, change the types. Check both with [Design](design.md). Name the invariants a later change could silently break, and say what enforces each invariant: a type, a check at the boundary, or the test of the promise the invariant protects.
  - **Behavior**: show how control and data flow through the changed parts: a sequence for messages between processes, a call tree within one process, or pseudocode for a rule.
  - **State**: for each piece of state, give its one owner, what sets it, what reads it, what happens on failure and on restart, and, for state that a retried or interruptible step writes, what a second or half-finished run leaves. In-memory state counts as state and gets the same entry. State with two writers is a design problem to resolve, as Design describes, not an entry to record.
- **Execution**: for a plot, list the children with their delivery units and order, as [Delivery](delivery.md) describes. List an agreed refactor as its own behavior-preserving change that blocks the work built on it. For a single seed, write one line naming its delivery step. Put a detail that only one child needs, such as the cases its tests cover or a mechanism inside its scope, in that child's body.
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

Name code by symbol and file. State the commit the findings describe once, at the top of Findings. Do not cite line numbers. A line number goes stale with the next edit, and the body is then wrong. Do not survey code the implementer can read. A finding is something the design depends on.

Number findings (F1, F2) and cite them where the design depends on them and in the child briefs whose work they constrain, so each child brief names the findings that apply without repeating them. When implementation overturns a finding, correct it in the body and record the reason in a note.

## Showing the design

Show each design part instead of describing it. A part with prose alone is incomplete.

When the shape already exists in the codebase, show its change as a diff of that shape: `+` and `-` lines on the file tree, the type, the call tree or the sequence. Show the whole shape when most of it is new, when a diff would hide ownership or order, or when the implementer needs a copyable target. A diff shows what changes where prose would describe it.

Scale each picture to the change. Give it enough detail that the reader can judge the design without reconstructing it from the code, and no more. Use names from the codebase. Put each picture beside the short explanation it supports, in a fenced or indented code block so it renders in the terminal and in the Garden. Keep pictures narrow, and split a wide picture into smaller views. Show separate production and test wiring when the distinction matters.

Choose the smallest view that explains the point. The `attn` skill's [showing reference](../../attn/references/showing.md) gives the forms for logic, call trees, component trees, file trees, and diffs. A plan also shows:

- component interaction or data flow as labeled arrows
- messages between processes as a sequence
- a UI layout or state comparison as an ASCII wireframe

## Template

Copy the template and replace everything in angle brackets. The bracketed content is one example plan, a per-session permission preset for pi. It shows each part's form. It uses a diff where the shape already exists and a whole block where the shape is new.

````markdown
<Let a pi session choose its approval policy and sandbox mode at creation, as a
preset, and switch them mid-session with /permissions. Today every pi session
runs under the daemon's one auto-mode config, so reviewing one risky change
means loosening or tightening all of them. The daemon's config stays the
default, and a session can differ from it. Done when a session created as Read Only
shows read-only in pi's status line, refuses a write until the agent escalates,
switches on /permissions default, and relaunches as Read Only after a daemon
restart. Out of scope: presets for Claude and Codex sessions, which have their
own permission modes.>

## Terms

- <preset: a named pair of approval policy and sandbox mode, with Codex's names
  and descriptions (Read Only, Default, Full Access).>
- <launch intent: the preset a session was created with, stored by the daemon.>

## Decisions

- <D1. Presets are the user-facing unit, because users already know Codex's
  names. The raw pair stays reachable on the CLI.>
- <D2. A mid-session switch changes pi only, as in Codex. The daemon knows only
  the launch intent.>

Open: <whether `--yolo` on a pi launch maps to Full Access (blocks the CLI
child)> · <the picker's position beside auto mode (the app tender decides)>

## Tradeoffs accepted

- <D2. We accept that a session switched mid-way relaunches with its launch
  intent after a daemon restart, in exchange for no per-session state shared
  between pi and the daemon.>

## Findings

<From the spike on branch spike/pi-presets at 3f2a91c. Its code is not kept.>

- <F1. Pi's sandbox wraps only bash. The spike's Read Only session wrote a file
  through `edit` with no prompt. Read Only therefore needs a file-tool guard in
  the approval extension. Setting sandbox_mode alone looks sufficient and is not.>
- <F2. Pi repaints the status line only on session events, so after a switch the
  spike showed the old preset until the next turn. The switch must repaint itself:
  `ctx.ui.setStatus("permissions", preset.label)` right after the config changes.>

## Design

Ownership:

    <internal/automode/automode.go          Preset type; Presets(); PresetFor(policy, mode)
     internal/protocol/schema/main.tsp      approval_policy and sandbox_mode on SpawnSessionMessage
     internal/daemon/spawn_pipeline.go      applies the launch intent's pair over the daemon default
     plugins/attn-pi/approval/rules.ts      refuses file-tool writes under Read Only (F1)
     plugins/attn-pi/approval/session.ts    /permissions picker; repaints the status line (F2)
     app/src/components/LocationPicker.tsx  preset control beside auto mode>

Interfaces:

<```go
label := "Custom"
if p, ok := automode.PresetFor(cfg.ApprovalPolicy, cfg.SandboxMode); ok {
    label = p.Label
}
```

```go
type Preset struct {
    ID, Label, Description string
    ApprovalPolicy         string
    SandboxMode            string
}
func Presets() []Preset
func PresetFor(policy, mode string) (Preset, bool) // false when no preset matches
```

```diff
 model SpawnSessionMessage {
   cwd: string;
+  approval_policy?: string;
+  sandbox_mode?: string;
 }
```

New wire fields: edit main.tsp and run make generate-types, then commit the
generated types and protocol schema hash. Invariant: a session relaunches with
the pair stored as its launch intent. C1's wire test enforces it.>

Behavior:

    <user   -> app:    picks Read Only in the location picker
     app    -> daemon: SpawnSessionMessage{approval_policy, sandbox_mode}
     daemon -> store:  SetLaunchIntent(session, pair)
     daemon -> pi:     launch with the pair>

<```diff
 executeSpawn
   GetAutoModeConfig
+  applyLaunchIntent          the intent pair replaces the config pair when set
   autoModeConfigForSession
   spawnSessionRuntime
```>

    <user  -> pi:     /permissions full-access
     pi    -> pi:     setup.config := preset; repaint status line
     pi    -> daemon: nothing; the switch is not durable>

State:

    <preset selection (app, LocationPicker)
       set by:      the picker control; starts from the daemon default
       read by:     the spawn message on launch
       on failure:  a refused launch keeps the selection for a retry
       on restart:  starts from the daemon default; nothing persisted

     launch intent (daemon, store.LaunchIntent)
       set by:      spawn or delegate message, stored before the runtime spawns
       read by:     spawn pipeline; reload after daemon restart
       on failure:  a new session is removed with its intent; a relaunch restores the prior intent
       twice:       a retried spawn writes the same pair again
       on restart:  relaunched with the stored pair; a mid-session switch is lost>

## Execution

<Three children, delivered separately in this order: the
daemon and protocol (presets, wire fields, launch intent), then pi (/permissions
and the file-tool guard), then the app picker. The pi child blocks nothing. The
app child waits for the daemon child's wire fields.>

## Completion

<The checks are on the seeds. C4's recording and scenario output go on the app
child as one attachment, and the child's report cites that attachment. The pi child changes no daemon
code, so the Linux build the verification guidance asks of daemon changes does
not apply to it.>

## Alternatives rejected

- <D1. Two raw pickers, approval policy and sandbox mode: users would have to
  learn which combinations make sense. The presets cover the useful pairs.>
- <D2. A durable mid-session switch: pi would report every switch to the daemon
  and the app would need a per-session badge, for a choice Codex itself does
  not keep.>
````

The same plan planted with its checks. The plot holds the check every child proves, and each child holds its own:

```json
{
  "title": "Pi permission presets",
  "body": "<the plan body above>",
  "checks": [
    {"id": "C5", "shared": true, "text": "The child is delivered as the repository delivers changes, with that step's own checks passing. The report links the pull request or the landed commits."}
  ],
  "children": [
    {"title": "Daemon presets and launch intent", "body": "…",
     "checks": [{"id": "C1", "text": "A daemon wire test spawns a pi session with each preset, restarts the daemon and gets the same pair back. A wsctl run against a non-production daemon shows the same. Both outputs on this seed."}]},
    {"title": "Pi permissions switch and file-tool guard", "body": "…",
     "checks": [{"id": "C2", "text": "The pi plugin's approval tests for the switch and the file-tool guard pass. Output on this seed."}]},
    {"title": "App preset picker", "body": "…", "after": ["daemon-presets-launch-intent"],
     "checks": [
       {"id": "C3", "text": "An app wire test picks Read Only and sees the spawn message carry its pair. Output on this seed."},
       {"id": "C4", "text": "The real-app harness scenario creates a Read Only session, sees a write refused, switches with /permissions, restarts the daemon and sees Read Only again. Recording on this seed."}
     ]}
  ]
}
```

## Tracking and handoff

Record progress in seed states and notes. Plant deferred work as seeds. Do not keep a task checklist or activity log in the plan body.

When the approach changes, rewrite every section of the body that depends on the old approach, so the body reads as if the new decision had been the first decision. A new approach usually changes the design, the tradeoffs, the completion checks, and the alternatives rejected, not only the decision. Do not leave amendments, "superseded" markers, or revision numbers in the body. Record what changed and why in a note. Keep the D and F numbers. When you remove a decision or finding, leave its number unused, so a note or brief that cites the number still points at the same thing. Check ids follow the same rule: `attn seed check remove` keeps the id, and `restore` brings the check back with its result. `attn seed check edit` keeps a check's report and verdict, so reopen a check whose evidence no longer proves its new statement. When a term comes to mean something else, rename the term or rewrite each use.

Rewrite the briefs of children that are not yet harvested in the same way. Leave a harvested child's body as the record of what the child delivered, and add the work the change brings as a new child. A body edit does not reach a tender who is already working, so tell the tender of each growing child whose brief changed, with `attn agent msg <seed-id>`.

Read back the saved plan, children, and dependency links as a fresh agent. Can you tell what to build, where to start, which constraints apply, and how to establish completion? Would an implementer who never saw the spikes avoid every trap that the spikes hit? Can a reader who doubts a decision find the reason for the decision, and the cost it accepts, without opening a note or a transcript? Check that every design part is present or explicitly waived, that `attn seed show` lists each check on the seed that must prove it, and that each learning from a spike is in the body rather than only in the spike's artifacts. Then cut. Delete each line an implementer would not act on and a reviewer would not question, and delete every statement made twice. Show the user the proposed plan, its delivery units and their order, as [Delivery](delivery.md) describes. Name the seed or plot so the user can review it.

Recommend how to execute the plan. Recommend an Orchestrator when the plan requires coordinated or reviewed Builder work, or benefits from mixing harnesses or models between the coordinating agent and its Builders. Otherwise, recommend a single Builder. Explain the recommendation briefly.

With the plan, reflect your understanding of the work, the decisions you made while writing the plan, and the assumptions the implementer will rely on, so the user can correct them in the same answer. That reflection is the [Align](align.md) check before dispatch. Ask whether the user wants to review or adjust the plan, or dispatch, and wait for their answer. This checkpoint applies even when the user's earlier request included execution. Dispatch only when the user chooses dispatch after seeing the proposed plan and handoff, or when the user said explicitly, before the plan existed, to dispatch without reviewing it. A request that includes execution is not that instruction. When the user authorizes dispatch, record the reflection above and the user's instruction as notes on the plan seed, dispatch, and name the plan seed in your reply so the user can read it later. Keep execution seeds open when only the plan is complete.

For an authorized handover, keep the plan in the plan seed and record the next assignment and execution authorization in a handoff note. Use the delegation process in the `attn` skill to choose the explicit folder and checkout. Give the next agent the plan seed, code location, agreed scope, completion checks, and authorization. The successor reads the plan and handoff from the seed.
