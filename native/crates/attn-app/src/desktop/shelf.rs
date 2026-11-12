//! The shelf: the columns of the current desktop's row that the screen does not show, as
//! chips in the title bar either side of a dot per column on screen.

use attn_protocol::{
    Desktop, DesktopMoveLeafMessage, DesktopSetWantedColsMessage, SetSettingMessage,
};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{Drag, El, Phase, Tx, col, row, text};

use super::drag::{Owner, Source};

use super::layout::{
    self, Column, Edge, LeafKind, NarrowestStep, Node, RowInput, ShelfDrop, ShelfSend, View,
};
use super::{Desk, actions};
use crate::State;
use crate::action::Action;
use crate::sidebar::AgentState;

const CHIP_PX: f32 = 112.0;
const LIVE_DOT_PX: f32 = 28.0;
const CAPS_PX: f32 = 104.0;
/// How long the width label shows after fewer or more tiles.
const WIDTH_LABEL_SECONDS: f64 = 1.5;
/// How long a chip flashes when its tile arrives on the shelf.
const FLASH_SECONDS: f64 = 0.9;

/// A chip flashing for a moment because its tile arrived on the shelf or went off screen.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Flash {
    pub desktop: String,
    pub leaf: String,
    pub until: f64,
}

/// Where a desktop's screen started before a document arrived, and the column it started at
/// once the document showed: while the screen has not moved since, the document leaving the
/// row puts the screen back.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Return {
    first_column: layout::FirstColumn,
    shown_at: String,
}

/// What a chip shows of each tile in its column: an agent's state, or a plain mark.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
enum Member {
    Agent(AgentState),
    Tile,
}

/// The width label fewer and more tiles show for a moment.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct WidthLabel {
    pub cleared: usize,
    pub until: f64,
}

/// One column of the row as its chip or dot shows it.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
struct Chip {
    /// The column's first leaf, which names the chip.
    key: String,
    /// The leaf the chip brings: the one this window used last.
    go: String,
    label: String,
    /// A tile in the column waits on the person, or arrived unseen.
    needs: bool,
    /// A tile in the column just arrived on the shelf.
    arrival: bool,
    members: Vec<Member>,
    leaves: Vec<String>,
}

fn member(desktop: &Desktop, facts: &attn_client::Facts, leaf: &str) -> Member {
    super::view::pane_of(desktop, leaf)
        .and_then(|pane| facts.sessions.iter().find(|s| s.id == pane.session_id))
        .map_or(Member::Tile, |s| Member::Agent(AgentState::parse(&s.state)))
}

/// A tile's name on the shelf: an agent by its session's name, a tile by what it holds.
pub(crate) fn title(
    desktop: &Desktop,
    facts: &attn_client::Facts,
    tree: &Node,
    leaf: &str,
) -> String {
    match tree.find_leaf(leaf) {
        Some(Node::Leaf(l)) => match &l.kind {
            LeafKind::Tile { kind, params, .. } => {
                crate::sidebar::tile_title(kind, params.as_deref().unwrap_or(""))
            }
            LeafKind::Pane => {
                let pane = super::view::pane_of(desktop, leaf);
                let session =
                    pane.and_then(|p| facts.sessions.iter().find(|s| s.id == p.session_id));
                session
                    .map(crate::sidebar::display_name)
                    .or_else(|| pane.map(|p| p.title.clone()).filter(|t| !t.is_empty()))
                    .unwrap_or_else(|| "Terminal".into())
            }
        },
        _ => "Terminal".into(),
    }
}

fn label(titles: &[String]) -> String {
    match titles {
        [one] => one.clone(),
        [a, b] => format!("{a} + {b}"),
        [first, rest @ ..] => format!("{first} + {}", rest.len()),
        [] => String::new(),
    }
}

fn chip(
    desktop: &Desktop,
    facts: &attn_client::Facts,
    tree: &Node,
    row: &super::RowState,
    flashing: &[&str],
    column: &Column,
) -> Chip {
    let titles: Vec<String> = column
        .leaf_ids
        .iter()
        .map(|leaf| title(desktop, facts, tree, leaf))
        .collect();
    let members: Vec<Member> = column
        .leaf_ids
        .iter()
        .map(|leaf| member(desktop, facts, leaf))
        .collect();
    let waits = members.iter().any(|m| {
        matches!(
            m,
            Member::Agent(AgentState::WaitingInput | AgentState::PendingApproval)
        )
    });
    Chip {
        key: column.first().to_owned(),
        go: column.last_used(&row.focus_order).to_owned(),
        label: label(&titles),
        needs: waits || column.leaf_ids.iter().any(|l| row.unseen.contains(l)),
        arrival: column
            .leaf_ids
            .iter()
            .any(|l| flashing.contains(&l.as_str())),
        members,
        leaves: column.leaf_ids.clone(),
    }
}

/// How many chips show either side when the shelf is `available` points wide; the rest go
/// into the caps, the chips nearest the screen kept.
fn allot(available: f32, before: usize, after: usize, dots: usize) -> (usize, usize) {
    let without_caps = ((available - dots as f32 * LIVE_DOT_PX) / CHIP_PX).floor();
    if (before + after) as f32 <= without_caps {
        return (before, after);
    }
    let room = ((available - dots as f32 * LIVE_DOT_PX - CAPS_PX) / CHIP_PX)
        .floor()
        .max(0.0) as usize;
    let (mut left, mut right) = (0, 0);
    while left + right < room && (left < before || right < after) {
        if right < after && (right <= left || left >= before) {
            right += 1;
        } else {
            left += 1;
        }
    }
    (left, right)
}

/// The current desktop's shelf as this window lays it out: chips either side of the dots,
/// and the chips the title bar has no room for in the caps.
pub(crate) struct Shelf {
    pub desktop: String,
    active: String,
    before: Vec<Chip>,
    screen: Vec<Chip>,
    after: Vec<Chip>,
    /// How many of `before`, from its start, wait in the before cap.
    capped_before: usize,
    /// How many of `after`, from its start, show as chips.
    shown_after: usize,
}

/// A place on the shelf a tile can be dropped beside: a chip by either half, or a cap by the
/// side it holds.
pub(crate) struct Anchor {
    pub element: String,
    pub column: String,
    pub edge: Option<Edge>,
}

impl Shelf {
    /// The shelf of the current desktop, or None when nothing is off screen, the window shows
    /// Home, or the active tile is in focus mode.
    pub(crate) fn of(state: &State) -> Option<Shelf> {
        if state.window.view != crate::shell::View::Desktop {
            return None;
        }
        let facts = &state.facts;
        let desk = &state.window.desk;
        let desktop = facts.current_desktop()?;
        let row_state = desk.row(&desktop.id);
        if matches!(row_state.view, View::Focused(_)) {
            return None;
        }
        let (tree, plan) = desk.plan(facts, desktop)?;
        if plan.before.is_empty() && plan.after.is_empty() {
            return None;
        }
        let flashing: Vec<&str> = desk
            .flashes
            .iter()
            .filter(|f| f.desktop == desktop.id)
            .map(|f| f.leaf.as_str())
            .collect();
        let chips = |columns: &[Column]| -> Vec<Chip> {
            columns
                .iter()
                .map(|c| chip(desktop, facts, &tree, &row_state, &flashing, c))
                .collect()
        };
        let (before, screen, after) =
            (chips(&plan.before), chips(&plan.screen), chips(&plan.after));
        let available = desk.shelf_px.unwrap_or(f32::INFINITY);
        let (left, right) = allot(available, before.len(), after.len(), screen.len());
        Some(Shelf {
            desktop: desktop.id.clone(),
            active: desktop.active_pane_id.clone(),
            capped_before: before.len() - left,
            shown_after: right,
            before,
            screen,
            after,
        })
    }

    fn before_cap(&self) -> &[Chip] {
        &self.before[..self.capped_before]
    }

    fn before_chips(&self) -> &[Chip] {
        &self.before[self.capped_before..]
    }

    fn after_chips(&self) -> &[Chip] {
        &self.after[..self.shown_after]
    }

    fn after_cap(&self) -> &[Chip] {
        &self.after[self.shown_after..]
    }

    /// Whether `leaf` is off screen, on a chip or in a cap.
    pub(crate) fn holds(&self, leaf: &str) -> bool {
        self.before
            .iter()
            .chain(&self.after)
            .any(|c| c.leaves.iter().any(|l| l == leaf))
    }

    /// What dragging the chip of `column` picks up.
    pub(crate) fn chip_source(&self, column: &str) -> Option<Source> {
        let chip = self
            .before
            .iter()
            .chain(&self.after)
            .find(|c| c.key == column)?;
        Some(Source::Chip {
            desktop: self.desktop.clone(),
            column: chip.key.clone(),
            leaf: chip.go.clone(),
            label: chip.label.clone(),
        })
    }

    /// Where a dragged tile can land on the shelf, in the order the shelf shows them.
    pub(crate) fn anchors(&self) -> Vec<Anchor> {
        let chip = |c: &Chip| Anchor {
            element: format!("shelf.chip.{}", c.key),
            column: c.key.clone(),
            edge: None,
        };
        let cap = |name: &str, c: Option<&Chip>, edge| {
            c.map(|c| Anchor {
                element: format!("shelf.cap.{name}"),
                column: c.key.clone(),
                edge: Some(edge),
            })
        };
        cap("before", self.before_cap().first(), Edge::Left)
            .into_iter()
            .chain(self.before_chips().iter().map(chip))
            .chain(self.after_chips().iter().map(chip))
            .chain(cap("after", self.after_cap().last(), Edge::Right))
            .collect()
    }
}

pub(crate) fn view(state: &State, t: &Tokens) -> Option<El> {
    let Some(shelf) = Shelf::of(state) else {
        return holding(state, Owner::Shelf).then(owner);
    };
    let desk = &state.window.desk;
    let id = &shelf.desktop;
    let dots = row()
        .key("shelf.live")
        .items_center()
        .gap(space::S1)
        .on_hover_change(|state: &mut State, inside, _: &mut Tx| {
            state.window.desk.width_control = inside
        })
        .children(shelf.screen.iter().map(|chip| {
            let current = chip.leaves.contains(&shelf.active);
            dot(chip, current, id, t)
        }))
        .children(
            desk.width_control
                .then(|| width_control(shelf.screen.len(), Desk::narrowest_cols(&state.facts), t)),
        );
    let drop = desk.drag.as_ref().and_then(|d| d.shelf_mark());
    let peeked = super::peek::peeked(state, id);
    let menu = |side: Edge, columns: &[Chip]| {
        let marked = drop.as_ref().is_some_and(|(column, edge)| {
            *edge == side && columns.iter().any(|c| c.key == *column)
        });
        cap(side, columns, desk.cap_open == Some(side), marked, t)
    };
    let mark = |c: &Chip| {
        drop.as_ref()
            .filter(|(column, _)| *column == c.key)
            .map(|(_, e)| *e)
    };
    Some(
        owner()
            .role("toolbar")
            .label("Shelf")
            .items_center()
            .gap(space::S1)
            .on_layout_tx(|state: &mut State, rect, tx: &mut Tx| record(state, "shelf", rect, tx))
            .children(menu(Edge::Left, shelf.before_cap()))
            .children(
                shelf
                    .before_chips()
                    .iter()
                    .map(|c| chip_button(c, id, peeked.as_deref(), mark(c), t)),
            )
            .child(dots)
            .children(
                shelf
                    .after_chips()
                    .iter()
                    .map(|c| chip_button(c, id, peeked.as_deref(), mark(c), t)),
            )
            .children(menu(Edge::Right, shelf.after_cap())),
    )
}

/// The shelf's row, which runs its chips' drags.
fn owner() -> El {
    row()
        .key("shelf")
        .on_drag(|state: &mut State, d: &mut Drag, tx: &mut Tx| {
            super::drag::gesture(state, d, tx, Owner::Shelf)
        })
}

/// Whether a drag `owner` runs is in flight, so `owner` stays on screen with nothing in it.
fn holding(state: &State, owner: Owner) -> bool {
    state
        .window
        .desk
        .drag
        .as_ref()
        .is_some_and(|d| d.owner == owner)
}

/// Keeps where a shelf element is in the window, for drops.
fn record(state: &mut State, key: &str, rect: [f32; 4], tx: &mut Tx) {
    let rects = &mut state.window.desk.rects;
    if rects.get(key) == Some(&rect) {
        tx.live_only();
        return;
    }
    rects.insert(key.to_owned(), rect);
}

/// The title bar's middle, which the shelf sits in and measures for its chips.
pub(crate) fn slot(state: &State, t: &Tokens) -> El {
    row()
        .key("shelf.slot")
        .grow(1.0)
        .min_w(0.0)
        .h_full()
        .center()
        .on_layout_tx(|state: &mut State, [_, _, w, _], tx: &mut Tx| {
            if state.window.desk.shelf_px == Some(w) {
                tx.live_only();
                return;
            }
            state.window.desk.shelf_px = Some(w);
        })
        .children(view(state, t))
}

/// A column's chip. The pointer resting on it peeks at the tile it brings, or at the tile whose
/// mark it rests on; `peeked` is the tile the open peek shows.
fn chip_button(
    chip: &Chip,
    desktop: &str,
    peeked: Option<&str>,
    drop: Option<Edge>,
    t: &Tokens,
) -> El {
    let key = format!("shelf.chip.{}", chip.key);
    let recorded = key.clone();
    let stacked = chip.leaves.len() > 1;
    let marks = chip
        .members
        .iter()
        .zip(&chip.leaves)
        .enumerate()
        .map(|(i, (m, leaf))| {
            let peeked = stacked && peeked == Some(leaf.as_str());
            let (desktop, leaf, column) = (desktop.to_owned(), leaf.clone(), chip.key.clone());
            row()
                .key(format!("{key}.member.{i}"))
                .pt(2.0)
                .border_sides(
                    [0.0, 0.0, 0.0, 2.0],
                    if peeked {
                        t.text_secondary
                    } else {
                        rune::Color::TRANSPARENT
                    },
                )
                .on_hover_change(move |state: &mut State, inside, tx: &mut Tx| {
                    if inside {
                        super::peek::hover_member(state, &desktop, &leaf, &column, tx);
                    }
                })
                .child(member_mark(*m, chip.needs, t))
        });
    let moved = (desktop.to_owned(), chip.go.clone(), chip.key.clone());
    let left = chip.key.clone();
    let state = [(chip.needs, "needs you"), (chip.arrival, "arrived")]
        .iter()
        .filter(|(on, _)| *on)
        .map(|(_, words)| *words)
        .collect::<Vec<_>>()
        .join(", ");
    let chip = row()
        .key(key.clone())
        .role("button")
        .label(chip.label.clone())
        .a11y_value(state)
        .w(CHIP_PX - space::S1)
        .min_w(0.0)
        .items_center()
        .gap(space::S1)
        .px(space::S2)
        .py(space::S1)
        .radius(5.0)
        .fill(t.bg_button)
        .border(1.0, if chip.arrival { t.accent } else { t.border })
        .on_layout_tx(move |state: &mut State, rect, tx: &mut Tx| {
            record(state, &recorded, rect, tx)
        })
        .on_hover_change(move |state: &mut State, inside, tx: &mut Tx| {
            if !inside {
                super::peek::leave_chip(state, &left, tx);
            }
        })
        .on_pointer_move_local(move |state: &mut State, _, tx: &mut Tx| {
            let (desktop, leaf, column) = &moved;
            super::peek::hover_chip(state, desktop, leaf, column, tx)
        })
        .children(marks)
        .child(
            text(chip.label.clone())
                .font(type_::LABEL)
                .color(if chip.arrival {
                    t.text_primary
                } else {
                    t.text_secondary
                })
                .single_line()
                .truncate()
                .grow(1.0),
        );
    drop_bar(chip, drop, t)
}

/// A tile's mark on its chip: an agent's state dot, or a square, in the warning colour while
/// the chip needs the person.
fn member_mark(member: Member, needs: bool, t: &Tokens) -> El {
    let mark = rune::el();
    match member {
        Member::Agent(state) => mark
            .w(7.0)
            .h(7.0)
            .radius(3.5)
            .fill(crate::sidebar::state_color(state, t)),
        Member::Tile => {
            mark.w(7.0)
                .h(7.0)
                .radius(1.0)
                .fill(if needs { t.warning } else { t.text_secondary })
        }
    }
}

fn dot(chip: &Chip, current: bool, desktop: &str, t: &Tokens) -> El {
    let (desktop, leaf) = (desktop.to_owned(), chip.go.clone());
    row()
        .key(format!("shelf.dot.{}", chip.key))
        .role("button")
        .label(chip.label.clone())
        .a11y_value(if current { "current" } else { "" })
        .w(LIVE_DOT_PX - space::S1)
        .h(LIVE_DOT_PX - space::S1)
        .center()
        .on_press(move |state: &mut State| actions::show_leaf(state, &desktop, &leaf))
        .child(rune::el().w(8.0).h(8.0).radius(4.0).fill(if current {
            t.accent
        } else {
            t.text_tertiary
        }))
}

/// The accent bar on the side of a chip or cap a dragged tile would land on.
fn drop_bar(el: El, drop: Option<Edge>, t: &Tokens) -> El {
    match drop {
        None => el,
        Some(Edge::Left) => el.border_sides([3.0, 0.0, 0.0, 0.0], t.accent),
        Some(Edge::Right) => el.border_sides([0.0, 3.0, 0.0, 0.0], t.accent),
    }
}

fn cap(side: Edge, chips: &[Chip], open: bool, drop: bool, t: &Tokens) -> Option<El> {
    if chips.is_empty() {
        return None;
    }
    let n = chips.len();
    let (name, caption) = match side {
        Edge::Left => ("before", format!("‹ {n}")),
        Edge::Right => ("after", format!("{n} ›")),
    };
    let key = format!("shelf.cap.{name}");
    let recorded = key.clone();
    let cap = row()
        .key(key)
        .on_layout_tx(move |state: &mut State, rect, tx: &mut Tx| {
            record(state, &recorded, rect, tx)
        })
        .role("button")
        .label(format!("{n} more this way"))
        .px(space::S2)
        .py(space::S1)
        .radius(4.0)
        .border(1.0, t.border)
        .on_press(move |state: &mut State| {
            let desk = &mut state.window.desk;
            desk.cap_open = (desk.cap_open != Some(side)).then_some(side);
        })
        .a11y_value(if open { "open" } else { "" })
        .child(
            text(caption)
                .font(type_::LABEL)
                .color(if chips.iter().any(|c| c.needs) {
                    t.warning
                } else {
                    t.text_secondary
                }),
        );
    Some(drop_bar(cap, drop.then_some(side), t))
}

/// The open cap's menu of the chips it holds, under the title bar on its side.
pub(crate) fn cap_menu(state: &State, t: &Tokens) -> Option<El> {
    let layer = rune::el()
        .key("shelf.menu.layer")
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .on_drag(menu_gesture);
    let open = state.window.desk.cap_open.zip(Shelf::of(state));
    let Some((side, shelf)) = open else {
        return holding(state, Owner::Menu).then_some(layer);
    };
    let (name, held) = match side {
        Edge::Left => ("before", shelf.before_cap()),
        Edge::Right => ("after", shelf.after_cap()),
    };
    if held.is_empty() {
        return holding(state, Owner::Menu).then_some(layer);
    }
    let peeked = super::peek::peeked(state, &shelf.desktop);
    let menu = col()
        .key(format!("shelf.menu.{name}"))
        .role("menu")
        .label(format!("{} more this way", held.len()))
        .padding(space::S1)
        .gap(space::S1)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .children(
            held.iter()
                .map(|c| chip_button(c, &shelf.desktop, peeked.as_deref(), None, t)),
        );
    let place = row().w_full().padding(space::S2);
    let place = match side {
        Edge::Left => place,
        Edge::Right => place.justify_end(),
    };
    Some(
        layer.child(
            place
                .absolute(0.0, crate::shell::TITLE_BAR_HEIGHT)
                .child(menu),
        ),
    )
}

/// A press on an open cap's menu drags the chip under it; one beside the menu closes it.
fn menu_gesture(state: &mut State, d: &mut Drag, tx: &mut Tx) {
    let ours = holding(state, Owner::Menu);
    if d.phase == Phase::Up && !ours {
        state.window.desk.cap_open = None;
        return;
    }
    super::drag::gesture(state, d, tx, Owner::Menu);
}

fn width_control(shown: usize, cols: u32, t: &Tokens) -> El {
    let button = |key: &str, label: &str, glyph: &str, action: Action| {
        row()
            .key(key.to_owned())
            .role("button")
            .label(label.to_owned())
            .px(space::S1)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                crate::dispatch::dispatch(state, action.clone(), tx);
            })
            .child(
                text(glyph.to_owned())
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            )
    };
    row()
        .key("shelf.width")
        .items_center()
        .gap(space::S1)
        .child(
            text(format!("{shown} · ≥{cols} cols"))
                .font(type_::LABEL)
                .color(t.text_tertiary)
                .single_line(),
        )
        .child(button(
            "shelf.width.fewer",
            "Fewer, wider tiles",
            "−",
            Action::ShelfFewerTiles,
        ))
        .child(button(
            "shelf.width.more",
            "More, narrower tiles",
            "+",
            Action::ShelfMoreTiles,
        ))
}

/// The label fewer and more tiles show: how many tiles fit, how wide each is at least, and
/// the counts the steps reach.
pub(crate) fn width_label(state: &State, t: &Tokens) -> Option<El> {
    let desk = &state.window.desk;
    let label = desk.width_label.as_ref()?;
    let facts = &state.facts;
    let desktop = facts.current_desktop()?;
    let (tree, plan) = desk.plan(facts, desktop)?;
    let row_state = desk.row(&desktop.id);
    let cols = Desk::narrowest_cols(facts);
    let input = desk.row_input(facts, &tree, desktop, &row_state)?;
    let shown = plan.screen.len();
    let counts = layout::reachable_tile_counts(&input, cols);
    let cleared = match label.cleared {
        0 => None,
        1 => Some("Cleared the dragged width".to_owned()),
        n => Some(format!("Cleared {n} dragged widths")),
    };
    let steps = counts
        .iter()
        .map(|n| {
            if *n == shown {
                format!("[{n}]")
            } else {
                n.to_string()
            }
        })
        .collect::<Vec<_>>()
        .join(" ");
    let card = col()
        .key("shelf.width.label")
        .role("status")
        .label(format!("{shown} tile{}", if shown == 1 { "" } else { "s" }))
        .padding(space::S2)
        .gap(space::S1)
        .radius(6.0)
        .fill(t.bg_panel)
        .border(1.0, t.border)
        .child(
            text(format!("{shown} tile{}", if shown == 1 { "" } else { "s" }))
                .font(type_::LABEL)
                .color(t.text_primary),
        )
        .child(
            text(format!("at least {cols} cols each"))
                .font(type_::LABEL)
                .color(t.text_secondary),
        )
        .children(cleared.map(|c| text(c).font(type_::LABEL).color(t.text_secondary)))
        .child(text(steps).font(type_::LABEL).color(t.text_tertiary));
    Some(
        rune::el()
            .key("shelf.width.layer")
            .absolute(0.0, crate::shell::TITLE_BAR_HEIGHT)
            .w_full()
            .child(
                row()
                    .w_full()
                    .justify_center()
                    .padding(space::S2)
                    .child(card),
            ),
    )
}

fn request_id() -> String {
    uuid::Uuid::new_v4().to_string()
}

/// Moves `leaf` beside `anchor` on `target`, leaving out what the daemon takes as given: no
/// share, a single leaf rather than its column, and the moved leaf becoming active.
#[allow(clippy::too_many_arguments)]
pub(crate) fn send_move(
    state: &State,
    source: &str,
    target: &str,
    leaf: &str,
    anchor: &str,
    edge: &str,
    share: Option<f32>,
    whole_column: bool,
    activate: bool,
) {
    let _ = state.daemon.send(&DesktopMoveLeafMessage {
        request_id: request_id(),
        source_desktop_id: source.to_owned(),
        target_desktop_id: Some(target.to_owned()),
        leaf_id: leaf.to_owned(),
        anchor_id: Some(anchor.to_owned()),
        edge: Some(edge.to_owned()),
        leaf_share: share.map(f64::from),
        whole_column: whole_column.then_some(true),
        activate: (!activate).then_some(false),
        ..DesktopMoveLeafMessage::new()
    });
}

fn move_leaf(
    state: &State,
    desktop: &str,
    leaf: &str,
    anchor: &str,
    edge: Edge,
    whole_column: bool,
    activate: bool,
) {
    send_move(
        state,
        desktop,
        desktop,
        leaf,
        anchor,
        edge.as_str(),
        None,
        whole_column,
        activate,
    );
}

pub(crate) fn clear_widths(state: &mut State, desktop: &str, leaves: &[String]) {
    for leaf in leaves {
        let _ = state.daemon.send(&DesktopSetWantedColsMessage {
            request_id: request_id(),
            desktop_id: desktop.to_owned(),
            leaf_id: leaf.clone(),
            cols: 0,
            area_cols: 0,
            ..DesktopSetWantedColsMessage::new()
        });
    }
    let row = state.window.desk.row_mut(desktop);
    row.pending_widths
        .extend(leaves.iter().map(|leaf| (leaf.clone(), 0)));
}

/// Brings `leaf`'s column beside the active tile, or jumps to it where it is when it is on
/// screen already or the two cannot show side by side.
pub(crate) fn bring(state: &mut State, desktop_id: &str, leaf: &str, _tx: &mut Tx) {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let Some(desktop) = facts.desktops.iter().find(|d| d.id == desktop_id) else {
        return;
    };
    let active = desktop.active_pane_id.clone();
    let jump = || actions::show_leaf(state, desktop_id, leaf);
    let Some((tree, plan)) = desk.plan(facts, desktop) else {
        return jump();
    };
    let Some(measures) = desk.measures(facts) else {
        return jump();
    };
    let on_screen = plan.screen.iter().any(|c| c.holds(leaf));
    let active_at = plan.screen.iter().position(|c| c.holds(&active));
    let row = layout::read_row(&tree);
    let (Some(active_at), Some(column), Some(active_column)) = (
        active_at,
        row.iter().find(|c| c.holds(leaf)),
        row.iter().find(|c| c.holds(&active)),
    ) else {
        return jump();
    };
    if on_screen
        || plan.screen.len() < 2
        || !layout::columns_fit_together(active_column, column, &measures)
    {
        return jump();
    }
    let sent_on = desktop.tree_json.clone();
    let active_then = active.clone();
    let edge = if active_at == plan.screen.len() - 1 {
        Edge::Left
    } else {
        Edge::Right
    };
    move_leaf(state, desktop_id, leaf, &active, edge, true, true);
    state.window.desk.row_mut(desktop_id).drop = Some(PendingDrop {
        drop: ShelfDrop {
            leaf: leaf.to_owned(),
            anchor: active,
            edge,
            whole_column: true,
        },
        tree: sent_on,
        active: active_then,
    });
}

/// Carries the active tile's column one place along the row.
pub(crate) fn carry(state: &mut State, step: i32, tx: &mut Tx) {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let Some(desktop) = facts.current_desktop() else {
        return;
    };
    let row_state = desk.row(&desktop.id);
    let active = desktop.active_pane_id.clone();
    if active.is_empty() || matches!(row_state.view, View::Focused(_)) {
        return;
    }
    let Some(tree) = Node::parse(&desktop.tree_json).and_then(|t| t.main()) else {
        return;
    };
    let row: Vec<Column> = layout::read_row(&tree)
        .into_iter()
        .filter(|c| c.holds(&active) || !c.leaf_ids.iter().any(|id| row_state.held.contains(id)))
        .collect();
    let Some(at) = layout::column_index(&row, &active) else {
        return;
    };
    let Some(neighbour) = at
        .checked_add_signed(step as isize)
        .and_then(|i| row.get(i))
    else {
        let end = if step < 0 { "start" } else { "end" };
        state
            .window
            .notify(format!("This tile is already at the {end} of the row."), tx);
        return;
    };
    let anchor = neighbour.first().to_owned();
    let desktop_id = desktop.id.clone();
    if let Some(first) = &row_state.first_column
        && step > 0
        && layout::column_index(&row, &first.leaf) == Some(at)
    {
        let index = first.index;
        state.window.desk.row_mut(&desktop_id).first_column = Some(layout::FirstColumn {
            leaf: anchor.clone(),
            index,
        });
    }
    let edge = if step < 0 { Edge::Left } else { Edge::Right };
    move_leaf(state, &desktop_id, &active, &anchor, edge, true, true);
}

/// Sends the active tile off screen to the shelf, showing the tile that takes its place.
pub(crate) fn send_to_shelf(state: &mut State, tx: &mut Tx) {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let Some(desktop) = facts.current_desktop() else {
        return;
    };
    let row_state = desk.row(&desktop.id);
    if desktop.active_pane_id.is_empty() || matches!(row_state.view, View::Focused(_)) {
        return;
    }
    let Some((tree, plan)) = desk.plan(facts, desktop) else {
        return;
    };
    let Some(input) = desk.row_input(facts, &tree, desktop, &row_state) else {
        return;
    };
    let send = layout::plan_send_to_shelf(&input, &plan.screen, &row_state.focus_order);
    let id = desktop.id.clone();
    match send {
        ShelfSend::Alone => state
            .window
            .notify("This is the only tile on screen.".into(), tx),
        ShelfSend::Unstack { leaf, anchor, next } => {
            actions::show_leaf(state, &id, &next);
            move_leaf(state, &id, &leaf, &anchor, Edge::Right, false, false);
        }
        ShelfSend::Hold { leaf, next } => {
            actions::show_leaf(state, &id, &next);
            state.window.desk.row_mut(&id).held.push(leaf.clone());
            flash_leaves(state, &id, vec![leaf], tx);
        }
        ShelfSend::Move {
            leaf,
            anchor,
            edge,
            next,
        } => {
            actions::show_leaf(state, &id, &next);
            move_leaf(state, &id, &leaf, &anchor, edge, true, false);
        }
    }
}

/// Fewer, wider tiles or more, narrower ones: the next narrowest width that changes how many
/// tiles fit, forgetting dragged widths when only they keep more off screen.
pub(crate) fn step_width(state: &mut State, fewer: bool, tx: &mut Tx) {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let Some(desktop) = facts.current_desktop() else {
        return;
    };
    let row_state = desk.row(&desktop.id);
    let Some(tree) = Node::parse(&desktop.tree_json).and_then(|t| t.main()) else {
        return;
    };
    let Some(input) = desk.row_input(facts, &tree, desktop, &row_state) else {
        return;
    };
    let current = Desk::narrowest_cols(facts);
    let window_px = desk.window_w.unwrap_or(0.0);
    let (cols, clear) = match layout::step_narrowest_cols(&input, current, fewer, window_px) {
        NarrowestStep::Limit(message) => return state.window.notify(message, tx),
        NarrowestStep::Step(cols) => (cols, vec![]),
        NarrowestStep::ClearThenStep { cols, clear, .. } => (cols, clear),
    };
    let id = desktop.id.clone();
    clear_widths(state, &id, &clear);
    if cols != current {
        let _ = state.daemon.send(&SetSettingMessage {
            key: "narrowest_tile_cols".into(),
            value: cols.to_string(),
            ..SetSettingMessage::new()
        });
    }
    let until = tx.clock() + WIDTH_LABEL_SECONDS;
    state.window.desk.width_label = Some(WidthLabel {
        cleared: clear.len(),
        until,
    });
    tx.wake_at(until);
}

/// After the daemon's row changes: a brought column that landed sets where the screen starts
/// so its anchor keeps its place, forgetting dragged widths that keep the two apart, and
/// every other row keeps the start its screen settled on.
pub(crate) fn settle(state: &mut State, tx: &mut Tx) {
    let ids: Vec<String> = state.facts.desktops.iter().map(|d| d.id.clone()).collect();
    for id in ids {
        settle_desktop(state, &id, tx);
    }
    let desktops = &state.facts.desktops;
    state
        .window
        .desk
        .flashes
        .retain(|f| desktops.iter().any(|d| d.id == f.desktop));
    super::drag::settle_drag(state);
    super::divider::settle_ratios(state);
    super::peek::settle(state);
}

fn settle_desktop(state: &mut State, id: &str, tx: &mut Tx) {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let Some(desktop) = facts.desktops.iter().find(|d| d.id == *id) else {
        return;
    };
    let desktop_tree = desktop.tree_json.clone();
    let Some(tree) = Node::parse(&desktop.tree_json).and_then(|t| t.main()) else {
        return;
    };
    let row_state = desk.row(id);
    let leaves = tree.leaves();
    let key = Settled {
        active: desktop.active_pane_id.clone(),
        order: leaves.iter().map(|l| l.id.clone()).collect(),
        show_requests: leaves.iter().map(|l| show_requests(l)).collect(),
        screen: vec![],
    };
    let Some(input) = desk.row_input(facts, &tree, desktop, &row_state) else {
        let row_mut = state.window.desk.row_mut(id);
        row_mut.settled.get_or_insert(key);
        return;
    };
    let row = layout::read_row(&tree);
    let awaited = |(leaf, cols): &(String, u32)| {
        row.iter()
            .any(|c| c.first() == leaf && c.wanted_cols.unwrap_or(0) != *cols)
    };
    let mid_drag = desk.column_drag.as_ref().is_some_and(|d| d.desktop == *id);
    let pending: Vec<(String, u32)> = row_state
        .pending_widths
        .iter()
        .filter(|w| mid_drag || awaited(w))
        .cloned()
        .collect();
    let plan = layout::resolve_row(&input);
    let visible = state.window.view == crate::shell::View::Desktop
        && facts.current_desktop().is_some_and(|d| d.id == *id);
    let landed = row_state
        .drop
        .as_ref()
        .filter(|pending| dropped_beside(&row, &desktop.active_pane_id, &pending.drop));
    let reveal = landed
        .and_then(|pending| layout::reveal_drop(&input, &pending.drop, plan.first_column.index));
    let waiting = row_state.drop.clone().filter(|pending| {
        landed.is_none()
            && pending.tree == desktop.tree_json
            && pending.active == desktop.active_pane_id
    });
    let previous = row_state.settled.clone();
    let moved = previous.as_ref().is_none_or(|p| !p.same_row(&key));
    let mut first = match &reveal {
        Some((first, _)) => Some(first.clone()),
        None if moved || landed.is_some() => Some(plan.first_column.clone()),
        None => row_state.first_column.clone(),
    };
    let mut unseen = row_state.unseen.clone();
    let mut returns = row_state.returns.clone();
    let mut flash: Vec<String> = vec![];
    if let Some(previous) = previous.as_ref().filter(|_| moved) {
        let was_first = row_state
            .first_column
            .clone()
            .unwrap_or_else(|| plan.first_column.clone());
        let arrivals = previous.arrivals(&key, &plan.screen);
        for (leaf, back) in &returns {
            if !key.order.contains(leaf)
                && previous.order.contains(leaf)
                && was_first.leaf == back.shown_at
                && layout::column_index(&row, &back.first_column.leaf).is_some()
            {
                first = Some(back.first_column.clone());
            }
        }
        let on_screen = |leaf: &str| plan.screen.iter().any(|c| c.holds(leaf));
        for leaf in arrivals.iter().filter(|l| **l != desktop.active_pane_id) {
            let Some(owner) = leaves.iter().find_map(|l| match &l.kind {
                LeafKind::Tile { session, .. } if l.id == *leaf => Some(session.clone()),
                _ => None,
            }) else {
                continue;
            };
            let owner_shown = owner
                .and_then(|s| desktop.panes.iter().find(|p| p.session_id == s))
                .is_some_and(|p| previous.screen.contains(&p.pane_id));
            let revealed = match (owner_shown, on_screen(leaf)) {
                (false, _) => None,
                (true, true) => Some(plan.first_column.clone()),
                (true, false) => layout::reveal_first_column(&input, leaf, plan.first_column.index),
            };
            if let Some(revealed) = &revealed {
                first = Some(revealed.clone());
            } else if !on_screen(leaf) && !unseen.contains(leaf) {
                unseen.push(leaf.clone());
            }
            let shown_at = revealed.unwrap_or_else(|| plan.first_column.clone()).leaf;
            set_return(&mut returns, leaf, was_first.clone(), shown_at);
        }
        if desktop.active_pane_id != previous.active && unseen.contains(&desktop.active_pane_id) {
            let shown_at = first
                .clone()
                .unwrap_or_else(|| plan.first_column.clone())
                .leaf;
            set_return(&mut returns, &desktop.active_pane_id, was_first, shown_at);
        }
        let reordered = previous.order != key.order;
        flash = arrivals
            .into_iter()
            .chain(previous.screen.iter().filter(|_| reordered).cloned())
            .collect();
    }
    let settled_input = RowInput {
        first_column: first.as_ref(),
        ..input
    };
    let screen: Vec<String> = layout::resolve_row(&settled_input)
        .screen
        .iter()
        .flat_map(|c| c.leaf_ids.clone())
        .collect();
    let seen = visible || row_state.shown;
    let settled = Settled {
        screen: if seen { screen.clone() } else { vec![] },
        ..key
    };
    unseen.retain(|l| settled.order.contains(l) && !screen.contains(l));
    returns.retain(|(l, _)| settled.order.contains(l));
    let flash: Vec<String> = flash
        .into_iter()
        .filter(|l| visible && settled.order.contains(l) && !screen.contains(l))
        .collect();
    let row_mut = state.window.desk.row_mut(id);
    super::drag::settle_swap(row_mut, &row, &desktop_tree);
    row_mut.pending_widths = pending;
    row_mut.drop = waiting;
    row_mut.settled = Some(settled);
    row_mut.first_column = first;
    row_mut.unseen = unseen;
    row_mut.returns = returns;
    row_mut.shown = seen;
    if let Some((_, clear)) = reveal {
        clear_widths(state, id, &clear);
    }
    flash_leaves(state, id, flash, tx);
}

fn show_requests(leaf: &layout::Leaf) -> u64 {
    match &leaf.kind {
        LeafKind::Tile { show_requests, .. } => *show_requests,
        LeafKind::Pane => 0,
    }
}

fn set_return(
    returns: &mut Vec<(String, Return)>,
    leaf: &str,
    first_column: layout::FirstColumn,
    shown_at: String,
) {
    returns.retain(|(l, _)| l != leaf);
    returns.push((
        leaf.to_owned(),
        Return {
            first_column,
            shown_at,
        },
    ));
}

/// Flashes the chips of `leaves` on desktop `desktop` for a moment.
fn flash_leaves(state: &mut State, desktop: &str, leaves: Vec<String>, tx: &mut Tx) {
    if leaves.is_empty() {
        return;
    }
    let until = tx.clock() + FLASH_SECONDS;
    let flashes = &mut state.window.desk.flashes;
    flashes.retain(|f| f.desktop != desktop || !leaves.contains(&f.leaf));
    flashes.extend(leaves.into_iter().map(|leaf| Flash {
        desktop: desktop.to_owned(),
        leaf,
        until,
    }));
    tx.wake_at(until);
}

/// What a row's screen start settles on: it settles again only when the active tile, the
/// order of the row's leaves or a tile's show requests change, so a width or window change
/// leaves it where it was. `screen` is the leaves the screen showed then, empty on a desktop
/// this window never showed.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Settled {
    active: String,
    order: Vec<String>,
    show_requests: Vec<u64>,
    screen: Vec<String>,
}

impl Settled {
    fn same_row(&self, other: &Settled) -> bool {
        self.active == other.active
            && self.order == other.order
            && self.show_requests == other.show_requests
    }

    /// The leaves new to the row in `now`, and the tiles off `screen` asked to show again.
    fn arrivals(&self, now: &Settled, screen: &[Column]) -> Vec<String> {
        let asked = |at: usize, leaf: &str| {
            let before = self
                .order
                .iter()
                .position(|l| l == leaf)
                .map_or(0, |i| self.show_requests[i]);
            !screen.iter().any(|c| c.holds(leaf)) && now.show_requests[at] != before
        };
        now.order
            .iter()
            .enumerate()
            .filter(|(at, leaf)| !self.order.contains(leaf) || asked(*at, leaf))
            .map(|(_, leaf)| leaf.clone())
            .collect()
    }
}

/// A column brought beside the active tile, and the row and active tile as they were when the
/// window asked for it: once either changes without the column landing, the bring is over.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct PendingDrop {
    pub drop: ShelfDrop,
    pub tree: String,
    pub active: String,
}

fn dropped_beside(row: &[Column], active: &str, drop: &ShelfDrop) -> bool {
    active == drop.leaf && beside(row, drop)
}

/// The moved leaf sits in the column on the drop's side of its anchor, alone there unless it
/// brought its whole column.
pub(crate) fn beside(row: &[Column], drop: &ShelfDrop) -> bool {
    let Some(at) = layout::column_index(row, &drop.anchor) else {
        return false;
    };
    let next = match drop.edge {
        Edge::Left => at.checked_sub(1).and_then(|i| row.get(i)),
        Edge::Right => row.get(at + 1),
    };
    next.is_some_and(|c| c.holds(&drop.leaf) && (drop.whole_column || c.leaf_ids.len() == 1))
}

impl Desk {
    /// The input the row's layout reads for `desktop`, once the tile area is laid out.
    pub(crate) fn row_input<'a>(
        &self,
        facts: &attn_client::Facts,
        tree: &'a Node,
        desktop: &'a Desktop,
        row: &'a super::RowState,
    ) -> Option<RowInput<'a>> {
        Some(RowInput {
            tree,
            active: &desktop.active_pane_id,
            first_column: row.first_column.as_ref(),
            held: &row.held,
            pending: &row.pending_widths,
            measures: self.measures(facts)?,
        })
    }
}
