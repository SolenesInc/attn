//! How a notebook surface looks: the file tree, the open document with its save status, and a
//! rail with the note's outline and the notes that link to it.

use attn_ui::Tokens;
use attn_ui::keycap;
use attn_ui::type_;
use rune::tok::space;
use rune::{Color, El, ScrollSpec, TextStyle, Tx, col, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize};

use super::file_kind::FileKind;
use super::io;
use super::model::{Backlinks, Doc, Draft, Listing, Open, Surface, SurfaceId};
use super::outline::outline;
use crate::State;

const TREE_WIDTH: f32 = 280.0;
/// A tile narrower than these folds its file tree and its rail, as the Tauri tile does.
const TREE_FOLDS_BELOW: f32 = 620.0;
const RAIL_FOLDS_BELOW: f32 = 900.0;
const RAIL_WIDTH: f32 = 280.0;
const TREE_INDENT: f32 = 14.0;
const TREE_ROW: f32 = 30.0;
const NOTE: TextStyle = TextStyle::new(14.0, 400.0).lh(23.0);
const TITLE: TextStyle = TextStyle::new(15.0, 650.0).tracking(-0.225);
const NOTE_TITLE: TextStyle = TextStyle::new(24.0, 680.0).tracking(-0.84);
const STATE_TITLE: TextStyle = TextStyle::new(15.0, 600.0);
const INSET: f32 = 24.0;
const SHELL_INSET: f32 = 12.0;
const RADIUS: f32 = 14.0;
const FOLD: [f32; 2] = [16.0, 46.0];

/// How far a note and its header sit in from the note pane's sides, as the Tauri app's
/// `clamp(28px, 6vw, 90px)`.
fn page_inset(window_w: f32) -> f32 {
    (window_w * 0.06).clamp(28.0, 90.0)
}

/// The file tree's and the rail's ground: the app's background at 58% over what is behind.
fn pane_fill(t: &Tokens) -> Color {
    t.bg_app.alpha(0.58)
}

pub(super) fn key(id: &SurfaceId, part: &str) -> String {
    match id {
        SurfaceId::Fullscreen => format!("notebook.{part}"),
        SurfaceId::Tile(tile) => format!("notebook.tile.{tile}.{part}"),
    }
}

/// The fullscreen notebook, over the whole window.
pub(crate) fn fullscreen(state: &State, t: &Tokens) -> El {
    let id = SurfaceId::Fullscreen;
    let body = match state.notebook.fullscreen.as_ref() {
        Some(surface) => {
            let find = state
                .keymap
                .binding(&crate::Action::FileOpen)
                .map(|b| state.keymap.label(b));
            surface_view(&state.notebook, &id, surface, find, t)
        }
        None => col(),
    };
    let [w, h] = state.notebook.panel;
    let glow = 0.32 * (0.8 * w).hypot(h);
    let mut panel = col()
        .key("notebook.panel")
        .role("dialog")
        .labelled_by("notebook.title")
        .modal_tx(true, |state: &mut State, tx: &mut Tx| {
            super::close_fullscreen(state, tx)
        })
        .on_paste_content(crate::keys::on_paste)
        .on_layout(|state: &mut State, r: [f32; 4]| {
            if state.notebook.panel != [r[2], r[3]] {
                state.notebook.panel = [r[2], r[3]];
                state.notebook.revision += 1;
            }
        })
        .w_full()
        .h_full()
        .radius(RADIUS)
        .clip_rounded(RADIUS)
        .fill(t.bg_panel)
        .border(1.0, t.border)
        .box_shadow(attn_ui::frame::shadow(t, 24.0, 80.0, 0.42));
    if glow > 0.0 {
        panel = panel.radial_gradient(
            [0.8, 0.0],
            glow,
            [(0.0, t.brand.alpha(0.08)), (1.0, t.brand.alpha(0.0))],
        );
    }
    let panel = panel
        .child(header(state, t))
        .child(body.grow(1.0).min_h(0.0));
    crate::shell::under_title_bar("notebook", SHELL_INSET, panel, t)
}

fn header(state: &State, t: &Tokens) -> El {
    let pulse = chief(state).map(|active| {
        text(if active {
            "chief: active"
        } else {
            "chief: idle"
        })
        .key("notebook.chief")
        .role("status")
        .font(type_::LABEL)
        .color(if active { t.accent } else { t.text_tertiary })
    });
    row()
        .w_full()
        .min_h(72.0)
        .items_center()
        .gap(space::S6)
        .px(18.0)
        .py(space::S3)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .child(
            row()
                .grow(1.0)
                .items_center()
                .gap(11.0)
                .child(rune::icon(&attn_ui::icon::NOTEBOOK, 25.0).color(t.brand))
                .child(
                    col()
                        .gap(2.0)
                        .child(type_::upper("Knowledge base", type_::BOLD_CAPS).color(t.brand))
                        .child(
                            text("Notebook")
                                .key("notebook.title")
                                .role("heading")
                                .level(1)
                                .font(TITLE)
                                .color(t.text_primary),
                        ),
                ),
        )
        .children(pulse)
        .child(
            Button::new("Close")
                .variant(ButtonVariant::GhostMuted)
                .control_size(ControlSize::Small)
                .label_with(word_and_key("Close", Some("esc".into()), *t))
                .build(attn_ui::kit(t))
                .key("notebook.close")
                .on_press_tx(|state: &mut State, tx: &mut Tx| super::close_fullscreen(state, tx)),
        )
}

/// Whether the selected profile's chief is working; `None` when the profile has none.
fn chief(state: &State) -> Option<bool> {
    let profile = &state.facts.profiles.selected;
    state
        .facts
        .sessions
        .iter()
        .find(|s| s.chief_of_staff == Some(true) && s.profile_id == *profile)
        .map(|s| s.state == "working")
}

/// A notebook tile's body; `find` is the key that opens the finder, which its empty page names.
pub(crate) fn tile_body(
    notebook: &super::Notebook,
    tile: &str,
    find: Option<String>,
    t: &Tokens,
) -> El {
    let id = SurfaceId::Tile(tile.to_owned());
    match notebook.tiles.get(tile) {
        Some(surface) => surface_view(notebook, &id, surface, find, t),
        None => col()
            .w_full()
            .h_full()
            .fill(t.bg_panel)
            .child(message("Loading…", t)),
    }
}

fn surface_view(
    notebook: &super::Notebook,
    id: &SurfaceId,
    surface: &Surface,
    find: Option<String>,
    t: &Tokens,
) -> El {
    let (tree_fits, rail_fits) = fits(id, surface.width);
    let tree_open = surface.folds.tree.unwrap_or(tree_fits);
    let rail = rail(id, surface, t);
    let rail_open = surface.folds.rail.unwrap_or(rail_fits);
    let sized = id.clone();
    let inset = page_inset(notebook.window_w);
    let folds = (
        fold_button(id, Pane::Tree, tree_open, t),
        rail.is_some()
            .then(|| fold_button(id, Pane::Rail, rail_open, t)),
    );
    let surface_el = row().key(key(id, "surface"));
    let surface_el = match id {
        SurfaceId::Tile(_) => surface_el.fill(t.bg_panel),
        SurfaceId::Fullscreen => surface_el,
    };
    surface_el
        .on_layout(move |state: &mut State, r: [f32; 4]| {
            let moved = state
                .notebook
                .surface(&sized)
                .is_some_and(|s| (s.width - r[2]).abs() > 0.5);
            if moved && let Some(surface) = state.notebook.surface_mut(&sized) {
                surface.width = r[2];
            }
        })
        .w_full()
        .h_full()
        .min_h(0.0)
        .children(tree_open.then(|| tree(id, surface, t)))
        .child(document(id, surface, find, inset, folds, t))
        .children(rail.filter(|_| rail_open))
        .children(super::finder::view(notebook, id, t))
}

/// Whether a tile this wide has room for its file tree and its rail; the fullscreen notebook
/// and a tile not laid out yet always have.
fn fits(id: &SurfaceId, width: f32) -> (bool, bool) {
    let roomy = matches!(id, SurfaceId::Fullscreen) || width <= 0.0;
    (
        roomy || width >= TREE_FOLDS_BELOW,
        roomy || width >= RAIL_FOLDS_BELOW,
    )
}

#[derive(Clone, Copy)]
enum Pane {
    Tree,
    Rail,
}

fn fold_button(id: &SurfaceId, pane: Pane, open: bool, t: &Tokens) -> El {
    let (label, glyph) = match (pane, open) {
        (Pane::Tree, true) => ("Hide file tree", "‹"),
        (Pane::Tree, false) => ("Show file tree", "›"),
        (Pane::Rail, true) => ("Hide context rail", "›"),
        (Pane::Rail, false) => ("Show context rail", "‹"),
    };
    let id = id.clone();
    let pill = col()
        .key(key(
            &id,
            match pane {
                Pane::Tree => "fold.tree",
                Pane::Rail => "fold.rail",
            },
        ))
        .role("button")
        .label(label)
        .help(label)
        .focusable(true)
        .expanded(open)
        .size(FOLD[0], FOLD[1])
        .center()
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .radius(7.0)
        .group_opacity(0.5, 1.0)
        .child(text(glyph).font(type_::LABEL).color(t.text_muted))
        .on_press(move |state: &mut State| {
            if let Some(surface) = state.notebook.surface_mut(&id) {
                let fold = match pane {
                    Pane::Tree => &mut surface.folds.tree,
                    Pane::Rail => &mut surface.folds.rail,
                };
                *fold = Some(!open);
            }
        });
    let (left, right) = match pane {
        Pane::Tree => (Some(4.0), None),
        Pane::Rail => (None, Some(4.0)),
    };
    col()
        .pin(left, Some(0.0), right, Some(0.0))
        .w(FOLD[0])
        .justify_center()
        .hover_group()
        .child(pill)
}

fn tree(id: &SurfaceId, surface: &Surface, t: &Tokens) -> El {
    let mut rows = Vec::new();
    folder_rows(id, surface, "", 0, &mut rows, t);
    col()
        .key(key(id, "files"))
        .role("navigation")
        .label("Notebook files")
        .w(TREE_WIDTH)
        .h_full()
        .min_h(0.0)
        .px(10.0)
        .py(14.0)
        .fill(pane_fill(t))
        .border_sides([0.0, 1.0, 0.0, 0.0], t.border_subtle)
        .scroll(ScrollSpec {
            stick_bottom: false,
        })
        .children(rows)
}

fn folder_rows(
    id: &SurfaceId,
    surface: &Surface,
    folder: &str,
    depth: usize,
    rows: &mut Vec<El>,
    t: &Tokens,
) {
    match surface.tree.folders.get(folder) {
        None | Some(Listing::Loading) if depth == 0 => rows.push(message("Loading…", t)),
        None | Some(Listing::Loading) => {}
        Some(Listing::Failed(error)) => rows.push(
            text(error.clone())
                .font(type_::BODY)
                .color(t.error)
                .wrap()
                .px(space::S3),
        ),
        Some(Listing::Listed(entries)) => {
            for entry in entries {
                let expanded = surface.tree.expanded.contains(&entry.path);
                let open = surface.path() == Some(entry.path.as_str());
                rows.push(tree_row(id, entry, depth, expanded, open, t));
                if entry.is_dir && expanded {
                    folder_rows(id, surface, &entry.path, depth + 1, rows, t);
                }
            }
        }
    }
}

fn tree_row(
    id: &SurfaceId,
    entry: &attn_protocol::FsEntry,
    depth: usize,
    expanded: bool,
    open: bool,
    t: &Tokens,
) -> El {
    let (id, path, folder) = (id.clone(), entry.path.clone(), entry.is_dir);
    let chevron = folder.then(|| {
        rune::icon(
            if expanded {
                &attn_ui::icon::DISCLOSE_OPEN
            } else {
                &attn_ui::icon::DISCLOSE_CLOSED
            },
            12.0,
        )
        .color(t.text_secondary)
    });
    let mut row_el = row()
        .key(format!("{}.{}", key(&id, "file"), entry.path))
        .role(if folder { "treeitem" } else { "link" })
        .label(entry.name.clone())
        .w_full()
        .h(TREE_ROW)
        .pl(8.0 + depth as f32 * TREE_INDENT)
        .pr(10.0)
        .items_center()
        .gap(6.0)
        .radius(6.0)
        .border(1.0, Color::TRANSPARENT)
        .hover(t.bg_elevated.alpha(0.6))
        .children(chevron)
        .child(
            text(entry.name.clone())
                .font(TextStyle {
                    weight: if folder { 550.0 } else { 400.0 },
                    ..type_::BODY
                })
                .color(if open { t.brand } else { t.text_primary })
                .truncate(),
        )
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            if folder {
                toggle_folder(state, &id, &path, tx);
            } else {
                io::open(state, &id, path.clone(), tx);
            }
        });
    if folder {
        row_el = row_el.expanded(expanded);
    }
    if open {
        row_el = row_el
            .fill(t.bg_elevated)
            .border(1.0, t.border)
            .a11y_value("open");
    }
    row_el
}

fn toggle_folder(state: &mut State, id: &SurfaceId, path: &str, tx: &mut Tx) {
    let Some(surface) = state.notebook.surface_mut(id) else {
        return;
    };
    if !surface.tree.expanded.remove(path) {
        surface.tree.expanded.insert(path.to_owned());
        if !surface.tree.folders.contains_key(path) {
            io::list(state, id, path, tx);
        }
    }
}

fn document(
    id: &SurfaceId,
    surface: &Surface,
    find: Option<String>,
    inset: f32,
    (tree_fold, rail_fold): (El, Option<El>),
    t: &Tokens,
) -> El {
    let body = match &surface.doc {
        Doc::Empty => empty(id, find, t),
        Doc::Loading { shown: None, .. } => message("Loading…", t),
        Doc::Loading {
            shown: Some(open), ..
        } => open_view(id, open, inset, t),
        Doc::Failed { error, .. } => notice("File unavailable", error, None, t),
        Doc::Binary { path } => notice(
            "Preview not available",
            &format!("{} can't be opened here yet.", basename(path)),
            Some(path),
            t,
        ),
        Doc::Open(open) => open_view(id, open, inset, t),
    };
    col()
        .key(key(id, "document"))
        .role("main")
        .grow(1.0)
        .min_w(0.0)
        .h_full()
        .min_h(0.0)
        .child(body)
        .child(tree_fold)
        .children(rail_fold)
}

/// A document pane with nothing to edit, as the Tauri app's `.notebook-browser-document-state`:
/// the notebook's mark, a title and what to do, centered in the pane.
fn state_view(title: &str, t: &Tokens) -> El {
    col()
        .w_full()
        .h_full()
        .min_h(0.0)
        .center()
        .child(
            rune::icon(&attn_ui::icon::NOTEBOOK, 34.0)
                .color(t.text_dimmed)
                .mb(18.0),
        )
        .child(
            text(title.to_owned())
                .role("heading")
                .level(2)
                .font(STATE_TITLE)
                .color(t.text_secondary)
                .mb(7.0),
        )
}

fn empty(id: &SurfaceId, find: Option<String>, t: &Tokens) -> El {
    let id = id.clone();
    state_view("Nothing selected", t)
        .child(
            text("Find a note, or pick one from the tree.")
                .font(type_::LABEL)
                .color(t.text_muted),
        )
        .child(
            Button::new("Find a note")
                .variant(ButtonVariant::GhostMuted)
                .control_size(ControlSize::Small)
                .label_with(word_and_key("Find a note", find, *t))
                .build(attn_ui::kit(t))
                .key(key(&id, "find"))
                .mt(space::S3)
                .fill(t.bg_elevated)
                .border(1.0, t.border)
                .radius(7.0)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::finder::open(state, id.clone(), tx)
                }),
        )
}

fn notice(title: &str, detail: &str, path: Option<&str>, t: &Tokens) -> El {
    state_view(title, t)
        .child(
            text(detail.to_owned())
                .font(type_::LABEL)
                .color(t.text_muted)
                .wrap()
                .max_w(360.0),
        )
        .children(path.map(|p| {
            text(p.to_owned())
                .font(type_::MONO_CAPTION)
                .color(t.text_dimmed)
                .mt(space::S1)
        }))
}

fn message(words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .font(type_::BODY)
        .color(t.text_tertiary)
        .padding(INSET)
}

fn open_view(id: &SurfaceId, open: &Open, inset: f32, t: &Tokens) -> El {
    let title = open.path.rsplit('/').next().unwrap_or(&open.path);
    let title = title.strip_suffix(".md").unwrap_or(title).to_owned();
    let (badge, journal) = match open.kind {
        FileKind::Markdown => {
            let kind = note_type(&open.draft.text()).unwrap_or_else(|| "note".into());
            let journal = kind == "journal";
            (kind, journal)
        }
        _ => ("text".to_owned(), false),
    };
    let status = status(open);
    let editor = match &open.draft {
        Draft::Note(editor) => super::editor::view(
            editor,
            &key(id, "editor"),
            "Note",
            (super::editor_of, super::editor_of_mut),
            t,
        ),
        Draft::Field(field) => {
            let (field_id, changed_id) = (id.clone(), id.clone());
            text_input(field, NOTE)
                .key(key(id, "editor"))
                .label("File contents")
                .return_breaks_line()
                .w_full()
                .on_edit(move |state: &mut State| draft_of(state, &field_id))
                .on_input_change(move |state: &mut State, tx: &mut Tx| {
                    super::edited(state, &changed_id, tx)
                })
        }
    };
    col()
        .w_full()
        .h_full()
        .min_h(0.0)
        .child(
            col()
                .gap(space::S1)
                .px(inset)
                .pt(28.0)
                .pb(18.0)
                .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
                .child(
                    row()
                        .items_center()
                        .gap(10.0)
                        .child(
                            text(title)
                                .key(key(id, "document.title"))
                                .role("heading")
                                .level(2)
                                .font(NOTE_TITLE)
                                .color(t.text_primary),
                        )
                        .child(
                            kind_badge(&badge, if journal { t.accent } else { t.brand }, t)
                                .key(key(id, "document.kind")),
                        ),
                )
                .child(
                    text(open.path.clone())
                        .font(type_::MONO_CAPTION)
                        .color(t.text_tertiary),
                )
                .children(status.map(|s| {
                    text(s)
                        .key(key(id, "status"))
                        .role("status")
                        .font(type_::LABEL)
                        .color(t.text_tertiary)
                }))
                .children(save_error(open, t))
                .children(conflict(id, open, t)),
        )
        .children(match &open.draft {
            Draft::Note(editor) => {
                super::editor::find::bar(editor, &key(id, "editor"), super::editor_of_mut, t)
            }
            Draft::Field(_) => None,
        })
        .child(
            col()
                .key(key(id, "scroll"))
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .px(inset)
                .pt(18.0)
                .pb(INSET)
                .scroll(ScrollSpec {
                    stick_bottom: false,
                })
                .child(editor),
        )
}

/// What the save status line says, if anything.
fn status(open: &Open) -> Option<&'static str> {
    if open.save.error.is_some() {
        None
    } else if open.save.writing.is_some() {
        Some("Saving…")
    } else if open.dirty() {
        Some("Unsaved…")
    } else if open.save.saved_at.is_some() {
        Some("Saved")
    } else {
        None
    }
}

fn save_error(open: &Open, t: &Tokens) -> Option<El> {
    let error = open.save.error.as_ref()?;
    Some(
        text(error.clone())
            .key("notebook.save-error")
            .role("alert")
            .font(type_::BODY)
            .color(t.error)
            .wrap(),
    )
}

fn conflict(id: &SurfaceId, open: &Open, t: &Tokens) -> Option<El> {
    let current = open.save.conflict.as_ref()?;
    let words = if current.is_some() {
        "This file changed on disk since you opened it."
    } else {
        "This file was deleted on disk since you opened it."
    };
    let saving = open.save.writing.is_some();
    let (reload_id, over_id) = (id.clone(), id.clone());
    Some(
        col()
            .key(key(id, "document.conflict"))
            .role("alert")
            .gap(space::S2)
            .padding(space::S3)
            .radius(6.0)
            .border(1.0, t.warning)
            .child(text(words).font(type_::BODY).color(t.text_primary).wrap())
            .child(
                row()
                    .gap(space::S2)
                    .child(
                        Button::new("Reload from disk")
                            .control_size(ControlSize::Small)
                            .disabled(saving)
                            .build(attn_ui::kit(t))
                            .key(key(id, "document.reload"))
                            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                                super::reload(state, &reload_id, tx)
                            }),
                    )
                    .child(
                        Button::new("Overwrite anyway")
                            .control_size(ControlSize::Small)
                            .disabled(saving)
                            .build(attn_ui::kit(t))
                            .key(key(id, "document.overwrite"))
                            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                                super::overwrite(state, &over_id, tx)
                            }),
                    ),
            ),
    )
}

/// The `type` in a note's front matter.
fn note_type(text: &str) -> Option<String> {
    let rest = text.strip_prefix("---")?;
    let rest = rest
        .strip_prefix('\n')
        .or_else(|| rest.strip_prefix("\r\n"))?;
    for line in rest.lines() {
        let trimmed = line.trim();
        if trimmed == "---" || trimmed == "..." {
            return None;
        }
        if let Some(value) = line.strip_prefix("type:") {
            let value = value
                .trim()
                .trim_matches(|c| c == '"' || c == '\'')
                .to_owned();
            return (!value.is_empty()).then_some(value);
        }
    }
    None
}

fn draft_of<'a>(state: &'a mut State, id: &SurfaceId) -> &'a mut rune::TextEdit {
    let notebook = &mut state.notebook;
    let open = match id {
        SurfaceId::Fullscreen => notebook.fullscreen.as_mut(),
        SurfaceId::Tile(tile) => notebook.tiles.get_mut(tile),
    }
    .and_then(|s| match &mut s.doc {
        Doc::Open(open) => match &mut open.draft {
            Draft::Field(field) => Some(field),
            Draft::Note(_) => None,
        },
        _ => None,
    });
    open.expect("the draft field shows only while its file is open as one")
}

fn rail(id: &SurfaceId, surface: &Surface, t: &Tokens) -> Option<El> {
    let open = surface.open().filter(|o| o.kind == FileKind::Markdown)?;
    if !surface.on_notebook() {
        return None;
    }
    let note = open.draft.text();
    let headings = outline(&note);
    let folds = surface.folds;
    let outline_count = headings.len();
    let outline_items = headings.into_iter().enumerate().map(|(i, h)| {
        let (indent, weight, ink) = match h.level {
            1 => (8.0, 620.0, t.text_primary),
            2 => (18.0, 400.0, t.text_secondary),
            3 => (30.0, 400.0, t.text_secondary),
            _ => (42.0, 400.0, t.text_muted),
        };
        text(h.text.clone())
            .key(key(id, &format!("outline.{i}")))
            .font(TextStyle {
                weight,
                ..type_::BODY
            })
            .color(ink)
            .truncate()
            .w_full()
            .pl(indent)
            .pr(8.0)
            .py(4.0)
            .radius(6.0)
            .hover(t.bg_elevated.alpha(0.6))
            .role("button")
            .label(h.text)
            .focusable(true)
            .on_press_tx({
                let id = id.clone();
                move |state: &mut State, tx: &mut Tx| super::reveal_heading(state, &id, i, tx)
            })
    });
    let backlinks: Vec<El> = match &surface.backlinks {
        Backlinks::Listed(entries) => entries
            .iter()
            .map(|e| {
                let name = e
                    .title
                    .clone()
                    .filter(|t| !t.is_empty())
                    .unwrap_or_else(|| basename(&e.path).to_owned());
                let (id, path) = (id.clone(), e.path.clone());
                col()
                    .key(key(&id, &format!("backlink.{path}")))
                    .role("button")
                    .label(name.clone())
                    .w_full()
                    .gap(2.0)
                    .px(10.0)
                    .py(7.0)
                    .fill(t.bg_elevated)
                    .border(1.0, t.border_subtle)
                    .radius(7.0)
                    .child(
                        text(name)
                            .font(type_::LABEL)
                            .color(t.text_secondary)
                            .truncate(),
                    )
                    .child(
                        text(e.path.clone())
                            .font(type_::MONO_CAPTION)
                            .color(t.brand)
                            .truncate(),
                    )
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        io::open(state, &id, path.clone(), tx)
                    })
            })
            .collect(),
        _ => Vec::new(),
    };
    let backlinks_count = backlinks.len();
    let backlinks_empty = match &surface.backlinks {
        Backlinks::Loading => Some("Finding backlinks…"),
        Backlinks::Listed(e) if e.is_empty() => Some("No other note links here."),
        _ => None,
    };
    let outline_body = (!folds.outline_closed).then(|| {
        if outline_count == 0 {
            rail_empty("No headings.", t)
        } else {
            col()
                .key(key(id, "outline.headings"))
                .role("list")
                .label("Headings")
                .focus_group()
                .px(2.0)
                .children(outline_items)
        }
    });
    let backlinks_body = (!folds.backlinks_closed).then(|| match backlinks_empty {
        Some(words) => rail_empty(words, t),
        None => col().px(2.0).gap(6.0).children(backlinks),
    });
    Some(
        col()
            .key(key(id, "context"))
            .role("complementary")
            .label("Context")
            .w(RAIL_WIDTH)
            .h_full()
            .min_h(0.0)
            .px(space::S3)
            .py(16.0)
            .fill(pane_fill(t))
            .border_sides([0.0, 0.0, 0.0, 1.0], t.border_subtle)
            .scroll(ScrollSpec {
                stick_bottom: false,
            })
            .child(
                col()
                    .w_full()
                    .child(section(
                        id,
                        Section::Outline,
                        !folds.outline_closed,
                        outline_count,
                        t,
                    ))
                    .children(outline_body),
            )
            .child(
                col()
                    .w_full()
                    .mt(16.0)
                    .pt(space::S3)
                    .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)
                    .child(section(
                        id,
                        Section::Backlinks,
                        !folds.backlinks_closed,
                        backlinks_count,
                        t,
                    ))
                    .children(backlinks_body),
            ),
    )
}

fn rail_empty(words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .font(TextStyle {
            weight: 400.0,
            ..type_::LABEL
        })
        .color(t.text_dimmed)
        .px(6.0)
        .pt(2.0)
        .pb(4.0)
}

#[derive(Clone, Copy)]
enum Section {
    Outline,
    Backlinks,
}

/// A rail section's toggle, as the Tauri app's `.notebook-browser-rail-toggle`: a caret, the
/// title, and while open a pill with how many the section lists.
fn section(id: &SurfaceId, section: Section, open: bool, count: usize, t: &Tokens) -> El {
    let title = match section {
        Section::Outline => "Outline",
        Section::Backlinks => "Backlinks",
    };
    let id = id.clone();
    let caret = if open {
        &attn_ui::icon::DISCLOSE_OPEN
    } else {
        &attn_ui::icon::DISCLOSE_CLOSED
    };
    row()
        .key(key(&id, &title.to_lowercase()))
        .role("button")
        .label(title)
        .focusable(true)
        .expanded(open)
        .w_full()
        .items_center()
        .gap(7.0)
        .px(6.0)
        .pt(2.0)
        .pb(space::S2)
        .child(rune::icon(caret, 10.0).color(t.text_muted))
        .child(
            type_::upper(title, type_::BOLD_CAPS)
                .color(t.text_muted)
                .grow(1.0),
        )
        .children((open && count > 0).then(|| {
            text(count.to_string())
                .font(type_::MONO_CAPTION)
                .color(t.text_secondary)
                .px(6.0)
                .py(1.0)
                .radius(8.0)
                .fill(t.bg_elevated)
        }))
        .on_press(move |state: &mut State| {
            if let Some(surface) = state.notebook.surface_mut(&id) {
                let closed = match section {
                    Section::Outline => &mut surface.folds.outline_closed,
                    Section::Backlinks => &mut surface.folds.backlinks_closed,
                };
                *closed = !*closed;
            }
        })
}

/// A note's kind beside its title, as the Tauri app's `.notebook-kind` pill.
fn kind_badge(kind: &str, ink: rune::Color, t: &Tokens) -> El {
    type_::upper(kind, type_::BADGE)
        .color(ink)
        .px(space::S2)
        .py(2.0)
        .radius(999.0)
        .fill(t.bg_elevated)
        .border(1.0, ink.alpha(0.42))
}

fn basename(path: &str) -> &str {
    path.rsplit('/').next().unwrap_or(path)
}

/// A button's word in the secondary ink, then its key as a keycap, as Tauri's notebook
/// buttons carry their shortcut.
fn word_and_key(
    word: &'static str,
    keys: Option<String>,
    t: Tokens,
) -> impl FnOnce(TextStyle, Color) -> El {
    move |font, _| {
        row()
            .gap(8.0)
            .items_center()
            .child(
                text(word)
                    .font(TextStyle {
                        weight: 400.0,
                        ..font
                    })
                    .color(t.text_secondary),
            )
            .children(keys.map(|keys| keycap::Keys::outlined(&[keys]).build(&t).a11y_hidden()))
    }
}
