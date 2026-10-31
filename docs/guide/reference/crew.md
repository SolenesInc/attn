# Crew

Crew members are permanent agents with a name and a charter. A member lives
through a series of sessions: each one starts from the member's charter and
the letter the last session left, and ends when the member files its next
letter. Between sessions the member is asleep. While a session runs, the member
is awake.

Use a crew member for work that outlives one conversation: a project you come
back to every week, an area you want one agent to own, a role you want to talk
to by name.

## Add a member

`attn crew create <name>` adds a member to your profile. A name is letters,
digits and `-`, starting with a letter, at most 40 characters; `chief`, `attn`,
`user` and `you` are taken. The member's home is `crew/<name>/` in your
profile's Notebook, or `crew/<name>-2/` when that folder is taken, so you can
read and edit its charter and letters there. It starts with an empty
`CHARTER.md`: the member's first session agrees a charter with you, then writes
the file in its own words.

A home folder you make yourself also works: `~/.attn/crew/<key>/CHARTER.md`
(for a named instance, `~/.attn-<instance>/crew/<key>/`), where the folder name
is lowercase letters, digits and `-`. The daemon adds it to the roster at its
next start (`attn daemon stop`, then `attn daemon ensure`). Its name starts as
the key with a capital letter, so `fern` is Fern, and it joins the profile you
used most recently.

A member wakes only in its profile. `attn crew list` shows the members of your
profile, with their state, harness, model, launch desktop and home folder.

A member's name is unique in its profile, ignoring case. Commands find a
member by name in your profile: inside Attn, the profile of the session you
run them from; outside, pass `--profile <name|id>` when you have several.
`member:<key>`, such as `member:fern`, reaches the member by its key, which
never changes.

## Move a member's home

A member whose home is in Attn's data folder, because you made the folder
yourself or the member was added before homes moved to the Notebook, keeps it
there until you run `attn crew move-home <member>`. That moves the home into its
profile's Notebook, at `crew/<name>/` like a new member's. Its charter, letters
and anything else in the folder move with it, and a working or awareness folder
inside the home follows. So do its past sessions: [resume](sessions.md#resume)
one from the ledger or with `attn session reopen` and it opens in the new home.
The member must be asleep. Changing the profile's Notebook folder later leaves
every home where it is.

If a member's home goes missing, Attn refuses to wake the member or open its
charter and names the path. Other members keep working. Put the folder back, or,
if you moved or renamed it on purpose, run
`attn crew set <member> --home <folder>`, without other settings. The folder
must hold the member's `CHARTER.md`, and the member must be asleep. Attn renames the folder after the
member if its name differs. As with `move-home`, a working or awareness folder
inside the old home follows, and so do its past sessions.

To undo `attn crew move-home`, move the folder to `~/.attn/crew/<key>/` (for a
named instance, `~/.attn-<instance>/crew/<key>/`) and run
`attn crew set <member> --home` on it. The folder keeps that name.

## The Crew section

The sidebar has a Crew section. In [queue flow](queue.md) it lists every
member that is not retired, asleep or awake. In desktop flow it lists the
members who are asleep and not retired; awake members are tiles on their
desktops.

- A sleeping member's row shows a sun. Click it once to arm it and again to
  wake the member.
- An awake member's row has a ☾ that asks the member to sleep.
- ••• on a row opens **Member details**. **manage** in the section header, or
  "Manage crew" in the command palette (Cmd+Shift+K, Ctrl+Alt+K on Linux),
  opens the same window.

**Manage crew** has four tabs for each member:

- **Launch settings**. The launch desktop and the harness, model and effort
  the next session starts with, the Wake or Sleep button, and Apply while the
  member is awake. Changes save as you make them.
- **Charter**. Edits `CHARTER.md` and saves as you type. If the file changes
  on disk while you edit, you pick "Use file version" or "Keep my edit".
- **Handoffs**. The member's letters, read only.
- **Seeds**. The seeds the member tends or planted.

## Waking a member

These wake a sleeping member:

- You: the sun in the sidebar, Wake in Manage crew, or
  `attn crew wake <member>`. `--agent <name>` picks another harness for this
  one session.
- An agent running `attn crew wake <member>`.
- An item for the member's mailbox: a message (`attn agent msg <member>`), an
  update on a seed it tends, or the answer to one of its asks.

The session starts on the member's launch desktop, in the member's home
folder unless you set another one. Attn primes it with the charter, the
freshest letter, the names of older letters and the seeds the member tends.
The member then greets you: who it is, where things stand, and what it would
do next. Waking an awake member does nothing and names its session.

## Putting a member to sleep

The ☾ in the sidebar, Sleep in Manage crew, and `attn crew sleep <member>`
all ask the member to finish what it needs to settle, write its letter and
file it. The member ends its own session; nothing stops it mid-sentence.

A member files its letter with `attn handoff -m "<letter>"` (or `-m -` to
read it from stdin). Attn saves it as a new file in `handoffs/` in the
member's home and never edits it; a correction is a new letter. Filing ends
the session and the member sleeps until something wakes it. The one exception
is a letter asked for by a harness change you applied, which starts the next
session on the new harness (see below).

If the session cannot end or the next one cannot start, the letter stays filed
and the session keeps running. `attn handoff --retry` tries again with that
letter. To give up on a harness switch, ask the member to sleep first; the
retry then ends the session with the member asleep.

## Configuring a member

`attn crew set <member>` saves launch settings for the member's next session.
It never touches the member's markdown files.

| Flag | What it sets |
|---|---|
| `--launch-desktop <own\|digit\|name\|id>` | Where sessions start. `own` or an empty desktop digit creates a new desktop; `--desktop-name` names it. |
| `--cwd <dir>` | The working folder. Without it, the member works in its home. |
| `--agent <name>` | `claude`, `codex` or an installed plugin harness. `""` goes back to the crew default, Claude Code. |
| `--model <name>`, `--effort <level>` | The model and effort. `""` goes back to the harness default. |
| `--awareness-dir <dir>` | A folder the charter is about, which the member can reach. Repeat to set several; `""` clears them. |
| `--home <dir>` | Where the member's home is now, after you moved or renamed its folder. See [Move a member's home](#move-a-members-home). |

By default a member starts on its own desktop, named after it, which Attn
creates the first time the member wakes.

## Applying changed settings

Saved settings reach an awake member when you apply them: **Apply** in Manage
crew's Launch settings, or `attn crew apply <member>`. The member itself, an
agent above it in the delegation chain, and the Chief can run it too.

- On the same harness, such as a new model or effort or an edited charter, the
  session reloads in place and keeps its conversation. Attn then asks the
  member to read its charter again.
- On a different harness, Attn asks the member to file its letter. Filing ends
  the session and starts the next one on the new harness from the letter. The
  new session picks up any exchange that was in progress without asking you to
  repeat yourself.

An asleep member needs nothing: its next wake uses the saved settings. Any
reload of a member's session, such as "Reload this agent" or
`attn agent reload`, also uses its saved model and effort while the session
runs on the saved harness; a changed harness needs Apply. Manage crew shows a
harness switch that waits for the member's letter. Saving the running harness
back, or asking the member to sleep, cancels it.

## Details

<details>
<summary>Do crew members wait in my queue?</summary>

Not by default. Members keep their rows in the Crew section and stay out of the queue's waiting list. Run "Put the crew in the queue" from the command palette to make a member that owes you a turn wait in the queue too; its row stays in the Crew section. "Take the crew out of the queue" turns it off. The setting applies to every profile. In queue flow, the Chief, if you have one, has its own row at the top of the Crew section. The Chief never waits in the queue; with Crew v2 on it is a crew member (see [the Chief](chief.md)).
</details>

<details>
<summary>Why did a member wake up on its own?</summary>

Something wrote to its mailbox: another agent messaged it, a seed it tends changed, or an ask it sent got an answer. Attn wakes the member to read it. These wakes are capped at 8 per member every 12 hours (`crew.wake_limit` and `crew.wake_limit_window_seconds`, set with `attn settings set`); `0` turns them off. Your own wakes and wakes for your messages never count. When the cap refuses a wake, the sender's receipt names the limit, and you can still wake the member yourself.
</details>

<details>
<summary>What is the heartbeat I see in a member's terminal?</summary>

A member's harness caches its conversation for a while; Attn assumes an hour for Claude Code and 30 minutes for Codex. When you are around, the member is between turns, and the cache is 5 minutes from lapsing, Attn sends a heartbeat. The member repeats its last message, which keeps the cache warm for your next prompt. If you have been away from Attn for 2.5 hours instead, Attn asks the member to file its letter and sleep. `crew.heartbeat_enabled`, `crew.autosleep_enabled`, `crew.away_seconds`, `crew.heartbeat_lead_seconds` and `crew.cache_ttl_seconds` (or `crew.cache_ttl_seconds.<harness>`) control this.
</details>

<details>
<summary>What happens when I clear a member's conversation?</summary>

The member stays awake. `/clear` in Claude Code, or `/clear` and `/new` in
Codex, starts a new conversation in the same terminal, and that conversation
is the member's: it keeps the member's name, mail and the seeds it tends, and
waking the member returns it. The new conversation starts empty, so the member
runs `attn crew prime` to read its charter, freshest letter and seeds again.
When the old conversation still shows in another terminal, the member stays
with it, and the new conversation is an ordinary session.
</details>

<details>
<summary>What happens if I close a member's agent without a letter?</summary>

The member is asleep. Its next session starts from the last letter it filed, so anything the closed session did after that letter is only in the work itself and in its seeds.
</details>

<details>
<summary>Can I rename or retire a member?</summary>

With Crew v2 on, rename a member with `attn crew rename <member> <name>` or
**Rename** next to its name in Manage crew. Its key, letters, mail and seeds
stay, and an awake member's agent takes the new name. A home in the Notebook
moves to a folder named after the new name, taking the member's past sessions
along as a move does: at once when the member is asleep, and at its next wake
when it is awake, since its agent works from that folder. A home in Attn's data
folder stays put.

`attn crew retire <member>` stops a member's work. The seeds they tend go back
to planted, each with a note; their seed and pull request watches and open
asks are removed; new mail to them is refused. An awake member is asked to
file their letter and sleep, and that letter cannot start a next session; if
you clear its conversation first, it is asked again.
A retired member leaves the Crew section and `attn crew list` (`--all` shows
them), does not wake, and does not stop you deleting their profile. Their home,
letters, unread mail and name stay. `attn crew restore <member>` brings them
back, and the mail they kept can wake them.
</details>

<details>
<summary>How long can a letter be?</summary>

64,000 bytes. Attn refuses a longer one and says so.
</details>

## Crew v2 (experimental)

Turn on **Settings → Experimental → Crew v2** (`crew_v2_enabled`) to try the
next version of the crew. It is off by default and applies from each member's
next session. With it on:

- Members get shorter guidance that sends them to the work itself, their
  letter and the seeds it names before they act.
- The harness's own memory is off for crew sessions (Claude Code's auto memory,
  Codex memories), so a member carries over only through its charter, letters
  and seeds.
- Each letter records the session that wrote it, so Attn can keep that
  session's transcript for the member to read later.
- You can rename members.
- Every profile has a Chief, a crew member named Chief, which onboards you on
  its first day. [The Chief](chief.md) covers it.
