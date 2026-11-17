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
    let list = col()
        .key("opener.list")
        .role("listbox")
        .label(LABEL)
        .max_h(finder::LIST_MAX)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .gap(2.0)
        .children(
            rows.iter()
                .enumerate()
                .map(|(i, r)| option(r, at == Some(i), t)),
        )
        .children(
            rows.is_empty()
                .then(|| finder::empty(t, empty).key("opener.empty")),
        );
    let panel = finder::panel(t, field, list)
        .key("opener.dialog")
        .role("dialog")
        .label(LABEL)
        .modal_tx(true, super::close);
    finder::layer(t, "opener", panel)
}

fn option(r: &Row, highlighted: bool, t: &Tokens) -> El {
    let picked = r.key.clone();
    finder::option(t, r.title.clone(), r.detail.clone(), highlighted)
        .key(format!("opener.row.{}", r.key))
        .role("option")
        .label(format!("{} {}", r.title, r.detail))
        .focus_selected(highlighted)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::pick_key(state, &picked, tx))
}
