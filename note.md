Agents in Claude Code's Code Mode stop guessing `attn_record({code: …})`. The server instructions now say both script tools take one argument, `script`, and every prompt that hands an agent a call writes it out whole, such as `attn_record({script: 'agent_inbox()'})`.

On 12 Sonnet bench runs, main sent a wrong parameter in 4; with this change it sent none in 34, and cost per task did not rise. The bench's `usage` column now counts these client-side refusals too.
