//! An editor tile's params on the wire (types/desktop.ts): a JSON object with `root` and `path`
//! when the tile has a root, else the bare path of its open file, or nothing.

use serde_json::{Value, json};

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct Params {
    pub(crate) root: Option<String>,
    pub(crate) path: Option<String>,
}

impl Params {
    pub(crate) fn parse(raw: &str) -> Params {
        if raw.is_empty() {
            return Params::default();
        }
        if raw.starts_with('{')
            && let Ok(Value::Object(object)) = serde_json::from_str::<Value>(raw)
        {
            let field = |name: &str| {
                object
                    .get(name)
                    .and_then(Value::as_str)
                    .filter(|s| !s.is_empty())
                    .map(str::to_owned)
            };
            return Params {
                root: field("root"),
                path: field("path"),
            };
        }
        Params {
            root: None,
            path: Some(raw.to_owned()),
        }
    }

    pub(crate) fn wire(&self) -> String {
        match (&self.root, &self.path) {
            (Some(root), Some(path)) => json!({ "root": root, "path": path }).to_string(),
            (Some(root), None) => json!({ "root": root }).to_string(),
            (None, path) => path.clone().unwrap_or_default(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn params_read_a_root_and_a_path_or_a_bare_path_and_write_them_back_the_same_way() {
        let both = Params {
            root: Some("/w".into()),
            path: Some("a.md".into()),
        };
        assert_eq!(Params::parse(&both.wire()), both);
        assert_eq!(both.wire(), r#"{"path":"a.md","root":"/w"}"#);
        assert_eq!(
            Params::parse("notes/a.md"),
            Params {
                root: None,
                path: Some("notes/a.md".into())
            }
        );
        assert_eq!(
            Params::parse("{not json"),
            Params {
                root: None,
                path: Some("{not json".into())
            }
        );
        assert_eq!(
            Params::parse(r#"{"root":"","path":"a.md"}"#),
            Params {
                root: None,
                path: Some("a.md".into())
            }
        );
        assert_eq!(Params::parse(""), Params::default());
        assert_eq!(Params::default().wire(), "");
        assert_eq!(
            Params {
                root: None,
                path: Some("a.md".into())
            }
            .wire(),
            "a.md"
        );
    }
}
