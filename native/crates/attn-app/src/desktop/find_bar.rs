use attn_ui::Tokens;
use attn_ui::blocks::TerminalBar;
use rune::{El, Key, KeyEvent, Tx};
use rune_kit::TextInput;

use crate::State;
use crate::terminal::{Finding, Toward, find_field_key};

/// The find field's width.
const FIELD_W: f32 = 160.0;

/// Terminal `id`'s find bar floating over its top right corner: the field, Match case, the
/// count, and the keys to step through the matches or close it.
pub(super) fn bar(id: &str, finding: &Finding, t: &Tokens) -> El {
    let (edit, edited, keyed) = (id.to_owned(), id.to_owned(), id.to_owned());
    let field = TextInput::new(&finding.query)
        .key(find_field_key(id))
        .label("Find")
        .placeholder("Find")
        .on_edit(move |state: &mut State| state.terminals.find_query(&edit))
        .on_change(move |state: &mut State, _: &mut Tx| state.terminals.find_edited(&edited))
        .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| on_key(state, &keyed, ev, tx));
    let bar = TerminalBar::new(format!("terminal.{id}.find"))
        .field(field, FIELD_W, t)
        .push(find_button(
            id,
            "case",
            "Aa",
            "Match case",
            finding.case,
            t,
            |state, id, _| state.terminals.toggle_find_case(id),
        ))
        .count(format!("terminal.{id}.find.count"), finding.count(), t)
        .push(find_button(
            id,
            "prev",
            attn_ui::glyph::PREVIOUS,
            "Previous match",
            false,
            t,
            |state, id, _| state.terminals.step_find(id, Toward::Older),
        ))
        .push(find_button(
            id,
            "next",
            attn_ui::glyph::NEXT,
            "Next match",
            false,
            t,
            |state, id, _| state.terminals.step_find(id, Toward::Newer),
        ))
        .push(find_button(
            id,
            "close",
            attn_ui::glyph::TIMES,
            "Close find",
            false,
            t,
            |state, id, tx| state.terminals.close_find(id, tx),
        ))
        .build(t);
    let laid = id.to_owned();
    TerminalBar::corner(vec![bar.on_layout_tx(
        move |state: &mut State, [_, _, _, height], tx: &mut Tx| {
            if !state.terminals.bars_laid_out(&laid, height) {
                tx.live_only();
            }
        },
    )])
}

/// Escape closes the bar; Enter steps to the older match and Shift+Enter to the newer.
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
    glyph: &'static str,
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
    glyph: &'static str,
    label: &str,
    on: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
    field: impl Fn(&State) -> Option<String> + 'static,
) -> El {
    TerminalBar::button(key, label, glyph, on)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            press(state, tx);
            if let Some(field) = field(state) {
                tx.focus(field, false);
            }
        })
        .build(t)
        .into()
}
