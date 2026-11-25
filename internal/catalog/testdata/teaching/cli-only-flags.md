Read the seeds you tend (`attn presence --seed-ids` lists them). Run `attn terminal run <terminal> "make test" --wait --timeout 600` and read the result, or:

```sh
attn terminal run <terminal> "make lint" --wait
```

`run --wait` reads the next block, and `--timeout <seconds>` bounds it.
