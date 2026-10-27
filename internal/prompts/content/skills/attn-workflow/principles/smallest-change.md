# Smallest change

Apply when sizing a diff, or when tempted to add a layer, an option, or an abstraction.

Make the smallest change that delivers the full requirement, and prefer deletion and plain code over boilerplate that looks elegant. When one choice shows up in several places, make it once, behind one source of truth, and pass the result along. Share structure, not every line: three similar statements still beat a premature abstraction, and explicit beats clever. A short diff that grows a parallel mechanism is not small; [refactor first](refactor-first.md) instead.
