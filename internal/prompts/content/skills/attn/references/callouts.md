# Callouts

Point at the attn app when the user asks where something is, during a guided
first day, or when you name another agent the user should go to. A callout is
short text beside a named part of the app window. The user steps through it.

Callouts are experimental and off unless the user turns them on in
Settings > Experimental > Agent callouts. Both commands check that setting
each time, except when the Chief runs them while the Chief's onboarding is
pending. A refusal names the toggle. Let the user decide whether to enable it.

List the anchors first, then point:

```sh
attn callout anchors
attn callout show garden "Work you and your agents track"
attn callout show sidebar "Your agents, the ones waiting on you first" \
  garden "Work you and your agents track" \
  commands "Everything attn can do, searchable (⌘K)"
attn callout show agent:a1b2 "Needs your review on the migration plan"
```

`anchors --json` gives names, descriptions and which fixed anchors are on
screen. The fixed anchors are `sidebar`, `home`, `commands`, `garden`,
`desktops`, `queue`, `crew` and `settings-button`. Which of them are visible
depends on the sidebar's shape. `queue` is the waiting-turns card in queue flow.

`agent:<who>` accepts a session id, short id or crew name.
`desktop:<which>` accepts a slot digit, name or id. Both address only the
pointing session's profile. The app reveals the target when it can, or shows
the step with its name. When the user presses Enter, the app goes to the
target and ends the callout. → skips the step.

Keep each step short, at most 160 characters. A callout can have at most 20
steps. If a fixed anchor is missing, the command refuses the whole callout and
names the anchors on screen. Choose a visible anchor from that answer.

The command returns once the app accepts the callout. Continue your work,
because there is no completion callback. A new callout replaces the one
showing. The user advances with Enter or → and ends the callout with Esc or a
click outside. Another overlay or a profile switch also ends the callout.

The desktop app must be showing your profile. With no such app, `anchors`
still lists the directory and says so, and `show` refuses. Every refusal exits 1.
Use `--session <id>` only when pointing from another session.
