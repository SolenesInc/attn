# Design

Use this reference to shape, compare, or review an interface: any boundary one piece of code offers another, such as a function, type, module, package, command line, file format, protocol, or web API. The checks are the same inside a codebase and across organizations; what changes is the cost of a mistake, which grows with the number of callers and how hard they are to change.

## Start from the caller

Write the calling code before the interface. Pick two or three realistic call sites: the common case, a demanding case, and a call that should fail. Judge a design by how those call sites read, not by how convenient the implementation is. When comparing designs, write the same call sites against each and read them side by side. (Bloch: "Code the use-cases against your API before you implement it"; Muratori: write the usage code first.)

## Checks

Apply each check to the call sites. Skip a check that does not fit the interface rather than forcing it. The sources in parentheses give the fuller argument.

- **Deep, not shallow.** Count the parameters and concepts a caller must handle against what the implementation hides; the first should be much smaller. A layer whose signature is as wide as the thing it wraps adds a concept without removing one. Pull complexity down into the implementation instead of pushing it onto every caller. (Ousterhout, *A Philosophy of Software Design*)
- **Hide the decisions likely to change.** Each module owns a design decision that callers cannot observe, so the decision can change without them. (Parnas, "On the Criteria To Be Used in Decomposing Systems into Modules")
- **Common case short, rare case possible.** The typical call states only what is essential and takes defaults for the rest; full control stays reachable through the same interface. (Bloch: "easy to do simple things; possible to do complex things")
- **Hard to misuse.** Prefer designs in which a wrong call cannot be written: types that carry the invariant, distinct types for values that must not mix, required arguments instead of rules about call order. When misuse is only detectable at runtime, fail at the first point it is detectable. (Bloch: "Fail fast… Compile-time is best"; parse, don't validate)
- **Useful from the start.** A new value works without a setup ritual. An initialization step that callers must remember is an error waiting to happen. (Go proverb: "Make the zero value useful")
- **Define errors out of existence.** Redefine an operation so the edge case is ordinary: removing something absent succeeds, a range past the end is clamped. The errors that remain name what failed, the value or limit involved, and what the caller can do; whoever fixes the call, a person or an agent, often sees only the message. (Ousterhout; Bloch, *Effective Java*: "Include failure-capture information in detail messages")
- **Small pieces that compose.** Narrow interfaces that combine beat a broad one that anticipates every combination. Small interfaces and uniform data at the boundary let unrelated parts work together. (The Unix philosophy; Go's `io.Reader` and `io.Writer`)
- **Keep concerns apart.** Can a caller change when, where, or by whom something runs without changing what it does? If not, the interface ties those choices together, and callers must accept all of them to get one. (After Hickey, "Simple Made Easy")
- **When in doubt, leave it out.** Every element is a promise. You can add to an interface later, but you cannot take anything away. (Bloch)
- **Names that explain.** Read each call site aloud. If it needs a comment to say what it does, rename, and if no clear name comes, move the boundary. The same concept keeps the same name everywhere, and opposite operations come in named pairs. (Bloch: "Strive for intelligibility, consistency, and symmetry")
- **Consistent with its neighbors.** Follow the conventions of the surrounding code and ecosystem, even where a local variation would be slightly better; callers carry what they learned from one interface to the next.
- **A small vocabulary with a validator, where uses are many.** When people or agents will write many uses in a bounded domain, a small, constrained vocabulary (typed builders, an internal DSL, a schema) backed by a deterministic checker whose errors point to the fix lets an author write a candidate, check it, and repair it unaided. It costs a language to design and maintain, and pays only while the domain is truly bounded and the vocabulary stays small. (Joshi, "DSLs Enable Reliable Use of LLMs", martinfowler.com, 2026)
- **Evolves without breaking callers.** Name the likeliest next requirement and show what changes at the call sites. Do not build for it; check only that it would be additive. If existing callers would have to change, reconsider the boundary. With enough callers, every observable behavior is depended on, whatever the contract says, so changing it is a break. (Hyrum's law, *Software Engineering at Google*; Torvalds: "we don't break userspace")

## Weigh the result

The checks pull against each other: a deep module against small composable ones, a constrained vocabulary against a general one. Match the effort to the interface's reach. A private helper with one caller needs a good name and little else; a boundary that many callers or other teams depend on deserves the full pass. Prefer the design whose call sites read clearly with the fewest concepts, and say which checks decided it.
