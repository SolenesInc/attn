# Callouts

Point at the attn app when the user asks where something is, during a guided first day, or when you name another agent the user should go to. A callout is short text beside a named part of the app window. The user steps through it.

## Anchors

The fixed anchors are `sidebar`, `home`, `commands`, `garden`, `asks`, `desktops`, `queue`, `crew` and `settings-button`. Which of them are visible depends on the sidebar's shape. `queue` is the waiting-turns card in queue flow.

`agent:<who>` accepts a session id, short id or crew name. `desktop:<which>` accepts a slot digit, name or id. Both address only the profile of the pointing session. The app reveals the target when it can. Otherwise the app shows the step with the target's name.

- `attn callout anchors [--json]`: list the anchors and which are on screen

## Showing a callout

Run `attn callout anchors` first, to pick an anchor the user can see. Put the agent or desktop the user should go to on the last step, because Enter and → go there only from the last step. If a fixed anchor is missing, the command refuses the whole callout and names the anchors on screen. Choose a visible anchor from that answer.

The command returns once the app accepts the callout. Continue your work, because there is no completion callback.

The user advances the callout with Enter or →. On every step except the last, either key moves to the next step. On a last step that names an agent or desktop, either key goes to it and ends the callout. Esc, the card's ×, a new callout or a profile switch ends the callout. A callout with one step also ends on another overlay or a click elsewhere.

A callout with several steps lets the user try what it points at. Another overlay, such as the command palette, hides the card, and the card comes back on the same step when the overlay closes. After a click elsewhere the card stays, and Enter, → and Esc go to what the user clicked until they click the card.

The desktop app must be showing your profile. With no such app, `attn callout anchors` still lists the directory and says so, and `attn callout show` refuses. Use `--session <id>` only when pointing from another session.

- `attn callout show <anchor> <text> [<anchor> <text>]... [--json]`: show a callout the user steps through
