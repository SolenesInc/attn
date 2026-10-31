# The Chief

The Chief is the agent that looks after your other agents. You shape work with
it and decide what gets delegated. It follows what each agent reports, brings
you blockers, collisions and the decisions that need you, and journals your day
in the Notebook. Ask it where things stand, or go straight to an agent and work
beside it.

Each profile has at most one Chief. Make any agent your Chief with **Make chief
of staff** in its ••• menu, or turn **Chief of Staff** on when you start one;
**Remove chief role** takes the role away. The home screen's Chief of Staff
card shows it.

## Give the Chief a folder

Start the Chief in a folder of its own, where you describe how you work and
keep the plans and knowledge that span your repos:

```text
chief-of-staff/
├── AGENTS.md          how you want to be supported, decide and delegate
├── CLAUDE.md          -> AGENTS.md, for Claude Code
├── projects/          active efforts and why they matter
├── areas/             ongoing responsibilities and standards
├── resources/         reference material
└── archive/           finished work
```

`AGENTS.md` tells the Chief what good support looks like, how you make
decisions, when to explore and when to act, how to propose delegations, and
where your important code and systems live. A starter:

```md
# My chief of staff

This is my coordination and knowledge workspace. Help me manage my agents and
act as my thinking partner.

## Working relationship

- Reduce my cognitive load. Surface what matters and make the next move clear.
- Gather evidence before recommending action.
- When a choice is ambiguous, frame the decision and its tradeoffs.

## Delegation

- I decide every delegation; a goal is not approval to run it end to end.
- Propose a delegation, harness, and model when a separate specialist would
  help.
- Give each agent a clear outcome, context, constraints, and handoff.
- Track what the agents report and bring me blockers, collisions, and
  consequential decisions.

## Organization

- Read projects/index.md before discussing priorities or incoming work.
- projects/ holds time-bound efforts with a clear outcome.
- areas/ holds ongoing responsibilities and standards.
- resources/ holds reference material useful across projects.
- archive/ holds inactive material. Move completed projects here.

## Boundaries

- Planning, research, decisions, and coordination live here.
- Implementation, tests, commits, and pull requests live in the owning repo.
```

To keep the Notebook in the same folder, open **Settings → Notebook Folder**
and point **This profile** at `chief-of-staff/notebook`, or **Shared** to use
it for every profile without a folder of its own. Add `notebook/.attn/` to
`.gitignore` if the folder is a repository; it is machine state. Changing a
Notebook folder doesn't move existing notes.

Then press Cmd+N (Ctrl+Shift+N on Linux), pick a harness, select the
`chief-of-staff` folder, turn **Chief of Staff** on, and give it a mission,
such as: "We need to ship the new import flow this week. Help me understand
what's already in flight, suggest how to break it down, and prepare delegation
options."

## The Chief as a crew member (experimental)

With **Settings → Experimental → Crew v2** (`crew_v2_enabled`) on, the Chief is
a [crew member](crew.md) instead of a session you mark, and every profile has
one, named Chief:

- `chief` reaches it under any name, in `attn agent msg`, `attn crew` and the
  other commands that take a member. You can rename it like any member.
- It can sleep, but it can't be closed or retired, and Attn never sends it
  heartbeats or puts it to sleep on its own. Deleting a profile needs its Chief
  asleep, and retires it.
- It has its own row at the top of the sidebar's Crew section. A Chief with
  nothing to start from has no harness and stays asleep, keeping its mail.
  Until you set it up, its row opens the setup: pick Claude Code or Codex, the
  model and effort, and the profile's Notebook folder, then press Enter to wake
  it. The Chief's home moves into the folder you pick. If neither harness is
  installed, the setup says so; install one and choose "Check again". From a
  shell, `attn crew set chief --agent <harness> --model <model>` sets it up,
  and `attn crew wake chief` wakes it.
- The Chief's menus offer "Harness and model…" to change what it runs on for
  its next wake, "Edit charter", and Sleep in place of Close. It wakes with one
  click. The command palette has the same actions.
- It runs on its own launch settings (`attn crew set chief`). The Chief's
  model, effort and context budget in **Settings → Background agents**, and
  the Chief of Staff card on Home, apply only to a Chief session with Crew v2
  off.
- If a session was your Chief, the new Chief starts with that session's
  harness, model, effort and folder, and the session becomes an ordinary one.
  On its first day the new Chief can message that session for its open work
  and context.
- Turning Crew v2 off gives the Chief role back to the session that had it.

### The Chief's first day

On its first day the Chief introduces itself, agrees its name and charter with
you, and onboards you, one question at a time. Skip anything you don't want.

- If you are new to Attn, it takes the full path: delegation roles, how the
  Notebook is laid out, a tour of the app with callouts, an offer to help
  Attn's maintainers, and, if you like, building something small together.
- If you already used Attn before the Chief, it takes the tour: what's new
  about it, the steps you haven't set up yet, and the same offer. If it took
  over from your previous Chief session, it says so and can ask that session
  for its open work.

Helping Attn's maintainers means publishing usage analytics to them. No
personal information is sent; [telemetry](telemetry.md) says what is recorded.
Vents are an extra layer, short reports agents file when Attn gets in their
way. You choose them separately and can send only the ones you pick. You can
also let agents vent about your own tools and environment; those vents never
leave your machine. Nothing turns on unless you say yes to it.

The Chief changes Attn for you with its commands, and tells you what each one
did. During onboarding it can point at the app even when Agent callouts is off.
Onboarding can span several sessions: the Chief keeps its progress in its
letter and picks up where it left off.

| Command | What it does |
|---|---|
| `attn onboarding` | Whether onboarding is pending, and which path it takes |
| `attn onboarding done` | Marks onboarding finished; the Chief runs it when the agenda is through |
| `attn onboarding again` | Runs the full path again from the Chief's next session |

Ask the Chief for a skipped step anytime.
