//! Where a document's links go, and which of them the reader may follow, as the Tauri reader
//! decides (markdownLinks.ts): a fragment scrolls the document, a web or mail link opens in its
//! handler, a local file opens with the system's opener when its kind is safe, and a file on an
//! outpost opens in a tile when it is markdown.

use std::path::Path;

use attn_protocol::OpenMarkdownMessage;
use percent_encoding::percent_decode_str;
use rune::Tx;

use crate::State;
use crate::requests::Answer;

#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum Target {
    Fragment(String),
    External(String),
    Local(String),
}

const SAFE_DOCUMENTS: [&str; 6] = ["md", "markdown", "pdf", "rst", "text", "txt"];
const SAFE_IMAGES: [&str; 8] = ["bmp", "gif", "jpeg", "jpg", "png", "tif", "tiff", "webp"];

/// What `href` in the document at `document` points at; `None` for a link the reader shows as
/// text: an unknown scheme, a link to another host, or a relative link with no document.
pub(crate) fn resolve(document: &str, href: &str) -> Option<Target> {
    let href = href.trim();
    if href.is_empty() || href.starts_with("//") {
        return None;
    }
    if let Some(fragment) = href.strip_prefix('#') {
        return Some(Target::Fragment(fragment.to_owned()));
    }
    if let Some(scheme) = scheme(href) {
        return match scheme.to_ascii_lowercase().as_str() {
            "http" | "https" | "mailto" => Some(Target::External(href.to_owned())),
            "file" => file_url_path(&href[scheme.len() + 1..]).map(Target::Local),
            _ => None,
        };
    }
    if document.is_empty() {
        return None;
    }
    let relative = decoded(path_part(href));
    let joined = if relative.starts_with('/') {
        relative
    } else {
        let dir = document.rsplit_once('/').map_or("", |(dir, _)| dir);
        format!("{dir}/{relative}")
    };
    Some(Target::Local(normalized(&joined)))
}

/// Whether the system's opener may open the local file at `path`: a document or an image, never
/// something it would run.
pub(crate) fn is_safe_local(path: &str) -> bool {
    extension(path)
        .is_some_and(|e| SAFE_DOCUMENTS.contains(&e.as_str()) || SAFE_IMAGES.contains(&e.as_str()))
}

pub(crate) fn is_image(path: &str) -> bool {
    extension(path).is_some_and(|e| SAFE_IMAGES.contains(&e.as_str()))
}

pub(crate) fn is_markdown(path: &str) -> bool {
    extension(path).is_some_and(|e| e == "md" || e == "markdown")
}

/// What the pointer's tooltip says of a link, and whether the reader follows it.
pub(crate) struct Verdict {
    pub(crate) help: Option<String>,
    pub(crate) follows: bool,
}

/// How the reader treats `target` in a document on `outpost` (named `outpost_name`), or on this
/// machine when `None`.
pub(crate) fn verdict(target: Option<&Target>, outpost: Option<&str>) -> Verdict {
    let follows = |help: Option<String>| Verdict {
        help,
        follows: true,
    };
    let shown = |help: Option<String>| Verdict {
        help,
        follows: false,
    };
    match (target, outpost) {
        (None, _) => shown(None),
        (Some(Target::Fragment(_) | Target::External(_)), _) => follows(None),
        (Some(Target::Local(path)), None) if is_safe_local(path) => follows(Some(path.clone())),
        (Some(Target::Local(path)), None) => shown(Some(format!("Blocked local target: {path}"))),
        (Some(Target::Local(path)), Some(name)) if is_markdown(path) => {
            follows(Some(format!("{path} on {name}")))
        }
        (Some(Target::Local(path)), Some(name)) => shown(Some(format!(
            "{path} is on {name}; only markdown files open from there"
        ))),
    }
}

/// The document a link belongs to: its file, the session its tile was opened for, the outpost
/// it is on, and the scroll view whose headings a fragment names.
#[derive(Clone)]
pub(crate) struct Document {
    pub(crate) path: String,
    pub(crate) session: Option<String>,
    pub(crate) outpost: Option<Outpost>,
    pub(crate) view: String,
}

#[derive(Clone)]
pub(crate) struct Outpost {
    pub(crate) host: String,
    pub(crate) name: String,
}

/// Follows the link `href` pressed in `document`.
pub(crate) fn follow(state: &mut State, document: &Document, href: &str, tx: &mut Tx) {
    let target = resolve(&document.path, href);
    let outpost = document.outpost.as_ref();
    if !verdict(target.as_ref(), outpost.map(|o| o.name.as_str())).follows {
        return;
    }
    match (target, outpost) {
        (Some(Target::Fragment(fragment)), _) => {
            let id = decoded(&fragment);
            tx.scroll_key_to(format!("{}/{id}", document.view), 0.0);
        }
        (Some(Target::External(url)), _) => {
            if let Err(why) = tx.open_url(&url) {
                state
                    .window
                    .notify(format!("Could not open {url}: {why}"), tx);
            }
        }
        (Some(Target::Local(path)), None) => {
            let opened = safe_file(&path).and_then(|file| {
                tx.open_uri(&attn_term::links::file_url(&file.to_string_lossy()))
                    .map_err(|why| why.to_string())
            });
            if let Err(why) = opened {
                state
                    .window
                    .notify(format!("Could not open {path}: {why}"), tx);
            }
        }
        (Some(Target::Local(path)), Some(outpost)) => {
            let Outpost { host, name } = outpost.clone();
            let asked = path.clone();
            let session = document.session.clone();
            state.ask(
                |id| OpenMarkdownMessage {
                    path: asked,
                    host: Some(host),
                    session_id: session,
                    request_id: Some(id),
                    ..OpenMarkdownMessage::new()
                },
                move |state, answer: Answer, tx| {
                    if let Err(why) = answer.outcome() {
                        state
                            .window
                            .notify(format!("Could not open {path} from {name}: {why}"), tx);
                    }
                },
                tx,
            );
        }
        (None, _) => {}
    }
}

/// The file at `path` as the system's opener may open it: a regular file reached through no
/// symbolic link at its last step, by its canonical path.
fn safe_file(path: &str) -> Result<std::path::PathBuf, String> {
    let path = Path::new(path);
    let metadata = std::fs::symlink_metadata(path).map_err(|e| e.to_string())?;
    if metadata.file_type().is_symlink() {
        return Err("links in a document cannot open symbolic links".into());
    }
    if !metadata.is_file() {
        return Err("links in a document can only open regular files".into());
    }
    std::fs::canonicalize(path).map_err(|e| e.to_string())
}

fn scheme(href: &str) -> Option<&str> {
    let (scheme, _) = href.split_once(':')?;
    let mut chars = scheme.chars();
    let first = chars.next()?;
    (first.is_ascii_alphabetic()
        && chars.all(|c| c.is_ascii_alphanumeric() || matches!(c, '+' | '.' | '-')))
    .then_some(scheme)
}

/// The local path of a `file:` URL's remainder `rest`; `None` when it names another host.
fn file_url_path(rest: &str) -> Option<String> {
    let path = match rest.strip_prefix("//") {
        Some(authority_and_path) => {
            let (host, path) = authority_and_path
                .find('/')
                .map_or((authority_and_path, ""), |at| {
                    authority_and_path.split_at(at)
                });
            if !host.is_empty() && !host.eq_ignore_ascii_case("localhost") {
                return None;
            }
            path
        }
        None => rest,
    };
    let path = decoded(path_part(path));
    Some(normalized(&if path.starts_with('/') {
        path
    } else {
        format!("/{path}")
    }))
}

fn path_part(href: &str) -> &str {
    href.find(['?', '#']).map_or(href, |at| &href[..at])
}

fn decoded(text: &str) -> String {
    percent_decode_str(text).decode_utf8_lossy().into_owned()
}

/// `path` with its `.` and `..` steps applied, as a URL resolves them: `..` stops at the root.
fn normalized(path: &str) -> String {
    let mut steps: Vec<&str> = Vec::new();
    let segments: Vec<&str> = path.split('/').skip(1).collect();
    let last = segments.len().saturating_sub(1);
    for (i, segment) in segments.into_iter().enumerate() {
        match segment {
            "." => {}
            ".." => {
                steps.pop();
            }
            _ => steps.push(segment),
        }
        if i == last && matches!(segment, "." | "..") {
            steps.push("");
        }
    }
    format!("/{}", steps.join("/"))
}

fn extension(path: &str) -> Option<String> {
    let name = path.trim_end_matches('/').rsplit('/').next()?;
    let (_, extension) = name.rsplit_once('.')?;
    Some(extension.to_ascii_lowercase())
}
