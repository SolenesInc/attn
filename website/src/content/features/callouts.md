---
title: Callouts
summary: An agent points at the part of the app it means, and you step through what it says.
order: 6
guide:
  - link: callouts
    label: Callouts, the full guide
  - link: chief#the-chiefs-first-day
    label: The Chief's first day, a tour of the app with callouts
---

## What it does for you

A callout is short text from an agent, shown beside a named part of the app window. Ask an agent where something is, and it can point at the thing instead of describing the screen. The Chief uses callouts to tour the app on its first day. An agent can also point you at another agent you should go to.

## How you use it

Turn on **Agent callouts** in Settings first.

You step through a callout with the keyboard:

- Enter or → moves to the next step.
- On a last step that names an agent or a desktop, Enter or → goes there and ends the callout.
- Esc ends the callout. So does a click outside it, another overlay, or a profile switch.

An agent lists what it can point at, then points:

```sh
attn callout anchors
attn callout show sidebar "Your agents, the ones waiting on you first" \
  garden "Work you and your agents track"
attn callout show agent:a1b2 "Needs your review on the migration plan"
```
