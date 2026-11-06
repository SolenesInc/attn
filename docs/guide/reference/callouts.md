# Callouts

A callout is short text from an agent, shown beside a named part of the app
window. Ask an agent where something is, and it can point at the thing instead
of describing your screen. An agent can also point you at another agent or a
desktop, and take you there in one key.

Callouts are experimental.

## Turn them on

**Agent callouts** in **Settings → Experimental** is off by default. While the
setting is off, Attn refuses the agent's command and names the setting, so the
agent can tell you. `attn settings set callouts_enabled true` does the same as
the switch.

The one exception is [the Chief's first day](chief.md#the-chiefs-first-day): the
Chief can tour the app with callouts during its onboarding, even with the setting
off.

## Step through a callout

The card shows the agent that is pointing, the step count, and the text. A ring
marks the part of the window it points at.

| Input | What it does |
|---|---|
| Enter or → | Moves to the next step |
| Enter or → on a last step that names an agent or a desktop | Goes there and ends the callout |
| Enter or → on any other last step | Ends the callout |
| Esc, or **End callout** | Ends the callout |

A profile switch also ends the callout. A callout with one step also ends when
you click outside the card or open another overlay, such as Settings.

A callout with several steps stays so you can try what it points at. Another
overlay hides the card, and the card comes back on the same step when the overlay
closes. After a click elsewhere the card stays, and Enter, → and Esc go to what
you clicked until you click the card.

## What an agent can point at

Agents point at these named parts of the window:

| Anchor | What it marks |
|---|---|
| `sidebar` | The sidebar in its current shape |
| `home` | Home |
| `commands` | Search and run commands |
| `garden` | Work tracked in the garden |
| `desktops` | The desktop chips |
| `queue` | The waiting-turns card in queue flow |
| `crew` | The crew |
| `settings-button` | Open Settings |
| `agent:<who>` | An agent by session id, short id, or crew name |
| `desktop:<which>` | A desktop by slot digit, name, or id |

Which of the named parts are on screen depends on the sidebar's shape and on your
flow. Agents and desktops work only in the profile of the agent that points, and
the app reveals the target when it can. When it cannot, the step tells you that
the target isn't on screen, and Enter on a last step still goes there.

## How an agent points

You can ask in plain words, such as "show me where the garden is". The agent
lists the anchors, then points:

```sh
attn callout anchors
attn callout show sidebar "Your agents, the ones waiting on you first" \
  garden "Work you and your agents track"
attn callout show agent:a1b2 "Needs your review on the migration plan"
```

Each anchor and text pair is one step. A new callout replaces the one showing.
To point from your own shell instead of an agent, add `--session <id>` with the
agent the card should name.

Attn refuses the whole callout, and says why, in these cases:

- A step's text is longer than 160 characters.
- The callout has more than 20 steps.
- A named part of the window is not on screen. The refusal lists what is on
  screen. An open overlay hides them all, so close it and ask again.
- The app is not showing the agent's profile.
