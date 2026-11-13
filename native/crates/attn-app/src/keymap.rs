use std::collections::BTreeMap;

use rune::{Chord, KeyEvent, KeyPlatform, Tx};
use serde_json::Value;

use crate::action::Action;

/// The daemon setting that holds the user's rebinds and the dock, shared with the Tauri app.
pub const SETTING: &str = "keybindings_config";

/// The keys that run an action: one chord, or a leader chord then a second key.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Binding {
    Chord(Chord),
    Sequence { leader: Chord, then: Chord },
}

impl Binding {
    fn first(&self) -> Chord {
        match *self {
            Binding::Chord(chord) => chord,
            Binding::Sequence { leader, .. } => leader,
        }
    }

    /// Whether pressing one could run the other. A chord equal to a sequence's leader
    /// collides, because the chord fires first.
    fn collides(&self, other: &Binding, platform: KeyPlatform) -> bool {
        match (self, other) {
            (
                Binding::Sequence { leader: a, then: x },
                Binding::Sequence { leader: b, then: y },
            ) => a.same_keys(b, platform) && x.same_keys(y, platform),
            _ => self.first().same_keys(&other.first(), platform),
        }
    }
}

#[derive(Clone, Debug)]
struct Entry {
    action: Action,
    binding: Binding,
    /// A focused text field keeps these keys for itself: ⌘F searches the field, ⌘↑ moves
    /// its caret.
    field_keeps: bool,
}

/// Who holds the keyboard when a key arrives.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Focus {
    pub field: bool,
    pub terminal: bool,
}

/// What a key press means to the keymap.
#[derive(Clone, Debug, PartialEq)]
pub enum Press {
    Action(Action),
    /// The first chord of one or more sequences: the next key picks among them.
    Leader {
        chord: Chord,
        sequences: Vec<(Action, Chord)>,
    },
    Unbound,
}

/// Chords to actions, for one platform, with the user's rebinds applied.
#[derive(Clone, Debug)]
pub struct Keymap {
    platform: KeyPlatform,
    entries: Vec<Entry>,
}

impl Default for Keymap {
    fn default() -> Self {
        Keymap::from_setting(None, KeyPlatform::current())
    }
}

impl Keymap {
    /// The platform's defaults with the rebinds in `setting` (the `keybindings_config`
    /// JSON) applied. A setting that does not parse leaves the defaults.
    pub fn from_setting(setting: Option<&str>, platform: KeyPlatform) -> Keymap {
        let overrides = setting
            .map(|raw| overrides(raw, platform))
            .unwrap_or_default();
        let defaults = defaults(platform);
        let mut entries = Vec::new();
        for action in Action::ALL {
            let entry = match overrides.get(action) {
                Some(Some(entry)) => entry.clone(),
                Some(None) => continue,
                None => {
                    let Some(&binding) = defaults.get(action) else {
                        continue;
                    };
                    let claimed = overrides.iter().any(|(other, claim)| {
                        other != action
                            && claim.as_ref().is_some_and(|claim| {
                                !(may_share_keys(action, other)
                                    && matches!(
                                        (binding, claim.binding),
                                        (Binding::Chord(_), Binding::Chord(_))
                                    ))
                                    && binding.collides(&claim.binding, platform)
                            })
                    });
                    if claimed {
                        continue;
                    }
                    Entry {
                        action: action.clone(),
                        binding,
                        field_keeps: field_keeps(action, platform),
                    }
                }
            };
            entries.push(entry);
        }
        entries.sort_by_key(|entry| !TERMINAL_ONLY.contains(&entry.action));
        Keymap { platform, entries }
    }

    pub fn binding(&self, action: &Action) -> Option<Binding> {
        self.entries
            .iter()
            .find(|entry| entry.action == *action)
            .map(|entry| entry.binding)
    }

    /// Whether a focused text field keeps `action`'s keys for itself.
    pub fn kept_by_fields(&self, action: &Action) -> bool {
        self.entries
            .iter()
            .any(|entry| entry.action == *action && entry.field_keeps)
    }

    /// The action a key runs where `focus` holds the keyboard, or the sequences it starts.
    pub fn press(&self, ev: &KeyEvent, tx: &Tx, focus: Focus) -> Press {
        let matches = |chord: &Chord| chord.matches_tx_on(ev, tx, self.platform);
        for entry in &self.entries {
            let Binding::Chord(chord) = entry.binding else {
                continue;
            };
            if matches(&chord) && applies(entry, focus) {
                return Press::Action(entry.action.clone());
            }
        }
        if focus.field {
            return Press::Unbound;
        }
        let mut chord = None;
        let sequences: Vec<(Action, Chord)> = self
            .entries
            .iter()
            .filter_map(|entry| match entry.binding {
                Binding::Sequence { leader, then } if matches(&leader) && applies(entry, focus) => {
                    chord.get_or_insert(leader);
                    Some((entry.action.clone(), then))
                }
                _ => None,
            })
            .collect();
        match chord {
            Some(chord) => Press::Leader { chord, sequences },
            None => Press::Unbound,
        }
    }

    /// The second key of a sequence: the action among `sequences` it picks.
    pub fn then(&self, ev: &KeyEvent, tx: &Tx, sequences: &[(Action, Chord)]) -> Option<Action> {
        sequences
            .iter()
            .find(|(_, then)| then.matches_tx_on(ev, tx, self.platform))
            .map(|(action, _)| action.clone())
    }

    pub fn matches(&self, chord: &Chord, ev: &KeyEvent, tx: &Tx) -> bool {
        chord.matches_tx_on(ev, tx, self.platform)
    }

    pub fn label(&self, binding: Binding) -> String {
        match binding {
            Binding::Chord(chord) => chord.label_on(self.platform),
            Binding::Sequence { leader, then } => format!(
                "{} {}",
                leader.label_on(self.platform),
                then.label_on(self.platform)
            ),
        }
    }
}

/// Close and send-annotations share their keys with their terminal versions; the terminal
/// version wins while a terminal holds the keyboard.
const TERMINAL_ONLY: [Action; 2] = [Action::TerminalClose, Action::TerminalSendAnnotations];

fn may_share_keys(a: &Action, b: &Action) -> bool {
    matches!(
        (a, b),
        (Action::SessionClose, Action::TerminalClose)
            | (Action::TerminalClose, Action::SessionClose)
            | (
                Action::MarkdownSendAnnotations,
                Action::TerminalSendAnnotations
            )
            | (
                Action::TerminalSendAnnotations,
                Action::MarkdownSendAnnotations
            )
    )
}

fn applies(entry: &Entry, focus: Focus) -> bool {
    if focus.field && entry.field_keeps {
        return false;
    }
    match entry.action {
        Action::TerminalClose | Action::TerminalSendAnnotations => focus.terminal,
        Action::SessionRefreshPrs => !focus.terminal,
        _ => true,
    }
}

/// The defaults a focused text field keeps. ⌘B stays the field's on macOS only: its Linux
/// keys type nothing a field takes.
fn field_keeps(action: &Action, platform: KeyPlatform) -> bool {
    match action {
        Action::TerminalToggleZoom
        | Action::TerminalFind
        | Action::SessionPrev
        | Action::SessionNext
        | Action::MarkdownSendAnnotations
        | Action::TerminalSendAnnotations => true,
        Action::SessionToggleSidebar => platform == KeyPlatform::Mac,
        _ => false,
    }
}

/// The chord `action` is bound to on `platform` before any rebind, as `Chord::parse` reads it.
pub(crate) fn default_chord(action: &Action, platform: KeyPlatform) -> Option<&'static str> {
    table(platform)
        .iter()
        .find(|(a, _)| a == action)
        .map(|&(_, spec)| spec)
}

fn table(platform: KeyPlatform) -> &'static [(Action, &'static str)] {
    if platform == KeyPlatform::Mac {
        MAC
    } else {
        LINUX
    }
}

fn defaults(platform: KeyPlatform) -> BTreeMap<Action, Binding> {
    table(platform)
        .iter()
        .map(|(action, spec)| {
            let chord = Chord::parse(spec)
                .unwrap_or_else(|why| panic!("{} default {spec:?}: {why}", action.id()));
            (action.clone(), Binding::Chord(chord))
        })
        .collect()
}

/// The user's rebinds from `keybindings_config`: a binding, or `None` for an action the user
/// unbound. Ids saved by earlier versions load under their current names; ids that no longer
/// exist and bindings that do not parse are left out.
fn overrides(raw: &str, platform: KeyPlatform) -> BTreeMap<Action, Option<Entry>> {
    let Ok(Value::Object(config)) = serde_json::from_str::<Value>(raw) else {
        return BTreeMap::new();
    };
    let Some(Value::Object(stored)) = config.get("overrides") else {
        return BTreeMap::new();
    };
    let mut out = BTreeMap::new();
    for (stored_id, value) in stored {
        let current = renamed(stored_id);
        let Some(action) = Action::from_id(&current) else {
            continue;
        };
        if current != *stored_id && stored.contains_key(&current) {
            continue;
        }
        if value.is_null() {
            out.insert(action, None);
        } else if let Some((binding, field_keeps)) = stored_binding(value, platform) {
            out.insert(
                action.clone(),
                Some(Entry {
                    action,
                    binding,
                    field_keeps,
                }),
            );
        }
    }
    out
}

fn renamed(id: &str) -> String {
    match id.strip_prefix("workspace.select") {
        Some(slot) => format!("desktop.select{slot}"),
        None => id.to_owned(),
    }
}

/// A binding as the Tauri app stores it: `{key, code?, meta?, shift?, ctrl?, alt?,
/// editableTarget?}`, or `{leader, then}` with one of those each.
fn stored_binding(value: &Value, platform: KeyPlatform) -> Option<(Binding, bool)> {
    if let (Some(leader), Some(then)) = (value.get("leader"), value.get("then")) {
        let (leader, keeps) = stored_chord(leader, platform)?;
        let (then, _) = stored_chord(then, platform)?;
        return Some((Binding::Sequence { leader, then }, keeps));
    }
    let (chord, keeps) = stored_chord(value, platform)?;
    Some((Binding::Chord(chord), keeps))
}

fn stored_chord(value: &Value, platform: KeyPlatform) -> Option<(Chord, bool)> {
    let key = value.get("key")?.as_str().filter(|key| !key.is_empty())?;
    let code = value.get("code").and_then(Value::as_str);
    let flag = |name: &str| value.get(name).and_then(Value::as_bool) == Some(true);
    let (meta, shift, ctrl, alt) = (flag("meta"), flag("shift"), flag("ctrl"), flag("alt"));
    let follows_physical_key = shift || (alt && platform == KeyPlatform::Mac);
    let key_name = match code.and_then(key_of_code) {
        Some(physical) if follows_physical_key => physical.to_owned(),
        physical => key_of_name(key).or(physical.map(str::to_owned))?,
    };
    let mut spec = Vec::new();
    if meta {
        spec.push("mod");
    } else if ctrl {
        spec.push("ctrl");
    }
    if alt {
        spec.push("alt");
    }
    if shift {
        spec.push("shift");
    }
    spec.push(&key_name);
    let chord = Chord::parse(&spec.join("+")).ok()?;
    let keeps = value.get("editableTarget").and_then(Value::as_str) == Some("native");
    Some((chord, keeps))
}

/// A DOM `KeyboardEvent.key` as a chord key name.
fn key_of_name(key: &str) -> Option<String> {
    let named = match key {
        "ArrowUp" => "up",
        "ArrowDown" => "down",
        "ArrowLeft" => "left",
        "ArrowRight" => "right",
        "Enter" => "enter",
        "Escape" => "escape",
        "Tab" => "tab",
        "Backspace" => "backspace",
        "Delete" => "delete",
        "Home" => "home",
        "End" => "end",
        "PageUp" => "pageup",
        "PageDown" => "pagedown",
        " " => "space",
        "+" => "plus",
        _ => {
            let mut chars = key.chars();
            return match (chars.next(), chars.next()) {
                (Some(c), None) if c.is_ascii_graphic() => Some(c.to_ascii_lowercase().into()),
                (Some('F'), Some(_)) => Chord::parse(&key.to_ascii_lowercase())
                    .ok()
                    .map(|_| key.to_ascii_lowercase()),
                _ => None,
            };
        }
    };
    Some(named.into())
}

/// A DOM `KeyboardEvent.code` as the key a US layout has in its place.
fn key_of_code(code: &str) -> Option<&'static str> {
    if let Some(letter) = code.strip_prefix("Key") {
        return LETTERS
            .iter()
            .copied()
            .find(|l| l.eq_ignore_ascii_case(letter));
    }
    if let Some(digit) = code.strip_prefix("Digit") {
        return DIGITS.iter().copied().find(|d| *d == digit);
    }
    Some(match code {
        "Minus" => "-",
        "Equal" => "=",
        "BracketLeft" => "[",
        "BracketRight" => "]",
        "Backquote" => "`",
        "Comma" => ",",
        "Period" => ".",
        "Slash" => "/",
        "Backslash" => "\\",
        "Semicolon" => ";",
        "Quote" => "'",
        _ => return None,
    })
}

const LETTERS: [&str; 26] = [
    "a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s",
    "t", "u", "v", "w", "x", "y", "z",
];
const DIGITS: [&str; 10] = ["0", "1", "2", "3", "4", "5", "6", "7", "8", "9"];

const MAC: &[(Action, &str)] = &[
    (Action::AppQuit, "mod+q"),
    (Action::TerminalOpen, "mod+`"),
    (Action::TerminalSplitVertical, "mod+d"),
    (Action::TerminalSplitHorizontal, "mod+shift+d"),
    (Action::TerminalToggleZoom, "mod+shift+z"),
    (Action::TerminalToggleMaximize, "mod+shift+enter"),
    (Action::TerminalClose, "mod+w"),
    (Action::TerminalFocusLeft, "mod+alt+left"),
    (Action::TerminalFocusRight, "mod+alt+right"),
    (Action::TerminalFocusUp, "mod+alt+up"),
    (Action::TerminalFocusDown, "mod+alt+down"),
    (Action::ShelfFewerTiles, "mod+alt+-"),
    (Action::ShelfMoreTiles, "mod+alt+="),
    (Action::ShelfCarryLeft, "mod+alt+shift+left"),
    (Action::ShelfCarryRight, "mod+alt+shift+right"),
    (Action::ShelfSendToShelf, "mod+alt+shift+up"),
    (Action::TilePopOut, "mod+shift+o"),
    (Action::SidebarWider, "mod+alt+]"),
    (Action::SidebarNarrower, "mod+alt+["),
    (Action::TerminalFind, "mod+f"),
    (Action::SessionNew, "mod+n"),
    (Action::SessionNewTerminal, "mod+t"),
    (Action::SessionNewHorizontal, "mod+shift+n"),
    (Action::SessionClose, "mod+w"),
    (Action::SessionPrev, "mod+up"),
    (Action::SessionNext, "mod+down"),
    (Action::SessionHistoryBack, "mod+["),
    (Action::SessionHistoryForward, "mod+]"),
    (Action::SessionOrchestrator, "mod+shift+up"),
    (Action::SessionGoToDashboard, "mod+shift+h"),
    (Action::DesktopOverview, "mod+g"),
    (Action::ProfileSwitch, "mod+shift+u"),
    (Action::SessionJumpToWaiting, "mod+j"),
    (Action::SessionNextRun, "mod+shift+j"),
    (Action::SidebarAgentList, "mod+shift+a"),
    (Action::SessionSettle, "mod+shift+e"),
    (Action::SessionPriority, "mod+shift+i"),
    (Action::SessionSnooze, "mod+shift+s"),
    (Action::SessionCancelCountdown, "mod+."),
    (Action::SessionToggleSidebar, "mod+b"),
    (Action::SessionRefreshPrs, "mod+r"),
    (Action::DesktopSelect1, "mod+1"),
    (Action::DesktopSelect2, "mod+2"),
    (Action::DesktopSelect3, "mod+3"),
    (Action::DesktopSelect4, "mod+4"),
    (Action::DesktopSelect5, "mod+5"),
    (Action::DesktopSelect6, "mod+6"),
    (Action::DesktopSelect7, "mod+7"),
    (Action::DesktopSelect8, "mod+8"),
    (Action::DesktopSelect9, "mod+9"),
    (Action::DesktopSend1, "mod+alt+1"),
    (Action::DesktopSend2, "mod+alt+2"),
    (Action::DesktopSend3, "mod+alt+3"),
    (Action::DesktopSend4, "mod+alt+4"),
    (Action::DesktopSend5, "mod+alt+5"),
    (Action::DesktopSend6, "mod+alt+6"),
    (Action::DesktopSend7, "mod+alt+7"),
    (Action::DesktopSend8, "mod+alt+8"),
    (Action::DesktopSend9, "mod+alt+9"),
    (Action::DesktopSendStay1, "mod+alt+shift+1"),
    (Action::DesktopSendStay2, "mod+alt+shift+2"),
    (Action::DesktopSendStay3, "mod+alt+shift+3"),
    (Action::DesktopSendStay4, "mod+alt+shift+4"),
    (Action::DesktopSendStay5, "mod+alt+shift+5"),
    (Action::DesktopSendStay6, "mod+alt+shift+6"),
    (Action::DesktopSendStay7, "mod+alt+shift+7"),
    (Action::DesktopSendStay8, "mod+alt+shift+8"),
    (Action::DesktopSendStay9, "mod+alt+shift+9"),
    (Action::DockAttention, "mod+shift+p"),
    (Action::DockAsks, "mod+shift+r"),
    (Action::UiActionMenu, "mod+k"),
    (Action::UiCommandPalette, "mod+shift+k"),
    (Action::UiOpenSettings, "mod+,"),
    (Action::UiShowShortcuts, "mod+/"),
    (Action::UiIncreaseFontSize, "mod+="),
    (Action::UiDecreaseFontSize, "mod+-"),
    (Action::UiResetFontSize, "mod+0"),
    (Action::NotebookOpenTile, "mod+alt+n"),
    (Action::NotebookOpenFullscreen, "mod+alt+shift+n"),
    (Action::FileOpen, "mod+p"),
    (Action::BoardOpen, "mod+shift+t"),
    (Action::SessionsOpen, "mod+shift+l"),
    (Action::MarkdownSendAnnotations, "mod+enter"),
    (Action::TerminalSendAnnotations, "mod+enter"),
];

/// Plain Ctrl and a letter belong to the shell on Linux, so app actions take Ctrl+Shift, or
/// Ctrl+Alt where the macOS binding already has Shift.
const LINUX: &[(Action, &str)] = &[
    (Action::AppQuit, "mod+shift+q"),
    (Action::TerminalOpen, "mod+shift+`"),
    (Action::TerminalSplitVertical, "mod+shift+d"),
    (Action::TerminalSplitHorizontal, "mod+alt+d"),
    (Action::TerminalToggleZoom, "mod+alt+z"),
    (Action::TerminalToggleMaximize, "mod+alt+enter"),
    (Action::TerminalClose, "mod+shift+w"),
    (Action::TerminalFocusLeft, "mod+shift+left"),
    (Action::TerminalFocusRight, "mod+shift+right"),
    (Action::TerminalFocusUp, "mod+shift+up"),
    (Action::TerminalFocusDown, "mod+shift+down"),
    (Action::ShelfFewerTiles, "mod+alt+-"),
    (Action::ShelfMoreTiles, "mod+alt+="),
    (Action::ShelfCarryLeft, "mod+alt+shift+left"),
    (Action::ShelfCarryRight, "mod+alt+shift+right"),
    (Action::ShelfSendToShelf, "mod+alt+shift+up"),
    (Action::TilePopOut, "mod+shift+o"),
    (Action::SidebarWider, "mod+alt+]"),
    (Action::SidebarNarrower, "mod+alt+["),
    (Action::TerminalFind, "mod+shift+f"),
    (Action::SessionNew, "mod+shift+n"),
    (Action::SessionNewTerminal, "mod+shift+t"),
    (Action::SessionNewHorizontal, "mod+alt+n"),
    (Action::SessionClose, "mod+shift+w"),
    (Action::SessionPrev, "mod+shift+pageup"),
    (Action::SessionNext, "mod+shift+pagedown"),
    (Action::SessionHistoryBack, "mod+shift+["),
    (Action::SessionHistoryForward, "mod+shift+]"),
    (Action::SessionOrchestrator, "mod+alt+o"),
    (Action::SessionGoToDashboard, "mod+alt+h"),
    (Action::DesktopOverview, "mod+shift+g"),
    (Action::ProfileSwitch, "mod+alt+u"),
    (Action::SessionJumpToWaiting, "mod+shift+j"),
    (Action::SessionNextRun, "mod+alt+j"),
    (Action::SidebarAgentList, "mod+alt+a"),
    (Action::SessionSettle, "mod+alt+e"),
    (Action::SessionPriority, "mod+alt+i"),
    (Action::SessionSnooze, "mod+alt+s"),
    (Action::SessionCancelCountdown, "mod+shift+."),
    (Action::SessionToggleSidebar, "mod+alt+b"),
    (Action::SessionRefreshPrs, "mod+shift+r"),
    (Action::DesktopSelect1, "mod+shift+1"),
    (Action::DesktopSelect2, "mod+shift+2"),
    (Action::DesktopSelect3, "mod+shift+3"),
    (Action::DesktopSelect4, "mod+shift+4"),
    (Action::DesktopSelect5, "mod+shift+5"),
    (Action::DesktopSelect6, "mod+shift+6"),
    (Action::DesktopSelect7, "mod+shift+7"),
    (Action::DesktopSelect8, "mod+shift+8"),
    (Action::DesktopSelect9, "mod+shift+9"),
    (Action::DesktopSend1, "mod+alt+1"),
    (Action::DesktopSend2, "mod+alt+2"),
    (Action::DesktopSend3, "mod+alt+3"),
    (Action::DesktopSend4, "mod+alt+4"),
    (Action::DesktopSend5, "mod+alt+5"),
    (Action::DesktopSend6, "mod+alt+6"),
    (Action::DesktopSend7, "mod+alt+7"),
    (Action::DesktopSend8, "mod+alt+8"),
    (Action::DesktopSend9, "mod+alt+9"),
    (Action::DesktopSendStay1, "mod+alt+shift+1"),
    (Action::DesktopSendStay2, "mod+alt+shift+2"),
    (Action::DesktopSendStay3, "mod+alt+shift+3"),
    (Action::DesktopSendStay4, "mod+alt+shift+4"),
    (Action::DesktopSendStay5, "mod+alt+shift+5"),
    (Action::DesktopSendStay6, "mod+alt+shift+6"),
    (Action::DesktopSendStay7, "mod+alt+shift+7"),
    (Action::DesktopSendStay8, "mod+alt+shift+8"),
    (Action::DesktopSendStay9, "mod+alt+shift+9"),
    (Action::DockAttention, "mod+alt+p"),
    (Action::DockAsks, "mod+alt+r"),
    (Action::UiActionMenu, "mod+shift+k"),
    (Action::UiCommandPalette, "mod+alt+k"),
    (Action::UiOpenSettings, "mod+shift+,"),
    (Action::UiShowShortcuts, "mod+shift+/"),
    (Action::UiIncreaseFontSize, "mod+shift+="),
    (Action::UiDecreaseFontSize, "mod+shift+-"),
    (Action::UiResetFontSize, "mod+shift+0"),
    (Action::NotebookOpenTile, "mod+alt+shift+n"),
    (Action::NotebookOpenFullscreen, "mod+alt+shift+f"),
    (Action::FileOpen, "mod+shift+p"),
    (Action::BoardOpen, "mod+alt+g"),
    (Action::SessionsOpen, "mod+alt+l"),
    (Action::MarkdownSendAnnotations, "mod+shift+enter"),
    (Action::TerminalSendAnnotations, "mod+shift+enter"),
];
