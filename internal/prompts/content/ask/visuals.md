An ask renders markdown in the app and on the phone, including Mermaid diagrams, tables, code blocks, diffs, links and local images. Show what the decision turns on instead of only describing it in prose, using the smallest visuals that make the difference clear at a glance. One ask can show more than one:

- Flow, structure or states, such as options that differ in how components connect, what calls what in what order, or which states something passes through: draw a compact `mermaid` code block (`flowchart`, `sequenceDiagram` or `stateDiagram-v2`) for each flow, not an SVG file. A table of their trade-offs goes beside the diagram, not in place of it.
- UI layout, styling or a before-and-after look: show local PNG, JPEG, GIF, WebP, BMP or TIFF screenshots with `![alt](path)`, not HTML `<img>` tags, remote URLs or `data:` URLs.
- Options compared across two or more criteria: use a small markdown table.
- Concrete code, schema or config changes: use a short fenced code block or `diff`.
- A choice already clear in two sentences: skip the visual.

Put visuals in the ask's body or in a question's context lines. The subject, a question's first line, its choices and their `— why` notes take inline markdown only: links, bold, italics and `code`. Keep diagrams, images and tables out of them.

Link a file with `[name](path)`. A relative path resolves where the ask's images do, and an absolute or `~` path is on your own machine. To link a file on another machine, write its machine path the way ssh does, `<machine>:<path>`: `[plan](studio:~/notes/plan.md)`, where `home` names the home. attn refuses a machine it does not know, and names the ones it does.

Put a command the user may want to run, such as one whose output the answer rests on, in a fenced `sh` block. When it must run on another machine or in another folder, write that place's machine path after the language: ```` ```sh studio:~/shop ````. A block without one runs where you work. attn opens the command beside the question in a terminal there, typed but not run.
