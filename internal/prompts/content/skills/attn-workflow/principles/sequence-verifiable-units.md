# Sequence verifiable units

Apply to multi-step work, and to how you order commits and delivery steps.

Rebase onto a current trunk first, so every check measures against the real baseline. Order multi-step work as small units that each end in a check, and verify each unit before you start the next, so you catch a break at the unit that caused it. When a [lever](build-the-lever.md) does the edits, the check per unit is nearly free. Run the check anyway.

Put the proof before the change, so you watch your own assumption fail and then hold instead of trusting it. Examples are the failing test before the fix, the subtraction before the reshape, and the baseline before the treatment. Order delivery units the same way, as [Delivery](../references/delivery.md) describes. Order the commits so a reviewer can replay them, such as the failing test and then the fix, or the scaffold before the feature.
