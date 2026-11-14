//! The note finder: ⌘P inside the notebook or an editor tile finds a note of that surface's
//! folder by name, as the Tauri notebook's finder does (NotebookFinder.tsx).

use attn_protocol::{FsIndexMessage, FsIndexResultMessage};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, TextEdit, Tx, col, el, row, text};
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
        .text_style(type_::BODY)
        .height(type_::BODY.line_px())
        .width(None)
        .label(LABEL)
        .placeholder("Find a note…")
        .on_edit(|state: &mut State| match state.notebook.finder.as_mut() {
            Some(finder) => &mut finder.query,
            None => &mut state.notebook.scratch,
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
        .w_full()
        .max_h(520.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .gap(2.0)
        .children(
            rows.iter()
                .enumerate()
                .map(|(i, path)| option(path, i == finder.highlighted, t)),
        )
        .children(
            rows.is_empty()
                .then(|| text(empty).font(type_::BODY).color(t.text_tertiary)),
        );
    let panel = col()
        .key("notebook.finder")
        .role("dialog")
        .label(LABEL)
        .on_press_outside(close)
        .w_full()
        .max_w(640.0)
        .radius(8.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .padding(space::S3)
        .gap(space::S2)
        .child(field)
        .child(list);
    Some(
        el().key("notebook.finder.layer")
            .pin(Some(0.0), Some(0.0), Some(0.0), Some(0.0))
            .w_full()
            .h_full()
            .child(
                el().absolute(0.0, 0.0)
                    .w_full()
                    .h_full()
                    .fill(t.bg_overlay)
                    .a11y_hidden(),
            )
            .child(
                col()
                    .w_full()
                    .h_full()
                    .px(16.0)
                    .pt(80.0)
                    .items_center()
                    .child(panel),
            ),
    )
}

fn option(path: &str, highlighted: bool, t: &Tokens) -> El {
    let name = path.rsplit('/').next().unwrap_or(path).to_owned();
    let picked = path.to_owned();
    row()
        .key(format!("notebook.finder.row.{path}"))
        .role("option")
        .label(format!("{name} {path}"))
        .focus_selected(highlighted)
        .w_full()
        .items_center()
        .gap(space::S2)
        .px(space::S2)
        .py(space::S1)
        .radius(4.0)
        .fill(if highlighted {
            t.bg_selected
        } else {
            t.bg_elevated
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| pick(state, picked.clone(), tx))
        .child(
            text(name)
                .font(type_::BODY)
                .color(t.text_primary)
                .single_line(),
        )
        .child(
            text(path.to_owned())
                .font(type_::MONO_CAPTION)
                .color(t.text_tertiary)
                .single_line()
                .truncate()
                .shrink(),
        )
}

/// The surface ⌘P finds a note in: the fullscreen notebook while it shows, else the editor
/// tile that is the active leaf.
pub(crate) fn target(state: &State) -> Option<SurfaceId> {
    if state.window.layer == Some(crate::shell::Layer::Notebook) {
        return Some(SurfaceId::Fullscreen);
    }
    let desktop = state.facts.current_desktop()?;
    state
        .notebook
        .tiles
        .contains_key(&desktop.active_pane_id)
        .then(|| SurfaceId::Tile(desktop.active_pane_id.clone()))
}
