# Planning

Principles this process relies on: [Refactor first](../principles/refactor-first.md), [Redesign around the new requirement](../principles/redesign-around-the-new-requirement.md), [Model the domain first](../principles/model-the-domain-first.md), [Compare options before committing](../principles/compare-options-before-committing.md), [Subtract before you add](../principles/subtract-before-you-add.md), [Make operations idempotent](../principles/make-operations-idempotent.md), [Separate before serializing shared state](../principles/separate-before-serializing-shared-state.md), [Converge on the outcome](../principles/converge-on-the-outcome.md), [Sequence verifiable units](../principles/sequence-verifiable-units.md), [Experience first](../principles/experience-first.md), [Prove it works](../principles/prove-it-works.md), [Build the lever](../principles/build-the-lever.md). Read them before you start; [the skill's index](../SKILL.md#principles) lists the rest.

Develop a concrete implementation approach that the user can review and another agent can execute without this conversation. The plan lives in a garden seed or plot body. Do not create a standalone plan file.

Read the `attn` skill's garden guidance and run `attn seed guide` for seed and plot mechanics, child briefs and lifecycle rules. This reference says what the plan itself must contain. If the garden is unavailable, report what is missing; do not fall back to a file. Write the plan, its child briefs and its questions under [Technical writing](technical-writing.md); read it before the first draft.

## Workflow

1. **Find the work.** Read the relevant implementation seed or plot, its notes, and any children. Reuse existing work and preserve its scope and decisions. If the work has no implementation seed, plant one to hold the plan.
2. **Investigate the approach.** Start from the user's request, relevant conversation, and any vision referenced by the work or supplied by the user. Read vision seeds with `attn seed show <id>`. Read the results of any spikes and investigation for the work, including their code, notes, and reports. Read enough code to identify the components, entry points, state, interfaces, and ownership involved, and apply [Refactor first](../principles/refactor-first.md): list the existing paths that do the same job, with how each is addressed, triggered, and recovered, and record the list as a finding. Trace production and test paths where they differ. When the change moves ownership or layering, find out why the current shape exists, from its history, its pull requests and the decisions recorded in the garden, and record the reason as a finding: a reason that still holds is a constraint, and one that no longer holds is what the plan removes.
3. **Design it twice.** When the change adds or reshapes a boundary other code depends on, or moves ownership, sketch at least two structurally different shapes, whole designs rather than variations inside one, even when the first looks sufficient, and compare them with [Design](design.md). Other changes need one shape. When the choice is contested and costly to reverse, suggest building the shapes as [prototypes](prototype.md), as independent candidates on different models when the delegation roles offer more than one, as Prototype describes. The user decides whether to run them, as for any spike. The chosen shape becomes the Design; when it combines candidates, the Design is the base with each graft and the candidate it came from. The shapes that lost go under Alternatives rejected, and what a candidate revealed, such as a realistic case it broke on, becomes a finding. Ask about choices or assumptions that could change the plan.
4. **Write the plan.** Fill the template below into the seed or plot body. If there is a vision seed, reference its ID; do not overwrite its body with the implementation plan.
5. **Organize execution.** Use a plot when the work has distinct pieces to scope and track separately, even within one PR. Use a single seed for one coherent task. Add or reuse child seeds for each unit of work, with an outcome, scope, and its own checks; refer to the parent plan without repeating it. When delivery is by pull request, explain which changes belong in each one. Add `blocks` links only for actual prerequisites; otherwise leave children independent.

## The plan body

Write for an implementer starting fresh and a user reviewing the direction. The body states the current design; it is not a record of the conversation. Who decided what, when, through which question, and what the user authorized go in seed notes. A body that reads as meeting minutes is not a plan. What was learned is not history: it is the evidence the design rests on, and it belongs in the body.

A plan is one document that needs several modes, so it keeps one mode per section instead of the one mode per document that technical writing asks for. Terms and Design are reference. Task and outcome, Decisions, Tradeoffs accepted, Findings and Alternatives rejected explain, so a finding can say what it requires and what it rules out. Execution and Completion are how-to. Keep the section names below as written, even where technical writing would have a heading carry its point: orchestration and child briefs find a plan's parts by these names.

The body has these sections in this order. Each design part is required; a part that does not apply becomes one line with the reason in its place.

- **Task and outcome**: at most ten lines. What changes; why, in the one reason every decision traces back to; what done looks like; and what the work deliberately leaves out.
- **Terms**: each name the plan introduces or narrows, one line each. Use that name, and no other, for the thing everywhere in the plan and its children. Omit the section when the plan introduces no names.
- **Decisions**: the choices the user might still change, numbered (D1, D2) so other sections can cite them, each stated as it stands now with its reason, in a sentence or two. Put the decisions that shape the system first and the tactics an implementer could change after. Rejected options go under Alternatives rejected, and who decided goes in a note. Then the open questions, written so they can be answered cold: what each decides and which child or Builder waits on it, the options, your recommendation and why, what happens if it stays unanswered, and whether it blocks work or the implementer may decide it. When a refactor would let the change fit existing code, show the user both orders, refactor first or build beside it, with the smallest refactor that would do; replacing a whole subsystem is not the only alternative.
- **Tradeoffs accepted**: one line each, in the form "we accept X in exchange for Y", citing the decision it comes from when there is one (D2) instead of restating it. Name every choice a later reader could mistake for an oversight: a duplicated write, a cost paid for a reason that lives elsewhere, a behavior change, a simplification that looks premature. A cost with no stated exchange invites the next agent to remove it.
- **Findings**: what spikes and investigation established that the design depends on, written as described under Findings below. When nothing was learned beyond what the code shows, one line saying so.
- **Design**, in four parts:
  - **Ownership**: each file or package that changes, as a shallow file tree with one comment per entry saying what it is responsible for.
  - **Interfaces**: the types, signatures, wire messages and persisted data the change adds or alters, sketched in the codebase's language, with the schema, generation and migration steps a wire or storage change requires. For an interface the change adds or reshapes, write a representative call site first and derive the types from it; when they disagree, change the types. Check both with [Design](design.md). Name the invariants a later change could silently break, and what enforces each: a type, a check at the boundary, or the test of the promise it protects.
  - **Behavior**: how control and data flow through the changed parts, as a sequence for messages between processes, a call tree within one process, or pseudocode for a rule.
  - **State**: each piece of state with its one owner, what sets it, what reads it, what happens on failure and on restart, and, for state a retried or interruptible step writes, what a second or half-finished run leaves. In-memory state counts as state and has the same entry. State with two writers is a design problem to resolve, as Design describes, not an entry to record.
- **Execution**: for a plot, the children, which changes belong to each pull request, and their order, with an agreed refactor as its own behavior-preserving change that blocks the work built on it; for a single seed, one line saying so; without pull requests, the delivery step. A detail only one child needs, such as the cases its tests cover or a mechanism inside its scope, goes in that child's body.
- **Completion**: what the checks cannot hold, citing them by id. The checks themselves are seed checks, defined as data on the seeds that must prove them, as Checks below describes. This section keeps a documented exemption, where long evidence goes, and any order the checks must be proven in. When nothing is left over, one line saying the checks are on the seeds.
- **Alternatives rejected**: one line each, citing the decision the option lost to (D1) or naming the shape that lost when designing twice, and why it lost. Omit the section when nothing was rejected.

## Checks

A check is one numbered statement (C1, C2) a seed must prove, with the evidence that proves it and where that evidence goes: a test run, a recording, a measurement, a commit. attn keeps each check on the seed with its latest report and verdict, so define checks with `checks` in the plot JSON, or `attn seed check add` later, never as bullets in the body. Include the checks the repository's verification guidance requires for the affected surfaces, or the documented exemption when one applies. State each check at the level a reviewer judges the outcome.

Put each check on the seed that proves it. A check every child must prove goes on the plot once, with `"shared": true`, and each child proves it with its own report. A check only one child owns goes on that child. A check the plot proves as a whole, such as a recording after every child merges, goes on the plot without `shared`. Ids are unique across the plot tree, so C5 names one statement wherever a body, note or report cites it. Each child's body cites its checks by id and holds the detail behind them, such as the cases its tests cover.

## Findings

A plan built on a spike contains what the spike learned. Spike code, branches, transcripts, reports, and notes are evidence behind the plan, not a substitute for it: an implementer works from the plan, and a learning that lives only in an artifact is relearned, often wrongly. Link the artifacts as supporting evidence and write the learnings themselves into the body. The same holds for consequential discoveries from investigation.

Write each finding so an implementer can act on it without the spike:

- the fact, concretely: the behavior, mechanism, ordering, or limit observed
- the evidence: the function, type or file involved, a command and its output, a measurement, or the snippet that worked, quoted, because spike code is disposable
- what it requires of the design
- what it rules out: the approach that looks simpler or more natural, and why it fails

Name code by symbol and file, and state once, at the top of Findings, the commit the findings describe. Line numbers move with every change and leave the body wrong. Do not survey code the implementer can read; a finding is something the design depends on.

Number findings (F1, F2) and cite them where the design depends on them and in the child briefs whose work they constrain, so a child names the findings that apply without repeating them. When implementation overturns a finding, correct it in the body and record the reason in a note.

## Showing the design

A design part is shown, not described. A part answered with prose alone is incomplete.

When the shape already exists in the codebase, show its change as a diff of that shape: `+` and `-` lines on the file tree, the type, the call tree or the sequence. Show the whole shape when most of it is new, when a diff would hide ownership or order, or when the implementer needs a copyable target. A diff shows what changes where prose would describe it.

Scale each picture to the change: enough that the reader can judge the design without reconstructing it from the code, no more. Use names from the codebase. Put each picture beside the short explanation it supports, in a fenced or indented code block so it reads in the terminal and in the Garden. Keep pictures narrow; split a wide one into smaller views. Show separate production and test wiring when the distinction matters.

Choose the smallest view that explains the point:

- logic or an algorithm as pseudocode
- runtime control flow as a call tree
- UI structure as a component tree with the state and module boundaries that matter
- file responsibility or a broad refactor as a shallow file tree
- component interaction or data flow as labeled arrows
- messages between processes as a sequence
- a UI layout or state comparison as an ASCII wireframe

## Template

Copy the template and replace everything in angle brackets. The bracketed content is one example plan, a per-session permission preset for pi; it shows each part's form, and it shows a diff where the shape already exists and a whole block where it is new.

````markdown
<Let a pi session choose its approval policy and sandbox mode at creation, as a
preset, and switch them mid-session with /permissions. Today every pi session
runs under the daemon's one auto-mode config, so reviewing one risky change
means loosening or tightening all of them. The daemon's config stays the
default; a session can differ from it. Done when a session created as Read Only
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

<Three children, each its own pull request, in this order: the
daemon and protocol (presets, wire fields, launch intent), then pi (/permissions
and the file-tool guard), then the app picker. The pi child blocks nothing. The
app child waits for the daemon child's wire fields.>

## Completion

<The checks are on the seeds. C4's recording and scenario output go on the app
child as one attachment, which its report cites. The pi child changes no daemon
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
    {"id": "C5", "shared": true, "text": "The child's pull request is green on CI, carries a changelog fragment and has its review addressed. PR link in the report."}
  ],
  "children": [
    {"title": "Daemon presets and launch intent", "body": "…", "blocks": ["app-preset-picker"],
     "checks": [{"id": "C1", "text": "A daemon wire test spawns a pi session with each preset, restarts the daemon and gets the same pair back. A wsctl run against a non-production daemon shows the same. Both outputs on this seed."}]},
    {"title": "Pi permissions switch and file-tool guard", "body": "…",
     "checks": [{"id": "C2", "text": "The pi plugin's approval tests for the switch and the file-tool guard pass. Output on this seed."}]},
    {"title": "App preset picker", "body": "…",
     "checks": [
       {"id": "C3", "text": "An app wire test picks Read Only and sees the spawn message carry its pair. Output on this seed."},
       {"id": "C4", "text": "The real-app harness scenario creates a Read Only session, sees a write refused, switches with /permissions, restarts the daemon and sees Read Only again. Recording on this seed."}
     ]}
  ]
}
```

## Tracking and handoff

Seed states and notes carry progress. Plant deferred work as seeds. Do not keep a task checklist or activity log in the plan body.

When the approach changes, rewrite every section of the body that depends on it, so the body reads as if the new decision had been the first one. A new approach usually changes the design, the tradeoffs, the completion checks and the alternatives rejected, not only the decision. Do not leave amendments, "superseded" markers or revision numbers in it; record what changed and why in a note. Keep the D and F numbers: a removed one leaves its number unused, so a note or brief that cites a number still points at the same thing. attn holds check ids to the same rule: `attn seed check remove` keeps the id, and `restore` brings the check back with its result. `attn seed check edit` keeps a check's report and verdict, so reopen a check whose evidence no longer proves its new statement. When a term comes to mean something else, rename it or rewrite each use.

Rewrite the briefs of children not yet harvested in the same way. A harvested child's body stays the record of what it delivered; work the change adds becomes a new child. A body edit does not reach a tender who is already working, so tell the tender of each growing child whose brief changed with `attn agent msg <seed-id>`.

Read back the saved plan, children, and dependency links as a fresh agent: can you tell what to build, where to start, which constraints apply, and how to establish completion? Would an implementer who never saw the spikes avoid every trap they hit? Can a reader who doubts a decision find its reason, and the cost it accepts, without opening a note or a transcript? Check that every design part is present or explicitly waived, that `attn seed show` lists each check on the seed that must prove it, and that each learning from a spike is in the body rather than only in its artifacts. Then cut: delete each line an implementer would not act on and a reviewer would not question, and every statement made twice. Show the user the proposed plan and, when pull-request delivery applies, its proposed pull-request boundaries and ordering. Name the seed or plot for review.

Recommend how to execute it. Recommend an Orchestrator when the plan requires coordinated or reviewed Builder work, or benefits from mixing harnesses or models between the coordinating agent and its Builders. Otherwise, recommend a single Builder. Explain the recommendation briefly.

With the plan, reflect your understanding of the work, the decisions you made while writing it, and the assumptions the implementer will rely on, so the user can correct them in the same answer; this is the [Align](align.md) check before dispatch. Ask whether the user wants to review or adjust the plan, or dispatch, and wait for their answer. This checkpoint applies even when their earlier request included execution. Dispatch only when the user chooses dispatch after seeing the proposed plan and handoff, or when they said explicitly, before the plan existed, to dispatch without reviewing it. A request that includes execution is not that instruction. When it applies, record the reflection above and the user's instruction as notes on the plan seed, dispatch, and name the plan seed in your reply so the user can read it later. Keep execution seeds open when only the plan is complete.

For an authorized handover, keep the plan in its seed and record the next assignment and execution authorization in a handoff note. Use the delegation process in the `attn` skill to choose the explicit folder and checkout. Give the next agent the plan seed, code location, agreed scope, completion checks, and authorization. The successor reads the plan and handoff from the seed.
