# Code navigation

In a review attn shows, the user can go to a definition, list the uses of a name and show its type. A language server on the machine that holds the review's files answers. attn starts the server when a shown review needs it and stops it once no shown review does. When a language has no working server, the review's key line says so, and the user's key starts an agent to set one up. This reference is for that agent, and for anyone setting up a server by hand.

## How attn launches a server

For each language attn uses a definition: the server's command, the file extensions it serves, the files that mark a workspace folder, the settings it receives at start, and an optional prepare step. Built-in definitions cover go (`gopls`), typescript (`typescript-language-server --stdio`), rust (`rust-analyzer`) and python (`pyright-langserver --stdio`). A definition set for a repository's origin replaces the built-in one for that language, and outposts with a clone of the repository use it too.

attn looks for the command's program in this machine's code navigation folder, then on `PATH`. `attn nav status`, run inside the repository, prints the folder, the definition of each language, where each program was found or why it was not, and the servers running with their state.

## Set up a server

1. Find how this machine and this repository already run a server for the language. Developers usually have one: installed by their editor or a version manager outside `PATH`, or provided by the repository's own tooling. Search the home folder for the program, and read the repository's setup scripts and documentation for how its developers get one. Reuse what works here before you install anything.
2. Install a server only when this machine has none. Install it for this user with the language's usual installer, such as `go install golang.org/x/tools/gopls@latest` or `npm install -g typescript-language-server typescript`, never with `sudo`.
3. Link the program when it is not on `PATH`: `attn nav link <absolute-path-to-program>`. `--as <name>` links it under the name the definition's command uses.
4. Set a definition with `attn nav set <language> '<json>'` when the built-in one does not fit, or the language has none. `attn nav --help` describes the fields.
5. Prove it: run `attn nav check <path>:<line>` from the repository, on a line of a file in the review that uses a name defined elsewhere. Setup is done when it resolves a definition. The review updates its key line on its own.

## When the repository generates what the server needs

Some repositories work only after a step produces the server's configuration, such as a classpath generated from build files. Find the script the repository's developers run for their editor, and put it in the definition's `prepare`, with the files whose change makes it run again as `inputs`. attn runs it from the workspace folder before the server starts, and again before a start once an input has changed. Set the server's own options in `settings`.

## When a server failed

A failed server's reason is in the key line and in `attn nav status`, which shows the end of the server's error output. Fix that cause, such as a missing dependency of the server, a wrong argument or a missing prepare step, before you install another server. `attn nav check` starts the server again.

## Remove a setup

`attn nav unlink <name>` removes a link. `attn nav rm <language>` removes a set definition, so the built-in one applies again.
