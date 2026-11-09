# Using the Claude mod

Attn delivers messages to your Claude Code sessions through a Claude Code mod
instead of typing into the terminal. A mod is a plugin that runs inside Claude. Rings, annotations and your messages
from elsewhere no longer race what you are typing, and Attn no longer waits for
a quiet moment in the terminal.

Claude reads the same text either way: the bare message when it is idle, and
Claude's own "The user sent a new message while you were working" wording when
it is in the middle of a turn. Attn was checked against Claude Code 2.1.318.

## What you see

- **Claude is idle.** Your message starts a turn. Claude draws a "Prompt from the
  attn plugin" line above it, which no mod can remove.
- **Claude is working.** The message appears in a band above the prompt, with
  "attn adds this after the current tool call". Once Claude has it, the band
  clears and the transcript shows `attn: ❯ <message>`. If the turn ends with no
  further tool call, the message starts the next turn instead.
- **You press Esc while Claude works.** Attn stops delivering its own messages, so a session you
  stopped stays stopped. The band lists them under "Waiting in attn — sent with
  your next prompt", and they go out after your next prompt. Your own words, such
  as annotations and phone prompts, are typed into the terminal, which counts as
  your next prompt. If the mod was holding your words when you pressed Esc, Attn
  types and submits them, along with anything already in the prompt.
- **Claude asks permission during a turn.** The mod takes the message. Claude
  draws no band while the question replaces the prompt, so nothing shows in the
  terminal until you answer. If you approve, the message follows the rules for a
  working Claude. If you refuse, with "No" or with Esc, the turn ends but the
  session is not stopped, the same as in Claude itself: the message starts a new
  turn, and so does any message that arrives later.
- **Claude shows a question outside a turn, or the mod is not connected.** Nothing
  is delivered until you answer it. A ring waits, and an annotation stays unsent
  so you can send it again.

The band shows the first line of each message. A ring you read elsewhere stays
listed in the band until your next prompt.

## When Attn still types

Typing stays the fallback. Attn types into the terminal whenever the mod is not
connected, for example while Claude is starting, while Claude waits for you to
answer its trust question in a folder it has not trusted yet, or if the mod
failed to load. A session whose mod has not connected 30 seconds after it
launched gets a warning in the daemon log that names its Claude Code version,
including when you take longer than that to answer the trust question. There is
no setting that turns the mod off: if a Claude Code update breaks it, Attn says
which version in that warning and needs a fix for it.

A session that was running when you upgraded to a version of Attn with the mod
always on keeps being typed into until you reload it.

Attn never types the mail for a session it resumes because its seed is on
standby. Attn waits for the mod's first connection and delivers through it, as
[Seeds on standby](delegation.md#seeds-on-standby) describes.

## Compacting before a close

An agent can compact its conversation through the mod just before it closes
itself, as [Seeds on standby](delegation.md#seeds-on-standby) describes. Claude
compacts in its own way, as `/compact` does. A Claude session without the mod
can't. Typing into the session while it compacts does not stop the close.

## Sessions on an outpost

A Claude session on an outpost takes messages and compacts through its mod, the
same as a session on your home machine. If the mod gives a message back while the home is
offline or unreachable, the outpost keeps it and hands it to the home when the
link returns.

## Upgrades and restarts

Attn keeps the mod in a `claude-mod` folder in its data directory. When an Attn
upgrade changes the mod, the daemon writes the new version as it starts, every
running session reloads the mod, and the transcript shows `attn: reloaded`. Attn
types into a session until its mod has reloaded.

A daemon restart loses nothing while Claude keeps running, on your home machine or
on an outpost: the mod keeps a message that waits for the next tool call, and hands
it back if you press Esc. If the restart comes with an upgrade that changes the
mod, the reload drops that message, and you need to send it again.
