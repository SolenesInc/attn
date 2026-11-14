use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, el, row, text};
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
        .text_style(type_::BODY)
        .height(type_::BODY.line_px())
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
        .w_full()
        .max_h(520.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .gap(2.0)
        .children(
            rows.iter()
                .enumerate()
                .map(|(i, r)| option(r, at == Some(i), t)),
        )
        .children(rows.is_empty().then(|| {
            text(empty)
                .key("opener.empty")
                .font(type_::BODY)
                .color(t.text_tertiary)
        }));
    let panel = col()
        .key("opener.dialog")
        .role("dialog")
        .label(LABEL)
        .modal_tx(true, super::close)
        .w_full()
        .max_w(640.0)
        .padding(space::S3)
        .gap(space::S2)
        .child(field)
        .child(list);
    let panel = rune_kit::popover::raised(attn_ui::kit(t), panel, 8.0);
    el().key("opener")
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(
            el().key("opener.scrim")
                .absolute(0.0, 0.0)
                .w_full()
                .h_full()
                .fill(t.bg_overlay)
                .a11y_hidden(),
        )
        .child(
            col()
                .w_full()
                .h_full()
                .px(16.0)
                .pt(80.0)
                .items_center()
                .child(panel),
        )
}

fn option(r: &Row, highlighted: bool, t: &Tokens) -> El {
    let picked = r.key.clone();
    row()
        .key(format!("opener.row.{}", r.key))
        .role("option")
        .label(format!("{} {}", r.title, r.detail))
        .focus_selected(highlighted)
        .w_full()
        .items_center()
        .gap(space::S2)
        .px(space::S2)
        .py(space::S1)
        .radius(4.0)
        .fill(if highlighted {
            t.bg_selected
        } else {
            t.bg_elevated
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::pick_key(state, &picked, tx))
        .child(
            text(r.title.clone())
                .font(type_::BODY)
                .color(t.text_primary)
                .single_line(),
        )
        .child(
            text(r.detail.clone())
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary)
                .single_line()
                .truncate()
                .shrink(),
        )
}
