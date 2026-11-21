# Code navigation

In a review attn shows, the user can go to a definition, list the uses of a name and show its type. A language server on the machine that holds the review's files answers. attn starts the server when a shown review needs it and stops it once no shown review does. When a language has no working server, the review's key line says so, and the user's key starts an agent to set one up. This reference is for that agent, and for anyone setting up a server by hand.

## How attn launches a server

For each language attn uses a definition: the server's command, the file extensions it serves, the files that mark a workspace folder, the settings it receives at start, and an optional prepare step. Built-in definitions cover go (`gopls`), typescript (`typescript-language-server --stdio`), rust (`rust-analyzer`) and python (`pyright-langserver --stdio`). attn also recognizes java, kotlin, scala, c, cpp, csharp, ruby, php, swift, zig and elixir files but has no definition for them. A review of one shows the language as `no server`, and setup defines one. A definition set for a repository's origin replaces the built-in one for that language, and outposts with a clone of the repository use it too.

attn looks for the command's program in this machine's code navigation folder, then on `PATH`. `attn nav status`, run inside the repository, prints the folder, the definition of each language, where each program was found or why it was not, and the servers running with their state.

    attn nav status [--repository <dir>] [--json]

The server definitions for the repository in `--repository`, where each program was found or why it was not, and the language servers running on this machine with their memory and, for one still starting, when its start began.

## Set up a server

1. Find how this machine and this repository already run a server for the language. Developers usually have one: installed by their editor or a version manager outside `PATH`, or provided by the repository's own tooling. Search the home folder for the program, and read the repository's setup scripts and documentation for how its developers get one. Reuse what works here before you install anything.
2. Install a server only when this machine has none. Install it for this user with the language's usual installer, such as `go install golang.org/x/tools/gopls@latest` or `npm install -g typescript-language-server typescript`, never with `sudo`.
3. Link the program when it is not on `PATH`: `attn nav link <absolute-path-to-program>`. `--as <name>` links it under the name the definition's command uses.
4. Set a definition with `attn nav set <language> '<json>'` when the built-in one does not fit, or the language has none. `attn nav set` below describes the fields.
5. Prove it: run `attn nav check <path>:<line>` from the repository, on a line of a file in the review that uses a name defined elsewhere. Setup is done when it resolves a definition. The review updates its key line on its own.

    attn nav link <program> [--as <name>] [--json]

Links the server's program at `<program>` into this machine's code navigation folder, which attn searches before PATH. Use it for a server your editor installed outside PATH, such as Neovim's mason folder. `--as` links it under the name the definition's command uses.

    attn nav set <language> <definition> [--repository <dir>] [--json]

Sets how to launch the server for `<language>` in the repositories with this repository's origin. Outposts with a clone use it too. `<definition>` is JSON, such as {"command": ["jdtls"], "extensions": [".java"], "roots": ["MODULE.bazel"], "settings": {}, "prepare": {"command": ["tools/ide/setup_classpath.py"], "inputs": ["**/BUILD.bazel", "MODULE.bazel"]}}. command and extensions are required. roots are files that mark a workspace folder. settings go to the server at start. prepare runs from the workspace folder before the server starts, and again before a start once a file its inputs match has changed.

    attn nav check <path>:<line> [--repository <dir>] [--json]

Asks for the definition of each name on the line `<path>:<line>`, in order, and answers with the first that resolves. Starts the server when it is not running. Setup is done when this resolves a real definition. Called as a tool, a server still starting after 20 s answers `starting` with the time so far and keeps starting: call again, and a running server answers at once. On the CLI the command waits for the answer.

    attn nav check internal/daemon/review.go:42

## When the repository generates what the server needs

Some repositories work only after a step produces the server's configuration, such as a classpath generated from build files. Find the script the repository's developers run for their editor, and put it in the definition's `prepare`, with the files whose change makes it run again as `inputs`. attn runs it from the workspace folder before the server starts, and again before a start once an input has changed. Set the server's own options in `settings`.

## When a server failed

A failed server's reason is in the key line and in `attn nav status`, which shows the end of the server's error output. Fix that cause, such as a missing dependency of the server, a wrong argument or a missing prepare step, before you install another server. `attn nav check` starts the server again.

## Remove a setup

`attn nav unlink <name>` removes a link. `attn nav rm <language>` removes a set definition, so the built-in one applies again.

    attn nav unlink <name> [--json]

Removes the link `<name>` from this machine's code navigation folder.

    attn nav rm <language> [--repository <dir>] [--json]

Removes the definition set for `<language>`, so the built-in one applies again.
