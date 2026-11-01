# Converge on the outcome

Apply to a planned rewrite or migration with explicit phases.

In a planned rewrite or migration, aim at the verified end state. Do not aim at keeping every intermediate step smooth, because compatibility code written only to keep a midpoint working tends to outlive the migration. In the plan, declare where intermediate breakage is allowed under [Delivery](../references/delivery.md). Keep the checks for the areas you change running throughout the migration, and verify everything at the end. Leave the product working after each delivered part.
