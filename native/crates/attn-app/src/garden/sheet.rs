use std::sync::Arc;

use attn_protocol::{DesktopUpdateTileMessage, Seed, SeedCheckTally, SeedDocument};
use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::space;
use rune::{El, TextStyle, Tx, col, memo, row, text};
use rune_kit::MarkdownStyle;

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
    /// The marks people make on the seed's body, in a tile that takes them.
    pub(super) drafts: Option<&'a crate::markdown::Drafts>,
    pub(super) now_ms: i64,
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

    /// What a part of the page says when it has nothing to show, as the Tauri garden's
    /// `.garden-note-empty`.
    pub(super) fn note(&self, part: &str, words: impl Into<String>) -> El {
        quiet_note(&self.key(part), words, self.t.text_muted)
    }

    pub(super) fn section(&self, part: &str, title: &str) -> El {
        page_section(&self.key(part), title, self.t)
    }

    /// The seed's title beside `action`, then its state, who tends it, its checks, its age and
    /// id, then `notes`, why it closed and its plot's progress.
    pub(super) fn head(&self, here: &Seed, action: El, notes: Vec<El>) -> El {
        let t = self.t;
        let mut head = col().key(self.key("head")).w_full().gap(7.0).child(
            row()
                .w_full()
                .items_start()
                .gap(16.0)
                .child(
                    text(here.title.clone())
                        .key(self.key("head.title"))
                        .role("heading")
                        .label(here.title.clone())
                        .font(PAGE_TITLE)
                        .color(t.text_primary)
                        .wrap()
                        .grow(1.0)
                        .min_w(TITLE_MIN_WIDTH),
                )
                .child(
                    action
                        .gap(6.0)
                        .mt(1.0)
                        .flex_wrap()
                        .justify_end()
                        .shrink()
                        .min_w(0.0),
                ),
        );
        let mut meta = row()
            .key(self.key("head.meta"))
            .w_full()
            .flex_wrap()
            .items_center()
            .gap(12.0)
            .child(
                row()
                    .items_center()
                    .gap(6.0)
                    .child(super::view::pip(&here.status, t))
                    .child(said(here.status.clone(), t)),
            );
        if let Some((sentence, _)) = model::harvest_when(here) {
            meta = meta.child(self.harvest_when(here, sentence));
        }
        let tender = model::tender_of(here, self.names);
        if !tender.is_empty() {
            meta = meta.child(said(format!("tended by {tender}"), t));
        }
        if here.planter.r#ref.starts_with("member:") {
            meta = meta.child(said(format!("by {}", here.planter.name), t));
        }
        if !model::is_plot(here)
            && let Some(tally) = model::check_tally(here)
        {
            meta = meta.child(self.tally(here, &tally));
        }
        meta = meta
            .child(said(model::ago(&here.created_at, self.now_ms), t))
            .child(
                text(here.id.clone())
                    .font(type_::MONO_CAPTION)
                    .color(t.text_muted),
            );
        head = head.child(meta).children(notes);
        if let Some(reason) = here.reason.as_ref().filter(|r| !r.is_empty()) {
            head = head.child(
                text(reason.clone())
                    .key(self.key("head.reason"))
                    .role("note")
                    .label(reason.clone())
                    .font(type_::BODY)
                    .color(t.text_secondary)
                    .wrap()
                    .mt(6.0),
            );
        }
        if model::is_plot(here) {
            head = head.child(
                row()
                    .key(self.key("head.progress"))
                    .items_center()
                    .gap(10.0)
                    .children(progress_bar(here, t))
                    .child(
                        said(model::progress_words(here), t)
                            .key(self.key("head.progress.words"))
                            .role("note")
                            .label(model::progress_words(here)),
                    )
                    .children(model::check_tally(here).map(|tally| self.tally(here, &tally))),
            );
        }
        let gone_to = self
            .garden
            .check_focus
            .as_ref()
            .is_some_and(|f| f.sheet == self.prefix && f.seed == here.id);
        if !gone_to {
            return head;
        }
        head.on_layout_tx(|state: &mut State, _, tx: &mut Tx| {
            if let Some(focus) = state.window.garden.check_focus.take() {
                tx.scroll_center(focus.check_box.clone());
                tx.focus(&focus.check_box, false);
            }
        })
    }

    /// The seed's check tally, which goes to its checks when it has checks of its own.
    fn tally(&self, here: &Seed, tally: &SeedCheckTally) -> El {
        let (words, sentence) = (model::tally_words(tally), model::tally_sentence(tally));
        let own = here.checks.as_ref().is_some_and(|c| !c.is_empty())
            || here.shared_checks.as_ref().is_some_and(|c| !c.is_empty());
        if !own {
            return said(words, self.t).help(format!("checks: {sentence}"));
        }
        let (checks, heading) = (self.key("checks"), self.key("checks.heading"));
        said(words, self.t)
            .key(self.key("head.tally"))
            .role("button")
            .focusable(true)
            .on_press_tx(move |_: &mut State, tx: &mut Tx| {
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
        super::view::kit_button(format!("{sentence} ↗"))
            .help(url.clone())
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                if let Err(why) = tx.open_url(&url) {
                    state
                        .window
                        .notify(format!("Could not open {url}: {why}"), tx);
                }
            })
            .build(attn_ui::kit(self.t))
            .key(self.key("head.harvest-when"))
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
                    },
                    std::rc::Rc::default(),
                    kept,
                    None,
                    body_look(t.text_secondary, &t),
                    &t,
                )
            },
        )
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
        let kept = self.readings.kept(&view);
        let style = {
            let look = body_look(t.text_primary.alpha(0.9), &t);
            match kept.width {
                Some(width) => look.text_width(width as f32),
                None => look,
            }
        };
        let read = {
            let (body, view, kept) = (body.clone(), view.clone(), kept.clone());
            move |marking| {
                crate::markdown::reader(
                    &body,
                    crate::markdown::Document {
                        path: String::new(),
                        session: None,
                        outpost: None,
                        view,
                        opens_seed: None,
                    },
                    std::rc::Rc::default(),
                    kept,
                    marking,
                    style,
                    &t,
                )
            }
        };
        let plan = |reading: El| {
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
                .child(reading)
        };
        if let (Go::Tile { tile, .. }, Some(drafts)) = (&self.go, self.drafts) {
            let (reading, over) = crate::markdown::seed_reader(
                (drafts, self.readings),
                tile,
                &here.id,
                (&body, &view, &kept, t),
                read,
                &t,
            );
            return plan(reading).children(over);
        }
        memo(
            self.key(&format!("plan.{}", here.id)),
            (body.clone(), view.clone(), kept.clone(), t),
            move || plan(read(None)),
        )
    }

    /// The seeds `rows` name, each a button that goes to it beside its state, under `title`.
    pub(super) fn seeds(&self, part: &str, title: &str, rows: Vec<Relation>) -> Option<El> {
        self.seeds_saying(part, title, rows, |seed| Some(seed.status.clone()))
    }

    /// The seeds `rows` name, each a button that goes to it beside what `says` of it.
    pub(super) fn seeds_saying(
        &self,
        part: &str,
        title: &str,
        rows: Vec<Relation>,
        says: impl Fn(&Seed) -> Option<String>,
    ) -> Option<El> {
        if rows.is_empty() {
            return None;
        }
        let t = self.t;
        let mut list = col().w_full().gap(4.0);
        for relation in rows {
            let other: Arc<Seed> = relation.seed.clone();
            let go = self.go.clone();
            let id = other.id.clone();
            let mut line = row().w_full().gap(10.0).items_center();
            if !relation.label.is_empty() {
                line = line.child(
                    text(relation.label)
                        .font(type_::CAPTION)
                        .color(t.text_muted)
                        .w(84.0),
                );
            }
            let key = if relation.label.is_empty() {
                self.key(&format!("{part}.{}", other.id))
            } else {
                self.key(&format!("{part}.{}.{}", relation.label, other.id))
            };
            list = list.child(
                line.child(
                    super::view::text_button(
                        key,
                        other.title.clone(),
                        (type_::BODY, t.text_primary),
                        t,
                    )
                    .grow(1.0)
                    .justify_start()
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| go.to(state, &id, tx)),
                )
                .children(says(&other).map(|words| said(words, t))),
            );
        }
        let list = self.section(part, title).child(list);
        Some(list)
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
        let mut entries = col().w_full().gap(12.0);
        for entry in spoken {
            let who = model::who_name(&entry.author.r#ref, &entry.author.name, self.names);
            let mut head = row()
                .w_full()
                .items_center()
                .gap(space::S2)
                .child(text(who).font(type_::CAPTION).color(t.text_secondary));
            if entry.kind != "note" {
                head = head.child(
                    text(entry.kind.clone())
                        .font(type_::CAPTION)
                        .color(t.text_tertiary),
                );
            }
            head = head.child(rune::el().grow(1.0)).child(
                text(model::ago(&entry.created_at, self.now_ms))
                    .font(type_::CAPTION)
                    .color(t.text_muted),
            );
            let mut item = col()
                .key(self.key(&format!("log.{}", entry.id)))
                .role("listitem")
                .w_full()
                .gap(4.0)
                .pl(10.0)
                .border_sides(
                    [0.0, 0.0, 0.0, 2.0],
                    if entry.kind == "handoff" {
                        t.brand
                    } else {
                        t.border_subtle
                    },
                )
                .child(head);
            if !entry.body.is_empty() {
                item = item.child(self.log_body(&entry.id, &entry.body));
            }
            entries = entries.child(item);
        }
        log = log.child(entries);
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

/// The page's title, `.garden-head__title`.
const PAGE_TITLE: TextStyle = TextStyle::new(18.0, 600.0).lh(22.5);

/// How narrow a seed's title gets before its actions wrap under one another instead.
const TITLE_MIN_WIDTH: f32 = 160.0;

/// A section's heading on a seed's page, `.garden-section h3`.
const HEADING: TextStyle = TextStyle::new(12.0, 600.0).tracking(0.72);

/// A seed's body and its log entries' words in `ink`, as the Tauri garden reads them: the
/// file reader's document, in the garden's 13 points with narrower lists.
fn body_look(ink: rune::Color, t: &Tokens) -> MarkdownStyle {
    let body = TextStyle {
        size: 13.0,
        line_height: 21.1,
        ..type_::BODY
    };
    crate::markdown::look(t)
        .body_font(body)
        .list_font(body)
        .list(21.0, 4.0)
        .text(ink)
        .deep_heading(ink)
}

/// How far a plot has drained, as the Tauri garden's `.garden-progress`: a 160 point bar in
/// segments for done, growing, parked and withered children and the rest.
fn progress_bar(here: &Seed, t: &Tokens) -> Option<El> {
    let p = here.plot_progress.as_ref().filter(|p| p.total > 0)?;
    let rest = (p.total - p.done - p.growing - p.parked - p.withered).max(0);
    let segments = [
        (p.done - p.withered, t.text_muted),
        (p.growing, t.brand),
        (p.parked, t.warning),
        (p.withered, t.error),
        (rest, t.accent.alpha(0.5)),
    ];
    Some(
        row()
            .w(160.0)
            .h(3.0)
            .clip_rounded(2.0)
            .gap(1.0)
            .a11y_hidden()
            .children(
                segments
                    .into_iter()
                    .filter(|(count, _)| *count > 0)
                    .map(|(count, color)| rune::el().h_full().grow(count as f32).fill(color)),
            ),
    )
}

/// What a seed's page says about it in passing, as the Tauri garden's `.garden-head__meta`.
pub(super) fn said(words: impl Into<String>, t: &Tokens) -> El {
    text(words.into())
        .font(type_::CAPTION)
        .color(t.text_tertiary)
}

pub(super) fn quiet_note(key: &str, words: impl Into<String>, ink: rune::Color) -> El {
    let words = words.into();
    text(words.clone())
        .key(key.to_owned())
        .role("note")
        .label(words)
        .font(type_::CAPTION)
        .color(ink)
        .wrap()
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

/// `body` with each single newline outside a code fence made a hard break, so the lines a log
/// entry was written in stay lines, as the Tauri log reads it with `breaks`.
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

pub(super) fn section(key: &str, title: &str, t: &Tokens) -> El {
    region(key, title).child(heading(title, t))
}

/// A section of a seed's page, as the Tauri garden's `.garden-section`: a rule above it and
/// its heading in small capitals.
pub(super) fn page_section(key: &str, title: &str, t: &Tokens) -> El {
    page_region(key, title, t).child(page_heading(title, t))
}

/// A section of a seed's page that a control elsewhere on it goes to: its heading takes the
/// keyboard without being a stop on Tab.
pub(super) fn section_gone_to(key: &str, title: &str, t: &Tokens) -> El {
    page_region(key, title, t).child(
        page_heading(title, t)
            .key(format!("{key}.heading"))
            .focusable(true)
            .tab_index(-1),
    )
}

pub(super) fn page_region(key: &str, title: &str, t: &Tokens) -> El {
    col()
        .key(key.to_owned())
        .role("region")
        .label(title.to_owned())
        .w_full()
        .gap(space::S2)
        .pt(14.0)
        .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)
}

pub(super) fn page_heading(title: &str, t: &Tokens) -> El {
    type_::upper(title, HEADING)
        .role("heading")
        .color(t.text_muted)
}

fn region(key: &str, title: &str) -> El {
    col()
        .key(key.to_owned())
        .role("region")
        .label(title.to_owned())
        .w_full()
        .gap(space::S1)
        .pt(space::S3)
}

fn heading(title: &str, t: &Tokens) -> El {
    text(title.to_owned())
        .role("heading")
        .label(title.to_owned())
        .font(type_::SECTION)
        .color(t.text_tertiary)
}

pub(super) fn meta_word(words: impl Into<String>, t: &Tokens) -> El {
    text(words.into())
        .font(type_::LABEL)
        .color(t.text_secondary)
}
