//! A document's YAML front matter as the Tauri reader shows it (frontmatter.ts,
//! FrontmatterCard): a card of its top-level `key: value` lines, lists as tags.

use attn_ui::Tokens;
use rune::tok::{radius, space};
use rune::{El, TextStyle, col, el, row, text};

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

const SMALL: TextStyle = TextStyle::new(14.0, 400.0);
const KEY: TextStyle = TextStyle::new(14.0, 500.0);
const TAG: TextStyle = TextStyle::new(12.0, 400.0);
const KEY_WIDTH: f32 = 80.0;

/// The card `yaml` shows as; nothing for front matter with no top-level keys.
pub(crate) fn card(yaml: &str, t: &Tokens) -> El {
    let entries = entries(yaml);
    if entries.is_empty() {
        return el().w_full().h(0.0);
    }
    col()
        .key("front-matter")
        .role("note")
        .label("Front matter")
        .w_full()
        .mt(0.0)
        .mb(space::S6)
        .padding(16.0)
        .gap(space::S2)
        .radius(radius::MD)
        .fill(t.bg_elevated.alpha(0.3))
        .border(1.0, t.border.alpha(0.5))
        .children(entries.into_iter().map(|(key, value)| {
            let value = match value {
                Value::Text(value) => text(value).font(SMALL).color(t.text_primary).shrink(),
                Value::List(items) => {
                    row()
                        .wrap()
                        .gap(space::S1)
                        .shrink()
                        .children(items.into_iter().map(|item| {
                            el().px(6.0)
                                .py(2.0)
                                .radius(4.0)
                                .fill(t.accent.alpha(0.1))
                                .child(text(item).font(TAG).color(t.accent))
                        }))
                }
            };
            row()
                .w_full()
                .gap(space::S2)
                .child(
                    text(format!("{key}:"))
                        .font(KEY)
                        .color(t.text_secondary)
                        .min_w(KEY_WIDTH),
                )
                .child(value)
        }))
}
