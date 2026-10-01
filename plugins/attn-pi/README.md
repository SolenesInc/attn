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

## Resume availability

Resume and Reopen check that the saved Pi conversation still exists. A missing
file produces a reason naming the conversation and storage directory. Starting
fresh remains an explicit action in the session ledger.

The driver advertises `resume_availability` and answers
`driver.resume_available({cwd, resume_session_id})` with `{available, reason?}`.
This read-only check matches the session header ID in Pi's cwd directory. It
honors `PI_CODING_AGENT_DIR`, `PI_CODING_AGENT_SESSION_DIR`, and project/global
`sessionDir` settings. The driver checks again before preparing a resume launch.

Plugins without this optional capability keep their existing resume behavior.
The updated Pi plugin requires a daemon that recognizes `resume_availability`.
