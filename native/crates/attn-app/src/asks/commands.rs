//! The commands an ask offers: each of its shell-language code blocks, wherever it sits in the
//! markdown, with the machine and folder it runs in.

use attn_protocol::Ask;
use pulldown_cmark::{CodeBlockKind, Event, Options, Parser, Tag, TagEnd};

const SHELLS: [&str; 5] = ["sh", "bash", "zsh", "fish", "shell"];

/// Fences the reader draws as a diff, which it leaves out of its count of code blocks; it reads
/// a fence's first word, up to a space or a comma, in any case.
const DIFFS: [&str; 2] = ["diff", "patch"];

/// A command and where it runs: `host` is the machine, absent for the home's own, and `cwd`
/// the folder. `block` is its code block's place among its part's code blocks, as the reader
/// counts them for `CodeFrame::index`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Command {
    pub(crate) text: String,
    pub(crate) host: Option<String>,
    pub(crate) cwd: String,
    pub(crate) block: usize,
}

/// The commands `source`, a part of `ask`, offers, in reading order. A command whose block
/// names an address runs where the ask's reference to that address resolved; any other runs at
/// the ask's default place.
pub(crate) fn of_part(ask: &Ask, source: &str) -> Vec<Command> {
    let options = Options::ENABLE_STRIKETHROUGH
        | Options::ENABLE_TABLES
        | Options::ENABLE_TASKLISTS
        | Options::ENABLE_GFM
        | Options::ENABLE_YAML_STYLE_METADATA_BLOCKS
        | Options::ENABLE_FOOTNOTES;
    let mut commands = Vec::new();
    let mut blocks = 0;
    let mut open: Option<(usize, Option<String>, String)> = None;
    for event in Parser::new_ext(source, options) {
        match event {
            Event::Start(Tag::CodeBlock(kind)) => {
                let info = match &kind {
                    CodeBlockKind::Fenced(info) => info.as_ref(),
                    CodeBlockKind::Indented => "",
                };
                let word = info.split(|c: char| c.is_whitespace() || c == ',').next();
                if word.is_some_and(|w| DIFFS.contains(&w.to_ascii_lowercase().as_str())) {
                    continue;
                }
                let mut fields = info.split_whitespace();
                let language = fields.next();
                if language.is_some_and(|l| SHELLS.contains(&l)) {
                    open = Some((blocks, fields.next().map(str::to_owned), String::new()));
                }
                blocks += 1;
            }
            Event::Text(text) => {
                if let Some((_, _, body)) = open.as_mut() {
                    body.push_str(&text);
                }
            }
            Event::End(TagEnd::CodeBlock) => {
                if let Some((block, address, body)) = open.take() {
                    let (host, cwd) = place(ask, address.as_deref());
                    commands.push(Command {
                        text: body.trim_end_matches('\n').to_owned(),
                        host,
                        cwd,
                        block,
                    });
                }
            }
            _ => {}
        }
    }
    commands
}

/// The machine and folder a block that names `address` runs in.
fn place(ask: &Ask, address: Option<&str>) -> (Option<String>, String) {
    let addressed = address.and_then(|address| {
        ask.references
            .iter()
            .flatten()
            .find(|r| r.kind == "command" && r.text == address)
    });
    let (host, cwd) = match addressed {
        Some(reference) => (reference.host.clone(), reference.path.clone()),
        None => (
            ask.host.clone(),
            ask.cwd.clone().unwrap_or_else(|| ask.base_dir.clone()),
        ),
    };
    (host.filter(|h| !h.is_empty()), cwd)
}

/// Every command `ask` offers, in reading order.
pub(crate) fn of(ask: &Ask) -> Vec<Command> {
    ask.parts
        .iter()
        .filter_map(|part| part.markdown.as_deref())
        .flat_map(|source| of_part(ask, source))
        .collect()
}
