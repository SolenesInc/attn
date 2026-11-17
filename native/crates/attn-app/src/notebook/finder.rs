//! The note finder: ⌘P inside the notebook or an editor tile finds a note of that surface's
//! folder by name, as the Tauri notebook's finder does (NotebookFinder.tsx).

use attn_protocol::{FsIndexMessage, FsIndexResultMessage};
use attn_ui::Tokens;
use attn_ui::finder;
use rune::{El, Key, KeyEvent, TextEdit, Tx, col};
use rune_kit::TextInput;

use super::io;
use super::model::SurfaceId;
use crate::State;

const LABEL: &str = "Find a note";
const FIELD: &str = "notebook.finder.field";
const LIMIT: usize = 50;

pub(crate) struct Finder {
    pub(crate) surface: SurfaceId,
    pub(crate) query: TextEdit,
    /// The folder's files, root-relative; `None` until the daemon indexed it.
    pub(crate) files: Option<Vec<String>>,
    pub(crate) highlighted: usize,
}

/// Opens the finder for the surface, indexing its folder.
pub(crate) fn open(state: &mut State, id: SurfaceId, tx: &mut Tx) {
    let root = state.notebook.surface(&id).and_then(|s| io::root(state, s));
    state.notebook.revision += 1;
    state.notebook.finder = Some(Finder {
        surface: id.clone(),
        query: TextEdit::new(""),
        files: None,
        highlighted: 0,
    });
    tx.focus(FIELD, false);
    state.ask(
        move |request_id| FsIndexMessage {
            root,
            request_id: Some(request_id),
            ..FsIndexMessage::new()
        },
        move |state, answer, _| {
            let files = answer
                .parse::<FsIndexResultMessage>()
                .filter(|r| r.success)
                .map(|r| r.files)
                .unwrap_or_default();
            if let Some(finder) = state.notebook.finder.as_mut().filter(|f| f.surface == id) {
                finder.files = Some(files);
                state.notebook.revision += 1;
            }
        },
        tx,
    );
}

pub(crate) fn close(state: &mut State, _: &mut Tx) {
    state.notebook.finder = None;
    state.notebook.revision += 1;
}

/// The files the query finds, best first: a fuzzy match on the path, the name counting more.
fn ranked(files: &[String], query: &str) -> Vec<String> {
    if query.trim().is_empty() {
        return files.iter().take(LIMIT).cloned().collect();
    }
    let mut scored: Vec<(f64, &String)> = files
        .iter()
        .map(|f| (crate::opener::rank::score(f, query), f))
        .filter(|(score, _)| *score > 0.0)
        .collect();
    scored.sort_by(|(a, left), (b, right)| b.total_cmp(a).then_with(|| left.cmp(right)));
    scored
        .into_iter()
        .take(LIMIT)
        .map(|(_, f)| f.clone())
        .collect()
}

fn rows(finder: &Finder) -> Vec<String> {
    ranked(
        finder.files.as_deref().unwrap_or_default(),
        &finder.query.text,
    )
}

fn pick(state: &mut State, path: String, tx: &mut Tx) {
    let Some(finder) = state.notebook.finder.take() else {
        return;
    };
    state.notebook.revision += 1;
    io::open(state, &finder.surface, path, tx);
}

fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let Some(finder) = state.notebook.finder.as_mut() else {
        return false;
    };
    let rows = rows(finder);
    match ev.key {
        Key::Down if !rows.is_empty() => {
            finder.highlighted = (finder.highlighted + 1).min(rows.len() - 1)
        }
        Key::Up => finder.highlighted = finder.highlighted.saturating_sub(1),
        Key::Enter => {
            if let Some(path) = rows.get(finder.highlighted).cloned() {
                pick(state, path, tx);
            }
        }
        Key::Escape => close(state, tx),
        _ => return false,
    }
    state.notebook.revision += 1;
    true
}

/// The finder over surface `id`, while it is open there, as the Tauri finder sits over the
/// notebook or the tile it finds in.
pub(crate) fn view(notebook: &super::Notebook, id: &SurfaceId, t: &Tokens) -> Option<El> {
    let finder = notebook.finder.as_ref().filter(|f| f.surface == *id)?;
    let rows = rows(finder);
    let field = TextInput::new(&finder.query)
        .key(FIELD)
        .plain()
        .text_style(finder::FIELD_TEXT)
        .height(finder::FIELD_TEXT.line_px())
        .width(None)
        .label(LABEL)
        .placeholder("Find a note…")
        .on_edit(|state: &mut State| {
            &mut state
                .notebook
                .finder
                .as_mut()
                .expect("the finder shows")
                .query
        })
        .on_change(|state: &mut State, _: &mut Tx| {
            if let Some(finder) = state.notebook.finder.as_mut() {
                finder.highlighted = 0;
            }
            state.notebook.revision += 1;
        })
        .on_key(on_key)
        .build(attn_ui::kit(t));
    let empty = match finder.files {
        None => "Loading notes…",
        Some(_) => "No notes match.",
    };
    let list = col()
        .key("notebook.finder.list")
        .role("listbox")
        .label(LABEL)
        .max_h(finder::LIST_MAX)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .gap(2.0)
        .children(
            rows.iter()
                .enumerate()
                .map(|(i, path)| option(path, i == finder.highlighted, t)),
        )
        .children(rows.is_empty().then(|| finder::empty(t, empty)));
    let panel = finder::panel(t, field, list)
        .key("notebook.finder")
        .role("dialog")
        .label(LABEL)
        .on_press_outside(close);
    Some(finder::layer(t, "notebook.finder.layer", panel))
}

fn option(path: &str, highlighted: bool, t: &Tokens) -> El {
    let name = path.rsplit('/').next().unwrap_or(path).to_owned();
    let picked = path.to_owned();
    finder::option(t, name.clone(), path.to_owned(), highlighted)
        .key(format!("notebook.finder.row.{path}"))
        .role("option")
        .label(format!("{name} {path}"))
        .focus_selected(highlighted)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| pick(state, picked.clone(), tx))
}

/// The surface ⌘P finds a note in: the fullscreen notebook while it shows, else the editor
/// tile the window acts on.
pub(crate) fn target(state: &State) -> Option<SurfaceId> {
    if state.window.layer == Some(crate::shell::Layer::Notebook) {
        return Some(SurfaceId::Fullscreen);
    }
    let (_, leaf) = state.window.acting(&state.facts)?;
    state
        .notebook
        .tiles
        .contains_key(&leaf)
        .then_some(SurfaceId::Tile(leaf))
}
