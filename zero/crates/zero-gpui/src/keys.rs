use gpui::Keystroke;
use zero::editor::Key;

/// Bytes a terminal program expects for a keystroke; None for keys that mean nothing to a PTY.
pub fn keystroke_bytes(keystroke: &Keystroke) -> Option<Vec<u8>> {
    let m = keystroke.modifiers;
    if m.platform {
        return None;
    }
    let modifier_param = 1 + m.shift as u8 + (m.alt as u8) * 2 + (m.control as u8) * 4;
    let arrow = |letter: u8| {
        if modifier_param == 1 {
            vec![0x1b, b'[', letter]
        } else {
            format!("\x1b[1;{modifier_param}{}", letter as char).into_bytes()
        }
    };
    let bytes = match keystroke.key.as_str() {
        "enter" => b"\r".to_vec(),
        "backspace" => vec![0x7f],
        "tab" if m.shift => b"\x1b[Z".to_vec(),
        "tab" => b"\t".to_vec(),
        "escape" => vec![0x1b],
        "delete" => b"\x1b[3~".to_vec(),
        "up" => arrow(b'A'),
        "down" => arrow(b'B'),
        "right" => arrow(b'C'),
        "left" => arrow(b'D'),
        "home" => b"\x1b[H".to_vec(),
        "end" => b"\x1b[F".to_vec(),
        "pageup" => b"\x1b[5~".to_vec(),
        "pagedown" => b"\x1b[6~".to_vec(),
        "space" if m.control => vec![0],
        "space" => b" ".to_vec(),
        key => {
            let mut chars = key.chars();
            let (first, rest) = (chars.next()?, chars.next());
            if rest.is_some() {
                return None;
            }
            if m.control {
                let code = match first.to_ascii_lowercase() {
                    c @ 'a'..='z' => c as u8 & 0x1f,
                    '[' => 0x1b,
                    '\\' => 0x1c,
                    ']' => 0x1d,
                    _ => return None,
                };
                return Some(vec![code]);
            }
            if m.alt {
                let mut out = vec![0x1b];
                out.extend_from_slice(key.as_bytes());
                return Some(out);
            }
            keystroke
                .key_char
                .clone()
                .unwrap_or_else(|| key.to_string())
                .into_bytes()
        }
    };
    Some(bytes)
}

/// The text a keystroke types, if it types text (no control or command modifier).
pub fn typed_text(keystroke: &Keystroke) -> Option<String> {
    let m = keystroke.modifiers;
    if m.platform || m.control || m.function {
        return None;
    }
    if keystroke.key == "space" {
        return Some(" ".to_string());
    }
    if let Some(text) = &keystroke.key_char {
        return Some(text.clone());
    }
    let mut chars = keystroke.key.chars();
    let first = chars.next()?;
    chars.next().is_none().then(|| first.to_string())
}

/// The keystroke as the editor adapter sees it; None for command chords, which stay with zero.
pub fn editor_key(keystroke: &Keystroke) -> Option<Key> {
    let m = keystroke.modifiers;
    if m.platform {
        return None;
    }
    let text = if m.control || m.function {
        None
    } else if keystroke.key == "space" {
        Some(" ".to_string())
    } else {
        keystroke.key_char.clone()
    };
    Some(Key { name: keystroke.key.clone(), text, shift: m.shift, control: m.control, alt: m.alt })
}

#[cfg(test)]
mod tests {
    use super::*;
    use gpui::Modifiers;

    fn key(key: &str, modifiers: Modifiers, key_char: Option<&str>) -> Keystroke {
        Keystroke {
            modifiers,
            key: key.to_string(),
            key_char: key_char.map(str::to_string),
        }
    }

    #[test]
    fn shell_bytes_for_the_keys_a_shell_cares_about() {
        let none = Modifiers::default();
        assert_eq!(keystroke_bytes(&key("enter", none, None)), Some(b"\r".to_vec()));
        assert_eq!(keystroke_bytes(&key("escape", none, None)), Some(vec![0x1b]));
        assert_eq!(
            keystroke_bytes(&key("a", Modifiers { control: true, ..none }, None)),
            Some(vec![0x01])
        );
        assert_eq!(
            keystroke_bytes(&key("f", Modifiers { alt: true, ..none }, Some("ƒ"))),
            Some(b"\x1bf".to_vec())
        );
        assert_eq!(
            keystroke_bytes(&key("left", Modifiers { control: true, ..none }, None)),
            Some(b"\x1b[1;5D".to_vec())
        );
        assert_eq!(keystroke_bytes(&key("s", Modifiers { platform: true, ..none }, None)), None);
        assert_eq!(keystroke_bytes(&key("a", Modifiers { shift: true, ..none }, Some("A"))), Some(b"A".to_vec()));
    }

    #[test]
    fn editor_keys_carry_text_and_modifiers_but_never_command_chords() {
        let none = Modifiers::default();
        let shifted = editor_key(&key("a", Modifiers { shift: true, ..none }, Some("A"))).unwrap();
        assert_eq!((shifted.name.as_str(), shifted.text.as_deref(), shifted.shift), ("a", Some("A"), true));
        let control = editor_key(&key("a", Modifiers { control: true, ..none }, None)).unwrap();
        assert_eq!((control.text, control.control), (None, true));
        assert_eq!(editor_key(&key("space", none, None)).unwrap().text.as_deref(), Some(" "));
        assert!(editor_key(&key("k", Modifiers { platform: true, ..none }, None)).is_none());
    }
}
