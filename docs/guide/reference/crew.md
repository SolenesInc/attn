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

A member is a folder in Attn's data folder: `~/.attn/crew/<name>/` (for a named
instance, `~/.attn-<instance>/crew/<name>/`), with a `CHARTER.md` inside.

1. Pick a folder name: lowercase letters, digits and `-`, starting with a
   letter, at most 40 characters. `attn` is taken. The folder name is the
   member's permanent key; its name starts as the key with a capital letter,
   so `fern` is Fern.
2. Create `crew/<name>/CHARTER.md`. Leave it empty if you want the member to
   write it: its first session agrees a name and charter with you, then
   writes the file in its own words.
3. Restart the daemon: `attn daemon stop`, then `attn daemon ensure`. Attn
   adds new homes to the roster when the daemon starts.

The member joins the profile you used most recently, and wakes only there.
`attn crew list` shows the members of your profile, with their state,
harness, model, launch desktop and home folder.

A member's name is unique in its profile, ignoring case. Commands find a
member by name in your profile: inside Attn, the profile of the session you
run them from; outside, pass `--profile <name|id>` when you have several.
`member:<key>`, such as `member:fern`, reaches the member by its key, which
never changes.

## The Crew section

The sidebar has a Crew section. In [queue flow](queue.md) it lists every
member, asleep or awake. In desktop flow it lists the members who are asleep;
awake members are tiles on their desktops.

- A sleeping member's row shows a sun. Click it once to arm it and again to
  wake the member.
- An awake member's row has a ☾ that asks the member to sleep.
- ••• on a row opens **Member details**. **manage** in the section header, or
  "Manage crew" in the command palette (Cmd+Shift+K, Ctrl+Alt+K on Linux),
  opens the same window.

**Manage crew** has four tabs for each member:

- **Launch settings**. The launch desktop and the harness, model and effort
  the next session starts with, and the Wake or Sleep button. Changes save as
  you make them.
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
member's home and never edits it; a correction is a new letter. Then:

- Plain `attn handoff` ends the session and the member sleeps until something
  wakes it.
- `attn handoff --nap` ends the session and starts the next one right away
  from the letters. The new session picks up any exchange that was in
  progress without asking you to repeat yourself.

If the turnover fails, the letter stays filed and the session keeps running.
`attn handoff --retry` uses that letter to end the session with the member
asleep; `--retry --nap` starts the next session instead.

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

By default a member starts on its own desktop, named after it, which Attn
creates the first time the member wakes.

## Details

<details>
<summary>Do crew members wait in my queue?</summary>

Not by default. Members keep their rows in the Crew section and stay out of the queue's waiting list. Run "Put the crew in the queue" from the command palette to make a member that owes you a turn wait in the queue too; its row stays in the Crew section. "Take the crew out of the queue" turns it off. The setting applies to every profile. In queue flow, the Chief, if you have one, has its own row at the top of the Crew section. The Chief is not a crew member and never waits in the queue.
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
<summary>What happens if I close a member's agent without a letter?</summary>

The member is asleep. Its next session starts from the last letter it filed, so anything the closed session did after that letter is only in the work itself and in its seeds.
</details>

<details>
<summary>Can I rename or remove a member?</summary>

With Crew v2 on, rename a member with `attn crew rename <member> <name>` or
**Rename** next to its name in Manage crew. Its key, home folder, letters,
mail and seeds stay, and an awake member's agent takes the new name. Attn has
no command to remove a member yet; the roster keeps every member it has
imported.
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
