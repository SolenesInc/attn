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

use io::{Target, write};
use model::{Doc, Surface, SurfaceId, shelf_key};
use params::Params;

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
    if state.notebook.fullscreen.is_some() {
        return;
    }
    let changes = changes_of(state, None);
    state.notebook.fullscreen = Some(Surface::new(None, changes));
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
    let Some(desktop) = state.facts.current_desktop().map(|d| d.id.clone()) else {
        return;
    };
    let root = tile_root(state);
    let tile_id = format!("notebook-tile-{}", uuid::Uuid::new_v4());
    state.act(
        move |request_id| DesktopDockTileMessage {
            request_id,
            desktop_id: desktop,
            tile_id,
            tile_kind: "notebook".into(),
            tile_params: root.map(|root| {
                Params {
                    root: Some(root),
                    path: None,
                }
                .wire()
            }),
            edge: "right".into(),
            tile_share: Some(TILE_SHARE),
            ..DesktopDockTileMessage::new()
        },
        Refused::Prefixed("Could not open the notebook"),
        |_, _, _| {},
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
    crate::sidebar::model::leaves(&desktop.tree_json)
        .into_iter()
        .filter_map(|leaf| match leaf {
            crate::sidebar::model::Leaf::Tile { id, kind, params }
                if kind == "notebook" && leaves.contains(&id) =>
            {
                Some((id, Params::parse(&params)))
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
    changed |= follow_watches(state, tx);
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
    let may_show = crate::desktop::drawn_leaves(state)
        .is_some_and(|(d, _)| d.tree_json.contains("\"notebook\""));
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
        let key = shelf_key(&tile, surface.root.as_deref());
        if let Doc::Open(open) = surface.doc
            && (open.dirty() || open.save.writing.is_some() || open.save.conflict.is_some())
        {
            let save = open.dirty() && open.save.writing.is_none() && open.save.conflict.is_none();
            state.notebook.shelved.insert(key.clone(), *open);
            if save {
                write(state, Target::Shelved(key), None, tx, None);
            }
        }
    }
    for (tile, params) in shown {
        if state.notebook.tiles.contains_key(&tile) {
            continue;
        }
        changed = true;
        let changes = changes_of(state, params.root.as_deref());
        let mut surface = Surface::new(params.root.clone(), changes);
        let key = shelf_key(&tile, params.root.as_deref());
        let returning = state.notebook.shelved.remove(&key);
        if let Some(open) = returning.as_ref() {
            surface.doc = Doc::Open(Box::new(open.clone()));
        }
        state.notebook.tiles.insert(tile.clone(), surface);
        let id = SurfaceId::Tile(tile);
        io::list(state, &id, "", tx);
        match (returning, params.path) {
            (Some(open), _) => {
                if open.kind == file_kind::FileKind::Markdown && params.root.is_none() {
                    io::backlinks(state, &id, open.path, tx);
                }
            }
            (None, Some(path)) => io::read(state, &id, path, tx),
            (None, None) => finder::open(state, id, tx),
        }
    }
    changed
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
            editor.checked.retain(|p| !editor.missing.contains(p));
            editor.missing.clear();
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
    let path = open.path.clone();
    write(state, Target::Surface(id.clone(), path), None, tx, None);
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
    let path = open.path.clone();
    write(
        state,
        Target::Surface(id.clone(), path),
        Some(base),
        tx,
        None,
    );
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
    let Some(editor) = editor_of_mut(state, key) else {
        return;
    };
    let slug = links::heading_slug(anchor);
    let text = editor.text();
    let heading = outline::outline(&text)
        .into_iter()
        .find(|h| links::heading_slug(&h.text) == slug || h.text.eq_ignore_ascii_case(anchor));
    if let Some(h) = heading {
        let line = h.line - 1;
        editor.move_to(editor::Pos::new(line, 0), false, false);
        editor.focused = true;
        let id = editor.doc.id(line);
        tx.scroll_into_view(format!("{key}.line.{id}"));
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

/// Asks whether each note the open note links to exists, once per note, after the note
/// changes; a link to a missing note shows as broken, as the Tauri editor marks it.
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
    if let Some(model::Open {
        draft: model::Draft::Note(editor),
        ..
    }) = state.notebook.surface_mut(id).and_then(Surface::open_mut)
    {
        editor.links_revision = Some(editor.doc.revision);
        editor.checked.extend(asked.iter().cloned());
    }
    for path in asked {
        let (id, note, root, asked) = (id.clone(), note.clone(), root.clone(), path.clone());
        state.ask(
            move |request_id| attn_protocol::FsExistsMessage {
                path: asked,
                root,
                request_id: Some(request_id),
                ..attn_protocol::FsExistsMessage::new()
            },
            move |state, answer, _| {
                let missing = answer
                    .parse::<attn_protocol::FsExistsResultMessage>()
                    .filter(|r| r.success)
                    .and_then(|r| r.result)
                    .is_some_and(|r| !r.exists);
                if !missing {
                    return;
                }
                if let Some(model::Open {
                    draft: model::Draft::Note(editor),
                    ..
                }) = state
                    .notebook
                    .surface_mut(&id)
                    .and_then(Surface::open_mut)
                    .filter(|o| o.path == note)
                {
                    editor.missing.insert(path);
                }
            },
            tx,
        );
    }
}
