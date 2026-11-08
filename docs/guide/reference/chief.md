# The Chief

The Chief is the agent that looks after your other agents. You shape work with
it and decide what gets delegated. The Chief follows what each agent reports,
brings you blockers, collisions and the decisions that need you, and journals
your day in the Notebook. Ask the Chief where things stand, or go straight to
an agent and work beside that agent.

The Chief is a [crew member](crew.md). Every profile has exactly one, named
Chief, from the start. It has a charter and letters like any member, and sleeps
and wakes like one.

## Set up the Chief

The Chief has its own row at the top of the sidebar's Crew section. A new Chief
has no harness, so it stays asleep and keeps its mail. Until you set it up, its
row opens the setup: pick Claude Code or Codex, the model and effort, and the
profile's Notebook folder, then press Enter to wake the Chief. The Chief's home
moves into the folder you pick. If neither harness is installed, the setup says
so. Install one and choose "Check again".

From a shell:

```sh
attn crew set chief --agent <harness> --model <model>
attn crew wake chief
```

## Where the Chief works

The Chief's home is `crew/chief/` in your profile's Notebook, and it works
there. Its charter is the place for how you want to be supported, how you make
decisions and how you want delegations proposed. The Chief writes the charter
with you on its first day, and you can edit it anytime.

To run the Chief in a folder of your own, such as a repository of plans and
knowledge that span your projects, run `attn crew set chief --cwd <dir>`. The
folder's `AGENTS.md` or `CLAUDE.md` then applies as it does for any agent.

## How the Chief differs from other members

The [crew guide](crew.md) covers waking, sleeping, letters and launch settings.
The Chief follows it, with these differences:

- `chief` always names the Chief in `attn agent msg`, `attn crew` and the other
  commands that take a member, even after you rename the Chief.
- The Chief can sleep, but it can't be closed or retired, and Attn never sends
  it heartbeats or puts it to sleep on its own. Deleting a profile needs its
  Chief asleep, and retires the Chief.
- The Chief's menus offer "Harness and model…" to change what it runs on for
  its next wake, "Edit charter", and Sleep in place of Close. The Chief wakes
  with one click. The command palette has the same actions.
- With "Put the crew in the queue" on in Settings → Attention queue, a Chief
  that is owed a turn waits in the queue and keeps its row, and you settle or
  snooze it like any agent. When a delegate reports, the Chief asks you for a
  decision only you can make, opens its turn for a result you should read, and
  otherwise only records the report. Answering its ask takes it out of the
  queue.

## If a session was your Chief

Earlier versions of Attn made a session the Chief. When you upgrade, the new
Chief starts with that session's harness, model, effort and folder, and the
session becomes an ordinary session. On its first day the new Chief can message
that session for its open work and context.

## The Chief's first day

On its first day the Chief introduces itself, agrees its name and charter with
you, and onboards you, one question at a time. Skip anything you don't want.

- If you are new to Attn, the Chief takes the full path: delegation roles, how the
  Notebook is laid out, a tour of the app with [callouts](callouts.md), an offer to help
  Attn's maintainers, and, if you like, building something small together.
- If you already used Attn before the Chief, the Chief takes the tour: what's
  new about Attn, the steps you haven't set up yet, and the same offer. If the
  Chief took over from your previous Chief session, the Chief says so and can
  ask that session for its open work.

Helping Attn's maintainers means sharing up to two things, and you choose each
separately:

- Usage analytics. They contain no personal information.
  [Telemetry](telemetry.md) lists what is recorded.
- Vents, which are short reports that agents file when Attn gets in their way.
  A vent's text can mention things from your work, such as a project name.
  Read a vent before you send it, and send only the ones you pick. Or turn on
  automatic sending.

You can also let agents vent about your own tools and environment. Those vents
never leave your machine. Nothing turns on unless you say yes.

The Chief changes Attn for you with its commands, and tells you what each one
did. During onboarding the Chief can point at the app even when [Agent callouts](callouts.md) is off.
Onboarding can span several sessions: the Chief keeps its progress in its
letter and continues from there.

| Command | What it does |
|---|---|
| `attn onboarding` | Whether onboarding is pending, and which path it takes |
| `attn onboarding done` | Marks onboarding finished. The Chief runs it when the agenda is done |
| `attn onboarding again` | Runs the full path again from the Chief's next session |

Ask the Chief for a skipped step anytime.
