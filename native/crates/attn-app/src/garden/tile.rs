use std::sync::Arc;

use attn_protocol::{Seed, Session};
use attn_ui::Tokens;
use rune::tok::space;
use rune::{El, Tx, col};

use super::Garden;
use super::model::{self, Index, Relation};
use super::sheet::{Go, Sheet};
use crate::State;

/// What a seed tile reads besides its seed's document.
pub(crate) struct Reads<'a> {
    pub(crate) garden: &'a Garden,
    pub(crate) seeds: &'a [Arc<Seed>],
    pub(crate) sessions: &'a [Session],
    pub(crate) now_ms: i64,
}

/// The seed a tile holds, as the garden's page shows it: the garden's copy when the daemon's
/// document has not come yet, and a way to the garden when the garden holds the seed.
pub(crate) fn body(desktop: &str, leaf: &str, id: &str, reads: &Reads, t: &Tokens) -> El {
    let prefix = format!("seed-tile.{leaf}");
    let view = format!("{prefix}.viewport");
    let (document, error) = super::reading(reads.garden, id);
    let held = reads.seeds.iter().find(|s| s.id == id);
    let names = model::session_names(reads.sessions);
    let sheet = Sheet {
        prefix,
        view: view.clone(),
        go: Go::Tile {
            desktop: desktop.to_owned(),
            tile: leaf.to_owned(),
        },
        names: &names,
        now_ms: reads.now_ms,
        t,
    };
    let here = document.map(|d| &d.seed).or(held.map(|s| s.as_ref()));
    let Some(here) = here else {
        let words = match (id.is_empty(), error) {
            (true, _) => "No seed is associated with this tile.".to_owned(),
            (false, Some(error)) => error.to_owned(),
            (false, None) => "Loading seed…".to_owned(),
        };
        return col().w_full().grow(1.0).child(sheet.note("missing", words));
    };
    let to_garden = match held {
        Some(_) => {
            let id = id.to_owned();
            super::view::button(sheet.key("open-garden"), "Reveal in Garden", t)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| super::open_seed(state, &id, tx))
        }
        None => col(),
    };
    let children = document
        .map(|d| {
            d.children
                .iter()
                .map(|child| Relation {
                    label: "",
                    seed: Arc::new(child.clone()),
                })
                .collect()
        })
        .unwrap_or_default();
    let related = Index::of(reads.seeds).relations(id);
    let parent = model::parent_of(here);
    let back = (!parent.is_empty()).then(|| {
        let title = reads
            .seeds
            .iter()
            .find(|s| s.id == parent)
            .map_or(parent, |s| s.title.as_str());
        let (go, parent) = (sheet.go.clone(), parent.to_owned());
        super::view::button(sheet.key("back"), format!("Back to {title}"), t)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| go.to(state, &parent, tx))
    });
    let page = col()
        .w_full()
        .padding(space::S3)
        .gap(space::S2)
        .children(back)
        .child(sheet.head(here, to_garden))
        .child(sheet.plan(here))
        .children(sheet.checks(here))
        .children(sheet.seeds("plot", "Plot", children))
        .children(sheet.seeds("related", "Related", related))
        .children(sheet.artifacts(document))
        .child(sheet.log(document, error));
    super::view::scroll(&view, page)
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
        .flat_map(|d| crate::sidebar::model::leaves(&d.tree_json))
        .find_map(|leaf| match leaf {
            crate::sidebar::model::Leaf::Tile { id, kind, params }
                if id == holder.pane && kind == "seed" =>
            {
                Some(params)
            }
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
