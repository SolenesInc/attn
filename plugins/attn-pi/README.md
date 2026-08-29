# attn-pi

attn driver plugin for [pi](https://github.com/earendil-works/pi): pi launches,
resumes, and lives as an attn session. Pure driver with dumb state — the daemon
owns the PTY and session records; this plugin only decides what argv to run.

## Auto mode outside attn

`automode.js` is pi's permission system on its own: a safety envelope around
the working directory, and a classifier for everything past it.

```
pi -e /path/to/attn-pi/automode.js --auto
```

`/auto` toggles it, `--auto` / `--no-auto` set it at launch, and the status
line says which it is. Without a flag it starts off unless a config file turns
it on:

```jsonc
// ~/.pi/agent/attn-automode.json  (or $PI_CODING_AGENT_DIR/attn-automode.json)
{
  "enabled_default": true,
  "environment": ["this machine has no production credentials"],
  "allow": ["git push origin*"],
  "hard_deny": ["rm -rf /*"],
  "models": ["opencode-go/glm-5.3", "opencode-go/qwen3.8-max"]
}
```

Every field is optional. A file without `enabled_default` configures auto mode
without turning it on.

Both passes walk one ordered list: the first entry judges, and the rest are
tried only when the one before it cannot be reached — a thrown request, a
provider error, an endpoint that is down. Each entry gets one immediate retry
first. A model that *answers* ends the walk whatever it said, so a deny is
never re-asked of the next model. When no model answers, the call is blocked
and the block says it was an outage rather than a judgment.

One list rather than one per pass because both passes send the same system
prompt byte for byte: the second pass lands on a prefix the first already
warmed, and a different model there would pay for the whole rulebook twice.
The older per-layer spellings (`classifier_models`, `escalation_models`, and
their singular forms) still load, folded into one chain with the classifier's
first.
