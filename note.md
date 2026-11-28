A closed agent in the middle of a delegation chain now keeps its place while agents below it still work. Before, closing b in a → b → c → d left c → d alone (the card said "Started by Earlier session"), and a's card lost its delegates. Now b stays as a quiet row (hollow dot, "closed 2h", its role's mark), so the chain still reads a → b → c → d. Return on b resumes it. `attn session list --tree` keeps b too, as a `closed` row.

The daemon walks the closed dispatchers above each live session and standby tender and sends them as `closed_dispatchers`. It also re-sends the delegates below an agent when that agent closes or comes back, which it did not do before. A closed agent with nothing live below it still leaves the chain.

![The chain from the top, with the closed planner in place](https://land-feed.caketroll.cloud/m/SolenesInc/attn/3346254dc6b40c9144f398b0fc0f0d83d42c891667a177b407b20c6fef461a18.png)
