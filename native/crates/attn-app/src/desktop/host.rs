use attn_protocol::{OutpostInfo, Session};
use attn_ui::Tokens;
use rune::{El, el, text};

const HOST_TYPE: rune::TextStyle = rune::TextStyle::new(10.0, 400.0);

/// The machine a remote session runs on, by its outpost's name, or by the id its host names
/// when no outpost row has it.
pub(crate) fn name(session: &Session, outposts: &[OutpostInfo]) -> Option<String> {
    let host = session.host.as_deref().filter(|h| !h.is_empty())?;
    Some(
        outposts
            .iter()
            .find(|o| o.id == host)
            .map_or(host, |o| o.name.as_str())
            .to_owned(),
    )
}

pub(crate) fn piece(leaf: &str, name: &str, t: &Tokens) -> El {
    el().key(format!("tile.{leaf}.host"))
        .px(4.0)
        .border(1.0, t.border)
        .radius(3.0)
        .child(
            text(name.to_owned())
                .font(HOST_TYPE)
                .color(t.text_secondary)
                .single_line(),
        )
}
