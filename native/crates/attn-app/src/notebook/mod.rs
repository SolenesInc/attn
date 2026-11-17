//! The notebook: the user's folder of markdown notes, read and edited fullscreen or in an
//! editor tile beside the terminals.

mod editor;
mod file_kind;
mod finder;
mod io;
pub(crate) mod links;
mod model;
mod outline;
mod params;
mod root_picker;
mod view;

use attn_client::Connection;
use attn_protocol::{
    DesktopDockTileMessage, FsUnwatchMessage, FsWatchMessage, FsWatchResultMessage,
};
use rune::Tx;

pub use model::Notebook;
pub(crate) use root_picker::{overlay as root_picker_overlay, trigger as root_picker};
pub(crate) use view::{fullscreen, tile_body};

use io::{Place, write};
use model::{Doc, Home, Surface, SurfaceId};
pub(crate) use params::Params;

use crate::State;
use crate::opener::NOTEBOOK_ROOT;
use crate::requests::Refused;
use crate::shell::Layer;

/// How long after the last keystroke an edit saves itself.
const AUTOSAVE_S: f64 = 0.7;
/// How long "Saved" shows after a save.
const SAVED_S: f64 = 2.5;
/// The share of the desktop a new editor tile takes, as the Tauri app docks it.
const TILE_SHARE: f64 = 0.4;

/// The profile's notebook folder, as the daemon resolved it.
pub(crate) fn notebook_root(state: &State) -> Option<String> {
    notebook_root_of(&state.facts)
}

pub(crate) fn notebook_root_of(facts: &attn_client::Facts) -> Option<String> {
    facts
        .settings
        .get(NOTEBOOK_ROOT)
        .filter(|r| !r.is_empty())
        .cloned()
}

/// Opens the notebook over the whole window, on the file it showed last.
pub(crate) fn open_fullscreen(state: &mut State, tx: &mut Tx) {
    state.window.layer = Some(Layer::Notebook);
    if state.notebook.fullscreen.is_none() {
        start_fullscreen(state, tx);
    }
}

fn start_fullscreen(state: &mut State, tx: &mut Tx) {
    let changes = changes_of(state, None);
    state.notebook.fullscreen = Some(Surface::new(None, notebook_root(state), changes));
    let id = SurfaceId::Fullscreen;
    io::list(state, &id, "", tx);
    match state.notebook.last_path.clone() {
        Some(path) => io::read(state, &id, path, tx),
        None => open_preferred(state, tx),
    }
}

/// Closes the fullscreen notebook once its edit is saved.
pub(crate) fn close_fullscreen(state: &mut State, tx: &mut Tx) {
    io::leave_then(state, &SurfaceId::Fullscreen, tx, |state, _| {
        state.notebook.fullscreen = None;
        if state.window.layer == Some(Layer::Notebook) {
            state.window.layer = None;
        }
    });
}

/// The first of the preferred notes that reads, else the first file of the root.
fn open_preferred(state: &mut State, tx: &mut Tx) {
    let id = SurfaceId::Fullscreen;
    let mut paths = io::PREFERRED
        .iter()
        .map(|p| (*p).to_owned())
        .collect::<Vec<_>>();
    paths.reverse();
    try_next(state, id, paths, tx);
}

fn try_next(state: &mut State, id: SurfaceId, mut paths: Vec<String>, tx: &mut Tx) {
    let Some(path) = paths.pop() else {
        return first_file(state, id, tx);
    };
    let asked = path.clone();
    state.ask(
        move |request_id| attn_protocol::FsReadMessage {
            path: asked,
            request_id: Some(request_id),
            ..attn_protocol::FsReadMessage::new()
        },
        move |state, answer, tx| {
            let read = answer
                .outcome()
                .ok()
                .and_then(|r| r.parse::<attn_protocol::FsReadResultMessage>())
                .and_then(|r| r.result);
            if !matches!(
                state.notebook.surface(&id).map(|s| &s.doc),
                Some(Doc::Empty)
            ) {
                return;
            }
            match read {
                Some(_) => io::read(state, &id, path, tx),
                None => try_next(state, id, paths, tx),
            }
        },
        tx,
    );
}

fn first_file(state: &mut State, id: SurfaceId, tx: &mut Tx) {
    state.ask(
        |request_id| attn_protocol::FsListMessage {
            request_id: Some(request_id),
            ..attn_protocol::FsListMessage::new()
        },
        move |state, answer, tx| {
            let first = answer
                .parse::<attn_protocol::FsListResultMessage>()
                .and_then(|r| r.entries)
                .and_then(|e| e.into_iter().find(|e| !e.is_dir));
            if let Some(first) = first
                && matches!(
                    state.notebook.surface(&id).map(|s| &s.doc),
                    Some(Doc::Empty)
                )
            {
                io::read(state, &id, first.path, tx);
            }
        },
        tx,
    );
}

/// Docks an editor tile beside the active leaf, rooted at the agent on screen's folder when
/// that is not the notebook.
pub(crate) fn open_tile(state: &mut State, tx: &mut Tx) {
    let root = tile_root(state);
    open_tile_at(state, Params { root, path: None }, tx);
}

/// Docks an editor tile beside the active leaf, rooted at `params.root` with `params.path`
/// open.
pub(crate) fn open_tile_at(state: &mut State, params: Params, tx: &mut Tx) {
    let Some(desktop) = state.facts.current_desktop().map(|d| d.id.clone()) else {
        return;
    };
    let tile_id = format!("notebook-tile-{}", uuid::Uuid::new_v4());
    let docked = (desktop.clone(), tile_id.clone());
    state.act(
        move |request_id| DesktopDockTileMessage {
            request_id,
            desktop_id: desktop,
            tile_id,
            tile_kind: "notebook".into(),
            tile_params: (params.root.is_some() || params.path.is_some()).then(|| params.wire()),
            edge: "right".into(),
            tile_share: Some(TILE_SHARE),
            ..DesktopDockTileMessage::new()
        },
        Refused::Prefixed("Could not open the notebook"),
        move |state, _, tx| {
            let (desktop, tile) = docked;
            state.window.show_desktop(tx);
            crate::desktop::show_leaf(state, &desktop, &tile, tx);
        },
        tx,
    );
}

/// The folder of the local agent the window shows, unless it is the notebook.
fn tile_root(state: &State) -> Option<String> {
    let session = crate::opener::behind_screen(state)?;
    if session.host.as_deref().is_some_and(|h| !h.is_empty()) {
        return None;
    }
    let directory = session.directory.trim().to_owned();
    (!directory.is_empty() && Some(&directory) != notebook_root(state).as_ref())
        .then_some(directory)
}

/// How many times files under `root` changed, the notebook's when `root` is `None`.
fn changes_of(state: &State, root: Option<&str>) -> u64 {
    let key = match root {
        Some(root) => state
            .notebook
            .watches
            .get(root)
            .cloned()
            .flatten()
            .unwrap_or_else(|| root.to_owned()),
        None => notebook_root(state).unwrap_or_default(),
    };
    state.facts.file_changes.get(&key).copied().unwrap_or(0)
}

/// The notebook tiles this window shows, with their params.
fn shown_tiles(state: &State) -> Vec<(String, Params)> {
    let Some((desktop, leaves)) = crate::desktop::drawn_leaves(state) else {
        return Vec::new();
    };
    if leaves.is_empty() {
        return Vec::new();
    }
    desktop
        .leaves()
        .into_iter()
        .filter_map(|leaf| match leaf.tile() {
            Some(("notebook", params)) if leaves.contains(&leaf.id) => {
                Some((leaf.id.clone(), Params::parse(params)))
            }
            _ => None,
        })
        .collect()
}

/// Keeps every surface in step with the window: a tile that comes on screen opens its file or
/// takes back its edit, a tile that leaves saves its edit on the way out, files that changed
/// on disk are read again, and edits save themselves a moment after the last keystroke.
pub(crate) fn follow(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    if state.facts.connection != Connection::Connected {
        state.notebook.watches.clear();
        return false;
    }
    let before = state.notebook.revision;
    let mut changed = follow_focus(state, tx);
    changed |= place_tiles(state, tx);
    changed |= hand_settled_to_main(state);
    changed |= bring_forward(state, tx);
    changed |= follow_watches(state, tx);
    changed |= follow_notebook(state, tx);
    let ids: Vec<SurfaceId> = state
        .notebook
        .fullscreen
        .iter()
        .map(|_| SurfaceId::Fullscreen)
        .chain(state.notebook.tiles.keys().cloned().map(SurfaceId::Tile))
        .collect();
    for id in ids {
        changed |= follow_disk(state, &id, tx);
        changed |= follow_save(state, &id, now, tx);
        follow_links(state, &id, tx);
    }
    state.notebook.revision = before + u64::from(changed);
    changed
}

fn place_tiles(state: &mut State, tx: &mut Tx) -> bool {
    let may_show = crate::desktop::drawn_leaves(state).is_some_and(|(d, _)| {
        d.leaves()
            .iter()
            .any(|l| matches!(l.tile(), Some(("notebook", _))))
    });
    if state.notebook.tiles.is_empty() && !may_show {
        return false;
    }
    let shown = shown_tiles(state);
    let leaving: Vec<String> = state
        .notebook
        .tiles
        .iter()
        .filter(|(id, surface)| {
            !shown
                .iter()
                .any(|(s, params)| s == *id && params.root == surface.root)
        })
        .map(|(id, _)| id.clone())
        .collect();
    let mut changed = !leaving.is_empty();
    for tile in leaving {
        let Some(surface) = state.notebook.tiles.remove(&tile) else {
            continue;
        };
        state
            .notebook
            .after_save
            .remove(&SurfaceId::Tile(tile.clone()));
        if let Doc::Open(open) = surface.doc {
            shelve(state, tile, surface.root, *open, tx);
        }
    }
    for (tile, params) in shown {
        if state.notebook.tiles.contains_key(&tile) {
            continue;
        }
        changed = true;
        let changes = changes_of(state, params.root.as_deref());
        let surface = Surface::new(params.root.clone(), notebook_root(state), changes);
        let returning = state
            .notebook
            .shelf
            .take_newest(&tile, params.root.as_deref());
        state.notebook.tiles.insert(tile.clone(), surface);
        let id = SurfaceId::Tile(tile.clone());
        io::list(state, &id, "", tx);
        match (returning, params.path) {
            (Some(open), _) => show_returning(state, &tile, open, tx),
            (None, Some(path)) => io::read(state, &id, path, tx),
            (None, None) => finder::open(state, id, tx),
        }
    }
    changed
}

/// Keeps the edit of a tile that left the screen until it is on disk: it saves now unless a
/// save is in flight or the person has a conflict to settle first.
fn shelve(state: &mut State, tile: String, root: Option<String>, open: model::Open, tx: &mut Tx) {
    let (writing, conflict) = (open.save.writing.is_some(), open.save.conflict.is_some());
    if !(open.dirty() || writing || conflict) {
        return;
    }
    let save = open.dirty() && !writing && !conflict;
    let id = state.notebook.shelf.put(tile, root, open);
    if save {
        write(state, Place::Shelf(id), None, tx);
    }
}

/// Shows `open`, the edit of tile `tile` back from the shelf or another window, on the tile's
/// surface, which drops any answer for what it was loading.
fn show_returning(state: &mut State, tile: &str, open: model::Open, tx: &mut Tx) {
    let Some(surface) = state.notebook.tiles.get_mut(tile) else {
        return;
    };
    if let Home::Notebook(read_in) = &open.home {
        surface.notebook.clone_from(read_in);
    }
    surface.load += 1;
    let backlinks = (open.kind == file_kind::FileKind::Markdown && surface.on_notebook())
        .then(|| open.path.clone());
    surface.doc = Doc::Open(Box::new(open));
    if let Some(path) = backlinks {
        io::backlinks(state, &SurfaceId::Tile(tile.to_owned()), path, tx);
    }
}

/// A tile window keeps the edit of a tile it no longer shows only while its save is in flight;
/// then the main window takes it, so a save that failed or conflicted is never left in a window
/// the person cannot see.
fn hand_settled_to_main(state: &mut State) -> bool {
    if state.window.tile.is_none() {
        return false;
    }
    let settled = state.notebook.shelf.settled();
    for id in &settled {
        if let Some(shelved) = state.notebook.shelf.remove(*id) {
            let (tile, root, open) = shelved.into_parts();
            let handed = open.hand();
            crate::desktop::to_main(
                state,
                Box::new(move |state: &mut State, tx: &mut Tx| {
                    take(state, tile, root, handed.open(), tx)
                }),
            );
        }
    }
    !settled.is_empty()
}

/// The main window takes the edit a tile window settled: it waits on the shelf, saving again
/// if it can, and its tile shows it once the tile is on screen here showing what disk holds;
/// the window says why when it conflicts and the tile cannot show it yet.
fn take(state: &mut State, tile: String, root: Option<String>, open: model::Open, tx: &mut Tx) {
    let notice = open
        .save
        .conflict
        .is_some()
        .then(|| io::save_notice(&open))
        .flatten();
    shelve(state, tile.clone(), root, open, tx);
    state.notebook.revision += 1;
    if !show_settled(state, &tile, tx)
        && let Some(notice) = notice
    {
        state.window.notify(notice, tx);
    }
}

/// Each tile on screen showing what disk holds shows the edit of it that waits for the person.
fn bring_forward(state: &mut State, tx: &mut Tx) -> bool {
    let tiles: Vec<String> = state.notebook.tiles.keys().cloned().collect();
    let mut changed = false;
    for tile in tiles {
        changed |= show_settled(state, &tile, tx);
    }
    changed
}

/// Shows on tile `tile` its oldest shelved edit with no save in flight, in place of what disk
/// holds, when the tile shows nothing else.
fn show_settled(state: &mut State, tile: &str, tx: &mut Tx) -> bool {
    let Some(surface) = state.notebook.tiles.get(tile) else {
        return false;
    };
    let shows_disk = surface
        .open()
        .is_none_or(|o| !o.dirty() && o.save.writing.is_none() && o.save.conflict.is_none());
    if !shows_disk {
        return false;
    }
    let root = surface.root.clone();
    let Some(open) = state.notebook.shelf.take_settled(tile, root.as_deref()) else {
        return false;
    };
    show_returning(state, tile, open, tx);
    true
}

/// Watches each folder an on-screen tile reads outside the notebook, and stops watching the
/// ones no tile reads.
fn follow_watches(state: &mut State, tx: &mut Tx) -> bool {
    let notebook = notebook_root(state);
    let wanted: Vec<String> = state
        .notebook
        .tiles
        .values()
        .filter_map(|s| s.root.clone())
        .filter(|r| Some(r) != notebook.as_ref())
        .collect();
    let stale: Vec<(String, Option<String>)> = state
        .notebook
        .watches
        .iter()
        .filter(|(root, _)| !wanted.contains(root))
        .map(|(r, resolved)| (r.clone(), resolved.clone()))
        .collect();
    let mut changed = false;
    for (root, resolved) in stale {
        state.notebook.watches.remove(&root);
        if let Some(resolved) = resolved {
            unwatch(state, resolved, tx);
        }
        changed = true;
    }
    for root in wanted {
        if state.notebook.watches.contains_key(&root) {
            continue;
        }
        state.notebook.watches.insert(root.clone(), None);
        changed = true;
        let asked = root.clone();
        state.ask(
            move |request_id| FsWatchMessage {
                root: Some(asked),
                request_id: Some(request_id),
                ..FsWatchMessage::new()
            },
            move |state, answer, tx| {
                let Some(resolved) = answer
                    .parse::<FsWatchResultMessage>()
                    .filter(|r| r.success)
                    .and_then(|r| r.root)
                    .filter(|r| !r.is_empty())
                else {
                    return;
                };
                match state.notebook.watches.get_mut(&root) {
                    Some(slot) => *slot = Some(resolved),
                    None => unwatch(state, resolved, tx),
                }
            },
            tx,
        );
    }
    changed
}

fn unwatch(state: &mut State, root: String, tx: &mut Tx) {
    state.ask(
        move |request_id| FsUnwatchMessage {
            root: Some(root),
            request_id: Some(request_id),
            ..FsUnwatchMessage::new()
        },
        |_, _, _| {},
        tx,
    );
}

/// Moves each surface on the profile's notebook to the folder the notebook is now, once its
/// edit is saved into the folder it was read from. A surface whose save conflicts or fails
/// stays until the user settles it.
fn follow_notebook(state: &mut State, tx: &mut Tx) -> bool {
    let now = notebook_root(state);
    let notebook = &state.notebook;
    let moving: Vec<SurfaceId> = notebook
        .fullscreen
        .iter()
        .map(|s| (SurfaceId::Fullscreen, s))
        .chain(
            notebook
                .tiles
                .iter()
                .map(|(t, s)| (SurfaceId::Tile(t.clone()), s)),
        )
        .filter(|(id, s)| {
            s.on_notebook()
                && s.notebook != now
                && !notebook.after_save.contains_key(id)
                && s.open()
                    .is_none_or(|o| o.save.conflict.is_none() && o.save.error.is_none())
        })
        .map(|(id, _)| id)
        .collect();
    let changed = !moving.is_empty();
    for id in moving {
        let reopened = id.clone();
        io::leave_then(state, &id, tx, move |state, tx| match reopened {
            SurfaceId::Fullscreen => start_fullscreen(state, tx),
            SurfaceId::Tile(tile) => {
                state.notebook.tiles.remove(&tile);
                place_tiles(state, tx);
            }
        });
    }
    changed
}

/// Lists the tree again and reads the open file when files under the surface's folder changed.
fn follow_disk(state: &mut State, id: &SurfaceId, tx: &mut Tx) -> bool {
    let Some(surface) = state.notebook.surface(id) else {
        return false;
    };
    let now = changes_of(state, surface.root.as_deref());
    if now == surface.changes {
        return false;
    }
    let folders: Vec<String> = std::iter::once(String::new())
        .chain(surface.tree.expanded.iter().cloned())
        .collect();
    if let Some(surface) = state.notebook.surface_mut(id) {
        surface.changes = now;
        if let Some(model::Open {
            draft: model::Draft::Note(editor),
            ..
        }) = surface.open_mut()
        {
            editor.links_generation += 1;
            editor.checked.clear();
            editor.links_revision = None;
        }
    }
    for folder in folders {
        io::list(state, id, &folder, tx);
    }
    io::follow_disk(state, id, tx);
    true
}

/// Saves an edit a moment after its last keystroke, and lets "Saved" fade.
fn follow_save(state: &mut State, id: &SurfaceId, now: f64, tx: &mut Tx) -> bool {
    let Some(open) = state.notebook.surface_mut(id).and_then(Surface::open_mut) else {
        return false;
    };
    let mut changed = false;
    if let Some(at) = open.save.saved_at {
        if now >= at + SAVED_S {
            open.save.saved_at = None;
            changed = true;
        } else {
            tx.wake_at(at + SAVED_S);
        }
    }
    let due = open.edited_at.is_some()
        && open.save.writing.is_none()
        && open.save.conflict.is_none()
        && open.dirty();
    if !due {
        return changed;
    }
    let at = open.edited_at.unwrap_or(now) + AUTOSAVE_S;
    if now < at {
        tx.wake_at(at);
        return changed;
    }
    open.edited_at = None;
    write(state, Place::Surface(id.clone()), None, tx);
    true
}

/// Notes that the surface's edit changed now, so it saves itself once typing pauses.
pub(crate) fn edited(state: &mut State, id: &SurfaceId, tx: &mut Tx) {
    let now = tx.clock();
    if let Some(open) = state.notebook.surface_mut(id).and_then(Surface::open_mut) {
        open.edited_at = Some(now);
        open.save.saved_at = None;
    }
    tx.wake_at(now + AUTOSAVE_S);
}

/// Saves the edit over whatever disk holds now, as the user chose.
pub(crate) fn overwrite(state: &mut State, id: &SurfaceId, tx: &mut Tx) {
    let Some(open) = state.notebook.surface(id).and_then(Surface::open) else {
        return;
    };
    let base = open.save.conflict.clone().unwrap_or(None);
    write(state, Place::Surface(id.clone()), Some(base), tx);
}

/// Drops the edit and reads what disk holds now.
pub(crate) fn reload(state: &mut State, id: &SurfaceId, tx: &mut Tx) {
    let Some(path) = state
        .notebook
        .surface(id)
        .and_then(Surface::open)
        .map(|o| o.path.clone())
    else {
        return;
    };
    if let Some(surface) = state.notebook.surface_mut(id) {
        surface.doc = Doc::Empty;
    }
    io::read(state, id, path, tx);
}

/// Opens the note finder for the notebook surface in front, if there is one.
pub(crate) fn find_note(state: &mut State, tx: &mut Tx) -> bool {
    let Some(id) = finder::target(state) else {
        return false;
    };
    finder::open(state, id, tx);
    true
}

/// The surface whose editor is keyed `key`.
fn surface_of_editor(key: &str) -> Option<SurfaceId> {
    let rest = key.strip_suffix(".editor")?;
    if rest == "notebook" {
        return Some(SurfaceId::Fullscreen);
    }
    rest.strip_prefix("notebook.tile.")
        .map(|tile| SurfaceId::Tile(tile.to_owned()))
}

pub(crate) fn editor_of<'a>(state: &'a State, key: &str) -> Option<&'a editor::Editor> {
    let id = surface_of_editor(key)?;
    match &state.notebook.surface(&id)?.open()?.draft {
        model::Draft::Note(editor) => Some(editor),
        model::Draft::Field(_) => None,
    }
}

pub(crate) fn editor_of_mut<'a>(state: &'a mut State, key: &str) -> Option<&'a mut editor::Editor> {
    let id = surface_of_editor(key)?;
    match &mut state.notebook.surface_mut(&id)?.open_mut()?.draft {
        model::Draft::Note(editor) => Some(editor),
        model::Draft::Field(_) => None,
    }
}

/// The editor keyed `key` changed: its surface saves once typing pauses.
pub(crate) fn edited_editor(state: &mut State, key: &str, tx: &mut Tx) {
    let Some(id) = surface_of_editor(key) else {
        return;
    };
    let dirty = state
        .notebook
        .surface(&id)
        .and_then(Surface::open)
        .is_some_and(model::Open::dirty);
    if dirty {
        edited(state, &id, tx);
    } else {
        state.notebook.revision += 1;
    }
}

/// Follows `href` from the note the editor keyed `key` shows: another note opens in the same
/// surface, an anchor scrolls to its heading, anything else opens outside.
pub(crate) fn follow_link(state: &mut State, key: &str, href: &str, tx: &mut Tx) {
    let Some(id) = surface_of_editor(key) else {
        return;
    };
    let Some(path) = state
        .notebook
        .surface(&id)
        .and_then(Surface::open)
        .map(|o| o.path.clone())
    else {
        return;
    };
    match links::resolve(href, links::note_dir(&path)) {
        links::NoteLink::Note { path: target, .. } if target != path => {
            io::open(state, &id, target, tx);
        }
        links::NoteLink::Note {
            anchor: Some(anchor),
            ..
        }
        | links::NoteLink::Fragment(anchor) => {
            scroll_to_heading(state, key, &anchor, tx);
        }
        links::NoteLink::Note { .. } => {}
        links::NoteLink::External(url) if !url.is_empty() => {
            let _ = tx.open_uri(&url);
        }
        links::NoteLink::External(_) => {}
    }
}

/// Puts the caret on the heading `anchor` names and scrolls it to the top.
fn scroll_to_heading(state: &mut State, key: &str, anchor: &str, tx: &mut Tx) {
    let Some(editor) = editor_of(state, key) else {
        return;
    };
    let slug = links::heading_slug(anchor);
    let line = outline::outline(&editor.text())
        .into_iter()
        .find(|h| links::heading_slug(&h.text) == slug || h.text.eq_ignore_ascii_case(anchor))
        .map(|h| h.line);
    reveal_line(state, key, line, tx);
}

/// Puts the caret on the surface's `index`th outline heading and scrolls it to the top.
pub(crate) fn reveal_heading(state: &mut State, id: &SurfaceId, index: usize, tx: &mut Tx) {
    let key = view::key(id, "editor");
    let line = editor_of(state, &key)
        .and_then(|editor| outline::outline(&editor.text()).into_iter().nth(index))
        .map(|h| h.line);
    reveal_line(state, &key, line, tx);
}

fn reveal_line(state: &mut State, key: &str, line: Option<usize>, tx: &mut Tx) {
    if let (Some(line), Some(editor)) = (line, editor_of_mut(state, key)) {
        let line = line - 1;
        editor.move_to(editor::Pos::new(line, 0), false, false);
        editor.focused = true;
        let row = format!("{key}.line.{}", editor.doc.id(line));
        tx.scroll_into_view(row.clone());
        tx.scroll_key_to(row, 0.0);
        tx.focus(key.to_owned(), false);
    }
    state.notebook.revision += 1;
}

/// Keeps each editor's caret shown only while it has the keyboard.
fn follow_focus(state: &mut State, tx: &Tx) -> bool {
    let focused = tx.focused().map(str::to_owned);
    let mut changed = false;
    let surfaces = state
        .notebook
        .fullscreen
        .iter_mut()
        .chain(state.notebook.tiles.values_mut());
    for surface in surfaces {
        if let Some(model::Open {
            draft: model::Draft::Note(editor),
            ..
        }) = surface.open_mut()
        {
            let has = focused
                .as_deref()
                .is_some_and(|f| f.ends_with(".editor") && editor.focused);
            if editor.focused && !has {
                editor.focused = false;
                editor.preedit = None;
                changed = true;
            }
        }
    }
    changed
}

/// A paste while a notebook editor has the keyboard goes into it.
pub(crate) fn paste(state: &mut State, text: &str, tx: &mut Tx) -> bool {
    let Some(key) = tx.focused().map(str::to_owned) else {
        return false;
    };
    let now = tx.clock();
    let Some(editor) = editor_of_mut(state, &key) else {
        return false;
    };
    editor::paste(editor, text, now);
    edited_editor(state, &key, tx);
    true
}

/// What an input method composes while a notebook editor has the keyboard.
pub(crate) fn ime(state: &mut State, ime: &rune::Ime, tx: &mut Tx) -> bool {
    let Some(key) = tx.focused().map(str::to_owned) else {
        return false;
    };
    let now = tx.clock();
    let Some(editor) = editor_of_mut(state, &key) else {
        return false;
    };
    match ime {
        rune::Ime::Preedit(text, _) => {
            editor.preedit = Some(text.clone()).filter(|t| !t.is_empty())
        }
        rune::Ime::Commit(text) => {
            editor.preedit = None;
            editor.doc.replace(text, editor::Kind::Typing, now);
            editor.edited();
        }
        rune::Ime::Enabled | rune::Ime::Disabled => return false,
    }
    edited_editor(state, &key, tx);
    true
}

/// Asks in one request whether each note the open note links to exists, once per note,
/// after the note changes; a link to a missing note shows as broken, as the Tauri editor marks it.
fn follow_links(state: &mut State, id: &SurfaceId, tx: &mut Tx) {
    let root = state.notebook.surface(id).and_then(|s| io::root(state, s));
    let Some(model::Open {
        draft: model::Draft::Note(editor),
        path: note,
        ..
    }) = state.notebook.surface(id).and_then(Surface::open)
    else {
        return;
    };
    if editor.links_revision == Some(editor.doc.revision) {
        return;
    }
    let (asked, note) = (editor.unchecked_links(), note.clone());
    let generation = editor.links_generation;
    if let Some(model::Open {
        draft: model::Draft::Note(editor),
        ..
    }) = state.notebook.surface_mut(id).and_then(Surface::open_mut)
    {
        editor.links_revision = Some(editor.doc.revision);
        editor.checked.extend(asked.iter().cloned());
    }
    if asked.is_empty() {
        return;
    }
    let (id, paths) = (id.clone(), asked.clone());
    state.ask(
        move |request_id| attn_protocol::FsExistsManyMessage {
            paths,
            root,
            request_id: Some(request_id),
            ..attn_protocol::FsExistsManyMessage::new()
        },
        move |state, answer, _| {
            let missing: std::collections::BTreeSet<String> = answer
                .parse::<attn_protocol::FsExistsManyResultMessage>()
                .filter(|r| r.success)
                .and_then(|r| r.results)
                .into_iter()
                .flatten()
                .filter(|r| !r.exists)
                .map(|r| r.path)
                .collect();
            if let Some(model::Open {
                draft: model::Draft::Note(editor),
                ..
            }) = state
                .notebook
                .surface_mut(&id)
                .and_then(Surface::open_mut)
                .filter(|o| o.path == note)
                && editor.links_generation == generation
            {
                for path in asked {
                    if missing.contains(&path) {
                        editor.missing.insert(path);
                    } else {
                        editor.missing.remove(&path);
                    }
                }
            }
        },
        tx,
    );
}

/// The images the open notes show, with the folder each is read from and how many times files
/// there changed, for the window's pictures to read and read again.
pub(crate) fn images(state: &State) -> Vec<(Option<String>, String, u64)> {
    let notebook = &state.notebook;
    notebook
        .fullscreen
        .iter()
        .chain(notebook.tiles.values())
        .filter_map(|s| {
            let open = s.open()?;
            match &open.draft {
                model::Draft::Note(editor) => {
                    Some((io::root(state, s), editor.images(), s.changes))
                }
                model::Draft::Field(_) => None,
            }
        })
        .flat_map(|(root, paths, changes)| {
            paths.into_iter().map(move |p| (root.clone(), p, changes))
        })
        .collect()
}

/// Where the note in the editor keyed `key` reads its images from, and its folder.
pub(crate) fn place_of_editor(state: &State, key: &str) -> Option<(Option<String>, String)> {
    let surface = state.notebook.surface(&surface_of_editor(key)?)?;
    let open = surface.open()?;
    Some((
        io::root(state, surface),
        links::note_dir(&open.path).to_owned(),
    ))
}
