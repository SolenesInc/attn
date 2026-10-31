# Build the lever

Apply to any work that is not trivial: edits, migrations, analyses, checks, and work fanned out to other agents.

For work that is not trivial, write the tool that does the work, such as a script, a codemod, or a generator, instead of working by hand. Scripting the check itself is how you [prove it works](prove-it-works.md). A reviewer can read and rerun a tool, but can check hand-done work only by redoing it.

Do the first unit by hand to learn the recipe. Then build the tool and prove it on that unit. Keep the tool the smallest script that does the job, never a framework. Keep it where the reviewer can rerun it: link it from the change or attach it to the seed, and commit it when the work outlives the session.

A tool that handles every unit in one pass beats delegating agents to apply the recipe by hand. When you do fan work out, put the recipe, the check for the recipe, and what not to touch in one place that the agents do not edit, such as a section of the plot body or a file attached to the plot. Name that place in every child's body as required reading before the child starts, because a delegate reads only its own seed unless told otherwise.
