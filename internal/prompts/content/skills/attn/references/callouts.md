# Callouts

Point at the attn app when the user asks where something is, during a guided
first day, or when naming another agent the user should go to. A callout is
short text beside a named part of the app window. The user steps through it.

Callouts are experimental and off unless the user turns them on in
Settings > Experimental > Agent callouts. Both commands check that setting
each time, except from the Chief while its onboarding is pending. A refusal
names the toggle; let the user decide whether to enable it.

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
`desktops`, `queue`, `crew` and `settings-button`. Their visibility depends on
the sidebar's shape; `queue` is the waiting-turns card in queue flow.

`agent:<who>` accepts a session id, short id or crew name.
`desktop:<which>` accepts a slot digit, name or id. Both address only the
pointing session's profile. The app reveals the target when it can, or shows
the step with its name. Enter goes there and ends the callout; → skips it.

Keep each step short, at most 160 characters. A callout can have at most 20
steps. A missing fixed anchor refuses the whole callout and names the ones
on screen. Use that answer to choose a visible anchor.

The command returns once the app accepts the callout. Continue your work;
there is no completion callback. A new callout replaces the one showing.
Enter or → advances; Esc or a click outside ends it. Another overlay or a
profile switch also ends it.

The desktop app must be showing your profile. With no such app, `anchors`
still lists the directory and says so; `show` refuses. Every refusal exits 1.
Use `--session <id>` only when pointing from another session.
