//! A stop: lines of one file at head, shown as they are now with the change from base, and
//! the agent's notes under the lines they are about.

use attn_protocol::{ReviewNote, ReviewRow, ReviewStop};
use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::{radius, space};
use rune::{Color, El, Span, TextStyle, col, rich, row, text};
use rune_kit::syntax::{Highlight, highlight, lang_for_path};

use std::collections::HashMap;

/// What code navigation draws on a review's rows: the labels on the row a name is being picked
/// on, and the row a question waits on.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) struct Asking {
    pub(crate) labels: Option<(String, Vec<super::nav::Labeled>)>,
    pub(crate) asked: Option<String>,
}

impl Asking {
    pub(crate) fn labels_at(&self, row: &str) -> &[super::nav::Labeled] {
        match &self.labels {
            Some((r, labels)) if r == row => labels,
            _ => &[],
        }
    }
}

pub(super) fn row_key(stop: &str, row: usize) -> String {
    format!("{stop}.row.{row}")
}

/// How the stop names its lines: as they are now, or as written at head when the file is gone.
pub(super) fn range(stop: &ReviewStop, rows: &[ReviewRow]) -> String {
    if rows.len() != stop.rows.len() {
        let lines: Vec<i64> = rows.iter().filter_map(|r| r.now).collect();
        if let (Some(first), Some(last)) = (lines.first(), lines.last()) {
            return format!("L{first}–{last}");
        }
    }
    if stop.now_from > 0 {
        format!("L{}–{}", stop.now_from, stop.now_to)
    } else {
        format!("L{}–{} at head", stop.from, stop.to)
    }
}

fn note_range(note: &ReviewNote) -> String {
    let (from, to) = if note.now_from > 0 {
        (note.now_from, note.now_to)
    } else {
        (note.from, note.to)
    };
    if from == to {
        format!("L{from}")
    } else {
        format!("L{from}–{to}")
    }
}

/// The stop keyed `key`, with the cursor on its row `cursor`, its rows `rows` (more than its own
/// when `e` showed more), in its file's colors when `file` holds them.
pub(super) fn stop(
    (tile, key): (&str, &str),
    stop: &ReviewStop,
    rows: &[ReviewRow],
    (cursor, selected): (Option<usize>, Option<(usize, usize)>),
    (marks, nav): (&[(i64, &attn_protocol::MarkdownAnnotation)], &Asking),
    file: Option<&Colors>,
    t: &Tokens,
) -> El {
    let shown: Vec<i64> = rows.iter().filter_map(|r| r.now).collect();
    let indent = note_indent(t);
    let unplaced = stop
        .notes
        .iter()
        .filter(|n| !shown.contains(&n.now_to))
        .map(|n| note(n, indent, t));
    let own = file.is_none().then(|| Colors::read(&stop.path, rows));
    let spans = |j: usize, r: &ReviewRow| match (file, &own) {
        (Some(file), _) => file.spans_of(r, t).unwrap_or_default(),
        (None, Some(own)) => own.spans(j, t),
        (None, None) => Vec::new(),
    };
    let in_range = |r: &ReviewRow| {
        r.now
            .is_some_and(|n| n >= stop.now_from && n <= stop.now_to)
    };
    let first = rows.iter().position(in_range).unwrap_or(0);
    let lines = rows.iter().zip(0..).flat_map(|(r, j)| {
        let notes = r
            .now
            .map(|now| {
                stop.notes
                    .iter()
                    .filter(move |n| n.now_to == now)
                    .map(|n| note(n, indent, t))
                    .collect::<Vec<_>>()
            })
            .unwrap_or_default();
        let keyed = row_key(key, j);
        let labels = nav.labels_at(&keyed);
        let line = code_row(
            r,
            &spans(j, r),
            in_range(r).then(|| range_marker(t)),
            true,
            (labels, nav.asked.as_deref() == Some(keyed.as_str())),
            t,
        )
        .key(keyed);
        let line = match selected {
            Some((a, b)) if j >= a && j <= b && cursor != Some(j) => {
                let tint = super::cursor_tint(t);
                line.gradient(tint, tint)
            }
            _ => line,
        };
        let marked: Vec<El> = marks
            .iter()
            .filter(|(at, _)| r.now == Some(*at))
            .map(|(_, m)| super::marks::mark_row(m, t))
            .collect();
        let line = super::cursor_frame(line, cursor == Some(j), t);
        std::iter::once(super::place_press(line, tile, row_key(key, j)))
            .chain(notes)
            .chain(marked)
    });
    let lines: Vec<El> = lines.collect();
    let opens = action("file", "↵", format!("{key}.file"), t);
    let opens = super::file_press(opens, tile, row_key(key, first));
    card(key, stop, rows, t)
        .child(header(stop, rows, [opens], t))
        .children(unplaced)
        .children(lines)
        .children(rows.is_empty().then(|| empty(t)))
}

/// A stop's card keyed `key`: its border and fill, its rows running to its edges and clipped
/// at its corners, as the Tauri review draws `.review-stop`.
pub(super) fn card(key: &str, stop: &ReviewStop, rows: &[ReviewRow], t: &Tokens) -> El {
    col()
        .key(key.to_owned())
        .role("group")
        .label(format!("{} {}", stop.path, range(stop, rows)))
        .w_full()
        .radius(radius::MD)
        .clip_rounded(radius::MD)
        .border(1.0, t.border.alpha(0.6))
        .fill(t.bg_tile)
}

/// What a stop with no rows says.
pub(super) fn empty(t: &Tokens) -> El {
    text("The file is gone or empty now.")
        .font(type_::LABEL)
        .color(t.text_secondary)
        .px(12.0)
        .py(6.0)
}

/// A button in a stop's header, `what` and its key `key`, as the Tauri review's
/// `.review-stop-actions` draw them.
pub(super) fn action(what: &str, key: &str, keyed: String, t: &Tokens) -> El {
    row()
        .key(keyed)
        .role("button")
        .label(format!("{what} {key}"))
        .gap(4.0)
        .items_center()
        .child(
            text(what.to_owned())
                .font(TextStyle {
                    weight: 400.0,
                    ..type_::LABEL
                })
                .color(t.text_secondary)
                .a11y_hidden(),
        )
        .child(super::keys::caps(key, t).a11y_hidden())
}

/// A stop's header strip: where it is, the lines it shows, and `actions` at its right end.
pub(super) fn header(
    stop: &ReviewStop,
    rows: &[ReviewRow],
    actions: impl IntoIterator<Item = El>,
    t: &Tokens,
) -> El {
    let (dir, name) = stop
        .path
        .rsplit_once('/')
        .map_or(("", stop.path.as_str()), |(d, n)| (d, n));
    let dir = (!dir.is_empty()).then(|| format!("{dir}/"));
    let changed = stop.changed_since_presented.then(|| {
        text("changed since presented")
            .font(type_::LABEL)
            .color(t.warning)
    });
    row()
        .w_full()
        .items_center()
        .gap(space::S3)
        .px(12.0)
        .py(6.0)
        .fill(t.bg_elevated)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border.alpha(0.5))
        .child(
            row()
                .children(dir.map(|d| text(d).font(type_::MONO_CAPTION).color(t.text_tertiary)))
                .child(text(name).font(type_::MONO_CAPTION).color(t.text_primary)),
        )
        .child(
            text(range(stop, rows))
                .font(type_::MONO_CAPTION)
                .color(t.text_muted),
        )
        .children(changed)
        .child(col().grow(1.0))
        .children(actions)
}

/// A stop in the Walkthrough's script: where it is and what the agent says; the code is on the
/// stage beside it. The stop the stage shows is marked.
pub(super) fn script(key: &str, stop: &ReviewStop, stage: Option<Option<i64>>, t: &Tokens) -> El {
    let current = stage.is_some();
    let line = stage.flatten();
    let at =
        |n: &ReviewNote| line.is_some_and(|l| n.now_from > 0 && l >= n.now_from && l <= n.now_to);
    let notes = col().py(2.0).children(stop.notes.iter().map(|n| {
        let el = note(n, SCRIPT_NOTE_INDENT, t);
        if at(n) {
            let tint = super::cursor_tint(t);
            el.gradient(tint, tint)
        } else {
            el
        }
    }));
    let el = card(key, stop, &stop.rows, t)
        .role("listitem")
        .child(header(stop, &stop.rows, [], t))
        .children((!stop.notes.is_empty()).then_some(notes));
    if current {
        el.border_sides([1.0, 1.0, 1.0, 3.0], t.accent)
            .focus_selected(true)
    } else {
        el
    }
}

/// How far a note in the Walkthrough's script sits in, as the Tauri script's 0.5rem.
const SCRIPT_NOTE_INDENT: f32 = 7.0;

const WHO: TextStyle = TextStyle {
    weight: 400.0,
    tracking: 0.6,
    ..type_::LABEL
};

/// An agent's note on lines, `indent` points in: a "look here" note in the warning color on
/// its tint, any other in the quiet colors of the text around it.
pub(super) fn note(note: &ReviewNote, indent: f32, t: &Tokens) -> El {
    let who = if note.look { "look here" } else { "agent" };
    let (bar, label, words) = if note.look {
        (t.warning, t.warning, t.text_primary)
    } else {
        (t.border, t.text_muted, t.text_secondary)
    };
    let el = row()
        .role("note")
        .label(format!("{who} {}: {}", note_range(note), note.text))
        .mt(2.0)
        .mb(4.0)
        .ml(indent)
        .gap(space::S2)
        .px(10.0)
        .py(4.0)
        .border_sides([0.0, 0.0, 0.0, 2.0], bar)
        .child(type_::upper(who, WHO).color(label))
        .child(
            text(note_range(note))
                .font(type_::MONO_CAPTION)
                .color(t.text_muted),
        )
        .child(
            text(note.text.clone())
                .font(type_::BODY)
                .color(words)
                .selectable(true)
                .wrap()
                .grow(1.0),
        );
    if note.look {
        el.fill(t.warning.alpha(0.1))
    } else {
        el
    }
}

/// Where notes under code rows start: past both line numbers and the sign, as the Tauri
/// review's 8.25em of its code font.
pub(super) fn note_indent(t: &Tokens) -> f32 {
    NOTE_EM * code_font(t).size
}

/// The bar inside the sign column of a row in the stop's own lines.
pub(super) fn range_marker(t: &Tokens) -> (f32, Color) {
    (2.0, t.accent.alpha(0.5))
}

/// A code row's columns in its font's em, as the Tauri review's grid: each line number,
/// right-aligned half an em short of its column's end, then the sign.
const NUMBER_EM: f32 = 3.5;
const SIGN_EM: f32 = 1.25;
const NOTE_EM: f32 = 2.0 * NUMBER_EM + SIGN_EM;

fn code_font(t: &Tokens) -> TextStyle {
    TextStyle {
        size: 13.0,
        line_height: 20.0,
        ..attn_ui::kit(t).ty.mono
    }
}

/// The width of a code row's gutter: both line numbers and the sign.
pub(super) fn gutter(t: &Tokens) -> f32 {
    note_indent(t)
}

/// A row of code: its line at base, its line now, its sign with `marker`'s bar inside its left
/// edge when given, and its text in the file's colors on the row's added or removed tint.
pub(super) fn code_row(
    r: &ReviewRow,
    spans: &[Span],
    marker: Option<(f32, Color)>,
    wrap: bool,
    (labels, asked): (&[super::nav::Labeled], bool),
    t: &Tokens,
) -> El {
    let font = code_font(t);
    let em = font.size;
    let (shown, spans) = labelled(&r.text, spans, labels, t);
    let (sign, tint, kind) = match r.kind.as_str() {
        "+" => ("+", Some(t.success), "added"),
        "-" => ("−", Some(t.error), "removed"),
        _ => ("", None, "unchanged"),
    };
    let number = |n: Option<i64>| {
        row()
            .w(NUMBER_EM * em)
            .pr(0.5 * em)
            .justify_end()
            .child(
                text(n.map(|n| n.to_string()).unwrap_or_default())
                    .font(font)
                    .color(t.text_muted),
            )
            .a11y_hidden()
    };
    let sign = row()
        .w(SIGN_EM * em)
        .h_full()
        .center()
        .child(text(sign).font(font).color(t.text_muted))
        .a11y_hidden();
    let sign = match marker {
        Some((width, color)) => sign.border_sides([0.0, 0.0, 0.0, width], color),
        None => sign,
    };
    let line = r.now.or(r.old).unwrap_or_default();
    let fill = tint.map_or(Color::TRANSPARENT, |c| c.alpha(0.16));
    let mut el = row()
        .role("row")
        .label(format!("{kind} line {line}: {}", r.text))
        .w_full()
        .fill(fill)
        .child(number(r.old))
        .child(number(r.now))
        .child(sign)
        .child({
            let code = rich(shown, spans)
                .font(font)
                .color(t.text_primary)
                .selectable(true)
                .a11y_hidden();
            if wrap {
                code.wrap().grow(1.0)
            } else {
                code.grow(1.0)
            }
        });
    if !labels.is_empty() {
        let names: Vec<String> = labels
            .iter()
            .map(|l| format!("{} {}", l.label, l.name))
            .collect();
        el = el.description(format!("labels: {}", names.join(", ")));
    }
    if asked {
        el = el
            .description("asked, waiting for the answer")
            .child(text("…").font(font).color(t.accent).a11y_hidden());
    }
    el
}

/// `text` with each label drawn before its name, and `spans` moved to the text they color.
fn labelled(
    text_: &str,
    spans: &[Span],
    labels: &[super::nav::Labeled],
    t: &Tokens,
) -> (String, Vec<Span>) {
    if labels.is_empty() {
        return (text_.to_owned(), spans.to_vec());
    }
    let mut shown = String::with_capacity(text_.len() + labels.len() * 4);
    let mut inserted: Vec<(usize, usize)> = Vec::new();
    let mut from = 0;
    let mut chips = Vec::new();
    for l in labels {
        shown.push_str(&text_[from..l.byte]);
        let at = shown.len();
        shown.push_str(&l.label);
        shown.push('\u{2009}');
        inserted.push((l.byte, shown.len() - at));
        chips.push(
            Span::new(at, at + l.label.len())
                .color(t.accent)
                .weight(700.0),
        );
        from = l.byte;
    }
    shown.push_str(&text_[from..]);
    let shift = |b: u32| {
        b + inserted
            .iter()
            .filter(|(at, _)| *at as u32 <= b)
            .map(|(_, n)| *n as u32)
            .sum::<u32>()
    };
    let mut moved: Vec<Span> = spans
        .iter()
        .map(|s| {
            let mut s = s.clone();
            (s.start, s.end) = (shift(s.start), shift(s.end));
            s
        })
        .collect();
    moved.extend(chips);
    (shown, moved)
}

/// The colors of a file's lines, read from its text as one piece of its language, by row.
pub(crate) struct Colors {
    lines: Vec<Vec<Highlight>>,
    /// Each row's place, by its lines at base and now.
    at: HashMap<(Option<i64>, Option<i64>), usize>,
}

impl Colors {
    pub(crate) fn read(path: &str, rows: &[ReviewRow]) -> Colors {
        let code: Vec<&str> = rows.iter().map(|r| r.text.as_str()).collect();
        let highlights = highlight(lang_for_path(path), &code.join("\n"));
        let mut start = 0;
        let lines = code
            .iter()
            .map(|line| {
                let end = start + line.len();
                let within = highlights
                    .iter()
                    .filter(|(r, _)| r.start < end && r.end > start)
                    .map(|(r, p)| (r.start.max(start) - start..r.end.min(end) - start, *p))
                    .collect();
                start = end + 1;
                within
            })
            .collect();
        let at = rows
            .iter()
            .zip(0..)
            .map(|(r, i)| ((r.old, r.now), i))
            .collect();
        Colors { lines, at }
    }

    /// How many lines it colors, which its cache counts.
    pub(crate) fn len(&self) -> usize {
        self.lines.len()
    }

    /// The spans of row `row` in the theme of `t`.
    pub(crate) fn spans(&self, row: usize, t: &Tokens) -> Vec<Span> {
        let theme = attn_ui::colors::syntax(t.dark);
        self.lines
            .get(row)
            .map(|l| theme.spans(l, 0))
            .unwrap_or_default()
    }

    /// The spans of the row with `r`'s lines at base and now, when it colors that row.
    fn spans_of(&self, r: &ReviewRow, t: &Tokens) -> Option<Vec<Span>> {
        let row = *self.at.get(&(r.old, r.now))?;
        (self.lines.get(row).is_some()).then(|| self.spans(row, t))
    }
}
