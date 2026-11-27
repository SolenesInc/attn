# Remote home

Moving the home is experimental. The home is the machine whose daemon owns your
sessions, the Garden, the queue and the settings. A move gives that role to an
outpost that is connected to the home now, and makes the old home an outpost of
the new one. Turn on `remote_home_enabled` at the home first
(**Settings → Experimental**, or `attn settings set remote_home_enabled true`).

The machine you move to must already run Attn as an outpost of this home. See
[Using outposts](using-outposts.md) to add one.

## What a move keeps

- Every session and terminal keeps its process and its screen. Agents keep
  running through the move.
- The Garden, profiles, settings, automations and unsent analytics move to the
  new home.
- The new home keeps its own worktrees, machine settings and unsent analytics.
- Each machine keeps its daemon id, its agent key and its bridge host key.
- The new home uses its own copy of each Notebook folder. Each profile's
  `notebook.root` points at that copy, and a crew member whose home was inside a
  Notebook folder moves with it. No Notebook copy is renamed.
- Other outposts connect to the new home without leaving or re-pairing. A
  machine that dials the home, such as a Mac, is refused by the old home with
  the new home's name. Run `attn enrollment join` there with the new home's ssh
  destination. It stays enrolled and keeps its terminals.
- The Cloud Relay link moves, and a paired phone reads from the new home.

## Checks before a move

The home checks the move before it starts and refuses with each cause. Nothing
changes on either machine until every check passes.

| Check | Passes when |
| --- | --- |
| connected | The target is connected to the home now. |
| build | The target runs the home's build. |
| notebook | The target's Notebook is in sync. |
| ssh `<destination>` | The target reaches each ssh outpost of the home with its own ssh. |
| reach back | The new home has a way to reach the old one: the old home dials it, or `--via` names the old home's ssh destination. |
| gh | `gh auth status` succeeds on the target. |
| linger | On Linux, linger is on for the target's user (`loginctl enable-linger <user>`), so the home keeps running after you log out. |
| systemd | On Linux, the target has a systemd user session. |
| notebook copy | The target holds a copy of every Notebook folder the home's profiles use. |

The checks also list the Notebook paths that change. When the target is a Mac,
they say that the home is down while that Mac sleeps.

## Move from the app

1. Open **Settings → Machines** at the home.
2. Choose the machine's row and click **Make home**, or press M.
3. Read the checks and the paths that change. If a check fails, fix its cause
   and click **Check again**.
4. Click **Move home to** *machine*.

The row lists the move's six steps, and a band over the window counts them:
stop changes, copy, build the database, switch roles, start the home on the
new machine, and reconnect. Both daemons restart, and the app on the old home
then reaches the new home through its own daemon. The band then says **Home
moved to** *machine*.

If the new home doesn't start, the band says **Move failed:** *machine*'s home
didn't start, and offers **Roll back**. If the old home restarted while the
move committed, the band says **Move to** *machine* **didn't finish**, and
offers **Retry**, which finishes the move, and **Roll back**.

## Move from the CLI

Run the commands on the machine that is the home:

```sh
attn home move --check oma
attn home move oma
attn home move --via victor@laptop oma
```

- The machine is the outpost's name, which ignores case, or its id.
- `--check` only runs the checks.
- On Linux the old home becomes an outpost that the new home reaches over ssh.
  `--via` names the ssh destination by which the new home reaches it.
- `--json` prints the answer as JSON.

The command exits 1 when a check fails or the move fails.

On any machine, `attn home status` prints that machine's latest move and its
step. `attn home rollback` undoes it, as [When a move stops](#when-a-move-stops)
describes.

## What happens during a move

1. The home stops writing. It refuses every write to its database, and stops
   its jobs and its Cloud Relay link.
2. The home sends a snapshot of its database and its data files to the target,
   with the size and SHA-256 hash of each file. The target checks each one.
3. The target prepares the new home: it keeps its own worktrees and machine
   settings, and changes the paths that point into the old home's Notebook to
   its own copies.
4. The home asks the target to commit, then commits itself.
5. Both daemons restart in their new roles, and the old home connects to the new
   home.

Each machine records the move's step in `<data-dir>/home-move/<id>/move.json`,
and applies it the next time its daemon starts.

| Step | Meaning |
| --- | --- |
| checked | Every check passed. Nothing changed. |
| refused | A check failed. Nothing changed. |
| checking | A move runs its checks, before it writes its record. Nothing changed yet. |
| staged | The target holds the snapshot. |
| committing | The home asked the target to commit. |
| committed | This machine committed. It applies the move at its next start. |
| applied | This machine started in its new role. |
| done | The old home connected to the new home. The move can no longer be rolled back. |
| aborted | The move stopped before it committed. Both machines are as they were. |
| rolling-back | This machine rolls the move back at its next start. |
| rolled-back | This machine is as it was before the move. |

## The new home on Linux

A move to a Linux machine installs a systemd user unit there and starts it:
`~/.config/systemd/user/attn.service`, or `attn-<instance>.service` for a named
instance. The unit runs `attn daemon` with `ATTN_DATA_DIR`, `ATTN_INSTANCE`, `PATH` and
`SSH_AUTH_SOCK` from your login environment, and restarts the daemon when it
stops. With linger on, the home starts at boot, without the app and without an
outpost that dials it, and its automations run. When the home moves away again,
attn disables the unit and removes it once this machine has connected to the
new home; a move that rolls back here removes it too.

## When a move stops

- If a daemon stops before the move commits, both machines stay as they were.
- If a daemon stops while the move commits, run `attn home move` with the same
  machine again, or click **Retry** on the band. It finishes the move.
- If the new home does not start, roll the move back on the old home. The app
  there shows **Can't reach** *machine* with **Roll back**, or run
  `attn home rollback`. The old home restarts as the home, and the other
  machine rolls back its side when the two connect. Until that machine is an
  outpost again, the old home runs no background jobs and no phone link, so two
  machines never run the home at once, and the band says **Rolled back. Waiting
  for** *machine* **to rejoin as an outpost**. If that machine is gone for good,
  click **Stop waiting** on the band, or run `attn home rollback` again, to run
  the home without word from it. If that
  machine did run as the home meanwhile, what it recorded as the home stays in
  its `home-move/<id>/rolled-back` folder.

Once the old home has connected to the new home, the move is done and
`attn home rollback` refuses. On the new home, `attn home rollback` always
refuses. To bring the home back, move it again.

## Move the home back

Run the move on the machine that is the home now, and name the old home:

```sh
attn home move laptop
```

In the app on the old home, open **Settings → Machines**, choose the **This
Mac** row and click **Make home**. The button that starts the move then reads
**Move home to this Mac**.
