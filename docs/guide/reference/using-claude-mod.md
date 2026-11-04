# Using the Claude mod

The Claude mod is experimental. With it on, Attn delivers messages to your Claude
Code sessions through a Claude Code mod instead of typing into the terminal. A
mod is a plugin that runs inside Claude. Rings, annotations and your messages
from elsewhere no longer race what you are typing, and Attn no longer waits for
a quiet moment in the terminal.

Claude reads the same text either way: the bare message when it is idle, and
Claude's own "The user sent a new message while you were working" wording when
it is in the middle of a turn. Attn was checked against Claude Code 2.1.318.

## Turn it on or off

**Claude mod** in **Settings → Experimental** is off by default. From a terminal,
run `attn settings set claude_mod_enabled true`, or `false` to turn it off.

The setting decides how new Claude sessions launch, including the ones
automations and delegations start. A session that started with the setting off
stays typed until you reload it. A session that started with the setting on goes
back to typing as soon as you turn the setting off.

## What you see

- **Claude is idle.** Your message starts a turn. Claude draws a "Prompt from the
  attn plugin" line above it, which no mod can remove.
- **Claude is working.** The message appears in a band above the prompt, with
  "attn adds this after the current tool call". Once Claude has it, the band
  clears and the transcript shows `attn: ❯ <message>`. If the turn ends with no
  further tool call, the message starts the next turn instead.
- **You press Esc.** Attn stops delivering its own messages, so a session you
  stopped stays stopped. The band lists them under "Waiting in attn — sent with
  your next prompt", and they go out after your next prompt. Your own words, such
  as annotations and phone prompts, are typed into the terminal, which counts as
  your next prompt. If the mod was holding your words when you pressed Esc and
  the prompt has an unsent draft, Attn leaves the draft alone, and your words go
  out after you send your next prompt.
- **Claude asks permission during a turn.** The mod takes the message. Claude
  draws no band while the question replaces the prompt, so nothing shows in the
  terminal until you answer. If you approve, the message follows the rules for a
  working Claude. If you refuse, the session counts as stopped, so the message
  follows the rules for Esc.
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
launched gets a warning in the daemon log, including when you take longer than
that to answer the trust question. Sessions on an outpost are always typed.

## Upgrades and restarts

Attn keeps the mod in a `claude-mod` folder in its data directory. When an Attn
upgrade changes the mod, the daemon writes the new version as it starts, every
running session reloads the mod, and the transcript shows `attn: reloaded`. Attn
types into a session until its mod has reloaded.

If the Attn daemon restarts while the band shows a message waiting for the next
tool call, Attn no longer knows about that message. After an upgrade that changes
the mod, the message is gone and you need to send it again.
