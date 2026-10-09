use std::ffi::CString;
use std::fs;
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};

use serde::{Deserialize, Serialize};

use crate::blocks::AttachBlock;
use crate::ghostty::Theme;
use crate::quiesce::set_cloexec;
use crate::signals::ShellState;

pub const CAPABILITY: &str = "handover";
pub const ADOPT_FLAG: &str = "--adopt-handoff";
const VERSION: u8 = 1;

#[derive(Deserialize, Serialize)]
pub struct HostHandoff {
    pub version: u8,
    pub daemon_instance_id: String,
    pub generation: String,
    pub socket_path: String,
    pub registry_dir: String,
    pub host_registry_path: String,
    pub control_token: String,
    pub idle_timeout_ms: u64,
    pub listener_fd: i32,
    pub fallback: Option<Fallback>,
    pub sessions: Vec<SessionHandoff>,
}

#[derive(Clone, Deserialize, Serialize)]
pub struct Fallback {
    pub executable: String,
    pub generation: String,
}

#[derive(Deserialize, Serialize)]
#[allow(clippy::struct_excessive_bools)]
pub struct SessionHandoff {
    pub id: String,
    pub agent: String,
    pub cwd: String,
    pub child_pid: i32,
    pub attempt_index: usize,
    pub registry_path: String,
    pub cleanup_dir: String,
    pub master_fd: Option<i32>,
    pub screen_path: String,
    pub alternate_screen_path: Option<String>,
    pub removing: bool,
    pub seq: u32,
    pub cols: u16,
    pub rows: u16,
    pub cell_width: u16,
    pub cell_height: u16,
    pub pixel_width: u16,
    pub pixel_height: u16,
    pub theme: Theme,
    pub color_scheme_reports: bool,
    pub blocks: Vec<AttachBlock>,
    pub next_block_id: u64,
    pub program_status_reported: bool,
    pub shell: ShellState,
    pub running: bool,
    pub state: String,
    pub state_detail: String,
    pub state_source: String,
    pub exit_code: Option<i32>,
    pub exit_signal: Option<String>,
}

impl HostHandoff {
    pub fn new(
        daemon_instance_id: String,
        generation: String,
        socket_path: String,
        registry_dir: String,
        host_registry_path: String,
        control_token: String,
        idle_timeout_ms: u64,
    ) -> Self {
        Self {
            version: VERSION,
            daemon_instance_id,
            generation,
            socket_path,
            registry_dir,
            host_registry_path,
            control_token,
            idle_timeout_ms,
            listener_fd: -1,
            fallback: None,
            sessions: Vec::new(),
        }
    }

    pub fn read(path: &Path) -> Result<Self, String> {
        let raw =
            fs::read(path).map_err(|error| format!("read handoff {}: {error}", path.display()))?;
        let handoff: Self = serde_json::from_slice(&raw)
            .map_err(|error| format!("parse handoff {}: {error}", path.display()))?;
        if handoff.version != VERSION {
            return Err(format!(
                "handoff {} is version {}, this host reads version {VERSION}",
                path.display(),
                handoff.version
            ));
        }
        Ok(handoff)
    }

    pub fn write(&self, path: &Path) -> Result<(), String> {
        let raw = serde_json::to_vec(self).map_err(|error| format!("encode handoff: {error}"))?;
        write_private(path, &raw)
    }

    pub fn remove(&self, path: &Path) {
        for session in &self.sessions {
            let _ = fs::remove_file(&session.screen_path);
            if let Some(alternate) = &session.alternate_screen_path {
                let _ = fs::remove_file(alternate);
            }
        }
        let _ = fs::remove_file(path);
    }
}

pub fn handoff_dir(host_registry_path: &str) -> PathBuf {
    let registry = Path::new(host_registry_path);
    registry
        .parent()
        .and_then(Path::parent)
        .unwrap_or_else(|| Path::new("."))
        .join("handoff")
}

pub fn write_private(path: &Path, data: &[u8]) -> Result<(), String> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)
            .map_err(|error| format!("create {}: {error}", parent.display()))?;
    }
    let temporary = path.with_extension("tmp");
    fs::OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .mode(0o600)
        .open(&temporary)
        .and_then(|mut file| std::io::Write::write_all(&mut file, data))
        .and_then(|()| fs::rename(&temporary, path))
        .map_err(|error| format!("write {}: {error}", path.display()))
}

static FALLBACK_ARMED: AtomicBool = AtomicBool::new(false);

pub fn arm_fallback(path: PathBuf) {
    FALLBACK_ARMED.store(true, Ordering::SeqCst);
    std::panic::set_hook(Box::new(move |info| {
        eprintln!(
            "attn-pty-host: adopting {} panicked: {info}",
            path.display()
        );
        if FALLBACK_ARMED.swap(false, Ordering::SeqCst) {
            fall_back(&path);
        }
    }));
}

pub fn disarm_fallback() {
    if FALLBACK_ARMED.swap(false, Ordering::SeqCst) {
        drop(std::panic::take_hook());
    }
}

pub fn fallback_armed() -> bool {
    FALLBACK_ARMED.load(Ordering::SeqCst)
}

pub fn fall_back(path: &Path) -> ! {
    let error = match hand_back(path) {
        Ok(fallback) => {
            eprintln!(
                "attn-pty-host: handing the terminals back to {}",
                fallback.executable
            );
            exec(&fallback.executable, path)
        }
        Err(error) => error,
    };
    eprintln!(
        "attn-pty-host: the terminals in {} are lost: {error}",
        path.display()
    );
    std::process::exit(1);
}

fn hand_back(path: &Path) -> Result<Fallback, String> {
    let raw = fs::read(path).map_err(|error| format!("read handoff: {error}"))?;
    let mut handoff: serde_json::Map<String, serde_json::Value> =
        serde_json::from_slice(&raw).map_err(|error| format!("parse handoff: {error}"))?;
    let fallback = handoff
        .remove("fallback")
        .filter(|value| !value.is_null())
        .ok_or_else(|| "the handoff names no build to fall back to".to_owned())?;
    let fallback: Fallback = serde_json::from_value(fallback)
        .map_err(|error| format!("parse the fallback build: {error}"))?;
    for fd in inherited_fds(&handoff) {
        set_cloexec(fd, false)?;
    }
    handoff.insert(
        "generation".to_owned(),
        serde_json::Value::String(fallback.generation.clone()),
    );
    let raw = serde_json::to_vec(&handoff).map_err(|error| format!("encode handoff: {error}"))?;
    write_private(path, &raw)?;
    Ok(fallback)
}

fn inherited_fds(handoff: &serde_json::Map<String, serde_json::Value>) -> Vec<i32> {
    let sessions = handoff
        .get("sessions")
        .and_then(serde_json::Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(|session| session.get("master_fd"));
    std::iter::once(handoff.get("listener_fd"))
        .flatten()
        .chain(sessions)
        .filter_map(serde_json::Value::as_i64)
        .filter_map(|fd| i32::try_from(fd).ok())
        .collect()
}

pub fn exec(executable: &str, handoff_path: &Path) -> String {
    let argv = [executable, ADOPT_FLAG, &handoff_path.to_string_lossy()]
        .into_iter()
        .map(CString::new)
        .collect::<Result<Vec<_>, _>>();
    let argv = match argv {
        Ok(argv) => argv,
        Err(error) => return format!("handover arguments: {error}"),
    };
    let mut pointers = argv.iter().map(|arg| arg.as_ptr()).collect::<Vec<_>>();
    pointers.push(std::ptr::null());
    unsafe { libc::execv(pointers[0], pointers.as_ptr()) };
    format!("exec {executable}: {}", std::io::Error::last_os_error())
}

pub fn check_executable(path: &str) -> Result<(), String> {
    let metadata =
        fs::metadata(path).map_err(|error| format!("handover executable {path}: {error}"))?;
    if !metadata.is_file() || metadata.permissions().mode() & 0o111 == 0 {
        return Err(format!(
            "handover executable {path} is not an executable file"
        ));
    }
    Ok(())
}
