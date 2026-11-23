Two flaky tests from main 65554b3df are fixed at their causes.

- A reload right after an app server crashed could report success against the dead connection. The daemon handed out its held server connection until the status watcher noticed the hang-up. It now sees that the peer has hung up (a non-blocking socket peek) before it hands the connection out, and connects again, so the reload gets the new server's real answer.
- The transcript `--follow` stack test started its second conversation before the pipe's reader had gone. It now waits for the reader to exit first.
