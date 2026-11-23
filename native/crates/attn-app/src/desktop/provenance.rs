use attn_protocol::{AutomationProvenance, Session, SessionPullRequest};
use attn_ui::Tokens;
use attn_ui::blocks::{Actions, Block, HeaderPiece, Inline, Popover, Stack};
use attn_ui::system::{
    DotShape, DotSize, IconSize, Link, ListRow, Type, dot, icon_button, keys_hint, words,
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
        .shrink()
        .max_w(RUN_MAX_W)
        .child(words(attn_ui::glyph::BOLT, Type::Mono, t.text_tertiary))
        .child(
            words(run.definition.clone(), Type::Mono, t.text_tertiary)
                .single_line()
                .truncate()
                .shrink(),
        )
        .children(run.number.map(|n| {
            words(format!("#{n}"), Type::Mono, t.text_tertiary)
                .key(format!("{row_key}.run.number"))
                .single_line()
        }))
}

const RUN_MAX_W: f32 = 120.0;

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
        if matches!(merge, Some("blocked" | "unstable")) {
            ("approved", Ok)
        } else {
            ("ready to merge", Ok)
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
        Some("pending") => ("waiting on a reviewer", Tone::Neutral),
        _ => ("none requested", Tone::Neutral),
    }
}

fn merge(pr: &SessionPullRequest) -> Said {
    match pr.mergeable_state.as_deref() {
        Some("clean") => ("no conflicts", Tone::Ok),
        Some("dirty") => ("conflicts", Tone::Bad),
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

/// A pull request at a glance, as the delegation chain's details show it: its status's dot, its
/// name and its status.
pub(crate) fn status_mark(pr: &SessionPullRequest, t: &Tokens) -> El {
    let (said, tone) = status(pr);
    Inline::joined()
        .child(dot(tone.color(t), DotShape::Filled, DotSize::Small))
        .child(words(name(pr), Type::Mono, t.text_secondary).single_line())
        .child(
            words(said, Type::Small, tone.color(t))
                .single_line()
                .truncate()
                .shrink(),
        )
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
                "approved" => Some("GitHub still blocks the merge".to_owned()),
                "draft" => Some("Mark it ready for review to merge it".to_owned()),
                _ => None,
            };
            (capitalized(said), tone, why)
        }
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

/// The checks, review and merge that are not yet fine, leaving out what the verdict `said`, its
/// word and its reason, already names.
fn in_the_way(pr: &SessionPullRequest, said: &str) -> Vec<(String, Tone)> {
    if !fetched(pr) || matches!(pr.state.as_str(), "merged" | "closed") {
        return Vec::new();
    }
    let said = said.to_lowercase();
    [
        ("checks ", checks(pr)),
        ("review ", review(pr)),
        ("", merge(pr)),
    ]
    .into_iter()
    .filter(|(_, (word, tone))| {
        *tone != Tone::Ok
            && !matches!(*word, "none reported" | "none requested" | "unknown")
            && !said.contains(word)
    })
    .map(|(what, (word, tone))| (format!("{what}{word}"), tone))
    .collect()
}

/// Who or what the watch waits for and when it last looked, as "Codex watch · 2m ago".
fn watch_words(pr: &SessionPullRequest, now_ms: i64) -> Option<String> {
    if pr.watching != Some(true) {
        return None;
    }
    let reviewer = pr.watch_reviewer.as_deref().filter(|r| !r.is_empty());
    let who = match (pr.watch_mode.as_deref(), reviewer) {
        (Some("formal-review"), Some(reviewer)) => format!("{reviewer} watch"),
        (Some("formal-review"), None) => "Review watch".to_owned(),
        (Some("green") | None, _) => "Checks watch".to_owned(),
        (Some(mode), _) => format!("{} watch", capitalized(mode)),
    };
    let checked = pr
        .watch_last_checked_at
        .as_deref()
        .map(|at| crate::garden::model::ago(at, now_ms))
        .filter(|ago| !ago.is_empty());
    Some(match checked {
        Some(ago) => format!("{who} · checked {ago}"),
        None => who,
    })
}

/// The picked pull request: its title, which opens it, what stands before its merge and why,
/// its watch, and where and when it opened.
fn details(key: &str, pr: &SessionPullRequest, now_ms: i64, t: &Tokens) -> El {
    let identity = name(pr);
    let url = pr.url.clone();
    let title = El::from(
        Link::new(
            format!("{key}.title"),
            pr.title
                .clone()
                .filter(|t| !t.is_empty())
                .unwrap_or_else(|| pr.url.clone()),
        )
        .kind(Type::Name)
        .ink(t.text_primary)
        .wrap()
        .build(t)
        .label(format!("Open {identity} on GitHub")),
    )
    .min_w(0.0)
    .on_press_tx(move |state: &mut State, tx: &mut Tx| open(state, &url, tx));
    let (word, tone, why) = verdict(pr, now_ms);
    let in_way = in_the_way(
        pr,
        &format!("{word} {}", why.as_deref().unwrap_or_default()),
    );
    let ink = |tone: Tone| match tone {
        Tone::Neutral => t.text_secondary,
        tone => tone.color(t),
    };
    let verdict_line = Inline::tight()
        .key(format!("{key}.verdict"))
        .child(dot(tone.color(t), DotShape::Filled, DotSize::Regular))
        .child(words(word.clone(), Type::Name, ink(tone)))
        .children(why.map(|why| words(why, Type::Small, t.text_secondary).wrap()));
    let in_way = (!in_way.is_empty()).then(|| {
        Inline::tight().key(format!("{key}.in-the-way")).children(
            in_way
                .into_iter()
                .map(|(said, tone)| words(said, Type::Small, ink(tone))),
        )
    });
    let watch = watch_words(pr, now_ms);
    let error = pr
        .watch_error
        .clone()
        .filter(|e| !e.is_empty())
        .map(|error| {
            if pr.watch_health.as_deref() == Some("delayed") {
                format!("Watch delayed: {error}")
            } else {
                error
            }
        });
    let watch = (watch.is_some() || error.is_some()).then(|| {
        Inline::tight()
            .key(format!("{key}.watch"))
            .children(watch.map(|w| words(w, Type::Small, t.text_tertiary)))
            .children(error.map(|e| words(e, Type::Small, t.warning).wrap()))
    });
    let repository = pr
        .repository
        .strip_prefix("github.com/")
        .unwrap_or(&pr.repository);
    let opened = crate::garden::model::ago(&pr.created_at, now_ms);
    let place = Inline::tight()
        .key(format!("{key}.place"))
        .child(
            words(
                format!("{repository}#{}", pr.number),
                Type::Mono,
                t.text_tertiary,
            )
            .wrap(),
        )
        .children(
            (!opened.is_empty())
                .then(|| words(format!("opened {opened}"), Type::Small, t.text_tertiary)),
        );
    Stack::loose()
        .w_full()
        .min_w(0.0)
        .child(title)
        .child(verdict_line)
        .children(in_way)
        .children(watch)
        .child(place)
}

/// The session's pull requests, one row each, the picked one marked: its status's dot, its
/// number, and its title.
fn list(key: &str, all: &[&SessionPullRequest], picked: usize, t: &Tokens) -> El {
    let one_repository = all.iter().all(|pr| pr.repository == all[0].repository);
    col()
        .role("list")
        .label("Pull requests from this session")
        .w_full()
        .children(all.iter().enumerate().map(|(i, pr)| {
            let number = if one_repository {
                format!("#{}", pr.number)
            } else {
                name(pr)
            };
            let title = pr
                .title
                .clone()
                .filter(|t| !t.is_empty())
                .unwrap_or_else(|| pr.url.clone());
            let (said, tone) = status(pr);
            let ink = if state_rank(pr) > 0 {
                t.text_tertiary
            } else {
                t.text_primary
            };
            let url = pr.url.clone();
            ListRow::new(
                format!("{key}.pr.{i}"),
                format!("{number} {title} · {said}"),
            )
            .lead(dot(tone.color(t), DotShape::Filled, DotSize::Regular))
            .title_el(
                Inline::joined()
                    .shrink()
                    .child(words(number, Type::Mono, t.text_tertiary).single_line())
                    .child(
                        words(title, Type::Body, ink)
                            .single_line()
                            .truncate()
                            .shrink(),
                    ),
            )
            .unfocusable()
            .cursor(i == picked)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| open(state, &url, tx))
            .build(t)
            .focus_selected(i == picked)
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

/// The open details popover: the session's pull requests to pick from when it has more than one,
/// and the picked one's details.
pub(crate) fn popover(state: &State, pop: &Pop, t: &Tokens) -> Option<El> {
    let session = state.facts.sessions.iter().find(|s| s.id == pop.session)?;
    let all = pull_requests(session);
    let picked = state
        .window
        .desk
        .header_pop
        .picked
        .min(all.len().checked_sub(1)?);
    let current = all[picked];
    let identity = name(current);
    let surface_key = pop.surface();
    let close = icon_button(
        format!("{surface_key}.close"),
        "Close pull request details",
        attn_ui::glyph::TIMES,
        IconSize::Large,
    )
    .on_press(|state: &mut State| header_pop::close(state))
    .build(t);
    let picking = all.len() > 1;
    let picked_block = Block::custom(
        format!("{surface_key}.picked"),
        details(&surface_key, current, state.window.now_ms, t),
    );
    let surface = Popover::new(
        surface_key.clone(),
        if picking {
            "Pull requests"
        } else {
            "Pull request"
        },
    )
    .aside(close);
    let surface = if picking {
        surface
            .push(Block::custom(
                format!("{surface_key}.prs"),
                list(&surface_key, &all, picked, t),
            ))
            .footing(picked_block)
    } else {
        surface.push(picked_block)
    };
    let surface = surface
        .actions(
            Actions::new("open").push(
                Inline::loose()
                    .key(format!("{surface_key}.keys"))
                    .children(picking.then(|| keys_hint(["↑", "↓"], "pick", t)))
                    .child(keys_hint(["↵"], "open", t))
                    .child(keys_hint(["c"], "copy url", t)),
            ),
        )
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
