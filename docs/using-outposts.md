# Using outposts

Outposts are experimental. An outpost is another computer running attn, such
as a Linux box or a cloud VM, that your home connects to. Your home is the attn
on the Mac whose app you use, and it can connect to several outposts.

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

On the outpost:

1. Copy `/tmp/attn-outpost` to `~/.local/bin/attn` and run it as a systemd
   user service:

   ```ini
   # ~/.config/systemd/user/attn.service
   [Unit]
   Description=attn daemon

   [Service]
   ExecStart=%h/.local/bin/attn daemon

   [Install]
   WantedBy=default.target
   ```

   ```sh
   systemctl --user enable --now attn.service
   loginctl enable-linger   # keep it running when you log out
   ```

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

The first connection pairs them, with no prompt. The home remembers the
outpost's identity, and the outpost accepts only this home from then on.
Their identities live in each attn's data folder, so keep that folder when
you update the binary.

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

A split from an outpost shell stays on that outpost, in the original shell's
start directory. Removing the outpost closes its terminals. If it is
unreachable, the home closes the terminals on its side. Run
`attn enrollment leave` on the outpost later to stop the shells still running
there.

A lost connection leaves the shell running. The screen comes from the
outpost again when it reconnects. Shells also survive a restart of either
daemon. If the shell process on the outpost is gone when the daemons
reconnect, the home marks the shell recoverable. Revive it to start a new
shell in its place.

Only shells run on outposts for now. Spawning an agent with a host fails.
An `attn` command that needs a daemon also fails inside an outpost shell,
and the error names the outpost and tells you to run the command from an
agent at the home.

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

## Work in a shell on an outpost

To open the first shell, ask an agent to run `attn terminal open --host <name>`.
It opens beside the agent's tile. In that shell, **⌘D** opens another shell on
its outpost, using the first shell's start directory.

When the bridge is lost or refused, each terminal stays in place. Its state
dot becomes hollow and crossed, and its sidebar and Dashboard rows have a
faint red tint. A strip over the terminal says "<name> unreachable · typing
is not sent" without changing the terminal's size. Each refused key flashes
the tile red. Typing and resizes are not sent, and close, split, kill, and
reload refuse immediately, naming the outpost. A stalled or connecting
bridge does not mark terminals offline.

A tile that stays open keeps its last screen. If you switch desktops or
restart the app while the terminal is offline, its tile shows an empty
terminal and tells you that the screen is on the outpost. When the bridge
returns, the tile attaches and shows the current screen. Refused typing is
never replayed.

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
| the two run different builds | Install the same build on both. |
| the outpost changed host key or daemon id | It was reinstalled or its data folder replaced. If you expect that, **Re-pair** it. |
| the outpost is paired to another home | Run `attn enrollment leave` on the outpost. This home pairs on its next try. |
| no ssh-agent, no keys in it, or no key accepted | Load your key with `ssh-add`, and check it is in the outpost's `authorized_keys`. |
| it is the same outpost as another row | You added it twice under two addresses. Remove one. |

**Remove** closes the outpost's terminals across all profiles, then forgets
the outpost and its identity. When it has terminals, the row asks you to
confirm with **Remove and close N terminals**, or **Cancel**. When it has
none, removal happens immediately. If the outpost is unreachable, the home
closes its records; run `attn enrollment leave` on the outpost to stop its
remaining shells.

Turning Outposts off disconnects every outpost and keeps the list for next
time.

## Checking a change to outposts (for contributors)

`scripts/verify-outposts.py` runs two isolated attns on one machine, pairs
them, freezes the outpost and checks that it goes stalled, lost and back to
connected. Run it with `--help` for its arguments.
