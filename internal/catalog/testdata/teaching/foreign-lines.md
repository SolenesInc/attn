Close the seed with `attn seed harvest <id> -m "done"`, take one over with `attn seed tend <id> --force`, then publish:

```sh
git push --force origin main
gh pr view --json state
grep 'attn seed show' notes.md
```

```sh
attn seed show <id>   prints the seed
                      git push --force origin main
                          gh pr view --json state
```

    attn seed show <id>   prints the seed; --section prints one part
    git log --grep seed

    attn seed show <id>   prints the seed
                          git push --force origin main
                              git push --force origin next
