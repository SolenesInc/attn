//! Build-profile awareness for the Tauri shell. See docs/profiles.md.

use std::env;
use std::fs::{self, File, OpenOptions};
use std::io::Write;
use std::os::fd::AsRawFd;
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::thread;
use std::time::{Duration, Instant};

const BUILD_PROFILE: Option<&str> = option_env!("ATTN_BUILD_PROFILE");

const BUILD_WS_PORT: Option<&str> = option_env!("ATTN_BUILD_WS_PORT");
const BUILD_BUNDLE_ID: Option<&str> = option_env!("ATTN_BUILD_BUNDLE_ID");
const BUILD_DEFAULT_PROFILE_HARNESS: bool =
    option_env!("ATTN_BUILD_DEFAULT_PROFILE_HARNESS").is_some();
const HARNESS_DATA_DIR_ENV: &str = "ATTN_HARNESS_DATA_DIR";
const APP_PID_FILE: &str = "app.pid";
const APP_LOCK_DIR: &str = ".attn.locks";
// Tripwire past a whole `profile clean` (measured 0.44s, 2026-08-30, macOS): a launch
// that close to one waits it out instead of refusing to start.
const APP_LOCK_WAIT: Duration = Duration::from_secs(3);
const ROUTING_PATH_OVERRIDES: [&str; 7] = [
    "ATTN_SOCKET_PATH",
    "ATTN_DB_PATH",
    "ATTN_CONFIG_PATH",
    "ATTN_DATA_DIR",
    "ATTN_PLUGIN_DIR",
    "ATTN_DAEMON_BINARY",
    "ATTN_BUNDLED_PLUGIN_DIR",
];

pub fn build_profile() -> &'static str {
    BUILD_PROFILE.unwrap_or("").trim()
}

pub fn build_profile_label() -> &'static str {
    let p = build_profile();
    if p.is_empty() {
        "default"
    } else {
        p
    }
}

/// Mirrors `config.WSPortForProfile()` in Go.
pub fn default_port_for_build_profile() -> &'static str {
    if let Some(port) = BUILD_WS_PORT {
        let port = port.trim();
        if !port.is_empty() {
            return port;
        }
    }
    match build_profile() {
        "dev" => "29849",
        _ => "9849",
    }
}

/// Applies the build-time profile and scrubs routing overrides leaked in by a
/// parent attn terminal. Must run before anything reads `ATTN_PROFILE`/`ATTN_WS_PORT`.
pub fn apply_build_profile_env() {
    let profile = build_profile();
    for key in ROUTING_PATH_OVERRIDES {
        env::remove_var(key);
    }
    if profile.is_empty() {
        env::remove_var("ATTN_PROFILE");
    } else {
        env::set_var("ATTN_PROFILE", profile);
    }
    if BUILD_DEFAULT_PROFILE_HARNESS {
        let data_dir = validated_harness_data_dir()
            .unwrap_or_else(|error| panic!("refusing default-profile harness launch: {error}"));
        env::set_var("ATTN_DATA_DIR", data_dir);
    }
    env::set_var("ATTN_WS_PORT", default_port_for_build_profile());
}

fn validated_harness_data_dir() -> Result<PathBuf, String> {
    let raw = env::var(HARNESS_DATA_DIR_ENV)
        .map_err(|_| format!("{HARNESS_DATA_DIR_ENV} is required"))?;
    let requested = PathBuf::from(raw.trim());
    if !requested.is_absolute() {
        return Err(format!("{HARNESS_DATA_DIR_ENV} must be absolute"));
    }
    let metadata = fs::symlink_metadata(&requested)
        .map_err(|error| format!("inspect {}: {error}", requested.display()))?;
    if !metadata.is_dir() || metadata.file_type().is_symlink() {
        return Err(format!(
            "{} must be a direct directory",
            requested.display()
        ));
    }
    if metadata.permissions().mode() & 0o077 != 0 {
        return Err(format!("{} must be owner-only", requested.display()));
    }
    let resolved = fs::canonicalize(&requested)
        .map_err(|error| format!("resolve {}: {error}", requested.display()))?;
    let home = dirs::home_dir().ok_or_else(|| "home directory is unavailable".to_string())?;
    let production = fs::canonicalize(home.join(".attn")).unwrap_or_else(|_| home.join(".attn"));
    if resolved == production || resolved.starts_with(&production) {
        return Err(format!(
            "{} resolves inside production {}",
            requested.display(),
            production.display()
        ));
    }
    Ok(resolved)
}

pub(crate) fn data_dir() -> Result<PathBuf, String> {
    if let Ok(raw) = env::var("ATTN_DATA_DIR") {
        let trimmed = raw.trim();
        if !trimmed.is_empty() {
            return Ok(PathBuf::from(trimmed));
        }
    }
    let home = dirs::home_dir().ok_or_else(|| "home directory is unavailable".to_string())?;
    let name = match build_profile() {
        "" => ".attn".to_string(),
        profile => format!(".attn-{profile}"),
    };
    Ok(home.join(name))
}

/// Mirrors `config.AppLockPathForProfile()` in Go.
fn app_lock_path() -> Result<PathBuf, String> {
    let home = dirs::home_dir().ok_or_else(|| "home directory is unavailable".to_string())?;
    Ok(home
        .join(APP_LOCK_DIR)
        .join(format!("app-{}.lock", build_profile_label())))
}

/// Shared for this process's lifetime; `attn profile clean` wants it exclusively,
/// so it gives way while any app instance holds it.
pub fn hold_app_lock() -> Result<(), String> {
    let file = acquire_app_lock(&app_lock_path()?, APP_LOCK_WAIT)?;
    std::mem::forget(file);
    Ok(())
}

fn acquire_app_lock(path: &Path, wait: Duration) -> Result<File, String> {
    let dir = path
        .parent()
        .ok_or_else(|| format!("{} has no parent directory", path.display()))?;
    fs::create_dir_all(dir).map_err(|err| format!("create {}: {err}", dir.display()))?;
    let file = OpenOptions::new()
        .create(true)
        .read(true)
        .write(true)
        .truncate(false)
        .mode(0o600)
        .open(path)
        .map_err(|err| format!("open {}: {err}", path.display()))?;
    let deadline = Instant::now() + wait;
    loop {
        if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_SH | libc::LOCK_NB) } == 0 {
            return Ok(file);
        }
        if Instant::now() >= deadline {
            return Err(format!(
                "{} is still held exclusively after {}s: `attn profile clean` is removing this profile",
                path.display(),
                wait.as_secs_f64()
            ));
        }
        thread::sleep(Duration::from_millis(50));
    }
}

pub fn write_app_pid_file() {
    let Ok(dir) = data_dir() else { return };
    if fs::create_dir_all(&dir).is_err() {
        return;
    }
    let _ = fs::write(dir.join(APP_PID_FILE), std::process::id().to_string());
}

pub fn remove_app_pid_file() {
    let Ok(dir) = data_dir() else { return };
    let path = dir.join(APP_PID_FILE);
    match fs::read_to_string(&path) {
        Ok(raw) if raw.trim() == std::process::id().to_string() => {
            let _ = fs::remove_file(&path);
        }
        _ => {}
    }
}

pub fn read_client_token() -> Result<String, String> {
    if let Ok(token) = env::var("ATTN_CLIENT_TOKEN") {
        let token = token.trim().to_string();
        if !token.is_empty() {
            return Ok(token);
        }
    }
    let path = data_dir()?.join("client-token");
    Ok(fs::read_to_string(&path)
        .map(|token| token.trim().to_string())
        .unwrap_or_default())
}

pub fn ensure_browser_host_token() -> Result<String, String> {
    let dir = data_dir()?;
    let path = dir.join("browser-host-token");
    if let Ok(token) = fs::read_to_string(&path) {
        let token = token.trim().to_string();
        if token.len() >= 64 {
            return Ok(token);
        }
    }

    fs::create_dir_all(&dir).map_err(|error| format!("create attn data directory: {error}"))?;
    let mut random = [0_u8; 32];
    getrandom::getrandom(&mut random)
        .map_err(|error| format!("generate browser host token: {error}"))?;
    let token = random
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    let mut file = OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .mode(0o600)
        .open(&path)
        .map_err(|error| format!("open browser host token: {error}"))?;
    file.write_all(token.as_bytes())
        .map_err(|error| format!("write browser host token: {error}"))?;
    fs::set_permissions(&path, fs::Permissions::from_mode(0o600))
        .map_err(|error| format!("secure browser host token: {error}"))?;
    Ok(token)
}

pub fn bundle_identifier() -> &'static str {
    if let Some(id) = BUILD_BUNDLE_ID {
        let id = id.trim();
        if !id.is_empty() {
            return id;
        }
    }
    match build_profile() {
        "dev" => "com.attn.manager.dev",
        _ => "com.attn.manager",
    }
}

/// Whether the UI automation bridge runs: `ATTN_AUTOMATION=1`/`0` decides,
/// otherwise any non-empty `ATTN_PROFILE`. Needs `apply_build_profile_env` first.
pub fn automation_enabled() -> bool {
    let automation = env::var("ATTN_AUTOMATION").ok();
    let profile = env::var("ATTN_PROFILE").ok();
    decide_automation_enabled(automation.as_deref(), profile.as_deref())
}

fn decide_automation_enabled(automation: Option<&str>, profile: Option<&str>) -> bool {
    match automation.map(str::trim) {
        Some("1") => return true,
        Some("0") => return false,
        Some("") | None => {}
        Some(_) => return false,
    }
    profile.map(str::trim).is_some_and(|p| !p.is_empty())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn every_path_that_can_redirect_a_bundle_is_scrubbed() {
        for key in ROUTING_PATH_OVERRIDES {
            env::set_var(key, format!("foreign-{key}"));
        }

        apply_build_profile_env();

        for key in ROUTING_PATH_OVERRIDES {
            assert_eq!(env::var_os(key), None, "{key} survived app startup");
        }
    }

    #[test]
    fn unbaked_build_falls_back_to_prod_resources() {
        // Unbaked builds must fall back to the SAFE prod values, never dev's
        // 29849 (the pre-PR4 unknown-profile collision bug).
        assert_eq!(build_profile(), "");
        assert_eq!(default_port_for_build_profile(), "9849");
        assert_eq!(bundle_identifier(), "com.attn.manager");
    }

    fn hold_exclusive(path: &Path) -> File {
        let file = OpenOptions::new()
            .create(true)
            .read(true)
            .write(true)
            .truncate(false)
            .mode(0o600)
            .open(path)
            .expect("open lock");
        assert_eq!(
            unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) },
            0,
            "take the exclusive lock"
        );
        file
    }

    #[test]
    fn two_app_instances_hold_the_lock_together() {
        // #51's no-bus fallback launches a second app instance to deliver a deep link.
        let dir = env::temp_dir().join(format!("attn-lock-shared-{}", std::process::id()));
        fs::create_dir_all(&dir).expect("temp dir");
        let path = dir.join("app-test.lock");

        let first = acquire_app_lock(&path, Duration::from_millis(50)).expect("first instance");
        let second = acquire_app_lock(&path, Duration::from_millis(50))
            .expect("a second app instance must hold the lock alongside the first");

        drop(first);
        drop(second);
        fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn an_exclusive_holder_stops_startup() {
        let dir = env::temp_dir().join(format!("attn-lock-held-{}", std::process::id()));
        fs::create_dir_all(&dir).expect("temp dir");
        let path = dir.join("app-test.lock");
        let held = hold_exclusive(&path);

        let err = acquire_app_lock(&path, Duration::from_millis(50))
            .expect_err("an exclusive holder must refuse startup");
        assert!(err.contains(&path.display().to_string()), "{err}");
        assert!(err.contains("profile clean"), "{err}");

        drop(held);
        acquire_app_lock(&path, Duration::from_millis(50))
            .expect("startup resumes once the exclusive holder is gone");
        fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn a_lock_path_that_cannot_be_created_stops_startup() {
        let blocker = env::temp_dir().join(format!("attn-lock-blocker-{}", std::process::id()));
        fs::write(&blocker, b"not a directory").expect("blocker file");

        let err = acquire_app_lock(&blocker.join("app-test.lock"), Duration::from_millis(50))
            .expect_err("an unusable lock path must refuse startup");
        assert!(err.starts_with("create "), "{err}");
        fs::remove_file(&blocker).ok();
    }

    #[test]
    fn the_app_lock_mirrors_config_app_lock_path_for_profile() {
        let home = dirs::home_dir().expect("home dir");
        assert_eq!(
            app_lock_path().expect("lock path"),
            home.join(".attn.locks").join("app-default.lock")
        );
    }

    #[test]
    fn automation_decision_rules() {
        assert!(decide_automation_enabled(Some("1"), None));
        assert!(decide_automation_enabled(Some("1"), Some("dev")));
        assert!(decide_automation_enabled(Some("1"), Some("")));
        assert!(!decide_automation_enabled(Some("0"), None));
        assert!(!decide_automation_enabled(Some("0"), Some("dev")));
        assert!(!decide_automation_enabled(Some("0"), Some("ticketqa")));
        assert!(!decide_automation_enabled(Some("yes"), Some("dev")));
        assert!(decide_automation_enabled(None, Some("dev")));
        assert!(decide_automation_enabled(None, Some("DEV")));
        assert!(decide_automation_enabled(None, Some("ticketqa")));
        assert!(decide_automation_enabled(None, Some("agent7")));
        assert!(decide_automation_enabled(None, Some("ci")));
        assert!(!decide_automation_enabled(None, None));
        assert!(!decide_automation_enabled(None, Some("")));
        assert!(!decide_automation_enabled(None, Some("  ")));
        assert!(decide_automation_enabled(Some(""), Some("dev")));
        assert!(decide_automation_enabled(Some("  "), Some("ticketqa")));
        assert!(!decide_automation_enabled(Some("  "), None));
        assert!(!decide_automation_enabled(Some(""), Some("")));
    }

    #[test]
    fn harness_data_dir_requires_a_direct_owner_only_directory() {
        let root = env::temp_dir().join(format!(
            "attn-default-profile-harness-{}",
            std::process::id()
        ));
        let direct = root.join("direct");
        let link = root.join("link");
        let _ = fs::remove_dir_all(&root);
        fs::create_dir_all(&direct).expect("direct dir");
        fs::set_permissions(&direct, fs::Permissions::from_mode(0o700)).expect("permissions");
        std::os::unix::fs::symlink(&direct, &link).expect("symlink");

        let previous = env::var_os(HARNESS_DATA_DIR_ENV);
        env::set_var(HARNESS_DATA_DIR_ENV, &direct);
        assert_eq!(
            validated_harness_data_dir().expect("direct harness root"),
            fs::canonicalize(&direct).unwrap()
        );
        env::set_var(HARNESS_DATA_DIR_ENV, &link);
        assert!(validated_harness_data_dir()
            .unwrap_err()
            .contains("direct directory"));

        if let Some(value) = previous {
            env::set_var(HARNESS_DATA_DIR_ENV, value);
        } else {
            env::remove_var(HARNESS_DATA_DIR_ENV);
        }
        fs::remove_dir_all(&root).expect("cleanup");
    }
}
