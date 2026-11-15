use attn_protocol::{OutpostInfo, Session};
use attn_ui::Tokens;
use rune::{El, el, text};

use super::fit::{Level, Widths, host_mark, text_w};

const HOST_TYPE: rune::TextStyle = rune::TextStyle::new(10.0, 400.0);
const HOST_PAD: f32 = 4.0;
const HOST_BORDER: f32 = 1.0;

/// The machine a remote session runs on, by its outpost's name, or by the id its host names
/// when no outpost row has it.
pub(crate) fn name(session: &Session, outposts: &[OutpostInfo]) -> Option<String> {
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
    let shown = match level {
        Level::Short => host_mark(name),
        _ => name.to_owned(),
    };
    el().key(format!("tile.{leaf}.host"))
        .label(name.to_owned())
        .px(HOST_PAD)
        .border(HOST_BORDER, t.border)
        .radius(3.0)
        .child(
            text(shown)
                .font(HOST_TYPE)
                .color(t.text_secondary)
                .single_line(),
        )
}

/// The machine's width by its name, and by its mark.
pub(super) fn widths(name: &str) -> Widths {
    let around = 2.0 * (HOST_PAD + HOST_BORDER);
    Widths {
        full: around + text_w(name, HOST_TYPE),
        short: Some(around + text_w(&host_mark(name), HOST_TYPE)),
        icon: None,
    }
}
