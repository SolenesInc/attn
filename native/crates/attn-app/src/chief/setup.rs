//! The Chief's setup beside its slot: the harness, model and effort it runs on, and on a
//! first setup the Notebook folder, saved with one press that also starts a first Chief.

use attn_protocol::{GetSettingsMessage, HarnessModel};
use attn_ui::Tokens;
use attn_ui::blocks::{Actions, Block, Field, Inline, Notice, Popover, Prose, Status};
use attn_ui::frame::Frame;
use attn_ui::system::{Button, Choice as ChoiceRow, Look, Segment, Segmented, Size, Type, words};
use rune::{El, FileDialogOptions, Tx, el};
use rune_kit::Placement;
use rune_kit::popover::anchored;

use super::{Phase, View};
use crate::State;
use crate::settings::{EffortMode, Route, RouteHarness, Rules};

pub(crate) const POP: &str = "chief-setup";
const PRIMARY: &str = "chief-setup.primary";
const RECHECK: &str = "chief-setup.recheck";

/// The setup while it shows: the route it would save, the folder the user chose, and the
/// focus it still owes.
pub(crate) struct Draft {
    key: String,
    first_run: bool,
    route: Route,
    notebook_root: Option<String>,
    wants_focus: bool,
    last_radio: Option<Group>,
    /// Whether the focus has been inside the setup: from then on, the focus leaving it closes
    /// it.
    held: bool,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Group {
    Harness,
    Model,
    Effort,
}

impl Group {
    const ALL: [Group; 3] = [Group::Harness, Group::Model, Group::Effort];

    fn id(self) -> &'static str {
        match self {
            Group::Harness => "harness",
            Group::Model => "model",
            Group::Effort => "effort",
        }
    }

    fn label(self) -> &'static str {
        match self {
            Group::Harness => "Chief harness",
            Group::Model => "Chief model",
            Group::Effort => "Chief effort",
        }
    }

    fn prefix(self) -> String {
        format!("{POP}.{}.", self.id())
    }
}

#[derive(Clone)]
enum Pick {
    Harness(String),
    Model(Box<HarnessModel>),
    Effort(String),
}

/// One radio of a group.
#[derive(Clone)]
struct Choice {
    key: String,
    label: String,
    mark: Option<String>,
    checked: bool,
    pick: Option<Pick>,
}

/// What the setup shows and would save, worked out from the draft and what the daemon said.
struct Form {
    busy: bool,
    checking: bool,
    no_harness: bool,
    harness_error: Option<String>,
    harnesses: Vec<Choice>,
    models: Vec<Choice>,
    model_error: Option<String>,
    models_loading: bool,
    efforts: Vec<Choice>,
    value: Route,
    harness: Option<(String, bool)>,
    folder: String,
    current_folder: String,
    can_save: bool,
}

fn chief_harnesses(state: &State) -> Vec<RouteHarness> {
    crate::settings::harness_read(state)
        .listed
        .iter()
        .filter(|h| {
            matches!(h.id.as_str(), "claude" | "codex")
                && h.available
                && state
                    .facts
                    .settings
                    .get(&format!("{}_cap_launch_instructions", h.id))
                    .is_some_and(|v| v == "true")
        })
        .map(RouteHarness::from)
        .collect()
}

fn low(_: &str) -> &'static str {
    "low"
}

fn form(state: &State, draft: &Draft, busy: bool) -> Form {
    let read = crate::settings::harness_read(state);
    let harnesses = chief_harnesses(state);
    let preferred = state.facts.settings.get("new_session_agent");
    let default = harnesses
        .iter()
        .find(|h| Some(&h.id) == preferred)
        .or(harnesses.first());
    let harness = harnesses
        .iter()
        .find(|h| h.id == draft.route.harness)
        .or(default)
        .cloned();
    let route = match &harness {
        Some(h) if h.id == draft.route.harness => draft.route.clone(),
        _ => Route {
            harness: harness.as_ref().map(|h| h.id.clone()).unwrap_or_default(),
            effort: "low".into(),
            ..Route::default()
        },
    };
    let rules = Rules {
        harnesses: harnesses.clone(),
        require_available: true,
        tier: Some("deep"),
        default_effort: Some(low),
        ..Rules::default()
    };
    let view = crate::settings::route_view(state, &route, &rules);
    let recommended = view.default_model.cloned();
    let selected = if route.model.is_empty() {
        recommended.clone()
    } else {
        view.selected.cloned()
    };
    let value = Route {
        model: selected
            .as_ref()
            .map_or_else(|| route.model.clone(), |m| m.id.clone()),
        provider: selected
            .as_ref()
            .map_or_else(|| route.provider.clone(), |m| m.provider.clone()),
        effort: if view.effort_mode == EffortMode::Unsupported {
            String::new()
        } else {
            route.effort.clone()
        },
        ..route.clone()
    };
    let models = view
        .options("")
        .into_iter()
        .filter(|o| {
            o.model
                .as_ref()
                .is_none_or(|m| m.access != "unsupported" && m.tier_source != "alias")
        })
        .map(|o| {
            let (key, checked) = match &o.model {
                None => (
                    format!("{}{}", Group::Model.prefix(), route.stored_model()),
                    true,
                ),
                Some(m) => (
                    format!("{}{}/{}", Group::Model.prefix(), m.provider, m.id),
                    value.model == m.id && value.provider == m.provider,
                ),
            };
            let mark = match &o.model {
                Some(m)
                    if recommended
                        .as_ref()
                        .is_some_and(|r| r.id == m.id && r.provider == m.provider) =>
                {
                    Some("recommended".to_owned())
                }
                Some(m) => m.tier.clone(),
                None => None,
            };
            Choice {
                key,
                label: o.label,
                mark,
                checked,
                pick: o.model.map(|m| Pick::Model(Box::new(m))),
            }
        })
        .collect();
    let efforts = if view.effort_mode == EffortMode::Levels {
        view.effort_levels
            .iter()
            .map(|level| Choice {
                key: format!("{}{level}", Group::Effort.prefix()),
                label: level.clone(),
                mark: None,
                checked: value.effort == *level,
                pick: Some(Pick::Effort(level.clone())),
            })
            .collect()
    } else {
        Vec::new()
    };
    let harness_choices = if harnesses.len() > 1 {
        harnesses
            .iter()
            .map(|h| Choice {
                key: format!("{}{}", Group::Harness.prefix(), h.id),
                label: h.name.clone(),
                mark: None,
                checked: harness.as_ref().is_some_and(|c| c.id == h.id),
                pick: Some(Pick::Harness(h.id.clone())),
            })
            .collect()
    } else {
        Vec::new()
    };
    let model_error = view.discovered.error.map(str::to_owned);
    let models_loading = view.discovered.loading;
    let current_folder = state
        .facts
        .settings
        .get("notebook.root.effective")
        .cloned()
        .unwrap_or_default();
    let folder = draft
        .notebook_root
        .clone()
        .unwrap_or_else(|| current_folder.clone());
    let can_save = !busy
        && harness.is_some()
        && !value.model.is_empty()
        && !read.loading
        && read.error.is_none()
        && !models_loading
        && model_error.is_none()
        && (!draft.first_run || !folder.trim().is_empty());
    Form {
        busy,
        checking: !read.answered && read.error.is_none(),
        no_harness: harnesses.is_empty(),
        harness_error: read.error,
        harnesses: harness_choices,
        models,
        model_error,
        models_loading,
        efforts,
        value,
        harness: harness.map(|h| (h.id, h.discovery)),
        folder,
        current_folder,
        can_save,
    }
}

impl Form {
    fn group(&self, group: Group) -> &[Choice] {
        match group {
            Group::Harness => &self.harnesses,
            Group::Model => &self.models,
            Group::Effort => &self.efforts,
        }
    }

    fn shows_route(&self) -> bool {
        !self.checking && !self.no_harness && self.harness_error.is_none()
    }

    /// Where the setup puts the focus it owes: the checked radio of `group` while that group is
    /// drawn and enabled, else the primary action, else the link that checks again.
    fn focus_target(&self, group: Option<Group>) -> Option<String> {
        let drawn = |g: &Group| {
            self.shows_route() && !self.busy && (*g != Group::Model || self.model_error.is_none())
        };
        let radio = group.filter(drawn).and_then(|g| {
            self.group(g)
                .iter()
                .find(|c| c.checked)
                .map(|c| c.key.clone())
        });
        radio.or_else(|| {
            if self.shows_route() && self.can_save {
                Some(PRIMARY.to_owned())
            } else if !self.shows_route() && !self.checking || self.model_error.is_some() {
                Some(RECHECK.to_owned())
            } else {
                None
            }
        })
    }
}

fn shown(state: &State) -> Option<(View, &Draft)> {
    let view = super::view(state)?;
    let draft = state
        .window
        .chief
        .draft
        .as_ref()
        .filter(|d| d.key == view.key)?;
    Some((view, draft))
}

/// Keeps the draft while the setup shows, and its discovery and focus going.
pub(super) fn poll(state: &mut State, tx: &mut Tx) -> bool {
    if !super::setup_shows(state) {
        return state.window.chief.draft.take().is_some();
    }
    let Some(view) = super::view(state) else {
        return false;
    };
    let mut changed = false;
    let created = state
        .window
        .chief
        .draft
        .as_ref()
        .is_none_or(|d| d.key != view.key);
    if created {
        let member = super::member(state).cloned().unwrap_or_default();
        let effort = member.effort.clone().filter(|e| !e.is_empty());
        state.window.chief.draft = Some(Draft {
            key: view.key.clone(),
            first_run: view.agent.is_none(),
            route: Route::stored(
                member.agent.as_deref().unwrap_or_default(),
                member.model.as_deref().unwrap_or_default(),
                effort.as_deref().unwrap_or("low"),
            ),
            notebook_root: None,
            wants_focus: true,
            last_radio: None,
            held: false,
        });
        changed = true;
    }
    let Some((view, draft)) = shown(state) else {
        return changed;
    };
    let form = form(state, draft, view.busy);
    if draft.route.harness.is_empty()
        && let Some((harness, _)) = &form.harness
    {
        let route = Route {
            harness: harness.clone(),
            effort: "low".into(),
            ..Route::default()
        };
        if let Some(d) = state.window.chief.draft.as_mut() {
            d.route = route;
        }
        changed = true;
    }
    changed |= crate::settings::chief_poll(state, form.harness.clone(), tx);
    changed |= follow_focus(state, &form, created, tx);
    changed
}

fn radio_group(key: &str) -> Option<Group> {
    Group::ALL
        .into_iter()
        .find(|g| key.starts_with(&g.prefix()))
}

/// Gives the setup the focus it owes on opening, gives a radio the user had focused back once
/// its group is drawn again, and takes the focus back when it falls to nothing, as when the
/// control holding it goes: each frame until the focus lands, since the setup draws after it
/// asks. While no control can take it yet the setup itself holds it, and that settles nothing.
/// The focus moving anywhere else settles both, and moving out of the setup once it held the
/// focus closes the setup.
fn follow_focus(state: &mut State, form: &Form, created: bool, tx: &mut Tx) -> bool {
    let focused = tx.focused().map(str::to_owned);
    let anchor = super::row_key(state).unwrap_or_default();
    let within = |f: &str, root: &str| f == root || f.starts_with(&format!("{root}."));
    let inside = focused.as_deref().is_some_and(|f| within(f, POP));
    let unsettled = focused
        .as_deref()
        .is_none_or(|f| f == POP || within(f, &anchor));
    let Some(draft) = state.window.chief.draft.as_mut() else {
        return false;
    };
    if draft.held && !inside && !unsettled {
        super::dismiss(state, false, tx);
        return true;
    }
    draft.held |= inside;
    if let Some(group) = focused.as_deref().and_then(radio_group) {
        draft.last_radio = Some(group);
        draft.wants_focus = false;
        return false;
    }
    if !created && !unsettled {
        draft.last_radio = None;
        draft.wants_focus = false;
        return false;
    }
    if focused.is_some() && !draft.wants_focus && draft.last_radio.is_none() {
        return false;
    }
    let target = form.focus_target(draft.last_radio);
    draft.wants_focus |= target.is_none();
    let target = target.unwrap_or_else(|| POP.to_owned());
    if focused.as_deref() == Some(target.as_str()) {
        return false;
    }
    tx.focus(target, true);
    true
}

fn pick(state: &mut State, pick: &Pick) {
    let Some((view, draft)) = shown(state) else {
        return;
    };
    let route = match pick {
        Pick::Harness(id) => Route {
            harness: id.clone(),
            effort: "low".into(),
            ..Route::default()
        },
        Pick::Model(model) => {
            let rules = Rules {
                harnesses: chief_harnesses(state),
                require_available: true,
                tier: Some("deep"),
                default_effort: Some(low),
                ..Rules::default()
            };
            let value = form(state, draft, view.busy).value;
            crate::settings::route_view(state, &value, &rules).with_model(Some(model))
        }
        Pick::Effort(effort) => Route {
            effort: effort.clone(),
            ..form(state, draft, view.busy).value
        },
    };
    if let Some(d) = state.window.chief.draft.as_mut() {
        d.route = route;
    }
}

fn submit(state: &mut State, tx: &mut Tx) {
    let Some((view, draft)) = shown(state) else {
        return;
    };
    let form = form(state, draft, view.busy);
    if !form.can_save {
        return;
    }
    let return_focus = !draft.first_run
        && tx
            .focused()
            .is_some_and(|f| f == POP || f.starts_with(&format!("{POP}.")));
    let folder = form.folder.trim();
    let picked = (draft.first_run && folder != form.current_folder).then(|| folder.to_owned());
    super::set_up(state, form.value, picked, return_focus, tx);
}

fn recheck(state: &mut State, tx: &mut Tx) {
    let Some((view, draft)) = shown(state) else {
        return;
    };
    let harness = form(state, draft, view.busy).harness.map(|(h, _)| h);
    let _ = state.daemon.send(&GetSettingsMessage::new());
    crate::settings::recheck_harnesses(state, harness.as_deref(), tx);
}

fn recheck_models(state: &mut State, tx: &mut Tx) {
    let Some((view, draft)) = shown(state) else {
        return;
    };
    let Some((harness, _)) = form(state, draft, view.busy).harness else {
        return;
    };
    crate::settings::rediscover_models(state, &harness, tx);
}

fn choose_folder(state: &mut State, tx: &mut Tx) {
    let Some((view, draft)) = shown(state) else {
        return;
    };
    let folder = form(state, draft, view.busy).folder;
    let options = FileDialogOptions {
        title: "Choose the Notebook folder".into(),
        directory: (!folder.is_empty()).then(|| folder.into()),
        ..FileDialogOptions::default()
    };
    tx.open_folder(options, |state: &mut State, chosen, tx| match chosen {
        Ok(Some(folder)) => {
            if let Some(d) = state.window.chief.draft.as_mut() {
                d.notebook_root = Some(folder.to_string_lossy().into_owned());
            }
        }
        Ok(None) => {}
        Err(why) => state
            .window
            .notify(format!("Could not ask for a folder: {why}"), tx),
    });
}

/// Arrows walk the focused radio's group, picking as they go, and Enter on a radio saves. The
/// harness and the effort are segments; the models are a list of choices.
fn radios(group: Group, choices: &[Choice], busy: bool, t: &Tokens) -> El {
    let label = |c: &Choice| match &c.mark {
        Some(mark) => format!("{} · {mark}", c.label),
        None => c.label.clone(),
    };
    let press = |c: &Choice| {
        let p = c.pick.clone();
        move |state: &mut State, _: &mut Tx| {
            if let Some(p) = &p {
                pick(state, p);
            }
        }
    };
    let key = format!("{POP}.{}", group.id());
    match group {
        Group::Harness | Group::Effort => Segmented::new(key, group.label())
            .disabled(busy)
            .options(choices.iter().map(|c| {
                Segment::new(c.key.clone(), c.label.clone())
                    .chosen(c.checked)
                    .on_press_tx(press(c))
            }))
            .build(t)
            .into(),
        Group::Model => {
            let n = choices.len();
            attn_ui::system::choices(&key, group.label()).children(choices.iter().enumerate().map(
                |(i, c)| {
                    let mut choice = ChoiceRow::new(c.key.clone(), c.label.clone())
                        .chosen(c.checked)
                        .disabled(busy);
                    if let Some(mark) = &c.mark {
                        choice = choice.note(mark.clone());
                    }
                    El::from(choice.build((i, n), t, press(c)).label(label(c)))
                },
            ))
        }
    }
}

fn field(key: &str, label: &str, control: El, t: &Tokens) -> Block {
    Field::new(format!("{POP}.{key}"), label)
        .control(control)
        .build(t)
}

fn link(key: &str, label: &str, t: &Tokens, press: fn(&mut State, &mut Tx)) -> El {
    Button::new(label)
        .look(Look::Quiet)
        .size(Size::Compact)
        .on_press_tx(press)
        .build(t)
        .key(key.to_owned())
        .into()
}

fn alert(said: &str, t: &Tokens) -> Block {
    Notice::error(format!("{POP}.alert"), said).build(t)
}

fn open_agents_settings(state: &mut State, tx: &mut Tx) {
    super::dismiss(state, false, tx);
    crate::settings::open(state, crate::settings::Section::Agents, tx);
}

/// The setup beside the slot, while it shows.
pub(crate) fn overlay(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let Some((view, draft)) = shown(state) else {
        return;
    };
    let Some(anchor) = super::row_key(state) else {
        return;
    };
    let form = form(state, draft, view.busy);
    let first_run = draft.first_run;
    let title = if first_run {
        format!("Set up {}", view.name)
    } else {
        format!("{}'s harness", view.name)
    };
    let k = attn_ui::kit(t);
    let mut popover = Popover::new(POP, title).aside(
        Button::new("esc")
            .look(Look::Quiet)
            .size(Size::Compact)
            .on_press_tx(|state: &mut State, tx: &mut Tx| super::dismiss(state, true, tx))
            .build(t)
            .key(format!("{POP}.close"))
            .label("Close Chief setup"),
    );
    if form.checking {
        popover = popover
            .push(Status::new(format!("{POP}.checking"), "Checking harnesses…", true).build(t));
    } else if !form.shows_route() {
        popover = popover.push(match &form.harness_error {
            Some(why) => alert(why, t),
            None => Prose::lead(
                format!("{POP}.empty"),
                "No harness here can run the Chief. Install Claude Code or Codex to start it.",
            )
            .build(t),
        });
        popover = popover.push(
            Actions::new(format!("{POP}.empty-actions"))
                .push(link(RECHECK, "Check again", t, recheck))
                .push(link(
                    &format!("{POP}.settings"),
                    "Settings › Agents",
                    t,
                    open_agents_settings,
                ))
                .build(),
        );
    } else {
        if !form.harnesses.is_empty() {
            popover = popover.push(field(
                "harness-field",
                "Harness",
                radios(Group::Harness, &form.harnesses, view.busy, t),
                t,
            ));
        }
        match &form.model_error {
            Some(why) => {
                popover = popover.push(alert(why, t)).push(
                    Actions::new(format!("{POP}.models-actions"))
                        .push(link(RECHECK, "Check again", t, recheck_models))
                        .build(),
                );
            }
            None => {
                popover = popover.push(field(
                    "model-field",
                    "Model",
                    radios(Group::Model, &form.models, view.busy, t),
                    t,
                ));
            }
        }
        if form.models_loading {
            popover = popover.push(
                Status::new(format!("{POP}.models-checking"), "Checking models…", true).build(t),
            );
        }
        if !form.efforts.is_empty() {
            popover = popover.push(field(
                "effort-field",
                "Effort",
                radios(Group::Effort, &form.efforts, view.busy, t),
                t,
            ));
        }
        if first_run {
            let choose = Button::new("Choose…")
                .look(Look::Quiet)
                .size(Size::Compact)
                .disabled(view.busy)
                .on_press_tx(choose_folder)
                .build(t)
                .key(format!("{POP}.folder.choose"))
                .label("Choose the Notebook folder");
            popover = popover.push(Block::custom(
                format!("{POP}.folder.block"),
                Inline::loose()
                    .key(format!("{POP}.folder-field"))
                    .child(words("Notebook", Type::Label, t.text_tertiary))
                    .child(
                        words(form.folder.clone(), Type::Mono, t.text_secondary)
                            .key(format!("{POP}.folder"))
                            .wrap_anywhere()
                            .shrink()
                            .min_w(0.0),
                    )
                    .child(El::from(choose)),
            ));
        }
        if let Some(why) = super::setup_error(state) {
            popover = popover.push(alert(why, t));
        }
        let primary = match (view.phase, first_run, &view.session) {
            (Phase::Configuring, true, _) => "Starting…".to_owned(),
            (Phase::Configuring, false, _) => "Saving…".to_owned(),
            (_, true, _) => format!("Start {}", view.name),
            (_, false, Some(_)) => "Save for next session".to_owned(),
            (_, false, None) => "Save".to_owned(),
        };
        let can_save = form.can_save;
        let button = El::from(
            Button::new(primary.clone())
                .look(if can_save { Look::Primary } else { Look::Plain })
                .trailing(words("⏎", Type::Control, attn_ui::warning_ink(t)).a11y_hidden())
                .on_press_tx(submit)
                .build(t)
                .key(PRIMARY)
                .label(primary),
        )
        .default_button()
        .a11y_value(if can_save { "ready" } else { "unavailable" });
        popover = popover.actions(
            Actions::new(format!("{POP}.actions"))
                .then_end()
                .push(button),
        );
    }
    let frame = Frame::panel(t);
    let surface = popover
        .build(t)
        .on_press_outside(|state: &mut State, tx: &mut Tx| {
            let over_the_window = state.window.layer.is_some();
            super::dismiss(state, !over_the_window, tx)
        });
    let notch = el()
        .key(format!("{POP}.notch"))
        .size(NOTCH, NOTCH)
        .fill(frame.fill())
        .border_sides([0.0, 0.0, 1.0, 1.0], frame.edge())
        .rotate(std::f32::consts::FRAC_PI_4)
        .a11y_hidden();
    let beside_row = SIDEBAR_INSET + BESIDE_SIDEBAR;
    layers.push(
        anchored(k, surface, &anchor, Placement::RightStart, beside_row).offset(0.0, -ABOVE_ROW),
    );
    layers.push(anchored(
        k,
        notch,
        &anchor,
        Placement::Right,
        beside_row - NOTCH / 2.0,
    ));
}

/// How far the popover sits from the sidebar's edge, and how far its top rises above the
/// row it opens from: `.chief-pop` at the sidebar's right plus 10 px and the row's top less
/// 10 px.
const BESIDE_SIDEBAR: f32 = 10.0;
const ABOVE_ROW: f32 = 10.0;
/// How far the row ends inside the sidebar: the sidebar's padding.
const SIDEBAR_INSET: f32 = rune::tok::space::S2;
/// The square turned into the notch that points at the row: `.chief-pop-notch`.
const NOTCH: f32 = 10.0;
