# Check an outpost over ssh

Use a fresh named instance on the Mac and the same instance on a Linux outpost.
Keep production data and production outposts out of the run.

Build the Mac binary from this repository. Add installs the matching Linux
build on the outpost, so the instance must have no files there: the harness
refuses an existing install path, instance data, Notebook folder or scratch
folder. It removes the instances, the installed binary, and the remote scratch
and Notebook folders when finished.

Run the stack checks and record the host folders they must leave alone:

```sh
python3 scripts/verify-ssh-outposts/stack.py --out /tmp/bridge-stack-proof
```

Run an idle typing check through the destination you use with ssh:

```sh
node scripts/verify-ssh-outposts/transport.mjs \
  --mode idle --macBin /tmp/attn-mac --instance bridge-proof \
  --ssh ai-sandbox --scratch /tmp/bridge-proof \
  --out /tmp/bridge-idle-proof --notebook /tmp/bridge-idle-notebook \
  --samples 10001
python3 scripts/verify-ssh-outposts/analyze.py /tmp/bridge-idle-proof
```

The idle check first exits attn's master and verifies that the daemon and shell
keep their PIDs. It then samples typing echo and plain echoes through the same
master and a separate ssh connection in the same seconds. The sample interval comes from the route spike's
measurement method. The sample count above covers five minutes.

For a first sync, use `--mode bulk` and give `--notebook` a copy of a real
Notebook, or generate the route spike’s measured file-size distribution with
`python3 scripts/verify-ssh-outposts/gen-notebook.py --out /tmp/bridge-notebook`. The harness records the first sync and typing until it finishes or
reaches `--capMs`. Keep the source Notebook read-only.

`receipts.json` records actions and outcomes. `samples.json` holds every typing
sample and Notebook update. `control-echo.json` holds both control routes' echo
samples. `input-traces.json` holds the daemon's input-write durations. Per-call
R uses one duration per input batch; the echo-based multikey estimate remains
a diagnostic. `analyze.py` reports both, raw echo, same-seconds control echo,
unanswered keys, and sync throughput.

The driver PID is in `receipts.json`. SIGTERM stops sampling and cleans both
instances.

Compare an output controller with the preceding build in one sequence:

```sh
python3 scripts/verify-ssh-outposts/compare-window.py \
  --before-mac /tmp/attn-before-mac --after-mac /tmp/attn-after-mac \
  --instance bwin-proof --notebook /tmp/bridge-notebook \
  --out /tmp/bridge-window-comparison
```

The comparison starts with idle, then runs three first-sync pairs in alternating
order. Each run copies the source Notebook, lets Add install the matching Linux
build, and cleans its instances. `comparison.json` reports changes from idle and the
throughput ratio. Each run keeps its raw receipts and analysis.
