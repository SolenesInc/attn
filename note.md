Agents' ids in a terminal are now live: attn underlines the short id (`ag-…`), the whole id, or its first 8 hex digits. Rest the pointer on one to see that agent's card, or Cmd-click it to open the agent. A closed agent's card offers Resume, its ledger row and its seed. Cmd-click on a seed id opens the seed as a tile beside the agent.

Seed and agent cards now share one host and timing: both open after 0.16 s of rest and close after 0.24 s. Only ids the app or daemon knows are marked, so a commit hash stays plain text.
