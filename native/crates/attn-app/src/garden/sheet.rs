use std::sync::Arc;

use attn_protocol::{DesktopUpdateTileMessage, Seed, SeedDocument};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, memo, row, text};

use super::model::{self, Relation, SessionNames};
use crate::State;
use crate::requests::Refused;

/// Where a seed's page shows: the keys its parts take, the scroll its body reads in, and where
/// pressing another seed goes.
pub(super) struct Sheet<'a> {
    pub(super) prefix: String,
    pub(super) view: String,
    pub(super) go: Go,
    pub(super) names: &'a SessionNames,
    pub(super) garden: &'a super::Garden,
    pub(super) seeds: &'a [Arc<Seed>],
    pub(super) chief: bool,
    pub(super) readings: &'a crate::markdown::Readings,
    pub(super) now_ms: i64,
    pub(super) t: &'a Tokens,
}

#[derive(Clone)]
pub(super) enum Go {
    Garden,
    Tile { desktop: String, tile: String },
}

impl Go {
    pub(super) fn to(&self, state: &mut State, id: &str, tx: &mut Tx) {
        match self {
            Go::Garden => super::drill(state, id, tx),
            Go::Tile { desktop, tile } => {
                let (desktop_id, tile_id) = (desktop.clone(), tile.clone());
                let params = id.to_owned();
                state.act(
                    |request_id| DesktopUpdateTileMessage {
                        request_id,
                        desktop_id,
                        tile_id,
                        tile_params: Some(params),
                        ..DesktopUpdateTileMessage::new()
                    },
                    Refused::Prefixed("Could not open the seed"),
                    |_, _, _| {},
                    tx,
                );
            }
        }
    }
}

impl Sheet<'_> {
    pub(super) fn key(&self, part: &str) -> String {
        format!("{}.{part}", self.prefix)
    }

    pub(super) fn note(&self, part: &str, words: impl Into<String>) -> El {
        note(&self.key(part), words, self.t)
    }

    fn section(&self, part: &str, title: &str) -> El {
        section(&self.key(part), title, self.t)
    }

    /// The seed's title beside `action`, then its state, who tends it, its checks, its age and
    /// id, then why it closed and its plot's progress.
    pub(super) fn head(&self, here: &Seed, action: El) -> El {
        let t = self.t;
        let mut head = col().key(self.key("head")).w_full().gap(space::S1).child(
            row()
                .w_full()
                .flex_wrap()
                .items_center()
                .gap(space::S2)
                .child(
                    text(here.title.clone())
                        .key(self.key("head.title"))
                        .role("heading")
                        .label(here.title.clone())
                        .font(type_::TITLE)
                        .color(t.text_primary)
                        .wrap()
                        .grow(1.0),
                )
                .child(action.flex_wrap()),
        );
        let mut meta = row()
            .key(self.key("head.meta"))
            .gap(space::S2)
            .child(meta_word(here.status.clone(), t));
        if let Some((sentence, _)) = model::harvest_when(here) {
            meta = meta.child(meta_word(sentence, t));
        }
        let tender = model::tender_of(here, self.names);
        if !tender.is_empty() {
            meta = meta.child(meta_word(format!("tended by {tender}"), t));
        }
        if here.planter.r#ref.starts_with("member:") {
            meta = meta.child(meta_word(format!("by {}", here.planter.name), t));
        }
        if !model::is_plot(here)
            && let Some(tally) = model::check_tally(here)
        {
            meta = meta.child(meta_word(model::tally_words(&tally), t));
        }
        meta = meta
            .child(meta_word(model::ago(&here.created_at, self.now_ms), t))
            .child(
                text(here.id.clone())
                    .font(type_::MONO_CAPTION)
                    .color(t.text_tertiary),
            );
        head = head.child(meta);
        if let Some(reason) = here.reason.as_ref().filter(|r| !r.is_empty()) {
            head = head.child(self.note("head.reason", reason.clone()));
        }
        if model::is_plot(here) {
            head = head.child(self.note("head.progress", model::progress_words(here)));
        }
        head
    }

    /// The seed's body, read where the sheet scrolls.
    pub(super) fn plan(&self, here: &Seed) -> El {
        if here.body.trim().is_empty() {
            return self.note("body.empty", "No body — the title is the whole seed.");
        }
        let body = here.body.clone();
        let view = self.view.clone();
        let key = self.key("plan");
        let t = *self.t;
        let folds = self.readings.folds(&view);
        memo(
            self.key(&format!("plan.{}", here.id)),
            (body.clone(), view.clone(), folds.clone(), t),
            move || {
                col()
                    .key(key)
                    .role("document")
                    .label("Plan")
                    .w_full()
                    .child(crate::markdown::reader(
                        &body,
                        crate::markdown::Document {
                            path: String::new(),
                            session: None,
                            outpost: None,
                            view,
                        },
                        std::rc::Rc::default(),
                        folds,
                        None,
                        &t,
                    ))
            },
        )
    }

    /// The seeds `rows` name, each a button that goes to it, under `title`.
    pub(super) fn seeds(&self, part: &str, title: &str, rows: Vec<Relation>) -> Option<El> {
        if rows.is_empty() {
            return None;
        }
        let mut list = self.section(part, title);
        for relation in rows {
            let other: Arc<Seed> = relation.seed.clone();
            let go = self.go.clone();
            let id = other.id.clone();
            let mut line = row().gap(space::S2).items_center();
            if !relation.label.is_empty() {
                line = line.child(meta_word(relation.label, self.t));
            }
            let key = if relation.label.is_empty() {
                self.key(&format!("{part}.{}", other.id))
            } else {
                self.key(&format!("{part}.{}.{}", relation.label, other.id))
            };
            list = list.child(
                line.child(
                    super::view::button(key, other.title.clone(), self.t)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| go.to(state, &id, tx)),
                )
                .child(meta_word(other.status.clone(), self.t)),
            );
        }
        Some(list)
    }

    pub(super) fn artifacts(&self, document: Option<&SeedDocument>) -> Option<El> {
        let document = document?;
        if document.artifacts.is_empty() && document.references.is_empty() {
            return None;
        }
        let mut artifacts = self.section("artifacts", "Artifacts");
        for artifact in &document.artifacts {
            artifacts = artifacts.child(
                text(artifact.filename.clone())
                    .key(self.key(&format!("artifact.{}", artifact.filename)))
                    .role("listitem")
                    .label(artifact.filename.clone())
                    .font(type_::BODY)
                    .color(self.t.text_primary),
            );
        }
        Some(artifacts)
    }

    pub(super) fn log(&self, document: Option<&SeedDocument>, error: Option<&str>) -> El {
        let t = self.t;
        let mut log = self.section("log", "Log");
        if let Some(error) = error {
            return log.child(self.note("log.error", error.to_owned()));
        }
        let Some(document) = document else {
            return log;
        };
        if document.notes.is_empty() {
            log = log.child(self.note("log.empty", "Nothing on this seed’s log yet."));
        }
        let spoken = document
            .notes
            .iter()
            .filter(|n| n.kind != "attach" && n.kind != "detach");
        for entry in spoken {
            let who = model::who_name(&entry.author.r#ref, &entry.author.name, self.names);
            let mut head = row()
                .gap(space::S2)
                .child(text(who).font(type_::LABEL).color(t.text_primary));
            if entry.kind != "note" {
                head = head.child(meta_word(entry.kind.clone(), t));
            }
            head = head.child(meta_word(model::ago(&entry.created_at, self.now_ms), t));
            let mut item = col()
                .key(self.key(&format!("log.{}", entry.id)))
                .role("listitem")
                .label(entry.body.clone())
                .w_full()
                .gap(2.0)
                .child(head);
            if !entry.body.is_empty() {
                item = item.child(
                    text(entry.body.clone())
                        .font(type_::BODY)
                        .color(t.text_secondary)
                        .wrap(),
                );
            }
            log = log.child(item);
        }
        let withheld = (document.notes_total - document.notes.len() as i64).max(0);
        if withheld > 0 {
            log = log.child(self.note(
                "log.withheld",
                format!(
                    "{withheld} more {} on the log.",
                    if withheld == 1 { "entry" } else { "entries" }
                ),
            ));
        }
        log
    }
}

pub(super) fn note(key: &str, words: impl Into<String>, t: &Tokens) -> El {
    let words = words.into();
    text(words.clone())
        .key(key.to_owned())
        .role("note")
        .label(words)
        .font(type_::BODY)
        .color(t.text_secondary)
        .padding(space::S2)
}

pub(super) fn section(key: &str, title: &str, t: &Tokens) -> El {
    col()
        .key(key.to_owned())
        .role("region")
        .label(title.to_owned())
        .w_full()
        .gap(space::S1)
        .pt(space::S3)
        .child(
            text(title.to_owned())
                .role("heading")
                .label(title.to_owned())
                .font(type_::SECTION)
                .color(t.text_tertiary),
        )
}

pub(super) fn meta_word(words: impl Into<String>, t: &Tokens) -> El {
    text(words.into())
        .font(type_::LABEL)
        .color(t.text_secondary)
}
