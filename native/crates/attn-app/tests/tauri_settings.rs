#![cfg(target_os = "macos")]

use std::fs;
use std::path::{Path, PathBuf};

use attn_app::tauri_settings::{Import, TAURI_SETTINGS, import};
use rune::prefs::Prefs;

const FIXTURE: &str = concat!(env!("CARGO_MANIFEST_DIR"), "/tests/fixtures/tauri-web-data");

const FIXTURE_VALUES: [(&str, &str); 12] = [
    ("attn:terminal-diagnostics", "0"),
    ("attn.codex_update_banner.dismissed_version", "0.130.0"),
    (
        "attn.codex_old_tiles_banner.dismissed",
        r#"{"s-1":"0.129.0"}"#,
    ),
    ("attn.profile.selected", "profile-work"),
    (
        "attn.outpost_updates.dismissed_home_build",
        "0.14.0 (abc1234)",
    ),
    ("attn.workspace.selectionStyle", "spotlight"),
    ("attn-closed-update-run", "run-42"),
    ("attn.whats_new.last_seen", "profiles-2026-10"),
    ("attn.whats_new.banner_dismissed", "profiles-2026-10"),
    ("attn.garden.frame", "full"),
    ("attn.garden.fullscreenView", "board"),
    ("attn.update_banner.dismissed_version", "0.15.2"),
];

fn copy_tree(from: &Path, to: &Path) {
    fs::create_dir_all(to).unwrap();
    for entry in fs::read_dir(from).unwrap() {
        let entry = entry.unwrap();
        let target = to.join(entry.file_name());
        if entry.file_type().unwrap().is_dir() {
            copy_tree(&entry.path(), &target);
        } else {
            fs::copy(entry.path(), target).unwrap();
        }
    }
}

fn database(web_data: &Path) -> PathBuf {
    web_data.join(
        "WebsiteData/Default/salt-of-tauri-localhost/salt-of-tauri-localhost/LocalStorage/localstorage.sqlite3",
    )
}

#[test]
fn the_first_launch_imports_every_tauri_setting_once_and_leaves_the_store_untouched() {
    let dir = tempfile::tempdir().unwrap();
    let web_data = dir.path().join("WebKit/com.attn.manager.fixture");
    copy_tree(Path::new(FIXTURE), &web_data);
    let store_before = fs::read(database(&web_data)).unwrap();
    let prefs_file = dir.path().join("prefs.json");

    let mut prefs = Prefs::at(&prefs_file);
    assert_eq!(import(&web_data, &mut prefs), Ok(Import::Imported(12)));

    let reopened = Prefs::at(&prefs_file);
    assert_eq!(FIXTURE_VALUES.map(|(key, _)| key), TAURI_SETTINGS);
    for (key, value) in FIXTURE_VALUES {
        assert_eq!(reopened.get::<String>(key).as_deref(), Some(value), "{key}");
    }
    assert!(!reopened.contains("attn.grid.layout"));
    assert_eq!(fs::read(database(&web_data)).unwrap(), store_before);

    let mut prefs = Prefs::at(&prefs_file);
    prefs.set("attn.garden.frame", "dock").unwrap();
    assert_eq!(import(&web_data, &mut prefs), Ok(Import::AlreadyDone));
    assert_eq!(
        Prefs::at(&prefs_file)
            .get::<String>("attn.garden.frame")
            .as_deref(),
        Some("dock")
    );
}

#[test]
fn an_instance_the_tauri_app_never_ran_imports_nothing_and_is_not_asked_again() {
    let dir = tempfile::tempdir().unwrap();
    let web_data = dir.path().join("WebKit/com.attn.manager.never-tauri");
    let prefs_file = dir.path().join("prefs.json");

    assert_eq!(
        import(&web_data, &mut Prefs::at(&prefs_file)),
        Ok(Import::NoTauriStore)
    );
    copy_tree(Path::new(FIXTURE), &web_data);
    assert_eq!(
        import(&web_data, &mut Prefs::at(&prefs_file)),
        Ok(Import::AlreadyDone)
    );
}
