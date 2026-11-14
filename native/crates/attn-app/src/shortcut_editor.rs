//! The editor that rebinds shortcuts: each action's keys recorded from the keyboard, as one
//! combination or a leader and a second key, a reset to the default, an unbind, and Restore
//! Defaults. Every change goes to the daemon's `keybindings_config` setting, shared with the
//! Tauri app.

use std::collections::BTreeMap;

use attn_protocol::SetSettingMessage;
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{
    Chord, ChordKey, ChordMod, El, KeyPlatform, Shortcut, ShortcutError, TextEdit, Tx, col, row,
    text,
};
use rune_kit::{
    Badge, BadgeVariant, Button, ButtonVariant, Dialog, Kit, ShortcutRecorder,
    ShortcutRecorderState, TextInput, shortcut_error_message,
};
use serde_json::{Map, Value, json};

use crate::State;
use crate::action::{Action, Category};
use crate::keymap::{self, Binding, Keymap};
use crate::shell::Layer;

const THEN_RECORDER: &str = "shortcut-editor.then";
const FILTER: &str = "shortcut-editor.filter";
/// The kit dialog's own inset, which the editor's filter, list and footer line up with.
const PAD: f32 = 16.0;

/// The editor's recordings, its filter and a reassignment waiting for the user's answer. They
/// start over each time the editor opens.
#[derive(Default)]
pub(crate) struct Editor {
    filter: TextEdit,
    recorders: BTreeMap<Action, ShortcutRecorderState>,
    recording: Option<(Action, Mode)>,
    /// Records the key that follows a chord's leader.
    then: ShortcutRecorderState,
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
fn setting(state: &State) -> Option<&str> {
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
    write(state, Value::Object(config).to_string(), tx);
}

/// The setting with each change applied: a value to keep for an action, or `None` to drop its
/// rebind. Everything else in the setting, such as the Tauri app's dock, stays.
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
    let sections: Vec<El> = CATEGORIES
        .into_iter()
        .filter_map(|category| {
            let rows: Vec<El> = Action::ALL
                .iter()
                .filter(|a| a.category() == category && matches(a))
                .map(|action| action_row(state, &keymap, action, t, k))
                .collect();
            (!rows.is_empty()).then(|| {
                col()
                    .key(format!("shortcut-editor.category.{category:?}"))
                    .w_full()
                    .gap(space::S1)
                    .child(
                        text(category.label())
                            .role("heading")
                            .label(category.label())
                            .font(type_::SECTION)
                            .color(t.text_tertiary),
                    )
                    .children(rows)
            })
        })
        .collect();
    let empty = sections.is_empty().then(|| {
        let said = format!("No shortcuts match “{}”", editor.filter.text.trim());
        text(said.clone())
            .key("shortcut-editor.no-matches")
            .role("status")
            .label(said)
            .font(type_::BODY)
            .color(t.text_tertiary)
    });
    let filter = TextInput::new(&editor.filter)
        .key(FILTER)
        .width(None)
        .placeholder("Filter shortcuts…")
        .label("Filter shortcuts")
        .on_edit(|state: &mut State| &mut state.window.shortcut_editor.filter)
        .on_change(|state: &mut State, _: &mut Tx| {
            let editor = &mut state.window.shortcut_editor;
            editor.pending = None;
            editor.recording = None;
        })
        .build(k);
    let body = col()
        .key("shortcut-editor.body")
        .w_full()
        .h(440.0)
        .px(PAD)
        .gap(PAD)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .children(sections)
        .children(empty);
    let footer = row()
        .key("shortcut-editor.footer")
        .w_full()
        .padding(PAD)
        .gap(space::S2)
        .justify_end()
        .child(
            Button::new("Restore Defaults")
                .on_press_tx(restore_defaults)
                .build(k)
                .key("shortcut-editor.restore"),
        )
        .child(
            Button::new("Done")
                .variant(ButtonVariant::Submit)
                .on_press(close)
                .build(k)
                .key("shortcut-editor.done"),
        );
    Dialog::new("shortcut-editor", "Customize Shortcuts")
        .description("Click a shortcut to rebind it. Changes save automatically.")
        .open(true)
        .fit()
        .width(760.0)
        .on_close(close)
        .child(col().w_full().px(PAD).pb(space::S3).child(filter))
        .child(body)
        .child(footer)
        .build(k, layers);
}

fn action_row(state: &State, keymap: &Keymap, action: &Action, t: &Tokens, k: &Kit) -> El {
    let id = action.id();
    let badge = |name: &str, variant: BadgeVariant| {
        Badge::new(name)
            .variant(variant)
            .build(k)
            .key(format!(
                "shortcut-editor.badge.{id}.{}",
                name.to_lowercase()
            ))
            .role("note")
            .label(name)
    };
    let name = row()
        .grow(1.0)
        .min_w(0.0)
        .gap(space::S2)
        .items_center()
        .child(
            text(action.label())
                .font(type_::BODY)
                .color(t.text_primary)
                .truncate()
                .shrink()
                .min_w(0.0),
        )
        .children(
            keymap
                .is_customized(action)
                .then(|| badge("Customized", BadgeVariant::Accent)),
        )
        .children(
            action
                .is_protected()
                .then(|| badge("Required", BadgeVariant::Neutral)),
        )
        .children(
            action
                .requires_terminal()
                .then(|| badge("Needs terminal", BadgeVariant::Subtle)),
        );
    let editor = &state.window.shortcut_editor;
    let controls = match &editor.pending {
        Some(pending) if pending.action == *action => reassign_prompt(keymap, pending, t, k),
        _ => controls(state, keymap, action, t, k),
    };
    row()
        .key(format!("shortcut-editor.row.{id}"))
        .role("group")
        .label(action.label())
        .w_full()
        .min_h(36.0)
        .gap(space::S3)
        .items_center()
        .child(name)
        .child(controls)
}

fn reassign_prompt(keymap: &Keymap, pending: &Pending, t: &Tokens, k: &Kit) -> El {
    let said = format!(
        "{} is {}.",
        keymap.label(pending.binding),
        quoted_list(&pending.holders)
    );
    row()
        .key("shortcut-editor.pending")
        .gap(space::S2)
        .items_center()
        .child(
            text(said.clone())
                .key("shortcut-editor.pending.text")
                .role("status")
                .label(said)
                .font(type_::BODY)
                .color(t.text_secondary),
        )
        .child(
            Button::new("Reassign")
                .variant(ButtonVariant::Submit)
                .on_press_tx(reassign)
                .build(k)
                .key("shortcut-editor.reassign"),
        )
        .child(
            Button::new("Cancel")
                .on_press(cancel_reassign)
                .build(k)
                .key("shortcut-editor.cancel"),
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

fn controls(state: &State, keymap: &Keymap, action: &Action, t: &Tokens, k: &Kit) -> El {
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
        return row()
            .gap(space::S2)
            .items_center()
            .child(
                text(lead.clone())
                    .key("shortcut-editor.then.lead")
                    .font(type_::MONO_CAPTION)
                    .color(t.text_secondary),
            )
            .child(
                ShortcutRecorder::new(THEN_RECORDER, &editor.then, |state: &mut State| {
                    &mut state.window.shortcut_editor.then
                })
                .label(format!("The key after {lead}"))
                .allow_unmodified()
                .width(160.0)
                .on_change(recorded_then)
                .error_message(message)
                .build(k),
            );
    }
    let shown = ShortcutRecorderState::new(shown_shortcut(keymap, action));
    let state_of = editor
        .recorders
        .get(action)
        .filter(|r| r.is_recording())
        .unwrap_or(&shown);
    let leader_mode = matches!(mode_of(state, action), Mode::Leader) && state_of.is_recording();
    let mut recorder = ShortcutRecorder::new(keys_key(action), state_of, {
        let action = action.clone();
        move |state: &mut State| recorder_of(state, &action)
    })
    .label(action.label())
    .width(160.0)
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
    let sequence_then = match binding {
        Some(Binding::Sequence { then, .. }) => Some(then.label_on(keymap.platform())),
        _ => None,
    };
    let risky = binding.is_some_and(|b| types_in_terminals(&b, keymap.platform()));
    let (accel, alt) = if keymap.platform() == KeyPlatform::Mac {
        ("⌘", "⌥")
    } else {
        ("Ctrl", "Alt")
    };
    let default = keymap.default_binding(action).map(|b| keymap.label(b));
    row()
        .gap(space::S2)
        .items_center()
        .children(risky.then(|| {
            let said =
                format!("No {accel}/{alt} modifier — this may collide with typing in the terminal");
            text("⚠")
                .key(format!("shortcut-editor.risky.{id}"))
                .role("note")
                .label(said)
                .font(type_::BODY)
                .color(t.text_secondary)
        }))
        .child(recorder.build(k))
        .children(sequence_then.map(|then| {
            text(format!("then {then}"))
                .key(format!("shortcut-editor.then-key.{id}"))
                .font(type_::MONO_CAPTION)
                .color(t.text_secondary)
        }))
        .child({
            let action = action.clone();
            Button::new("Chord")
                .variant(ButtonVariant::Ghost)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| record_chord(state, &action, tx))
                .build(k)
                .key(format!("shortcut-editor.chord.{id}"))
                .label("Record a chord")
        })
        .children(keymap.is_customized(action).then(|| {
            let action = action.clone();
            let said = format!("Reset to {}", default.clone().unwrap_or_default());
            Button::new("Reset")
                .variant(ButtonVariant::Ghost)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| reset(state, &action, tx))
                .build(k)
                .key(format!("shortcut-editor.reset.{id}"))
                .label(said)
        }))
        .children((!action.is_protected() && binding.is_some()).then(|| {
            let action = action.clone();
            Button::new("Unbind")
                .variant(ButtonVariant::Ghost)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| unbind(state, &action, tx))
                .build(k)
                .key(format!("shortcut-editor.unbind.{id}"))
                .label("Unbind")
        }))
}

fn message(error: &ShortcutError, shortcut: Option<&Shortcut>) -> String {
    match error {
        ShortcutError::Backend(said) => said.clone(),
        _ => shortcut_error_message(error, shortcut),
    }
}
