use rune::{Chord, ChordKey, ChordMod, Key, KeyEvent, KeyPlatform, Mods};

/// The key event the keyboard sends for `chord` on this platform: "mod+shift+k" is ⇧⌘K on
/// macOS and Ctrl+Shift+K on Linux.
pub fn key_event(chord: &Chord) -> Result<KeyEvent, String> {
    let platform = KeyPlatform::current();
    let mut mods = Mods::default();
    for m in chord.mods_on(platform) {
        match m {
            ChordMod::Primary => {
                mods.cmd = true;
                if platform == KeyPlatform::Mac {
                    mods.logo = true;
                } else {
                    mods.ctrl = true;
                }
            }
            ChordMod::Ctrl => mods.ctrl = true,
            ChordMod::Alt => mods.alt = true,
            ChordMod::Shift => mods.shift = true,
            ChordMod::Super => mods.logo = true,
        }
    }
    let key = match chord.key() {
        ChordKey::Char(c) if mods.cmd || mods.ctrl => Key::Command(c),
        ChordKey::Char(c) if mods.shift => Key::Text(shifted(c).to_string()),
        ChordKey::Char(c) => Key::Text(c.to_string()),
        ChordKey::Enter => Key::Enter,
        ChordKey::Tab => Key::Tab,
        ChordKey::Escape => Key::Escape,
        ChordKey::Backspace => Key::Backspace,
        ChordKey::Delete => Key::Delete,
        ChordKey::Home => Key::Home,
        ChordKey::End => Key::End,
        ChordKey::PageUp => Key::PageUp,
        ChordKey::PageDown => Key::PageDown,
        ChordKey::Left => Key::Left,
        ChordKey::Right => Key::Right,
        ChordKey::Up => Key::Up,
        ChordKey::Down => Key::Down,
        ChordKey::Function(n) => Key::Function(n),
        other => return Err(format!("no key event for {other:?}")),
    };
    Ok(KeyEvent { key, mods })
}

/// What a US layout types for `c` with Shift.
fn shifted(c: char) -> char {
    let (plain, shifted) = ("`1234567890-=[]\\;',./", "~!@#$%^&*()_+{}|:\"<>?");
    plain
        .chars()
        .position(|p| p == c)
        .and_then(|i| shifted.chars().nth(i))
        .unwrap_or(c.to_ascii_uppercase())
}
