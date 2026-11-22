use std::sync::Arc;

use attn_protocol::{DesktopUpdateTileMessage, Seed, SeedCheckTally, SeedDocument};
use attn_ui::Tokens;
use attn_ui::blocks::{Block, Byline, Entry, Head, Inline, Prose, Section, Stack, entries};
use attn_ui::system::{FieldState, Link, Said, Type, area_box, dot_on_line, meter, words};
use rune::{El, KeyEvent, TextEdit, Tx, col, memo, row};
use rune_kit::TextArea;

use super::model::{self, Relation, SessionNames};
use crate::State;

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
    pub(super) pictures: &'a crate::markdown::Pictures,
    /// The marks people make on the seed's body, in a tile that takes them.
    pub(super) drafts: Option<&'a crate::markdown::Drafts>,
    pub(super) now_ms: i64,
    /// The harnesses the daemon pushed, which a handover chooses among.
    pub(super) harnesses: &'a [attn_protocol::Harness],
    pub(super) t: &'a Tokens,
}

#[derive(Clone)]
pub(super) enum Go {
    Garden,
    Tile { desktop: String, tile: String },
    Review,
}

impl Go {
    pub(super) fn to(&self, state: &mut State, id: &str, tx: &mut Tx) {
        match self {
            Go::Garden => super::drill(state, id, tx),
            Go::Review => super::review::browse(state, id, tx),
            Go::Tile { desktop, tile } => {
                let (desktop_id, tile_id) = (desktop.clone(), tile.clone());
                let params = id.to_owned();
                state.ask(
                    |request_id| DesktopUpdateTileMessage {
                        request_id,
                        desktop_id,
                        tile_id,
                        tile_params: Some(params),
                        ..DesktopUpdateTileMessage::new()
                    },
                    |state, answer, tx| {
                        if let Err(reason) = answer.outcome() {
                            state.window.garden.check_focus = None;
                            state
                                .window
                                .notify(format!("Could not open the seed: {reason}"), tx);
                        }
                    },
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

    /// What a part of the page says when it has nothing to show.
    pub(super) fn note(&self, part: &str, words: impl Into<Said>) -> Block {
        Prose::empty(self.key(part), words).build(self.t)
    }

    /// A short note that goes with something above it, such as why a seed cannot resume.
    pub(super) fn small_note(&self, part: &str, said: impl Into<String>) -> El {
        let said = said.into();
        words(said, Type::Small, self.t.text_tertiary)
            .key(self.key(part))
            .role("note")
            .wrap()
    }

    pub(super) fn section(&self, part: &str, title: &str) -> Section {
        Section::new(self.key(part), title.to_owned()).region(title)
    }

    /// The seed's title beside `aside`, over its state, who tends it, its checks, its age and
    /// id, then `notes`.
    pub(super) fn head(&self, here: &Seed, aside: Vec<El>, notes: Vec<El>) -> Head {
        let t = self.t;
        let mut facts = Byline::new().key(self.key("head.meta")).wrap().push(
            Inline::loose()
                .child(dot_on_line(
                    super::view::state_color(&here.status, t),
                    Type::Small,
                ))
                .child(said(here.status.clone(), t)),
        );
        if let Some((sentence, _)) = model::harvest_when(here) {
            facts = facts.push(self.harvest_when(here, sentence));
        }
        let tender = model::tender_of(here, self.names);
        if !tender.is_empty() {
            facts = facts.said(format!("tended by {tender}"), t);
        }
        if here.planter.r#ref.starts_with("member:") {
            facts = facts.said(format!("by {}", here.planter.name), t);
        }
        if !model::is_plot(here)
            && let Some(tally) = model::check_tally(here)
        {
            facts = facts.push(self.tally(here, &tally));
        }
        let facts = facts
            .said(model::ago(&here.created_at, self.now_ms), t)
            .id(here.id.clone(), t.text_tertiary)
            .build(t);
        let gone_to = self
            .garden
            .check_focus
            .as_ref()
            .is_some_and(|f| f.sheet == self.prefix && f.seed == here.id);
        let facts = if gone_to {
            facts.on_layout_tx(|state: &mut State, _, tx: &mut Tx| {
                if let Some(focus) = state.window.garden.check_focus.take() {
                    tx.scroll_center(focus.check_box.clone());
                    tx.focus(&focus.check_box, false);
                }
            })
        } else {
            facts
        };
        let head = Head::new(self.key("head.title"), here.title.clone())
            .note(Stack::tight().child(facts).children(notes));
        aside.into_iter().fold(head, Head::aside)
    }

    /// Why the seed closed, and how far its plot has drained: what the head says after its
    /// facts.
    pub(super) fn head_after(&self, here: &Seed) -> Vec<Block> {
        let t = self.t;
        let mut after = Vec::new();
        if let Some(reason) = here.reason.as_ref().filter(|r| !r.is_empty()) {
            after.push(Prose::empty(self.key("head.reason"), reason.clone()).build(t));
        }
        if model::is_plot(here) {
            let progress = Inline::loose()
                .key(self.key("head.progress"))
                .children(progress_meter(here, t))
                .child(
                    said(model::progress_words(here), t)
                        .key(self.key("head.progress.words"))
                        .role("note")
                        .label(model::progress_words(here)),
                )
                .children(model::check_tally(here).map(|tally| self.tally(here, &tally)));
            after.push(Block::custom(self.key("head.progress"), progress));
        }
        after
    }

    /// The seed's check tally, which goes to its checks when it has checks of its own.
    fn tally(&self, here: &Seed, tally: &SeedCheckTally) -> El {
        let (said_words, sentence) = (model::tally_words(tally), model::tally_sentence(tally));
        let own = here.checks.as_ref().is_some_and(|c| !c.is_empty())
            || here.shared_checks.as_ref().is_some_and(|c| !c.is_empty());
        if !own {
            return said(said_words, self.t).help(format!("checks: {sentence}"));
        }
        let (checks, heading) = (self.key("checks"), self.key("checks.heading"));
        let link: El = Link::new(self.key("head.tally"), said_words)
            .ink(self.t.text_tertiary)
            .build(self.t)
            .into();
        link.on_press_tx(move |_: &mut State, tx: &mut Tx| {
            tx.scroll_into_view(checks.clone());
            tx.focus(&heading, false);
        })
        .label(format!("Checks: {sentence}"))
    }

    /// What the seed harvests on, a link to its pull request when the daemon knows where it is.
    fn harvest_when(&self, here: &Seed, sentence: String) -> El {
        let url = here
            .harvest_when
            .as_ref()
            .map(|h| h.url.trim().to_owned())
            .unwrap_or_default();
        if url.is_empty() {
            return said(sentence, self.t);
        }
        let link: El = Link::new(self.key("head.harvest-when"), format!("{sentence} ↗"))
            .ink(self.t.text_tertiary)
            .build(self.t)
            .into();
        link.help(url.clone())
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                if let Err(why) = tx.open_url(&url) {
                    state
                        .window
                        .notify(format!("Could not open {url}: {why}"), tx);
                }
            })
            .role("link")
            .label(sentence)
    }

    /// A log entry's words, read as markdown as the plan is.
    fn log_body(&self, id: &str, body: &str) -> El {
        let view = format!("{}/log.{id}", self.view);
        let kept = self.readings.kept(&view);
        let t = *self.t;
        let body = line_breaks(body);
        memo(
            self.key(&format!("log.{id}.body")),
            (body.clone(), view.clone(), kept.clone(), t),
            move || {
                crate::markdown::reader(
                    &body,
                    crate::markdown::Document {
                        path: String::new(),
                        session: None,
                        outpost: None,
                        view,
                        opens_seed: None,
                        seed: None,
                        part: None,
                    },
                    std::rc::Rc::default(),
                    kept,
                    None,
                    attn_ui::system::note_style(t.text_secondary, &t),
                    &t,
                )
            },
        )
    }

    /// The seed's body, read where the sheet scrolls.
    pub(super) fn plan(&self, here: &Seed) -> Block {
        if here.body.trim().is_empty() {
            return self.note("body.empty", "No body — the title is the whole seed.");
        }
        let body = here.body.clone();
        let view = self.view.clone();
        let key = self.key("plan");
        let t = *self.t;
        let kept = self.readings.kept(&view);
        let in_card = matches!(self.go, Go::Tile { .. });
        let width = kept.width.map(|w| w as f32);
        let (padding, text_width) = if in_card {
            let (padding, text) = attn_ui::blocks::document_inside(width.unwrap_or(0.0));
            (padding, width.map(|_| text))
        } else {
            (Default::default(), width)
        };
        let style = {
            let look = attn_ui::system::note_style(t.text_primary, &t);
            let look = if in_card {
                look.prose_measure(attn_ui::blocks::DOCUMENT)
            } else {
                look
            };
            match text_width {
                Some(width) => look.text_width(width),
                None => look,
            }
        };
        let pictures = self.pictures.of_seed(&here.id);
        let pictured = self.pictures.revision;
        let read = {
            let (body, view, kept, seed) =
                (body.clone(), view.clone(), kept.clone(), here.id.clone());
            move |marking| {
                crate::markdown::reader(
                    &body,
                    crate::markdown::Document {
                        path: String::new(),
                        session: None,
                        outpost: None,
                        view,
                        opens_seed: None,
                        seed: Some(seed.clone()),
                        part: None,
                    },
                    pictures.clone(),
                    kept,
                    marking,
                    style,
                    &t,
                )
            }
        };
        let plan = move |reading: El| {
            col()
                .key(key)
                .role("document")
                .label("Plan")
                .w_full()
                .on_layout_tx({
                    let view = view.clone();
                    move |state: &mut State, [_, _, w, _], tx: &mut Tx| {
                        state.readings.measured(&view, w, tx)
                    }
                })
                .child(if in_card {
                    crate::markdown::card(padding, reading, &t)
                } else {
                    reading.max_w(attn_ui::blocks::DOCUMENT)
                })
        };
        if let (Go::Tile { tile, .. }, Some(drafts)) = (&self.go, self.drafts) {
            let (reading, over) = crate::markdown::seed_reader(
                (drafts, self.readings),
                tile,
                &here.id,
                (&body, &self.view, &kept, pictured, t),
                read,
                &t,
            );
            return Block::part(plan(reading).children(over));
        }
        Block::part(memo(
            self.key(&format!("plan.{}", here.id)),
            (body.clone(), self.view.clone(), kept.clone(), pictured, t),
            move || plan(read(None)),
        ))
    }

    /// The seeds `rows` name, each a button that goes to it beside its state, under `title`.
    pub(super) fn seeds(&self, part: &str, title: &str, rows: Vec<Relation>) -> Option<Block> {
        self.seeds_saying(part, title, rows, |seed| Some(seed.status.clone()))
    }

    /// The seeds `rows` name, each a button that goes to it beside what `says` of it.
    pub(super) fn seeds_saying(
        &self,
        part: &str,
        title: &str,
        rows: Vec<Relation>,
        says: impl Fn(&Seed) -> Option<String>,
    ) -> Option<Block> {
        if rows.is_empty() {
            return None;
        }
        let t = self.t;
        let mut list = Stack::tight().w_full();
        for relation in rows {
            let other: Arc<Seed> = relation.seed.clone();
            let go = self.go.clone();
            let id = other.id.clone();
            let key = if relation.label.is_empty() {
                self.key(&format!("{part}.{}", other.id))
            } else {
                self.key(&format!("{part}.{}.{}", relation.label, other.id))
            };
            let link: El = Link::new(key, other.title.clone())
                .kind(Type::Body)
                .ink(t.text_primary)
                .build(t)
                .into();
            let line = row()
                .w_full()
                .items_center()
                .justify_between()
                .child(
                    Inline::loose()
                        .children((!relation.label.is_empty()).then(|| {
                            words(relation.label, Type::Small, t.text_tertiary).w(RELATION_LABEL)
                        }))
                        .child(link.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            go.to(state, &id, tx)
                        }))
                        .w(0.0)
                        .grow(1.0)
                        .min_w(0.0),
                )
                .children(says(&other).map(|words| said(words, t)));
            list = list.child(line);
        }
        Some(
            self.section(part, title)
                .push(Block::custom(self.key(&format!("{part}.list")), list))
                .build(t),
        )
    }

    pub(super) fn log(&self, document: Option<&SeedDocument>, error: Option<&str>) -> Block {
        let t = self.t;
        let log = self.section("log", "Log");
        if let Some(error) = error {
            return log.push(self.note("log.error", error.to_owned())).build(t);
        }
        let Some(document) = document else {
            return log.build(t);
        };
        let mut log = log;
        if document.notes.is_empty() {
            log = log.push(self.note("log.empty", "Nothing on this seed’s log yet."));
        }
        let spoken = document
            .notes
            .iter()
            .filter(|n| n.kind != "attach" && n.kind != "detach");
        let shown: Vec<El> = spoken
            .map(|entry| {
                let who = model::who_name(&entry.author.r#ref, &entry.author.name, self.names);
                let mut item = Entry::new(
                    self.key(&format!("log.{}", entry.id)),
                    who,
                    model::ago(&entry.created_at, self.now_ms),
                )
                .accent(entry.kind == "handoff");
                if entry.kind != "note" {
                    item = item.kind(entry.kind.clone());
                }
                if !entry.body.is_empty() {
                    item = item.body(self.log_body(&entry.id, &entry.body));
                }
                item.build(t)
            })
            .collect();
        if !shown.is_empty() {
            log = log.push(Block::custom(self.key("log.entries"), entries(shown)));
        }
        let withheld = (document.notes_total - document.notes.len() as i64).max(0);
        if withheld > 0 {
            log = log.push(self.note(
                "log.withheld",
                format!(
                    "{withheld} more {} on the log.",
                    if withheld == 1 { "entry" } else { "entries" }
                ),
            ));
        }
        log.build(t)
    }
}

/// How wide the name of a relation runs before the seed it names: "discovered from", the
/// longest, in `Small`.
const RELATION_LABEL: f32 = 96.0;

/// How far a plot has drained: done, growing, parked and withered children and the rest.
fn progress_meter(here: &Seed, t: &Tokens) -> Option<El> {
    let p = here.plot_progress.as_ref().filter(|p| p.total > 0)?;
    let rest = (p.total - p.done - p.growing - p.parked - p.withered).max(0);
    Some(meter([
        ((p.done - p.withered) as f32, t.text_muted),
        (p.growing as f32, t.brand),
        (p.parked as f32, t.warning),
        (p.withered as f32, t.error),
        (rest as f32, t.accent.alpha(0.5)),
    ]))
}

/// What a seed's page says about it in passing.
pub(super) fn said(said: impl Into<Said>, t: &Tokens) -> El {
    words(said, Type::Small, t.text_tertiary)
}

/// A field for words that its grip drags taller.
pub(super) fn words_field(
    written: &TextEdit,
    key: String,
    label: &str,
    placeholder: &str,
    lens: impl for<'a> Fn(&'a mut State) -> &'a mut TextEdit + 'static,
    keyed: impl Fn(&mut State, &KeyEvent, &mut Tx) -> bool + 'static,
    t: &Tokens,
) -> El {
    let area = TextArea::new(written)
        .key(key)
        .rows(2)
        .resizable()
        .label(label.to_owned())
        .placeholder(placeholder.to_owned())
        .on_edit(lens)
        .on_key(keyed);
    area_box(area, FieldState::Ready, t).into()
}

/// `body` with each single newline outside a code fence made a hard break, so the lines a log
/// entry was written in stay lines.
fn line_breaks(body: &str) -> String {
    let mut fence: Option<&str> = None;
    let lines: Vec<&str> = body.lines().collect();
    let mut out = String::with_capacity(body.len() + lines.len() * 2);
    for (i, line) in lines.iter().enumerate() {
        let marker = ["```", "~~~"]
            .into_iter()
            .find(|m| line.trim_start().starts_with(m));
        match (fence, marker) {
            (None, Some(m)) => fence = Some(m),
            (Some(open), Some(m)) if open == m => fence = None,
            _ => {}
        }
        out.push_str(line);
        let next_written = lines.get(i + 1).is_some_and(|n| !n.trim().is_empty());
        if fence.is_none() && marker.is_none() && !line.trim().is_empty() && next_written {
            out.push_str("  ");
        }
        out.push('\n');
    }
    out
}
