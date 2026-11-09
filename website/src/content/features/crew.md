---
title: Crew
summary: Permanent agents with a name and a charter. Each session starts from the letter the last session left.
order: 3
guide:
  - link: crew
    label: Crew, the full guide
  - link: crew#starting-a-session
    label: What starts a member's session
  - link: chief#how-the-chief-differs-from-other-members
    label: How the Chief differs from other members
---

## What it does for you

Use a crew member for work that outlives one conversation: a project you come back to every week, an area you want one agent to own, a role you want to talk to by name.

A member lives through a series of sessions. Each session starts from the member's charter and the letter the last session left, and ends when the member files its next letter. Between sessions nothing runs for the member. The harness's own memory is off for crew sessions, so a member carries over only through its charter, its letters and its seeds. The charter and letters are files in your Notebook, and you can read them.

attn starts a session for a member when you start one, and when something arrives in its mailbox: a message, an update on a seed it tends, or the answer to one of its asks.

Every profile has a Chief, a crew member named Chief. The Chief onboards you on its first day. You can rename the Chief like any member, and `chief` still names it in commands. The Chief ends its sessions with a letter like any member, but you can't close its session without one or retire it.

## How you use it

Add a member. In its first session, the member agrees a charter with you and writes the file in its own words.

```sh
attn crew create fern
```

The sidebar's Crew section lists your members.

- Click the ☀ on the row of a member between sessions once to arm it and again to start its session.
- Click the ☾ on the row of a member in session to ask it to close. The member finishes its current work, files its letter and ends its own session.
- Run "Manage crew" from the command palette (Cmd+Shift+K, Ctrl+Alt+K on Linux) to edit a member's charter, read its letters, see its seeds, and set its harness and model.

The same moves work from any terminal, for you and for your agents:

```sh
attn crew start fern
attn crew close fern
attn agent msg fern "The release moved to Friday"
```
