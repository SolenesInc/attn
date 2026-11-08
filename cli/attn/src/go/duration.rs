use std::fmt::Write;

const NANOS_PER_SECOND: u64 = 1_000_000_000;
const LARGEST_MAGNITUDE: u64 = 1 << 63;

pub fn parse(input: &str) -> Result<i64, String> {
    let invalid = || format!("time: invalid duration {}", time_quote(input));
    let (negative, mut rest) = match input.as_bytes().first() {
        Some(b'-') => (true, &input[1..]),
        Some(b'+') => (false, &input[1..]),
        _ => (false, input),
    };
    if rest == "0" {
        return Ok(0);
    }
    if rest.is_empty() {
        return Err(invalid());
    }
    let mut total: u64 = 0;
    while !rest.is_empty() {
        if !rest.starts_with(|c: char| c == '.' || c.is_ascii_digit()) {
            return Err(invalid());
        }
        let (whole, after_whole) = leading_int(rest).ok_or_else(invalid)?;
        let had_whole = after_whole.len() != rest.len();
        rest = after_whole;
        let mut fraction = 0;
        let mut scale = 1.0;
        let mut had_fraction = false;
        if let Some(after_dot) = rest.strip_prefix('.') {
            let (digits, digits_scale, after_digits) = leading_fraction(after_dot);
            had_fraction = after_digits.len() != after_dot.len();
            fraction = digits;
            scale = digits_scale;
            rest = after_digits;
        }
        if !had_whole && !had_fraction {
            return Err(invalid());
        }
        let unit_end = rest
            .find(|c: char| c == '.' || c.is_ascii_digit())
            .unwrap_or(rest.len());
        if unit_end == 0 {
            return Err(format!(
                "time: missing unit in duration {}",
                time_quote(input)
            ));
        }
        let unit_text = &rest[..unit_end];
        rest = &rest[unit_end..];
        let unit = unit_nanos(unit_text).ok_or_else(|| {
            format!(
                "time: unknown unit {} in duration {}",
                time_quote(unit_text),
                time_quote(input)
            )
        })?;
        if whole > LARGEST_MAGNITUDE / unit {
            return Err(invalid());
        }
        let mut amount = whole * unit;
        if fraction > 0 {
            amount += fraction_nanos(fraction, unit, scale);
            if amount > LARGEST_MAGNITUDE {
                return Err(invalid());
            }
        }
        total += amount;
        if total > LARGEST_MAGNITUDE {
            return Err(invalid());
        }
    }
    if negative {
        return Ok(0i64.wrapping_sub_unsigned(total));
    }
    i64::try_from(total).map_err(|_| invalid())
}

#[allow(
    clippy::cast_precision_loss,
    clippy::cast_possible_truncation,
    clippy::cast_sign_loss
)]
fn fraction_nanos(fraction: u64, unit: u64, scale: f64) -> u64 {
    (fraction as f64 * (unit as f64 / scale)) as u64
}

fn unit_nanos(unit: &str) -> Option<u64> {
    match unit {
        "ns" => Some(1),
        "us" | "µs" | "μs" => Some(1_000),
        "ms" => Some(1_000_000),
        "s" => Some(NANOS_PER_SECOND),
        "m" => Some(60 * NANOS_PER_SECOND),
        "h" => Some(3600 * NANOS_PER_SECOND),
        _ => None,
    }
}

fn leading_int(input: &str) -> Option<(u64, &str)> {
    let mut value: u64 = 0;
    let mut consumed = 0;
    for byte in input.bytes().take_while(u8::is_ascii_digit) {
        if value > LARGEST_MAGNITUDE / 10 {
            return None;
        }
        value = value * 10 + u64::from(byte - b'0');
        if value > LARGEST_MAGNITUDE {
            return None;
        }
        consumed += 1;
    }
    Some((value, &input[consumed..]))
}

fn leading_fraction(input: &str) -> (u64, f64, &str) {
    let mut value: u64 = 0;
    let mut scale = 1.0;
    let mut overflowed = false;
    let mut consumed = 0;
    for byte in input.bytes().take_while(u8::is_ascii_digit) {
        consumed += 1;
        if overflowed {
            continue;
        }
        if value > (LARGEST_MAGNITUDE - 1) / 10 {
            overflowed = true;
            continue;
        }
        let next = value * 10 + u64::from(byte - b'0');
        if next > LARGEST_MAGNITUDE {
            overflowed = true;
            continue;
        }
        value = next;
        scale *= 10.0;
    }
    (value, scale, &input[consumed..])
}

pub fn format(nanos: i64) -> String {
    if nanos == 0 {
        return "0s".to_owned();
    }
    let magnitude = nanos.unsigned_abs();
    let sign = if nanos < 0 { "-" } else { "" };
    if magnitude < NANOS_PER_SECOND {
        let (digits, unit) = match magnitude {
            0..1_000 => (0, "ns"),
            1_000..1_000_000 => (3, "µs"),
            _ => (6, "ms"),
        };
        let (whole, fraction) = split_fraction(magnitude, digits);
        return format!("{sign}{whole}{fraction}{unit}");
    }
    let (whole_seconds, fraction) = split_fraction(magnitude, 9);
    let seconds = whole_seconds % 60;
    let minutes = whole_seconds / 60;
    if minutes == 0 {
        return format!("{sign}{seconds}{fraction}s");
    }
    let hours = minutes / 60;
    let minutes = minutes % 60;
    if hours == 0 {
        return format!("{sign}{minutes}m{seconds}{fraction}s");
    }
    format!("{sign}{hours}h{minutes}m{seconds}{fraction}s")
}

fn split_fraction(value: u64, digits: u32) -> (u64, String) {
    let divisor = 10u64.pow(digits);
    let remainder = value % divisor;
    if remainder == 0 {
        return (value / divisor, String::new());
    }
    let padded = format!("{remainder:0width$}", width = digits as usize);
    (
        value / divisor,
        format!(".{}", padded.trim_end_matches('0')),
    )
}

pub(crate) fn time_quote(input: &str) -> String {
    let mut out = String::with_capacity(input.len() + 2);
    out.push('"');
    for character in input.chars() {
        if (' '..'\u{80}').contains(&character) {
            if matches!(character, '"' | '\\') {
                out.push('\\');
            }
            out.push(character);
        } else {
            let mut encoded = [0; 4];
            for byte in character.encode_utf8(&mut encoded).bytes() {
                write!(out, "\\x{byte:02x}").expect("writing to a String cannot fail");
            }
        }
    }
    out.push('"');
    out
}
