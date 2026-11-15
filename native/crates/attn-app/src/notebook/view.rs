//! How a notebook surface looks: the file tree, the open document with its save status, and a
//! rail with the note's outline and the notes that link to it.

use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, ScrollSpec, TextStyle, Tx, col, el, row, text, text_input};
use rune_kit::{Button, ButtonVariant, ControlSize};

use super::file_kind::FileKind;
use super::io;
use super::model::{Backlinks, Doc, Draft, Listing, Open, Surface, SurfaceId};
use super::outline::outline;
use crate::State;

const TREE_WIDTH: f32 = 220.0;
/// A tile narrower than these folds its file tree and its rail, as the Tauri tile does.
const TREE_FOLDS_BELOW: f32 = 620.0;
const RAIL_FOLDS_BELOW: f32 = 900.0;
const RAIL_WIDTH: f32 = 240.0;
const INDENT: f32 = 12.0;
const NOTE: TextStyle = TextStyle::new(14.0, 400.0).lh(23.0);
const HEADING: TextStyle = TextStyle::new(18.0, 600.0);
const INSET: f32 = 24.0;

fn key(id: &SurfaceId, part: &str) -> String {
    match id {
        SurfaceId::Fullscreen => format!("notebook.{part}"),
        SurfaceId::Tile(tile) => format!("notebook.tile.{tile}.{part}"),
    }
}

/// The fullscreen notebook, over the whole window.
pub(crate) fn fullscreen(state: &State, t: &Tokens) -> El {
    let id = SurfaceId::Fullscreen;
    let body = match state.notebook.fullscreen.as_ref() {
        Some(surface) => surface_view(&state.notebook, &id, surface, t),
        None => col(),
    };
    let panel = col()
        .key("notebook.panel")
        .role("dialog")
        .labelled_by("notebook.title")
        .modal_tx(true, |state: &mut State, tx: &mut Tx| {
            super::close_fullscreen(state, tx)
        })
        .on_paste_content(crate::keys::on_paste)
        .w_full()
        .h_full()
        .radius(8.0)
        .fill(t.bg_panel)
        .border(1.0, t.border)
        .child(header(state, t))
        .child(body.grow(1.0).min_h(0.0));
    el().key("notebook")
        .absolute(0.0, 0.0)
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
                .pt(crate::shell::TITLE_BAR_HEIGHT + INSET)
                .px(INSET)
                .pb(INSET)
                .child(panel),
        )
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
        .items_center()
        .gap(space::S3)
        .px(space::S3)
        .py(space::S3)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .child(
            col()
                .grow(1.0)
                .child(
                    text("Knowledge base")
                        .font(type_::SECTION)
                        .color(t.text_tertiary),
                )
                .child(
                    text("Notebook")
                        .key("notebook.title")
                        .role("heading")
                        .level(1)
                        .font(HEADING)
                        .color(t.text_primary),
                ),
        )
        .children(pulse)
        .child(
            Button::new("Close")
                .control_size(ControlSize::Small)
                .help("Close (Esc)")
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

/// An editor tile's body.
pub(crate) fn tile_body(notebook: &super::Notebook, tile: &str, t: &Tokens) -> El {
    let id = SurfaceId::Tile(tile.to_owned());
    match notebook.tiles.get(tile) {
        Some(surface) => surface_view(notebook, &id, surface, t),
        None => col().w_full().h_full().child(message("Loading…", t)),
    }
}

fn surface_view(notebook: &super::Notebook, id: &SurfaceId, surface: &Surface, t: &Tokens) -> El {
    let (tree_fits, rail_fits) = fits(id, surface.width);
    let tree_open = surface.folds.tree.unwrap_or(tree_fits);
    let rail = rail(id, surface, t);
    let rail_open = surface.folds.rail.unwrap_or(rail_fits);
    let sized = id.clone();
    row()
        .key(key(id, "surface"))
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
        .child(fold_button(id, Pane::Tree, tree_open, t))
        .child(document(id, surface, t))
        .children(
            rail.is_some()
                .then(|| fold_button(id, Pane::Rail, rail_open, t)),
        )
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
    Button::new(glyph)
        .variant(ButtonVariant::GhostMuted)
        .control_size(ControlSize::Mini)
        .help(label)
        .build(attn_ui::kit(t))
        .key(key(
            &id,
            match pane {
                Pane::Tree => "fold.tree",
                Pane::Rail => "fold.rail",
            },
        ))
        .label(label)
        .expanded(open)
        .on_press(move |state: &mut State| {
            if let Some(surface) = state.notebook.surface_mut(&id) {
                let fold = match pane {
                    Pane::Tree => &mut surface.folds.tree,
                    Pane::Rail => &mut surface.folds.rail,
                };
                *fold = Some(!open);
            }
        })
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
        .py(space::S2)
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
    let name = if folder {
        format!(
            "{} {}",
            if expanded {
                attn_ui::icon::CARET_DOWN
            } else {
                attn_ui::icon::CARET_RIGHT
            },
            entry.name
        )
    } else {
        entry.name.clone()
    };
    let mut row_el = row()
        .key(format!("{}.{}", key(&id, "file"), entry.path))
        .role(if folder { "treeitem" } else { "link" })
        .label(entry.name.clone())
        .w_full()
        .pl(space::S3 + depth as f32 * INDENT)
        .pr(space::S2)
        .py(3.0)
        .child(
            text(name)
                .font(type_::BODY)
                .color(if open {
                    t.text_primary
                } else {
                    t.text_secondary
                })
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
        row_el = row_el.fill(t.bg_selected).a11y_value("open");
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

fn document(id: &SurfaceId, surface: &Surface, t: &Tokens) -> El {
    let body = match &surface.doc {
        Doc::Empty => empty(id, t),
        Doc::Loading { shown: None, .. } => message("Loading…", t),
        Doc::Loading {
            shown: Some(open), ..
        } => open_view(id, open, t),
        Doc::Failed { error, .. } => notice("File unavailable", error, None, t),
        Doc::Binary { path } => notice(
            "Preview not available",
            &format!("{} can't be opened here yet.", basename(path)),
            Some(path),
            t,
        ),
        Doc::Open(open) => open_view(id, open, t),
    };
    col()
        .key(key(id, "document"))
        .role("main")
        .grow(1.0)
        .min_w(0.0)
        .h_full()
        .min_h(0.0)
        .child(body)
}

fn empty(id: &SurfaceId, t: &Tokens) -> El {
    let id = id.clone();
    col()
        .gap(space::S2)
        .padding(INSET)
        .child(heading("Nothing selected", t))
        .child(
            text("Find a note, or pick one from the tree.")
                .font(type_::BODY)
                .color(t.text_secondary),
        )
        .child(
            Button::new("Find a note")
                .control_size(ControlSize::Small)
                .build(attn_ui::kit(t))
                .key(key(&id, "find"))
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::finder::open(state, id.clone(), tx)
                }),
        )
}

fn notice(title: &str, detail: &str, path: Option<&str>, t: &Tokens) -> El {
    col()
        .gap(space::S2)
        .padding(INSET)
        .child(heading(title, t))
        .child(
            text(detail.to_owned())
                .font(type_::BODY)
                .color(t.text_secondary)
                .wrap(),
        )
        .children(path.map(|p| text(p.to_owned()).font(type_::LABEL).color(t.text_tertiary)))
}

fn heading(words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .role("heading")
        .level(2)
        .font(HEADING)
        .color(t.text_primary)
}

fn message(words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .font(type_::BODY)
        .color(t.text_tertiary)
        .padding(INSET)
}

fn open_view(id: &SurfaceId, open: &Open, t: &Tokens) -> El {
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
                .px(INSET)
                .pt(space::S3)
                .pb(space::S2)
                .child(
                    row()
                        .items_center()
                        .gap(space::S2)
                        .child(heading(&title, t).key(key(id, "document.title")))
                        .child(
                            text(badge)
                                .key(key(id, "document.kind"))
                                .font(type_::LABEL)
                                .color(if journal { t.accent } else { t.text_tertiary }),
                        ),
                )
                .child(
                    text(open.path.clone())
                        .font(type_::LABEL)
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
                .px(INSET)
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
    match open {
        Some(draft) => draft,
        None => &mut notebook.scratch,
    }
}

fn rail(id: &SurfaceId, surface: &Surface, t: &Tokens) -> Option<El> {
    let open = surface.open().filter(|o| o.kind == FileKind::Markdown)?;
    if !surface.on_notebook() {
        return None;
    }
    let note = open.draft.text();
    let headings = outline(&note);
    let folds = surface.folds;
    let outline_items = headings.into_iter().enumerate().map(|(i, h)| {
        text(h.text.clone())
            .key(key(id, &format!("outline.{i}")))
            .font(type_::BODY)
            .color(t.text_secondary)
            .truncate()
            .pl(space::S2 + (h.level.saturating_sub(1)) as f32 * INDENT)
            .role("button")
            .label(h.text)
    });
    let outline_count = outline(&note).len();
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
                    .px(space::S2)
                    .py(3.0)
                    .child(
                        text(name)
                            .font(type_::BODY)
                            .color(t.text_secondary)
                            .truncate(),
                    )
                    .child(
                        text(e.path.clone())
                            .font(type_::LABEL)
                            .color(t.text_tertiary)
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
    Some(
        col()
            .key(key(id, "context"))
            .role("complementary")
            .label("Context")
            .w(RAIL_WIDTH)
            .h_full()
            .min_h(0.0)
            .py(space::S3)
            .gap(space::S3)
            .border_sides([0.0, 0.0, 0.0, 1.0], t.border_subtle)
            .scroll(ScrollSpec {
                stick_bottom: false,
            })
            .child(section(
                id,
                Section::Outline,
                !folds.outline_closed,
                outline_count,
                t,
            ))
            .children((!folds.outline_closed).then(|| {
                if outline_count == 0 {
                    col().child(
                        text("No headings.")
                            .font(type_::BODY)
                            .color(t.text_tertiary)
                            .px(space::S3),
                    )
                } else {
                    col().px(space::S2).children(outline_items)
                }
            }))
            .child(section(
                id,
                Section::Backlinks,
                !folds.backlinks_closed,
                backlinks_count,
                t,
            ))
            .children((!folds.backlinks_closed).then(|| {
                match backlinks_empty {
                    Some(words) => col().child(
                        text(words)
                            .font(type_::BODY)
                            .color(t.text_tertiary)
                            .px(space::S3),
                    ),
                    None => col().px(space::S1).children(backlinks),
                }
            })),
    )
}

#[derive(Clone, Copy)]
enum Section {
    Outline,
    Backlinks,
}

fn section(id: &SurfaceId, section: Section, open: bool, count: usize, t: &Tokens) -> El {
    let title = match section {
        Section::Outline => "Outline",
        Section::Backlinks => "Backlinks",
    };
    let words = if open && count > 0 {
        format!("{title} {count}")
    } else {
        title.to_owned()
    };
    let id = id.clone();
    Button::new(words)
        .variant(ButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
        .build(attn_ui::kit(t))
        .key(key(&id, &title.to_lowercase()))
        .label(title)
        .expanded(open)
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

fn basename(path: &str) -> &str {
    path.rsplit('/').next().unwrap_or(path)
}
