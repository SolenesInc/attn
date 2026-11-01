mod artifacts;
mod audio;
mod engine;

use std::collections::HashMap;
use std::io::{self, BufRead, Read, Write};
use std::path::PathBuf;
use std::process::ExitCode;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc;
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::Instant;

use serde::Deserialize;
use serde_json::{Value, json};
use transcribe_cpp::CancelToken;

use artifacts::{Cache, PARAKEET_UNIFIED_ENGLISH, REQUIRED, SILERO_VAD};
use engine::{Engine, Recording};

const PROTOCOL: u32 = 2;
const USAGE: &str = "usage: attn-speech serve --cache DIR";

#[derive(Deserialize)]
#[serde(tag = "op", rename_all = "snake_case")]
enum Request {
    Prepare { id: u64 },
    Warm { id: u64 },
    Listen { id: u64 },
    Audio { target: u64, bytes: usize },
    Finish { target: u64 },
    Cancel { target: u64 },
}

enum Job {
    Prepare(Stop),
    Warm(Stop),
    Listen(Stop),
    Audio(Vec<u8>),
    Finish,
    Cancel,
}

#[derive(Clone)]
struct Stop {
    token: CancelToken,
    flag: Arc<AtomicBool>,
}

impl Stop {
    fn new() -> Self {
        Self {
            token: CancelToken::new(),
            flag: Arc::new(AtomicBool::new(false)),
        }
    }

    fn trigger(&self) {
        self.token.cancel();
        self.flag.store(true, Ordering::Relaxed);
    }

    fn triggered(&self) -> bool {
        self.flag.load(Ordering::Relaxed)
    }
}

type Pending = Arc<Mutex<HashMap<u64, Stop>>>;

fn main() -> ExitCode {
    let args: Vec<String> = std::env::args().skip(1).collect();
    match args.as_slice() {
        [command, flag, dir] if command == "serve" && flag == "--cache" => {
            serve(Cache::new(PathBuf::from(dir)));
            ExitCode::SUCCESS
        }
        _ => {
            eprintln!("{USAGE}");
            ExitCode::from(2)
        }
    }
}

fn emit(message: &Value) {
    let mut stdout = io::stdout().lock();
    let _ = writeln!(stdout, "{message}");
    let _ = stdout.flush();
}

fn serve(cache: Cache) {
    unsafe { std::env::set_var("GGML_METAL_NO_RESIDENCY", "1") };
    transcribe_cpp::disable_logging();
    emit(&json!({
        "event": "hello",
        "protocol": PROTOCOL,
        "version": env!("CARGO_PKG_VERSION"),
        "model": PARAKEET_UNIFIED_ENGLISH.name,
    }));
    let pending: Pending = Arc::default();
    let (jobs, queue) = mpsc::channel::<(u64, Job)>();
    {
        let pending = Arc::clone(&pending);
        thread::spawn(move || work(&cache, &queue, &pending));
    }
    read_requests(&jobs, &pending);
    std::process::exit(0);
}

fn read_requests(jobs: &mpsc::Sender<(u64, Job)>, pending: &Pending) {
    let mut stdin = io::stdin().lock();
    let mut line = String::new();
    loop {
        line.clear();
        match stdin.read_line(&mut line) {
            Ok(0) | Err(_) => return,
            Ok(_) => {}
        }
        let request = match serde_json::from_str::<Request>(line.trim()) {
            Ok(request) => request,
            Err(error) => {
                emit(&json!({"event": "error", "message": format!("unreadable request: {error}")}));
                continue;
            }
        };
        let (id, job) = match request {
            Request::Cancel { target } => {
                if let Some(stop) = pending
                    .lock()
                    .unwrap_or_else(std::sync::PoisonError::into_inner)
                    .get(&target)
                {
                    stop.trigger();
                }
                (target, Job::Cancel)
            }
            Request::Prepare { id } => (id, Job::Prepare(watch(pending, id))),
            Request::Warm { id } => (id, Job::Warm(watch(pending, id))),
            Request::Listen { id } => (id, Job::Listen(watch(pending, id))),
            Request::Audio { target, bytes } => {
                let mut body = vec![0; bytes];
                if stdin.read_exact(&mut body).is_err() {
                    return;
                }
                (target, Job::Audio(body))
            }
            Request::Finish { target } => (target, Job::Finish),
        };
        if jobs.send((id, job)).is_err() {
            return;
        }
    }
}

fn watch(pending: &Pending, id: u64) -> Stop {
    let stop = Stop::new();
    pending
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
        .insert(id, stop.clone());
    stop
}

struct Listening {
    id: u64,
    recording: Recording,
    stop: Stop,
}

#[derive(Default)]
struct Worker {
    engine: Option<Engine>,
    listening: Option<Listening>,
}

fn work(cache: &Cache, queue: &mpsc::Receiver<(u64, Job)>, pending: &Pending) {
    let mut worker = Worker::default();
    for (id, job) in queue {
        let answer = worker.run(cache, id, job);
        if worker.listening.as_ref().is_none_or(|open| open.id != id) {
            pending
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner)
                .remove(&id);
        }
        let Some(answer) = answer else { continue };
        let mut message = match answer {
            Ok(message) => message,
            Err(error) => json!({"event": "error", "message": error}),
        };
        message["id"] = json!(id);
        emit(&message);
    }
}

impl Worker {
    fn run(&mut self, cache: &Cache, id: u64, job: Job) -> Option<Result<Value, String>> {
        let open = self.listening.as_ref().is_some_and(|open| open.id == id);
        match job {
            Job::Prepare(stop) | Job::Warm(stop) | Job::Listen(stop) if stop.triggered() => {
                Some(Err("canceled before it started".to_string()))
            }
            Job::Prepare(stop) => {
                started(id);
                Some(prepare(cache, &mut self.engine, id, &stop))
            }
            Job::Warm(_) => {
                started(id);
                Some(warm(self.engine.as_mut()))
            }
            Job::Listen(stop) => {
                started(id);
                self.listen(id, stop).err().map(Err)
            }
            Job::Audio(body) if open => self.hear(&body).err().map(Err),
            Job::Finish if open => Some(self.finish()),
            Job::Finish => Some(Err(format!("recording {id} is not open"))),
            Job::Cancel if open => {
                self.stop_listening();
                Some(Err("recording canceled".to_string()))
            }
            Job::Audio(_) | Job::Cancel => None,
        }
    }

    fn listen(&mut self, id: u64, stop: Stop) -> Result<(), String> {
        let engine = self.engine.as_mut().ok_or_else(not_prepared)?;
        if let Some(open) = &self.listening {
            return Err(format!(
                "recording {} is still open; finish or cancel it first",
                open.id
            ));
        }
        self.listening = Some(Listening {
            id,
            recording: engine.listen(),
            stop,
        });
        Ok(())
    }

    fn hear(&mut self, body: &[u8]) -> Result<(), String> {
        let (Some(engine), Some(open)) = (self.engine.as_mut(), self.listening.as_mut()) else {
            return Ok(());
        };
        if open.stop.triggered() {
            return Ok(());
        }
        let heard = audio::samples(body)
            .and_then(|samples| engine.hear(&mut open.recording, &samples, &open.stop.token));
        if open.stop.triggered() {
            return Ok(());
        }
        heard
    }

    fn finish(&mut self) -> Result<Value, String> {
        let (Some(engine), Some(open)) = (self.engine.as_mut(), self.listening.take()) else {
            return Err(not_prepared());
        };
        let started = Instant::now();
        let transcript = engine.finish(open.recording, &open.stop.token)?;
        Ok(json!({
            "event": "transcript",
            "text": transcript.text,
            "speech": transcript.speech,
            "audio_ms": transcript.audio_ms,
            "segments": transcript.segments,
            "asr_ms": transcript.asr_ms,
            "finish_ms": started.elapsed().as_millis(),
        }))
    }

    fn stop_listening(&mut self) {
        if let Some(engine) = self.engine.as_mut() {
            engine.stop_listening();
        }
        self.listening = None;
    }
}

fn prepare(
    cache: &Cache,
    engine: &mut Option<Engine>,
    id: u64,
    stop: &Stop,
) -> Result<Value, String> {
    if engine.is_some() {
        return Ok(json!({"event": "ready", "load_ms": 0, "warm_ms": 0}));
    }
    let mut paths = Vec::new();
    for artifact in REQUIRED {
        let path = cache.prepare(artifact, &stop.flag, &mut |stage, bytes| {
            emit(&json!({
                "id": id,
                "event": "progress",
                "artifact": artifact.name,
                "stage": stage.as_str(),
                "bytes": bytes,
                "total": artifact.bytes,
            }));
        })?;
        paths.push(path);
    }
    emit(&json!({"id": id, "event": "progress", "stage": "loading"}));
    let started = Instant::now();
    let mut loaded = Engine::load(
        &cache.path(&PARAKEET_UNIFIED_ENGLISH),
        &cache.path(&SILERO_VAD),
    )?;
    let load_ms = started.elapsed().as_millis();
    if stop.triggered() {
        return Err("preparation canceled".into());
    }
    let started = Instant::now();
    loaded.warm()?;
    let warm_ms = started.elapsed().as_millis();
    *engine = Some(loaded);
    Ok(json!({"event": "ready", "load_ms": load_ms, "warm_ms": warm_ms}))
}

fn started(id: u64) {
    emit(&json!({"event": "started", "id": id}));
}

fn not_prepared() -> String {
    "the speech model is not prepared; send prepare first".into()
}

fn warm(engine: Option<&mut Engine>) -> Result<Value, String> {
    let engine = engine.ok_or_else(not_prepared)?;
    let started = Instant::now();
    engine.warm()?;
    Ok(json!({"event": "warmed", "ms": started.elapsed().as_millis()}))
}
