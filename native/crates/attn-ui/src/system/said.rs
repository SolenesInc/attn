use std::borrow::Cow;

/// Text a block draws, and whether it is read for markup. Copy written into the source, a
/// `&'static str`, sets what stands between backticks in `Mono`. Text built while the app runs,
/// such as a name, a path or an error, is drawn exactly as written. A line that puts a value
/// in `Mono` among words is built from [`Part`]s.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Said(Kind);

#[derive(Clone, Debug, PartialEq, Eq)]
enum Kind {
    Copy(&'static str),
    Data(String),
    Parts(Vec<Part>),
}

/// A piece of a line built from parts: words, or a value set in `Mono`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Part {
    Text(String),
    Code(String),
}

impl Said {
    /// A line of `parts`, each drawn as written.
    pub fn parts(parts: impl IntoIterator<Item = Part>) -> Said {
        Said(Kind::Parts(parts.into_iter().collect()))
    }

    /// The text as it is shown, and the byte ranges of it set in `Mono`.
    pub(crate) fn shown(&self) -> (Cow<'_, str>, Vec<(usize, usize)>) {
        match &self.0 {
            Kind::Copy(copy) if copy.contains('`') => {
                let (shown, code) = marked(copy);
                (Cow::Owned(shown), code)
            }
            Kind::Copy(copy) => (Cow::Borrowed(copy), Vec::new()),
            Kind::Data(data) => (Cow::Borrowed(data), Vec::new()),
            Kind::Parts(parts) => {
                let mut shown = String::new();
                let mut code = Vec::new();
                for part in parts {
                    match part {
                        Part::Text(text) => shown.push_str(text),
                        Part::Code(value) => {
                            let start = shown.len();
                            shown.push_str(value);
                            code.push((start, shown.len()));
                        }
                    }
                }
                (Cow::Owned(shown), code)
            }
        }
    }

    /// This text, then `code` set in `Mono` after a space, `code` read exactly as given.
    pub fn then_code(&self, code: impl Into<String>) -> Said {
        let (shown, ranges) = self.shown();
        let mut parts = Vec::new();
        let mut at = 0;
        for (start, end) in ranges {
            parts.push(Part::Text(shown[at..start].to_owned()));
            parts.push(Part::Code(shown[start..end].to_owned()));
            at = end;
        }
        parts.push(Part::Text(format!("{} ", &shown[at..])));
        parts.push(Part::Code(code.into()));
        Said::parts(parts)
    }

    /// The text as it is shown, which is how assistive tools read it.
    pub fn text(&self) -> String {
        self.shown().0.into_owned()
    }

    pub fn is_empty(&self) -> bool {
        self.shown().0.is_empty()
    }
}

fn marked(copy: &str) -> (String, Vec<(usize, usize)>) {
    let mut shown = String::with_capacity(copy.len());
    let mut code = Vec::new();
    for (i, part) in copy.split('`').enumerate() {
        let start = shown.len();
        shown.push_str(part);
        if i % 2 == 1 && !part.is_empty() {
            code.push((start, shown.len()));
        }
    }
    (shown, code)
}

impl From<&'static str> for Said {
    fn from(copy: &'static str) -> Said {
        Said(Kind::Copy(copy))
    }
}

impl From<String> for Said {
    fn from(data: String) -> Said {
        Said(Kind::Data(data))
    }
}

impl From<&String> for Said {
    fn from(data: &String) -> Said {
        Said(Kind::Data(data.clone()))
    }
}

impl Default for Said {
    fn default() -> Said {
        Said(Kind::Copy(""))
    }
}
