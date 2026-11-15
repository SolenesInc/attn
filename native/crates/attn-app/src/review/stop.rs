//! A stop: lines of one file at head, shown as they are now with the change from base, and
//! the agent's notes under the lines they are about.

use attn_protocol::{ReviewNote, ReviewRow, ReviewStop};
use attn_ui::Tokens;
use rune::tok::{radius, space, type_};
use rune::{Color, El, Span, TextStyle, col, rich, row, text};
use rune_kit::syntax::{Highlight, SyntaxTheme, highlight, lang_for_path};

use std::collections::HashMap;

pub(super) fn row_key(stop: &str, row: usize) -> String {
    format!("{stop}.row.{row}")
}

/// How the stop names its lines: as they are now, or as written at head when the file is gone.
fn range(stop: &ReviewStop, rows: &[ReviewRow]) -> String {
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
    key: &str,
    stop: &ReviewStop,
    rows: &[ReviewRow],
    cursor: Option<usize>,
    file: Option<&Colors>,
    t: &Tokens,
) -> El {
    let shown: Vec<i64> = rows.iter().filter_map(|r| r.now).collect();
    let unplaced = stop
        .notes
        .iter()
        .filter(|n| !shown.contains(&n.now_to))
        .map(|n| note(n, t));
    let own = file.is_none().then(|| Colors::read(&stop.path, rows));
    let spans = |j: usize, r: &ReviewRow| match (file, &own) {
        (Some(file), _) => file.spans_of(r, t).unwrap_or_default(),
        (None, Some(own)) => own.spans(j, t),
        (None, None) => Vec::new(),
    };
    let digits = rows
        .iter()
        .flat_map(|r| [r.old, r.now])
        .flatten()
        .max()
        .unwrap_or(1)
        .to_string()
        .len();
    let lines = rows.iter().zip(0..).flat_map(|(r, j)| {
        let notes = r
            .now
            .map(|now| {
                stop.notes
                    .iter()
                    .filter(move |n| n.now_to == now)
                    .map(|n| note(n, t))
                    .collect::<Vec<_>>()
            })
            .unwrap_or_default();
        let in_range = r
            .now
            .is_some_and(|n| n >= stop.now_from && n <= stop.now_to);
        let line = code_row(r, &spans(j, r), digits, in_range, true, t).key(row_key(key, j));
        std::iter::once(super::cursor_frame(line, cursor == Some(j), t)).chain(notes)
    });
    let lines: Vec<El> = lines.collect();
    let empty = rows.is_empty().then(|| {
        text("The file is gone or empty now.")
            .font(type_::BODY)
            .color(t.text_tertiary)
    });
    col()
        .key(key.to_owned())
        .role("group")
        .label(format!("{} {}", stop.path, range(stop, rows)))
        .w_full()
        .radius(radius::MD)
        .border(1.0, t.border.alpha(0.6))
        .fill(t.bg_tile)
        .padding(space::S2)
        .gap(1.0)
        .child(header(stop, rows, t))
        .children(unplaced)
        .children(lines)
        .children(empty)
}

fn header(stop: &ReviewStop, rows: &[ReviewRow], t: &Tokens) -> El {
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
        .gap(space::S2)
        .pb(space::S1)
        .children(dir.map(|d| text(d).font(type_::LABEL).color(t.text_tertiary)))
        .child(text(name).font(type_::LABEL).color(t.text_primary))
        .child(
            text(range(stop, rows))
                .font(type_::LABEL)
                .color(t.text_secondary),
        )
        .children(changed)
}

/// A stop in the Walkthrough's script: where it is and what the agent says; the code is on the
/// stage beside it. The stop the stage shows is marked.
pub(super) fn script(key: &str, stop: &ReviewStop, stage: Option<Option<i64>>, t: &Tokens) -> El {
    let current = stage.is_some();
    let line = stage.flatten();
    let at =
        |n: &ReviewNote| line.is_some_and(|l| n.now_from > 0 && l >= n.now_from && l <= n.now_to);
    let el = col()
        .key(key.to_owned())
        .role("listitem")
        .label(format!("{} {}", stop.path, range(stop, &stop.rows)))
        .w_full()
        .radius(radius::MD)
        .border(
            1.0,
            if current {
                t.accent
            } else {
                t.border.alpha(0.6)
            },
        )
        .padding(space::S2)
        .gap(space::S1)
        .child(header(stop, &stop.rows, t))
        .children(stop.notes.iter().map(|n| {
            let el = note(n, t);
            if at(n) { el.border(1.0, t.accent) } else { el }
        }));
    if current {
        el.fill(t.bg_selected).focus_selected(true)
    } else {
        el
    }
}

pub(super) fn note(note: &ReviewNote, t: &Tokens) -> El {
    let who = if note.look { "look here" } else { "agent" };
    let tone = if note.look { t.warning } else { t.accent };
    row()
        .role("note")
        .label(format!("{who} {}: {}", note_range(note), note.text))
        .w_full()
        .gap(space::S2)
        .padding(space::S1)
        .radius(radius::SM)
        .fill(tone.alpha(0.08))
        .border_sides([0.0, 0.0, 0.0, 2.0], tone)
        .child(text(who).font(type_::LABEL).color(tone))
        .child(
            text(note_range(note))
                .font(type_::LABEL)
                .color(t.text_secondary),
        )
        .child(
            text(note.text.clone())
                .font(type_::BODY)
                .color(t.text_primary)
                .wrap()
                .grow(1.0),
        )
}

fn code_font(t: &Tokens) -> TextStyle {
    TextStyle {
        size: 13.0,
        line_height: 20.0,
        ..attn_ui::kit(t).ty.mono
    }
}

/// A row of code: its line at base, its line now, its sign and its text in the file's colors.
pub(super) fn code_row(
    r: &ReviewRow,
    spans: &[Span],
    digits: usize,
    in_range: bool,
    wrap: bool,
    t: &Tokens,
) -> El {
    let font = code_font(t);
    let number_w = (digits as f32 * 0.6 * font.size + 12.0).ceil();
    let (sign, tint, kind) = match r.kind.as_str() {
        "+" => ("+", Some(t.success), "added"),
        "-" => ("−", Some(t.error), "removed"),
        _ => ("", None, "unchanged"),
    };
    let number = |n: Option<i64>| {
        text(n.map(|n| n.to_string()).unwrap_or_default())
            .font(font)
            .color(t.text_tertiary)
            .w(number_w)
            .a11y_hidden()
    };
    let line = r.now.or(r.old).unwrap_or_default();
    let fill = tint.map_or(Color::TRANSPARENT, |c| c.alpha(0.12));
    let mut el = row()
        .role("row")
        .label(format!("{kind} line {line}: {}", r.text))
        .w_full()
        .fill(fill)
        .child(number(r.old))
        .child(number(r.now))
        .child(
            text(sign)
                .font(font)
                .color(tint.unwrap_or(t.text_tertiary))
                .w(14.0)
                .a11y_hidden(),
        )
        .child({
            let code = rich(r.text.clone(), spans.to_vec())
                .font(font)
                .color(t.text_primary)
                .a11y_hidden();
            if wrap {
                code.wrap().grow(1.0)
            } else {
                code.grow(1.0)
            }
        });
    if !in_range {
        el = el.group_opacity(0.75, 0.75);
    }
    el
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
        let theme = SyntaxTheme::from_kit(attn_ui::kit(t));
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
