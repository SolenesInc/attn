//! A view's values: the sources the daemon read, joined as the view language says, and the
//! templates of its parts filled from them.

use std::collections::HashMap;

use attn_protocol::{ViewDocument, ViewItems, ViewTemplatePiece};
use serde_json::Value;

/// The values of a view's sources: its data, which the parts' fields name, and each file by
/// its path from the view's folder, which a part's items may name.
#[derive(Default)]
pub(crate) struct Data<'a> {
    top: Vec<(Option<&'a str>, &'a Value)>,
    files: HashMap<&'a str, &'a Value>,
    sources: HashMap<&'a str, (&'a str, &'a str)>,
}

impl<'a> Data<'a> {
    pub(crate) fn of(view: &'a ViewDocument) -> Data<'a> {
        let mut top = Vec::new();
        let mut files = HashMap::new();
        let sources = (view.sources.iter())
            .map(|s| {
                let notice = s.notice.as_deref().unwrap_or_default();
                (s.id.as_str(), (s.state.as_str(), notice))
            })
            .collect();
        for values in &view.values {
            let Some(source) = view.sources.iter().find(|s| s.id == values.source) else {
                continue;
            };
            if source.data {
                top.push((source.r#as.as_deref(), &values.values));
            }
            if source.kind == "file" {
                files.insert(source.id.as_str(), &values.values);
            }
        }
        Data {
            top,
            files,
            sources,
        }
    }

    pub(crate) fn scope(&'a self, now_ms: i64) -> Scope<'a> {
        Scope {
            data: self,
            items: Vec::new(),
            now_ms,
        }
    }

    /// The value at `path` in the view's data: the last data source that has it wins.
    fn field(&self, path: &[String]) -> Option<&'a Value> {
        self.top.iter().rev().find_map(|(name, value)| match name {
            Some(name) => match path.split_first() {
                Some((first, rest)) if first == name => at(value, rest),
                _ => None,
            },
            None => value.is_object().then(|| at(value, path)).flatten(),
        })
    }
}

/// Where a part reads its fields: the view's data, inside the items around it, innermost last.
#[derive(Clone)]
pub(crate) struct Scope<'a> {
    data: &'a Data<'a>,
    items: Vec<&'a Value>,
    now_ms: i64,
}

impl<'a> Scope<'a> {
    /// The scope inside `item`.
    pub(crate) fn within(&self, item: &'a Value) -> Scope<'a> {
        let mut inner = self.clone();
        inner.items.push(item);
        inner
    }

    /// The value at `path`: a field of the innermost item that has it, or of the view's data.
    pub(crate) fn get(&self, path: &[String]) -> Option<&'a Value> {
        (self.items.iter().rev())
            .find_map(|item| at(item, path))
            .or_else(|| self.data.field(path))
    }

    /// The items `items` names: a field's list, or a file's.
    pub(crate) fn items(&self, items: &ViewItems) -> &'a [Value] {
        let value = match (&items.file, &items.field) {
            (Some(file), _) => self.data.files.get(file.as_str()).copied(),
            (None, Some(field)) => self.get(field),
            (None, None) => None,
        };
        value.and_then(Value::as_array).map_or(&[], Vec::as_slice)
    }

    /// `template` with each field's value in its place, drawn in its format.
    pub(crate) fn text(&self, template: &[ViewTemplatePiece]) -> String {
        template
            .iter()
            .map(|piece| match (&piece.text, &piece.field) {
                (Some(text), _) => text.clone(),
                (None, Some(field)) => {
                    let value = self.get(field);
                    match piece.format.as_deref() {
                        Some("duration") => value
                            .and_then(Value::as_f64)
                            .map_or_else(String::new, duration),
                        Some("ago") => value.map_or_else(String::new, |v| ago(v, self.now_ms)),
                        _ => value.map_or_else(String::new, said),
                    }
                }
                (None, None) => String::new(),
            })
            .collect()
    }

    /// How the source `id` stands, and what it says when it is not ok.
    pub(crate) fn source(&self, id: &str) -> Option<(&'a str, String)> {
        let (state, notice) = self.data.sources.get(id)?;
        Some((state, (*notice).to_owned()))
    }

    /// The number `path` holds, read from a number or from text that is one.
    pub(crate) fn number(&self, path: &[String]) -> Option<f64> {
        match self.get(path)? {
            Value::Number(n) => n.as_f64(),
            Value::String(s) => s.trim().parse().ok(),
            _ => None,
        }
    }
}

fn at<'a>(root: &'a Value, path: &[String]) -> Option<&'a Value> {
    path.iter()
        .try_fold(root, |value, field| value.get(field.as_str()))
}

/// A value as a person reads it: text as it is, a number without a needless fraction, nothing
/// for null.
pub(crate) fn said(value: &Value) -> String {
    match value {
        Value::Null => String::new(),
        Value::String(s) => s.clone(),
        Value::Bool(b) => if *b { "yes" } else { "no" }.to_owned(),
        Value::Number(n) => match (n.as_i64(), n.as_f64()) {
            (Some(i), _) => i.to_string(),
            (None, Some(f)) => format!("{f}"),
            _ => n.to_string(),
        },
        other => other.to_string(),
    }
}

/// A number of seconds as a duration: 18s, 2m 41s, 1h 4m.
pub(crate) fn duration(seconds: f64) -> String {
    let total = seconds.max(0.0).round() as i64;
    let (h, m, s) = (total / 3600, total % 3600 / 60, total % 60);
    match (h, m) {
        (0, 0) => format!("{s}s"),
        (0, _) => format!("{m}m {s:02}s"),
        _ => format!("{h}h {m}m"),
    }
}

/// An instant, as text or as seconds since 1970, as how long ago it was.
fn ago(value: &Value, now_ms: i64) -> String {
    match value {
        Value::String(iso) => crate::garden::model::ago(iso, now_ms),
        Value::Number(n) => n.as_f64().map_or_else(String::new, |seconds| {
            crate::garden::model::ago_ms((seconds * 1000.0) as i64, now_ms)
        }),
        _ => String::new(),
    }
}
