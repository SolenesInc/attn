# Crew

Crew members are permanent agents with a name, a charter and a memory that
carries from one session to the next. The Chief is one of them. Run
`attn crew --help` for commands and flags.

New homes live in your profile's Notebook at `crew/<name>/`, with a numeric
suffix when the folder is taken. Open `CHARTER.md` in the Notebook editor to
read, edit or annotate it. The Chief works from the Notebook root; other
members work from their home unless you set `--cwd`.

`attn crew rename <member> <name>` moves a Notebook home to match the name.
Put a member with a Notebook home to sleep first with
`attn crew sleep <member>`, then wait for their session to close.
Legacy members can be renamed while awake; their homes stay in place.
To move one into the current Notebook, put the member to sleep and run `attn crew move-home <member>`.
Changing `notebook.root` later repoints the Notebook; recorded crew homes
stay at their absolute paths.
