//! Home's Pull Requests card, as the Tauri app's dashboard has: the user's own pull requests,
//! then the review requests by repository, each with Approve, Merge and Mute.

use std::collections::{HashMap, HashSet};

use attn_client::{Facts, REFRESH_PRS_ID, pr_action_id};
use attn_protocol::{
    ApprovePRMessage, MergePRMessage, MuteAuthorMessage, MutePRMessage, MuteRepoMessage, PR,
    PRVisitedMessage, RefreshPRsMessage,
};
use attn_ui::type_;
use attn_ui::{Dialog, DialogFrame, GlyphButton, Tokens, glyph, icon};
use rune::tok::space;
use rune::{Color, Cycle, El, TextStyle, Tx, col, el, memo, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize};

use crate::State;
use crate::requests::Answer;
const DONE_SHOWS_SECONDS: f64 = 1.5;

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
enum Act {
    Approve,
    Merge,
}

impl Act {
    fn wire(self) -> &'static str {
        match self {
            Act::Approve => "approve",
            Act::Merge => "merge",
        }
    }

    /// The glyph and name of its button while it is in `phase`.
    fn shown(self, phase: Option<Phase>) -> (&'static str, &'static str) {
        match (self, phase) {
            (Act::Approve, None) => (glyph::CHECK, "Approve"),
            (Act::Approve, Some(Phase::Busy)) => ("…", "Approving"),
            (Act::Approve, Some(Phase::Done(_))) => (glyph::CHECK, "Approved"),
            (Act::Merge, None) => (glyph::MERGE, "Merge"),
            (Act::Merge, Some(Phase::Busy)) => ("…", "Merging"),
            (Act::Merge, Some(Phase::Done(_))) => (glyph::CHECK, "Merged"),
        }
    }
}

/// Who a pull request wants the user as, which its row opens with, as the Tauri app's `.pr-role`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Role {
    Reviewer,
    BotReviewer,
    Author,
}

impl Role {
    pub const ALL: [Role; 3] = [Role::Reviewer, Role::BotReviewer, Role::Author];

    fn of(pr: &PR) -> Role {
        if pr.role == "author" {
            Role::Author
        } else if pr.author.to_lowercase().contains("bot") {
            Role::BotReviewer
        } else {
            Role::Reviewer
        }
    }

    /// Drawn from the system's emoji font, as the Tauri app's are.
    pub fn emoji(self) -> &'static str {
        match self {
            Role::Reviewer => "👀",
            Role::BotReviewer => "🤖",
            Role::Author => "✏️",
        }
    }

    fn said(self) -> &'static str {
        match self {
            Role::Reviewer => "Review requested",
            Role::BotReviewer => "Review requested by a bot",
            Role::Author => "Yours",
        }
    }
}

/// Where the last refresh the user asked for stands.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
enum Refresh {
    #[default]
    Idle,
    Running,
    Failed(String),
}

#[derive(Clone, Copy, Debug, PartialEq)]
enum Phase {
    Busy,
    /// Done, showing its check until the clock reads this.
    Done(f64),
}

struct Merging {
    pr: String,
    number: i64,
    /// The Merge button that asked, to take the keyboard back.
    button: String,
}

#[derive(Default)]
pub(crate) struct PullRequests {
    acts: HashMap<(Act, String), Phase>,
    /// The pull request whose merge waits for the user's yes.
    merging: Option<Merging>,
    /// Pull requests the user muted, hidden until the daemon reports them muted or gone: Mute
    /// toggles, so a second press on a row still shown would unmute it.
    muting: HashSet<String>,
    /// Pull requests merged here, gone from the lists once their check has shown, until the
    /// daemon stops reporting them.
    merged: HashSet<String>,
    /// The repositories whose review requests home's card folds away.
    folded_repos: HashSet<String>,
    refresh: Refresh,
}

const SECTION_TITLE: rune::TextStyle = rune::TextStyle {
    weight: 500.0,
    tracking: 1.4,
    ..type_::CAPS
};

/// The pull requests nobody muted, not the pull request, its repository or its author, and not
/// merged here.
fn active<'a>(facts: &'a Facts, tracked: &PullRequests) -> Vec<&'a PR> {
    let pulls = &facts.pull_requests;
    pulls
        .prs
        .iter()
        .filter(|p| !p.muted && !tracked.muting.contains(&p.id))
        .filter(|p| !tracked.merged.contains(&p.id))
        .filter(|p| !pulls.repos.iter().any(|r| r.repo == p.repo && r.muted))
        .filter(|p| {
            !pulls
                .authors
                .iter()
                .any(|a| a.author == p.author && a.muted)
        })
        .collect()
}

fn needs_the_user(pr: &PR) -> bool {
    !pr.approved_by_me || pr.has_new_changes
}

/// Asks the daemon to poll GitHub for the pull requests now, unless a poll the user asked for
/// still runs; the refresh button says why a poll failed.
pub(crate) fn refresh(state: &mut State, tx: &mut Tx) {
    if state.window.pull_requests.refresh == Refresh::Running {
        return;
    }
    state.window.pull_requests.refresh = Refresh::Running;
    state.ask_as(
        REFRESH_PRS_ID.to_owned(),
        &RefreshPRsMessage::new(),
        |state, answer, _| {
            state.window.pull_requests.refresh = match answer.outcome() {
                Ok(_) => Refresh::Idle,
                Err(why) => Refresh::Failed(why),
            };
        },
        tx,
    );
}

fn act(state: &mut State, act: Act, pr: &str, tx: &mut Tx) {
    let key = (act, pr.to_owned());
    if state.window.pull_requests.acts.contains_key(&key) {
        return;
    }
    state
        .window
        .pull_requests
        .acts
        .insert(key.clone(), Phase::Busy);
    let id = pr_action_id(act.wire(), pr);
    let answered = move |state: &mut State, answer: Answer, tx: &mut Tx| match answer.outcome() {
        Ok(_) => {
            let until = tx.clock() + DONE_SHOWS_SECONDS;
            state
                .window
                .pull_requests
                .acts
                .insert(key, Phase::Done(until));
            tx.wake_at(until);
        }
        Err(reason) => {
            state.window.pull_requests.acts.remove(&key);
            state.window.notify(reason, tx);
        }
    };
    let pr = pr.to_owned();
    match act {
        Act::Approve => state.ask_as(
            id,
            &ApprovePRMessage {
                id: pr,
                ..ApprovePRMessage::new()
            },
            answered,
            tx,
        ),
        Act::Merge => state.ask_as(
            id,
            &MergePRMessage {
                id: pr,
                method: "squash".into(),
                ..MergePRMessage::new()
            },
            answered,
            tx,
        ),
    }
}

/// Lets a done action's check lapse when its time is up, a merged pull request with it, and
/// forgets a mute the daemon has taken and a merge the daemon no longer reports.
pub(crate) fn poll(state: &mut State, now: f64) -> bool {
    let tracked = &mut state.window.pull_requests;
    let before = tracked.acts.len();
    let mut merged = Vec::new();
    tracked.acts.retain(|(act, pr), phase| {
        let lapsed = matches!(phase, Phase::Done(until) if now >= *until);
        if lapsed && *act == Act::Merge {
            merged.push(pr.clone());
        }
        !lapsed
    });
    tracked.merged.extend(merged);
    let prs = &state.facts.pull_requests.prs;
    tracked
        .muting
        .retain(|id| prs.iter().any(|p| &p.id == id && !p.muted));
    tracked.merged.retain(|id| prs.iter().any(|p| &p.id == id));
    tracked.acts.len() != before
}

fn mute(state: &mut State, pr: &str) {
    if !state.window.pull_requests.muting.insert(pr.to_owned()) {
        return;
    }
    let _ = state.daemon.send(&MutePRMessage {
        id: pr.to_owned(),
        ..MutePRMessage::new()
    });
}

fn ask_merge(state: &mut State, pr: &str, number: i64, button: String) {
    state.window.pull_requests.merging = Some(Merging {
        pr: pr.to_owned(),
        number,
        button,
    });
}

fn confirm_merge(state: &mut State, tx: &mut Tx) {
    if let Some(pr) = end_merge(state, tx) {
        act(state, Act::Merge, &pr, tx);
    }
}

/// Closes the merge confirmation and hands the keyboard back to the button that opened it.
fn end_merge(state: &mut State, tx: &mut Tx) -> Option<String> {
    let Merging { pr, button, .. } = state.window.pull_requests.merging.take()?;
    tx.focus(button, true);
    Some(pr)
}

fn cancel_merge(state: &mut State, tx: &mut Tx) {
    end_merge(state, tx);
}

fn section(key: &str, title: &str, count: usize, rows: Vec<El>, t: &Tokens) -> El {
    col()
        .key(key.to_owned())
        .role("group")
        .label(title)
        .w_full()
        .gap(space::S1)
        .child(
            row()
                .gap(space::S2)
                .child(type_::upper(title, SECTION_TITLE).color(t.text_dimmed))
                .child(
                    text(count.to_string())
                        .font(type_::LABEL)
                        .color(t.text_tertiary),
                ),
        )
        .children(rows)
}

/// A pull request by its repository's name and number: "attn #20".
fn named(pr: &PR) -> String {
    format!("{} #{}", repo_name(&pr.repo), pr.number)
}

fn repo_name(repo: &str) -> &str {
    repo.rsplit('/').next().unwrap_or(repo)
}

struct Heading {
    said: String,
    /// The pull request's GitHub host, shown when its repository's pull requests span more
    /// than one.
    host: Option<String>,
}

fn pr_row(state: &State, pr: &PR, yours: bool, key: String, heading: Heading, t: &Tokens) -> El {
    let k = attn_ui::kit(t);
    let Heading {
        said: heading,
        host,
    } = heading;
    let role = Role::of(pr);
    let url = pr.url.clone();
    let visited = pr.id.clone();
    let named = match &host {
        Some(host) => format!("{heading} on {host}"),
        None => heading.clone(),
    };
    let open = Button::new(format!("{named}: {}", pr.title))
        .variant(ButtonVariant::GhostMuted)
        .control_size(ControlSize::Small)
        .help(format!("Open {heading} on GitHub"))
        .label_with({
            let heading = heading.clone();
            let host_ink = t.text_tertiary;
            let host_fill = t.bg_selected;
            move |font, ink| {
                row()
                    .grow(1.0)
                    .items_center()
                    .gap(space::S2)
                    .child(text(heading.clone()).font(font).color(ink))
                    .children(host.clone().map(|host| {
                        text(host.to_lowercase())
                            .font(TextStyle::new(10.0, 400.0).mono())
                            .color(host_ink)
                            .fill(host_fill)
                            .px(6.0)
                            .py(2.0)
                            .radius(3.0)
                    }))
            }
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            visit(state, &visited);
            open_url(state, &url, tx)
        })
        .build(k)
        .key(format!("{key}.open"))
        .grow(1.0);
    let top = row()
        .w_full()
        .items_center()
        .gap(space::S1)
        .child(
            text(role.emoji())
                .key(format!("{key}.role"))
                .role("img")
                .label(role.said())
                .font(type_::LABEL),
        )
        .child(open)
        .children(marks(pr, &key, t));
    let title = text(pr.title.clone())
        .font(type_::BODY)
        .color(t.text_primary)
        .wrap()
        .w_full()
        .a11y_hidden();
    let phase = |act: Act| {
        state
            .window
            .pull_requests
            .acts
            .get(&(act, pr.id.clone()))
            .copied()
    };
    let number = pr.number;
    let action = |chosen: Act| {
        let (glyph, label) = chosen.shown(phase(chosen));
        let id = pr.id.clone();
        let button = format!("{key}.{}", chosen.wire());
        let asking = button.clone();
        GlyphButton::new(glyph, label)
            .help(label)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| match chosen {
                Act::Approve => act(state, Act::Approve, &id, tx),
                Act::Merge => ask_merge(state, &id, number, asking.clone()),
            })
            .build(k)
            .key(button)
    };
    let mute_id = pr.id.clone();
    let new_session = {
        let pr = pr.clone();
        GlyphButton::new(glyph::POP_OUT, "New worktree session")
            .help("New worktree session")
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                crate::picker::pull_request::open_in_worktree(state, &pr, tx)
            })
            .build(k)
            .key(format!("{key}.new"))
    };
    let mute_author = (!pr.author.is_empty()).then(|| {
        let author = pr.author.clone();
        Button::new(author.clone())
            .variant(ButtonVariant::GhostMuted)
            .control_size(ControlSize::Small)
            .help(format!("Mute all PRs by {author}"))
            .on_press(move |state: &mut State| mute_author(state, &author))
            .build(k)
            .key(format!("{key}.mute-author"))
            .label(format!("Mute all PRs by {}", pr.author))
    });
    let footer = row()
        .w_full()
        .items_center()
        .gap(space::S1)
        .child(
            text(if yours {
                pr.reason.replace('_', " ")
            } else {
                String::new()
            })
            .key(format!("{key}.reason"))
            .font(type_::LABEL)
            .color(t.text_tertiary)
            .grow(1.0),
        )
        .child(new_session)
        .child(action(Act::Approve))
        .child(action(Act::Merge))
        .child(
            GlyphButton::new(glyph::BELL_SLASH, "Mute this PR")
                .help("Mute this PR")
                .on_press(move |state: &mut State| mute(state, &mute_id))
                .build(k)
                .key(format!("{key}.mute")),
        )
        .children(mute_author);
    let settled = pr.approved_by_me && !pr.has_new_changes;
    col()
        .key(key)
        .role("group")
        .label(heading)
        .w_full()
        .gap(space::S1)
        .padding(space::S2)
        .radius(6.0)
        .fill(t.bg_elevated)
        .opacity(if settled { 0.5 } else { 1.0 })
        .child(top)
        .child(title)
        .child(footer)
}

/// What changed on a pull request at a glance: new commits or comments since the user's last
/// visit, the user's approval, and how its CI went.
fn marks(pr: &PR, key: &str, t: &Tokens) -> Vec<El> {
    let mut marks = Vec::new();
    if pr.has_new_changes {
        marks.push(
            text("UPDATED")
                .key(format!("{key}.updated"))
                .role("status")
                .label("Updated")
                .help("New commits/comments since your last visit")
                .font(type_::SECTION)
                .color(t.accent)
                .px(space::S1)
                .radius(3.0)
                .fill(t.bg_selected),
        );
    }
    if pr.approved_by_me {
        marks.push(
            text(glyph::CHECK)
                .key(format!("{key}.approved"))
                .role("status")
                .label("Approved")
                .help("You approved this PR")
                .font(type_::LABEL)
                .color(t.success),
        );
    }
    let ci = pr
        .ci_status
        .as_deref()
        .filter(|s| !s.is_empty() && *s != "none");
    if let Some(ci) = ci {
        let color = match ci {
            "success" => t.success,
            "failure" => t.error,
            "pending" => t.warning,
            _ => t.text_tertiary,
        };
        marks.push(
            el().key(format!("{key}.ci"))
                .role("status")
                .label(format!("CI {ci}"))
                .help(format!("CI {ci}"))
                .w(6.0)
                .h(6.0)
                .radius(3.0)
                .fill(color),
        );
    }
    marks
}

/// Tells the daemon the user looked at the pull request, which clears its new changes.
fn visit(state: &mut State, pr: &str) {
    let _ = state.daemon.send(&PRVisitedMessage {
        id: pr.to_owned(),
        ..PRVisitedMessage::new()
    });
}

fn mute_author(state: &mut State, author: &str) {
    let _ = state.daemon.send(&MuteAuthorMessage {
        author: author.to_owned(),
        ..MuteAuthorMessage::new()
    });
}

fn mute_repo(state: &mut State, repo: &str) {
    let _ = state.daemon.send(&MuteRepoMessage {
        repo: repo.to_owned(),
        ..MuteRepoMessage::new()
    });
}

fn fold_repo(state: &mut State, repo: &str) {
    let folded = &mut state.window.pull_requests.folded_repos;
    if !folded.remove(repo) {
        folded.insert(repo.to_owned());
    }
}

/// Home's Pull Requests card: the user's own pull requests, then the review requests by
/// repository, each repository folding away or muted from its header.
pub(crate) fn home_card(state: &State, t: &Tokens) -> El {
    let tracked = &state.window.pull_requests;
    let mut acts: Vec<(Act, String, Option<u64>)> = tracked
        .acts
        .iter()
        .map(|((act, pr), phase)| {
            let done = match phase {
                Phase::Busy => None,
                Phase::Done(until) => Some(until.to_bits()),
            };
            (*act, pr.clone(), done)
        })
        .collect();
    acts.sort();
    let mut muting: Vec<&String> = tracked.muting.iter().collect();
    muting.sort();
    let mut folded: Vec<&String> = tracked.folded_repos.iter().collect();
    folded.sort();
    let mut merged: Vec<&String> = tracked.merged.iter().collect();
    merged.sort();
    let reads = (
        std::sync::Arc::as_ptr(&state.facts.pull_requests) as usize,
        std::sync::Arc::as_ptr(&state.facts.github) as usize,
        std::sync::Arc::as_ptr(&state.keymap) as usize,
        acts,
        (muting, merged),
        folded,
        &tracked.refresh,
        *t,
    );
    memo("home.prs", reads, || pr_card(state, t))
}

fn pr_card(state: &State, t: &Tokens) -> El {
    let facts = &state.facts;
    let tracked = &state.window.pull_requests;
    let active = active(facts, tracked);
    let needing = active.iter().filter(|p| needs_the_user(p)).count();
    let yours: Vec<&PR> = active
        .iter()
        .copied()
        .filter(|p| p.role == "author")
        .collect();
    let mut repos: Vec<(&str, Vec<&PR>)> = Vec::new();
    for pr in active.iter().copied().filter(|p| p.role != "author") {
        match repos.iter_mut().find(|(repo, _)| *repo == pr.repo) {
            Some((_, prs)) => prs.push(pr),
            None => repos.push((&pr.repo, vec![pr])),
        }
    }
    let mut hosts: HashMap<&str, HashSet<&str>> = HashMap::new();
    for pr in active.iter().filter(|p| !p.host.is_empty()) {
        hosts.entry(&pr.repo).or_default().insert(&pr.host);
    }
    let heading = |pr: &PR, said: String| Heading {
        said,
        host: (hosts.get(pr.repo.as_str()).map_or(0, HashSet::len) > 1).then(|| pr.host.clone()),
    };
    let mut body = col().key("home.prs.body").w_full().gap(14.0);
    let empty = |said: &str| crate::home::card_empty("home.prs.empty", said, t).role("status");
    if let Some(reason) = &facts.github.polling_off_reason {
        body = body.child(empty(reason));
    } else if active.is_empty() {
        body = body.child(empty("No PRs need attention"));
    } else {
        if !yours.is_empty() {
            body = body.child(section(
                "home.prs.yours",
                "Yours",
                yours.len(),
                yours
                    .iter()
                    .map(|p| {
                        let key = format!("home.pr.{}", p.id);
                        pr_row(state, p, true, key, heading(p, named(p)), t)
                    })
                    .collect(),
                t,
            ));
        }
        if !repos.is_empty() {
            let count = repos.iter().map(|(_, prs)| prs.len()).sum();
            let groups = repos
                .iter()
                .map(|(repo, prs)| repo_group(state, repo, prs, &heading, t))
                .collect();
            body = body.child(section(
                "home.prs.review",
                "Review requested",
                count,
                groups,
                t,
            ));
        }
    }
    let refresh_help = match crate::palette::label_of(state, &crate::Action::SessionRefreshPrs) {
        Some(keys) => format!("Refresh PRs ({keys})"),
        None => "Refresh PRs".to_owned(),
    };
    let actions = [refresh_button(&tracked.refresh, refresh_help, t)]
        .into_iter()
        .chain(facts.github.polling_off_reason.is_none().then(|| {
            crate::home::card_count(
                "home.prs.count",
                needing,
                format!("{needing} need attention"),
                t,
            )
        }))
        .collect();
    crate::home::card("home.prs", "Pull Requests", actions, body, t)
}

/// The card's refresh button: dots while the poll the user asked for runs, an error mark whose
/// help says why it failed, as the Tauri app's `.refresh-btn`.
fn refresh_button(refresh: &Refresh, idle_help: String, t: &Tokens) -> El {
    let (glyph, ink, help) = match refresh {
        Refresh::Idle => (&icon::REFRESH, t.text_dimmed, idle_help),
        Refresh::Running => (&icon::REFRESH, t.brand, idle_help),
        Refresh::Failed(why) => (&icon::REFRESH_FAILED, t.error, why.clone()),
    };
    let running = *refresh == Refresh::Running;
    Button::new(if running {
        "Refreshing PRs"
    } else {
        "Refresh PRs"
    })
    .variant(ButtonVariant::GhostMuted)
    .help(help)
    .on_press_tx(self::refresh)
    .label_with(move |_, _| {
        if running {
            refresh_dots(ink)
        } else {
            rune::icon(glyph, 12.0).color(ink)
        }
    })
    .build(attn_ui::kit(t))
    .key("home.prs.refresh")
    .h(20.0)
    .min_w(20.0)
    .px(0.0)
    .disabled(running)
}

/// `.refresh-dots`: three 4 point dots pulsing 0.16 s apart over 1.4 s.
fn refresh_dots(ink: Color) -> El {
    const PULSE: &[(f32, f32)] = &[(0.0, 0.5), (0.4, 1.0), (0.8, 0.5), (1.0, 0.5)];
    const PERIOD: f32 = 1.4;
    row()
        .gap(2.0)
        .items_center()
        .children([0.0, 0.16, 0.32].map(|delay| {
            el().w(4.0).h(4.0).radius(2.0).fill(ink).cycle(Cycle {
                period: PERIOD,
                phase: delay / PERIOD,
                stops: PULSE,
                still: 1.0,
                fps: 30.0,
            })
        }))
}

/// The band above Home's cards while GitHub holds the daemon's polls back, counting down to when
/// they resume, as the Tauri app's `.rate-limit-banner`.
pub(crate) fn rate_limit_banner(state: &State, t: &Tokens) -> Option<El> {
    let left_ms = rate_limit_left_ms(&state.facts, state.window.now_ms)?;
    let (minutes, seconds) = (left_ms / 60_000, left_ms % 60_000 / 1000);
    Some(
        row()
            .key("home.rate-limit")
            .role("status")
            .w_full()
            .items_center()
            .gap(8.0)
            .px(16.0)
            .py(10.0)
            .radius(6.0)
            .fill(t.brand.alpha(0.10))
            .border(1.0, t.brand.alpha(0.30))
            .child(rune::icon(&icon::RATE_LIMITED, 16.0).color(t.brand))
            .child(
                text(format!(
                    "GitHub rate limited. Resuming in {minutes}m {seconds}s"
                ))
                .key("home.rate-limit.text")
                .font(type_::BODY)
                .color(t.brand),
            ),
    )
}

fn rate_limit_left_ms(facts: &Facts, now_ms: i64) -> Option<i64> {
    let until = facts.pull_requests.rate_limited_until.as_deref()?;
    let left = crate::sidebar::queue::epoch_ms(until)? - now_ms;
    (left > 0).then_some(left)
}

/// The next moment the rate limit's countdown shows another second, or ends.
pub(crate) fn next_change(facts: &Facts, now_ms: i64) -> Option<i64> {
    let left = rate_limit_left_ms(facts, now_ms)?;
    Some(now_ms + left % 1000 + 1)
}

fn repo_group(
    state: &State,
    repo: &str,
    prs: &[&PR],
    heading: &dyn Fn(&PR, String) -> Heading,
    t: &Tokens,
) -> El {
    let k = attn_ui::kit(t);
    let folded = state.window.pull_requests.folded_repos.contains(repo);
    let key = format!("home.prs.repo.{repo}");
    let name = repo_name(repo);
    let (fold_repo_id, mute_repo_id) = (repo.to_owned(), repo.to_owned());
    let header = row()
        .w_full()
        .items_center()
        .gap(space::S1)
        .child(
            Button::new(format!("{name}, {} review", prs.len()))
                .variant(ButtonVariant::GhostMuted)
                .control_size(ControlSize::Small)
                .label_with({
                    let said = format!(
                        "{} {name}  {} review",
                        if folded {
                            glyph::CARET_RIGHT
                        } else {
                            glyph::CARET_DOWN
                        },
                        prs.len()
                    );
                    move |font, ink| {
                        row()
                            .grow(1.0)
                            .child(text(said.clone()).font(font).color(ink))
                    }
                })
                .on_press(move |state: &mut State| fold_repo(state, &fold_repo_id))
                .build(k)
                .key(format!("{key}.fold"))
                .a11y_value(if folded { "collapsed" } else { "expanded" })
                .grow(1.0),
        )
        .child(
            GlyphButton::new(glyph::BAN, "Mute all PRs from this repo")
                .help("Mute all PRs from this repo")
                .on_press(move |state: &mut State| mute_repo(state, &mute_repo_id))
                .build(k)
                .key(format!("{key}.mute")),
        );
    col()
        .key(key)
        .role("group")
        .label(name.to_owned())
        .w_full()
        .gap(space::S1)
        .child(header)
        .children(
            (!folded)
                .then(|| {
                    prs.iter()
                        .map(|p| {
                            pr_row(
                                state,
                                p,
                                false,
                                format!("home.pr.{}", p.id),
                                heading(p, format!("#{}", p.number)),
                                t,
                            )
                        })
                        .collect::<Vec<_>>()
                })
                .into_iter()
                .flatten(),
        )
}

fn open_url(state: &mut State, url: &str, tx: &mut Tx) {
    if let Err(why) = tx.open_url(url) {
        state
            .window
            .notify(format!("Could not open {url}: {why}"), tx);
    }
}

/// The yes the user gives before a merge, over the window.
pub(crate) fn merge_confirmation(state: &State, t: &Tokens) -> Vec<El> {
    let Some(Merging { number, .. }) = &state.window.pull_requests.merging else {
        return Vec::new();
    };
    let mut layer = Vec::new();
    Dialog::new(
        "home.prs.merge",
        format!("Merge PR #{number}?"),
        DialogFrame::merge(t),
    )
    .width(400.0)
    .on_close_tx(cancel_merge)
    .child(
        text("This will merge the pull request and delete the branch.")
            .key("home.prs.merge.said")
            .font(type_::LABEL)
            .color(t.text_tertiary)
            .wrap()
            .padding(16.0),
    )
    .child(
        row()
            .w_full()
            .gap(8.0)
            .padding(16.0)
            .justify_end()
            .border_sides([1.0, 0.0, 0.0, 0.0], t.border)
            .border_layout()
            .child(merge_button("Cancel", false, t, cancel_merge).key("home.prs.merge.cancel"))
            .child(merge_button("Merge", true, t, confirm_merge).key("home.prs.merge.confirm")),
    )
    .build(t, &mut layer);
    layer
}

/// `.modal-btn`: 8 points above and below 12 point text, inside a 1 point edge.
const MERGE_BUTTON_HEIGHT: f32 = 32.0;

/// The merge confirmation's `.modal-btn-cancel`, or its `.modal-btn-primary` when `primary`.
fn merge_button(
    label: &'static str,
    primary: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    let (fill, edge, ink, hover) = if primary {
        (
            t.annotation,
            t.annotation,
            t.on_accent,
            attn_ui::colors::mix_srgb(t.text_primary, t.merged, 0.71),
        )
    } else {
        (
            rune::Color::TRANSPARENT,
            t.border,
            t.text_tertiary,
            t.bg_elevated,
        )
    };
    Button::new(label)
        .variant(ButtonVariant::Ghost)
        .on_press_tx(press)
        .label_with(move |_, _| text(label).font(TextStyle::new(12.0, 600.0)).color(ink))
        .build(attn_ui::kit(t))
        .h(MERGE_BUTTON_HEIGHT)
        .px(16.0)
        .radius(4.0)
        .fill(fill)
        .border(1.0, edge)
        .hover(hover)
}
