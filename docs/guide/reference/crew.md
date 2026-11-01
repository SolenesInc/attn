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
digits and `-`, starting with a letter, at most 40 characters. The names
`chief`, `attn`, `user` and `you` are taken. The member's home is `crew/<name>/` in your
profile's Notebook, or `crew/<name>-2/` when that folder is taken, so you can
read and edit the member's charter and letters there. The home starts with an
empty `CHARTER.md`. In its first session, the member agrees a charter with you, then
writes the file in the member's own words.

A home folder you make yourself also works. The folder is `~/.attn/crew/<key>/`
(for a named instance, `~/.attn-<instance>/crew/<key>/`), it holds a
`CHARTER.md`, and its name is lowercase letters, digits and `-`. After
`attn daemon stop` and `attn daemon ensure`, the daemon adds the member to the
roster.

The member's name starts as the key with a capital letter, so `fern` is Fern.
The member joins the profile you used most recently.

A member wakes only in its profile. `attn crew list` shows the members of your
profile, with their state, harness, model, launch desktop and home folder.

A member's name is unique in its profile, ignoring case. Commands find a
member by name in your profile. Inside Attn, your profile is the profile of the
session you run the command from. Outside Attn, pass `--profile <name|id>` when
you have several.
`member:<key>`, such as `member:fern`, reaches the member by its key, which
never changes.

## Move a member's home

A home stays where it is until you move it. Changing the profile's Notebook
folder leaves every home in the old Notebook folder. A home in Attn's data
folder also stays there, either because you made the folder yourself or because
the member was added before homes moved to the Notebook.

To move a home, the member must be asleep, and the two folders must be on one
volume. `attn crew move-home <member>` then moves a home into the profile's
current Notebook, at `crew/<name>/` like a new member's, from wherever it is:
Attn's data folder, an earlier Notebook or any other folder. The home's charter,
letters and anything else in the folder move with it. Attn also moves a working
or awareness folder inside the home, and updates the member's past sessions to
the new home. If you [resume](sessions.md#resume) a past session from the ledger
or with `attn session reopen`, the session opens in the new home.

If a member's home goes missing, Attn refuses to wake the member or open the
member's charter and names the path. Other members keep working. Put the folder back, or,
if you moved or renamed it on purpose, run
`attn crew set <member> --home <folder>`, without other settings. The folder
must hold the member's `CHARTER.md`, and the member must be asleep. Attn renames
the folder after the member if the folder's name differs. As with `move-home`, Attn
also moves a working or awareness folder inside the old home, and updates the
member's past sessions.

To undo `attn crew move-home`, move the folder to `~/.attn/crew/<key>/` (for a
named instance, `~/.attn-<instance>/crew/<key>/`) and run
`attn crew set <member> --home` on that folder. The folder keeps that name.

## The Crew section

The sidebar has a Crew section. In [queue flow](queue.md) it lists every
member who is not retired, whether the member is asleep or awake. In desktop
flow it lists the members who are asleep and not retired. Awake members are
tiles on their desktops.

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
folder unless you set another one. Attn primes the session with the charter, the
latest letter, the names of older letters and the seeds the member tends.
The member then greets you: who it is, where things stand, and what it would
do next. Waking an awake member does nothing and names its session.

## Putting a member to sleep

The ☾ in the sidebar, Sleep in Manage crew, and `attn crew sleep <member>`
all ask the member to finish its current work, write its letter and
file it. The member ends its own session, and nothing stops the member mid-sentence.

A member files its letter with `attn handoff -m "<letter>"` (or `-m -` to
read it from stdin). Attn saves the letter as a new file in `handoffs/` in the
member's home and never edits the file. A correction is a new letter. When the
member files a letter, the session ends and the member sleeps until something
wakes the member. The exception is the letter Attn asks for when you
apply a harness change. Filing that letter ends the session and starts the next
session on the new harness. See [Applying changed settings](#applying-changed-settings).

If the session cannot end or the next one cannot start, the letter stays filed
and the session keeps running. `attn handoff --retry` tries again with that letter. To give up on a harness switch instead, ask the member to sleep first. The retry then ends the session and leaves the member asleep.

## Configuring a member

`attn crew set <member>` saves launch settings for the member's next session.
It never touches the member's markdown files.

| Flag | What it sets |
|---|---|
| `--launch-desktop <own\|digit\|name\|id>` | Where sessions start. `own` or an empty desktop digit creates a new desktop, and `--desktop-name` names the desktop. |
| `--cwd <dir>` | The working folder. Without it, the member works in its home. |
| `--agent <name>` | `claude`, `codex` or an installed plugin harness. `""` goes back to the crew default, Claude Code. |
| `--model <name>`, `--effort <level>` | The model and effort. `""` goes back to the harness default. |
| `--awareness-dir <dir>` | A folder the charter is about, which the member can reach. Repeat to set several. `""` clears them. |
| `--home <dir>` | Where the member's home is now, after you moved or renamed its folder. See [Move a member's home](#move-a-members-home). |

By default a member starts on its own desktop, named after the member. Attn
creates that desktop when you add the member.

## Applying changed settings

Saved settings reach an awake member when you apply them: **Apply** in Manage
crew's Launch settings, or `attn crew apply <member>`. The member itself, an
agent above the member in the delegation chain, and the Chief can also run
`attn crew apply`.

- If the harness stays the same, for example when you change the model or the
  effort or edit the charter, the session reloads in place and keeps its
  conversation. Attn then asks the
  member to read its charter again.
- On a different harness, Attn asks the member to file its letter. When the
  member files the letter, the session ends and the next session starts on the
  new harness from the letter. The new session picks up any exchange that was in progress without asking you to
  repeat yourself.

A sleeping member needs nothing, because the member's next wake uses the saved
settings. Any reload of a member's session, such as "Reload this agent" or
`attn agent reload`, also uses the member's saved model and effort while the
session runs on the saved harness. A changed harness needs Apply. Manage crew
shows a harness switch that waits for the member's letter. If you save the
running harness again, or ask the member to sleep, Attn cancels the switch.

## Details

<details>
<summary>Do crew members wait in my queue?</summary>

Not by default. Members keep their rows in the Crew section and stay out of the queue's waiting list. Run "Put the crew in the queue" from the command palette to make a member that is owed a turn wait in the queue too. The member's row stays in the Crew section. "Take the crew out of the queue" turns the setting off. The setting applies to every profile. In queue flow, the Chief, if you have one, has its own row at the top of the Crew section. The Chief never waits in the queue. With Crew v2 on, the Chief is a crew member (see [the Chief](chief.md)).
</details>

<details>
<summary>Why did a member wake up on its own?</summary>

An item arrived in the member's mailbox: another agent messaged the member, a seed the member tends changed, or an ask the member sent got an answer. Attn wakes the member to read the new item. These wakes are capped at 8 per member every 12 hours (`crew.wake_limit` and `crew.wake_limit_window_seconds`, set with `attn settings set`), and `0` turns them off. Your own wakes and wakes for your messages never count. When Attn refuses a wake because of the cap, the reply to the sender names the limit, and you can still wake the member yourself.
</details>

<details>
<summary>What is the heartbeat I see in a member's terminal?</summary>

A member's harness caches its conversation for a while. Attn assumes an hour for Claude Code and 30 minutes for Codex. When you are around, the member is between turns, and the cache will lapse in 5 minutes, Attn sends a heartbeat: the member repeats its last message. The repeat keeps the cache from lapsing before your next prompt. If you have been away from Attn for 2.5 hours, Attn instead asks the member to file its letter and sleep. `crew.heartbeat_enabled`, `crew.autosleep_enabled`, `crew.away_seconds`, `crew.heartbeat_lead_seconds` and `crew.cache_ttl_seconds` (or `crew.cache_ttl_seconds.<harness>`) control the heartbeat and the automatic sleep.
</details>

<details>
<summary>What happens when I clear a member's conversation?</summary>

The member stays awake. `/clear` in Claude Code, or `/clear` and `/new` in
Codex, starts a new conversation in the same terminal, and that conversation
is the member's. The new conversation keeps the member's name, mail and the
seeds the member tends, and waking the member returns to that conversation. The
new conversation starts empty, so the member runs `attn crew prime` to read the
charter, latest letter and seeds again.
If the old conversation is still open in another terminal, the member stays in
the old conversation, and the new conversation is an ordinary session.
</details>

<details>
<summary>What happens if I close a member's agent without a letter?</summary>

The member is asleep. Its next session starts from the last letter it filed, so anything the closed session did after that letter is only in the work itself and in its seeds.
</details>

<details>
<summary>Can I rename or retire a member?</summary>

With Crew v2 on, rename a member with `attn crew rename <member> <name>` or
**Rename** next to its name in Manage crew. Its key, letters, mail and seeds
stay, and an awake member's agent takes the new name. Attn moves a home in the
Notebook to a folder named after the new name, and updates the member's past
sessions to the new home, as `move-home` does. Attn moves the home at once when
the member is asleep. When the member is awake, Attn moves the home at the
member's next wake, because the member's agent works from that folder. A home
in Attn's data folder stays where it is.

`attn crew retire <member>` stops a member's work. The seeds the member tends go
back to planted, each with a note. Attn removes the member's seed and pull
request watches and open asks, and refuses new mail to the member. Attn asks an
awake member to file a letter and sleep. That letter does not start another
session. If you clear the member's conversation before the member files the
letter, Attn asks again. A retired member leaves the Crew section and
`attn crew list` (`--all` includes retired members), does not wake, and does not
block you from deleting the member's profile. The member's home, letters,
unread mail and name stay. `attn crew restore <member>` brings the member back,
and unread mail can wake the member.
</details>

<details>
<summary>How long can a letter be?</summary>

64,000 bytes. Attn refuses a longer one and says so.
</details>

## Crew v2 (experimental)

Turn on **Settings → Experimental → Crew v2** (`crew_v2_enabled`) to try the
next version of the crew. Crew v2 is off by default and applies from each member's
next session. With Crew v2 on:

- Members get shorter guidance. It points them to the work itself, to their
  letter, and to the seeds the letter names, before they act.
- The harness's own memory is off for crew sessions (Claude Code's auto memory,
  Codex memories), so a member carries over only through its charter, letters
  and seeds.
- Each letter records the session that wrote it, so Attn can keep that
  session's transcript for the member to read later.
- You can rename members.
- Every profile has a Chief, a crew member named Chief, which onboards you on
  its first day. See [the Chief](chief.md).
