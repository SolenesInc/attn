# Using outposts

Outposts are experimental. An outpost is another computer running attn, such
as a Linux box or a cloud VM, that your home connects to. Your home is the attn
on the Mac whose app you use, and it can connect to several outposts. The goal
is to run agents on those computers from the same app, queue and garden.

For now an outpost only connects. The app shows each one with its state, and
explains why when a connection is refused. Agents, terminals and files don't
cross yet.

## Set up an outpost

The outpost runs the same attn build as your home; a different build is
refused. Build both from the same checkout:

```sh
make build OUTPUT=/tmp/attn-home
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
   `restrict`, `command=` or `from=`, so add the key without any.

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
Their identities live in each attn's data folder, so keep it when you update
the binary.

## Connection states

| State | Meaning |
| --- | --- |
| connected | Both attns hear each other. |
| stalled | Nothing heard for 3 seconds, usually a network hiccup. It returns to connected on its own. |
| lost | Nothing heard for 10 seconds, or the connection dropped. attn keeps reconnecting. |
| refused | attn won't connect until you act; the error says why. |

The last error stays on the outpost's row until it connects again. On the
outpost, `attn settings list --all` shows its side: `outpost_state`,
`outpost_home` and `outpost_last_error`.

## When an outpost is refused

| Refused because | What to do |
| --- | --- |
| the two run different builds | Install the same build on both. |
| the outpost changed host key or daemon id | It was reinstalled or its data folder replaced. If you expect that, **Re-pair** it. |
| the outpost is paired to another home | Run `attn enrollment leave` on the outpost; this home pairs on its next try. |
| no ssh-agent, no keys in it, or no key accepted | Load your key with `ssh-add`, and check it is in the outpost's `authorized_keys`. |
| it is the same outpost as another row | You added it twice under two addresses. Remove one. |

**Remove** forgets the outpost and its identity. Turning Outposts off
disconnects every outpost and keeps the list for next time.

## Checking a change to outposts

`scripts/verify-outposts.py` runs two isolated attns on one machine, pairs
them, freezes the outpost and checks that it goes stalled, lost and back to
connected. Run it with `--help` for its arguments.
