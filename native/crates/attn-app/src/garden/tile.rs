use std::sync::Arc;

use attn_client::Facts;
use attn_protocol::Seed;
use attn_ui::Tokens;
use attn_ui::blocks::{Reader, Room};
use rune::{El, Tx};

use super::Garden;
use super::model::{self, Index, Relation};
use super::sheet::{Go, Sheet};
use crate::State;

/// What a seed tile reads besides its seed's document.
pub(crate) struct Reads<'a> {
    pub(crate) garden: &'a Garden,
    pub(crate) seeds: &'a [Arc<Seed>],
    pub(crate) terminals: &'a crate::terminal::Terminals,
    pub(crate) facts: &'a Facts,
    pub(crate) v2: bool,
    pub(crate) readings: &'a crate::markdown::Readings,
    pub(crate) pictures: &'a crate::markdown::Pictures,
    pub(crate) now_ms: i64,
    pub(crate) harnesses: &'a [attn_protocol::Harness],
    pub(crate) chief: bool,
    pub(crate) zoom: f32,
    /// The seed tile, by desktop and tile, that offers the way back to Manage crew.
    pub(crate) crew_back: Option<(&'a str, &'a str)>,
}

impl<'a> Reads<'a> {
    pub(crate) fn of(state: &'a State) -> Reads<'a> {
        Reads {
            garden: &state.window.garden,
            seeds: super::seeds(state),
            terminals: &state.terminals,
            facts: &state.facts,
            v2: super::v2::enabled(state),
            readings: &state.readings,
            pictures: &state.pictures,
            now_ms: state.window.now_ms,
            harnesses: &state.facts.harnesses,
            chief: model::has_chief(&state.facts.sessions, &state.facts.profiles.selected),
            zoom: super::zoom(state),
            crew_back: crate::crew::back_tile(state),
        }
    }
}

/// The seed a tile holds, as the garden's page shows it: the garden's copy when the daemon's
/// document has not come yet, and a way to the garden when the garden holds the seed.
pub(crate) fn body(
    desktop: &str,
    leaf: &str,
    id: &str,
    reads: &Reads,
    drafts: Option<&crate::markdown::Drafts>,
    t: &Tokens,
) -> El {
    let go = Go::Tile {
        desktop: desktop.to_owned(),
        tile: leaf.to_owned(),
    };
    page(go, id, reads, drafts, t)
}

/// Seed `id` as the panel beside an ask shows it, where a seed it links to opens another tab.
pub(crate) fn panel_body(id: &str, reads: &Reads, t: &Tokens) -> El {
    page(Go::Panel, id, reads, None, t)
}

const PANEL_VIEW: &str = "asks.panel.seed.viewport";

fn page(
    go: Go,
    id: &str,
    reads: &Reads,
    drafts: Option<&crate::markdown::Drafts>,
    t: &Tokens,
) -> El {
    let (prefix, view, zoom) = match &go {
        Go::Tile { tile, .. } => (
            format!("seed-tile.{tile}"),
            super::tile_view(tile),
            reads.zoom,
        ),
        _ => ("asks.panel.seed".to_owned(), PANEL_VIEW.to_owned(), 1.0),
    };
    let (document, error) = super::reading(reads.garden, id);
    let held = reads.seeds.iter().find(|s| s.id == id);
    let names = model::session_names(&reads.facts.sessions, reads.harnesses);
    let sheet = Sheet {
        prefix,
        view: view.clone(),
        go,
        names: &names,
        garden: reads.garden,
        seeds: reads.seeds,
        terminals: reads.terminals,
        facts: reads.facts,
        v2: reads.v2,
        chief: reads.chief,
        readings: reads.readings,
        pictures: reads.pictures,
        drafts,
        now_ms: reads.now_ms,
        harnesses: reads.harnesses,
        t,
    };
    let here = document.map(|d| &d.seed).or(held.map(|s| s.as_ref()));
    let Some(here) = here else {
        let words = match (id.is_empty(), error) {
            (true, _) => "No seed is associated with this tile.".to_owned(),
            (false, Some(error)) => error.to_owned(),
            (false, None) => "Loading seed…".to_owned(),
        };
        let note = Reader::new().push(sheet.note("missing", words)).build(
            sheet.key("page"),
            Room::Narrow,
            t,
        );
        return super::zoomed(zoom, note);
    };
    let can_reveal = held.is_some()
        || document.is_some_and(|d| d.seed.profile_id == reads.facts.profiles.selected);
    let in_panel = matches!(sheet.go, Go::Panel);
    let to_garden = can_reveal.then(|| {
        let id = id.to_owned();
        super::view::plain("Reveal in Garden")
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                if in_panel {
                    crate::asks::close(state, tx);
                }
                super::open_seed(state, &id, tx)
            })
            .build(t)
            .key(sheet.key("open-garden"))
            .into()
    });
    let cached = super::reading_seeds(reads.garden, id);
    let children = cached
        .map(|cached| {
            cached
                .children
                .iter()
                .map(|child| Relation {
                    label: "",
                    seed: child.clone(),
                })
                .collect()
        })
        .unwrap_or_default();
    let index = Index::of(reads.seeds);
    let related = index.relations_with(
        here,
        cached.and_then(|c| c.related),
        cached.and_then(|c| c.ancestors),
    );
    let parent = model::parent_of(here);
    let back = (!parent.is_empty()).then(|| {
        let title = reads
            .seeds
            .iter()
            .find(|s| s.id == parent)
            .map(|s| s.title.as_str())
            .or_else(|| {
                document
                    .and_then(|d| d.ancestors.as_deref())
                    .and_then(|ancestors| ancestors.iter().find(|s| s.id == parent))
                    .map(|s| s.title.as_str())
            })
            .unwrap_or(parent);
        let (go, parent) = (sheet.go.clone(), parent.to_owned());
        super::view::quiet(format!("‹ Back to {title}"))
            .on_press_tx(move |state: &mut State, tx: &mut Tx| go.to(state, &parent, tx))
            .build(t)
            .key(sheet.key("back"))
            .label(format!("Back to {title}"))
    });
    let to_crew = match &sheet.go {
        Go::Tile { desktop, tile } if reads.crew_back == Some((desktop, tile)) => Some(
            super::view::quiet("‹ Back to Crew")
                .on_press_tx(|state: &mut State, tx: &mut Tx| crate::crew::go_back(state, tx))
                .build(t)
                .key(format!("tile.{tile}.crew-back"))
                .label("Back to Crew"),
        ),
        _ => None,
    };
    let mut actions: Vec<El> = to_garden.into_iter().collect();
    actions.extend(sheet.continuation(held.map(|s| s.as_ref()), document));
    let mut page = Reader::new();
    for way_back in to_crew.into_iter().chain(back) {
        page = page.above(way_back);
    }
    let page = page
        .head(sheet.head(here, actions, Vec::new()))
        .push_all(sheet.head_after(here, &index, None))
        .push_all(sheet.handover_form(document))
        .push_all(sheet.chief_form(document))
        .push_all(sheet.seeds_saying("plot", "Plot", children, |seed| {
            model::signal(seed, 0).map(|(words, _)| words)
        }))
        .push(sheet.authorization(here))
        .push(sheet.plan(here))
        .push_all(sheet.checks(here, document))
        .push_all(sheet.seeds("related", "Related", related))
        .push_all(sheet.artifacts(document))
        .push(sheet.log(document, error))
        .build(sheet.key("page"), Room::Narrow, t);
    let bar = super::reading::find_bar(reads.readings, &view, (id, &here.body), t);
    super::zoomed(zoom, super::view::scroll(&view, page, t).children(bar))
}

/// Escape in a seed tile that holds the keyboard climbs to its seed's plot; false when the
/// keyboard is elsewhere or the seed stands in no plot.
pub(crate) fn climb(state: &mut State, tx: &mut Tx) -> bool {
    let Some(holder) = state.window.keyboard.clone() else {
        return false;
    };
    if tx
        .focused()
        .is_some_and(|key| !key.starts_with(&format!("seed-tile.{}.", holder.pane)))
    {
        return false;
    }
    let held = state
        .facts
        .desktops
        .iter()
        .find(|d| d.id == holder.desktop)
        .into_iter()
        .flat_map(|d| d.leaves())
        .find_map(|leaf| match leaf.tile() {
            Some(("seed", params)) if leaf.id == holder.pane => Some(params.to_owned()),
            _ => None,
        });
    let Some(id) = held else {
        return false;
    };
    let (document, _) = super::reading(&state.window.garden, &id);
    let seed = document.map(|d| &d.seed).or_else(|| {
        super::seeds(state)
            .iter()
            .find(|s| s.id == id)
            .map(|s| s.as_ref())
    });
    let parent = seed.map(model::parent_of).unwrap_or_default().to_owned();
    if parent.is_empty() {
        return false;
    }
    Go::Tile {
        desktop: holder.desktop,
        tile: holder.pane,
    }
    .to(state, &parent, tx);
    true
}
