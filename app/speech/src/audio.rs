use std::io::Cursor;

use rubato::audioadapter_buffers::direct::InterleavedSlice;
use rubato::{Fft, FixedSync, Indexing, Resampler, WindowFunction};

pub const SAMPLE_RATE: u32 = 16_000;
const MINIMUM_SAMPLES: usize = 24_000;
const TRAILING_SILENCE_SAMPLES: usize = 3_200;
const RESAMPLE_CHUNK: usize = 1024;

#[derive(Debug)]
pub struct Clip {
    pub samples: Vec<f32>,
    pub duration_ms: u64,
}

#[allow(clippy::cast_precision_loss)]
pub fn decode_wav(bytes: &[u8]) -> Result<Clip, String> {
    let mut reader = hound::WavReader::new(Cursor::new(bytes))
        .map_err(|error| format!("not a WAV recording: {error}"))?;
    let spec = reader.spec();
    if spec.sample_rate == 0 || spec.channels == 0 {
        return Err(format!(
            "WAV recording has {} channels at {} Hz",
            spec.channels, spec.sample_rate
        ));
    }
    let interleaved: Vec<f32> = match spec.sample_format {
        hound::SampleFormat::Float => reader
            .samples::<f32>()
            .collect::<Result<_, _>>()
            .map_err(|error| format!("could not read WAV samples: {error}"))?,
        hound::SampleFormat::Int => {
            let scale = 1.0 / (1_i64 << (spec.bits_per_sample - 1)) as f32;
            reader
                .samples::<i32>()
                .map(|sample| sample.map(|value| value as f32 * scale))
                .collect::<Result<_, _>>()
                .map_err(|error| format!("could not read WAV samples: {error}"))?
        }
    };
    let channels = usize::from(spec.channels);
    let mono: Vec<f32> = interleaved
        .chunks(channels)
        .map(|frame| frame.iter().sum::<f32>() / channels as f32)
        .collect();
    let duration_ms = mono.len() as u64 * 1000 / u64::from(spec.sample_rate);
    Ok(Clip {
        samples: resample(&mono, spec.sample_rate)?,
        duration_ms,
    })
}

fn resample(samples: &[f32], source_rate: u32) -> Result<Vec<f32>, String> {
    if source_rate == SAMPLE_RATE {
        return Ok(samples.to_vec());
    }
    let mut resampler = Fft::<f32>::new_custom(
        source_rate as usize,
        SAMPLE_RATE as usize,
        RESAMPLE_CHUNK,
        1,
        1,
        WindowFunction::BlackmanHarris2,
        FixedSync::Input,
    )
    .map_err(|error| format!("cannot resample {source_rate} Hz audio: {error}"))?;
    let expected = samples.len() * SAMPLE_RATE as usize / source_rate as usize;
    let delay = resampler.output_delay();
    let mut output = Vec::with_capacity(expected + delay + RESAMPLE_CHUNK);
    let mut chunks = samples.chunks_exact(RESAMPLE_CHUNK);
    for chunk in &mut chunks {
        output.extend(process(&mut resampler, chunk, false));
    }
    output.extend(process(&mut resampler, chunks.remainder(), true));
    while output.len() < expected + delay {
        output.extend(process(&mut resampler, &[], true));
    }
    output.drain(..delay);
    output.truncate(expected);
    Ok(output)
}

fn process(resampler: &mut Fft<f32>, samples: &[f32], partial: bool) -> Vec<f32> {
    let input =
        InterleavedSlice::new(samples, 1, samples.len()).expect("mono chunk fits its slice");
    let indexing = partial.then(|| Indexing {
        partial_len: Some(samples.len()),
        ..Indexing::default()
    });
    resampler
        .process(&input, indexing.as_ref())
        .expect("fixed-rate resampling succeeds")
        .take_data()
}

pub fn pad_for_model(samples: &mut Vec<f32>) {
    let padded = samples.len().max(MINIMUM_SAMPLES) + TRAILING_SILENCE_SAMPLES;
    samples.resize(padded, 0.0);
}

pub fn pad_chunk(samples: &mut Vec<f32>) {
    samples.resize(samples.len().max(MINIMUM_SAMPLES), 0.0);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[allow(clippy::cast_precision_loss, clippy::cast_possible_truncation)]
    fn wav(rate: u32, channels: u16, frames: usize) -> Vec<u8> {
        let mut bytes = Cursor::new(Vec::new());
        let spec = hound::WavSpec {
            channels,
            sample_rate: rate,
            bits_per_sample: 16,
            sample_format: hound::SampleFormat::Int,
        };
        let mut writer = hound::WavWriter::new(&mut bytes, spec).unwrap();
        for frame in 0..frames {
            let value = ((frame as f32 / rate as f32 * 440.0 * std::f32::consts::TAU).sin()
                * 8000.0) as i16;
            for _ in 0..channels {
                writer.write_sample(value).unwrap();
            }
        }
        writer.finalize().unwrap();
        bytes.into_inner()
    }

    #[test]
    fn any_rate_and_channel_count_becomes_16khz_mono_of_the_same_duration() {
        for (rate, channels) in [
            (16_000, 1),
            (48_000, 2),
            (44_100, 1),
            (22_050, 2),
            (8_000, 1),
        ] {
            let clip = decode_wav(&wav(rate, channels, rate as usize * 3 / 2)).unwrap();
            assert_eq!(clip.duration_ms, 1500, "{rate} Hz x{channels}");
            assert_eq!(clip.samples.len(), 24_000, "{rate} Hz x{channels}");
            let peak = clip
                .samples
                .iter()
                .fold(0.0_f32, |peak, sample| peak.max(sample.abs()));
            assert!(
                (0.2..0.3).contains(&peak),
                "{rate} Hz x{channels} peak {peak}"
            );
        }
    }

    #[test]
    fn a_non_wav_body_names_the_problem() {
        assert!(
            decode_wav(b"not audio")
                .unwrap_err()
                .starts_with("not a WAV recording")
        );
    }
}
