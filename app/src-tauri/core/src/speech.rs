use std::collections::HashMap;
use std::fs::{self, File};
use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};
use std::process::{Child, ChildStdin, ChildStdout, Command, Stdio};
use std::sync::mpsc::Sender;
use std::sync::{Mutex, MutexGuard};
use std::thread;

use serde::Serialize;
use serde_json::{json, Value};
use tauri::{AppHandle, Emitter, Manager, Runtime};

pub const STATUS_EVENT: &str = "speech-status";
const HELPER: &str = "attn-speech";

#[derive(Clone, Default, Serialize)]
#[serde(tag = "phase", rename_all = "camelCase")]
pub enum Phase {
    #[default]
    Off,
    Starting,
    Downloading {
        artifact: String,
        bytes: u64,
        total: u64,
    },
    Verifying {
        artifact: String,
    },
    Loading,
    Ready,
    Failed {
        message: String,
    },
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Status {
    #[serde(flatten)]
    phase: Phase,
    cache_bytes: u64,
}

#[derive(Default)]
pub struct Speech(Mutex<State>);

#[derive(Default)]
struct State {
    helper: Option<Helper>,
    phase: Phase,
    generation: u64,
}

struct Helper {
    child: Child,
    stdin: ChildStdin,
    generation: u64,
    next_id: u64,
    prepare: Option<u64>,
    answers: HashMap<u64, Sender<Value>>,
}

impl Helper {
    fn send(&mut self, request: Value) -> Result<u64, String> {
        self.next_id += 1;
        let mut request = request;
        request["id"] = json!(self.next_id);
        self.write(format!("{request}\n").as_bytes(), &[])?;
        Ok(self.next_id)
    }

    fn write(&mut self, header: &[u8], body: &[u8]) -> Result<(), String> {
        self.stdin
            .write_all(header)
            .and_then(|()| self.stdin.write_all(body))
            .and_then(|()| self.stdin.flush())
            .map_err(|error| format!("could not reach the speech helper: {error}"))
    }

    fn stop(self) {
        let Helper {
            mut child, stdin, ..
        } = self;
        drop(stdin);
        let _ = child.wait();
    }
}

fn cache_root() -> Result<PathBuf, String> {
    let data_dir = crate::instance::data_dir()?;
    let home = dirs::home_dir().ok_or_else(|| "home directory is unavailable".to_string())?;
    if data_dir != home.join(".attn") {
        return Ok(data_dir.join("speech-models"));
    }
    dirs::cache_dir()
        .map(|dir| dir.join("attn").join("speech-models"))
        .ok_or_else(|| "the user cache directory is unavailable".to_string())
}

fn helper_path() -> Result<PathBuf, String> {
    let exe = std::env::current_exe().map_err(|error| format!("could not locate attn: {error}"))?;
    Ok(exe.with_file_name(HELPER))
}

fn directory_bytes(path: &Path) -> u64 {
    let Ok(entries) = fs::read_dir(path) else {
        return 0;
    };
    entries
        .flatten()
        .map(|entry| match entry.file_type() {
            Ok(kind) if kind.is_dir() => directory_bytes(&entry.path()),
            Ok(_) => entry.metadata().map_or(0, |meta| meta.len()),
            Err(_) => 0,
        })
        .sum()
}

impl Speech {
    fn lock(&self) -> MutexGuard<'_, State> {
        self.0
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
    }
}

fn status_of(state: &State) -> Status {
    Status {
        phase: state.phase.clone(),
        cache_bytes: cache_root().map_or(0, |root| directory_bytes(&root)),
    }
}

fn publish<R: Runtime>(app: &AppHandle<R>, state: &State) -> Status {
    let status = status_of(state);
    let _ = app.emit(STATUS_EVENT, status.clone());
    status
}

fn spawn<R: Runtime>(app: &AppHandle<R>, state: &mut State) -> Result<(), String> {
    let path = helper_path()?;
    let cache = cache_root()?;
    let log = crate::instance::data_dir()
        .and_then(|dir| {
            fs::create_dir_all(&dir).map_err(|error| error.to_string())?;
            File::create(dir.join("speech-helper.log")).map_err(|error| error.to_string())
        })
        .map_or_else(|_| Stdio::null(), Stdio::from);
    let mut child = Command::new(&path)
        .arg("serve")
        .arg("--cache")
        .arg(&cache)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(log)
        .spawn()
        .map_err(|error| format!("could not start {}: {error}", path.display()))?;
    let stdin = child.stdin.take().expect("helper stdin is piped");
    let stdout = child.stdout.take().expect("helper stdout is piped");
    state.generation += 1;
    let generation = state.generation;
    state.helper = Some(Helper {
        child,
        stdin,
        generation,
        next_id: 0,
        prepare: None,
        answers: HashMap::new(),
    });
    let app = app.clone();
    thread::spawn(move || read_events(&app, stdout, generation));
    Ok(())
}

fn read_events<R: Runtime>(app: &AppHandle<R>, stdout: ChildStdout, generation: u64) {
    let speech = app.state::<Speech>();
    for line in BufReader::new(stdout).lines() {
        let Ok(line) = line else { break };
        let Ok(event) = serde_json::from_str::<Value>(&line) else {
            continue;
        };
        let mut state = speech.lock();
        let Some(helper) = state
            .helper
            .as_mut()
            .filter(|helper| helper.generation == generation)
        else {
            return;
        };
        let id = event["id"].as_u64();
        if let Some(answer) = id.and_then(|id| helper.answers.get(&id)) {
            if answer.send(event.clone()).is_err() {
                helper.answers.remove(&id.expect("answered id"));
            }
            continue;
        }
        if helper.prepare.is_none() || id != helper.prepare {
            continue;
        }
        if let Some(phase) = phase_after(&event) {
            state.phase = phase;
            publish(app, &state);
        }
    }
    let mut state = speech.lock();
    let Some(helper) = state
        .helper
        .take_if(|helper| helper.generation == generation)
    else {
        return;
    };
    let mut child = helper.child;
    drop(helper.stdin);
    let exit = child
        .wait()
        .map_or_else(|error| error.to_string(), |status| status.to_string());
    state.phase = Phase::Failed {
        message: format!(
            "The speech helper stopped unexpectedly ({exit}). Retry to start it again."
        ),
    };
    publish(app, &state);
    drop(state);
    #[cfg(target_os = "macos")]
    crate::dictation::release(app, "The speech helper stopped while recording.");
}

fn phase_after(event: &Value) -> Option<Phase> {
    let text = |key: &str| event[key].as_str().unwrap_or_default().to_string();
    match (event["event"].as_str()?, event["stage"].as_str()) {
        ("progress", Some("downloading")) => Some(Phase::Downloading {
            artifact: text("artifact"),
            bytes: event["bytes"].as_u64().unwrap_or(0),
            total: event["total"].as_u64().unwrap_or(0),
        }),
        ("progress", Some("verifying")) => Some(Phase::Verifying {
            artifact: text("artifact"),
        }),
        ("progress", Some("loading")) => Some(Phase::Loading),
        ("ready", _) => Some(Phase::Ready),
        ("error", _) => Some(Phase::Failed {
            message: text("message"),
        }),
        _ => None,
    }
}

fn release(state: &mut State) {
    if let Some(helper) = state.helper.take() {
        helper.stop();
    }
    state.phase = Phase::Off;
}

#[tauri::command]
pub async fn speech_enable<R: Runtime>(app: AppHandle<R>) -> Status {
    let speech = app.state::<Speech>();
    let mut state = speech.lock();
    if matches!(state.phase, Phase::Ready) && state.helper.is_some() {
        return status_of(&state);
    }
    let started = match state.helper.as_mut() {
        Some(_) => Ok(()),
        None => spawn(&app, &mut state),
    };
    state.phase = match started.and_then(|()| {
        let helper = state.helper.as_mut().expect("a running helper");
        helper.prepare = Some(helper.send(json!({"op": "prepare"}))?);
        Ok(())
    }) {
        Ok(()) => Phase::Starting,
        Err(message) => {
            release(&mut state);
            Phase::Failed { message }
        }
    };
    publish(&app, &state)
}

#[tauri::command]
pub async fn speech_disable<R: Runtime>(app: AppHandle<R>) -> Status {
    #[cfg(target_os = "macos")]
    crate::dictation::release(&app, "Dictation turned off while recording.");
    let speech = app.state::<Speech>();
    let mut state = speech.lock();
    release(&mut state);
    publish(&app, &state)
}

#[tauri::command]
pub async fn speech_status<R: Runtime>(app: AppHandle<R>) -> Status {
    status_of(&app.state::<Speech>().lock())
}

#[tauri::command]
pub async fn speech_remove<R: Runtime>(app: AppHandle<R>) -> Result<Status, String> {
    let speech = app.state::<Speech>();
    let state = speech.lock();
    if state.helper.is_some() {
        return Err("Turn dictation off before removing the speech model.".into());
    }
    let root = cache_root()?;
    match fs::remove_dir_all(&root) {
        Err(error) if error.kind() != std::io::ErrorKind::NotFound => {
            return Err(format!("could not remove {}: {error}", root.display()));
        }
        _ => {}
    }
    Ok(publish(&app, &state))
}

#[cfg(target_os = "macos")]
pub fn ready<R: Runtime>(app: &AppHandle<R>) -> Result<(), String> {
    let speech = app.state::<Speech>();
    let state = speech.lock();
    match (&state.phase, state.helper.is_some()) {
        (Phase::Ready, true) => Ok(()),
        (Phase::Off, _) => Err("Dictation is off. Turn it on in Settings.".into()),
        (Phase::Failed { message }, _) => {
            Err(format!("The speech model is not available: {message}"))
        }
        _ => Err(
            "The speech model is still preparing. Try again when Settings shows it ready.".into(),
        ),
    }
}

#[cfg(target_os = "macos")]
pub fn request<R: Runtime>(
    app: &AppHandle<R>,
    request: Value,
) -> Result<(u64, std::sync::mpsc::Receiver<Value>), String> {
    let speech = app.state::<Speech>();
    let mut state = speech.lock();
    let helper = state
        .helper
        .as_mut()
        .ok_or_else(|| "The speech helper is not running.".to_string())?;
    let id = helper.send(request)?;
    let (answer, answers) = std::sync::mpsc::channel();
    helper.answers.insert(id, answer);
    Ok((id, answers))
}

#[cfg(target_os = "macos")]
pub fn notify<R: Runtime>(app: &AppHandle<R>, request: Value) -> Result<(), String> {
    let speech = app.state::<Speech>();
    let mut state = speech.lock();
    let helper = state
        .helper
        .as_mut()
        .ok_or_else(|| "The speech helper is not running.".to_string())?;
    helper.write(format!("{request}\n").as_bytes(), &[])
}

#[cfg(target_os = "macos")]
pub fn audio<R: Runtime>(app: &AppHandle<R>, target: u64, samples: &[f32]) -> Result<(), String> {
    let body: Vec<u8> = samples
        .iter()
        .flat_map(|sample| sample.to_le_bytes())
        .collect();
    let header = format!(
        "{}\n",
        json!({"op": "audio", "target": target, "bytes": body.len()})
    );
    let speech = app.state::<Speech>();
    let mut state = speech.lock();
    let helper = state
        .helper
        .as_mut()
        .ok_or_else(|| "The speech helper is not running.".to_string())?;
    helper.write(header.as_bytes(), &body)
}

pub fn shutdown<R: Runtime>(app: &AppHandle<R>) {
    release(&mut app.state::<Speech>().lock());
}
