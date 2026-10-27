# Converge on the outcome

Apply to a planned rewrite or migration with explicit phases.

In a planned rewrite or migration, aim at the verified end state, not at keeping every intermediate step smooth; compatibility code written only to keep a midpoint working tends to outlive the migration. Declare in the plan where intermediate breakage is allowed, such as commits inside one branch, keep the checks for the areas being changed running throughout, and verify everything at the end. Each part that lands on its own, such as a merged pull request, still leaves the product working.
