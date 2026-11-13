//! The drafts of marks the daemon keeps for the documents the window's tiles show: read when a
//! document arrives and again when the daemon says it changed, saved a pause after each change
//! as only what changed here, and sent to a session or a seed.

use std::collections::HashMap;
use std::ops::Range;
use std::rc::Rc;

use attn_client::Connection;
use attn_protocol::{
    MarkdownAnnotation, MarkdownAnnotationsGetMessage, MarkdownAnnotationsGetResultMessage,
    MarkdownAnnotationsSaveMessage, MarkdownAnnotationsSaveResultMessage,
    MarkdownAnnotationsSubmitMessage, MarkdownAnnotationsSubmitResultMessage,
};
use rune::Tx;

use super::anchoring::Resolved;
use super::marks;
use super::passages::Passages;
use crate::State;

/// How long a change waits for the next before the draft is saved, as the Tauri reader waits.
const SAVE_PAUSE: f64 = 0.5;
/// How long a failed read or save waits before it is tried again, as the Tauri reader waits.
const READ_RETRY: f64 = 2.0;
const SAVE_RETRY: f64 = 5.0;

/// The document a draft belongs to, as the daemon names it.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Source {
    pub(crate) path: String,
    /// The outpost the file is on; none on this machine.
    pub(crate) host: Option<String>,
}

impl Source {
    /// The document's address, the Tauri reader's `documentSource.ts` form.
    pub(crate) fn uri(&self) -> String {
        match &self.host {
            None => format!("attn://file/{}", encode(&self.path)),
            Some(host) => format!("attn://file/{}?host={}", encode(&self.path), encode(host)),
        }
    }

    /// The source's kind, path, host and seed as the draft commands name them.
    fn fields(&self) -> (String, Option<String>, Option<String>, Option<String>) {
        (
            "file".into(),
            Some(self.path.clone()),
            self.host.clone(),
            None,
        )
    }
}

/// `encodeURIComponent`.
fn encode(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for b in s.bytes() {
        if b.is_ascii_alphanumeric() || b"-_.!~*'()".contains(&b) {
            out.push(b as char);
        } else {
            out.push_str(&format!("%{b:02X}"));
        }
    }
    out
}

/// How the last send went.
#[derive(Clone, Debug, PartialEq)]
pub(crate) enum Sent {
    Sending,
    /// Went to a session (or, `noted`, onto a seed); `kept` marks were made meanwhile and stayed.
    Done {
        noted: bool,
        kept: usize,
        until: f64,
    },
    /// Went, but the daemon has something to say about it.
    Warning(String),
    /// The session waits on an approval; nothing was typed.
    Skipped,
    Failed(String),
}

/// One document's draft.
pub(crate) struct Draft {
    pub(crate) source: Source,
    pub(crate) marks: Vec<MarkdownAnnotation>,
    /// The marks as the daemon last answered with them.
    synced: Vec<MarkdownAnnotation>,
    generation: i64,
    /// The daemon answered a read, so saves may go.
    pub(crate) hydrated: bool,
    reading: bool,
    saving: bool,
    /// When a read or a save is due.
    read_due: Option<f64>,
    save_due: Option<f64>,
    /// Marks whose passage this document no longer has.
    pub(crate) orphans: Vec<String>,
    /// The document as the tiles last showed it, by its contents' revision.
    pub(crate) passages: Option<(u64, Rc<Passages>)>,
    /// Where each placed mark draws.
    pub(crate) painted: Vec<Painted>,
    /// The marks changed since they were placed.
    unplaced: bool,
}

/// A mark's passage in the source the reader draws.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Painted {
    pub(crate) id: String,
    pub(crate) deletion: bool,
    pub(crate) source: Vec<Range<usize>>,
}

/// The drafts of every document a tile shows.
#[derive(Default)]
pub struct Drafts {
    drafts: HashMap<String, Draft>,
    /// Moves with every change to a draft, so views that show them build again.
    pub(crate) revision: u64,
}

impl Drafts {
    pub(crate) fn get(&self, uri: &str) -> Option<&Draft> {
        self.drafts.get(uri)
    }

    pub(crate) fn get_mut(&mut self, uri: &str) -> Option<&mut Draft> {
        self.revision += 1;
        self.drafts.get_mut(uri)
    }
}

impl Draft {
    fn new(source: Source) -> Draft {
        Draft {
            source,
            marks: Vec::new(),
            synced: Vec::new(),
            generation: 0,
            hydrated: false,
            reading: false,
            saving: false,
            read_due: Some(0.0),
            save_due: None,
            orphans: Vec::new(),
            passages: None,
            painted: Vec::new(),
            unplaced: true,
        }
    }

    /// Takes the daemon's draft at `generation`, keeping what changed here on top. An older
    /// answer than one already taken changes nothing. True when changes here still need saving.
    fn adopt(&mut self, annotations: Vec<MarkdownAnnotation>, generation: i64) -> bool {
        if generation < self.generation {
            return false;
        }
        let draft = marks::kept(annotations);
        self.unplaced = true;
        self.marks = marks::rebase(&self.marks, &self.synced, draft.clone());
        self.synced = draft;
        self.generation = generation;
        let (upserts, removed) = marks::unsaved(&self.marks, &self.synced);
        !upserts.is_empty() || !removed.is_empty()
    }

    /// Something here changed: the draft is saved once changes pause.
    pub(crate) fn changed(&mut self, now: f64) {
        self.unplaced = true;
        self.save_due = Some(now + SAVE_PAUSE);
    }

    /// Finds each mark's passage in the document: where it was, where its words moved to
    /// (which is saved), or nowhere.
    fn place(&mut self, now: f64) {
        let Some((_, passages)) = self.passages.clone() else {
            return;
        };
        self.unplaced = false;
        let (mut painted, mut orphans, mut moved) = (Vec::new(), Vec::new(), false);
        for mark in &mut self.marks {
            let Some(anchor) = mark.anchor.as_ref() else {
                continue;
            };
            let found = match passages.blocks.resolve(anchor) {
                Resolved::Exact(found) => found,
                Resolved::Moved(found, to) => {
                    mark.anchor = Some(*to);
                    moved = true;
                    found
                }
                Resolved::Orphan => {
                    orphans.push(mark.id.clone());
                    continue;
                }
            };
            painted.push(Painted {
                id: mark.id.clone(),
                deletion: mark.r#type == "deletion",
                source: passages.source(found),
            });
        }
        (self.painted, self.orphans) = (painted, orphans);
        if moved {
            self.save_due = Some(now + SAVE_PAUSE);
        }
    }
}

/// Starts a draft for each document a tile shows, forgets those no tile shows, reads each that
/// is due and saves each whose changes paused. True when a draft changed.
pub(crate) fn follow(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    if state.facts.connection != Connection::Connected {
        return false;
    }
    let shown: Vec<(Source, &attn_client::TileContent)> = state
        .facts
        .tile_contents
        .tiles
        .values()
        .filter(|c| c.message.tile_kind == "markdown" && !c.message.path.is_empty())
        .map(|c| {
            let host = c.message.host.clone().filter(|h| !h.is_empty());
            (
                Source {
                    path: c.message.path.clone(),
                    host,
                },
                c,
            )
        })
        .collect();
    let drafts = &mut state.drafts;
    let before = drafts.drafts.len();
    drafts.drafts.retain(|uri, d| {
        d.save_due.is_some() || d.saving || shown.iter().any(|(s, _)| s.uri() == *uri)
    });
    let mut changed = drafts.drafts.len() != before;
    for (source, content) in shown {
        let draft = drafts.drafts.entry(source.uri()).or_insert_with(|| {
            changed = true;
            Draft::new(source)
        });
        let generation = content.message.annotations_generation;
        if generation.is_some_and(|g| g > draft.generation) && !draft.reading && draft.hydrated {
            draft.read_due = Some(now);
        }
        let readable = content.message.error.as_deref().is_none_or(str::is_empty);
        if readable
            && draft
                .passages
                .as_ref()
                .is_none_or(|(revision, _)| *revision != content.revision)
        {
            let source = &content.message.content;
            draft.passages = Some((
                content.revision,
                Rc::new(Passages::read(
                    source,
                    &super::read_as(source),
                    &rune_kit::Kit::neutral(true),
                )),
            ));
            draft.unplaced = true;
        }
        if draft.unplaced && draft.hydrated && draft.passages.is_some() {
            draft.place(now);
            changed = true;
        }
    }
    let due: Vec<(String, bool)> = state
        .drafts
        .drafts
        .iter()
        .flat_map(|(uri, d)| {
            let read =
                (!d.reading && d.read_due.is_some_and(|at| at <= now)).then(|| (uri.clone(), true));
            let save = (d.hydrated && !d.saving && d.save_due.is_some_and(|at| at <= now))
                .then(|| (uri.clone(), false));
            read.into_iter().chain(save)
        })
        .collect();
    for (uri, read) in due {
        if read {
            fetch(state, &uri, tx)
        } else {
            save(state, &uri, false, tx)
        }
    }
    if changed {
        state.drafts.revision += 1;
    }
    let wake = state
        .drafts
        .drafts
        .values()
        .flat_map(|d| d.read_due.into_iter().chain(d.save_due))
        .fold(f64::INFINITY, f64::min);
    if wake.is_finite() {
        tx.wake_at(wake);
    }
    changed
}

fn fetch(state: &mut State, uri: &str, tx: &mut Tx) {
    let Some(draft) = state.drafts.drafts.get_mut(uri) else {
        return;
    };
    draft.reading = true;
    draft.read_due = None;
    let (source_kind, path, host, seed_id) = draft.source.fields();
    let document_uri = uri.to_owned();
    let uri = uri.to_owned();
    state.ask(
        move |request_id| MarkdownAnnotationsGetMessage {
            request_id,
            document_uri,
            source_kind,
            path,
            host,
            seed_id,
            ..MarkdownAnnotationsGetMessage::new()
        },
        move |state, answer, tx| {
            let now = tx.clock();
            let Some(draft) = state.drafts.get_mut(&uri) else {
                return;
            };
            draft.reading = false;
            match answer
                .parse::<MarkdownAnnotationsGetResultMessage>()
                .filter(|r| r.success)
            {
                Some(read) => {
                    draft.hydrated = true;
                    if draft.adopt(read.annotations, read.generation) {
                        draft.changed(now);
                    }
                }
                None => draft.read_due = Some(now + READ_RETRY),
            }
            tx.wake_at(now);
        },
        tx,
    );
}

/// Saves what changed here in document `uri`'s draft; `now` even while a save is under way,
/// which only writes again what that one writes.
fn save(state: &mut State, uri: &str, now: bool, tx: &mut Tx) {
    let Some(draft) = state
        .drafts
        .drafts
        .get_mut(uri)
        .filter(|d| d.hydrated && (now || !d.saving))
    else {
        return;
    };
    draft.save_due = None;
    let (upserts, removed_ids) = marks::unsaved(&draft.marks, &draft.synced);
    if upserts.is_empty() && removed_ids.is_empty() {
        return;
    }
    draft.saving = true;
    let (source_kind, path, host, seed_id) = draft.source.fields();
    let (document_uri, uri) = (uri.to_owned(), uri.to_owned());
    state.ask(
        move |request_id| MarkdownAnnotationsSaveMessage {
            request_id,
            document_uri,
            source_kind,
            path,
            host,
            seed_id,
            upserts,
            removed_ids,
            ..MarkdownAnnotationsSaveMessage::new()
        },
        move |state, answer, tx| {
            let now = tx.clock();
            let Some(draft) = state.drafts.get_mut(&uri) else {
                return;
            };
            draft.saving = false;
            match answer
                .parse::<MarkdownAnnotationsSaveResultMessage>()
                .filter(|r| r.success)
            {
                Some(saved) => {
                    if draft.adopt(saved.annotations, saved.generation) && draft.save_due.is_none()
                    {
                        draft.changed(now);
                    }
                }
                None => draft.save_due = Some(now + SAVE_RETRY),
            }
            tx.wake_at(now);
        },
        tx,
    );
}

/// Saves what changed here in document `uri`'s draft at once.
pub(crate) fn save_now(state: &mut State, uri: &str, tx: &mut Tx) {
    save(state, uri, true, tx);
}

/// Sends document `uri`'s draft to session `session`, telling `done` how it went.
pub(crate) fn submit(
    state: &mut State,
    uri: &str,
    session: String,
    done: impl FnOnce(&mut State, Sent, &mut Tx) + 'static,
    tx: &mut Tx,
) {
    let Some(draft) = state.drafts.drafts.get(uri) else {
        return;
    };
    if !draft.hydrated {
        done(
            state,
            Sent::Failed("Annotations are still syncing — try again in a moment".into()),
            tx,
        );
        return;
    }
    save(state, uri, true, tx);
    let draft = &state.drafts.drafts[uri];
    let (source_kind, path, host, seed_id) = draft.source.fields();
    let orphaned_ids = Some(draft.orphans.clone()).filter(|o| !o.is_empty());
    let target_session_id = Some(session);
    let (document_uri, uri) = (uri.to_owned(), uri.to_owned());
    state.ask(
        move |request_id| MarkdownAnnotationsSubmitMessage {
            request_id,
            document_uri,
            source_kind,
            path,
            host,
            seed_id,
            target_session_id,
            orphaned_ids,
            ..MarkdownAnnotationsSubmitMessage::new()
        },
        move |state, answer, tx| {
            let now = tx.clock();
            let result = answer.parse::<MarkdownAnnotationsSubmitResultMessage>();
            let status = result.as_ref().map(|r| {
                if r.status.is_empty() {
                    "delivered"
                } else {
                    r.status.as_str()
                }
            });
            let sent = match (&result, status) {
                (Some(r), Some(status @ ("delivered" | "noted"))) if r.success => {
                    let mut kept = 0;
                    if let (Some(generation), Some(draft)) =
                        (r.generation, state.drafts.get_mut(&uri))
                    {
                        if draft.adopt(r.annotations.clone().unwrap_or_default(), generation) {
                            draft.changed(now);
                        }
                        kept = draft.marks.len();
                    }
                    match r.error.clone().filter(|e| !e.is_empty()) {
                        Some(warning) => Sent::Warning(warning),
                        None => Sent::Done {
                            noted: status == "noted",
                            kept,
                            until: now,
                        },
                    }
                }
                (_, Some("skipped_pending_approval")) => Sent::Skipped,
                (Some(r), _) => Sent::Failed(
                    r.error
                        .clone()
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| "Send failed".into()),
                ),
                (None, _) => Sent::Failed("Send failed".into()),
            };
            done(state, sent, tx);
        },
        tx,
    );
}
