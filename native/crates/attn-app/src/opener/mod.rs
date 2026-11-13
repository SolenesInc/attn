//! The markdown opener (⌘P), as the Tauri app's (MarkdownOpener.tsx): the files opened
//! recently and the markdown files under the folder of the agent behind the screen (else the
//! notebook), ranked as the query is typed; a typed path browses a folder instead. A picked file
//! opens in a tile for that agent.

mod paths;
mod rank;
mod view;

use attn_protocol::{
    BrowseDirectoryMessage, BrowseDirectoryResultMessage, FsIndexMessage, FsIndexResultMessage,
    OpenMarkdownMessage, OpenMarkdownResultMessage, RecentFilesMessage, RecentFilesResultMessage,
};
use rune::{Key, KeyEvent, TextEdit, Tx};

use crate::State;
use crate::shell::{Layer, Palette};

pub(crate) use view::view;

const EXTENSIONS: [&str; 1] = ["md"];
const RECENTS: i64 = 50;
/// How long typing settles before the opener browses a typed folder, as in the Tauri app.
const BROWSE_DELAY: f64 = 0.15;
pub(crate) const FIELD: &str = "opener.field";
pub(crate) const NOTEBOOK_ROOT: &str = "notebook.root.effective";

/// The opener while it is open; a fresh one each time it opens.
#[derive(Default)]
pub struct Opener {
    pub(crate) query: TextEdit,
    root: Option<String>,
    session: Option<String>,
    recents: Vec<rank::Recent>,
    index: Vec<String>,
    truncated: bool,
    indexing: bool,
    browse: Browse,
    /// The highlighted row, by key, so it stays on its file as the list changes.
    highlighted: Option<String>,
    /// Counts openings, so an answer to an earlier opening is dropped.
    opening: u64,
}

#[derive(Default)]
struct Browse {
    /// When the typed folder is due to be listed.
    at: Option<f64>,
    entries: Vec<Entry>,
    listing: bool,
    failed: bool,
}

struct Entry {
    name: String,
    shown: String,
    path: String,
    folder: bool,
}

/// A row the opener lists: a file to open, or a folder to browse into.
pub(crate) struct Row {
    pub(crate) key: String,
    pub(crate) title: String,
    pub(crate) detail: String,
    target: String,
    folder: bool,
    host: Option<String>,
}

/// Opens the opener on the agent behind the screen, or the notebook when there is none.
pub(crate) fn open(state: &mut State, tx: &mut Tx) {
    let session = behind_screen(state);
    let folder = session
        .as_ref()
        .filter(|s| s.host.as_deref().is_none_or(str::is_empty))
        .map(|s| s.directory.clone())
        .filter(|d| !d.is_empty());
    let root = folder.or_else(|| {
        state
            .facts
            .settings
            .get(NOTEBOOK_ROOT)
            .filter(|r| !r.is_empty())
            .cloned()
    });
    let opening = state.opener.opening + 1;
    state.opener = Opener {
        root: root.clone(),
        session: session.map(|s| s.id.clone()),
        indexing: root.is_some(),
        opening,
        ..Opener::default()
    };
    state.window.layer = Some(Layer::Palette(Palette::Files));
    tx.focus(FIELD, false);
    let asked_root = root.clone();
    state.ask(
        |id| RecentFilesMessage {
            limit: Some(RECENTS),
            root: asked_root,
            request_id: Some(id),
            ..RecentFilesMessage::new()
        },
        move |state, answer, _| {
            let Some(result) = answer
                .parse::<RecentFilesResultMessage>()
                .filter(|r| r.success)
            else {
                return;
            };
            if state.opener.opening == opening {
                state.opener.recents = result
                    .files
                    .into_iter()
                    .map(|f| rank::Recent {
                        path: f.path,
                        at: f.last_at,
                        host: f.host.filter(|h| !h.is_empty()),
                    })
                    .collect();
            }
        },
        tx,
    );
    let Some(root) = root else {
        return;
    };
    state.ask(
        |id| FsIndexMessage {
            root: Some(root),
            extensions: Some(EXTENSIONS.map(str::to_owned).to_vec()),
            request_id: Some(id),
            ..FsIndexMessage::new()
        },
        move |state, answer, _| {
            if state.opener.opening != opening {
                return;
            }
            state.opener.indexing = false;
            if let Some(result) = answer.parse::<FsIndexResultMessage>().filter(|r| r.success) {
                state.opener.index = result.files;
                state.opener.truncated = result.truncated;
            }
        },
        tx,
    );
}

/// The session of the tile the screen shows as active: its agent, or the agent a document
/// tile was opened for.
fn behind_screen(state: &State) -> Option<&attn_protocol::Session> {
    if state.window.view != crate::shell::View::Desktop {
        return None;
    }
    let desktop = state.facts.current_desktop()?;
    let session = crate::desktop::session_of_leaf(desktop, &desktop.active_pane_id)?;
    state.facts.sessions.iter().find(|s| s.id == session)
}

pub(crate) fn close(state: &mut State, tx: &mut Tx) {
    if state.window.layer == Some(Layer::Palette(Palette::Files)) {
        state.window.layer = None;
        crate::picker::refocus_inline(state, tx);
    }
}

/// The rows the query lists now, with the empty list's message.
pub(crate) fn rows(state: &State) -> (Vec<Row>, &'static str) {
    let opener = &state.opener;
    let query = opener.query.text.as_str();
    if paths::is_path(query) {
        let rows = opener
            .browse
            .entries
            .iter()
            .map(|e| Row {
                key: e.path.clone(),
                title: if e.folder {
                    format!("{}/", e.name)
                } else {
                    e.name.clone()
                },
                detail: e.shown.clone(),
                target: if e.folder {
                    paths::descend(&e.shown)
                } else {
                    e.path.clone()
                },
                folder: e.folder,
                host: None,
            })
            .collect();
        let empty = if paths::browse_input(query, opener.root.as_deref()).is_none() {
            "No folder to resolve this path against."
        } else if opener.browse.listing || opener.browse.at.is_some() {
            "Listing…"
        } else if opener.browse.failed {
            "That folder could not be listed."
        } else {
            "Nothing here matches."
        };
        return (rows, empty);
    }
    let files = rank::merged(&opener.recents, opener.root.as_deref(), &opener.index);
    let rows = rank::ranked(&files, query)
        .into_iter()
        .map(|f| Row {
            key: match &f.host {
                Some(host) => format!("{host}:{}", f.path),
                None => f.path.clone(),
            },
            title: rank::basename(&f.label).to_owned(),
            detail: match &f.host {
                Some(host) => format!(
                    "{} on {}",
                    f.label,
                    crate::desktop::outpost_name(host, &state.facts.outposts)
                ),
                None => f.label.clone(),
            },
            target: f.path,
            folder: false,
            host: f.host,
        })
        .collect();
    let empty = if opener.indexing {
        "Loading files…"
    } else if query.trim().is_empty() {
        "No recently opened files. Type to search, or a path to browse."
    } else if opener.truncated {
        "No files match (the index is capped, so some files are missing). Type a path to browse."
    } else {
        "No files match. Type a path to browse."
    };
    (rows, empty)
}

/// The highlighted row: the one it was on while it is still listed, else the first.
pub(crate) fn highlighted(opener: &Opener, rows: &[Row]) -> Option<usize> {
    opener
        .highlighted
        .as_ref()
        .and_then(|key| rows.iter().position(|r| &r.key == key))
        .or((!rows.is_empty()).then_some(0))
}

pub(crate) fn edited(state: &mut State, tx: &mut Tx) {
    let browsing = paths::is_path(&state.opener.query.text);
    if !browsing {
        state.opener.browse = Browse::default();
    }
    state.opener.browse.at = browsing.then(|| tx.clock() + BROWSE_DELAY);
    if let Some(at) = state.opener.browse.at {
        tx.wake_at(at);
    }
}

/// Lists the typed folder once typing has settled. Returns whether it asked.
pub(crate) fn browse_due(state: &mut State, tx: &mut Tx) -> bool {
    if state
        .opener
        .browse
        .at
        .take_if(|at| tx.clock() >= *at)
        .is_none()
    {
        return false;
    }
    let Some(input) = paths::browse_input(&state.opener.query.text, state.opener.root.as_deref())
    else {
        return true;
    };
    let (opening, asked) = (state.opener.opening, input.clone());
    state.opener.browse.listing = true;
    state.ask(
        |id| BrowseDirectoryMessage {
            input_path: input,
            extensions: Some(EXTENSIONS.map(str::to_owned).to_vec()),
            request_id: Some(id),
            ..BrowseDirectoryMessage::new()
        },
        move |state, answer, _| {
            let opener = &mut state.opener;
            if opener.opening != opening
                || paths::browse_input(&opener.query.text, opener.root.as_deref()).as_ref()
                    != Some(&asked)
            {
                return;
            }
            let result = answer
                .parse::<BrowseDirectoryResultMessage>()
                .filter(|r| r.success);
            let home = result
                .as_ref()
                .and_then(|r| r.home_path.clone())
                .unwrap_or_default();
            opener.browse = Browse {
                failed: result.is_none(),
                entries: result
                    .map(|r| r.entries)
                    .unwrap_or_default()
                    .into_iter()
                    .map(|e| Entry {
                        shown: crate::picker::display_path(&e.path, &home),
                        name: e.name,
                        path: e.path,
                        folder: e.is_dir,
                    })
                    .collect(),
                ..Browse::default()
            };
        },
        tx,
    );
    true
}

pub(crate) fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let (rows, _) = rows(state);
    let at = highlighted(&state.opener, &rows);
    match ev.key {
        Key::Down | Key::Up if !rows.is_empty() => {
            let last = rows.len() - 1;
            let next = match (&ev.key, at) {
                (Key::Down, Some(i)) => (i + 1).min(last),
                (Key::Up, Some(i)) => i.saturating_sub(1),
                _ => 0,
            };
            state.opener.highlighted = Some(rows[next].key.clone());
            true
        }
        Key::Enter => {
            if let Some(row) = at.and_then(|i| rows.into_iter().nth(i)) {
                pick(state, row, tx);
            }
            true
        }
        Key::Escape => {
            close(state, tx);
            true
        }
        _ => false,
    }
}

pub(crate) fn pick_key(state: &mut State, key: &str, tx: &mut Tx) {
    let (rows, _) = rows(state);
    if let Some(row) = rows.into_iter().find(|r| r.key == key) {
        pick(state, row, tx);
    }
}

fn pick(state: &mut State, row: Row, tx: &mut Tx) {
    if row.folder {
        state.opener.query = TextEdit::new(row.target.clone());
        let end = row.target.len();
        state.opener.query.set_caret(end);
        state.opener.highlighted = None;
        edited(state, tx);
        return;
    }
    close(state, tx);
    let session = state.opener.session.clone();
    open_markdown(state, row.target, row.host, session, tx);
}

/// Shows the markdown file at `path`, on outpost `host`, in a tile opened for `session`; the
/// system's opener opens a local file the daemon could not.
pub(crate) fn open_markdown(
    state: &mut State,
    path: String,
    host: Option<String>,
    session: Option<String>,
    tx: &mut Tx,
) {
    let asked = (path.clone(), host.clone());
    state.ask(
        |id| OpenMarkdownMessage {
            path: asked.0,
            host: asked.1,
            session_id: session,
            request_id: Some(id),
            ..OpenMarkdownMessage::new()
        },
        move |state, answer, tx| {
            let opened = answer
                .outcome()
                .map(|reply| reply.parse::<OpenMarkdownResultMessage>());
            match (opened, host) {
                (Ok(Some(result)), _) => {
                    if let (Some(desktop), Some(tile)) = (result.desktop_id, result.tile_id) {
                        crate::dispatch(state, crate::Action::OpenTile { desktop, tile }, tx);
                    }
                }
                (Ok(None), _) => {}
                (Err(why), Some(_)) => state
                    .window
                    .notify(format!("Could not open {path}: {why}"), tx),
                (Err(_), None) => {
                    if let Err(why) = tx.open_uri(&attn_term::links::file_url(&path)) {
                        state
                            .window
                            .notify(format!("Could not open {path}: {why}"), tx);
                    }
                }
            }
        },
        tx,
    );
}
