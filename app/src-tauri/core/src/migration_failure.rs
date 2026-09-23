use std::path::{Path, PathBuf};

use crate::instance;

const MARKER_FILE_NAME: &str = "migration-failure.json";

#[derive(Debug, serde::Serialize)]
pub(crate) struct MigrationFailureMarker {
    marker_path: String,
    contents: String,
}

fn marker_path_in(data_dir: &Path) -> PathBuf {
    data_dir.join(MARKER_FILE_NAME)
}

pub(crate) fn marker_exists() -> bool {
    instance::data_dir()
        .map(|dir| marker_path_in(&dir).exists())
        .unwrap_or(false)
}

fn read_marker_in(data_dir: &Path) -> Result<Option<MigrationFailureMarker>, String> {
    let path = marker_path_in(data_dir);
    match std::fs::read_to_string(&path) {
        Ok(contents) => Ok(Some(MigrationFailureMarker {
            marker_path: path.display().to_string(),
            contents,
        })),
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(err) => Err(format!("read {}: {err}", path.display())),
    }
}

#[tauri::command]
pub(crate) fn read_migration_failure() -> Result<Option<MigrationFailureMarker>, String> {
    read_marker_in(&instance::data_dir()?)
}
