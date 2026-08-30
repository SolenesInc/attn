//! The palette's argument grammar: `new`, `term`, `doc`, `close` with completion hints.

pub const KINDS: [&str; 4] = ["claude", "codex", "copilot", "pi"];

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Verb {
    New,
    Term,
    Doc,
    Close,
}

impl Verb {
    pub const ALL: [Verb; 4] = [Verb::New, Verb::Term, Verb::Doc, Verb::Close];

    pub fn word(self) -> &'static str {
        match self {
            Verb::New => "new",
            Verb::Term => "term",
            Verb::Doc => "doc",
            Verb::Close => "close",
        }
    }

    pub fn grammar(self) -> &'static str {
        match self {
            Verb::New => "new <kind> [dir] [on <desktop>] [as <name>]",
            Verb::Term => "term [dir] [on <desktop>]",
            Verb::Doc => "doc <file.md> [edit] [on <desktop>]",
            Verb::Close => "close",
        }
    }

    pub fn detail(self) -> &'static str {
        match self {
            Verb::New => "start an agent (claude, codex, copilot, pi)",
            Verb::Term => "open a terminal",
            Verb::Doc => "open a document tile",
            Verb::Close => "close the focused shell or document",
        }
    }

    pub fn parse(word: &str) -> Option<Verb> {
        Verb::ALL.into_iter().find(|verb| verb.word() == word)
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum PaletteCommand {
    New { kind: &'static str, cwd: Option<String>, desktop: Option<u8>, name: Option<String> },
    Term { cwd: Option<String>, desktop: Option<u8> },
    Doc { path: String, edit: bool, desktop: Option<u8> },
    Close,
}

/// What the token being typed can be; the UI turns each into concrete candidates.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Expect {
    Kind,
    Dir,
    Path,
    Desktop,
    Name,
    Keyword(&'static str),
}

#[derive(Debug)]
pub struct Analysis {
    pub verb: Option<Verb>,
    pub ready: Option<PaletteCommand>,
    pub expects: Vec<Expect>,
    /// The in-progress token and where it starts, so a completion knows what to replace.
    pub prefix: String,
    pub replace_from: usize,
}

pub fn analyze(query: &str) -> Analysis {
    let tokens = tokens(query);
    let open = !query.is_empty() && !query.ends_with(char::is_whitespace);
    let (prefix, replace_from, committed) = if open && !tokens.is_empty() {
        let (start, token) = *tokens.last().unwrap();
        (token.to_string(), start, &tokens[..tokens.len() - 1])
    } else {
        (String::new(), query.len(), &tokens[..])
    };
    let verb = match committed.first() {
        Some((_, word)) => Verb::parse(word),
        None => Verb::parse(&prefix),
    };
    let Some(verb) = verb else {
        return Analysis { verb: None, ready: None, expects: Vec::new(), prefix, replace_from };
    };
    let all: Vec<&str> = tokens.iter().map(|(_, token)| *token).collect();
    let ready = parse_full(verb, &all[1..]);
    let expects = if committed.is_empty() {
        Vec::new()
    } else {
        expects(verb, &committed[1..].iter().map(|(_, token)| *token).collect::<Vec<_>>())
    };
    Analysis { verb: Some(verb), ready, expects, prefix, replace_from }
}

fn tokens(query: &str) -> Vec<(usize, &str)> {
    let mut out = Vec::new();
    let mut start = None;
    for (index, ch) in query.char_indices() {
        if ch.is_whitespace() {
            if let Some(begin) = start.take() {
                out.push((begin, &query[begin..index]));
            }
        } else if start.is_none() {
            start = Some(index);
        }
    }
    if let Some(begin) = start {
        out.push((begin, &query[begin..]));
    }
    out
}

fn desktop(token: &str) -> Option<u8> {
    matches!(token, "1" | "2" | "3" | "4" | "5" | "6" | "7" | "8" | "9").then(|| token.parse().unwrap())
}

/// Strict parse over every token typed so far; None until the command is complete and clean.
fn parse_full(verb: Verb, args: &[&str]) -> Option<PaletteCommand> {
    match verb {
        Verb::Close => args.is_empty().then_some(PaletteCommand::Close),
        Verb::New => {
            let (&first, rest) = args.split_first()?;
            let kind = KINDS.into_iter().find(|kind| *kind == first)?;
            let (mut cwd, mut on, mut name) = (None, None, None);
            let mut rest = rest.iter();
            while let Some(&arg) = rest.next() {
                match arg {
                    "on" if on.is_none() => on = Some(desktop(rest.next()?)?),
                    "as" if name.is_none() => name = Some(rest.next()?.to_string()),
                    _ if cwd.is_none() && !matches!(arg, "on" | "as") => cwd = Some(arg.to_string()),
                    _ => return None,
                }
            }
            Some(PaletteCommand::New { kind, cwd, desktop: on, name })
        }
        Verb::Term => {
            let (mut cwd, mut on) = (None, None);
            let mut rest = args.iter();
            while let Some(&arg) = rest.next() {
                match arg {
                    "on" if on.is_none() => on = Some(desktop(rest.next()?)?),
                    _ if cwd.is_none() && arg != "on" => cwd = Some(arg.to_string()),
                    _ => return None,
                }
            }
            Some(PaletteCommand::Term { cwd, desktop: on })
        }
        Verb::Doc => {
            let (mut path, mut edit, mut on) = (None, false, None);
            let mut rest = args.iter();
            while let Some(&arg) = rest.next() {
                match arg {
                    "edit" if !edit && path.is_some() => edit = true,
                    "on" if on.is_none() && path.is_some() => on = Some(desktop(rest.next()?)?),
                    _ if path.is_none() => path = Some(arg.to_string()),
                    _ => return None,
                }
            }
            // A directory is never a document; a trailing slash keeps the command unready.
            let path = path.filter(|path: &String| !path.ends_with('/'))?;
            Some(PaletteCommand::Doc { path, edit, desktop: on })
        }
    }
}

/// What can follow the committed args, for the token being typed.
fn expects(verb: Verb, args: &[&str]) -> Vec<Expect> {
    match verb {
        Verb::Close => Vec::new(),
        Verb::New => {
            let Some((&first, rest)) = args.split_first() else {
                return vec![Expect::Kind];
            };
            if !KINDS.contains(&first) {
                return Vec::new();
            }
            let (mut cwd, mut on, mut name) = (false, false, false);
            let mut rest = rest.iter();
            while let Some(&arg) = rest.next() {
                match arg {
                    "on" if !on => match rest.next() {
                        Some(_) => on = true,
                        None => return vec![Expect::Desktop],
                    },
                    "as" if !name => match rest.next() {
                        Some(_) => name = true,
                        None => return vec![Expect::Name],
                    },
                    _ if !cwd => cwd = true,
                    _ => return Vec::new(),
                }
            }
            let mut out = Vec::new();
            if !cwd {
                out.push(Expect::Dir);
            }
            if !on {
                out.push(Expect::Keyword("on"));
            }
            if !name {
                out.push(Expect::Keyword("as"));
            }
            out
        }
        Verb::Term => {
            let (mut cwd, mut on) = (false, false);
            let mut rest = args.iter();
            while let Some(&arg) = rest.next() {
                match arg {
                    "on" if !on => match rest.next() {
                        Some(_) => on = true,
                        None => return vec![Expect::Desktop],
                    },
                    _ if !cwd => cwd = true,
                    _ => return Vec::new(),
                }
            }
            let mut out = Vec::new();
            if !cwd {
                out.push(Expect::Dir);
            }
            if !on {
                out.push(Expect::Keyword("on"));
            }
            out
        }
        Verb::Doc => {
            let (mut path, mut edit, mut on) = (false, false, false);
            let mut rest = args.iter();
            while let Some(&arg) = rest.next() {
                match arg {
                    "edit" if !edit && path => edit = true,
                    "on" if !on && path => match rest.next() {
                        Some(_) => on = true,
                        None => return vec![Expect::Desktop],
                    },
                    _ if !path => path = true,
                    _ => return Vec::new(),
                }
            }
            if !path {
                return vec![Expect::Path];
            }
            let mut out = Vec::new();
            if !edit {
                out.push(Expect::Keyword("edit"));
            }
            if !on {
                out.push(Expect::Keyword("on"));
            }
            out
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ready(query: &str) -> Option<PaletteCommand> {
        analyze(query).ready
    }

    #[test]
    fn full_commands_parse() {
        assert_eq!(ready("term"), Some(PaletteCommand::Term { cwd: None, desktop: None }));
        assert_eq!(
            ready("term ~/x on 3"),
            Some(PaletteCommand::Term { cwd: Some("~/x".into()), desktop: Some(3) })
        );
        assert_eq!(
            ready("new codex ~/api on 7 as api/codex"),
            Some(PaletteCommand::New {
                kind: "codex",
                cwd: Some("~/api".into()),
                desktop: Some(7),
                name: Some("api/codex".into()),
            })
        );
        assert_eq!(
            ready("doc notes.md edit on 2"),
            Some(PaletteCommand::Doc { path: "notes.md".into(), edit: true, desktop: Some(2) })
        );
        assert_eq!(ready("close"), Some(PaletteCommand::Close));
    }

    #[test]
    fn incomplete_or_wrong_commands_stay_unready() {
        for query in ["new", "new x", "new codex on", "new codex on 0", "doc", "doc docs/", "doc a.md b.md", "term on", "close now"] {
            assert_eq!(ready(query), None, "{query:?}");
        }
    }

    #[test]
    fn the_typed_token_still_counts_for_readiness() {
        assert_eq!(
            ready("new codex on 3"),
            Some(PaletteCommand::New { kind: "codex", cwd: None, desktop: Some(3), name: None })
        );
    }

    #[test]
    fn expects_follow_the_grammar_position() {
        assert_eq!(analyze("new ").expects, vec![Expect::Kind]);
        assert_eq!(analyze("new co").expects, vec![Expect::Kind]);
        assert_eq!(
            analyze("new codex ").expects,
            vec![Expect::Dir, Expect::Keyword("on"), Expect::Keyword("as")]
        );
        assert_eq!(analyze("new codex on ").expects, vec![Expect::Desktop]);
        assert_eq!(analyze("new codex as ").expects, vec![Expect::Name]);
        assert_eq!(analyze("term ").expects, vec![Expect::Dir, Expect::Keyword("on")]);
        assert_eq!(analyze("doc ").expects, vec![Expect::Path]);
        assert_eq!(
            analyze("doc a.md ").expects,
            vec![Expect::Keyword("edit"), Expect::Keyword("on")]
        );
    }

    #[test]
    fn the_prefix_names_what_a_completion_replaces() {
        let analysis = analyze("new co");
        assert_eq!((analysis.prefix.as_str(), analysis.replace_from), ("co", 4));
        let analysis = analyze("new codex ");
        assert_eq!((analysis.prefix.as_str(), analysis.replace_from), ("", 10));
        assert_eq!(analyze("gar").verb, None);
        assert_eq!(analyze("term").verb, Some(Verb::Term));
    }
}
