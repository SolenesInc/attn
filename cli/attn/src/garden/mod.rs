pub mod json;
pub mod plot;
pub mod section;

use attn_protocol::go_values::{ID_ALPHABET, ID_BODY_LEN, MAX_SLUG_CHARS, MAX_SLUG_WORDS};

const ID_PREFIX: &str = "s-";
const STOP_WORDS: &[&str] = &[
    "a", "an", "the", "of", "in", "on", "at", "to", "for", "with", "by", "from", "and", "or", "as",
    "is", "into", "its", "it",
];

pub fn is_seed_id(id: &str) -> bool {
    id.strip_prefix(ID_PREFIX).is_some_and(|body| {
        i64::try_from(body.len()) == Ok(ID_BODY_LEN)
            && body.chars().all(|letter| ID_ALPHABET.contains(letter))
    })
}

pub fn step_slug(title: &str) -> String {
    let lowered = title.trim().to_lowercase();
    let words: Vec<&str> = lowered
        .split(|letter: char| !(letter.is_ascii_lowercase() || letter.is_ascii_digit()))
        .filter(|word| !word.is_empty())
        .collect();
    let mut kept: Vec<&str> = words
        .iter()
        .copied()
        .filter(|word| !STOP_WORDS.contains(word))
        .collect();
    if kept.is_empty() {
        kept = words;
    }
    kept.truncate(usize::try_from(MAX_SLUG_WORDS).unwrap_or(usize::MAX));
    let mut slug = kept.join("-");
    let limit = usize::try_from(MAX_SLUG_CHARS).unwrap_or(usize::MAX);
    if slug.chars().count() > limit {
        slug = slug.chars().take(limit).collect::<String>();
        slug = slug.trim_matches('-').to_string();
    }
    if slug.is_empty() {
        return "seed".to_string();
    }
    slug
}

pub fn valid_check_id(id: &str) -> bool {
    id.strip_prefix('C').is_some_and(|digits| {
        !digits.is_empty()
            && !digits.starts_with('0')
            && digits.bytes().all(|byte| byte.is_ascii_digit())
            && digits.parse::<u32>().is_ok()
    })
}

pub fn check_id_error(id: &str) -> String {
    format!(
        "{} is not a check id: a check id is C and a number, like C1",
        crate::go::quote::quote(id.as_bytes())
    )
}
