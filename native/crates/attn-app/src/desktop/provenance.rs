use attn_protocol::{AutomationProvenance, Session, SessionPullRequest};
use attn_ui::Tokens;
use attn_ui::blocks::{Actions, Block, Facts, HeaderPiece, Inline, Popover, Stack};
use attn_ui::system::{Button, DotShape, DotSize, Link, Look, Size, Type, dot, keys_hint, words};
use rune::{Color, El, Key, KeyEvent, Tx};
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

/// How a pull request's status reads at a glance, as the Tauri app colors it.
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

fn said((label, tone): Said) -> (String, Tone) {
    (label.to_owned(), tone)
}

fn age(created: &str, now_ms: i64) -> String {
    let Some(at) = crate::sidebar::queue::epoch_ms(created) else {
        return String::new();
    };
    let seconds = ((now_ms - at).max(0) as f64 / 1000.0).round() as i64;
    let when = crate::calendar::date_time(at);
    let ago = match seconds {
        s if s < 60 => "just now".to_owned(),
        s if s < 3600 => format!("{}m ago", (s as f64 / 60.0).round()),
        s if s < 86_400 => format!("{}h ago", (s as f64 / 3600.0).round()),
        s => format!("{}d ago", (s as f64 / 86_400.0).round()),
    };
    format!("{ago} · {when}")
}

/// How wide the details stand: a pull request's title of 60 characters keeps two lines in
/// `Name`.
const DETAILS_W: f32 = 360.0;

/// The open details popover: the picked pull request's state, and the others the session has.
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
    let identity = target_name(&current.repository, current.number);
    let surface_key = pop.surface();
    let fact = |facts: Facts, name: &str, (value, tone): (String, Tone)| {
        let ink = match tone {
            Tone::Neutral => t.text_secondary,
            tone => tone.color(t),
        };
        facts.item(
            format!("{surface_key}.{name}"),
            name.to_owned(),
            words(value, Type::Body, ink),
        )
    };
    let facts = fact(
        Facts::new(format!("{surface_key}.facts")),
        "state",
        (current.state.clone(), Tone::Neutral),
    );
    let facts = if current
        .status_fetched_at
        .as_deref()
        .is_none_or(str::is_empty)
    {
        fact(
            facts,
            "status",
            ("waiting for GitHub".into(), Tone::Neutral),
        )
    } else {
        let facts = fact(facts, "checks", said(checks(current)));
        let facts = fact(facts, "review", said(review(current)));
        fact(facts, "merge", said(merge(current)))
    };
    let facts = fact(
        facts,
        "opened",
        (age(&current.created_at, state.window.now_ms), Tone::Neutral),
    );
    let mut blocks = vec![facts.build(t)];
    if all.len() > 1 {
        let list = Stack::tight()
            .role("list")
            .label("Pull requests from this session")
            .w_full()
            .children(all.iter().enumerate().map(|(i, pr)| {
                let url = pr.url.clone();
                let shown = format!(
                    "#{} {} · {}",
                    pr.number,
                    pr.title
                        .clone()
                        .filter(|t| !t.is_empty())
                        .unwrap_or_else(|| pr.url.clone()),
                    status(pr).0
                );
                El::from(
                    Link::new(format!("{surface_key}.pr.{i}"), shown)
                        .ink(if i == picked {
                            t.text_primary
                        } else {
                            t.text_secondary
                        })
                        .build(t),
                )
                .focusable(false)
                .on_hover_change(move |state: &mut State, inside, _: &mut Tx| {
                    if inside {
                        state.window.desk.header_pop.picked = i;
                    }
                })
                .on_press_tx(move |state: &mut State, tx: &mut Tx| open(state, &url, tx))
            }));
        blocks.push(Block::custom(format!("{surface_key}.prs"), list));
    }
    let picking = all.len() > 1;
    let url = current.url.clone();
    let open_button: El = Button::new("Open on GitHub ↗")
        .look(Look::Plain)
        .size(Size::Compact)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open(state, &url, tx))
        .build(t)
        .key(format!("{surface_key}.title"))
        .label(format!("Open {identity} on GitHub"))
        .into();
    let title = current
        .title
        .clone()
        .filter(|t| !t.is_empty())
        .unwrap_or_else(|| identity.clone());
    let surface = Popover::new(surface_key.clone(), title)
        .aside(words(identity.clone(), Type::Mono, t.text_tertiary))
        .push_all(blocks)
        .actions(
            Actions::new("open").push(open_button).push(
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

fn open(state: &mut State, url: &str, tx: &mut Tx) {
    if let Err(why) = tx.open_url(url) {
        state
            .window
            .notify(format!("Could not open {url}: {why}"), tx);
    }
}
