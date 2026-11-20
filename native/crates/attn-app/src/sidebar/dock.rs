//! The sidebar's dock, as the Tauri app's `SidebarFooter`: chips that remind the person of the
//! keys they chose to keep in view, from the `dock` of the shortcuts setting the two apps share.
//! The PRs chip also opens its drawer.

use attn_ui::Tokens;
use attn_ui::blocks::Inline;
use attn_ui::system::{self, GroupHead, IconSize, ListRow, Type, words};
use rune::{El, Tx};
use serde_json::{Value, json};

use crate::State;
use crate::action::Action;

const DEFAULT_ITEMS: &[&str] = &[
    "terminal.splitVertical",
    "terminal.splitHorizontal",
    "session.newHorizontal",
    "terminal.toggleZoom",
    "session.toggleSidebar",
];

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Chip {
    action: Action,
    keys: String,
    label: &'static str,
    active: bool,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Dock {
    collapsed: bool,
    chips: Vec<Chip>,
}

struct Stored {
    collapsed: bool,
    items: Vec<String>,
}

fn stored(state: &State) -> Stored {
    let dock = crate::shortcut_editor::setting(state)
        .and_then(|s| serde_json::from_str::<Value>(s).ok())
        .map(|config| config["dock"].clone())
        .unwrap_or(Value::Null);
    match dock["items"].as_array() {
        Some(items) => Stored {
            collapsed: dock["collapsed"].as_bool().unwrap_or(false),
            items: items
                .iter()
                .filter_map(Value::as_str)
                .map(str::to_owned)
                .collect(),
        },
        None => Stored {
            collapsed: false,
            items: DEFAULT_ITEMS.iter().map(|id| (*id).to_owned()).collect(),
        },
    }
}

fn current_id(id: &str) -> String {
    match id.strip_prefix("workspace.select") {
        Some(slot) => format!("desktop.select{slot}"),
        None => id.to_owned(),
    }
}

/// The terse words a chip shows for `action`, or its label.
fn chip_label(action: &Action) -> &'static str {
    match action {
        Action::SessionNewTerminal => "terminal",
        Action::SessionNewHorizontal => "session h",
        Action::DockAsks => "Asks",
        Action::SessionToggleSidebar => "sidebar",
        Action::TerminalSplitVertical => "split v",
        Action::TerminalSplitHorizontal => "split h",
        Action::TerminalToggleZoom => "zoom",
        Action::MarkdownSendAnnotations => "send notes",
        Action::TerminalSendAnnotations => "send marks",
        other => other.label(),
    }
}

fn needs_an_agent(action: &Action) -> bool {
    matches!(
        action,
        Action::TerminalSplitVertical
            | Action::TerminalSplitHorizontal
            | Action::SessionNewHorizontal
            | Action::TerminalToggleZoom
    )
}

/// The actions the dock lists, in order and once each: the ones pinned, or the default.
pub(crate) fn pinned(state: &State) -> Vec<Action> {
    let mut pinned: Vec<Action> = Vec::new();
    for action in stored(state)
        .items
        .iter()
        .filter_map(|id| Action::from_id(&current_id(id)))
    {
        if !pinned.contains(&action) {
            pinned.push(action);
        }
    }
    pinned
}

/// Pins `action` at the dock's end, or takes it out.
pub(crate) fn pin(state: &mut State, action: &Action, on: bool, tx: &mut Tx) {
    let mut pinned = pinned(state);
    if pinned.contains(action) == on {
        return;
    }
    match on {
        true => pinned.push(action.clone()),
        false => pinned.retain(|a| a != action),
    }
    write_items(state, &pinned, tx);
}

/// Moves `action` one place toward the dock's start, or toward its end when `later`.
pub(crate) fn shift(state: &mut State, action: &Action, later: bool, tx: &mut Tx) {
    let mut pinned = pinned(state);
    let Some(from) = pinned.iter().position(|a| a == action) else {
        return;
    };
    let to = match later {
        true => Some(from + 1).filter(|&to| to < pinned.len()),
        false => from.checked_sub(1),
    };
    let Some(to) = to else {
        return;
    };
    pinned.swap(from, to);
    write_items(state, &pinned, tx);
}

fn write_items(state: &mut State, pinned: &[Action], tx: &mut Tx) {
    let items: Vec<&str> = pinned.iter().map(Action::id).collect();
    let dock = json!({"collapsed": stored(state).collapsed, "items": items});
    crate::shortcut_editor::write_dock(state, dock, tx);
}

/// The dock a fresh setting has, as Restore Defaults writes it.
pub(crate) fn default_dock() -> Value {
    json!({"collapsed": false, "items": DEFAULT_ITEMS})
}

pub(crate) fn of(state: &State) -> Dock {
    let stored = stored(state);
    let agent_shown = state.window.on_screen(&state.facts).is_some();
    let zoomed = agent_shown
        && state.facts.current_desktop().is_some_and(|desktop| {
            state.window.desk.row(&desktop.id).view == crate::desktop::layout::View::Zoomed
        });
    let chips = pinned(state)
        .into_iter()
        .filter(|action| agent_shown || !needs_an_agent(action))
        .filter_map(|action| {
            let keys = crate::palette::label_of(state, &action)?;
            let active = match action {
                Action::TerminalToggleZoom => zoomed,
                _ => false,
            };
            Some(Chip {
                label: chip_label(&action),
                action,
                keys,
                active,
            })
        })
        .collect();
    Dock {
        collapsed: stored.collapsed,
        chips,
    }
}

fn toggle_collapsed(state: &mut State, tx: &mut Tx) {
    let stored = stored(state);
    let dock = json!({"collapsed": !stored.collapsed, "items": stored.items});
    crate::shortcut_editor::write_dock(state, dock, tx);
}

pub(crate) fn view(dock: &Dock, t: &Tokens) -> El {
    let (toggle, toggle_said) = match dock.collapsed {
        true => ("+", "Show dock"),
        false => ("−", "Hide dock"),
    };
    let toggle = El::from(
        system::icon_button("sidebar.dock.toggle", toggle_said, toggle, IconSize::Small)
            .size(system::Size::Small)
            .on_press_tx(toggle_collapsed)
            .build(t),
    )
    .a11y_value(if dock.collapsed {
        "collapsed"
    } else {
        "expanded"
    });
    let head = GroupHead::new("sidebar.dock.head", "Dock")
        .heading()
        .section("Dock")
        .trailing(toggle)
        .build(t);
    let chips = (!dock.collapsed).then(|| {
        ListRow::column().child(
            Inline::loose()
                .key("sidebar.dock.chips")
                .role("list")
                .label("Dock")
                .w_full()
                .children(dock.chips.iter().map(|chip| chip_view(chip, t))),
        )
    });
    ListRow::column()
        .key("sidebar.dock")
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border)
        .child(head)
        .children(chips)
}

fn chip_view(chip: &Chip, t: &Tokens) -> El {
    let said = format!("{} {}", chip.keys, chip.label);
    let (keys, label) = if chip.active {
        (t.brand, t.text_primary)
    } else {
        (t.text_dimmed, t.text_tertiary)
    };
    let face = Inline::joined()
        .key(format!("sidebar.dock.chip.{}", chip.action.id()))
        .role("listitem")
        .label(said)
        .child(words(chip.keys.clone(), Type::Mono, keys))
        .child(words(chip.label, Type::Small, label));
    if chip.active {
        face.a11y_value("active")
    } else {
        face
    }
}
