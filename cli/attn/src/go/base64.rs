use std::fmt;

const PADDING: u8 = b'=';

#[derive(Debug, PartialEq, Eq)]
pub struct CorruptInput(pub usize);

impl fmt::Display for CorruptInput {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "illegal base64 data at input byte {}", self.0)
    }
}

fn sextet(byte: u8) -> Option<u8> {
    match byte {
        b'A'..=b'Z' => Some(byte - b'A'),
        b'a'..=b'z' => Some(byte - b'a' + 26),
        b'0'..=b'9' => Some(byte - b'0' + 52),
        b'+' => Some(62),
        b'/' => Some(63),
        _ => None,
    }
}

fn skip_newlines(input: &[u8], mut at: usize) -> usize {
    while at < input.len() && (input[at] == b'\n' || input[at] == b'\r') {
        at += 1;
    }
    at
}

fn check_padding(input: &[u8], read: usize, filled: usize) -> Result<(), CorruptInput> {
    let mut at = read;
    if filled == 2 {
        at = skip_newlines(input, at);
        if at == input.len() {
            return Err(CorruptInput(at));
        }
        if input[at] != PADDING {
            return Err(CorruptInput(at - 1));
        }
        at += 1;
    }
    at = skip_newlines(input, at);
    if at < input.len() {
        return Err(CorruptInput(at));
    }
    Ok(())
}

pub fn decode(text: &str) -> Result<Vec<u8>, CorruptInput> {
    let input = text.as_bytes();
    let mut out = Vec::with_capacity(input.len() / 4 * 3);
    let mut at = 0;
    let mut quantum = [0_u8; 4];
    let mut filled = 0;
    loop {
        at = skip_newlines(input, at);
        if at == input.len() {
            if filled != 0 {
                return Err(CorruptInput(at - filled));
            }
            return Ok(out);
        }
        let byte = input[at];
        at += 1;
        if let Some(value) = sextet(byte) {
            quantum[filled] = value;
            filled += 1;
            if filled == 4 {
                out.push(quantum[0] << 2 | quantum[1] >> 4);
                out.push(quantum[1] << 4 | quantum[2] >> 2);
                out.push(quantum[2] << 6 | quantum[3]);
                filled = 0;
            }
            continue;
        }
        if byte != PADDING || filled < 2 {
            return Err(CorruptInput(at - 1));
        }
        check_padding(input, at, filled)?;
        out.push(quantum[0] << 2 | quantum[1] >> 4);
        if filled == 3 {
            out.push(quantum[1] << 4 | quantum[2] >> 2);
        }
        return Ok(out);
    }
}
