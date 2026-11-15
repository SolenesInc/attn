use std::fs;
use std::io;
use std::path::{Path, PathBuf};

use rune::prefs::Prefs;
use rusqlite::{Connection, OptionalExtension};

pub const TAURI_SETTINGS: [&str; 12] = [
    "attn:terminal-diagnostics",
    "attn.codex_update_banner.dismissed_version",
    "attn.codex_old_tiles_banner.dismissed",
    "attn.profile.selected",
    "attn.outpost_updates.dismissed_home_build",
    "attn.workspace.selectionStyle",
    "attn-closed-update-run",
    "attn.whats_new.last_seen",
    "attn.whats_new.banner_dismissed",
    "attn.garden.frame",
    "attn.garden.fullscreenView",
    "attn.update_banner.dismissed_version",
];

const IMPORTED: &str = "tauri_settings_imported";

#[derive(Debug, PartialEq, Eq)]
pub enum Import {
    AlreadyDone,
    NoTauriStore,
    Imported(usize),
}

/// Copies the Tauri app's settings under `web_data` (`~/Library/WebKit/<bundle id>`) into
/// `prefs`, each as the string `localStorage` held, unless an earlier launch did. A failed
/// import leaves prefs unmarked, so the next launch tries again.
pub fn import(web_data: &Path, prefs: &mut Prefs) -> Result<Import, String> {
    if prefs.contains(IMPORTED) {
        return Ok(Import::AlreadyDone);
    }
    let outcome = match tauri_local_storage(web_data)? {
        None => Import::NoTauriStore,
        Some(database) => {
            let settings = read_settings(&database)?;
            for (key, value) in &settings {
                prefs
                    .set(key, value)
                    .map_err(|error| format!("saving {key} to prefs: {error}"))?;
            }
            Import::Imported(settings.len())
        }
    };
    prefs
        .set(IMPORTED, &true)
        .map_err(|error| format!("saving {IMPORTED} to prefs: {error}"))?;
    Ok(outcome)
}

fn tauri_local_storage(web_data: &Path) -> Result<Option<PathBuf>, String> {
    let origins = web_data.join("WebsiteData").join("Default");
    let entries = match fs::read_dir(&origins) {
        Ok(entries) => entries,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(format!("reading {}: {error}", origins.display())),
    };
    for entry in entries.flatten() {
        let store = entry.path().join(entry.file_name());
        let database = store.join("LocalStorage").join("localstorage.sqlite3");
        if is_tauri_origin(&fs::read(store.join("origin")).unwrap_or_default())
            && database.is_file()
        {
            return Ok(Some(database));
        }
    }
    Ok(None)
}

fn is_tauri_origin(origin: &[u8]) -> bool {
    let contains = |needle: &[u8]| origin.windows(needle.len()).any(|window| window == needle);
    contains(b"tauri") && contains(b"localhost")
}

fn read_settings(database: &Path) -> Result<Vec<(String, String)>, String> {
    let copy =
        tempfile::tempdir().map_err(|error| format!("making a temporary folder: {error}"))?;
    let copied = copy.path().join("localstorage.sqlite3");
    for suffix in ["", "-wal", "-shm"] {
        let from = PathBuf::from(format!("{}{suffix}", database.display()));
        let to = PathBuf::from(format!("{}{suffix}", copied.display()));
        match fs::copy(&from, &to) {
            Ok(_) => {}
            Err(error) if !suffix.is_empty() && error.kind() == io::ErrorKind::NotFound => {}
            Err(error) => return Err(format!("copying {}: {error}", from.display())),
        }
    }
    let connection = Connection::open(&copied)
        .map_err(|error| format!("opening {}: {error}", database.display()))?;
    let mut settings = Vec::new();
    for key in TAURI_SETTINGS {
        let value: Option<Vec<u8>> = connection
            .query_row("SELECT value FROM ItemTable WHERE key = ?1", [key], |row| {
                row.get(0)
            })
            .optional()
            .map_err(|error| format!("reading {key} from {}: {error}", database.display()))?;
        if let Some(value) = value {
            settings.push((
                key.to_string(),
                utf16le(&value).map_err(|error| format!("{key}: {error}"))?,
            ));
        }
    }
    Ok(settings)
}

fn utf16le(bytes: &[u8]) -> Result<String, String> {
    let (pairs, []) = bytes.as_chunks::<2>() else {
        return Err(format!("{} bytes is not UTF-16", bytes.len()));
    };
    let units: Vec<u16> = pairs.iter().map(|pair| u16::from_le_bytes(*pair)).collect();
    String::from_utf16(&units).map_err(|error| error.to_string())
}
