# Using outposts

Outposts are experimental. An outpost is another computer running attn, such
as a Linux box or a cloud VM, that your home connects to. Your home is the attn
on the Mac where you use the app. One home can connect to several outposts.

Claude, Codex and shells on an outpost appear in terminal tiles beside your
local terminals. The tile header shows the outpost's name after the title.
Input, screen updates and terminal commands pass through your home. Agents
run on the outpost; their state, turns, cost and PR links reach the Mac.
File tiles stay on the home's machine.

## Set up an outpost

The outpost runs the same attn build as your home. A different build is
refused. Install the home build on your Mac and build the outpost binary
from the same checkout:

```sh
make install-daemon
make build-linux-amd64 OUTPUT=/tmp/attn-outpost   # or build-linux-arm64
```

To run attn on the outpost:

1. From the Mac, copy `/tmp/attn-outpost` to the outpost's `/tmp` (for
   example `scp /tmp/attn-outpost ai-sandbox:/tmp/`). Then, on the outpost, install it as
   `~/.local/bin/attn` and run it as a systemd user service:

   ```sh
   install -D /tmp/attn-outpost ~/.local/bin/attn
   ```

   ```ini
   # ~/.config/systemd/user/attn.service
   [Unit]
   Description=attn daemon

   [Service]
   ExecStart=%h/.local/bin/attn daemon
   Restart=on-failure
   RestartForceExitStatus=75
   SuccessExitStatus=75
   KillMode=process

   [Install]
   WantedBy=default.target
   ```

   ```sh
   systemctl --user enable --now attn.service
   loginctl enable-linger   # keep it running when you log out
   ```

   `KillMode=process` keeps your shells running when the daemon stops or
   restarts. Without it, systemd ends every shell along with the daemon.
   `Restart=on-failure` starts the daemon again after a crash, and it picks
   up the shells that kept running. Exit status 75 requests a restart after
   a build update and counts as a successful stop. To stop the daemon and its shells
   together, run `systemctl --user kill attn.service`.

2. Add the public key your home's ssh-agent holds (`ssh-add -L` on the Mac) to
   `~/.ssh/authorized_keys`. attn skips lines with options such as
   `restrict`, `command=` or `from=`, so add the key with no options.

3. Choose the address your home will reach it on, for example its Tailscale
   IP, and a free port:

   ```sh
   attn settings set outpost_listen 100.101.102.103:7777
   ```

   Nothing listens until you set this. Set it to `''` to stop listening.

On the home, turn on **Outposts** in Settings → Experimental, then add the
outpost by name and address in Settings → Outposts.

The home and outpost must use the same [instance](instances.md) name. The
default install on both sides already matches. A named instance cannot pair
with production or another named instance.

The first connection pairs them, with no prompt. The home remembers the
outpost's identity, and the outpost accepts only this home from then on.

## Read and change settings

Home settings, such as model defaults and automatic settlement, apply on every
outpost. Each outpost keeps a read-only copy and reads it locally, including
while the home is unreachable. `attn settings set` on an outpost changes a home
setting at the home. The copy follows when the bridge delivers the change.
`attn settings list --all` marks the outpost's home settings read-only.

Machine settings describe one computer: harness executables, the terminal
backend and shared terminal host, worktree sweep, analytics recording,
`notebook.root`, `outposts_enabled`, and `outpost_listen`. Changing one on an
outpost changes only that outpost. Computed machine values, such as harness
availability and connection status, also describe the computer where you read
them.

After `attn enrollment leave`, the machine uses the settings it had before
pairing. Home settings can be read while offline, but changing them requires
the home to be reachable.

## Update outposts

After installing a new home build, click **Update outposts** in the banner
or **Update to home build** on an outpost's row in Settings → Outposts.
The banner updates every outpost on a different build. Each row shows
building or downloading, sending, verifying, and restarting, then the outcome.
Dismissing the banner hides it until the home's build changes. The row button
stays available.

A source install builds the Linux daemon when you click Update. Keep the
checkout the home was built from available, with Zig installed. A home built
from a clean commit uses that commit. For an uncommitted build, the worktree
must still contain the same changes. A release install downloads its own
version's Linux asset. Both cache the result for later pushes. The Mac bundle carries no Linux binary.

The outpost verifies the transfer, the staged build, and database compatibility
before replacing its binary. Its systemd service restarts it, and existing
shells keep running. The home waits up to 60 seconds for it to reconnect on the
new build. An outpost with **Shared terminal host** enabled refuses an update,
because the push carries only `attn`.

Outposts installed before this version need one more manual install, including
those that already support build updates. This version checks instance names
before pairing or sending an update.
Build and copy the binary as in **Set up an outpost**, update the service unit
above, rerun `install`, and run `systemctl --user daemon-reload` followed by
`systemctl --user restart attn.service`. Use `install` when replacing a running
binary. `cp` fails with "Text file busy", including while an old shell's worker
still uses it. Keep the home's and outpost's data folders, because their
pairing identities live there. Install a named instance in its own binary path. An outpost running
as a named instance refuses to replace the default `~/.local/bin/attn`.

If the new build does not come back, check its row's outcome and the outpost's
journal with `journalctl --user -u attn.service`. Fix forward, or stop the service
and install `~/.local/bin/attn.previous` over `~/.local/bin/attn` before starting
it again. If the new build migrated the database, the previous build may also
need a backup restored with `attn db restore`. attn keeps the previous binary
and never rolls back automatically.

## Open a shell on an outpost

From an attn agent at the home, run:

```sh
attn terminal open --host ai-sandbox
attn terminal open --host ai-sandbox --cwd /home/user/project
```

The first command starts in the outpost's home directory. `--cwd` names a
directory on the outpost. The shell opens beside your agent and preserves
focus. Read it and type into it with `attn terminal read`, `run`, and `send`,
using the terminal id printed by `open`.

In an outpost shell, **⌘D** opens another shell on the same outpost, in the
original shell's start directory.

A lost connection leaves the shell running. The screen comes from the
outpost again when it reconnects. Shells also survive a restart of either
daemon. If the shell process on the outpost is gone when the daemons
reconnect, the home marks the shell recoverable. **Resume** starts a new shell
in its place.

An `attn` command in an outpost shell or a plain SSH shell reaches the home
through the outpost. [Garden](glossary.md) commands and document subscriptions
use the home's records. Hooks and machine settings run on the outpost. If the home is
unreachable, the command fails immediately and names the home. If the bridge
is lost during a command, the error says that it may have run.

Files on an outpost don't open on the Mac yet. **Open in editor** and
⌘-clicked paths in an outpost shell fail and name the outpost, and nothing
opens a Mac file that happens to have the same path. With an outpost shell in
front, the editor tile and the file opener (⌘P) start at your notebook root.

## Run agents on an outpost

Install and log in to Claude Code or Codex on the outpost. Each machine uses
its own harness executable and credentials. Home settings, including monitor
policy, are copied read-only to outposts; machine settings, including harness
executables, stay local to each machine.

The outpost's first Notebook sync must finish before an agent can start.
An early spawn refuses and names the remaining work. From the home checkout,
spawn through the app protocol's `spawn_session` with a `host`, or:

```sh
go run ./scripts/wsctl add-session --host studio --agent claude --cwd /home/user/project
```

Use `--agent codex` for Codex. The directory is on the outpost. The Mac can
settle, snooze and unsnooze the agent's turn, read its assistant messages and
transcript, and reload its current conversation. Harness commands such as
`/clear`, `/new` and `/resume` keep the conversation chain and each session's
cost. The agent's `attn` commands reach the home through its outpost daemon.

The chief and crew run only at the home. Spawning, waking or handing off a
crew member on an outpost, or making an outpost agent the chief, refuses and
names that limit. Plugin harnesses, including Pi, also run only at the home.

Outpost conversations have no kept copy at the home. Files sent by a remote
agent open no tile and enter no recent-file list. Transcript reads go to the
outpost, so they need its connection.

## Notebook sync

Pairing an outpost starts two-way sync of the whole Notebook. Each machine has
its own folder, selected by its machine setting `notebook.root`. Read the
resolved path with `attn settings get notebook.root.effective` on that machine.
Files edited, created or deleted on either machine reach the others. Knowledge
notes and seed artifacts stay ordinary files you can read and edit with native
tools. Append journal entries with `attn journal append --entry '<text>'`, so
concurrent appends share the home's writer and date.

Before the first sync, attn moves a non-empty outpost folder it has no index for
to `<root>.before-sync-<date>`. The outpost row names that saved folder. The home
supplies the initial contents. Later connections resume from the existing index.
Changing `notebook.root` while paired sync is active refuses and names the peers.
Turn Outposts off at the home, or clear `outpost_listen` on the outpost, before
changing that machine's root.

Settings → Outposts shows first sync, syncing with the bytes and items left,
in sync, or the error and its reason. Byte and item counts appear once the
remaining work is known. The row also shows a count of conflict copies.
Concurrent edits keep both versions as an original and a
`*.sync-conflict-*` copy. Each new conflict copy produces one notification at
the home. Compare the files, merge the content you need, and remove the copy to
clear the count. Received deletions and replacements go to
`<data-dir>/notebook-trash/` on the receiving machine for 30 days. This trash
keeps the latest received replacement at each path.

From an outpost shell, `attn seed attach <seed> --path <file> --move` or `--copy` reads
that outpost's file. `attn seed detach <seed> --path <filename> --to <destination>`
writes its destination on that outpost. Each command waits for the matching
change at the home before returning, so a following check report can name the
artifact as evidence. A sync failure names what is left. After fixing it, retry
the same command. `attn seed show <seed>` prints the artifact folder for the
machine you called from.

The machine-local `.attn` folder, atomic-write and seed-transfer temporary files,
and `.DS_Store` do not sync. Other dotfiles remain content. attn writes its
marker under `.attn` and adds no `.stfolder` or `.stignore`. If the marker or root
is removed later, sync stops with an error instead of spreading deletions.

The engine runs at the home while Outposts is on and a paired outpost remains.
On an outpost it runs while enrolled and listening, including while disconnected.
Turning the home switch off stops its engine and keeps its index. Removing an
outpost drops that device from the home. `attn enrollment leave` stops the
outpost's engine and drops its index, retaining its Notebook files. The next
pairing treats that folder as a first sync again. On the outpost,
`attn settings list --all` includes `notebook_sync_state` and
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

A terminal is offline when its outpost's connection is lost or refused, or
when Outposts is off. Each offline terminal stays in place. Its state dot
becomes hollow and crossed, and its sidebar and home screen rows have a faint
red tint. A strip over the terminal says "<name> unreachable · typing is not
sent" without changing the terminal's size. Typing and resizes are not sent,
and close, split, kill and reload of a live terminal refuse immediately,
naming the outpost. A stopped, recoverable agent still supports record-only
close, rename and priority changes.

Offline agents keep their queue rows, marked, but Next, automatic advance,
⌘J and waiting counts skip them. Settle, snooze, unsnooze, countdown cancel
and annotation submit are hidden. A running auto-settle countdown shows
frozen, and the assistant window names the unreachable outpost. Rename and
priority remain available.

Messages and rings to an offline agent queue durably at the home. The sender
gets a receipt naming the outpost. When it reconnects, delivery resumes at a
safe prompt, including the usual protection for a recently typed draft.

While the home is still connecting to an outpost, its terminals don't look
offline, but typing is not sent yet. This happens after the home starts, when
you turn Outposts on, and when you add or re-pair an outpost. A stalled
connection still sends typing. If it then turns lost, the keys still on their
way fail. The tile says how many keys may not have been sent, and clears that
message when a key you type goes out again. Some of those keys may already
have left the home and reach the outpost when it comes back, so look at the
shell before you retype.

Every key that is not sent flashes its tile red. attn never retries a key it
could not send.

A tile that stays open keeps its last screen. If you switch desktops or
restart the app while the terminal is offline, its tile shows an empty
terminal and tells you that the screen is on the outpost. When the connection
returns, the tile attaches and shows the current screen.

## Connection states

| State | Meaning |
| --- | --- |
| connecting | The home is dialing the outpost. |
| connected | Both attns hear each other. |
| stalled | Nothing heard for 3 seconds, usually a network hiccup. It returns to connected on its own. |
| lost | Nothing heard for 10 seconds, or the connection dropped. attn keeps reconnecting. |
| refused | attn won't connect until you act. The error says why. |

The last error stays on the outpost's row until it connects again. On the
outpost, `attn settings list --all` shows its side: `outpost_state`,
`outpost_home` and `outpost_last_error`.

## When an outpost is refused

| Refused because | What to do |
| --- | --- |
| the two use different instance names | Start an outpost with the same instance name as the home. |
| the two run different builds | Click **Update to home build**. Older outposts need the manual install described above. |
| the outpost changed host key or daemon id | It was reinstalled or its data folder replaced. If you expect that, **Re-pair** it. |
| the outpost is paired to another home | Run `attn enrollment leave` on the outpost. This home pairs on its next try. |
| no ssh-agent, no keys in it, or no key accepted | Load your key with `ssh-add`, and check it is in the outpost's `authorized_keys`. |
| it is the same outpost as another row | You added it twice under two addresses. Remove one. |

## Remove an outpost

**Remove** closes the outpost's terminals across all profiles, then forgets
the outpost and its identity. When the outpost has terminals, the row asks you to
confirm with **Remove and close N terminals**, or **Cancel**. When the outpost has
none, removal happens immediately. If the outpost is unreachable or still
connecting, the row asks **Remove and forget N terminals** instead and says
that its shells keep running on the outpost: Remove closes their tiles at the
home only. Run `attn enrollment leave` on the outpost to stop those shells.

Turning Outposts off disconnects every outpost and keeps the list for next
time. Their terminals show offline until you turn Outposts on again.

## Checking a change to outposts (for contributors)

`scripts/verify-outposts.py` runs two isolated attns on one machine, pairs
them, freezes the outpost and checks that it goes stalled, lost and back to
connected. `scripts/verify-outpost-systemd.py` runs on a Linux box with
systemd. It starts the outpost under the unit from this page and checks that
two shells keep their process, screen and state through
`systemctl --user restart`, `attn daemon stop` followed by
`systemctl --user start`, a daemon crash, and an update to another build.
Run either with `--help` for its arguments.

For a real bridge check, `scripts/outpost-instance.py` starts a named outpost
on ai-sandbox with its own binary, data folder, listener and systemd user unit.
Copy the script and Linux binary there, then run it through Bash (the login
shell is fish):

```sh
scp scripts/outpost-instance.py /tmp/attn-outpost ai-sandbox:/tmp/
ssh ai-sandbox bash -s <<'EOF'
python3 /tmp/outpost-instance.py start --instance router-check --binary /tmp/attn-outpost --listen 127.0.0.1:17777
EOF
```

Pair it only with the Mac's `router-check` instance. The example listens on
localhost, so reach it through an SSH tunnel from the Mac, or pass `--listen`
an address the Mac can reach directly. When finished, run:

```sh
ssh ai-sandbox bash -s <<'EOF'
python3 /tmp/outpost-instance.py remove --instance router-check
EOF
```

The helper stops the user unit and cleans the instance before removing its
binary and unit file. If cleanup cannot stop a worker, it preserves the
instance data and reports what remains. Resolve that failure and rerun removal.
