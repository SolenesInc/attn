# Fix root causes

Apply when debugging, and before adding a guard for a state that broke something.

Fix the fault, not the place where it surfaced. Reproduce the failure first. Then ask why at each step until the answer is a wrong decision in the code rather than a consequence of an earlier decision. When you are stuck, instrument instead of guessing.

Ask whether the state that triggered the symptom is legitimate. If the system may be in that state, the code that broke on it is the cause. If the state should never exist, a guard where the code broke leaves the bug in place, so find what produced the state.

Guards, retries, swallowed errors, and clamped values are where bugs usually sit. When a failure appears only after a restart, suspect persisted state first. Fix every instance of the pattern. When finding the deciding fact took a long investigation, propose the cheap, always-on signal that would have shown the fact the first time anyone looked.

When two fixes that share an assumption have failed the same check, [attack the premise](attack-the-premise.md). The process is in [Debugging](../references/debugging.md).
