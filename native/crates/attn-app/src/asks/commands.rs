//! The commands an ask offers: each of its shell-language code blocks, with the machine and
//! folder it runs in.

use attn_protocol::Ask;
use pulldown_cmark::{CodeBlockKind, Event, Options, Parser, Tag, TagEnd};

const SHELLS: [&str; 5] = ["sh", "bash", "zsh", "fish", "shell"];

/// A command and where it runs: `host` is the machine, absent for the home's own, and `cwd`
/// the folder.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Command {
    pub(crate) text: String,
    pub(crate) host: Option<String>,
    pub(crate) cwd: String,
}

/// A stretch of a part's markdown: prose, or one command with the fenced block that holds it.
pub(crate) enum Piece<'s> {
    Prose(&'s str),
    Command(Command, &'s str),
}

/// `source`, a part of `ask`, cut at the commands written at its top level. A command whose
/// block names an address runs where the ask's reference to that address resolved; any other
/// runs at the ask's default place.
pub(crate) fn pieces<'s>(ask: &Ask, source: &'s str) -> Vec<Piece<'s>> {
    let mut pieces = Vec::new();
    let mut from = 0;
    let mut depth = 0usize;
    let mut open: Option<(usize, Option<String>, String)> = None;
    for (event, range) in Parser::new_ext(source, Options::empty()).into_offset_iter() {
        match event {
            Event::Start(Tag::CodeBlock(CodeBlockKind::Fenced(info))) if depth == 0 => {
                let mut fields = info.split_whitespace();
                if fields
                    .next()
                    .is_some_and(|language| SHELLS.contains(&language))
                {
                    open = Some((range.start, fields.next().map(str::to_owned), String::new()));
                }
                depth += 1;
            }
            Event::Text(text) if open.is_some() => {
                if let Some((_, _, body)) = open.as_mut() {
                    body.push_str(&text);
                }
            }
            Event::End(TagEnd::CodeBlock) if depth == 1 => {
                depth -= 1;
                let Some((start, address, body)) = open.take() else {
                    continue;
                };
                if start > from {
                    pieces.push(Piece::Prose(&source[from..start]));
                }
                let command = Command {
                    text: body.trim_end_matches('\n').to_owned(),
                    ..place(ask, address.as_deref())
                };
                pieces.push(Piece::Command(command, &source[start..range.end]));
                from = range.end;
            }
            Event::Start(_) => depth += 1,
            Event::End(_) => depth = depth.saturating_sub(1),
            _ => {}
        }
    }
    if from < source.len() {
        pieces.push(Piece::Prose(&source[from..]));
    }
    pieces
}

fn place(ask: &Ask, address: Option<&str>) -> Command {
    let addressed = address.and_then(|address| {
        ask.references
            .iter()
            .flatten()
            .find(|r| r.kind == "command" && r.text == address)
    });
    match addressed {
        Some(reference) => Command {
            text: String::new(),
            host: reference.host.clone().filter(|h| !h.is_empty()),
            cwd: reference.path.clone(),
        },
        None => Command {
            text: String::new(),
            host: ask.host.clone().filter(|h| !h.is_empty()),
            cwd: ask.cwd.clone().unwrap_or_else(|| ask.base_dir.clone()),
        },
    }
}

/// Every command `ask` offers, in reading order.
pub(crate) fn of(ask: &Ask) -> Vec<Command> {
    ask.parts
        .iter()
        .filter_map(|part| part.markdown.as_deref())
        .flat_map(|source| {
            pieces(ask, source)
                .into_iter()
                .filter_map(|piece| match piece {
                    Piece::Command(command, _) => Some(command),
                    Piece::Prose(_) => None,
                })
        })
        .collect()
}
