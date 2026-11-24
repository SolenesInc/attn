Pinned the Sigma worker turn-opening rule (Q1 on `s-j1rzp3`): worker sessions follow standard attention queue rules so parent-mailed (`sigma.mail`) runs, person-started (`sigma.user`) runs, and approval waits all open a turn.

- Added `AppServer.Mail` in `internal/fakeagent` modeling Sigma's `sigma.mail` entries (`packages/sigma/src/agents.ts` at `e69ff71`) with numbered completions.
- Added wire tests proving settled-to-owed turn transitions across parent-mailed stops, person-started stops, and approval waits on both `sigma` and `sample-server`, and documented `sigma.mail` and worker attention-queue behavior.
