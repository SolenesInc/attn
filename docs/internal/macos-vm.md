# macOS VM

Agents open, drive, screenshot and record Mac app windows in a disposable macOS
VM on the user's Mac. The VM runs headless, so nothing appears on the user's
screen and no input reaches their keyboard or pointer. `macvm`, from
shell-config, starts and stops these VMs. Each job gets its own copy of a
prepared base image, with nothing of the host inside it: no shared folders,
Keychain, tokens or signing identity.

## When to use it

Use the VM for every Mac app window you open, drive, screenshot or record:

- driving an app through its automation bridge
- PR evidence clips and screenshots
- guide and landing-page footage
- Attn Lens checks

CLI, daemon and wire checks open no window and need no VM. drive-attn
recipes and `createScenarioRunner` scenarios do not run in the VM yet (seed
`s-6ktd3v`); until they do, keep their parked, focus-free launch on the host.

Stay on the host for:

- speed, memory and CPU budgets, which VM numbers do not represent
- how something feels to use
- anything that needs the signed bundle or the user's real attn
- recordings that need a smooth, high frame rate. Window recordings in the VM
  reach about 22 fps with light terminal output and about 11 fps with heavy
  output (seed `s-cj6223`). If a recording needs more, record on the host.

## Stop the VM when you are done

Run `macvm down <name>` as soon as you are done with the VM, including after an
abandoned run. A running VM holds 8 GB of the user's memory, and macOS allows
only two at a time: a third `macvm up` waits until one stops. `macvm ls` shows
every VM, its owner and its age.

## Run attn in the VM

1. Build the app on the host. The guest has no Go or Rust, and compiling there
   is slow. `DEMO=1` gives footage the plain "attn" title.

   ```bash
   make build-app INSTANCE=<name> DEMO=1
   ```

   The bundle lands in `app/src-tauri/target/staged/attn-<name>.app`. Unlike
   `make install`, `build-app` opens nothing on the host.

2. Start a VM and install the app. `install-app` signs it ad hoc and grants
   Screen Recording, Accessibility and input, so no permission dialog appears.

   ```bash
   macvm up <name>
   macvm install-app <name> app/src-tauri/target/staged/attn-<name>.app
   macvm exec <name> open Applications/attn-<name>.app
   ```

3. Drive the app with the automation bridge. Copy in the harness and its `ws`
   dependency, then point the harness at the installed bundle:

   ```bash
   macvm push <name> h/app/scripts app/scripts/real-app-harness
   macvm push <name> h/app/node_modules "$(readlink -f app/node_modules/ws)"
   macvm exec <name> env ATTN_HARNESS_INSTANCE=<name> \
     ATTN_HARNESS_BIN=/Users/admin/Applications/attn-<name>.app/Contents/MacOS/attn \
     node h/app/scripts/real-app-harness/ui-automation-cli.mjs ping
   ```

   Repeat `ping` until it prints `"frontendReady": true`, then send other
   actions the same way, such as `create_session`. Leave out `--wait-ready`:
   it compares the build with a source checkout, and the guest has none.

4. Record the window and copy the clip out. `macvm-record` records the app's
   largest window for the given seconds. It adds a frame only when the window
   repaints, so a clip of a still window has few frames.

   ```bash
   macvm exec <name> macvm-record attn-<name> 20 clip.mp4
   macvm pull <name> clip.mp4 <dir>
   ```

   Publish it from the host with `./scripts/pr-evidence.sh publish`. Its
   `record` command does not work in the VM: `screencapture -v` writes an empty
   video there.

5. Stop and delete the VM: `macvm down <name>`.

## Limits

- The guest display is 1920x1200 points at 2x, so a full-screen still is
  3840x2400 pixels.
- A VM boots in seconds when the Mac is quiet and in up to 15 minutes when
  other agents load it. `macvm up` fails after 30 minutes.
- Right after a boot the guest is busy for several minutes. A capture can time
  out then with `could not create image from display`; run it again.
