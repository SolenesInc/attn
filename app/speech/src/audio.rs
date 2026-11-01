pub const SAMPLE_RATE: u32 = 16_000;
const MINIMUM_SAMPLES: usize = 24_000;
const TRAILING_SILENCE_SAMPLES: usize = 3_200;
const SAMPLE_BYTES: usize = 4;

pub fn samples(bytes: &[u8]) -> Result<Vec<f32>, String> {
    if !bytes.len().is_multiple_of(SAMPLE_BYTES) {
        return Err(format!(
            "audio must be whole little-endian f32 samples; got {} bytes",
            bytes.len()
        ));
    }
    Ok(bytes
        .chunks_exact(SAMPLE_BYTES)
        .map(|sample| f32::from_le_bytes([sample[0], sample[1], sample[2], sample[3]]))
        .collect())
}

pub fn pad_for_model(samples: &mut Vec<f32>) {
    let padded = samples.len().max(MINIMUM_SAMPLES) + TRAILING_SILENCE_SAMPLES;
    samples.resize(padded, 0.0);
}

pub fn pad_chunk(samples: &mut Vec<f32>) {
    samples.resize(samples.len().max(MINIMUM_SAMPLES), 0.0);
}
