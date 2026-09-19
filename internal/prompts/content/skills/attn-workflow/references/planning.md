# Planning

Develop a concrete implementation approach that the user can review and another agent can execute without this conversation. The plan lives in a garden seed or plot body. Do not create a standalone plan file.

Read the `attn` skill's garden guidance and run `attn seed guide` for seed and plot mechanics, child briefs and lifecycle rules. This reference says what the plan itself must contain. If the garden is unavailable, report what is missing; do not fall back to a file.

## Workflow

1. **Find the work.** Read the relevant implementation seed or plot, its notes, and any children. Reuse existing work and preserve its scope and decisions. If the work has no implementation seed, plant one to hold the plan.
2. **Investigate the approach.** Start from the user's request, relevant conversation, and any vision referenced by the work or supplied by the user. Read vision seeds with `attn seed show <id>`. Read enough code to identify the components, entry points, state, interfaces, and ownership involved. Trace production and test paths where they differ. Propose an approach from your findings. Ask about choices or assumptions that could change the plan.
3. **Write the plan.** Fill the template below into the seed or plot body. If there is a vision seed, reference its ID; do not overwrite its body with the implementation plan.
4. **Organize execution.** Use a plot when the work has distinct pieces to scope and track separately, even within one PR. Use a single seed for one coherent task. Add or reuse child seeds for each unit of work, with an outcome, scope, and verification; refer to the parent plan without repeating it. When delivery is by pull request, explain which changes belong in each one. Add `blocks` links only for actual prerequisites; otherwise leave children independent.

## The plan body

Write for an implementer starting fresh and a user reviewing the direction. The body describes the current design; it is not a record of the conversation. Who decided what, when, and what the user authorized go in seed notes. A body that reads as meeting minutes is not a plan.

The body has five sections in this order. Each design part is required; a part that does not apply becomes one line with the reason in its place.

- **Task and outcome**: what to build and what done looks like, in a few sentences.
- **Decisions**: the choices the user might still change, each with its reason, and the open questions, saying which block work and which the implementer may decide.
- **Design**, in four parts:
  - **Ownership**: each file or package that changes, as a shallow file tree with one comment per entry saying what it is responsible for.
  - **Interfaces**: the types, signatures, wire messages and persisted data the change adds or alters, sketched in the codebase's language, with the schema, generation and migration steps a wire or storage change requires.
  - **Behavior**: how control and data flow through the changed parts, as a sequence for messages between processes, a call tree within one process, or pseudocode for a rule.
  - **State**: each piece of state with its owner, what sets it, what reads it, what happens on failure and what happens on restart. In-memory state counts as state and has the same entry.
- **Execution**: for a plot, the children, which changes belong to each pull request, and their order; for a single seed, one line saying so; without pull requests, the delivery step.
- **Completion**: the checks the repository's verification guidance requires for the affected surfaces and where the evidence is recorded, or the documented exemption when one applies.

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
preset, and switch them mid-session with /permissions. The daemon's auto-mode
config stays the default; a session can differ from it. Done when a session
created as Read Only shows read-only in pi's status line, refuses a write until
the agent escalates, switches on /permissions default, and relaunches as Read
Only after a daemon restart.>

## Decisions

- <Presets are the user-facing unit, with Codex's names and descriptions, because
  users already know them; the raw pair stays reachable on the CLI.>
- <A mid-session switch is not durable, like Codex, so the daemon knows only the
  launch choice and the app shows no per-session badge.>

Open: <whether `--yolo` on a pi launch maps to the full-access preset (blocks the
CLI child)> · <the picker's position beside auto mode (the app tender decides)>

## Design

Ownership:

    <internal/automode/automode.go          Preset type; Presets(); PresetFor(policy, mode)
     internal/protocol/schema/main.tsp      approval_policy and sandbox_mode on SpawnSessionMessage
     internal/protocol/constants.go         ProtocolVersion bump
     app/src/hooks/useDaemonSocket.ts       PROTOCOL_VERSION bump
     internal/daemon/spawn_pipeline.go      applies the launch intent's pair over the daemon default
     plugins/attn-pi/approval/session.ts    /permissions picker; repaints the status line
     app/src/components/LocationPicker.tsx  preset control beside auto mode>

Interfaces:

<```go
type Preset struct {
    ID, Label, Description string
    ApprovalPolicy         string
    SandboxMode            string
}
func PresetFor(policy, mode string) (Preset, bool) // false when no preset matches
```

```diff
 model SpawnSessionMessage {
   cwd: string;
+  approval_policy?: string;
+  sandbox_mode?: string;
 }
```

New wire fields: edit main.tsp, make generate-types, bump ProtocolVersion and
PROTOCOL_VERSION.>

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
       on restart:  relaunched with the stored pair; a mid-session switch is lost>

## Execution

<Three children, each its own pull request against next, in this order: the
daemon and protocol (presets, wire fields, launch intent), then pi (/permissions
and the file-tool guard), then the app picker. The pi child blocks nothing; the
app child waits for the daemon child's wire fields.>

## Completion

<Go tests for PresetFor and the spawn pipeline's intent merge; the pi plugin's
approval tests for the switch; a frontend test for the picker. The real-app
harness scenario creates a Read Only session, switches it, restarts the daemon,
and records the run; the recording and the scenario output go on the app child.>
````

## Tracking and handoff

Seed states and notes carry progress. Update the plan body when the approach changes, and record the reason in a note. Plant deferred work as seeds. Do not keep a task checklist or activity log in the plan body.

Read back the saved plan, children, and dependency links as a fresh agent: can you tell what to build, where to start, which constraints apply, and how to establish completion? Check that every design part is present or explicitly waived. Show the user the proposed plan and, when pull-request delivery applies, its proposed pull-request boundaries and ordering. Name the seed or plot for review.

Recommend how to execute it. Recommend an Orchestrator when the plan requires coordinated or reviewed Builder work, or benefits from mixing harnesses or models between the coordinating agent and its Builders. Otherwise, recommend a single Builder. Explain the recommendation briefly.

Ask whether the user wants to review or adjust the plan, or dispatch, and wait for their answer. This checkpoint applies even when their earlier request included execution. Dispatch only when the user chooses dispatch after seeing the proposed plan and handoff. Agreement on the plan alone does not authorize execution. Keep execution seeds open when only the plan is complete.

For an authorized handover, keep the plan in its seed and record the next assignment and execution authorization in a handoff note. Use the delegation process in the `attn` skill to choose the explicit folder and checkout. Give the next agent the plan seed, code location, agreed scope, verification expectations, and authorization. The successor reads the plan and handoff from the seed.
