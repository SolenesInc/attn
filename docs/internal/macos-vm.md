# macOS VM

Agents open, drive, screenshot and record Mac app windows in a macOS VM on the
user's Mac. The VM runs headless, so nothing appears on the user's screen and no
input reaches their keyboard or pointer. Nothing of the host goes inside: no
shared folders, Keychain, tokens or signing identity.

`scripts/vm` runs this checkout's attn in such a VM. It sits on `macvm`, from
shell-config, which starts, shares and stops the VMs for any repository.

## When to use it

Use the VM for every Mac app window you open, drive, screenshot or record:

- driving an app through its automation bridge
- PR evidence clips and screenshots
- guide and landing-page footage
- Attn Lens checks

CLI, daemon and wire checks open no window and need no VM. drive-attn recipes
and `createScenarioRunner` scenarios do not run in the VM yet (seed `s-6ktd3v`);
until they do, keep their parked, focus-free launch on the host.

Stay on the host for:

- speed, memory and CPU budgets, which VM numbers do not represent
- how something feels to use
- anything that needs the signed bundle or the user's real attn
- recordings that need a smooth, high frame rate. Window recordings in the VM
  reach about 22 fps with light terminal output and about 11 fps with heavy
  output (seed `s-cj6223`). If a recording needs more, record on the host.

## Run attn in the VM

```bash
scripts/vm up
scripts/vm drive create_session '{"label":"vm","cwd":"/Users/admin","agent":"shell"}'
scripts/vm record 20 /tmp/clip.mp4
scripts/vm shot /tmp/still.png
scripts/vm down
```

`up` installs `app/node_modules` if it is missing, builds the app with `DEMO=1`
unless the staged bundle already matches the source, gets a VM, installs the
app, opens it without taking focus and waits until it is ready. Run it again
after changing code: it rebuilds and reinstalls only when the source changed.

`drive` sends one automation-bridge action to the app; `ui-automation-cli.mjs`
in `app/scripts/real-app-harness` lists them. `record` adds a frame only when the
window repaints, so a clip of a still window has few frames. Publish clips from
the host with `./scripts/pr-evidence.sh publish`; its `record` command does not
work in the VM, where `screencapture -v` writes an empty video.

The instance is `vm-<worktree name>`. `--instance NAME`, before or right after
the command, selects another. `down` quits and cleans the instance and releases
the VM, so if one agent runs two instances, bring both down together.

## Sharing

macOS allows two running macOS VMs per Mac, so agents share them, up to four
agents per VM. `scripts/vm up` joins the fullest running VM with room and starts
one only when none has room. Each instance keeps its data apart, and clips show
only their own window. One recording runs at a time per VM; a second one waits
for the first.

When both VMs are full, `up` prints who holds them and waits up to 30 minutes.

For a guest with one app in front, as scenarios that need real focus or system
input do, or a guest nobody else can read, as Lens's Keychain token needs, use
`macvm up --alone` directly.

`macvm ls` shows every VM, who holds it, whether each holder is working, and
when the VM will stop. `macvm stop NAME` stops one now, whoever holds it, and
names the holders it removes.

## How a VM stops

`scripts/vm down` means "I am done". Run it when you are done, including after
an abandoned run, and do not stop the VM yourself: another agent may be using it.

A reaper, run by launchd each minute, stops VMs nobody needs. A VM stays up
while any attn session holding it is working, or while the guest has work in
progress. Once that ends, it stops 60 minutes later if a holder is still around
and 15 minutes later if nobody holds it, which leaves it warm for the next job. A
killed agent's VM stops the same way. A later `scripts/vm` or `macvm` call from
an agent whose VM stopped fails and says when and why;
`~/.local/state/macvm/stops.log` keeps the record. Run `scripts/vm up` again to
get a new one.

## Limits

- The guest display is 1920x1200 points at 2x, so a full-screen still is
  3840x2400 pixels.
- A VM boots in seconds when the Mac is quiet and in up to 15 minutes when other
  agents load it.
- Right after a boot the guest is busy for several minutes. A capture can time
  out then with `could not create image from display`; run it again.
