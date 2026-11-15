//! What a notebook surface asks the daemon: the folders of its tree, the file it opens, the
//! edits it saves, and the notes that link to the open one.

use attn_protocol::{
    DesktopUpdateTileMessage, FsListMessage, FsListResultMessage, FsReadMessage,
    FsReadResultMessage, FsWriteMessage, FsWriteResultMessage, NotebookBacklinksMessage,
    NotebookBacklinksResultMessage,
};
use rune::Tx;

use super::file_kind::{FileKind, file_kind};
use super::model::{Backlinks, Doc, Home, Listing, Open, Surface, SurfaceId, shelf_key};
use super::params::Params;
use crate::State;
use crate::requests::Refused;

pub(crate) const PREFERRED: [&str; 2] = ["knowledge/index.md", "index.md"];

/// The `root` a request carries: none for the notebook, else the folder the surface reads.
pub(crate) fn root(state: &State, surface: &Surface) -> Option<String> {
    let root = surface.root.clone()?;
    Some(
        state
            .notebook
            .watches
            .get(&root)
            .cloned()
            .flatten()
            .unwrap_or(root),
    )
}

fn root_of(state: &State, id: &SurfaceId) -> Option<String> {
    state.notebook.surface(id).and_then(|s| root(state, s))
}

/// The folder a file the surface reads now comes from.
fn home_of(state: &State, id: &SurfaceId) -> Home {
    match root_of(state, id) {
        Some(root) => Home::Folder(root),
        None => Home::Notebook(super::notebook_root(state)),
    }
}

/// The `root` a request about a file from `home` carries: none while the notebook is still
/// the folder the file came from.
fn home_root(state: &State, home: &Home) -> Option<String> {
    match home {
        Home::Folder(root) => Some(root.clone()),
        Home::Notebook(read_in) => read_in
            .clone()
            .filter(|r| Some(r) != super::notebook_root(state).as_ref()),
    }
}

/// Lists `folder` of the surface's tree, keeping what it showed until the answer.
pub(crate) fn list(state: &mut State, id: &SurfaceId, folder: &str, tx: &mut Tx) {
    let root = root_of(state, id);
    let Some(surface) = state.notebook.surface_mut(id) else {
        return;
    };
    surface
        .tree
        .folders
        .entry(folder.to_owned())
        .or_insert(Listing::Loading);
    let (id, folder) = (id.clone(), folder.to_owned());
    let asked = folder.clone();
    state.ask(
        move |request_id| FsListMessage {
            path: Some(asked).filter(|p| !p.is_empty()),
            root,
            request_id: Some(request_id),
            ..FsListMessage::new()
        },
        move |state, answer, _| {
            let listing = match answer.outcome() {
                Ok(reply) => match reply.parse::<FsListResultMessage>() {
                    Some(result) => {
                        let mut entries = result.entries.unwrap_or_default();
                        entries.sort_by(|a, b| {
                            b.is_dir
                                .cmp(&a.is_dir)
                                .then_with(|| a.name.to_lowercase().cmp(&b.name.to_lowercase()))
                        });
                        Listing::Listed(entries)
                    }
                    None => Listing::Failed("Filesystem list failed".into()),
                },
                Err(error) => Listing::Failed(error),
            };
            if let Some(surface) = state.notebook.surface_mut(&id) {
                surface.tree.folders.insert(folder, listing);
            }
        },
        tx,
    );
}

/// Opens `path` in the surface once its unsaved edit is saved; a save that conflicts or fails
/// keeps the surface where it is.
pub(crate) fn open(state: &mut State, id: &SurfaceId, path: String, tx: &mut Tx) {
    let id = id.clone();
    leave_then(state, &id.clone(), tx, move |state, tx| {
        read(state, &id, path, tx)
    });
}

/// Runs `then` once the surface's edit is on disk, saving it first, and again when it changed
/// while a save was in flight; runs nothing when a save conflicts or fails, which the surface
/// then shows.
pub(crate) fn leave_then(
    state: &mut State,
    id: &SurfaceId,
    tx: &mut Tx,
    then: impl FnOnce(&mut State, &mut Tx) + 'static,
) {
    let Some(open) = state.notebook.surface(id).and_then(Surface::open) else {
        return then(state, tx);
    };
    if open.save.conflict.is_some() {
        return;
    }
    if !open.dirty() && open.save.writing.is_none() {
        return then(state, tx);
    }
    let writing = open.save.writing.is_some();
    let path = open.path.clone();
    state.notebook.after_save.insert(id.clone(), Box::new(then));
    if !writing {
        write(state, Target::Surface(id.clone(), path), None, tx);
    }
}

/// Reads `path` into the surface: a file it cannot show opens without a read.
pub(crate) fn read(state: &mut State, id: &SurfaceId, path: String, tx: &mut Tx) {
    if matches!(id, SurfaceId::Fullscreen) {
        state.notebook.last_path = Some(path.clone());
    }
    remember_path(state, id, &path, tx);
    let root = root_of(state, id);
    let home = home_of(state, id);
    let Some(surface) = state.notebook.surface_mut(id) else {
        return;
    };
    surface.load += 1;
    surface.backlinks = Backlinks::None;
    let kind = file_kind(&path);
    if kind == FileKind::Binary {
        surface.doc = Doc::Binary { path };
        return;
    }
    let shown = match std::mem::replace(&mut surface.doc, Doc::Empty) {
        Doc::Open(open) => Some(open),
        Doc::Loading { shown, .. } => shown,
        _ => None,
    };
    surface.doc = Doc::Loading {
        path: path.clone(),
        shown,
    };
    let (load, id, asked) = (surface.load, id.clone(), path.clone());
    state.ask(
        move |request_id| FsReadMessage {
            path: asked,
            root,
            request_id: Some(request_id),
            ..FsReadMessage::new()
        },
        move |state, answer, tx| {
            let read = answer.outcome().and_then(|reply| {
                reply
                    .parse::<FsReadResultMessage>()
                    .and_then(|r| r.result)
                    .ok_or_else(|| "Could not read this file".to_owned())
            });
            let Some(surface) = state.notebook.surface_mut(&id).filter(|s| s.load == load) else {
                return;
            };
            match read {
                Ok(file) => {
                    let open = Open::read(home, path.clone(), kind, file.content, file.hash);
                    surface.doc = Doc::Open(Box::new(open));
                    if kind == FileKind::Markdown && surface.on_notebook() {
                        backlinks(state, &id, path, tx);
                    }
                }
                Err(error) => surface.doc = Doc::Failed { path, error },
            }
        },
        tx,
    );
}

/// Reads the open file again after it changed on disk, unless the surface holds an edit; an
/// unchanged hash changes nothing.
pub(crate) fn follow_disk(state: &mut State, id: &SurfaceId, tx: &mut Tx) {
    let Some(surface) = state.notebook.surface(id) else {
        return;
    };
    let load = surface.load;
    let Doc::Open(open) = &surface.doc else {
        return;
    };
    if open.dirty() || open.save.writing.is_some() || open.save.conflict.is_some() {
        return;
    }
    let root = home_root(state, &open.home);
    let (id, path) = (id.clone(), open.path.clone());
    let asked = path.clone();
    state.ask(
        move |request_id| FsReadMessage {
            path: asked,
            root,
            request_id: Some(request_id),
            ..FsReadMessage::new()
        },
        move |state, answer, tx| {
            let read = answer.outcome().and_then(|reply| {
                reply
                    .parse::<FsReadResultMessage>()
                    .and_then(|r| r.result)
                    .ok_or_else(|| "Could not read this file".to_owned())
            });
            let Some(surface) = state.notebook.surface_mut(&id).filter(|s| s.load == load) else {
                return;
            };
            let on_notebook = surface.on_notebook();
            let Some(open) = surface.open_mut().filter(|o| o.path == path) else {
                return;
            };
            if open.dirty() || open.save.writing.is_some() {
                return;
            }
            match read {
                Ok(file) if file.hash == open.hash => {}
                Ok(file) => {
                    open.reread(file.content, file.hash);
                    if open.kind == FileKind::Markdown && on_notebook {
                        backlinks(state, &id, path, tx);
                    }
                }
                Err(error) => surface.doc = Doc::Failed { path, error },
            }
        },
        tx,
    );
}

/// Asks for the notes that link to `path`, dropping an answer for a file the surface left.
pub(crate) fn backlinks(state: &mut State, id: &SurfaceId, path: String, tx: &mut Tx) {
    let Some(surface) = state.notebook.surface_mut(id) else {
        return;
    };
    surface.backlinks = Backlinks::Loading;
    let (load, id) = (surface.load, id.clone());
    state.ask(
        move |request_id| NotebookBacklinksMessage {
            path,
            request_id: Some(request_id),
            ..NotebookBacklinksMessage::new()
        },
        move |state, answer, _| {
            let entries = answer
                .parse::<NotebookBacklinksResultMessage>()
                .filter(|r| r.success)
                .and_then(|r| r.entries)
                .unwrap_or_default();
            if let Some(surface) = state.notebook.surface_mut(&id).filter(|s| s.load == load) {
                surface.backlinks = Backlinks::Listed(entries);
            }
        },
        tx,
    );
}

/// Whose edit a write saves: a surface's open file, or a tile's edit waiting off screen.
#[derive(Clone)]
pub(crate) enum Target {
    Surface(SurfaceId, String),
    Shelved(String),
}

pub(crate) type Then = Box<dyn FnOnce(&mut State, &mut Tx)>;

/// Where the target's edit is now: open on a surface, or on the shelf by `shelf_key`.
enum Place {
    Surface(SurfaceId),
    Shelf(String),
}

fn locate(state: &State, target: &Target) -> Option<Place> {
    let notebook = &state.notebook;
    match target {
        Target::Surface(id, path) => {
            if notebook
                .surface(id)
                .and_then(Surface::open)
                .is_some_and(|o| o.path == *path)
            {
                return Some(Place::Surface(id.clone()));
            }
            let SurfaceId::Tile(tile) = id else {
                return None;
            };
            let prefix = format!("{tile}\0");
            notebook
                .shelved
                .iter()
                .find(|(key, o)| key.starts_with(&prefix) && o.path == *path)
                .map(|(key, _)| Place::Shelf(key.clone()))
        }
        Target::Shelved(key) => {
            if let Some((tile, _)) = key.split_once('\0')
                && notebook
                    .tiles
                    .get(tile)
                    .is_some_and(|s| shelf_key(tile, s.root.as_deref()) == *key)
            {
                return Some(Place::Surface(SurfaceId::Tile(tile.to_owned())));
            }
            notebook
                .shelved
                .contains_key(key)
                .then(|| Place::Shelf(key.clone()))
        }
    }
}

/// The open file the target names, wherever it is now.
pub(crate) fn target_open<'a>(state: &'a mut State, target: &Target) -> Option<&'a mut Open> {
    match locate(state, target)? {
        Place::Surface(id) => state.notebook.surface_mut(&id)?.open_mut(),
        Place::Shelf(key) => state.notebook.shelved.get_mut(&key),
    }
}

/// Saves the target's edit into the folder it was read from, against the version it read, or
/// over `base` when the user chose to overwrite. A write still in flight is waited for.
pub(crate) fn write(state: &mut State, target: Target, over: Option<Option<String>>, tx: &mut Tx) {
    let write_id = state.notebook.next_write();
    let Some(open) = target_open(state, &target) else {
        return;
    };
    if open.save.writing.is_some() && over.is_none() {
        return;
    }
    let base = match over {
        Some(base) => base,
        None => Some(open.hash.clone()).filter(|h| !h.is_empty()),
    };
    let content = open.draft.text();
    open.save.writing = Some((write_id, content.clone()));
    open.save.error = None;
    open.save.conflict = None;
    let (path, home) = (open.path.clone(), open.home.clone());
    let root = home_root(state, &home);
    let sent = content.clone();
    let asked = path.clone();
    state.ask(
        move |request_id| FsWriteMessage {
            path: asked,
            content,
            base_hash: base,
            root,
            request_id: Some(request_id),
            ..FsWriteMessage::new()
        },
        move |state, answer, tx| {
            let result = answer.outcome().and_then(|reply| {
                reply
                    .parse::<FsWriteResultMessage>()
                    .and_then(|r| r.result)
                    .ok_or_else(|| "Could not save this file".to_owned())
            });
            let now = tx.clock();
            let Some(open) = target_open(state, &target).filter(|o| {
                o.save
                    .writing
                    .as_ref()
                    .is_some_and(|(id, _)| *id == write_id)
            }) else {
                return;
            };
            open.save.writing = None;
            match result {
                Ok(result) if result.conflict => {
                    open.save.conflict = Some(result.current_hash.filter(|h| !h.is_empty()));
                    failed(
                        state,
                        &target,
                        format!("Could not save {path}: it changed on disk"),
                        tx,
                    );
                }
                Ok(result) => {
                    open.disk = sent;
                    open.hash = result.hash.unwrap_or_default();
                    open.save.saved_at = Some(now);
                    landed(state, &target, tx);
                }
                Err(error) => {
                    open.save.error = Some(error.clone());
                    open.edited_at = Some(now);
                    failed(
                        state,
                        &target,
                        format!("Could not save {path}: {error}"),
                        tx,
                    );
                }
            }
        },
        tx,
    );
}

/// A save reached disk: what waited for the surface's edit goes on, saving again first when
/// the edit changed meanwhile, and an edit that changed with nothing waiting saves itself
/// again; an edit on the shelf saves again or leaves it.
fn landed(state: &mut State, target: &Target, tx: &mut Tx) {
    match locate(state, target) {
        Some(Place::Surface(id)) => {
            if let Some(waiting) = state.notebook.after_save.remove(&id) {
                leave_then(state, &id, tx, waiting);
            } else if state
                .notebook
                .surface(&id)
                .and_then(Surface::open)
                .is_some_and(|o| o.dirty() && o.edited_at.is_none())
            {
                super::edited(state, &id, tx);
            }
        }
        Some(Place::Shelf(key)) => {
            forget_waiting(state, &key);
            if state.notebook.shelved.get(&key).is_some_and(Open::dirty) {
                write(state, Target::Shelved(key), None, tx);
            } else {
                state.notebook.shelved.remove(&key);
            }
        }
        None => {}
    }
}

/// A save conflicted or failed: a surface shows why and stays where it is; an edit on the shelf
/// says so in the window, and its tile shows why when it comes back.
fn failed(state: &mut State, target: &Target, notice: String, tx: &mut Tx) {
    match locate(state, target) {
        Some(Place::Surface(id)) => {
            state.notebook.after_save.remove(&id);
        }
        Some(Place::Shelf(key)) => {
            forget_waiting(state, &key);
            state.window.notify(notice, tx);
        }
        None => {}
    }
}

/// A move asked of a tile that then left the screen does not follow it back.
fn forget_waiting(state: &mut State, key: &str) {
    if let Some((tile, _)) = key.split_once('\0') {
        state
            .notebook
            .after_save
            .remove(&SurfaceId::Tile(tile.to_owned()));
    }
}

/// Writes a tile's open file and root into its params, so the tile reopens it.
fn remember_path(state: &mut State, id: &SurfaceId, path: &str, tx: &mut Tx) {
    let SurfaceId::Tile(tile) = id else {
        return;
    };
    let Some(root) = state.notebook.tiles.get(tile).map(|s| s.root.clone()) else {
        return;
    };
    let params = Params {
        root,
        path: Some(path.to_owned()),
    }
    .wire();
    update_tile(state, tile, params, tx);
}

pub(crate) fn update_tile(state: &mut State, tile: &str, params: String, tx: &mut Tx) {
    let Some((desktop, current)) = tile_params(state, tile) else {
        return;
    };
    if current == params {
        return;
    }
    let tile = tile.to_owned();
    state.act(
        move |request_id| DesktopUpdateTileMessage {
            request_id,
            desktop_id: desktop,
            tile_id: tile,
            tile_params: Some(params),
            ..DesktopUpdateTileMessage::new()
        },
        Refused::Quiet,
        |_, _, _| {},
        tx,
    );
}

/// The desktop that holds notebook tile `tile`, and the tile's params.
pub(crate) fn tile_params(state: &State, tile: &str) -> Option<(String, String)> {
    state.facts.desktops.iter().find_map(|desktop| {
        crate::sidebar::model::leaves(&desktop.tree_json)
            .into_iter()
            .find_map(|leaf| match leaf {
                crate::sidebar::model::Leaf::Tile { id, kind, params }
                    if id == tile && kind == "notebook" =>
                {
                    Some((desktop.id.clone(), params))
                }
                _ => None,
            })
    })
}
