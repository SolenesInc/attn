use crate::go_values::{ID_ALPHABET, ID_BODY_LEN, SESSION_SHORT_ID_PREFIX};
use std::borrow::Cow;

const DIGIT_BITS: u32 = 5;
const KEY_HEX: usize = 8;
#[allow(clippy::cast_possible_truncation, clippy::cast_sign_loss)]
const SPARE_HEX_BITS: u32 = 32 - DIGIT_BITS * ID_BODY_LEN as u32;
const UUID_SHAPE: &str = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx";

#[must_use]
pub fn short(id: &str) -> Cow<'_, str> {
    if id.len() != UUID_SHAPE.len() {
        return Cow::Borrowed(id);
    }
    match key_of(id) {
        Some(key) => Cow::Owned(format!("{SESSION_SHORT_ID_PREFIX}{}", encode(key))),
        None => Cow::Borrowed(id),
    }
}

#[must_use]
pub fn key_of(reference: &str) -> Option<u32> {
    if let Some(body) = reference.strip_prefix(SESSION_SHORT_ID_PREFIX) {
        return decode(body);
    }
    if reference.len() < KEY_HEX || !starts_uuid(reference) {
        return None;
    }
    let bits = u32::from_str_radix(&reference[..KEY_HEX], 16).ok()?;
    Some(bits >> SPARE_HEX_BITS)
}

fn starts_uuid(reference: &str) -> bool {
    reference.len() <= UUID_SHAPE.len()
        && reference
            .bytes()
            .zip(UUID_SHAPE.bytes())
            .all(|(byte, shape)| match shape {
                b'-' => byte == b'-',
                _ => matches!(byte, b'0'..=b'9' | b'a'..=b'f'),
            })
}

fn encode(key: u32) -> String {
    let digits = ID_ALPHABET.as_bytes();
    (0..ID_BODY_LEN)
        .rev()
        .map(|place| {
            let shift = u32::try_from(place).unwrap_or_default() * DIGIT_BITS;
            let digit = (key >> shift) as usize % digits.len();
            char::from(digits[digit])
        })
        .collect()
}

fn decode(body: &str) -> Option<u32> {
    if i64::try_from(body.len()) != Ok(ID_BODY_LEN) {
        return None;
    }
    body.bytes().try_fold(0_u32, |key, byte| {
        let digit = ID_ALPHABET.bytes().position(|known| known == byte)?;
        Some((key << DIGIT_BITS) | u32::try_from(digit).ok()?)
    })
}
