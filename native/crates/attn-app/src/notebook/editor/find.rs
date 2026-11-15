//! Find in the note: ⌘F opens a bar over the editor, every match shows, and Return, ⌘G and
//! their Shift forms move the selection to the next or previous one, as CodeMirror's search
//! panel does in the Tauri editor.

use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, TextEdit, Tx, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, TextInput};

use super::{Editor, Pos};
use crate::State;

#[derive(Clone, Debug)]
pub(crate) struct Find {
    pub(crate) query: TextEdit,
}

impl Editor {
    pub(crate) fn query(&self) -> Option<&str> {
        self.find
            .as_ref()
            .map(|f| f.query.text.as_str())
            .filter(|q| !q.is_empty())
    }

    /// Every match of the query, start and end, in document order, ignoring case.
    pub(crate) fn matches(&self) -> Vec<(Pos, Pos)> {
        let Some(query) = self.query() else {
            return Vec::new();
        };
        self.doc
            .lines()
            .iter()
            .enumerate()
            .flat_map(|(i, line)| {
                find_in(line, query)
                    .into_iter()
                    .map(move |m| (Pos::new(i, m.start), Pos::new(i, m.end)))
            })
            .collect()
    }

    /// Selects the next match after the selection, or the one before it, wrapping around.
    pub(crate) fn find_next(&mut self, back: bool) -> Option<usize> {
        let matches = self.matches();
        if matches.is_empty() {
            return None;
        }
        let (a, b) = self.doc.range();
        let at = if back {
            matches
                .iter()
                .rposition(|(s, _)| *s < a)
                .unwrap_or(matches.len() - 1)
        } else {
            matches
                .iter()
                .position(|(s, _)| *s >= b && (*s, b) != (a, a) || *s > a)
                .unwrap_or(0)
        };
        let (s, e) = matches[at];
        self.doc.select(s, e);
        Some(self.doc.id(s.line) as usize)
    }
}

/// The byte ranges of `text` that match `query` ignoring case, each spanning whole characters
/// of `text` however lowercasing changes their length.
pub(crate) fn find_in(text: &str, query: &str) -> Vec<std::ops::Range<usize>> {
    let needle: String = query.chars().flat_map(fold).collect();
    if needle.is_empty() {
        return Vec::new();
    }
    let mut folded = String::with_capacity(text.len());
    let mut chars = Vec::new();
    for (at, c) in text.char_indices() {
        chars.push((folded.len(), at));
        folded.extend(fold(c));
    }
    let source = |folded_at: usize| chars.partition_point(|(f, _)| *f <= folded_at) - 1;
    folded
        .match_indices(&needle)
        .map(|(at, _)| {
            let (first, last) = (source(at), source(at + needle.len() - 1));
            chars[first].1..chars.get(last + 1).map_or(text.len(), |(_, s)| *s)
        })
        .collect()
}

/// `c` as find compares it: lowercased, with final sigma as sigma.
fn fold(c: char) -> impl Iterator<Item = char> {
    c.to_lowercase().map(|l| if l == 'ς' { 'σ' } else { l })
}

fn field_key(key: &str) -> String {
    format!("{key}.find")
}

/// Opens the bar, seeded with the selection, and puts the keyboard in it.
pub(crate) fn open(editor: &mut Editor, key: &str, tx: &mut Tx) {
    let seed = Some(editor.doc.selected_text()).filter(|s| !s.is_empty() && !s.contains('\n'));
    let query = match (&editor.find, seed) {
        (_, Some(seed)) => TextEdit::new(&seed),
        (Some(find), None) => find.query.clone(),
        (None, None) => TextEdit::new(""),
    };
    editor.find = Some(Find { query });
    tx.focus(field_key(key), false);
}

/// The find bar over the editor keyed `key`, while it is open.
pub(crate) fn bar(
    editor: &Editor,
    key: &str,
    access: super::view::Access,
    t: &Tokens,
) -> Option<El> {
    let find = editor.find.as_ref()?;
    let count = editor.matches().len();
    let (edit_key, change_key, field_keys, next_key, prev_key, close_key) = (
        key.to_owned(),
        key.to_owned(),
        key.to_owned(),
        key.to_owned(),
        key.to_owned(),
        key.to_owned(),
    );
    let field = TextInput::new(&find.query)
        .key(field_key(key))
        .plain()
        .text_style(type_::BODY)
        .height(type_::BODY.line_px())
        .width(Some(220.0))
        .label("Find")
        .placeholder("Find")
        .on_edit(move |state: &mut State| {
            if access(state, &edit_key).is_none_or(|e| e.find.is_none()) {
                return &mut state.notebook.scratch;
            }
            match access(state, &edit_key).and_then(|e| e.find.as_mut()) {
                Some(find) => &mut find.query,
                None => unreachable!("checked above"),
            }
        })
        .on_change(move |state: &mut State, _: &mut Tx| {
            let _ = &change_key;
            state.notebook.revision += 1;
        })
        .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
            on_key(state, &field_keys, access, ev, tx)
        })
        .build(attn_ui::kit(t));
    let button = |words: &'static str, key: String, back: Option<bool>| {
        Button::new(words)
            .variant(ButtonVariant::GhostMuted)
            .control_size(ControlSize::Small)
            .build(attn_ui::kit(t))
            .on_press_tx(move |state: &mut State, tx: &mut Tx| match back {
                Some(back) => step(state, &key, access, back, tx),
                None => close(state, &key, access, tx),
            })
    };
    Some(
        row()
            .key(format!("{key}.findbar"))
            .role("search")
            .label("Find in note")
            .w_full()
            .items_center()
            .gap(space::S2)
            .px(space::S2)
            .py(space::S1)
            .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
            .child(field)
            .child(
                text(if count == 1 {
                    "1 match".to_owned()
                } else {
                    format!("{count} matches")
                })
                .key(format!("{key}.findcount"))
                .font(type_::LABEL)
                .color(t.text_tertiary),
            )
            .child(button("Previous", prev_key, Some(true)))
            .child(button("Next", next_key, Some(false)))
            .child(button("Close", close_key, None)),
    )
}

fn on_key(
    state: &mut State,
    key: &str,
    access: super::view::Access,
    ev: &KeyEvent,
    tx: &mut Tx,
) -> bool {
    match &ev.key {
        Key::Enter => step(state, key, access, ev.mods.shift, tx),
        Key::Command('g') => step(state, key, access, ev.mods.shift, tx),
        Key::Escape => close(state, key, access, tx),
        _ => return false,
    }
    true
}

fn step(state: &mut State, key: &str, access: super::view::Access, back: bool, tx: &mut Tx) {
    if let Some(editor) = access(state, key)
        && let Some(id) = editor.find_next(back)
    {
        tx.scroll_into_view(format!("{key}.line.{id}"));
    }
    state.notebook.revision += 1;
}

fn close(state: &mut State, key: &str, access: super::view::Access, tx: &mut Tx) {
    if let Some(editor) = access(state, key) {
        editor.find = None;
        editor.focused = true;
        tx.focus(key.to_owned(), false);
    }
    state.notebook.revision += 1;
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn find_ignores_case_and_selects_the_exact_source_text_where_case_changes_length() {
        for (line, query, want) in [
            (
                "a Lantern and a lantern",
                "LANTERN",
                vec!["Lantern", "lantern"],
            ),
            ("İstanbul lantern", "lantern", vec!["lantern"]),
            ("İstanbul", "i̇stanbul", vec!["İstanbul"]),
            ("STRAẞE strasse", "straße", vec!["STRAẞE"]),
            ("ΟΔΟΣ οδος", "οδοσ", vec!["ΟΔΟΣ", "οδος"]),
            ("ΟΔΟΣ οδος", "ΟΔΟΣ", vec!["ΟΔΟΣ", "οδος"]),
            (
                "cafe\u{301} CAFE\u{301} lamp",
                "cafe\u{301}",
                vec!["cafe\u{301}", "CAFE\u{301}"],
            ),
            ("cafe\u{301} CAFE\u{301} lamp", "LAMP", vec!["lamp"]),
            ("plain text", "nothing", vec![]),
        ] {
            let found: Vec<&str> = find_in(line, query).into_iter().map(|m| &line[m]).collect();
            assert_eq!(found, want, "{query:?} in {line:?}");
        }
    }
}
