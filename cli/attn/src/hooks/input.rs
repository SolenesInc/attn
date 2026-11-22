use serde::Deserialize;
use serde_json::value::RawValue;
use std::collections::HashMap;

pub type Raw<'a> = Option<&'a RawValue>;

#[derive(Default)]
pub struct HookInput<'a> {
    fields: HashMap<String, &'a RawValue>,
}

impl<'a> HookInput<'a> {
    pub fn parse(bytes: &'a [u8]) -> Self {
        let fields = serde_json::from_slice(bytes).unwrap_or_default();
        HookInput { fields }
    }

    pub fn field(&self, name: &str) -> Raw<'a> {
        self.fields.get(name).copied()
    }
}

pub fn array_items(raw: Raw<'_>) -> Vec<&RawValue> {
    let Some(raw) = raw else {
        return Vec::new();
    };
    if !raw.get().starts_with('[') {
        return Vec::new();
    }
    serde_json::from_str(raw.get()).unwrap_or_default()
}

pub fn parse_object<'a, T: Deserialize<'a>>(raw: Raw<'a>) -> Option<T> {
    let raw = raw?;
    if !raw.get().starts_with('{') {
        return None;
    }
    serde_json::from_str(raw.get()).ok()
}

pub fn text(raw: Raw<'_>) -> String {
    string_value(raw).unwrap_or_default()
}

pub fn string_value(raw: Raw<'_>) -> Option<String> {
    let body = raw?.get().strip_prefix('"')?.strip_suffix('"')?;
    Some(unquote(body))
}

pub fn strict_text(raw: Raw<'_>) -> Option<String> {
    match raw {
        None => Some(String::new()),
        Some(value) if value.get() == "null" => Some(String::new()),
        Some(_) => string_value(raw),
    }
}

fn unquote(body: &str) -> String {
    let mut out = String::with_capacity(body.len());
    let mut chars = body.chars();
    while let Some(ch) = chars.next() {
        if ch != '\\' {
            out.push(ch);
            continue;
        }
        match chars.next() {
            Some('n') => out.push('\n'),
            Some('t') => out.push('\t'),
            Some('r') => out.push('\r'),
            Some('b') => out.push('\u{8}'),
            Some('f') => out.push('\u{c}'),
            Some('u') => out.push(unicode_escape(&mut chars)),
            Some(other) => out.push(other),
            None => {}
        }
    }
    out
}

fn hex4(chars: &mut std::str::Chars<'_>) -> u32 {
    (0..4).fold(0, |value, _| {
        value * 16 + chars.next().and_then(|c| c.to_digit(16)).unwrap_or(0)
    })
}

fn unicode_escape(chars: &mut std::str::Chars<'_>) -> char {
    let first = hex4(chars);
    if !(0xD800..0xDC00).contains(&first) {
        return char::from_u32(first).unwrap_or('\u{FFFD}');
    }
    let mut lookahead = chars.clone();
    if lookahead.next() == Some('\\') && lookahead.next() == Some('u') {
        let second = hex4(&mut lookahead);
        if (0xDC00..0xE000).contains(&second) {
            *chars = lookahead;
            let combined = 0x10000 + ((first - 0xD800) << 10) + (second - 0xDC00);
            return char::from_u32(combined).unwrap_or('\u{FFFD}');
        }
    }
    '\u{FFFD}'
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_lone_surrogate_keeps_the_rest_of_the_input() {
        let input = HookInput::parse(br#"{"prompt":"bad \ud83d end","session_id":"s1"}"#);
        assert_eq!(text(input.field("prompt")), "bad \u{FFFD} end");
        assert_eq!(text(input.field("session_id")), "s1");
    }

    #[test]
    fn a_wrong_field_type_keeps_the_other_fields() {
        let input = HookInput::parse(br#"{"prompt":7,"session_id":"s1"}"#);
        assert_eq!(text(input.field("prompt")), "");
        assert_eq!(text(input.field("session_id")), "s1");
    }
}
