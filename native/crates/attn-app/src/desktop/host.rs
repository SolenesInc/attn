use attn_protocol::{OutpostInfo, Session};

use crate::machine::Machines;
use attn_ui::Tokens;
use attn_ui::blocks::HeaderPiece;
use rune::{Color, El};

use super::fit::{Level, Widths, host_mark};

/// The machine a session runs on, when it is not this app's.
pub(crate) fn name(session: &Session, machines: &Machines) -> Option<String> {
    machines.away(session.host.as_deref())
}

/// The outpost a session runs on, by its name, or by the id its host names when no outpost row
/// has it.
pub(crate) fn outpost_of(session: &Session, outposts: &[OutpostInfo]) -> Option<String> {
    let host = session.host.as_deref().filter(|h| !h.is_empty())?;
    Some(outpost_name(host, outposts))
}

/// Outpost `host` by its name, or by its id when no outpost row has it.
pub(crate) fn outpost_name(host: &str, outposts: &[OutpostInfo]) -> String {
    outposts
        .iter()
        .find(|o| o.id == host)
        .map_or(host, |o| o.name.as_str())
        .to_owned()
}

/// The machine's name, or at `Level::Short` its mark, which names the machine to assistive tech.
pub(super) fn piece(leaf: &str, name: &str, level: Level, t: &Tokens) -> El {
    face(name, level, t.text_secondary)
        .build(t)
        .key(format!("tile.{leaf}.host"))
        .label(name.to_owned())
}

/// The machine's width by its name, and by its mark.
pub(super) fn widths(name: &str) -> Widths {
    let at = |level| face(name, level, Color::TRANSPARENT).width();
    Widths {
        full: at(Level::Full),
        short: Some(at(Level::Short)),
        icon: None,
    }
}

fn face(name: &str, level: Level, ink: Color) -> HeaderPiece {
    let shown = match level {
        Level::Short => host_mark(name),
        _ => name.to_owned(),
    };
    HeaderPiece::new().state(shown, ink)
}
