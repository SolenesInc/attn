use serde::Serialize;
use serde_json::ser::Formatter;
use std::io::{self, Write};

const INDENT: &[u8] = b"  ";

pub fn indent(compact: &[u8]) -> Vec<u8> {
    let mut out = Vec::with_capacity(compact.len() + compact.len() / 4);
    let mut in_string = false;
    let mut escaped = false;
    let mut pending_open = false;
    let mut depth = 0;
    let mut open_containers = 0usize;
    let mut started = false;
    for &byte in compact {
        if in_string {
            out.push(byte);
            if escaped {
                escaped = false;
            } else if byte == b'\\' {
                escaped = true;
            } else if byte == b'"' {
                in_string = false;
            }
            continue;
        }
        if matches!(byte, b' ' | b'\t' | b'\r' | b'\n') {
            if started && open_containers == 0 {
                out.push(byte);
            }
            continue;
        }
        started = true;
        if pending_open && !matches!(byte, b'}' | b']') {
            pending_open = false;
            depth += 1;
            push_newline(&mut out, depth);
        }
        match byte {
            b'{' | b'[' => {
                pending_open = true;
                open_containers += 1;
                out.push(byte);
            }
            b',' => {
                out.push(byte);
                push_newline(&mut out, depth);
            }
            b':' => out.extend_from_slice(b": "),
            b'}' | b']' => {
                open_containers -= 1;
                if pending_open {
                    pending_open = false;
                } else {
                    depth -= 1;
                    push_newline(&mut out, depth);
                }
                out.push(byte);
            }
            b'"' => {
                in_string = true;
                out.push(byte);
            }
            _ => out.push(byte),
        }
    }
    out
}

fn push_newline(out: &mut Vec<u8>, depth: usize) {
    out.push(b'\n');
    for _ in 0..depth {
        out.extend_from_slice(INDENT);
    }
}

pub fn encode<T: Serialize + ?Sized>(value: &T) -> serde_json::Result<Vec<u8>> {
    let mut out = Vec::new();
    let mut serializer = serde_json::Serializer::with_formatter(&mut out, GoEscapes);
    value.serialize(&mut serializer)?;
    Ok(out)
}

struct GoEscapes;

impl Formatter for GoEscapes {
    fn write_string_fragment<W: Write + ?Sized>(
        &mut self,
        writer: &mut W,
        fragment: &str,
    ) -> io::Result<()> {
        let mut clean_start = 0;
        for (index, character) in fragment.char_indices() {
            let replacement = match character {
                '<' => "\\u003c",
                '>' => "\\u003e",
                '&' => "\\u0026",
                '\u{2028}' => "\\u2028",
                '\u{2029}' => "\\u2029",
                _ => continue,
            };
            writer.write_all(&fragment.as_bytes()[clean_start..index])?;
            writer.write_all(replacement.as_bytes())?;
            clean_start = index + character.len_utf8();
        }
        writer.write_all(&fragment.as_bytes()[clean_start..])
    }
}
