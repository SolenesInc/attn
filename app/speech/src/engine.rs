use std::path::Path;
use std::time::Instant;

use transcribe_cpp::{
    Backend, CancelToken, DeviceType, Model, ModelOptions, RunOptions, Session, SessionOptions,
    TimestampKind, VadOptions, VadSession,
};

use crate::audio;

const VAD_THRESHOLD: f64 = 0.4;
const VAD_MIN_SPEECH_MS: i32 = 160;
const EXPECTED_ARCHITECTURE: &str = "parakeet";
const EXPECTED_VARIANT: &str = "unified-en-0.6b";

pub struct Engine {
    model: Model,
    session: Option<Session>,
    session_options: SessionOptions,
    run_options: RunOptions,
    max_chunk_samples: Option<usize>,
    vad: VadSession,
}

pub enum Outcome {
    Text(String),
    NoSpeech,
}

pub struct Timed {
    pub outcome: Outcome,
    pub vad_ms: u128,
    pub asr_ms: u128,
}

impl Engine {
    pub fn load(model_path: &Path, vad_path: &Path) -> Result<Self, String> {
        let model = Model::load_with(
            model_path,
            &ModelOptions {
                backend: Backend::Metal,
                ..Default::default()
            },
        )
        .map_err(|error| format!("could not load {}: {error}", model_path.display()))?;
        let (architecture, variant) = (model.arch(), model.variant());
        if architecture != EXPECTED_ARCHITECTURE || variant != EXPECTED_VARIANT {
            return Err(format!(
                "{} contains {architecture}/{variant}, expected {EXPECTED_ARCHITECTURE}/{EXPECTED_VARIANT}",
                model_path.display()
            ));
        }
        let device = model.device().map_err(|error| error.to_string())?;
        if device.kind != "metal" {
            return Err(format!(
                "the speech model selected {} ({}) instead of Metal",
                device.kind, device.name
            ));
        }
        let capabilities = model.capabilities().map_err(|error| error.to_string())?;
        let session_options = match device.device_type {
            DeviceType::Gpu | DeviceType::Igpu => SessionOptions {
                n_threads: 1,
                ..Default::default()
            },
            _ => SessionOptions::default(),
        };
        let session = model
            .session_with(&session_options)
            .map_err(|error| error.to_string())?;
        let vad = Model::load_with(
            vad_path,
            &ModelOptions {
                backend: Backend::Cpu,
                ..Default::default()
            },
        )
        .and_then(|vad| vad.vad_session())
        .map_err(|error| format!("could not load {}: {error}", vad_path.display()))?;
        let max_chunk_samples = usize::try_from(capabilities.max_audio_ms)
            .ok()
            .filter(|ms| *ms > 0)
            .map(|ms| ms * audio::SAMPLE_RATE as usize / 1000);
        Ok(Self {
            model,
            session: Some(session),
            session_options,
            run_options: RunOptions {
                timestamps: TimestampKind::None,
                language: Some("en".into()),
                ..Default::default()
            },
            max_chunk_samples,
            vad,
        })
    }

    pub fn warm(&mut self) -> Result<(), String> {
        let mut silence = Vec::new();
        audio::pad_for_model(&mut silence);
        self.run(&silence, &CancelToken::new()).map(|_| ())
    }

    pub fn transcribe(&mut self, samples: Vec<f32>, cancel: &CancelToken) -> Result<Timed, String> {
        let started = Instant::now();
        let speech = self.detect_speech(&samples)?;
        let vad_ms = started.elapsed().as_millis();
        if !speech {
            return Ok(Timed {
                outcome: Outcome::NoSpeech,
                vad_ms,
                asr_ms: 0,
            });
        }
        let started = Instant::now();
        let text = self.recognize(samples, cancel)?;
        Ok(Timed {
            outcome: Outcome::Text(text),
            vad_ms,
            asr_ms: started.elapsed().as_millis(),
        })
    }

    fn detect_speech(&mut self, samples: &[f32]) -> Result<bool, String> {
        if samples.is_empty() {
            return Ok(false);
        }
        let speech = self
            .vad
            .run(
                samples,
                &VadOptions {
                    threshold: VAD_THRESHOLD,
                    min_speech_ms: VAD_MIN_SPEECH_MS,
                    ..VadOptions::default()
                },
            )
            .map_err(|error| format!("speech detection failed: {error}"))?;
        Ok(!speech.segments.is_empty())
    }

    fn recognize(&mut self, mut samples: Vec<f32>, cancel: &CancelToken) -> Result<String, String> {
        audio::pad_for_model(&mut samples);
        let Some(max) = self.max_chunk_samples.filter(|max| samples.len() > *max) else {
            return self.run(&samples, cancel);
        };
        let mut parts = Vec::new();
        for chunk in samples.chunks(max) {
            let mut chunk = chunk.to_vec();
            audio::pad_chunk(&mut chunk);
            let text = self.run(&chunk, cancel)?;
            if !text.trim().is_empty() {
                parts.push(text.trim().to_string());
            }
        }
        Ok(parts.join(" "))
    }

    fn run(&mut self, samples: &[f32], cancel: &CancelToken) -> Result<String, String> {
        let mut session = match self.session.take() {
            Some(session) => session,
            None => self
                .model
                .session_with(&self.session_options)
                .map_err(|error| error.to_string())?,
        };
        session.set_cancel_token(cancel);
        let result = match session.run(samples, &self.run_options) {
            Ok(_) if cancel.is_cancelled() => Err("transcription canceled".into()),
            Ok(transcript) => Ok(transcript.text),
            Err(transcribe_cpp::Error::OutputRepetition {
                partial: Some(partial),
                ..
            }) => Ok(partial.text),
            Err(transcribe_cpp::Error::Aborted { .. }) => Err("transcription canceled".into()),
            Err(error) => Err(format!("transcription failed: {error}")),
        };
        drop(session);
        self.session = self.model.session_with(&self.session_options).ok();
        result
    }
}
