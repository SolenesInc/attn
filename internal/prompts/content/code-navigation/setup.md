Set up code navigation for {{language}} in {{repository}}, so the user's review {{document}} can go to a definition, list uses and show a type.

{{failure}}

Before you start, read `attn skill --reference code-navigation`. It says where attn looks for a language server and how to tell it how to launch one.

1. Find how this machine and this repository already run a {{language}} language server, and reuse it. Install one only when nothing here does.
2. Make attn find it: link its program with `attn nav link`, and set a definition with `attn nav set` when the built-in one does not fit this repository.
3. From {{repository}}, run `attn nav check {{file}}:<line>` on a line that uses a name defined elsewhere. You are done when it resolves a definition. The user's open review gains navigation without being reopened.

Finish with what you found, what you installed, linked or set, and the output of `attn nav check`. If no server works, say what stopped you and what the user can do about it.
