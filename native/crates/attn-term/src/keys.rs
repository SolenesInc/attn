use rune::{Key, KeyEvent, KeyPlatform, PhysicalKeyCode as Code};

use crate::vt::{KeyInput, OptionAsAlt, mods};

/// What a key sends to a terminal: bytes the app writes itself, or a key for the encoder.
#[derive(Debug, PartialEq, Eq)]
pub enum Press {
    Bytes(&'static [u8]),
    Encode(KeyInput),
}

/// `physical` is `Tx::physical_key`; a key with no physical origin is read from its logical
/// key as a US layout types it. None for a key the terminal does not take: ⌘ chords on macOS
/// stay with the app.
pub fn press(
    ev: &KeyEvent,
    physical: Option<Code>,
    repeat: bool,
    platform: KeyPlatform,
) -> Option<Press> {
    let m = ev.mods;
    let mac = platform == KeyPlatform::Mac;
    if mac && m.cmd {
        return None;
    }
    let plain_shift = m.shift && !m.ctrl && !m.alt && !(mac && m.logo);
    match &ev.key {
        Key::Tab if plain_shift => return Some(Press::Bytes(b"\x1b[Z")),
        Key::Enter if m.shift && !m.ctrl && !m.alt => return Some(Press::Bytes(b"\n")),
        _ => {}
    }
    let typed = match &ev.key {
        Key::Text(text) => Some(text.as_str()),
        _ => None,
    };
    let ch = match &ev.key {
        Key::Text(text) => single(text),
        Key::Command(c) => Some(*c),
        _ => None,
    };
    if mac && m.ctrl && !m.alt && !m.shift && ch.is_some_and(|c| c.eq_ignore_ascii_case(&'v')) {
        return Some(Press::Bytes(b"\x16"));
    }
    let key = physical
        .and_then(ghostty_code)
        .or_else(|| named(&ev.key))
        .or_else(|| ch.and_then(|c| by_char(c.to_ascii_lowercase())))
        .unwrap_or(UNIDENTIFIED);
    if key == UNIDENTIFIED && typed.is_none() {
        return None;
    }
    let text = typed
        .filter(|_| !m.alt)
        .filter(|t| printable(t))
        .unwrap_or_default()
        .to_owned();
    let mut held = 0;
    for (on, bit) in [
        (m.shift, mods::SHIFT),
        (m.ctrl, mods::CTRL),
        (m.alt, mods::ALT),
        (m.logo, mods::SUPER),
    ] {
        if on {
            held |= bit;
        }
    }
    let mut consumed = 0;
    if !text.is_empty() && m.shift {
        consumed |= mods::SHIFT;
    }
    let unshifted = if text.is_empty() {
        physical
            .and_then(us_char)
            .or_else(|| ch.map(|c| c.to_ascii_lowercase()))
    } else {
        unshifted_of(&text, m.shift || m.alt || m.logo)
    }
    .map_or(0, u32::from);
    Some(Press::Encode(KeyInput {
        key,
        mods: held,
        consumed,
        text,
        unshifted,
        option_as_alt: OptionAsAlt::Both,
        repeat,
    }))
}

fn single(text: &str) -> Option<char> {
    let mut chars = text.chars();
    chars.next().filter(|_| chars.next().is_none())
}

fn printable(text: &str) -> bool {
    single(text).is_some_and(|c| {
        let cp = u32::from(c);
        cp >= 0x20 && cp != 0x7f && !(0xf700..=0xf8ff).contains(&cp)
    })
}

fn unshifted_of(text: &str, modified: bool) -> Option<char> {
    let c = single(text)?;
    let lower: Vec<char> = c.to_lowercase().collect();
    if c.is_alphabetic() && lower.len() == 1 {
        return Some(lower[0]);
    }
    (!modified).then_some(c)
}

const UNIDENTIFIED: u32 = 0;

fn us_char(code: Code) -> Option<char> {
    use Code::*;
    let letters = "abcdefghijklmnopqrstuvwxyz".chars().zip([
        KeyA, KeyB, KeyC, KeyD, KeyE, KeyF, KeyG, KeyH, KeyI, KeyJ, KeyK, KeyL, KeyM, KeyN, KeyO,
        KeyP, KeyQ, KeyR, KeyS, KeyT, KeyU, KeyV, KeyW, KeyX, KeyY, KeyZ,
    ]);
    let digits = "0123456789".chars().zip([
        Digit0, Digit1, Digit2, Digit3, Digit4, Digit5, Digit6, Digit7, Digit8, Digit9,
    ]);
    let punctuation = "`\\[],=-.';/ ".chars().zip([
        Backquote,
        Backslash,
        BracketLeft,
        BracketRight,
        Comma,
        Equal,
        Minus,
        Period,
        Quote,
        Semicolon,
        Slash,
        Space,
    ]);
    letters
        .chain(digits)
        .chain(punctuation)
        .find_map(|(c, k)| (k == code).then_some(c))
}

fn by_char(c: char) -> Option<u32> {
    Some(match c {
        'a'..='z' => 20 + (c as u32 - 'a' as u32),
        '0' => 6,
        '1'..='9' => 7 + (c as u32 - '1' as u32),
        '`' => 1,
        '\\' => 2,
        '[' => 3,
        ']' => 4,
        ',' => 5,
        '=' => 16,
        '-' => 46,
        '.' => 47,
        '\'' => 48,
        ';' => 49,
        '/' => 50,
        ' ' => 63,
        _ => return None,
    })
}

fn named(key: &Key) -> Option<u32> {
    Some(match key {
        Key::Backspace => 53,
        Key::Enter => 58,
        Key::Tab => 64,
        Key::Delete => 68,
        Key::End => 69,
        Key::Home => 71,
        Key::Insert => 72,
        Key::PageDown => 73,
        Key::PageUp => 74,
        Key::Down => 75,
        Key::Left => 76,
        Key::Right => 77,
        Key::Up => 78,
        Key::Escape => 120,
        Key::Function(n @ 1..=25) => 120 + u32::from(*n),
        Key::ContextMenu => 55,
        Key::PrintScreen => 148,
        Key::ScrollLock => 149,
        Key::Pause => 150,
        _ => return None,
    })
}

fn ghostty_code(code: Code) -> Option<u32> {
    use Code::*;
    let letters = [
        KeyA, KeyB, KeyC, KeyD, KeyE, KeyF, KeyG, KeyH, KeyI, KeyJ, KeyK, KeyL, KeyM, KeyN, KeyO,
        KeyP, KeyQ, KeyR, KeyS, KeyT, KeyU, KeyV, KeyW, KeyX, KeyY, KeyZ,
    ];
    if let Some(i) = letters.iter().position(|&k| k == code) {
        return Some(20 + i as u32);
    }
    let digits = [
        Digit0, Digit1, Digit2, Digit3, Digit4, Digit5, Digit6, Digit7, Digit8, Digit9,
    ];
    if let Some(i) = digits.iter().position(|&k| k == code) {
        return Some(6 + i as u32);
    }
    let numpad = [
        Numpad0, Numpad1, Numpad2, Numpad3, Numpad4, Numpad5, Numpad6, Numpad7, Numpad8, Numpad9,
    ];
    if let Some(i) = numpad.iter().position(|&k| k == code) {
        return Some(80 + i as u32);
    }
    Some(match code {
        Backquote => 1,
        Backslash => 2,
        BracketLeft => 3,
        BracketRight => 4,
        Comma => 5,
        Equal => 16,
        IntlBackslash => 17,
        IntlRo => 18,
        IntlYen => 19,
        Minus => 46,
        Period => 47,
        Quote => 48,
        Semicolon => 49,
        Slash => 50,
        Backspace => 53,
        Enter => 58,
        Space => 63,
        Tab => 64,
        Delete => 68,
        End => 69,
        Home => 71,
        Insert => 72,
        PageDown => 73,
        PageUp => 74,
        ArrowDown => 75,
        ArrowLeft => 76,
        ArrowRight => 77,
        ArrowUp => 78,
        NumpadAdd => 90,
        NumpadDecimal => 95,
        NumpadDivide => 96,
        NumpadEnter => 97,
        NumpadEqual => 98,
        NumpadMultiply => 104,
        NumpadSubtract => 107,
        Escape => 120,
        _ => return None,
    })
}
