use attn_ui::Tokens;
use attn_ui::type_;
use rune::{El, Key, KeyEvent, Span, Tx, col, el, row, text};
use rune_kit::{Button, ButtonVariant, TextInput};

use crate::State;
use crate::terminal::{Filtered, Filtering, filter_field_key};

/// Terminal `id`'s block filter over its top right corner, as the Tauri terminal floats it: the
/// field, Match case, the count and Close, and below them the output lines that hold the query,
/// each a press away from its row.
pub(super) fn panel(id: &str, filtering: &Filtering, results_height: f32, t: &Tokens) -> El {
    let (edit, edited, keyed) = (id.to_owned(), id.to_owned(), id.to_owned());
    let field = TextInput::new(&filtering.query)
        .key(filter_field_key(id))
        .plain()
        .text_style(type_::MONO_CAPTION)
        .height(type_::MONO_CAPTION.line_px())
        .width(Some(200.0))
        .label("Filter block output")
        .placeholder("Filter block output")
        .on_edit(move |state: &mut State| state.terminals.filter_query(&edit))
        .on_change(move |state: &mut State, _: &mut Tx| state.terminals.filter_edited(&edited))
        .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
            ev.key == Key::Escape && {
                state.terminals.close_filter(&keyed, tx);
                true
            }
        })
        .build(attn_ui::kit(t));
    let case = filter_button(
        id,
        "case",
        "Aa",
        "Match case",
        filtering.case,
        t,
        |state, id, _| state.terminals.toggle_filter_case(id),
    );
    let count = text(filtering.count())
        .key(format!("terminal.{id}.filter.count"))
        .font(type_::MONO_CAPTION)
        .color(t.text_secondary)
        .single_line();
    let close = filter_button(
        id,
        "close",
        attn_ui::glyph::TIMES,
        "Close filter",
        false,
        t,
        |state, id, tx| state.terminals.close_filter(id, tx),
    );
    let bar = row()
        .key(format!("terminal.{id}.filter"))
        .items_center()
        .gap(4.0)
        .px(6.0)
        .py(4.0)
        .children([field, case, count, close]);
    let bar = surface(bar, t);
    let mut panel = col().items_end().gap(4.0).max_w(640.0).child(bar);
    if let Some(lines) = filtering.lines() {
        panel = panel.child(surface(results(id, lines, results_height, t), t));
    }
    el().absolute(0.0, 6.0)
        .w_full()
        .pr(14.0)
        .justify_end()
        .child(panel)
}

fn results(id: &str, lines: &[Filtered], max_height: f32, t: &Tokens) -> El {
    let list = col()
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
        let wash = t.find.alpha(0.45);
        let spans: Vec<Span> = line
            .matches
            .iter()
            .map(|m| Span {
                bg: Some(wash),
                ..Span::new(m.start, m.end)
            })
            .collect();
        let (words, ink) = (line.text.clone(), t.text_primary);
        let key = format!("terminal.{id}.filter.line.{offset}");
        Button::new("Scroll to line")
            .variant(ButtonVariant::Ghost)
            .on_press(move |state: &mut State| state.terminals.scroll_to_filtered(&id, offset))
            .label_with(move |_, _| {
                text(words)
                    .font(type_::MONO_CAPTION)
                    .color(ink)
                    .single_line()
                    .spans(spans)
                    .w_full()
            })
            .build(attn_ui::kit(t))
            .key(key)
            .w_full()
            .h(type_::MONO_CAPTION.line_px() + 4.0)
            .px(6.0)
            .radius(4.0)
    }))
}

fn surface(el: El, t: &Tokens) -> El {
    rune_kit::popover::raised(attn_ui::kit(t), el, 6.0)
}

fn filter_button(
    id: &str,
    part: &str,
    glyph: &str,
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
