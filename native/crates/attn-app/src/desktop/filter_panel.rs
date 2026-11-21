use attn_ui::Tokens;
use attn_ui::blocks::TerminalBar;
use rune::{El, Key, KeyEvent, Tx};
use rune_kit::TextInput;

use crate::State;
use crate::terminal::{Filtered, Filtering, filter_field_key};

/// The filter field's width, as the Tauri terminal's.
const FIELD_W: f32 = 200.0;

/// Terminal `id`'s block filter over its top right corner, as the Tauri terminal floats it: the
/// field, Match case, the count and Close, and below them the output lines that hold the query,
/// each a press away from its row.
pub(super) fn panel(id: &str, filtering: &Filtering, results_height: f32, t: &Tokens) -> El {
    let (edit, edited, keyed) = (id.to_owned(), id.to_owned(), id.to_owned());
    let field = TextInput::new(&filtering.query)
        .key(filter_field_key(id))
        .label("Filter block output")
        .placeholder("Filter block output")
        .on_edit(move |state: &mut State| state.terminals.filter_query(&edit))
        .on_change(move |state: &mut State, _: &mut Tx| state.terminals.filter_edited(&edited))
        .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
            ev.key == Key::Escape && {
                state.terminals.close_filter(&keyed, tx);
                true
            }
        });
    let bar = TerminalBar::new(format!("terminal.{id}.filter"))
        .field(field, FIELD_W, t)
        .push(filter_button(
            id,
            "case",
            "Aa",
            "Match case",
            filtering.case,
            t,
            |state, id, _| state.terminals.toggle_filter_case(id),
        ))
        .count(format!("terminal.{id}.filter.count"), filtering.count(), t)
        .push(filter_button(
            id,
            "close",
            attn_ui::glyph::TIMES,
            "Close filter",
            false,
            t,
            |state, id, tx| state.terminals.close_filter(id, tx),
        ))
        .build(t);
    let results = filtering
        .lines()
        .map(|lines| results(id, lines, results_height, t));
    let laid = id.to_owned();
    TerminalBar::corner([bar].into_iter().chain(results).collect()).on_layout_tx(
        move |state: &mut State, [_, _, _, height], tx: &mut Tx| {
            if !state.terminals.bars_laid_out(&laid, height) {
                tx.live_only();
            }
        },
    )
}

fn results(id: &str, lines: &[Filtered], max_height: f32, t: &Tokens) -> El {
    let key = format!("terminal.{id}.filter.results");
    if lines.is_empty() {
        let none = TerminalBar::none(
            format!("terminal.{id}.filter.empty"),
            "No matching lines",
            t,
        );
        return TerminalBar::list(key, max_height, vec![none], t);
    }
    let wash = t.find.alpha(0.45);
    let found = lines.iter().map(|line| {
        let (id, offset) = (id.to_owned(), line.offset);
        let matches: Vec<(usize, usize)> = line.matches.iter().map(|m| (m.start, m.end)).collect();
        TerminalBar::line(
            format!("terminal.{id}.filter.line.{offset}"),
            "Scroll to line",
            line.text.clone(),
            &matches,
            wash,
            t,
            move |state: &mut State| state.terminals.scroll_to_filtered(&id, offset),
        )
    });
    TerminalBar::list(key, max_height, found.collect(), t)
}

fn filter_button(
    id: &str,
    part: &str,
    glyph: &'static str,
    label: &str,
    on: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &str, &mut Tx) + 'static,
) -> El {
    let (pressed, open) = (id.to_owned(), id.to_owned());
    super::find_bar::button(
        format!("terminal.{id}.filter.{part}"),
        glyph,
        label,
        on,
        t,
        move |state, tx| press(state, &pressed, tx),
        move |state| {
            state
                .terminals
                .filtering(&open)
                .map(|_| filter_field_key(&open))
        },
    )
}
