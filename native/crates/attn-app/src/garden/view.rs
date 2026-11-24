use std::cell::Cell;
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
use super::search::{self, Lookups, Match, Partial, Query};
use super::sheet::{Go, Sheet};
use super::v2::{self, Attention, BedFilter, Lens};
use super::{BODY, Mode, SEARCH_FIELD};
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

pub(super) fn row_key(id: &str) -> String {
    format!("{ROW_PREFIX}{id}")
}

pub(super) fn index(state: &State) -> Index {
    Index::of(super::seeds(state))
}

struct Look<'a> {
    index: &'a Index,
    names: &'a SessionNames,
    beds: &'a [attn_protocol::GardenBed],
}

impl Lookups for Look<'_> {
    fn tender_of(&self, seed: &Seed) -> String {
        model::tender_of(seed, self.names)
    }
    fn blockers_of(&self, seed: &Seed) -> usize {
        self.index.blockers(&seed.id)
    }
    fn bed_slug_of(&self, seed: &Seed) -> Option<&str> {
        let id = seed.bed_id.as_deref()?;
        self.beds
            .iter()
            .find(|b| b.id == id)
            .map(|b| b.slug.as_str())
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

/// What the garden shows, worked out once per view and per key.
struct Place {
    index: Index,
    names: SessionNames,
    trail: Vec<String>,
    query: Query,
    wide: bool,
    answers: Vec<Match>,
    /// Matches in the whole garden that the plot's search leaves out.
    outside: usize,
    /// Matches in the plot while the search stands wide.
    in_plot: usize,
    hidden_closed: usize,
    now_ms: i64,
    v2: v2::Snapshot,
    lens: Lens,
    has_beds: bool,
}

impl Place {
    fn of(state: &State) -> Place {
        let index = index(state);
        let names = model::session_names(&state.facts.sessions, &state.facts.harnesses);
        let trail = super::living_trail(state);
        let garden = &state.window.garden;
        let v2_snap = v2::Snapshot::of(state, &index);
        let query = search::parse(&garden.query.text, v2_snap.on);
        let lens = garden.lens;
        let has_beds = v2_snap.on && !v2::beds(state).is_empty();
        let plot = trail.last().cloned();
        let wide = query.searches && plot.is_some() && garden.wide_in == plot;
        let (mut answers, mut outside, mut in_plot, mut hidden_closed) = (Vec::new(), 0, 0, 0);
        if query.searches {
            let look = Look {
                index: &index,
                names: &names,
                beds: v2::beds(state),
            };
            let seeds = super::seeds(state);
            let bed_allows = |s: &Arc<Seed>| {
                if !v2_snap.on || !query.beds.is_empty() {
                    true
                } else {
                    v2::matches_bed_in(s, &v2_snap.bed, &index)
                }
            };
            let scoped_seeds: Vec<&Arc<Seed>> = seeds.iter().filter(|s| bed_allows(s)).collect();
            let in_subtree = plot.as_ref().map(|p| index.subtree(p));
            let plot_pool = || {
                scoped_seeds
                    .iter()
                    .copied()
                    .filter(|s| in_subtree.as_ref().is_none_or(|w| w.contains(&s.id)))
            };
            answers = if wide {
                search::search(scoped_seeds.iter().copied(), &query, &look)
            } else {
                search::search(plot_pool(), &query, &look)
            };
            if plot.is_some() && !wide {
                outside = search::search(scoped_seeds.iter().copied(), &query, &look)
                    .len()
                    .saturating_sub(answers.len());
            }
            if wide {
                in_plot = search::search(plot_pool(), &query, &look).len();
            }
            if query.is.is_empty() {
                let any = search::parse(&format!("{} is:any", garden.query.text), v2_snap.on);
                let with_closed = if wide {
                    search::search(scoped_seeds.iter().copied(), &any, &look).len()
                } else {
                    search::search(plot_pool(), &any, &look).len()
                };
                hidden_closed = with_closed.saturating_sub(answers.len());
            }
        }
        Place {
            index,
            names,
            trail,
            query,
            wide,
            answers,
            outside,
            in_plot,
            hidden_closed,
            now_ms: state.window.now_ms,
            v2: v2_snap,
            lens,
            has_beds,
        }
    }

    fn attention_of(&self, seed: &Seed) -> Attention {
        self.v2.attention_of(seed)
    }

    fn v2_roots(&self, docked: bool) -> Vec<Arc<Seed>> {
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
        out
    }

    fn v2_bed_closed_count(&self) -> usize {
        self.index
            .roots
            .iter()
            .filter(|s| v2::matches_bed(s, &self.v2.bed) && v2::is_hidden_closed(s))
            .count()
    }

    fn look(&self) -> Look<'_> {
        Look {
            index: &self.index,
            names: &self.names,
            beds: &[],
        }
    }

    fn here(&self) -> Option<&Arc<Seed>> {
        self.index.by_id.get(self.trail.last()?)
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
    fn levels(&self) -> Vec<(Vec<Arc<Seed>>, String)> {
        let mut levels = vec![(
            self.lens(&self.index.roots),
            self.trail.first().cloned().unwrap_or_default(),
        )];
        for depth in 0..self.trail.len() {
            let kids = self.index.children_of(&self.trail[depth]);
            if kids.is_empty() {
                break;
            }
            levels.push((
                self.lens(kids),
                self.trail.get(depth + 1).cloned().unwrap_or_default(),
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
            if let Some(focused_id) = focused
                && let Some(here) = place.here()
                && model::is_plot(here)
            {
                let children = place.lens(place.index.children_of(&here.id));
                if children.iter().any(|c| c.id == focused_id) {
                    return ids(&children);
                }
            }
            return ids(&place.v2_roots(docked));
        }
        return match place.here() {
            Some(here) => ids(&place.lens(place.index.children_of(&here.id))),
            None => ids(&place.v2_roots(docked)),
        };
    }
    if columns(state) {
        let levels = place.levels();
        let column = focused
            .and_then(|id| {
                levels
                    .iter()
                    .find(|(seeds, _)| seeds.iter().any(|s| s.id == id))
            })
            .or(levels.last());
        return column.map(|(seeds, _)| ids(seeds)).unwrap_or_default();
    }
    match place.here() {
        Some(here) => ids(&place.lens(place.index.children_of(&here.id))),
        None => ids(&place.lens(&place.index.roots)),
    }
}

/// What pressing the row of `id` does where it shows.
pub(super) fn press_row(state: &mut State, id: &str, tx: &mut Tx) {
    let place = Place::of(state);
    if place.query.searches {
        return super::open_found(state, id, tx);
    }
    if place.v2.on && columns(state) {
        if place.index.roots.iter().any(|s| s.id == id) {
            return super::select_at(state, 0, id, tx);
        }
        return super::drill(state, id, tx);
    }
    if columns(state) {
        let levels = place.levels();
        if let Some(level) = levels
            .iter()
            .position(|(seeds, _)| seeds.iter().any(|s| s.id == id))
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

pub(super) fn full_frame(_state: &State, t: &Tokens, content: El) -> El {
    let dialog = col()
        .key("garden.frame")
        .role("dialog")
        .label("The garden")
        .modal_tx(true, |state: &mut State, _: &mut Tx| super::close(state))
        .w_full()
        .h_full()
        .child(content);
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
    if let Some((count, on)) = closed_toggle(place, columns(state)) {
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

/// The closed toggle's count and whether it is on, or nothing when it has nothing to say.
fn closed_toggle(place: &Place, wide: bool) -> Option<(usize, bool)> {
    let on = place.query.is.contains(&"any");
    let other = place.query.is.iter().any(|v| *v != "any");
    let hidden_here = match place.here() {
        Some(here) => {
            let child_hidden = place
                .index
                .children_of(&here.id)
                .iter()
                .filter(|s| v2::is_hidden_closed(s))
                .count();
            if place.v2.on && wide {
                place.v2_bed_closed_count() + child_hidden
            } else {
                child_hidden
            }
        }
        None if place.v2.on => place.v2_bed_closed_count(),
        None => place
            .index
            .roots
            .iter()
            .filter(|s| v2::is_hidden_closed(s))
            .count(),
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
        let levels = place.levels().len();
        let shown = levels.min(if frame_width(state) >= THIRD_COLUMN_MIN {
            3
        } else {
            2
        });
        levels - shown
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
        let title = place
            .index
            .by_id
            .get(id)
            .map_or_else(|| id.clone(), |s| s.title.clone());
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
                        let slug = bed.slug.clone();
                        meta.push(
                            button(format!("garden.search.bed.{}", bed.id), bed.name.clone(), t)
                                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                                    let raw = state.window.garden.query.text.clone();
                                    let cut =
                                        raw.rfind("bed:").map_or(raw.as_str(), |at| &raw[..at]);
                                    super::set_query(state, format!("{cut}bed:{slug} "), tx);
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
    id_hit: bool,
    home: Option<String>,
    tender: String,
    standby: Option<model::Standby>,
    v2: bool,
    attention: Attention,
    show_bed: bool,
    short_age: String,
}

struct RowSpec {
    seed: Arc<Seed>,
    look: RowLook,
}

fn row_specs(
    seeds: &[Arc<Seed>],
    place: &Place,
    pick: Pick,
    selected: &str,
    matches: Option<(&[Match], usize)>,
) -> Vec<RowSpec> {
    let show_bed = place.v2.on && place.v2.bed == BedFilter::All;
    seeds
        .iter()
        .enumerate()
        .map(|(i, seed)| {
            let hit = matches.and_then(|(m, _)| m.get(i));
            let parent = model::parent_of(seed);
            let home = (matches.is_some()
                && parent != place.trail.last().map_or("", String::as_str))
            .then(|| place.index.by_id.get(parent).map(|p| p.title.clone()))
            .flatten();
            let v2_row = place.v2.on && pick != Pick::Found;
            let short_age = if v2_row && seed.plot_progress.is_none() {
                let age_iso = if seed.updated_at.is_empty() {
                    &seed.created_at
                } else {
                    &seed.updated_at
                };
                model::short_age(age_iso, place.now_ms)
            } else {
                String::new()
            };
            RowSpec {
                seed: seed.clone(),
                look: RowLook {
                    seed: Arc::as_ptr(seed) as usize,
                    blockers: if v2_row {
                        0
                    } else {
                        place.index.blockers(&seed.id)
                    },
                    selected: seed.id == selected,
                    active: matches.is_some_and(|(_, active)| active == i),
                    pick,
                    title_ranges: hit.map(|h| h.title_ranges.clone()).unwrap_or_default(),
                    snippet: hit.and_then(|h| h.snippet.clone()),
                    id_hit: hit.is_some_and(|h| h.id_hit),
                    home,
                    tender: if v2_row {
                        String::new()
                    } else {
                        model::tender_of(seed, &place.names)
                    },
                    standby: model::standby_of(seed, place.now_ms),
                    v2: place.v2.on,
                    attention: place.attention_of(seed),
                    show_bed,
                    short_age,
                },
            }
        })
        .collect()
}

fn list(key: &str, role: &'static str, label: &str, specs: Vec<RowSpec>, t: &Tokens) -> El {
    let specs: Rc<[RowSpec]> = specs.into();
    let keys = specs.clone();
    let t = *t;
    col()
        .key(key.to_owned())
        .role(role)
        .label(label.to_owned())
        .self_stretch()
        .growth(GrowthSpec::default())
        .virtual_rows(
            specs.len(),
            ListRow::two_line_height(),
            move |_: &State, i| row_key(&keys[i].seed.id).into(),
            move |_: &State, i| seed_row(&specs[i], &t),
        )
}

/// A seed's row: its title, then its facts. A search answer's facts wrap whole, so its plot tells
/// it apart, and the passage of its body that answered stands under them inside the same option.
/// The passage is not a List row's lower block, which lies outside the row's press target and
/// selected fill and needs a List, where these rows are built virtually as they scroll in.
fn seed_row(spec: &RowSpec, t: &Tokens) -> El {
    let seed = &spec.seed;
    let look = &spec.look;
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

fn capped(state: &State, place: &Place, t: &Tokens) -> Option<El> {
    let shown = super::seeds(state).len() as i64;
    let total = state.facts.garden.total_of(&state.facts.profiles.selected);
    (total > shown).then(|| {
        let words = if place.query.active {
            format!("Search covers {shown} of {total} seeds (every open seed and the newest closed).")
        } else {
            format!("The garden holds {total} seeds; this panel has {shown} (every open seed and the newest closed).")
        };
        Prose::empty("garden.capped", words).el(t)
    })
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
        return nothing_found(state, place, t);
    }
    let active = place.active(state).min(place.answers.len() - 1);
    let seeds: Vec<Arc<Seed>> = place.answers.iter().map(|m| m.seed.clone()).collect();
    let specs = row_specs(
        &seeds,
        place,
        Pick::Found,
        "",
        Some((&place.answers, active)),
    );
    list(RESULTS, "listbox", "Results", specs, t)
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
    let roots = place.lens(&place.index.roots);
    let closed = place
        .index
        .roots
        .iter()
        .filter(|s| model::is_closed(s))
        .count();
    let stacked = pick == Pick::Drill;
    let mut page: Vec<Block> = capped(state, place, t)
        .map(|capped| Block::custom("garden.capped.block", capped))
        .into_iter()
        .collect();
    if roots.is_empty() && closed > 0 {
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
                "garden.list",
                "list",
                "Seeds",
                row_specs(&roots, place, pick, selected, None),
                t,
            ),
        ));
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
    let roots = place.v2_roots(docked);
    let open_in_bed = place.v2.counts.open_for(&place.v2.bed);
    let closed_in_bed = place.v2_bed_closed_count();
    let mut page: Vec<Block> = capped(state, place, t)
        .map(|capped| Block::custom("garden.capped.block", capped))
        .into_iter()
        .collect();
    if roots.is_empty() {
        if open_in_bed > 0 && place.lens != Lens::All && place.query.is.is_empty() {
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
        } else if closed_in_bed > 0 {
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
    page
}

fn stack_view(state: &State, place: &Place, t: &Tokens) -> El {
    let content = if place.query.searches {
        Reader::new()
            .push_all(
                capped(state, place, t).map(|capped| Block::custom("garden.capped.block", capped)),
            )
            .push(Block::custom(
                "garden.results.block",
                results(state, place, t),
            ))
    } else {
        match place.here() {
            None => Reader::new().push_all(root_list(state, place, Pick::Drill, t)),
            Some(here) => page(state, place, here, true, t),
        }
    };
    scroll(
        "garden.viewport",
        content.build("garden.page", Room::Narrow, t),
        t,
    )
}

fn columns_view(state: &State, place: &Place, t: &Tokens) -> El {
    let mut columns = row().key("garden.columns").w_full().grow(1.0).min_h(0.0);
    let column = |key: String, content: El| Pane::new(key, content).width(COLUMN_WIDTH).build(t);
    if place.v2.on {
        columns = columns.child(v2::beds_rail(state, &place.v2.counts, t));
        if place.query.searches {
            columns = columns.child(column(
                "garden.column.results".into(),
                col()
                    .w_full()
                    .children(capped(state, place, t))
                    .child(results(state, place, t)),
            ));
        } else {
            columns = columns.child(column(
                "garden.column.0".into(),
                blocks_column(root_list(state, place, Pick::Level(0), t), t),
            ));
        }
        let reader_inner = match place.here() {
            Some(here) => page(state, place, here, false, t)
                .build("garden.page", Room::Wide, t)
                .max_w(READER_MAX_WIDTH),
            None => Reader::new()
                .push(Prose::empty("garden.reader.empty", "Select a seed to read it.").build(t))
                .build("garden.page", Room::Wide, t)
                .max_w(READER_MAX_WIDTH),
        };
        return columns.child(
            col()
                .key("garden.reader")
                .grow(1.0)
                .h_full()
                .min_h(0.0)
                .items_center()
                .scroll(ScrollSpec {
                    stick_bottom: false,
                })
                .child(reader_inner)
                .scroll_bar_styled(attn_ui::thin_scroll_bar(t)),
        );
    }
    if place.query.searches {
        columns = columns.child(column(
            "garden.column.results".into(),
            col()
                .w_full()
                .children(capped(state, place, t))
                .child(results(state, place, t)),
        ));
    } else if place.here().is_none() {
        let root =
            blocks_column(root_list(state, place, Pick::Level(0), t), t).max_w(ROOT_MAX_WIDTH);
        return columns.child(
            Pane::new("garden.column.0", col().w_full().items_center().child(root)).build(t),
        );
    } else {
        let levels = place.levels();
        let max = if frame_width(state) >= THIRD_COLUMN_MIN {
            3
        } else {
            2
        };
        let first = levels.len().saturating_sub(max);
        for (level, (seeds, selected)) in levels.iter().enumerate().skip(first) {
            let content = if level == 0 {
                blocks_column(root_list(state, place, Pick::Level(0), t), t)
            } else if seeds.is_empty() {
                Prose::empty("garden.column.empty", "Nothing here yet.").el(t)
            } else {
                list(
                    &format!("garden.list.{level}"),
                    "list",
                    "Seeds",
                    row_specs(seeds, place, Pick::Level(level), selected, None),
                    t,
                )
            };
            columns = columns.child(column(format!("garden.column.{level}"), content));
        }
    }
    if let Some(here) = place.here() {
        columns = columns.child(
            col()
                .key("garden.reader")
                .grow(1.0)
                .h_full()
                .min_h(0.0)
                .items_center()
                .scroll(ScrollSpec {
                    stick_bottom: false,
                })
                .child(
                    page(state, place, here, false, t)
                        .build("garden.page", Room::Wide, t)
                        .max_w(READER_MAX_WIDTH),
                )
                .scroll_bar_styled(attn_ui::thin_scroll_bar(t)),
        );
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
    let sheet = Sheet {
        prefix: "garden".into(),
        view: if stacked {
            "garden.viewport"
        } else {
            "garden.reader"
        }
        .into(),
        go: Go::Garden,
        names: &place.names,
        garden: &state.window.garden,
        seeds: super::seeds(state),
        sessions: &state.facts.sessions,
        asks: &state.facts.asks,
        v2: place.v2.on,
        chief: model::has_chief(&state.facts.sessions, &state.facts.profiles.selected),
        readings: &state.readings,
        pictures: &state.pictures,
        drafts: None,
        now_ms: state.window.now_ms,
        harnesses: &state.facts.harnesses,
        t,
    };
    let mut actions = vec![open_as_tile(&here.id, t)];
    actions.extend(sheet.continuation(Some(here), document));
    let mut page = Reader::new()
        .head(sheet.head(here, actions, sheet.continuation_notes(here, document)))
        .push_all(sheet.head_after(here, &place.index, Some(place.attention_of(here))))
        .push_all(sheet.handover_form(document))
        .push_all(sheet.chief_form(document))
        .push(sheet.authorization(here))
        .push(sheet.plan(here))
        .push_all(sheet.checks(here, document));
    if (stacked || place.v2.on) && model::is_plot(here) {
        page = page.push(plot_children(place, here, t));
    }
    page.push_all(sheet.seeds("related", "Related", place.index.relations(&here.id)))
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

fn plot_children(place: &Place, here: &Arc<Seed>, t: &Tokens) -> Block {
    let children = place.index.children_of(&here.id);
    let open = place.lens(children);
    let closed = children.iter().filter(|s| model::is_closed(s)).count();
    let plot = Section::new("garden.plot", "Plot").region("Plot");
    if open.is_empty() && closed > 0 {
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
    plot.push(Block::custom(
        "garden.plot.list.block",
        list(
            "garden.plot.list",
            "list",
            "Plot",
            row_specs(&open, place, Pick::Drill, "", None),
            t,
        ),
    ))
    .build(t)
}
