use std::cell::Cell;
use std::rc::Rc;
use std::sync::Arc;

use attn_protocol::{OpenSeedMessage, Seed};
use attn_ui::{GlyphButton, Tokens};
use rune::tok::{space, type_};
use rune::{El, GrowthSpec, ScrollSpec, Span, Tx, col, el, memo, rich, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize};

use super::model::{self, Index, SessionNames};
use super::search::{self, Lookups, Match, Partial, Query};
use super::sheet::{Go, Sheet, note, section};
use super::{BODY, Mode, SEARCH_FIELD};
use crate::State;
use crate::requests::Refused;

thread_local! {
    pub(super) static ROWS_BUILT: Cell<usize> = const { Cell::new(0) };
}

pub(super) const ROW_PREFIX: &str = "garden.row.";
const COLUMNS_MIN: f32 = 1160.0;
const THIRD_COLUMN_MIN: f32 = 1460.0;
const FULL_INSET: f32 = 12.0;
const ROW_HEIGHT: f32 = 44.0;
const DOCK_WIDTH: f32 = 360.0;

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
        _ => DOCK_WIDTH,
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

/// The garden in the dock: the drawer's slot beside the work.
pub(crate) fn panel(state: &State, t: &Tokens) -> El {
    col()
        .key("drawer")
        .w(DOCK_WIDTH)
        .h_full()
        .fill(t.bg_panel)
        .border_sides([0.0, 0.0, 0.0, 1.0], t.border_subtle)
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
        .radius(8.0)
        .fill(t.bg_panel)
        .border(1.0, t.border)
        .child(content);
    el().key("garden.full")
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(
            el().key("garden.full.scrim")
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
                .pt(crate::shell::TITLE_BAR_HEIGHT + FULL_INSET)
                .pl(FULL_INSET)
                .pr(FULL_INSET)
                .pb(FULL_INSET)
                .child(dialog),
        )
}

fn body(state: &State, t: &Tokens) -> El {
    let place = Place::of(state);
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
        .child(content)
}

pub(super) fn button(key: String, label: impl Into<String>, t: &Tokens) -> El {
    kit_button(label).build(attn_ui::kit(t)).key(key)
}

pub(super) fn kit_button(label: impl Into<String>) -> Button {
    Button::new(label)
        .variant(ButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
}

fn chrome(state: &State, place: &Place, t: &Tokens) -> El {
    let full = super::mode(state) == Mode::Full;
    let mut bar = row()
        .key("garden.chrome")
        .w_full()
        .items_center()
        .gap(space::S2)
        .padding(space::S2)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .child(trail_nav(state, place, t).grow(1.0));
    if full {
        bar = bar.child(view_switch(state, t));
    }
    if let Some((count, on)) = closed_toggle(place) {
        let label = match (on, count) {
            (true, 0) => "hide closed".to_owned(),
            (true, n) => format!("hide {n} closed"),
            (false, n) => format!("{n} closed"),
        };
        bar = bar.child(
            button("garden.closed".into(), label, t)
                .a11y_value(if on { "pressed" } else { "" })
                .on_press_tx(super::toggle_closed),
        );
    }
    let frame_label = if full {
        "Return the garden to the dock"
    } else {
        "Expand the garden"
    };
    let frame_glyph = if full {
        attn_ui::icon::COMPRESS
    } else {
        attn_ui::icon::EXPAND
    };
    bar.child(
        GlyphButton::new(frame_glyph, frame_label)
            .on_press_tx(super::toggle_frame)
            .build(attn_ui::kit(t))
            .key("garden.frame-button"),
    )
    .child(
        GlyphButton::new(attn_ui::icon::CLOSE, "Close")
            .on_press(super::close)
            .build(attn_ui::kit(t))
            .key("garden.close"),
    )
}

pub(super) fn view_switch(state: &State, t: &Tokens) -> El {
    let board = super::shows_board(state);
    let choice = |name: &'static str, on: bool, pick: bool| {
        kit_button(name)
            .variant(if on {
                ButtonVariant::Neutral
            } else {
                ButtonVariant::GhostMuted
            })
            .on_press_tx(move |state: &mut State, tx: &mut Tx| super::choose_board(state, pick, tx))
            .build(attn_ui::kit(t))
            .key(format!("garden.view.{name}"))
            .a11y_value(if on { "pressed" } else { "" })
    };
    row()
        .key("garden.view")
        .role("group")
        .label("Garden view")
        .gap(space::S1)
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
    let nav = row()
        .key("garden.trail")
        .role("navigation")
        .label(if place.wide {
            "Standing here, searching the whole garden"
        } else {
            "The way back"
        })
        .items_center()
        .gap(space::S1)
        .min_w(0.0);
    let Some(here) = place.here() else {
        return nav.child(
            text("The garden")
                .key("garden.trail.here")
                .font(type_::LABEL)
                .color(t.text_primary),
        );
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
        button(key, label, t)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| super::climb_to(state, depth, tx))
    };
    let sep = || {
        text("›")
            .font(type_::LABEL)
            .color(t.text_muted)
            .a11y_hidden()
    };
    let mut nav = nav.child(step("garden.trail.0".into(), "Garden".into(), 0));
    if fold {
        nav = nav.child(sep()).child(
            button("garden.trail.fold".into(), "…", t)
                .label(format!("Show {folded} more steps"))
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
        nav = nav
            .child(sep())
            .child(step(format!("garden.trail.{depth}"), title, depth));
    }
    if place.query.searches {
        nav = nav.child(sep()).child(
            text(here.title.clone())
                .key("garden.trail.here")
                .font(type_::LABEL)
                .color(t.text_primary),
        );
    }
    nav
}

fn search_line(state: &State, place: &Place, t: &Tokens) -> El {
    let garden = &state.window.garden;
    let plot = !place.trail.is_empty();
    let place_word = if plot && !place.wide {
        "this plot"
    } else {
        "the garden"
    };
    let field = text_input(&garden.query, type_::BODY)
        .key(SEARCH_FIELD)
        .single_line()
        .label(format!("Search {place_word}"))
        .placeholder("search — or is:ready, tender:…", t.text_muted)
        .on_edit(|state: &mut State| &mut state.window.garden.query)
        .on_input_key(super::field_key)
        .grow(1.0)
        .color(t.text_primary);
    let mut meta = row()
        .key("garden.search.meta")
        .items_center()
        .gap(space::S1);
    let q = &place.query;
    if !q.unknown.is_empty() {
        meta = meta.child(
            text(format!("no filter called {}", q.unknown.join(" ")))
                .key("garden.search.unknown")
                .role("status")
                .label(format!("no filter called {}", q.unknown.join(" ")))
                .font(type_::LABEL)
                .color(t.warning),
        );
    } else if let Some(partial) = q.partial {
        match partial {
            Partial::Is => {
                for value in search::IS_VALUES {
                    meta = meta.child(
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
                meta = meta.child(
                    text("a crew member or session")
                        .font(type_::LABEL)
                        .color(t.text_muted),
                );
            }
        }
    } else if q.active && plot {
        if !place.wide && place.outside > 0 {
            meta = meta.child(
                button(
                    "garden.search.widen".into(),
                    format!("+{} in the whole garden", place.outside),
                    t,
                )
                .on_press_tx(super::toggle_wide),
            );
        }
        if place.wide {
            meta = meta.child(
                button(
                    "garden.search.widen".into(),
                    format!("{} in this plot", place.in_plot),
                    t,
                )
                .on_press_tx(super::toggle_wide),
            );
        }
    }
    row()
        .key("garden.search")
        .w_full()
        .items_center()
        .gap(space::S2)
        .padding(space::S2)
        .child(
            text("/")
                .font(type_::MONO_CAPTION)
                .color(t.text_muted)
                .a11y_hidden(),
        )
        .child(field)
        .child(meta)
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
        .w_full()
        .growth(GrowthSpec::default())
        .virtual_rows(
            specs.len(),
            ROW_HEIGHT,
            move |_: &State, i| row_key(&keys[i].seed.id).into(),
            move |_: &State, i| seed_row(&specs[i], &t),
        )
}

fn marked(text_: &str, ranges: &[search::Range], color: rune::Color, t: &Tokens) -> El {
    let spans = ranges
        .iter()
        .map(|&(from, to)| Span::new(from, to).color(t.text_primary).weight(600.0))
        .collect();
    rich(text_.to_owned(), spans).font(type_::BODY).color(color)
}

fn seed_row(spec: &RowSpec, t: &Tokens) -> El {
    let seed = &spec.seed;
    let look = &spec.look;
    let key = row_key(&seed.id);
    memo(key.clone(), (look.clone(), *t), || {
        ROWS_BUILT.with(|n| n.set(n.get() + 1));
        let closed = model::is_closed(seed);
        let armed = model::harvest_when(seed);
        let mut primary = row().w_full().items_center().gap(space::S2).child(
            marked(
                &seed.title,
                &look.title_ranges,
                if closed { t.text_muted } else { t.text_primary },
                t,
            )
            .grow(1.0),
        );
        if let Some(standby) = &seed.standby {
            let words = if standby.resume_blocked.is_some() {
                "standby · resume blocked"
            } else {
                "standby"
            };
            primary = primary.child(text(words).font(type_::LABEL).color(t.warning));
        } else if let Some((signal, tone)) = model::signal(seed, look.blockers)
            && !(armed.is_some() && signal == "parked")
        {
            let color = match tone {
                "blocked" => t.warning,
                "active" => t.accent,
                _ => t.text_muted,
            };
            primary = primary.child(text(signal).font(type_::LABEL).color(color));
        }
        if let Some((sentence, marker)) = &armed {
            primary = primary.child(
                text(marker.clone())
                    .help(sentence.clone())
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            );
        }
        let mut meta =
            row()
                .gap(space::S2)
                .child(
                    text(seed.id.clone())
                        .font(type_::MONO_CAPTION)
                        .color(if look.id_hit {
                            t.text_primary
                        } else {
                            t.text_tertiary
                        }),
                );
        if seed.standby.is_some() && look.blockers > 0 {
            meta = meta.child(
                text(format!("blocked by {}", look.blockers))
                    .font(type_::LABEL)
                    .color(t.text_tertiary),
            );
        }
        if !look.tender.is_empty() {
            meta = meta.child(
                text(format!("tended by {}", look.tender))
                    .font(type_::LABEL)
                    .color(t.text_tertiary),
            );
        }
        if let Some(home) = &look.home {
            meta = meta.child(
                text(format!("in {home}"))
                    .font(type_::LABEL)
                    .color(t.text_tertiary),
            );
        }
        if let Some(tally) = model::check_tally(seed) {
            meta = meta.child(
                text(model::tally_words(&tally))
                    .font(type_::LABEL)
                    .color(t.text_tertiary),
            );
        }
        let mut content = col()
            .grow(1.0)
            .min_w(0.0)
            .gap(2.0)
            .child(primary)
            .child(meta);
        if let Some(snippet) = &look.snippet {
            content = content.child(marked(&snippet.text, &snippet.ranges, t.text_secondary, t));
        }
        let mut line = row().w_full().items_center().gap(space::S2).child(content);
        if let Some(progress) = &seed.plot_progress {
            line = line.child(
                text(format!("{}/{} ›", progress.done, progress.total))
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            );
        }
        let id = seed.id.clone();
        let pick = look.pick;
        let found = pick == Pick::Found;
        line.key(key)
            .role(if found { "option" } else { "button" })
            .focus_selected(found && look.active)
            .label(seed.title.clone())
            .a11y_value(seed.status.clone())
            .focusable(true)
            .w_full()
            .padding(space::S2)
            .radius(4.0)
            .fill(if look.selected || look.active {
                t.bg_selected
            } else {
                t.bg_panel
            })
            .hover(t.bg_elevated)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| match pick {
                Pick::Drill => super::drill(state, &id, tx),
                Pick::Level(level) => super::select_at(state, level, &id, tx),
                Pick::Found => super::open_found(state, &id, tx),
            })
    })
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
        note("garden.capped", words, t)
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
    let mut moves = col().key("garden.nothing.moves").gap(space::S1);
    if plot && !place.wide && place.outside > 0 {
        moves = moves.child(
            button(
                "garden.nothing.widen".into(),
                format!("Search the whole garden {}", place.outside),
                t,
            )
            .on_press_tx(super::toggle_wide),
        );
    }
    if plot && place.wide {
        moves = moves.child(
            button(
                "garden.nothing.narrow".into(),
                format!("Search this plot only {}", place.in_plot),
                t,
            )
            .on_press_tx(super::toggle_wide),
        );
    }
    if place.hidden_closed > 0 {
        moves = moves.child(
            button(
                "garden.nothing.closed".into(),
                format!("Include closed seeds {}", place.hidden_closed),
                t,
            )
            .on_press_tx(super::toggle_closed),
        );
    }
    moves = moves.child(
        button("garden.nothing.clear".into(), "Clear the search", t).on_press_tx(
            |state: &mut State, tx: &mut Tx| super::set_query(state, String::new(), tx),
        ),
    );
    col()
        .key("garden.nothing")
        .w_full()
        .padding(space::S2)
        .gap(space::S2)
        .child(note(
            "garden.nothing.line",
            format!("Nothing in {where_} matches {query}."),
            t,
        ))
        .child(moves)
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

pub(super) fn scroll(key: &str, content: El) -> El {
    col()
        .key(key.to_owned())
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .scroll(ScrollSpec {
            stick_bottom: false,
        })
        .child(content)
}

fn root_list(state: &State, place: &Place, pick: Pick, t: &Tokens) -> El {
    let roots = place.lens(&place.index.roots);
    let closed = place
        .index
        .roots
        .iter()
        .filter(|s| model::is_closed(s))
        .count();
    let mut page = col().w_full().children(capped(state, place, t));
    if roots.is_empty() && closed > 0 {
        page = page.child(note("garden.empty", hidden_closed_words(closed), t));
    } else if roots.is_empty() {
        page = page.child(note(
            "garden.empty",
            "The garden is empty. attn seed plant \"what this is\" puts something in it.",
            t,
        ));
    } else {
        let selected = place.trail.first().map_or("", String::as_str);
        page = page.child(list(
            "garden.list",
            "list",
            "Seeds",
            row_specs(&roots, place, pick, selected, None),
            t,
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

fn stack_view(state: &State, place: &Place, t: &Tokens) -> El {
    let content = if place.query.searches {
        col()
            .w_full()
            .children(capped(state, place, t))
            .child(results(state, place, t))
    } else {
        match place.here() {
            None => root_list(state, place, Pick::Drill, t),
            Some(here) => page(state, place, here, true, t),
        }
    };
    scroll("garden.viewport", content.key("garden.page"))
}

fn columns_view(state: &State, place: &Place, t: &Tokens) -> El {
    let mut columns = row()
        .key("garden.columns")
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .gap(1.0);
    let column = |key: String, content: El| {
        col()
            .key(key)
            .w(320.0)
            .h_full()
            .min_h(0.0)
            .border_sides([0.0, 1.0, 0.0, 0.0], t.border_subtle)
            .scroll(ScrollSpec {
                stick_bottom: false,
            })
            .child(content)
    };
    if place.query.searches {
        columns = columns.child(column(
            "garden.column.results".into(),
            col()
                .w_full()
                .children(capped(state, place, t))
                .child(results(state, place, t)),
        ));
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
                root_list(state, place, Pick::Level(0), t)
            } else if seeds.is_empty() {
                note("garden.column.empty", "Nothing here yet.", t)
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
                .scroll(ScrollSpec {
                    stick_bottom: false,
                })
                .child(page(state, place, here, false, t).key("garden.page")),
        );
    }
    columns
}

/// The opened seed: its state and who tends it, why it closed, its plot's progress, its plan,
/// its checks, its plot's children while the walk stacks, its relations, artifacts and log.
fn page(state: &State, place: &Place, here: &Arc<Seed>, stacked: bool, t: &Tokens) -> El {
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
        now_ms: state.window.now_ms,
        t,
    };
    let mut page = col()
        .w_full()
        .padding(space::S3)
        .gap(space::S2)
        .child(
            sheet.head(
                here,
                row()
                    .gap(space::S1)
                    .child(open_as_tile(&here.id, t))
                    .children(sheet.continuation(Some(here), document)),
            ),
        )
        .children(sheet.handover_form(document))
        .children(sheet.chief_form(document))
        .child(sheet.authorization(here))
        .child(sheet.plan(here))
        .children(sheet.checks(here));
    if stacked && model::is_plot(here) {
        page = page.child(plot_children(place, here, t));
    }
    page.children(sheet.seeds("related", "Related", place.index.relations(&here.id)))
        .children(sheet.artifacts(document))
        .child(sheet.log(document, error))
}

fn open_as_tile(id: &str, t: &Tokens) -> El {
    let id = id.to_owned();
    button("garden.open-tile".into(), "Open as tile", t).on_press_tx(
        move |state: &mut State, tx: &mut Tx| {
            let seed_id = id.clone();
            state.act(
                |request_id| OpenSeedMessage {
                    seed_id,
                    request_id: Some(request_id),
                    ..OpenSeedMessage::new()
                },
                Refused::Prefixed("Could not open the seed"),
                |_, _, _| {},
                tx,
            );
        },
    )
}

fn plot_children(place: &Place, here: &Arc<Seed>, t: &Tokens) -> El {
    let children = place.index.children_of(&here.id);
    let open = place.lens(children);
    let closed = children.iter().filter(|s| model::is_closed(s)).count();
    let plot = section("garden.plot", "Plot", t);
    if open.is_empty() && closed > 0 {
        return plot.child(note("garden.plot.empty", hidden_closed_words(closed), t));
    }
    if open.is_empty() {
        return plot.child(note(
            "garden.plot.empty",
            format!(
                "Nothing planted in this plot yet. attn seed plant \"what this is\" --part-of {} puts something in it.",
                here.id
            ),
            t,
        ));
    }
    plot.child(list(
        "garden.plot.list",
        "list",
        "Plot",
        row_specs(&open, place, Pick::Drill, "", None),
        t,
    ))
}
