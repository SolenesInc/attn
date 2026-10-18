# Outposts

An outpost is a daemon reached from your home daemon over a bridge. This first
experimental slice pairs the daemons and reports connection health. Agents,
terminals and files do not cross the bridge yet.

The home connects to an address you provide. The outpost listens only after you
set `outpost_listen`; an empty value turns its listener off. The home makes no
connections until `outposts_enabled` is true. Both settings are off by default.

## Run an outpost by hand

Build the home and outpost from the same revision, with separate output paths
so the Linux build keeps the native binary:

```sh
make build OUTPUT=/tmp/attn-home
make build-linux-amd64 OUTPUT=/tmp/attn-outpost
```

Use `build-linux-arm64` for an arm64 outpost. The bridge checks the version,
source fingerprint and protocol version, including `unknown` values. A plain
`go build` cannot distinguish two revisions that both report `dev` and `unknown`.

Copy `/tmp/attn-outpost` to `~/.local/bin/attn` on the outpost. Install this user
service as `~/.config/systemd/user/attn.service`:

```ini
[Unit]
Description=attn daemon

[Service]
ExecStart=%h/.local/bin/attn daemon

[Install]
WantedBy=default.target
```

Start it:

```sh
systemctl --user daemon-reload
systemctl --user enable --now attn.service
```

Set its listen address through that machine's CLI. Use the address of the
interface the home can reach. For example, replace `OUTPOST_IP` and `PORT`:

```sh
attn settings set outpost_listen OUTPOST_IP:PORT
attn settings list --all
```

There is no default address or port. Tailscale is one way to reach the outpost;
the bridge also works over other TCP networks.

The outpost checks its user's `~/.ssh/authorized_keys` on every authentication
attempt. Add the public key that your home's ssh-agent holds. Lines with SSH
options, such as `command=`, `from=` or `restrict`, are skipped and logged with
their line number. Use an unrestricted key for the bridge.

On the home:

```sh
ssh-add -l
attn settings set outposts_enabled true
```

The home uses the `SSH_AUTH_SOCK` inherited by its daemon. A missing socket, an
unreachable agent, an empty agent and a rejected key each produce a different
error. Load a key with `ssh-add` if the agent is empty.

The daemon protocol accepts `add_outpost` with `name` and `address`,
`remove_outpost` with `id`, and `repair_outpost` with `id`. Connected app clients
receive `outposts_updated`, the full list with state, pins, last observed build
and last error. The Settings UI for these commands is a separate slice.

For a non-production daemon, the repository helper can drive the same protocol:

```sh
ATTN_WS_URL=ws://HOME_IP:WS_PORT/ws go run ./scripts/wsctl outposts \
  --add sandbox --address OUTPOST_IP:PORT --until connected
ATTN_WS_URL=ws://HOME_IP:WS_PORT/ws go run ./scripts/wsctl outposts --watch
```

Run the helper with that daemon's instance or data-directory environment so it
reads the right client token. Its WebSocket address is separate from the
outpost's bridge address.

## Pairing and recovery

On first contact, the home pins the outpost's SSH host key and daemon id. The
outpost records the home in its enrollment record. There is no fingerprint
prompt. Keep each daemon's data directory when updating its binary so its
identity and pins survive.

A changed key or daemon id is refused. The error names the pinned identity and
the received key or id. Use `repair_outpost` on the home to accept the replacement
outpost. Reconnecting never changes a pin. Adding a second address for an already pinned
outpost is refused with the existing row's name and address; remove one of the
rows.

An outpost accepts one home. To release it, run this on the outpost:

```sh
attn enrollment status
attn enrollment leave
```

A different build gets no bridge, and the error names both builds. Update the
binary by hand; a failed connection never installs, restarts or kills anything.

Each daemon sends a heartbeat every 500 ms. After 3 seconds without a frame the
bridge is stalled; after 10 seconds it is lost and closes. A frame before loss
restores the same connection. After loss, the home dials immediately, then
retries failed connections every 2 seconds. Refusals retry every 10 seconds.
Re-pairing or changing the switch starts a new attempt immediately.

The outpost exposes read-only settings:

| Key | Value |
| --- | --- |
| `outpost_state` | `off`, `listening`, `bridged`, `stalled`, `refused` or `error` |
| `outpost_home` | The recorded home's daemon id, or empty when unpaired |
| `outpost_last_error` | The last error, kept until a successful connection |

After losing a connection, the outpost returns to `listening` and keeps its
error. A new authenticated connection from its recorded home replaces the old
connection immediately. On the home, a failed bridge keeps its `lost` or
`refused` state during background retries. Errors survive `stalled`, re-pairing
and switch changes, and clear on `connected`.

Remove an outpost with `remove_outpost`. Its row and pins are deleted and stay
deleted across a home restart. Turning `outposts_enabled` off stops all home
dialers but keeps the rows for the next time you enable it. To stop the outpost
listener:

```sh
attn settings set outpost_listen ''
```

## Verify two local daemons

From a checkout with the native build dependencies prepared:

```sh
make build OUTPUT=/tmp/attn-bridge-proof
go build -o /tmp/attn-bridge-wsctl ./scripts/wsctl
python3 scripts/verify-outposts.py \
  --attn /tmp/attn-bridge-proof --wsctl /tmp/attn-bridge-wsctl
```

The script creates isolated data directories and SSH keys, starts two daemons,
uses the settings CLI and `wsctl`, freezes its outpost, and records
`connected → stalled → lost → connected`. It stops the processes it started and
removes its temporary state on exit. It does not read production data or SSH
keys.
