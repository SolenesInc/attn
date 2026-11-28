Check whether it can still finish. Look at what it waits for, such as the CI run, the service or the file, not at the task's own output. Then do one of these:

- It is still on its way: say what you checked, what you saw, and the latest it should end. Keep waiting.
- It cannot finish, or it already has: stop the task and carry on, or start a wait that ends on its own.
- You need the user: run `attn turn open` and say what you need.