use attn_protocol::{AutomationProvenance, Session, SessionPullRequest};
use attn_ui::Tokens;
use attn_ui::blocks::{Block, Cells, HeaderPiece, Inline, Popover, Stack};
use attn_ui::system::{
    DotShape, DotSize, IconSize, Link, ListRow, Type, dot, glyph, icon, keys_hint, width_of, words,
};
use rune::{Color, El, Key, KeyEvent, Tx, col};
use rune_kit::Placement;

use super::fit::{Level, Widths};
use super::header_pop::{self, Pop, What};
use crate::State;

/// Where the session came from, as its header says it: the automation that started it and
/// the pull request it works on.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Line {
    automation: Option<Automation>,
    pull_request: Option<PullRequest>,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
struct Automation {
    definition: String,
    superseded: bool,
    target: Option<Target>,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
struct Target {
    name: String,
    url: String,
    title: Option<String>,
}

/// How a pull request's status reads at a glance.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
enum Tone {
    Ok,
    Warn,
    Bad,
    Merged,
    Neutral,
}

impl Tone {
    fn color(self, t: &Tokens) -> rune::Color {
        match self {
            Tone::Ok => t.success,
            Tone::Warn => t.warning,
            Tone::Bad => t.error,
            Tone::Merged => t.merged,
            Tone::Neutral => t.text_tertiary,
        }
    }
}

type Said = (&'static str, Tone);

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
struct PullRequest {
    name: String,
    status: Said,
    title: Option<String>,
}

pub(crate) fn line(session: &Session) -> Option<Line> {
    let automation = session.automation.as_ref().map(automation);
    let pull_request = pull_request(session).map(|pr| PullRequest {
        name: target_name(&pr.repository, pr.number),
        status: status(pr),
        title: pr.title.clone().filter(|t| !t.is_empty()),
    });
    (automation.is_some() || pull_request.is_some()).then_some(Line {
        automation,
        pull_request,
    })
}

fn automation(automation: &AutomationProvenance) -> Automation {
    Automation {
        definition: short_definition(&automation.definition_name),
        superseded: automation.superseded == Some(true),
        target: automation.pull_request.as_ref().map(|pr| Target {
            name: target_name(&pr.repository, pr.number),
            url: pr.url.clone(),
            title: pr.title.clone().filter(|t| !t.is_empty()),
        }),
    }
}

fn short_definition(name: &str) -> String {
    const REVIEW: &str = "requested pr review";
    let short = name
        .get(..REVIEW.len())
        .filter(|head| head.eq_ignore_ascii_case(REVIEW))
        .and_then(|_| {
            name[REVIEW.len()..]
                .trim_start()
                .strip_prefix(['-', '—', ':'])
        })
        .map(str::trim)
        .filter(|short| !short.is_empty());
    short.unwrap_or(name).to_owned()
}

fn target_name(repository: &str, number: i64) -> String {
    let name = repository
        .split('/')
        .rfind(|p| !p.is_empty())
        .unwrap_or(repository);
    format!("{name}#{number}")
}

fn state_rank(pr: &SessionPullRequest) -> u8 {
    match pr.state.as_str() {
        "merged" => 1,
        "closed" => 2,
        _ => 0,
    }
}

/// The session's pull requests, open ones first, then merged, then closed, newest first.
pub(crate) fn pull_requests(session: &Session) -> Vec<&SessionPullRequest> {
    let mut all: Vec<&SessionPullRequest> = session.pull_requests.iter().flatten().collect();
    let created =
        |pr: &SessionPullRequest| crate::sidebar::queue::epoch_ms(&pr.created_at).unwrap_or(0);
    all.sort_by(|a, b| {
        state_rank(a)
            .cmp(&state_rank(b))
            .then_with(|| created(b).cmp(&created(a)))
    });
    all
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Badge {
    number: i64,
    said: String,
    tone: Tone,
}

pub(crate) fn badge(session: &Session) -> Option<Badge> {
    let pr = pull_request(session)?;
    let (label, tone) = status(pr);
    let title = pr.title.as_deref().filter(|t| !t.is_empty());
    let said = [
        Some(format!("{}#{}", pr.repository, pr.number)),
        Some(label.to_owned()),
        title.map(str::to_owned),
    ]
    .into_iter()
    .flatten()
    .collect::<Vec<_>>()
    .join(" · ");
    Some(Badge {
        number: pr.number,
        said,
        tone,
    })
}

pub(crate) fn badge_view(row_key: &str, badge: &Badge, t: &Tokens) -> El {
    let number = format!("#{}", badge.number);
    Inline::joined()
        .key(format!("{row_key}.pr"))
        .role("img")
        .label(badge.said.clone())
        .a11y_value(number.clone())
        .child(dot(badge.tone.color(t), DotShape::Filled, DotSize::Small))
        .child(words(number, Type::Mono, t.text_secondary))
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Run {
    definition: String,
    number: Option<i64>,
    said: String,
}

impl Run {
    fn shown(&self) -> String {
        match self.number {
            Some(n) => format!("{} #{n}", self.definition),
            None => self.definition.clone(),
        }
    }
}

pub(crate) fn run(session: &Session) -> Option<Run> {
    let automation = session.automation.as_ref()?;
    let mut said = vec![format!("Automation: {}", automation.definition_name)];
    if automation.superseded == Some(true) {
        said.push("Superseded session".into());
    }
    if let Some(pr) = &automation.pull_request {
        said.push(format!("{}#{}", pr.repository, pr.number));
        said.extend(pr.title.clone().filter(|t| !t.is_empty()));
    }
    Some(Run {
        definition: short_definition(&automation.definition_name),
        number: automation.pull_request.as_ref().map(|pr| pr.number),
        said: said.join(" · "),
    })
}

pub(crate) fn run_view(row_key: &str, run: &Run, t: &Tokens) -> El {
    Inline::joined()
        .key(format!("{row_key}.run"))
        .role("img")
        .label(run.said.clone())
        .a11y_value(run.shown())
        .child(words(attn_ui::glyph::BOLT, Type::Mono, t.text_tertiary))
        .children(run.number.map(|n| {
            words(format!("#{n}"), Type::Mono, t.text_tertiary)
                .key(format!("{row_key}.run.number"))
                .single_line()
        }))
}

/// The pull request the header names: the best one, unless even that one is closed.
pub(crate) fn pull_request(session: &Session) -> Option<&SessionPullRequest> {
    pull_requests(session)
        .into_iter()
        .next()
        .filter(|pr| state_rank(pr) < 2)
}

fn status(pr: &SessionPullRequest) -> Said {
    use Tone::*;
    let ci = pr.ci_status.as_deref();
    let review = pr.review_status.as_deref();
    let merge = pr.mergeable_state.as_deref();
    match pr.state.as_str() {
        "merged" => return ("merged", Merged),
        "closed" => return ("closed", Neutral),
        _ => {}
    }
    if merge == Some("dirty") {
        ("conflicts", Bad)
    } else if ci == Some("failure") {
        ("checks failed", Bad)
    } else if review == Some("changes_requested") {
        ("changes requested", Warn)
    } else if ci == Some("pending") {
        ("checks running", Warn)
    } else if pr.state == "draft" {
        ("draft", Neutral)
    } else if review == Some("approved") {
        if matches!(merge, Some("clean" | "has_hooks")) {
            ("ready to merge", Ok)
        } else {
            ("approved", Ok)
        }
    } else if review == Some("pending") {
        ("in review", Neutral)
    } else if ci == Some("success") {
        ("checks passed", Ok)
    } else {
        ("open", Neutral)
    }
}

/// One part of the provenance line, and the glyph it keeps when the line shrinks to its icons.
struct Part<'a> {
    text: String,
    icon: Option<&'static str>,
    does: Does<'a>,
}

enum Does<'a> {
    Say,
    Title,
    Status(Tone),
    OpenTarget(&'a str),
    ShowDetails,
}

/// An automation run's provenance as words, as the drawer's run history says it:
/// "⚡ Automation · nightly docs · Superseded session · attn #20".
pub(crate) fn automation_words(provenance: &AutomationProvenance) -> String {
    let line = Line {
        automation: Some(automation(provenance)),
        pull_request: None,
    };
    parts(&line)
        .into_iter()
        .filter(|p| !matches!(p.does, Does::Title))
        .map(|p| p.text.trim_end_matches(" ↗").to_owned())
        .collect::<Vec<_>>()
        .join(" · ")
}

fn parts(line: &Line) -> Vec<Part<'_>> {
    let part = |text: String, icon, does| Part { text, icon, does };
    let mut parts = Vec::new();
    if let Some(automation) = &line.automation {
        let kind_icon = automation.target.is_none().then_some("⚡");
        parts.push(part("⚡ Automation".into(), kind_icon, Does::Say));
        parts.push(part(automation.definition.clone(), None, Does::Say));
        if automation.superseded {
            parts.push(part("Superseded session".into(), None, Does::Say));
        }
        if let Some(target) = &automation.target {
            parts.push(part(
                format!("{} ↗", target.name),
                Some("⚡"),
                Does::OpenTarget(&target.url),
            ));
            parts.extend(target.title.clone().map(|t| part(t, None, Does::Title)));
        }
    }
    if let Some(pr) = &line.pull_request {
        parts.push(part("⎇ PR".into(), None, Does::Say));
        parts.push(part(format!("{} ↗", pr.name), Some("⎇"), Does::ShowDetails));
        parts.push(part(pr.status.0.into(), None, Does::Status(pr.status.1)));
        parts.extend(pr.title.clone().map(|t| part(t, None, Does::Title)));
    }
    parts
}

/// The provenance line in the header of `leaf`, in full or as its icons alone, inked by `t`
/// when it is drawn and by nothing when it is only measured.
fn face(leaf: &str, session: &str, line: &Line, level: Level, t: Option<&Tokens>) -> HeaderPiece {
    let icons = level == Level::Icon;
    let ink = |pick: fn(&Tokens) -> Color| t.map_or(Color::TRANSPARENT, pick);
    let pr_name = line.pull_request.as_ref().map(|pr| pr.name.as_str());
    let target_name = line
        .automation
        .as_ref()
        .and_then(|a| a.target.as_ref())
        .map(|t| t.name.as_str());
    let accent = if icons {
        ink(|t| t.accent)
    } else {
        ink(|t| t.text_secondary)
    };
    parts(line)
        .into_iter()
        .fold(HeaderPiece::new(), |face, part| {
            let Some(shown) = (if icons {
                part.icon.map(str::to_owned)
            } else {
                Some(part.text)
            }) else {
                return face;
            };
            match part.does {
                Does::Say => face.words(shown, ink(|t| t.text_tertiary)),
                Does::Title => face.words(shown, ink(|t| t.text_secondary)),
                Does::Status(tone) => {
                    face.words(shown, t.map_or(Color::TRANSPARENT, |t| tone.color(t)))
                }
                Does::OpenTarget(url) => {
                    let url = url.to_owned();
                    face.link(
                        format!("tile.{leaf}.automation.target"),
                        format!("Open pull request {}", target_name.unwrap_or_default()),
                        shown,
                        accent,
                        move |link| {
                            link.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                                open(state, &url, tx)
                            })
                        },
                    )
                }
                Does::ShowDetails => {
                    let (leaf, session) = (leaf.to_owned(), session.to_owned());
                    face.link(
                        header_pop::anchor(&leaf, What::Details),
                        format!("Pull request {} details", pr_name.unwrap_or_default()),
                        shown,
                        accent,
                        move |link| {
                            let pressed = (leaf.clone(), session.clone());
                            let link = link.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                                header_pop::press(state, &pressed.0, &pressed.1, What::Details, tx)
                            });
                            header_pop::hovers(link, &leaf, &session, What::Details)
                        },
                    )
                }
            }
        })
}

/// The provenance line in the header of `leaf`, in full or as its icons alone.
pub(super) fn piece(leaf: &str, session: &str, line: &Line, level: Level, t: &Tokens) -> El {
    face(leaf, session, line, level, Some(t))
        .build(t)
        .key(format!("tile.{leaf}.provenance"))
}

/// The provenance line's width in full, and as its icons alone.
pub(super) fn widths(line: &Line) -> Widths {
    let at = |level| face("", "", line, level, None).width();
    let has_icons = parts(line).iter().any(|p| p.icon.is_some());
    Widths {
        full: at(Level::Full),
        short: None,
        icon: has_icons.then(|| at(Level::Icon)),
    }
}

fn checks(pr: &SessionPullRequest) -> Said {
    match pr.ci_status.as_deref() {
        Some("success") => ("passed", Tone::Ok),
        Some("failure") => ("failed", Tone::Bad),
        Some("pending") => ("running", Tone::Warn),
        _ => ("none reported", Tone::Neutral),
    }
}

fn review(pr: &SessionPullRequest) -> Said {
    match pr.review_status.as_deref() {
        Some("approved") => ("approved", Tone::Ok),
        Some("changes_requested") => ("changes requested", Tone::Warn),
        Some("pending") => ("waiting on a reviewer", Tone::Warn),
        _ => ("none requested", Tone::Neutral),
    }
}

fn merge(pr: &SessionPullRequest) -> Said {
    match pr.mergeable_state.as_deref() {
        Some("clean" | "has_hooks") => ("no conflicts", Tone::Ok),
        Some("dirty") => ("conflicts", Tone::Bad),
        Some("behind") => ("behind the base", Tone::Warn),
        Some("blocked") => ("blocked", Tone::Warn),
        Some("unstable") => ("unstable", Tone::Warn),
        _ => ("unknown", Tone::Neutral),
    }
}

fn fetched(pr: &SessionPullRequest) -> bool {
    pr.status_fetched_at
        .as_deref()
        .is_some_and(|at| !at.is_empty())
}

/// The pull request's name, as "attn#812".
pub(crate) fn name(pr: &SessionPullRequest) -> String {
    target_name(&pr.repository, pr.number)
}

/// The dot in the colour of a pull request's status, as its sidebar row shows it.
pub(crate) fn status_dot(pr: &SessionPullRequest, t: &Tokens) -> El {
    dot(status(pr).1.color(t), DotShape::Filled, DotSize::Small)
}

pub(crate) fn status_said(pr: &SessionPullRequest) -> &'static str {
    status(pr).0
}

fn capitalized(said: &str) -> String {
    let mut chars = said.chars();
    chars
        .next()
        .map(|first| first.to_uppercase().chain(chars).collect())
        .unwrap_or_default()
}

/// What stands between the pull request and its merge, as its watch or else its status says it:
/// a word, its tone, and the reason when there is one to add.
fn verdict(pr: &SessionPullRequest, now_ms: i64) -> (String, Tone, Option<String>) {
    match pr.state.as_str() {
        "merged" => return ("Merged".into(), Tone::Merged, None),
        "closed" => return ("Closed".into(), Tone::Neutral, None),
        _ => {}
    }
    if !fetched(pr) {
        return (
            "Waiting for GitHub".into(),
            Tone::Neutral,
            Some("attn has not fetched its status yet".into()),
        );
    }
    let watched = pr.watching == Some(true);
    match pr.readiness_state.as_deref().filter(|_| watched) {
        Some("ready") => (
            "Ready to merge".into(),
            Tone::Ok,
            readiness_words(pr, now_ms),
        ),
        Some("waiting") => ("Waiting".into(), Tone::Warn, readiness_words(pr, now_ms)),
        Some("blocked") => ("Blocked".into(), Tone::Bad, readiness_words(pr, now_ms)),
        _ => {
            let (said, tone) = status(pr);
            let why = match said {
                "approved" => Some(merge_hold(pr).to_owned()),
                "draft" => Some("Mark it ready for review to merge it".to_owned()),
                _ => None,
            };
            (capitalized(said), tone, why)
        }
    }
}

/// Why GitHub does not merge an approved pull request yet, from its merge state.
fn merge_hold(pr: &SessionPullRequest) -> &'static str {
    match pr.mergeable_state.as_deref() {
        Some("behind") => "Its branch is behind the base",
        Some("unstable") => "Some checks are not green",
        Some("blocked") => "GitHub still blocks the merge",
        _ => "GitHub has not yet said whether it can merge",
    }
}

/// Why the watch reads its pull request as it does, from the reason the daemon reported.
fn readiness_words(pr: &SessionPullRequest, now_ms: i64) -> Option<String> {
    let named = pr.watch_reviewer.as_deref().filter(|r| !r.is_empty());
    let reviewer = named.unwrap_or("the reviewer");
    let said = match pr.readiness_reason.as_deref()? {
        "green" => "GitHub reports it ready to merge".to_owned(),
        "codex_settling" => {
            let left = pr
                .settling_until
                .as_deref()
                .and_then(crate::sidebar::queue::epoch_ms)
                .map(|until| (until - now_ms + 59_999) / 60_000)
                .filter(|minutes| *minutes > 0);
            match left {
                Some(minutes) => format!("Codex's review settles in {minutes}m"),
                None => "Codex's review is settling".to_owned(),
            }
        }
        "codex_reviewing" => "Codex is reviewing it".to_owned(),
        "codex_approved_best_effort" => "Codex gave a thumbs-up".to_owned(),
        "codex_approval_missing" => "Waiting for a thumbs-up from Codex".to_owned(),
        "formal_review_approved" => "Approved on GitHub".to_owned(),
        "changes_requested" => match named {
            Some(reviewer) => format!("{reviewer} requested changes"),
            None => "Changes were requested".to_owned(),
        },
        "review_required" => "GitHub requires a review".to_owned(),
        "formal_approval_missing" => "Waiting for an approval".to_owned(),
        "selected_reviewer_requested" => format!("Waiting for {reviewer} to review"),
        "selected_reviewer_approved" => format!("{reviewer} approved it"),
        "review_dismissed" => format!("{reviewer}'s review was dismissed"),
        "selected_reviewer_approval_missing" => format!("Waiting for {reviewer} to approve"),
        "draft" => "It is a draft".to_owned(),
        "repository_policy_blocked" => "Repository rules block the merge".to_owned(),
        "behind_base" => "Its branch is behind the base".to_owned(),
        "merge_conflict" => "It has merge conflicts".to_owned(),
        "checks_not_green" => "Some checks are not green".to_owned(),
        _ => return None,
    };
    Some(said)
}

/// The gates a pull request passes before its merge: each one's name, and how it stands.
fn gates(pr: &SessionPullRequest) -> [(&'static str, Said); 3] {
    [
        ("Checks", checks(pr)),
        ("Review", review(pr)),
        ("Merge", merge(pr)),
    ]
}

/// How a gate stands, as a mark of its own shape.
fn gate_mark(tone: Tone, t: &Tokens) -> El {
    let ink = tone.color(t);
    match tone {
        Tone::Ok | Tone::Merged => icon(&attn_ui::icon::DONE, IconSize::Small, ink),
        Tone::Bad => icon(&attn_ui::icon::FAILED, IconSize::Small, ink),
        Tone::Warn => icon(&attn_ui::icon::WORKING, IconSize::Small, ink),
        Tone::Neutral => glyph(attn_ui::glyph::CIRCLE_OPEN, IconSize::Small, t.text_muted),
    }
}

fn cell_tone(tone: Tone) -> attn_ui::system::Tone {
    use attn_ui::system::Tone as Cell;
    match tone {
        Tone::Ok => Cell::Good,
        Tone::Warn => Cell::Warn,
        Tone::Bad => Cell::Bad,
        Tone::Merged | Tone::Neutral => Cell::Neutral,
    }
}

fn settled(pr: &SessionPullRequest) -> bool {
    state_rank(pr) > 0
}

fn title_of(pr: &SessionPullRequest) -> String {
    pr.title
        .clone()
        .filter(|t| !t.is_empty())
        .unwrap_or_else(|| pr.url.clone())
}

/// What stands before the pull request's merge and why, and why its watch is late. `key` names
/// the one shown.
fn standing(key: Option<&str>, pr: &SessionPullRequest, now_ms: i64, t: &Tokens) -> El {
    let (word, tone, why) = verdict(pr, now_ms);
    let ink = match tone {
        Tone::Neutral => t.text_secondary,
        tone => tone.color(t),
    };
    let keyed = |part: El, name: &str| match key {
        Some(key) => part.key(format!("{key}.{name}")),
        None => part,
    };
    let verdict = Inline::tight()
        .child(words(word, Type::Name, ink))
        .children(why.map(|why| words(why, Type::Small, t.text_secondary).wrap()));
    let error = pr
        .watch_error
        .clone()
        .filter(|e| !e.is_empty())
        .map(|error| {
            let said = if pr.watch_health.as_deref() == Some("delayed") {
                format!("Watch delayed: {error}")
            } else {
                error
            };
            words(said, Type::Small, t.warning).wrap()
        });
    Stack::tight()
        .w_full()
        .min_w(0.0)
        .child(keyed(verdict, "verdict"))
        .children(error.map(|e| keyed(e, "watch")))
}

/// The picked pull request: where it is and how old, its title, which opens it, its checks, review
/// and merge in a cell each, and what stands before its merge and why. Over a list, each part keeps
/// the room the list's pull requests need at most, so the list holds still while the pick moves.
fn details(key: &str, all: &[&SessionPullRequest], picked: usize, now_ms: i64, t: &Tokens) -> El {
    let pr = all[picked];
    let listed = all.len() > 1;
    let others = || {
        all.iter()
            .enumerate()
            .filter(move |(i, _)| *i != picked)
            .map(|(_, pr)| *pr)
    };
    let repository = pr
        .repository
        .strip_prefix("github.com/")
        .unwrap_or(&pr.repository);
    let place = Inline::top()
        .key(format!("{key}.place"))
        .min_w(0.0)
        .child(
            words(
                format!("{repository}#{}", pr.number),
                Type::Mono,
                t.text_tertiary,
            )
            .single_line()
            .truncate_middle()
            .shrink()
            .grow(1.0),
        )
        .child(
            words(
                crate::garden::model::ago(&pr.created_at, now_ms),
                Type::Small,
                t.text_tertiary,
            )
            .single_line(),
        );
    let url = pr.url.clone();
    let title = El::from(
        Link::new(format!("{key}.title"), title_of(pr))
            .kind(Type::Name)
            .ink(t.text_primary)
            .wrap()
            .build(t),
    )
    .description(format!("Open {} on GitHub", name(pr)))
    .min_w(0.0)
    .on_press_tx(move |state: &mut State, tx: &mut Tx| open(state, &url, tx));
    let title = Stack::steady(
        title,
        others().map(|pr| words(title_of(pr), Type::Name, t.text_primary).wrap_anywhere()),
    );
    let cells = Cells::new(format!("{key}.gates"));
    let cells = if fetched(pr) && !settled(pr) {
        Some(
            gates(pr)
                .into_iter()
                .fold(cells, |cells, (gate, (said, tone))| {
                    cells.marked(
                        format!("{gate} {said}"),
                        gate_mark(tone, t),
                        gate,
                        cell_tone(tone),
                    )
                }),
        )
    } else {
        listed.then(|| cells.said(capitalized(status(pr).0)))
    };
    let shown = standing(Some(key), pr, now_ms, t);
    let shown = match listed && settled(pr) {
        true => rune::el(),
        false => shown,
    };
    Stack::loose()
        .w_full()
        .min_w(0.0)
        .child(place)
        .child(title)
        .children(cells.map(|cells| cells.el(t)))
        .child(Stack::steady(
            shown,
            others()
                .filter(|pr| !settled(pr))
                .map(|pr| standing(None, pr, now_ms, t)),
        ))
}

/// The session's pull requests, one row each, the picked one marked: its number, its title, and
/// how its checks, review and merge stand, in columns.
fn list(key: &str, all: &[&SessionPullRequest], picked: usize, t: &Tokens) -> El {
    let one_repository = all.iter().all(|pr| pr.repository == all[0].repository);
    let number_of = |pr: &SessionPullRequest| match one_repository {
        true => format!("#{}", pr.number),
        false => name(pr),
    };
    let numbers_w = all
        .iter()
        .map(|pr| width_of(number_of(pr), Type::Mono))
        .fold(0.0, f32::max);
    col()
        .role("list")
        .label("Pull requests from this session")
        .w_full()
        .children(all.iter().enumerate().map(|(i, pr)| {
            let number = number_of(pr);
            let title = title_of(pr);
            let (said, tone) = status(pr);
            let chosen = i == picked;
            let ink = if settled(pr) {
                t.text_tertiary
            } else {
                t.text_primary
            };
            let (stands, reads): (El, String) = if fetched(pr) && !settled(pr) {
                let marks = Inline::joined()
                    .children(gates(pr).map(|(_, (_, tone))| gate_mark(tone, t).a11y_hidden()));
                let reads = gates(pr)
                    .map(|(gate, (said, _))| format!("{} {said}", gate.to_lowercase()))
                    .join(", ");
                (marks, reads)
            } else {
                let ink = if settled(pr) {
                    tone.color(t)
                } else {
                    t.text_tertiary
                };
                (words(said, Type::Small, ink).single_line(), said.to_owned())
            };
            let url = pr.url.clone();
            ListRow::new(
                format!("{key}.pr.{i}"),
                format!("{number} {title} · {reads}"),
            )
            .title_el(
                Inline::joined()
                    .shrink()
                    .child(
                        words(number, Type::Mono, t.text_tertiary)
                            .single_line()
                            .min_w(numbers_w),
                    )
                    .child(
                        words(title, if chosen { Type::Name } else { Type::Body }, ink)
                            .single_line()
                            .truncate()
                            .shrink(),
                    ),
            )
            .aside(stands)
            .unfocusable()
            .cursor(chosen)
            .chosen(chosen)
            .tint(chosen.then_some(t.accent))
            .on_press_tx(move |state: &mut State, tx: &mut Tx| open(state, &url, tx))
            .build(t)
            .focus_selected(chosen)
            .on_pointer_move_local(move |state: &mut State, _, tx: &mut Tx| {
                let pop = &mut state.window.desk.header_pop;
                if pop.picked == i {
                    tx.live_only();
                } else {
                    pop.picked = i;
                }
            })
        }))
}

/// How wide the details stand: as wide as the delegation chain, so a title of 60 characters
/// keeps two lines in `Name`.
const DETAILS_W: f32 = 400.0;

/// The open details popover: the picked pull request over the session's pull requests to pick
/// from, when it has more than one.
pub(crate) fn popover(state: &State, pop: &Pop, t: &Tokens) -> Option<El> {
    let session = state.facts.sessions.iter().find(|s| s.id == pop.session)?;
    let all = pull_requests(session);
    let picked = state
        .window
        .desk
        .header_pop
        .picked
        .min(all.len().checked_sub(1)?);
    let identity = name(all[picked]);
    let surface_key = pop.surface();
    let picking = all.len() > 1;
    let picked_block = Block::custom(
        format!("{surface_key}.picked"),
        details(&surface_key, &all, picked, state.window.now_ms, t),
    );
    let surface = Popover::new(surface_key.clone(), "Pull requests")
        .sectioned()
        .holding(pop.pinned);
    let surface = if picking {
        surface.about(picked_block).push(Block::custom(
            format!("{surface_key}.prs"),
            list(&surface_key, &all, picked, t),
        ))
    } else {
        surface.push(picked_block)
    };
    let moves = picking
        .then(|| keys_hint(["↑", "↓"], "move", t))
        .into_iter()
        .chain([
            keys_hint(["↵"], "open", t),
            keys_hint(["C"], "copy link", t),
        ]);
    let surface = match header_pop::foot(state, pop, moves, None, t) {
        Some(foot) => surface.actions(foot),
        None => surface,
    };
    let surface = surface
        .build(t)
        .label(format!("Pull request {identity}"))
        .w(DETAILS_W)
        .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
            header_pop::hover_popover(state, inside, tx)
        });
    let surface = if pop.pinned {
        let urls: Vec<String> = all.iter().map(|pr| pr.url.clone()).collect();
        surface
            .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                details_key(state, &urls, ev, tx)
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
        surface,
        &pop.anchor(),
        Placement::BottomStart,
        4.0,
    ))
}

fn details_key(state: &mut State, urls: &[String], ev: &KeyEvent, tx: &mut Tx) -> bool {
    if header_pop::switch_key(state, ev, tx) {
        return true;
    }
    let pop = &mut state.window.desk.header_pop;
    let last = urls.len().saturating_sub(1);
    let plain = ev.mods == rune::Mods::default();
    match &ev.key {
        Key::Down => pop.picked = (pop.picked + 1).min(last),
        Key::Up => pop.picked = pop.picked.saturating_sub(1),
        Key::Enter => {
            let url = urls[pop.picked.min(last)].clone();
            open(state, &url, tx);
        }
        Key::Text(c) if plain && c == "c" => tx.copy(urls[pop.picked.min(last)].clone()),
        _ => return false,
    }
    true
}

pub(crate) fn open(state: &mut State, url: &str, tx: &mut Tx) {
    if let Err(why) = tx.open_url(url) {
        state
            .window
            .notify(format!("Could not open {url}: {why}"), tx);
    }
}
