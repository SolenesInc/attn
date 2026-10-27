# Onboarding

Read this when your priming says onboarding is pending, or the user asks to
run it again. This is the Chief's checklist. Run it as a conversation, with
one question at a time and room to skip anything.

## Posture

The user already knows a coding agent, but may be using attn for the first
time. Explain more than on an ordinary day. Offer help and do the work when
asked. For a how-to question, read `attn help` and the relevant topic, then
present what helps in the terminal. Never answer only by pointing elsewhere.

Use attn verbs to change attn. Inspect their help, read the current state,
and report what each command printed. Bring up a setting when it matters;
`attn settings list --all` lists preferences and the harnesses attn found.

Read `attn onboarding` and your predecessor's letter before opening. Resume
an interrupted checklist from what remains. If the user asks to restart,
run `attn onboarding again`, then use the full path. Ask whether they have
used attn before when the suggested path does not fit; adapt the depth with
them. A replay starts with the full agenda, even in an established profile.

## Full path

Keep this checklist privately. Each item can be done or skipped.

1. Open with a short agenda, for example: "Here's what I'd like to cover:
   your delegation roles, how your Notebook is laid out, a quick tour of the
   app, your analytics choice, and building something together if you want.
   Skip anything."
2. Set up delegation roles from the harnesses attn found. Read
   `attn settings list --all` (`<harness>_available`) and
   `attn delegate roles show`. If only Claude is found, ask whether the roles
   should use only Claude or whether the user expects Codex to work too
   (and vice versa). If several are found, offer concrete choices: planning
   on a deep model and building on a cheaper one, or reviewing on a different
   harness than the one that built. Read the delegation-roles reference.
   Once the user chooses, run `attn delegate roles install -m "<reason>"`,
   then `attn delegate roles set <role> --agent <harness> --model <id>`
   with their reason. Inspect the harness model catalog to resolve model IDs;
   do not invent IDs. Preserve roles already set up. Named instances refuse
   the user-global skill install; explain the printed refusal and offer
   custom roles through `attn delegate roles add`.
3. Explain the Notebook in its existing folder. Read its `index.md` and the
   notebook reference. Show where notes live and why:

   ```text
   index.md             where agents orient and learn the user's layout
   journal/<date>.md    the lasting record of the day
   knowledge/           projects, areas, resources and archive by convention
   seeds/<id>/          seed artifacts
   crew/<name>/         crew charters and letters
   .attn/               attn's machine state
   ```

   `journal/`, `seeds/`, `crew/` and `.attn/` are fixed.
   The knowledge layout is the user's to reshape. Ask whether it suits them;
   record any agreed layout in `index.md` so other agents follow it. The
   Notebook folder was picked before this wake; do not ask for it again.
4. Offer a tour with callouts. Read the callouts reference and
   `attn callout anchors`, then choose visible anchors. Introduce the sidebar,
   agent states, your slot (`agent:chief`), the garden and the command
   palette. Use `attn callout show` with a sequence of short steps. The Chief
   can show callouts while onboarding is pending even when Agent callouts is
   off. Explain how to step through, and ask the user to return to your
   terminal afterwards. The command's reply means the tour was accepted;
   wait for the user to tell you they are ready before continuing.
5. Offer the analytics choice below.
6. Ask whether the user wants to build something together. If so, read
   `attn help building-with-roles` and propose a small version of that flow.
   Get their agreement on the task and any delegation before starting it.
   Start in desktop flow. Once two or more agents are running, you may offer
   queue flow: attn brings the next agent that needs them, but they hand over
   which agent comes next, and it can be more tiring. Keep it opt-in.
7. Close as described below.

Do not pitch crew members or offer to rename the Chief. Create crew only
when asked, with a name the user picks. Auto mode, worktree preferences and
planning the day are outside this agenda; answer questions when asked.

## Analytics choice

Read `attn help telemetry` and the current `analytics.enabled`,
`analytics.sharing_enabled` and `analytics.share_vents` settings. Explain
the current choice before asking to change it. These settings apply across
all profiles in this instance.

Explain what the user is choosing:

- Local recording is experimental and off by default. It records attn
  activity, commands and app actions, plus resource samples. Names, paths
  and other free text are redacted; identifiers are hashed.
- Sharing is a separate opt-in, also off by default. It uploads that record
  to attn's maintainers. Settings › Analytics shows exactly what is stored
  and sent, and lets the user turn sharing off or delete the record.
- Attn vents contain the agent's report text. They leave only when the user
  sends one or enables automatic vent sharing. Explain any existing
  `analytics.share_vents` choice before offering uploads. Tool vents stay
  on the machine.

Ask whether to leave analytics off, record only on this machine, or record
and share with attn's maintainers. Wait for an explicit answer before
changing any setting. Skipping leaves the current settings as they are.
For local-only recording, turn sharing off and enable `analytics.enabled`.
Sharing needs both `analytics.enabled` and `analytics.sharing_enabled` on.
Do not change `analytics.share_vents` or `analytics.tool_vents` without a
separate answer. Use `attn settings set`, check its replies and read back
the resulting values. Tell the user how to revisit the choice in Settings.

## Tour path

This profile was in use before its Chief. Start with what changed: you are
permanent, your home lives in the Notebook's `crew/<name>/`, and your charter
is a file the user can edit there.

Read `attn crew list`, `attn delegate roles show`, and the relevant settings.
Cover only missing setup. Offer to move legacy crew homes into the Notebook
on demand; read the crew reference and `attn crew move-home --help` first.
The move requires the member to be asleep. Do not move homes unasked.

Offer callouts for your slot and what is new. Use the full checklist's
Notebook and roles steps only where needed. Skip what is already set,
including a queue already in use. Do not offer to build something unless the
user asks. Offer the analytics choice, then close.

## Interruptions and close

Keep checklist progress in your letter when sleeping, napping or handing
over. There is no separate progress file. After `/clear`, `attn crew prime`
refreshes priming, `attn onboarding` reports whether the first day is pending,
and your letter records what was covered. Check live state through the verbs
before repeating a change.

When every agenda item is done or skipped, recap what changed and how to
change it later. Skipped items remain available: the user can ask anytime.
Explain that `attn onboarding again` runs the full path again, then run
`attn onboarding done`. Check the reply before saying onboarding is finished.
