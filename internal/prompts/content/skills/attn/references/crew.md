# Crew

Each profile has a Chief and can have other permanent crew members. Names
resolve in your profile. Read `attn crew --help` for command flags.

New homes live at `<notebook>/crew/<name>/`, with a numeric suffix if the
folder is taken. The registry keeps the absolute home path. Charters and
letters are Notebook files, so the user can edit and annotate them in attn.

The Chief works from the Notebook root. Other members work from their own
home, or the directory saved with `attn crew set <member> --cwd <dir>`.

`attn crew rename <member> <name>` moves a Notebook home to match the name.
For a Notebook home, put the member to sleep first with
`attn crew sleep <member>` and wait for their session to close.

Legacy members can be renamed while awake; their homes under the attn data
directory stay in place. To move one and every file in it into the profile's current
Notebook, put the member to sleep, then run `attn crew move-home <member>`.
The home and Notebook must be on the same volume; a move across volumes is
refused and leaves the original home intact.

Changing `notebook.root` repoints the Notebook and the Chief's next wake or
handoff. Clearing, reloading or recovering an existing conversation keeps
its working folder. Existing homes keep their absolute paths. Help the user
move those files when they ask.
