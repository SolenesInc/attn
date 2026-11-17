//! What a key does in the editor: typing, Return that continues a list, motion by character,
//! word, visual line and document, selection, the clipboard, undo, and the formatting keys
//! ⌘B, ⌘I and ⌘E, as the Tauri editor binds them.

use rune::{Key, KeyEvent, Tx};

use super::{Editor, Kind, Pos};

/// Runs `ev` in the editor at time `now`; false when the editor leaves the key to the app.
pub(crate) fn on_key(editor: &mut Editor, ev: &KeyEvent, now: f64, tx: &mut Tx) -> bool {
    let m = ev.mods;
    let shift = m.shift;
    let word = if cfg!(target_os = "macos") {
        m.alt
    } else {
        m.ctrl
    };
    let line_jump = if cfg!(target_os = "macos") {
        m.cmd
    } else {
        false
    };
    match &ev.key {
        Key::Text(t) if !m.cmd && !m.ctrl => editor.doc.replace(t, Kind::Typing, now),
        Key::Enter if !m.cmd => newline(editor, now),
        Key::Tab if !m.cmd && !m.ctrl => editor.doc.replace("\t", Kind::Other, now),
        Key::Backspace if word => delete_word(editor, true, now),
        Key::Delete if word => delete_word(editor, false, now),
        Key::Backspace => editor.doc.backspace(now),
        Key::Delete => editor.doc.delete_forward(now),
        Key::Left if line_jump => editor.within_row(shift, |s, l| s.line_start(l, shift)),
        Key::Right if line_jump => editor.within_row(shift, |s, l| s.line_end(l, shift)),
        Key::Left if word => move_word(editor, true, shift),
        Key::Right if word => move_word(editor, false, shift),
        Key::Left if editor.doc.has_selection() && !shift => {
            let (a, _) = editor.doc.range();
            editor.move_to(a, false, false);
        }
        Key::Right if editor.doc.has_selection() && !shift => {
            let (_, b) = editor.doc.range();
            editor.move_to(b, false, false);
        }
        Key::Left => {
            let c = editor.doc.caret;
            let to = if c.col > 0 {
                let col = editor.doc.line(c.line)[..c.col]
                    .char_indices()
                    .last()
                    .map_or(0, |(i, _)| i);
                Pos::new(c.line, col)
            } else if c.line > 0 {
                Pos::new(c.line - 1, editor.doc.line(c.line - 1).len())
            } else {
                c
            };
            editor.move_to(to, shift, false);
        }
        Key::Right => {
            let c = editor.doc.caret;
            let line = editor.doc.line(c.line);
            let to = if c.col < line.len() {
                Pos::new(
                    c.line,
                    c.col + line[c.col..].chars().next().map_or(0, char::len_utf8),
                )
            } else if c.line + 1 < editor.doc.len() {
                Pos::new(c.line + 1, 0)
            } else {
                c
            };
            editor.move_to(to, shift, false);
        }
        Key::Up if m.cmd => editor.move_to(Pos::default(), shift, false),
        Key::Down if m.cmd => {
            let end = editor.doc.end();
            editor.move_to(end, shift, false);
        }
        Key::Up => editor.vertical(-1, shift),
        Key::Down => editor.vertical(1, shift),
        Key::Home if m.cmd || m.ctrl => editor.move_to(Pos::default(), shift, false),
        Key::End if m.cmd || m.ctrl => {
            let end = editor.doc.end();
            editor.move_to(end, shift, false);
        }
        Key::Home => editor.within_row(shift, |s, l| s.line_start(l, shift)),
        Key::End => editor.within_row(shift, |s, l| s.line_end(l, shift)),
        Key::Command('a') if !shift => {
            let end = editor.doc.end();
            editor.doc.select(Pos::default(), end);
        }
        Key::Command('z') if shift => {
            editor.doc.redo();
        }
        Key::Command('y') if !cfg!(target_os = "macos") => {
            editor.doc.redo();
        }
        Key::Command('z') => {
            editor.doc.undo();
        }
        Key::Command('c') if !shift => {
            if editor.doc.has_selection() {
                tx.copy(editor.doc.selected_text());
            }
        }
        Key::Command('x') if !shift => {
            if editor.doc.has_selection() {
                tx.copy(editor.doc.selected_text());
                editor.doc.replace("", Kind::Other, now);
            }
        }
        Key::Command('g') if editor.find.is_some() => {
            editor.find_next(shift);
        }
        Key::Command('b') if !shift => toggle(editor, "**", now),
        Key::Command('i') if !shift => toggle(editor, "*", now),
        Key::Command('e') if !shift => toggle(editor, "`", now),
        _ => return false,
    }
    editor.edited();
    true
}

/// Moves the caret a word back or forward.
fn move_word(editor: &mut Editor, back: bool, extend: bool) {
    let to = word_edge(editor, back);
    editor.move_to(to, extend, false);
}

/// Where the word before or after the caret ends; at a line's edge the line break is the word,
/// so it lies on the neighbouring line, as CodeMirror moves by group.
fn word_edge(editor: &Editor, back: bool) -> Pos {
    let c = editor.doc.caret;
    let len = editor.doc.line(c.line).len();
    if back && c.col == 0 && c.line > 0 {
        Pos::new(c.line - 1, editor.doc.line(c.line - 1).len())
    } else if !back && c.col == len && c.line + 1 < editor.doc.len() {
        Pos::new(c.line + 1, 0)
    } else if back {
        editor.in_row(|s, l| s.previous_visual_word(l, false))
    } else {
        editor.in_row(|s, l| s.next_visual_word(l, false))
    }
}

/// Removes the selection, or the word before or after the caret as one undo step.
fn delete_word(editor: &mut Editor, back: bool, now: f64) {
    let to = word_edge(editor, back);
    editor.doc.delete_to(to, Kind::Other, now);
}

/// Pastes `text` over the selection.
pub(crate) fn paste(editor: &mut Editor, text: &str, now: f64) {
    editor
        .doc
        .replace(&text.replace("\r\n", "\n"), Kind::Other, now);
    editor.edited();
}

/// Return: in a list item it starts the next item, and on an empty item it ends the list, as
/// CodeMirror's markdown keymap does; elsewhere a plain line break.
fn newline(editor: &mut Editor, now: f64) {
    let c = editor.doc.caret;
    let line = editor.doc.line(c.line).to_owned();
    let Some((marker, next)) = list_marker(&line) else {
        return editor.doc.replace("\n", Kind::Other, now);
    };
    if editor.doc.has_selection() || c.col < marker.len() {
        return editor.doc.replace("\n", Kind::Other, now);
    }
    if line[marker.len()..].trim().is_empty() {
        let (start, end) = (Pos::new(c.line, 0), Pos::new(c.line, line.len()));
        return editor.doc.replace_between(start, end, "", Kind::Other, now);
    }
    editor.doc.replace(&format!("\n{next}"), Kind::Other, now);
}

/// The marker that opens a list line, with the marker its next item takes: `- `, `- [ ] `,
/// `3. ` (then `4. `), with the line's indent.
fn list_marker(line: &str) -> Option<(String, String)> {
    let indent = &line[..line.len() - line.trim_start().len()];
    let rest = &line[indent.len()..];
    let (mark, next_mark) =
        if let Some(c) = rest.chars().next().filter(|c| matches!(c, '-' | '*' | '+')) {
            if !rest[1..].starts_with(' ') {
                return None;
            }
            let task = ["[ ] ", "[x] ", "[X] "]
                .iter()
                .find(|t| rest[2..].starts_with(**t));
            match task {
                Some(task) => (format!("{c} {task}"), format!("{c} [ ] ")),
                None => (format!("{c} "), format!("{c} ")),
            }
        } else {
            let digits = rest.chars().take_while(char::is_ascii_digit).count();
            let after = rest[digits..].chars().next()?;
            if digits == 0 || !matches!(after, '.' | ')') || !rest[digits + 1..].starts_with(' ') {
                return None;
            }
            let n: u64 = rest[..digits].parse().ok()?;
            (
                format!("{}{after} ", &rest[..digits]),
                format!("{}{after} ", n + 1),
            )
        };
    Some((format!("{indent}{mark}"), format!("{indent}{next_mark}")))
}

/// ⌘B, ⌘I or ⌘E: unwraps `mark` around the selection or the word under the caret when it is
/// there, else wraps the selection, the word, or puts an empty pair with the caret inside.
fn toggle(editor: &mut Editor, mark: &str, now: f64) {
    let had = editor.doc.has_selection();
    let caret = editor.doc.caret;
    let (a, b) = editor.doc.range();
    if a.line != b.line {
        return;
    }
    let line = editor.doc.line(a.line).to_owned();
    let (a, b) = if had { (a, b) } else { editor.word_at(a) };
    let n = mark.len();
    let doubled = |s: &str| mark == "*" && s.ends_with("**");
    let wrapped = line[..a.col].ends_with(mark)
        && line[b.col..].starts_with(mark)
        && !(doubled(&line[..a.col]) && !line[b.col..].starts_with("**"));
    let inner = line[a.col..b.col].to_owned();
    let at = |col: usize| Pos::new(a.line, col);
    if wrapped {
        editor
            .doc
            .replace_between(at(a.col - n), at(b.col + n), &inner, Kind::Other, now);
        if had {
            editor
                .doc
                .select(at(a.col - n), at(a.col - n + inner.len()));
        } else {
            editor.doc.select(at(caret.col - n), at(caret.col - n));
        }
    } else {
        editor
            .doc
            .replace_between(a, b, &format!("{mark}{inner}{mark}"), Kind::Other, now);
        if had {
            editor.doc.select(at(a.col + n), at(b.col + n));
        } else {
            editor.doc.select(at(caret.col + n), at(caret.col + n));
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn editor(text: &str, caret: (usize, usize)) -> Editor {
        let mut e = Editor::new(text);
        let p = Pos::new(caret.0, caret.1);
        e.doc.select(p, p);
        e
    }

    #[test]
    fn return_continues_a_list_and_ends_it_on_an_empty_item() {
        for (text, caret, want) in [
            ("- one", (0, 5), "- one\n- "),
            ("  * one", (0, 7), "  * one\n  * "),
            ("- [x] done", (0, 10), "- [x] done\n- [ ] "),
            ("3. three", (0, 8), "3. three\n4. "),
            ("- ", (0, 2), ""),
            ("plain", (0, 5), "plain\n"),
        ] {
            let mut e = editor(text, caret);
            newline(&mut e, 0.0);
            assert_eq!(e.text(), want, "{text:?}");
        }
    }

    #[test]
    fn formatting_keys_wrap_the_word_or_selection_and_unwrap_it_again() {
        let mut e = editor("make it bold", (0, 9));
        toggle(&mut e, "**", 0.0);
        assert_eq!(e.text(), "make it **bold**");
        assert_eq!(e.doc.caret, Pos::new(0, 11));
        toggle(&mut e, "**", 1.0);
        assert_eq!(e.text(), "make it bold");

        let mut e = editor("x", (0, 1));
        e.doc.replace(" ", Kind::Typing, 0.0);
        toggle(&mut e, "*", 1.0);
        assert_eq!(e.text(), "x **");
        assert_eq!(e.doc.caret, Pos::new(0, 3));

        let mut e = Editor::new("code here");
        e.doc.select(Pos::new(0, 0), Pos::new(0, 4));
        toggle(&mut e, "`", 0.0);
        assert_eq!(e.text(), "`code` here");
        assert_eq!(e.doc.selected_text(), "code");
        toggle(&mut e, "`", 1.0);
        assert_eq!(e.text(), "code here");
    }
}
