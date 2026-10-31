# Desktop flow

In Attn, a desktop is the whole arrangement of agents, shells, seeds and any
other kind of tile that is visible to you.

In desktop flow you are presented with all your desktops. You decide which agent
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
and Cmd+T (Ctrl+Shift+T on Linux) starts with a shell picked. To open a shell
next to the agent or shell you're looking at, split it: Cmd+D (Ctrl+Shift+D on
Linux) opens one beside it, Cmd+Shift+D (Ctrl+Alt+D on Linux) opens one below.

Drag a tile to rearrange it on the desktop. Drag it over a desktop in the
sidebar to switch to that desktop and drop it there.

Cmd+Option+arrows (Ctrl+Shift+arrows on Linux) move between tiles, and continue
to the next desktop when you reach the edge. When you close a tile, you land on
the tile you used before it. Cmd+[ and Cmd+] (Ctrl+Shift+[ and ] on Linux) walk
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
Chief can move any agent. A delegate opens next to the agent that started it,
unless you ask for another desktop ("put it on desktop 7").

## Profiles

A profile is a separate world: its own desktops, agents, Chief, crew,
automations and garden. Say one for work and one for personal projects. Nothing
can move between profiles. Profiles share one Notebook folder unless a profile
sets its own with `attn settings set notebook.profile_root <folder>`; crew homes
moved into the Notebook live in their profile's folder, under `crew/`.

Cmd+Shift+U (Ctrl+Alt+U on Linux) opens your profiles. Pick one to switch to it.
From the same list, N creates a profile and switches to it, R renames the
highlighted one, and Delete or Backspace deletes it after you confirm. You can
only delete an empty profile.

## Details

<details>
<summary>Why did my desktop disappear?</summary>

An empty desktop closes on its own 30 seconds after you leave it. Attn never closes the desktop you're on, and it keeps any desktop you've named or that a crew member or automation starts on. To keep an empty desktop around, give it a name (the pencil on its sidebar row). A numbered desktop that closed this way comes back when you press its digit, or when an agent that was last on it resumes.
</details>

<details>
<summary>What happens to my agents when I close a desktop?</summary>

Closing a desktop closes every agent and shell on it, and their sessions wait in the ledger for you to resume. Attn asks first unless the desktop is empty, and it won't close your only desktop. Cmd+W (Ctrl+Shift+W on Linux) on an empty desktop closes it too. Attn refuses to close a desktop that holds the Chief or an awake crew member, and names them so you can move them to another desktop first. The Chief's session closes only after you run "Remove chief role" from its actions menu in the sidebar, and a crew member's only when you put that member to sleep. When you close the desktop you're on, you land on the one above it in the sidebar, or the one below when it was at the top.
</details>

<details>
<summary>Where does an agent open when I resume it?</summary>

Resuming a closed session from the ledger puts it back on the desktop it was last on. When that desktop is gone, Attn brings it back if it had a digit, and otherwise opens the agent on the desktop you're on.
</details>

<details>
<summary>Why does a desktop have no number?</summary>

A new desktop takes the lowest free digit from 1 to 9, and gets none only when all nine are taken or when Attn makes it for a crew member or automation. An unnamed desktop without a digit reads "Desktop 10", "Desktop 11" and so on, in sidebar order, and you reach it from the desktop overview (Cmd+G, Ctrl+Shift+G on Linux) or its "Go to" command in the command palette. In desktop flow, Cmd+Up and Cmd+Down (Ctrl+Shift+PageUp and Ctrl+Shift+PageDown on Linux) also step through all your desktops in sidebar order and wrap around. A desktop's digit never changes, so to put work on a digit, move its tiles there with Cmd+Option+digit (Ctrl+Alt+digit on Linux).
</details>

<details>
<summary>Why is there a desktop named after my crew member or automation?</summary>

Each crew member and automation starts its sessions on a launch desktop, and a new one gets a desktop of its own, named after it and without a digit. That desktop never closes on its own, even when it's empty. If you close it, the next session makes it again, on its digit if it had one and otherwise as a new desktop named after the member or automation. You can pick a different desktop with the Desktop field in the member's launch settings or in the automation's form.
</details>

<details>
<summary>Why didn't pressing my desktop's digit take me back?</summary>

Pressing the digit of the desktop you're on takes you back only while you're looking at that desktop, and from Home it opens the desktop instead. It also needs a previous desktop that still exists, and Attn forgets the previous desktop when you switch profiles.
</details>

<details>
<summary>Where does a tile land when I move it to another desktop?</summary>

Cmd+Option+digit (Ctrl+Alt+digit on Linux), with or without Shift, puts the tile to the right of the active tile on that desktop and makes it the active tile there, and it creates the desktop when the digit has none. Dropping a tile on a desktop in the sidebar docks it along that desktop's left side at about a third of its width, and dropping it on the "New desktop" spot that appears below your desktops while you drag makes a new desktop for it. In the desktop overview (Cmd+G, Ctrl+Shift+G on Linux), Shift+Enter sends the active tile to the highlighted desktop and keeps you where you are. Delegates come along only with an Option drop (Alt on Linux) or the "Move with delegates to" commands.
</details>

<details>
<summary>Which agents move when I move with delegates?</summary>

The agent takes along every delegate it started that sits on the same desktop, plus the delegates those started, and each lands beside the agent that started it. Delegates on other desktops stay put along with anything they started (even on this desktop), and so do shells you split off the agent. The "Move with delegates to" commands appear in the command palette only when the agent you're looking at has a delegate of its own on its desktop, and they leave you where you are. An Option drop (Alt on Linux) on a sidebar desktop does the same move and takes you there.
</details>

<details>
<summary>How do I tell an agent which desktop to use?</summary>

Point the agent at a desktop by its digit, its name in any case, the label an unnamed one shows (like "Desktop 12") or its id. Send an agent to a digit with no desktop and Attn makes that desktop, whether it's a new delegate or an agent that's already running. Attn refuses a name it can't find and lists your desktops instead, and when two desktops share a name it asks for the digit or id. On another desktop the tile lands beside that desktop's active tile, and the desktop you're on stays the same.
</details>

<details>
<summary>Why did zoom or focus turn off?</summary>

Zoom stays on for its desktop while you switch desktops, and it follows whichever tile is active there. Focus on an agent ends when you look at another agent or leave its desktop, while focus on any other tile stays when you switch desktops. Turning one on turns the other off, and neither survives an app restart.
</details>

<details>
<summary>Why can't I delete a profile?</summary>

Attn deletes only an empty profile, and never your last one. Empty means it holds no running agents or shells, its Chief included, no crew members, no automations, no browser, markdown, seed or editor tiles, no open seeds (planted, growing or parked), no pending delegations and no running garden reviews. To close the Chief, first run "Remove chief role" from its actions menu in the sidebar. When something's left, the refusal counts each kind, so you can clean up or ask an agent to do it.
</details>
