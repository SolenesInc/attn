# Using outposts

Outposts are experimental. An outpost is another computer running Attn, such as
a Linux machine or a cloud VM, that your home connects to. Your home is the Attn
on the Mac where you use the app. One home can connect to several outposts.

Claude, Codex and shells on an outpost appear in terminal tiles beside your
local terminals. The tile header shows the outpost's name after the title.
Input, screen updates and terminal commands pass through your home. Agents
run on the outpost. The agents' state, turns, cost and PR links reach the Mac.
File tiles stay on the home's machine.

## Add an outpost

An outpost is a Linux amd64 or arm64 machine that you can reach with your own
`ssh` command. Use the same destination in Attn, such as `ai-sandbox`,
`user@host`, or `ssh://host:2222`. Your OpenSSH configuration chooses the route,
keys and known hosts.

Add installs your home's Linux build at the install path:

- Default instance: `~/.local/bin/attn`.
- Named instance: `~/.local/share/attn-outpost-instances/<instance>/attn`.

The outpost needs `sh`, `uname`, `cat`, `mkdir`, `mv`, `chmod`, and `busctl`.

The home and the outpost use the same [instance](../../internal/instances.md)
name. A named home starts only its same-named instance on the outpost.

To add an outpost:

1. Run `ssh <destination>` in your terminal and answer any authentication or
   host-key prompts. Attn's background commands never prompt.
2. On the home, turn on **Outposts** in **Settings → Experimental**.
3. Open **Settings → Outposts**, enter the **ssh destination**, and click **Add
   outpost**. Name is optional and defaults to the destination. The name `home`
   is reserved.

Attn starts the daemon over ssh and keeps it running when ssh disconnects.
The machine must have logind's `KillUserProcesses` off, or Attn refuses and
names the setting. No systemd service or root access is needed.

Add continues until the daemons first pair. After ssh authentication succeeds,
Attn finishes installation on the next connection attempt. An Add interrupted by
a home restart continues when the home starts again. If installation fails, the
row keeps the reason, and Attn waits for you to choose **Update to home build**
or **Re-pair** before trying another install.

The first connection pairs the daemons. The home remembers the outpost's
identity, and the outpost accepts only that home until you release it.

## Read and change settings

Home settings, such as model defaults and automatic settlement, apply on every
outpost. Each outpost keeps a read-only copy and reads it locally, including
while the home is unreachable. `attn settings set` on an outpost changes a home
setting at the home. The copy follows when the bridge delivers the change.
`attn settings list --all` marks the outpost's home settings read-only.

Machine settings describe one computer: harness executables, the terminal
backend and shared terminal host, worktree sweep, analytics recording,
`notebook.root` and `outposts_enabled`. Changing a machine setting on an
outpost changes only that outpost. Computed machine values, such as harness
availability and connection status, also describe the computer where you read
them.

After `attn enrollment leave`, the machine uses the settings it had before
pairing. You can read home settings while offline, but you can change them only
when the home is reachable.

## Update outposts

When an outpost runs another build, choose **Update to home build** on its
row or **Update outposts** in the banner. Update also retries a failed first
install when nothing is installed yet.

The home builds or downloads its matching Linux binary, sends it over ssh,
verifies it, and reconnects. A home built from source cross-compiles the Linux
binary, for Add as well as Update. Its first build takes about a minute; later
builds reuse the build cache and take seconds. Running shells keep their
processes. The old binary stays at `<install path>.previous`. Installation
refuses if the outpost has `pty_shared_host_enabled` on or its database needs a
newer build.

After pairing, a reconnect never installs a build. If the installed build
differs or is missing, the row says why and offers Update.

Keep the data folders, because they hold the pairing identities and work.
If a build migrated the database, an older build may need a backup restored
with `attn db restore`.

## Open a shell on an outpost

From an Attn agent at the home, run:

```sh
attn terminal open --host ai-sandbox
attn terminal open --host ai-sandbox --cwd /home/user/project
```

The first command starts in the outpost's home directory. An outpost agent's
`attn terminal open` defaults to its own machine and directory. Use `--host home`
to open a shell at the home. `--cwd` names a directory on the target machine.
The shell opens beside your agent and preserves
focus. Read it and type into it with `attn terminal read`, `run`, and `send`,
using the terminal id printed by `open`.

In an outpost shell, **⌘D** opens another shell on the same outpost, in the
original shell's start directory.

A lost connection leaves the shell running. After the connection returns, the screen comes from the outpost again. Shells also survive a restart of either
daemon. If the shell process on the outpost is gone when the daemons
reconnect, the home marks the shell recoverable. **Resume** starts a new shell
in its place.

An `attn` command in an outpost shell or a plain SSH shell reaches the home
through the outpost. [Garden](../../internal/glossary.md) commands and document
subscriptions use the home's records. Hooks run on the outpost, and the command reads and changes machine settings there. If the home is unreachable, the command fails
immediately and names the home. If the bridge is lost during a command, the
error says that the command may have run.

Files on an outpost don't open on the Mac yet. **Open in editor** and
⌘-clicked paths in an outpost shell fail and name the outpost, and nothing
opens a Mac file that happens to have the same path. With an outpost shell in
front, the editor tile and the file opener (⌘P) start at your notebook root.

## Run agents on an outpost

Install and log in to Claude Code or Codex on the outpost. Each machine uses its
own harness executable and credentials. The home copies home settings, including
monitor policy, read-only to outposts. Machine settings, including harness
executables, stay local to each machine.

The outpost's first Notebook sync must finish before an agent can start.
An early spawn refuses and names the remaining work. From a home agent,
delegate onto an outpost with:

```sh
attn delegate --seed s-example --role builder --host ai-sandbox \
  --cwd '~/project' --new-worktree --branch feature-x --from main
```

`attn delegate` defaults to the caller's machine. The target machine expands and
checks the directory, refs and worktree path, and creates the worktree. An
outpost agent uses `--host home` to delegate at the home. Attn refuses an
unknown or unreachable host before it accepts the delegation. After a home
restart, Attn resumes an accepted delegation on the same machine and path. If
that outpost is offline, the accepted operation waits until the outpost returns.
`attn delegate status <request-id>` names the host the operation is waiting for.
Removing that outpost fails the waiting operation.

On the Mac, you can settle, snooze and unsnooze the agent's turn, read the
agent's assistant messages and transcript, and reload the agent's current
conversation. Harness commands such as `/clear`, `/new` and `/resume` keep the
conversation chain and each session's cost. The agent's `attn` commands reach
the home through its outpost daemon.

A closed session's ledger row shows its outpost. **Reopen** in the Sessions
ledger, or `attn session reopen <id>`, resumes the session's conversation on
that same outpost with its cost continuing. If the session's worktree was
deleted, the ledger can offer to recreate the worktree on that outpost, fetching
the branch first when needed. Attn refuses to resume a session whose outpost is
unreachable or removed, and names the host.

The chief and crew run only at the home. Attn refuses to spawn, wake or hand off
a crew member on an outpost, and refuses to make an outpost agent the chief. The
refusal names that limit. Plugin harnesses, including Pi, also run only at the
home.

Outpost conversations have no kept copy at the home. If the harness deleted the
conversation, Attn says it is no longer in storage when you resume it. Files
sent by a remote agent open no tile and enter no recent-file list. Transcript
reads go to the outpost, so they need its connection.

## Notebook sync

Pairing an outpost starts two-way sync of the shared Notebook. Each machine has
its own folder, selected by its machine setting `notebook.root`. Read the
resolved path with `attn settings get notebook.root.effective` on that machine.
A profile that sets its own folder with `notebook.profile_root` keeps that
folder at the home, unsynced. The profile's agents on an outpost see the shared Notebook,
and `attn seed attach`, `detach` and `check report --evidence` refuse to run
there for that profile's seeds and name the folder. Run the commands from a
shell at the home. Files edited, created or deleted on either machine reach the
others. Knowledge notes and seed artifacts stay ordinary files you can read and
edit with native tools. Append journal entries with
`attn journal append --entry '<text>'`, so concurrent appends share the home's
writer and date.

Before the first sync, Attn moves a non-empty outpost folder it has no index for
to `<root>.before-sync-<date>`. The outpost row names that saved folder. The
home supplies the initial contents. Later connections resume from the existing
index. Attn refuses to change `notebook.root` while paired sync is active, and
names the peers. Turn Outposts off at the home before changing the home's root.
On an outpost, run `attn enrollment leave` before changing the outpost's root.

**Settings → Outposts** shows first sync, syncing with the bytes and items left,
in sync, or the error and its reason. Byte and item counts appear once the
remaining work is known. The row also shows a count of conflict copies.
Concurrent edits keep both versions as an original and a
`*.sync-conflict-*` copy. Each new conflict copy produces one notification at
the home. Compare the files, merge the content you need, and remove the copy to
clear the count. Attn moves a file that a sync deletes or
replaces on the receiving machine to `<data-dir>/notebook-trash/` and keeps it
there for 30 days. For each path, the trash keeps the latest received
replacement.

From an outpost shell, `attn seed attach <seed> --path <file> --move` or
`--copy` reads that outpost's file and replaces the seed's file of the same
name, if the seed has one.
`attn seed detach <seed> --path <filename> --to <destination>` writes the file
to the destination on that outpost. Each command waits for the matching change
at the home before returning, so a following check report can name the artifact
as evidence. `attn seed check report <seed> <C#> --evidence <file>` from an
outpost shell copies that outpost's file the same way before it records the
report. A sync failure names what is left. After you fix the failure, retry the
same command. `attn seed show <seed>` prints the artifact folder for the machine
you called from.

The machine-local `.attn` folder, atomic-write and seed-transfer temporary files,
and `.DS_Store` do not sync. Other dotfiles remain content. Attn writes its
marker under `.attn` and adds no `.stfolder` or `.stignore`. If the marker or root
is removed later, sync stops with an error instead of spreading deletions.

The engine runs at the home while Outposts is on and a paired outpost remains.
On an outpost it runs while enrolled, including while disconnected. Turning the
home switch off stops the home's engine and keeps the home's index. Removing an
outpost drops that device from the home. `attn enrollment leave` stops the
outpost's engine and drops the outpost's index, and keeps the outpost's Notebook
files. The next pairing treats that folder as a first sync again. On the
outpost, `attn settings list --all` includes `notebook_sync_state` and
`notebook_sync_last_error`.

## Show the host in your prompt

Outpost shells have `ATTN_HOST` set to the outpost's name. Home shells leave
it unset. Add `$ATTN_HOST` to your prompt, or use this Starship configuration:

```toml
[hostname]
ssh_only = false
detect_env_vars = ["SSH_CONNECTION", "ATTN_HOST"]
```

The hostname then appears over SSH and inside an outpost shell. It stays
hidden in a local shell. `ssh_only = true` does not detect `ATTN_HOST`.

## Work while an outpost is offline

A terminal is offline when its outpost's connection is lost or refused, or when
Outposts is off. Each offline terminal stays in place. Its state dot becomes
hollow and crossed, and its sidebar and home screen rows have a faint red tint.
A strip over the terminal says "<name> unreachable · typing is not sent" without
changing the terminal's size. Attn does not send typing or resizes, and refuses
immediately to close, split, kill or reload a live terminal, naming the outpost.
A stopped, recoverable agent still supports record-only close, rename and
priority changes.

Offline agents keep their queue rows, marked, but Next, automatic advance, ⌘J
and waiting counts skip them. Attn hides the actions Settle, snooze, unsnooze,
cancel countdown and submit annotation. A running auto-settle countdown shows
frozen, and the assistant window names the unreachable outpost. Rename and
priority remain available.

Messages and rings to an offline agent queue durably at the home. The sender
gets a receipt naming the outpost. When the outpost reconnects, delivery resumes
at a safe prompt, with the usual protection for a recently typed draft.

While the home is connecting to an outpost, each tile says
"Connecting to <name> · typing is not sent". The tiles say this after the home
starts, when you turn Outposts on, and when you add or re-pair an outpost. The
terminal's sidebar, queue and home screen rows keep their usual appearance.

A stalled outpost's tiles say "<name> not responding". The connection stays open
and still sends typing, without waiting for earlier keys to be answered. If the
connection ends before a key's answer arrives, Attn cannot tell whether that key
ran. Attn does not flash the tile for that key. A key that Attn handed to the bridge may still reach the outpost when the outpost comes back, so look at the shell before you
retype.

A tile flashes red for a key only when Attn refuses the key before handing it to
the bridge, or the outpost answers with an error. The tile says how many keys
may not have been sent, and clears that message when a key you type goes out
again. Attn never retries a key.

A tile that stays open keeps its last screen. If you switch desktops or
restart the app while the terminal is offline, its tile shows an empty
terminal and tells you that the screen is on the outpost. When the connection
returns, the tile attaches and shows the current screen.

## Connection states

| State | Meaning |
| --- | --- |
| connecting | The home is connecting to the outpost. |
| connected | The home and the outpost hear each other. |
| stalled | Nothing heard for 3 seconds, usually a brief network problem. It returns to connected on its own. |
| lost | Nothing heard for 60 seconds, or the connection dropped. Attn keeps reconnecting. |
| refused | Attn won't connect until you act. The error says why. |

The last error stays on the outpost's row until it connects again. On the
outpost, `attn settings list --all` shows its side: `outpost_state`,
`outpost_home` and `outpost_last_error`.

## When an outpost is refused

| Refused because | What to do |
| --- | --- |
| the two use different instance names | Start an outpost with the same instance name as the home. |
| the two run different builds | Choose **Update to home build** on the row or **Update outposts** in the banner. |
| the outpost changed host key or daemon id | It was reinstalled or its data folder replaced. If you expect that, **Re-pair** it. |
| the outpost is paired to another home | This home installs nothing there, even when its build differs. Run `attn enrollment leave` on the outpost. This home pairs on its next try. |
| ssh needs a host-key answer, passphrase, password, 2FA, or security-key touch | Choose **Connect in a terminal** and answer ssh there. When it succeeds, the outpost connects automatically. Close the terminal with **⌘W**. |
| ssh says the machine's host key changed | Choose **Connect in a terminal** to read ssh's refusal. Check the machine's identity and follow ssh's message to fix your known hosts before trying again. |
| it is the same outpost as another row | You added it twice under two destinations. Remove one. |

The row shows ssh's error verbatim. **Connect in a terminal** opens a focused
terminal beside your active tile on the home. Background connection attempts
pause while you answer. The terminal stays open so you can read the result.
If ssh failed, **Resume** on that terminal runs ssh again in the same tile.
Once the outpost connects, there is nothing left to answer, and **Resume**
says so.

## Remove an outpost

**Remove** closes the outpost's terminals across all profiles. Over ssh, Remove
releases the outpost's enrollment to this home, stops the outpost's daemon for
the home's instance, then exits that outpost's ssh master and removes the row.
An outpost enrolled to another home, such as one whose Add this home was
refused, keeps its enrollment and its running daemon. Remove keeps the binary,
`<install path>.previous`, the data folder, and the Notebook folder on the
outpost.

With terminals, the row offers **Remove and close N terminals** or **Cancel**.
With none, Attn removes the outpost immediately. If ssh cannot reach the
outpost, **Remove and forget N terminals** closes only their tiles at the home.
To stop what remains there, set the matching `ATTN_INSTANCE` and run these with
the outpost's binary: `enrollment leave`, then `daemon stop`.

Turning Outposts off disconnects every outpost and keeps the list for next
time. Their terminals show offline until you turn Outposts on again.

## Checking a change to outposts

The stack tests in `cmd/attn` pair through testworld's fake ssh and run real
daemons, bridge processes and terminal workers. Run the focused lifecycle check:

```sh
go test ./cmd/attn -run TestSSH -v
```

For a fresh Add, Update, and Remove on your ssh route, run
`scripts/verify-ssh-outposts/install.mjs --help`. It starts from no files for
its named instance, records upload time, verifies a shell survives Update,
and checks that Remove keeps the instance's files and stops its daemon.

`scripts/verify-ssh-outposts/transport.mjs` measures typing echo beside a plain
echo on the same master. Add installs the matching binary. Use `--mode bulk`
with a copied Notebook to measure a first sync. The harness removes the
instances and scratch files it starts.
