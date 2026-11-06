# Quick Capture

Quick Capture sends a message and files to the Chief or a crew member from any
app. A small panel opens over the app you are using. You write and press Enter.
The panel hides, and you are back where you were.

Quick Capture runs on macOS and is experimental.

## Turn it on

**Quick Capture** in **Settings → Experimental** is off by default. While the
setting is off, the shortcut does nothing and the **File → Quick Capture** menu
item is disabled.

Chief and crew sessions that start after you turn the setting on also get a
short instruction: treat each capture like a note given to an assistant. A
session that was already running still receives your messages, and picks up the
instruction the next time it launches.

## Open the panel

Ctrl+Option+Cmd+Space opens the panel from any app. The other app stays in front
until you click the panel. **File → Quick Capture** in Attn's menu bar opens the
same panel.

To change the shortcut or turn it off, run "Customize keyboard shortcuts" from
the command palette (Cmd+Shift+K) and find **Quick Capture · system-wide**.
macOS refuses a shortcut the system already uses, and the previous shortcut
stays.

The panel opens centered on the screen that has the pointer. Drag its surface to
move it.

## Write and send

| Key | What it does |
|---|---|
| Enter | Sends the message and hides the panel |
| Shift+Enter | Starts a new line |
| Cmd+K | Opens the list of who can receive the message |
| Cmd+1 to Cmd+4 | Picks one of the first four names on that list |
| Esc | Closes the list or **Recent**, otherwise hides the panel and keeps your draft |

The message goes to the Chief unless you pick someone else. After each send the
panel goes back to the Chief. The list holds the Chief and the crew members of
your profile.

To attach files, paste them or drop them on the panel. A pasted screenshot works
too. Each file shows in the panel, with a button to remove it.

Your draft stays through hiding the panel and restarting the app. To throw a
draft away, open **Recent** and click **Discard draft**.

## What the agent receives

Attn types your message into the agent's conversation as your own message. The
message says you sent it through Quick Capture and that you are not looking at
the session right now.

A sleeping crew member wakes for the message. If you have no Chief yet, a message
to the Chief waits for the next Chief session. Attn holds a message back for 30
seconds after your last keystroke in that agent's terminal, so it never lands on
top of what you are typing.

For each attached file, the message includes an `attn agent attachment` command
that saves the file where the agent runs.

The run that a capture starts is yours, so the agent goes into your
[queue](queue.md) when it stops, and you see its reply.

## See what you sent

**Recent** at the top of the panel lists your messages, newest last. Use ↑ and ↓
to move through them, and Esc to go back to your draft. **Show older messages**
loads earlier ones.

Each message shows one of two marks:

- **Sent**: Attn stored the message, and the agent has not retrieved it yet.
- **Read**: the agent retrieved the message from its inbox. The mark does not say
  whether the agent understood or acted on it.

"Saved for" and a name at the bottom of the panel means the same as **Sent**.

## Send from a program or to any agent

The panel reaches only the Chief and crew. To send to any running agent, or from
a script, see [Send files to an agent from outside
Attn](sessions.md#send-files-to-an-agent-from-outside-attn). That path does not
need the Quick Capture setting.

## Details

<details>
<summary>The panel says "Submission unconfirmed". Did my message go?</summary>

Attn lost the answer while saving, so it does not know yet. It checks again by itself when the panel opens and when the connection returns. If Attn saved the message, the draft clears. If not, the panel says "Not yet saved" and you can edit or send the draft again. **Retry** does the same check and sends if the message was not saved, so it cannot send the message twice. Attn refuses to discard a draft while it is in this state.
</details>

<details>
<summary>Which profile does a capture go to?</summary>

The profile the app shows. The list of names, the draft and **Recent** all belong to that profile, and they change when you switch profiles.
</details>
