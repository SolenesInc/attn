//! An editor tile's "Editor root" picker in its header: the notebook, the folder of the agent
//! the window acts on, the tile's own folder, or one the user browses to. A switch saves the
//! open edit first and stays put when that save conflicts or fails.

use attn_ui::Tokens;
use rune::{El, Tx};
use rune_kit::{Select, SelectAppearance};

use super::io;
use super::model::SurfaceId;
use super::params::Params;
use crate::State;
use attn_client::{Desktop, Facts};

use super::Notebook;

const NOTEBOOK: &str = "Notebook";
const BROWSE: &str = "Browse…";
/// `.desktop-dock-tile-root-picker`'s `max-width`.
const PICKER_WIDTH: f32 = 140.0;
/// The root picker's height.
const PICKER_HEIGHT: f32 = 24.0;

fn select_key(tile: &str) -> String {
    format!("notebook.tile.{tile}.root")
}

/// The leaf a window acts on, with its desktop.
type Acting<'a> = (&'a Desktop, &'a str);

/// The options the picker offers for a tile on `root`, with the folder each one roots it at.
fn options(
    facts: &Facts,
    acting: Option<Acting>,
    root: Option<&str>,
) -> Vec<(String, Option<String>)> {
    let notebook = super::notebook_root_of(facts);
    let desktop = acting
        .and_then(|acting| desktop_folder(facts, acting))
        .filter(|d| Some(d) != notebook.as_ref());
    let mut options = vec![(NOTEBOOK.to_owned(), None)];
    if let Some(folder) = &desktop {
        options.push((
            format!("Desktop — {}", basename(folder)),
            Some(folder.clone()),
        ));
    }
    if let Some(root) =
        root.filter(|r| Some(*r) != notebook.as_deref() && Some(*r) != desktop.as_deref())
    {
        options.push((basename(root).to_owned(), Some(root.to_owned())));
    }
    options.push((BROWSE.to_owned(), None));
    options
}

/// The folder of the local agent of the leaf the window acts on.
fn desktop_folder(facts: &Facts, (desktop, leaf): Acting) -> Option<String> {
    let session = crate::desktop::session_of_leaf(desktop, leaf)?;
    let session = facts.sessions.iter().find(|s| s.id == session)?;
    if session.host.as_deref().is_some_and(|h| !h.is_empty()) {
        return None;
    }
    Some(session.directory.trim().to_owned()).filter(|d| !d.is_empty())
}

fn basename(path: &str) -> &str {
    path.trim_end_matches('/')
        .rsplit('/')
        .next()
        .unwrap_or(path)
}

fn select(
    facts: &Facts,
    notebook: &Notebook,
    acting: Option<Acting>,
    tile: &str,
    params: &str,
) -> Select {
    let root = Params::parse(params).root;
    let options = options(facts, acting, root.as_deref());
    let current = options
        .iter()
        .find(|(label, folder)| label != BROWSE && *folder == root)
        .map(|(label, _)| label.clone());
    let labels: Vec<String> = options.iter().map(|(l, _)| l.clone()).collect();
    let tile = tile.to_owned();
    let (toggled, closed) = (tile.clone(), tile.clone());
    Select::new(select_key(&tile), &labels)
        .label("Editor root")
        .appearance(SelectAppearance::Field)
        .width(PICKER_WIDTH)
        .height(PICKER_HEIGHT)
        .current(current.as_deref())
        .open(notebook.root_picker.as_deref() == Some(tile.as_str()))
        .on_toggle(move |state: &mut State| {
            state.notebook.revision += 1;
            state.notebook.root_picker = match state.notebook.root_picker.take() {
                Some(open) if open == toggled => None,
                _ => Some(toggled.clone()),
            };
        })
        .on_close(move |state: &mut State| {
            if state.notebook.root_picker.as_deref() == Some(closed.as_str()) {
                state.notebook.revision += 1;
                state.notebook.root_picker = None;
            }
        })
        .on_choose(move |state: &mut State, label: &str, tx: &mut Tx| {
            state.notebook.revision += 1;
            state.notebook.root_picker = None;
            if label == BROWSE {
                let tile = tile.clone();
                return crate::folders::choose(
                    state,
                    crate::folders::FolderFor::EditorRoot { tile },
                    None,
                    tx,
                );
            }
            let root = options
                .iter()
                .find(|(l, _)| l == label)
                .and_then(|(_, f)| f.clone());
            switch(state, &tile, root, tx);
        })
}

/// The picker as the tile header shows it.
pub(crate) fn trigger(
    facts: &Facts,
    notebook: &Notebook,
    acting: Acting,
    tile: &str,
    params: &str,
    t: &Tokens,
) -> El {
    let mut unused = Vec::new();
    select(facts, notebook, Some(acting), tile, params).build(attn_ui::kit(t), &mut unused)
}

/// The picker's open list, over the window.
pub(crate) fn overlay(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let Some(tile) = state.notebook.root_picker.as_deref() else {
        return;
    };
    let Some((_, params)) = io::tile_params(state, tile) else {
        return;
    };
    let acting = state.window.acting(&state.facts);
    let acting = acting
        .as_ref()
        .map(|(desktop, leaf)| (*desktop, leaf.as_str()));
    select(&state.facts, &state.notebook, acting, tile, &params).build(attn_ui::kit(t), layers);
}

/// Points the tile at `root` once its edit is saved, dropping the open file.
pub(crate) fn switch(state: &mut State, tile: &str, root: Option<String>, tx: &mut Tx) {
    let tile = tile.to_owned();
    let id = SurfaceId::Tile(tile.clone());
    io::leave_then(state, &id, tx, move |state, tx| {
        let params = Params { root, path: None }.wire();
        io::update_tile(state, &tile, params, tx);
    });
}
