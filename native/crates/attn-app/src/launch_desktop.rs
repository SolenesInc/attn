//! The Desktop select of an item that launches an agent, a crew member or an automation: the
//! desktop it starts on, a new desktop it names, or an empty ⌘ slot it takes.

use std::collections::BTreeMap;

use attn_protocol::{
    Desktop, LaunchDesktopGetMessage, LaunchDesktopKind, LaunchDesktopResultMessage,
    LaunchDesktopSetting,
};
use attn_ui::Tokens;
use rune::tok::type_;
use rune::{El, Key, KeyEvent, TextEdit, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize, Dialog, Select, SelectAppearance, TextInput};

use crate::State;

const SLOTS: std::ops::RangeInclusive<i64> = 1..=9;

/// What the item does with a choice: `(state, item_id, setting, tx)`.
pub(crate) type Change = fn(&mut State, &str, LaunchDesktopSetting, &mut Tx);

/// One Desktop select, as its owner describes it each frame.
#[derive(Clone)]
pub(crate) struct Field {
    /// The owner's key; the select is keyed `<key>.desktop`.
    pub(crate) key: String,
    pub(crate) kind: LaunchDesktopKind,
    pub(crate) item_id: String,
    pub(crate) profile_id: String,
    pub(crate) default_name: String,
    pub(crate) value: Option<LaunchDesktopSetting>,
    pub(crate) disabled: bool,
    pub(crate) on_change: Change,
}

impl Field {
    fn select_key(&self) -> String {
        format!("{}.desktop", self.key)
    }
}

struct Naming {
    field: Field,
    layer: Option<crate::Layer>,
    slot: i64,
    name: TextEdit,
}

/// The selects' shared state: the one open, the desktops each last read, and the name being
/// typed for a new desktop.
#[derive(Default)]
pub(crate) struct Picker {
    open: Option<(Field, Option<crate::Layer>)>,
    reading: Option<Field>,
    reads: BTreeMap<String, Read>,
    naming: Option<Naming>,
}

#[derive(Default)]
struct Read {
    desktops: Option<Vec<Desktop>>,
    error: Option<String>,
}

#[derive(Clone, PartialEq)]
enum Choice {
    Saved,
    New(i64),
    Desktop(String),
}

/// How a choice the daemon has not answered yet reads: as the desktop it asks for.
pub(crate) fn choice_label(setting: &LaunchDesktopSetting) -> String {
    match &setting.label {
        Some(label) if !label.is_empty() => label.clone(),
        _ => format!(
            "{} (new)",
            setting.desktop_name.as_deref().unwrap_or_default()
        ),
    }
}

fn new_desktop(profile: &str, name: String, slot: i64) -> LaunchDesktopSetting {
    LaunchDesktopSetting {
        desktop_id: (slot > 0).then(|| format!("{profile}/desktop_{slot}")),
        desktop_name: Some(name),
        label: None,
    }
}

fn selected(value: Option<&LaunchDesktopSetting>) -> Choice {
    match value {
        Some(LaunchDesktopSetting {
            desktop_id: Some(id),
            desktop_name: None,
            ..
        }) => Choice::Desktop(id.clone()),
        _ => Choice::Saved,
    }
}

fn options(state: &State, field: &Field) -> Vec<(String, Choice)> {
    let read = state
        .window
        .launch_desktop
        .reads
        .get(&field.select_key())
        .and_then(|r| r.desktops.as_ref());
    let available: Vec<&Desktop> = read
        .unwrap_or(&state.facts.desktops)
        .iter()
        .filter(|d| d.profile_id == field.profile_id)
        .collect();
    let current = selected(field.value.as_ref());
    let mut options = Vec::new();
    if !available
        .iter()
        .any(|d| current == Choice::Desktop(d.id.clone()))
    {
        let label = field
            .value
            .as_ref()
            .map_or_else(|| "Its own desktop".to_owned(), choice_label);
        options.push((label, current));
    }
    options.push(("A new desktop…".to_owned(), Choice::New(0)));
    let mut alike: BTreeMap<String, usize> = BTreeMap::new();
    for desktop in &available {
        let name = if desktop.name.is_empty() {
            format!(
                "Desktop {}",
                desktop
                    .shortcut_slot
                    .map(|s| s.to_string())
                    .unwrap_or_default()
            )
        } else {
            desktop.name.clone()
        };
        let label = match desktop.shortcut_slot {
            Some(slot) => format!("{slot} · {name}"),
            None => name,
        };
        let seen = alike.entry(label.clone()).or_insert(0);
        *seen += 1;
        let label = if *seen == 1 {
            label
        } else {
            format!("{label} ({seen})")
        };
        options.push((label, Choice::Desktop(desktop.id.clone())));
    }
    for slot in SLOTS.filter(|s| !available.iter().any(|d| d.shortcut_slot == Some(*s))) {
        options.push((format!("Empty slot {slot}…"), Choice::New(slot)));
    }
    options
}

fn is_open(state: &State, field: &Field) -> bool {
    state
        .window
        .launch_desktop
        .open
        .as_ref()
        .is_some_and(|(open, layer)| open.key == field.key && *layer == state.window.layer)
}

fn select(state: &State, field: &Field) -> Select {
    let options = options(state, field);
    let labels: Vec<String> = options.iter().map(|(l, _)| l.clone()).collect();
    let current = selected(field.value.as_ref());
    let current = options
        .iter()
        .find(|(_, c)| *c == current)
        .map(|(l, _)| l.clone());
    let (toggled, closed) = (field.clone(), field.key.clone());
    let chosen = field.clone();
    Select::new(field.select_key(), &labels)
        .label("Desktop")
        .appearance(SelectAppearance::Field)
        .current(current.as_deref())
        .disabled(field.disabled)
        .open(is_open(state, field))
        .on_toggle(move |state: &mut State| toggle(state, &toggled))
        .on_close(move |state: &mut State| {
            let picker = &mut state.window.launch_desktop;
            if picker.open.as_ref().is_some_and(|(f, _)| f.key == closed) {
                picker.open = None;
            }
        })
        .on_choose(move |state: &mut State, label: &str, tx: &mut Tx| {
            state.window.launch_desktop.open = None;
            let Some((_, choice)) = options.iter().find(|(l, _)| l == label) else {
                return;
            };
            match choice {
                Choice::Saved => {}
                Choice::Desktop(id) => (chosen.on_change)(
                    state,
                    &chosen.item_id,
                    LaunchDesktopSetting {
                        desktop_id: Some(id.clone()),
                        ..LaunchDesktopSetting::default()
                    },
                    tx,
                ),
                Choice::New(slot) => name_new(state, &chosen, *slot, tx),
            }
        })
}

fn toggle(state: &mut State, field: &Field) {
    if is_open(state, field) {
        state.window.launch_desktop.open = None;
        return;
    }
    state.window.launch_desktop.open = Some((field.clone(), state.window.layer));
    state.window.launch_desktop.reading = Some(field.clone());
}

/// Reads the desktops a select offers as it opens, and lets go of a select or a name whose
/// layer has gone; whether it asked.
pub(crate) fn poll(state: &mut State, tx: &mut Tx) -> bool {
    let layer = state.window.layer;
    let picker = &mut state.window.launch_desktop;
    if picker.open.as_ref().is_some_and(|(_, l)| *l != layer) {
        picker.open = None;
    }
    if picker.naming.as_ref().is_some_and(|n| n.layer != layer) {
        picker.naming = None;
    }
    let Some(field) = state.window.launch_desktop.reading.take() else {
        return false;
    };
    let key = field.select_key();
    state.ask(
        |request_id| LaunchDesktopGetMessage {
            kind: field.kind.clone(),
            item_id: field.item_id.clone(),
            request_id,
            ..LaunchDesktopGetMessage::new()
        },
        move |state, answer, _| {
            let read = state.window.launch_desktop.reads.entry(key).or_default();
            match answer.outcome() {
                Ok(reply) => {
                    read.desktops = reply
                        .parse::<LaunchDesktopResultMessage>()
                        .map(|r| r.desktops.unwrap_or_default());
                    read.error = None;
                }
                Err(why) => read.error = Some(why),
            }
        },
        tx,
    );
    true
}

fn name_new(state: &mut State, field: &Field, slot: i64, tx: &mut Tx) {
    let name = if field.default_name.is_empty() {
        "Agent".to_owned()
    } else {
        field.default_name.clone()
    };
    let mut edit = TextEdit::new(name.clone());
    edit.set_selection(0, name.len());
    state.window.launch_desktop.naming = Some(Naming {
        field: field.clone(),
        layer: state.window.layer,
        slot,
        name: edit,
    });
    tx.focus(NAME_FIELD, true);
}

const NAME_FIELD: &str = "launch-desktop.name";

fn use_name(state: &mut State, tx: &mut Tx) {
    let Some(naming) = state.window.launch_desktop.naming.take() else {
        return;
    };
    let name = naming.name.text.trim().to_owned();
    if name.is_empty() {
        state.window.launch_desktop.naming = Some(naming);
        return;
    }
    let field = naming.field;
    let setting = new_desktop(&field.profile_id, name, naming.slot);
    (field.on_change)(state, &field.item_id, setting, tx);
    tx.focus(field.select_key(), false);
}

fn cancel_name(state: &mut State, tx: &mut Tx) {
    if let Some(naming) = state.window.launch_desktop.naming.take() {
        tx.focus(naming.field.select_key(), false);
    }
}

/// The select as its owner lays it out, with why the desktops could not be read.
pub(crate) fn field(state: &State, t: &Tokens, field: &Field) -> El {
    let mut unused = Vec::new();
    let trigger = select(state, field).build(attn_ui::kit(t), &mut unused);
    let error = state
        .window
        .launch_desktop
        .reads
        .get(&field.select_key())
        .and_then(|r| r.error.clone());
    col().gap(4.0).child(trigger).children(error.map(|why| {
        text(why.clone())
            .key(format!("{}.error", field.select_key()))
            .role("alert")
            .label(why)
            .font(type_::LABEL)
            .color(t.error)
            .wrap()
    }))
}

/// The open select's list and the new desktop's name, over the owner's layer.
pub(crate) fn overlay(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let picker = &state.window.launch_desktop;
    if let Some((field, _)) = picker.open.as_ref().filter(|(f, _)| is_open(state, f)) {
        select(state, field).build(attn_ui::kit(t), layers);
    }
    let Some(naming) = &picker.naming else {
        return;
    };
    let k = attn_ui::kit(t);
    let empty = naming.name.text.trim().is_empty();
    let shortcut = crate::Action::desktop_select(naming.slot)
        .and_then(|action| state.keymap.binding(&action))
        .map(|binding| state.keymap.label(binding));
    let hint = match shortcut {
        Some(keys) => format!("On {keys}."),
        None => "No shortcut.".to_owned(),
    };
    Dialog::new("launch-desktop.naming", "A new desktop")
        .open(true)
        .width(360.0)
        .on_close_tx(cancel_name)
        .child(
            col()
                .padding(16.0)
                .gap(8.0)
                .child(
                    TextInput::new(&naming.name)
                        .key(NAME_FIELD)
                        .label("Name")
                        .width(None)
                        .on_edit(|state: &mut State| {
                            &mut state
                                .window
                                .launch_desktop
                                .naming
                                .as_mut()
                                .expect("the name field shows only while naming")
                                .name
                        })
                        .on_submit(use_name)
                        .on_key(|state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                            if ev.key != Key::Escape {
                                return false;
                            }
                            cancel_name(state, tx);
                            true
                        })
                        .build(k),
                )
                .child(
                    text(hint.clone())
                        .key("launch-desktop.naming.hint")
                        .label(hint)
                        .font(type_::LABEL)
                        .color(t.text_tertiary),
                )
                .child(
                    row()
                        .gap(8.0)
                        .justify_end()
                        .child(
                            Button::new("Cancel")
                                .variant(ButtonVariant::Neutral)
                                .control_size(ControlSize::Small)
                                .on_press_tx(cancel_name)
                                .build(k)
                                .key("launch-desktop.naming.cancel"),
                        )
                        .child(
                            Button::new("Use this name")
                                .variant(ButtonVariant::Submit)
                                .control_size(ControlSize::Small)
                                .disabled(empty)
                                .on_press_tx(use_name)
                                .build(k)
                                .key("launch-desktop.naming.use"),
                        ),
                ),
        )
        .build(k, layers);
}
