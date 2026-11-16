//! A member's launch settings as the panel edits them: the harness, model, effort and launch
//! desktop its next session starts with, each change written at once against the revision the
//! daemon last acknowledged.

use attn_protocol::{CrewMember, CrewSetMessage, CrewSetResultMessage, LaunchDesktopSetting};
use rune::Tx;

use super::autosave::{self, Edits, Phase, Sent, Spec};
use super::member_ref;
use crate::State;
use crate::settings::{Route, RouteHarness, Rules};

/// The launch fields the user changed, each over what the daemon holds.
#[derive(Clone, Debug, Default)]
pub(crate) struct Pending {
    pub(crate) agent: Option<String>,
    pub(crate) model: Option<String>,
    pub(crate) effort: Option<String>,
    pub(crate) launch_desktop: Option<LaunchDesktopSetting>,
}

/// The launch fields as a write carries them.
#[derive(Clone, Debug, Default)]
pub(crate) struct Selection {
    pub(crate) agent: String,
    pub(crate) model: String,
    pub(crate) effort: String,
    pub(crate) launch_desktop: Option<LaunchDesktopSetting>,
}

impl Selection {
    fn of(member: &CrewMember) -> Selection {
        Selection {
            agent: member.agent.clone().unwrap_or_default(),
            model: member.model.clone().unwrap_or_default(),
            effort: member.effort.clone().unwrap_or_default(),
            launch_desktop: member.launch_desktop.clone(),
        }
    }

    fn same(&self, other: &Selection) -> bool {
        self.agent == other.agent
            && self.model == other.model
            && self.effort == other.effort
            && json(&self.launch_desktop) == json(&other.launch_desktop)
    }
}

fn json(value: &impl serde::Serialize) -> serde_json::Value {
    serde_json::to_value(value).unwrap_or_default()
}

/// The `⌘` number a desktop id ends in, as `…/desktop_3` does.
fn desktop_slot(id: Option<&str>) -> u32 {
    id.and_then(|id| id.rsplit_once("/desktop_"))
        .and_then(|(_, n)| n.parse().ok())
        .unwrap_or(0)
}

/// The label the daemon gives a new desktop choice once it is saved.
fn saved_label(wanted: &LaunchDesktopSetting) -> String {
    let name = wanted.desktop_name.clone().unwrap_or_default();
    match desktop_slot(wanted.desktop_id.as_deref()) {
        0 => format!("{name} (no ⌘ number)"),
        slot => format!("{slot} · {name}"),
    }
}

/// The user's changes over what `member` holds: a new desktop choice the daemon saved under
/// its own label reads as the saved one.
pub(crate) fn merge(pending: &Pending, member: &CrewMember) -> Selection {
    let mut selection = Selection::of(member);
    if let Some(agent) = &pending.agent {
        selection.agent = agent.clone();
    }
    if let Some(model) = &pending.model {
        selection.model = model.clone();
    }
    if let Some(effort) = &pending.effort {
        selection.effort = effort.clone();
    }
    if let Some(wanted) = &pending.launch_desktop {
        let saved = member.launch_desktop.as_ref();
        let same_desktop = saved.is_some_and(|saved| {
            saved.desktop_id.is_some()
                && if wanted
                    .desktop_name
                    .as_deref()
                    .is_some_and(|n| !n.is_empty())
                {
                    saved.label.as_deref() == Some(saved_label(wanted).as_str())
                } else {
                    wanted.desktop_id == saved.desktop_id
                }
        });
        selection.launch_desktop = if same_desktop {
            saved.cloned()
        } else {
            Some(wanted.clone())
        };
    }
    selection
}

pub(crate) struct Launch;

impl Spec for Launch {
    type Draft = Pending;
    type Ack = CrewMember;

    fn equal(draft: &Pending, ack: &CrewMember) -> bool {
        merge(draft, ack).same(&Selection::of(ack))
    }

    fn settled(_: &CrewMember) -> Pending {
        Pending::default()
    }

    fn stale(candidate: &CrewMember, current: &CrewMember) -> bool {
        candidate.revision < current.revision
            || (candidate.revision == current.revision && json(candidate) == json(current))
    }
}

pub(crate) type LaunchEdits = Edits<Launch>;

/// What the launch card shows of a member's edit.
pub(crate) struct Shown {
    pub(crate) acknowledged: CrewMember,
    pub(crate) draft: Selection,
    pub(crate) saved: bool,
    pub(crate) saving: bool,
    pub(crate) error: Option<String>,
}

pub(crate) fn shown(state: &State, member: &str) -> Option<Shown> {
    let edit = state.window.crew.launches.get(member)?;
    let acknowledged = edit.acknowledged.clone()?;
    Some(Shown {
        draft: merge(&edit.draft, &acknowledged),
        acknowledged,
        saved: edit.phase == Phase::Saved,
        saving: !matches!(edit.phase, Phase::Saved | Phase::Error),
        error: edit.error.clone(),
    })
}

/// Takes the roster: each member of the selected profile is followed, its pushes adopted.
pub(crate) fn follow(state: &mut State) {
    let crew = state.facts.crew.clone();
    for member in crew.iter() {
        state.window.crew.launches.adopt(&member.id, member.clone());
    }
}

/// The user's change to `member`'s launch fields, written at once.
/// The user chose where `member`'s next session starts.
pub(crate) fn choose_desktop(
    state: &mut State,
    member: &str,
    desktop: attn_protocol::LaunchDesktopSetting,
    tx: &mut Tx,
) {
    let change = Pending {
        launch_desktop: Some(desktop),
        ..Pending::default()
    };
    update(state, member, change, tx);
}

pub(crate) fn update(state: &mut State, member: &str, change: Pending, tx: &mut Tx) {
    let Some(edit) = state.window.crew.launches.get(member) else {
        return;
    };
    let mut draft = edit.draft.clone();
    if change.agent.is_some() {
        draft.agent = change.agent;
    }
    if change.model.is_some() {
        draft.model = change.model;
    }
    if change.effort.is_some() {
        draft.effort = change.effort;
    }
    if change.launch_desktop.is_some() {
        draft.launch_desktop = change.launch_desktop;
    }
    state.window.crew.launches.update(member, draft);
    pump(state, member, tx);
}

/// Writes `member`'s edit when one is due, then each edit made while it was out.
pub(crate) fn pump(state: &mut State, member: &str, tx: &mut Tx) {
    let Some(write) = state.window.crew.launches.start(member) else {
        return;
    };
    let selection = merge(&write.draft, &write.acknowledged);
    let generation = write.generation;
    let member = member.to_owned();
    let profile_id =
        (!write.acknowledged.profile_id.is_empty()).then(|| write.acknowledged.profile_id.clone());
    let command_member = member_ref(&member);
    let expected = write.acknowledged.revision;
    state.ask(
        move |request_id| CrewSetMessage {
            member: command_member,
            expected_revision: Some(expected),
            agent: Some(selection.agent),
            model: Some(selection.model),
            effort: Some(selection.effort),
            launch_desktop_setting: selection.launch_desktop,
            profile_id,
            request_id: Some(request_id),
            ..CrewSetMessage::new()
        },
        move |state, answer, tx| {
            let sent = match autosave::lost(&answer) {
                Some(why) => Sent::Lost(why),
                None => match answer.parse::<CrewSetResultMessage>() {
                    Some(r) if r.success => match r.member {
                        Some(m) => Sent::Ack(m),
                        None => Sent::Refused(
                            "The daemon did not return the saved launch settings.".into(),
                            None,
                        ),
                    },
                    Some(r) => Sent::Refused(
                        r.error
                            .filter(|e| !e.is_empty())
                            .unwrap_or_else(|| "The launch settings were not saved.".into()),
                        r.member,
                    ),
                    None => Sent::Refused("The launch settings were not saved.".into(), None),
                },
            };
            if state.window.crew.launches.finish(&member, generation, sent) {
                pump(state, &member, tx);
            }
        },
        tx,
    );
}

/// The harness a member with no pin of its own runs on.
pub(crate) fn followed_harness(shown: &Shown) -> String {
    if shown.draft.agent.is_empty()
        && !shown
            .acknowledged
            .agent
            .clone()
            .unwrap_or_default()
            .is_empty()
    {
        return String::new();
    }
    if shown.draft.agent.is_empty() {
        shown.acknowledged.resolved_agent.clone()
    } else {
        shown.draft.agent.clone()
    }
}

/// The route chip's value and rules for `member`.
pub(crate) fn route(state: &State, member: &str) -> Option<(Route, Rules)> {
    let shown = shown(state, member)?;
    let follows = followed_harness(&shown);
    let rules = Rules {
        harnesses: crate::settings::listed_harnesses(state)
            .iter()
            .map(RouteHarness::from)
            .collect(),
        allow_none: true,
        require_available: true,
        none_label: Some("Crew default"),
        follows: (!follows.is_empty()).then_some(follows),
        ..Rules::default()
    };
    let value = Route::stored(&shown.draft.agent, &shown.draft.model, &shown.draft.effort);
    Some((value, rules))
}

/// The route the user picked in the chip, as one write.
pub(crate) fn write_route(state: &mut State, member: &str, route: Route, tx: &mut Tx) {
    let Some(shown) = shown(state, member) else {
        return;
    };
    let model = route.stored_model();
    let selection = &shown.draft;
    if route.harness != selection.agent {
        update(
            state,
            member,
            Pending {
                agent: Some(route.harness),
                model: Some(model),
                effort: Some(route.effort),
                launch_desktop: None,
            },
            tx,
        );
        return;
    }
    let mut change = Pending::default();
    if model != selection.model {
        change.model = Some(model);
    }
    if route.effort != selection.effort {
        change.effort = Some(route.effort);
    }
    if change.model.is_some() || change.effort.is_some() {
        update(state, member, change, tx);
    }
}

/// Writes again each edit the lost connection took.
pub(crate) fn reconnected(state: &mut State, tx: &mut Tx) {
    for member in state.window.crew.launches.lost_to_the_connection() {
        pump(state, &member, tx);
    }
}

/// What the save status says.
pub(crate) fn status(shown: &Shown) -> &'static str {
    if shown.error.is_some() {
        "Not saved"
    } else if shown.saving {
        "Saving…"
    } else {
        "Saved"
    }
}
