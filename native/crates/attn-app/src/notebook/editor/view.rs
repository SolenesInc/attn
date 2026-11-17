//! How the editor draws: one row per line, built only near the viewport, each a `rich()` text
//! laid out on its own; the caret, the selection and find's matches drawn over the rows; rules,
//! images and the front matter card as rows of their own.

use std::borrow::Cow;

use attn_ui::Tokens;
use attn_ui::type_;
use rune::text::{Span, TextHighlight};
use rune::tok::space;
use rune::{Color, El, GrowthSpec, Phase, Tx, col, el, rich, row, text};

use super::markdown::{LineKind, Shown, Style};
use super::{Editor, Pos};
use crate::State;

/// Rows not built yet are this tall until they are: one line of body text.
const ESTIMATED_ROW: f32 = 23.0;
/// An image not read yet holds this much room, as the Tauri editor estimates it.
const IMAGE_ESTIMATE: f32 = 220.0;

/// How the editor finds itself in the window's state from a handler.
pub(crate) type Access = for<'a> fn(&'a mut State, &str) -> Option<&'a mut Editor>;
/// How the editor finds itself in the window's state to build a row.
pub(crate) type Read = for<'a> fn(&'a State, &str) -> Option<&'a Editor>;

/// The editor keyed `key`, labelled `label` for assistive technology. Rows build only near the
/// viewport, each keyed by its line's id so a row keeps its layout while lines come and go.
pub(crate) fn view(
    editor: &Editor,
    key: &str,
    label: &str,
    (read, access): (Read, Access),
    t: &Tokens,
) -> El {
    let (row_key, build_key) = (key.to_owned(), key.to_owned());
    let ids = editor.doc.ids();
    let tokens = *t;
    let rows = col()
        .key(format!("{key}.rows"))
        .w_full()
        .growth(GrowthSpec::default())
        .virtual_rows(
            editor.doc.len(),
            ESTIMATED_ROW,
            move |_: &State, i| -> Cow<'_, str> {
                Cow::Owned(format!(
                    "{row_key}.line.{}",
                    ids.get(i).copied().unwrap_or(i as u64)
                ))
            },
            move |state: &State, i| match read(state, &build_key) {
                Some(editor) if i < editor.doc.len() => {
                    let place =
                        crate::notebook::place_of_editor(state, &build_key).unwrap_or_default();
                    line_row(
                        editor,
                        (&state.pictures, &place),
                        &build_key,
                        i,
                        access,
                        &tokens,
                    )
                }
                _ => el(),
            },
        )
        .virtual_rows_version(editor.doc.structure);
    let key_for_keys = key.to_owned();
    let key_for_width = key.to_owned();
    let key_for_outside = key.to_owned();
    col()
        .key(key.to_owned())
        .role("textbox")
        .label(label)
        .a11y_value(editor.text())
        .focusable(true)
        .w_full()
        .on_press_outside(move |state: &mut State, tx: &mut Tx| {
            let mine = tx.focused() == Some(key_for_outside.as_str());
            if let Some(editor) = access(state, &key_for_outside)
                && editor.focused
            {
                editor.focused = false;
                editor.preedit = None;
                if mine {
                    tx.blur();
                }
                state.notebook.revision += 1;
            }
        })
        .on_layout(move |state: &mut State, r: [f32; 4]| {
            let moved = read(state, &key_for_width).is_some_and(|e| (e.width - r[2]).abs() > 0.5);
            if moved && let Some(editor) = access(state, &key_for_width) {
                editor.width = r[2];
            }
        })
        .on_key(move |state: &mut State, ev, tx: &mut Tx| {
            let now = tx.clock();
            let Some(editor) = access(state, &key_for_keys) else {
                return false;
            };
            editor.focused = true;
            if matches!(ev.key, rune::Key::Command('f')) && !ev.mods.shift {
                super::find::open(editor, &key_for_keys, tx);
                state.notebook.revision += 1;
                return true;
            }
            let handled = super::on_key(editor, ev, now, tx);
            if handled {
                crate::notebook::edited_editor(state, &key_for_keys, tx);
            }
            handled
        })
        .child(rows)
}

fn line_row(
    editor: &Editor,
    (pictures, place): (&crate::markdown::Pictures, &(Option<String>, String)),
    key: &str,
    i: usize,
    access: Access,
    t: &Tokens,
) -> El {
    let row_key = format!("{key}.line.{}", editor.doc.id(i));
    match &editor.kinds()[i] {
        LineKind::FrontMatter { card: true, first } if !editor.editing_front_matter() => {
            if *first {
                front_matter_card(editor, key, access, t).key(row_key)
            } else {
                el().key(row_key).h(0.0)
            }
        }
        LineKind::Rule if !editor.active(i) => col()
            .key(row_key)
            .w_full()
            .h(ESTIMATED_ROW)
            .justify_center()
            .child(el().w_full().h(1.0).fill(t.border))
            .with_press(key, i, access),
        LineKind::Table { first, lines } if !editor.touches(lines) => {
            if *first {
                table_grid(editor, key, lines.start, access, t).key(row_key)
            } else {
                el().key(row_key).h(0.0)
            }
        }
        LineKind::Image { alt, src } if !editor.active(i) => {
            image_row(editor, (pictures, place), alt, src, t)
                .key(row_key)
                .with_press(key, i, access)
        }
        LineKind::Fence | LineKind::Code | LineKind::FrontMatter { .. } => {
            let line = editor.doc.line(i);
            let shown = Shown::raw(line);
            let color = if matches!(editor.kinds()[i], LineKind::Fence) {
                t.text_tertiary
            } else {
                t.text_primary
            };
            text_row(
                editor,
                key,
                i,
                &shown,
                vec![Span::new(0, line.len()).mono()],
                super::BODY,
                color,
                access,
                t,
            )
            .key(row_key)
        }
        _ => {
            let shown = editor.shown(i);
            let (_, style) = editor.styled(&shown);
            let spans = with_theme(editor, &shown, &place.1, t);
            text_row(
                editor,
                key,
                i,
                &shown,
                spans,
                style,
                t.text_primary,
                access,
                t,
            )
            .key(row_key)
            .description(if editor.broken_on(&shown, &place.1) {
                "Link target not found in the notebook"
            } else {
                ""
            })
            .children(shown.quote.then(|| {
                el().absolute(-space::S2, 0.0)
                    .w(2.0)
                    .h(ESTIMATED_ROW)
                    .fill(t.border)
            }))
        }
    }
}

/// The spans that draw a line's styles: weight, slant, type, colour and size.
pub(crate) fn spans(shown: &Shown, t: Option<&Tokens>) -> Vec<Span> {
    let mut spans = Vec::new();
    for (range, style) in &shown.styles {
        let span = Span::new(range.start, range.end);
        let span = match style {
            Style::Strong => span.bold(),
            Style::Emphasis => span.italic(),
            Style::Code => span.mono(),
            Style::Strike => span,
            Style::Link => match t {
                Some(t) => span.color(t.accent).underline(t.accent),
                None => span,
            },
            Style::Mark => match t {
                Some(t) => span.color(t.text_tertiary),
                None => span,
            },
            Style::Heading(_) => span.bold(),
        };
        spans.push(span);
    }
    spans
}

impl Editor {
    /// Whether a link the line shows points at a note the daemon said is missing.
    fn broken_on(&self, shown: &Shown, dir: &str) -> bool {
        shown
            .links
            .iter()
            .any(|(_, href)| Editor::note_of(href, dir).is_some_and(|p| self.missing.contains(&p)))
    }
}

/// The line's spans in the window's colours, with a link to a missing note marked broken.
fn with_theme(editor: &Editor, shown: &Shown, dir: &str, t: &Tokens) -> Vec<Span> {
    let mut out = spans(shown, Some(t));
    for (range, href) in &shown.links {
        if Editor::note_of(href, dir).is_some_and(|p| editor.missing.contains(&p)) {
            out.push(
                Span::new(range.start, range.end)
                    .color(t.error)
                    .underline(t.error),
            );
        }
    }
    out
}

#[allow(clippy::too_many_arguments)]
fn text_row(
    editor: &Editor,
    key: &str,
    i: usize,
    shown: &Shown,
    spans: Vec<Span>,
    style: rune::TextStyle,
    color: Color,
    access: Access,
    t: &Tokens,
) -> El {
    let caret_here = editor.focused && editor.doc.caret.line == i;
    let (a, b) = editor.doc.range();
    let mut highlights = Vec::new();
    if a != b && a.line <= i && i <= b.line {
        let from = if a.line == i {
            shown.to_display(a.col)
        } else {
            0
        };
        let to = if b.line == i {
            shown.to_display(b.col)
        } else {
            shown.text.len()
        };
        if to > from {
            highlights.push(TextHighlight::new(from..to, selection_color(t)));
        }
    }
    if let Some(find) = editor.query() {
        for m in super::find::find_in(&shown.text, find) {
            highlights.push(TextHighlight::new(m, find_color(t)));
        }
    }
    let (display, spans) = match (&editor.preedit, caret_here) {
        (Some(preedit), true) => {
            let at = shown.to_display(editor.doc.caret.col);
            let mut text = shown.text.clone();
            text.insert_str(at, preedit);
            let mut spans: Vec<Span> = spans
                .into_iter()
                .map(|mut s| {
                    if s.start as usize >= at {
                        s.start += preedit.len() as u32;
                    }
                    if s.end as usize > at {
                        s.end += preedit.len() as u32;
                    }
                    s
                })
                .collect();
            spans.push(Span::new(at, at + preedit.len()).underline(t.text_primary));
            (text, spans)
        }
        _ => (shown.text.clone(), spans),
    };
    let body = rich(display, spans)
        .font(style)
        .color(color)
        .wrap()
        .w_full()
        .text_highlights(highlights);
    let mut line = col().w_full().child(body).with_press(key, i, access);
    if caret_here {
        let r = editor.caret_rect();
        let x = match &editor.preedit {
            Some(p) => r[0] + rune::text::advance(p, style),
            None => r[0],
        };
        line = line.child(
            el().key(format!("{key}.caret"))
                .absolute(x, r[1])
                .w(1.5)
                .h(r[3].max(style.line_px() * 0.8))
                .fill(t.text_primary)
                .ime_cursor(),
        );
    }
    line
}

fn selection_color(t: &Tokens) -> Color {
    t.accent.alpha(0.3)
}

fn find_color(t: &Tokens) -> Color {
    t.warning.alpha(0.25)
}

trait Press {
    fn with_press(self, key: &str, i: usize, access: Access) -> El;
}

impl Press for El {
    /// A press in row `i` places the caret where it lands; a drag extends the selection; two
    /// presses select the word and three the line. ⌘ or Ctrl with a press follows a link.
    fn with_press(self, key: &str, i: usize, access: Access) -> El {
        let key = key.to_owned();
        self.on_drag(move |state: &mut State, d, tx: &mut Tx| {
            let follow_link = matches!(d.phase, Phase::Down) && (tx.mods().cmd || tx.mods().ctrl);
            let Some(editor) = access(state, &key) else {
                return;
            };
            if matches!(d.phase, Phase::Down) && editor.toggle_task(i, d.x, d.y, tx.clock()) {
                editor.toggling = true;
                crate::notebook::edited_editor(state, &key, tx);
                return;
            }
            if editor.toggling {
                editor.toggling = !matches!(d.phase, Phase::Up);
                return;
            }
            let at = editor.hit(i, d.x, d.y);
            if follow_link && let Some(href) = editor.link_at(i, d.x, d.y) {
                crate::notebook::follow_link(state, &key, &href, tx);
                return;
            }
            match (d.phase, d.clicks) {
                (Phase::Down, 2) => {
                    let (a, b) = editor.word_at(at);
                    editor.doc.select(a, b);
                }
                (Phase::Down, n) if n >= 3 => {
                    let len = editor.doc.line(i).len();
                    editor.doc.select(Pos::new(i, 0), Pos::new(i, len));
                }
                (Phase::Down, _) => editor.move_to(at, tx.mods().shift, false),
                _ if d.clicks <= 1 => editor.move_to(at, true, false),
                _ => {}
            }
            editor.focused = true;
            tx.focus(key.clone(), false);
            crate::notebook::edited_editor(state, &key, tx);
        })
    }
}

/// The table starting at line `start` as a grid: a press on the header puts the caret on the
/// table's first line, a press on a row on that row's line, which shows the table's source.
fn table_grid(editor: &Editor, key: &str, start: usize, access: Access, t: &Tokens) -> El {
    let Some(table) = super::table::table_at(editor.doc.lines(), start) else {
        return el();
    };
    let columns = table.header.len();
    let align = |a: super::table::Align| match a {
        super::table::Align::Right => rune::Align::End,
        super::table::Align::Center => rune::Align::Center,
        _ => rune::Align::Start,
    };
    let row_of = |cells: &[String], line: usize, header: bool| {
        let key = key.to_owned();
        row()
            .key(format!("{key}.table.{line}"))
            .w_full()
            .equal_columns(columns)
            .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
            .children(cells.iter().take(columns).enumerate().map(|(c, cell)| {
                text(cell.clone())
                    .font(if header { type_::TITLE } else { type_::BODY })
                    .color(t.text_primary)
                    .align(align(
                        table
                            .align
                            .get(c)
                            .copied()
                            .unwrap_or(super::table::Align::None),
                    ))
                    .wrap()
                    .px(space::S2)
                    .py(space::S1)
                    .grid_cell(c)
            }))
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                if let Some(editor) = access(state, &key) {
                    editor.focused = true;
                    editor.move_to(Pos::new(line, 0), false, false);
                    tx.focus(key.clone(), false);
                }
                crate::notebook::edited_editor(state, &key, tx);
            })
    };
    col()
        .role("table")
        .label("Table")
        .w_full()
        .my(space::S1)
        .radius(4.0)
        .border(1.0, t.border_subtle)
        .child(row_of(&table.header, start, true))
        .children(
            table
                .rows
                .iter()
                .enumerate()
                .map(|(r, cells)| row_of(cells, start + 2 + r, false)),
        )
}

/// An image alone on its line: the picture, fitted to the note's width, or what it is and that
/// it could not be found.
fn image_row(
    editor: &Editor,
    (pictures, (root, dir)): (&crate::markdown::Pictures, &(Option<String>, String)),
    alt: &str,
    src: &str,
    t: &Tokens,
) -> El {
    let name = if alt.is_empty() { src } else { alt }.to_owned();
    let found = match Editor::note_of(src, dir) {
        Some(path) => pictures.of_notebook(root.as_deref()).get(&path).cloned(),
        None => Some(None),
    };
    let body = match found {
        Some(Some(image)) => {
            let [w, h] = image.size().unwrap_or([1, 1]).map(|v| v.max(1) as f32);
            let width = w.min(editor.width.max(1.0));
            el().role("image")
                .label(name)
                .size(width, h * width / w)
                .pixels(image.pixels())
        }
        Some(None) => col()
            .role("image")
            .label(format!("{name} (not found)"))
            .padding(space::S2)
            .radius(4.0)
            .border(1.0, t.border_subtle)
            .child(text(name).font(type_::LABEL).color(t.text_secondary))
            .child(
                text("image not found")
                    .font(type_::LABEL)
                    .color(t.text_tertiary),
            ),
        None => col()
            .role("image")
            .label(format!("{name} (loading)"))
            .w_full()
            .h(IMAGE_ESTIMATE)
            .radius(4.0)
            .fill(t.bg_elevated),
    };
    row().w_full().py(space::S1).child(body)
}

/// The front matter as a card of its properties: pressing it, or Return or Space on it, opens
/// the YAML for editing.
fn front_matter_card(editor: &Editor, key: &str, access: Access, t: &Tokens) -> El {
    let close = editor
        .kinds()
        .iter()
        .take_while(|k| matches!(k, LineKind::FrontMatter { .. }))
        .count();
    let yaml: Vec<&str> = editor.doc.lines()[1..close.saturating_sub(1)]
        .iter()
        .map(String::as_str)
        .collect();
    let entries = crate::markdown::front_matter_entries(&yaml.join("\n"));
    let field = |name: &str| {
        entries
            .iter()
            .find(|(k, _)| k == name)
            .map(|(_, v)| match v {
                crate::markdown::FrontMatterValue::Text(s) => s.clone(),
                crate::markdown::FrontMatterValue::List(items) => items.join(", "),
            })
    };
    let kind = field("type").unwrap_or_else(|| "note".into());
    let dates = [
        field("created").map(|c| format!("created {c}")),
        field("updated").map(|u| format!("updated {u}")),
    ]
    .into_iter()
    .flatten()
    .collect::<Vec<_>>()
    .join("  ·  ");
    let tags: Vec<String> = entries
        .iter()
        .find_map(|(k, v)| match v {
            crate::markdown::FrontMatterValue::List(items) if k == "tags" => Some(items.clone()),
            _ => None,
        })
        .unwrap_or_default();
    let key = key.to_owned();
    let open = move |state: &mut State, tx: &mut Tx| {
        if let Some(editor) = access(state, &key) {
            editor.focused = true;
            editor.doc.select(Pos::new(1, 0), Pos::new(1, 0));
            tx.focus(key.clone(), false);
        }
        crate::notebook::edited_editor(state, &key, tx);
    };
    let keyed = open.clone();
    col()
        .role("button")
        .label("Edit note properties")
        .focusable(true)
        .w_full()
        .gap(space::S1)
        .padding(space::S3)
        .radius(6.0)
        .border(1.0, t.border_subtle)
        .fill(t.bg_elevated)
        .child(text(kind).font(type_::LABEL).color(t.accent))
        .children(
            (!dates.is_empty()).then(|| text(dates).font(type_::LABEL).color(t.text_tertiary)),
        )
        .children(
            field("summary").map(|s| text(s).font(type_::BODY).color(t.text_secondary).wrap()),
        )
        .children((!tags.is_empty()).then(|| {
            row().gap(space::S1).children(tags.into_iter().map(|tag| {
                text(tag)
                    .font(type_::LABEL)
                    .color(t.text_secondary)
                    .px(space::S1)
                    .radius(4.0)
                    .fill(t.bg_button)
            }))
        }))
        .children(
            field("sources").map(|s| text(s).font(type_::LABEL).color(t.text_tertiary).wrap()),
        )
        .child(
            text("click to edit")
                .font(type_::LABEL)
                .color(t.text_dimmed),
        )
        .on_press_tx(open)
        .on_key(move |state: &mut State, ev, tx: &mut Tx| {
            let opens = matches!(ev.key, rune::Key::Enter)
                || matches!(&ev.key, rune::Key::Text(t) if t == " ");
            if opens {
                keyed(state, tx);
            }
            opens
        })
}
