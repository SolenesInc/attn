use std::cell::Cell;
use std::rc::Rc;
use std::sync::Arc;

use attn_protocol::Seed;
use attn_ui::blocks::{
    Actions, Bar, Block, Facts, Inline, ListRow, Note, Pane, Reader, Room, Section, Surface, Trail,
    rows,
};
use attn_ui::system::{Button, Link, Look as ButtonLook, Size, Type, bare_field, marked, words};
use attn_ui::{GlyphButton, Tokens};
use rune::{El, ScrollSpec, Tx, col, memo, row};
use rune_kit::ScrollBar;

use super::model::{self, Index, SessionNames};
use super::search::{self, Lookups, Match, Partial, Query};
use super::sheet::{Go, Sheet};
use super::{BODY, Mode, SEARCH_FIELD};
use crate::State;

thread_local! {
    pub(super) static ROWS_BUILT: Cell<usize> = const { Cell::new(0) };
}

pub(super) const ROW_PREFIX: &str = "garden.row.";
const COLUMNS_MIN: f32 = 1160.0;
const THIRD_COLUMN_MIN: f32 = 1460.0;
const FULL_INSET: f32 = 12.0;
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
}

impl Lookups for Look<'_> {
    fn tender_of(&self, seed: &Seed) -> String {
        model::tender_of(seed, self.names)
    }
    fn blockers_of(&self, seed: &Seed) -> usize {
        self.index.blockers(&seed.id)
    }
}

fn frame_width(state: &State) -> f32 {
    match super::mode(state) {
        Mode::Full => (state.window.width.window - FULL_INSET * 2.0).max(0.0),
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
}

impl Place {
    fn of(state: &State) -> Place {
        let index = index(state);
        let names = model::session_names(&state.facts.sessions);
        let trail = super::living_trail(state);
        let garden = &state.window.garden;
        let query = search::parse(&garden.query.text);
        let plot = trail.last().cloned();
        let wide = query.searches && plot.is_some() && garden.wide_in == plot;
        let look = Look {
            index: &index,
            names: &names,
        };
        let seeds = super::seeds(state);
        let in_subtree = plot.as_ref().map(|p| index.subtree(p));
        let plot_pool = || {
            seeds
                .iter()
                .filter(|s| in_subtree.as_ref().is_none_or(|w| w.contains(&s.id)))
        };
        let (mut answers, mut outside, mut in_plot, mut hidden_closed) = (Vec::new(), 0, 0, 0);
        if query.searches {
            answers = if wide {
                search::search(seeds, &query, &look)
            } else {
                search::search(plot_pool(), &query, &look)
            };
            if plot.is_some() && !wide {
                outside = search::search(seeds, &query, &look)
                    .len()
                    .saturating_sub(answers.len());
            }
            if wide {
                in_plot = search::search(plot_pool(), &query, &look).len();
            }
            if query.is.is_empty() {
                let any = search::parse(&format!("{} is:any", garden.query.text));
                let with_closed = if wide {
                    search::search(seeds, &any, &look).len()
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
        }
    }

    fn look(&self) -> Look<'_> {
        Look {
            index: &self.index,
            names: &self.names,
        }
    }

    fn here(&self) -> Option<&Arc<Seed>> {
        self.index.by_id.get(self.trail.last()?)
    }

    fn lens(&self, seeds: &[Arc<Seed>]) -> Vec<Arc<Seed>> {
        let look = self.look();
        seeds
            .iter()
            .filter(|s| search::satisfies_lens(s, &self.query.is, &look))
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
    let content = if super::shows_board(state) && !super::review::is_open(state) {
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
    crate::shell::under_title_bar("garden.full", FULL_INSET, Surface::panel(dialog, t), t)
}

fn body(state: &State, t: &Tokens) -> El {
    let place = Place::of(state);
    if super::review::is_open(state) {
        let sheet = Sheet {
            prefix: super::review::AT.into(),
            view: "garden.review.viewport".into(),
            go: Go::Review,
            names: &place.names,
            garden: &state.window.garden,
            seeds: super::seeds(state),
            chief: model::has_chief(&state.facts.sessions, &state.facts.profiles.selected),
            readings: &state.readings,
            pictures: &state.pictures,
            drafts: None,
            now_ms: state.window.now_ms,
            t,
        };
        let full = super::mode(state) == Mode::Full;
        return super::review::view(state, &sheet, full, t);
    }
    let prompt = if place.trail.is_empty() && !place.query.searches {
        super::review::prompt(state, t)
    } else {
        Vec::new()
    };
    let content = if columns(state) {
        columns_view(state, &place, t)
    } else {
        stack_view(state, &place, t)
    };
    col()
        .key(BODY)
        .role("region")
        .label("The garden")
        .focusable(true)
        .w_full()
        .h_full()
        .min_h(0.0)
        .on_key(super::key)
        .child(chrome(state, &place, t))
        .child(search_line(state, &place, t))
        .children(prompt)
        .child(content)
}

/// One of the garden's quiet buttons, keyed `key`.
pub(super) fn button(key: impl Into<String>, label: impl Into<String>, t: &Tokens) -> El {
    quiet(label).build(t).key(key).into()
}

/// A quiet compact button, to give a press and build.
pub(super) fn quiet(label: impl Into<String>) -> Button {
    Button::new(label)
        .look(ButtonLook::Quiet)
        .size(Size::Compact)
}

/// A bordered compact button beside a seed's title.
pub(super) fn plain(label: impl Into<String>) -> Button {
    Button::new(label).size(Size::Compact)
}

fn chrome(state: &State, place: &Place, t: &Tokens) -> El {
    let full = super::mode(state) == Mode::Full;
    let mut bar = Bar::new("garden.chrome").lead(trail_nav(state, place, t));
    if full {
        bar = bar.push(view_switch(state, t));
    }
    if let Some((count, on)) = closed_toggle(place) {
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
fn closed_toggle(place: &Place) -> Option<(usize, bool)> {
    let on = place.query.is.contains(&"any");
    let other = place.query.is.iter().any(|v| *v != "any");
    let level = match place.here() {
        Some(here) => place.index.children_of(&here.id),
        None => &place.index.roots,
    };
    let closed_here = level.iter().filter(|s| model::is_closed(s)).count();
    let count = if !place.query.searches {
        closed_here
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
    let Some(here) = place.here() else {
        return nav.here("garden.trail.here", "The garden", t).build(t);
    };
    let first_visible = if columns(state) && !place.query.searches {
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
    let mut nav = nav.step(step("garden.trail.0".into(), "Garden".into(), 0));
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
    if place.query.searches {
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
    let field = bare_field(&garden.query, Type::Body)
        .key(SEARCH_FIELD)
        .single_line()
        .label(format!("Search {place_word}"))
        .placeholder("search — or is:ready, tender:…", t.text_muted)
        .on_edit(|state: &mut State| &mut state.window.garden.query)
        .on_input_key(super::field_key)
        .color(t.text_primary);
    let found = place
        .query
        .searches
        .then(|| place.answers.get(place.active(state)))
        .flatten();
    let field = match found {
        Some(m) => field.active_descendant(row_key(&m.seed.id)),
        None => field,
    };
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
    Bar::new("garden.search")
        .start(prompt)
        .lead(field)
        .push(
            Inline::tight()
                .key("garden.search.meta")
                .grow(1.0)
                .shrink()
                .min_w(0.0)
                .justify_end()
                .children(meta),
        )
        .build(t)
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
            RowSpec {
                seed: seed.clone(),
                look: RowLook {
                    seed: Arc::as_ptr(seed) as usize,
                    blockers: place.index.blockers(&seed.id),
                    selected: seed.id == selected,
                    active: matches.is_some_and(|(_, active)| active == i),
                    pick,
                    title_ranges: hit.map(|h| h.title_ranges.clone()).unwrap_or_default(),
                    snippet: hit.and_then(|h| h.snippet.clone()),
                    id_hit: hit.is_some_and(|h| h.id_hit),
                    home,
                    tender: model::tender_of(seed, &place.names),
                    standby: model::standby_of(seed, place.now_ms),
                },
            }
        })
        .collect()
}

fn list(key: &str, role: &'static str, label: &str, specs: Vec<RowSpec>, t: &Tokens) -> El {
    let specs: Rc<[RowSpec]> = specs.into();
    let keys = specs.clone();
    let t = *t;
    rows(
        key.to_owned(),
        role,
        label.to_owned(),
        specs.len(),
        move |_: &State, i| row_key(&keys[i].seed.id).into(),
        move |_: &State, i| seed_row(&specs[i], &t),
    )
}

fn seed_row(spec: &RowSpec, t: &Tokens) -> El {
    let seed = &spec.seed;
    let look = &spec.look;
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
        let title = marked(&seed.title, &look.title_ranges, Type::Body, ink, t.brand);
        let mut row = ListRow::new(key.clone(), seed.title.clone(), title)
            .mark(state_color(&seed.status, t))
            .value(seed.status.clone())
            .selected(look.selected || look.active);
        if let Some(standby) = &look.standby {
            row = row.signal(
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
            row = row.signal(words(signal, Type::Small, tone_color(tone, t)));
        }
        if let Some((sentence, marker)) = &armed {
            row = row
                .signal(words(marker.clone(), Type::Small, t.text_tertiary).help(sentence.clone()));
        }
        let mut facts = Facts::new().id(
            seed.id.clone(),
            if look.id_hit {
                t.brand
            } else {
                t.text_tertiary
            },
        );
        if seed.standby.is_some() && look.blockers > 0 {
            facts = facts.said(format!("blocked by {}", look.blockers), t);
        }
        if !look.tender.is_empty() {
            facts = facts.said(format!("tended by {}", look.tender), t);
        }
        if let Some(home) = &look.home {
            facts = facts.said(format!("in {home}"), t);
        }
        if let Some(tally) = model::check_tally(seed) {
            facts = facts.said(model::tally_words(&tally), t);
        }
        row = row.facts(facts);
        if let Some(snippet) = &look.snippet {
            row = row.snippet(marked(
                &snippet.text,
                &snippet.ranges,
                Type::Small,
                t.text_tertiary,
                t.brand,
            ));
        }
        if let Some(progress) = &seed.plot_progress {
            row = row.end(
                Inline::tight()
                    .child(words(
                        format!("{}/{}", progress.done, progress.total),
                        Type::Small,
                        t.text_tertiary,
                    ))
                    .child(words("›", Type::Small, t.text_tertiary).a11y_hidden()),
            );
        }
        let id = seed.id.clone();
        let pick = look.pick;
        let found = pick == Pick::Found;
        row.role(if found { "option" } else { "button" })
            .build(t)
            .focus_selected(found && look.active)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| match pick {
                Pick::Drill => super::drill(state, &id, tx),
                Pick::Level(level) => super::select_at(state, level, &id, tx),
                Pick::Found => super::open_found(state, &id, tx),
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
            format!("Search covers the newest {shown} of {total} seeds.")
        } else {
            format!("The garden holds {total} seeds; this panel has the newest {shown}.")
        };
        Note::new("garden.capped", words).el(t)
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
    let mut moves = Actions::new();
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
            Note::new(
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
    list("garden.results", "listbox", "Results", specs, t)
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
    let roots = place.lens(&place.index.roots);
    let closed = place
        .index
        .roots
        .iter()
        .filter(|s| model::is_closed(s))
        .count();
    let stacked = pick == Pick::Drill;
    let mut page: Vec<Block> = capped(state, place, t)
        .map(Block::custom)
        .into_iter()
        .collect();
    if roots.is_empty() && closed > 0 {
        page.push(Note::new("garden.empty", hidden_closed_words(closed)).build(t));
    } else if roots.is_empty() && stacked {
        page.push(
            Note::new(
                "garden.empty",
                "The garden is empty. `attn seed plant \"what this is\"` puts something in it.",
            )
            .build(t),
        );
    } else if roots.is_empty() {
        page.push(Note::new("garden.empty", "Nothing here yet.").build(t));
    } else {
        let selected = place.trail.first().map_or("", String::as_str);
        page.push(Block::custom(list(
            "garden.list",
            "list",
            "Seeds",
            row_specs(&roots, place, pick, selected, None),
            t,
        )));
    }
    page
}

fn hidden_closed_words(closed: usize) -> String {
    format!(
        "Nothing open here. {closed} closed {} behind the closed toggle above.",
        if closed == 1 { "seed is" } else { "seeds are" }
    )
}

fn stack_view(state: &State, place: &Place, t: &Tokens) -> El {
    let content = if place.query.searches {
        Reader::new()
            .push_all(capped(state, place, t).map(Block::custom))
            .push(Block::custom(results(state, place, t)))
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
                Note::new("garden.column.empty", "Nothing here yet.").el(t)
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
        chief: model::has_chief(&state.facts.sessions, &state.facts.profiles.selected),
        readings: &state.readings,
        pictures: &state.pictures,
        drafts: None,
        now_ms: state.window.now_ms,
        t,
    };
    let mut actions = vec![open_as_tile(&here.id, t)];
    actions.extend(sheet.continuation(Some(here), document));
    let mut page = Reader::new()
        .head(sheet.head(here, actions, sheet.continuation_notes(here, document)))
        .push_all(sheet.head_after(here))
        .push_all(sheet.handover_form(document))
        .push_all(sheet.chief_form(document))
        .push(sheet.authorization(here))
        .push(sheet.plan(here))
        .push_all(sheet.checks(here, document));
    if stacked && model::is_plot(here) {
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
    let plot = Section::new("garden.plot", "Plot").region();
    if open.is_empty() && closed > 0 {
        return plot
            .push(Note::new("garden.plot.empty", hidden_closed_words(closed)).build(t))
            .build(t);
    }
    if open.is_empty() {
        let command = format!("attn seed plant \"what this is\" --part-of {}", here.id);
        return plot
            .push(
                Note::new(
                    "garden.plot.empty",
                    format!("Nothing planted in this plot yet. `{command}` puts something in it."),
                )
                .build(t),
            )
            .build(t);
    }
    plot.push(Block::custom(list(
        "garden.plot.list",
        "list",
        "Plot",
        row_specs(&open, place, Pick::Drill, "", None),
        t,
    )))
    .build(t)
}
