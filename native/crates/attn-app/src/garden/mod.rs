mod artifacts;
mod authorization;
mod board;
mod checks;
mod continuation;
mod handover;
pub(crate) mod model;
mod reading;
pub(crate) mod search;
mod sheet;
mod spot;
mod tile;
pub(crate) mod v2;
mod view;

use std::collections::{HashMap, HashSet};
use std::hash::{Hash, Hasher};
use std::rc::Rc;
use std::sync::Arc;

use attn_protocol::{
    FsExistsMessage, FsExistsResultMessage, GardenSearchHit, GardenSearchMessage,
    GardenSearchRange, GardenSearchResultMessage, GardenSeedsPageMessage,
    GardenSeedsPageResultMessage, OpenSeedMessage, OpenSeedResultMessage, Seed, SeedDocument,
    SeedDocumentGetMessage, SeedDocumentGetResultMessage,
};
use rune::{El, Key, KeyEvent, TextEdit, Tx, col};

use crate::State;
use crate::action::Action;
use crate::keymap::{Focus, Press};
use crate::requests::Refused;
use crate::shell::Drawer;

pub(crate) use reading::{home_of, reads_in, tile_doc, tile_view};
pub(crate) use tile::{Reads as TileReads, body as tile_body, climb as climb_tile};
pub(crate) use v2::overlay;
pub(crate) use view::{frame_layer, panel};

/// How many times its size the garden draws inside a window the app's text size already
/// zooms: its own size over the app's.
pub(crate) fn zoom(state: &State) -> f32 {
    let settings = &state.facts.settings;
    crate::settings::garden_scale(settings) / crate::settings::text_scale(settings)
}

/// `content` at `zoom` times its size: laid out in a `1 / zoom` share of the box it fills and
/// drawn from the box's top-left corner, so it fills the box again.
pub(crate) fn zoomed(zoom: f32, content: El) -> El {
    col()
        .w_frac(zoom.recip())
        .h_frac(zoom.recip())
        .origin(0.0, 0.0)
        .scale(zoom)
        .child(content)
}

/// How many garden rows this thread has built, memo misses only: a row whose seed and look did
/// not change is reused and not counted.
pub fn rows_built() -> usize {
    view::ROWS_BUILT.with(std::cell::Cell::get)
}

const FRAME_PREF: &str = "attn.garden.frame";
const VIEW_PREF: &str = "attn.garden.fullscreenView";
pub(crate) const SEARCH_FIELD: &str = "garden.search.field";
pub(crate) const BODY: &str = "garden.body";

/// Where the garden shows: closed, in the dock beside the work, or holding the window.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Mode {
    Closed,
    Dock,
    Full,
}

/// A check whose box takes the keyboard when its seed's page next lays out on `sheet`, as a
/// shared check's row goes to an inheriting seed.
pub(super) struct CheckFocus {
    pub(super) sheet: String,
    pub(super) seed: String,
    pub(super) check_box: String,
}

pub(super) const PAGE_LIMIT: i64 = 50;
const MAX_PAGE_LIMIT: i64 = 200;

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(super) struct PageKey {
    pub(super) profile: String,
    pub(super) bed: String,
    pub(super) plot_id: String,
    pub(super) status: String,
}

impl PageKey {
    pub(super) fn root(state: &State) -> Self {
        Self {
            profile: state.facts.profiles.selected.clone(),
            bed: if v2::enabled(state) {
                v2::bed_scope(&v2::active_bed(state))
            } else {
                String::new()
            },
            plot_id: String::new(),
            status: "closed".to_owned(),
        }
    }

    pub(super) fn plot(state: &State, plot_id: &str) -> Self {
        Self {
            profile: state.facts.profiles.selected.clone(),
            bed: String::new(),
            plot_id: plot_id.to_owned(),
            status: "closed".to_owned(),
        }
    }
}

#[derive(Default)]
pub(super) struct PageBucket {
    pub(super) asked_revision: u64,
    pub(super) answered: bool,
    pub(super) seeds: Vec<Arc<Seed>>,
    pub(super) next_before: Option<String>,
    pub(super) harvested: i64,
    pub(super) withered: i64,
    pub(super) loading: bool,
    pub(super) loading_more: bool,
    pub(super) request_id: Option<String>,
    pub(super) error: Option<String>,
    pub(super) retry_before: Option<String>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub(super) struct SearchKey {
    pub(super) profile: String,
    pub(super) terms: Vec<String>,
    pub(super) is: Vec<String>,
    pub(super) tenders: Vec<String>,
    pub(super) beds: Vec<String>,
    pub(super) bed_scope: String,
    pub(super) plot_id: String,
    pub(super) wide: bool,
}

#[derive(Clone, Debug)]
pub(super) struct SearchHit {
    pub(super) seed: Arc<Seed>,
    pub(super) where_: String,
    pub(super) id_hit: bool,
    pub(super) title_ranges: Vec<search::Range>,
    pub(super) snippet: Option<search::Snippet>,
    pub(super) plot_title: Option<String>,
}

#[derive(Default)]
pub(super) struct SearchState {
    pub(super) key: Option<SearchKey>,
    pub(super) asked_ptr: usize,
    pub(super) asked_sessions: u64,
    pub(super) hits: Rc<[SearchHit]>,
    pub(super) next_offset: Option<i64>,
    pub(super) hidden_closed: i64,
    pub(super) outside: i64,
    pub(super) in_plot: i64,
    pub(super) loading: bool,
    pub(super) loading_more: bool,
    pub(super) request_id: Option<String>,
    pub(super) error: Option<String>,
    pub(super) retry_offset: i64,
}

/// The window's garden: its frame and the walk through it. The seeds are the daemon's.
#[derive(Default)]
pub(crate) struct Garden {
    /// The garden the terminals last marked seed ids from, by its address, and the profile it
    /// was read for.
    marked: (usize, String),
    /// Each outpost's reach as the reads last followed it, and how often it has changed.
    reach: (Vec<(String, String)>, usize),
    /// The garden holds the window, over the dock.
    full: bool,
    /// The frame the garden opens in, as the user last chose it; read from prefs at first use.
    last: Option<Mode>,
    /// Fullscreen shows the board instead of the list; read from prefs at first use.
    board: Option<bool>,
    /// The plots walked into from the root, then the seed opened, by id.
    pub(crate) trail: Vec<String>,
    pub(crate) query: TextEdit,
    /// The plot the search stood in when it widened to the whole garden.
    pub(crate) wide_in: Option<String>,
    /// The answer the arrows reached, for the question it answers.
    pub(crate) walked: (String, usize),
    /// The folded trail is open.
    pub(crate) trail_open: bool,
    /// Each shown seed's document, by seed.
    readings: HashMap<String, Reading>,
    /// Rises with every change a seed page shows besides the seeds: a reading's answer, a
    /// check's move, a fold opened.
    pub(crate) revision: u64,
    /// What each part of a seed page is doing, and the words being written for it.
    edits: HashMap<spot::Spot, spot::Edit>,
    /// The folds open on seed pages, by the key of the control that opens each.
    folds: HashSet<String>,
    pub(super) check_focus: Option<CheckFocus>,
    pub(crate) board_walk: board::Board,
    last_profile: String,
    pub(crate) bed: v2::BedFilter,
    pub(crate) lens: v2::Lens,
    pub(crate) bed_form: Option<v2::BedForm>,
    pub(crate) bed_picker: Option<v2::BedPicker>,
    pub(super) pages: HashMap<PageKey, PageBucket>,
    pub(super) search: SearchState,
    pending_open: Option<String>,
}

/// A garden push, by its address, and the count of outpost reach changes, which a seed's
/// document reads again on.
type Followed = (usize, usize);

/// A seed's document as the daemon last answered it.
#[derive(Default)]
struct Reading {
    /// The garden push and outpost reach the latest read followed, and its request.
    asked: Option<(Followed, String)>,
    document: Option<Arc<SeedDocument>>,
    seed: Option<Arc<Seed>>,
    children: Vec<Arc<Seed>>,
    ancestors: Option<Vec<Arc<Seed>>>,
    related: Option<Vec<Arc<Seed>>>,
    /// A read since the latest garden push answered, so the document describes that garden.
    answered: bool,
    error: Option<String>,
    /// The linked files asked about, and those the daemon says are not on disk.
    looked_for: Vec<String>,
    missing: HashSet<String>,
}

pub(crate) fn mode(state: &State) -> Mode {
    if !state.window.shows(Drawer::Garden) {
        Mode::Closed
    } else if state.window.garden.full {
        Mode::Full
    } else {
        Mode::Dock
    }
}

/// The seeds of the selected profile's garden, newest first.
pub(crate) fn seeds(state: &State) -> &[Arc<Seed>] {
    state.facts.garden.of(&state.facts.profiles.selected)
}

fn last_frame(state: &State) -> Mode {
    state.window.garden.last.unwrap_or_else(|| {
        match crate::frame::pref_string(FRAME_PREF).as_deref() {
            Some("full") => Mode::Full,
            _ => Mode::Dock,
        }
    })
}

pub(crate) fn shows_board(state: &State) -> bool {
    mode(state) == Mode::Full
        && state
            .window
            .garden
            .board
            .unwrap_or_else(|| crate::frame::pref_string(VIEW_PREF).as_deref() == Some("board"))
}

pub(crate) fn choose_board(state: &mut State, board: bool, tx: &mut Tx) {
    state.window.garden.board = Some(board);
    crate::frame::set_pref_string(VIEW_PREF, if board { "board" } else { "list" });
    focus_frame(state, tx);
    follow(state, tx);
}

fn focus_frame(state: &State, tx: &mut Tx) {
    tx.focus(
        if shows_board(state) {
            board::BOARD
        } else {
            BODY
        },
        false,
    );
}

fn show(state: &mut State, frame: Mode, tx: &mut Tx) {
    let was = mode(state);
    let window = &mut state.window;
    window.garden.full = frame == Mode::Full;
    window.open_drawer(Drawer::Garden);
    if was != frame {
        focus_frame(state, tx);
    }
    follow(state, tx);
}

/// ⌘⇧T and the palette: opens the garden in the frame it last had, or moves an open garden
/// between the dock and the window and remembers that frame.
pub(crate) fn toggle_frame(state: &mut State, tx: &mut Tx) {
    let next = match mode(state) {
        Mode::Closed => last_frame(state),
        Mode::Dock => Mode::Full,
        Mode::Full => Mode::Dock,
    };
    if mode(state) != Mode::Closed {
        state.window.garden.last = Some(next);
        let value = if next == Mode::Full { "full" } else { "dock" };
        crate::frame::set_pref_string(FRAME_PREF, value);
    }
    show(state, next, tx);
}

/// The sidebar icon: opens the garden in its last frame, or closes it.
pub(crate) fn toggle_from_icon(state: &mut State, tx: &mut Tx) {
    match mode(state) {
        Mode::Closed => {
            let frame = last_frame(state);
            show(state, frame, tx);
        }
        _ => close(state),
    }
}

pub(crate) fn close(state: &mut State) {
    let window = &mut state.window;
    window.garden.full = false;
    window.garden.bed_form = None;
    window.garden.bed_picker = None;
    window.garden.pages.clear();
    window.garden.search = SearchState::default();
    window.close_drawer(Drawer::Garden);
}

pub(super) fn lookup_seed(state: &State, id: &str) -> Option<Arc<Seed>> {
    if let Some(seed) = seeds(state).iter().find(|s| s.id == id) {
        return Some(seed.clone());
    }
    let garden = &state.window.garden;
    if let Some(seed) = garden.readings.get(id).and_then(|r| r.seed.as_ref()) {
        return Some(seed.clone());
    }
    for reading in garden.readings.values() {
        if let Some(seed) = reading
            .ancestors
            .iter()
            .flatten()
            .chain(reading.children.iter())
            .chain(reading.related.iter().flatten())
            .find(|s| s.id == id)
        {
            return Some(seed.clone());
        }
    }
    for bucket in garden.pages.values() {
        if let Some(seed) = bucket.seeds.iter().find(|s| s.id == id) {
            return Some(seed.clone());
        }
    }
    garden
        .search
        .hits
        .iter()
        .find(|hit| hit.seed.id == id)
        .map(|hit| hit.seed.clone())
}

pub(crate) fn living_trail(state: &State) -> Vec<String> {
    state.window.garden.trail.clone()
}

/// The seeds on screen: the one the garden opened and each seed tile's.
fn shown_seeds(state: &State) -> HashSet<String> {
    let mut shown: HashSet<String> = state
        .facts
        .desktops
        .iter()
        .flat_map(|d| d.leaves())
        .filter_map(|leaf| match leaf.tile() {
            Some(("seed", params)) if !params.is_empty() => Some(params.to_owned()),
            _ => None,
        })
        .collect();
    if mode(state) != Mode::Closed {
        shown.extend(living_trail(state).pop());
    }
    shown.extend(crate::desktop::seed_chip::told(state));
    if let Some(crate::terminal::Subject::Seed(id)) = state
        .window
        .desk
        .header_pop
        .marking()
        .map(|mark| mark.target())
        && !seeds(state).iter().any(|seed| seed.id == *id)
    {
        shown.insert(id.clone());
    }
    shown
}

/// Reads each shown seed's document whenever the seed or the garden around it changes, or an
/// outpost's reach does. Only a seed's latest read counts.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    let selected_profile = state.facts.profiles.selected.clone();
    if state.window.garden.last_profile != selected_profile {
        if !state.window.garden.last_profile.is_empty() {
            let garden = &mut state.window.garden;
            garden.bed = v2::BedFilter::All;
            garden.bed_form = None;
            garden.bed_picker = None;
            garden.trail.clear();
            garden.wide_in = None;
            garden.pending_open = None;
            garden.pages.clear();
            garden.search = SearchState::default();
            garden.board_walk.clear_walk();
        }
        state.window.garden.last_profile = selected_profile;
    }
    if !v2::enabled(state) {
        state.window.garden.bed_form = None;
        state.window.garden.bed_picker = None;
    }
    mark_in_terminals(state);
    let shown = shown_seeds(state);
    let reach: Vec<_> = state
        .facts
        .outposts
        .iter()
        .map(|o| (o.id.clone(), o.reach.clone()))
        .collect();
    let followed = &mut state.window.garden.reach;
    if followed.0 != reach {
        *followed = (reach, followed.1 + 1);
    }
    let push = (
        Arc::as_ptr(&state.facts.garden) as usize,
        state.window.garden.reach.1,
    );
    let readings = &mut state.window.garden.readings;
    let before = readings.len();
    readings.retain(|id, _| shown.contains(id));
    if readings.len() != before {
        state.window.garden.revision += 1;
    }
    for id in shown {
        if state
            .window
            .garden
            .readings
            .get(&id)
            .is_some_and(|r| r.asked.as_ref().is_some_and(|(at, _)| *at == push))
        {
            continue;
        }
        read(state, id, push, tx);
    }
    sync_pages(state, tx);
    sync_search(state, tx);
}

fn mark_in_terminals(state: &mut State) {
    let marking = (
        Arc::as_ptr(&state.facts.garden) as usize,
        state.facts.profiles.selected.clone(),
    );
    if state.window.garden.marked == marking {
        return;
    }
    state.window.garden.marked = marking;
    let known = state
        .facts
        .garden
        .of(&state.facts.profiles.selected)
        .iter()
        .map(|seed| seed.id.clone())
        .collect();
    state.terminals.know_seeds(known);
}

fn read(state: &mut State, id: String, push: Followed, tx: &mut Tx) {
    let request_id = state.next_request_id();
    let reading = state.window.garden.readings.entry(id.clone()).or_default();
    let new_push = reading.asked.as_ref().is_none_or(|(at, _)| at.0 != push.0);
    reading.asked = Some((push, request_id.clone()));
    if new_push && std::mem::take(&mut reading.answered) {
        state.window.garden.revision += 1;
    }
    let command = SeedDocumentGetMessage {
        seed_id: id.clone(),
        request_id: request_id.clone(),
        ..SeedDocumentGetMessage::new()
    };
    state.ask_as(
        request_id.clone(),
        &command,
        move |state, answer, tx| {
            let garden = &mut state.window.garden;
            let Some(reading) = garden.readings.get_mut(&id).filter(|r| {
                r.asked
                    .as_ref()
                    .is_some_and(|(_, asked)| *asked == request_id)
            }) else {
                return;
            };
            let mut shrank_trail = false;
            match answer.parse::<SeedDocumentGetResultMessage>() {
                Some(reply) if reply.success => {
                    let doc = reply.document.map(Arc::new);
                    let linked = linked_files(doc.as_deref());
                    let linked_changed = linked != reading.looked_for;
                    reading.document = doc.clone();
                    reading.seed = doc.as_deref().map(|d| Arc::new(d.seed.clone()));
                    reading.children = doc
                        .as_deref()
                        .map(|d| d.children.iter().cloned().map(Arc::new).collect())
                        .unwrap_or_default();
                    reading.ancestors = doc
                        .as_deref()
                        .and_then(|d| d.ancestors.as_ref())
                        .map(|list| list.iter().cloned().map(Arc::new).collect());
                    reading.related = doc
                        .as_deref()
                        .and_then(|d| d.related.as_ref())
                        .map(|list| list.iter().cloned().map(Arc::new).collect());
                    reading.answered = true;
                    reading.error = None;
                    if linked_changed {
                        reading.looked_for = linked.clone();
                        reading.missing.clear();
                    }
                    let mut trail_changed = false;
                    if let Some(doc) = doc.as_deref() {
                        if garden.trail.len() == 1
                            && garden.trail[0] == id
                            && let Some(ancestors) = &doc.ancestors
                            && !ancestors.is_empty()
                        {
                            garden.trail = ancestors
                                .iter()
                                .map(|a| a.id.clone())
                                .chain(std::iter::once(id.clone()))
                                .collect();
                            trail_changed = true;
                        }
                        if garden.pending_open.as_deref() == Some(id.as_str()) {
                            garden.pending_open = None;
                            let root = doc
                                .ancestors
                                .as_deref()
                                .and_then(|a| a.first())
                                .unwrap_or(&doc.seed)
                                .clone();
                            align_v2_to_root(state, &root);
                            trail_changed = true;
                        }
                    }
                    state.window.garden.revision += 1;
                    if trail_changed {
                        follow(state, tx);
                    }
                    if linked_changed {
                        look_for(state, &id, linked, tx);
                        return;
                    }
                    return;
                }
                Some(reply) => {
                    let in_live = seeds(state).iter().any(|s| s.id == id);
                    let garden = &mut state.window.garden;
                    if let Some(reading) = garden.readings.get_mut(&id) {
                        if !in_live {
                            reading.document = None;
                            reading.seed = None;
                            reading.children.clear();
                            reading.ancestors = None;
                            reading.related = None;
                        }
                        reading.error = Some(
                            reply
                                .error
                                .unwrap_or_else(|| format!("Could not read {id}")),
                        );
                    }
                    if !in_live && garden.trail.last().map(String::as_str) == Some(id.as_str()) {
                        garden.trail.pop();
                        shrank_trail = true;
                    }
                    if garden.pending_open.as_deref() == Some(id.as_str()) {
                        garden.pending_open = None;
                    }
                }
                None => {
                    reading.error = Some(format!("Could not read {id}"));
                }
            }
            state.window.garden.revision += 1;
            if shrank_trail {
                follow(state, tx);
            }
        },
        tx,
    );
}

fn align_v2_to_root(state: &mut State, root: &Seed) {
    if !v2::enabled(state) {
        return;
    }
    let current_bed = v2::active_bed(state);
    if !v2::matches_bed(root, &current_bed) {
        state.window.garden.bed = v2::filter_for_root(root);
    }
    if state.window.garden.lens != v2::Lens::All {
        let index = view::index(state);
        let att = v2::AttentionIndex::of(&state.facts).attention(root, &index);
        let shown_in_lens = match state.window.garden.lens {
            v2::Lens::Now => att.is_now(),
            v2::Lens::Triage => {
                matches!(att, v2::Attention::Quiet | v2::Attention::Parked)
                    || (!v2::beds(state).is_empty()
                        && !v2::is_automation(root)
                        && root.bed_id.as_deref().unwrap_or("").is_empty())
            }
            v2::Lens::All => true,
        };
        if !shown_in_lens || model::is_closed(root) {
            state.window.garden.lens = v2::Lens::All;
        }
    }
}

fn wanted_pages(state: &State) -> Vec<PageKey> {
    if mode(state) == Mode::Closed || state.facts.profiles.selected.is_empty() {
        return Vec::new();
    }
    if shows_board(state) {
        let expanded = state.window.garden.board_walk.closed_open;
        return match board::current_plot(state) {
            Some(plot_id) if expanded || history_is_capped(state) => {
                vec![PageKey::plot(state, &plot_id)]
            }
            None if expanded => vec![PageKey::root(state)],
            _ => Vec::new(),
        };
    }
    let query = search::parse(&state.window.garden.query.text, v2::enabled(state));
    if query.searches || !(query.is.contains(&"closed") || query.is.contains(&"any")) {
        return Vec::new();
    }
    let mut keys = Vec::new();
    let trail = living_trail(state);
    let wide = view::columns(state);
    if trail.is_empty() || wide {
        keys.push(PageKey::root(state));
    }
    if wide && !v2::enabled(state) {
        for id in &trail {
            if lookup_seed(state, id).is_some_and(|s| model::is_plot(&s)) {
                let key = PageKey::plot(state, id);
                if !keys.contains(&key) {
                    keys.push(key);
                }
            }
        }
    } else if let Some(last) = trail.last()
        && lookup_seed(state, last).is_some_and(|s| model::is_plot(&s))
    {
        keys.push(PageKey::plot(state, last));
    }
    keys
}

pub(super) fn history_is_capped(state: &State) -> bool {
    state.facts.garden.total_of(&state.facts.profiles.selected) > seeds(state).len() as i64
}

fn sync_pages(state: &mut State, tx: &mut Tx) {
    if mode(state) == Mode::Closed {
        if !state.window.garden.pages.is_empty() {
            state.window.garden.pages.clear();
        }
        return;
    }
    let profile = state.facts.profiles.selected.clone();
    let wanted = wanted_pages(state);
    let revision = state.facts.garden.revision;
    let keep_cached = state.window.garden.pages.len() <= 4;
    state.window.garden.pages.retain(|k, b| {
        k.profile == profile
            && (wanted.contains(k) || (keep_cached && b.asked_revision == revision))
    });
    for key in wanted {
        if state
            .window
            .garden
            .pages
            .get(&key)
            .is_none_or(|bucket| !bucket.loading && bucket.asked_revision != revision)
        {
            refresh_page(state, key, tx);
        }
    }
}

pub(super) fn load_more_page(state: &mut State, key: PageKey, tx: &mut Tx) -> bool {
    let cursor = state.window.garden.pages.get(&key).and_then(|bucket| {
        if bucket.loading || bucket.loading_more || bucket.error.is_some() {
            return None;
        }
        bucket.next_before.clone()
    });
    let Some(cursor) = cursor else {
        return false;
    };
    fetch_page(state, key, Page::More(cursor), tx);
    true
}

pub(super) fn retry_page(state: &mut State, key: PageKey, tx: &mut Tx) {
    let cursor = state
        .window
        .garden
        .pages
        .get(&key)
        .and_then(|bucket| bucket.retry_before.clone());
    if let Some(cursor) = cursor {
        fetch_page(state, key, Page::More(cursor), tx);
    } else {
        refresh_page(state, key, tx);
    }
}

struct PageRefresh {
    before: Option<String>,
    depth: usize,
    through: Option<Arc<Seed>>,
    seeds: Vec<Arc<Seed>>,
}

impl PageRefresh {
    fn add(&mut self, mut seeds: Vec<Seed>) -> bool {
        let mut take = self.depth.saturating_sub(self.seeds.len());
        if let Some(boundary) = &self.through {
            let reached = seeds
                .iter()
                .position(|seed| seed.id == boundary.id)
                .or_else(|| {
                    seeds
                        .last()
                        .filter(|last| older_archive_row(last, boundary))
                        .and_then(|_| {
                            seeds
                                .iter()
                                .position(|seed| older_archive_row(seed, boundary))
                        })
                });
            let Some(at) = reached else {
                self.seeds.extend(seeds.into_iter().map(Arc::new));
                return false;
            };
            self.through = None;
            take = take.max(at + 1);
        }
        let partial = self.before.is_some() && seeds.len() > take;
        if partial {
            seeds.truncate(take);
        }
        self.seeds.extend(seeds.into_iter().map(Arc::new));
        partial
    }

    fn needs_more(&self) -> bool {
        self.seeds.len() < self.depth || self.through.is_some()
    }
}

fn older_archive_row(seed: &Seed, boundary: &Seed) -> bool {
    use crate::sidebar::queue::epoch_ms;

    if !seed.state_changed_at.is_empty() && seed.state_changed_at == boundary.state_changed_at {
        return seed.id < boundary.id;
    }
    epoch_ms(&seed.state_changed_at)
        .zip(epoch_ms(&boundary.state_changed_at))
        .is_some_and(|(seen, boundary)| seen < boundary)
}

enum Page {
    Refresh(PageRefresh),
    More(String),
}

fn refresh_page(state: &mut State, key: PageKey, tx: &mut Tx) {
    let bucket = state.window.garden.pages.entry(key.clone()).or_default();
    bucket.asked_revision = state.facts.garden.revision;
    let refresh = PageRefresh {
        before: None,
        depth: bucket.seeds.len(),
        through: bucket.seeds.last().cloned(),
        seeds: Vec::new(),
    };
    fetch_page(state, key, Page::Refresh(refresh), tx);
}

fn fetch_page(state: &mut State, key: PageKey, page: Page, tx: &mut Tx) {
    let request_id = state.next_request_id();
    let (before, limit, retry_before) = match &page {
        Page::Refresh(refresh) => (
            refresh.before.clone(),
            (refresh.depth.saturating_sub(refresh.seeds.len()) as i64)
                .clamp(PAGE_LIMIT, MAX_PAGE_LIMIT),
            None,
        ),
        Page::More(cursor) => (Some(cursor.clone()), PAGE_LIMIT, Some(cursor.clone())),
    };
    let bucket = state.window.garden.pages.get_mut(&key).unwrap();
    bucket.request_id = Some(request_id.clone());
    bucket.error = None;
    bucket.retry_before = None;
    bucket.loading_more = retry_before.is_some();
    bucket.loading = !bucket.loading_more;
    let command = GardenSeedsPageMessage {
        request_id: request_id.clone(),
        bed: (!key.bed.is_empty()).then(|| key.bed.clone()),
        plot_id: (!key.plot_id.is_empty()).then(|| key.plot_id.clone()),
        status: Some(key.status.clone()),
        before,
        limit: Some(limit),
        ..GardenSeedsPageMessage::new()
    };
    state.ask_as(
        request_id.clone(),
        &command,
        move |state, answer, tx| {
            let garden = &mut state.window.garden;
            let Some(bucket) = garden.pages.get_mut(&key) else {
                return;
            };
            if bucket.request_id.as_deref() != Some(request_id.as_str()) {
                return;
            }
            match answer.parse::<GardenSeedsPageResultMessage>() {
                Some(reply) if reply.success => {
                    let mut next = reply.next_before;
                    match page {
                        Page::Refresh(mut refresh) => {
                            if refresh.add(reply.seeds) {
                                next = refresh.before.clone();
                            }
                            if refresh.needs_more()
                                && let Some(before) = next
                            {
                                refresh.before = Some(before);
                                fetch_page(state, key, Page::Refresh(refresh), tx);
                                return;
                            }
                            let mut seen = HashSet::new();
                            refresh.seeds.retain(|seed| seen.insert(seed.id.clone()));
                            bucket.seeds = refresh.seeds;
                        }
                        Page::More(_) => {
                            let mut seen: HashSet<String> =
                                bucket.seeds.iter().map(|s| s.id.clone()).collect();
                            for seed in reply.seeds {
                                if seen.insert(seed.id.clone()) {
                                    bucket.seeds.push(Arc::new(seed));
                                }
                            }
                        }
                    }
                    bucket.answered = true;
                    bucket.harvested = reply.harvested;
                    bucket.withered = reply.withered;
                    bucket.next_before = next;
                }
                other => {
                    bucket.retry_before = retry_before;
                    bucket.error = Some(
                        other
                            .and_then(|reply| reply.error)
                            .unwrap_or_else(|| "Could not load older seeds".to_owned()),
                    );
                }
            }
            bucket.request_id = None;
            bucket.loading = false;
            bucket.loading_more = false;
            let outdated = bucket.asked_revision != state.facts.garden.revision;
            garden.revision += 1;
            if outdated {
                sync_pages(state, tx);
            }
        },
        tx,
    );
}

fn wanted_search_key(state: &State) -> Option<SearchKey> {
    if mode(state) == Mode::Closed || shows_board(state) {
        return None;
    }
    let profile = state.facts.profiles.selected.clone();
    if profile.is_empty() {
        return None;
    }
    let v2_on = v2::enabled(state);
    let query = search::parse(&state.window.garden.query.text, v2_on);
    if !query.searches || !query.unknown.is_empty() {
        return None;
    }
    let plot_id = living_trail(state).last().cloned().unwrap_or_default();
    let wide =
        !plot_id.is_empty() && state.window.garden.wide_in.as_deref() == Some(plot_id.as_str());
    let bed_scope = if v2_on && query.beds.is_empty() {
        v2::bed_scope(&v2::active_bed(state))
    } else {
        String::new()
    };
    Some(SearchKey {
        profile,
        terms: query.terms,
        is: query.is.into_iter().map(str::to_owned).collect(),
        tenders: query.tenders,
        beds: query.beds,
        bed_scope,
        plot_id,
        wide,
    })
}

fn sessions_signature(state: &State) -> u64 {
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    for session in state.facts.sessions.iter() {
        session.id.hash(&mut hasher);
        session.label.hash(&mut hasher);
    }
    hasher.finish()
}

fn sync_search(state: &mut State, tx: &mut Tx) {
    let Some(key) = wanted_search_key(state) else {
        let search = &mut state.window.garden.search;
        if search.key.is_some()
            || !search.hits.is_empty()
            || search.loading
            || search.loading_more
            || search.error.is_some()
        {
            *search = SearchState::default();
            state.window.garden.revision += 1;
        }
        return;
    };
    let garden_ptr = Arc::as_ptr(&state.facts.garden) as usize;
    let sessions_sig = sessions_signature(state);
    let search = &state.window.garden.search;
    if search.key.as_ref() == Some(&key)
        && search.asked_ptr == garden_ptr
        && search.asked_sessions == sessions_sig
    {
        return;
    }
    let limit = if search.key.as_ref() == Some(&key) {
        (search.hits.len() as i64).clamp(PAGE_LIMIT, MAX_PAGE_LIMIT)
    } else {
        PAGE_LIMIT
    };
    fetch_search(state, key, garden_ptr, sessions_sig, 0, limit, tx);
}

pub(super) fn load_more_search(state: &mut State, tx: &mut Tx) -> bool {
    let next = {
        let search = &state.window.garden.search;
        if search.loading || search.loading_more || search.error.is_some() {
            None
        } else {
            search.key.clone().zip(search.next_offset)
        }
    };
    let Some((key, offset)) = next else {
        return false;
    };
    let garden_ptr = Arc::as_ptr(&state.facts.garden) as usize;
    let sessions_sig = sessions_signature(state);
    fetch_search(state, key, garden_ptr, sessions_sig, offset, PAGE_LIMIT, tx);
    true
}

pub(super) fn retry_search(state: &mut State, tx: &mut Tx) {
    let Some(key) = wanted_search_key(state) else {
        return;
    };
    let search = &state.window.garden.search;
    let offset = if search.key.as_ref() == Some(&key) {
        search.retry_offset
    } else {
        0
    };
    let limit = if offset > 0 {
        PAGE_LIMIT
    } else if search.key.as_ref() == Some(&key) {
        (search.hits.len() as i64).clamp(PAGE_LIMIT, MAX_PAGE_LIMIT)
    } else {
        PAGE_LIMIT
    };
    let garden_ptr = Arc::as_ptr(&state.facts.garden) as usize;
    let sessions_sig = sessions_signature(state);
    fetch_search(state, key, garden_ptr, sessions_sig, offset, limit, tx);
}

fn safe_ranges(text: &str, raw: Option<Vec<GardenSearchRange>>) -> Vec<search::Range> {
    let Some(raw) = raw else {
        return Vec::new();
    };
    let mut out = Vec::with_capacity(raw.len());
    for range in raw {
        if range.start < 0 || range.end <= range.start {
            continue;
        }
        let start = (range.start as usize).min(text.len());
        let end = (range.end as usize).min(text.len());
        if start < end && text.is_char_boundary(start) && text.is_char_boundary(end) {
            out.push((start, end));
        }
    }
    out
}

fn search_hit_from(raw: GardenSearchHit) -> SearchHit {
    let title_ranges = safe_ranges(&raw.seed.title, raw.title_ranges);
    let snippet = raw.snippet.filter(|s| !s.is_empty()).map(|text| {
        let ranges = safe_ranges(&text, raw.snippet_ranges);
        search::Snippet { text, ranges }
    });
    SearchHit {
        seed: Arc::new(raw.seed),
        where_: raw.r#where,
        id_hit: raw.id_hit,
        title_ranges,
        snippet,
        plot_title: raw.plot_title.filter(|p| !p.is_empty()),
    }
}

fn fetch_search(
    state: &mut State,
    key: SearchKey,
    garden_ptr: usize,
    sessions_sig: u64,
    offset: i64,
    limit: i64,
    tx: &mut Tx,
) {
    let request_id = state.next_request_id();
    let append = offset > 0;
    let search = &mut state.window.garden.search;
    if search.key.as_ref() != Some(&key) {
        search.hits = Rc::default();
        search.next_offset = None;
        search.hidden_closed = 0;
        search.outside = 0;
        search.in_plot = 0;
        search.retry_offset = 0;
    }
    search.key = Some(key.clone());
    search.asked_ptr = garden_ptr;
    search.asked_sessions = sessions_sig;
    search.request_id = Some(request_id.clone());
    search.error = None;
    if append {
        search.loading_more = true;
    } else {
        search.loading = true;
        search.loading_more = false;
    }
    let command = GardenSearchMessage {
        request_id: request_id.clone(),
        terms: (!key.terms.is_empty()).then(|| key.terms.clone()),
        is: (!key.is.is_empty()).then(|| key.is.clone()),
        tenders: (!key.tenders.is_empty()).then(|| key.tenders.clone()),
        beds: (!key.beds.is_empty()).then(|| key.beds.clone()),
        bed_scope: (!key.bed_scope.is_empty()).then(|| key.bed_scope.clone()),
        plot_id: (!key.plot_id.is_empty()).then(|| key.plot_id.clone()),
        wide: key.wide.then_some(true),
        offset: Some(offset),
        limit: Some(limit),
        ..GardenSearchMessage::new()
    };
    state.ask_as(
        request_id.clone(),
        &command,
        move |state, answer, _| {
            let garden = &mut state.window.garden;
            let search = &mut garden.search;
            if search.request_id.as_deref() != Some(request_id.as_str()) {
                return;
            }
            search.request_id = None;
            search.loading = false;
            search.loading_more = false;
            match answer.parse::<GardenSearchResultMessage>() {
                Some(reply) if reply.success => {
                    search.hidden_closed = reply.hidden_closed;
                    search.outside = reply.outside;
                    search.in_plot = reply.in_plot;
                    search.next_offset = reply.next_offset;
                    search.error = None;
                    search.retry_offset = 0;
                    if append {
                        let mut merged: Vec<SearchHit> = search.hits.to_vec();
                        let mut seen: HashSet<String> =
                            merged.iter().map(|h| h.seed.id.clone()).collect();
                        for hit in reply.hits {
                            if seen.insert(hit.seed.id.clone()) {
                                merged.push(search_hit_from(hit));
                            }
                        }
                        search.hits = merged.into();
                    } else {
                        search.hits = reply
                            .hits
                            .into_iter()
                            .map(search_hit_from)
                            .collect::<Vec<_>>()
                            .into();
                    }
                }
                Some(reply) => {
                    search.retry_offset = offset;
                    search.error = Some(
                        reply
                            .error
                            .unwrap_or_else(|| "Could not search the garden".to_owned()),
                    );
                }
                None => {
                    search.retry_offset = offset;
                    search.error = Some("Could not search the garden".to_owned());
                }
            }
            garden.revision += 1;
        },
        tx,
    );
}

/// The absolute paths of the files `document` links to.
fn linked_files(document: Option<&SeedDocument>) -> Vec<String> {
    document
        .into_iter()
        .flat_map(|d| &d.references)
        .filter_map(|r| r.path.clone())
        .filter(|p| p.starts_with('/'))
        .collect()
}

/// Asks the daemon whether each linked file of seed `id` is on disk.
fn look_for(state: &mut State, id: &str, paths: Vec<String>, tx: &mut Tx) {
    for path in paths {
        let Some((dir, name)) = path.rsplit_once('/').filter(|(dir, _)| !dir.is_empty()) else {
            continue;
        };
        let command = FsExistsMessage {
            path: name.to_owned(),
            root: Some(dir.to_owned()),
            ..FsExistsMessage::new()
        };
        let (id, path) = (id.to_owned(), path.clone());
        state.ask(
            |request_id| FsExistsMessage {
                request_id: Some(request_id),
                ..command
            },
            move |state, answer, _| {
                let gone = answer
                    .parse::<FsExistsResultMessage>()
                    .and_then(|r| r.result)
                    .is_some_and(|r| !r.exists);
                let garden = &mut state.window.garden;
                if let Some(reading) = garden
                    .readings
                    .get_mut(&id)
                    .filter(|r| gone && r.looked_for.contains(&path))
                {
                    reading.missing.insert(path);
                    garden.revision += 1;
                }
            },
            tx,
        );
    }
}

/// Whether seed `id`'s document answers the read the latest garden push asked for.
pub(super) fn answered(garden: &Garden, id: &str) -> bool {
    garden.readings.get(id).is_some_and(|r| r.answered)
}

/// The linked files of seed `id` the daemon says are not on disk.
pub(super) fn missing<'a>(garden: &'a Garden, id: &str) -> Option<&'a HashSet<String>> {
    garden.readings.get(id).map(|r| &r.missing)
}

/// The shown seed's document as the daemon last answered it, and the error of its last read.
/// Seed `id` as its tile shows it: the daemon's document of it, else the garden's copy.
pub(crate) fn shown_seed<'a>(
    garden: &'a Garden,
    seeds: &'a [Arc<Seed>],
    id: &str,
) -> Option<&'a Seed> {
    let (document, _) = reading(garden, id);
    document
        .map(|d| &d.seed)
        .or_else(|| seeds.iter().find(|s| s.id == id).map(|s| s.as_ref()))
}

pub(crate) fn preview_seed<'a>(state: &'a State, id: &str) -> Option<&'a Seed> {
    seeds(state)
        .iter()
        .find(|seed| seed.id == id)
        .map(Arc::as_ref)
        .or_else(|| {
            let found = state.terminals.resolved_seed(id)?;
            let (document, _) = reading(&state.window.garden, id);
            Some(document.map_or(found.as_ref(), |doc| &doc.seed))
        })
}

pub(crate) fn reading<'a>(
    garden: &'a Garden,
    id: &str,
) -> (Option<&'a SeedDocument>, Option<&'a str>) {
    let Some(reading) = garden.readings.get(id) else {
        return (None, None);
    };
    (
        reading.document.as_deref().filter(|d| d.seed.id == id),
        reading.error.as_deref(),
    )
}

#[derive(Clone, Copy)]
pub(super) struct ReadingSeeds<'a> {
    pub(super) children: &'a [Arc<Seed>],
    pub(super) ancestors: Option<&'a [Arc<Seed>]>,
    pub(super) related: Option<&'a [Arc<Seed>]>,
}

pub(super) fn reading_seeds<'a>(garden: &'a Garden, id: &str) -> Option<ReadingSeeds<'a>> {
    let reading = garden.readings.get(id)?;
    reading.document.as_deref().filter(|d| d.seed.id == id)?;
    Some(ReadingSeeds {
        children: &reading.children,
        ancestors: reading.ancestors.as_deref(),
        related: reading.related.as_deref(),
    })
}

/// A read seed's body, and its files as the daemon last described them: name, size and when
/// each last changed.
#[derive(Hash, PartialEq, Eq, PartialOrd, Ord)]
pub(crate) struct ReadBody {
    pub(crate) seed: String,
    pub(crate) body: String,
    pub(crate) files: Vec<(String, i64, String)>,
}

/// The body and files of each seed whose document the window has read.
pub(crate) fn read_bodies(state: &State) -> Vec<ReadBody> {
    let mut bodies: Vec<_> = state
        .window
        .garden
        .readings
        .iter()
        .filter_map(|(id, r)| {
            let document = r.document.as_ref()?;
            Some(ReadBody {
                seed: id.clone(),
                body: document.seed.body.clone(),
                files: document
                    .artifacts
                    .iter()
                    .map(|a| (a.filename.clone(), a.size, a.modified_at.clone()))
                    .collect(),
            })
        })
        .collect();
    bodies.sort();
    bodies
}

/// Drops the walk into a plot or seed, and reads it.
pub(crate) fn drill(state: &mut State, id: &str, tx: &mut Tx) {
    let mut trail = living_trail(state);
    trail.push(id.to_owned());
    state.window.garden.trail = trail;
    tx.focus(BODY, false);
    follow(state, tx);
}

/// Climbs to `depth` steps from the root, focusing the row it came out of.
pub(crate) fn climb_to(state: &mut State, depth: usize, tx: &mut Tx) {
    let mut trail = living_trail(state);
    let from = trail.get(depth).cloned();
    trail.truncate(depth);
    state.window.garden.trail = trail;
    match from {
        Some(row) => tx.focus(view::row_key(&row), false),
        None => tx.focus(BODY, false),
    }
    follow(state, tx);
}

/// A row of the column `level` steps down picks that seed there, dropping what followed.
pub(crate) fn select_at(state: &mut State, level: usize, id: &str, tx: &mut Tx) {
    let mut trail = living_trail(state);
    trail.truncate(level);
    trail.push(id.to_owned());
    state.window.garden.trail = trail;
    tx.focus(BODY, false);
    follow(state, tx);
}

/// Opens a seed in the garden, docking the garden when it is closed.
pub(crate) fn open_seed(state: &mut State, id: &str, tx: &mut Tx) {
    if mode(state) == Mode::Closed {
        show(state, Mode::Dock, tx);
    } else if shows_board(state) {
        state.window.garden.board = Some(false);
    }
    open_found(state, id, tx);
}

/// Opens seed `id` in a tile, beside `session` when one asks for it.
pub(crate) fn open_tile(state: &mut State, id: &str, tx: &mut Tx) {
    let session = crate::opener::behind_screen(state).map(|s| s.id.clone());
    open_tile_beside(state, id, session, tx);
}

/// Opens seed `id` as a tile beside `session` and shows it, putting the keyboard in it unless
/// the user moved the keyboard while it opened.
pub(crate) fn open_tile_beside(state: &mut State, id: &str, session: Option<String>, tx: &mut Tx) {
    if state.window.tile.is_some() {
        let id = id.to_owned();
        let update =
            move |state: &mut State, tx: &mut Tx| open_tile_beside(state, &id, session, tx);
        crate::desktop::to_main(state, Box::new(update));
        crate::frame::raise_main();
        return;
    }
    let before = tx.focused().map(str::to_owned);
    let inputs_before = tx.inputs();
    state.act(
        |request_id| OpenSeedMessage {
            seed_id: id.to_owned(),
            request_id: Some(request_id),
            session_id: session,
            ..OpenSeedMessage::new()
        },
        Refused::Prefixed("Could not open the seed"),
        move |state, reply, tx| {
            let Some(OpenSeedResultMessage {
                desktop_id: Some(desktop),
                tile_id: Some(tile),
                ..
            }) = reply.parse::<OpenSeedResultMessage>()
            else {
                return;
            };
            let moved = tx.inputs() != inputs_before
                && tx.focused().is_some_and(|f| Some(f) != before.as_deref());
            crate::desktop::reveal_leaf(state, &desktop, &tile, !moved, tx);
        },
        tx,
    );
}

/// Opens a found seed under the whole path of plots above it, and ends the search.
pub(crate) fn open_found(state: &mut State, id: &str, tx: &mut Tx) {
    let index = view::index(state);
    let mut path = index.path_to(id);
    if path.is_empty() {
        if let Some(doc) = state
            .window
            .garden
            .readings
            .get(id)
            .and_then(|r| r.document.as_deref())
        {
            path = doc
                .ancestors
                .iter()
                .flatten()
                .map(|a| a.id.clone())
                .chain(std::iter::once(id.to_owned()))
                .collect();
            let root = doc
                .ancestors
                .as_deref()
                .and_then(|a| a.first())
                .unwrap_or(&doc.seed)
                .clone();
            state.window.garden.pending_open = None;
            align_v2_to_root(state, &root);
        } else {
            path = vec![id.to_owned()];
            state.window.garden.pending_open = Some(id.to_owned());
        }
    } else if let Some(root) = path.first().and_then(|root_id| index.by_id.get(root_id)) {
        let root = (**root).clone();
        state.window.garden.pending_open = None;
        align_v2_to_root(state, &root);
    }
    let garden = &mut state.window.garden;
    garden.trail = path;
    garden.query = TextEdit::default();
    garden.wide_in = None;
    tx.focus(BODY, false);
    follow(state, tx);
}

fn focus_search(state: &mut State, tx: &mut Tx) {
    let query = &mut state.window.garden.query;
    let end = query.text.len();
    query.set_selection(0, end);
    tx.focus(SEARCH_FIELD, true);
}

pub(crate) fn toggle_wide(state: &mut State, tx: &mut Tx) {
    let plot = living_trail(state).pop();
    let garden = &mut state.window.garden;
    garden.wide_in = match garden.wide_in {
        None => plot,
        Some(_) => None,
    };
    focus_search(state, tx);
    follow(state, tx);
}

pub(crate) fn toggle_closed(state: &mut State, tx: &mut Tx) {
    let query = &mut state.window.garden.query;
    *query = TextEdit::new(search::toggle_token(&query.text, "is:any"));
    focus_search(state, tx);
    follow(state, tx);
}

pub(crate) fn set_query(state: &mut State, text: String, tx: &mut Tx) {
    state.window.garden.query = TextEdit::new(text);
    focus_search(state, tx);
    follow(state, tx);
}

/// The search field's keys: the arrows walk the answers while there are some and show the one
/// reached whole, Return opens it, and Alt+Return widens the scope out of the plot or back.
pub(crate) fn field_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let answers = view::answers(state);
    match ev.key {
        Key::Down | Key::Up if !answers.ids.is_empty() => {
            let last = answers.ids.len() - 1;
            let at = answers.active.min(last);
            let next = if ev.key == Key::Down {
                (at + 1).min(last)
            } else {
                at.saturating_sub(1)
            };
            state.window.garden.walked = (answers.question, next);
            tx.scroll_row_into_view_in(view::RESULTS, view::row_key(&answers.ids[next]));
            if ev.key == Key::Down && next + 5 >= answers.ids.len() {
                load_more_search(state, tx);
            }
            true
        }
        Key::Down | Key::Up => walk_rows(state, ev.key == Key::Down, tx),
        Key::Enter if ev.mods.alt => {
            if answers.can_widen {
                toggle_wide(state, tx);
            }
            true
        }
        Key::Enter => match answers.ids.get(answers.active) {
            Some(id) => {
                let id = id.clone();
                open_found(state, &id, tx);
                true
            }
            None => false,
        },
        Key::Escape => crate::shell::escape_drawers(state, tx),
        _ => false,
    }
}

/// Escape in the garden puts away words being written on the seed it shows before it closes
/// anything, as they were opened last.
fn put_away_written(state: &mut State, tx: &mut Tx) -> bool {
    let Some(here) = living_trail(state).pop() else {
        return false;
    };
    spot::Spot::put_away_written(state, ("garden", &here), BODY, tx)
}

/// The garden's keys after app shortcuts: the seed page it reads takes them first, as
/// [`reading::key`] says; then `/` or ⌘F goes to the search field, the arrows walk the rows of
/// the place it stands in, → drills into the focused row in columns, ← climbs out, and Escape
/// closes the garden.
pub(crate) fn key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let find = reading::finds(ev);
    if !tx.in_text_field()
        && (app_key(state, ev, find, tx) && crate::keys::on_key(state, ev, tx)
            || reading::key(state, ev, tx))
    {
        return true;
    }
    let slash = matches!(&ev.key, Key::Text(t) if t == "/");
    if find || (slash && !tx.in_text_field()) {
        focus_search(state, tx);
        return true;
    }
    if ev.key == Key::Escape {
        return put_away_written(state, tx) || crate::shell::escape_drawers(state, tx);
    }
    if tx.in_text_field() || ev.mods.cmd || ev.mods.ctrl || ev.mods.alt {
        return false;
    }
    if v2::enabled(state)
        && let Key::Text(ref t) = ev.key
    {
        match t.as_str() {
            "b" => {
                let target = focused_row(tx).or_else(|| living_trail(state).pop());
                if let Some(seed_id) = target {
                    let back = tx.focused().map(str::to_owned);
                    v2::open_bed_picker(state, &seed_id, back, tx);
                    return true;
                }
            }
            "j" => return walk_rows(state, true, tx),
            "k" => return walk_rows(state, false, tx),
            _ => {}
        }
    }
    match ev.key {
        Key::Left => {
            let trail = living_trail(state);
            if trail.is_empty() {
                return false;
            }
            climb_to(state, trail.len() - 1, tx);
            true
        }
        Key::Right if view::columns(state) => match focused_row(tx) {
            Some(id) => {
                view::press_row(state, &id, tx);
                true
            }
            None => false,
        },
        Key::Down | Key::Up => walk_rows(state, ev.key == Key::Down, tx),
        _ => false,
    }
}

fn app_key(state: &State, ev: &KeyEvent, find: bool, tx: &Tx) -> bool {
    if !matches!(state.window.hints, crate::hints::Hints::Off)
        || state
            .window
            .leader
            .as_ref()
            .is_some_and(|leader| tx.clock() < leader.until)
    {
        return true;
    }
    if ev.key == Key::Escape {
        return false;
    }
    let focus = Focus {
        page: crate::browser::holds_keys(state),
        ..Focus::default()
    };
    match state.keymap.press(ev, tx, focus) {
        Press::Unbound => false,
        Press::Action(Action::TerminalFind) => !find,
        _ => true,
    }
}

fn focused_row(tx: &Tx) -> Option<String> {
    tx.focused()?
        .strip_prefix(view::ROW_PREFIX)
        .map(str::to_owned)
}

/// Moves focus to the last row of the focused row's column, or of the place the garden stands
/// in, or to its first.
fn walk_to_end(state: &mut State, last: bool, tx: &mut Tx) -> bool {
    let rows = view::walkable_rows(state, focused_row(tx).as_deref());
    let end = if last { rows.last() } else { rows.first() };
    let Some(end) = end else {
        return false;
    };
    tx.focus(view::row_key(end), true);
    true
}

/// Moves focus one row down or up the rows of the focused row's column, or of the place the
/// garden stands in, from the top or the bottom when no row has it.
fn walk_rows(state: &mut State, down: bool, tx: &mut Tx) -> bool {
    let focused = focused_row(tx);
    let rows = view::walkable_rows(state, focused.as_deref());
    if rows.is_empty() {
        return false;
    }
    let at = focused
        .as_deref()
        .and_then(|id| rows.iter().position(|r| r == id));
    let next = match (at, down) {
        (None, true) => 0,
        (None, false) => rows.len() - 1,
        (Some(at), true) => (at + 1).min(rows.len() - 1),
        (Some(at), false) => at.saturating_sub(1),
    };
    tx.focus(view::row_key(&rows[next]), true);
    if down && next + 5 >= rows.len() {
        if let Some(key) = view::active_page_key(state, focused.as_deref()) {
            load_more_page(state, key, tx);
        } else if state.window.garden.search.next_offset.is_some() {
            load_more_search(state, tx);
        }
    }
    true
}
