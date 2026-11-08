use attn_protocol::go_values::GO_PRINTABLE_RANGES;
use std::fmt::Write;

pub fn quote(input: &[u8]) -> String {
    let mut out = String::with_capacity(input.len() + 2);
    out.push('"');
    for chunk in input.utf8_chunks() {
        for character in chunk.valid().chars() {
            push_escaped(&mut out, character);
        }
        for byte in chunk.invalid() {
            write!(out, "\\x{byte:02x}").expect("writing to a String cannot fail");
        }
    }
    out.push('"');
    out
}

fn push_escaped(out: &mut String, character: char) {
    match character {
        '"' => out.push_str("\\\""),
        '\\' => out.push_str("\\\\"),
        printable if is_printable(printable) => out.push(printable),
        '\u{07}' => out.push_str("\\a"),
        '\u{08}' => out.push_str("\\b"),
        '\u{0c}' => out.push_str("\\f"),
        '\n' => out.push_str("\\n"),
        '\r' => out.push_str("\\r"),
        '\t' => out.push_str("\\t"),
        '\u{0b}' => out.push_str("\\v"),
        other => {
            let code = u32::from(other);
            if code < 0x20 || code == 0x7f {
                write!(out, "\\x{code:02x}")
            } else if code < 0x1_0000 {
                write!(out, "\\u{code:04x}")
            } else {
                write!(out, "\\U{code:08x}")
            }
            .expect("writing to a String cannot fail");
        }
    }
}

fn is_printable(character: char) -> bool {
    let code = u32::from(character);
    GO_PRINTABLE_RANGES
        .binary_search_by(|&(low, high)| {
            if code < low {
                std::cmp::Ordering::Greater
            } else if code > high {
                std::cmp::Ordering::Less
            } else {
                std::cmp::Ordering::Equal
            }
        })
        .is_ok()
}
