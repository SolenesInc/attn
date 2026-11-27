# Using outposts

Outposts are experimental. An outpost is another computer running Attn, such as
a Linux machine or a cloud VM, connected to your home: the home connects to it
over ssh, or it dials the home. Your home is the Attn
on the Mac where you use the app, until you move the home to an outpost, as
[Remote home](remote-home.md) describes. One home can connect to several
outposts.

Agents and shells on an outpost appear in terminal tiles beside your
local terminals. The tile header shows the outpost's name under the title.
Input, screen updates and terminal commands pass through your home. Agents
run on the outpost. The agents' state, turns, cost and PR links reach the Mac.
A markdown tile shows its file from the machine that holds it. Editor tiles stay
on the home's machine.

## Add an outpost

An outpost is a Linux amd64 or arm64 machine that you can reach with your own
`ssh` command. Use the same destination in Attn, such as `ai-sandbox`,
`user@host`, or `ssh://host:2222`. Your OpenSSH configuration sets the route,
keys and known hosts.

Add installs your home's Linux build, `attn` and the shared terminal host
`attn-pty-host`, in the install folder:

- Default instance: `~/.local/bin`.
- Named instance: `~/.local/share/attn-outpost-instances/<instance>`.

The outpost needs `sh`, `uname`, `cat`, `mkdir`, `mv`, `chmod`, and `busctl`.

The home and the outpost use the same [instance](../../internal/instances.md)
name. A named home starts only its same-named instance on the outpost.

To add an outpost:

1. Run `ssh <destination>` in your terminal and answer any authentication or
   host-key prompts. Attn's background commands never prompt.
2. On the home, open **Settings → Machines** and turn on **Outposts**.
3. Under **Add a machine**, enter the **ssh destination** and click **Add**. Name is optional and defaults to the destination. The name `home`
   is reserved, and Attn refuses a name that another machine already has,
   ignoring case.

Attn starts the daemon over ssh and keeps it running when ssh disconnects.
The machine must have logind's `KillUserProcesses` off, or Attn refuses and
names the setting. No systemd service or root access is needed.

Adding an outpost finishes when the two daemons pair for the first time. After ssh authentication succeeds, Attn finishes the installation on the next connection attempt. If a home restart interrupts the Add, the Add continues when the home starts again. If the installation fails, the row shows the reason, and Attn waits for you to choose **Update** or **Re-pair** on the outpost's row before it tries another install.

The first connection pairs the daemons. The home remembers the outpost's
identity, and the outpost accepts only that home until you release it.

## Join a home from the outpost

A machine that the home can't reach with ssh, such as a laptop behind NAT or
often asleep, can join the home by dialing it. Turn on the experimental setting
`remote_home_enabled` (**Settings → Experimental**, or
`attn settings set remote_home_enabled true`) on both machines, and turn on
**Outposts** at the home.

1. Install attn on the machine yourself. The home never installs or updates it.
   The home's `attn` must be in its install folder or on the `PATH` of a
   non-interactive ssh login.
   To have the machine update the home to each build you install on it, also
   turn on `self_update_enabled` at the home; see
   [Update the home from the machine that dials it](#update-the-home-from-the-machine-that-dials-it).
2. Run `ssh <home-destination>` once and answer any prompts.
3. On the machine, run `attn enrollment join <home-destination>`. It waits for
   the first dial and prints the result. Run it again to dial at once.

The first connection pairs both sides. The home lists the machine under its
short host name, or under the name you pass with `--name`, and shows it as lost
while it isn't dialing. The machine dials while its daemon runs. After a failed dial it waits longer each time, up to 30 seconds,
and it dials at once when it wakes from sleep. `attn enrollment status` on the
machine shows the dial as `dialing`, `bridged` or `refused`, with the last
error. The home refuses a dial while `remote_home_enabled` or Outposts is off
there, while it already has the machine as an outpost it dials, after you
removed the machine, and when the machine's name is `home` or one another of
its machines has, ignoring case. A machine on the same computer as the home has
the home's host name, so join it with `--name`.

A machine that is its own home and has open sessions refuses to join, and names
them. Close them first. While it is enrolled, its own outposts wait. After
`attn enrollment leave`, its sessions, Garden and settings read as they did
before it joined, and it connects to its own outposts again.

The app on that machine connects to its own daemon, which passes it to the
home. The app shows the home's desktops, queue and Garden, and you use the
home's terminals and its other outposts from it. `attn` on the machine still
talks to its own daemon.

That app names the home's machine where it would name This Mac: on tiles, in the
launcher and in the file opener, and `home:` names it. Files on the home don't
open on this machine. **Open in editor** and Cmd-clicked paths in a home shell
name the home and open nothing here; a markdown link opens in a tile from the
home. A seed's files are in this machine's copy of the Notebook, so **Open** and
**Reveal** on them open that copy, and say so when the copy hasn't synced yet.
**Move out** on a seed's file, and
**Move into seed** or **Copy into seed** with a file you choose, move the file
between the seed and this machine's disk. **Browse** beside a folder the home
reads, such as the projects folder, the Notebook folder, a plugin's source, an
automation's folder or the editor tile's root, lists the home's folders in place
of this machine's dialog: **Enter** chooses the highlighted folder, **Tab** opens
it, and the first row is the folder typed.

Terminals start on the machine as on any outpost: name it in the launcher, or
pass `--host` to `attn terminal open` or `attn delegate`. An `attn://spawn`
link opened on the machine starts its agent there. The app reaches the
machine's own terminals through the machine's daemon, not through the home, so
they echo at local speed.

While the machine can't reach the home, its own terminals keep working. If the
app lost the home after showing it, it keeps the desk under a **Lost** band with
the home's name. The machine's terminals still take typing, and you can move
between them; a popped-out one keeps its window. The home's tiles keep their
last screen and say typing is not sent, and the home's popped-out tiles hide. If the app starts
while the home is unreachable, it shows **Can't reach** with the home's name and
the reason. When the machine has terminals of its own in the profile the app
last showed, they show live in one row, and the sidebar says what waits for the
home. Until the home answers you can type in that row, but you can't split,
move or close its tiles. Without such terminals, or on the app's first launch
with this home, the app shows only **Can't reach**. Either way, the app
connects by itself when the home answers, and the home's desktops replace the
row, with each terminal once. Press **R** or click **Retry** to dial at once.

Attn never installs, updates or re-pairs a machine that dials the home, and
can't connect to it in a terminal. Update attn on that machine. On its row,
**Update**, **Re-pair** and **Connect in a terminal** refuse and say so.

## Update the home from the machine that dials it

With the experimental setting `self_update_enabled` on, which the home decides
like every feature switch, installing a new build on the machine that dials the
home updates the home to that build. The machine does so once for each build you install, and only
when the home still runs the build the two last shared. The machine sends the
home's Linux `attn` and `attn-pty-host` over ssh, and the home switches to them
and restarts. The home then updates the outposts it dials, in the same run, one
attempt each. Running shells keep their processes.

A machine built from source builds the home's binaries itself, so it needs the
same Zig and Rust targets as a home built from source. It also sends the
binaries each outpost of the home needs. When an outpost of the home needs a
build for another platform and the machine that holds the source is away, the
update of that outpost fails, names that machine and installs nothing.

Once the home is back, the app shows the home's run in the banner and
**Settings → Updates**: a row for the home, one for the machine and one for each
outpost, each with its own action. While the home refuses the machine's build,
the app shows **Can't reach** with the reason, the machine's update of the home
and its action, and `attn update status` on the machine prints the same rows.
A failed row names one reason with its cause:

| Reason | Meaning |
| --- | --- |
| no build | The build could not be made or downloaded. |
| unreachable | ssh could not reach the home. |
| refused | The home rejected the build, for example because its database is newer or its platform is not Linux amd64 or arm64. |
| not back | The home switched to the new build and did not connect again. |
| interrupted | attn on the machine restarted before the update finished. Once the home answers, the row shows what the home runs, with **Update** when the builds still differ. |
| behind | The home runs a newer build. The row opens the home's release, copies the command that builds its commit, or, for a build from a changed worktree, says to build it there. |

Every reason but not back leaves the home on its old build. A failed update
does not try again, even when the machine reconnects. An outpost the run left
on its build keeps the build it runs and offers **Update outpost**. When the home runs a
build the two never shared, nothing installs. `attn update status` on the
machine answers while the home is unreachable.

## Read and change settings

Home settings, such as model defaults and automatic settlement, apply on every
outpost. Each outpost keeps a read-only copy and reads it locally, including
while the home is unreachable. `attn settings set` on an outpost changes a home
setting at the home, except `outposts_enabled`, which you change at the home
because the outpost reaches the home over that bridge. The copy updates when the
bridge delivers the change.
`attn settings list` marks the outpost's home settings `set at the home`.
Profile settings, such as a profile's Notebook folder `notebook.root`, live
only at the home. On an outpost, `attn settings get notebook.root` refuses and
points there, and `notebook.root.effective` names this machine's copy.

Machine settings describe one computer: harness executables, the terminal
backend, worktree sweep, the editor command and the Quick Capture shortcut. A
switch for an attn feature is never a machine setting: the home decides it.
Changing a machine setting on an outpost changes only that outpost. On a machine
that dials its home, the app reads and saves the editor and the Quick Capture
shortcut on this machine's own daemon. Computed machine values, such as harness
availability and connection status, also describe the computer where you read
them.

After `attn enrollment leave`, the machine uses the settings it had before
pairing. You can read home settings while offline, but you can change them only
when the home is reachable.

## Update outposts

When an outpost runs another build, choose its row and **Update**, or press U,
or choose **Update outposts** in the banner. Update also retries a failed first
install when nothing is installed yet. The banner and the row show each step
from the moment you choose Update until the outpost runs the home build or the
update fails with its reason. While an update runs, choosing Update again joins
it instead of starting another.

The home connects over ssh, builds or downloads its matching Linux `attn` and
`attn-pty-host`, sends them, verifies them, and reconnects. A home built from
source cross-compiles both, for Add as well as Update, so it needs Zig and Rust
with the outpost's Linux target, such as `rustup target add x86_64-unknown-linux-gnu`.
The first build takes about a minute. Later builds reuse Go's build cache and
rebuild the host, which takes about 30 seconds. If either binary can't be
downloaded or built, the update fails with the reason and installs nothing. A
failed build shows the compiler's error in the banner, and **Details** names the
file under the home's data directory that holds the build's whole output.
Running shells keep their processes. Each old binary
stays beside the new one as `attn.previous` and `attn-pty-host.previous`. Attn
refuses the installation if the outpost's database needs a newer build.

To see where a terminal runs, type this in it:

```sh
sh -c 'ps -o comm= -p $(ps -o ppid= -p $PPID)'
```

It prints `attn-pty-host`, the shared terminal host where every terminal
runs.

After pairing, a reconnect never installs a build. If the installed build
differs or is missing, the row shows why and offers Update.

An outpost can run the home's build without `attn-pty-host`, for example when
you installed `attn` there yourself. Add installs both binaries on such a machine.
If an outpost is already paired, its row says the shared terminal host is not
installed and offers **Install shared terminal host**, and the banner offers
**Update outposts**. Until then, new terminals on it fail to start, and the
error says to update the outpost from its home. Installing the host restarts the outpost's daemon so the daemon finds it.
Running shells keep their processes.

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
The shell opens at the end of the row, and focus stays
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

`attn open notes.md` from an outpost agent, and a markdown file the agent sends,
open a markdown tile that reads the file on the outpost. Edits on the outpost
reach the tile, and its relative images load from the outpost. A link to
another markdown file opens that file from the outpost; other local links name
the outpost and don't open. A file with the same path on the Mac is never shown.
Annotations in these tiles belong to the outpost's file, apart from any Mac
file with the same path.

Markdown files an outpost agent sends or edits show in the file opener (Cmd+P),
also those from while the outpost was away, and open on the outpost. Each row
names its machine, This Mac included. **Tab** lists one machine's files at a
time, This Mac first and then each outpost, its name in the field, and
**Shift+Tab** goes back. **Backspace** in the empty field lists every machine
again. While one machine is listed, a path that starts with `/` or `~` browses
that machine. The outpost checks its files on its own disk, so a file deleted
there leaves the opener. While the outpost can't be reached, its files stay
listed, marked offline, and don't open.

Other files on an outpost don't open on the Mac yet. **Open in editor** and
Cmd-clicked paths in an outpost shell fail and name the outpost, and nothing
opens a Mac file that happens to have the same path. With an outpost shell in
front, the editor tile and the file opener (Cmd+P) start at your notebook root.

## Run agents on an outpost

Install and log in to Claude Code, Codex or Sigma on the outpost. Each machine uses its own harness executable and credentials, because harness executables are machine settings. See [Read and change settings](#read-and-change-settings).

Attn starts a harness on an outpost when that outpost offers it, and refuses
the start, naming the outpost, when it doesn't. An outpost offers the harnesses
it finds, the same way the home does. A plugin harness also needs its plugin at
the home, because the home builds every launch. A harness that runs on a shared
server, such as Sigma, uses the outpost's own server.

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

A seed whose closed session ran on an outpost offers **Resume** on the seed's
page, and the button names the outpost.
`attn seed show` prints a `resume` line that names the outpost. If the session's
directory or conversation is gone, Resume says which and where, and leaves the
seed as it was. While the outpost is unreachable, the seed page dims the button,
which says why when you hover over it or Tab to it, and offers it again when the
outpost returns. **Handover**
asks where the new agent should work and starts it on the home.

The Chief and the rest of the crew run only at the home. Attn refuses to start
or hand off a crew member on an outpost, and the refusal names that limit.

Outpost conversations have no kept copy at the home. If the harness deleted the conversation, Attn says the conversation is no longer in storage when you resume it. Transcript
reads go to the outpost, so they need its connection.

## Start on an outpost from the launcher

With [the launcher](sessions.md#the-launcher) on and an outpost added, the
launcher starts agents and shells on any machine.

Recent projects from every machine share one list. Each row shows its machine,
This Mac included. **Enter** and **Tab** on a row act on the row's machine.

An address names a machine and a place on it, as ssh writes it:
`<machine>:<rest>`. Text without a machine means the home's machine, and `home:`
names it too: This Mac, unless the home runs on another machine. Attn reads a
machine's name by one rule wherever it takes one: in the launcher, in `--host`
on `attn terminal open` and `attn delegate`, and in a bed's repository
addresses. Machine names match whole and ignore case, so names that contain `@`
or `:`, such as `user@host`, work as typed. An outpost's id works too. The
home's machine is named after its short host name: `--host` and bed addresses
take that name as well as `home`, and the launcher takes only `home`. Two
machines that share a name from an earlier Attn are refused by name with both
ids; the launcher lists both under that name and puts the id of the one you
pick into the input.

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
profiles use. A profile uses its `notebook.root`, or `~/attn-notebook` when
that is blank. Profiles on the same folder sync it once.
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
as on a first sync.

Each folder syncs on its own, so one can't hold another. Attn refuses to set a
profile's folder inside or around another profile's folder, or inside or around
the default folder, even when no profile uses the default folder now, because a
new profile starts there. It refuses whether or not an outpost is paired. The
refusal names both folders and the profile involved, and the folder stays as it
was.

In **Settings → Machines**, an outpost's **Details** show its Notebook state:
first sync, syncing with the bytes and items left, in sync, or the error and its
reason. They show the state of the folder furthest from in sync, with an error first.
The counts add up every folder that syncs, and each error starts with its folder's path at
the home. Byte and item counts appear
once the remaining work is known. The details also show a count of conflict copies.
Sync keeps both versions of concurrent edits, as the original and a
`*.sync-conflict-*` copy. For each new conflict copy, attn sends a notification
at the home to every profile that uses the folder. The notification names the
original and the copy, and other profiles never see it. Compare the files, merge
the content you need, and remove the copy to clear the count. Attn moves a file that a sync deletes or
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
outpost shell copies that outpost's file onto the seed before it records the
report. The copy replaces a file on the seed only when an earlier report on that
check copied that file there and the seed's file is still that copy. A report
whose copy would change any other file is refused. If the sync fails, the error
names what is left to sync. After you fix the failure, retry the same command.
`attn seed show <seed>` prints the artifact folder for the machine you called
from.

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

Offline agents keep their queue rows, marked, below the agents you can reach, but Next, automatic advance and Cmd+J skip them. The waiting counts leave them out and name them apart, as in "2 waiting · 1 unreachable". Attn hides the actions Settle, snooze, unsnooze,
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
| lost | Nothing heard for 60 seconds, or no connection. The side that dials keeps trying. A machine that dials the home shows lost until it dials. |
| refused | Attn won't connect until you act. The error says why. |

The last error stays on the outpost's row until it connects again. On the
outpost, `attn settings list --all` shows its side: `outpost_state`,
`outpost_home` and `outpost_last_error`.

## When an outpost is refused

| Refused because | What to do |
| --- | --- |
| the home and the outpost use different instance names | Start an outpost with the same instance name as the home. |
| the home and the outpost run different builds | Choose **Update** on the outpost's row or **Update outposts** in the banner. |
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
An outpost enrolled to another home keeps its enrollment and its running daemon. The same holds for an outpost for which this home's Add was refused. Remove keeps the
binaries, their `.previous` copies, the data folder, and the Notebook folder on
the outpost.

For a machine that joined by dialing, **Remove** closes its terminals and its
bridge and removes the row, without running ssh to it. The machine's next dial
is refused, and it leaves the home on its own, which may be when it wakes. To
stop dialing from the machine first, run `attn enrollment leave` there. The row
stays at the home as lost until you remove it.

With terminals, the row offers **Remove and close N terminals** or **Cancel**.
With none, Attn removes the outpost immediately. If ssh cannot reach the
outpost, **Remove and forget N terminals** closes only their tiles at the home.
To stop what remains there, set the matching `ATTN_INSTANCE` and run `enrollment leave`, then `daemon stop`, with
the outpost's binary.

Turning Outposts off disconnects every outpost and keeps the list for next
time. Their terminals show offline until you turn Outposts on again.
