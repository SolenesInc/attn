A CLI that can't read a successful reply now says the command ran, so an agent doesn't retry it.

Before: after a daemon changed a result's shape (fb61846e4 slimmed search hits), an older CLI exited 1 with `receive response: missing field body`. That happened even after `seed plant` had planted, and the CLI printed no stale-protocol warning, so a retry planted a duplicate (bug 030eba7).

Now: any error reading a reply the daemon answered ok becomes a stale-protocol check, then:

```
attn: warning: this CLI speaks protocol … but the running attn app speaks …
seed plant: attn ran the command, but this CLI could not read its answer (…). Anything the command changed stays changed, so check before you run it again.
```

Trade-off: CLIs that are already installed still fail as before. The user chose this over keeping reply shapes compatible in the daemon.
