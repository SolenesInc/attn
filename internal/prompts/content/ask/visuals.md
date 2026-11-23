An ask renders markdown in the app and on the phone, including Mermaid diagrams, tables, code blocks, diffs, links and local images. Show what the decision turns on instead of only describing it in prose, using the smallest visual that makes the difference clear at a glance:

- Flow, state transitions or component structure: draw a compact `mermaid` code block (`flowchart`, `sequenceDiagram` or `stateDiagram-v2`), not an SVG file.
- UI layout, styling or a before-and-after look: show local PNG, JPEG, GIF, WebP, BMP or TIFF screenshots with `![alt](path)`, not HTML `<img>` tags, remote URLs or `data:` URLs.
- Options compared across two or more criteria: use a small markdown table.
- Concrete code, schema or config changes: use a short fenced code block or `diff`.
- A choice already clear in two sentences: skip the visual.

Put visuals in the ask's body or in a question's context lines. The subject, a question's first line, its choices and their `— why` notes take inline markdown only: links, bold, italics and `code`. Keep diagrams, images and tables out of them.
