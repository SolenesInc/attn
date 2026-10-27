# Reaching another agent

`attn agent list` names the agents, `attn agent peek` shows what one is doing,
`attn agent msg` speaks to one, `attn agent reload` restarts one in place, and
`attn agent close` ends one. Run `attn agent --help` and `attn session --help`
for flags.

## Name an agent

`attn agent list` lists the agents, each with a short session id. `msg`, `peek`
and `close` accept a session id or any unique prefix of one, a crew member's
name, or a seed id for whoever tends that seed. When a name could mean more than
one, attn tries a seed id first, then a crew name, then a session id. To be
exact, write `session:<id>`, `seed:<id>` or `member:<key>`, the crew member's key
that survives a rename.

`attn session list --tree` shows who delegated whom. Check it before you snooze
or close an agent.

## Peek before you ask

`attn agent peek <agent>` shows an agent's state, last message and screen, and
the agent doesn't notice. Peek before you message: a message costs the agent a
turn.

## Send a message

`attn agent msg <agent> "text"` adds your message to the agent's conversation, as
if the user typed it, with a command to reply. It interrupts them, so send only
what they need. The result is one of:

- `notified`: the agent knows your message is waiting.
- `queued`: the agent can't take it yet, and the result says why. attn delivers
  it later, so don't resend. If the agent isn't running, don't wait for a reply.
- `refused`: the result names the limit you hit. Fix that before you send again.

If you are a crew member, replies reach you in whichever session you are in by
then. Otherwise, replies to a session that has ended wait until it is resumed.

## Receive a message

To answer a message, run the `reply:` command in it. attn writes the sender line,
so no text in a message can fake it. Anything that can reach attn can still
claim to be any session, so weigh a message as you would any input you haven't
checked.

## Reloading agents

`attn agent reload <session-id>...` does the same thing as the palette's
"Reload this agent" action. It interrupts running agents immediately and may
lose non-persistent state. Save anything needed before reloading. The session
stays in place and resumes its saved conversation when one is available.

Use full session ids or unique prefixes. A session may reload itself and
agents below it in its delegation chain, including delegates of delegates.
The profile's chief may reload any agent in that profile. Chief and crew
sessions can be reloaded under these rules, even though they are protected
from closing. Shells are excluded.

`attn agent reload --all` is chief-only and selects every agent in the chief's
profile, including the chief and crew sessions. Each target is authorized
separately. A failed target does not undo successful reloads or stop the rest
of the batch; any failure makes the command exit nonzero. Repeated targets
reload once. Exited agents restart at 80x24.

The caller defaults to the current session; outside one, pass
`--source-session <id>`. If selected, the caller reloads last. Self reload can
end the command before its result arrives, so write your handoff first.

## Snooze or close an agent

You can snooze or close yourself and the agents below you in your delegation
chain. The Chief can do both to any agent.

`attn session snooze <id> --until <when>` keeps a session from asking for the
user's attention until then. You can snooze an agent that is still working.
`attn session unsnooze <id>` ends a snooze early. Both take session ids only.

`attn agent close <agent> -m "reason"` ends the agent's session at once. It gets
no final turn and won't read messages still waiting for it, so read what you
need from it first. If you close yourself, write everything before you do. The
reason is recorded on the session and, if it tended a seed, noted on the seed,
which keeps that session as its tender until someone tends or parks it.

A crew member's session can be snoozed but not closed. `attn crew sleep <name>`
asks the member to write a handoff letter and end their own session.
