# Worktree dependency bootstrap hook

Example attn plugin built with `@victorarias/attn-plugin`.

It demonstrates:

- the SDK-backed daemon handshake
- typed surface registration through `client.handle(...)`
- a `worktree.after_create` lifecycle hook

The example keeps the behavior genuinely useful:

- let attn create the requested worktree normally
- inspect the created worktree for dependency lockfiles
- run `pnpm install` when `pnpm-lock.yaml` is present
- otherwise run `yarn install` when `yarn.lock` is present
- otherwise run `npm install` when `package-lock.json` is present
- surface package-manager failures back to attn through the hook RPC call

This example consumes `@victorarias/attn-plugin` through a `file:` dependency
back into this repository, so it runs from a checkout. To install it with
`attn plugin install --path`, copy the folder and change that dependency to a
published version of `@victorarias/attn-plugin`, as
`examples/plugins/sample-harness` does.
