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
`user@host`, or `ssh://host:2222`. Your OpenSSH configuration sets the route,
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

Adding an outpost finishes when the two daemons pair for the first time. After ssh authentication succeeds, Attn finishes the installation on the next connection attempt. If a home restart interrupts the Add, the Add continues when the home starts again. If the installation fails, the row shows the reason, and Attn waits for you to choose **Update to home build** or **Re-pair** before it tries another install.

The first connection pairs the daemons. The home remembers the outpost's
identity, and the outpost accepts only that home until you release it.

## Read and change settings

Home settings, such as model defaults and automatic settlement, apply on every
outpost. Each outpost keeps a read-only copy and reads it locally, including
while the home is unreachable. `attn settings set` on an outpost changes a home
setting at the home. The copy updates when the bridge delivers the change.
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
binary, for Add as well as Update. The first build takes about a minute. Later builds reuse the build cache and take seconds. Running shells keep their
processes. The old binary stays at `<install path>.previous`. Attn
refuses the installation if the outpost has `pty_shared_host_enabled` on or the outpost's database needs a
newer build.

After pairing, a reconnect never installs a build. If the installed build
differs or is missing, the row shows why and offers Update.

Keep the data folders, because they hold the pairing identities and work. To return to an older build after a build migrated the database, you may need to restore a backup with `attn db restore`.

## Open a shell on an outpost

From an Attn agent at the home, run:

```sh
attn terminal open --host ai-sandbox
attn terminal open --host ai-sandbox --cwd /home/user/project
```

The first command starts in the outpost's home directory. An outpost agent's
`attn terminal open` defaults to its own machine and directory. Use `--host home`
to open a shell at the home. `--cwd` names a directory on the target machine.
The shell opens beside your agent, and focus stays
where it was. Read it and type into it with `attn terminal read`, `run`, and `send`,
using the terminal id printed by `open`.

In an outpost shell, Cmd+D opens another shell on the same outpost, in the
original shell's start directory.

A lost connection leaves the shell running. After the connection returns, the screen comes from the outpost again. Shells also survive a restart of either
daemon. If the shell process on the outpost is gone when the daemons
reconnect, the home marks the shell recoverable. **Resume** starts a new shell
in its place.

An `attn` command in an outpost shell or a plain SSH shell reaches the home
through the outpost. [Garden](../../internal/glossary.md) commands and document
subscriptions use the home's records. Hooks run on the outpost, and the command reads and changes machine settings there. If the home is unreachable, the command fails
immediately and names the home. If the bridge is lost during a command, the
error reports that the command may have run.

Files on an outpost don't open on the Mac yet. **Open in editor** and
Cmd-clicked paths in an outpost shell fail and name the outpost, and nothing
opens a Mac file that happens to have the same path. With an outpost shell in
front, the editor tile and the file opener (Cmd+P) start at your notebook root.

## Run agents on an outpost

Install and log in to Claude Code or Codex on the outpost. Each machine uses its own harness executable and credentials, because harness executables are machine settings. See [Read and change settings](#read-and-change-settings).

An agent starts on an outpost once the first sync of its profile's Notebook
folder finishes. Attn refuses an earlier start and names the folder and the
remaining work. From a home agent,
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
the home through the outpost's daemon.

A closed session's ledger row shows its outpost. **Reopen** in the Sessions
ledger, or `attn session reopen <id>`, resumes the session's conversation on
that same outpost with its cost continuing. If the session's worktree was
deleted, the ledger can offer to recreate the worktree on that outpost, fetching
the branch first when needed. Attn refuses to resume a session whose outpost is
unreachable or removed, and names the host.

The chief and crew run only at the home. Attn refuses to start, wake or hand off
a crew member on an outpost, and refuses to make an outpost agent the chief. The
refusal names that limit. Plugin harnesses, including Pi, also run only at the
home.

Outpost conversations have no kept copy at the home. If the harness deleted the conversation, Attn says the conversation is no longer in storage when you resume it. Files sent by an outpost agent open no tile and enter no recent-file list. Transcript
reads go to the outpost, so they need its connection.

## Start on an outpost from the launcher

With **Launcher 2.0** on in **Settings → Experimental** and an outpost added,
the new-session launcher (**⌘N**, or **⌘T** for a terminal) starts agents and
shells on any machine.

Recent projects from every machine share one list. Each row shows its machine,
This Mac included. **Enter** starts on the row's machine. For an agent, **Tab**
on a repository opens the repository's worktree choices on the row's machine. **Tab** on any other row, and
on every row in a terminal launcher, puts the row's address in the input.

An address names a machine and a place on it, as ssh writes it:
`<machine>:<rest>`. Text without a machine means This Mac, and `home:` names
This Mac too. Machine names match whole and ignore case, so names that contain
`@` or `:`, such as `user@host`, work as typed. An outpost's id works too. When
two outposts share a name, the launcher lists both under that name, and the launcher puts the id of the one
you pick into the input.

- With `ai-sandbox:~/projects/`, the launcher lists folders on ai-sandbox. It
  browses for anything that starts with `~` or `/`.
- With `ai-sandbox:attn`, the launcher searches recent projects and starts the
  result on ai-sandbox. ai-sandbox's own recents come first. Recents from other machines
  follow once ai-sandbox confirms it has the same folder, relative to its home.
- If you type the start of a machine's name, such as `ai`, the launcher offers
  that machine. **Tab** completes `ai-sandbox:~/`.

While you browse an outpost, a line under the input shows what the outpost is
doing: `asking ai-sandbox for ~/projects/ · 120 ms`, `~/scratch is not on
ai-sandbox`, `ai-sandbox unreachable · folders as of 3m ago`, or
`ai-sandbox unreachable · no folders cached for ~/projects`. When a folder you
browse is missing on that machine, the launcher asks your other reachable
machines and adds a row such as `~/scratch/ is on This Mac`. **Enter** on it
changes the address to that machine.

When the launcher opens, it asks each reachable outpost for the outpost's home folder and
the parent folders of the outpost's recent projects, so the launcher usually lists
those folders at once when you browse them. The launcher keeps these listings until it closes.

The launcher refuses a start on an unreachable machine, or with a harness that machine lacks, and shows
the reason. A start on a stalled outpost waits.
**Esc** closes the launcher and leaves the start to finish in the background.

## Notebook sync

Pairing an outpost starts two-way sync of every Notebook folder the home's
profiles use. A profile uses its `notebook.profile_root`, or the shared
`notebook.root` when that is blank. Profiles on the same folder sync it once.
The outpost keeps a copy of each folder at `~/attn-notebook-<name>-<key>`.
`<name>` is the folder's name at the home, lowercased, and `<key>` is 8 hex
digits derived from the folder's path at the home. A non-default instance puts
its name after `attn-notebook`. An agent on an outpost works in the copy of its
profile's folder, and `attn settings get notebook.root.effective` inside that
agent prints the copy. Files edited, created or deleted on either machine reach
the other. Knowledge notes and seed artifacts stay ordinary files you can read
and edit with native tools. Append journal entries with
`attn journal append --entry '<text>'`, so concurrent appends share the home's
writer and date.

Before a folder's first sync, Attn moves a non-empty outpost copy it has no
index for to `<copy>.before-sync-<date>`. The outpost row names that saved
folder. The home supplies the initial contents. Later connections resume from
the existing index.

You can change a profile's folder while paired. The new folder syncs into its
own copy. The old copy keeps its files and stops syncing. If you later point the
profile back to the old folder, Attn moves the old copy aside and syncs it fresh,
as on a first sync. While an outpost is paired, Attn refuses a profile folder
inside another profile's folder, and it refuses to pair an outpost while such
folders exist. The refusal names both profiles and both folders.

**Settings → Outposts** shows one Notebook state per outpost: first sync,
syncing with the bytes and items left, in sync, or the error and its reason. The
row shows the state of the folder furthest from in sync, with an error first.
The counts add up every folder, and each error starts with its folder's path at
the home. Byte and item counts appear
once the remaining work is known. The row also shows a count of conflict copies.
Sync keeps both versions of concurrent edits, as the original and a
`*.sync-conflict-*` copy. Attn sends one notification at the home for each new
conflict copy. Compare the files, merge the content you need, and remove the copy to
clear the count. Attn moves a file that a sync deletes or
replaces on the receiving machine to `<data-dir>/notebook-trash/` and keeps it
there for 30 days. For each path, the trash holds only the latest received
replacement.

From an outpost shell, `attn seed attach <seed> --path <file> --move` or
`--copy` reads that outpost's file and replaces the seed's file of the same
name, if the seed has one.
`attn seed detach <seed> --path <filename> --to <destination>` writes the file
to the destination on that outpost. Each command waits for the matching change
at the home before returning, so a following check report can name the artifact
as evidence. `attn seed check report <seed> <C#> --evidence <file>` from an
outpost shell copies that outpost's file the same way before it records the
report. If the sync fails, the error names what is left to sync. After you fix the failure, retry the
same command. `attn seed show <seed>` prints the artifact folder for the machine
you called from.

The machine-local `.attn` folder, atomic-write and seed-transfer temporary files,
and `.DS_Store` do not sync. Other dotfiles remain content. Attn writes its
marker under `.attn` and adds no `.stfolder` or `.stignore`. If the marker or root
is removed later, sync stops with an error instead of spreading deletions.

The sync engine runs at the home while Outposts is on and a paired outpost remains.
On an outpost it runs while enrolled, including while disconnected. Turning the
home switch off stops the home's engine and keeps the home's index. Removing an
outpost drops that device from the home. `attn enrollment leave` stops the
outpost's engine and drops the outpost's index, and keeps the files of each
copy. At the next pairing, Attn treats each copy as a first sync again. On the
outpost, `attn settings list --all` includes `notebook_sync_state` and
`notebook_sync_last_error`.

## Show the host in your prompt

Outpost shells have `ATTN_HOST` set to the outpost's name. In home shells, `ATTN_HOST` is
unset. Add `$ATTN_HOST` to your prompt, or use this Starship configuration:

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
A strip over the terminal shows "<name> unreachable · typing is not sent" without
changing the terminal's size. Attn does not send typing or resizes, and refuses
immediately to close, split, kill or reload a live terminal, naming the outpost.
A stopped, recoverable agent still allows record-only close, rename and
priority changes.

Offline agents keep their queue rows, marked, but Next, automatic advance and Cmd+J skip them, and the waiting counts leave them out. Attn hides the actions Settle, snooze, unsnooze,
cancel countdown and submit annotation. A running auto-settle countdown shows
frozen, and the assistant window names the unreachable outpost. Rename and
priority remain available.

Messages and rings to an offline agent queue durably at the home. The sender
gets a receipt naming the outpost. When the outpost reconnects, delivery resumes
at a safe prompt, with the usual protection for a recently typed draft.

While the home is connecting to an outpost, each tile shows
"Connecting to <name> · typing is not sent". The tiles show this message after the home
starts, when you turn Outposts on, and when you add or re-pair an outpost. The
terminal's sidebar, queue and home screen rows keep their usual appearance.

A stalled outpost's tiles show "<name> not responding". The connection stays open and still sends typing, without waiting for earlier keys to be answered. If the connection ends before a key's answer arrives, Attn cannot tell whether that key ran, and the tile does not flash for it. Attn may have handed that key to the bridge, so the key can still reach the outpost when the outpost comes back. Look at the shell before you retype.

A tile flashes red for a key only when Attn refuses the key before handing it to
the bridge, or the outpost answers with an error. The tile shows how many keys
may not have been sent, and clears that message when a key you type is sent
again. Attn never retries a key.

While a terminal is offline, an open tile keeps its last screen. If you switch desktops or restart the app, the tile instead shows an empty terminal with a note that the screen is on the outpost. When the connection returns, the tile attaches and shows the current screen.

## Connection states

| State | Meaning |
| --- | --- |
| connecting | The home is connecting to the outpost. |
| connected | Each side receives the other's messages. |
| stalled | Nothing heard for 3 seconds, usually a brief network problem. It returns to connected on its own. |
| lost | Nothing heard for 60 seconds, or the connection dropped. Attn keeps reconnecting. |
| refused | Attn won't connect until you act. The error says why. |

The last error stays on the outpost's row until it connects again. On the
outpost, `attn settings list --all` shows its side: `outpost_state`,
`outpost_home` and `outpost_last_error`.

## When an outpost is refused

| Refused because | What to do |
| --- | --- |
| the home and the outpost use different instance names | Start an outpost with the same instance name as the home. |
| the home and the outpost run different builds | Choose **Update to home build** on the row or **Update outposts** in the banner. |
| the outpost changed host key or daemon id | Someone reinstalled the outpost or replaced its data folder. If you expect that change, choose **Re-pair**. |
| the outpost is paired to another home | This home installs nothing there, even when the outpost's build differs. Run `attn enrollment leave` on the outpost. This home pairs on its next try. |
| ssh needs a host-key answer, passphrase, password, 2FA, or security-key touch | Choose **Connect in a terminal** and answer ssh there. When it succeeds, the outpost connects automatically. Close the terminal with Cmd+W. |
| ssh says the machine's host key changed | Choose **Connect in a terminal** to read ssh's refusal. Check the machine's identity and follow ssh's message to fix your known hosts before trying again. |
| it is the same outpost as another row | You added it twice under two destinations. Remove one. |

The row shows ssh's error verbatim. **Connect in a terminal** opens a focused
terminal beside your active tile on the home. Background connection attempts
pause while you answer. The terminal stays open so you can read the result.
If ssh failed, **Resume** on that terminal runs ssh again in the same tile.
Once the outpost connects, nothing is left to answer, and **Resume**
reports that.

## Remove an outpost

**Remove** closes the outpost's terminals across all profiles. Over ssh, Remove
releases the outpost's enrollment to this home, stops the outpost's daemon for
the home's instance, then exits that outpost's ssh master and removes the row.
An outpost enrolled to another home keeps its enrollment and its running daemon. The same holds for an outpost for which this home's Add was refused. Remove keeps the binary,
`<install path>.previous`, the data folder, and the Notebook folder on the
outpost.

With terminals, the row offers **Remove and close N terminals** or **Cancel**.
With none, Attn removes the outpost immediately. If ssh cannot reach the
outpost, **Remove and forget N terminals** closes only their tiles at the home.
To stop what remains there, set the matching `ATTN_INSTANCE` and run `enrollment leave`, then `daemon stop`, with
the outpost's binary.

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
echo on the same master. The script adds the outpost, and Add installs the matching binary. Use `--mode bulk`
with a copied Notebook to measure a first sync. The harness removes the
instances and scratch files it starts.
