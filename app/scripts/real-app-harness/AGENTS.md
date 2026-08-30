# Real-app harness

Read [profiles.md](../../../docs/profiles.md) before installation or verification.
Run commands from the repository root.

## Running scenarios

- Scenarios share one display; run serially. Batch with
  `pnpm --dir app run real-app:serial-matrix`.
- Profile: `ATTN_HARNESS_PROFILE` overrides `ATTN_PROFILE`, which defaults to `dev`.
  Production needs `ATTN_HARNESS_PROFILE=`, `--run-against-prod`, and explicit approval.
- Install the current checkout; source fingerprint mismatches fail.
- Remote target: `attn-remote@orb`; provision with
  `pnpm --dir app run real-app:provision-remote`.

## Writing scenarios

- Exercise actual app actions/order; update scenarios when product flows change.
- Use `mockAgent.mjs`, `writeMockAgentFixture`, and `configureMockAgent`
  unless testing a provider integration; state why a real provider is needed.
- `launchFreshAppAndConnect` pins cheap models/low effort and restores settings.
  Never pin `fable`. `ATTN_HARNESS_LAUNCH_MODEL_<AGENT>=inherit` needs explicit request.
  Stronger scenario-specific models need an explanatory comment.
- Crew fixtures use synthetic names and `claude-haiku-4-5` unless stronger reasoning is required.
- Resolve pane ids from app/daemon state. Assert empty workspaces are removed.
  Shortcuts use registry ids.
- Keep OS-specific install paths, launch, observation, and quit behavior in
  `platform.mjs`. Use automation-manifest or spawned PIDs; verify manifest PIDs
  still run the installed executable before signalling them.

## Reading results

- Read the last `ATTN_VERDICT ` stdout line; hand-rolled `main()` scenarios omit it.
- Inspect captured pane text and native screenshots before diagnosing failures.
- Dark/locked screens block input. Check `pmset -g log | rg "Display is turned"`.

## Recordings

Record the installed verification profile; watch for private data before publishing
to the public evidence repository:

```bash
./scripts/pr-evidence.sh record --profile <name> --seconds 20 --out clip.mp4
./scripts/pr-evidence.sh publish clip.mp4
```

`publish` uploads MP4/GIF and prints PR Markdown. Re-record private content;
keep clips around 20 seconds and heed the 10MB GIF warning.
`ATTN_HARNESS_RECORD=1` writes `recording-NN.mp4` segments to scenario artifacts.
Install/update the recorder with `make install-window-recorder`; its stable
bundle preserves macOS Screen Recording permission.
