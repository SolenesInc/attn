# Shell title recordings

Recorded on macOS with fish 4.9.2, zsh 5.9, bash 5.3 and /bin/sh (bash 3.2),
plus nvim 0.12.5. The seed s-h2828d has the original title spike. Each JSON keeps
the original PTY read chunks as base64 and foreground observations between them.
The expected title timeline contains changes only, starting at the launch folder.

Each recording changes to /tmp, runs a command without a title, runs nvim with
`set title` and `+'redraw'`, and runs a command that sets OSC 0. Commands wait
for input so the recording can observe their foreground group before they exit.
Fish uses its standard interactive initialization and either an empty fish_title
or its default-style title. Zsh uses attn's startup marks, with and without the
spike's oh-my-zsh termsupport hooks. Bash uses attn's marks, with and without a
PS1 title, and a PROMPT_COMMAND title. Sh has no marks and relies on foreground observations.

The title order is a program's title, then its command line while a command runs;
at the prompt it is the shell's title, then the folder's basename. A program's
title ends with its command. Empty titles fall through. A user name overrides
this automatic title in the app. Automatic titles contain at most 256 Unicode
characters (the spike's longest title was 84; these recordings also include
longer temporary paths).

For shells without command marks, a foreground observation decides whether a
title belongs to a program or the shell. A command that has already exited when
its title is read cannot be distinguished from the shell setting a prompt title.
