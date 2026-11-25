# A reference a later release ships

Close the seed with `attn seed harvest <id> -m "done"`, then publish:

```sh
git push --force origin main
gh pr view --json state
```

    attn seed show <id>   prints the seed; --section prints one part
    git log --grep seed

Run `attn terminal run <terminal> "make test" --wait --timeout 600` and read the result, or:

```sh
attn terminal run <terminal> "make lint" --wait
```

Report with `attn seed check report s-1 C2 -m "ok" --evidence a.txt --evidence b.txt`, or:

```sh
attn seed check report s-1 C3 -m "ok" --evidence c.txt --evidence d.txt
```

Give a payload with `attn automation run 3 --input-file input.json`, `attn delegate roles apply roles.json` or `attn seed plot -f -`.
