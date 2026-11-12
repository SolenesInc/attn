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
            host.shot(path, patience(request))?;
            return Ok(json!({"path": path}));
        }
        _ => {}
    }
    let wait = patience(request);
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
            Attempt::Done(value) => {
                handled(host, &request.action)?;
                return Ok(value);
            }
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

fn patience(request: &Request) -> Duration {
    request
        .payload
        .get("timeout_ms")
        .and_then(Value::as_u64)
        .map_or(WAIT, Duration::from_millis)
}

/// Returns once the host has handled the input the request queued: a host handles an
/// inspection's input before it runs the next one. The reply is then a receipt that the app's
/// handlers ran, and the commands they sent are on their way to the daemon.
fn handled<S: 'static>(host: &dyn Host<S>, action: &str) -> Result<(), String> {
    let (done, handled) = mpsc::channel();
    host.inspect(Box::new(move |_| {
        let _ = done.send(());
    }));
    handled
        .recv()
        .map_err(|_| format!("{action}: the app closed before it handled the request"))
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

#[cfg(test)]
mod tests {
    use std::sync::Mutex;
    use std::sync::atomic::{AtomicU32, Ordering};

    use rune::{Cx, El, Env, Runtime, Tx, el};

    use super::*;
    use crate::host::Inspection;

    struct Counter;

    impl Hooks<u32> for Counter {
        fn ready(&self, _: &u32) -> bool {
            true
        }

        fn run(&self, _: &mut u32, name: &str, _: &mut Tx) -> Result<(), String> {
            Err(format!("no action {name}"))
        }

        fn state(&self, _: &u32, of: &str) -> Result<Option<Value>, String> {
            Err(format!("no slice {of}"))
        }
    }

    /// A UI thread that handles an inspection's input only once the next inspection arrives,
    /// as late as a host may, and publishes the count the press handler keeps.
    struct LateHandler {
        work: Mutex<mpsc::Sender<Inspection<u32>>>,
        frames: attn_client::Pulse,
    }

    impl Host<u32> for LateHandler {
        fn inspect(&self, f: Inspection<u32>) {
            let _ = self.work.lock().unwrap().send(f);
        }

        fn frames(&self) -> &attn_client::Pulse {
            &self.frames
        }
    }

    fn view(_: &u32, _: &mut Cx<u32>) -> El {
        el().key("button")
            .w(40.0)
            .h(20.0)
            .on_press(|n: &mut u32| *n += 1)
    }

    #[test]
    fn a_press_replies_once_the_app_has_handled_it() {
        let (work, inbox) = mpsc::channel::<Inspection<u32>>();
        let presses = Arc::new(AtomicU32::new(0));
        let published = presses.clone();
        std::thread::spawn(move || {
            let mut rt = Runtime::new(0u32, view, (100.0, 100.0), Env::default());
            rt.frame(0.0);
            for f in inbox {
                while rt.has_pending_input() {
                    rt.frame(0.0);
                }
                published.store(rt.state, Ordering::SeqCst);
                f(&mut rt);
            }
        });
        let host = LateHandler {
            work: Mutex::new(work),
            frames: attn_client::Pulse::default(),
        };
        let hooks: Arc<dyn Hooks<u32>> = Arc::new(Counter);
        let press: Request = serde_json::from_value(
            json!({"action": "press", "payload": {"target": {"key": "button"}}}),
        )
        .unwrap();

        let receipt = answer(&host, &hooks, &press).unwrap();

        assert_eq!(receipt["matched"], true, "{receipt}");
        assert_eq!(
            presses.load(Ordering::SeqCst),
            1,
            "the press handler ran before the reply"
        );
    }
}
