use std::path::{Path, PathBuf};

use crate::instance;

const MARKER_FILE_NAME: &str = "migration-failure.json";

#[derive(Debug, serde::Serialize)]
pub(crate) struct MigrationFailureMarker {
    marker_path: String,
    contents: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    read_error: Option<String>,
}

fn marker_path_in(data_dir: &Path) -> PathBuf {
    data_dir.join(MARKER_FILE_NAME)
}

pub(crate) fn marker_exists() -> bool {
    instance::data_dir()
        .map(|dir| marker_path_in(&dir).exists())
        .unwrap_or(false)
}

fn read_marker_in(data_dir: &Path) -> Option<MigrationFailureMarker> {
    let path = marker_path_in(data_dir);
    let marker_path = path.display().to_string();
    match std::fs::read_to_string(&path) {
        Ok(contents) => Some(MigrationFailureMarker {
            marker_path,
            contents,
            read_error: None,
        }),
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => None,
        Err(err) => Some(MigrationFailureMarker {
            marker_path,
            contents: String::new(),
            read_error: Some(err.to_string()),
        }),
    }
}

#[tauri::command]
pub(crate) fn read_migration_failure() -> Result<Option<MigrationFailureMarker>, String> {
    Ok(read_marker_in(&instance::data_dir()?))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn data_dir_named(name: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!(
            "attn-migration-marker-{name}-{}",
            std::process::id()
        ));
        std::fs::remove_dir_all(&dir).ok();
        std::fs::create_dir_all(&dir).expect("temp dir");
        dir
    }

    #[test]
    fn an_unreadable_marker_is_still_a_failure_to_show() {
        let dir = data_dir_named("unreadable");
        std::fs::create_dir_all(marker_path_in(&dir)).expect("marker path as a directory");

        let marker = read_marker_in(&dir).expect("a marker that exists must be reported");
        assert_eq!(
            marker.marker_path,
            marker_path_in(&dir).display().to_string()
        );
        assert!(marker.read_error.is_some(), "{marker:?}");
        assert!(marker_path_in(&dir).exists());

        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn no_marker_means_no_failure() {
        let dir = data_dir_named("absent");
        assert!(read_marker_in(&dir).is_none());
        std::fs::remove_dir_all(&dir).ok();
    }
}
