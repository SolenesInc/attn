# attn-pi

attn driver plugin for [pi](https://github.com/earendil-works/pi): pi launches,
resumes, and lives as an attn session. Pure driver with dumb state — the daemon
owns the PTY and session records; this plugin only decides what argv to run.

Pi sessions also load [execution security](docs/security.md): an OS sandbox
for built-in tools and credential filtering, independent of auto mode.
Use `/security` for interactive settings, path lists and effective permissions.

Every bash command walks an approval path before it runs: prefix rules, then a
sandbox, then one reviewer — your approval card, or the Guardian model when
`/auto on` is set. [docs/automode.md](docs/automode.md) has the whole flow.

## Outside attn

`security.js` is the OS sandbox and credential filtering on their own:

```
pi -e /path/to/attn-pi/security.js
```

Without attn there is no daemon config, so there is no approval policy, no
network proxy and no reviewer. `/security` and its settings file are the whole
story there.
