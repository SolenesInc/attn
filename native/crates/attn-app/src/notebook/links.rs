//! Where a link in a note goes, as the Tauri notebook resolves it (linkResolver.ts). The daemon
//! reads every path relative to the root, so a link relative to its note is resolved here.

use percent_encoding::percent_decode_str;

#[derive(Debug, PartialEq, Eq)]
pub(crate) enum NoteLink {
    Note {
        path: String,
        anchor: Option<String>,
    },
    Fragment(String),
    External(String),
}

/// The note folder of `note`: everything before its last `/`.
pub(crate) fn note_dir(note: &str) -> &str {
    note.rfind('/').map_or("", |i| &note[..i])
}

/// What `href` in a note in folder `dir` points at. A leading `/` starts from the root, `..`
/// stops at the root, and a link with a scheme or `//` leaves the notebook.
pub(crate) fn resolve(href: &str, dir: &str) -> NoteLink {
    let href = href.trim();
    if href.is_empty() {
        return NoteLink::External(String::new());
    }
    if let Some(anchor) = href.strip_prefix('#') {
        return NoteLink::Fragment(decoded(anchor));
    }
    if href.starts_with("//") || has_scheme(href) {
        return NoteLink::External(href.to_owned());
    }
    let (path, anchor) = match href.split_once('#') {
        Some((path, anchor)) => (path, Some(decoded(anchor))),
        None => (href, None),
    };
    let path = path.split_once('?').map_or(path, |(p, _)| p);
    let path = match path.strip_prefix('/') {
        Some(rooted) => joined("", rooted),
        None => joined(dir, path),
    };
    if path.is_empty() {
        return NoteLink::External(String::new());
    }
    NoteLink::Note { path, anchor }
}

/// A heading's anchor: lowercase, only letters, digits, spaces and dashes, spaces as dashes.
pub(crate) fn heading_slug(text: &str) -> String {
    let kept: String = text
        .to_lowercase()
        .chars()
        .filter(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || *c == ' ' || *c == '-')
        .collect();
    kept.split_whitespace().collect::<Vec<_>>().join("-")
}

fn has_scheme(href: &str) -> bool {
    let Some((scheme, _)) = href.split_once(':') else {
        return false;
    };
    let mut chars = scheme.chars();
    chars.next().is_some_and(|c| c.is_ascii_alphabetic())
        && chars.all(|c| c.is_ascii_alphanumeric() || matches!(c, '+' | '.' | '-'))
}

fn decoded(text: &str) -> String {
    percent_decode_str(text)
        .decode_utf8()
        .map_or_else(|_| text.to_owned(), |t| t.into_owned())
}

fn joined(dir: &str, path: &str) -> String {
    let mut parts: Vec<&str> = Vec::new();
    for segment in dir.split('/').chain(path.split('/')) {
        match segment {
            "" | "." => {}
            ".." => {
                parts.pop();
            }
            s => parts.push(s),
        }
    }
    parts.join("/")
}

#[cfg(test)]
mod tests {
    use super::*;

    fn note(path: &str, anchor: Option<&str>) -> NoteLink {
        NoteLink::Note {
            path: path.into(),
            anchor: anchor.map(Into::into),
        }
    }

    #[test]
    fn a_link_resolves_from_its_note_or_the_root_and_leaves_the_notebook_with_a_scheme() {
        let cases = [
            (
                "foo.md",
                "knowledge/areas",
                note("knowledge/areas/foo.md", None),
            ),
            (
                "foo.md",
                note_dir("knowledge/areas/note.md"),
                note("knowledge/areas/foo.md", None),
            ),
            ("foo.md", note_dir("note.md"), note("foo.md", None)),
            ("./foo.md", "knowledge", note("knowledge/foo.md", None)),
            (
                "../bar.md",
                "knowledge/areas",
                note("knowledge/bar.md", None),
            ),
            ("../../../x.md", "a", note("x.md", None)),
            ("/abs.md", "knowledge/areas", note("abs.md", None)),
            (
                "/knowledge/areas/foo.md",
                "",
                note("knowledge/areas/foo.md", None),
            ),
            (
                "foo.md?x=1#sec",
                "knowledge/areas",
                note("knowledge/areas/foo.md", Some("sec")),
            ),
            ("/knowledge/foo.md?v=2", "", note("knowledge/foo.md", None)),
            (
                "/knowledge/foo.md#Sec%20One",
                "",
                note("knowledge/foo.md", Some("Sec One")),
            ),
            (
                "#Sec%20One",
                "knowledge/areas",
                NoteLink::Fragment("Sec One".into()),
            ),
            (
                "https://example.com",
                "",
                NoteLink::External("https://example.com".into()),
            ),
            (
                "http://example.com",
                "knowledge",
                NoteLink::External("http://example.com".into()),
            ),
            (
                "//example.com/x",
                "",
                NoteLink::External("//example.com/x".into()),
            ),
            (
                "mailto:someone@example.com",
                "",
                NoteLink::External("mailto:someone@example.com".into()),
            ),
            (
                "file:///etc/hosts",
                "",
                NoteLink::External("file:///etc/hosts".into()),
            ),
            ("", "knowledge/areas", NoteLink::External(String::new())),
            ("   ", "knowledge/areas", NoteLink::External(String::new())),
        ];
        for (href, dir, want) in cases {
            assert_eq!(resolve(href, dir), want, "{href:?} from {dir:?}");
        }
    }

    #[test]
    fn a_heading_slug_keeps_letters_digits_and_dashes() {
        for (heading, slug) in [
            ("My Heading!", "my-heading"),
            ("A   B", "a-b"),
            ("  Setup: step 2  ", "setup-step-2"),
        ] {
            assert_eq!(heading_slug(heading), slug, "{heading:?}");
        }
    }
}
