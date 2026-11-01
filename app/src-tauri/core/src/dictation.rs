use std::cell::{Cell, RefCell};
use std::path::PathBuf;
use std::ptr::NonNull;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::mpsc::Receiver;
use std::sync::{Arc, Mutex, MutexGuard};
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use block2::RcBlock;
use objc2::rc::Retained;
use objc2::runtime::{Bool, ProtocolObject};
use objc2::AllocAnyThread;
use objc2_av_foundation::{AVAuthorizationStatus, AVCaptureDevice, AVMediaTypeAudio};
use objc2_avf_audio::{
    AVAudioBuffer, AVAudioCommonFormat, AVAudioConverter, AVAudioConverterInputStatus,
    AVAudioEngine, AVAudioEngineConfigurationChangeNotification, AVAudioFormat, AVAudioPCMBuffer,
    AVAudioTime,
};
use objc2_foundation::{NSNotification, NSNotificationCenter, NSObjectProtocol};
use serde::Serialize;
use serde_json::{json, Value};
use tauri::{AppHandle, Emitter, Manager, Runtime, Window};

pub const EVENT: &str = "dictation";
pub const REQUEST_EVENT: &str = "dictation-request";
const SAMPLE_RATE: f64 = 16_000.0;
const TAP_FRAMES: u32 = 1_600;
const TRANSCRIPT_TRIPWIRE: Duration = Duration::from_secs(600);
const MICROPHONE_DENIED: &str = "attn does not have microphone access. Allow attn in System Settings › Privacy & Security › Microphone, then start dictation again.";

#[derive(Clone, Copy, Default, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Timeline {
    pressed: Option<f64>,
    requested: f64,
    engine_started: Option<f64>,
    first_audio: Option<f64>,
    stopped: Option<f64>,
    transcribed: Option<f64>,
}

#[derive(Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub enum StopReason {
    Shortcut,
    Button,
    DeviceChanged,
}

#[derive(Clone, Serialize)]
#[serde(tag = "kind", rename_all = "camelCase")]
enum Update {
    #[serde(rename_all = "camelCase")]
    Listening { recording: u64, at: f64 },
    #[serde(rename_all = "camelCase")]
    Stopped {
        recording: u64,
        reason: StopReason,
        at: f64,
    },
    #[serde(rename_all = "camelCase")]
    Transcript {
        recording: u64,
        text: String,
        speech: bool,
        audio_ms: u64,
        reason: StopReason,
        timeline: Timeline,
        helper: Value,
    },
    #[serde(rename_all = "camelCase")]
    Failed { recording: u64, message: String },
    #[serde(rename_all = "camelCase")]
    Canceled { recording: u64 },
}

struct Active {
    recording: u64,
    window: String,
    timeline: Timeline,
    answers: Receiver<Value>,
}

#[derive(Default)]
struct State {
    active: Option<Active>,
    pressed: Option<f64>,
    clip: Option<Clip>,
}

#[derive(Clone)]
pub struct Clip {
    pub path: PathBuf,
    pub stop_at_end: bool,
}

#[derive(Default)]
pub struct Dictation(Mutex<State>);

impl Dictation {
    fn lock(&self) -> MutexGuard<'_, State> {
        self.0
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
    }
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Started {
    recording: u64,
}

enum Capture {
    Microphone {
        engine: Retained<AVAudioEngine>,
        observer: Retained<ProtocolObject<dyn NSObjectProtocol>>,
    },
    Clip(Arc<AtomicBool>),
}

thread_local! {
    static CAPTURE: RefCell<Option<Capture>> = const { RefCell::new(None) };
}

fn now() -> f64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0.0, |elapsed| elapsed.as_secs_f64() * 1000.0)
}

fn send<R: Runtime>(app: &AppHandle<R>, window: &str, update: Update) {
    let _ = app.emit_to(window, EVENT, update);
}

fn microphone_access() -> Result<(), String> {
    let media = unsafe { AVMediaTypeAudio }.ok_or("AVFoundation has no audio media type")?;
    let status = unsafe { AVCaptureDevice::authorizationStatusForMediaType(media) };
    if status == AVAuthorizationStatus::Authorized {
        return Ok(());
    }
    if status != AVAuthorizationStatus::NotDetermined {
        return Err(MICROPHONE_DENIED.into());
    }
    let (answer, granted) = std::sync::mpsc::channel();
    let handler = RcBlock::new(move |allowed: Bool| {
        let _ = answer.send(allowed.as_bool());
    });
    unsafe { AVCaptureDevice::requestAccessForMediaType_completionHandler(media, &handler) };
    match granted.recv() {
        Ok(true) => Ok(()),
        _ => Err(MICROPHONE_DENIED.into()),
    }
}

fn open_microphone<R: Runtime>(
    app: &AppHandle<R>,
    recording: u64,
    window: String,
) -> Result<(), String> {
    let engine = unsafe { AVAudioEngine::new() };
    let input = unsafe { engine.inputNode() };
    let hardware = unsafe { input.outputFormatForBus(0) };
    let rate = unsafe { hardware.sampleRate() };
    if rate <= 0.0 || unsafe { hardware.channelCount() } == 0 {
        return Err("No microphone is available. Connect one and start dictation again.".into());
    }
    let speech_format = unsafe {
        AVAudioFormat::initWithCommonFormat_sampleRate_channels_interleaved(
            AVAudioFormat::alloc(),
            AVAudioCommonFormat::PCMFormatFloat32,
            SAMPLE_RATE,
            1,
            false,
        )
    }
    .ok_or("Cannot describe 16 kHz mono audio")?;
    let converter = unsafe {
        AVAudioConverter::initFromFormat_toFormat(
            AVAudioConverter::alloc(),
            &hardware,
            &speech_format,
        )
    }
    .ok_or_else(|| format!("Cannot convert {rate} Hz microphone audio to 16 kHz"))?;
    unsafe { converter.setDownmix(true) };
    let heard_once = Cell::new(false);
    let tap_app = app.clone();
    let tap_window = window.clone();
    let tap = RcBlock::new(
        move |buffer: NonNull<AVAudioPCMBuffer>, _: NonNull<AVAudioTime>| {
            let buffer = unsafe { buffer.as_ref() };
            let samples = match convert(&converter, &speech_format, buffer, rate) {
                Ok(samples) => samples,
                Err(message) => return abandon(&tap_app, recording, message),
            };
            if !heard_once.replace(true) {
                heard(&tap_app, recording, &tap_window);
            }
            if let Err(message) = crate::speech::audio(&tap_app, recording, &samples) {
                abandon(&tap_app, recording, message);
            }
        },
    );
    unsafe {
        input.installTapOnBus_bufferSize_format_block(
            0,
            TAP_FRAMES,
            Some(&hardware),
            RcBlock::as_ptr(&tap),
        );
    }
    let changed_app = app.clone();
    let changed = RcBlock::new(move |_: NonNull<NSNotification>| {
        let app = changed_app.clone();
        let _ = changed_app
            .run_on_main_thread(move || stop_recording(&app, recording, StopReason::DeviceChanged));
    });
    let observer = unsafe {
        NSNotificationCenter::defaultCenter().addObserverForName_object_queue_usingBlock(
            Some(AVAudioEngineConfigurationChangeNotification),
            Some(&engine),
            None,
            &changed,
        )
    };
    unsafe { engine.prepare() };
    if let Err(error) = unsafe { engine.startAndReturnError() } {
        unsafe { input.removeTapOnBus(0) };
        unsafe { NSNotificationCenter::defaultCenter().removeObserver(observer.as_ref()) };
        return Err(format!(
            "The microphone did not start: {}",
            error.localizedDescription()
        ));
    }
    CAPTURE.with(|capture| capture.replace(Some(Capture::Microphone { engine, observer })));
    Ok(())
}

fn heard<R: Runtime>(app: &AppHandle<R>, recording: u64, window: &str) {
    send(
        app,
        window,
        Update::Listening {
            recording,
            at: now(),
        },
    );
    if let Some(active) = app.state::<Dictation>().lock().active.as_mut() {
        active.timeline.first_audio.get_or_insert(now());
    }
}

fn open_clip<R: Runtime>(
    app: &AppHandle<R>,
    recording: u64,
    window: String,
    clip: &Clip,
) -> Result<(), String> {
    let bytes = std::fs::read(&clip.path)
        .map_err(|error| format!("Cannot read test clip {}: {error}", clip.path.display()))?;
    let stop_at_end = clip.stop_at_end;
    let samples: Vec<f32> = bytes
        .chunks_exact(4)
        .map(|sample| f32::from_le_bytes([sample[0], sample[1], sample[2], sample[3]]))
        .collect();
    let stopped = Arc::new(AtomicBool::new(false));
    let flag = stopped.clone();
    let app = app.clone();
    thread::spawn(move || {
        let chunk = TAP_FRAMES as usize;
        let period = Duration::from_secs_f64(TAP_FRAMES as f64 / SAMPLE_RATE);
        let silence = vec![0.0; chunk];
        let begun = Instant::now();
        for index in 0.. {
            std::thread::sleep(
                (begun + period * (index + 1)).saturating_duration_since(Instant::now()),
            );
            if flag.load(Ordering::Acquire) {
                return;
            }
            let start = index as usize * chunk;
            if stop_at_end && start >= samples.len() {
                let stopper = app.clone();
                let _ = app.run_on_main_thread(move || {
                    stop_recording(&stopper, recording, StopReason::Shortcut)
                });
                return;
            }
            let block = samples
                .get(start..(start + chunk).min(samples.len()))
                .filter(|block| !block.is_empty())
                .unwrap_or(&silence);
            if index == 0 {
                heard(&app, recording, &window);
            }
            if let Err(message) = crate::speech::audio(&app, recording, block) {
                return abandon(&app, recording, message);
            }
        }
    });
    CAPTURE.with(|capture| capture.replace(Some(Capture::Clip(stopped))));
    Ok(())
}

fn convert(
    converter: &AVAudioConverter,
    format: &AVAudioFormat,
    buffer: &AVAudioPCMBuffer,
    rate: f64,
) -> Result<Vec<f32>, String> {
    let frames = unsafe { buffer.frameLength() };
    let capacity = (f64::from(frames) * SAMPLE_RATE / rate).ceil() as u32 + 64;
    let output = unsafe {
        AVAudioPCMBuffer::initWithPCMFormat_frameCapacity(
            AVAudioPCMBuffer::alloc(),
            format,
            capacity,
        )
    }
    .ok_or("Cannot allocate a 16 kHz audio buffer")?;
    let supplied = Cell::new(false);
    let source: *const AVAudioPCMBuffer = buffer;
    let feed = RcBlock::new(
        move |_: u32, status: NonNull<AVAudioConverterInputStatus>| -> *mut AVAudioBuffer {
            if supplied.replace(true) {
                unsafe { status.write(AVAudioConverterInputStatus::NoDataNow) };
                return std::ptr::null_mut();
            }
            unsafe { status.write(AVAudioConverterInputStatus::HaveData) };
            source.cast_mut().cast()
        },
    );
    let mut error = None;
    unsafe {
        converter.convertToBuffer_error_withInputFromBlock(
            &output,
            Some(&mut error),
            RcBlock::as_ptr(&feed),
        )
    };
    if let Some(error) = error {
        return Err(format!(
            "Microphone audio could not be converted: {}",
            error.localizedDescription()
        ));
    }
    let length = unsafe { output.frameLength() } as usize;
    let channels = unsafe { output.floatChannelData() };
    if channels.is_null() || length == 0 {
        return Ok(Vec::new());
    }
    let first = unsafe { *channels };
    Ok(unsafe { std::slice::from_raw_parts(first.as_ptr(), length) }.to_vec())
}

fn close_microphone() {
    CAPTURE.with(|capture| match capture.borrow_mut().take() {
        Some(Capture::Microphone { engine, observer }) => unsafe {
            engine.inputNode().removeTapOnBus(0);
            engine.stop();
            NSNotificationCenter::defaultCenter().removeObserver(observer.as_ref());
        },
        Some(Capture::Clip(stopped)) => stopped.store(true, Ordering::Release),
        None => {}
    });
}

fn take_active<R: Runtime>(app: &AppHandle<R>, recording: Option<u64>) -> Option<Active> {
    let dictation = app.state::<Dictation>();
    let mut state = dictation.lock();
    if recording.is_some_and(|id| state.active.as_ref().map(|active| active.recording) != Some(id))
    {
        return None;
    }
    let active = state.active.take();
    drop(state);
    if active.is_some() {
        let _ = crate::hotkeys::dictating(app, false);
    }
    active
}

fn on_main<R: Runtime>(app: &AppHandle<R>, action: impl FnOnce() + Send + 'static) {
    if objc2::MainThreadMarker::new().is_some() {
        action();
    } else {
        let _ = app.run_on_main_thread(action);
    }
}

fn abandon<R: Runtime>(app: &AppHandle<R>, recording: u64, message: String) {
    let app = app.clone();
    on_main(&app.clone(), move || {
        let Some(active) = take_active(&app, Some(recording)) else {
            return;
        };
        close_microphone();
        let _ = crate::speech::notify(&app, json!({"op": "cancel", "target": recording}));
        send(&app, &active.window, Update::Failed { recording, message });
    });
}

fn stop_recording<R: Runtime>(app: &AppHandle<R>, recording: u64, reason: StopReason) {
    let Some(mut active) = take_active(app, Some(recording)) else {
        return;
    };
    let stopped = now();
    active.timeline.stopped = Some(stopped);
    close_microphone();
    send(
        app,
        &active.window,
        Update::Stopped {
            recording,
            reason,
            at: stopped,
        },
    );
    if let Err(message) = crate::speech::notify(app, json!({"op": "finish", "target": recording})) {
        send(app, &active.window, Update::Failed { recording, message });
        return;
    }
    let app = app.clone();
    thread::spawn(move || {
        let update = await_transcript(&active, reason);
        send(&app, &active.window, update);
    });
}

fn await_transcript(active: &Active, reason: StopReason) -> Update {
    let recording = active.recording;
    loop {
        let event = match active.answers.recv_timeout(TRANSCRIPT_TRIPWIRE) {
            Ok(event) => event,
            Err(std::sync::mpsc::RecvTimeoutError::Timeout) => {
                return Update::Failed {
                    recording,
                    message: format!(
                        "The speech helper did not return a transcript within {} s.",
                        TRANSCRIPT_TRIPWIRE.as_secs()
                    ),
                }
            }
            Err(std::sync::mpsc::RecvTimeoutError::Disconnected) => {
                return Update::Failed {
                    recording,
                    message: "The speech helper stopped before it returned the transcript.".into(),
                }
            }
        };
        match event["event"].as_str() {
            Some("transcript") => {
                let mut timeline = active.timeline;
                timeline.transcribed = Some(now());
                return Update::Transcript {
                    recording,
                    text: event["text"].as_str().unwrap_or_default().to_string(),
                    speech: event["speech"].as_bool().unwrap_or(false),
                    audio_ms: event["audio_ms"].as_u64().unwrap_or(0),
                    reason,
                    timeline,
                    helper: json!({
                        "asrMs": event["asr_ms"],
                        "finishMs": event["finish_ms"],
                        "segments": event["segments"],
                    }),
                };
            }
            Some("error") => {
                return Update::Failed {
                    recording,
                    message: event["message"]
                        .as_str()
                        .unwrap_or("speech helper error")
                        .to_string(),
                }
            }
            _ => continue,
        }
    }
}

pub fn press<R: Runtime>(app: &AppHandle<R>, window: Option<String>, browser: Option<String>) {
    let at = now();
    let recording = {
        let dictation = app.state::<Dictation>();
        let mut state = dictation.lock();
        state.pressed = Some(at);
        state.active.as_ref().map(|active| active.recording)
    };
    match (recording, window) {
        (Some(recording), _) => stop_recording(app, recording, StopReason::Shortcut),
        (None, Some(window)) => {
            let _ = app.emit_to(
                window.as_str(),
                REQUEST_EVENT,
                json!({"browser": browser, "at": at}),
            );
        }
        (None, None) => {}
    }
}

#[tauri::command]
pub async fn dictation_start<R: Runtime>(
    app: AppHandle<R>,
    window: Window<R>,
) -> Result<Started, String> {
    let requested = now();
    crate::speech::ready(&app)?;
    let clip = app.state::<Dictation>().lock().clip.clone();
    if clip.is_none() {
        tauri::async_runtime::spawn_blocking(microphone_access)
            .await
            .map_err(|error| error.to_string())??;
    }
    let _ = crate::speech::request(&app, json!({"op": "warm"}))?;
    let (recording, answers) = crate::speech::request(&app, json!({"op": "listen"}))?;
    let label = window.label().to_string();
    let opened = crate::hotkeys::on_main(&app, move |app| {
        if let Some(active) = app.state::<Dictation>().lock().active.as_ref() {
            return Err(format!(
                "A recording is already running in the {} window. Stop it first.",
                active.window
            ));
        }
        match clip {
            Some(clip) => open_clip(app, recording, label.clone(), &clip)?,
            None => open_microphone(app, recording, label.clone())?,
        }
        let dictation = app.state::<Dictation>();
        let mut state = dictation.lock();
        let pressed = state.pressed.take();
        state.active = Some(Active {
            recording,
            window: label,
            timeline: Timeline {
                pressed,
                requested,
                engine_started: Some(now()),
                ..Timeline::default()
            },
            answers,
        });
        drop(state);
        let _ = crate::hotkeys::dictating(app, true);
        Ok(())
    });
    match opened {
        Ok(()) => Ok(Started { recording }),
        Err(message) => {
            let _ = crate::speech::notify(&app, json!({"op": "cancel", "target": recording}));
            Err(message)
        }
    }
}

#[tauri::command]
pub async fn dictation_stop<R: Runtime>(app: AppHandle<R>, recording: u64) -> Result<(), String> {
    crate::hotkeys::on_main(&app, move |app| {
        stop_recording(app, recording, StopReason::Button);
        Ok(())
    })
}

#[tauri::command]
pub async fn dictation_cancel<R: Runtime>(
    app: AppHandle<R>,
    recording: Option<u64>,
    window: Window<R>,
) -> Result<(), String> {
    let label = window.label().to_string();
    crate::hotkeys::on_main(&app, move |app| {
        cancel(app, recording, Some(&label));
        Ok(())
    })
}

#[tauri::command]
pub async fn dictation_bind<R: Runtime>(
    app: AppHandle<R>,
    binding: Option<String>,
) -> Result<(), String> {
    crate::hotkeys::bind(&app, crate::hotkeys::Action::Dictation, binding)
}

fn cancel<R: Runtime>(app: &AppHandle<R>, recording: Option<u64>, window: Option<&str>) {
    let owned = {
        let dictation = app.state::<Dictation>();
        let state = dictation.lock();
        state.active.as_ref().is_some_and(|active| {
            recording.is_none_or(|id| id == active.recording)
                && window.is_none_or(|label| label == active.window)
        })
    };
    if !owned {
        return;
    }
    let Some(active) = take_active(app, recording) else {
        return;
    };
    close_microphone();
    let _ = crate::speech::notify(app, json!({"op": "cancel", "target": active.recording}));
    send(
        app,
        &active.window,
        Update::Canceled {
            recording: active.recording,
        },
    );
}

pub fn use_clip<R: Runtime>(app: &AppHandle<R>, clip: Option<Clip>) {
    app.state::<Dictation>().lock().clip = clip;
}

pub fn release<R: Runtime>(app: &AppHandle<R>, reason: &str) {
    let app = app.clone();
    let message = reason.to_string();
    on_main(&app.clone(), move || {
        if let Some(active) = take_active(&app, None) {
            close_microphone();
            send(
                &app,
                &active.window,
                Update::Failed {
                    recording: active.recording,
                    message,
                },
            );
        }
    });
}
