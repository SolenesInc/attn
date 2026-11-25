Close the seed with `seed_harvest({seed: "<id>", reason: "done"})`, take one over with `seed_tend({seed: "<id>", force: true})`, then publish:

```text
git push --force origin main
gh pr view --json state
grep 'attn seed show' notes.md
```

```text
seed_show({seed: "<id>"})   prints the seed
                      git push --force origin main
                          gh pr view --json state
```

    seed_show({seed: "<id>"})   prints the seed; `section` prints one part
    git log --grep seed

    seed_show({seed: "<id>"})   prints the seed
                          git push --force origin main
                              git push --force origin next
Run `skill({command: "seed_<verb>"})` for a verb, and `attn <command> --help` elsewhere.
