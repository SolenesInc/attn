//! The editor that rebinds shortcuts: each action's keys recorded from the keyboard, as one
//! combination or a leader and a second key, a reset to the default, an unbind, and Restore
//! Defaults. Every change goes to the daemon's `keybindings_config` setting, shared with the
//! Tauri app.

use std::collections::BTreeMap;

use attn_protocol::SetSettingMessage;
use attn_ui::blocks::DialogSize;
use attn_ui::blocks::{Actions, Block, Inline, List, Notice, Prose, Section, Stack, Status};
use attn_ui::system::{
    Button, FieldState, IconSize, ListRow, Look, Reveal, Size, Tone, Type, chip_button,
    icon_button, keycaps, line_box, tag, words,
};
use attn_ui::{Dialog, Tokens, glyph, keycap};
use rune::{Chord, ChordKey, ChordMod, El, KeyPlatform, Shortcut, ShortcutError, TextEdit, Tx};
use rune_kit::{Kit, ShortcutRecorder, ShortcutRecorderState, TextInput, shortcut_error_message};
use serde_json::{Map, Value, json};

use crate::State;
use crate::action::{Action, Category};
use crate::keymap::{self, Binding, Keymap};
use crate::shell::Layer;
use crate::sidebar::dock;

const THEN_RECORDER: &str = "shortcut-editor.then";
const FILTER: &str = "shortcut-editor.filter";
/// The editor's recordings, its filter and a reassignment waiting for the user's answer. They
/// start over each time the editor opens.
#[derive(Default)]
pub(crate) struct Editor {
    filter: TextEdit,
    recorders: BTreeMap<Action, ShortcutRecorderState>,
    recording: Option<(Action, Mode)>,
    /// Records the key that follows a chord's leader.
    then: ShortcutRecorderState,
    /// Records Quick Capture's system-wide shortcut.
    capture: ShortcutRecorderState,
    pending: Option<Pending>,
    /// The setting as last written, until the daemon's value is it, so a change made before
    /// the last one comes back builds on it.
    written: Option<String>,
}

#[derive(Clone, Debug)]
enum Mode {
    Combination,
    Leader,
    Then { leader: Shortcut },
}

/// Keys other actions hold, waiting for Reassign or Cancel.
#[derive(Clone, Debug)]
struct Pending {
    action: Action,
    stored: Option<Value>,
    binding: Binding,
    holders: Vec<Action>,
}

pub(crate) fn is_open(state: &State) -> bool {
    state.window.layer == Some(Layer::ShortcutEditor)
}

pub(crate) fn open(state: &mut State, tx: &mut Tx) {
    let written = state.window.shortcut_editor.written.take();
    state.window.shortcut_editor = Editor {
        written,
        ..Editor::default()
    };
    state.window.layer = Some(Layer::ShortcutEditor);
    tx.focus(FILTER, false);
}

fn close(state: &mut State) {
    let editor = &mut state.window.shortcut_editor;
    if editor.pending.take().is_some() {
        return;
    }
    editor.recording = None;
    state.window.layer = None;
}

/// The setting the editor shows and changes: what it last wrote, until the daemon's value
/// moves on from what it was then.
pub(crate) fn setting(state: &State) -> Option<&str> {
    match &state.window.shortcut_editor.written {
        Some(written) => Some(written),
        None => state
            .facts
            .settings
            .get(keymap::SETTING)
            .map(String::as_str),
    }
}

fn keymap(state: &State) -> Keymap {
    Keymap::from_setting(setting(state), KeyPlatform::current())
}

/// Lets the daemon's value take over once it moved, and forgets recorders showing keys that
/// are no longer the action's.
pub(crate) fn follow(state: &mut State) {
    let actual = state.facts.settings.get(keymap::SETTING).cloned();
    let editor = &mut state.window.shortcut_editor;
    if editor.written.is_some() && editor.written == actual {
        editor.written = None;
    }
    let keymap = keymap(state);
    state
        .window
        .shortcut_editor
        .recorders
        .retain(|action, recorder| {
            recorder.is_recording()
                || recorder.shortcut() == shown_shortcut(&keymap, action).as_ref()
        });
}

/// The keys the recorder of `action` shows: the binding, or a sequence's leader.
fn shown_shortcut(keymap: &Keymap, action: &Action) -> Option<Shortcut> {
    let chord = match keymap.binding(action)? {
        Binding::Chord(chord) => chord,
        Binding::Sequence { leader, .. } => leader,
    };
    Shortcut::parse_on(&chord.to_string(), keymap.platform()).ok()
}

/// The recorder of `action`. Reached while it does not record, a chord whose capture ended
/// without its keys is over, so the next capture records a combination.
fn recorder_of<'a>(state: &'a mut State, action: &Action) -> &'a mut ShortcutRecorderState {
    let shown = shown_shortcut(&keymap(state), action);
    let editor = &mut state.window.shortcut_editor;
    let recorder = editor
        .recorders
        .entry(action.clone())
        .or_insert_with(|| ShortcutRecorderState::new(shown));
    if !recorder.is_recording() {
        let over = match &editor.recording {
            Some((_, Mode::Leader)) => true,
            Some((_, Mode::Then { .. })) => !editor.then.is_recording(),
            _ => false,
        };
        if over {
            editor.recording = None;
        }
    }
    recorder
}

fn record_chord(state: &mut State, action: &Action, tx: &mut Tx) {
    let editor = &mut state.window.shortcut_editor;
    editor.pending = None;
    let mut recorder = ShortcutRecorderState::new(None);
    recorder.begin();
    editor.recorders.insert(action.clone(), recorder);
    editor.recording = Some((action.clone(), Mode::Leader));
    tx.focus(keys_key(action), false);
}

fn mode_of(state: &State, action: &Action) -> Mode {
    match &state.window.shortcut_editor.recording {
        Some((recording, mode)) if recording == action => mode.clone(),
        _ => Mode::Combination,
    }
}

/// The recorder of `action` heard keys: a combination to bind, a chord's leader to follow, or
/// Backspace to unbind.
fn recorded(
    state: &mut State,
    action: &Action,
    shortcut: Option<&Shortcut>,
    tx: &mut Tx,
) -> Result<(), ShortcutError> {
    state.window.shortcut_editor.pending = None;
    let Some(shortcut) = shortcut else {
        if action.is_protected() {
            return Err(ShortcutError::Backend(format!(
                "“{}” is required, so it can’t be unbound.",
                action.label()
            )));
        }
        write_overrides(state, vec![(action.clone(), Some(Value::Null))], tx);
        return Ok(());
    };
    let stored = storable(shortcut)?;
    match mode_of(state, action) {
        Mode::Combination => apply(state, action, Some(stored), tx),
        Mode::Leader => {
            let editor = &mut state.window.shortcut_editor;
            editor.then = ShortcutRecorderState::new(None);
            editor.then.begin();
            editor.recording = Some((
                action.clone(),
                Mode::Then {
                    leader: shortcut.clone(),
                },
            ));
            tx.focus(THEN_RECORDER, false);
            Ok(())
        }
        Mode::Then { .. } => Ok(()),
    }
}

/// The key after a chord's leader: the chord to bind.
fn recorded_then(
    state: &mut State,
    shortcut: Option<&Shortcut>,
    tx: &mut Tx,
) -> Result<(), ShortcutError> {
    let Some((action, Mode::Then { leader })) = state.window.shortcut_editor.recording.clone()
    else {
        return Ok(());
    };
    let Some(then) = shortcut else {
        return Ok(());
    };
    let chord = json!({"leader": storable(&leader)?, "then": storable(then)?});
    apply(state, &action, Some(chord), tx)?;
    state.window.shortcut_editor.recording = None;
    tx.focus(keys_key(&action), false);
    Ok(())
}

/// `shortcut` as `keybindings_config` stores it, refused when the keymap would read it back
/// with other modifiers than the ones pressed: Control on macOS and Super elsewhere are not
/// shortcut modifiers there.
fn storable(shortcut: &Shortcut) -> Result<Value, ShortcutError> {
    let platform = KeyPlatform::current();
    let stored = stored_combo(shortcut);
    let kept = match keymap::parse_binding(&stored, platform) {
        Some(Binding::Chord(chord)) => chord.mods_on(platform),
        _ => return Err(ShortcutError::InvalidAccelerator(shortcut.to_string())),
    };
    if kept == pressed_modifiers(shortcut).mods_on(platform) {
        return Ok(stored);
    }
    let said = if platform == KeyPlatform::Mac {
        "Control isn’t available as a shortcut modifier on macOS. Use ⌘, ⌥, or ⇧."
    } else {
        "Super isn’t available as a shortcut modifier. Use Ctrl, Alt, or Shift."
    };
    Err(ShortcutError::Backend(said.into()))
}

/// The modifier keys held for `shortcut`, as a chord on any key.
fn pressed_modifiers(shortcut: &Shortcut) -> Chord {
    let mods = shortcut.modifiers();
    [
        (mods.ctrl, ChordMod::Ctrl),
        (mods.alt, ChordMod::Alt),
        (mods.shift, ChordMod::Shift),
        (mods.logo, ChordMod::Super),
    ]
    .into_iter()
    .filter(|(held, _)| *held)
    .fold(Chord::new(ChordKey::Char('a')), |chord, (_, m)| {
        chord.with(m)
    })
}

/// Binds `action` to `stored`, or back to its default for `None`, unless another action holds
/// those keys: a required one refuses them, any other asks first.
fn apply(
    state: &mut State,
    action: &Action,
    stored: Option<Value>,
    tx: &mut Tx,
) -> Result<(), ShortcutError> {
    let keymap = keymap(state);
    let binding = match &stored {
        Some(value) => keymap::parse_binding(value, keymap.platform()),
        None => keymap.default_binding(action),
    };
    let Some(binding) = binding else {
        return Err(ShortcutError::InvalidAccelerator(String::new()));
    };
    let holders = keymap.holders(&binding, action);
    if let Some(protected) = holders.iter().find(|holder| holder.is_protected()) {
        return Err(ShortcutError::Backend(format!(
            "{} is reserved by “{}” and can’t be reassigned.",
            keymap.label(binding),
            protected.label()
        )));
    }
    if !holders.is_empty() {
        state.window.shortcut_editor.pending = Some(Pending {
            action: action.clone(),
            stored,
            binding,
            holders,
        });
        return Ok(());
    }
    let value = override_value(&keymap, action, stored, &binding);
    write_overrides(state, vec![(action.clone(), value)], tx);
    Ok(())
}

/// What `keybindings_config` keeps for `action` bound to `binding`: nothing when that is its
/// default combination, so the default follows later versions. A sequence is never a default.
fn override_value(
    keymap: &Keymap,
    action: &Action,
    stored: Option<Value>,
    binding: &Binding,
) -> Option<Value> {
    let default = keymap.default_binding(action);
    stored.filter(|_| !default.is_some_and(|default| keymap.same_keys(&default, binding)))
}

fn reassign(state: &mut State, tx: &mut Tx) {
    let Some(pending) = state.window.shortcut_editor.pending.take() else {
        return;
    };
    let value = override_value(
        &keymap(state),
        &pending.action,
        pending.stored,
        &pending.binding,
    );
    state
        .window
        .shortcut_editor
        .recorders
        .remove(&pending.action);
    let freed = pending
        .holders
        .into_iter()
        .map(|holder| (holder, Some(Value::Null)));
    let changes = std::iter::once((pending.action, value)).chain(freed);
    write_overrides(state, changes.collect(), tx);
}

fn cancel_reassign(state: &mut State) {
    let editor = &mut state.window.shortcut_editor;
    if let Some(pending) = editor.pending.take() {
        editor.recorders.remove(&pending.action);
    }
}

fn unbind(state: &mut State, action: &Action, tx: &mut Tx) {
    if action.is_protected() {
        return;
    }
    write_overrides(state, vec![(action.clone(), Some(Value::Null))], tx);
}

fn reset(state: &mut State, action: &Action, tx: &mut Tx) {
    state.window.shortcut_editor.pending = None;
    let refused = apply(state, action, None, tx).err();
    if let Some(error) = refused {
        state.window.notify(message(&error, None), tx);
    }
}

fn restore_defaults(state: &mut State, tx: &mut Tx) {
    let editor = &mut state.window.shortcut_editor;
    editor.pending = None;
    editor.recording = None;
    let mut config = config_of(setting(state));
    config.insert("overrides".into(), Value::Object(Map::new()));
    config.insert("dock".into(), dock::default_dock());
    write(state, Value::Object(config).to_string(), tx);
}

/// The setting with each change applied: a value to keep for an action, or `None` to drop its
/// rebind. Everything else in the setting, such as the dock, stays.
fn write_overrides(state: &mut State, changes: Vec<(Action, Option<Value>)>, tx: &mut Tx) {
    let mut config = config_of(setting(state));
    let overrides = config
        .entry("overrides")
        .or_insert_with(|| Value::Object(Map::new()));
    if !overrides.is_object() {
        *overrides = Value::Object(Map::new());
    }
    let overrides = overrides.as_object_mut().expect("an object");
    for (action, value) in changes {
        match value {
            Some(value) => overrides.insert(action.id().into(), value),
            None => overrides.remove(action.id()),
        };
    }
    write(state, Value::Object(config).to_string(), tx);
}

/// The setting with its dock replaced by `dock`, keeping the rebinds.
pub(crate) fn write_dock(state: &mut State, dock: Value, tx: &mut Tx) {
    let mut config = config_of(setting(state));
    config.insert("dock".into(), dock);
    write(state, Value::Object(config).to_string(), tx);
}

fn config_of(setting: Option<&str>) -> Map<String, Value> {
    let mut config = match setting.map(serde_json::from_str::<Value>) {
        Some(Ok(Value::Object(config))) => config,
        _ => Map::new(),
    };
    config.entry("version").or_insert(json!(1));
    config
}

fn write(state: &mut State, value: String, tx: &mut Tx) {
    state.window.shortcut_editor.written = Some(value.clone());
    let sent = value.clone();
    state.ask(
        move |id| SetSettingMessage {
            key: keymap::SETTING.into(),
            value: sent,
            request_id: Some(id),
            ..SetSettingMessage::new()
        },
        move |state, answer, tx| {
            let Err(reason) = answer.outcome() else {
                return;
            };
            let editor = &mut state.window.shortcut_editor;
            if editor.written.as_deref() == Some(value.as_str()) {
                editor.written = None;
            }
            state
                .window
                .notify(format!("Could not save shortcuts: {reason}"), tx);
        },
        tx,
    );
}

/// A recorded combination as the Tauri app stores it: the key it types under the accelerator,
/// with its physical key as `code` where the typed key depends on the layout.
fn stored_combo(shortcut: &Shortcut) -> Value {
    let mac = KeyPlatform::current() == KeyPlatform::Mac;
    let mods = shortcut.modifiers();
    let display = shortcut.to_string();
    let name = display.rsplit('+').next().unwrap_or_default();
    let typed = shortcut
        .keycaps()
        .pop()
        .filter(|cap| mac && cap.chars().count() == 1)
        .map(|cap| cap.to_lowercase());
    let (key, named) = match dom_key(name) {
        Some(DomKey::Char(c)) => (typed.unwrap_or_else(|| c.to_string()), false),
        Some(DomKey::Named(named)) => (named.to_owned(), true),
        None => (name.to_owned(), true),
    };
    let code = format!("{:?}", shortcut.physical_key());
    let mut stored = Map::new();
    stored.insert("key".into(), json!(key));
    if code.starts_with("Digit") || named || (mods.alt && mac) {
        stored.insert("code".into(), json!(code));
    }
    if mods.cmd || mods.ctrl || mods.logo {
        stored.insert("meta".into(), json!(true));
    }
    if mods.alt {
        stored.insert("alt".into(), json!(true));
    }
    if mods.shift {
        stored.insert("shift".into(), json!(true));
    }
    Value::Object(stored)
}

enum DomKey {
    Char(char),
    Named(&'static str),
}

/// A physical key's name as `KeyboardEvent.key` has it on a US layout.
fn dom_key(name: &str) -> Option<DomKey> {
    let mut chars = name.chars();
    if let (Some(c), None) = (chars.next(), chars.next()) {
        return Some(DomKey::Char(c));
    }
    let char_of = |c| Some(DomKey::Char(c));
    match name {
        "minus" => char_of('-'),
        "equal" => char_of('='),
        "comma" => char_of(','),
        "period" => char_of('.'),
        "slash" => char_of('/'),
        "semicolon" => char_of(';'),
        "quote" => char_of('\''),
        "bracketleft" => char_of('['),
        "bracketright" => char_of(']'),
        "backslash" => char_of('\\'),
        "backquote" => char_of('`'),
        "space" => char_of(' '),
        _ => Some(DomKey::Named(match name {
            "escape" => "Escape",
            "enter" => "Enter",
            "tab" => "Tab",
            "backspace" => "Backspace",
            "delete" => "Delete",
            "insert" => "Insert",
            "home" => "Home",
            "end" => "End",
            "pageup" => "PageUp",
            "pagedown" => "PageDown",
            "left" => "ArrowLeft",
            "right" => "ArrowRight",
            "up" => "ArrowUp",
            "down" => "ArrowDown",
            _ => return function_key(name),
        })),
    }
}

fn function_key(name: &str) -> Option<DomKey> {
    const KEYS: [&str; 24] = [
        "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12", "F13", "F14",
        "F15", "F16", "F17", "F18", "F19", "F20", "F21", "F22", "F23", "F24",
    ];
    KEYS.iter()
        .find(|key| key.eq_ignore_ascii_case(name))
        .map(|key| DomKey::Named(key))
}

fn keys_key(action: &Action) -> String {
    format!("shortcut-editor.keys.{}", action.id())
}

/// A combination without ⌘ or ⌥ (Ctrl or Alt elsewhere) types in a terminal.
fn types_in_terminals(binding: &Binding, platform: KeyPlatform) -> bool {
    let first = match binding {
        Binding::Chord(chord) => chord,
        Binding::Sequence { leader, .. } => leader,
    };
    !first.mods_on(platform).iter().any(|m| {
        matches!(
            m,
            rune::ChordMod::Primary | rune::ChordMod::Ctrl | rune::ChordMod::Alt
        )
    })
}

const CATEGORIES: [Category; 5] = [
    Category::Sessions,
    Category::Panes,
    Category::Markdown,
    Category::Review,
    Category::App,
];

/// The editor over the window, while it is open.
pub(crate) fn view(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    if !is_open(state) {
        return;
    }
    let k = attn_ui::kit(t);
    let keymap = keymap(state);
    let editor = &state.window.shortcut_editor;
    let query = editor.filter.text.trim().to_lowercase();
    let matches = |action: &Action| {
        query.is_empty()
            || action.label().to_lowercase().contains(&query)
            || keymap
                .binding(action)
                .is_some_and(|b| keymap.label(b).to_lowercase().contains(&query))
    };
    let sections: Vec<Block> = CATEGORIES
        .into_iter()
        .filter_map(|category| {
            let rows: Vec<El> = Action::ALL
                .iter()
                .filter(|a| a.category() == category && matches(a))
                .map(|action| action_row(state, &keymap, action, t, k))
                .collect();
            (!rows.is_empty()).then(|| {
                let key = format!("shortcut-editor.category.{category:?}");
                Section::new(format!("{key}.section"), category.label())
                    .push(List::new(key, category.label()).rows(rows).build(t))
                    .build(t)
            })
        })
        .collect();
    let capture = (state.capture.is_some()
        && (query.is_empty() || "quick capture system-wide".contains(&query)))
    .then(|| capture_section(state, t, k));
    let docked = query.is_empty().then(|| dock_section(state, &keymap, t));
    let empty = (sections.is_empty() && capture.is_none()).then(|| {
        let said = format!("No shortcuts match “{}”", editor.filter.text.trim());
        Status::new("shortcut-editor.no-matches", said, false).build(t)
    });
    let filter = line_box(
        TextInput::new(&editor.filter)
            .key(FILTER)
            .placeholder("Filter shortcuts…")
            .label("Filter shortcuts")
            .on_edit(|state: &mut State| &mut state.window.shortcut_editor.filter)
            .on_change(|state: &mut State, _: &mut Tx| {
                let editor = &mut state.window.shortcut_editor;
                editor.pending = None;
                editor.recording = None;
            }),
        FieldState::Ready,
        t,
    );
    Dialog::new("shortcut-editor", "Customize Shortcuts")
        .close_button()
        .lead("Click a shortcut to rebind it. Changes save automatically.")
        .size(DialogSize::Sheet)
        .on_close(close)
        .top(
            Stack::tight()
                .key("shortcut-editor.search")
                .w_full()
                .child(filter.into()),
        )
        .push_all(capture)
        .push_all(docked)
        .push_all(sections)
        .push_all(empty)
        .actions(
            Actions::new("shortcut-editor.actions")
                .push(
                    Button::new("Restore Defaults")
                        .on_press_tx(restore_defaults)
                        .build(t)
                        .key("shortcut-editor.restore"),
                )
                .then_end()
                .push(
                    Button::new("Done")
                        .look(Look::Primary)
                        .on_press(close)
                        .build(t)
                        .key("shortcut-editor.done"),
                ),
        )
        .build(t, layers);
}

/// A small button with words, for a row's or a section's controls.
fn small(words: &'static str, look: Look, t: &Tokens) -> Button {
    let _ = t;
    Button::new(words).look(look).size(Size::Compact)
}

/// The Dock section: the dock's shortcuts in order, each moved up or down or taken out.
fn dock_section(state: &State, keymap: &Keymap, t: &Tokens) -> Block {
    let pinned = dock::pinned(state);
    let hint = format!(
        "Shortcuts shown in the sidebar dock, in order. Reorder or remove them here, or pin any shortcut with {} below.",
        glyph::STAR_OPEN
    );
    let last = pinned.len().saturating_sub(1);
    let rows: Vec<El> = pinned
        .iter()
        .enumerate()
        .map(|(i, action)| {
            let id = action.id();
            let name = action.label();
            let keys = keymap
                .binding(action)
                .map(|b| keymap.keycaps(b))
                .unwrap_or_default();
            let mover =
                |key: String, glyph: &'static str, said: String, later: bool, disabled: bool| {
                    let action = action.clone();
                    El::from(
                        icon_button(key, said, glyph, IconSize::Small)
                            .disabled(disabled)
                            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                                dock::shift(state, &action, later, tx)
                            })
                            .build(t),
                    )
                };
            let remove = {
                let action = action.clone();
                El::from(
                    icon_button(
                        format!("shortcut-editor.dock.remove.{id}"),
                        format!("Remove {name} from dock"),
                        glyph::TIMES,
                        IconSize::Small,
                    )
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        dock::pin(state, &action, false, tx)
                    })
                    .build(t),
                )
            };
            ListRow::new(format!("shortcut-editor.dock.{id}"), name)
                .still()
                .title(name)
                .aside(keycaps(keys, t))
                .controls(
                    [
                        mover(
                            format!("shortcut-editor.dock.up.{id}"),
                            glyph::ARROW_UP,
                            format!("Move {name} up"),
                            false,
                            i == 0,
                        ),
                        mover(
                            format!("shortcut-editor.dock.down.{id}"),
                            glyph::ARROW_DOWN,
                            format!("Move {name} down"),
                            true,
                            i == last,
                        ),
                        remove,
                    ],
                    Reveal::Always,
                )
                .build(t)
        })
        .collect();
    let list = if rows.is_empty() {
        Prose::lead(
            "shortcut-editor.dock.empty",
            "No shortcuts pinned to the dock.",
        )
        .quiet(true)
        .build(t)
    } else {
        List::new("shortcut-editor.dock.list", "Dock")
            .rows(rows)
            .build(t)
    };
    Section::new("shortcut-editor.dock", "Dock")
        .region("Dock")
        .description(hint)
        .push(list)
        .build(t)
}

fn action_row(state: &State, keymap: &Keymap, action: &Action, t: &Tokens, k: &Kit) -> El {
    let id = action.id();
    let badge = |name: &'static str, tone: Tone| -> El {
        El::from(tag(
            format!("shortcut-editor.badge.{id}.{}", name.to_lowercase()),
            name,
            tone,
            t,
        ))
        .role("note")
        .label(name)
    };
    let editor = &state.window.shortcut_editor;
    let controls = match &editor.pending {
        Some(pending) if pending.action == *action => vec![reassign_prompt(keymap, pending, t)],
        _ => controls(state, keymap, action, t, k),
    };
    let mut row = ListRow::new(format!("shortcut-editor.row.{id}"), action.label())
        .still()
        .role("group")
        .title(action.label());
    if keymap.is_customized(action) {
        row = row.tag(badge("Customized", Tone::Warn));
    }
    if action.is_protected() {
        row = row.tag(badge("Required", Tone::Neutral));
    }
    if action.requires_terminal() {
        row = row.tag(badge("Needs terminal", Tone::Neutral));
    }
    row.controls(controls, Reveal::Always)
        .controls_grow()
        .build(t)
}

fn reassign_prompt(keymap: &Keymap, pending: &Pending, t: &Tokens) -> El {
    let said = format!(
        "{} is {}.",
        keymap.label(pending.binding),
        quoted_list(&pending.holders)
    );
    Inline::tight()
        .key("shortcut-editor.pending")
        .child(
            words(said.clone(), Type::Small, t.text_secondary)
                .key("shortcut-editor.pending.text")
                .role("status")
                .label(said),
        )
        .child(
            small("Reassign", Look::Primary, t)
                .on_press_tx(reassign)
                .build(t)
                .key("shortcut-editor.reassign")
                .into(),
        )
        .child(
            small("Cancel", Look::Plain, t)
                .on_press(cancel_reassign)
                .build(t)
                .key("shortcut-editor.cancel")
                .into(),
        )
}

/// “A”, “B” and “C”.
fn quoted_list(actions: &[Action]) -> String {
    let quoted: Vec<String> = actions
        .iter()
        .map(|action| format!("“{}”", action.label()))
        .collect();
    match quoted.split_last() {
        Some((last, rest)) if !rest.is_empty() => format!("{} and {last}", rest.join(", ")),
        _ => quoted.concat(),
    }
}

/// The keys of a shortcut, as a button that starts recording when pressed.
fn keys_button(keymap: &Keymap, action: &Action, t: &Tokens) -> El {
    let caps = shown_shortcut(keymap, action)
        .map(|shortcut| shortcut.keycaps())
        .unwrap_or_default();
    let (face, said) = match caps.is_empty() {
        true => (
            words("Unassigned", Type::Small, t.text_dimmed),
            "Unassigned".to_owned(),
        ),
        false => (keycap::Keys::raised(&caps).build(t), caps.join(" ")),
    };
    let key = keys_key(action);
    let action = action.clone();
    let button = chip_button(key.clone(), action.label(), face.a11y_hidden(), false, t)
        .a11y_value(said)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            state.window.shortcut_editor.pending = None;
            recorder_of(state, &action).begin();
            tx.focus(keys_key(&action), false);
        });
    recorder_place(key, button)
}

/// `face` where a `ShortcutRecorder` keyed `key` puts its control, so focus stays on the
/// control as it turns from the recorder to `face` and back.
fn recorder_place(key: String, face: El) -> El {
    Stack::tight().key(format!("{key}-field")).child(face)
}

/// A recorder's width: the longest chord, "⌃⌥⇧⌘ PageDown", with its frame.
const RECORDER_W: f32 = 160.0;

fn controls(state: &State, keymap: &Keymap, action: &Action, t: &Tokens, k: &Kit) -> Vec<El> {
    let editor = &state.window.shortcut_editor;
    let id = action.id();
    let binding = keymap.binding(action);
    let then = match &editor.recording {
        Some((recording, Mode::Then { leader }))
            if recording == action && editor.then.is_recording() =>
        {
            Some(leader)
        }
        _ => None,
    };
    if let Some(leader) = then {
        let lead = format!("{} then…", leader.keycaps().join(""));
        return vec![
            words(lead.clone(), Type::Mono, t.text_secondary).key("shortcut-editor.then.lead"),
            ShortcutRecorder::new(THEN_RECORDER, &editor.then, |state: &mut State| {
                &mut state.window.shortcut_editor.then
            })
            .label(format!("The key after {lead}"))
            .allow_unmodified()
            .width(RECORDER_W)
            .on_change(recorded_then)
            .error_message(message)
            .build(k),
        ];
    }
    let risky = binding.is_some_and(|b| types_in_terminals(&b, keymap.platform()));
    let recording = editor.recorders.get(action).filter(|r| r.is_recording());
    let keys = match recording {
        Some(recorder) => {
            let leader_mode = matches!(mode_of(state, action), Mode::Leader);
            let mut recorder = ShortcutRecorder::new(keys_key(action), recorder, {
                let action = action.clone();
                move |state: &mut State| recorder_of(state, &action)
            })
            .label(action.label())
            .width(RECORDER_W)
            .on_change({
                let action = action.clone();
                move |state: &mut State, shortcut: Option<&Shortcut>, tx: &mut Tx| {
                    recorded(state, &action, shortcut, tx)
                }
            })
            .error_message(move |error, shortcut| {
                let bare = shortcut.is_some_and(|s| s.require_modifier().is_err());
                if leader_mode && bare && matches!(error, ShortcutError::InvalidAccelerator(_)) {
                    let (accel, alt) = if KeyPlatform::current() == KeyPlatform::Mac {
                        ("⌘", "⌥")
                    } else {
                        ("Ctrl", "Alt")
                    };
                    return format!("A chord leader needs a {accel} or {alt} modifier.");
                }
                message(error, shortcut)
            });
            if !leader_mode {
                recorder = recorder.allow_unmodified();
            }
            recorder.build(k)
        }
        None => keys_button(keymap, action, t),
    };
    let sequence_then = match binding {
        Some(Binding::Sequence { then, .. }) => Some(then.label_on(keymap.platform())),
        _ => None,
    };
    let (accel, alt) = if keymap.platform() == KeyPlatform::Mac {
        ("⌘", "⌥")
    } else {
        ("Ctrl", "Alt")
    };
    let default = keymap.default_binding(action).map(|b| keymap.label(b));
    let pinned = dock::pinned(state).contains(action);
    let (star, star_said) = match pinned {
        true => (glyph::STAR, "Remove from dock"),
        false => (glyph::STAR_OPEN, "Add to dock"),
    };
    let mut out = Vec::new();
    if risky {
        let said =
            format!("No {accel}/{alt} modifier — this may collide with typing in the terminal");
        out.push(
            El::from(tag(
                format!("shortcut-editor.risky.{id}"),
                "⚠",
                Tone::Warn,
                t,
            ))
            .role("note")
            .label(said),
        );
    }
    out.push(keys);
    if let Some(then) = sequence_then {
        out.push(
            words(format!("then {then}"), Type::Mono, t.text_secondary)
                .key(format!("shortcut-editor.then-key.{id}")),
        );
    }
    out.push({
        let action = action.clone();
        small("chord", Look::Plain, t)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| record_chord(state, &action, tx))
            .build(t)
            .key(format!("shortcut-editor.chord.{id}"))
            .label("Record a chord")
            .into()
    });
    out.push({
        let action = action.clone();
        El::from(
            icon_button(
                format!("shortcut-editor.dock-pin.{id}"),
                star_said,
                star,
                IconSize::Small,
            )
            .selected(pinned)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                dock::pin(state, &action, !pinned, tx)
            })
            .build(t),
        )
        .a11y_value(if pinned { "pinned" } else { "not pinned" })
    });
    if keymap.is_customized(action) {
        let action = action.clone();
        let said = format!("Reset to {}", default.clone().unwrap_or_default());
        out.push(El::from(
            icon_button(
                format!("shortcut-editor.reset.{id}"),
                said,
                glyph::UNDO,
                IconSize::Small,
            )
            .on_press_tx(move |state: &mut State, tx: &mut Tx| reset(state, &action, tx))
            .build(t),
        ));
    }
    if !action.is_protected() && binding.is_some() {
        let action = action.clone();
        out.push(El::from(
            icon_button(
                format!("shortcut-editor.unbind.{id}"),
                "Unbind",
                glyph::TIMES,
                IconSize::Small,
            )
            .on_press_tx(move |state: &mut State, tx: &mut Tx| unbind(state, &action, tx))
            .build(t),
        ));
    }
    out
}

fn message(error: &ShortcutError, shortcut: Option<&Shortcut>) -> String {
    match error {
        ShortcutError::Backend(said) => said.clone(),
        _ => shortcut_error_message(error, shortcut),
    }
}

const CAPTURE_RECORDER: &str = "shortcut-editor.capture.keys";

/// Quick Capture's system-wide shortcut: the one chosen, changed by recording, reset to the
/// default or turned off, with what the system holds now.
fn capture_section(state: &State, t: &Tokens, k: &Kit) -> Block {
    let global = &state.capture_shortcut;
    let recorder = &state.window.shortcut_editor.capture;
    let chosen = match &global.named {
        Some(named) => named.clone(),
        None => global.active.clone(),
    };
    let change = match recorder.is_recording() {
        true => ShortcutRecorder::new(CAPTURE_RECORDER, recorder, capture_recorder)
            .label("Quick Capture shortcut")
            .width(CAPTURE_RECORDER_W)
            .on_change(
                |state: &mut State, shortcut: Option<&Shortcut>, _: &mut Tx| {
                    let next = shortcut.map(crate::quick_capture::shortcut::stored);
                    crate::quick_capture::shortcut::choose(state, next)
                        .map_err(ShortcutError::Backend)
                },
            )
            .error_message(|error, shortcut| match error {
                ShortcutError::InvalidAccelerator(_) => {
                    "Use Command, Control or Option with another key.".to_owned()
                }
                _ => message(error, shortcut),
            })
            .build(k),
        false => recorder_place(
            CAPTURE_RECORDER.to_owned(),
            small("Change", Look::Plain, t)
                .on_press_tx(|state: &mut State, tx: &mut Tx| {
                    capture_recorder(state).begin();
                    tx.focus(CAPTURE_RECORDER, false);
                })
                .build(t)
                .key(CAPTURE_RECORDER)
                .label("Quick Capture shortcut")
                .into(),
        ),
    };
    let line = Inline::loose()
        .child(
            words(
                chosen.unwrap_or_else(|| "Off".to_owned()),
                Type::Body,
                t.text_primary,
            )
            .key("shortcut-editor.capture.chosen"),
        )
        .child(change)
        .child(
            small("Reset", Look::Plain, t)
                .on_press(|state: &mut State| {
                    let _ = crate::quick_capture::shortcut::choose(
                        state,
                        Some(crate::quick_capture::shortcut::DEFAULT.to_owned()),
                    );
                })
                .build(t)
                .key("shortcut-editor.capture.reset")
                .into(),
        )
        .child(
            small("Turn off", Look::Plain, t)
                .on_press(|state: &mut State| {
                    let _ = crate::quick_capture::shortcut::choose(state, None);
                })
                .build(t)
                .key("shortcut-editor.capture.off")
                .into(),
        );
    let active = format!("Active: {}", global.active.as_deref().unwrap_or("Off"));
    Section::new("shortcut-editor.capture", "Quick Capture · system-wide")
        .region("Global Quick Capture shortcut")
        .description("Open Quick Capture over the current app. Escape keeps your draft.")
        .push(Block::custom("shortcut-editor.capture.line.block", line))
        .push_all(
            global
                .error
                .clone()
                .map(|said| Notice::error("shortcut-editor.capture.error", said).build(t)),
        )
        .push(Prose::lead("shortcut-editor.capture.active", active).build(t))
        .push(
            Prose::lead(
                "shortcut-editor.capture.note",
                "macOS system and exclusive conflicts are reported. Shared shortcuts may not be detectable.",
            )
            .quiet(true)
            .build(t),
        )
        .build(t)
}

/// The Quick Capture recorder's width: a chord with three modifiers and its frame.
const CAPTURE_RECORDER_W: f32 = 200.0;

fn active_shortcut(state: &State) -> Option<Shortcut> {
    state
        .capture_shortcut
        .active
        .as_deref()
        .and_then(|b| crate::quick_capture::shortcut::parse(b).ok())
}

/// The recorder of the Quick Capture shortcut, showing the active one while it does not record.
fn capture_recorder(state: &mut State) -> &mut ShortcutRecorderState {
    let active = active_shortcut(state);
    let recorder = &mut state.window.shortcut_editor.capture;
    if !recorder.is_recording() {
        *recorder = ShortcutRecorderState::new(active);
    }
    recorder
}
