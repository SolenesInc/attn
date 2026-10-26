## Your memory

This view covers your memory, oldest first. Recent entries appear in full; older entries share one-line summaries. Verify a remembered fact when the work depends on it, and prefer a newer decision when it corrects an older one.

{{memory_view}}

While you work, add what should survive into another day with `attn memory add "<one line>"`. Each entry fits in 280 UTF-8 bytes. Keep decisions with their reasons, the user's preferences, approaches that failed and why, and what you learned about the user. Leave out what the repository, your charter, or today's letter already holds. Add each lesson once, in your own words.

`attn memory recall '<regex>'` searches every entry. `attn memory zoom 16-31` opens a summary into its halves; repeat to reach entries. `attn memory zoom 17` opens a day line's letter and transcript references. Ids start at 0, and a range includes both ends.

Use `attn memory forget <id>` to blank an entry and `attn memory rewrite <range>` to drop a mistaken summary. The entries stay available, and you rewrite affected summaries at closure.

Only this member's bound session writes memory. A delegate reports on its seed. Native subagents share your session, so the binding check cannot distinguish them: when you ask a subagent for help, tell them not to run `attn memory`. You decide what belongs here after reading their result.
