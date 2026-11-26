//! Document Find: a find bar over a rendered document, which washes every match of its query
//! and brings the current one to the middle of the view.

use std::ops::Range;
use std::rc::Rc;

use attn_ui::Tokens;
use attn_ui::blocks::TerminalBar;
use rune::{El, Key, KeyEvent, TextEdit, Tx};
use rune_kit::{Case, Highlight, Markdown, TextInput};

use crate::State;

/// The find field's width.
const FIELD_W: f32 = 160.0;

/// The document a find bar searches, read when a key steps through its matches.
pub(crate) type Source = Rc<dyn for<'a> Fn(&'a State) -> Option<&'a str>>;

/// One document's find bar, by the scroll view the document reads in.
#[derive(Default)]
pub(crate) struct Finding {
    pub(crate) query: TextEdit,
    pub(crate) open: bool,
    current: usize,
}

impl Finding {
    /// Where each match of the query sits in `source`, in document order.
    pub(crate) fn matches(&self, source: &str) -> Vec<Vec<Range<usize>>> {
        Markdown::find(source, &self.query.text, Case::Insensitive)
    }

    /// The current match among `count`, kept inside them as the document changes.
    fn current(&self, count: usize) -> Option<usize> {
        (count > 0).then(|| self.current.min(count - 1))
    }
}

fn field_key(view: &str) -> String {
    format!("{view}.find.input")
}

fn match_key(view: &str, at: usize) -> String {
    format!("{view}.find.match.{at}")
}

/// The open find bar of the document `view` scrolls.
pub(crate) fn finding<'r>(readings: &'r super::Readings, view: &str) -> Option<&'r Finding> {
    readings.finds.get(view).filter(|f| f.open)
}

/// Opens the find bar of the document `view` scrolls, its last query selected.
pub(crate) fn open(state: &mut State, view: &str, tx: &mut Tx) {
    let finding = state.readings.finds.entry(view.to_owned()).or_default();
    finding.open = true;
    finding.query.anchor = 0;
    finding.query.focus = finding.query.text.len();
    finding.query.focused = true;
    state.readings.revision += 1;
    tx.focus(field_key(view), false);
}

/// Closes the find bar of the document `view` scrolls, and its washes with it: true when one
/// was open.
pub(crate) fn close(state: &mut State, view: &str, tx: &mut Tx) -> bool {
    let Some(finding) = state.readings.finds.get_mut(view).filter(|f| f.open) else {
        return false;
    };
    finding.open = false;
    finding.query.focused = false;
    state.readings.revision += 1;
    tx.blur();
    true
}

/// Steps to the next match in the document `source` reads, or the previous one, wrapping at
/// the ends, and brings it to the middle of the view.
pub(crate) fn step(state: &mut State, view: &str, source: &Source, forward: bool, tx: &mut Tx) {
    let count = match (finding(&state.readings, view), source(state)) {
        (Some(finding), Some(text)) => finding.matches(text).len(),
        _ => return,
    };
    let Some(finding) = state.readings.finds.get_mut(view) else {
        return;
    };
    let Some(at) = finding.current(count) else {
        return;
    };
    finding.current = if forward {
        (at + 1) % count
    } else {
        (at + count - 1) % count
    };
    let current = finding.current;
    state.readings.revision += 1;
    tx.scroll_center(match_key(view, current));
}

/// What the find bar of the document `view` scrolls washes over `source`: every match, the
/// current one stronger.
pub(crate) fn highlights(
    finding: Option<&Finding>,
    source: &str,
    view: &str,
) -> Vec<(Vec<Range<usize>>, Highlight)> {
    let Some(finding) = finding else {
        return Vec::new();
    };
    let matches = finding.matches(source);
    let current = finding.current(matches.len());
    matches
        .into_iter()
        .enumerate()
        .map(|(at, ranges)| {
            let wash = if Some(at) == current {
                Highlight::current()
            } else {
                Highlight::new()
            };
            (ranges, wash.key(match_key(view, at)))
        })
        .collect()
}

/// The find bar floating over the top right corner of the document `view` scrolls, with
/// `count` matches in it: the field, the count, and the buttons that step through the matches
/// or close it. Enter steps to the next match and Shift+Enter to the previous one, handing the
/// keyboard back to the document, and Escape closes the bar.
pub(crate) fn bar(view: &str, finding: &Finding, count: usize, source: Source, t: &Tokens) -> El {
    let (edit, edited, keyed) = (view.to_owned(), view.to_owned(), view.to_owned());
    let typed = source.clone();
    let field = TextInput::new(&finding.query)
        .key(field_key(view))
        .label("Find in document")
        .placeholder("Find")
        .on_edit(move |state: &mut State| {
            &mut state.readings.finds.entry(edit.clone()).or_default().query
        })
        .on_change(move |state: &mut State, tx: &mut Tx| edited_query(state, &edited, tx))
        .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
            field_key_pressed(state, &keyed, &typed, ev, tx)
        });
    let said = match finding.current(count) {
        Some(at) => format!("{}/{count}", at + 1),
        None => "0/0".into(),
    };
    let stepper = |part: &str, glyph: &'static str, label: &str, forward: bool| {
        let (view, source) = (view.to_owned(), source.clone());
        button(view.clone(), part, glyph, label, t, move |state, tx| {
            step(state, &view, &source, forward, tx)
        })
    };
    let closes = view.to_owned();
    let bar = TerminalBar::new(format!("{view}.find"))
        .field(field, FIELD_W, t)
        .count(format!("{view}.find.count"), said, t)
        .push(stepper(
            "prev",
            attn_ui::glyph::PREVIOUS,
            "Previous match",
            false,
        ))
        .push(stepper("next", attn_ui::glyph::NEXT, "Next match", true))
        .push(button(
            view.to_owned(),
            "close",
            attn_ui::glyph::TIMES,
            "Close find",
            t,
            move |state, tx| {
                close(state, &closes, tx);
            },
        ))
        .build(t);
    TerminalBar::corner(vec![bar])
}

fn edited_query(state: &mut State, view: &str, tx: &mut Tx) {
    if let Some(finding) = state.readings.finds.get_mut(view) {
        finding.current = 0;
    }
    state.readings.revision += 1;
    tx.scroll_center(match_key(view, 0));
}

fn field_key_pressed(
    state: &mut State,
    view: &str,
    source: &Source,
    ev: &KeyEvent,
    tx: &mut Tx,
) -> bool {
    let forward = match ev.key {
        Key::Escape => return close(state, view, tx),
        Key::Enter => !ev.mods.shift,
        _ => return false,
    };
    step(state, view, source, forward, tx);
    if let Some(finding) = state.readings.finds.get_mut(view) {
        finding.query.focused = false;
    }
    tx.blur();
    true
}

fn button(
    view: String,
    part: &str,
    glyph: &'static str,
    label: &str,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    TerminalBar::button(format!("{view}.find.{part}"), label, glyph, false)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            press(state, tx);
            if finding(&state.readings, &view).is_some() {
                tx.focus(field_key(&view), false);
            }
        })
        .build(t)
        .into()
}
