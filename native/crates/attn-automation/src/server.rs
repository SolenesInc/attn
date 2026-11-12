use std::io::{BufRead, BufReader, Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::mpsc;
use std::time::Duration;

use attn_client::Clock;
use serde::Serialize;
use serde_json::{Value, json};

use crate::act::{Attempt, Hooks, Request, act};
use crate::host::Host;

/// How long a request waits for its element before it fails and says what it waited for.
/// Under the Node client's 35 s request timeout (`uiAutomationClient.mjs`), so the app's
/// answer, which names the element, arrives first.
const WAIT: Duration = Duration::from_secs(30);

/// What the scenario runner reads to find the server: the fields of the Tauri app's that the
/// runner reads, so both shells launch and connect the same way.
#[derive(Clone, Debug, Serialize)]
pub struct Manifest {
    pub enabled: bool,
    pub port: u16,
    pub token: String,
    pub pid: u32,
}

/// The automation server: newline-delimited JSON over loopback TCP, one request and its
/// reply at a time per connection, each carrying the manifest's token.
pub struct Server {
    pub manifest: Manifest,
}

impl Server {
    /// Serves `host` from a thread of its own and writes the manifest to `manifest_path`.
    pub fn start<S: 'static>(
        host: Arc<dyn Host<S>>,
        hooks: Arc<dyn Hooks<S>>,
        manifest_path: &Path,
    ) -> Result<Server, String> {
        let listener = TcpListener::bind("127.0.0.1:0")
            .map_err(|e| format!("bind the automation server: {e}"))?;
        let port = listener
            .local_addr()
            .map_err(|e| format!("read the automation server's port: {e}"))?
            .port();
        let manifest = Manifest {
            enabled: true,
            port,
            token: token()?,
            pid: std::process::id(),
        };
        write_manifest(manifest_path, &manifest)?;
        let token = manifest.token.clone();
        std::thread::Builder::new()
            .name("automation".into())
            .spawn(move || {
                for stream in listener.incoming().flatten() {
                    let (host, hooks, token) = (host.clone(), hooks.clone(), token.clone());
                    let _ = std::thread::Builder::new()
                        .name("automation-conn".into())
                        .spawn(move || serve(stream, &*host, &hooks, &token));
                }
            })
            .map_err(|e| format!("start the automation server: {e}"))?;
        Ok(Server { manifest })
    }
}

fn serve<S: 'static>(
    stream: TcpStream,
    host: &dyn Host<S>,
    hooks: &Arc<dyn Hooks<S>>,
    token: &str,
) {
    let Ok(mut writer) = stream.try_clone() else {
        return;
    };
    for line in BufReader::new(stream).lines() {
        let Ok(line) = line else { return };
        if line.trim().is_empty() {
            continue;
        }
        let reply = match serde_json::from_str::<Request>(&line) {
            Err(e) => json!({"id": "invalid", "ok": false, "error": format!("not a request: {e}")}),
            Ok(request) if request.token != token => {
                json!({"id": request.id, "ok": false, "error": "invalid token"})
            }
            Ok(request) => match answer(host, hooks, &request) {
                Ok(result) => json!({"id": request.id, "ok": true, "result": result}),
                Err(error) => json!({"id": request.id, "ok": false, "error": error}),
            },
        };
        let mut out = reply.to_string();
        out.push('\n');
        if writer.write_all(out.as_bytes()).is_err() {
            return;
        }
    }
}

/// Answers one request, retrying after each frame while it waits for the screen.
pub(crate) fn answer<S: 'static>(
    host: &dyn Host<S>,
    hooks: &Arc<dyn Hooks<S>>,
    request: &Request,
) -> Result<Value, String> {
    match request.action.as_str() {
        "native_windows" => {
            return Ok(json!({
                "pid": std::process::id(),
                "windows": host.window().map(|w| json!({"label": "main", "title": "attn", "id": w.id, "mapped": w.mapped})).into_iter().collect::<Vec<_>>(),
            }));
        }
        "shot" => {
            let path = request
                .payload
                .get("path")
                .and_then(Value::as_str)
                .ok_or("shot: give `path`")?;
            host.shot(path)?;
            return Ok(json!({"path": path}));
        }
        _ => {}
    }
    let wait = request
        .payload
        .get("timeout_ms")
        .and_then(Value::as_u64)
        .map_or(WAIT, Duration::from_millis);
    let clock = Clock::system();
    loop {
        let seen = host.frames().count();
        let (reply, attempt) = mpsc::channel();
        let (hooks, action, payload) = (
            hooks.clone(),
            request.action.clone(),
            request.payload.clone(),
        );
        host.inspect(Box::new(move |rt| {
            let _ = reply.send(act(rt, &*hooks, &action, &payload));
        }));
        let attempt = attempt
            .recv()
            .map_err(|_| format!("{}: the app closed before it answered", request.action))?;
        match attempt {
            Attempt::Done(value) => return Ok(value),
            Attempt::Fail(why) => return Err(why),
            Attempt::Wait(what) => {
                let left = wait.saturating_sub(clock.now());
                if left.is_zero() || !host.frames().wait_past(seen, left) {
                    return Err(format!(
                        "{}: waited {} ms (timeout_ms) for {what}",
                        request.action,
                        wait.as_millis()
                    ));
                }
            }
        }
    }
}

fn token() -> Result<String, String> {
    let mut random = [0_u8; 32];
    std::fs::File::open("/dev/urandom")
        .and_then(|mut source| source.read_exact(&mut random))
        .map_err(|e| format!("generate the automation token: {e}"))?;
    Ok(random.iter().map(|b| format!("{b:02x}")).collect())
}

fn write_manifest(path: &Path, manifest: &Manifest) -> Result<(), String> {
    let dir: PathBuf = path.parent().map(Path::to_path_buf).unwrap_or_default();
    std::fs::create_dir_all(&dir).map_err(|e| format!("create {}: {e}", dir.display()))?;
    let mut text = serde_json::to_string_pretty(manifest).expect("a manifest serializes");
    text.push('\n');
    let staged = path.with_extension("json.tmp");
    std::fs::write(&staged, text).map_err(|e| format!("write {}: {e}", staged.display()))?;
    std::fs::rename(&staged, path).map_err(|e| format!("write {}: {e}", path.display()))
}
