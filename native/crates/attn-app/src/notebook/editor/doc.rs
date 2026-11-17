//! The editor's document: one `String` per line, each with an id that stays while the line
//! does, a selection, and an undo history of edits rather than copies.

/// A place in the document: a line and a byte offset into it.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord)]
pub(crate) struct Pos {
    pub(crate) line: usize,
    pub(crate) col: usize,
}

impl Pos {
    pub(crate) fn new(line: usize, col: usize) -> Pos {
        Pos { line, col }
    }
}

/// One replacement: the text from `at` that `removed` held now holds `inserted`.
#[derive(Clone, Debug, PartialEq)]
struct Edit {
    at: Pos,
    removed: String,
    inserted: String,
}

/// How an edit groups with the one before it into one undo step.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Kind {
    /// Characters typed one after another merge while the caret stays at the run's end.
    Typing,
    /// Backspaces one after another merge the same way.
    Deleting,
    /// Everything else is its own step: a paste, Return, a formatting key.
    Other,
}

#[derive(Clone, Debug)]
struct Step {
    edits: Vec<Edit>,
    before: (Pos, Pos),
    after: (Pos, Pos),
    kind: Kind,
    at: f64,
}

/// Typing merges into one undo step while keystrokes come closer together than this, as Zed's
/// history groups them.
const GROUP_S: f64 = 0.3;
/// The undo history keeps at most this many bytes of removed and inserted text, a tripwire far
/// above an afternoon of typing: the oldest steps go first.
const HISTORY_BYTES: usize = 64 << 20;

#[derive(Clone, Debug)]
pub(crate) struct Doc {
    lines: Vec<String>,
    ids: std::rc::Rc<Vec<u64>>,
    next_id: u64,
    /// Rises whenever lines come or go, so rows know their keys changed.
    pub(crate) structure: u64,
    /// Rises with every change to the text.
    pub(crate) revision: u64,
    pub(crate) anchor: Pos,
    pub(crate) caret: Pos,
    undo: Vec<Step>,
    redo: Vec<Step>,
    history_bytes: usize,
}

impl Doc {
    pub(crate) fn new(text: &str) -> Doc {
        let lines: Vec<String> = text.split('\n').map(str::to_owned).collect();
        let ids = std::rc::Rc::new((0..lines.len() as u64).collect());
        Doc {
            next_id: lines.len() as u64,
            lines,
            ids,
            structure: 0,
            revision: 0,
            anchor: Pos::default(),
            caret: Pos::default(),
            undo: Vec::new(),
            redo: Vec::new(),
            history_bytes: 0,
        }
    }

    pub(crate) fn text(&self) -> String {
        self.lines.join("\n")
    }

    pub(crate) fn lines(&self) -> &[String] {
        &self.lines
    }

    pub(crate) fn line(&self, i: usize) -> &str {
        &self.lines[i]
    }

    pub(crate) fn id(&self, i: usize) -> u64 {
        self.ids[i]
    }

    /// Every line's id, in order, shared until the next edit.
    pub(crate) fn ids(&self) -> std::rc::Rc<Vec<u64>> {
        self.ids.clone()
    }

    pub(crate) fn len(&self) -> usize {
        self.lines.len()
    }

    pub(crate) fn end(&self) -> Pos {
        let last = self.lines.len() - 1;
        Pos::new(last, self.lines[last].len())
    }

    /// The selection, start first.
    pub(crate) fn range(&self) -> (Pos, Pos) {
        if self.anchor <= self.caret {
            (self.anchor, self.caret)
        } else {
            (self.caret, self.anchor)
        }
    }

    pub(crate) fn has_selection(&self) -> bool {
        self.anchor != self.caret
    }

    pub(crate) fn clamp(&self, p: Pos) -> Pos {
        let line = p.line.min(self.lines.len() - 1);
        let text = &self.lines[line];
        let mut col = p.col.min(text.len());
        while !text.is_char_boundary(col) {
            col -= 1;
        }
        Pos::new(line, col)
    }

    pub(crate) fn select(&mut self, anchor: Pos, caret: Pos) {
        self.anchor = self.clamp(anchor);
        self.caret = self.clamp(caret);
    }

    /// The byte offset of `p` in the whole text.
    pub(crate) fn offset(&self, p: Pos) -> usize {
        self.lines[..p.line]
            .iter()
            .map(|l| l.len() + 1)
            .sum::<usize>()
            + p.col
    }

    /// The place at byte `offset` of the whole text.
    pub(crate) fn pos(&self, mut offset: usize) -> Pos {
        for (i, line) in self.lines.iter().enumerate() {
            if offset <= line.len() {
                return self.clamp(Pos::new(i, offset));
            }
            offset -= line.len() + 1;
        }
        self.end()
    }

    pub(crate) fn slice(&self, a: Pos, b: Pos) -> String {
        if a.line == b.line {
            return self.lines[a.line][a.col..b.col].to_owned();
        }
        let mut out = self.lines[a.line][a.col..].to_owned();
        for line in &self.lines[a.line + 1..b.line] {
            out.push('\n');
            out += line;
        }
        out.push('\n');
        out += &self.lines[b.line][..b.col];
        out
    }

    pub(crate) fn selected_text(&self) -> String {
        let (a, b) = self.range();
        self.slice(a, b)
    }

    fn end_of(at: Pos, text: &str) -> Pos {
        match text.rfind('\n') {
            Some(i) => Pos::new(at.line + text.matches('\n').count(), text.len() - i - 1),
            None => Pos::new(at.line, at.col + text.len()),
        }
    }

    /// Puts `text` where `a..b` was, keeping the ids of lines that stay; returns where it ends.
    fn splice(&mut self, a: Pos, b: Pos, text: &str) -> Pos {
        let head = self.lines[a.line][..a.col].to_owned();
        let tail = self.lines[b.line][b.col..].to_owned();
        let mut new: Vec<String> = text.split('\n').map(str::to_owned).collect();
        new[0] = head + &new[0];
        let end = Pos::new(a.line + new.len() - 1, new.last().map_or(0, String::len));
        if let Some(last) = new.last_mut() {
            last.push_str(&tail);
        }
        let kept = self.ids[a.line];
        let fresh = new.len() - 1;
        if fresh > 0 || b.line > a.line {
            self.structure += 1;
        }
        let mut ids: Vec<u64> = std::iter::once(kept)
            .chain((0..fresh as u64).map(|k| self.next_id + k))
            .collect();
        if a.col == 0 && !tail.is_empty() {
            ids.rotate_left(1);
        }
        self.next_id += fresh as u64;
        std::rc::Rc::make_mut(&mut self.ids).splice(a.line..=b.line, ids);
        self.lines.splice(a.line..=b.line, new);
        self.revision += 1;
        end
    }

    /// Replaces the selection with `text` as one undoable edit, merging into the step before it
    /// as `kind` says, at time `now` in seconds.
    pub(crate) fn replace(&mut self, text: &str, kind: Kind, now: f64) {
        let (a, b) = self.range();
        self.replace_between(a, b, text, kind, now);
    }

    /// Removes the selection, or the text between the caret and `to`; undo puts back the
    /// selection the user had, not the removed span.
    pub(crate) fn delete_to(&mut self, to: Pos, kind: Kind, now: f64) {
        let (a, b) = if self.has_selection() {
            self.range()
        } else {
            let to = self.clamp(to);
            (to.min(self.caret), to.max(self.caret))
        };
        self.replace_between(a, b, "", kind, now);
    }

    /// Replaces the text from `a` to `b` with `text` as one undoable edit; undo puts back the
    /// selection the user had, not the replaced span.
    pub(crate) fn replace_between(&mut self, a: Pos, b: Pos, text: &str, kind: Kind, now: f64) {
        let removed = self.slice(a, b);
        if removed.is_empty() && text.is_empty() {
            return;
        }
        let before = (self.anchor, self.caret);
        let end = self.splice(a, b, text);
        self.caret = end;
        self.anchor = end;
        let edit = Edit {
            at: a,
            removed,
            inserted: text.to_owned(),
        };
        self.record(edit, before, kind, now);
    }

    /// Removes the character before the caret, or the selection.
    pub(crate) fn backspace(&mut self, now: f64) {
        let c = self.caret;
        let to = if c.col > 0 {
            let col = self.lines[c.line][..c.col]
                .char_indices()
                .last()
                .map_or(0, |(i, _)| i);
            Pos::new(c.line, col)
        } else if c.line > 0 {
            Pos::new(c.line - 1, self.lines[c.line - 1].len())
        } else {
            c
        };
        self.delete_to(to, Kind::Deleting, now);
    }

    /// Removes the character after the caret, or the selection.
    pub(crate) fn delete_forward(&mut self, now: f64) {
        let c = self.caret;
        let line = &self.lines[c.line];
        let to = if c.col < line.len() {
            Pos::new(
                c.line,
                c.col + line[c.col..].chars().next().map_or(0, char::len_utf8),
            )
        } else if c.line + 1 < self.lines.len() {
            Pos::new(c.line + 1, 0)
        } else {
            c
        };
        self.delete_to(to, Kind::Other, now);
    }

    fn record(&mut self, edit: Edit, before: (Pos, Pos), kind: Kind, now: f64) {
        self.redo.clear();
        self.history_bytes += edit.removed.len() + edit.inserted.len();
        let after = (self.anchor, self.caret);
        let merges = self.undo.last().is_some_and(|last| {
            kind != Kind::Other
                && last.kind == kind
                && now - last.at < GROUP_S
                && last.after.1 == before.1
                && !edit.inserted.contains('\n')
        });
        if merges && let Some(last) = self.undo.last_mut() {
            last.edits.push(edit);
            last.after = after;
            last.at = now;
        } else {
            self.undo.push(Step {
                edits: vec![edit],
                before,
                after,
                kind,
                at: now,
            });
        }
        while self.history_bytes > HISTORY_BYTES && self.undo.len() > 1 {
            let dropped = self.undo.remove(0);
            self.history_bytes -= dropped
                .edits
                .iter()
                .map(|e| e.removed.len() + e.inserted.len())
                .sum::<usize>();
        }
    }

    /// Takes back the last step and restores the selection it started from.
    pub(crate) fn undo(&mut self) -> bool {
        let Some(step) = self.undo.pop() else {
            return false;
        };
        for edit in step.edits.iter().rev() {
            let end = Doc::end_of(edit.at, &edit.inserted);
            self.splice(edit.at, end, &edit.removed);
        }
        (self.anchor, self.caret) = step.before;
        self.redo.push(step);
        true
    }

    pub(crate) fn redo(&mut self) -> bool {
        let Some(step) = self.redo.pop() else {
            return false;
        };
        for edit in &step.edits {
            let end = Doc::end_of(edit.at, &edit.removed);
            self.splice(edit.at, end, &edit.inserted);
        }
        (self.anchor, self.caret) = step.after;
        self.undo.push(step);
        true
    }

    /// Takes `text` as what the document holds, changing only the part that differs and
    /// keeping the selection where its text still is; the change is not undoable, as a file
    /// read from disk is not.
    pub(crate) fn follow(&mut self, text: &str) {
        let current = self.text();
        let Some((from, to, insert)) = minimal_edit(&current, text) else {
            return;
        };
        let shift = |offset: usize| -> usize {
            if offset <= from {
                offset
            } else if offset >= to {
                offset - to + from + insert.len()
            } else {
                from + insert.len()
            }
        };
        let (anchor, caret) = (
            shift(self.offset(self.anchor)),
            shift(self.offset(self.caret)),
        );
        let (a, b) = (self.pos(from), self.pos(to));
        self.splice(a, b, &insert);
        self.anchor = self.pos(anchor);
        self.caret = self.pos(caret);
        self.undo.clear();
        self.redo.clear();
        self.history_bytes = 0;
    }
}

/// The smallest replacement that turns `current` into `next`: the shared prefix and suffix
/// stay, `current[from..to]` becomes `insert`. `None` when they are equal.
pub(crate) fn minimal_edit(current: &str, next: &str) -> Option<(usize, usize, String)> {
    if current == next {
        return None;
    }
    let (a, b) = (current.as_bytes(), next.as_bytes());
    let mut prefix = a.iter().zip(b).take_while(|(x, y)| x == y).count();
    while !current.is_char_boundary(prefix) || !next.is_char_boundary(prefix) {
        prefix -= 1;
    }
    let (mut end_a, mut end_b) = (a.len(), b.len());
    while end_a > prefix && end_b > prefix && a[end_a - 1] == b[end_b - 1] {
        end_a -= 1;
        end_b -= 1;
    }
    while !current.is_char_boundary(end_a) || !next.is_char_boundary(end_b) {
        end_a += 1;
        end_b += 1;
    }
    Some((prefix, end_a, next[prefix..end_b].to_owned()))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn at(doc: &mut Doc, line: usize, col: usize) {
        doc.select(Pos::new(line, col), Pos::new(line, col));
    }

    #[test]
    fn typing_runs_undo_as_one_step_and_a_pause_or_a_jump_starts_another() {
        let mut doc = Doc::new("one\ntwo");
        at(&mut doc, 0, 3);
        for (i, ch) in [" a", "b", "c"].iter().enumerate() {
            doc.replace(ch, Kind::Typing, i as f64 * 0.1);
        }
        doc.replace("d", Kind::Typing, 1.0);
        at(&mut doc, 1, 0);
        doc.replace("x", Kind::Typing, 1.05);
        assert_eq!(doc.text(), "one abcd\nxtwo");
        assert!(doc.undo());
        assert_eq!(doc.text(), "one abcd\ntwo");
        assert!(doc.undo());
        assert_eq!(doc.text(), "one abc\ntwo");
        assert!(doc.undo());
        assert_eq!(doc.text(), "one\ntwo");
        assert_eq!(doc.caret, Pos::new(0, 3));
        assert!(!doc.undo());
        assert!(doc.redo() && doc.redo() && doc.redo());
        assert_eq!(doc.text(), "one abcd\nxtwo");
        assert_eq!(doc.caret, Pos::new(1, 1));
    }

    #[test]
    fn return_and_paste_split_lines_and_keep_the_ids_of_lines_that_stay() {
        let mut doc = Doc::new("alpha\nbeta\ngamma");
        let (beta, gamma) = (doc.id(1), doc.id(2));
        at(&mut doc, 1, 2);
        doc.replace("\n", Kind::Other, 0.0);
        assert_eq!(doc.lines(), ["alpha", "be", "ta", "gamma"]);
        assert_eq!(doc.id(1), beta);
        assert_eq!(doc.id(3), gamma);
        assert_ne!(doc.id(2), beta);
        doc.select(Pos::new(0, 0), Pos::new(3, 5));
        doc.replace("x\ny", Kind::Other, 1.0);
        assert_eq!(doc.text(), "x\ny");
        assert!(doc.undo());
        assert_eq!(doc.text(), "alpha\nbe\nta\ngamma");
        assert_eq!((doc.anchor, doc.caret), (Pos::new(0, 0), Pos::new(3, 5)));
    }

    #[test]
    fn backspace_joins_lines_and_respects_characters() {
        let mut doc = Doc::new("é\nb");
        at(&mut doc, 1, 0);
        doc.backspace(0.0);
        assert_eq!(doc.text(), "éb");
        doc.backspace(0.1);
        assert_eq!(doc.text(), "b");
        assert_eq!(doc.caret, Pos::new(0, 0));
        doc.undo();
        assert_eq!(doc.text(), "é\nb");
    }

    #[test]
    fn a_whole_document_replaced_undoes_and_redoes_in_one_step() {
        let text = (0..5000)
            .map(|i| format!("line {i} of the note"))
            .collect::<Vec<_>>()
            .join("\n");
        let mut doc = Doc::new(&text);
        doc.select(Pos::default(), doc.end());
        doc.replace(&text.to_uppercase(), Kind::Other, 0.0);
        assert!(doc.undo());
        assert_eq!(doc.text(), text);
        assert!(doc.redo());
        assert_eq!(doc.text(), text.to_uppercase());
    }

    #[test]
    fn following_the_disk_changes_only_what_differs_and_keeps_the_caret_on_its_text() {
        let mut doc = Doc::new("# Title\n\nbody\n");
        at(&mut doc, 2, 2);
        let kept = doc.id(2);
        doc.follow("# Title\n\nintro\n\nbody\n");
        assert_eq!(doc.text(), "# Title\n\nintro\n\nbody\n");
        assert_eq!(doc.line(doc.caret.line), "body");
        assert_eq!(doc.caret.col, 2);
        assert_eq!(
            doc.id(4),
            kept,
            "{:?}",
            (0..doc.len()).map(|i| doc.id(i)).collect::<Vec<_>>()
        );
        assert!(!doc.undo(), "a read from disk is not undone");
    }

    #[test]
    fn the_minimal_edit_narrows_to_what_changed() {
        for (current, next, want) in [
            ("abc", "abc", None),
            ("abc", "abcd", Some((3, 3, "d"))),
            ("abc", "xabc", Some((0, 0, "x"))),
            ("abXc", "abYc", Some((2, 3, "Y"))),
            ("abc", "xyz", Some((0, 3, "xyz"))),
            ("aXbc", "bc", Some((0, 2, ""))),
            ("aXb", "ab", Some((1, 2, ""))),
            ("abc", "", Some((0, 3, ""))),
            ("aaa", "aa", Some((2, 3, ""))),
            ("café", "cafe", Some((3, 5, "e"))),
        ] {
            let got = minimal_edit(current, next);
            assert_eq!(
                got.as_ref().map(|(f, t, i)| (*f, *t, i.as_str())),
                want,
                "{current:?} → {next:?}"
            );
            if let Some((f, t, i)) = got {
                assert_eq!(format!("{}{}{}", &current[..f], i, &current[t..]), next);
            }
        }
    }
}
