use std::collections::HashSet;
use std::sync::Arc;

use attn_client::Facts;
use attn_protocol::{Seed, SeedDocument, SeedNote, Session};
use attn_ui::{GlyphButton, Tokens};
use rune::tok::{space, type_};
use rune::{Color, El, Key, KeyEvent, TextStyle, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, Placement};

use super::fit::{Level, Widths, text_w};
use super::header_pop::{self, Pop, What};
use crate::State;

/// What an agent's header says about its work: the seed it tends, the plot its seeds share,
/// how many it tends, or the seed it reports to.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
enum Kind {
    Crown,
    Seed,
    Plot,
    Multi,
}

/// Why a seed is in an agent's list: the plot its tended seeds share, one it tends, or the
/// one it reports to.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
enum Role {
    Plot,
    Tended,
    Crown,
}

#[derive(Clone, Debug, PartialEq, Hash)]
struct Entry {
    id: String,
    role: Role,
    title: String,
    status: String,
    fraction: Option<String>,
}

impl Entry {
    fn of(id: &str, seed: Option<&Seed>, role: Role) -> Entry {
        Entry {
            id: id.to_owned(),
            role,
            title: seed
                .map(|s| s.title.trim())
                .filter(|t| !t.is_empty())
                .unwrap_or(id)
                .to_owned(),
            status: seed.map_or("unknown", |s| s.status.as_str()).to_owned(),
            fraction: seed.and_then(fraction_of),
        }
    }
}

/// An agent's seed chip: what it names, where a press takes it, and the seeds its popover
/// lists.
#[derive(Clone, Debug, PartialEq, Hash)]
pub(crate) struct Chip {
    kind: Kind,
    label: String,
    status: String,
    /// The seed a press opens; an aggregate chip opens its list instead.
    opens: Option<String>,
    fraction: Option<String>,
    counts: Vec<(&'static str, i64)>,
    repeats_title: bool,
    entries: Vec<Entry>,
    crown: Option<String>,
}

const MARK_GAP: f32 = 4.0;
const PAD: f32 = 4.0;
const SHORT_LABEL: f32 = 84.0;
const MAX_ANCESTRY: usize = 32;

fn ancestry(seed: &Arc<Seed>, seeds: &[Arc<Seed>]) -> Vec<String> {
    let mut chain = Vec::new();
    let mut seen = HashSet::new();
    let mut current = Some(seed);
    while let Some(seed) = current.filter(|s| chain.len() < MAX_ANCESTRY && seen.insert(&s.id)) {
        chain.push(seed.id.clone());
        current = seed
            .edges
            .iter()
            .find(|e| e.kind == "part-of")
            .and_then(|e| seeds.iter().find(|s| s.id == e.to));
    }
    chain
}

fn common_plot(tended: &[Arc<Seed>], seeds: &[Arc<Seed>]) -> Option<Arc<Seed>> {
    let chains: Vec<Vec<String>> = tended.iter().map(|s| ancestry(s, seeds)).collect();
    let (first, rest) = chains.split_first()?;
    first
        .iter()
        .find(|id| rest.iter().all(|c| c.contains(id)))
        .and_then(|id| seeds.iter().find(|s| s.id == *id))
        .cloned()
}

pub(crate) fn state_label(status: &str) -> &'static str {
    match status {
        "planted" => "Planted",
        "growing" => "Growing",
        "parked" => "Parked",
        "harvested" => "Harvested",
        "withered" => "Withered",
        _ => "Unknown",
    }
}

fn state_meaning(status: &str) -> &'static str {
    match status {
        "planted" => "Open, not currently being tended.",
        "growing" => "Claimed and being worked on.",
        "parked" => "Paused. Tending it again resumes the work.",
        "harvested" => "The work is complete.",
        "withered" => "Closed without a harvest.",
        _ => "Seed details are not available yet.",
    }
}

fn state_color(status: &str, t: &Tokens) -> Color {
    match status {
        "growing" => t.accent,
        "parked" => t.warning,
        "harvested" => t.success,
        "planted" => t.text_tertiary,
        "plot" => t.text_secondary,
        _ => t.text_muted,
    }
}

fn plot_counts(seed: &Seed) -> Vec<(&'static str, i64)> {
    let Some(p) = &seed.plot_progress else {
        return Vec::new();
    };
    let planted = (p.total - p.growing - p.parked - p.done - p.withered).max(0);
    [
        ("growing", p.growing),
        ("parked", p.parked),
        ("harvested", p.done),
        ("withered", p.withered),
        ("planted", planted),
    ]
    .into_iter()
    .filter(|(_, n)| *n > 0)
    .collect()
}

fn fraction_of(seed: &Seed) -> Option<String> {
    seed.plot_progress
        .as_ref()
        .filter(|p| p.total > 0)
        .map(|p| format!("{}/{}", p.done, p.total))
}

fn normalized(title: &str) -> String {
    let kept: String = title
        .to_lowercase()
        .chars()
        .filter(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || *c == ' ')
        .collect();
    kept.split_whitespace().collect::<Vec<_>>().join(" ")
}

/// Whether one title starts the other, so a chip beside it would only say it again.
pub(crate) fn titles_repeat(a: &str, b: &str) -> bool {
    let (x, y) = (normalized(a), normalized(b));
    !x.is_empty() && !y.is_empty() && (x.starts_with(&y) || y.starts_with(&x))
}

/// The chip of `session`, whose header reads `title`, from the garden's `seeds`.
pub(crate) fn of(session: &Session, seeds: &[Arc<Seed>], title: &str) -> Option<Chip> {
    let tended: Vec<Arc<Seed>> = seeds
        .iter()
        .filter(|s| {
            s.tender.as_ref().and_then(|t| t.session_id.as_deref()) == Some(session.id.as_str())
        })
        .cloned()
        .collect();
    let crown = session.seed_id.clone().filter(|id| !id.is_empty());
    let crown_entry = |id: &str| {
        Entry::of(
            id,
            seeds.iter().find(|s| s.id == id).map(|s| &**s),
            Role::Crown,
        )
    };
    let entry = |seed: &Arc<Seed>, role| Entry::of(&seed.id, Some(seed), role);
    let mut plot_seed = None;
    let (kind, mut entries) = match tended.as_slice() {
        [] => (Kind::Crown, vec![crown_entry(crown.as_deref()?)]),
        [one] => (Kind::Seed, vec![entry(one, Role::Tended)]),
        many => match common_plot(many, seeds) {
            Some(plot) => {
                plot_seed = Some(plot.clone());
                let mut entries = vec![entry(&plot, Role::Plot)];
                entries.extend(
                    many.iter()
                        .filter(|s| s.id != plot.id)
                        .map(|s| entry(s, Role::Tended)),
                );
                (Kind::Plot, entries)
            }
            None => (
                Kind::Multi,
                many.iter().map(|s| entry(s, Role::Tended)).collect(),
            ),
        },
    };
    if let Some(id) = &crown
        && kind != Kind::Crown
        && !entries.iter().any(|e| e.id == *id)
    {
        entries.push(crown_entry(id));
    }
    let first = &entries[0];
    let (label, status, opens, fraction, counts) = match kind {
        Kind::Crown | Kind::Seed => (
            first.title.clone(),
            first.status.clone(),
            Some(first.id.clone()),
            None,
            Vec::new(),
        ),
        Kind::Plot => (
            first.title.clone(),
            "plot".to_owned(),
            None,
            first.fraction.clone(),
            plot_seed.as_deref().map(plot_counts).unwrap_or_default(),
        ),
        Kind::Multi => (
            format!("tending {}", tended.len()),
            "multi".to_owned(),
            None,
            None,
            Vec::new(),
        ),
    };
    Some(Chip {
        kind,
        repeats_title: titles_repeat(title, &label),
        label,
        status,
        opens,
        fraction,
        counts,
        entries,
        crown,
    })
}

/// The chip of `session` from what the window knows.
fn of_facts(session: &str, facts: &Facts) -> Option<Chip> {
    let session = facts.sessions.iter().find(|s| s.id == session)?;
    let title = crate::sidebar::display_name(session);
    of(session, facts.garden.of(&facts.profiles.selected), &title)
}

impl Chip {
    fn aggregate(&self) -> bool {
        matches!(self.kind, Kind::Plot | Kind::Multi)
    }

    /// Whether the popover tells one seed's story instead of listing seeds.
    fn single(&self) -> bool {
        match self.entries.as_slice() {
            [_] => true,
            [first, second] => first.role == Role::Tended && second.role == Role::Crown,
            _ => false,
        }
    }

    fn name(&self) -> String {
        let state = match (self.kind, &self.fraction) {
            (Kind::Plot, Some(fraction)) => format!("{fraction} harvested"),
            (Kind::Plot | Kind::Multi, _) => "Growing".to_owned(),
            _ => state_label(&self.status).to_owned(),
        };
        let does = if self.opens.is_some() {
            "Open seed"
        } else {
            "Show seeds"
        };
        format!("{}, {state}. {does}", self.label)
    }
}

/// Whether `session` has a seed for its header to name.
pub(crate) fn shown(session: &Session, facts: &Facts) -> bool {
    of(session, facts.garden.of(&facts.profiles.selected), "").is_some()
}

/// The row a pinned list picks first: the first seed the agent tends.
pub(crate) fn first_pick(session: &str, facts: &Facts) -> usize {
    of_facts(session, facts)
        .and_then(|c| c.entries.iter().position(|e| e.role != Role::Crown))
        .unwrap_or(0)
}

/// The seed whose story the open popover tells, which the garden reads while it is open.
pub(crate) fn told(state: &State) -> Option<String> {
    let pop = state
        .window
        .desk
        .header_pop
        .open
        .as_ref()
        .filter(|p| p.what == What::Seed)?;
    let chip = of_facts(&pop.session, &state.facts)?;
    chip.single().then(|| chip.entries[0].id.clone())
}

fn mark(status: &str, font: TextStyle, t: &Tokens) -> El {
    text(attn_ui::icon::SEEDLING)
        .font(font)
        .color(state_color(status, t))
}

/// The chip in the header of `leaf`: a press opens its seed beside the agent, or pins the list
/// of an aggregate chip; resting on it previews the popover.
pub(super) fn piece(
    leaf: &str,
    session: &str,
    chip: &Chip,
    level: Level,
    font: TextStyle,
    t: &Tokens,
) -> El {
    let anchor = header_pop::anchor(leaf, What::Seed);
    let full = level != Level::Icon;
    let (status, label, fraction) = (
        chip.status.clone(),
        chip.label.clone(),
        chip.fraction.clone(),
    );
    let counts = chip.counts.clone();
    let state = (!chip.aggregate()).then(|| state_label(&chip.status));
    let shows_label = full && !chip.repeats_title;
    let short = level == Level::Short;
    let mut face = row()
        .items_center()
        .gap(MARK_GAP)
        .child(mark(&status, font, t));
    if shows_label {
        let words = text(label)
            .key(format!("{anchor}.label"))
            .font(font)
            .color(t.text_secondary)
            .single_line()
            .truncate();
        face = face.child(if short {
            words.max_w(SHORT_LABEL)
        } else {
            words
        });
    }
    if full {
        face = face
            .children(counts.iter().map(|(state, n)| {
                row()
                    .items_center()
                    .gap(1.0)
                    .label(format!("{n} {state}"))
                    .child(mark(state, font, t))
                    .child(text(n.to_string()).font(font).color(t.text_tertiary))
            }))
            .children(fraction.map(|f| text(f).font(font).color(t.text_tertiary)))
            .children(state.map(|s| text(s).font(font).color(state_color(&status, t))));
    }
    let pressed = (leaf.to_owned(), session.to_owned(), chip.opens.clone());
    let button = Button::new(chip.name())
        .variant(ButtonVariant::GhostMuted)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            press(state, &pressed.0, &pressed.1, pressed.2.as_deref(), tx)
        })
        .label_with(move |_, _| face)
        .build(attn_ui::kit(t))
        .key(anchor)
        .h(font.line_px())
        .px(PAD);
    header_pop::hovers(button, leaf, session, What::Seed)
}

/// The chip's width in full, with its label cut short, and as its mark alone.
pub(super) fn widths(chip: &Chip, font: TextStyle) -> Widths {
    let mark = text_w(attn_ui::icon::SEEDLING, font);
    let icon = 2.0 * PAD + mark;
    let label = if chip.repeats_title {
        0.0
    } else {
        text_w(&chip.label, font)
    };
    let mut rest: Vec<f32> = chip
        .counts
        .iter()
        .map(|(_, n)| mark + 1.0 + text_w(&n.to_string(), font))
        .collect();
    if let Some(fraction) = &chip.fraction {
        rest.push(text_w(fraction, font));
    }
    if !chip.aggregate() {
        rest.push(text_w(state_label(&chip.status), font));
    }
    let rest: f32 = rest.iter().map(|w| w + MARK_GAP).sum();
    let with = |label: f32| icon + if label > 0.0 { MARK_GAP + label } else { 0.0 } + rest;
    Widths {
        full: with(label),
        short: Some(with(label.min(SHORT_LABEL))),
        icon: Some(icon),
    }
}

fn press(state: &mut State, leaf: &str, session: &str, opens: Option<&str>, tx: &mut Tx) {
    let pinned_here = state
        .window
        .desk
        .header_pop
        .open
        .as_ref()
        .is_some_and(|p| p.pinned && p.leaf == leaf && p.what == What::Seed);
    match opens {
        Some(seed) if !pinned_here => open(state, seed, session, tx),
        _ => header_pop::press(state, leaf, session, What::Seed, tx),
    }
}

fn open(state: &mut State, seed: &str, session: &str, tx: &mut Tx) {
    header_pop::close(state);
    crate::garden::open_tile(state, seed, Some(session), tx);
}

/// Plain words from markdown `body`, cut at a word near `max` characters.
fn excerpt(body: &str, max: usize) -> String {
    let mut prose = String::new();
    let mut fenced = false;
    for line in body.lines() {
        if line.trim_start().starts_with("```") {
            fenced = !fenced;
            continue;
        }
        if fenced {
            continue;
        }
        let line = line.trim_start();
        let line = line.trim_start_matches('#');
        let line = ["- ", "* ", "+ "]
            .iter()
            .find_map(|m| line.strip_prefix(m))
            .unwrap_or(line);
        prose.push_str(line);
        prose.push(' ');
    }
    let mut plain = String::new();
    let mut rest = prose.as_str();
    while let Some(open) = rest.find('[') {
        let end = rest[open..].find("](").map(|i| open + i);
        let close = end.and_then(|e| rest[e..].find(')').map(|i| e + i));
        match (end, close) {
            (Some(end), Some(close)) => {
                let before = &rest[..open];
                match before.strip_suffix('!') {
                    Some(before) => plain.push_str(before),
                    None => {
                        plain.push_str(before);
                        plain.push_str(&rest[open + 1..end]);
                    }
                }
                rest = &rest[close + 1..];
            }
            _ => {
                plain.push_str(&rest[..=open]);
                rest = &rest[open + 1..];
            }
        }
    }
    plain.push_str(rest);
    let plain: String = plain
        .chars()
        .map(|c| if "*_~`>|".contains(c) { ' ' } else { c })
        .collect();
    let plain = plain.split_whitespace().collect::<Vec<_>>().join(" ");
    if plain.chars().count() <= max {
        return plain;
    }
    let clipped: String = plain.chars().take(max + 1).collect();
    let cut = clipped
        .rfind(' ')
        .filter(|&i| i * 100 > max * 65)
        .unwrap_or_else(|| {
            clipped
                .char_indices()
                .nth(max)
                .map_or(clipped.len(), |(i, _)| i)
        });
    format!("{}…", clipped[..cut].trim_end())
}

fn latest_note(document: Option<&SeedDocument>) -> Option<&SeedNote> {
    document?
        .notes
        .iter()
        .filter(|n| !matches!(n.kind.as_str(), "attach" | "detach" | "move" | "link"))
        .filter(|n| !n.body.trim().is_empty())
        .max_by_key(|n| crate::sidebar::queue::epoch_ms(&n.created_at).unwrap_or(i64::MIN))
}

fn caption(words: &str, t: &Tokens) -> El {
    text(words.to_owned())
        .font(type_::LABEL)
        .color(t.text_tertiary)
}

fn prose(words: String, t: &Tokens) -> El {
    text(words).font(type_::BODY).color(t.text_primary).wrap()
}

/// The open seed popover, under its chip.
pub(crate) fn popover(state: &State, pop: &Pop, t: &Tokens) -> Option<El> {
    let chip = of_facts(&pop.session, &state.facts)?;
    let close = GlyphButton::new("×", "Close seeds")
        .variant(ButtonVariant::GhostFaint)
        .font(type_::LABEL)
        .side(type_::LABEL.line_px())
        .on_press(|state: &mut State| header_pop::close(state))
        .build(attn_ui::kit(t))
        .key(format!("{}.close", pop.surface()));
    let single = chip.single();
    let body = if single {
        context(state, pop, &chip, t)
    } else {
        list(state, pop, &chip, t)
    };
    let surface = col()
        .key(pop.surface())
        .role(if single { "dialog" } else { "listbox" })
        .label(if single {
            "Seed context"
        } else {
            "Seeds this agent is tending"
        })
        .w(320.0)
        .padding(space::S2)
        .gap(space::S2)
        .min_h(0.0)
        .focusable(true)
        .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
            header_pop::hover_popover(state, inside, tx)
        })
        .child(row().w_full().child(rune::el().grow(1.0)).child(close))
        .child(body)
        .children((pop.pinned && !single).then(|| {
            text("↑ ↓ move · ↵ open · esc close")
                .font(type_::LABEL)
                .color(t.text_muted)
        }));
    let surface = if pop.pinned {
        let ids: Vec<String> = chip.entries.iter().map(|e| e.id.clone()).collect();
        let session = pop.session.clone();
        surface
            .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                list_key(state, &session, &ids, single, ev, tx)
            })
            .modal_tx(true, |state: &mut State, _: &mut Tx| {
                header_pop::close(state)
            })
            .modal_press_through(true)
    } else {
        surface
    };
    Some(rune_kit::popover::anchored(
        attn_ui::kit(t),
        rune_kit::popover::raised(attn_ui::kit(t), surface, 6.0),
        &pop.anchor(),
        Placement::BottomStart,
        4.0,
    ))
}

fn context(state: &State, pop: &Pop, chip: &Chip, t: &Tokens) -> El {
    let primary = &chip.entries[0];
    let (document, error) = crate::garden::reading(&state.window.garden, &primary.id);
    let seed = document
        .map(|d| &d.seed)
        .or_else(|| garden_seed(state, &primary.id));
    let status = seed.map_or("unknown", |s| s.status.as_str());
    let now = state.window.now_ms;
    let elapsed = seed
        .map(|s| {
            let ago = crate::garden::model::ago(&s.state_changed_at, now);
            match (ago.is_empty(), s.state_changed_at_exact) {
                (true, _) => String::new(),
                (false, true) => ago,
                (false, false) => format!("~{ago}"),
            }
        })
        .unwrap_or_default();
    let standing = row()
        .key(format!("{}.state", pop.surface()))
        .w_full()
        .items_center()
        .gap(space::S2)
        .child(mark(status, type_::TITLE, t))
        .child(
            col()
                .grow(1.0)
                .child(
                    text(state_label(status))
                        .font(type_::LABEL)
                        .color(t.text_primary),
                )
                .child(
                    text(state_meaning(status))
                        .font(type_::LABEL)
                        .color(t.text_tertiary)
                        .wrap(),
                ),
        )
        .children((!elapsed.is_empty()).then(|| caption(&elapsed, t)));
    let latest = latest_note(document);
    let note_words = match (latest, error, document) {
        (Some(note), _, _) => excerpt(&note.body, 360),
        (None, Some(_), _) => "Latest note unavailable.".into(),
        (None, None, None) => "Loading latest note…".into(),
        (None, None, Some(_)) => "No notes yet.".into(),
    };
    let note_head = row()
        .w_full()
        .gap(space::S2)
        .child(caption(
            if latest.is_some_and(|n| n.kind == "handoff") {
                "Latest handoff"
            } else {
                "Latest note"
            },
            t,
        ))
        .children(latest.map(|n| caption(&crate::garden::model::ago(&n.created_at, now), t)));
    let note = col()
        .key(format!("{}.note", pop.surface()))
        .label("Latest note")
        .gap(2.0)
        .child(note_head)
        .child(prose(note_words, t));
    let reason = seed
        .and_then(|s| s.reason.as_deref())
        .map(str::trim)
        .filter(|r| !r.is_empty());
    let summary = seed.map(|s| excerpt(&s.body, 240)).unwrap_or_default();
    let outcome = match reason {
        Some(reason) => Some((
            if status == "harvested" {
                "Outcome"
            } else {
                "Reason"
            },
            excerpt(reason, 360),
        )),
        None if latest.is_none() && !summary.is_empty() => Some(("About this seed", summary)),
        None => None,
    };
    let reports_here = primary.role == Role::Crown || chip.crown.as_deref() == Some(&primary.id);
    let session = pop.session.clone();
    let reports_to = chip.entries.get(1).map(|crown| {
        let (id, session) = (crown.id.clone(), session.clone());
        kit_link(
            format!("Reports to {} ↗", crown.title),
            format!("{}.reports-to", pop.surface()),
            t,
            move |state, tx| open(state, &id, &session, tx),
        )
    });
    let (id, face) = (primary.id.clone(), primary.id.clone());
    let open_seed = Button::new(format!("Open seed {id}"))
        .variant(ButtonVariant::GhostMuted)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open(state, &id, &session, tx))
        .label_with(move |_, ink| {
            row()
                .w_full()
                .gap(space::S2)
                .child(text("Open seed ↗").font(type_::LABEL).color(ink).grow(1.0))
                .child(text(face).font(type_::MONO_CAPTION).color(ink))
        })
        .build(attn_ui::kit(t))
        .key(format!("{}.open", pop.surface()))
        .w_full();
    col()
        .w_full()
        .gap(space::S2)
        .child(standing)
        .child(note)
        .children(outcome.map(|(head, words)| {
            col()
                .key(format!("{}.outcome", pop.surface()))
                .gap(2.0)
                .child(caption(head, t))
                .child(prose(words, t))
        }))
        .children(reports_here.then(|| caption("This agent reports to this seed.", t)))
        .children(reports_to)
        .child(open_seed)
}

fn kit_link(
    label: String,
    key: String,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    Button::new(label)
        .variant(ButtonVariant::GhostMuted)
        .on_press_tx(press)
        .build(attn_ui::kit(t))
        .key(key)
}

fn garden_seed<'a>(state: &'a State, id: &str) -> Option<&'a Seed> {
    crate::garden::seeds(state)
        .iter()
        .find(|s| s.id == id)
        .map(|s| &**s)
}

fn list(state: &State, pop: &Pop, chip: &Chip, t: &Tokens) -> El {
    let picked = state.window.desk.header_pop.picked;
    col()
        .key(format!("{}.seeds", pop.surface()))
        .w_full()
        .gap(2.0)
        .children(chip.entries.iter().enumerate().map(|(i, entry)| {
            let about = garden_seed(state, &entry.id)
                .map(|s| excerpt(s.reason.as_deref().unwrap_or(&s.body), 100))
                .unwrap_or_default();
            option(pop, chip, i, entry, about, pop.pinned && i == picked, t)
        }))
}

fn option(
    pop: &Pop,
    chip: &Chip,
    i: usize,
    entry: &Entry,
    about: String,
    picked: bool,
    t: &Tokens,
) -> El {
    let title = entry.title.clone();
    let fraction = entry.fraction.clone().filter(|_| entry.role == Role::Plot);
    let side = fraction.unwrap_or_else(|| state_label(&entry.status).to_owned());
    let reports = entry.role == Role::Crown || chip.crown.as_deref() == Some(&entry.id);
    let note = format!("{}{about}", if reports { "Reports here. " } else { "" });
    let status = if entry.role == Role::Plot {
        "plot"
    } else {
        entry.status.as_str()
    };
    let (id, session) = (entry.id.clone(), pop.session.clone());
    row()
        .key(format!("{}.seed.{i}", pop.surface()))
        .role("option")
        .label(format!("{title} · {side}"))
        .focus_selected(picked)
        .w_full()
        .items_center()
        .gap(space::S2)
        .padding(4.0)
        .radius(4.0)
        .fill(if picked { t.bg_selected } else { t.bg_elevated })
        .child(mark(status, type_::BODY, t))
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .child(
                    text(title)
                        .font(type_::LABEL)
                        .color(t.text_primary)
                        .truncate(),
                )
                .children((!note.is_empty()).then(|| {
                    text(note)
                        .font(type_::LABEL)
                        .color(t.text_tertiary)
                        .truncate()
                })),
        )
        .child(text(side).font(type_::LABEL).color(t.text_tertiary))
        .on_pointer_move_local(move |state: &mut State, _, tx: &mut Tx| {
            let pop = &mut state.window.desk.header_pop;
            if pop.picked == i {
                tx.live_only();
            } else {
                pop.picked = i;
            }
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open(state, &id, &session, tx))
}

fn list_key(
    state: &mut State,
    session: &str,
    ids: &[String],
    single: bool,
    ev: &KeyEvent,
    tx: &mut Tx,
) -> bool {
    if single {
        if ev.key == Key::Enter
            && let Some(id) = ids.first()
        {
            open(state, id, session, tx);
            return true;
        }
        return false;
    }
    let pop = &mut state.window.desk.header_pop;
    let last = ids.len().saturating_sub(1);
    let ctrl = |c: &str| ev.mods.ctrl && ev.key == Key::Text(c.into());
    if ev.key == Key::Down || ctrl("n") {
        pop.picked = if pop.picked >= last {
            0
        } else {
            pop.picked + 1
        };
    } else if ev.key == Key::Up || ctrl("p") {
        pop.picked = if pop.picked == 0 {
            last
        } else {
            pop.picked - 1
        };
    } else if ev.key == Key::Enter {
        if let Some(id) = ids.get(pop.picked.min(last)).cloned() {
            open(state, &id, session, tx);
        }
    } else {
        return false;
    }
    true
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_title_repeats_another_that_starts_it_ignoring_case_punctuation_and_spacing() {
        for (a, b, repeats) in [
            (
                "Prototype options for the session pane header",
                "Prototype options for the session pane header strip",
                true,
            ),
            (
                "Fix the  Queue-Sidebar fit!",
                "fix the queuesidebar fit",
                true,
            ),
            ("Review the release", "Coordinate the release", false),
            ("", "Anything", false),
        ] {
            assert_eq!(titles_repeat(a, b), repeats, "{a} / {b}");
        }
    }
}
