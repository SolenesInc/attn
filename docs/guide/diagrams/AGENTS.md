# Guide diagrams

Each diagram has two sources, and both must always change together:

- `<name>.d2` draws the image that GitHub and the website show.
- `<name>.mmd` draws the text that `attn guide` prints.

After changing a diagram, render both and commit the sources with their
renders:

    d2 --layout elk <name>.d2 <name>.svg
    mermaid-ascii -f <name>.mmd > <name>.txt

Read the `.txt` after rendering. When it doesn't read well, simplify the
diagram; the page's prose carries the detail.

Pages show the image with `![alt](../diagrams/<name>.svg)`. Write alt text that
says what the diagram shows.

Install the tools with `brew install d2` and
`go install github.com/AlexanderGrooff/mermaid-ascii@latest`.
