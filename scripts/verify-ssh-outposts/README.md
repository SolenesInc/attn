# Check an outpost over ssh

Use a fresh named instance on the Mac and the same instance on a Linux outpost.
Keep production data and production outposts out of the run.

Build matching Mac and Linux binaries. Put the Linux binary at
`~/.local/share/attn-outpost-instances/<instance>/attn` on the outpost before
running the check. The harness refuses existing instance data and Notebook
folders. It removes the instances, the installed binary, and the remote scratch
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
  --samples 10000
python3 scripts/verify-ssh-outposts/analyze.py /tmp/bridge-idle-proof
```

The idle check first exits attn's master and verifies that the daemon and shell
keep their PIDs. It then samples typing echo and a plain echo through the same
master in the same seconds. The sample interval comes from the route spike's
measurement method. The sample count above covers five minutes.

For a first sync, use `--mode bulk` and give `--notebook` a copy of a real
Notebook. The harness records the first sync and typing until it finishes or
reaches `--capMs`. Keep the source Notebook read-only.

`receipts.json` records actions and outcomes. `samples.json` holds every typing
sample and Notebook update. `control-echo.json` holds the same-master echo
samples. `analyze.py` reports raw echo, per-call R, same-seconds control echo,
unanswered keys, and sync throughput using the spike's estimator.
