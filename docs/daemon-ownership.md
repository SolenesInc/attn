# Daemon ownership

## Home daemon

A **home daemon** is a standalone daemon that owns its Garden, crew, and other
user-level shared state. Every fresh install starts as a home, and the user's
app connects to one. Enrollment determines ownership; homes use the same
binary as outposts.

Every piece of state has one owner daemon. Sessions belong to the daemon where
they run, including on outposts. The Garden and crew belong to a home so the
fleet shares one work tracker and roster. Other daemons act as clients of that
owner.

The optional, closed central server operated by Victor connects home daemons
to each other. This is federation. Each home represents its fleet; outposts
never connect to the server. See the
[home, Garden, and crew plan](plans/2026-08-10-home-garden-crew-arc.md).

## Outpost

An **outpost** is a daemon enrolled to a home. It owns its sessions and routes
Garden and crew requests home through the **uplink**, a generic intent channel.
It holds no local copy or cache of Garden state; both reads and writes pass home.

**Enrollment** records this mutual relationship. The outpost stores its home's
daemon id beside its own `daemon-id` file, and accepts home authority only from
a connection presenting that id. A home writes the record when syncing the
remote. Each daemon has exactly one home. `attn enrollment` shows the record;
`attn enrollment leave` makes an outpost a home again and is required before
another home enrolls it.

Until the uplink is built, outposts are **fenced**. Garden and crew requests
fail with an error naming the home and the plan for the missing uplink.
Sessions, PTYs, PR flows, and local tickets remain available.

The transport uses **hub** for the dialing side, **endpoint** for a stored SSH
target, and **remote** for the dialed machine. Home and outpost describe state
ownership over that transport.

A **parked endpoint** waits for the user to click Sync because its binary or
protocol differs from the client's: `binary_mismatch`, `version_mismatch`, or
`version_ahead`. The hub refuses commands even if the WebSocket remains open.
The refusal carries the endpoint's status message, keeping command errors and
the app banner consistent. This status is independent of parking a seed.

## Client token

The credential a client sends in `client_hello` to use the daemon protocol.
The daemon creates an owner-only `client-token` file in its profile data
directory on first startup and reuses it across restarts. This isolates
profiles on the loopback WebSocket port, where Unix-socket file permissions
cannot protect the connection.

The **browser host token** identifies an already-connected client as the trusted
Tauri main WebView. `ATTN_WS_AUTH_TOKEN` is an operator-set HTTP bearer for a
WebSocket port exposed beyond loopback. A connection that passes bearer
authentication is exempt from the client-token check.

`attn client-token` prints the token. A refused hello names the required file
and profile.
