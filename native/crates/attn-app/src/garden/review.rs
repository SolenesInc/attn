use std::sync::Arc;

use attn_protocol::{
    GardenReview, GardenReviewItem, SeedResumeMessage, SeedResumeResultMessage,
    SeedReviewActionContext, SeedReviewDraftMessage, SeedReviewDraftResultMessage,
    SeedReviewKeepMessage, SeedReviewResultMessage, SeedReviewRetryMessage, SeedReviewShowMessage,
    SeedReviewStartMessage, SeedTransitionMessage,
};
use attn_ui::type_;
use attn_ui::{GlyphButton, Tokens};
use rune::tok::space;
use rune::{El, Key, KeyEvent, TextEdit, Tx, col, row, text, text_input};
use rune_kit::ButtonVariant;

use super::handover::Handover;
use super::model::{self, Index};
use super::sheet::{Sheet, meta_word, note, section};
use super::spot::{Sends, Spot};
use super::view::{button, kit_button};
use super::{BODY, Mode};
use crate::State;

pub(super) const AT: &str = "garden.review";
const PANEL: &str = "garden.review";
const HARVEST_REASON: &str = "The seed's stated outcome and required verification are complete.";
const REASON_MAX_CHARS: usize = 400;

/// The garden review as this window follows it: the overview the daemon last gave, and the
/// panel's walk through the review it shows.
#[derive(Default)]
pub(crate) struct Review {
    profile: String,
    count: i64,
    shown: Option<Arc<GardenReview>>,
    /// The broadcast last taken, by its place in memory.
    heard: usize,
    /// The garden push whose rows the overview was last read for, those rows, and its request.
    rows_at: usize,
    rows: Vec<usize>,
    asked: Option<String>,
    open: bool,
    starting: bool,
    problem: Option<String>,
    reading: Reading,
    failure: Option<String>,
    retrying: bool,
    drafting: Option<(String, String)>,
    drafted: Option<(String, String)>,
}

/// The item the review decides on, and the seed the reader shows: the item's own, or another
/// of its plot the user went to.
#[derive(Default)]
struct Reading {
    item: String,
    seed: String,
}

#[derive(Clone, Copy, PartialEq)]
enum Kind {
    Park,
    Wither,
    Handover,
    Chief,
}

impl Kind {
    fn part(self) -> &'static str {
        match self {
            Kind::Park => "park",
            Kind::Wither => "wither",
            Kind::Handover => "handover",
            Kind::Chief => "chief",
        }
    }

    fn of(action: &str) -> Option<Kind> {
        match action {
            "park" => Some(Kind::Park),
            "wither" => Some(Kind::Wither),
            "handover" => Some(Kind::Handover),
            "send_to_chief" => Some(Kind::Chief),
            _ => None,
        }
    }

    fn prompt(self) -> &'static str {
        match self {
            Kind::Park => "Why are you parking this? (optional)",
            Kind::Wither => "Why should this be withered? (optional)",
            Kind::Handover => "What should the new agent know? (optional)",
            Kind::Chief => "What should Chief know? (optional)",
        }
    }

    const ALL: [Kind; 4] = [Kind::Park, Kind::Wither, Kind::Handover, Kind::Chief];
}

fn action_label(action: &str) -> &str {
    match action {
        "resume" => "Resume",
        "handover" => "Handover",
        "chief" | "send_to_chief" => "Send to Chief",
        "keep_growing" => "Keep growing",
        "park" => "Park",
        "harvest" => "Harvest",
        "wither" => "Wither",
        other => other,
    }
}

fn spot(seed: &str, kind: Kind) -> Spot {
    Spot::new(AT, seed, kind.part())
}

fn guidance(item: &GardenReviewItem, action: &str) -> String {
    if item.recommendation.as_deref() != Some(action) {
        return String::new();
    }
    item.explanation
        .as_deref()
        .map(str::trim)
        .unwrap_or_default()
        .to_owned()
}

fn capped_reason(words: &str) -> String {
    let words = words.trim();
    if words.chars().count() <= REASON_MAX_CHARS {
        return words.to_owned();
    }
    let kept: String = words.chars().take(REASON_MAX_CHARS - 1).collect();
    format!("{kept}…")
}

fn status_words(item: &GardenReviewItem) -> &'static str {
    match (item.resolution.as_str(), item.status.as_str()) {
        ("resolved", _) if item.resolved_action.as_deref() == Some("keep_growing") => {
            "kept growing"
        }
        ("resolved", _) => "resolved",
        ("no_longer_applicable", _) => "no longer needs review",
        (_, "queued") if item.advisor_state.as_deref() == Some("retrying") => "advisor retrying",
        (_, "queued") => "waiting for advisor",
        (_, "running") => "advisor is reviewing",
        (_, "failed") => "advisor failed",
        (_, "invalidated") => "seed changed",
        _ => "ready",
    }
}

fn unresolved(review: &GardenReview) -> impl Iterator<Item = &GardenReviewItem> {
    review.items.iter().filter(|i| i.resolution == "unresolved")
}

fn advised(review: &GardenReview) -> usize {
    unresolved(review).filter(|i| i.status == "ready").count()
}

fn age_words(iso: Option<&str>, now_ms: i64) -> String {
    let Some(at) = iso.and_then(crate::sidebar::queue::epoch_ms) else {
        return String::new();
    };
    let seconds = ((now_ms - at) as f64 / 1000.0).round().max(0.0) as i64;
    match seconds {
        0..10 => "just now".into(),
        10..60 => format!("{seconds}s ago"),
        _ => format!("{}m ago", (seconds as f64 / 60.0).round() as i64),
    }
}

fn until_words(iso: Option<&str>, now_ms: i64) -> String {
    let Some(at) = iso.and_then(crate::sidebar::queue::epoch_ms) else {
        return String::new();
    };
    let seconds = ((at - now_ms) as f64 / 1000.0).ceil().max(0.0) as i64;
    match seconds {
        0..10 => "in a few seconds".into(),
        10..60 => format!("in {seconds}s"),
        _ => format!("in {}m", (seconds as f64 / 60.0).ceil() as i64),
    }
}

fn advisor_failure_words(error: Option<&str>) -> String {
    match error {
        None | Some("") => "The advisor stopped before it produced advice.".into(),
        Some(e) if e.contains("invalid advice") || e.contains("does not match its schema") => {
            "The advisor stopped because its answers did not match the expected format.".into()
        }
        Some(e) if e.contains("deadline exceeded") || e.contains("took too long") => {
            "The advisor stopped because its attempts took too long.".into()
        }
        Some(e) => e.to_owned(),
    }
}

fn review(state: &State) -> &Review {
    &state.window.garden.review
}

fn review_mut(state: &mut State) -> &mut Review {
    let garden = &mut state.window.garden;
    garden.revision += 1;
    &mut garden.review
}

/// The item the panel decides on: the one chosen, or else the first still unresolved.
fn selected(review: &Review) -> Option<&GardenReviewItem> {
    let shown = review.shown.as_deref()?;
    shown
        .items
        .iter()
        .find(|i| i.id == review.reading.item)
        .or_else(|| unresolved(shown).next())
        .or_else(|| shown.items.first())
}

/// Whether the garden shows the review instead of its walk.
pub(super) fn is_open(state: &State) -> bool {
    let review = review(state);
    review.open && review.shown.is_some()
}

/// The seed the open review reads, for the garden to read its document.
pub(super) fn browsed(state: &State) -> Option<String> {
    is_open(state).then(|| review(state).reading.seed.clone())
}

/// The receipt a move made from the review carries: the review and the evidence it decided on.
pub(super) fn receipt(spot: &Spot, state: &State) -> Option<SeedReviewActionContext> {
    if spot.at() != AT {
        return None;
    }
    let review = review(state);
    let shown = review.shown.as_deref()?;
    let item = shown.items.iter().find(|i| i.seed_id == spot.seed())?;
    Some(SeedReviewActionContext {
        review_id: shown.run.id.clone(),
        evidence_version: item.evidence_version.clone(),
    })
}

fn receipt_of(review: &Review, item: &GardenReviewItem) -> Option<SeedReviewActionContext> {
    let shown = review.shown.as_deref()?;
    Some(SeedReviewActionContext {
        review_id: shown.run.id.clone(),
        evidence_version: item.evidence_version.clone(),
    })
}

/// Reads the review overview when the garden opens and whenever a push changes its rows, never
/// on a push that changes none, and takes each review the daemon broadcasts.
pub(super) fn follow(state: &mut State, tx: &mut Tx) {
    let selected = state.facts.profiles.selected.clone();
    if review(state).profile != selected {
        *review_mut(state) = Review {
            profile: selected.clone(),
            ..Review::default()
        };
    }
    let heard = Arc::as_ptr(&state.facts.garden_review) as usize;
    if review(state).heard != heard {
        let broadcast = state.facts.garden_review.as_ref().clone();
        review_mut(state).heard = heard;
        if let Some(broadcast) = broadcast.filter(|r| r.run.profile_id == selected) {
            hear(state, broadcast);
        }
    }
    if super::mode(state) == Mode::Closed {
        return;
    }
    let push = Arc::as_ptr(&state.facts.garden) as usize;
    if review(state).rows_at == push {
        return;
    }
    let rows: Vec<usize> = super::seeds(state)
        .iter()
        .map(|s| Arc::as_ptr(s) as usize)
        .collect();
    let review = &mut state.window.garden.review;
    review.rows_at = push;
    if review.rows == rows && review.asked.is_some() {
        return;
    }
    review.rows = rows;
    read_overview(state, tx);
}

/// The garden opens on its walk, and reads the overview afresh.
pub(super) fn opened(state: &mut State) {
    let review = review_mut(state);
    review.open = false;
    review.problem = None;
    review.asked = None;
    review.rows.clear();
    review.rows_at = 0;
}

fn read_overview(state: &mut State, tx: &mut Tx) {
    let id = state.next_request_id();
    review_mut(state).asked = Some(id.clone());
    let command = SeedReviewShowMessage {
        request_id: Some(id.clone()),
        ..SeedReviewShowMessage::new()
    };
    state.ask_as(
        id.clone(),
        &command,
        move |state, answer, _| {
            if review(state).asked.as_deref() != Some(id.as_str()) {
                return;
            }
            match answer.outcome() {
                Ok(reply) => take(state, reply.parse::<SeedReviewResultMessage>()),
                Err(reason) => review_mut(state).problem = Some(reason),
            }
        },
        tx,
    );
}

/// A broadcast review, unless it is another run captured before the one shown.
fn hear(state: &mut State, broadcast: GardenReview) {
    let review = review_mut(state);
    if let Some(shown) = review.shown.as_deref()
        && shown.run.id != broadcast.run.id
        && let (Some(now), Some(next)) = (
            crate::sidebar::queue::epoch_ms(&shown.run.captured_at),
            crate::sidebar::queue::epoch_ms(&broadcast.run.captured_at),
        )
        && next < now
    {
        return;
    }
    review.count = unresolved(&broadcast).count() as i64;
    review.shown = Some(Arc::new(broadcast));
    settle_selection(state);
}

/// A review result for the selected profile becomes the overview.
fn take(state: &mut State, result: Option<SeedReviewResultMessage>) {
    let Some(result) = result else {
        return;
    };
    if result.profile_id != state.facts.profiles.selected {
        return;
    }
    let review = review_mut(state);
    review.count = result.candidate_count;
    review.shown = result.review.map(Arc::new);
    settle_selection(state);
}

/// Moves off an item that is no longer unresolved while some still are, and reads the item
/// chosen; a composer for another seed closes.
fn settle_selection(state: &mut State) {
    let review = review(state);
    let Some(shown) = review.shown.as_deref() else {
        return;
    };
    let current = shown.items.iter().find(|i| i.id == review.reading.item);
    let item = match unresolved(shown).next() {
        Some(first) if current.is_none_or(|i| i.resolution != "unresolved") => Some(first),
        _ => selected(review),
    };
    if let Some(item) = item.cloned() {
        choose(state, &item);
    }
}

/// Decides on `item`, reading its own seed unless it is already the item decided on.
fn choose(state: &mut State, item: &GardenReviewItem) {
    let review = review_mut(state);
    if review.reading.item != item.id {
        review.reading = Reading {
            item: item.id.clone(),
            seed: item.seed_id.clone(),
        };
        review.failure = None;
    }
    forget_composers(state, Some(&item.seed_id));
}

fn forget_composers(state: &mut State, keep: Option<&str>) {
    let seeds: Vec<String> = state
        .window
        .garden
        .review_spots()
        .filter(|s| keep != Some(s.seed()))
        .map(|s| s.seed().to_owned())
        .collect();
    for seed in seeds {
        for kind in Kind::ALL {
            spot(&seed, kind).forget(state);
        }
    }
    if keep.is_none() {
        let review = review_mut(state);
        review.drafting = None;
        review.drafted = None;
    }
}

fn composing(state: &State) -> Option<(Spot, Kind)> {
    let item = selected(review(state))?;
    Kind::ALL
        .into_iter()
        .map(|k| (spot(&item.seed_id, k), k))
        .find(|(s, _)| state.window.garden.shown(s).writing)
}

/// Opens the review: the one still running, or a new one the daemon starts.
fn begin(state: &mut State, tx: &mut Tx) {
    let running = review(state)
        .shown
        .as_deref()
        .is_some_and(|r| r.run.status == "running");
    if running {
        open_panel(state, tx);
        return;
    }
    let review = review_mut(state);
    review.problem = None;
    review.starting = true;
    state.ask(
        |request_id| SeedReviewStartMessage {
            request_id: Some(request_id),
            ..SeedReviewStartMessage::new()
        },
        |state, answer, tx| {
            review_mut(state).starting = false;
            match answer.outcome() {
                Ok(reply) => {
                    take(state, reply.parse::<SeedReviewResultMessage>());
                    open_panel(state, tx);
                }
                Err(reason) => review_mut(state).problem = Some(reason),
            }
        },
        tx,
    );
}

fn open_panel(state: &mut State, tx: &mut Tx) {
    let review = review_mut(state);
    review.open = true;
    review.reading = Reading::default();
    settle_selection(state);
    tx.focus(PANEL.to_owned(), true);
    super::follow(state, tx);
}

fn exit(state: &mut State, tx: &mut Tx) {
    forget_composers(state, None);
    review_mut(state).open = false;
    tx.focus(BODY.to_owned(), true);
}

fn select(state: &mut State, id: &str, tx: &mut Tx) {
    let Some(item) = review(state)
        .shown
        .as_deref()
        .and_then(|s| s.items.iter().find(|i| i.id == id))
        .cloned()
    else {
        return;
    };
    choose(state, &item);
    super::follow(state, tx);
}

/// Reads another seed in the open review, around the plot of the seed under review.
pub(super) fn browse(state: &mut State, id: &str, tx: &mut Tx) {
    forget_composers(state, None);
    let review = review_mut(state);
    review.reading.seed = id.to_owned();
    review.failure = None;
    super::follow(state, tx);
}

fn refresh(state: &mut State, tx: &mut Tx) {
    let Some(run) = review(state).shown.as_deref().map(|r| r.run.id.clone()) else {
        return;
    };
    state.ask(
        |request_id| SeedReviewShowMessage {
            request_id: Some(request_id),
            review_id: Some(run),
            ..SeedReviewShowMessage::new()
        },
        |state, answer, _| {
            if let Ok(reply) = answer.outcome() {
                take(state, reply.parse::<SeedReviewResultMessage>());
            }
        },
        tx,
    );
}

/// After a move the review asked for: shows why it failed, and reads the review again.
fn after_move(state: &mut State, failed: Option<String>, tx: &mut Tx) {
    if let Some(reason) = failed {
        review_mut(state).failure = Some(reason);
    }
    refresh(state, tx);
}

/// Reads the review again once a move from one of its composers is answered.
pub(super) fn settled(spot: &Spot, state: &mut State, tx: &mut Tx) {
    if spot.at() == AT {
        refresh(state, tx);
    }
}

fn choose_action(state: &mut State, item: &GardenReviewItem, action: &str, tx: &mut Tx) {
    let Some(receipt) = receipt_of(review(state), item) else {
        return;
    };
    review_mut(state).failure = None;
    let seed_id = item.seed_id.clone();
    match action {
        "resume" => state.ask(
            |request_id| SeedResumeMessage {
                seed_id,
                request_id: Some(request_id),
                review: Some(receipt),
                ..SeedResumeMessage::new()
            },
            |state, answer, tx| {
                let session = answer
                    .parse::<SeedResumeResultMessage>()
                    .and_then(|r| r.session_id);
                let failed = answer.outcome().err();
                if failed.is_none()
                    && let Some(session) = session
                {
                    crate::sidebar::open_agent(state, &session, tx);
                }
                after_move(state, failed, tx);
            },
            tx,
        ),
        "keep_growing" => state.ask(
            |request_id| SeedReviewKeepMessage {
                seed_id,
                request_id: Some(request_id),
                review: receipt,
                ..SeedReviewKeepMessage::new()
            },
            |state, answer, tx| {
                let failed = answer.outcome().err();
                after_move(state, failed, tx);
            },
            tx,
        ),
        "harvest" => {
            let reason = Some(capped_reason(&guidance(item, "harvest")))
                .filter(|r| !r.is_empty())
                .unwrap_or_else(|| HARVEST_REASON.to_owned());
            state.ask(
                |request_id| SeedTransitionMessage {
                    seed_id,
                    verb: "harvest".into(),
                    reason: Some(reason),
                    request_id: Some(request_id),
                    review: Some(receipt),
                    ..SeedTransitionMessage::new()
                },
                |state, answer, tx| {
                    let failed = answer.outcome().err();
                    after_move(state, failed, tx);
                },
                tx,
            );
        }
        _ => {
            if let Some(kind) = Kind::of(action) {
                compose(state, item, kind, tx);
            }
        }
    }
}

fn compose(state: &mut State, item: &GardenReviewItem, kind: Kind, tx: &mut Tx) {
    forget_composers(state, None);
    let words = match kind {
        Kind::Wither => capped_reason(&guidance(item, "wither")),
        Kind::Park => guidance(item, "park"),
        Kind::Handover => guidance(item, "handover"),
        Kind::Chief => guidance(item, "chief"),
    };
    let spot = spot(&item.seed_id, kind);
    let draft = (kind == Kind::Handover).then(|| {
        let garden = &state.window.garden;
        let read = super::reading(garden, &item.seed_id).0.map(|d| &d.seed);
        let held = super::seeds(state).iter().find(|s| s.id == item.seed_id);
        let continuation = read
            .and_then(|s| s.continuation.as_ref())
            .or_else(|| held.and_then(|s| s.continuation.as_ref()));
        Handover::suggested(continuation)
    });
    spot.write_with(state, &words, draft, tx);
}

fn lifecycle(spot: &Spot, kind: Kind, state: &mut State, words: String, tx: &mut Tx) {
    let Some(receipt) = receipt(spot, state) else {
        return;
    };
    let seed_id = spot.seed().to_owned();
    let words = Some(words).filter(|w| !w.is_empty());
    let (reason, comment) = match kind {
        Kind::Park => (None, words),
        _ => (words, None),
    };
    spot.send_as(
        state,
        None,
        |request_id| SeedTransitionMessage {
            seed_id,
            verb: kind.part().into(),
            reason,
            comment,
            request_id: Some(request_id),
            review: Some(receipt),
            ..SeedTransitionMessage::new()
        },
        spot.key("open"),
        |state, _, tx| refresh(state, tx),
        tx,
    );
}

fn draft(state: &mut State, spot: &Spot, tx: &mut Tx) {
    let Some(receipt) = receipt(spot, state) else {
        return;
    };
    let id = state.next_request_id();
    let base = spot.words(state);
    let asking = review_mut(state);
    asking.drafting = Some((id.clone(), base));
    asking.drafted = None;
    let command = SeedReviewDraftMessage {
        request_id: id.clone(),
        seed_id: spot.seed().to_owned(),
        review: receipt,
        ..SeedReviewDraftMessage::new()
    };
    let spot = spot.clone();
    state.ask_as(
        id.clone(),
        &command,
        move |state, answer, _| {
            let Some((asked, base)) = review(state).drafting.clone() else {
                return;
            };
            if asked != id {
                return;
            }
            review_mut(state).drafting = None;
            if !state.window.garden.shown(&spot).writing {
                return;
            }
            let result = answer.parse::<SeedReviewDraftResultMessage>();
            let handoff = result.as_ref().and_then(|r| r.handoff.clone());
            match (answer.outcome(), handoff) {
                (Ok(_), Some(handoff)) => {
                    let words = spot.words(state);
                    if words == base {
                        *spot.words_mut(state) = TextEdit::new(handoff);
                    } else {
                        review_mut(state).drafted = Some((handoff, words));
                    }
                }
                (Ok(_), None) => spot.fail(state, "Drafting the handoff failed".into()),
                (Err(reason), _) => spot.fail(state, reason),
            }
        },
        tx,
    );
}

fn use_draft(state: &mut State, spot: &Spot) {
    if let Some((handoff, _)) = review_mut(state).drafted.take() {
        *spot.words_mut(state) = TextEdit::new(handoff);
    }
}

fn retry(state: &mut State, item: &GardenReviewItem, tx: &mut Tx) {
    let Some(run) = review(state).shown.as_deref().map(|r| r.run.id.clone()) else {
        return;
    };
    let review = review_mut(state);
    review.retrying = true;
    review.failure = None;
    let seed_id = item.seed_id.clone();
    state.ask(
        |request_id| SeedReviewRetryMessage {
            review_id: run,
            seed_id,
            request_id: Some(request_id),
            ..SeedReviewRetryMessage::new()
        },
        |state, answer, _| {
            review_mut(state).retrying = false;
            match answer.outcome() {
                Ok(reply) => take(state, reply.parse::<SeedReviewResultMessage>()),
                Err(reason) => review_mut(state).failure = Some(reason),
            }
        },
        tx,
    );
}

/// Escape inside the open review closes its composer first, then leaves the review.
fn key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.key != Key::Escape {
        return false;
    }
    match composing(state) {
        Some((spot, _)) => spot.put_away(state, &spot.key("open"), tx),
        None => exit(state, tx),
    }
    true
}

/// How many seeds wait for review and the way into it, above the garden's root.
pub(super) fn prompt(state: &State, t: &Tokens) -> Vec<El> {
    let review = review(state);
    let mut shown = Vec::new();
    if review.count > 0 {
        let n = review.count;
        let headline = format!(
            "{n} {} review",
            if n == 1 { "seed needs" } else { "seeds need" }
        );
        let go: El = if review.starting {
            text("Starting review…")
                .role("status")
                .label("Starting review…")
                .font(type_::LABEL)
                .color(t.text_secondary)
        } else {
            let running = review
                .shown
                .as_deref()
                .is_some_and(|r| r.run.status == "running");
            kit_button(if running {
                "Continue review"
            } else {
                "Review garden"
            })
            .variant(ButtonVariant::Neutral)
            .on_press_tx(begin)
            .build(attn_ui::kit(t))
            .key("garden.review-prompt.open")
        };
        shown.push(
            row()
                .key("garden.review-prompt")
                .role("group")
                .label(headline.clone())
                .w_full()
                .items_center()
                .gap(space::S2)
                .padding(space::S2)
                .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
                .child(
                    col()
                        .grow(1.0)
                        .gap(2.0)
                        .child(
                            text(headline.clone())
                                .label(headline)
                                .font(rune::TextStyle {
                                    weight: 600.0,
                                    ..type_::LABEL
                                })
                                .color(t.text_primary),
                        )
                        .child(
                            text("Work that may be finished, parked, or ready for another agent.")
                                .font(type_::LABEL)
                                .color(t.text_secondary)
                                .wrap(),
                        ),
                )
                .child(go),
        );
    }
    if let Some(problem) = &review.problem {
        shown.push(alert("garden.review-prompt.error", problem, t));
    }
    shown
}

fn alert(key: &str, words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .key(key.to_owned())
        .role("alert")
        .label(words.to_owned())
        .font(type_::LABEL)
        .color(t.error)
        .wrap()
        .padding(space::S2)
}

/// The open review: its header, the seeds it holds, the seed being read, and the decision.
pub(super) fn view(state: &State, sheet: &Sheet, full: bool, t: &Tokens) -> El {
    let review = review(state);
    let shown = review.shown.as_deref().expect("an open review shows one");
    let complete = unresolved(shown).next().is_none();
    let item = selected(review);
    let panel = col()
        .key(PANEL)
        .role("region")
        .label("Review garden")
        .focusable(true)
        .w_full()
        .h_full()
        .min_h(0.0)
        .on_key(key)
        .child(header(shown, complete, full, t));
    if complete {
        return panel.child(super::view::scroll(
            "garden.review.viewport",
            finished(review, t),
        ));
    }
    let Some(item) = item else {
        return panel;
    };
    let rail = rail(shown, &item.id, t);
    let decision = decision(state, review, shown, item, sheet, t);
    let seed = reader(state, review, item, sheet, t);
    if full {
        panel.child(
            row()
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .child(
                    super::view::scroll("garden.review.rail.viewport", rail)
                        .w(240.0)
                        .grow(0.0)
                        .h_full(),
                )
                .child(super::view::scroll("garden.review.viewport", seed).h_full())
                .child(
                    super::view::scroll("garden.review.decision.viewport", decision)
                        .w(340.0)
                        .grow(0.0)
                        .h_full(),
                ),
        )
    } else {
        panel.child(super::view::scroll(
            "garden.review.viewport",
            col().w_full().child(rail).child(decision).child(seed),
        ))
    }
}

fn header(shown: &GardenReview, complete: bool, full: bool, t: &Tokens) -> El {
    let progress = if complete {
        "complete".to_owned()
    } else {
        format!(
            "{} left · {}/{} advised",
            unresolved(shown).count(),
            advised(shown),
            shown.items.len()
        )
    };
    let (frame_label, frame_glyph) = if full {
        ("Return the garden to the dock", attn_ui::icon::COMPRESS)
    } else {
        ("Expand the garden", attn_ui::icon::EXPAND)
    };
    row()
        .key("garden.review.chrome")
        .w_full()
        .items_center()
        .gap(space::S2)
        .padding(space::S2)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .child(button("garden.review.back".into(), "‹ Garden", t).on_press_tx(exit))
        .child(
            row()
                .grow(1.0)
                .gap(space::S2)
                .items_center()
                .child(
                    text("Review garden")
                        .role("heading")
                        .label("Review garden")
                        .font(rune::TextStyle {
                            weight: 600.0,
                            ..type_::LABEL
                        })
                        .color(t.text_primary),
                )
                .child(
                    text(progress.clone())
                        .key("garden.review.progress")
                        .label(progress)
                        .font(type_::LABEL)
                        .color(t.text_secondary),
                ),
        )
        .child(
            GlyphButton::new(frame_glyph, frame_label)
                .on_press_tx(super::toggle_frame)
                .build(attn_ui::kit(t))
                .key("garden.review.frame-button"),
        )
        .child(
            GlyphButton::new(attn_ui::icon::CLOSE, "Close")
                .on_press(super::close)
                .build(attn_ui::kit(t))
                .key("garden.review.close"),
        )
}

fn finished(review: &Review, t: &Tokens) -> El {
    let mut done = col()
        .key("garden.review.complete")
        .w_full()
        .items_center()
        .gap(space::S2)
        .padding(space::S3)
        .child(
            text("Garden review complete")
                .role("heading")
                .label("Garden review complete")
                .font(type_::TITLE)
                .color(t.text_primary),
        )
        .child(note(
            "garden.review.complete.note",
            "Every captured seed has been dealt with. New candidates will appear in the next review.",
            t,
        ));
    if let Some(failure) = &review.failure {
        done = done.child(alert("garden.review.error", failure, t));
    }
    done.child(
        button(
            "garden.review.complete.back".into(),
            "Back to the garden",
            t,
        )
        .on_press_tx(exit),
    )
}

fn rail(shown: &GardenReview, selected: &str, t: &Tokens) -> El {
    let mut rail = col()
        .key("garden.review.rail")
        .role("navigation")
        .label("Seeds in this review")
        .w_full()
        .gap(2.0)
        .padding(space::S2);
    for (n, item) in shown.items.iter().enumerate() {
        let id = item.id.clone();
        let current = item.id == selected;
        let resolved = item.resolution != "unresolved";
        let line = kit_button(item.title.clone())
            .variant(if current {
                ButtonVariant::Neutral
            } else {
                ButtonVariant::GhostMuted
            })
            .on_press_tx(move |state: &mut State, tx: &mut Tx| select(state, &id, tx))
            .build(attn_ui::kit(t))
            .key(format!("garden.review.rail.{}", item.id))
            .a11y_value(if current { "current" } else { "" });
        rail = rail.child(
            row()
                .w_full()
                .items_center()
                .gap(space::S1)
                .child(
                    text(format!("{}", n + 1))
                        .font(type_::MONO_CAPTION)
                        .color(t.text_tertiary)
                        .a11y_hidden(),
                )
                .child(line)
                .child(meta_word(status_words(item), t).color(if resolved {
                    t.text_tertiary
                } else {
                    t.text_secondary
                })),
        );
    }
    rail
}

fn reader(
    state: &State,
    review: &Review,
    item: &GardenReviewItem,
    sheet: &Sheet,
    t: &Tokens,
) -> El {
    let browsed = review.reading.seed.as_str();
    let at_review_seed = browsed == item.seed_id;
    let (document, error) = super::reading(&state.window.garden, browsed);
    let held = sheet.seeds.iter().find(|s| s.id == browsed);
    let mut page = col().w_full().padding(space::S3).gap(space::S2);
    let index = Index::of(sheet.seeds);
    let path: Vec<_> = index
        .path_to(browsed)
        .into_iter()
        .filter_map(|id| index.by_id.get(&id).cloned())
        .collect();
    let mut nav = row()
        .key("garden.review.path")
        .role("navigation")
        .label("Plot path")
        .w_full()
        .flex_wrap()
        .items_center()
        .gap(space::S1);
    if !at_review_seed {
        nav = nav.child(
            button("garden.review.return".into(), "‹ Review seed", t).on_press_tx({
                let seed = item.seed_id.clone();
                move |state: &mut State, tx: &mut Tx| browse(state, &seed, tx)
            }),
        );
    }
    if path.len() > 1 {
        for seed in &path[..path.len() - 1] {
            let id = seed.id.clone();
            nav = nav
                .child(
                    button(
                        format!("garden.review.path.{}", seed.id),
                        seed.title.clone(),
                        t,
                    )
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| browse(state, &id, tx)),
                )
                .child(meta_word("›", t).a11y_hidden());
        }
    }
    page = page.child(nav);
    let here = document.map(|d| &d.seed).or(held.map(|s| s.as_ref()));
    let Some(here) = here else {
        let words = match error {
            Some(error) => error.to_owned(),
            None => "Reading the seed and its log…".to_owned(),
        };
        return page.child(note("garden.review.loading", words, t));
    };
    let status = if at_review_seed {
        status_words(item).to_owned()
    } else {
        here.status.clone()
    };
    let children = document
        .map(|d| {
            d.children
                .iter()
                .map(|child| model::Relation {
                    label: "",
                    seed: Arc::new(child.clone()),
                })
                .collect()
        })
        .unwrap_or_default();
    if document.is_none() {
        page = page.child(note(
            "garden.review.loading",
            "Reading the seed and its log…",
            t,
        ));
    }
    if let Some(error) = error {
        page = page.child(alert("garden.review.read-error", error, t));
    }
    page.child(sheet.head(here, meta_word(status, t), Vec::new()))
        .child(sheet.authorization(here))
        .child(sheet.plan(here))
        .children(sheet.checks(here, document))
        .children(sheet.seeds("plot", "Plot", children))
        .children(sheet.artifacts(document))
        .child(sheet.log(document, error))
}

fn decision(
    state: &State,
    review: &Review,
    shown: &GardenReview,
    item: &GardenReviewItem,
    sheet: &Sheet,
    t: &Tokens,
) -> El {
    let at_review_seed = review.reading.seed == item.seed_id;
    let mut aside = col()
        .key("garden.review.decision")
        .w_full()
        .gap(space::S2)
        .padding(space::S3);
    if !at_review_seed {
        let seed = item.seed_id.clone();
        return aside.child(
            section(
                "garden.review.context",
                "You are looking around the plot",
                t,
            )
            .child(note(
                "garden.review.context.note",
                format!("The review decision still belongs to {}.", item.title),
                t,
            ))
            .child(
                button(
                    "garden.review.context.return".into(),
                    "Return to review seed",
                    t,
                )
                .on_press_tx(move |state: &mut State, tx: &mut Tx| browse(state, &seed, tx)),
            ),
        );
    }
    aside = aside.child(status_panel(state, review, shown, item, t));
    let evidence: Vec<_> = item
        .evidence
        .iter()
        .filter(|e| !e.label.starts_with("Related seed ") && !e.label.starts_with("Seed log "))
        .collect();
    let mut known = section("garden.review.evidence", "What we know", t);
    for entry in evidence {
        known = known.child(
            col()
                .role("group")
                .label(entry.label.clone())
                .gap(2.0)
                .child(meta_word(entry.label.clone(), t))
                .child(
                    text(entry.text.clone())
                        .label(entry.text.clone())
                        .font(type_::BODY)
                        .color(t.text_primary)
                        .wrap(),
                ),
        );
    }
    aside = aside.child(known);
    if let Some(failure) = &review.failure {
        aside = aside.child(alert("garden.review.error", failure, t));
    }
    let actionable = item.resolution == "unresolved" && item.status != "invalidated";
    if actionable {
        aside = aside.child(actions(state, item, sheet, t));
    }
    aside
}

fn status_panel(
    state: &State,
    review: &Review,
    shown: &GardenReview,
    item: &GardenReviewItem,
    t: &Tokens,
) -> El {
    if item.status == "ready" && item.resolution == "unresolved" {
        let Some(recommendation) = &item.recommendation else {
            return col();
        };
        let mut advice = col()
            .key("garden.review.advice")
            .role("region")
            .label("Advisor suggests")
            .w_full()
            .gap(space::S1)
            .padding(space::S2)
            .radius(6.0)
            .fill(t.bg_elevated)
            .child(meta_word("Advisor suggests", t))
            .child(
                text(action_label(recommendation).to_owned())
                    .key("garden.review.advice.action")
                    .label(action_label(recommendation).to_owned())
                    .font(rune::TextStyle {
                        weight: 600.0,
                        ..type_::BODY
                    })
                    .color(t.text_primary),
            );
        if let Some(explanation) = item.explanation.as_ref().filter(|e| !e.is_empty()) {
            advice = advice.child(
                text(explanation.clone())
                    .label(explanation.clone())
                    .font(type_::BODY)
                    .color(t.text_secondary)
                    .wrap(),
            );
        }
        for cited in item.cited_evidence.iter().flatten() {
            advice = advice.child(
                text(format!("• {cited}"))
                    .label(cited.clone())
                    .font(type_::LABEL)
                    .color(t.text_secondary)
                    .wrap(),
            );
        }
        return advice;
    }
    if item.status == "failed" || item.status == "invalidated" {
        let failed = item.status == "failed";
        let title = if failed {
            "The advisor could not review this seed"
        } else {
            "This seed changed"
        };
        let words = if failed {
            item.advisor_error
                .clone()
                .filter(|e| !e.is_empty())
                .unwrap_or_else(|| advisor_failure_words(item.error.as_deref()))
        } else {
            item.error
                .clone()
                .filter(|e| !e.is_empty())
                .unwrap_or_else(|| "Refresh the evidence and try again.".into())
        };
        let mut problem = section("garden.review.problem", title, t).child(note(
            "garden.review.problem.note",
            words,
            t,
        ));
        problem = if review.retrying {
            problem.child(
                text("Refreshing…")
                    .role("status")
                    .label("Refreshing…")
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            )
        } else {
            let item = item.clone();
            problem.child(
                button("garden.review.retry".into(), "Try again", t)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| retry(state, &item, tx)),
            )
        };
        return problem;
    }
    progress(state, shown, item, t)
}

fn progress(state: &State, shown: &GardenReview, item: &GardenReviewItem, t: &Tokens) -> El {
    let now = state.window.now_ms;
    let advised = advised(shown);
    let total = shown.items.len();
    let attempt = item.advisor_attempt.unwrap_or(0);
    let max = item.advisor_max_attempts.unwrap_or(3);
    let detail = match (item.advisor_state.as_deref(), item.status.as_str()) {
        (Some("retrying"), _) => format!(
            "Attempt {attempt} of {max} did not produce usable advice. Retrying {}.",
            until_words(item.advisor_retry_at.as_deref(), now)
        ),
        (Some("failed"), _) => format!("The advisor stopped after {attempt} of {max} attempts."),
        (_, "running") => format!(
            "Attempt {} of {max} started {}.",
            attempt.max(1),
            age_words(
                item.started_at
                    .as_deref()
                    .or(item.advisor_updated_at.as_deref()),
                now
            )
        ),
        _ if attempt > 0 => format!("Preparing attempt {} of {max}.", (attempt + 1).min(max)),
        _ => "Waiting for an advisor slot.".to_owned(),
    };
    let ready = format!("Advice ready for {advised} of {total} seeds.");
    let mut panel = section("garden.review.waiting", "Advisor progress", t)
        .child(note("garden.review.waiting.ready", ready, t))
        .child(meta_word(
            format!("{} · {}", shown.run.recipe.agent, shown.run.recipe.model),
            t,
        ))
        .child(note("garden.review.waiting.detail", detail, t));
    if let Some(error) = item.advisor_error.as_ref().filter(|e| !e.is_empty()) {
        panel = panel.child(note("garden.review.waiting.error", error.clone(), t));
    }
    panel.child(note(
        "garden.review.waiting.now",
        "You can choose an action now. Advice will appear here if it arrives first.",
        t,
    ))
}

fn actions(state: &State, item: &GardenReviewItem, sheet: &Sheet, t: &Tokens) -> El {
    let mut panel = section("garden.review.actions", "What should happen?", t);
    let document = super::reading(&state.window.garden, &item.seed_id).0;
    if let Some((spot, kind)) = composing(state) {
        return panel.child(match kind {
            Kind::Park | Kind::Wither => lifecycle_form(state, &spot, kind, t),
            Kind::Handover => col()
                .w_full()
                .gap(space::S1)
                .child(draft_line(state, &spot, t))
                .children(sheet.handover_form(document)),
            Kind::Chief => col().w_full().children(sheet.chief_form(document)),
        });
    }
    let mut buttons = row()
        .key("garden.review.choices")
        .role("group")
        .label("Choose what happens to this seed")
        .w_full()
        .flex_wrap()
        .gap(space::S1);
    for action in &item.actions {
        if action == "send_to_chief" && !sheet.chief {
            continue;
        }
        let label = match (action.as_str(), item.outpost.as_deref()) {
            ("resume", Some(outpost)) => format!("Resume on {outpost}"),
            _ => action_label(action).to_owned(),
        };
        let key = match Kind::of(action) {
            Some(kind) => spot(&item.seed_id, kind).key("open"),
            None => format!("garden.review.action.{action}"),
        };
        let recommended = item.recommendation.as_deref() == Some(action.as_str());
        let (chosen, action) = (item.clone(), action.clone());
        buttons = buttons.child(
            kit_button(label)
                .variant(if recommended {
                    ButtonVariant::Submit
                } else {
                    ButtonVariant::Neutral
                })
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    choose_action(state, &chosen, &action, tx)
                })
                .build(attn_ui::kit(t))
                .key(key)
                .a11y_value(if recommended { "recommended" } else { "" }),
        );
    }
    panel = panel.child(buttons);
    panel
}

fn draft_line(state: &State, spot: &Spot, t: &Tokens) -> El {
    let review = review(state);
    let mut line = row().w_full().items_center().gap(space::S1);
    let shown = state.window.garden.shown(spot);
    if review.drafting.is_some() {
        line = line.child(
            text("Drafting…")
                .role("status")
                .label("Drafting…")
                .font(type_::LABEL)
                .color(t.text_secondary),
        );
    } else if !shown.busy {
        let drafted = spot.clone();
        line = line.child(
            button(spot.key("draft"), "Draft", t)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| draft(state, &drafted, tx)),
        );
    }
    let words = shown.words.map(|w| w.text.trim()).unwrap_or_default();
    if let Some((_, at)) = &review.drafted
        && at == words
    {
        let used = spot.clone();
        line = line
            .child(
                text("Draft ready. Your edits were kept.")
                    .role("status")
                    .label("Draft ready. Your edits were kept.")
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            )
            .child(
                button(spot.key("use-draft"), "Use draft", t)
                    .on_press(move |state: &mut State| use_draft(state, &used)),
            );
    }
    line
}

fn lifecycle_form(state: &State, spot: &Spot, kind: Kind, t: &Tokens) -> El {
    let shown = state.window.garden.shown(spot);
    let Some(written) = shown.words else {
        return col();
    };
    let kit = attn_ui::kit(t);
    let (typed, edited) = (spot.clone(), spot.clone());
    let mut form = col()
        .key(spot.key("form"))
        .role("form")
        .label(action_label(kind.part()).to_owned())
        .w_full()
        .gap(space::S1)
        .padding(space::S2)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .child(
            text(kind.prompt())
                .font(type_::LABEL)
                .color(t.text_secondary)
                .a11y_hidden(),
        )
        .child(
            text_input(written, type_::BODY)
                .key(spot.field())
                .return_breaks_line()
                .label(kind.prompt())
                .on_edit(move |state: &mut State| edited.words_mut(state))
                .on_input_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                    let home = typed.key("open");
                    typed.field_key(
                        state,
                        ev,
                        (&home, Sends::CmdReturn),
                        |state, words, tx| lifecycle(&typed, kind, state, words, tx),
                        tx,
                    )
                })
                .w_full()
                .min_h(56.0)
                .color(t.text_primary)
                .fill(t.bg_input)
                .border(1.0, t.border_focus)
                .radius(4.0)
                .padding(space::S1),
        );
    if let Some(failure) = shown.failure {
        form = form.child(alert(&spot.key("error"), failure, t));
    }
    let label = action_label(kind.part());
    let line = if shown.busy {
        let words = format!("{label}ing…");
        row().child(
            text(words.clone())
                .role("status")
                .label(words)
                .font(type_::LABEL)
                .color(t.text_secondary),
        )
    } else {
        let (cancelled, sent) = (spot.clone(), spot.clone());
        row()
            .gap(space::S1)
            .child(
                kit_button("Cancel")
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        cancelled.put_away(state, &cancelled.key("open"), tx)
                    })
                    .build(kit)
                    .key(spot.key("cancel")),
            )
            .child(
                kit_button(label)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        let words = sent.words(state);
                        lifecycle(&sent, kind, state, words, tx)
                    })
                    .build(kit)
                    .key(spot.key("send")),
            )
    };
    form.child(line)
}
