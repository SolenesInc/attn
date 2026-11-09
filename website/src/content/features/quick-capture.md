---
title: Quick Capture
summary: Send a message or files to the Chief or a crew member from any app, then go back to what you were doing.
order: 2
guide:
  - link: quick-capture
    label: Quick Capture, the full guide
  - link: crew#starting-a-session
    label: Starting a member's session, which a quick capture does
  - link: sessions#send-files-to-an-agent-from-outside-attn
    label: Send files to an agent from outside Attn
---

## What it does for you

Quick Capture opens over the app you are using, with a global shortcut. You write the message, attach files if you have them, and press Enter. The window hides, and you are back where you were.

The message goes to the Chief unless you pick another crew member. Attn types it into the member's conversation as your message, and attn starts a session for a member between sessions.

## How you use it

Quick Capture runs on macOS. Ctrl+Option+Cmd+Space opens it from any app.

- Enter sends the message, and Shift+Enter starts a new line.
- Paste or drop files to attach them. A pasted screenshot works too.
- Cmd+K chooses who receives the message, and Cmd+1 to Cmd+4 pick from the first four names.
- Esc closes the window and keeps your draft.
- **Recent** lists the messages you sent.

To change the shortcut or turn it off, run "Customize keyboard shortcuts" from the command palette (Cmd+Shift+K).

To send to any running agent, from a script or another program, pair that program with `attn send pair`. The agent receives the files and the message as a message from you.
