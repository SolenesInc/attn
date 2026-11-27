//! Where a document's links go, and which of them the reader may follow: a fragment scrolls the
//! document, a web or mail link opens in its handler, a local file opens with the system's
//! opener when its kind is safe, and a file on another machine opens in a tile when it is markdown,
//! as does a markdown file the daemon placed on a machine, this app's included. While Asks is
//! open, a markdown file opens in the panel beside the question instead of a tile.

use std::path::Path;

use attn_protocol::{
    OpenMarkdownMessage, SeedArtifactTargetMessage, SeedArtifactTargetResultMessage,
};
use percent_encoding::percent_decode_str;
use rune::Tx;

use crate::State;
use crate::requests::Answer;
use crate::terminal::Subject;

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

/// How the reader treats `target` in a document on the machine named `away`, or on this app's
/// machine when `None`.
pub(crate) fn verdict(target: Option<&Target>, away: Option<&str>) -> Verdict {
    let follows = |help: Option<String>| Verdict {
        help,
        follows: true,
    };
    let shown = |help: Option<String>| Verdict {
        help,
        follows: false,
    };
    match (target, away) {
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

/// The document a link belongs to: its file, the session its tile was opened for, the machine
/// it is on when that is not this app's, and the scroll view whose headings a fragment names.
#[derive(Clone)]
pub(crate) struct Document {
    pub(crate) path: String,
    pub(crate) session: Option<String>,
    pub(crate) away: Option<Away>,
    pub(crate) view: String,
    pub(crate) opens_seed: Option<fn(&mut State, &str, &mut Tx)>,
    /// The seed whose files the document's bare file names name, as a seed's body names them.
    pub(crate) seed: Option<String>,
    /// The byte of the source a reading of one part of the document starts at.
    pub(crate) part: Option<usize>,
    /// The ids its words link.
    pub(crate) ids: crate::terminal::Known,
    /// The links whose file the daemon already placed on a machine.
    pub(crate) placed: Vec<Placed>,
}

/// A link whose file the daemon placed: the link as written, its absolute path, the `host` the
/// wire names its machine by, and that machine when it is not this app's.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Placed {
    pub(crate) href: String,
    pub(crate) path: String,
    pub(crate) host: Option<String>,
    pub(crate) away: Option<Away>,
}

/// The machine a document is on when it is not this app's: the `host` the wire names it by,
/// absent for the home's machine, and its name.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Away {
    pub(crate) host: Option<String>,
    pub(crate) name: String,
}

impl Away {
    pub(crate) fn of(machines: &crate::machine::Machines, host: Option<&str>) -> Option<Away> {
        let name = machines.away(host)?;
        Some(Away {
            host: host.filter(|h| !h.is_empty()).map(str::to_owned),
            name,
        })
    }
}

impl Document {
    /// How the reader treats the link `href` in this document.
    pub(crate) fn verdict(&self, href: &str) -> Verdict {
        if super::ids::subject(href).is_some() {
            return Verdict {
                help: None,
                follows: true,
            };
        }
        if let Some(seed) = self.seed(href) {
            return Verdict {
                help: Some(format!("Open {seed}")),
                follows: true,
            };
        }
        if let Some(file) = self.seed_file(href) {
            let name = decoded(&file);
            return if is_safe_local(&name) {
                Verdict {
                    help: Some(name),
                    follows: true,
                }
            } else {
                Verdict {
                    help: Some(format!("Blocked seed artifact target: {file}")),
                    follows: false,
                }
            };
        }
        if let Some(placed) = self.placed(href) {
            return verdict(
                Some(&Target::Local(placed.path.clone())),
                placed.away.as_ref().map(|o| o.name.as_str()),
            );
        }
        verdict(
            resolve(&self.path, href).as_ref(),
            self.away.as_ref().map(|o| o.name.as_str()),
        )
    }

    fn placed(&self, href: &str) -> Option<&Placed> {
        self.placed.iter().find(|p| p.href == href)
    }

    /// The picture an image's `src` shows, by the path the document's pictures are read by.
    pub(crate) fn picture(&self, src: &str) -> Option<String> {
        let path = match resolve(&self.path, src) {
            Some(Target::Local(file)) if !self.path.is_empty() => Some(file),
            Some(_) => None,
            None => self.seed_file(src),
        }?;
        is_image(&path).then_some(path)
    }

    /// The file of the document's seed that `target` names, when it names no other file.
    fn seed_file(&self, target: &str) -> Option<String> {
        self.seed.as_ref()?;
        resolve(&self.path, target)
            .is_none()
            .then(|| seed_file(target))?
    }

    fn seed(&self, href: &str) -> Option<String> {
        self.opens_seed?;
        let id = decoded(href.trim());
        is_seed_id(&id).then_some(id)
    }
}

/// The file of a seed that `target` names: a bare file name, never a path, a scheme or a hidden
/// file.
pub(crate) fn seed_file(target: &str) -> Option<String> {
    let target = target.trim();
    if target.is_empty() || scheme(target).is_some() || target.contains(['/', '\\', '?', '#']) {
        return None;
    }
    let name = percent_decode_str(target).decode_utf8().ok()?;
    (!name.is_empty() && !name.starts_with('.') && !name.contains(['/', '\\']))
        .then(|| target.to_owned())
}

fn is_seed_id(text: &str) -> bool {
    text.strip_prefix("s-").is_some_and(|body| {
        body.len() == 6
            && body
                .bytes()
                .all(|b| b.is_ascii_digit() || (b.is_ascii_lowercase() && !b"ilou".contains(&b)))
    })
}

/// Follows the link `href` pressed in `document`.
pub(crate) fn follow(state: &mut State, document: &Document, href: &str, tx: &mut Tx) {
    if let Some(target) = super::ids::subject(href) {
        match (target, document.opens_seed) {
            (Subject::Seed(seed), Some(open)) => open(state, &seed, tx),
            (target, _) => super::ids::open(state, target, tx),
        }
        return;
    }
    if let Some(seed) = document.seed(href)
        && let Some(open) = document.opens_seed
    {
        open(state, &seed, tx);
        return;
    }
    if !document.verdict(href).follows {
        return;
    }
    if let (Some(seed_id), Some(relative_target)) = (&document.seed, document.seed_file(href)) {
        let seed_id = seed_id.clone();
        let host = crate::machine::Machines::of(&state.facts).app_host();
        let at = host.clone();
        return state.ask(
            |request_id| SeedArtifactTargetMessage {
                seed_id,
                relative_target,
                purpose: "link".into(),
                host,
                request_id,
                ..SeedArtifactTargetMessage::new()
            },
            move |state, answer: Answer, tx| {
                let opened = answer
                    .outcome()
                    .and_then(|reply| {
                        reply
                            .parse::<SeedArtifactTargetResultMessage>()
                            .filter(|r| r.success)
                            .and_then(|r| r.result?.path)
                            .ok_or_else(|| "the daemon named no file".to_owned())
                    })
                    .and_then(|path| {
                        let machines = crate::machine::Machines::of(&state.facts);
                        match machines.refuse_open(&path, at.as_deref()) {
                            None => tx
                                .open_uri(&attn_term::links::file_url(&path))
                                .map_err(|why| format!("{path}: {why}")),
                            Some(_) if is_markdown(&path) => {
                                open_markdown(state, path, None, None, tx);
                                Ok(())
                            }
                            Some(refusal) => Err(refusal),
                        }
                    });
                if let Err(why) = opened {
                    state
                        .window
                        .notify(format!("Could not open the seed's file: {why}"), tx);
                }
            },
            tx,
        );
    }
    if let Some(placed) = document.placed(href) {
        let placed = placed.clone();
        if is_markdown(&placed.path) {
            open_markdown(
                state,
                placed.path,
                placed.host,
                document.session.clone(),
                tx,
            );
        } else {
            open_file(
                state,
                placed.path,
                placed.away.as_ref(),
                document.session.clone(),
                tx,
            );
        }
        return;
    }
    let target = resolve(&document.path, href);
    match (target, document.away.as_ref()) {
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
        (Some(Target::Local(path)), away) if is_markdown(&path) && crate::asks::is_open(state) => {
            let host = match away {
                Some(away) => away.host.clone(),
                None => crate::machine::Machines::of(&state.facts).app_host(),
            };
            open_markdown(state, path, host, document.session.clone(), tx)
        }
        (Some(Target::Local(path)), away) => {
            open_file(state, path, away, document.session.clone(), tx)
        }
        (None, _) => {}
    }
}

/// Opens the markdown file at `path` on the machine `host` names: in the panel beside the
/// question while Asks is open, else in a tile.
fn open_markdown(
    state: &mut State,
    path: String,
    host: Option<String>,
    session: Option<String>,
    tx: &mut Tx,
) {
    if crate::asks::is_open(state) {
        let reference = crate::asks::panel::Reference::Markdown { host, path };
        crate::asks::panel::open(state, reference, tx);
    } else {
        crate::opener::open_markdown(state, path, host, session, tx);
    }
}

/// Opens the file at `path`: here with the system's opener, or in a tile from the machine `away`
/// names, for the session the document's tile was opened for.
fn open_file(
    state: &mut State,
    path: String,
    away: Option<&Away>,
    session: Option<String>,
    tx: &mut Tx,
) {
    let Some(Away { host, name }) = away.cloned() else {
        let opened = safe_file(&path).and_then(|file| {
            tx.open_uri(&attn_term::links::file_url(&file.to_string_lossy()))
                .map_err(|why| why.to_string())
        });
        if let Err(why) = opened {
            state
                .window
                .notify(format!("Could not open {path}: {why}"), tx);
        }
        return;
    };
    let asked = path.clone();
    state.ask(
        |id| OpenMarkdownMessage {
            path: asked,
            host,
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

/// The local path a `file:` URL names, in any letter case; `None` for another scheme or host.
pub(crate) fn file_url(url: &str) -> Option<String> {
    let scheme = scheme(url).filter(|s| s.eq_ignore_ascii_case("file"))?;
    file_url_path(&url[scheme.len() + 1..])
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
