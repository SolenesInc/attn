//! The sidebar's dock, as the Tauri app's `SidebarFooter`: chips that remind the person of the
//! keys they chose to keep in view, from the `dock` of the shortcuts setting the two apps share.
//! The PRs chip also opens its drawer.

use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::space;
use rune::{El, Tx, col, row, text};
use serde_json::{Value, json};

use crate::State;
use crate::action::Action;
use crate::shell::Drawer;

const KEYS: rune::TextStyle = rune::TextStyle {
    weight: 600.0,
    ..type_::MONO_CAPTION
};

const HEADING: rune::TextStyle = rune::TextStyle {
    weight: 700.0,
    tracking: 1.8,
    ..type_::CAPS
};

const DEFAULT_ITEMS: &[&str] = &[
    "dock.attention",
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
        Action::DockAttention => "PRs",
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
                Action::DockAttention => state.window.shows(Drawer::Attention),
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
    let head = row()
        .key("sidebar.dock.head")
        .w_full()
        .items_center()
        .child(text("DOCK").font(HEADING).color(t.text_tertiary).grow(1.0))
        .child(
            text(toggle)
                .key("sidebar.dock.toggle")
                .role("button")
                .focusable(true)
                .label(toggle_said)
                .a11y_value(if dock.collapsed {
                    "collapsed"
                } else {
                    "expanded"
                })
                .font(type_::LABEL)
                .color(t.text_tertiary)
                .w(18.0)
                .h(18.0)
                .radius(6.0)
                .hover(t.bg_elevated)
                .on_press_tx(toggle_collapsed),
        );
    let chips = (!dock.collapsed).then(|| {
        row()
            .key("sidebar.dock.chips")
            .role("list")
            .label("Dock")
            .w_full()
            .flex_wrap()
            .gaps(space::S2, space::S3)
            .children(dock.chips.iter().map(|chip| chip_view(chip, t)))
    });
    col()
        .key("sidebar.dock")
        .w_full()
        .gap(space::S2)
        .padding(space::S3)
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border)
        .child(head)
        .children(chips)
}

fn chip_view(chip: &Chip, t: &Tokens) -> El {
    let said = format!("{} {}", chip.keys, chip.label);
    let face = row()
        .key(format!("sidebar.dock.chip.{}", chip.action.id()))
        .label(said)
        .items_center()
        .gap(4.0)
        .radius(999.0)
        .child(text(chip.keys.clone()).font(KEYS).color(t.text_muted))
        .child(
            text(chip.label)
                .font(type_::MONO_CAPTION)
                .color(if chip.active {
                    t.text_primary
                } else {
                    t.text_tertiary
                }),
        );
    let face = match chip.action == Action::DockAttention || chip.active {
        true => face.px(8.0).py(2.0),
        false => face,
    };
    let face = match chip.active {
        true => face
            .a11y_value("active")
            .fill(t.brand.alpha(0.12))
            .border(1.0, t.brand.alpha(0.35)),
        false => face,
    };
    match chip.action {
        Action::DockAttention => face
            .role("button")
            .focusable(true)
            .hover(t.bg_elevated)
            .on_press_tx(|state: &mut State, tx: &mut Tx| crate::attention::toggle(state, tx)),
        _ => face.role("listitem"),
    }
}
