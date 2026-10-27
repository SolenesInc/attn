# Minimize reader load

Apply when shaping or reviewing code that is hard to trace.

Judge structure by the work a reader does to understand it, the layers to trace between a question and its answer, and the state to hold in mind. Collapse wrappers with one caller, adapters with one implementation, and layers that pass the same arguments through. Prefer return values to mutation, locals to shared state, and derived values to copies kept in sync. Readers have finite working memory, as agents have a finite [context window](guard-the-context-window.md). A layer earns its place only by removing more reader load elsewhere than it adds. The test: a new reader answers "where does this value come from?" and "what can change it?" in under 30 seconds.
