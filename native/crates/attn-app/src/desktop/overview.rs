//! The desktop overview: every desktop of the profile as a card with its layout in miniature,
//! to switch to, to send the active tile to, or to start a new one.

use attn_protocol::{
    Desktop, DesktopCreateMessage, DesktopMoveLeafMessage, ProfileActionResultMessage,
};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, Tx, col, el, row, text};
use rune_kit::{Button, ButtonVariant};

use super::layout::{Direction, LeafKind, Node};
use crate::State;
use crate::requests::Refused;
use crate::shell::Layer;

const GRID_COLUMNS: usize = 3;
const MINI_ASPECT: f32 = 9.0 / 16.0;
pub(crate) const DIALOG: &str = "overview.dialog";

/// Opens the overview on the current desktop, or closes it.
pub(crate) fn toggle(state: &mut State, tx: &mut Tx) {
    if state.window.layer == Some(Layer::Overview) {
        close(state, tx);
        return;
    }
    state.window.desk.overview_focus = state.facts.current_desktop().map(|d| d.id.clone());
    state.window.layer = Some(Layer::Overview);
}

fn close(state: &mut State, tx: &mut Tx) {
    state.window.layer = None;
    state.window.desk.overview_focus = None;
    crate::picker::refocus_inline(state, tx);
}

fn ordered(state: &State) -> Vec<&Desktop> {
    let mut desktops: Vec<&Desktop> = state.facts.desktops.iter().collect();
    desktops.sort_by(|a, b| a.order_key.cmp(&b.order_key));
    desktops
}

fn switch(state: &mut State, desktop: &str, tx: &mut Tx) {
    close(state, tx);
    crate::sidebar::open_desktop(state, desktop, tx);
}

/// Whether the active tile of the current desktop can go to `desktop`.
fn can_send_to(state: &State, desktop: &str) -> bool {
    state
        .facts
        .current_desktop()
        .is_some_and(|c| !c.active_pane_id.is_empty() && c.id != desktop)
}

fn send(state: &mut State, desktop: &str, tx: &mut Tx) {
    let Some(source) = state.facts.current_desktop() else {
        return;
    };
    let message = DesktopMoveLeafMessage {
        source_desktop_id: source.id.clone(),
        target_desktop_id: Some(desktop.to_owned()),
        leaf_id: source.active_pane_id.clone(),
        ..DesktopMoveLeafMessage::new()
    };
    close(state, tx);
    state.act(
        move |id| DesktopMoveLeafMessage {
            request_id: id,
            ..message
        },
        Refused::Prefixed("Could not move that pane"),
        |_, _, _| {},
        tx,
    );
}

/// Makes a desktop and switches to it once the daemon made it.
fn create(state: &mut State, tx: &mut Tx) {
    let profile = state.facts.profiles.selected.clone();
    if profile.is_empty() {
        return;
    }
    close(state, tx);
    state.act(
        |id| DesktopCreateMessage {
            request_id: id,
            profile_id: profile,
            ..DesktopCreateMessage::new()
        },
        Refused::Prefixed("Could not create a desktop"),
        |state: &mut State, reply, tx: &mut Tx| {
            let made = reply
                .parse::<ProfileActionResultMessage>()
                .and_then(|r| r.desktops?.into_iter().next());
            if let Some(desktop) = made {
                crate::sidebar::open_desktop(state, &desktop.id, tx);
            }
        },
        tx,
    );
}

/// Arrows move the focused card, Return switches to it and Shift-Return sends the active tile
/// there, and a digit switches to the desktop on that slot.
fn key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let desktops: Vec<String> = ordered(state).iter().map(|d| d.id.clone()).collect();
    let at = state
        .window
        .desk
        .overview_focus
        .as_ref()
        .and_then(|f| desktops.iter().position(|d| d == f))
        .unwrap_or(0);
    let step = match ev.key {
        Key::Right => Some(1),
        Key::Left => Some(-1),
        Key::Down => Some(GRID_COLUMNS as i64),
        Key::Up => Some(-(GRID_COLUMNS as i64)),
        _ => None,
    };
    if let Some(step) = step {
        if let Some(last) = desktops.len().checked_sub(1) {
            let next = (at as i64 + step).clamp(0, last as i64) as usize;
            state.window.desk.overview_focus = Some(desktops[next].clone());
        }
        return true;
    }
    if ev.key == Key::Enter {
        let Some(focused) = desktops.get(at).cloned() else {
            return true;
        };
        if !ev.mods.shift {
            switch(state, &focused, tx);
        } else if can_send_to(state, &focused) {
            send(state, &focused, tx);
        }
        return true;
    }
    let plain = !(ev.mods.cmd || ev.mods.ctrl || ev.mods.alt);
    let slot = match &ev.key {
        Key::Text(t) if plain => t.parse::<i64>().ok().filter(|n| (1..=9).contains(n)),
        _ => None,
    };
    let target = slot.and_then(|slot| {
        state
            .facts
            .desktops
            .iter()
            .find(|d| d.shortcut_slot == Some(slot))
            .map(|d| d.id.clone())
    });
    match target {
        Some(desktop) => {
            switch(state, &desktop, tx);
            true
        }
        None => false,
    }
}

/// `desktop`'s layout in miniature: each leaf at its share of the layout, under its name,
/// the active one marked.
fn mini(desktop: &Desktop, t: &Tokens) -> Option<El> {
    let tree = Node::parse(&desktop.tree_json)?;
    Some(mini_node(&tree, desktop, t))
}

fn mini_node(node: &Node, desktop: &Desktop, t: &Tokens) -> El {
    match node {
        Node::Split(split) => {
            let [a, b] = &*split.children;
            let parent = match split.direction {
                Direction::Vertical => row(),
                Direction::Horizontal => col(),
            };
            parent
                .w_full()
                .h_full()
                .min_w(0.0)
                .min_h(0.0)
                .child(super::view::share(mini_node(a, desktop, t), split.ratio))
                .child(super::view::share(
                    mini_node(b, desktop, t),
                    1.0 - split.ratio,
                ))
        }
        Node::Leaf(leaf) => {
            let label = match &leaf.kind {
                LeafKind::Tile { kind, .. } => kind.clone(),
                LeafKind::Pane => super::view::pane_of(desktop, &leaf.id)
                    .map(|p| p.title.clone())
                    .filter(|t| !t.is_empty())
                    .unwrap_or_else(|| "Agent".to_owned()),
            };
            let on = leaf.id == desktop.active_pane_id;
            el().w_full()
                .h_full()
                .min_w(0.0)
                .min_h(0.0)
                .fill(t.bg_button)
                .border(1.0, if on { t.accent } else { t.border })
                .px(4.0)
                .py(2.0)
                .clip_rounded(0.0)
                .child(
                    text(label)
                        .font(rune::TextStyle {
                            size: 10.0,
                            ..type_::LABEL
                        })
                        .color(if on { t.text_primary } else { t.text_secondary })
                        .single_line()
                        .truncate(),
                )
        }
    }
}

pub(crate) fn view(state: &State, t: &Tokens) -> Option<El> {
    if state.window.layer != Some(Layer::Overview) {
        return None;
    }
    let facts = &state.facts;
    let desktops = ordered(state);
    let current = facts.current_desktop().map(|d| d.id.as_str());
    let focused = state.window.desk.overview_focus.as_deref();
    let profile = facts
        .profiles
        .all
        .iter()
        .find(|p| p.id == facts.profiles.selected)
        .map(|p| p.name.clone())
        .unwrap_or_default();
    let count = desktops.len();
    let mut cards: Vec<El> = desktops
        .iter()
        .map(|d| {
            let slot = d
                .shortcut_slot
                .and_then(crate::Action::desktop_select)
                .and_then(|a| state.keymap.binding(&a).map(|b| state.keymap.label(b)));
            card(
                d,
                &crate::sidebar::model::desktop_title(d, &facts.desktops),
                slot,
                (
                    Some(d.id.as_str()) == current,
                    Some(d.id.as_str()) == focused,
                ),
                can_send_to(state, &d.id),
                t,
            )
        })
        .collect();
    cards.push(new_card(t));
    let mut grid = col().w_full().gap(10.0);
    let mut cards = cards.into_iter().peekable();
    while cards.peek().is_some() {
        let mut line = row().w_full().gap(10.0).items_start();
        for _ in 0..GRID_COLUMNS {
            line = match cards.next() {
                Some(card) => line.child(card),
                None => line.child(el().grow(1.0).min_w(0.0)),
            };
        }
        grid = grid.child(line);
    }
    let dialog = col()
        .key(DIALOG)
        .role("dialog")
        .label("Desktop overview")
        .modal_tx(true, close)
        .focusable(true)
        .autofocus()
        .on_key(key)
        .w_full()
        .max_w(880.0)
        .max_h(800.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .padding(16.0)
        .radius(12.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_hover)
        .child(
            text(format!(
                "{profile} · {count} {}",
                if count == 1 { "desktop" } else { "desktops" }
            ))
            .font(type_::TITLE)
            .color(t.text_primary),
        )
        .child(
            text("Arrows move · ↵ switch · ⇧↵ send the focused pane · digits switch · Esc closes")
                .font(type_::LABEL)
                .color(t.text_tertiary)
                .pb(12.0),
        )
        .child(grid);
    Some(
        el().key("overview")
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .child(
                el().key("overview.scrim")
                    .absolute(0.0, 0.0)
                    .w_full()
                    .h_full()
                    .fill(t.bg_overlay)
                    .a11y_hidden(),
            )
            .child(
                col()
                    .w_full()
                    .h_full()
                    .padding(space::S6)
                    .center()
                    .child(dialog),
            ),
    )
}

fn card(
    desktop: &Desktop,
    title: &str,
    slot: Option<String>,
    (current, focused): (bool, bool),
    can_send: bool,
    t: &Tokens,
) -> El {
    let layout = mini(desktop, t);
    let empty = layout.is_none();
    let id = desktop.id.clone();
    let head = row()
        .w_full()
        .items_center()
        .gap(space::S2)
        .child(
            text(slot.clone().unwrap_or_else(|| "no shortcut".to_owned()))
                .font(rune::TextStyle {
                    family: rune::Family::Mono,
                    ..type_::LABEL
                })
                .color(if slot.is_some() {
                    t.text_secondary
                } else {
                    t.text_muted
                }),
        )
        .child(
            text(title.to_owned())
                .font(type_::BODY)
                .color(t.text_primary)
                .single_line()
                .truncate()
                .grow(1.0),
        )
        .children(empty.then(|| text("empty").font(type_::LABEL).color(t.text_muted)));
    let mini = el()
        .w_full()
        .aspect_ratio(1.0 / MINI_ASPECT)
        .radius(4.0)
        .fill(t.bg_app)
        .children(layout);
    let open = col()
        .key(format!("overview.card.{}", desktop.id))
        .role("button")
        .label(title.to_owned())
        .a11y_value(match (current, focused) {
            (true, true) => "current, focused",
            (true, false) => "current",
            (false, true) => "focused",
            _ => "",
        })
        .w_full()
        .gap(6.0)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| switch(state, &id, tx))
        .child(head)
        .child(mini);
    let target = desktop.id.clone();
    let send_button = can_send.then(|| {
        Button::new("Send focused pane here ⇧↵")
            .variant(ButtonVariant::GhostMuted)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| send(state, &target, tx))
            .label_with(|_, ink| {
                text("Send focused pane here ⇧↵")
                    .font(type_::LABEL)
                    .color(ink)
            })
            .build(attn_ui::kit(t))
            .key(format!("overview.send.{}", desktop.id))
            .h(type_::LABEL.line_px() + 4.0)
            .px(space::S2)
            .radius(5.0)
            .fill(t.bg_button)
            .border(1.0, t.border)
    });
    let card = col()
        .grow(1.0)
        .min_w(0.0)
        .gap(6.0)
        .padding(space::S2)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, if current { t.accent } else { t.border })
        .child(open)
        .child(row().gap(6.0).children(send_button));
    if focused {
        card.focus_ring(2.0, 0.0, t.border_focus)
    } else {
        card
    }
}

fn new_card(t: &Tokens) -> El {
    Button::new("New desktop")
        .variant(ButtonVariant::GhostFaint)
        .on_press_tx(create)
        .label_with(|_, ink| text("+ New desktop").font(type_::BODY).color(ink))
        .build(attn_ui::kit(t))
        .key("overview.new")
        .grow(1.0)
        .min_w(0.0)
        .h(110.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
}
