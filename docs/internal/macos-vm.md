# macOS VM

`scripts/vm` (built on `macvm` from shell-config) runs this checkout's Mac app
in a headless macOS VM with no host folders, Keychain, tokens, or signing
identity.

## When to use the VM vs the host

- **Use the VM** for every Mac app window you open, drive through automation,
  screenshot, or record (PR clips, guide footage, Attn Lens checks). An agent
  on a Linux outpost reaches it too: the home is the Mac, so open a shell there
  with `attn terminal open --host home` and run `scripts/vm` from a worktree
  on the Mac.
- **Stay on the host** for:
  - CLI, daemon and wire checks (no window opened);
  - speed, memory, CPU and frame-presentation budgets (VM numbers are not
    representative and Metal writes `present_ms: null` on every frame);
  - feel checks and anything needing the signed bundle or real attn;
  - smooth high-fps recordings (VM recordings cap around ~22 fps light / ~11
    fps heavy output);
  - `drive-attn` recipes and `createScenarioRunner` scenarios (parked,
    focus-free host launch until seed `s-6ktd3v` lands).

## Commands and hygiene

```sh
scripts/vm up
scripts/vm drive wait '{"target":{"key":"sidebar"}}'
scripts/vm record 20 /tmp/clip.mp4
scripts/vm shot /tmp/still.png
scripts/vm down
```

- Run `scripts/vm down` when done (including after abandoned runs). Never run
  `macvm stop` on a shared VM unless you own all holders (`macvm ls` shows
  holders; up to 4 agents share each of macOS's 2 allowed VMs).
- Each agent gets its own instance (`vm-<worktree>-<hash>`); pass
  `--instance NAME` to pick another, or `scripts/vm adopt --instance NAME` to
  claim an unowned legacy instance.
- For exclusive guest access (real focus, system input, or Keychain isolation),
  use `macvm up --alone`.
- Publish clips from the host with `./scripts/pr-evidence.sh publish`. Do not
  run `pr-evidence.sh record` inside the VM (`screencapture -v` writes an empty
  video there).
- See [Native app automation](native-app.md#automation) for `drive` actions.

## Guest quirks

- Display is 1920×1200 @ 2× (3840×2400 px). Boot takes seconds on a quiet Mac
  and up to 15 minutes under load.
- The guest plays no sound: `afplay` hangs, and an app's AVPlayer never becomes
  ready to play. Check audio playback on the host.
- Right after boot, captures can fail with `could not create image from display`;
  retry.
- **Frozen guest display:** the guest window server can stop presenting new
  frames while the app keeps drawing. Trust `drive find`/`wait` over a still;
  if a still looks stale, run
  `macvm exec open -a Applications/attn-<instance>.app` and capture again.
- `scripts/vm` fails if the launchd reaper stopped an idle VM
  (`~/.local/state/macvm/stops.log`); rerun `scripts/vm up`.
