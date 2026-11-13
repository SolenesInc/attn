//! Renders attn's product prompts from the catalog `internal/prompts` generates, as the Tauri
//! app's `renderer.ts` does, so both apps send an agent the same bytes.

use std::collections::HashMap;
use std::sync::LazyLock;

use serde::Deserialize;

const CATALOG: &str = include_str!("../../../../app/src/prompts/catalog.generated.json");

#[derive(Deserialize)]
struct Catalog {
    version: u32,
    recipients: Vec<Recipient>,
    sources: HashMap<String, String>,
}

#[derive(Deserialize)]
struct Recipient {
    id: String,
    events: Vec<Event>,
}

#[derive(Deserialize)]
struct Event {
    id: String,
    body: Node,
}

#[derive(Deserialize)]
struct Field {
    name: String,
    kind: String,
    #[serde(default)]
    trim_space: bool,
}

#[derive(Deserialize)]
struct Condition {
    field: Field,
    test: String,
}

#[derive(Deserialize)]
struct Binding {
    name: String,
    node: Node,
}

#[derive(Deserialize)]
struct Node {
    kind: String,
    source: Option<String>,
    field: Option<Field>,
    condition: Option<Condition>,
    #[serde(default)]
    children: Vec<Node>,
    #[serde(default)]
    bindings: Vec<Binding>,
    #[serde(default)]
    keep_final_newline: bool,
    separator: Option<String>,
}

static PARSED: LazyLock<Catalog> = LazyLock::new(|| {
    let catalog: Catalog =
        serde_json::from_str(CATALOG).expect("the generated prompt catalog parses");
    assert_eq!(catalog.version, 1, "unsupported prompt catalog version");
    catalog
});

/// Go's `strings.TrimSpace`: Unicode white space, which takes in NEL and leaves the BOM.
fn trim(text: &str) -> &str {
    text.trim_matches(char::is_whitespace)
}

/// The prompt `recipient` sends on `event`, filled from `values`. A prompt or input the
/// catalog does not know is a mistake in the caller, named in the error.
pub fn render(recipient: &str, event: &str, values: &[(&str, &str)]) -> Result<String, String> {
    let catalog = &*PARSED;
    let body = catalog
        .recipients
        .iter()
        .find(|r| r.id == recipient)
        .and_then(|r| r.events.iter().find(|e| e.id == event))
        .map(|e| &e.body)
        .ok_or_else(|| format!("unknown prompt: {recipient}/{event}"))?;
    let values: HashMap<&str, &str> = values.iter().copied().collect();
    let mut known = Vec::new();
    fields(body, &values, &mut known)?;
    if let Some(name) = values.keys().find(|name| !known.contains(name)) {
        return Err(format!("unknown prompt input: {name}"));
    }
    fill(catalog, body, &values)
}

fn fields<'a>(
    node: &'a Node,
    values: &HashMap<&str, &str>,
    known: &mut Vec<&'a str>,
) -> Result<(), String> {
    if let Some(field) = &node.field {
        known.push(&field.name);
    }
    if let Some(Condition { field, .. }) = &node.condition {
        known.push(&field.name);
        if field.kind == "flag"
            && let Some(value) = values.get(field.name.as_str())
            && !matches!(*value, "true" | "false")
        {
            return Err(format!("flag {} needs true or false", field.name));
        }
    }
    for binding in &node.bindings {
        fields(&binding.node, values, known)?;
    }
    for child in &node.children {
        fields(child, values, known)?;
    }
    Ok(())
}

fn fill(catalog: &Catalog, node: &Node, values: &HashMap<&str, &str>) -> Result<String, String> {
    let child = |i: usize| {
        node.children
            .get(i)
            .ok_or_else(|| format!("a {} node without child {i}", node.kind))
    };
    match (node.kind.as_str(), &node.field, &node.condition) {
        ("input", Some(field), _) => {
            let value = values
                .get(field.name.as_str())
                .ok_or_else(|| format!("missing prompt input: {}", field.name))?;
            Ok(if field.trim_space { trim(value) } else { value }.to_owned())
        }
        (kind @ ("when" | "choose"), _, Some(condition)) => {
            let value = values
                .get(condition.field.name.as_str())
                .copied()
                .unwrap_or("");
            let selected = if condition.test == "enabled" {
                value == "true"
            } else {
                !trim(value).is_empty()
            };
            if kind == "when" && !selected {
                return Ok(String::new());
            }
            fill(catalog, child(if selected { 0 } else { 1 })?, values)
        }
        ("trim", ..) => Ok(trim(&fill(catalog, child(0)?, values)?).to_owned()),
        (kind @ ("compose" | "join"), ..) => {
            let parts = node
                .children
                .iter()
                .map(|c| fill(catalog, c, values))
                .collect::<Result<Vec<_>, _>>()?;
            Ok(if kind == "compose" {
                parts
                    .into_iter()
                    .filter(|p| !p.is_empty())
                    .collect::<Vec<_>>()
                    .join("\n\n")
            } else {
                parts.join(node.separator.as_deref().unwrap_or(""))
            })
        }
        ("text", ..) => {
            let name = node
                .source
                .as_deref()
                .ok_or("a text node without a source")?;
            let mut source = catalog
                .sources
                .get(name)
                .ok_or_else(|| format!("missing prompt source: {name}"))?
                .as_str();
            if !node.keep_final_newline {
                source = source.strip_suffix('\n').unwrap_or(source);
            }
            let bindings = node
                .bindings
                .iter()
                .map(|b| Ok((b.name.as_str(), fill(catalog, &b.node, values)?)))
                .collect::<Result<HashMap<_, _>, String>>()?;
            substitute(source, &bindings)
        }
        (kind, ..) => Err(format!("unsupported prompt node: {kind}")),
    }
}

/// `source` with each `{{name}}` replaced by its binding.
fn substitute(source: &str, bindings: &HashMap<&str, String>) -> Result<String, String> {
    let mut out = String::with_capacity(source.len());
    let mut rest = source;
    while let Some(open) = rest.find("{{") {
        let after = &rest[open + 2..];
        let name_len = after
            .char_indices()
            .take_while(|&(i, c)| {
                if i == 0 {
                    c.is_ascii_alphabetic()
                } else {
                    c.is_ascii_alphanumeric() || c == '_'
                }
            })
            .count();
        if name_len == 0 || !after[name_len..].starts_with("}}") {
            out.push_str(&rest[..open + 1]);
            rest = &rest[open + 1..];
            continue;
        }
        let name = &after[..name_len];
        out.push_str(&rest[..open]);
        out.push_str(
            bindings
                .get(name)
                .ok_or_else(|| format!("missing prompt binding: {name}"))?,
        );
        rest = &after[name_len + 2..];
    }
    out.push_str(rest);
    Ok(out)
}
