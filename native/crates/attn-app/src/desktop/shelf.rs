//! The shelf: an entry per column of a desktop's row, in the title bar, with a frame behind the
//! columns on screen and a count for the ones folded away at either end. The row is the current
//! desktop's, or while the overview is open, that of the desktop it shows.

use attn_client::Desktop;
use attn_protocol::{DesktopMoveLeafMessage, DesktopSetWantedColsMessage, SetSettingMessage};
use attn_ui::blocks::{
    self, Card, CountSide, Inline, MemberState, Place, ShelfCount, ShelfEntry, ShelfMember,
};
use attn_ui::system::{IconSize, Size, Type, icon_button, words};
use attn_ui::{Glyph, Tokens};
use rune::{Drag, El, PointerEvent, Tx, row};

use super::drag::{Owner, Source};

use super::layout::{
    self, Column, Edge, LeafKind, NarrowestStep, Node, RowInput, ShelfDrop, ShelfSend, View,
};
use super::{Desk, actions, role_icon};
use crate::State;
use crate::action::Action;
use crate::sidebar::AgentState;

/// How long the width label shows after fewer or more tiles.
const WIDTH_LABEL_SECONDS: f64 = 1.5;
/// How long an entry flashes when its tile arrives on the shelf.
const FLASH_SECONDS: f64 = 0.9;
/// The gap between the shelf's frame and the width control, which the control's own edge spans
/// so the pointer crosses it without leaving both.
const WIDTH_CONTROL_GAP_PX: f32 = 4.0;
/// How far under the title bar's controls the layers they open hang.
const ANCHOR_GAP_PX: f32 = 4.0;

const FLASH_FADE: &[rune::Beat] = &[
    rune::Beat::new(0.0).opacity(0.0),
    rune::Beat::new(FLASH_SECONDS * 0.1)
        .opacity(1.0)
        .easing(rune::Easing::EASE_OUT),
    rune::Beat::new(FLASH_SECONDS * 0.6).opacity(1.0),
    rune::Beat::new(FLASH_SECONDS)
        .opacity(0.0)
        .easing(rune::Easing::EASE_OUT),
];

/// An entry flashing for a moment because its tile arrived on the shelf or went off screen.
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

/// The width label fewer and more tiles show for a moment.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct WidthLabel {
    pub cleared: usize,
    pub until: f64,
}

/// One column of the row as its entry shows it.
struct Entry {
    /// The column's first leaf, which names the entry.
    key: String,
    /// The leaf the entry goes to: the one this window used last.
    go: String,
    label: String,
    /// A tile in the column just arrived on the shelf: when its flash ends, as `f64` bits.
    arrival: Option<u64>,
    leaves: Vec<String>,
    drawn: ShelfEntry,
    name_px: f32,
    folded: bool,
}

impl Entry {
    fn needs_user(&self) -> bool {
        self.drawn.members().iter().any(ShelfMember::needs_user)
    }

    /// The columns on screen, and the current one wherever it stands.
    fn never_folds(&self) -> bool {
        self.drawn.place() != Place::OffScreen
    }

    fn width(&self) -> f32 {
        self.drawn.width(self.name_px)
    }

    fn least_width(&self) -> f32 {
        self.drawn.width(0.0)
    }
}

fn state_of(session: &attn_protocol::Session) -> Option<MemberState> {
    if session.agent == crate::harness::catalog::TERMINAL {
        return None;
    }
    match AgentState::parse(&session.state) {
        AgentState::Launching => None,
        AgentState::Working => Some(MemberState::Working),
        AgentState::WaitingInput => Some(MemberState::Waiting),
        AgentState::PendingApproval => Some(MemberState::Approval),
        AgentState::Scheduled => Some(MemberState::Scheduled),
        AgentState::Idle | AgentState::Recoverable => Some(MemberState::Idle),
        AgentState::Unknown => Some(MemberState::Unknown),
    }
}

fn icon_of(session: &attn_protocol::Session) -> Glyph {
    if session.agent == crate::harness::catalog::TERMINAL {
        return (&attn_ui::icon::TERMINAL).into();
    }
    match &session.delegation_role {
        Some(role) => role_icon::Mark::of(Some(role)).glyph(),
        None => (&attn_ui::icon::AGENT).into(),
    }
}

pub(super) fn member(
    desktop: &Desktop,
    facts: &attn_client::Facts,
    garden: Option<&crate::garden::Garden>,
    tree: &Node,
    unseen: &[String],
    leaf: &str,
) -> ShelfMember {
    let name = title(desktop, facts, garden, tree, leaf);
    let session = super::view::pane_of(desktop, leaf)
        .and_then(|pane| facts.sessions.iter().find(|s| s.id == pane.session_id));
    if let Some(session) = session {
        return ShelfMember {
            name,
            icon: icon_of(session),
            state: state_of(session),
        };
    }
    let icon = match tree.find_leaf(leaf) {
        Some(Node::Leaf(layout::Leaf {
            kind: LeafKind::Tile { kind, .. },
            ..
        })) => crate::sidebar::tile_icon(kind),
        _ => &attn_ui::icon::TERMINAL,
    };
    ShelfMember {
        name,
        icon: icon.into(),
        state: unseen
            .iter()
            .any(|l| l == leaf)
            .then_some(MemberState::Waiting),
    }
}

/// A tile's name on the shelf, as its header gives it.
pub(crate) fn title(
    desktop: &Desktop,
    facts: &attn_client::Facts,
    garden: Option<&crate::garden::Garden>,
    tree: &Node,
    leaf: &str,
) -> String {
    match tree.find_leaf(leaf) {
        Some(Node::Leaf(l)) => super::view::leaf_title(
            desktop,
            &facts.sessions,
            &facts.harnesses,
            &facts.tile_contents,
            facts.garden.of(&facts.profiles.selected),
            garden,
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

fn entry(
    desktop: &Desktop,
    (facts, garden): (&attn_client::Facts, Option<&crate::garden::Garden>),
    tree: &Node,
    row: &super::RowState,
    flashing: &[(&str, u64)],
    column: &Column,
    (active, on_screen): (&str, bool),
) -> Entry {
    let members: Vec<ShelfMember> = column
        .leaf_ids
        .iter()
        .map(|leaf| member(desktop, facts, garden, tree, &row.unseen, leaf))
        .collect();
    let titles: Vec<String> = members.iter().map(|m| m.name.clone()).collect();
    let place = match on_screen {
        _ if column.holds(active) => Place::Current,
        true => Place::OnScreen,
        false => Place::OffScreen,
    };
    let go = match place {
        Place::Current => active.to_owned(),
        _ => column.last_used(&row.focus_order).to_owned(),
    };
    let shown = column.leaf_ids.iter().position(|l| *l == go).unwrap_or(0);
    let asking = column
        .leaf_ids
        .iter()
        .filter(|_| !on_screen)
        .filter_map(|leaf| super::view::pane_of(desktop, leaf))
        .filter_map(|pane| facts.sessions.iter().find(|s| s.id == pane.session_id))
        .filter(|s| !crate::asks::of_session(facts, s).is_empty())
        .count();
    let drawn = ShelfEntry::new(members, shown, place, asking);
    Entry {
        key: column.first().to_owned(),
        go,
        label: label(&titles),
        arrival: flashing
            .iter()
            .find(|(leaf, _)| column.leaf_ids.iter().any(|l| l == leaf))
            .map(|(_, until)| *until),
        leaves: column.leaf_ids.clone(),
        name_px: drawn.name_width(),
        drawn,
        folded: false,
    }
}

fn folded(entries: &[Entry]) -> Vec<&Entry> {
    entries.iter().filter(|e| e.folded).collect()
}

/// The count on `side` of the columns `held` in it, with at most `marks` of the members that need the
/// user.
fn counted(side: CountSide, held: &[&Entry], marks: usize) -> Option<ShelfCount> {
    let needing: Vec<ShelfMember> = held
        .iter()
        .flat_map(|e| e.drawn.members())
        .filter(|m| m.needs_user())
        .cloned()
        .collect();
    (!held.is_empty()).then(|| ShelfCount {
        side,
        columns: held.len(),
        needs_user: !needing.is_empty(),
        marks: needing.into_iter().take(marks).collect(),
    })
}

/// The room `count` takes and the gap to its neighbour.
fn count_width(count: Option<&ShelfCount>) -> f32 {
    count.map_or(0.0, |c| c.width() + blocks::Shelf::gap())
}

/// The room `items` and the shelf's ends take, each item after the first with its gap.
fn shelf_width(items: impl Iterator<Item = f32>) -> f32 {
    items.map(|w| w + blocks::Shelf::gap()).sum::<f32>() - blocks::Shelf::gap()
        + blocks::Shelf::START
        + blocks::Shelf::END
}

/// The width each name is cut to so that all of them and the current one fit `room`: the others
/// evenly, the current one only once they have none.
fn cut_names(shown: &mut [&mut Entry], room: f32) {
    let least: f32 = shown.iter().map(|e| e.least_width()).sum();
    let gaps = blocks::Shelf::gap() * shown.len().saturating_sub(1) as f32;
    let spare = room - least - gaps;
    let wanted = |e: &Entry| e.width() - e.least_width();
    let current = |e: &Entry| e.drawn.place() == Place::Current;
    let kept: f32 = shown.iter().filter(|e| current(e)).map(|e| wanted(e)).sum();
    let mut others: Vec<f32> = shown
        .iter()
        .filter(|e| !current(e))
        .map(|e| wanted(e))
        .collect();
    others.sort_by(f32::total_cmp);
    let mut left = spare - kept;
    let mut share = f32::INFINITY;
    for (i, want) in others.iter().enumerate() {
        let even = left / (others.len() - i) as f32;
        if *want > even {
            share = even.max(0.0);
            break;
        }
        left -= want;
    }
    for e in shown.iter_mut().filter(|e| !current(e)) {
        if wanted(e) > share {
            e.name_px = e.drawn.name_within(share);
        }
    }
    let taken: f32 = shown
        .iter()
        .filter(|e| !current(e))
        .map(|e| wanted(e))
        .sum();
    for e in shown.iter_mut().filter(|e| current(e)) {
        if wanted(e) > spare - taken {
            e.name_px = e.drawn.name_within((spare - taken).max(0.0));
        }
    }
}

/// A desktop's shelf as this window lays it out: an entry per column of its row, and the entries
/// the title bar has no room for in the counts.
pub(crate) struct Shelf {
    pub desktop: String,
    /// The overview shows this desktop over the current one.
    previewed: bool,
    before: Vec<Entry>,
    screen: Vec<Entry>,
    after: Vec<Entry>,
    /// How many marks a count shows at most.
    marks: usize,
}

/// A place on the shelf a tile can be dropped beside: an entry by either half, or a count for its
/// end of the row.
pub(crate) struct Anchor {
    pub element: String,
    pub column: String,
    pub edge: Option<Edge>,
}

/// The desktop of the shelf with its row's plan, when the shelf shows: it has more than one tile,
/// the window shows the desktop, and the active tile is not in focus mode. An open overview's
/// shelf is of the desktop the overview shows.
fn shelved(state: &State) -> Option<(&Desktop, Node, layout::RowPlan)> {
    if state.window.view != crate::shell::View::Desktop {
        return None;
    }
    let desk = &state.window.desk;
    let desktop = match super::overview::open(state) {
        Some(overview) => super::overview::desktop_of(state, &overview.at().desktop)?,
        None => state.facts.current_desktop()?,
    };
    if matches!(desk.row(&desktop.id).view, View::Focused(_)) {
        return None;
    }
    let (tree, plan) = desk.plan(&state.facts, desktop)?;
    let tiles = [&plan.before, &plan.screen, &plan.after]
        .into_iter()
        .flatten()
        .map(|c| c.leaf_ids.len())
        .sum::<usize>();
    (tiles > 1).then_some((desktop, tree, plan))
}

/// The shelf's desktop and how many columns stand before its screen, on it and after it while the
/// shelf shows: what changes the room the shelf takes in the title bar.
pub(crate) fn columns(state: &State) -> Option<(String, [usize; 3])> {
    let (desktop, _, p) = shelved(state)?;
    let columns = [p.before.len(), p.screen.len(), p.after.len()];
    Some((desktop.id.clone(), columns))
}

impl Shelf {
    /// The shelf of the current desktop, or of the one an open overview shows. None when that
    /// desktop has one tile, the window shows Home, or the active tile is in focus mode.
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
        let active = super::overview::open(state)
            .and_then(|o| o.at().leaf.as_deref())
            .unwrap_or(&desktop.active_pane_id);
        let entries = |columns: &[Column], on_screen: bool| -> Vec<Entry> {
            let stands = (active, on_screen);
            columns
                .iter()
                .map(|c| {
                    entry(
                        desktop,
                        (facts, Some(&state.window.garden)),
                        &tree,
                        &row_state,
                        &flashing,
                        c,
                        stands,
                    )
                })
                .collect()
        };
        let (before, screen, after) = (
            entries(&plan.before, false),
            entries(&plan.screen, true),
            entries(&plan.after, false),
        );
        let mut shelf = Shelf {
            desktop: desktop.id.clone(),
            previewed: facts.current_desktop().is_none_or(|d| d.id != desktop.id),
            before,
            screen,
            after,
            marks: usize::MAX,
        };
        shelf.fit(desk.shelf_px.unwrap_or(f32::INFINITY));
        Some(shelf)
    }

    /// The width of the entries that never fold without their names and of both counts without
    /// marks, under which the shelf overflows its slot.
    fn least_px(&self) -> f32 {
        let bare = |side: CountSide, columns: usize| {
            (columns > 0).then(|| ShelfCount {
                side,
                columns,
                needs_user: true,
                marks: vec![],
            })
        };
        let least = shelf_width(
            self.before
                .iter()
                .chain(&self.screen)
                .chain(&self.after)
                .filter(|e| e.never_folds())
                .map(Entry::least_width),
        ) + count_width(bare(CountSide::Before, self.before.len()).as_ref())
            + count_width(bare(CountSide::After, self.after.len()).as_ref());
        least.ceil()
    }

    /// Folds the entries off screen that `room` cannot hold, the farthest from the screen first
    /// and the ones that need the user last. A row that still does not fit gives up the counts'
    /// marks, then the names on screen.
    fn fit(&mut self, room: f32) {
        while self.width() > room {
            let Some(next) = self.next_to_fold() else {
                break;
            };
            next.folded = true;
        }
        self.marks = [CountSide::Before, CountSide::After]
            .into_iter()
            .filter_map(|side| self.count(side))
            .map(|count| count.marks.len())
            .max()
            .unwrap_or(0);
        while self.marks > 0 && self.width() > room {
            self.marks -= 1;
        }
        let over = self.width() - room;
        if over > 0.0 {
            let mut kept: Vec<&mut Entry> = self
                .before
                .iter_mut()
                .chain(&mut self.screen)
                .chain(&mut self.after)
                .filter(|e| e.never_folds())
                .collect();
            let shown = shelf_width(kept.iter().map(|e| e.width()));
            let ends = blocks::Shelf::START + blocks::Shelf::END;
            cut_names(&mut kept, shown - ends - over);
        }
    }

    fn count(&self, side: CountSide) -> Option<ShelfCount> {
        counted(side, &self.folded(side), self.marks)
    }

    fn next_to_fold(&mut self) -> Option<&mut Entry> {
        let distance = self.before.len();
        let before = self
            .before
            .iter_mut()
            .enumerate()
            .map(|(i, e)| (distance - i, e));
        let after = self.after.iter_mut().enumerate().map(|(i, e)| (i + 1, e));
        before
            .chain(after)
            .filter(|(_, e)| !e.folded && !e.never_folds())
            .max_by(|(a, x), (b, y)| {
                (!x.needs_user())
                    .cmp(&!y.needs_user())
                    .then(a.cmp(b))
                    .then(x.width().total_cmp(&y.width()))
            })
            .map(|(_, e)| e)
    }

    fn shown(&self) -> impl Iterator<Item = &Entry> {
        self.before
            .iter()
            .chain(&self.screen)
            .chain(&self.after)
            .filter(|e| !e.folded)
    }

    fn width(&self) -> f32 {
        shelf_width(self.shown().map(Entry::width))
            + count_width(self.count(CountSide::Before).as_ref())
            + count_width(self.count(CountSide::After).as_ref())
    }

    /// Where the frame starts and how wide it stands: around the entries on screen.
    fn framed(&self) -> (f32, f32) {
        let gap = blocks::Shelf::gap();
        let start = blocks::Shelf::START
            + count_width(self.count(CountSide::Before).as_ref())
            + self
                .before
                .iter()
                .filter(|e| !e.folded)
                .map(|e| e.width() + gap)
                .sum::<f32>();
        let width = self.screen.iter().map(|e| e.width() + gap).sum::<f32>() - gap;
        let reach = blocks::Shelf::FRAME_REACH;
        (start - reach, width + 2.0 * reach)
    }

    fn folded(&self, side: CountSide) -> Vec<&Entry> {
        folded(match side {
            CountSide::Before => &self.before,
            CountSide::After => &self.after,
        })
    }

    /// Whether `leaf` is off screen, by its entry or in a count.
    pub(crate) fn holds(&self, leaf: &str) -> bool {
        self.before
            .iter()
            .chain(&self.after)
            .any(|c| c.leaves.iter().any(|l| l == leaf))
    }

    /// What dragging the entry of `column` picks up.
    pub(crate) fn entry_source(&self, column: &str) -> Option<Source> {
        let entry = self
            .before
            .iter()
            .chain(&self.after)
            .find(|e| e.key == column)?;
        Some(Source::Entry {
            desktop: self.desktop.clone(),
            column: entry.key.clone(),
            leaf: entry.go.clone(),
            label: entry.label.clone(),
        })
    }

    /// Where a dragged tile can land on the shelf, in the order the shelf shows them.
    pub(crate) fn anchors(&self) -> Vec<Anchor> {
        let entry = |e: &Entry| Anchor {
            element: entry_key(&e.key),
            column: e.key.clone(),
            edge: None,
        };
        let count = |side: CountSide, outermost: Option<&Entry>| {
            let outermost = outermost.filter(|_| !self.folded(side).is_empty())?;
            Some(Anchor {
                element: count_key(side).to_owned(),
                column: outermost.key.clone(),
                edge: Some(edge_of(side)),
            })
        };
        let kept = |entries: &[Entry]| -> Vec<Anchor> {
            entries.iter().filter(|e| !e.folded).map(entry).collect()
        };
        count(CountSide::Before, self.before.first())
            .into_iter()
            .chain(kept(&self.before))
            .chain(kept(&self.after))
            .chain(count(CountSide::After, self.after.last()))
            .collect()
    }
}

fn entry_key(column: &str) -> String {
    format!("shelf.entry.{column}")
}

fn count_key(side: CountSide) -> &'static str {
    match side {
        CountSide::Before => "shelf.count.before",
        CountSide::After => "shelf.count.after",
    }
}

fn edge_of(side: CountSide) -> Edge {
    match side {
        CountSide::Before => Edge::Left,
        CountSide::After => Edge::Right,
    }
}

fn view(state: &State, shelf: Option<Shelf>, t: &Tokens) -> Option<El> {
    let Some(shelf) = shelf else {
        return holding(state, Owner::Shelf).then(owner);
    };
    let desk = &state.window.desk;
    let id = &shelf.desktop;
    let (x, width) = shelf.framed();
    let frame = blocks::Shelf::frame(x, width, t)
        .on_pointer_move_local(|state: &mut State, _, tx: &mut Tx| {
            let hover = &mut state.window.desk.width_control;
            if hover.frame || tx.pointer_held() {
                tx.live_only();
                return;
            }
            hover.frame = true;
        })
        .on_hover_change(|state: &mut State, inside, _: &mut Tx| {
            if !inside {
                state.window.desk.width_control.frame = false;
            }
        });
    let drop = desk.drag.as_ref().and_then(|d| d.shelf_mark());
    let marked = |key: &str| drop.filter(|(marked, _)| *marked == key).map(|(_, e)| e);
    let count = |side: CountSide| {
        let marked = marked(count_key(side));
        shelf.count(side).map(|c| count(&c, marked, t))
    };
    let entries = shelf.shown().map(|e| match e.drawn.place() {
        Place::OffScreen => {
            let drop = marked(&entry_key(&e.key));
            off_screen(e, id, shelf.previewed, drop, t)
        }
        Place::OnScreen => on_screen(e, id, t),
        Place::Current => current(e, t),
    });
    Some(
        owner()
            .role("toolbar")
            .label("Shelf")
            .on_layout_tx(|state: &mut State, rect, tx: &mut Tx| record(state, "shelf", rect, tx))
            .child(frame)
            .child(blocks::Shelf::frame_edge(x, width, t))
            .children(count(CountSide::Before))
            .children(entries)
            .children(count(CountSide::After)),
    )
}

/// The shelf's row, which runs its entries' drags.
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

/// The title bar's middle, which the shelf sits in and measures for its entries. It keeps room
/// for the entries on screen and the counts, so what follows it moves aside.
pub(crate) fn slot(state: &State, t: &Tokens) -> El {
    let shelf = Shelf::of(state);
    row()
        .key("shelf.slot")
        .grow(1.0)
        .min_w(shelf.as_ref().map_or(0.0, |s| s.least_px()))
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

fn said(entry: &Entry) -> String {
    let place = match entry.drawn.place() {
        Place::OffScreen => None,
        Place::OnScreen => Some("on screen"),
        Place::Current => Some("current"),
    };
    place
        .into_iter()
        .chain(entry.needs_user().then_some("needs you"))
        .chain(entry.arrival.map(|_| "arrived"))
        .collect::<Vec<_>>()
        .join(", ")
}

fn drawn(entry: &Entry, t: &Tokens) -> El {
    let key = entry_key(&entry.key);
    let flash = entry.arrival.map(|until| flash_ring(&key, until, t));
    entry
        .drawn
        .build(&key, &entry.label, entry.name_px, t)
        .a11y_value(said(entry))
        .children(flash)
}

/// The entry of a column on screen, which shows its tile.
fn on_screen(entry: &Entry, desktop: &str, t: &Tokens) -> El {
    let (desktop, leaf) = (desktop.to_owned(), entry.go.clone());
    drawn(entry, t).on_press_tx(move |state: &mut State, tx: &mut Tx| {
        crate::sidebar::open_tile(state, &desktop, &leaf, tx)
    })
}

/// The current entry, which opens the overview and closes an open one.
fn current(entry: &Entry, t: &Tokens) -> El {
    drawn(entry, t).on_press_tx(super::overview::toggle)
}

/// The entry of a column off screen. The pointer resting on it peeks at the tile it goes to, or
/// at the one whose dot it rests on, unless its desktop is previewed.
fn off_screen(entry: &Entry, desktop: &str, previewed: bool, drop: Option<Edge>, t: &Tokens) -> El {
    let recorded = entry_key(&entry.key);
    let drawn = drawn(entry, t).on_layout_tx(move |state: &mut State, rect, tx: &mut Tx| {
        record(state, &recorded, rect, tx)
    });
    if previewed {
        return drop_bar(drawn, drop, t);
    }
    let stack = (entry.drawn.clone(), entry.leaves.clone());
    let drawn = peeks(drawn, entry, desktop, move |p: PointerEvent| {
        let (drawn, leaves) = &stack;
        drawn.dot_at(p.x, p.y).map(|i| leaves[i].clone())
    });
    drop_bar(drawn, drop, t)
}

/// `el` peeking at `entry`'s column while the pointer rests on it: at the tile `pointed` finds under
/// the pointer, else at the one the entry goes to.
fn peeks(
    el: El,
    entry: &Entry,
    desktop: &str,
    pointed: impl Fn(PointerEvent) -> Option<String> + 'static,
) -> El {
    let moved = (desktop.to_owned(), entry.go.clone(), entry.key.clone());
    let left = entry.key.clone();
    el.on_hover_change(move |state: &mut State, inside, tx: &mut Tx| {
        if !inside {
            super::peek::leave_entry(state, &left, tx);
        }
    })
    .on_pointer_move_local(move |state: &mut State, p, tx: &mut Tx| {
        let (desktop, leaf, column) = &moved;
        match pointed(p) {
            Some(pointed) => super::peek::hover_member(state, desktop, &pointed, column, tx),
            None => super::peek::hover_entry(state, desktop, leaf, column, tx),
        }
    })
}

/// The accent ring an entry shows while its tile's arrival flashes, held and then fading; keyed
/// by when the flash ends, so a new flash plays from the start.
fn flash_ring(entry_key: &str, until: u64, t: &Tokens) -> El {
    blocks::Shelf::flash(format!("{entry_key}.flash.{until}"), t)
        .transition(rune::Transition::none())
        .sequence(rune::Sequence::new(FLASH_FADE))
}

/// The accent bar on the side of an entry or count a dragged tile would land on.
fn drop_bar(el: El, drop: Option<Edge>, t: &Tokens) -> El {
    match drop {
        None => el,
        Some(Edge::Left) => el.border_sides([0.0, 0.0, 0.0, 2.0], t.accent),
        Some(Edge::Right) => el.border_sides([0.0, 2.0, 0.0, 0.0], t.accent),
    }
}

/// A count, which opens the overview.
fn count(count: &ShelfCount, drop: Option<Edge>, t: &Tokens) -> El {
    let side = count.side;
    let key = count_key(side);
    let state = if count.needs_user { "needs you" } else { "" };
    let label = format!("{} more this way", count.columns);
    let count = count
        .build(key, label, t)
        .on_press_tx(super::overview::show)
        .on_layout_tx(move |state: &mut State, rect, tx: &mut Tx| record(state, key, rect, tx))
        .a11y_value(state);
    drop_bar(count, drop, t)
}

/// Where the pointer moved to, with no button held, that shows the width control: over the
/// shelf's frame, where a frame that comes under a still pointer does not count, or over the
/// control.
#[derive(Default)]
pub(crate) struct WidthControlHover {
    frame: bool,
    control: bool,
}

/// The width control: under the shelf's frame while the pointer is over the frame or over it,
/// drawn over the tiles so the entries never move. Under a frame that leads the shelf it starts
/// at the frame's left edge.
pub(crate) fn width_control_layer(state: &State, t: &Tokens) -> Option<El> {
    let hover = &state.window.desk.width_control;
    if !(hover.frame || hover.control) {
        return None;
    }
    let shelf = Shelf::of(state).filter(|s| !s.previewed)?;
    let leading = shelf.before.is_empty();
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
                "shelf.frame",
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
    if let Some(swap) = row.swap.take_if(|s| s.entry == leaf || s.slot == leaf) {
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

/// Flashes the entries of `leaves` on desktop `desktop` for a moment.
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
