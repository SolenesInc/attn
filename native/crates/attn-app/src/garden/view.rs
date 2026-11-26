use std::cell::Cell;
use std::collections::HashSet;
use std::rc::Rc;
use std::sync::Arc;

use attn_protocol::Seed;
use attn_ui::blocks::{
    Actions, Bar, Block, Byline, Inline, Pane, Prose, Reader, Room, Section, Stack, Surface, Trail,
};
use attn_ui::system::{
    Button, DotShape, DotSize, GroupHead, Link, ListRow, Look as ButtonLook, Part, Said, Size,
    Type, dot, marked, search_field, words,
};
use attn_ui::{GlyphButton, Tokens};
use rune::{El, GrowthSpec, ScrollSpec, Tx, col, memo, row};
use rune_kit::{ScrollBar, TextInput};

use super::model::{self, Index, SessionNames};
use super::reading;
use super::search::{self, Lookups, Partial, Query};
use super::sheet::{Go, Sheet};
use super::v2::{self, Attention, BedFilter, Lens};
use super::{BODY, Mode, PageBucket, PageKey, SEARCH_FIELD, SearchHit};
use crate::State;

thread_local! {
    pub(super) static ROWS_BUILT: Cell<usize> = const { Cell::new(0) };
}

pub(super) const ROW_PREFIX: &str = "garden.row.";
/// The list a search's answers stand in.
pub(super) const RESULTS: &str = "garden.results";
const COLUMNS_MIN: f32 = 1160.0;
const THIRD_COLUMN_MIN: f32 = 1460.0;
/// A list column's width.
const COLUMN_WIDTH: f32 = 300.0;
/// How wide the reader beside the columns lets a seed's page grow.
const READER_MAX_WIDTH: f32 = 760.0;
/// How wide the expanded garden's root list grows before it centers.
const ROOT_MAX_WIDTH: f32 = 880.0;

/// Whether the garden's query searches, so its answers stand in the walk's place.
pub(super) fn searching(state: &State) -> bool {
    search::parse(&state.window.garden.query.text, v2::enabled(state)).searches
}

pub(super) fn row_key(id: &str) -> String {
    format!("{ROW_PREFIX}{id}")
}

pub(super) fn index(state: &State) -> Index {
    Index::of(super::seeds(state))
}

struct Look<'a> {
    index: &'a Index,
}

impl Lookups for Look<'_> {
    fn blockers_of(&self, seed: &Seed) -> usize {
        self.index.blockers(&seed.id)
    }
}

pub(super) fn frame_width(state: &State) -> f32 {
    match super::mode(state) {
        Mode::Full => {
            (state.window.width.window - attn_ui::blocks::Surface::under_bar_inset() * 2.0).max(0.0)
        }
        _ => crate::shell::Drawer::Garden.width(state.window.width.window),
    }
}

/// The walk lays out in columns once the frame is wide enough: fullscreen in a wide window.
pub(super) fn columns(state: &State) -> bool {
    frame_width(state) >= COLUMNS_MIN
}

struct Place {
    index: Rc<Index>,
    names: Rc<SessionNames>,
    trail: Vec<String>,
    here_seed: Option<Arc<Seed>>,
    query: Query,
    wide: bool,
    answers: Rc<[SearchHit]>,
    outside: usize,
    in_plot: usize,
    hidden_closed: usize,
    search_loading: bool,
    search_loading_more: bool,
    search_next_offset: Option<i64>,
    search_error: Option<String>,
    root_closed_count: usize,
    root_page_key: PageKey,
    now_ms: i64,
    v2: Rc<v2::Snapshot>,
    lens: Lens,
    has_beds: bool,
}

fn merge_paged(
    mut base: Vec<Arc<Seed>>,
    index: &Index,
    bucket: Option<&PageBucket>,
) -> Vec<Arc<Seed>> {
    let Some(bucket) = bucket else {
        return base;
    };
    if bucket.seeds.is_empty() {
        return base;
    }
    let extra: Vec<Arc<Seed>> = {
        let mut seen: HashSet<&str> = base.iter().map(|s| s.id.as_str()).collect();
        let mut out = Vec::new();
        for seed in &bucket.seeds {
            if !index.by_id.contains_key(&seed.id) && seen.insert(seed.id.as_str()) {
                out.push(seed.clone());
            }
        }
        out
    };
    base.extend(extra);
    base
}

impl Place {
    fn of(state: &State) -> Place {
        let index = Rc::new(index(state));
        let names = Rc::new(model::session_names(
            &state.facts.sessions,
            &state.facts.harnesses,
        ));
        let trail = super::living_trail(state);
        let here_seed = trail.last().and_then(|id| super::lookup_seed(state, id));
        let garden = &state.window.garden;
        let v2_snap = Rc::new(v2::Snapshot::of(state, &index));
        let query = search::parse(&garden.query.text, v2_snap.on);
        let lens = garden.lens;
        let has_beds = v2_snap.on && !v2::beds(state).is_empty();
        let plot = trail.last().cloned();
        let wide = query.searches && plot.is_some() && garden.wide_in == plot;
        let (
            answers,
            outside,
            in_plot,
            hidden_closed,
            search_loading,
            search_loading_more,
            search_next_offset,
            search_error,
        ) = if query.searches && query.unknown.is_empty() {
            let s = &garden.search;
            (
                s.hits.clone(),
                s.outside.max(0) as usize,
                s.in_plot.max(0) as usize,
                s.hidden_closed.max(0) as usize,
                s.loading,
                s.loading_more,
                s.next_offset,
                s.error.clone(),
            )
        } else {
            (Rc::default(), 0, 0, 0, false, false, None, None)
        };
        let closed_counts = state
            .facts
            .garden
            .closed_counts_of(&state.facts.profiles.selected);
        let root_closed_count = v2::closed_tally(closed_counts, v2_snap.on.then_some(&v2_snap.bed))
            .map_or_else(
                || {
                    index
                        .roots
                        .iter()
                        .filter(|s| {
                            (!v2_snap.on || v2::matches_bed(s, &v2_snap.bed))
                                && v2::is_hidden_closed(s)
                        })
                        .count()
                },
                |(h, w)| h + w,
            );
        let root_page_key = PageKey::root(state);
        Place {
            index,
            names,
            trail,
            here_seed,
            query,
            wide,
            answers,
            outside,
            in_plot,
            hidden_closed,
            search_loading,
            search_loading_more,
            search_next_offset,
            search_error,
            root_closed_count,
            root_page_key,
            now_ms: state.window.now_ms,
            v2: v2_snap,
            lens,
            has_beds,
        }
    }

    fn shows_closed_page(&self) -> bool {
        !self.query.searches
            && (self.query.is.contains(&"closed") || self.query.is.contains(&"any"))
    }

    fn attention_of(&self, seed: &Seed) -> Attention {
        self.v2.attention_of(seed)
    }

    fn v1_roots(&self, state: &State) -> Vec<Arc<Seed>> {
        let live = self.lens(&self.index.roots);
        if self.shows_closed_page() {
            merge_paged(
                live,
                &self.index,
                state.window.garden.pages.get(&self.root_page_key),
            )
        } else {
            live
        }
    }

    fn v2_roots(&self, state: &State, docked: bool) -> Vec<Arc<Seed>> {
        let look = self.look();
        let explicit_is = !self.query.is.is_empty();
        let lens = if docked && self.lens == Lens::Triage {
            Lens::All
        } else {
            self.lens
        };
        let mut out: Vec<Arc<Seed>> = self
            .index
            .roots
            .iter()
            .filter(|s| v2::matches_bed(s, &self.v2.bed))
            .filter(|s| {
                if explicit_is {
                    search::satisfies_lens(s, &self.query.is, &look)
                } else {
                    if v2::is_hidden_closed(s) {
                        return false;
                    }
                    match lens {
                        Lens::All => true,
                        Lens::Now => self.attention_of(s).is_now(),
                        Lens::Triage => {
                            let att = self.attention_of(s);
                            matches!(att, Attention::Quiet | Attention::Parked)
                                || (self.has_beds
                                    && !v2::is_automation(s)
                                    && s.bed_id.as_deref().unwrap_or("").is_empty())
                        }
                    }
                }
            })
            .cloned()
            .collect();
        out.sort_by_key(|s| {
            let att = self.attention_of(s);
            let band = if att.is_waiting() {
                0
            } else if att.is_now() {
                1
            } else {
                2
            };
            (band, att)
        });
        if self.shows_closed_page() {
            merge_paged(
                out,
                &self.index,
                state.window.garden.pages.get(&self.root_page_key),
            )
        } else {
            out
        }
    }

    fn children_of(&self, state: &State, plot_id: &str) -> Vec<Arc<Seed>> {
        let live = self.lens(self.index.children_of(plot_id));
        if self.shows_closed_page() {
            let key = PageKey::plot(state, plot_id);
            merge_paged(live, &self.index, state.window.garden.pages.get(&key))
        } else {
            live
        }
    }

    fn plot_closed_count(&self, state: &State, here: &Seed) -> usize {
        if let Some(bucket) = state
            .window
            .garden
            .pages
            .get(&PageKey::plot(state, &here.id))
            .filter(|b| b.answered)
        {
            return (bucket.harvested + bucket.withered).max(0) as usize;
        }
        if let (Some(doc), _) = super::reading(&state.window.garden, &here.id)
            && !doc.children.is_empty()
        {
            return doc
                .children
                .iter()
                .filter(|child| {
                    let current = self.index.by_id.get(&child.id).map_or(*child, Arc::as_ref);
                    v2::is_hidden_closed(current)
                })
                .count();
        }
        let live_closed = self
            .index
            .children_of(&here.id)
            .iter()
            .filter(|child| v2::is_hidden_closed(child))
            .count();
        if live_closed > 0 {
            return live_closed;
        }
        here.plot_progress
            .as_ref()
            .map_or(0, |p| (p.done + p.withered).max(0) as usize)
    }

    fn look(&self) -> Look<'_> {
        Look { index: &self.index }
    }

    fn here(&self) -> Option<&Arc<Seed>> {
        self.here_seed.as_ref()
    }

    fn lens(&self, seeds: &[Arc<Seed>]) -> Vec<Arc<Seed>> {
        let look = self.look();
        seeds
            .iter()
            .filter(|s| {
                search::satisfies_lens(s, &self.query.is, &look)
                    || (self.query.is.is_empty() && model::has_open_work_below(s))
            })
            .cloned()
            .collect()
    }

    /// The answer the arrows reached for this question, or the best one.
    fn active(&self, state: &State) -> usize {
        let question = self.question(&state.window.garden.query.text);
        let walked = &state.window.garden.walked;
        if walked.0 == question { walked.1 } else { 0 }
    }

    fn question(&self, raw: &str) -> String {
        format!("{} {raw}", self.trail.last().map_or("", String::as_str))
    }

    /// The levels a column walk shows: the roots, then each walked plot's children.
    fn levels(&self, state: &State) -> Vec<(Vec<Arc<Seed>>, String, Option<PageKey>)> {
        let mut levels = vec![(
            self.v1_roots(state),
            self.trail.first().cloned().unwrap_or_default(),
            self.shows_closed_page().then(|| self.root_page_key.clone()),
        )];
        for depth in 0..self.trail.len() {
            let id = &self.trail[depth];
            let kids = self.children_of(state, id);
            let is_plot = super::lookup_seed(state, id).is_some_and(|s| model::is_plot(&s));
            if kids.is_empty() && !is_plot {
                break;
            }
            levels.push((
                kids,
                self.trail.get(depth + 1).cloned().unwrap_or_default(),
                self.shows_closed_page().then(|| PageKey::plot(state, id)),
            ));
        }
        levels
    }
}

pub(super) struct Answers {
    pub(super) ids: Vec<String>,
    pub(super) active: usize,
    pub(super) question: String,
    pub(super) can_widen: bool,
}

pub(super) fn answers(state: &State) -> Answers {
    let place = Place::of(state);
    let question = place.question(&state.window.garden.query.text);
    let active = place.active(state);
    Answers {
        ids: place.answers.iter().map(|m| m.seed.id.clone()).collect(),
        active,
        can_widen: !place.trail.is_empty()
            && place.query.searches
            && (place.wide || place.outside > 0),
        question,
    }
}

/// The rows the arrows walk: the focused row's column, or the place the garden stands in.
pub(super) fn walkable_rows(state: &State, focused: Option<&str>) -> Vec<String> {
    let place = Place::of(state);
    let ids = |seeds: &[Arc<Seed>]| seeds.iter().map(|s| s.id.clone()).collect::<Vec<_>>();
    if place.query.searches {
        return place.answers.iter().map(|m| m.seed.id.clone()).collect();
    }
    if place.v2.on {
        let docked = super::mode(state) != Mode::Full;
        if columns(state) {
            if let Some(here) = place.here()
                && model::is_plot(here)
            {
                let children = place.children_of(state, &here.id);
                if !children.is_empty()
                    && focused.is_none_or(|id| children.iter().any(|child| child.id == id))
                {
                    return ids(&children);
                }
            }
            return ids(&place.v2_roots(state, docked));
        }
        return match place.here() {
            Some(here) => ids(&place.children_of(state, &here.id)),
            None => ids(&place.v2_roots(state, docked)),
        };
    }
    if columns(state) {
        let levels = place.levels(state);
        let column = focused
            .and_then(|id| {
                levels
                    .iter()
                    .find(|(seeds, _, _)| seeds.iter().any(|s| s.id == id))
            })
            .or(levels.last());
        return column.map(|(seeds, _, _)| ids(seeds)).unwrap_or_default();
    }
    match place.here() {
        Some(here) => ids(&place.children_of(state, &here.id)),
        None => ids(&place.v1_roots(state)),
    }
}

pub(super) fn active_page_key(state: &State, focused: Option<&str>) -> Option<PageKey> {
    let place = Place::of(state);
    if !place.shows_closed_page() {
        return None;
    }
    if place.v2.on {
        if columns(state) {
            if let Some(here) = place.here()
                && model::is_plot(here)
            {
                let children = place.children_of(state, &here.id);
                if !children.is_empty()
                    && focused.is_none_or(|id| children.iter().any(|child| child.id == id))
                {
                    return Some(PageKey::plot(state, &here.id));
                }
            }
            return Some(place.root_page_key);
        }
        return match place.here() {
            Some(here) if model::is_plot(here) => Some(PageKey::plot(state, &here.id)),
            Some(_) => None,
            None => Some(place.root_page_key),
        };
    }
    if columns(state) {
        let levels = place.levels(state);
        let column = focused
            .and_then(|id| {
                levels
                    .iter()
                    .find(|(seeds, _, _)| seeds.iter().any(|s| s.id == id))
            })
            .or(levels.last());
        return column.and_then(|(_, _, key)| key.clone());
    }
    match place.here() {
        Some(here) if model::is_plot(here) => Some(PageKey::plot(state, &here.id)),
        Some(_) => None,
        None => Some(place.root_page_key),
    }
}

/// What pressing the row of `id` does where it shows.
pub(super) fn press_row(state: &mut State, id: &str, tx: &mut Tx) {
    let place = Place::of(state);
    if place.query.searches {
        return super::open_found(state, id, tx);
    }
    if place.v2.on && columns(state) {
        let docked = super::mode(state) != Mode::Full;
        if place.v2_roots(state, docked).iter().any(|s| s.id == id) {
            return super::select_at(state, 0, id, tx);
        }
        return super::drill(state, id, tx);
    }
    if columns(state) {
        let levels = place.levels(state);
        if let Some(level) = levels
            .iter()
            .position(|(seeds, _, _)| seeds.iter().any(|s| s.id == id))
        {
            return super::select_at(state, level, id, tx);
        }
    }
    super::drill(state, id, tx);
}

/// The garden in the dock, over the work.
pub(crate) fn panel(state: &State, t: &Tokens) -> El {
    col()
        .key("drawer")
        .h_full()
        .child(super::zoomed(super::zoom(state), body(state, t)))
}

/// The garden holding the window, over everything, below the title bar.
pub(crate) fn frame_layer(state: &State, t: &Tokens) -> Option<El> {
    if super::mode(state) != Mode::Full {
        return None;
    }
    let content = if super::shows_board(state) {
        super::board::view(state, t)
    } else {
        body(state, t)
    };
    Some(full_frame(
        state,
        t,
        super::zoomed(super::zoom(state), content),
    ))
}

pub(super) fn full_frame(state: &State, t: &Tokens, content: El) -> El {
    let dialog = col()
        .key("garden.frame")
        .role("dialog")
        .label("The garden")
        .modal_tx(true, |state: &mut State, _: &mut Tx| super::close(state))
        .w_full()
        .h_full()
        .child(content)
        .children(crate::desktop::header_pop::in_sheet(state, t));
    crate::shell::under_title_bar("garden.full", Surface::panel(dialog, t), t)
}

fn body(state: &State, t: &Tokens) -> El {
    let place = Place::of(state);
    let wide = columns(state);
    let content = if wide {
        columns_view(state, &place, t)
    } else {
        stack_view(state, &place, t)
    };
    let mut view = col()
        .key(BODY)
        .role("region")
        .label("The garden")
        .focusable(true)
        .w_full()
        .h_full()
        .min_h(0.0)
        .on_key(super::key)
        .child(chrome(state, &place, t));
    if place.v2.on && !wide {
        view = view.child(v2::dock_bed_chips(state, &place.v2.counts, t));
    }
    view.child(search_line(state, &place, t)).child(content)
}

/// One of the garden's quiet buttons, keyed `key`.
pub(super) fn button(key: impl Into<String>, label: impl Into<Said>, t: &Tokens) -> El {
    quiet(label).build(t).key(key).into()
}

/// A quiet compact button, to give a press and build.
pub(super) fn quiet(label: impl Into<Said>) -> Button {
    Button::new(label)
        .look(ButtonLook::Quiet)
        .size(Size::Compact)
}

/// A bordered compact button beside a seed's title.
pub(super) fn plain(label: impl Into<Said>) -> Button {
    Button::new(label).size(Size::Compact)
}

fn chrome(state: &State, place: &Place, t: &Tokens) -> El {
    let full = super::mode(state) == Mode::Full;
    let mut bar = Bar::new("garden.chrome").lead(trail_nav(state, place, t));
    if full {
        bar = bar.push(view_switch(state, t));
    } else if place.v2.on {
        bar = bar.push(v2::dock_lens_button(state, t));
    }
    if let Some((count, on)) = closed_toggle(state, place, columns(state)) {
        let label = match (on, count) {
            (true, 0) => "hide closed".to_owned(),
            (true, n) => format!("hide {n} closed"),
            (false, n) => format!("{n} closed"),
        };
        let toggle = Button::new(label)
            .look(if on {
                ButtonLook::Plain
            } else {
                ButtonLook::Quiet
            })
            .size(Size::Compact)
            .on_press_tx(super::toggle_closed)
            .build(t)
            .key("garden.closed");
        bar = bar.push(El::from(toggle).a11y_value(if on { "pressed" } else { "" }));
    }
    let frame_label = if full {
        "Return the garden to the dock"
    } else {
        "Expand the garden"
    };
    let frame_glyph = if full {
        &attn_ui::icon::FRAME_IN
    } else {
        &attn_ui::icon::FRAME_OUT
    };
    bar.push(
        GlyphButton::new(frame_glyph, frame_label)
            .color(t.text_muted)
            .on_press_tx(super::toggle_frame)
            .build(attn_ui::kit(t))
            .key("garden.frame-button"),
    )
    .push(
        GlyphButton::new(&rune_kit::glyphs::CLOSE, "Close")
            .color(t.text_muted)
            .on_press(super::close)
            .build(attn_ui::kit(t))
            .key("garden.close"),
    )
    .build(t)
}

pub(super) fn view_switch(state: &State, t: &Tokens) -> El {
    let board = super::shows_board(state);
    let choice = |name: &'static str, on: bool, pick: bool| -> El {
        let button: El = Button::new(name)
            .look(if on {
                ButtonLook::Plain
            } else {
                ButtonLook::Quiet
            })
            .size(Size::Compact)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| super::choose_board(state, pick, tx))
            .build(t)
            .key(format!("garden.view.{name}"))
            .into();
        button.a11y_value(if on { "pressed" } else { "" })
    };
    Inline::tight()
        .key("garden.view")
        .role("group")
        .label("Garden view")
        .child(choice("list", !board, false))
        .child(choice("board", board, true))
}

fn visible_column_start(state: &State, count: usize) -> usize {
    let max = if frame_width(state) >= THIRD_COLUMN_MIN {
        3
    } else {
        2
    };
    count.saturating_sub(max)
}

/// The closed toggle's count and whether it is on, or nothing when it has nothing to say.
fn closed_toggle(state: &State, place: &Place, wide: bool) -> Option<(usize, bool)> {
    let on = place.query.is.contains(&"any");
    let other = place.query.is.iter().any(|v| *v != "any");
    let hidden_here = if wide && !place.v2.on && !place.trail.is_empty() {
        let levels = place.levels(state);
        let first = visible_column_start(state, levels.len());
        (first..levels.len()).fold(0usize, |sum, level| {
            let hidden = if level == 0 {
                place.root_closed_count
            } else {
                place
                    .trail
                    .get(level - 1)
                    .and_then(|id| super::lookup_seed(state, id))
                    .map_or(0, |plot| place.plot_closed_count(state, &plot))
            };
            sum + hidden
        })
    } else {
        match place.here() {
            Some(here) => {
                let child_hidden = place.plot_closed_count(state, here);
                if place.v2.on && wide {
                    place.root_closed_count + child_hidden
                } else {
                    child_hidden
                }
            }
            None => place.root_closed_count,
        }
    };
    let count = if !place.query.searches {
        hidden_here
    } else if on {
        place
            .answers
            .iter()
            .filter(|m| model::is_closed(&m.seed))
            .count()
    } else {
        place.hidden_closed
    };
    if other || (!on && count == 0) {
        return None;
    }
    Some((count, on))
}

fn trail_nav(state: &State, place: &Place, t: &Tokens) -> El {
    let trail = &place.trail;
    let nav = Trail::new(
        "garden.trail",
        if place.wide {
            "Standing here, searching the whole garden"
        } else {
            "The way back"
        },
    );
    let scoped_bed = place.v2.on && place.v2.bed != BedFilter::All;
    let Some(here) = place.here() else {
        if scoped_bed {
            let root: El = Link::new("garden.trail.0", "Garden").build(t).into();
            let root = root.on_press_tx(|state: &mut State, tx: &mut Tx| {
                v2::select_bed(state, BedFilter::All, tx)
            });
            return nav
                .step(root)
                .here("garden.trail.here", v2::bed_label(state, &place.v2.bed), t)
                .build(t);
        }
        return nav.here("garden.trail.here", "The garden", t).build(t);
    };
    let first_visible = if !place.v2.on && columns(state) && !place.query.searches {
        let levels = place.levels(state);
        let shown = levels.len().min(if frame_width(state) >= THIRD_COLUMN_MIN {
            3
        } else {
            2
        });
        let first = levels.len() - shown;
        if first == 0 && trail.len() > 1 && !levels[0].0.iter().any(|s| s.id == trail[0]) {
            trail.len() - 1
        } else {
            first
        }
    } else {
        usize::MAX
    };
    let ancestors = &trail[..first_visible.min(trail.len() - 1)];
    let fold = !state.window.garden.trail_open && ancestors.len() > 3;
    let shown = if fold {
        &ancestors[ancestors.len() - 2..]
    } else {
        ancestors
    };
    let folded = ancestors.len() - shown.len();
    let step = |key: String, label: String, depth: usize| {
        let link: El = Link::new(key, label).build(t).into();
        link.on_press_tx(move |state: &mut State, tx: &mut Tx| super::climb_to(state, depth, tx))
    };
    let mut nav = if scoped_bed {
        let root: El = Link::new("garden.trail.0", "Garden").build(t).into();
        let root = root.on_press_tx(|state: &mut State, tx: &mut Tx| {
            v2::select_bed(state, BedFilter::All, tx)
        });
        nav.step(root).step(step(
            "garden.trail.bed".into(),
            v2::bed_label(state, &place.v2.bed),
            0,
        ))
    } else {
        nav.step(step("garden.trail.0".into(), "Garden".into(), 0))
    };
    if fold {
        let link: El = Link::new("garden.trail.fold", "…").build(t).into();
        nav = nav.step(
            link.label(format!("Show {folded} more steps"))
                .on_press(|state: &mut State| state.window.garden.trail_open = true),
        );
    }
    for (offset, id) in shown.iter().enumerate() {
        let depth = folded + offset + 1;
        let title = super::lookup_seed(state, id).map_or_else(|| id.clone(), |s| s.title.clone());
        nav = nav.step(step(format!("garden.trail.{depth}"), title, depth));
    }
    if place.query.searches || place.v2.on {
        nav = nav.here("garden.trail.here", here.title.clone(), t);
    }
    nav.build(t)
}

fn search_line(state: &State, place: &Place, t: &Tokens) -> El {
    let garden = &state.window.garden;
    let plot = !place.trail.is_empty();
    let place_word = if plot && !place.wide {
        "this plot"
    } else {
        "the garden"
    };
    let found = place
        .query
        .searches
        .then(|| place.answers.get(place.active(state)))
        .flatten();
    let placeholder = if place.v2.on {
        "search — or is:ready, bed:attn, tender:…"
    } else {
        "search — or is:ready, tender:…"
    };
    let field: El = search_field(
        TextInput::new(&garden.query)
            .key(SEARCH_FIELD)
            .label(format!("Search {place_word}"))
            .placeholder(placeholder)
            .active_descendant(found.map(|m| row_key(&m.seed.id)))
            .on_edit(|state: &mut State| &mut state.window.garden.query)
            .on_change(super::follow)
            .on_key(super::field_key),
        t,
    )
    .into();
    let mut meta = Vec::new();
    let q = &place.query;
    if !q.unknown.is_empty() {
        let said = format!("no filter called {}", q.unknown.join(" "));
        meta.push(
            words(said.clone(), Type::Small, t.warning)
                .key("garden.search.unknown")
                .role("status")
                .label(said),
        );
    } else if let Some(partial) = q.partial {
        match partial {
            Partial::Is => {
                for value in search::IS_VALUES {
                    meta.push(
                        button(format!("garden.search.is.{value}"), value, t).on_press_tx(
                            move |state: &mut State, tx: &mut Tx| {
                                let raw = state.window.garden.query.text.clone();
                                let cut = raw.rfind("is:").map_or(raw.as_str(), |at| &raw[..at]);
                                super::set_query(state, format!("{cut}is:{value} "), tx);
                            },
                        ),
                    );
                }
            }
            Partial::Tender => {
                meta.push(words(
                    "a crew member or session",
                    Type::Small,
                    t.text_tertiary,
                ));
            }
            Partial::Bed if place.v2.on => {
                let profile_beds = v2::beds(state);
                if profile_beds.is_empty() {
                    meta.push(words(
                        "a bed name or unassigned",
                        Type::Small,
                        t.text_tertiary,
                    ));
                } else {
                    for bed in profile_beds.iter().take(5) {
                        let id = bed.id.clone();
                        meta.push(
                            button(format!("garden.search.bed.{}", bed.id), bed.name.clone(), t)
                                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                                    let raw = &state.window.garden.query.text;
                                    let before = raw
                                        .rsplit_once(char::is_whitespace)
                                        .map_or("", |(before, _)| before);
                                    let query = if before.is_empty() {
                                        format!("bed:{id} ")
                                    } else {
                                        format!("{before} bed:{id} ")
                                    };
                                    super::set_query(state, query, tx);
                                }),
                        );
                    }
                }
            }
            Partial::Bed => {}
        }
    } else if q.active && plot {
        if !place.wide && place.outside > 0 {
            meta.push(
                button(
                    "garden.search.widen",
                    format!("+{} in the whole garden", place.outside),
                    t,
                )
                .on_press_tx(super::toggle_wide),
            );
        }
        if place.wide {
            meta.push(
                button(
                    "garden.search.widen",
                    format!("{} in this plot", place.in_plot),
                    t,
                )
                .on_press_tx(super::toggle_wide),
            );
        }
    }
    let prompt = words("/", Type::Mono, t.text_dimmed).a11y_hidden();
    let mut bar = Bar::new("garden.search").start(prompt).lead(field);
    if !place.v2.on || !meta.is_empty() {
        bar = bar.push(
            Inline::tight()
                .key("garden.search.meta")
                .grow(1.0)
                .shrink()
                .min_w(0.0)
                .justify_end()
                .children(meta),
        );
    }
    if place.v2.on && super::mode(state) == Mode::Full {
        let waiting_any = place.v2.counts.waiting_for(&place.v2.bed);
        bar = bar.push(v2::lens_switch(state, waiting_any, t));
    }
    bar.build(t)
}

/// How a row's press acts where it shows.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
enum Pick {
    Drill,
    Level(usize),
    Found,
}

#[derive(Clone, PartialEq, Eq, Hash)]
struct RowLook {
    seed: usize,
    blockers: usize,
    selected: bool,
    active: bool,
    pick: Pick,
    title_ranges: Vec<search::Range>,
    snippet: Option<search::Snippet>,
    where_log: bool,
    id_hit: bool,
    home: Option<String>,
    tender: String,
    standby: Option<model::Standby>,
    v2: bool,
    attention: Attention,
    show_bed: bool,
    short_age: String,
}

struct RowListCtx {
    index: Rc<Index>,
    names: Rc<SessionNames>,
    v2: Rc<v2::Snapshot>,
    now_ms: i64,
    pick: Pick,
    selected: String,
    active: Option<usize>,
    trail_last: String,
    show_bed: bool,
}

#[derive(Clone)]
enum RowItems {
    Seeds(Rc<[Arc<Seed>]>),
    Hits(Rc<[SearchHit]>),
}

impl RowItems {
    fn len(&self) -> usize {
        match self {
            RowItems::Seeds(seeds) => seeds.len(),
            RowItems::Hits(hits) => hits.len(),
        }
    }

    fn seed(&self, i: usize) -> &Arc<Seed> {
        match self {
            RowItems::Seeds(seeds) => &seeds[i],
            RowItems::Hits(hits) => &hits[i].seed,
        }
    }

    fn hit(&self, i: usize) -> Option<&SearchHit> {
        match self {
            RowItems::Seeds(_) => None,
            RowItems::Hits(hits) => Some(&hits[i]),
        }
    }
}

fn row_look(items: &RowItems, i: usize, ctx: &RowListCtx) -> RowLook {
    let seed = items.seed(i);
    let hit = items.hit(i);
    let parent = model::parent_of(seed);
    let home = (hit.is_some() && parent != ctx.trail_last)
        .then(|| {
            hit.and_then(|h| h.plot_title.clone())
                .or_else(|| ctx.index.by_id.get(parent).map(|p| p.title.clone()))
        })
        .flatten();
    let v2_row = ctx.v2.on && ctx.pick != Pick::Found;
    let short_age = if v2_row && seed.plot_progress.is_none() {
        let age_iso = if seed.updated_at.is_empty() {
            &seed.created_at
        } else {
            &seed.updated_at
        };
        model::short_age(age_iso, ctx.now_ms)
    } else {
        String::new()
    };
    RowLook {
        seed: Arc::as_ptr(seed) as usize,
        blockers: if v2_row {
            0
        } else {
            ctx.index.blockers(&seed.id)
        },
        selected: seed.id == ctx.selected,
        active: ctx.active == Some(i),
        pick: ctx.pick,
        title_ranges: hit.map(|h| h.title_ranges.clone()).unwrap_or_default(),
        snippet: hit.and_then(|h| h.snippet.clone()),
        where_log: hit.is_some_and(|h| h.where_ == "log"),
        id_hit: hit.is_some_and(|h| h.id_hit),
        home,
        tender: if v2_row {
            String::new()
        } else {
            model::tender_of(seed, &ctx.names)
        },
        standby: model::standby_of(seed, ctx.now_ms),
        v2: ctx.v2.on,
        attention: ctx.v2.attention_of(seed),
        show_bed: ctx.show_bed,
        short_age,
    }
}

fn list(
    (key, role, label): (&str, &'static str, &str),
    items: RowItems,
    place: &Place,
    pick: Pick,
    selected: &str,
    active: Option<usize>,
    t: &Tokens,
) -> El {
    let keys = items.clone();
    let ctx = Rc::new(RowListCtx {
        index: place.index.clone(),
        names: place.names.clone(),
        v2: place.v2.clone(),
        now_ms: place.now_ms,
        pick,
        selected: selected.to_owned(),
        active,
        trail_last: place.trail.last().cloned().unwrap_or_default(),
        show_bed: place.v2.on && place.v2.bed == BedFilter::All,
    });
    let t = *t;
    col()
        .key(key.to_owned())
        .role(role)
        .label(label.to_owned())
        .self_stretch()
        .growth(GrowthSpec::default())
        .virtual_rows(
            items.len(),
            ListRow::two_line_height(),
            move |_: &State, i| row_key(&keys.seed(i).id).into(),
            move |_: &State, i| {
                let look = row_look(&items, i, &ctx);
                seed_row(items.seed(i), &look, &t)
            },
        )
}

/// A seed's row: its title, then its facts. A search answer's facts wrap whole, so its plot tells
/// it apart, and the passage of its body that answered stands under them inside the same option.
/// The passage is not a List row's lower block, which lies outside the row's press target and
/// selected fill and needs a List, where these rows are built virtually as they scroll in.
fn seed_row(seed: &Arc<Seed>, look: &RowLook, t: &Tokens) -> El {
    if look.v2 && look.pick != Pick::Found {
        let v2_look = V2RowLook {
            seed: look.seed,
            selected: look.selected,
            pick: look.pick,
            attention: look.attention,
            standby: look.standby.clone(),
            show_bed: look.show_bed,
            short_age: look.short_age.clone(),
        };
        return seed_row_v2(seed, &v2_look, t);
    }
    let key = row_key(&seed.id);
    memo(key.clone(), (look.clone(), *t), || {
        ROWS_BUILT.with(|n| n.set(n.get() + 1));
        let closed = model::is_closed(seed);
        let armed = model::harvest_when(seed);
        let ink = if closed && !look.selected {
            t.text_tertiary
        } else {
            t.text_primary
        };
        let title = marked(&seed.title, &look.title_ranges, Type::Name, ink, t.brand)
            .wrap()
            .w_full()
            .key(format!("{key}.title"));
        let mut row = ListRow::new(key.clone(), seed.title.clone())
            .wrap()
            .lead(
                dot(
                    state_color(&seed.status, t),
                    DotShape::Filled,
                    DotSize::Regular,
                )
                .a11y_hidden(),
            )
            .title_el(title)
            .selected(look.selected || look.active);
        if let Some(standby) = &look.standby {
            row = row.aside(
                words(
                    standby.word(),
                    Type::Small,
                    if standby.resume_blocked.is_empty() {
                        t.brand
                    } else {
                        t.warning
                    },
                )
                .key(format!("{key}.standby"))
                .help(standby.details()),
            );
        } else if let Some((signal, tone)) = model::signal(seed, look.blockers)
            && !(armed.is_some() && signal == "parked")
        {
            row = row.aside(words(signal, Type::Small, tone_color(tone, t)));
        }
        if let Some((sentence, marker)) = &armed {
            row = row
                .aside(words(marker.clone(), Type::Small, t.text_tertiary).help(sentence.clone()));
        }
        let found = look.pick == Pick::Found;
        let mut facts = Byline::new().id(
            seed.id.clone(),
            if look.id_hit {
                t.brand
            } else {
                t.text_tertiary
            },
        );
        let mut say = |fact: String| {
            facts = if found {
                std::mem::take(&mut facts).push(
                    words(fact, Type::Small, t.text_tertiary)
                        .wrap()
                        .shrink()
                        .min_w(0.0),
                )
            } else {
                std::mem::take(&mut facts).said(fact, t)
            };
        };
        if seed.standby.is_some() && look.blockers > 0 {
            say(format!("blocked by {}", look.blockers));
        }
        if !look.tender.is_empty() {
            say(format!("tended by {}", look.tender));
        }
        if let Some(home) = &look.home {
            say(format!("in {home}"));
        }
        if look.where_log {
            say("in log".to_owned());
        }
        if let Some(tally) = model::check_tally(seed) {
            say(model::tally_words(&tally));
        }
        let line = if found {
            facts.wrap().build(t)
        } else {
            facts.build(t)
        };
        row = row.subtitle_el(match &look.snippet {
            Some(snippet) => Stack::tight().w_full().min_w(0.0).child(line).child(
                marked(
                    &snippet.text,
                    &snippet.ranges,
                    Type::Small,
                    t.text_tertiary,
                    t.brand,
                )
                .key(format!("{key}.excerpt"))
                .wrap()
                .w_full(),
            ),
            None => line,
        });
        if let Some(progress) = &seed.plot_progress {
            row = row
                .aside(words(
                    format!("{}/{}", progress.done, progress.total),
                    Type::Small,
                    t.text_tertiary,
                ))
                .aside(words("›", Type::Small, t.text_tertiary).a11y_hidden());
        }
        let id = seed.id.clone();
        let pick = look.pick;
        if found {
            row = row.description(match &look.home {
                Some(home) => format!("{} in {home}", seed.id),
                None => seed.id.clone(),
            });
        }
        row.role(if found { "option" } else { "button" })
            .build(t)
            .a11y_value(seed.status.clone())
            .focus_selected(found && look.active)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| match pick {
                Pick::Drill => super::drill(state, &id, tx),
                Pick::Level(level) => super::select_at(state, level, &id, tx),
                Pick::Found => super::open_found(state, &id, tx),
            })
    })
}

#[derive(Clone, PartialEq, Eq, Hash)]
struct V2RowLook {
    seed: usize,
    selected: bool,
    pick: Pick,
    attention: Attention,
    standby: Option<model::Standby>,
    show_bed: bool,
    short_age: String,
}

fn seed_row_v2(seed: &Arc<Seed>, look: &V2RowLook, t: &Tokens) -> El {
    let key = row_key(&seed.id);
    memo(key.clone(), (look.clone(), *t), || {
        ROWS_BUILT.with(|n| n.set(n.get() + 1));
        let closed = model::is_closed(seed);
        let (dot_color, dot_shape) = look.attention.dot_look(t);
        let pulse = seed
            .pulse
            .as_deref()
            .map(str::trim)
            .filter(|p| !p.is_empty());
        let subtitle_el = if let Some(pulse) = pulse {
            words(pulse.to_owned(), Type::Small, t.text_tertiary)
                .key(format!("{key}.pulse"))
                .single_line()
                .truncate()
        } else if let Some(standby) = &look.standby {
            let ink = if standby.resume_blocked.is_empty() {
                t.text_tertiary
            } else {
                t.warning
            };
            words(standby.waits(), Type::Small, ink)
                .key(format!("{key}.standby"))
                .help(standby.details())
                .single_line()
                .truncate()
        } else if look.attention == Attention::Quiet {
            words("No live agent", Type::Small, t.text_tertiary)
                .single_line()
                .truncate()
        } else {
            let fallback = if look.show_bed {
                match seed.bed.as_deref().filter(|b| !b.is_empty()) {
                    Some(bed) => format!("{bed} · {}", seed.id),
                    None => seed.id.clone(),
                }
            } else {
                seed.id.clone()
            };
            words(fallback, Type::Mono, t.text_tertiary)
                .single_line()
                .truncate()
        };
        let mut row = ListRow::new(key.clone(), seed.title.clone())
            .lead(dot(dot_color, dot_shape, DotSize::Small).a11y_hidden())
            .title(seed.title.clone())
            .dimmed(closed && !look.selected)
            .selected(look.selected)
            .subtitle_el(subtitle_el);
        if let Some(progress) = &seed.plot_progress {
            row = row
                .aside(words(
                    format!("{}/{}", progress.done, progress.total),
                    Type::Mono,
                    t.text_tertiary,
                ))
                .aside(words("›", Type::Small, t.text_tertiary).a11y_hidden());
        } else if !look.short_age.is_empty() {
            row = row.aside(words(look.short_age.clone(), Type::Mono, t.text_muted));
        }
        let id = seed.id.clone();
        let pick = look.pick;
        row.role("button")
            .build(t)
            .a11y_value(seed.status.clone())
            .on_press_tx(move |state: &mut State, tx: &mut Tx| match pick {
                Pick::Level(level) => super::select_at(state, level, &id, tx),
                Pick::Drill | Pick::Found => super::drill(state, &id, tx),
            })
    })
}

pub(super) fn state_color(status: &str, t: &Tokens) -> rune::Color {
    match status {
        "planted" => t.accent,
        "growing" => t.brand,
        "harvested" => t.text_tertiary,
        "withered" => t.error,
        "parked" => t.warning,
        _ => t.text_muted,
    }
}

fn tone_color(tone: &str, t: &Tokens) -> rune::Color {
    match tone {
        "active" => t.brand,
        "parked" => t.warning,
        "blocked" | "closed" => t.text_muted,
        _ => t.text_tertiary,
    }
}

pub(super) fn page_tail(key: PageKey, bucket: Option<&PageBucket>, t: &Tokens) -> Option<El> {
    let bucket = bucket?;
    if bucket.loading_more || (bucket.loading && bucket.seeds.is_empty()) {
        return Some(Prose::empty("garden.more.loading", "Loading older seeds…").el(t));
    }
    if let Some(error) = &bucket.error {
        let retry_key = key.clone();
        return Some(
            Inline::loose()
                .key("garden.more.error")
                .child(words(error.clone(), Type::Small, t.error).wrap())
                .child(
                    quiet("Retry")
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            super::retry_page(state, retry_key.clone(), tx)
                        })
                        .build(t)
                        .key("garden.more.retry")
                        .into(),
                ),
        );
    }
    let next = bucket.next_before.as_deref()?;
    let press_key = key.clone();
    let vis_key = key;
    let load: El = quiet("Load older seeds")
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::load_more_page(state, press_key.clone(), tx);
        })
        .build(t)
        .key("garden.more")
        .into();
    Some(
        col()
            .key(format!("garden.more.sentinel.{next}"))
            .w_full()
            .on_visible(move |state: &mut State, shown: bool, tx: &mut Tx| {
                if !shown || !super::load_more_page(state, vis_key.clone(), tx) {
                    tx.live_only();
                }
            })
            .child(load),
    )
}

fn search_tail(place: &Place, t: &Tokens) -> Option<El> {
    if place.search_loading_more {
        return Some(Prose::empty("garden.search.more.loading", "Loading more results…").el(t));
    }
    if let Some(error) = &place.search_error {
        return Some(
            Inline::loose()
                .key("garden.search.more.error")
                .child(words(error.clone(), Type::Small, t.error).wrap())
                .child(
                    quiet("Retry")
                        .on_press_tx(super::retry_search)
                        .build(t)
                        .key("garden.search.more.retry")
                        .into(),
                ),
        );
    }
    let offset = place.search_next_offset?;
    let load: El = quiet("Load more results")
        .on_press_tx(|state: &mut State, tx: &mut Tx| {
            super::load_more_search(state, tx);
        })
        .build(t)
        .key("garden.search.more")
        .into();
    Some(
        col()
            .key(format!("garden.search.more.sentinel.{offset}"))
            .w_full()
            .on_visible(|state: &mut State, shown: bool, tx: &mut Tx| {
                if !shown || !super::load_more_search(state, tx) {
                    tx.live_only();
                }
            })
            .child(load),
    )
}

fn nothing_found(state: &State, place: &Place, t: &Tokens) -> El {
    let plot = !place.trail.is_empty();
    let where_ = if plot && !place.wide {
        "this plot"
    } else {
        "the garden"
    };
    let query = state.window.garden.query.text.trim().to_owned();
    let mut moves = Actions::new("garden.moves");
    if plot && !place.wide && place.outside > 0 {
        moves = moves.push(
            button(
                "garden.nothing.widen",
                format!("Search the whole garden {}", place.outside),
                t,
            )
            .on_press_tx(super::toggle_wide),
        );
    }
    if plot && place.wide {
        moves = moves.push(
            button(
                "garden.nothing.narrow",
                format!("Search this plot only {}", place.in_plot),
                t,
            )
            .on_press_tx(super::toggle_wide),
        );
    }
    if place.hidden_closed > 0 {
        moves = moves.push(
            button(
                "garden.nothing.closed",
                format!("Include closed seeds {}", place.hidden_closed),
                t,
            )
            .on_press_tx(super::toggle_closed),
        );
    }
    moves = moves.push(
        button("garden.nothing.clear", "Clear the search", t).on_press_tx(
            |state: &mut State, tx: &mut Tx| super::set_query(state, String::new(), tx),
        ),
    );
    Reader::new()
        .push(
            Prose::empty(
                "garden.nothing.line",
                format!("Nothing in {where_} matches {query}."),
            )
            .build(t),
        )
        .push(moves.build().key("garden.nothing.moves"))
        .build("garden.nothing", Room::Narrow, t)
}

fn results(state: &State, place: &Place, t: &Tokens) -> El {
    if place.answers.is_empty() {
        if place.search_loading {
            return Prose::empty("garden.search.loading", "Searching…").el(t);
        }
        if let Some(error) = &place.search_error {
            return Reader::new()
                .push(Prose::empty("garden.search.error", error.clone()).build(t))
                .push(
                    Actions::new("garden.search.error.moves")
                        .push(
                            button("garden.search.retry", "Retry", t)
                                .on_press_tx(super::retry_search),
                        )
                        .build()
                        .key("garden.search.error.actions"),
                )
                .build("garden.search.failed", Room::Narrow, t);
        }
        return nothing_found(state, place, t);
    }
    let active = place.active(state).min(place.answers.len() - 1);
    let list_el = list(
        (RESULTS, "listbox", "Results"),
        RowItems::Hits(place.answers.clone()),
        place,
        Pick::Found,
        "",
        Some(active),
        t,
    );
    match search_tail(place, t) {
        Some(tail) => col().w_full().child(list_el).child(tail),
        None => list_el,
    }
}

pub(super) fn scroll(key: &str, content: El, t: &Tokens) -> El {
    col()
        .key(key.to_owned())
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .scroll(ScrollSpec {
            stick_bottom: false,
        })
        .child(content)
        .scroll_bar_styled(attn_ui::scroll_bar(t))
}

fn root_list(state: &State, place: &Place, pick: Pick, t: &Tokens) -> Vec<Block> {
    if place.v2.on {
        return v2_root_list(state, place, pick, t);
    }
    let roots = place.v1_roots(state);
    let closed = place.root_closed_count;
    let stacked = pick == Pick::Drill;
    let bucket = place
        .shows_closed_page()
        .then(|| state.window.garden.pages.get(&place.root_page_key))
        .flatten();
    let mut page: Vec<Block> = Vec::new();
    if roots.is_empty() && bucket.is_some_and(|b| b.loading || b.error.is_some()) {
        if let Some(tail) = page_tail(place.root_page_key.clone(), bucket, t) {
            page.push(Block::custom("garden.more.block", tail));
        }
        return page;
    } else if roots.is_empty() && closed > 0 && !place.shows_closed_page() {
        page.push(Prose::empty("garden.empty", hidden_closed_words(closed)).build(t));
    } else if roots.is_empty() && stacked {
        page.push(
            Prose::empty(
                "garden.empty",
                "The garden is empty. `attn seed plant \"what this is\"` puts something in it.",
            )
            .build(t),
        );
    } else if roots.is_empty() {
        page.push(Prose::empty("garden.empty", "Nothing here yet.").build(t));
    } else {
        let selected = place.trail.first().map_or("", String::as_str);
        page.push(Block::custom(
            "garden.list.block",
            list(
                ("garden.list", "list", "Seeds"),
                RowItems::Seeds(roots.into()),
                place,
                pick,
                selected,
                None,
                t,
            ),
        ));
    }
    if let Some(tail) = page_tail(place.root_page_key.clone(), bucket, t) {
        page.push(Block::custom("garden.more.block", tail));
    }
    page
}

fn hidden_closed_words(closed: usize) -> String {
    format!(
        "Nothing open here. {closed} closed {} behind the closed toggle above.",
        if closed == 1 { "seed is" } else { "seeds are" }
    )
}

enum V2Line {
    Head {
        key: &'static str,
        label: &'static str,
        count: usize,
    },
    Seed {
        seed: Arc<Seed>,
        attention: Attention,
    },
}

impl V2Line {
    fn key(&self) -> String {
        match self {
            V2Line::Head { key, .. } => (*key).to_owned(),
            V2Line::Seed { seed, .. } => row_key(&seed.id),
        }
    }
}

struct V2ListCtx {
    pick: Pick,
    selected: String,
    show_bed: bool,
    now_ms: i64,
}

fn build_v2_line(line: &V2Line, ctx: &V2ListCtx, t: &Tokens) -> El {
    match line {
        V2Line::Head { key, label, count } => GroupHead::new(*key, *label)
            .section(*label)
            .heading()
            .aside(words(count.to_string(), Type::Mono, t.text_muted))
            .build(t),
        V2Line::Seed { seed, attention } => {
            let has_pulse = seed.pulse.as_deref().is_some_and(|p| !p.trim().is_empty());
            let standby = (!has_pulse)
                .then(|| model::standby_of(seed, ctx.now_ms))
                .flatten();
            let short_age = if seed.plot_progress.is_none() {
                let age_iso = if seed.updated_at.is_empty() {
                    &seed.created_at
                } else {
                    &seed.updated_at
                };
                model::short_age(age_iso, ctx.now_ms)
            } else {
                String::new()
            };
            let look = V2RowLook {
                seed: Arc::as_ptr(seed) as usize,
                selected: seed.id == ctx.selected,
                pick: ctx.pick,
                attention: *attention,
                standby,
                show_bed: ctx.show_bed,
                short_age,
            };
            seed_row_v2(seed, &look, t)
        }
    }
}

fn v2_root_list(state: &State, place: &Place, pick: Pick, t: &Tokens) -> Vec<Block> {
    let docked = super::mode(state) != Mode::Full;
    let roots = place.v2_roots(state, docked);
    let open_in_bed = place.v2.counts.open_for(&place.v2.bed);
    let closed_in_bed = place.root_closed_count;
    let bucket = place
        .shows_closed_page()
        .then(|| state.window.garden.pages.get(&place.root_page_key))
        .flatten();
    let mut page: Vec<Block> = Vec::new();
    if roots.is_empty() {
        if bucket.is_some_and(|b| b.loading || b.error.is_some()) {
            if let Some(tail) = page_tail(place.root_page_key.clone(), bucket, t) {
                page.push(Block::custom("garden.more.block", tail));
            }
            return page;
        } else if open_in_bed > 0 && place.lens != Lens::All && place.query.is.is_empty() {
            let bed_name = v2::bed_label(state, &place.v2.bed);
            let msg = if place.v2.bed == BedFilter::All {
                format!(
                    "Nothing in {}. {open_in_bed} open in the garden.",
                    place.lens.label()
                )
            } else {
                format!(
                    "Nothing in {} for {bed_name}. {open_in_bed} open here.",
                    place.lens.label()
                )
            };
            page.push(Prose::empty("garden.empty", msg).build(t));
            page.push(
                Actions::new("garden.v2.empty.actions")
                    .push(
                        plain("Show all open")
                            .on_press_tx(|state: &mut State, tx: &mut Tx| {
                                v2::select_lens(state, Lens::All, tx)
                            })
                            .build(t)
                            .key("garden.v2.show-all"),
                    )
                    .build(),
            );
        } else if closed_in_bed > 0 && !place.shows_closed_page() {
            page.push(Prose::empty("garden.empty", hidden_closed_words(closed_in_bed)).build(t));
        } else if place.v2.bed == BedFilter::All {
            page.push(
                Prose::empty(
                    "garden.empty",
                    "The garden is empty. `attn seed plant \"what this is\"` puts something in it.",
                )
                .build(t),
            );
        } else {
            page.push(Prose::empty("garden.empty", "Nothing here yet.").build(t));
        }
        if let Some(tail) = page_tail(place.root_page_key.clone(), bucket, t) {
            page.push(Block::custom("garden.more.block", tail));
        }
        return page;
    }

    let waiting_count = roots
        .iter()
        .take_while(|s| place.attention_of(s).is_waiting())
        .count();
    let rest_count = roots.len() - waiting_count;
    let mut lines = Vec::with_capacity(roots.len() + 2);
    if waiting_count > 0 {
        lines.push(V2Line::Head {
            key: "garden.group.waiting",
            label: "Waiting on you",
            count: waiting_count,
        });
        for seed in &roots[..waiting_count] {
            lines.push(V2Line::Seed {
                attention: place.attention_of(seed),
                seed: seed.clone(),
            });
        }
    }
    if rest_count > 0 {
        let rest_label = if !place.query.is.is_empty() {
            "Matching"
        } else if docked {
            if place.lens == Lens::Now {
                "In flight"
            } else {
                "Open"
            }
        } else {
            match place.lens {
                Lens::Now => "In flight",
                Lens::Triage => "Needs a decision",
                Lens::All => "Open",
            }
        };
        lines.push(V2Line::Head {
            key: "garden.group.rest",
            label: rest_label,
            count: rest_count,
        });
        for seed in &roots[waiting_count..] {
            lines.push(V2Line::Seed {
                attention: place.attention_of(seed),
                seed: seed.clone(),
            });
        }
    }
    let lines: Rc<[V2Line]> = lines.into();
    let keys = lines.clone();
    let ctx = Rc::new(V2ListCtx {
        pick,
        selected: place.trail.first().cloned().unwrap_or_default(),
        show_bed: place.v2.bed == BedFilter::All,
        now_ms: place.now_ms,
    });
    let t = *t;
    let list_el = col()
        .key("garden.list")
        .role("list")
        .label("Seeds")
        .self_stretch()
        .growth(GrowthSpec::default())
        .virtual_rows(
            lines.len(),
            ListRow::two_line_height(),
            move |_: &State, i| keys[i].key().into(),
            move |_: &State, i| build_v2_line(&lines[i], &ctx, &t),
        );
    page.push(Block::custom("garden.list.block", list_el));
    if let Some(tail) = page_tail(place.root_page_key.clone(), bucket, &t) {
        page.push(Block::custom("garden.more.block", tail));
    }
    page
}

fn loading_page(state: &State, id: &str, t: &Tokens) -> Reader {
    let (_, error) = super::reading(&state.window.garden, id);
    let words = error.unwrap_or("Loading seed…").to_owned();
    Reader::new().push(Prose::empty("garden.loading", words).build(t))
}

fn stack_view(state: &State, place: &Place, t: &Tokens) -> El {
    let content = if place.query.searches {
        Reader::new().push(Block::custom(
            "garden.results.block",
            results(state, place, t),
        ))
    } else {
        match (place.here(), place.trail.last()) {
            (Some(here), _) => page(state, place, here, true, t),
            (None, Some(id)) => loading_page(state, id, t),
            (None, None) => Reader::new().push_all(root_list(state, place, Pick::Drill, t)),
        }
    };
    let bar = (place.here())
        .filter(|_| !place.query.searches)
        .and_then(|here| {
            reading::find_bar(
                &state.readings,
                reading::VIEWPORT,
                (&here.id, &here.body),
                t,
            )
        });
    scroll(
        reading::VIEWPORT,
        content.build("garden.page", Room::Narrow, t),
        t,
    )
    .children(bar)
}

/// The open seed's page beside the columns, which takes the keyboard on a press anywhere in it
/// that leaves the focus outside it.
fn reader_pane(state: &State, place: &Place, page: El, t: &Tokens) -> El {
    let bar = (place.here()).and_then(|here| {
        reading::find_bar(&state.readings, reading::READER, (&here.id, &here.body), t)
    });
    col()
        .key(reading::READER)
        .focusable(true)
        .on_press_within(|_: &mut State, tx: &mut Tx| {
            if !tx.focus_within() {
                tx.focus(reading::READER, false);
            }
        })
        .grow(1.0)
        .h_full()
        .min_h(0.0)
        .items_center()
        .scroll(ScrollSpec {
            stick_bottom: false,
        })
        .child(page.max_w(READER_MAX_WIDTH))
        .scroll_bar_styled(attn_ui::thin_scroll_bar(t))
        .children(bar)
}

fn columns_view(state: &State, place: &Place, t: &Tokens) -> El {
    let mut columns = row().key("garden.columns").w_full().grow(1.0).min_h(0.0);
    let column = |key: String, content: El| Pane::new(key, content).width(COLUMN_WIDTH).build(t);
    if place.v2.on {
        columns = columns.child(v2::beds_rail(state, &place.v2.counts, t));
        if place.query.searches {
            columns = columns.child(column(
                "garden.column.results".into(),
                col().w_full().child(results(state, place, t)),
            ));
        } else {
            columns = columns.child(column(
                "garden.column.0".into(),
                blocks_column(root_list(state, place, Pick::Level(0), t), t),
            ));
        }
        let reader_inner = match (place.here(), place.trail.last()) {
            (Some(here), _) => {
                page(state, place, here, false, t).build("garden.page", Room::Wide, t)
            }
            (None, Some(id)) => loading_page(state, id, t).build("garden.page", Room::Wide, t),
            (None, None) => Reader::new()
                .push(Prose::empty("garden.reader.empty", "Select a seed to read it.").build(t))
                .build("garden.page", Room::Wide, t),
        };
        return columns.child(reader_pane(state, place, reader_inner, t));
    }
    if place.query.searches {
        columns = columns.child(column(
            "garden.column.results".into(),
            col().w_full().child(results(state, place, t)),
        ));
    } else if place.trail.is_empty() {
        let root =
            blocks_column(root_list(state, place, Pick::Level(0), t), t).max_w(ROOT_MAX_WIDTH);
        return columns.child(
            Pane::new("garden.column.0", col().w_full().items_center().child(root)).build(t),
        );
    } else {
        let levels = place.levels(state);
        let first = visible_column_start(state, levels.len());
        for (level, (seeds, selected, page_key)) in levels.iter().enumerate().skip(first) {
            let content = if level == 0 {
                blocks_column(root_list(state, place, Pick::Level(0), t), t)
            } else {
                let bucket = page_key
                    .as_ref()
                    .and_then(|k| state.window.garden.pages.get(k));
                let tail = page_key.clone().and_then(|k| page_tail(k, bucket, t));
                if seeds.is_empty() {
                    if bucket.is_some_and(|b| b.loading || b.error.is_some())
                        && let Some(tail) = tail
                    {
                        tail
                    } else {
                        let closed = place
                            .trail
                            .get(level - 1)
                            .and_then(|id| super::lookup_seed(state, id))
                            .map_or(0, |plot| place.plot_closed_count(state, &plot));
                        let words = if closed > 0 && !place.shows_closed_page() {
                            hidden_closed_words(closed)
                        } else {
                            "Nothing here yet.".to_owned()
                        };
                        let empty = Prose::empty("garden.column.empty", words).el(t);
                        match tail {
                            Some(tail) => col().w_full().child(empty).child(tail),
                            None => empty,
                        }
                    }
                } else {
                    let list_el = list(
                        (&format!("garden.list.{level}"), "list", "Seeds"),
                        RowItems::Seeds(seeds.clone().into()),
                        place,
                        Pick::Level(level),
                        selected,
                        None,
                        t,
                    );
                    match tail {
                        Some(tail) => col().w_full().child(list_el).child(tail),
                        None => list_el,
                    }
                }
            };
            columns = columns.child(column(format!("garden.column.{level}"), content));
        }
    }
    if let Some(here) = place.here() {
        let page = page(state, place, here, false, t).build("garden.page", Room::Wide, t);
        columns = columns.child(reader_pane(state, place, page, t));
    } else if let Some(id) = place.trail.last() {
        let page = loading_page(state, id, t).build("garden.page", Room::Wide, t);
        columns = columns.child(reader_pane(state, place, page, t));
    }
    columns
}

/// Blocks one under another as a page spaces them, inside a pane that insets them itself.
fn blocks_column(blocks: Vec<Block>, t: &Tokens) -> El {
    Reader::new().push_all(blocks).bare(t)
}

/// The opened seed: its state and who tends it, why it closed, its plot's progress, its plan,
/// its checks, its plot's children while the walk stacks, its relations, artifacts and log.
fn page(state: &State, place: &Place, here: &Arc<Seed>, stacked: bool, t: &Tokens) -> Reader {
    let (document, error) = super::reading(&state.window.garden, &here.id);
    let in_live = place.index.by_id.contains_key(&here.id);
    let shown = if in_live {
        here.as_ref()
    } else {
        document.map(|d| &d.seed).unwrap_or(here.as_ref())
    };
    let sheet = Sheet {
        prefix: "garden".into(),
        view: if stacked {
            reading::VIEWPORT
        } else {
            reading::READER
        }
        .into(),
        go: Go::Garden,
        names: &place.names,
        garden: &state.window.garden,
        seeds: super::seeds(state),
        terminals: &state.terminals,
        facts: &state.facts,
        v2: place.v2.on,
        chief: model::has_chief(&state.facts.sessions, &state.facts.profiles.selected),
        readings: &state.readings,
        pictures: &state.pictures,
        drafts: None,
        now_ms: state.window.now_ms,
        harnesses: &state.facts.harnesses,
        t,
    };
    let mut actions = vec![open_as_tile(&shown.id, t)];
    actions.extend(sheet.continuation(Some(shown), document));
    let mut page = Reader::new()
        .head(sheet.head(shown, actions, sheet.continuation_notes(shown, document)))
        .push_all(sheet.head_after(shown, &place.index, Some(place.attention_of(shown))))
        .push_all(sheet.handover_form(document))
        .push_all(sheet.chief_form(document))
        .push(sheet.authorization(shown))
        .push(sheet.plan(shown))
        .push_all(sheet.checks(shown, document));
    if (stacked || place.v2.on) && model::is_plot(shown) {
        page = page.push(plot_children(state, place, shown, t));
    }
    let cached = super::reading_seeds(&state.window.garden, &here.id);
    let related = place.index.relations_with(
        shown,
        cached.and_then(|c| c.related),
        cached.and_then(|c| c.ancestors),
    );
    page.push_all(sheet.seeds("related", "Related", related))
        .push_all(sheet.artifacts(document))
        .push(sheet.log(document, error))
}

fn open_as_tile(id: &str, t: &Tokens) -> El {
    let id = id.to_owned();
    plain("Open as tile")
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            super::close(state);
            super::open_tile(state, &id, tx)
        })
        .build(t)
        .key("garden.open-tile")
        .into()
}

fn plot_children(state: &State, place: &Place, here: &Seed, t: &Tokens) -> Block {
    let open = place.children_of(state, &here.id);
    let closed = place.plot_closed_count(state, here);
    let plot_key = PageKey::plot(state, &here.id);
    let bucket = place
        .shows_closed_page()
        .then(|| state.window.garden.pages.get(&plot_key))
        .flatten();
    let plot = Section::new("garden.plot", "Plot").region("Plot");
    if open.is_empty()
        && bucket.is_some_and(|b| b.loading || b.error.is_some())
        && let Some(tail) = page_tail(plot_key.clone(), bucket, t)
    {
        return plot
            .push(Block::custom("garden.plot.more.block", tail))
            .build(t);
    }
    if open.is_empty() && closed > 0 && !place.shows_closed_page() {
        return plot
            .push(Prose::empty("garden.plot.empty", hidden_closed_words(closed)).build(t))
            .build(t);
    }
    if open.is_empty() {
        let command = format!("attn seed plant \"what this is\" --part-of {}", here.id);
        return plot
            .push(
                Prose::empty(
                    "garden.plot.empty",
                    Said::parts([
                        Part::Text("Nothing planted in this plot yet. ".to_owned()),
                        Part::Code(command),
                        Part::Text(" puts something in it.".to_owned()),
                    ]),
                )
                .build(t),
            )
            .build(t);
    }
    let mut section = plot.push(Block::custom(
        "garden.plot.list.block",
        list(
            ("garden.plot.list", "list", "Plot"),
            RowItems::Seeds(open.into()),
            place,
            Pick::Drill,
            "",
            None,
            t,
        ),
    ));
    if let Some(tail) = page_tail(plot_key, bucket, t) {
        section = section.push(Block::custom("garden.plot.more.block", tail));
    }
    section.build(t)
}
