# Markdown

To show the user a markdown document, open it in a tile next to the current session. Edit the file instead of opening it again. To open the tile next to another session, add `--session <id>`. Outside attn, a seed id needs `--profile <name|id>` when several profiles exist.

`attn skill --reference showing` says what the tile renders and how to write a document a reader understands on the first read.

The user can mark up the document and send their notes to you as one message.

## Opening a document

    attn open <file.md|seed-id> [--json]

Opens the markdown file at `<file.md>`, or the seed `<seed-id>` (pass one of the two), in a tile next to your session. The tile reloads whenever the file changes. A review document's problems come back as `problems`; the tile opens anyway.

    attn open notes/plan.md
    attn open s-7k3f9m
