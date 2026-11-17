use attn_ui::Tokens;
use attn_ui::finder;
use rune::{El, Tx, col};
use rune_kit::TextInput;

use super::Row;
use crate::State;

const LABEL: &str = "Open a markdown file";

/// The opener over the window: its field and the rows its query lists, the highlighted one
/// marked.
pub(crate) fn view(state: &State, t: &Tokens) -> El {
    let (rows, empty) = super::rows(state);
    let at = super::highlighted(&state.opener, &rows);
    let field = TextInput::new(&state.opener.query)
        .key(super::FIELD)
        .plain()
        .text_style(finder::FIELD_TEXT)
        .height(finder::FIELD_TEXT.line_px())
        .width(None)
        .label(LABEL)
        .placeholder("Open a markdown file…")
        .on_edit(|state: &mut State| &mut state.opener.query)
        .on_change(super::edited)
        .on_key(super::on_key)
        .build(attn_ui::kit(t));
    let list = finder::list("opener.list")
        .label(LABEL)
        .children(
            rows.iter()
                .enumerate()
                .map(|(i, r)| option(r, at == Some(i), t)),
        )
        .children(
            rows.is_empty()
                .then(|| finder::empty(t, "opener.empty", empty)),
        );
    let panel = col()
        .key("opener.dialog")
        .role("dialog")
        .label(LABEL)
        .modal_tx(true, super::close)
        .max_h(finder::MAX_H)
        .shrink()
        .min_h(0.0);
    attn_ui::finder::layer(
        t,
        "opener",
        finder::panel(t, panel, finder::input_row(None, field, None), list, None),
    )
}

fn option(r: &Row, highlighted: bool, t: &Tokens) -> El {
    let picked = r.key.clone();
    finder::path_option(
        t,
        format!("opener.row.{}", r.key),
        &r.title,
        &r.detail,
        highlighted,
    )
    .role("option")
    .label(format!("{} {}", r.title, r.detail))
    .on_press_tx(move |state: &mut State, tx: &mut Tx| super::pick_key(state, &picked, tx))
}
