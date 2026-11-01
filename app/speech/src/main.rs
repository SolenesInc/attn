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
use engine::{Engine, Outcome};

const PROTOCOL: u32 = 1;
const USAGE: &str = "usage: attn-speech serve --cache DIR | attn-speech remove --cache DIR";

#[derive(Deserialize)]
#[serde(tag = "op", rename_all = "snake_case")]
enum Request {
    Prepare { id: u64 },
    Warm { id: u64 },
    Transcribe { id: u64, bytes: usize },
    Cancel { target: u64 },
}

enum Job {
    Prepare,
    Warm,
    Transcribe(Vec<u8>),
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
    let (command, cache) = match args.as_slice() {
        [command, flag, dir] if flag == "--cache" => {
            (command.as_str(), Cache::new(PathBuf::from(dir)))
        }
        _ => {
            eprintln!("{USAGE}");
            return ExitCode::from(2);
        }
    };
    match command {
        "serve" => {
            serve(cache);
            ExitCode::SUCCESS
        }
        "remove" => match cache.remove() {
            Ok(()) => ExitCode::SUCCESS,
            Err(error) => {
                eprintln!("could not remove the speech model cache: {error}");
                ExitCode::FAILURE
            }
        },
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
    let (jobs, queue) = mpsc::channel::<(u64, Stop, Job)>();
    {
        let pending = Arc::clone(&pending);
        thread::spawn(move || work(&cache, &queue, &pending));
    }
    read_requests(&jobs, &pending);
    std::process::exit(0);
}

fn read_requests(jobs: &mpsc::Sender<(u64, Stop, Job)>, pending: &Pending) {
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
                continue;
            }
            Request::Prepare { id } => (id, Job::Prepare),
            Request::Warm { id } => (id, Job::Warm),
            Request::Transcribe { id, bytes } => {
                let mut body = vec![0; bytes];
                if stdin.read_exact(&mut body).is_err() {
                    return;
                }
                (id, Job::Transcribe(body))
            }
        };
        let stop = Stop::new();
        pending
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .insert(id, stop.clone());
        if jobs.send((id, stop, job)).is_err() {
            return;
        }
    }
}

fn work(cache: &Cache, queue: &mpsc::Receiver<(u64, Stop, Job)>, pending: &Pending) {
    let mut engine: Option<Engine> = None;
    for (id, stop, job) in queue {
        let result = if stop.triggered() {
            Err("canceled before it started".to_string())
        } else {
            match job {
                Job::Prepare => prepare(cache, &mut engine, id, &stop),
                Job::Warm => warm(engine.as_mut()),
                Job::Transcribe(body) => transcribe(engine.as_mut(), &body, &stop),
            }
        };
        pending
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .remove(&id);
        let mut message = match result {
            Ok(message) => message,
            Err(error) => json!({"event": "error", "message": error}),
        };
        message["id"] = json!(id);
        emit(&message);
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

fn not_prepared() -> String {
    "the speech model is not prepared; send prepare first".into()
}

fn warm(engine: Option<&mut Engine>) -> Result<Value, String> {
    let engine = engine.ok_or_else(not_prepared)?;
    let started = Instant::now();
    engine.warm()?;
    Ok(json!({"event": "warmed", "ms": started.elapsed().as_millis()}))
}

fn transcribe(engine: Option<&mut Engine>, body: &[u8], stop: &Stop) -> Result<Value, String> {
    let engine = engine.ok_or_else(not_prepared)?;
    let started = Instant::now();
    let clip = audio::decode_wav(body)?;
    let decode_ms = started.elapsed().as_millis();
    let timed = engine.transcribe(clip.samples, &stop.token)?;
    let (text, speech) = match timed.outcome {
        Outcome::Text(text) => (text.trim().to_string(), true),
        Outcome::NoSpeech => (String::new(), false),
    };
    Ok(json!({
        "event": "transcript",
        "text": text,
        "speech": speech,
        "audio_ms": clip.duration_ms,
        "decode_ms": decode_ms,
        "vad_ms": timed.vad_ms,
        "asr_ms": timed.asr_ms,
    }))
}
