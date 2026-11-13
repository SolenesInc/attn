use attn_protocol::{AutomationProvenance, Session, SessionPullRequest};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, TextStyle, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, Placement};

use super::fit::{Level, Widths, text_w};
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

/// The provenance line in the header of `leaf`, in full or as its icons alone.
pub(super) fn piece(
    leaf: &str,
    session: &str,
    line: &Line,
    level: Level,
    font: TextStyle,
    t: &Tokens,
) -> El {
    let icons = level == Level::Icon;
    let pr_name = line.pull_request.as_ref().map(|pr| pr.name.as_str());
    let target_name = line
        .automation
        .as_ref()
        .and_then(|a| a.target.as_ref())
        .map(|t| t.name.as_str());
    let shown = parts(line).into_iter().filter_map(|part| {
        let content = if icons {
            part.icon?.to_owned()
        } else {
            part.text
        };
        let said = |ink| text(content.clone()).font(font).single_line().color(ink);
        let accent = if icons { t.accent } else { t.text_secondary };
        Some(match part.does {
            Does::Say => said(t.text_tertiary),
            Does::Title => said(t.text_secondary),
            Does::Status(tone) => said(tone.color(t)),
            Does::OpenTarget(url) => {
                let url = url.to_owned();
                header_pop::link(
                    format!("Open pull request {}", target_name.unwrap_or_default()),
                    content,
                    font,
                    accent,
                    t,
                    move |state, tx| open(state, &url, tx),
                )
                .key(format!("tile.{leaf}.automation.target"))
            }
            Does::ShowDetails => {
                let pressed = (leaf.to_owned(), session.to_owned());
                let details = header_pop::link(
                    format!("Pull request {} details", pr_name.unwrap_or_default()),
                    content,
                    font,
                    accent,
                    t,
                    move |state, tx| {
                        header_pop::press(state, &pressed.0, &pressed.1, What::Details, tx)
                    },
                )
                .key(header_pop::anchor(leaf, What::Details));
                header_pop::hovers(details, leaf, session, What::Details)
            }
        })
    });
    row()
        .key(format!("tile.{leaf}.provenance"))
        .items_center()
        .gap(space::S1)
        .children(shown)
}

/// The provenance line's width in full, and as its icons alone.
pub(super) fn widths(line: &Line, font: TextStyle) -> Widths {
    let run = |texts: Vec<&str>| {
        texts.iter().map(|s| text_w(s, font)).sum::<f32>()
            + space::S1 * texts.len().saturating_sub(1) as f32
    };
    let parts = parts(line);
    let icons: Vec<&str> = parts.iter().filter_map(|p| p.icon).collect();
    Widths {
        full: run(parts.iter().map(|p| p.text.as_str()).collect()),
        short: None,
        icon: (!icons.is_empty()).then(|| run(icons)),
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
    let local = rune::time::LocalTime::at(at);
    let when = format!(
        "{}-{:02}-{:02} {}:{:02}",
        local.year, local.month, local.day, local.hour, local.minute
    );
    let ago = match seconds {
        s if s < 60 => "just now".to_owned(),
        s if s < 3600 => format!("{}m ago", (s as f64 / 60.0).round()),
        s if s < 86_400 => format!("{}h ago", (s as f64 / 3600.0).round()),
        s => format!("{}d ago", (s as f64 / 86_400.0).round()),
    };
    format!("{ago} · {when}")
}

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
    let pair = |name: &str, (value, tone): (String, Tone)| {
        row()
            .w_full()
            .gap(space::S2)
            .child(
                text(name.to_owned())
                    .font(type_::LABEL)
                    .color(t.text_tertiary)
                    .w(64.0),
            )
            .child(
                text(value)
                    .font(type_::LABEL)
                    .color(match tone {
                        Tone::Neutral => t.text_secondary,
                        tone => tone.color(t),
                    })
                    .grow(1.0),
            )
    };
    let state_rows: Vec<El> = if current
        .status_fetched_at
        .as_deref()
        .is_none_or(str::is_empty)
    {
        vec![pair("status", ("waiting for GitHub".into(), Tone::Neutral))]
    } else {
        vec![
            pair("checks", said(checks(current))),
            pair("review", said(review(current))),
            pair("merge", said(merge(current))),
        ]
    };
    let url = current.url.clone();
    let head = row()
        .w_full()
        .gap(space::S2)
        .items_center()
        .child({
            let title = current
                .title
                .clone()
                .filter(|t| !t.is_empty())
                .unwrap_or_else(|| identity.clone());
            Button::new(format!("Open {identity} on GitHub"))
                .variant(ButtonVariant::Ghost)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| open(state, &url, tx))
                .label_with(move |_, ink| {
                    text(title)
                        .font(type_::LABEL)
                        .color(ink)
                        .truncate()
                        .grow(1.0)
                })
                .build(attn_ui::kit(t))
                .key(format!("{}.title", pop.surface()))
                .h(type_::LABEL.line_px())
                .px(0.0)
                .justify_start()
                .min_w(0.0)
                .grow(1.0)
        })
        .child(
            text(identity.clone())
                .font(type_::LABEL)
                .color(t.text_tertiary),
        );
    let list = (all.len() > 1).then(|| {
        col()
            .role("list")
            .label("Pull requests from this session")
            .w_full()
            .gap(2.0)
            .children(all.iter().enumerate().map(|(i, pr)| {
                let url = pr.url.clone();
                text(format!(
                    "#{} {} · {}",
                    pr.number,
                    pr.title
                        .clone()
                        .filter(|t| !t.is_empty())
                        .unwrap_or_else(|| pr.url.clone()),
                    status(pr).0
                ))
                .key(format!("{}.pr.{i}", pop.surface()))
                .role("button")
                .font(type_::LABEL)
                .color(if i == picked {
                    t.text_primary
                } else {
                    t.text_secondary
                })
                .single_line()
                .truncate()
                .on_hover_change(move |state: &mut State, inside, _: &mut Tx| {
                    if inside {
                        state.window.desk.header_pop.picked = i;
                    }
                })
                .on_press_tx(move |state: &mut State, tx: &mut Tx| open(state, &url, tx))
            }))
    });
    let hints = if all.len() > 1 {
        "↑↓ pick · ↵ open · c copy url"
    } else {
        "↵ open · c copy url"
    };
    let surface = col()
        .key(pop.surface())
        .role("dialog")
        .label(format!("Pull request {identity}"))
        .w(320.0)
        .padding(space::S2)
        .gap(space::S1)
        .focusable(true)
        .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
            header_pop::hover_popover(state, inside, tx)
        })
        .child(head)
        .child(pair("state", (current.state.clone(), Tone::Neutral)))
        .children(state_rows)
        .child(pair(
            "opened",
            (age(&current.created_at, state.window.now_ms), Tone::Neutral),
        ))
        .children(list)
        .child(text(hints).font(type_::LABEL).color(t.text_muted));
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
        rune_kit::popover::raised(attn_ui::kit(t), surface, 6.0),
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
