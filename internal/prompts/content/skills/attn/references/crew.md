# Crew

Crew members are permanent agents with a charter, letters (the note each session leaves for the next) and a home folder. The Chief is one of them. Read `attn crew --help` for commands and flags.

A member's home is `<notebook>/crew/<lowercase name>/`, with a numeric suffix if that folder is taken. It holds their `CHARTER.md` and a `handoffs/` folder of letters. These are Notebook files, so the user can edit and annotate them in attn.

With Crew memory on, their `memory/` folder holds the memory they keep across sessions.

Some older members have their home under the attn data directory instead. `attn crew list` shows each member's home. `attn crew move-home <member>` moves an older home into the Notebook.

`attn crew rename <member> <name>` renames a Notebook home folder to match the new name. An older home stays where it is.

The Chief works from the Notebook root. Other members work from their home, or from the directory set with `attn crew set <member> --cwd <dir>`.

After `attn settings set notebook.root <folder>`, the Chief's next session starts in the new folder. A running session keeps its folder, even if the user clears, reloads or recovers that conversation. Changing `notebook.root` doesn't move any member's home. No command moves a Notebook home to a new root, and attn can't find a home moved by hand.
