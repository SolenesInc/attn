use std::fs::OpenOptions;
use std::io::{self, Write};
use std::path::{Path, PathBuf};

use crate::frame::Folders;

const FREE_NAME_TRIES: usize = 1_000;

pub(super) fn file_name(created_at_ms: i64) -> String {
    format!(
        "attn-{}.attn-report.json",
        crate::calendar::file_stamp(created_at_ms)
    )
}

/// Where reports go without asking: the platform's Downloads, or the one in the home folder.
pub(super) fn downloads(folders: &Folders) -> Option<PathBuf> {
    folders
        .downloads
        .clone()
        .or_else(|| folders.home.as_ref().map(|home| home.join("Downloads")))
}

/// Writes `contents` to a new file in `folder` named `name`, or `name` with `-2`, `-3` and on
/// before `.json` when that name is taken. It never replaces a file.
pub(super) fn write_new(folder: &Path, name: &str, contents: &str) -> io::Result<PathBuf> {
    let stem = name.strip_suffix(".json").unwrap_or(name);
    for suffix in 1..=FREE_NAME_TRIES {
        let path = match suffix {
            1 => folder.join(name),
            n => folder.join(format!("{stem}-{n}.json")),
        };
        match OpenOptions::new().write(true).create_new(true).open(&path) {
            Ok(mut file) => {
                file.write_all(contents.as_bytes())?;
                return Ok(path);
            }
            Err(e) if e.kind() == io::ErrorKind::AlreadyExists => {}
            Err(e) => return Err(e),
        }
    }
    Err(io::Error::other(format!(
        "{FREE_NAME_TRIES} reports named {name} already exist in {}",
        folder.display()
    )))
}
