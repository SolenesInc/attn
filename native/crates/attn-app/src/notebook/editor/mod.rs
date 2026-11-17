//! The markdown editor of the notebook and the editor tile: an editor of attn's own on Rune's
//! text stack. The document is one line per row, rows are built near the viewport only, the
//! caret moves on the Parley layout Rune draws each row with, and undo keeps edits.

pub(crate) mod doc;
pub(crate) mod find;
mod keys;
pub(crate) mod markdown;
mod table;
mod view;

use rune::TextStyle;
use rune::parley::editing::{Cursor, Selection};
use rune::parley::layout::Affinity;
use rune::text::Span;

pub(crate) use doc::{Doc, Kind, Pos};
pub(crate) use keys::{on_key, paste};
pub(crate) use view::view;

use markdown::{LineKind, Shown};

/// The note's body type, and the size and weight of each heading level, as the Tauri editor
/// sets them: a serif, drawing `-->` as written rather than as an arrow. The Tauri app's
/// `ui-serif, Georgia, 'Times New Roman', serif`, with Linux's usual serifs for `serif`.
pub(crate) const BODY: TextStyle = TextStyle::new(14.0, 400.0)
    .lh(23.0)
    .family(rune::Family::Named(
        "New York, Georgia, Times New Roman, Noto Serif, DejaVu Serif, Liberation Serif",
    ))
    .features("'liga' 0, 'calt' 0");
const HEADING_SCALE: [f32; 6] = [1.7, 1.4, 1.2, 1.08, 1.0, 1.0];
const HEADING_WEIGHT: [f32; 6] = [650.0, 630.0, 600.0, 600.0, 600.0, 600.0];

#[derive(Clone, Debug)]
pub(crate) struct Editor {
    pub(crate) doc: Doc,
    pub(crate) focused: bool,
    /// The x the caret keeps while it moves up and down, in points.
    goal_x: Option<f32>,
    /// The width rows wrap at, in points, as the editor last laid out.
    pub(crate) width: f32,
    /// What an input method is composing at the caret, drawn there and never saved.
    pub(crate) preedit: Option<String>,
    /// A press that toggled a task box: the rest of its gesture moves nothing.
    pub(crate) toggling: bool,
    /// The notes the links point at that the daemon said are missing, and every note asked
    /// about since files last changed, by path from the root.
    pub(crate) missing: std::collections::BTreeSet<String>,
    /// The note's folder, which its links resolve from.
    dir: String,
    pub(crate) checked: std::collections::BTreeSet<String>,
    /// The document revision whose links were last looked at.
    pub(crate) links_revision: Option<u64>,
    /// Rises when files on disk change, which asks again about every note linked, while the
    /// marks stay until the answers come; an answer asked before the latest change is dropped.
    pub(crate) links_generation: u64,
    /// The find bar's query, while it is open.
    pub(crate) find: Option<find::Find>,
    kinds: (u64, Vec<LineKind>),
}

impl Editor {
    pub(crate) fn new(text: &str) -> Editor {
        let doc = Doc::new(text);
        let kinds = (doc.revision, markdown::kinds(doc.lines()));
        Editor {
            doc,
            focused: false,
            goal_x: None,
            width: 600.0,
            preedit: None,
            toggling: false,
            missing: Default::default(),
            dir: String::new(),
            checked: Default::default(),
            links_revision: None,
            links_generation: 0,
            find: None,
            kinds,
        }
    }

    /// The editor of a note in folder `dir`.
    pub(crate) fn in_folder(mut self, dir: &str) -> Editor {
        self.dir = dir.to_owned();
        self
    }

    pub(crate) fn text(&self) -> String {
        self.doc.text()
    }

    /// What each line is; read again after the text changes.
    pub(crate) fn kinds(&self) -> &[LineKind] {
        &self.kinds.1
    }

    pub(crate) fn refresh(&mut self) {
        if self.kinds.0 != self.doc.revision {
            self.kinds = (self.doc.revision, markdown::kinds(self.doc.lines()));
        }
    }

    /// Whether line `i` shows its source: the caret's lines while the editor has the keyboard.
    pub(crate) fn active(&self, i: usize) -> bool {
        let (a, b) = self.doc.range();
        self.focused && a.line <= i && i <= b.line
    }

    /// How line `i` shows, a link to a missing note marked broken unless the line shows its
    /// source.
    pub(crate) fn shown(&self, i: usize) -> Shown {
        let active = self.active(i);
        let mut shown = markdown::shown(self.doc.line(i), active);
        if !active {
            for link in 0..shown.links.len() {
                let (range, href) = shown.links[link].clone();
                if Editor::note_of(&href, &self.dir).is_some_and(|p| self.missing.contains(&p)) {
                    shown.mark_broken(range);
                }
            }
        }
        shown
    }

    /// The line's text, spans and type, as its row draws it.
    pub(crate) fn styled(&self, shown: &Shown) -> (Vec<Span>, TextStyle) {
        let style = match shown.heading {
            Some(level) => {
                let at = (level as usize).clamp(1, 6) - 1;
                let size = BODY.size * HEADING_SCALE[at];
                TextStyle {
                    size,
                    weight: HEADING_WEIGHT[at],
                    line_height: size * if at < 2 { 1.3 } else { 1.65 },
                    ..BODY
                }
            }
            None => BODY,
        };
        (view::spans(shown, None), style)
    }

    /// The caret's line laid out as its row draws it.
    fn layout(&self, line: usize) -> (std::rc::Rc<rune::parley::Layout<rune::text::Ink>>, Shown) {
        let shown = markdown::shown(self.doc.line(line), true);
        let (spans, style) = self.styled(&shown);
        (
            rune::text::layout(&shown.text, &spans, style, Some(self.width)),
            shown,
        )
    }

    fn cursor(
        &self,
        p: Pos,
    ) -> (
        Selection,
        std::rc::Rc<rune::parley::Layout<rune::text::Ink>>,
    ) {
        let (layout, shown) = self.layout(p.line);
        let at = shown.to_display(p.col);
        (
            Selection::from(Cursor::from_byte_index(&layout, at, Affinity::Downstream)),
            layout,
        )
    }

    /// The caret rect `[x, y, w, h]` within its row.
    pub(crate) fn caret_rect(&self) -> [f32; 4] {
        let (selection, layout) = self.cursor(self.doc.caret);
        let r = selection.focus().geometry(&layout, 2.0);
        [
            r.x0 as f32,
            r.y0 as f32,
            (r.x1 - r.x0) as f32,
            (r.y1 - r.y0) as f32,
        ]
    }

    /// Moves the caret a visual line up or down, into the next row at the same x.
    pub(crate) fn vertical(&mut self, lines: isize, extend: bool) {
        let caret = self.doc.caret;
        let (selection, layout) = self.cursor(caret);
        let x = *self
            .goal_x
            .get_or_insert_with(|| selection.focus().geometry(&layout, 1.0).x0 as f32);
        let moved = selection.move_lines(&layout, lines, false);
        let same_row = line_of(&layout, moved.focus()) != line_of(&layout, selection.focus());
        let target = if same_row {
            let (_, shown) = self.layout(caret.line);
            Pos::new(caret.line, shown.to_source(moved.focus().index()))
        } else {
            let next = caret.line as isize + lines.signum();
            if next < 0 {
                Pos::new(0, 0)
            } else if next as usize >= self.doc.len() {
                self.doc.end()
            } else {
                let line = next as usize;
                let (layout, shown) = self.layout(line);
                let y = if lines > 0 {
                    1.0
                } else {
                    layout.height() - 1.0
                };
                let found = Selection::from_point(&layout, x, y);
                Pos::new(line, shown.to_source(found.focus().index()))
            }
        };
        self.move_to(target, extend, true);
    }

    /// Moves within the caret's row as Parley moves a selection.
    pub(crate) fn within_row(
        &mut self,
        extend: bool,
        step: impl Fn(&Selection, &rune::parley::Layout<rune::text::Ink>) -> Selection,
    ) {
        let to = self.in_row(step);
        self.move_to(to, extend, false);
    }

    /// Where `step` takes a caret standing alone at the caret, within its row.
    pub(crate) fn in_row(
        &self,
        step: impl Fn(&Selection, &rune::parley::Layout<rune::text::Ink>) -> Selection,
    ) -> Pos {
        let caret = self.doc.caret;
        let (selection, layout) = self.cursor(caret);
        let moved = step(&selection, &layout);
        let (_, shown) = self.layout(caret.line);
        Pos::new(caret.line, shown.to_source(moved.focus().index()))
    }

    pub(crate) fn move_to(&mut self, p: Pos, extend: bool, keep_goal: bool) {
        let p = self.doc.clamp(p);
        if !keep_goal {
            self.goal_x = None;
        }
        let anchor = if extend { self.doc.anchor } else { p };
        self.doc.select(anchor, p);
    }

    /// Where a press at `(x, y)` in row `line` lands, in the source.
    pub(crate) fn hit(&self, line: usize, x: f32, y: f32) -> Pos {
        let shown = self.shown(line);
        let (spans, style) = self.styled(&shown);
        let layout = rune::text::layout(&shown.text, &spans, style, Some(self.width));
        let found = Selection::from_point(&layout, x, y);
        Pos::new(line, shown.to_source(found.focus().index()))
    }

    /// The word around `p`, as a double click selects it.
    pub(crate) fn word_at(&self, p: Pos) -> (Pos, Pos) {
        let line = self.doc.line(p.line);
        let is_word = |c: char| c.is_alphanumeric() || c == '_';
        let start = line[..p.col]
            .char_indices()
            .rev()
            .take_while(|(_, c)| is_word(*c))
            .last()
            .map_or(p.col, |(i, _)| i);
        let end = line[p.col..]
            .char_indices()
            .find(|(_, c)| !is_word(*c))
            .map_or(line.len(), |(i, _)| p.col + i);
        (Pos::new(p.line, start), Pos::new(p.line, end))
    }

    /// Toggles the task box under `(x, y)` in row `line`, leaving the caret where it is; false
    /// when the press is not on a box.
    pub(crate) fn toggle_task(&mut self, line: usize, x: f32, y: f32, now: f64) -> bool {
        if self.active(line) {
            return false;
        }
        let shown = self.shown(line);
        let Some((range, checked)) = shown.task.clone() else {
            return false;
        };
        let (spans, style) = self.styled(&shown);
        let layout = rune::text::layout(&shown.text, &spans, style, Some(self.width));
        let at = Selection::from_point(&layout, x, y).focus().index();
        let Some(glyph) = shown.task_glyph.clone() else {
            return false;
        };
        if at < glyph.start || at > glyph.end {
            return false;
        }
        let kept = (self.doc.anchor, self.doc.caret);
        self.doc.select(
            Pos::new(line, range.start + 1),
            Pos::new(line, range.start + 2),
        );
        self.doc
            .replace(if checked { " " } else { "x" }, Kind::Other, now);
        self.doc.select(kept.0, kept.1);
        self.edited();
        true
    }

    /// Whether the selection touches any of `lines` while the editor has the keyboard.
    pub(crate) fn touches(&self, lines: &std::ops::Range<usize>) -> bool {
        let (a, b) = self.doc.range();
        self.focused && a.line < lines.end && b.line >= lines.start
    }

    /// Whether the caret is in the front matter, which then shows as YAML.
    pub(crate) fn editing_front_matter(&self) -> bool {
        let (a, _) = self.doc.range();
        self.focused && matches!(self.kinds().get(a.line), Some(LineKind::FrontMatter { .. }))
    }

    /// The link under `(x, y)` in row `line`, as it shows.
    pub(crate) fn link_at(&self, line: usize, x: f32, y: f32) -> Option<String> {
        let shown = self.shown(line);
        let (spans, style) = self.styled(&shown);
        let layout = rune::text::layout(&shown.text, &spans, style, Some(self.width));
        let at = Selection::from_point(&layout, x, y).focus().index();
        shown
            .links
            .iter()
            .find(|(r, _)| r.start <= at && at <= r.end)
            .map(|(_, href)| href.clone())
    }

    /// The note a link in a note in folder `dir` points at, by path from the root; `None` for a
    /// link out of the notebook.
    pub(crate) fn note_of(href: &str, dir: &str) -> Option<String> {
        match crate::notebook::links::resolve(href, dir) {
            crate::notebook::links::NoteLink::Note { path, .. } => Some(path),
            _ => None,
        }
    }

    /// The image files the note shows on lines of their own, by path from its folder.
    pub(crate) fn images(&self) -> Vec<String> {
        self.kinds()
            .iter()
            .filter_map(|k| match k {
                LineKind::Image { src, .. } => Editor::note_of(src, &self.dir),
                _ => None,
            })
            .collect()
    }

    /// The notes the document's links point at that nobody asked about yet.
    pub(crate) fn unchecked_links(&self) -> Vec<String> {
        let mut paths = std::collections::BTreeSet::new();
        for line in self.doc.lines().iter().filter(|l| l.contains("](")) {
            for (_, href) in markdown::shown(line, true).links {
                if let Some(path) =
                    Editor::note_of(&href, &self.dir).filter(|p| !self.checked.contains(p))
                {
                    paths.insert(path);
                }
            }
        }
        paths.into_iter().collect()
    }

    pub(crate) fn edited(&mut self) {
        self.goal_x = None;
        self.refresh();
    }
}

fn line_of(layout: &rune::parley::Layout<rune::text::Ink>, cursor: Cursor) -> usize {
    let y = cursor.geometry(layout, 1.0).y0 as f32;
    layout
        .lines()
        .position(|l| {
            let m = l.metrics();
            y >= m.block_min_coord - 0.5 && y < m.block_max_coord - 0.5
        })
        .unwrap_or(0)
}
