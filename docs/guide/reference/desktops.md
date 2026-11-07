# Desktop flow

In Attn, a desktop is the whole arrangement of agents, shells, seeds and any
other kind of tile that is visible to you.

In desktop flow, Attn shows you all your desktops. You decide which agent
to go to, and you structure your work by how you organize your agents across
desktops.

Think of desktops as macOS Spaces you use to organize your work.

## Turning it on

Desktop flow is the default. If you turned on the queue, run "Turn off the agent
queue" from the command palette (Cmd+Shift+K, Ctrl+Alt+K on Linux), or click the
flow toggle ("Switch to desktop flow") at the top of the sidebar. The flow you
pick applies to every profile.

## Desktops

You can have as many desktops as you want. The first 9 are one keypress away:
Cmd+1–9 (Ctrl+Shift+1–9 on Linux). Pressing a digit with no desktop behind it
creates one, and pressing the digit of the desktop you're on takes you back to
the previous one. Cmd+G (Ctrl+Shift+G on Linux) shows all your desktops at once.

You can also have as many non-numbered desktops as you want. Cmd+K (Ctrl+Shift+K
on Linux) finds any agent or tile on any desktop and takes you to it.

## Tiles

Tiles are the boxes on a desktop. Agents, shells, seeds, markdown files, the
editor and browsers all appear in a tile.

Cmd+N (Ctrl+Shift+N on Linux) opens a new agent or shell on the current desktop,
and Cmd+T (Ctrl+Shift+T on Linux) starts with a shell picked. Cmd+D (Ctrl+Shift+D
on Linux) opens a shell beside the tile you're looking at, and Cmd+Shift+D
(Ctrl+Alt+D on Linux) opens one below it.

Drag a tile to rearrange it on the desktop. Drag it over a desktop in the
sidebar to switch to that desktop and drop it there.

Cmd+Option+arrows (Ctrl+Shift+arrows on Linux) move between tiles. At the edge of
a desktop, they move to the next desktop, and on an empty desktop they move
straight on from its launcher. Cmd+[ and Cmd+] (Ctrl+Shift+[ and ] on Linux) move
back and forward through the tiles you've looked at.

When a desktop gets crowded:

- Zoom, Cmd+Shift+Z (Ctrl+Alt+Z on Linux), gives the active tile most of the
  space and dims the others.
- Focus, Cmd+Shift+Enter (Ctrl+Alt+Enter on Linux), shows only the active tile.
  Esc or the same keys bring the rest back.

## Moving agents

Cmd+Option+1–9 (Ctrl+Alt+1–9 on Linux) moves the active tile to that desktop and
takes you with it. Add Shift to send it away and stay where you are.

When an agent has delegated work, its delegates can move with it: run "Move with
delegates to…" from the command palette, or hold Option (Alt on Linux) when you
drop the agent on a desktop in the sidebar.

You can ask agents to move themselves, their delegates or any shell, and the
Chief can move any agent. A delegate opens at the end of the row on the desktop
of the agent that started it, unless you ask for another desktop ("put it on desktop 7").

## The row and the shelf

A desktop is one row of columns, left to right. A column is one tile, or a stack of tiles one above the other. The window shows as many neighbouring columns as fit at a width you can read. The other columns wait as chips on the shelf. The shelf shows only while a column is off screen.

The window's title bar is one toolbar, so tiles and the sidebar list get the height the title bar and the sidebar's header took. From left to right, beside the window buttons, it holds New, Commands, the flow switch, the sidebar toggle and notifications. The shelf comes next, starting where the tiles start. On the right are the garden, the Notebook, the Ledger, automations, asks and the profile selector. Drag the empty parts of the toolbar to move the window, and double-click them to zoom it. While the Notebook, the Ledger, the full Garden or a dialog is open, the toolbar's controls are dimmed until you close it, and you can still drag the toolbar. With the queue sidebar collapsed, the queue bar's waiting pill, automation runs and desktop chips sit in the toolbar too. When they don't all fit, the desktop chips shrink to the current desktop and a +N chip that opens the desktop overview, and automation runs leave the bar until there is room again. While a tile is focused with its agent, the toolbar stays empty, so the window can still be moved. On Linux, the toolbar sits under the native title bar.

### Keys

| Action | macOS | Linux |
|---|---|---|
| Move along the row, and slide the row at the screen's edge. Past the row's end, go to the previous or next desktop. | Cmd+Option+Left, Right | Ctrl+Shift+Left, Right |
| Carry the active column past its neighbour | Cmd+Option+Shift+Left, Right | Ctrl+Alt+Shift+Left, Right |
| Send the active tile to the shelf | Cmd+Option+Shift+Up | Ctrl+Alt+Shift+Up |
| Show fewer, wider tiles or more, narrower tiles | Cmd+Option+-, Cmd+Option+= | Ctrl+Alt+-, Ctrl+Alt+= |
| Widen or narrow the open sidebar | Cmd+Option+], Cmd+Option+[ | Ctrl+Alt+], Ctrl+Alt+[ |

The command palette offers these actions too.

On Ubuntu 24.04, GNOME's default keys move a window between workspaces with Ctrl+Alt+Shift+arrows, so carry and send never reach Attn there. To use them, run the commands from the palette, rebind them in Attn (Cmd+/ on macOS, Ctrl+Shift+/ on Linux, then **Edit shortcuts**), or change GNOME's keys.

### How tiles move

- **Opening a tile.** Cmd+D, Cmd+N and Cmd+T (Ctrl+Shift+D, N and T on Linux) open beside the active column and take focus, even when the row is full. A tile on the far side goes to the shelf. Cmd+Shift+D (Ctrl+Alt+D on Linux) opens a shell below the active tile, in the same column, and Cmd+Shift+N (Ctrl+Alt+N on Linux) does the same for a new session. When a stack has no room for another tile at 320px, the new tile opens beside instead, and a message gives the numbers.
- **What agents open.** A terminal an agent starts goes to the row's end and takes no focus. It shows if there is room. If not, its chip flashes and your screen stays as it is. `attn terminal open --down` still stacks the terminal below the agent. A document or browser tile an agent opens goes beside that agent and takes no focus. It slides into view when the active tile can stay on screen. Otherwise its chip counts as waiting, and Cmd+J (Ctrl+Shift+J on Linux) brings it first. Closing the document puts your screen back. An agent that opens a tile already off screen gets the same treatment. A browser tile on the shelf keeps its page loaded, and the agent can go on driving it without changing your screen.
- **Going to a tile.** A chip, Cmd+J (Ctrl+Shift+J on Linux), the palette, a sidebar click and queue steps bring an off-screen tile beside the active tile. Cmd+[ and Cmd+] (Ctrl+Shift+[ and ] on Linux) slide to it and leave the row's order alone. A tile on another desktop switches desktop and reorders nothing.
- **Closing a tile.** Attn goes to the next tile in the stack, or else the previous one. If the column was a single tile, Attn goes to the next column, or else the previous one. Entering a stack lands on its most recently used tile.
- **Sending to the shelf.** A stacked tile leaves its stack for the row's end. A single tile moves past the screen's far side. If nothing else is off screen, the tile stays on the shelf although it would fit. It comes back with its chip, Cmd+J (Ctrl+Shift+J on Linux) or an app restart.
- **Peeking.** Hover a chip to read the terminal's latest text. Type in the box and press Enter to reply without moving anything.
- **Motion.** Tiles move on one spring. A browser tile snaps into place and keeps its page when it returns to the screen. With **Reduce motion** on in your system settings, nothing animates.

### Widths

A tile never gets narrower than the narrowest width, 66 terminal character columns by default. Set it in **Settings → Appearance**, or with `attn settings set narrowest_tile_cols <number>`. Cmd+Option+- and Cmd+Option+= (Ctrl+Alt+- and Ctrl+Alt+= on Linux) step through 57, 66, 76, 86, 100, 120 and 150 until one tile fewer or one tile more fits, and a label lists the tile counts this window can reach with the current one highlighted. When no step fits another tile, a message gives the width it tried and what would make room: closing the sidebar, or a narrower width than 57 set here or with the command. When a dragged width is what keeps a tile off the screen, Cmd+Option+= also clears the dragged widths of the tiles that end up on screen, and the label says so. Hovering the dots in the middle of the shelf shows the same control.

Drag the divider between two columns to give them the widths you want, and the other columns share the rest. A drag past the narrowest width moves the far column to the shelf, and dragging back restores it. Esc cancels the drag. To clear the widths beside one divider, double-click it. To clear every dragged width on the desktop, run "Clear dragged widths" from the palette. A dragged width remembers the room for tiles it was dragged at. With less room, such as on a smaller display or with the sidebar wider, it gives way, so the row shows the tiles the narrowest width allows. Attn keeps the width, and it comes back when the room does. A width saved before attn remembered the room gives way only when it is wider than all the room.

The open sidebar has one width, in desktop flow and queue flow and on every profile. Drag its right edge to change it. The tiles hold still during the drag and fit once when you release, so running agents resize only once. Esc, leaving the window or collapsing the sidebar cancels the drag. Cmd+Option+] and Cmd+Option+[ (Ctrl+Alt+] and [ on Linux) step the width by 40 pixels, and so do "Widen the sidebar" and "Narrow the sidebar" in the palette. To type a width, use **Settings → Appearance** or `attn settings set sidebar_width <pixels>`.

On a Mac, Option shortcuts go by key position, so a keyboard that isn't US English keeps every one of them. On a Swedish keyboard, Option+8 types `[`, yet Cmd+Option+8 moves a tile to desktop 8. Cmd+Option+[ is the key to the right of P, Å.

The width runs from 200 to 600 pixels, and tiles always keep at least 480 pixels, so an 800-pixel window shows at most 320. A wider saved width comes back when the window grows. A label shows the width during a drag and after a step, and Attn names the limit when a step can't go further. A wider sidebar leaves less room, so fewer tiles may fit.

To go back to the default of 240 pixels, double-click the edge, run "Reset sidebar width", click **Use default** in Settings, or run `attn settings set sidebar_width ""`. The width survives a restart. Whether the sidebar is collapsed does not.

### Drags

- Drop a chip on a tile's center to swap the two columns.
- Drop a chip on a tile's edge to place the chip's column there, or to stack it on the top or bottom edge.
- Drop a chip between other chips to reorder the row. Focus stays where it is.
- Drop a tile beside a chip to bring the two together: the tile moves to that side of the chip's column, both come on screen, and the tile takes focus. On a cap, the tile goes beside the row's first or last column. A stacked tile leaves its stack and becomes a column of its own.
- Drop a chip on a desktop in the sidebar to move its whole column there and go with it.

Esc cancels any of these drags.

## Profiles

A profile keeps its own desktops, agents, Chief, crew, automations, and garden,
separate from other profiles. For example, you might have one profile for work
and one for personal projects. Nothing can move between profiles.

Each profile also has its own Notebook folder setting: set it in Settings →
Notebook Folder or with `attn settings set notebook.root <folder>`. Profiles
that leave it blank all use the default folder, `~/attn-notebook`, and see each
other's notes. Attn refuses a folder inside or around another profile's folder or the default
folder. A new crew member's home lives in its profile's Notebook folder, under
`crew/`.

Cmd+Shift+U (Ctrl+Alt+U on Linux) opens your profiles. Pick one to switch to it.
From the same list, N creates a profile and switches to it, R renames the
highlighted one, and Delete or Backspace deletes it after you confirm. You can
delete only an empty profile.

## Details

<details>
<summary>Why did my desktop disappear?</summary>

An empty desktop closes on its own 30 seconds after you leave it. Attn never closes the desktop you're on, and it keeps any desktop you've named or that a crew member or automation starts on. To keep an empty desktop around, give it a name (the pencil on its sidebar row). A numbered desktop that closed this way comes back when you press its digit, or when an agent that was last on it resumes.
</details>

<details>
<summary>What happens to my agents when I close a desktop?</summary>

Closing a desktop closes every agent and shell on it, and their sessions wait in the ledger for you to resume. Attn asks first unless the desktop is empty, and it won't close your only desktop. Cmd+W (Ctrl+Shift+W on Linux) on an empty desktop closes it too. Attn refuses to close a desktop that holds the Chief or an awake crew member, and names them so you can move them to another desktop first. [The Chief](chief.md) says how to close the Chief. A crew member's session closes only when you put that member to sleep. When you close the desktop you're on, you land on the one above it in the sidebar, or the one below when it was at the top.
</details>

<details>
<summary>Where does an agent open when I resume it?</summary>

Resuming a closed session from the ledger opens the agent on the desktop it was last on. If that desktop is gone and had a digit, Attn recreates the desktop with that digit. If it had no digit, Attn opens the agent on the desktop you're on.
</details>

<details>
<summary>Why does a desktop have no number?</summary>

A new desktop takes the lowest free digit from 1 to 9, and gets none only when all nine are taken or when Attn makes it for a crew member or automation. An unnamed desktop without a digit reads "Desktop 10", "Desktop 11" and so on, in sidebar order, and you reach it from the desktop overview (Cmd+G, Ctrl+Shift+G on Linux) or its "Go to" command in the command palette. In desktop flow, Cmd+Up and Cmd+Down (Ctrl+Shift+PageUp and Ctrl+Shift+PageDown on Linux) also move you through all your desktops in sidebar order and wrap around. A desktop's digit never changes, so to put work on a digit, move its tiles there with Cmd+Option+digit (Ctrl+Alt+digit on Linux).
</details>

<details>
<summary>Why is there a desktop named after my crew member or automation?</summary>

Each crew member and automation starts its sessions on a launch desktop, and a new crew member or automation gets a desktop of its own, named after the member or automation and without a digit. That desktop never closes on its own, even when it's empty. If you close the desktop, the next session makes it again, on its digit if it had one and otherwise as a new desktop named after the member or automation. You can pick a different desktop with the Desktop field in the member's launch settings or in the automation's form.
</details>

<details>
<summary>Why didn't pressing my desktop's digit take me back?</summary>

Pressing the digit of the desktop you're on takes you back only while you're looking at that desktop, and from Home it opens the desktop instead. Going back also needs a previous desktop that still exists, and Attn forgets the previous desktop when you switch profiles.
</details>

<details>
<summary>Where does a tile land when I move it to another desktop?</summary>

Cmd+Option+digit (Ctrl+Alt+digit on Linux), with or without Shift, puts the tile to the right of the active tile on that desktop and makes the tile active there. The shortcut creates the desktop when the digit has none. Dropping a tile on a desktop in the sidebar puts its column at the start of that desktop's row. Dropping the tile on the "New desktop" spot that appears below your desktops while you drag makes a new desktop for the tile. In the desktop overview (Cmd+G, Ctrl+Shift+G on Linux), Shift+Enter sends the active tile to the highlighted desktop and keeps you where you are. Delegates come along only with an Option drop (Alt on Linux) or the "Move with delegates to" commands.
</details>

<details>
<summary>Which agents move when I move with delegates?</summary>

Attn moves every delegate that the agent started and that is on the agent's desktop, and the delegates those delegates started. The delegates follow the agent along that desktop's row, in the order they had. Attn leaves behind delegates on other desktops, and anything those delegates started, even if it is on this desktop. Attn also leaves behind shells you opened from the agent. The "Move with delegates to" commands appear in the command palette only when the agent you're looking at has a delegate of its own on its desktop. They leave you where you are. An Option drop (Alt on Linux) on a sidebar desktop makes the same move and takes you to that desktop.
</details>

<details>
<summary>How do I tell an agent which desktop to use?</summary>

Point the agent at a desktop by digit, by name in any case, by the label an unnamed desktop shows (such as "Desktop 12"), or by id. Send an agent to a digit with no desktop and Attn makes that desktop, whether it's a new delegate or an agent that's already running. Attn refuses a name it can't find and lists your desktops instead, and when two desktops share a name it asks for the digit or id. A new delegate opens at the end of that desktop's row, and an agent you move lands to the right of its active tile. The desktop you're on stays the same.
</details>

<details>
<summary>Why did zoom or focus turn off?</summary>

Zoom stays on for its desktop while you switch desktops, and it applies to whichever tile is active there. Focus on an agent ends when you look at another agent or leave its desktop, while focus on any other tile stays when you switch desktops. Turning zoom on turns focus off, turning focus on turns zoom off, and neither survives an app restart.
</details>

<details>
<summary>Why can't I delete a profile?</summary>

Attn deletes only an empty profile, and never your last one. Empty means the profile holds no running agents or shells, no awake Chief, no crew members other than the Chief and retired members, no automations, no browser, markdown, seed or editor tiles, no open seeds (planted, growing or parked), no pending delegations and no running garden reviews. [The Chief](chief.md) says how to close the Chief. When something is left, Attn refuses and counts each kind, so you can clean up or ask an agent to do it.
</details>
