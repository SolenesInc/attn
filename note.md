# Stranded asks: the daemon marks them and can send their answer to the Chief

An ask is *stranded* when no agent would take the user's answer: the asking session closed, or nobody tends the ask's seed and no standby resumes a tender for an answer. Every wire ask, and the phone's ask, now carries `stranded`. `asks_updated` re-projects on the facts that can change it: session open and close, a write to a seed with asks, a standby change, and `garden.tender_resume`.

`ask_answer`, `ask_follow_up` and the phone's answer take `to_chief`. The reply goes to the profile's Chief under a new `ask/to-chief` header that names the asker, the seed and why. It is written together with the reply and the seed's note, and the note keeps the plain answer. `to_chief` is refused while an agent would take the answer (the refusal names that agent), and in a profile with no Chief.

Idle cost: a daemon with three open asks, one of them stranded, sent 0 `asks_updated` in 120 s, both before and after this change. The native sheet's and the phone's Send to Chief buttons come in sibling slices of plot s-02bfn9.

The guide and the standby-request prompt now say that an agent's self close is refused when the answer would reach no agent.
