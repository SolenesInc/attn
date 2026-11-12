//! The clipboard writes a program asks for with OSC 52, read from its output as the Tauri
//! terminal reads them (`terminalOsc.ts`): every target, any terminator, a query ignored, and
//! a sequence split across chunks held until it ends.

const PREFIX: &[u8] = b"\x1b]52;";
const ESC: u8 = 0x1b;
const BEL: u8 = 0x07;
/// The longest unterminated sequence held for the next chunk, as the Tauri terminal holds.
const MAX_PENDING: usize = 16 * 1024 * 1024;

#[derive(Default)]
pub struct Osc52 {
    pending: Vec<u8>,
}

impl Osc52 {
    /// The text each complete OSC 52 write in `chunk` puts on the clipboard. A payload that is
    /// not base64 writes nothing.
    pub fn writes(&mut self, chunk: &[u8]) -> Vec<String> {
        if self.pending.is_empty() && !chunk.contains(&ESC) {
            return Vec::new();
        }
        let mut buffer = std::mem::take(&mut self.pending);
        buffer.extend_from_slice(chunk);
        let mut writes = Vec::new();
        let mut at = 0;
        while at < buffer.len() {
            let Some(start) = find(&buffer[at..], PREFIX).map(|i| at + i) else {
                let hold = partial_suffix(&buffer[at..], PREFIX);
                self.pending = buffer[buffer.len() - hold..].to_vec();
                return writes;
            };
            let command = start + PREFIX.len();
            let end = (command..buffer.len()).find_map(|i| match buffer[i] {
                BEL => Some((i, 1)),
                ESC if buffer.get(i + 1) == Some(&b'\\') => Some((i, 2)),
                _ => None,
            });
            let Some((end, terminator)) = end else {
                self.pending = buffer[start..buffer.len().min(start + MAX_PENDING)].to_vec();
                return writes;
            };
            if let Some(separator) = buffer[command..end].iter().position(|&b| b == b';') {
                let payload = &buffer[command + separator + 1..end];
                if payload != b"?"
                    && let Some(text) = decode(payload)
                {
                    writes.push(text);
                }
            }
            at = end + terminator;
        }
        writes
    }
}

/// A payload as the browser's `atob` reads it: padding optional, ASCII whitespace skipped.
fn decode(payload: &[u8]) -> Option<String> {
    use base64::Engine;
    use base64::engine::{DecodePaddingMode, GeneralPurpose, GeneralPurposeConfig};
    const ATOB: GeneralPurpose = GeneralPurpose::new(
        &base64::alphabet::STANDARD,
        GeneralPurposeConfig::new().with_decode_padding_mode(DecodePaddingMode::Indifferent),
    );
    let text: String = std::str::from_utf8(payload)
        .ok()?
        .chars()
        .filter(|c| !c.is_ascii_whitespace())
        .collect();
    let bytes = ATOB.decode(text).ok()?;
    Some(String::from_utf8_lossy(&bytes).into_owned())
}

fn find(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack.windows(needle.len()).position(|w| w == needle)
}

/// How many bytes at the end of `buffer` begin `pattern`, so the next chunk may finish it.
fn partial_suffix(buffer: &[u8], pattern: &[u8]) -> usize {
    (1..pattern.len().min(buffer.len() + 1))
        .rev()
        .find(|&n| buffer.ends_with(&pattern[..n]))
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn writes_are_read_whole_from_split_chunks_with_either_terminator() {
        let cases: &[(&[&[u8]], &[&str])] = &[
            (&[b"\x1b]52;c;aGVsbG8=\x07"], &["hello"]),
            (&[b"x\x1b]52;p;d29ybGQ=\x1b\\y"], &["world"]),
            (&[b"\x1b]5", b"2;c;aGk=", b"\x07"], &["hi"]),
            (&[b"\x1b]52;c;?\x07"], &[]),
            (&[b"\x1b]52;c;!!notbase64\x07"], &[]),
            (&[b"\x1b]52;c;\x07"], &[""]),
            (&[b"\x1b]52;c;aGk\x07"], &["hi"]),
            (&[b"\x1b]52;c;YQ==\x07\x1b]52;c;Yg==\x07"], &["a", "b"]),
            (&[b"\x1b]0;title\x07plain"], &[]),
        ];
        for (chunks, want) in cases {
            let mut osc = Osc52::default();
            let got: Vec<String> = chunks.iter().flat_map(|c| osc.writes(c)).collect();
            assert_eq!(got, *want, "{chunks:?}");
        }
    }
}
