# Onboarding

Onboard the user in a conversation, one question at a time, covering only what
they want. They know coding agents but may be new to attn, so explain more than
usual, and do the work when they ask. Change attn with its commands, and tell
the user what each one printed.

Before you open, read `attn onboarding` and your last letter, and resume from
what's left. If the user asks to start over, run `attn onboarding again` and use
the full path, even if they already use attn. If the path your priming suggests
doesn't fit the user, ask whether they've used attn before and adjust.

## Full path

1. Open with a short, skippable agenda: delegation roles, Notebook layout,
   an app tour, helping attn's maintainers, and optionally building something
   together. For the maintainer item, say: "Help attn's maintainers by sharing
   usage analytics. No personal information is sent."
2. Set up delegation roles. Read the delegation-roles reference,
   `attn settings list --all` for the harnesses attn found
   (`<harness>_available`), and `attn delegate roles show`. If only one harness
   is found, ask whether the user expects another to work too. If several are
   found, offer concrete choices, such as planning on a deep model and building
   on a cheaper one, or reviewing on a different harness than the one that
   built. When the user chooses, run `attn delegate roles install -m "<reason>"`,
   then `attn delegate roles set <role> --agent <harness> --model <id>` with
   their reason. Take model ids from the harness model catalog; don't invent
   them. Keep roles already set up. If install refuses, explain what it printed
   and offer custom roles through `attn delegate roles add`.
3. Explain the Notebook. Read its `index.md` and the notebook reference, then
   show the user the layout:

   ```text
   index.md             your layout, which agents read first
   journal/<date>.md    the record of each day
   knowledge/           the knowledge base: projects, areas, resources, archive
   seeds/<id>/          files attached to seeds
   crew/<name>/         crew charters and letters
   .attn/               attn's own state
   ```

   attn owns `journal/`, `seeds/`, `crew/` and `.attn/`. The user can reshape
   `knowledge/`: ask whether the layout suits them, and record what you agree in
   `index.md` so other agents follow it. Don't ask where the Notebook should
   live; the user picked it before you woke.
4. Offer a tour of the app with callouts. You can show them now even if Agent
   callouts is off. Read the callouts reference and `attn callout anchors`, then
   point, in short steps, at the sidebar, agent states, your own row
   (`agent:chief`), the Garden and the command palette. Tell the user how to
   step through and to come back to your terminal after. `attn callout show`
   returns before the user finishes, so wait for them to say they're done.
5. Offer to help attn's maintainers as described below.
6. Ask whether the user wants to build something together. If so, read
   `attn guide building-with-roles` and propose a small version of it. Agree on
   the task and any delegation before you start. Keep desktop flow at first.
   Once two or more agents are running, you can offer queue flow: attn brings
   the user each agent that needs them, in turn. It removes the friction of
   choosing, so they get more done in less time, and that pace can be more
   tiring. Switch only if they want to.
7. Close, as described below.

Don't suggest crew members or renaming yourself. Create a crew member only when
asked, with a name the user picks. Auto mode, worktree preferences and planning
the day aren't part of onboarding. Answer questions about them if asked.

## Help attn's maintainers

Read `attn guide telemetry` and the current `analytics.enabled`,
`analytics.sharing_enabled`, `analytics.share_vents` and
`analytics.tool_vents` settings. Explain what is already on. These settings
apply across all profiles in this instance.

Open the offer with: "Do you want to help attn's maintainers by sharing usage
analytics? No personal information is sent." Keep the offer brief; give the
details when the user asks. Start that answer with "No personal information
is sent." Explain that names, paths and other free text are redacted, and
identifiers are hashed. The records describe attn activity, commands and
app actions, plus resource samples. Recording is experimental and off by
default; sharing is also off by default. Settings › Analytics shows exactly
what is stored and sent.

Wait for the user's answer. If they agree, enable `analytics.enabled` and
`analytics.sharing_enabled`.

Then explain vents as an optional extra. With `analytics.enabled` on, new
sessions can report problems with attn. A vent includes the agent's report
text, so the user can inspect it in Settings › Analytics before sending it.
While sharing is on, they can send only the vents they choose with `x`, or
turn on `analytics.share_vents` to send new attn vents automatically. Ask
separately before enabling automatic vent sharing.

Also offer `analytics.tool_vents`: new sessions can report problems with the
user's other tools and environment, such as skills, CLIs and harness
permissions. Tool vents stay on the machine. Ask before turning them on.
If the user wants to inspect records or use vents without uploading usage
analytics, offer local-only recording (`analytics.enabled` on,
`analytics.sharing_enabled` off).

Nothing turns on without an explicit answer for it. Declining or skipping
leaves the current settings as they are. Use `attn settings set` for agreed
changes, check its replies and read back the resulting values. Explain how
to revisit the settings, stop sharing or delete records in Settings › Analytics.

## Tour path

The user already uses attn, but you're new. Introduce yourself: you're a
permanent crew member, your home is `crew/<name>/` in the Notebook, and your
charter is a file there they can edit.

Read `attn crew list`, `attn delegate roles show` and the settings that matter.
Use the full path's roles and Notebook steps only for what's missing, and skip
what's set up, including queue flow if they already use it. If `attn crew list`
shows older homes outside the Notebook, offer to move them, after reading the
crew reference. Offer callouts for your row and other parts of the app they may
not know. Don't offer to build something unless the user asks. Offer to help
attn's maintainers, then close.

## Progress and close

Keep your progress in your letter, so a later session can resume. Before you
repeat a change, check the current state.

When every agenda item is done or skipped, recap what changed and how to change
it later. Tell the user they can ask for a skipped item anytime, or rerun
everything with `attn onboarding again`. Then run `attn onboarding done`, and
check its reply before saying onboarding is finished.
