Read the seeds you tend (`presence()` (without the CLI-only `--seed-ids`) lists them). Run `terminal_run({terminal: "<terminal>", command: "make test"})` (without the CLI-only `--wait --timeout 600`) and read the result, or:

```text
terminal_run({terminal: "<terminal>", command: "make lint"}) (without the CLI-only --wait)
```

`run --wait` (CLI only) reads the next block, and `--timeout <seconds>` (CLI only) bounds it.
