//! How a notebook surface looks: the file tree, the open document with its save status, and a
//! rail with the note's outline and the notes that link to it.

use attn_ui::Tokens;
use attn_ui::blocks::{Block, Disclosure, Inline, Inspector, List, Notice, Sheet, Stack};
use attn_ui::system::{
    Button, FieldState, IconSize, ListRow, Look, Size, Tone, Type, area_box, icon, icon_button,
    keycaps, tag, words,
};
use rune::{Color, El, Tx, col, row};
use rune_kit::TextArea;

use super::file_kind::FileKind;
use super::io;
use super::model::{Backlinks, Doc, Draft, Listing, Open, Surface, SurfaceId};
use super::outline::outline;
use crate::State;

/// The file tree's width: the tree of the Tauri tile, which holds a note's name of about 30
/// letters two folders deep.
const TREE_WIDTH: f32 = 280.0;
/// A notebook narrower than these folds its file tree and its rail, as the Tauri tile does, so
/// the note keeps its room.
const TREE_FOLDS_BELOW: f32 = 620.0;
const RAIL_FOLDS_BELOW: f32 = 900.0;
const SHELL_INSET: f32 = 12.0;

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
    let head = row()
        .items_center()
        .child(ListRow::lead_slot(icon(
            &attn_ui::icon::NOTEBOOK,
            IconSize::Large,
            t.brand,
        )))
        .child(
            Stack::tight()
                .child(words("Knowledge base", Type::Label, t.brand))
                .child(
                    words("Notebook", Type::PageTitle, t.text_primary)
                        .key("notebook.title")
                        .role("heading")
                        .level(1),
                ),
        );
    let pulse = chief(state).map(|active| {
        words(
            if active {
                "chief: active"
            } else {
                "chief: idle"
            },
            Type::Small,
            if active { t.accent } else { t.text_tertiary },
        )
        .key("notebook.chief")
        .role("status")
    });
    let close = Button::new("Close")
        .size(Size::Compact)
        .look(Look::Quiet)
        .trailing(keycaps(["esc"], t))
        .on_press_tx(|state: &mut State, tx: &mut Tx| super::close_fullscreen(state, tx))
        .build(t)
        .key("notebook.close");
    let mut sheet = Sheet::new("notebook.panel", "Notebook").head(head);
    if let Some(pulse) = pulse {
        sheet = sheet.aside(pulse);
    }
    let [w, h] = state.notebook.panel;
    let glow = 0.32 * (0.8 * w).hypot(h);
    let mut panel = sheet
        .close(close)
        .fill(body)
        .framed()
        .build(t)
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
        .box_shadow(attn_ui::frame::shadow(t, 24.0, 80.0, 0.42));
    if glow > 0.0 {
        panel = panel.radial_gradient(
            [0.8, 0.0],
            glow,
            [(0.0, t.brand.alpha(0.08)), (1.0, t.brand.alpha(0.0))],
        );
    }
    crate::shell::under_title_bar("notebook", SHELL_INSET, panel, t)
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
    let (tree_fits, rail_fits) = fits(surface.width);
    let tree_open = surface.folds.tree.unwrap_or(tree_fits);
    let rail = rail(id, surface, t);
    let rail_open = surface.folds.rail.unwrap_or(rail_fits);
    let sized = id.clone();
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
        .child(document(id, surface, find, folds, t))
        .children(rail.filter(|_| rail_open))
        .children(super::finder::view(notebook, id, t))
}

/// Whether a notebook this wide has room for its file tree and its rail; one not laid out yet
/// has.
fn fits(width: f32) -> (bool, bool) {
    let roomy = width <= 0.0;
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

/// The button on a pane's edge that folds the file tree or the rail away and back.
fn fold_button(id: &SurfaceId, pane: Pane, open: bool, t: &Tokens) -> El {
    let (label, glyph) = match (pane, open) {
        (Pane::Tree, true) => ("Hide file tree", "‹"),
        (Pane::Tree, false) => ("Show file tree", "›"),
        (Pane::Rail, true) => ("Hide context rail", "›"),
        (Pane::Rail, false) => ("Show context rail", "‹"),
    };
    let part = match pane {
        Pane::Tree => "fold.tree",
        Pane::Rail => "fold.rail",
    };
    let id = id.clone();
    let button = icon_button(key(&id, part), label, glyph, IconSize::Small)
        .help(label)
        .on_press(move |state: &mut State| {
            if let Some(surface) = state.notebook.surface_mut(&id) {
                let fold = match pane {
                    Pane::Tree => &mut surface.folds.tree,
                    Pane::Rail => &mut surface.folds.rail,
                };
                *fold = Some(!open);
            }
        })
        .build(t);
    let (left, right) = match pane {
        Pane::Tree => (Some(0.0), None),
        Pane::Rail => (None, Some(0.0)),
    };
    col()
        .pin(left, Some(0.0), right, Some(0.0))
        .justify_center()
        .child(El::from(button).expanded(open))
}

fn tree(id: &SurfaceId, surface: &Surface, t: &Tokens) -> El {
    let mut list = List::new(key(id, "files.list"), "Files").led();
    match surface.tree.folders.get("") {
        None | Some(Listing::Loading) => {
            list = list.empty(key(id, "files.loading"), "Loading…", None);
        }
        Some(Listing::Failed(error)) => {
            list = list
                .failed(true)
                .empty(key(id, "files.failed"), error.clone(), None);
        }
        Some(Listing::Listed(_)) => {
            let mut rows = Vec::new();
            folder_rows(id, surface, "", 0, &mut rows, t);
            list = list.rows(rows);
        }
    }
    col()
        .w(TREE_WIDTH)
        .h_full()
        .min_h(0.0)
        .fill(pane_fill(t))
        .border_sides([0.0, 1.0, 0.0, 0.0], t.border_subtle)
        .child(
            list.scrolls_as(key(id, "files"), "navigation", "Notebook files")
                .scrolling(t),
        )
}

fn folder_rows(
    id: &SurfaceId,
    surface: &Surface,
    folder: &str,
    depth: u8,
    rows: &mut Vec<El>,
    t: &Tokens,
) {
    match surface.tree.folders.get(folder) {
        None | Some(Listing::Loading) => {}
        Some(Listing::Failed(error)) => rows.push(
            ListRow::note(
                key(id, &format!("files.failed.{folder}")),
                error.clone(),
                Type::Small,
                t.error,
                None,
                t,
            )
            .role("listitem"),
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
    depth: u8,
    expanded: bool,
    open: bool,
    t: &Tokens,
) -> El {
    let (id, path, folder) = (id.clone(), entry.path.clone(), entry.is_dir);
    let row = ListRow::new(
        format!("{}.{}", key(&id, "file"), entry.path),
        entry.name.clone(),
    )
    .depth(depth)
    .unfocusable()
    .selected(open);
    let row = if folder {
        let chevron = if expanded {
            &attn_ui::icon::DISCLOSE_OPEN
        } else {
            &attn_ui::icon::DISCLOSE_CLOSED
        };
        row.role("treeitem")
            .expanded(expanded)
            .lead(icon(chevron, IconSize::Small, t.text_secondary))
            .title(entry.name.clone())
    } else {
        row.role("link").quiet_title(entry.name.clone())
    };
    let row = row
        .build(t)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            if folder {
                toggle_folder(state, &id, &path, tx);
            } else {
                io::open(state, &id, path.clone(), tx);
            }
        });
    if open { row.a11y_value("open") } else { row }
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
    (tree_fold, rail_fold): (El, Option<El>),
    t: &Tokens,
) -> El {
    let body = match &surface.doc {
        Doc::Empty => empty(id, find, t),
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
        .child(tree_fold)
        .children(rail_fold)
}

/// A document pane with nothing to edit: the notebook's mark, a title and what to do, centred
/// in the pane.
fn state_view(title: &str, t: &Tokens) -> El {
    Stack::tight()
        .w_full()
        .h_full()
        .min_h(0.0)
        .center()
        .child(icon(
            &attn_ui::icon::NOTEBOOK,
            IconSize::Large,
            t.text_tertiary,
        ))
        .child(
            words(title.to_owned(), Type::Heading, t.text_secondary)
                .role("heading")
                .level(2),
        )
}

fn empty(id: &SurfaceId, find: Option<String>, t: &Tokens) -> El {
    let find_key = key(id, "find");
    let id = id.clone();
    let button = Button::new("Find a note")
        .size(Size::Compact)
        .look(Look::Plain);
    let button = match find {
        Some(keys) => button.trailing(keycaps([keys], t)),
        None => button,
    };
    state_view("Nothing selected", t)
        .child(words(
            "Find a note, or pick one from the tree.",
            Type::Small,
            t.text_tertiary,
        ))
        .child(El::from(
            button
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::finder::open(state, id.clone(), tx)
                })
                .build(t)
                .key(find_key),
        ))
}

fn notice(title: &str, detail: &str, path: Option<&str>, t: &Tokens) -> El {
    state_view(title, t)
        .child(
            words(detail.to_owned(), Type::Small, t.text_tertiary)
                .wrap()
                .max_w(360.0),
        )
        .children(path.map(|p| words(p.to_owned(), Type::Mono, t.text_tertiary)))
}

fn message(said: &str, t: &Tokens) -> El {
    col()
        .w_full()
        .h_full()
        .center()
        .child(words(said.to_owned(), Type::Body, t.text_tertiary))
}

fn open_view(id: &SurfaceId, open: &Open, t: &Tokens) -> El {
    let title = open.path.rsplit('/').next().unwrap_or(&open.path);
    let title = title.strip_suffix(".md").unwrap_or(title).to_owned();
    let kind = match open.kind {
        FileKind::Markdown => note_type(&open.draft.text()).unwrap_or_else(|| "note".into()),
        _ => "text".to_owned(),
    };
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
            let area = TextArea::new(field)
                .key(key(id, "editor"))
                .label("File contents")
                .auto_grow(3, usize::MAX)
                .on_edit(move |state: &mut State| draft_of(state, &field_id))
                .on_change(move |state: &mut State, tx: &mut Tx| {
                    super::edited(state, &changed_id, tx)
                });
            area_box(area, FieldState::Ready, t).into()
        }
    };
    let head = Inline::joined()
        .child(
            words(title.clone(), Type::PageTitle, t.text_primary)
                .key(key(id, "document.title"))
                .role("heading")
                .level(2)
                .truncate(),
        )
        .child(El::from(tag(
            key(id, "document.kind"),
            &kind,
            Tone::Neutral,
            t,
        )));
    let top = Stack::tight()
        .w_full()
        .child(words(open.path.clone(), Type::Mono, t.text_tertiary).truncate())
        .children(status(open).map(|s| {
            words(s, Type::Small, t.text_tertiary)
                .key(key(id, "status"))
                .role("status")
        }))
        .children(match &open.draft {
            Draft::Note(editor) => {
                super::editor::find::bar(editor, &key(id, "editor"), super::editor_of_mut, t)
            }
            Draft::Field(_) => None,
        });
    Sheet::new(key(id, "document.sheet"), title.clone())
        .head(head)
        .top(top)
        .article(key(id, "scroll"), title)
        .notices(conflict(id, open, t))
        .notices(save_error(open, t))
        .push(Block::custom(key(id, "editor.block"), editor))
        .build(t)
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

fn save_error(open: &Open, t: &Tokens) -> Option<Block> {
    let error = open.save.error.as_ref()?;
    Some(Notice::error("notebook.save-error", error.clone()).build(t))
}

fn conflict(id: &SurfaceId, open: &Open, t: &Tokens) -> Option<Block> {
    let current = open.save.conflict.as_ref()?;
    let said = if current.is_some() {
        "This file changed on disk since you opened it."
    } else {
        "This file was deleted on disk since you opened it."
    };
    let saving = open.save.writing.is_some();
    let (reload_id, over_id) = (id.clone(), id.clone());
    let actions = Inline::loose()
        .child(El::from(
            Button::new("Reload from disk")
                .size(Size::Compact)
                .look(Look::Plain)
                .disabled(saving)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::reload(state, &reload_id, tx)
                })
                .build(t)
                .key(key(id, "document.reload")),
        ))
        .child(El::from(
            Button::new("Overwrite anyway")
                .size(Size::Compact)
                .look(Look::Plain)
                .disabled(saving)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    super::overwrite(state, &over_id, tx)
                })
                .build(t)
                .key(key(id, "document.overwrite")),
        ));
    Some(
        Notice::warn(key(id, "document.conflict"), said)
            .action(actions)
            .build(t),
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
    let outline_rows = headings.into_iter().enumerate().map(|(i, h)| {
        let id = id.clone();
        let row = ListRow::new(key(&id, &format!("outline.{i}")), h.text.clone())
            .depth(h.level.saturating_sub(1).min(3) as u8);
        let row = if h.level == 1 {
            row.title(h.text)
        } else {
            row.quiet_title(h.text)
        };
        row.build(t)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                super::reveal_heading(state, &id, i, tx)
            })
    });
    let outline = List::new(key(id, "outline.headings"), "Headings")
        .roving()
        .rows(outline_rows)
        .empty(key(id, "outline.empty"), "No headings.", None);
    let (backlink_rows, backlinks_empty) = match &surface.backlinks {
        Backlinks::Loading => (Vec::new(), "Finding backlinks…"),
        Backlinks::Listed(entries) => (
            entries
                .iter()
                .map(|e| {
                    let name = e
                        .title
                        .clone()
                        .filter(|t| !t.is_empty())
                        .unwrap_or_else(|| basename(&e.path).to_owned());
                    let (id, path) = (id.clone(), e.path.clone());
                    ListRow::new(key(&id, &format!("backlink.{path}")), name.clone())
                        .unfocusable()
                        .title(name)
                        .subtitle(e.path.clone(), t)
                        .build(t)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            io::open(state, &id, path.clone(), tx)
                        })
                })
                .collect(),
            "No other note links here.",
        ),
        _ => (Vec::new(), "No other note links here."),
    };
    let backlinks_count = backlink_rows.len();
    let backlinks = List::new(key(id, "backlinks.list"), "Backlinks")
        .rows(backlink_rows)
        .empty(key(id, "backlinks.empty"), backlinks_empty, None);
    let rail = Inspector::side(key(id, "context"), "Context")
        .block(rail_part(
            id,
            Part::Outline,
            !folds.outline_closed,
            outline_count,
            outline,
            t,
        ))
        .block(rail_part(
            id,
            Part::Backlinks,
            !folds.backlinks_closed,
            backlinks_count,
            backlinks,
            t,
        ))
        .build(t)
        .fill(pane_fill(t));
    Some(rail)
}

#[derive(Clone, Copy)]
enum Part {
    Outline,
    Backlinks,
}

/// A part of the rail that opens and closes, with how many it lists.
fn rail_part(
    id: &SurfaceId,
    part: Part,
    open: bool,
    count: usize,
    list: List,
    t: &Tokens,
) -> Block {
    let title = match part {
        Part::Outline => "Outline",
        Part::Backlinks => "Backlinks",
    };
    let surface = id.clone();
    let toggle = move |state: &mut State, _: &mut Tx| {
        if let Some(surface) = state.notebook.surface_mut(&surface) {
            let closed = match part {
                Part::Outline => &mut surface.folds.outline_closed,
                Part::Backlinks => &mut surface.folds.backlinks_closed,
            };
            *closed = !*closed;
        }
    };
    let tags = if count > 0 {
        vec![count.to_string()]
    } else {
        Vec::new()
    };
    Disclosure::new(key(id, &title.to_lowercase()), title, open, toggle)
        .tags(tags)
        .push(list.build(t))
        .build(t)
}

fn basename(path: &str) -> &str {
    path.rsplit('/').next().unwrap_or(path)
}
