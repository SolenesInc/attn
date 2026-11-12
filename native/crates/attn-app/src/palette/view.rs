use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, el, row, text, text_input};

use super::{Face, Row};
use crate::State;

/// The search palette over the window: its field, the rows its query lists with the
/// highlighted one marked, and the keys that work here.
pub(crate) fn view(state: &State, t: &Tokens) -> El {
    let search = &state.window.search;
    let face = super::face_of(search);
    let label = match face {
        Face::Agents => "Agents",
        Face::Commands => "Commands",
    };
    let (rows, loading) = super::rows(state);
    let at = super::highlighted(search, &rows);
    let count = match face {
        Face::Commands if !loading => {
            Some(format!("{} of {}", rows.len(), super::command_count(state)))
        }
        _ => None,
    };
    let field = text_input(&search.query, type_::BODY)
        .key(super::field_key())
        .single_line()
        .label(label)
        .placeholder(
            "Jump to an agent or tile · type > for commands",
            t.text_muted,
        )
        .on_edit(|state: &mut State| &mut state.window.search.query)
        .on_input_change(super::edited)
        .on_input_key(super::on_key)
        .grow(1.0)
        .color(t.text_primary);
    let header = row()
        .key("palette.header")
        .w_full()
        .items_center()
        .gap(space::S2)
        .child(field)
        .children(count.map(|c| {
            text(c)
                .key("palette.count")
                .font(type_::LABEL)
                .color(t.text_tertiary)
        }))
        .children(super::badge(state, face).map(|b| {
            text(b)
                .key("palette.badge")
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary)
        }));
    let empty = rows.is_empty().then(|| {
        let message = match face {
            Face::Commands if loading => "Loading command history…",
            Face::Commands => "No matching commands",
            Face::Agents => "No match",
        };
        text(message)
            .key("palette.empty")
            .font(type_::BODY)
            .color(t.text_tertiary)
    });
    let list = col()
        .key("palette.list")
        .role("listbox")
        .label(label)
        .w_full()
        .max_h(620.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .gap(2.0)
        .children(
            rows.iter()
                .enumerate()
                .map(|(i, r)| option(r, at == Some(i), t)),
        )
        .children(empty);
    let hints: &[&str] = match face {
        Face::Agents => &["↑↓ move", "↵ jump", "type > for commands", "esc closes"],
        Face::Commands => &[
            "↑↓ move",
            "↵ run",
            "delete > to search agents",
            "esc closes",
        ],
    };
    let footer = row().key("palette.footer").gap(space::S3).children(
        hints
            .iter()
            .map(|h| text(*h).font(type_::LABEL).color(t.text_tertiary)),
    );
    let panel = col()
        .key("palette.dialog")
        .role("dialog")
        .label(label)
        .modal_tx(true, super::close)
        .w_full()
        .max_w(760.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .padding(space::S3)
        .gap(space::S2)
        .child(header)
        .child(list)
        .child(footer);
    el().key("palette")
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(
            el().key("palette.scrim")
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
                .pt(110.0)
                .items_center()
                .child(panel),
        )
}

fn option(r: &Row, highlighted: bool, t: &Tokens) -> El {
    let (pointing, pressing) = (r.key.clone(), r.key.clone());
    let mut name = row().gap(space::S2).grow(1.0).child(
        text(r.title.clone())
            .font(type_::BODY)
            .color(t.text_primary),
    );
    if let Some(description) = &r.description {
        name = name.child(
            text(description.clone())
                .font(type_::BODY)
                .color(t.text_tertiary)
                .single_line(),
        );
    }
    let mut option = row()
        .key(format!("palette.row.{}", r.key))
        .role("option")
        .label(r.title.clone())
        .focus_selected(highlighted)
        .w_full()
        .items_center()
        .px(space::S2)
        .py(space::S1)
        .radius(4.0)
        .fill(if highlighted {
            t.bg_selected
        } else {
            t.bg_elevated
        })
        .on_pointer_move(move |state: &mut State, p| super::pointed(state, &pointing, (p.x, p.y)))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| super::pick_key(state, &pressing, tx))
        .child(name);
    if let Some(description) = &r.description {
        option = option.description(description.clone());
    }
    option.children(
        r.shortcut
            .clone()
            .map(|s| text(s).font(type_::MONO_CAPTION).color(t.text_tertiary)),
    )
}
