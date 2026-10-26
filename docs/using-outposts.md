# Using outposts

Outposts are experimental. An outpost is another computer running attn, such
as a Linux box or a cloud VM, that your home connects to. Your home is the attn
on the Mac where you use the app. One home can connect to several outposts.

Shells on an outpost appear in terminal tiles beside your local terminals.
The tile header shows the outpost's name after the title. Input, screen
updates, and terminal commands pass through your home. Agents and file tiles
stay on the home's machine.

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
   up the shells that kept running. To stop the daemon and its shells
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

To update, install the new home build on the Mac and build the outpost
binary again from the same checkout. Copy it to the outpost's `/tmp`, rerun
the `install` command there and run `systemctl --user restart attn.service`.
Keep each attn's data folder, because their identities live there. Use
`install`, not `cp`: `cp` fails with "Text file busy" while attn runs from
the binary, and with `KillMode=process` it keeps running for as long as your
shells do.

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

Only shells run on outposts for now. Spawning an agent with a host fails.
An `attn` command in an outpost shell or a plain SSH shell reaches the home
through the outpost. [Garden](glossary.md) commands and document subscriptions use the home's
records. Hooks and machine settings run on the outpost. If the home is
unreachable, the command fails immediately and names the home. If the bridge
is lost during a command, the error says that it may have run.

Files on an outpost don't open on the Mac yet. **Open in editor** and
⌘-clicked paths in an outpost shell fail and name the outpost, and nothing
opens a Mac file that happens to have the same path. With an outpost shell
in front, the editor tile and the file opener (⌘P) start at your notebook
root.

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
and close, split, kill, and reload refuse immediately, naming the outpost.

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
| the two run different builds | Install the same build on both. |
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
