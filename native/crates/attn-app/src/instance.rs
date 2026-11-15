use std::env;
use std::fs::{self, File, OpenOptions};
use std::io::{Read, Write};
use std::os::fd::AsRawFd;
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::sync::{OnceLock, mpsc};
use std::thread;
use std::time::Duration;

const BUILD_INSTANCE: Option<&str> = option_env!("ATTN_BUILD_INSTANCE");
const BUILD_WS_PORT: Option<&str> = option_env!("ATTN_BUILD_WS_PORT");
const BUILD_BUNDLE_ID: Option<&str> = option_env!("ATTN_BUILD_BUNDLE_ID");

pub const ROUTING_PATH_OVERRIDES: [&str; 6] = [
    "ATTN_SOCKET_PATH",
    "ATTN_DB_PATH",
    "ATTN_CONFIG_PATH",
    "ATTN_DATA_DIR",
    "ATTN_PLUGIN_DIR",
    "ATTN_DAEMON_BINARY",
];

// Tripwire past a whole `attn instance clean` (measured 0.44 s, 2026-08-30, macOS): a launch
// that close to one waits it out instead of refusing to start.
const APP_LOCK_WAIT: Duration = Duration::from_secs(3);
const HOST_TOKEN_HEX_LEN: usize = 64;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Instance {
    pub name: String,
    label: String,
    pub ws_port: u16,
    pub bundle_id: String,
    pub data_dir: PathBuf,
    pub app_lock: PathBuf,
    pub tauri_web_data: PathBuf,
}

impl Instance {
    /// The instance `make install` baked into this build. A build without one, such as a bare
    /// `cargo run`, refuses before it touches any instance.
    pub fn baked() -> Result<Instance, String> {
        let (Some(name), Some(port), Some(bundle_id)) =
            (BUILD_INSTANCE, BUILD_WS_PORT, BUILD_BUNDLE_ID)
        else {
            return Err("this build carries no instance; build it with `make install INSTANCE=<name> APP=native`".into());
        };
        let home = env::home_dir().ok_or("the home directory is unavailable")?;
        Instance::resolve(name.trim(), port.trim(), bundle_id.trim(), &home)
    }

    fn resolve(name: &str, port: &str, bundle_id: &str, home: &Path) -> Result<Instance, String> {
        let ws_port = port
            .parse()
            .map_err(|_| format!("the baked ATTN_BUILD_WS_PORT {port:?} is not a port"))?;
        let data_dir = match name {
            "" => home.join(".attn"),
            name => home.join(format!(".attn-{name}")),
        };
        let label = if name.is_empty() { "default" } else { name };
        Ok(Instance {
            label: label.to_string(),
            name: name.to_string(),
            ws_port,
            bundle_id: bundle_id.to_string(),
            data_dir,
            app_lock: home.join(".attn.locks").join(format!("app-{label}.lock")),
            tauri_web_data: home.join("Library").join("WebKit").join(bundle_id),
        })
    }

    pub fn label(&self) -> &str {
        &self.label
    }

    /// Routes this process and everything it starts to the baked instance, whatever routing a
    /// parent attn terminal leaked in. Call before any thread starts.
    pub fn route_environment(&self) {
        // SAFETY: called from `main` before the app starts any thread.
        unsafe {
            for key in ROUTING_PATH_OVERRIDES {
                env::remove_var(key);
            }
            if self.name.is_empty() {
                env::remove_var("ATTN_INSTANCE");
            } else {
                env::set_var("ATTN_INSTANCE", &self.name);
            }
            env::set_var("ATTN_WS_PORT", self.ws_port.to_string());
        }
    }

    /// Held shared for the process's life; `attn instance clean` takes it exclusively, so a
    /// launch never starts inside an instance being removed.
    pub fn hold_app_lock(&self) -> Result<(), String> {
        let file = acquire_shared_lock(&self.app_lock, APP_LOCK_WAIT)?;
        std::mem::forget(file);
        Ok(())
    }

    /// Says which process is this instance's app, for `attn instance stop-app`, until
    /// [`remove_app_pid`] runs as the app quits.
    pub fn write_app_pid(&self) -> Result<(), String> {
        let path = self.data_dir.join("app.pid");
        fs::write(&path, std::process::id().to_string())
            .map_err(|error| format!("write {}: {error}", path.display()))?;
        let _ = APP_PID.set(path);
        Ok(())
    }

    /// The token every client of the daemon presents, made on first use as the daemon makes
    /// it, so the app holds it before the daemon has ever started.
    pub fn ensure_client_token(&self) -> Result<String, String> {
        fs::create_dir_all(&self.data_dir)
            .map_err(|error| format!("create {}: {error}", self.data_dir.display()))?;
        ensure_token(&self.data_dir.join("client-token"), 1)
    }
}

/// The app's own credential in `data_dir`, made on first use: the daemon trusts a `native-app`
/// hello that carries it.
pub fn ensure_host_token(data_dir: &Path) -> Result<String, String> {
    ensure_token(&data_dir.join("browser-host-token"), HOST_TOKEN_HEX_LEN)
}

fn ensure_token(path: &Path, shortest: usize) -> Result<String, String> {
    if let Ok(token) = fs::read_to_string(path) {
        let token = token.trim();
        if token.len() >= shortest {
            return Ok(token.to_string());
        }
    }
    let mut random = [0_u8; HOST_TOKEN_HEX_LEN / 2];
    File::open("/dev/urandom")
        .and_then(|mut source| source.read_exact(&mut random))
        .map_err(|error| format!("generate the host token: {error}"))?;
    let token: String = random.iter().map(|byte| format!("{byte:02x}")).collect();
    let mut file = OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .mode(0o600)
        .open(path)
        .map_err(|error| format!("open {}: {error}", path.display()))?;
    file.write_all(token.as_bytes())
        .map_err(|error| format!("write {}: {error}", path.display()))?;
    fs::set_permissions(path, fs::Permissions::from_mode(0o600))
        .map_err(|error| format!("secure {}: {error}", path.display()))?;
    Ok(token)
}

fn acquire_shared_lock(path: &Path, wait: Duration) -> Result<File, String> {
    let dir = path
        .parent()
        .ok_or_else(|| format!("{} has no parent directory", path.display()))?;
    fs::create_dir_all(dir).map_err(|error| format!("create {}: {error}", dir.display()))?;
    let file = OpenOptions::new()
        .create(true)
        .read(true)
        .write(true)
        .truncate(false)
        .mode(0o600)
        .open(path)
        .map_err(|error| format!("open {}: {error}", path.display()))?;
    let (locked, wait_for_lock) = mpsc::channel();
    thread::spawn(move || {
        // SAFETY: flock on the descriptor of a file this thread owns.
        let result = unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_SH) };
        let error = std::io::Error::last_os_error();
        let _ = locked.send(if result == 0 { Ok(file) } else { Err(error) });
    });
    match wait_for_lock.recv_timeout(wait) {
        Ok(Ok(file)) => Ok(file),
        Ok(Err(error)) => Err(format!("lock {}: {error}", path.display())),
        Err(_) => Err(format!(
            "{} is still held exclusively after {}s: `attn instance clean` is removing this instance",
            path.display(),
            wait.as_secs_f64()
        )),
    }
}

static APP_PID: OnceLock<PathBuf> = OnceLock::new();

pub fn remove_app_pid() {
    let Some(path) = APP_PID.get() else { return };
    if fs::read_to_string(path).is_ok_and(|pid| pid.trim() == std::process::id().to_string()) {
        let _ = fs::remove_file(path);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn an_instance_names_its_data_dir_and_lock_like_attn_instance_resolve() {
        let home = Path::new("/home/u");
        for (name, want_dir, want_lock) in [
            ("", "/home/u/.attn", "app-default.lock"),
            ("agent7", "/home/u/.attn-agent7", "app-agent7.lock"),
        ] {
            let instance = Instance::resolve(name, "31234", "com.attn.manager.x", home).unwrap();
            assert_eq!(instance.data_dir, Path::new(want_dir), "{name}");
            assert_eq!(
                instance.app_lock,
                home.join(".attn.locks").join(want_lock),
                "{name}"
            );
        }
    }

    #[test]
    fn an_exclusive_holder_refuses_the_launch_and_names_instance_clean() {
        let dir = env::temp_dir().join(format!("attn-app-lock-{}", std::process::id()));
        fs::create_dir_all(&dir).unwrap();
        let path = dir.join("app-test.lock");
        let held = OpenOptions::new()
            .create(true)
            .truncate(false)
            .write(true)
            .open(&path)
            .unwrap();
        assert_eq!(
            unsafe { libc::flock(held.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) },
            0
        );

        let refused = acquire_shared_lock(&path, Duration::from_millis(50)).unwrap_err();
        assert!(refused.contains("instance clean"), "{refused}");

        drop(held);
        let first = acquire_shared_lock(&path, APP_LOCK_WAIT).unwrap();
        let second = acquire_shared_lock(&path, APP_LOCK_WAIT).unwrap();
        drop((first, second));
        fs::remove_dir_all(&dir).ok();
    }
}
