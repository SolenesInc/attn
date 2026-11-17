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
    /// Edits of tiles off screen, or of tiles that showed another edit, until they reach disk.
    pub(crate) shelf: Shelf,
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

/// An edit on its way to another window: its draft as text, since each window keeps its own
/// editor.
pub(crate) struct Handed {
    home: Home,
    path: String,
    kind: FileKind,
    disk: String,
    hash: String,
    text: String,
    save: Save,
    edited_at: Option<f64>,
}

impl Open {
    pub(crate) fn hand(self) -> Handed {
        Handed {
            text: self.draft.text(),
            home: self.home,
            path: self.path,
            kind: self.kind,
            disk: self.disk,
            hash: self.hash,
            save: self.save,
            edited_at: self.edited_at,
        }
    }
}

impl Handed {
    pub(crate) fn open(self) -> Open {
        Open {
            draft: Draft::of(self.kind, &self.text),
            home: self.home,
            path: self.path,
            kind: self.kind,
            disk: self.disk,
            hash: self.hash,
            save: self.save,
            edited_at: self.edited_at,
        }
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

/// The edits waiting for their tile, each in a place of its own: putting an edit on the shelf
/// never replaces another, so two edits of one tile both wait until each reaches disk or the
/// person settles it.
#[derive(Default)]
pub(crate) struct Shelf {
    edits: Vec<Shelved>,
    next: u64,
}

pub(crate) struct Shelved {
    id: u64,
    tile: String,
    root: Option<String>,
    open: Open,
}

impl Shelved {
    pub(crate) fn into_parts(self) -> (String, Option<String>, Open) {
        (self.tile, self.root, self.open)
    }

    fn of(&self, tile: &str, root: Option<&str>) -> bool {
        self.tile == tile && self.root.as_deref() == root
    }
}

impl Shelf {
    /// Puts the edit of tile `tile`, reading `root`, beside any other edit waiting there.
    pub(crate) fn put(&mut self, tile: String, root: Option<String>, open: Open) -> u64 {
        self.next += 1;
        let id = self.next;
        self.edits.push(Shelved {
            id,
            tile,
            root,
            open,
        });
        id
    }

    pub(crate) fn get(&self, id: u64) -> Option<&Open> {
        self.edits.iter().find(|e| e.id == id).map(|e| &e.open)
    }

    pub(crate) fn get_mut(&mut self, id: u64) -> Option<&mut Open> {
        self.edits
            .iter_mut()
            .find(|e| e.id == id)
            .map(|e| &mut e.open)
    }

    pub(crate) fn remove(&mut self, id: u64) -> Option<Shelved> {
        let at = self.edits.iter().position(|e| e.id == id)?;
        Some(self.edits.remove(at))
    }

    /// The edit the tile showed last when it left the screen, taken off the shelf.
    pub(crate) fn take_newest(&mut self, tile: &str, root: Option<&str>) -> Option<Open> {
        let at = self.edits.iter().rposition(|e| e.of(tile, root))?;
        Some(self.edits.remove(at).open)
    }

    /// The oldest edit of the tile with no save in flight, which waits for the person, taken
    /// off the shelf.
    pub(crate) fn take_settled(&mut self, tile: &str, root: Option<&str>) -> Option<Open> {
        let at = self
            .edits
            .iter()
            .position(|e| e.of(tile, root) && e.open.save.writing.is_none())?;
        Some(self.edits.remove(at).open)
    }

    /// The edits with no save in flight.
    pub(crate) fn settled(&self) -> Vec<u64> {
        self.edits
            .iter()
            .filter(|e| e.open.save.writing.is_none())
            .map(|e| e.id)
            .collect()
    }

    /// The edit whose save in flight is write `write_id`.
    pub(crate) fn writing(&self, write_id: u64) -> Option<u64> {
        self.edits
            .iter()
            .find(|e| {
                e.open
                    .save
                    .writing
                    .as_ref()
                    .is_some_and(|(id, _)| *id == write_id)
            })
            .map(|e| e.id)
    }
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
