# Sequence verifiable units

Apply to multi-step work, and to how you order commits and pull requests.

Rebase onto a current trunk first, so every check measures against the real baseline. Order multi-step work as small units that each end in a check, and verify each before starting the next, so a break is caught at the unit that caused it. When a [lever](build-the-lever.md) does the edits, the check per unit is nearly free; run it anyway. Put the proof before the change, so you watch your own assumption fail and then hold instead of trusting it: the failing test before the fix, the subtraction before the reshape, the baseline before the treatment. Order delivery the same way: each commit or pull request lands on its own, and the sequence reads as an argument a reviewer can replay, such as the failing test and then the fix, or the scaffold before the feature.
