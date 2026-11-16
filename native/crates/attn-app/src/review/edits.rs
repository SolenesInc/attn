//! Edits the user makes to a review's files: what an edit at the cursor takes, and where a
//! saved edit stands in the file as it is now.

use attn_protocol::{
    MarkdownAnnotation, MarkdownAnnotationAnchor, ReviewEditMessage, ReviewEditResultMessage,
    ReviewRow,
};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, TextEdit, Tx, col, text};
use rune_kit::TextArea;
use rune_kit::syntax::{Paint, Syntax, highlight, lang_for_path};

use crate::State;

/// What an edit takes: the selected lines, a whole string or comment of several lines, or the
/// cursor's line.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum UnitKind {
    Selection,
    String,
    Comment,
    Line,
}

/// Lines of a file, 0-based and inclusive, that one edit takes.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) struct Unit {
    pub(crate) kind: UnitKind,
    pub(crate) from: usize,
    pub(crate) to: usize,
}

/// What an edit at line `cursor` of `lines`, a file at `path`, takes: the lines of
/// `selection`, else a whole string or comment that spans several lines around the cursor,
/// else the cursor's line. The extent of a string or comment comes from the language's
/// highlighting.
pub(crate) fn unit(
    lines: &[&str],
    path: &str,
    cursor: usize,
    selection: Option<(usize, usize)>,
) -> Unit {
    if let Some((a, b)) = selection {
        return Unit {
            kind: UnitKind::Selection,
            from: a.min(b),
            to: a.max(b),
        };
    }
    let line = Unit {
        kind: UnitKind::Line,
        from: cursor,
        to: cursor,
    };
    let Some(lang) = lang_for_path(path) else {
        return line;
    };
    let code = lines.join("\n");
    let mut starts = Vec::with_capacity(lines.len());
    let mut at = 0;
    for l in lines {
        starts.push(at);
        at += l.len() + 1;
    }
    let kinds = highlight(Some(lang), &code);
    let kind_at = |b: usize| {
        kinds.iter().find_map(|(r, p)| match p {
            Paint::Kind(k) if r.start <= b && b < r.end => Some(*k),
            _ => None,
        })
    };
    let words = |i: usize| -> Vec<usize> {
        lines[i]
            .char_indices()
            .filter(|(_, c)| !c.is_whitespace())
            .map(|(o, _)| starts[i] + o)
            .collect()
    };
    let first = |i: usize| words(i).first().and_then(|&b| kind_at(b));
    let last = |i: usize| words(i).last().and_then(|&b| kind_at(b));
    let holds = |i: usize, k: Syntax| words(i).iter().any(|&b| kind_at(b) == Some(k));
    let next_words = |i: usize| (i + 1..lines.len()).find(|&n| !words(n).is_empty());
    let runs_on = |i: usize| -> Option<UnitKind> {
        let next = next_words(i)?;
        match first(next) {
            Some(Syntax::String) if holds(i, Syntax::String) => Some(UnitKind::String),
            Some(Syntax::Comment) if last(i) == Some(Syntax::Comment) => Some(UnitKind::Comment),
            _ => None,
        }
    };
    let mut open: Vec<Option<UnitKind>> = Vec::with_capacity(lines.len());
    for i in 0..lines.len() {
        let here = if words(i).is_empty() && i > 0 {
            open[i - 1]
        } else {
            runs_on(i)
        };
        open.push(here);
    }
    if cursor >= lines.len() {
        return line;
    }
    let mut from = cursor;
    while from > 0 && open[from - 1].is_some() {
        from -= 1;
    }
    let mut to = cursor;
    while to + 1 < lines.len() && open[to].is_some() {
        to += 1;
    }
    if to > from
        && let Some(kind) = open[from]
    {
        return Unit { kind, from, to };
    }
    let comment_only = |i: usize| {
        let w = words(i);
        !w.is_empty() && w.iter().all(|&b| kind_at(b) == Some(Syntax::Comment))
    };
    if cursor >= lines.len() || !comment_only(cursor) {
        return line;
    }
    let mut from = cursor;
    while from > 0 && comment_only(from - 1) {
        from -= 1;
    }
    let mut to = cursor;
    while to + 1 < lines.len() && comment_only(to + 1) {
        to += 1;
    }
    if to > from {
        Unit {
            kind: UnitKind::Comment,
            from,
            to,
        }
    } else {
        line
    }
}

/// Where an edit of the 0-based lines `from` to `to` stands: between the nearest lines at head
/// above and below it, 0 where there is none.
pub(crate) fn where_it_stands(heads: &[Option<i64>], from: usize, to: usize) -> (i64, i64) {
    let above = heads[..from].iter().rev().find_map(|h| *h).unwrap_or(0);
    let below = heads
        .get(to + 1..)
        .into_iter()
        .flatten()
        .find_map(|h| *h)
        .unwrap_or(0);
    (above, below)
}

/// Where an edit's text `now` starts now, between the lines at head that hold it in: after the
/// line it followed (`after`) and before the one it preceded (`before`). Moved when the text is
/// not there exactly once; unknown when `rows` do not reach that span and are not the whole
/// file.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Stands {
    At(i64),
    Moved,
    Unknown,
}

pub(crate) fn start(
    rows: &[ReviewRow],
    (after, before): (i64, i64),
    now: &str,
    whole: bool,
) -> Stands {
    let unseen = if whole {
        Stands::Moved
    } else {
        Stands::Unknown
    };
    let mut text_at = std::collections::HashMap::new();
    let (mut low, mut high, mut last) = (0, i64::MAX, 0);
    for r in rows {
        let Some(n) = r.now else { continue };
        text_at.insert(n, r.text.trim_end_matches('\r'));
        last = last.max(n);
        let Some(h) = r.head else { continue };
        if h <= after {
            low = low.max(n);
        }
        if before > 0 && h >= before {
            high = high.min(n);
        }
    }
    let end = if high == i64::MAX {
        if whole { last + 1 } else { return unseen }
    } else {
        high
    };
    if !whole && after > 0 && low == 0 {
        return unseen;
    }
    let wanted: Vec<&str> = now.split('\n').collect();
    let mut found = Vec::new();
    let mut s = low + 1;
    while s + wanted.len() as i64 <= end {
        let texts: Vec<Option<&&str>> = (0..wanted.len() as i64)
            .map(|i| text_at.get(&(s + i)))
            .collect();
        if texts.iter().any(Option::is_none) {
            return unseen;
        }
        if texts
            .iter()
            .zip(&wanted)
            .all(|(t, w)| t.is_some_and(|t| t == w))
        {
            found.push(s);
        }
        s += 1;
    }
    if found.len() == 1 {
        Stands::At(found[0])
    } else {
        Stands::Moved
    }
}

/// Where an edit's mark shows: under the line now that holds the last line of its text.
pub(crate) fn place(rows: &[ReviewRow], stands: (i64, i64), now: &str, whole: bool) -> Stands {
    match start(rows, stands, now, whole) {
        Stands::At(s) => Stands::At(s + now.split('\n').count() as i64 - 1),
        other => other,
    }
}

/// An edit being written: the lines of a file it takes, 0-based, read with the file's hash.
pub(crate) struct Open {
    pub(crate) tile: String,
    pub(crate) source: (Option<String>, String),
    pub(crate) path: String,
    pub(crate) hash: String,
    pub(crate) lines: Vec<String>,
    pub(crate) heads: Vec<Option<i64>>,
    pub(crate) unit: Unit,
    pub(crate) doc_line: i64,
    pub(crate) problem: Option<String>,
    /// The id of the edit this box is saving, until the daemon answers.
    pub(crate) saving: Option<String>,
    /// The draft the edit is listed in.
    pub(crate) uri: String,
}

impl Open {
    fn text(&self) -> String {
        self.lines[self.unit.from..=self.unit.to].join("\n")
    }

    fn several(&self) -> bool {
        self.unit.to > self.unit.from
    }

    fn describe(&self) -> String {
        let count = self.unit.to - self.unit.from + 1;
        match self.unit.kind {
            UnitKind::String => format!("this string, {count} lines"),
            UnitKind::Comment => format!("this comment, {count} lines"),
            _ if count > 1 => format!("lines {}–{}", self.unit.from + 1, self.unit.to + 1),
            _ => format!("line {}", self.unit.from + 1),
        }
    }
}

pub(crate) const FIELD: &str = "review.edit.field";

/// Reads file `path` of the review on `source` and opens an edit of what line `line` now (or
/// the lines `selection` now) takes.
#[allow(clippy::too_many_arguments)]
pub(crate) fn begin(
    state: &mut State,
    tile: &str,
    source: (Option<String>, String),
    path: &str,
    (line, selection): (i64, Option<(i64, i64)>),
    doc_line: i64,
    uri: String,
    tx: &mut Tx,
) {
    let (host, document) = source.clone();
    let (tile, path_) = (tile.to_owned(), path.to_owned());
    super::file::read(
        state,
        (host, document, path.to_owned()),
        move |state, read, tx| {
            let m = match read {
                Ok(m) => m,
                Err(why) => {
                    state
                        .window
                        .notify(format!("Could not read {path_}: {why}"), tx);
                    return;
                }
            };
            let now: Vec<&ReviewRow> = m
                .rows
                .iter()
                .flatten()
                .filter(|r| r.now.is_some())
                .collect();
            let lines: Vec<String> = now
                .iter()
                .map(|r| r.text.trim_end_matches('\r').to_owned())
                .collect();
            let heads: Vec<Option<i64>> = now.iter().map(|r| r.head).collect();
            let index = |l: i64| now.iter().position(|r| r.now == Some(l));
            let Some(cursor) = index(line) else {
                return;
            };
            let selection = selection.and_then(|(a, b)| Some((index(a)?, index(b)?)));
            let refs: Vec<&str> = lines.iter().map(String::as_str).collect();
            let unit = unit(&refs, &path_, cursor, selection);
            let open = Open {
                tile,
                source,
                path: path_,
                hash: m.hash.unwrap_or_default(),
                lines,
                heads,
                unit,
                doc_line,
                problem: None,
                saving: None,
                uri,
            };
            state.readings.reviews.edit_text = TextEdit::new(open.text());
            state.readings.reviews.editing = Some(open);
            tx.focus(FIELD, false);
            state.readings.revision += 1;
        },
        tx,
    );
}

fn close(state: &mut State, tx: &mut Tx) {
    state.readings.reviews.editing = None;
    tx.blur();
    state.readings.revision += 1;
}

fn field(state: &mut State) -> &mut TextEdit {
    &mut state.readings.reviews.edit_text
}

/// The box's keys: Enter saves one line and ⌘Enter (Ctrl+Enter on Linux) several, ⌥↑ and ⌥↓
/// take in the line above or below, Escape cancels.
fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let Some(open) = state.readings.reviews.editing.as_mut() else {
        return false;
    };
    if open.saving.is_some() {
        return matches!(ev.key, Key::Escape | Key::Enter) || ev.mods.alt;
    }
    match ev.key {
        Key::Escape => {
            close(state, tx);
            true
        }
        Key::Up | Key::Down if ev.mods.alt => {
            let up = ev.key == Key::Up;
            let text = &mut state.readings.reviews.edit_text;
            if up && open.unit.from > 0 {
                open.unit.from -= 1;
                let taken = format!("{}\n{}", open.lines[open.unit.from], text.text);
                *text = TextEdit::new(taken);
            } else if !up && open.unit.to + 1 < open.lines.len() {
                open.unit.to += 1;
                let taken = format!("{}\n{}", text.text, open.lines[open.unit.to]);
                *text = TextEdit::new(taken);
            }
            state.readings.revision += 1;
            true
        }
        Key::Enter if !ev.mods.shift && (!open.several() || ev.mods.cmd || ev.mods.ctrl) => {
            save(state, tx);
            true
        }
        _ => false,
    }
}

/// Writes the box's text over the lines it took, against the hash they were read with, and
/// lists the edit in the review. A file that changed underneath writes nothing. The answer
/// lands whether or not the box is still open; it touches the box only while that box is the
/// one saving this edit.
fn save(state: &mut State, tx: &mut Tx) {
    let Some(open) = state.readings.reviews.editing.as_mut() else {
        return;
    };
    let text = state.readings.reviews.edit_text.text.clone();
    let was = open.text();
    if text == was || open.saving.is_some() {
        if text == was {
            close(state, tx);
        }
        return;
    }
    let (after, before) = where_it_stands(&open.heads, open.unit.from, open.unit.to);
    let mut mark = MarkdownAnnotation {
        id: format!("review-edit-{}", uuid::Uuid::new_v4()),
        r#type: "edit".into(),
        anchor: Some(MarkdownAnnotationAnchor {
            start_line: open.doc_line,
            end_line: open.doc_line,
            exact: text.clone(),
            file: Some(open.path.clone()),
            file_start_line: Some(open.unit.from as i64 + 1),
            file_end_line: Some(open.unit.from as i64 + text.split('\n').count() as i64),
            ..MarkdownAnnotationAnchor::default()
        }),
        edit_was: Some(was),
        edit_now: Some(text.clone()),
        edit_after_head: Some(after),
        edit_before_head: Some(before),
        ..MarkdownAnnotation::default()
    };
    open.saving = Some(mark.id.clone());
    open.problem = None;
    let (host, document) = open.source.clone();
    let (path, hash, from, to) = (
        open.path.clone(),
        open.hash.clone(),
        open.unit.from,
        open.unit.to,
    );
    let (uri, tile, source) = (open.uri.clone(), open.tile.clone(), open.source.clone());
    state.readings.revision += 1;
    let asked_path = path.clone();
    state.ask(
        |id| ReviewEditMessage {
            host,
            document,
            path: asked_path,
            base_hash: hash,
            from: from as i64 + 1,
            to: to as i64 + 1,
            text,
            request_id: Some(id),
            ..ReviewEditMessage::new()
        },
        move |state, answer, tx| {
            let result = answer
                .outcome()
                .and_then(|r| r.parse::<ReviewEditResultMessage>().ok_or_else(|| "the daemon's answer did not parse".into()));
            let shown = state
                .readings
                .reviews
                .editing
                .as_ref()
                .is_some_and(|o| o.saving.as_deref() == Some(mark.id.as_str()));
            let problem = match result {
                Ok(r) if !r.conflict => {
                    mark.created_at = state.wall_ms(tx);
                    let now = tx.clock();
                    if let Some(draft) = state.drafts.get_mut(&uri) {
                        draft.marks.push(mark);
                        draft.changed(now);
                    }
                    if shown {
                        close(state, tx);
                    }
                    super::reread(state, &tile, source, &path, tx);
                    return;
                }
                Ok(_) if shown => format!(
                    "{path} changed since this edit opened, so nothing was written. Press esc, then i to edit it as it is now."
                ),
                Ok(_) => format!(
                    "{path} changed since this edit opened, so nothing was written. Press i to edit it as it is now."
                ),
                Err(why) if shown => why,
                Err(why) => format!("Could not save the edit to {path}: {why}"),
            };
            match state.readings.reviews.editing.as_mut().filter(|_| shown) {
                Some(open) => {
                    open.saving = None;
                    open.problem = Some(problem);
                    state.readings.revision += 1;
                }
                None => state.window.notify(problem, tx),
            }
        },
        tx,
    );
}

/// The edit box over its tile.
pub(crate) fn view(open: &Open, words: &TextEdit, t: &Tokens) -> El {
    let area = TextArea::new(words)
        .key(FIELD)
        .width(None)
        .label("Edit")
        .on_edit(field)
        .on_key(on_key)
        .build(attn_ui::kit(t));
    let save = if open.several() {
        rune::Chord::parse("mod+enter")
            .map(|c| c.label_on(rune::KeyPlatform::current()))
            .unwrap_or_default()
    } else {
        "↵".to_owned()
    };
    let problem = open.problem.clone().map(|p| {
        text(p)
            .role("alert")
            .font(type_::LABEL)
            .color(t.error)
            .wrap()
    });
    let panel = col()
        .key("review.edit")
        .role("dialog")
        .label("Edit the file")
        .modal_tx(true, close)
        .w_full()
        .max_w(720.0)
        .padding(space::S3)
        .gap(space::S2)
        .child(text(format!("Editing {}: {}", open.path, open.describe())).font(type_::LABEL).color(t.text_secondary))
        .child(area)
        .children(problem)
        .child(
            text(format!("{save} save · ⌥↑ ⌥↓ take in a line · esc cancel · saved to the file and listed in the review"))
                .font(type_::LABEL)
                .color(t.text_tertiary)
                .wrap(),
        );
    rune_kit::popover::raised(attn_ui::kit(t), panel, 8.0)
}

/// Undoes the newest edit `mark` of file `path`: reads the file, finds the edit's text where it
/// stands now and writes its old text back, then drops it from the review.
pub(crate) fn undo(
    state: &mut State,
    (tile, source, uri): (String, (Option<String>, String), String),
    mark: MarkdownAnnotation,
    tx: &mut Tx,
) {
    let Some(path) = mark.anchor.as_ref().and_then(|a| a.file.clone()) else {
        return;
    };
    let (host, document) = source.clone();
    let p = path.clone();
    super::file::read(
        state,
        (host.clone(), document.clone(), path.clone()),
        move |state, read, tx| {
            let m = match read {
                Ok(m) => m,
                Err(why) => {
                    state
                        .window
                        .notify(format!("Could not undo the edit: {why}"), tx);
                    return;
                }
            };
            let now = mark.edit_now.clone().unwrap_or_default();
            let stands = (
                mark.edit_after_head.unwrap_or(0),
                mark.edit_before_head.unwrap_or(0),
            );
            let Stands::At(at) = start(m.rows.as_deref().unwrap_or_default(), stands, &now, true)
            else {
                state.window.notify(
                    format!(
                        "Your edit is no longer where you saved it in {p}, so nothing was undone."
                    ),
                    tx,
                );
                return;
            };
            let to = at + now.split('\n').count() as i64 - 1;
            let (id, was) = (mark.id.clone(), mark.edit_was.clone().unwrap_or_default());
            let asked_path = p.clone();
            state.ask(
                |rid| ReviewEditMessage {
                    host,
                    document,
                    path: asked_path,
                    base_hash: m.hash.clone().unwrap_or_default(),
                    from: at,
                    to,
                    text: was,
                    request_id: Some(rid),
                    ..ReviewEditMessage::new()
                },
                move |state, answer, tx| match answer.outcome().and_then(|r| {
                    r.parse::<ReviewEditResultMessage>()
                        .ok_or_else(|| "the daemon's answer did not parse".to_owned())
                }) {
                    Ok(r) if r.conflict => {
                        state.window.notify(
                            format!(
                                "{p} changed while undoing, so nothing was written. Press u again."
                            ),
                            tx,
                        );
                    }
                    Ok(_) => {
                        let now = tx.clock();
                        if let Some(draft) = state.drafts.get_mut(&uri) {
                            draft.marks.retain(|m| m.id != id);
                            draft.changed(now);
                        }
                        super::reread(state, &tile, source, &p, tx);
                    }
                    Err(why) => state
                        .window
                        .notify(format!("Could not undo the edit: {why}"), tx),
                },
                tx,
            );
        },
        tx,
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    const GO: &[&str] = &[
        "package store",
        "",
        "const listPRs = `",
        "SELECT id, title",
        "",
        "FROM prs",
        "WHERE host = ?`",
        "",
        "// ReplacePRs swaps the hosts' pull requests",
        "// and says which changed.",
        "func ReplacePRs() {",
        "\t/* the two reads",
        "\t   were one */",
        "\tx := \"one line\" // trailing",
        "}",
    ];

    const TS: &[&str] = &[
        "const query = `",
        "  select ${table}",
        "  where id = 1`;",
        "/**",
        " * Documented.",
        " */",
        "export const n = 1;",
    ];

    /// A case's name, file, path, cursor and selection, and the unit wanted.
    type Case<'a> = (
        &'a str,
        &'a [&'a str],
        &'a str,
        usize,
        Option<(usize, usize)>,
        Unit,
    );

    fn unit_of(kind: UnitKind, from: usize, to: usize) -> Unit {
        Unit { kind, from, to }
    }

    #[test]
    fn the_edit_unit_at_the_cursor() {
        use UnitKind::*;
        let settings: Vec<std::string::String> =
            (0..4000).map(|i| format!("key{i}: value {i}")).collect();
        let settings: Vec<&str> = settings.iter().map(std::string::String::as_str).collect();
        let cases: &[Case] = &[
            (
                "a raw string in Go, from inside it",
                GO,
                "store.go",
                3,
                None,
                unit_of(String, 2, 6),
            ),
            (
                "a raw string in Go, from a blank line inside it",
                GO,
                "store.go",
                4,
                None,
                unit_of(String, 2, 6),
            ),
            (
                "a raw string in Go, from the line that opens it",
                GO,
                "store.go",
                2,
                None,
                unit_of(String, 2, 6),
            ),
            (
                "line comments of several lines in Go",
                GO,
                "store.go",
                9,
                None,
                unit_of(Comment, 8, 9),
            ),
            (
                "a block comment of several lines in Go",
                GO,
                "store.go",
                12,
                None,
                unit_of(Comment, 11, 12),
            ),
            (
                "a line with a one-line string and a trailing comment",
                GO,
                "store.go",
                13,
                None,
                unit_of(Line, 13, 13),
            ),
            (
                "a single line of code",
                GO,
                "store.go",
                10,
                None,
                unit_of(Line, 10, 10),
            ),
            (
                "a template literal in TypeScript",
                TS,
                "query.ts",
                1,
                None,
                unit_of(String, 0, 2),
            ),
            (
                "a doc comment in TypeScript",
                TS,
                "query.ts",
                4,
                None,
                unit_of(Comment, 3, 5),
            ),
            (
                "a YAML value in a long file of them",
                &settings,
                "settings.yaml",
                2000,
                None,
                unit_of(Line, 2000, 2000),
            ),
            (
                "selected lines",
                GO,
                "store.go",
                10,
                Some((13, 10)),
                unit_of(Selection, 10, 13),
            ),
            (
                "a language the highlighter does not know",
                &["`", "a", "`"],
                "notes.unknownlang",
                1,
                None,
                unit_of(Line, 1, 1),
            ),
        ];
        for (name, lines, path, cursor, selection, want) in cases {
            assert_eq!(unit(lines, path, *cursor, *selection), *want, "{name}");
        }
    }
}
