use attn_ui::{GlyphButton, Tokens};
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, Tx, el, row, text, text_input};
use rune_kit::ButtonVariant;

use crate::State;
use crate::terminal::{Finding, Toward, find_field_key};

/// Terminal `id`'s find bar over its top right corner, as the Tauri terminal floats it: the
/// field, Match case, the count, and the keys to step through the matches or close it.
pub(super) fn bar(id: &str, finding: &Finding, t: &Tokens) -> El {
    let (edit, edited, keyed) = (id.to_owned(), id.to_owned(), id.to_owned());
    let field = text_input(&finding.query, type_::MONO_CAPTION)
        .key(find_field_key(id))
        .single_line()
        .label("Find")
        .placeholder("Find", t.text_muted)
        .on_edit(move |state: &mut State| state.terminals.find_query(&edit))
        .on_input_change(move |state: &mut State, _: &mut Tx| state.terminals.find_edited(&edited))
        .on_input_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
            on_key(state, &keyed, ev, tx)
        })
        .w(160.0)
        .color(t.text_primary);
    let case = find_button(
        id,
        "case",
        "Aa",
        "Match case",
        finding.case,
        t,
        |state, id, _| state.terminals.toggle_find_case(id),
    );
    let count = text(finding.count())
        .key(format!("terminal.{id}.find.count"))
        .font(type_::MONO_CAPTION)
        .color(t.text_secondary)
        .single_line();
    let older = find_button(
        id,
        "prev",
        attn_ui::icon::CARET_UP,
        "Previous match",
        false,
        t,
        |state, id, _| state.terminals.step_find(id, Toward::Older),
    );
    let newer = find_button(
        id,
        "next",
        attn_ui::icon::CARET_DOWN,
        "Next match",
        false,
        t,
        |state, id, _| state.terminals.step_find(id, Toward::Newer),
    );
    let close = find_button(
        id,
        "close",
        attn_ui::icon::CLOSE,
        "Close find",
        false,
        t,
        |state, id, tx| state.terminals.close_find(id, tx),
    );
    let bar = row()
        .key(format!("terminal.{id}.find"))
        .items_center()
        .gap(4.0)
        .px(6.0)
        .py(4.0)
        .children([field, case, count, older, newer, close]);
    let bar = rune_kit::popover::raised(attn_ui::kit(t), bar, 6.0);
    el().absolute(0.0, 6.0)
        .w_full()
        .pr(14.0)
        .justify_end()
        .child(bar)
}

/// Escape closes the bar; Enter steps to the older match and Shift+Enter to the newer, as in
/// the Tauri terminal.
fn on_key(state: &mut State, id: &str, ev: &KeyEvent, tx: &mut Tx) -> bool {
    match ev.key {
        Key::Escape => state.terminals.close_find(id, tx),
        Key::Enter if ev.mods.shift => state.terminals.step_find(id, Toward::Newer),
        Key::Enter => state.terminals.step_find(id, Toward::Older),
        _ => return false,
    }
    true
}

fn find_button(
    id: &str,
    part: &str,
    glyph: &str,
    label: &str,
    on: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &str, &mut Tx) + 'static,
) -> El {
    let (pressed, open) = (id.to_owned(), id.to_owned());
    button(
        format!("terminal.{id}.find.{part}"),
        glyph,
        label,
        on,
        t,
        move |state, tx| press(state, &pressed, tx),
        move |state| {
            state
                .terminals
                .finding(&open)
                .map(|_| find_field_key(&open))
        },
    )
}

/// A glyph button on a terminal's find bar or filter panel, set in when `on`, that hands the
/// keyboard back to the bar's `field` while the bar stays open.
pub(super) fn button(
    key: String,
    glyph: &str,
    label: &str,
    on: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
    field: impl Fn(&State) -> Option<String> + 'static,
) -> El {
    GlyphButton::new(glyph, label)
        .variant(if on {
            ButtonVariant::Ghost
        } else {
            ButtonVariant::GhostMuted
        })
        .font(type_::LABEL)
        .side(type_::LABEL.line_px())
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            press(state, tx);
            if let Some(field) = field(state) {
                tx.focus(field, false);
            }
        })
        .build(attn_ui::kit(t))
        .key(key)
        .px(space::S1)
        .radius(4.0)
        .hover(rune::Color::TRANSPARENT)
        .fill(if on {
            t.text_secondary.alpha(0.3)
        } else {
            rune::Color::TRANSPARENT
        })
}
