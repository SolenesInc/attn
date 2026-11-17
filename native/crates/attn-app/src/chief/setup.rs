//! The Chief's setup beside its slot: the harness, model and effort it runs on, and on a
//! first setup the Notebook folder, saved with one press that also starts a first Chief.

use attn_protocol::{GetSettingsMessage, HarnessModel};
use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::space;
use rune::{El, FileDialogOptions, Tx, col, row, text};
use rune_kit::popover::{anchored, raised};
use rune_kit::{Button, ButtonVariant, ControlSize, Placement, RadioItem};

use super::{Phase, View};
use crate::State;
use crate::settings::{EffortMode, Route, RouteHarness, Rules};

pub(crate) const POP: &str = "chief-setup";
const PRIMARY: &str = "chief-setup.primary";
const ESC_TYPE: rune::TextStyle = rune::TextStyle::new(10.0, 400.0).mono();
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
/// asks. The focus moving anywhere else settles both, and moving out of the setup once it held
/// the focus closes the setup.
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
    let target = form
        .focus_target(draft.last_radio)
        .unwrap_or_else(|| POP.to_owned());
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
    if let Some(d) = state.window.chief.draft.as_mut() {
        d.wants_focus = true;
    }
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
    if let Some(d) = state.window.chief.draft.as_mut() {
        d.wants_focus = true;
    }
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

/// Arrows walk the focused radio's group, picking as they go, and Enter on a radio saves.
fn radios(group: Group, choices: &[Choice], busy: bool, t: &Tokens) -> El {
    let k = attn_ui::kit(t);
    col()
        .key(format!("{POP}.{}", group.id()))
        .role("radiogroup")
        .label(group.label())
        .focus_group()
        .gap(space::S1)
        .children(choices.iter().map(|c| {
            let label = match &c.mark {
                Some(mark) => format!("{} · {mark}", c.label),
                None => c.label.clone(),
            };
            let mut item = RadioItem::new(label, c.checked).disabled(busy);
            if let Some(p) = c.pick.clone() {
                item = item.on_select(move |state: &mut State| pick(state, &p));
            }
            item.build(k).key(c.key.clone())
        }))
}

fn kicker(label: &str, t: &Tokens) -> El {
    type_::caps(label).color(t.text_tertiary)
}

fn link(key: &str, label: &str, t: &Tokens, press: fn(&mut State, &mut Tx)) -> El {
    Button::new(label)
        .variant(ButtonVariant::Ghost)
        .control_size(ControlSize::Small)
        .on_press_tx(press)
        .build(attn_ui::kit(t))
        .key(key.to_owned())
}

fn alert(text_: &str, t: &Tokens) -> El {
    text(text_.to_owned())
        .key(format!("{POP}.alert"))
        .role("alert")
        .font(type_::LABEL)
        .color(t.error)
        .wrap()
}

fn open_agents_settings(state: &mut State, tx: &mut Tx) {
    super::dismiss(state, false, tx);
    crate::settings::open(state, tx);
    crate::settings::select(state, crate::settings::Section::Agents, tx);
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
    let dimmed = t.text_dimmed;
    let head = row()
        .items_center()
        .gap(space::S2)
        .child(
            text(title.clone())
                .key(format!("{POP}.heading"))
                .role("heading")
                .font(type_::TITLE)
                .color(t.text_primary)
                .grow(1.0),
        )
        .child(
            Button::new("esc")
                .variant(ButtonVariant::GhostMuted)
                .control_size(ControlSize::Small)
                .on_press_tx(|state: &mut State, tx: &mut Tx| super::dismiss(state, true, tx))
                .label_with(move |_, _| text("esc").font(ESC_TYPE).color(dimmed))
                .build(k)
                .key(format!("{POP}.close"))
                .label("Close Chief setup")
                .px(0.0)
                .h(ESC_TYPE.line_px()),
        );
    let mut body = col().gap(space::S3);
    if form.checking {
        body = body.child(
            text("Checking harnesses…")
                .role("status")
                .font(type_::LABEL)
                .color(t.text_tertiary),
        );
    } else if !form.shows_route() {
        body = body.child(match &form.harness_error {
            Some(why) => alert(why, t),
            None => {
                text("No harness here can run the Chief. Install Claude Code or Codex to start it.")
                    .key(format!("{POP}.empty"))
                    .font(type_::LABEL)
                    .color(t.text_secondary)
                    .wrap()
            }
        });
        body = body.child(
            row()
                .gap(space::S2)
                .child(link(RECHECK, "Check again", t, recheck))
                .child(link(
                    &format!("{POP}.settings"),
                    "Settings › Agents",
                    t,
                    open_agents_settings,
                )),
        );
    } else {
        if !form.harnesses.is_empty() {
            body = body.child(
                col()
                    .gap(space::S1)
                    .child(kicker("Harness", t))
                    .child(radios(Group::Harness, &form.harnesses, view.busy, t)),
            );
        }
        let mut models = col().gap(space::S1).child(kicker("Model", t));
        match &form.model_error {
            Some(why) => {
                models = models.child(alert(why, t)).child(link(
                    RECHECK,
                    "Check again",
                    t,
                    recheck_models,
                ));
            }
            None => models = models.child(radios(Group::Model, &form.models, view.busy, t)),
        }
        if form.models_loading {
            models = models.child(
                text("Checking models…")
                    .role("status")
                    .font(type_::LABEL)
                    .color(t.text_tertiary),
            );
        }
        body = body.child(models);
        if !form.efforts.is_empty() {
            body = body.child(
                col()
                    .gap(space::S1)
                    .child(kicker("Effort", t))
                    .child(radios(Group::Effort, &form.efforts, view.busy, t)),
            );
        }
        if first_run {
            body = body.child(
                row()
                    .gap(space::S2)
                    .items_center()
                    .child(kicker("Notebook", t))
                    .child(
                        text(form.folder.clone())
                            .key(format!("{POP}.folder"))
                            .font(type_::MONO_CAPTION)
                            .color(t.text_secondary)
                            .grow(1.0)
                            .min_w(0.0),
                    )
                    .child(
                        Button::new("Choose…")
                            .variant(ButtonVariant::Neutral)
                            .control_size(ControlSize::Small)
                            .disabled(view.busy)
                            .on_press_tx(choose_folder)
                            .build(k)
                            .key(format!("{POP}.folder.choose"))
                            .label("Choose the Notebook folder"),
                    ),
            );
        }
        if let Some(why) = super::setup_error(state) {
            body = body.child(alert(why, t));
        }
        let primary = match (view.phase, first_run, &view.session) {
            (Phase::Configuring, true, _) => "Starting…".to_owned(),
            (Phase::Configuring, false, _) => "Saving…".to_owned(),
            (_, true, _) => format!("Start {}", view.name),
            (_, false, Some(_)) => "Save for next session".to_owned(),
            (_, false, None) => "Save".to_owned(),
        };
        let can_save = form.can_save;
        let shown = primary.clone();
        let mut button = Button::new(primary.clone())
            .variant(ButtonVariant::Submit)
            .label_with(move |style, ink| {
                row()
                    .gap(10.0)
                    .items_center()
                    .child(text(shown.clone()).font(style).color(ink))
                    .child(text("⏎").font(style).color(ink.alpha(0.65)))
            })
            .on_press_tx(submit)
            .build(k)
            .key(PRIMARY)
            .default_button()
            .label(primary)
            .a11y_value(if can_save { "ready" } else { "unavailable" });
        if !can_save {
            button = button.layer_opacity(0.45);
        }
        body = body.child(row().justify_end().child(button));
    }
    let surface = raised(
        k,
        col()
            .key(POP)
            .role("dialog")
            .label(title)
            .focusable(true)
            .no_focus_ring()
            .padding(space::S3)
            .gap(space::S3)
            .w(320.0)
            .fill(t.bg_elevated)
            .child(head)
            .child(body)
            .on_press_outside(|state: &mut State, tx: &mut Tx| {
                let over_the_window = state.window.layer.is_some();
                super::dismiss(state, !over_the_window, tx)
            }),
        k.radius.md,
    );
    layers.push(anchored(k, surface, &anchor, Placement::RightStart, 10.0));
}
