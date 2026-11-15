//! What a notebook surface holds: the fullscreen notebook and each editor tile on screen read
//! one folder, show one file of it, and save that file's edits.

use std::collections::{BTreeMap, BTreeSet};

use attn_protocol::{FsEntry, NotebookEntry};
use rune::TextEdit;

use super::file_kind::FileKind;

/// Every notebook surface of the window, and the edits of tiles that left the screen.
#[derive(Default)]
pub struct Notebook {
    pub(crate) fullscreen: Option<Surface>,
    pub(crate) tiles: BTreeMap<String, Surface>,
    /// Edits of tiles that left the screen, by `shelf_key`, kept until their save lands.
    pub(crate) shelved: BTreeMap<String, Open>,
    /// The file the fullscreen notebook showed last, which it opens again.
    pub(crate) last_path: Option<String>,
    /// The roots this window watches, as asked, with the root the daemon resolved.
    pub(crate) watches: BTreeMap<String, Option<String>>,
    /// The connection the watches were asked on: a new one asks again.
    pub(crate) finder: Option<super::finder::Finder>,
    /// What waits for a surface's edit to reach disk: opening another file, closing, switching
    /// the root. A save that conflicts or fails drops it.
    pub(crate) after_save: BTreeMap<SurfaceId, super::io::Then>,
    /// The editor tile whose "Editor root" list is open.
    pub(crate) root_picker: Option<String>,
    /// What a text field edits while its file is gone from the surface.
    pub(crate) scratch: TextEdit,
    /// Rises with every change a view could show, for the memos that read the notebook.
    pub(crate) revision: u64,
    next_write: u64,
}

#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub(crate) enum SurfaceId {
    Fullscreen,
    Tile(String),
}

pub(crate) struct Surface {
    /// The folder it reads; `None` is the profile's notebook.
    pub(crate) root: Option<String>,
    /// The profile's notebook folder when the surface opened on it.
    pub(crate) notebook: Option<String>,
    /// Its width in points as last laid out, which folds a narrow tile's panes.
    pub(crate) width: f32,
    pub(crate) tree: Tree,
    pub(crate) doc: Doc,
    pub(crate) backlinks: Backlinks,
    pub(crate) folds: Folds,
    /// The last `Facts::file_changes` count it followed.
    pub(crate) changes: u64,
    /// Rises with each file it starts to open, so a late answer for another file is dropped.
    pub(crate) load: u64,
}

#[derive(Default)]
pub(crate) struct Tree {
    pub(crate) folders: BTreeMap<String, Listing>,
    pub(crate) expanded: BTreeSet<String>,
}

pub(crate) enum Listing {
    Loading,
    Listed(Vec<FsEntry>),
    Failed(String),
}

pub(crate) enum Doc {
    Empty,
    Loading {
        path: String,
        shown: Option<Box<Open>>,
    },
    Failed {
        path: String,
        error: String,
    },
    Binary {
        path: String,
    },
    Open(Box<Open>),
}

/// A file open for editing: what disk held when it was read or last saved, and the edit.
#[derive(Clone)]
pub(crate) struct Open {
    pub(crate) home: Home,
    pub(crate) path: String,
    pub(crate) kind: FileKind,
    pub(crate) disk: String,
    pub(crate) hash: String,
    pub(crate) draft: Draft,
    pub(crate) save: Save,
    /// When the draft last changed, for the autosave that follows it.
    pub(crate) edited_at: Option<f64>,
}

/// The folder a file was read from, which every save of its edit goes back to.
#[derive(Clone, PartialEq, Eq)]
pub(crate) enum Home {
    /// The profile's notebook, as the folder it was when the file was read.
    Notebook(Option<String>),
    Folder(String),
}

#[derive(Clone, Default)]
pub(crate) struct Save {
    /// The write in flight: its id and the text it sends.
    pub(crate) writing: Option<(u64, String)>,
    pub(crate) saved_at: Option<f64>,
    pub(crate) error: Option<String>,
    /// The file changed on disk under the edit: the hash it has now, or `None` when it is gone.
    pub(crate) conflict: Option<Option<String>>,
}

pub(crate) enum Backlinks {
    None,
    Loading,
    Listed(Vec<NotebookEntry>),
}

#[derive(Clone, Copy, Default)]
pub(crate) struct Folds {
    /// Set once the user folds or unfolds the pane, which then wins over the tile's width.
    pub(crate) tree: Option<bool>,
    pub(crate) rail: Option<bool>,
    pub(crate) outline_closed: bool,
    pub(crate) backlinks_closed: bool,
}

impl Surface {
    pub(crate) fn new(root: Option<String>, notebook: Option<String>, changes: u64) -> Surface {
        Surface {
            notebook: notebook.filter(|_| root.is_none()),
            root,
            width: 0.0,
            tree: Tree::default(),
            doc: Doc::Empty,
            backlinks: Backlinks::None,
            folds: Folds::default(),
            changes,
            load: 0,
        }
    }

    pub(crate) fn open(&self) -> Option<&Open> {
        match &self.doc {
            Doc::Open(open) => Some(open),
            Doc::Loading { shown, .. } => shown.as_deref(),
            _ => None,
        }
    }

    pub(crate) fn open_mut(&mut self) -> Option<&mut Open> {
        match &mut self.doc {
            Doc::Open(open) => Some(open),
            _ => None,
        }
    }

    pub(crate) fn path(&self) -> Option<&str> {
        match &self.doc {
            Doc::Empty => None,
            Doc::Loading { path, .. } | Doc::Failed { path, .. } | Doc::Binary { path } => {
                Some(path)
            }
            Doc::Open(open) => Some(&open.path),
        }
    }

    /// Whether it reads the profile's notebook, which alone has backlinks.
    pub(crate) fn on_notebook(&self) -> bool {
        self.root.is_none()
    }
}

impl Open {
    pub(crate) fn read(
        home: Home,
        path: String,
        kind: FileKind,
        content: String,
        hash: String,
    ) -> Open {
        let draft = Draft::of(kind, &content);
        Open {
            home,
            path,
            kind,
            draft,
            disk: content,
            hash,
            save: Save::default(),
            edited_at: None,
        }
    }

    /// Takes what disk now holds as both the saved text and the edit.
    pub(crate) fn reread(&mut self, content: String, hash: String) {
        self.draft.follow(&content);
        self.disk = content;
        self.hash = hash;
    }

    pub(crate) fn dirty(&self) -> bool {
        self.draft.text() != self.disk
    }
}

impl Notebook {
    pub(crate) fn surface(&self, id: &SurfaceId) -> Option<&Surface> {
        match id {
            SurfaceId::Fullscreen => self.fullscreen.as_ref(),
            SurfaceId::Tile(tile) => self.tiles.get(tile),
        }
    }

    pub(crate) fn surface_mut(&mut self, id: &SurfaceId) -> Option<&mut Surface> {
        self.revision += 1;
        match id {
            SurfaceId::Fullscreen => self.fullscreen.as_mut(),
            SurfaceId::Tile(tile) => self.tiles.get_mut(tile),
        }
    }

    pub(crate) fn next_write(&mut self) -> u64 {
        self.next_write += 1;
        self.next_write
    }
}

/// Where a tile's edit waits while the tile is off screen: the tile and the folder it read.
pub(crate) fn shelf_key(tile: &str, root: Option<&str>) -> String {
    format!("{tile}\0{}", root.unwrap_or(""))
}

/// The edit of an open file: a note in the markdown editor, any other text in a plain field.
#[derive(Clone)]
pub(crate) enum Draft {
    Field(Box<TextEdit>),
    Note(Box<super::editor::Editor>),
}

impl Draft {
    pub(crate) fn of(kind: FileKind, content: &str) -> Draft {
        match kind {
            FileKind::Markdown => Draft::Note(Box::new(super::editor::Editor::new(content))),
            _ => Draft::Field(Box::new(TextEdit::new(content))),
        }
    }

    pub(crate) fn text(&self) -> String {
        match self {
            Draft::Field(edit) => edit.text.clone(),
            Draft::Note(editor) => editor.text(),
        }
    }

    /// Takes `content` from disk, keeping the caret on its text in a note.
    pub(crate) fn follow(&mut self, content: &str) {
        match self {
            Draft::Field(edit) => **edit = TextEdit::new(content),
            Draft::Note(editor) => {
                editor.doc.follow(content);
                editor.edited();
            }
        }
    }
}
