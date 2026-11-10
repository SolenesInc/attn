# Callouts

Point at the attn app when the user asks where something is, during a guided
first day, or when you name another agent the user should go to. A callout is
short text beside a named part of the app window. The user steps through it.

List the anchors first, then point:

```sh
attn callout anchors
attn callout show garden "Work you and your agents track"
attn callout show sidebar "Your agents, the ones waiting on you first" \
  garden "Work you and your agents track" \
  commands "Everything attn can do, searchable (⌘K or ⇧⌘K)"
attn callout show agent:a1b2 "Needs your review on the migration plan"
```

`anchors --json` gives names, descriptions and which fixed anchors are on
screen. The fixed anchors are `sidebar`, `home`, `commands`, `garden`,
`asks`, `desktops`, `queue`, `crew` and `settings-button`. Which of them are visible
depends on the sidebar's shape. `queue` is the waiting-turns card in queue flow.

`agent:<who>` accepts a session id, short id or crew name.
`desktop:<which>` accepts a slot digit, name or id. Both address only the
profile of the pointing session. The app reveals the target when it can.
Otherwise the app shows the step with the target's name. Put the agent or desktop
the user should go to on the last step, because Enter and → go there only from
the last step.

Keep each step short, at most 160 characters. A callout can have at most 20
steps. If a fixed anchor is missing, the command refuses the whole callout and
names the anchors on screen. Choose a visible anchor from that answer.

The command returns once the app accepts the callout. Continue your work,
because there is no completion callback. A new callout replaces the one
showing.

The user advances the callout with Enter or →. On every step except the last,
either key moves to the next step. On a last step that names an agent or
desktop, either key goes to it and ends the callout. Esc, the card's ×, a new
callout or a profile switch ends the callout. A callout with one step also ends on
another overlay or a click elsewhere.

A callout with several steps lets the user try what it points at. Another
overlay, such as the command palette, hides the card, and the card comes back on
the same step when the overlay closes. After a click elsewhere the card stays,
and Enter, → and Esc go to what the user clicked until they click the card.

The desktop app must be showing your profile. With no such app, `anchors`
still lists the directory and says so, and `show` refuses. Every refusal exits 1.
Use `--session <id>` only when pointing from another session.
