Ask another agent to run `attn agent close --self`, send with `attn agent msg <seed-id>`, press `attn terminal send --keys Enter`, read `attn terminal read --last`, then take it back with `attn seed harvest --when-merged --clear`.

```sh
attn agent close --self
attn agent close --self -m "done"
```

A target machine takes `--host ai-sandbox --cwd ~/project`:

```sh
attn delegate --seed s-1 --role builder --cwd /repo
```

Settle it with `attn seed check uncheck|reject <id> <C#>`.
