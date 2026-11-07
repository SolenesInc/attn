## What attn can do

attn runs you beside the user's other agents. Check this list before you work around a problem, tell the user something can't be done, or guess at a command.

- `attn <command> --help` shows a command's syntax. `attn --help` lists every command. Never run `attn` with no command, because it starts a session, and never leave out a command's arguments to see what it does. If attn reports an unknown command, check `attn --version` and `which -a attn`, then try `"$ATTN_WRAPPER_PATH"`.
- `attn skill --reference <name>` prints the rules for a capability. Read it before you first use that capability.
- `attn guide [<topic>]` is the user guide. Use it to answer the user's questions about attn.

| When you need to | Run | Reference |
|---|---|---|
| Track work, read a plan, record progress | `attn seed` | `garden` |
| Start another agent on work you may hand off | `attn delegate` | `delegation` |
| Change the models and harnesses each role uses | `attn delegate roles` | `delegation-roles` |
| Report on work delegated to you | `attn seed note`, `attn seed check` | `delegated-agent` |
| Deliver through the repository's pull requests or trunk | `attn pr` | `delivery` |
| See what other agents are doing, or message one. `chief` names the Chief | `attn agent list`, `attn agent peek`, `attn agent msg`, `attn agent inbox` | `converse-and-observe` |
| Close or reload an agent, wrap up, or close yourself | `attn agent close`, `attn agent reload` | `wrap-up` |
| Read past sessions, or rename or snooze a session | `attn session` | `converse-and-observe` |
| Open a shell, read a terminal, move between desktops | `attn terminal` | `terminal` |
| Show the user a markdown document | `attn open` | `markdown` |
| Write anything longer than a few sentences for a reader | | `showing` |
| Use the in-app browser | `attn browser` | `browser` |
| Point at something in the app | `attn callout` | `callouts` |
| Read or add to the Notebook, the user's journal and knowledge | `attn journal append`, your file tools | `notebook` |
| Work with permanent agents that keep a charter and letters | `attn crew` | `charter`, guide `crew` |
| Run an agent on a schedule or an event | `attn automation` | guide `automations` |
| Read or change settings | `attn settings list`, `attn settings get`, `attn settings set` | |
