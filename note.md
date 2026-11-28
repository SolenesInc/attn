With `attention_supervisor` on, the supervisor now acts on a finished verdict. It first asks the agent, in its inbox, to close itself (or asks its live dispatcher). If no model request follows within 5 minutes, the tile shows "finished" and no turn opens. The user's settle then closes the agent as closed by the supervisor ("settled when finished"), and Reopen brings it back. The `delegate_finished` rule closes a stopped delegate whose stake is harvested or withered and whose dispatcher watches it. A model's verdict alone never closes an agent.

The supervisor is its own `who` actor, so the ledger, the Reopen card and analytics name it. The wait check now quotes the agent's whole last message, which the measured `LastMessageMaxChars` bounds, in place of an unmeasured 300-character tail.

![part1](https://land-feed.caketroll.cloud/m/SolenesInc/attn/be607e8de7e28305ad584564a0b96bd577f7dfd5dcb5843355e9afd85d2bfdef.mp4)

![part2](https://land-feed.caketroll.cloud/m/SolenesInc/attn/173c742f3517b71c7120538e91973f44d1640b19806ea42e47d735db49ebc6f9.mp4)
