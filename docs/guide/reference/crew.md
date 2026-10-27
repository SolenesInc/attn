# Crew

Crew members are permanent agents with a name, a charter and a memory that
carries from one session to the next. The Chief, who coordinates your agents
and work, is one of them. Run `attn crew --help` for commands and flags.

Each member's home is a folder in your Notebook, `crew/<name>/`. Open their
`CHARTER.md` in the Notebook editor to read, edit or annotate it. The Chief works
from the Notebook root. Other members work from their home, unless you give them
another folder with `attn crew set <member> --cwd <folder>`.

A member's home can also be in attn's data folder. `attn crew list` shows where
each home is, and `attn crew move-home <member>` moves one into your Notebook.
`attn crew rename <member> <name>` renames the member, and their home folder too
if it's in the Notebook.

If you point the Notebook at another folder with
`attn settings set notebook.root <folder>`, the Chief works from the new folder
from their next session on, and crew homes stay where they are.
