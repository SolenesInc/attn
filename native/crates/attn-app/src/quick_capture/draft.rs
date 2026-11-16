//! The unsent capture of each profile, on disk where the Tauri app keeps it, so either app
//! picks up the other's draft: `<data dir>/quick-capture-drafts/<profile>/`, the draft as
//! `quick-capture-draft.json` and each attached file in `capture-draft-files/<id>.file`.

use std::fs::{self, File};
use std::io::Write;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

const DRAFTS: &str = "quick-capture-drafts";
const DRAFT: &str = "quick-capture-draft.json";
const FILES: &str = "capture-draft-files";

/// The draft as the file holds it.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub(crate) struct Stored {
    pub id: String,
    pub text: String,
    pub mailbox: String,
    pub files: Vec<StoredFile>,
    pub uncertain: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub(crate) struct StoredFile {
    pub id: String,
    pub name: String,
}

/// Where one profile keeps its draft.
#[derive(Clone, Debug)]
pub(crate) struct Folder(PathBuf);

impl Folder {
    /// The draft folder of `profile` under `data_dir`, for a profile id the daemon minted:
    /// `profile-` and a UUID.
    pub fn of(data_dir: &Path, profile: &str) -> Result<Folder, String> {
        let id = profile
            .strip_prefix("profile-")
            .ok_or("Quick Capture needs a profile identity")?;
        if !is_uuid(id) {
            return Err("Quick Capture profile id must contain a UUID".into());
        }
        Ok(Folder(data_dir.join(DRAFTS).join(profile)))
    }

    /// Where an attached file's bytes are kept.
    pub fn file(&self, id: &str) -> PathBuf {
        self.0.join(FILES).join(format!("{id}.file"))
    }

    /// The draft as last written.
    fn load(&self) -> Result<Option<Stored>, String> {
        let path = self.0.join(DRAFT);
        match fs::read(&path) {
            Ok(bytes) => serde_json::from_slice(&bytes)
                .map(Some)
                .map_err(|e| e.to_string()),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
            Err(e) => Err(format!("{}: {e}", path.display())),
        }
    }

    /// The draft as last written, for a panel taking it up. Files it does not name, left by a
    /// write that ended early, are removed: nothing copies into the folder before the panel
    /// holds its draft.
    pub fn read(&self) -> Result<Option<Stored>, String> {
        let stored = self.load()?;
        let kept: Vec<PathBuf> = stored
            .iter()
            .flat_map(|d| &d.files)
            .map(|f| self.file(&f.id))
            .collect();
        if let Ok(entries) = fs::read_dir(self.0.join(FILES)) {
            for entry in entries.flatten() {
                if !kept.contains(&entry.path()) {
                    let _ = fs::remove_file(entry.path());
                }
            }
        }
        Ok(stored)
    }

    /// Replaces the draft, durably, then removes the files the earlier draft kept and this one
    /// does not.
    pub fn write(&self, draft: &Stored) -> Result<(), String> {
        let previous = self
            .load()
            .ok()
            .flatten()
            .map(|d| d.files)
            .unwrap_or_default();
        let bytes = serde_json::to_vec(draft).map_err(|e| e.to_string())?;
        commit(&self.0.join(DRAFT), &bytes)?;
        for gone in previous.iter().filter(|f| !draft.files.contains(f)) {
            let path = self.file(&gone.id);
            match fs::remove_file(&path) {
                Err(e) if e.kind() != std::io::ErrorKind::NotFound => {
                    return Err(format!("{}: {e}", path.display()));
                }
                _ => {}
            }
        }
        Ok(())
    }
}

/// Writes `bytes` beside `path` and renames them over it, syncing both the file and its folder.
pub(crate) fn commit(path: &Path, bytes: &[u8]) -> Result<(), String> {
    let parent = path.parent().ok_or("Cache path has no parent")?;
    fn io(p: &Path) -> impl Fn(std::io::Error) -> String + '_ {
        move |e| format!("{}: {e}", p.display())
    }
    fs::create_dir_all(parent).map_err(io(parent))?;
    let temporary = path.with_extension("tmp");
    let mut file = File::create(&temporary).map_err(io(&temporary))?;
    file.write_all(bytes).map_err(io(&temporary))?;
    file.sync_all().map_err(io(&temporary))?;
    fs::rename(&temporary, path).map_err(io(path))?;
    File::open(parent)
        .and_then(|dir| dir.sync_all())
        .map_err(io(parent))
}

pub(crate) fn is_uuid(id: &str) -> bool {
    id.len() == 36
        && id.bytes().enumerate().all(|(index, byte)| {
            if matches!(index, 8 | 13 | 18 | 23) {
                byte == b'-'
            } else {
                byte.is_ascii_hexdigit()
            }
        })
}
