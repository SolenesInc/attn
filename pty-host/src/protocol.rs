use serde::{Deserialize, Serialize};
use serde_json::{Value, json};

use crate::ghostty::{KittyPlacement, Theme};

pub const RPC_MAJOR: u16 = 1;
pub const RPC_MINOR: u16 = 1;

// Methods are additive within a major. Old hosts accept newer daemons so they
// can keep serving their existing sessions after an app update.
pub fn is_compatible_version(peer_major: u16, _peer_minor: u16) -> bool {
    peer_major == RPC_MAJOR
}

pub const ERR_BAD_REQUEST: &str = "bad_request";
pub const ERR_UNSUPPORTED_VERSION: &str = "unsupported_version";
pub const ERR_UNAUTHORIZED: &str = "unauthorized";
pub const ERR_SESSION_NOT_FOUND: &str = "session_not_found";
pub const ERR_SESSION_NOT_RUNNING: &str = "session_not_running";
pub const ERR_IO: &str = "io_error";
pub const ERR_IMAGE_NOT_FOUND: &str = "image_not_found";

#[derive(Deserialize)]
pub struct Request {
    #[serde(rename = "type")]
    pub kind: String,
    pub id: String,
    pub method: String,
    #[serde(default)]
    pub session_id: String,
    #[serde(default)]
    pub params: Value,
}

#[derive(Deserialize)]
pub struct HelloParams {
    pub rpc_major: u16,
    pub rpc_minor: u16,
    pub daemon_instance_id: String,
    pub control_token: String,
    #[serde(default)]
    pub session_id: String,
    #[serde(default)]
    pub snapshot_format: String,
}

#[derive(Clone, Deserialize, Serialize)]
pub struct PreparedLaunchAttempt {
    pub executable: String,
    #[serde(default)]
    pub args: Vec<String>,
    #[serde(default)]
    pub env: Vec<String>,
    pub cwd: String,
    #[serde(default)]
    pub cleanup_dir: String,
    #[serde(default)]
    pub shell_path: String,
}

#[derive(Clone, Deserialize)]
pub struct SpawnParams {
    pub session_id: String,
    pub agent: String,
    pub cwd: String,
    #[serde(default)]
    #[serde(rename = "label")]
    pub _label: String,
    #[serde(default)]
    #[serde(rename = "lifecycle_id")]
    pub _lifecycle_id: String,
    pub cols: u16,
    pub rows: u16,
    #[serde(default)]
    pub theme: Theme,
    pub attempts: Vec<PreparedLaunchAttempt>,

    #[serde(default)]
    pub yolo_mode: bool,
    #[serde(default)]
    pub approval_route: String,
    #[serde(default)]
    pub executable: String,
    #[serde(default)]
    pub claude_executable: String,
    #[serde(default)]
    pub codex_executable: String,
    #[serde(default)]
    pub copilot_executable: String,
    #[serde(default)]
    pub model: String,
    #[serde(default)]
    pub effort: String,
    #[serde(default)]
    pub unattended_launch: Value,
}

#[derive(Deserialize)]
pub struct AttachParams {
    #[serde(default)]
    pub subscriber_id: String,
    #[serde(default)]
    pub omit_replay: bool,
}

#[derive(Deserialize)]
pub struct InputParams {
    pub data: String,
}

#[derive(Deserialize)]
pub struct ResizeParams {
    pub cols: u16,
    pub rows: u16,
    #[serde(default)]
    pub xpixel: u16,
    #[serde(default)]
    pub ypixel: u16,
}

#[derive(Deserialize)]
pub struct SignalParams {
    pub signal: String,
}

#[derive(Deserialize)]
pub struct KittyImageParams {
    pub image_id: u32,
}

#[allow(clippy::needless_pass_by_value)]
pub fn response(id: &str, result: Value) -> Value {
    json!({"type": "res", "id": id, "ok": true, "result": result})
}

pub fn error(id: &str, code: &str, message: impl Into<String>) -> Value {
    json!({
        "type": "res",
        "id": id,
        "ok": false,
        "error": {"code": code, "message": message.into()}
    })
}

pub fn output_event(session_id: &str, seq: u32, data: &str) -> Value {
    json!({
        "type": "evt",
        "event": "output",
        "session_id": session_id,
        "seq": seq,
        "data": data
    })
}

pub fn desync_event(session_id: &str, reason: &str) -> Value {
    json!({
        "type": "evt",
        "event": "desync",
        "session_id": session_id,
        "reason": reason
    })
}

pub fn kitty_placements_event(session_id: &str, seq: u32, placements: &[KittyPlacement]) -> Value {
    json!({
        "type": "evt",
        "event": "kitty_placements",
        "session_id": session_id,
        "seq": seq,
        "placements": placements
    })
}

pub fn state_event(session_id: &str, state: &str, detail: &str, source: &str) -> Value {
    json!({
        "type": "evt",
        "event": "state_changed",
        "session_id": session_id,
        "state": state,
        "state_source": source,
        "state_detail": detail
    })
}

pub fn exit_event(session_id: &str, exit_code: i32, exit_signal: Option<&str>) -> Value {
    let mut event = json!({
        "type": "evt",
        "event": "exit",
        "session_id": session_id,
        "exit_code": exit_code
    });
    if let Some(signal) = exit_signal {
        event["exit_signal"] = Value::String(signal.to_owned());
    }
    event
}

#[cfg(test)]
mod tests {
    use super::{RPC_MAJOR, RPC_MINOR, is_compatible_version};

    #[test]
    fn accepts_newer_minor_versions_within_the_same_major() {
        assert!(is_compatible_version(RPC_MAJOR, RPC_MINOR + 100));
        assert!(!is_compatible_version(RPC_MAJOR + 1, RPC_MINOR));
    }
}
