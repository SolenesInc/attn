use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{Color, El, Key, KeyEvent, Span, Tx, col, el, row, text, text_input};

use crate::State;
use crate::terminal::{Filtered, Filtering, filter_field_key};

/// The wash over a filtered line's matches, as the Tauri panel marks them: the find yellow at
/// 0.45.
fn match_wash() -> Color {
    rune::color::hex("#f5c542")
        .expect("the find yellow is a color")
        .alpha(0.45)
}

/// Terminal `id`'s block filter over its top right corner, as the Tauri terminal floats it: the
/// field, Match case, the count and Close, and below them the output lines that hold the query,
/// each a press away from its row.
pub(super) fn panel(id: &str, filtering: &Filtering, results_height: f32, t: &Tokens) -> El {
    let (edit, edited, keyed) = (id.to_owned(), id.to_owned(), id.to_owned());
    let field = text_input(&filtering.query, type_::MONO_CAPTION)
        .key(filter_field_key(id))
        .single_line()
        .label("Filter block output")
        .placeholder("Filter block output", t.text_muted)
        .on_edit(move |state: &mut State| state.terminals.filter_query(&edit))
        .on_input_change(move |state: &mut State, _: &mut Tx| {
            state.terminals.filter_edited(&edited)
        })
        .on_input_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
            ev.key == Key::Escape && {
                state.terminals.close_filter(&keyed, tx);
                true
            }
        })
        .w(200.0)
        .color(t.text_primary);
    let case = button(id, "case", "Aa", "Match case", t, |state, id, _| {
        state.terminals.toggle_filter_case(id)
    })
    .fill(if filtering.case {
        t.text_secondary.alpha(0.3)
    } else {
        Color::TRANSPARENT
    })
    .color(if filtering.case {
        t.text_primary
    } else {
        t.text_secondary
    });
    let count = text(filtering.count())
        .key(format!("terminal.{id}.filter.count"))
        .font(type_::MONO_CAPTION)
        .color(t.text_secondary)
        .single_line();
    let close = button(
        id,
        "close",
        attn_ui::icon::CLOSE,
        "Close filter",
        t,
        |state, id, tx| state.terminals.close_filter(id, tx),
    );
    let bar = boxed(row(), t)
        .key(format!("terminal.{id}.filter"))
        .items_center()
        .gap(4.0)
        .px(6.0)
        .py(4.0)
        .children([field, case, count, close]);
    let mut panel = col().items_end().gap(4.0).max_w(640.0).child(bar);
    if let Some(lines) = filtering.lines() {
        panel = panel.child(results(id, lines, results_height, t));
    }
    el().absolute(0.0, 6.0)
        .w_full()
        .pr(14.0)
        .justify_end()
        .child(panel)
}

fn results(id: &str, lines: &[Filtered], max_height: f32, t: &Tokens) -> El {
    let list = boxed(col(), t)
        .key(format!("terminal.{id}.filter.results"))
        .padding(4.0)
        .max_h(max_height)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        });
    if lines.is_empty() {
        return list.child(
            text("No matching lines")
                .key(format!("terminal.{id}.filter.empty"))
                .font(type_::LABEL)
                .color(t.text_secondary)
                .px(8.0)
                .py(6.0),
        );
    }
    list.children(lines.iter().map(|line| {
        let (id, offset) = (id.to_owned(), line.offset);
        let wash = match_wash();
        let spans: Vec<Span> = line
            .matches
            .iter()
            .map(|m| Span {
                bg: Some(wash),
                ..Span::new(m.start, m.end)
            })
            .collect();
        text(&line.text)
            .key(format!("terminal.{id}.filter.line.{offset}"))
            .role("button")
            .label("Scroll to line")
            .font(type_::MONO_CAPTION)
            .color(t.text_primary)
            .single_line()
            .spans(spans)
            .w_full()
            .px(6.0)
            .py(2.0)
            .radius(4.0)
            .on_press(move |state: &mut State| state.terminals.scroll_to_filtered(&id, offset))
    }))
}

fn boxed(el: El, t: &Tokens) -> El {
    el.radius(6.0).fill(t.bg_elevated).border(1.0, t.border)
}

fn button(
    id: &str,
    part: &str,
    glyph: &str,
    label: &str,
    t: &Tokens,
    press: impl Fn(&mut State, &str, &mut Tx) + 'static,
) -> El {
    let (id, field) = (id.to_owned(), filter_field_key(id));
    text(glyph)
        .key(format!("terminal.{id}.filter.{part}"))
        .role("button")
        .label(label)
        .font(type_::LABEL)
        .color(t.text_secondary)
        .px(space::S1)
        .radius(4.0)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            press(state, &id, tx);
            if state.terminals.filtering(&id).is_some() {
                tx.focus(field.clone(), false);
            }
        })
}
