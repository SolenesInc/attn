//! The shelf: the columns of the current desktop's row that the screen does not show, as
//! chips in the title bar either side of a dot per column on screen.

use attn_client::Desktop;
use attn_protocol::{DesktopMoveLeafMessage, DesktopSetWantedColsMessage, SetSettingMessage};
use attn_ui::Tokens;
use attn_ui::blocks::{self, Card, Inline, Menu};
use attn_ui::system::{
    DotShape, DotSize, IconSize, Size, Type, dot as state_dot, icon_button, words,
};
use rune::{Drag, El, Phase, Tx, row};

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
const MARK: DotSize = DotSize::Small;
const CAPS_PX: f32 = 104.0;
/// How long the width label shows after fewer or more tiles.
const WIDTH_LABEL_SECONDS: f64 = 1.5;
/// How long a chip flashes when its tile arrives on the shelf.
const FLASH_SECONDS: f64 = 0.9;
/// The Tauri app's `top: calc(100% + 4px)` between the shelf's dots and the width control,
/// which the control's own edge spans so the pointer crosses it without leaving both.
const WIDTH_CONTROL_GAP_PX: f32 = 4.0;
/// How far under the title bar's controls the layers they open hang.
const ANCHOR_GAP_PX: f32 = 4.0;

const FLASH_FADE: &[rune::Beat] = &[
    rune::Beat::new(0.0).opacity(1.0),
    rune::Beat::new(FLASH_SECONDS * 0.6).opacity(1.0),
    rune::Beat::new(FLASH_SECONDS)
        .opacity(0.0)
        .easing(rune::Easing::EASE_OUT),
];

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
    /// A tile in the column just arrived on the shelf: when its flash ends, as `f64` bits.
    arrival: Option<u64>,
    members: Vec<Member>,
    leaves: Vec<String>,
    /// How many of the column's agents have open asks.
    asking: usize,
}

fn member(desktop: &Desktop, facts: &attn_client::Facts, leaf: &str) -> Member {
    super::view::pane_of(desktop, leaf)
        .and_then(|pane| facts.sessions.iter().find(|s| s.id == pane.session_id))
        .map_or(Member::Tile, |s| Member::Agent(AgentState::parse(&s.state)))
}

/// A tile's name on the shelf, as its header gives it.
pub(crate) fn title(
    desktop: &Desktop,
    facts: &attn_client::Facts,
    tree: &Node,
    leaf: &str,
) -> String {
    match tree.find_leaf(leaf) {
        Some(Node::Leaf(l)) => super::view::leaf_title(
            desktop,
            &facts.sessions,
            &facts.tile_contents,
            facts.garden.of(&facts.profiles.selected),
            l,
        ),
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
    flashing: &[(&str, u64)],
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
        arrival: flashing
            .iter()
            .find(|(leaf, _)| column.leaf_ids.iter().any(|l| l == leaf))
            .map(|(_, until)| *until),
        members,
        leaves: column.leaf_ids.clone(),
        asking: column
            .leaf_ids
            .iter()
            .filter_map(|leaf| super::view::pane_of(desktop, leaf))
            .filter_map(|pane| facts.sessions.iter().find(|s| s.id == pane.session_id))
            .filter(|s| !crate::asks::of_session(facts, s).is_empty())
            .count(),
    }
}

/// How many chips show either side when the shelf is `available` points wide; the rest go
/// into the caps, the chips nearest the screen kept.
fn allot(available: f32, before: usize, after: usize, live: f32) -> (usize, usize) {
    let without_caps = ((available - live) / CHIP_PX).floor();
    if (before + after) as f32 <= without_caps {
        return (before, after);
    }
    let room = ((available - live - CAPS_PX) / CHIP_PX).floor().max(0.0) as usize;
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
    /// The width of the live dots and the caps, under which the shelf overflows its slot.
    least_px: f32,
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

/// The current desktop with its row's plan, when the shelf shows: some column is off screen,
/// the window shows the desktop, and the active tile is not in focus mode.
fn shelved(state: &State) -> Option<(&Desktop, Node, layout::RowPlan)> {
    if state.window.view != crate::shell::View::Desktop {
        return None;
    }
    let desk = &state.window.desk;
    let desktop = state.facts.current_desktop()?;
    if matches!(desk.row(&desktop.id).view, View::Focused(_)) {
        return None;
    }
    let (tree, plan) = desk.plan(&state.facts, desktop)?;
    (!plan.before.is_empty() || !plan.after.is_empty()).then_some((desktop, tree, plan))
}

/// How many columns stand before the screen, on it and after it while the shelf shows: what
/// changes the room the shelf takes in the title bar.
pub(crate) fn columns(state: &State) -> Option<[usize; 3]> {
    shelved(state).map(|(_, _, p)| [p.before.len(), p.screen.len(), p.after.len()])
}

impl Shelf {
    /// The shelf of the current desktop, or None when nothing is off screen, the window shows
    /// Home, or the active tile is in focus mode.
    pub(crate) fn of(state: &State) -> Option<Shelf> {
        let (desktop, tree, plan) = shelved(state)?;
        let facts = &state.facts;
        let desk = &state.window.desk;
        let row_state = desk.row(&desktop.id);
        let flashing: Vec<(&str, u64)> = desk
            .flashes
            .iter()
            .filter(|f| f.desktop == desktop.id)
            .map(|f| (f.leaf.as_str(), f.until.to_bits()))
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
        let live = screen.iter().map(dot_px).sum::<f32>() + blocks::Shelf::gap();
        let (left, right) = allot(available, before.len(), after.len(), live);
        Some(Shelf {
            desktop: desktop.id.clone(),
            active: desktop.active_pane_id.clone(),
            least_px: live + CAPS_PX,
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

fn view(state: &State, shelf: Option<Shelf>, t: &Tokens) -> Option<El> {
    let Some(shelf) = shelf else {
        return holding(state, Owner::Shelf).then(owner);
    };
    let desk = &state.window.desk;
    let id = &shelf.desktop;
    let dots = row()
        .key("shelf.live")
        .items_center()
        .on_hover_change(|state: &mut State, inside, _: &mut Tx| {
            state.window.desk.width_control.dots = inside
        })
        .children(shelf.screen.iter().map(|chip| {
            let current = chip.leaves.contains(&shelf.active);
            dot(chip, current, id, t)
        }));
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
            .on_layout_tx(|state: &mut State, rect, tx: &mut Tx| record(state, "shelf", rect, tx))
            .children(menu(Edge::Left, shelf.before_cap()))
            .children(
                shelf
                    .before_chips()
                    .iter()
                    .map(|c| chip_button(c, id, peeked.as_deref(), mark(c), Width::Slot, t)),
            )
            .child(dots)
            .children(
                shelf
                    .after_chips()
                    .iter()
                    .map(|c| chip_button(c, id, peeked.as_deref(), mark(c), Width::Slot, t)),
            )
            .children(menu(Edge::Right, shelf.after_cap())),
    )
}

/// The shelf's row, which runs its chips' drags.
fn owner() -> El {
    super::view::moves_with_the_row(blocks::Shelf::row("shelf")).on_drag(
        |state: &mut State, d: &mut Drag, tx: &mut Tx| {
            super::drag::gesture(state, d, tx, Owner::Shelf)
        },
    )
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

/// The title bar's middle, which the shelf sits in and measures for its chips. It keeps room
/// for the shelf's live dots and caps, so what follows it moves aside.
pub(crate) fn slot(state: &State, t: &Tokens) -> El {
    let shelf = Shelf::of(state);
    row()
        .key("shelf.slot")
        .grow(1.0)
        .min_w(shelf.as_ref().map_or(0.0, |s| s.least_px))
        .h_full()
        .items_center()
        .on_layout_tx(|state: &mut State, [_, _, w, _], tx: &mut Tx| {
            if state.window.desk.shelf_px == Some(w) {
                tx.live_only();
                return;
            }
            state.window.desk.shelf_px = Some(w);
        })
        .children(view(state, shelf, t))
}

/// How wide a column's chip stands: its slot of the shelf, or the whole width of a cap's menu.
#[derive(Clone, Copy)]
enum Width {
    Slot,
    Full,
}

/// A column's chip. The pointer resting on it peeks at the tile it brings, or at the tile whose
/// mark it rests on; `peeked` is the tile the open peek shows.
fn chip_button(
    chip: &Chip,
    desktop: &str,
    peeked: Option<&str>,
    drop: Option<Edge>,
    width: Width,
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
                .border_sides(
                    [0.0, 0.0, 2.0, 0.0],
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
    let flash = chip.arrival.map(|until| flash_ring(&key, until, t));
    let state = [
        (chip.needs, "needs you"),
        (chip.arrival.is_some(), "arrived"),
    ]
    .iter()
    .filter(|(on, _)| *on)
    .map(|(_, words)| *words)
    .collect::<Vec<_>>()
    .join(", ");
    let face = Inline::joined()
        .w_full()
        .child(blocks::Shelf::marks(marks))
        .child(
            words(
                chip.label.clone(),
                Type::Small,
                if chip.arrival.is_some() {
                    t.text_primary
                } else {
                    t.text_secondary
                },
            )
            .single_line()
            .truncate()
            .grow(1.0),
        )
        .children((0..chip.asking).map(|i| {
            words("?", Type::Label, t.accent)
                .key(format!("{key}.ask.{i}"))
                .role("img")
                .label("Open ask")
        }));
    let chip = blocks::Shelf::chip(key.clone(), chip.label.clone(), face, false, t)
        .a11y_value(state)
        .min_w(0.0)
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
        .children(flash);
    let chip = match width {
        Width::Slot => chip.w(CHIP_PX - blocks::Shelf::gap()),
        Width::Full => chip.w_full(),
    };
    drop_bar(chip, drop, t)
}

/// The accent ring a chip shows while its tile's arrival flashes, held and then fading, as
/// the Tauri app's `desktop-shelf-chip-arrival`; keyed by when the flash ends, so a new flash
/// plays from the start.
fn flash_ring(chip_key: &str, until: u64, t: &Tokens) -> El {
    blocks::Shelf::flash(format!("{chip_key}.flash.{until}"), t)
        .transition(rune::Transition::none())
        .sequence(rune::Sequence::new(FLASH_FADE))
}

/// A tile's mark on its chip: an agent's state dot, or a square, in the warning colour while
/// the chip needs the person.
fn member_mark(member: Member, needs: bool, t: &Tokens) -> El {
    match member {
        Member::Agent(state) => state_dot(
            crate::sidebar::state_color(state, t),
            DotShape::Filled,
            MARK,
        ),
        Member::Tile => state_dot(
            if needs { t.warning } else { t.text_secondary },
            DotShape::Square,
            MARK,
        ),
    }
}

/// How wide a tile on screen's button is: its marks, and at least `LIVE_DOT_PX`.
fn dot_px(chip: &Chip) -> f32 {
    blocks::Shelf::marks_chip_width(chip.members.len(), MARK).max(LIVE_DOT_PX)
}

/// A tile on screen's button in the shelf, as the Tauri app's `.desktop-shelf-dot`: the marks
/// of its column's tiles, filled while it holds the active tile.
fn dot(chip: &Chip, current: bool, desktop: &str, t: &Tokens) -> El {
    let (desktop, leaf) = (desktop.to_owned(), chip.go.clone());
    blocks::Shelf::chip(
        format!("shelf.dot.{}", chip.key),
        chip.label.clone(),
        blocks::Shelf::marks(chip.members.iter().map(|m| member_mark(*m, false, t))),
        current,
        t,
    )
    .a11y_value(if current { "current" } else { "" })
    .min_w(LIVE_DOT_PX)
    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
        actions::show_leaf(state, &desktop, &leaf, tx)
    })
}

/// The accent bar on the side of a chip or cap a dragged tile would land on.
fn drop_bar(el: El, drop: Option<Edge>, t: &Tokens) -> El {
    match drop {
        None => el,
        Some(Edge::Left) => el.border_sides([0.0, 0.0, 0.0, 2.0], t.accent),
        Some(Edge::Right) => el.border_sides([0.0, 2.0, 0.0, 0.0], t.accent),
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
    let ink = if chips.iter().any(|c| c.needs) {
        t.warning
    } else {
        t.text_secondary
    };
    let cap = blocks::Shelf::chip(
        key,
        format!("{n} more this way"),
        words(caption, Type::Label, ink),
        open,
        t,
    )
    .on_press(move |state: &mut State| {
        let desk = &mut state.window.desk;
        desk.cap_open = (desk.cap_open != Some(side)).then_some(side);
    })
    .on_layout_tx(move |state: &mut State, rect, tx: &mut Tx| record(state, &recorded, rect, tx))
    .a11y_value(if open { "open" } else { "" })
    .focusable(true);
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
    let menu = Menu::new(
        format!("shelf.menu.{name}"),
        format!("{} more this way", held.len()),
    )
    .rows(
        held.iter()
            .map(|c| chip_button(c, &shelf.desktop, peeked.as_deref(), None, Width::Full, t)),
    )
    .build(t)
    .anchored(
        format!("shelf.cap.{name}"),
        rune::Side::Bottom,
        match side {
            Edge::Left => rune::Align::Start,
            Edge::Right => rune::Align::End,
        },
        ANCHOR_GAP_PX,
    );
    Some(layer.child(menu))
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

/// Where the pointer is that shows the width control: over the shelf's dots or over the control.
#[derive(Default)]
pub(crate) struct WidthControlHover {
    dots: bool,
    control: bool,
}

/// The width control, as the Tauri app's `.desktop-shelf-stepper`: under the shelf's dots
/// while the pointer is over them or over it, drawn over the tiles so the chips beside the dots
/// never move. Under dots that lead the shelf it starts at their left edge.
pub(crate) fn width_control_layer(state: &State, t: &Tokens) -> Option<El> {
    let hover = &state.window.desk.width_control;
    if !(hover.dots || hover.control) {
        return None;
    }
    let shelf = Shelf::of(state)?;
    let leading = shelf.before_cap().is_empty() && shelf.before_chips().is_empty();
    let control = raised(
        "shelf.width.anchor",
        width_control(shelf.screen.len(), Desk::narrowest_cols(&state.facts), t),
        t,
    );
    Some(
        rune::col()
            .child(rune::el().h(WIDTH_CONTROL_GAP_PX))
            .child(control)
            .on_hover_change(|state: &mut State, inside, _: &mut Tx| {
                state.window.desk.width_control.control = inside
            })
            .anchored(
                "shelf.live",
                rune::Side::Bottom,
                if leading {
                    rune::Align::Start
                } else {
                    rune::Align::Center
                },
                0.0,
            ),
    )
}

/// `content` on a small card raised over the tiles, keyed `key`.
fn raised(key: &str, content: El, t: &Tokens) -> El {
    attn_ui::Frame::menu(t).draw(Card::new(key).push(content).build(t))
}

fn width_control(shown: usize, cols: u32, t: &Tokens) -> El {
    let button = |key: &str, label: &str, mark: &'static str, action: Action| {
        El::from(
            icon_button(key, label, mark, IconSize::Small)
                .size(Size::Small)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    crate::dispatch::dispatch(state, action.clone(), tx);
                })
                .build(t),
        )
    };
    Inline::joined()
        .key("shelf.width")
        .child(
            words(
                format!("{shown} · ≥{cols} cols"),
                Type::Label,
                t.text_tertiary,
            )
            .single_line(),
        )
        .child(button(
            "shelf.width.fewer",
            "Fewer, wider tiles",
            attn_ui::glyph::MINUS,
            Action::ShelfFewerTiles,
        ))
        .child(button(
            "shelf.width.more",
            "More, narrower tiles",
            attn_ui::glyph::PLUS,
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
    let tiles = format!("{shown} tile{}", if shown == 1 { "" } else { "s" });
    let lines = attn_ui::blocks::Stack::tight()
        .child(words(tiles.clone(), Type::Name, t.text_primary))
        .child(words(
            format!("at least {cols} cols each"),
            Type::Small,
            t.text_secondary,
        ))
        .children(cleared.map(|c| words(c, Type::Small, t.text_secondary)))
        .child(words(steps, Type::Mono, t.text_tertiary));
    let card = raised("shelf.width.label", lines, t)
        .role("status")
        .label(tiles);
    Some(
        rune::el()
            .key("shelf.width.layer")
            .inert()
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .child(card.anchored(
                "shelf.slot",
                rune::Side::Bottom,
                rune::Align::Center,
                ANCHOR_GAP_PX,
            )),
    )
}

/// Moves `leaf` beside `anchor` on `target`, leaving out what the daemon takes as given: no
/// share, a single leaf rather than its column, and the moved leaf becoming active. A refusal
/// is named as the failure to do `what`.
#[allow(clippy::too_many_arguments)]
pub(crate) fn send_move(
    state: &mut State,
    source: &str,
    target: &str,
    leaf: &str,
    anchor: &str,
    edge: &str,
    share: Option<f32>,
    whole_column: bool,
    activate: bool,
    what: &'static str,
    tx: &mut Tx,
) {
    send_move_then(
        state,
        (source, target, leaf, anchor, edge),
        share,
        whole_column,
        activate,
        what,
        |_, _, _| {},
        tx,
    );
}

/// [`send_move`], running `then` once the daemon answers, told whether it made the move.
#[allow(clippy::too_many_arguments)]
pub(crate) fn send_move_then(
    state: &mut State,
    (source, target, leaf, anchor, edge): (&str, &str, &str, &str, &str),
    share: Option<f32>,
    whole_column: bool,
    activate: bool,
    what: &'static str,
    then: impl FnOnce(&mut State, bool, &mut Tx) + 'static,
    tx: &mut Tx,
) {
    let (desktop, moved) = (source.to_owned(), leaf.to_owned());
    state.ask(
        |id| DesktopMoveLeafMessage {
            request_id: id,
            source_desktop_id: source.to_owned(),
            target_desktop_id: Some(target.to_owned()),
            leaf_id: leaf.to_owned(),
            anchor_id: Some(anchor.to_owned()),
            edge: Some(edge.to_owned()),
            leaf_share: share.map(f64::from),
            whole_column: whole_column.then_some(true),
            activate: (!activate).then_some(false),
            ..DesktopMoveLeafMessage::new()
        },
        move |state: &mut State, answer, tx: &mut Tx| match answer.outcome() {
            Err(error) => {
                refused_move(state, &desktop, &moved, what, &error, tx);
                then(state, false, tx);
            }
            Ok(_) => then(state, true, tx),
        },
        tx,
    );
}

/// The daemon refused to move `leaf`: the row shows what it showed before, and the keyboard
/// goes back to the active tile when it went ahead to `leaf`, which never arrived.
fn refused_move(
    state: &mut State,
    desktop: &str,
    leaf: &str,
    what: &str,
    error: &str,
    tx: &mut Tx,
) {
    let moved = super::arrival::Shown::Leaf {
        desktop: desktop.to_owned(),
        leaf: leaf.to_owned(),
    };
    super::arrival::forget(state, &moved);
    let row = state.window.desk.row_mut(desktop);
    if row.drop.as_ref().is_some_and(|p| p.drop.leaf == leaf) {
        row.drop = None;
    }
    if let Some(swap) = row.swap.take_if(|s| s.chip == leaf || s.slot == leaf) {
        row.held.retain(|id| *id != swap.slot);
    }
    let lost = take_back_keyboard(state, desktop, leaf, tx);
    let text = if lost {
        format!("Input typed while switching could not be delivered: {error}")
    } else {
        format!("Could not {what}: {error}")
    };
    state.window.notify(text, tx);
}

/// Gives the keyboard back to `desktop`'s active tile when `leaf` holds it without being
/// active, dropping what was typed for `leaf`'s terminal; whether anything was dropped.
fn take_back_keyboard(state: &mut State, desktop: &str, leaf: &str, tx: &mut Tx) -> bool {
    let holds = state
        .window
        .keyboard
        .as_ref()
        .is_some_and(|h| h.desktop == desktop && h.pane == leaf);
    let Some(d) = state.facts.desktops.iter().find(|d| d.id == desktop) else {
        return false;
    };
    if !holds || d.active_pane_id == leaf {
        return false;
    }
    let active = d.active_pane_id.clone();
    let runtime = super::view::pane_of(d, leaf).map(|p| p.runtime_id.clone());
    let lost = runtime.is_some_and(|r| state.terminals.discard_held(&r));
    actions::give_keyboard(state, desktop, &active, tx);
    lost
}

#[allow(clippy::too_many_arguments)]
fn move_leaf(
    state: &mut State,
    desktop: &str,
    leaf: &str,
    anchor: &str,
    edge: Edge,
    whole_column: bool,
    activate: bool,
    what: &'static str,
    tx: &mut Tx,
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
        what,
        tx,
    );
}

/// Forgets every width dragged on `desktop`, or says there is none.
pub(crate) fn clear_dragged_widths(state: &mut State, desktop: &str, tx: &mut Tx) {
    let leaves: Vec<String> = state
        .facts
        .desktops
        .iter()
        .find(|d| d.id == desktop)
        .and_then(Desktop::tree)
        .map(|tree| {
            layout::read_row(tree)
                .iter()
                .filter(|c| c.wanted_cols.is_some())
                .map(|c| c.first().to_owned())
                .collect()
        })
        .unwrap_or_default();
    if leaves.is_empty() {
        state
            .window
            .notify("This desktop has no dragged widths.".into(), tx);
        return;
    }
    clear_widths(state, desktop, &leaves, tx);
}

pub(crate) fn clear_widths(state: &mut State, desktop: &str, leaves: &[String], tx: &mut Tx) {
    for leaf in leaves {
        let (desktop_id, leaf_id) = (desktop.to_owned(), leaf.clone());
        let (row_of, cleared) = (desktop.to_owned(), leaf.clone());
        state.ask(
            |request_id| DesktopSetWantedColsMessage {
                request_id,
                desktop_id,
                leaf_id,
                cols: 0,
                area_cols: 0,
                ..DesktopSetWantedColsMessage::new()
            },
            move |state: &mut State, answer, tx: &mut Tx| {
                if let Err(error) = answer.outcome() {
                    let row = state.window.desk.row_mut(&row_of);
                    row.pending_widths
                        .retain(|(leaf, cols)| !(*leaf == cleared && *cols == 0));
                    state
                        .window
                        .notify(format!("Could not save that width: {error}"), tx);
                }
            },
            tx,
        );
    }
    let row = state.window.desk.row_mut(desktop);
    row.pending_widths
        .extend(leaves.iter().map(|leaf| (leaf.clone(), 0)));
}

/// Brings `leaf`'s column beside the active tile, or jumps to it where it is when it is on
/// screen already or the two cannot show side by side. Either way it takes the keyboard.
pub(crate) fn bring(state: &mut State, desktop_id: &str, leaf: &str, tx: &mut Tx) {
    let Some((active, edge, sent_on)) = beside_active(state, desktop_id, leaf) else {
        return actions::show_leaf(state, desktop_id, leaf, tx);
    };
    let active_then = active.clone();
    actions::give_keyboard(state, desktop_id, leaf, tx);
    let brought = super::arrival::Shown::Leaf {
        desktop: desktop_id.to_owned(),
        leaf: leaf.to_owned(),
    };
    super::arrival::expect(state, brought, true, tx);
    move_leaf(
        state,
        desktop_id,
        leaf,
        &active,
        edge,
        true,
        true,
        "move that tile",
        tx,
    );
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

/// The active tile, the side of it `leaf`'s column goes to, and the tree the move is made
/// on, when that column can come beside the active tile: off screen, and fitting beside it.
fn beside_active(state: &State, desktop_id: &str, leaf: &str) -> Option<(String, Edge, String)> {
    let facts = &state.facts;
    let desk = &state.window.desk;
    let desktop = facts.desktops.iter().find(|d| d.id == desktop_id)?;
    let active = desktop.active_pane_id.clone();
    let (tree, plan) = desk.plan(facts, desktop)?;
    let measures = desk.measures(facts)?;
    let on_screen = plan.screen.iter().any(|c| c.holds(leaf));
    let active_at = plan.screen.iter().position(|c| c.holds(&active))?;
    let row = layout::read_row(&tree);
    let column = row.iter().find(|c| c.holds(leaf))?;
    let active_column = row.iter().find(|c| c.holds(&active))?;
    if on_screen
        || plan.screen.len() < 2
        || !layout::columns_fit_together(active_column, column, &measures)
    {
        return None;
    }
    let edge = if active_at == plan.screen.len() - 1 {
        Edge::Left
    } else {
        Edge::Right
    };
    Some((active, edge, desktop.tree_json.clone()))
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
    let Some(tree) = desktop.tree().and_then(|t| t.main()) else {
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
    move_leaf(
        state,
        &desktop_id,
        &active,
        &anchor,
        edge,
        true,
        true,
        "carry that tile",
        tx,
    );
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
            actions::show_leaf(state, &id, &next, tx);
            move_leaf(
                state,
                &id,
                &leaf,
                &anchor,
                Edge::Right,
                false,
                false,
                "send that tile to the shelf",
                tx,
            );
        }
        ShelfSend::Hold { leaf, next } => {
            actions::show_leaf(state, &id, &next, tx);
            state.window.desk.row_mut(&id).held.push(leaf.clone());
            flash_leaves(state, &id, vec![leaf], tx);
        }
        ShelfSend::Move {
            leaf,
            anchor,
            edge,
            next,
        } => {
            actions::show_leaf(state, &id, &next, tx);
            state.window.desk.row_mut(&id).held.push(leaf.clone());
            let (row_of, released) = (id.clone(), leaf.clone());
            send_move_then(
                state,
                (&id, &id, &leaf, &anchor, edge.as_str()),
                None,
                true,
                false,
                "send that tile to the shelf",
                move |state: &mut State, _, _| {
                    state
                        .window
                        .desk
                        .row_mut(&row_of)
                        .held
                        .retain(|held| *held != released);
                },
                tx,
            );
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
    let Some(tree) = desktop.tree().and_then(|t| t.main()) else {
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
    clear_widths(state, &id, &clear, tx);
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
    let Some(tree) = desktop.tree().and_then(|t| t.main()) else {
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
        clear_widths(state, id, &clear, tx);
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
