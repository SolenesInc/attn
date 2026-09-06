# attn-pi plugin guide

**Read pi's own docs before changing this plugin.** The set for the pinned
version is installed at `node_modules/@earendil-works/pi-coding-agent/docs/`
(`bun install` here if it is missing).

## Auto mode

`automode/` is attn's pi extension for an automated permission system: a
user-configured static pre-approved or denied list, plus a classifier for
everything reaching past it, denied conversationally rather than through
dialogs. To understand it, or modify it, read
[docs/automode.md](docs/automode.md).

## Reporting pull requests

The suite watches pi's `tool_result` and reports every `gh pr create` URL to
attn, so a pi session records the pull requests it opens with no hook and no
prompt instruction. `src/pullrequest.ts` is the twin of
`internal/hooks/pullrequest.go` and the two run the same corpus
(`internal/hooks/testdata/pull-request-extraction.json`): change one regex and
you change both.

The driver declares the `pull_request_reporting` capability, which is what stops
attn adding the `attn pr record` block to a pi session's launch guidance.
