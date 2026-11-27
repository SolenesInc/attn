# Asks

## Asking the user

When you need a decision only the user can make, open an ask in attn instead of stopping to ask in this conversation or in your harness's own question prompt. The user answers every agent's asks from one place in attn, without opening this terminal. Ask this way whether or not you can keep working meanwhile.

The user reads the ask cold, without this conversation. In a few short sentences, say what the decision affects, what you know, why you recommend a choice, what waits on the answer, and what you will do if it stays unanswered. Ask only what you cannot settle yourself, a few questions at a time. To ask permission for a risky action, name the exact action and its target in the question, such as the command, branch or environment. The permission you get covers only the action the question names, and silence is not permission.

An ask renders markdown in the app and on the phone, including Mermaid diagrams, tables, code blocks, diffs, links and local images. Show what the decision turns on instead of only describing it in prose, using the smallest visuals that make the difference clear at a glance. One ask can show more than one:

- Flow, structure or states, such as options that differ in how components connect, what calls what in what order, or which states something passes through: draw a compact `mermaid` code block (`flowchart`, `sequenceDiagram` or `stateDiagram-v2`) for each flow, not an SVG file. A table of their trade-offs goes beside the diagram, not in place of it.
- UI layout, styling or a before-and-after look: show local PNG, JPEG, GIF, WebP, BMP or TIFF screenshots with `![alt](path)`, not HTML `<img>` tags, remote URLs or `data:` URLs.
- Options compared across two or more criteria: use a small markdown table.
- Concrete code, schema or config changes: use a short fenced code block or `diff`.
- A choice already clear in two sentences: skip the visual.

Put visuals in the ask's body or in a question's context lines. The subject, a question's first line, its choices and their `— why` notes take inline markdown only: links, bold, italics and `code`. Keep diagrams, images and tables out of them.

Link a file with `[name](path)`. A relative path resolves where the ask's images do, and an absolute or `~` path is on your own machine. To link a file on another machine, write its machine path the way ssh does, `<machine>:<path>`: `[plan](studio:~/notes/plan.md)`, where `home` names the home. attn refuses a machine it does not know, and names the ones it does.

Put a command the user may want to run, such as one whose output the answer rests on, in a fenced `sh` block in the ask's body, not in a question's context lines. One block can hold a short sequence of commands, one per line. When it must run on another machine or in another folder, write its machine path, `<machine>:<folder>`, after the language: ```` ```sh studio:~/shop ````. A block without one runs where you work. The user can open the block beside the question, in a terminal there, typed but not run.

After you ask, carry on with work that does not depend on the answer. When none is left, end your turn. Do not wait or poll. Your session stays open, so the user can find you, talk to you and run commands in your terminal: while any of your asks is open, attn refuses a standby that would close it, and a close of your own session that would leave the answer with no agent. When the user answers, attn types the answer, approvals included, as their message into the conversation of whoever tends the ask's seed by then, or into this one for an ask about no seed.

## Asking

- `attn ask [--file <path>] [--subject <text>] [--seed <id>] [--re <id>] [--json]`: open an ask in the user's queue; it returns at once
- `attn ask list [--json]`: your open asks and those about seeds you tend, then the open asks of the agents you oversee
- `attn ask show <id> [--json]`: one ask, with who asked, its seed, its age and text
- `attn ask amend <id> [--file <path>] [--json]`: add to an open ask when the facts change; the user keeps its place and their answer so far
- `attn ask close <id> [-m "<why>"] [--json]`: withdraw an ask you no longer need answered
