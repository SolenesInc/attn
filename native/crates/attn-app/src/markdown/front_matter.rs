//! A document's YAML front matter: a card of its top-level `key: value` lines, lists as tags.

use attn_ui::Tokens;
use attn_ui::blocks::{Facts, Inline, Surface};
use attn_ui::system::{Tone, tag};
use rune::{El, el};

#[derive(Debug, PartialEq, Eq)]
pub(crate) enum Value {
    Text(String),
    List(Vec<String>),
}

/// The top-level `key: value` lines of `yaml`. A value in `[a, b]` or followed by indented
/// `- item` lines is a list; quotes around a value or an item go; nested objects, comments
/// and blank lines are left out.
pub(crate) fn entries(yaml: &str) -> Vec<(String, Value)> {
    let mut entries = Vec::new();
    let mut pending: Option<(String, Vec<String>)> = None;
    let flush = |pending: &mut Option<(String, Vec<String>)>, entries: &mut Vec<_>| {
        if let Some((key, items)) = pending.take()
            && !items.is_empty()
        {
            entries.push((key, Value::List(items)));
        }
    };
    for line in yaml.lines().map(|l| l.strip_suffix('\r').unwrap_or(l)) {
        let trimmed = line.trim();
        if trimmed.is_empty() || trimmed.starts_with('#') {
            continue;
        }
        if let Some((_, items)) = pending.as_mut()
            && let Some(item) = dash_item(line)
        {
            items.push(unquoted(item).to_owned());
            continue;
        }
        flush(&mut pending, &mut entries);
        let Some((key, value)) = keyed(line) else {
            continue;
        };
        if value.is_empty() {
            pending = Some((key.to_owned(), Vec::new()));
            continue;
        }
        let value = match inline_list(value) {
            Some(items) => Value::List(items),
            None => Value::Text(unquoted(value).to_owned()),
        };
        entries.push((key.to_owned(), value));
    }
    flush(&mut pending, &mut entries);
    entries
}

/// `  - item` as `item`: an indented dash and space, then the item.
fn dash_item(line: &str) -> Option<&str> {
    let rest = line.trim_start();
    if rest.len() == line.len() {
        return None;
    }
    let item = rest.strip_prefix('-')?;
    let after = item.trim_start();
    (after.len() < item.len()).then(|| after.trim())
}

/// `key: value` at the start of a line, its key letters, digits, `_` and `-`.
fn keyed(line: &str) -> Option<(&str, &str)> {
    let (key, value) = line.split_once(':')?;
    let key = key.trim_end();
    let named = !key.is_empty()
        && key
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '-');
    named.then(|| (key, value.trim()))
}

fn inline_list(value: &str) -> Option<Vec<String>> {
    let inner = value.strip_prefix('[')?.strip_suffix(']')?.trim();
    if inner.is_empty() {
        return Some(Vec::new());
    }
    Some(
        inner
            .split(',')
            .map(|item| unquoted(item.trim()).to_owned())
            .filter(|item| !item.is_empty())
            .collect(),
    )
}

fn unquoted(value: &str) -> &str {
    ['"', '\'']
        .into_iter()
        .find_map(|q| {
            value
                .strip_prefix(q)
                .and_then(|v| v.strip_suffix(q))
                .filter(|_| value.len() >= 2)
        })
        .unwrap_or(value)
}

/// The card `yaml` shows as; nothing for front matter with no top-level keys.
pub(crate) fn card(yaml: &str, t: &Tokens) -> El {
    let entries = entries(yaml);
    if entries.is_empty() {
        return el().w_full().h(0.0);
    }
    let facts = entries.into_iter().enumerate().fold(
        Facts::new("front-matter.fields"),
        |facts, (n, (key, value))| {
            let at = format!("front-matter.{n}");
            match value {
                Value::Text(value) => facts.fact(at, key, value),
                Value::List(items) => {
                    let tags = items.iter().enumerate().map(|(i, item)| {
                        El::from(tag(format!("{at}.{i}"), item, Tone::Neutral, t))
                    });
                    facts.item(at.clone(), key, Inline::tight().children(tags))
                }
            }
        },
    );
    Surface::figure(facts.el(t), t.bg_elevated.alpha(0.3), t.border.alpha(0.5))
        .key("front-matter")
        .role("note")
        .label("Front matter")
        .w_full()
}
