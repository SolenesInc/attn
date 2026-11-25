A crashed app server is replaced in the same terminal. When the old server's exit was handled late, attn removed the replacement by terminal id, and every session of that server was left without one. The exit's cleanup now removes only the runtime that exited. This was the flake in `TestAnAppServerFeedRecordsEachEntryOnceAcrossDaemonAndServerRestarts`.

The tests force the late exit with two pause points, on the in-process PTY and on the worker and shared-host backends through a second crash and a daemon restart.
