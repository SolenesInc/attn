//! Vim keys for reading: one parser every reading surface shares, holding the first key of a
//! two-key sequence (`gg`, `]]`, `[[`, `gd`, `gr`) until the second comes or time runs out.

use std::collections::BTreeMap;

use rune::{Key, KeyEvent, Tx};

use crate::shell::LEADER_TIMEOUT;

pub(crate) const SETTING: &str = "vim_mode_enabled";

/// Whether people read markdown documents with vim keys.
pub(crate) fn enabled(settings: &BTreeMap<String, String>) -> bool {
    attn_client::setting::on(settings, SETTING)
}

/// The first key of a two-key sequence.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Prefix {
    G,
    OpenBracket,
    CloseBracket,
}

/// A sequence's first key, heard on the reading surface `surface`, waiting for its second until
/// `until`.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Pending {
    pub(crate) prefix: Prefix,
    pub(crate) until: f64,
    surface: String,
}

/// What a key, or a sequence of two, asks a reading surface to do. A surface takes the
/// commands it has a meaning for and lets the others go on as plain keys.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Command {
    LineDown,
    LineUp,
    HalfPageDown,
    HalfPageUp,
    Top,
    Bottom,
    Search,
    NextMatch,
    PrevMatch,
    NextSection,
    PrevSection,
    Left,
    Right,
    ShiftLeft,
    ShiftRight,
    VisualLine,
    GoDefinition,
    GoReferences,
    Hover,
}

/// What the parser made of a key.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Step {
    /// The key started a sequence, or Escape dropped one: the key is taken.
    Pending,
    Command(Command),
    /// Not a vim key.
    None,
}

/// Reads `ev`, heard on the reading surface `surface`, against the sequence `pending` holds. A
/// sequence started on another surface is dropped, so the keyboard moving between surfaces
/// never pairs their keys. A second key that completes no sequence drops it and reads as a
/// first key; Escape drops it and is taken. A typed character counts whatever Option or Shift
/// made it, as `[` takes Option on some layouts.
pub(crate) fn step(
    pending: &mut Option<Pending>,
    surface: &str,
    ev: &KeyEvent,
    tx: &mut Tx,
) -> Step {
    let now = tx.clock();
    let waiting = (pending.take()).filter(|p| now < p.until && p.surface == surface);
    if waiting.is_some() && ev.key == Key::Escape {
        return Step::Pending;
    }
    if ev.mods.cmd {
        return Step::None;
    }
    if ev.mods.ctrl {
        return match ev.key {
            _ if ev.mods.alt || ev.mods.shift => Step::None,
            Key::Command(c) if c.eq_ignore_ascii_case(&'d') => Step::Command(Command::HalfPageDown),
            Key::Command(c) if c.eq_ignore_ascii_case(&'u') => Step::Command(Command::HalfPageUp),
            _ => Step::None,
        };
    }
    if let Some(prefix) = &waiting
        && let Some(command) = second(prefix.prefix, typed(ev))
    {
        return Step::Command(command);
    }
    let prefix = match typed(ev) {
        "g" => Prefix::G,
        "[" => Prefix::OpenBracket,
        "]" => Prefix::CloseBracket,
        key => return first(key).map_or(Step::None, Step::Command),
    };
    let until = now + LEADER_TIMEOUT;
    *pending = Some(Pending {
        prefix,
        until,
        surface: surface.to_owned(),
    });
    tx.wake_at(until);
    Step::Pending
}

fn typed(ev: &KeyEvent) -> &str {
    match &ev.key {
        Key::Text(text) => text.as_str(),
        _ => "",
    }
}

fn first(key: &str) -> Option<Command> {
    Some(match key {
        "j" => Command::LineDown,
        "k" => Command::LineUp,
        "h" => Command::Left,
        "l" => Command::Right,
        "H" => Command::ShiftLeft,
        "L" => Command::ShiftRight,
        "V" => Command::VisualLine,
        "K" => Command::Hover,
        "G" => Command::Bottom,
        "/" => Command::Search,
        "n" => Command::NextMatch,
        "N" => Command::PrevMatch,
        _ => return None,
    })
}

fn second(prefix: Prefix, key: &str) -> Option<Command> {
    Some(match (prefix, key) {
        (Prefix::G, "g") => Command::Top,
        (Prefix::G, "d") => Command::GoDefinition,
        (Prefix::G, "r") => Command::GoReferences,
        (Prefix::OpenBracket, "[") => Command::PrevSection,
        (Prefix::CloseBracket, "]") => Command::NextSection,
        _ => return None,
    })
}
