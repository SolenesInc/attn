//! A harness route, the harness, model and effort an agent runs on, as a chip that opens the
//! model picker: a harness list, the harness's models by tier, a filter that also takes an exact
//! model id, and the effort.

use attn_protocol::HarnessModel;
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, TextEdit, Tx, col, row, text};
use rune_kit::popover::{anchored, raised};
use rune_kit::{Button, ButtonVariant, ControlSize, Placement, TextInput};
use serde::{Deserialize, Serialize};

use super::catalogs::{self, Catalog, Discovered, TIER_OVERRIDES};
use super::pages::CAPTION;
use crate::State;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct Route {
    pub(crate) harness: String,
    pub(crate) provider: String,
    pub(crate) model: String,
    pub(crate) effort: String,
}

impl Route {
    /// A route as a setting stores it, its model qualified by its provider.
    pub(crate) fn stored(harness: &str, model: &str, effort: &str) -> Route {
        let (provider, model) = model.split_once('/').unwrap_or(("", model));
        Route {
            harness: harness.into(),
            provider: provider.into(),
            model: model.into(),
            effort: effort.into(),
        }
    }

    pub(crate) fn stored_model(&self) -> String {
        stored_model(&self.provider, &self.model)
    }
}

fn stored_model(provider: &str, model: &str) -> String {
    if provider.is_empty() {
        model.to_owned()
    } else {
        format!("{provider}/{model}")
    }
}

/// A harness as a route offers it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct RouteHarness {
    pub(crate) id: String,
    pub(crate) name: String,
    pub(crate) available: bool,
    pub(crate) model_pin: bool,
    pub(crate) effort_pin: bool,
    pub(crate) discovery: bool,
}

impl From<&attn_protocol::Harness> for RouteHarness {
    fn from(h: &attn_protocol::Harness) -> RouteHarness {
        RouteHarness {
            id: h.id.clone(),
            name: h.name.clone(),
            available: h.available,
            model_pin: h.model_pin,
            effort_pin: h.effort_pin,
            discovery: h.discovery,
        }
    }
}

/// What a route may hold, as the surface that edits it decides.
#[derive(Clone, Default)]
pub(crate) struct Rules {
    pub(crate) harnesses: Vec<RouteHarness>,
    pub(crate) allow_none: bool,
    pub(crate) fixed_harness: bool,
    pub(crate) require_available: bool,
    /// Whether the harness takes an effort here; every harness does without it.
    pub(crate) effort: Option<fn(&str) -> bool>,
    /// The tier the work needs: the route without a model runs the harness's default for it.
    pub(crate) tier: Option<&'static str>,
    pub(crate) default_effort: Option<fn(&str) -> &'static str>,
}

/// A tier the user gave a model, kept in the `model_tier_overrides` setting.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub(crate) struct TierOverride {
    pub(crate) harness: String,
    pub(crate) provider: String,
    pub(crate) model: String,
    pub(crate) tier: String,
}

pub(crate) fn tier_overrides(raw: Option<&str>) -> Vec<TierOverride> {
    raw.filter(|r| !r.trim().is_empty())
        .and_then(|r| serde_json::from_str::<Option<Vec<TierOverride>>>(r).ok())
        .flatten()
        .unwrap_or_default()
}

pub(crate) fn tier_label(tier: &str) -> String {
    let mut chars = tier.chars();
    chars
        .next()
        .map(|c| c.to_uppercase().chain(chars).collect())
        .unwrap_or_default()
}

fn tier_rank(tier: &str) -> Option<usize> {
    ["light", "standard", "deep"]
        .iter()
        .position(|t| *t == tier)
}

/// A model's tier and where it comes from, as the tier mark shows it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct TierInfo {
    pub(crate) tier: Option<String>,
    pub(crate) source: String,
}

impl TierInfo {
    /// "· deep", or "· no tier", or nothing; a row in a list leaves out its tier.
    pub(crate) fn mark(info: Option<&TierInfo>, in_row: bool) -> String {
        match info {
            None => String::new(),
            Some(TierInfo { tier: None, .. }) if in_row => "no tier".into(),
            Some(TierInfo { tier: None, .. }) => "· no tier".into(),
            Some(_) if in_row => String::new(),
            Some(TierInfo { tier: Some(t), .. }) => format!("· {t}"),
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum EffortMode {
    Hidden,
    Free,
    Unsupported,
    Levels,
    Pending,
}

/// One row of the model list.
#[derive(Clone, Debug)]
pub(crate) struct ModelOption {
    pub(crate) model: Option<HarnessModel>,
    pub(crate) label: String,
    pub(crate) group: String,
}

/// A route as its surfaces read it: what it runs, how its parts are named, and what the picker
/// offers for it.
pub(crate) struct View<'a> {
    pub(crate) value: Route,
    pub(crate) rules: &'a Rules,
    pub(crate) harness: Option<&'a RouteHarness>,
    pub(crate) discovered: Discovered<'a>,
    pub(crate) pins_unavailable: bool,
    pub(crate) can_pin_model: bool,
    pub(crate) effective: String,
    pub(crate) harness_name: String,
    pub(crate) selected: Option<&'a HarnessModel>,
    pub(crate) default_model: Option<&'a HarnessModel>,
    pub(crate) tier: Option<TierInfo>,
    pub(crate) model_label: String,
    pub(crate) unset_label: String,
    pub(crate) missing: bool,
    pub(crate) above_tier: bool,
    pub(crate) effort_mode: EffortMode,
    pub(crate) effort_levels: Vec<String>,
    pub(crate) default_effort: String,
    pub(crate) effort_missing: bool,
}

pub(crate) fn view<'a>(state: &'a State, input: &Route, rules: &'a Rules) -> View<'a> {
    let effective = input.harness.clone();
    let harness = rules.harnesses.iter().find(|h| h.id == effective);
    let discovered = harness.map_or_else(Discovered::default, |h| {
        catalogs::of(state, &h.id, h.discovery)
    });
    let catalog: Option<&Catalog> = discovered.catalog;
    let pins_unavailable = rules.require_available && !harness.is_some_and(|h| h.available);
    let can_pin_model = harness.is_none_or(|h| h.model_pin) && !pins_unavailable;
    let models: &[HarnessModel] = catalog.map_or(&[], |c| c.models.as_slice());
    let selected = models
        .iter()
        .find(|m| stored_model(&m.provider, &m.id) == input.stored_model());
    let mut value = input.clone();
    if let Some(m) = selected {
        value.model = m.id.clone();
        value.provider = m.provider.clone();
    }
    let default_model = rules.tier.and_then(|tier| {
        let id = catalog?.tier_default(tier)?;
        models
            .iter()
            .find(|m| m.id == id && m.tier.as_deref() == Some(tier) && m.access != "unsupported")
    });
    let runs = if !value.model.is_empty() {
        selected
    } else {
        default_model
    };
    let overrides = tier_overrides(state.facts.settings.get(TIER_OVERRIDES).map(String::as_str));
    let own = (!value.model.is_empty() && runs.is_none())
        .then(|| {
            overrides.into_iter().find(|o| {
                o.harness == effective && o.provider == value.provider && o.model == value.model
            })
        })
        .flatten();
    let tier = match runs {
        Some(m) => Some(TierInfo {
            tier: m.tier.clone(),
            source: m.tier_source.clone(),
        }),
        None if !value.model.is_empty()
            && (own.is_some()
                || catalog.is_some()
                || discovered.error.is_some()
                || harness.is_some_and(|h| !h.discovery)) =>
        {
            Some(TierInfo {
                source: if own.is_some() { "override" } else { "none" }.into(),
                tier: own.map(|o| o.tier),
            })
        }
        None => None,
    };
    let harness_name = harness
        .map(|h| h.name.clone())
        .filter(|n| !n.is_empty())
        .or_else(|| (!effective.is_empty()).then(|| effective.clone()))
        .unwrap_or_else(|| "None".into());
    let checking = discovered.loading
        || (catalog.is_none()
            && discovered.error.is_none()
            && harness.is_some_and(|h| h.discovery));
    let unset_label = match rules.tier {
        Some(tier) => {
            let runs = if checking {
                "checking models…".to_owned()
            } else if let Some(m) = default_model {
                display_name(m)
            } else {
                format!("none found, {harness_name} decides")
            };
            format!("{} default → {runs}", tier_label(tier))
        }
        None => format!("{harness_name} default"),
    };
    let model_label = if value.model.is_empty() {
        unset_label.clone()
    } else {
        selected
            .map(|m| m.name.clone())
            .filter(|n| !n.is_empty())
            .unwrap_or_else(|| value.stored_model())
    };
    let missing = !value.model.is_empty() && catalog.is_some() && selected.is_none();
    let effort_model = if value.model.is_empty() {
        default_model
    } else {
        selected
    };
    let effort_mode = if effective.is_empty()
        || harness.is_some_and(|h| !h.effort_pin)
        || rules.effort.is_some_and(|takes| !takes(&effective))
    {
        EffortMode::Hidden
    } else if (value.model.is_empty() && rules.tier.is_none())
        || harness.is_none_or(|h| !h.discovery)
        || discovered.error.is_some()
        || missing
    {
        EffortMode::Free
    } else if effort_model.is_some_and(|m| m.effort_support == "unsupported") {
        EffortMode::Unsupported
    } else if effort_model.is_some_and(|m| !m.effort_levels.is_empty()) {
        EffortMode::Levels
    } else if catalog.is_some() {
        EffortMode::Free
    } else {
        EffortMode::Pending
    };
    let effort_levels = effort_model.map_or_else(Vec::new, |m| m.effort_levels.clone());
    let above_tier = match (rules.tier, tier.as_ref().and_then(|t| t.tier.as_deref())) {
        (Some(want), Some(got)) => tier_rank(got) > tier_rank(want),
        _ => false,
    };
    let surface = rules
        .default_effort
        .map(|f| f(&effective))
        .unwrap_or_default();
    let default_effort = if value.effort.is_empty()
        && (effort_levels.iter().any(|l| l == surface) || effort_mode == EffortMode::Free)
    {
        surface.to_owned()
    } else {
        String::new()
    };
    let effort_missing = !value.effort.is_empty()
        && effort_mode == EffortMode::Levels
        && !effort_levels.contains(&value.effort);
    View {
        value,
        rules,
        harness,
        discovered,
        pins_unavailable,
        can_pin_model,
        effective,
        harness_name,
        selected,
        default_model,
        tier,
        model_label,
        unset_label,
        missing,
        above_tier,
        effort_mode,
        effort_levels,
        default_effort,
        effort_missing,
    }
}

fn display_name(m: &HarnessModel) -> String {
    if m.name.is_empty() {
        m.id.clone()
    } else {
        m.name.clone()
    }
}

impl View<'_> {
    /// The models the picker lists for `query`: the harness's aliases, then each tier from the
    /// one the work needs, with a stored model the catalog lacks in its tier's place.
    pub(crate) fn options(&self, query: &str) -> Vec<ModelOption> {
        if !self.can_pin_model {
            return Vec::new();
        }
        let models: &[HarnessModel] = self.discovered.catalog.map_or(&[], |c| &c.models);
        let mut ordered: Vec<ModelOption> = models
            .iter()
            .filter(|m| m.tier_source == "alias")
            .map(|m| ModelOption {
                model: Some(m.clone()),
                label: display_name(m),
                group: String::new(),
            })
            .collect();
        let mut groups: Vec<&str> = Vec::new();
        for group in [
            self.rules.tier,
            Some("deep"),
            Some("standard"),
            Some("light"),
            Some("no tier"),
        ]
        .into_iter()
        .flatten()
        {
            if !groups.contains(&group) {
                groups.push(group);
            }
        }
        for group in groups {
            for m in models {
                if m.tier_source != "alias" && m.tier.as_deref().unwrap_or("no tier") == group {
                    ordered.push(ModelOption {
                        model: Some(m.clone()),
                        label: display_name(m),
                        group: group.into(),
                    });
                }
            }
            let stored_tier = self
                .tier
                .as_ref()
                .and_then(|t| t.tier.as_deref())
                .unwrap_or("no tier");
            if !self.value.model.is_empty() && self.selected.is_none() && stored_tier == group {
                ordered.push(ModelOption {
                    model: None,
                    label: self.value.stored_model(),
                    group: group.into(),
                });
            }
        }
        let query = query.to_lowercase();
        let terms: Vec<&str> = query.split_whitespace().collect();
        ordered
            .into_iter()
            .filter(|option| {
                let words = format!(
                    "{} {} {}",
                    option.label,
                    option.model.as_ref().map_or(&self.value.model, |m| &m.id),
                    option
                        .model
                        .as_ref()
                        .map_or(&self.value.provider, |m| &m.provider)
                )
                .to_lowercase();
                terms.iter().all(|term| words.contains(term))
            })
            .collect()
    }

    /// The route with `model` picked, or the harness's default without one, keeping the effort
    /// where the new model takes it.
    pub(crate) fn with_model(&self, model: Option<&HarnessModel>) -> Route {
        let (id, provider) = model.map_or((String::new(), String::new()), |m| {
            (m.id.clone(), m.provider.clone())
        });
        let effort_pin = self.harness.is_none_or(|h| h.effort_pin);
        if id == self.value.model && provider == self.value.provider && effort_pin {
            return self.value.clone();
        }
        let source = model.or(self.default_model);
        let levels: &[String] = source.map_or(&[], |m| &m.effort_levels);
        let supported = source.is_none_or(|m| m.effort_support != "unsupported");
        let keeps =
            effort_pin && supported && (levels.is_empty() || levels.contains(&self.value.effort));
        Route {
            model: id,
            provider,
            effort: if keeps {
                self.value.effort.clone()
            } else {
                String::new()
            },
            ..self.value.clone()
        }
    }

    /// The effort the chip shows, when it shows one.
    fn effort_shown(&self) -> Option<String> {
        let shows = self.effort_mode != EffortMode::Hidden
            && (self.effort_mode != EffortMode::Unsupported || !self.value.effort.is_empty());
        shows.then(|| {
            [&self.value.effort, &self.default_effort]
                .into_iter()
                .find(|e| !e.is_empty())
                .cloned()
                .unwrap_or_else(|| "default".into())
        })
    }
}

/// Where a chip's route lives: `read` gives its value and rules, and `write` keeps a new one.
/// `id` tells the page which route of its own it is.
#[derive(Clone)]
pub(crate) struct Target {
    pub(crate) id: String,
    pub(crate) read: fn(&State, &str) -> Option<(Route, Rules)>,
    pub(crate) write: fn(&mut State, &str, Route, &mut Tx),
}

impl Target {
    fn chip_key(&self) -> String {
        format!("settings.route.{}", self.id)
    }
}

/// The open picker.
pub(crate) struct Picker {
    target: Target,
    /// The harness being browsed, until a pick commits it.
    preview: Option<String>,
    query: TextEdit,
    provider: TextEdit,
    /// The typed effort, while the effort field has it.
    effort: Option<TextEdit>,
}

const PICKER: &str = "settings.route-picker";
const FILTER: &str = "settings.route-picker.filter";
const PROVIDER: &str = "settings.route-picker.provider";
const EFFORT: &str = "settings.route-picker.effort";

fn picker(state: &State) -> Option<&Picker> {
    state.window.settings.route_picker.as_ref()
}

/// A route chip: the harness, model and effort it runs, opening the picker. A field also
/// labels each part and discovers its harness's models as it shows.
pub(super) fn chip(state: &State, t: &Tokens, target: Target, label: &str, field: bool) -> El {
    let Some((value, rules)) = (target.read)(state, &target.id) else {
        return row();
    };
    let view = view(state, &value, &rules);
    let key = target.chip_key();
    let open = picker(state).is_some_and(|p| p.target.id == target.id);
    let mut parts: Vec<(&str, String)> = Vec::new();
    if field || !rules.fixed_harness {
        parts.push(("Harness", view.harness_name.clone()));
    }
    let model = if view.effective.is_empty() {
        "Choose a model".to_owned()
    } else {
        let mark = TierInfo::mark(view.tier.as_ref(), false);
        if mark.is_empty() {
            view.model_label.clone()
        } else {
            format!("{} {mark}", view.model_label)
        }
    };
    parts.push(("Model", model));
    if let Some(effort) = view.effort_shown() {
        parts.push(("Effort", effort));
    }
    let summary = parts
        .iter()
        .map(|(_, v)| v.as_str())
        .collect::<Vec<_>>()
        .join(" · ");
    let shown = parts.clone();
    let ink_secondary = t.text_tertiary;
    let warning = view.missing.then_some(t.warning);
    let opener = target.clone();
    let button = Button::new(label)
        .label_with(move |font, ink| {
            row()
                .gap(space::S3)
                .items_center()
                .children(shown.into_iter().map(|(name, v)| {
                    col()
                        .gap(2.0)
                        .children(field.then(|| text(name).font(CAPTION).color(ink_secondary)))
                        .child(text(v).font(font).color(match warning {
                            Some(warning) if name == "Model" => warning,
                            _ => ink,
                        }))
                }))
        })
        .expanded(open)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| toggle(state, opener.clone(), tx))
        .build(attn_ui::kit(t))
        .key(key.clone())
        .a11y_value(summary);
    let recommended = (field && view.above_tier)
        .then_some(rules.tier)
        .flatten()
        .map(|tier| {
            text(format!("{} recommended", tier_label(tier)))
                .key(format!("{key}.recommended"))
                .font(CAPTION)
                .color(t.warning)
        });
    row()
        .gap(space::S2)
        .items_center()
        .child(button)
        .children(recommended)
}

/// The harnesses whose models the page's chips and the open picker want discovered.
pub(super) fn discover_wanted(state: &mut State, wanted: Vec<(String, bool)>, tx: &mut Tx) -> bool {
    let mut asked = false;
    let mut harnesses = wanted;
    if let Some(p) = picker(state)
        && let Some((value, rules)) = (p.target.read)(state, &p.target.id)
    {
        let shown = shown_harness(p, &value, &rules);
        if let Some(h) = rules.harnesses.iter().find(|h| h.id == shown) {
            harnesses.push((h.id.clone(), h.discovery));
        }
    }
    for (id, discovery) in harnesses {
        if discovery {
            asked |= catalogs::discover(state, &id, false, tx);
        }
    }
    asked
}

/// The harnesses a field chip of `target` discovers.
pub(super) fn wanted(state: &State, target: &Target) -> Option<(String, bool)> {
    let (value, rules) = (target.read)(state, &target.id)?;
    let h = rules.harnesses.iter().find(|h| h.id == value.harness)?;
    Some((h.id.clone(), h.discovery))
}

fn shown_harness(p: &Picker, value: &Route, rules: &Rules) -> String {
    p.preview.clone().unwrap_or_else(|| {
        if !value.harness.is_empty() || rules.allow_none {
            value.harness.clone()
        } else {
            rules
                .harnesses
                .first()
                .map(|h| h.id.clone())
                .unwrap_or_default()
        }
    })
}

/// The route the picker shows: the stored one, or a bare route on the harness being browsed.
fn shown_route(p: &Picker, value: &Route, rules: &Rules) -> Route {
    let harness = shown_harness(p, value, rules);
    if harness == value.harness {
        value.clone()
    } else {
        Route {
            harness,
            ..Route::default()
        }
    }
}

fn toggle(state: &mut State, target: Target, tx: &mut Tx) {
    if picker(state).is_some_and(|p| p.target.id == target.id) {
        close(state, tx);
        return;
    }
    state.window.settings.route_picker = Some(Picker {
        target,
        preview: None,
        query: TextEdit::default(),
        provider: TextEdit::default(),
        effort: None,
    });
    super::invalidate(state);
}

/// Closes the picker, keeping an effort typed into its field.
pub(super) fn close(state: &mut State, tx: &mut Tx) {
    commit_effort(state, tx);
    state.window.settings.route_picker = None;
}

fn finish(state: &mut State, tx: &mut Tx) {
    close(state, tx);
}

fn current(state: &State) -> Option<(Open, Route, Rules)> {
    let p = picker(state)?;
    let (value, rules) = (p.target.read)(state, &p.target.id)?;
    let shown = shown_route(p, &value, &rules);
    Some((
        Open {
            query: p.query.text.clone(),
            provider: p.provider.text.clone(),
        },
        shown,
        rules,
    ))
}

/// What a handler needs of the open picker, out of the borrow of the state.
struct Open {
    query: String,
    provider: String,
}

fn change(state: &mut State, route: Route, tx: &mut Tx) {
    let Some(p) = picker(state) else { return };
    let target = p.target.clone();
    (target.write)(state, &target.id, route, tx);
}

fn commit_effort(state: &mut State, tx: &mut Tx) {
    let Some(draft) = state
        .window
        .settings
        .route_picker
        .as_mut()
        .and_then(|p| p.effort.take())
    else {
        return;
    };
    let Some((_, shown, _)) = current(state) else {
        return;
    };
    let effort = draft.text.trim().to_owned();
    if effort != shown.effort {
        change(state, Route { effort, ..shown }, tx);
    }
}

/// Picks a model, then moves to its effort, or closes when the model takes none.
fn pick_model(state: &mut State, next: Route, tx: &mut Tx) {
    commit_effort(state, tx);
    let Some(p) = picker(state) else { return };
    let Some((value, _)) = (p.target.read)(state, &p.target.id) else {
        return;
    };
    if next != value {
        if let Some(p) = state.window.settings.route_picker.as_mut() {
            p.preview = None;
            p.query = TextEdit::default();
        }
        change(state, next, tx);
    }
    focus_effort(state, tx);
}

fn focus_effort(state: &mut State, tx: &mut Tx) {
    let Some((_, shown, rules)) = current(state) else {
        return;
    };
    let view = view(state, &shown, &rules);
    match view.effort_mode {
        EffortMode::Hidden | EffortMode::Unsupported => finish(state, tx),
        EffortMode::Levels => tx.focus(format!("{EFFORT}.0"), true),
        EffortMode::Pending => {}
        EffortMode::Free => tx.focus(EFFORT, true),
    }
}

fn pick_harness(state: &mut State, id: String, tx: &mut Tx) {
    commit_effort(state, tx);
    if let Some(p) = state.window.settings.route_picker.as_mut() {
        p.preview = Some(id);
        p.query = TextEdit::default();
        p.provider = TextEdit::default();
        p.effort = None;
    }
    super::invalidate(state);
}

fn has_provider(harness: &str) -> bool {
    !super::harnesses::builtin(harness)
}

/// The model and provider typed into the filter, and whether the catalog refuses them.
fn manual(state: &State) -> Option<(String, String, bool)> {
    let (p, shown, rules) = current(state)?;
    let mut model = p.query.trim().to_owned();
    let mut provider = if has_provider(&shown.harness) {
        p.provider.trim().to_owned()
    } else {
        String::new()
    };
    if has_provider(&shown.harness)
        && provider.is_empty()
        && let Some((pre, post)) = model.clone().split_once('/')
    {
        provider = pre.to_owned();
        model = post.to_owned();
    }
    let view = view(state, &shown, &rules);
    let unsupported = view.discovered.catalog.is_some_and(|c| {
        c.models
            .iter()
            .any(|m| m.id == model && m.provider == provider && m.access == "unsupported")
    });
    Some((model, provider, unsupported))
}

fn use_manual(state: &mut State, tx: &mut Tx) {
    let Some((model, provider, unsupported)) = manual(state) else {
        return;
    };
    if model.is_empty() || unsupported {
        return;
    }
    let Some((_, shown, _)) = current(state) else {
        return;
    };
    pick_model(
        state,
        Route {
            model,
            provider,
            effort: String::new(),
            ..shown
        },
        tx,
    );
}

fn submit_filter(state: &mut State, tx: &mut Tx) {
    let Some((p, shown, rules)) = current(state) else {
        return;
    };
    if p.query.trim().is_empty() {
        return;
    }
    let Some((_, _, unsupported)) = manual(state) else {
        return;
    };
    if unsupported {
        return;
    }
    if has_provider(&shown.harness) && !p.provider.trim().is_empty() {
        use_manual(state, tx);
        return;
    }
    let view = view(state, &shown, &rules);
    let first = view
        .options(&p.query)
        .into_iter()
        .find_map(|o| o.model.filter(|m| m.access != "unsupported"));
    match first {
        Some(model) => {
            let next = view.with_model(Some(&model));
            pick_model(state, next, tx);
        }
        None => use_manual(state, tx),
    }
}

/// The picker over the page, when one is open.
pub(super) fn overlay(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let Some(p) = picker(state) else { return };
    let Some((value, rules)) = (p.target.read)(state, &p.target.id) else {
        return;
    };
    let shown = shown_route(p, &value, &rules);
    let view = view(state, &shown, &rules);
    let k = attn_ui::kit(t);
    let mut body = row().gap(space::S3).items_start();
    if !rules.fixed_harness {
        body = body.child(harness_list(&shown, &rules, t));
    }
    body = body.child(main(state, p, &view, t));
    let surface = raised(
        k,
        col()
            .key(PICKER)
            .role("dialog")
            .label("Choose a model")
            .padding(space::S3)
            .w(560.0)
            .fill(t.bg_elevated)
            .child(body)
            .on_key(on_key)
            .modal_tx(true, close)
            .modal_press_through(true)
            .focus_return(p.target.chip_key()),
        k.radius.md,
    );
    layers.push(anchored(
        k,
        surface,
        &p.target.chip_key(),
        Placement::BottomStart,
        4.0,
    ));
}

fn kicker(label: &str, t: &Tokens) -> El {
    text(label.to_owned())
        .font(type_::SECTION)
        .color(t.text_tertiary)
}

fn option(key: String, label: &str, selected: bool, disabled: bool, t: &Tokens, content: El) -> El {
    Button::new(label)
        .variant(ButtonVariant::Ghost)
        .label_with(move |_, _| content)
        .disabled(disabled)
        .build(attn_ui::kit(t))
        .key(key)
        .role("option")
        .focus_selected(selected)
}

fn harness_list(shown: &Route, rules: &Rules, t: &Tokens) -> El {
    let preview = shown.harness.clone();
    let mut list = col()
        .key("settings.route-picker.harnesses")
        .role("listbox")
        .label("Harness")
        .gap(2.0)
        .w(160.0)
        .child(kicker("Harness", t));
    if rules.allow_none {
        list = list.child(
            option(
                "settings.route-picker.harness.none".into(),
                "None",
                preview.is_empty(),
                false,
                t,
                text("None").font(type_::BODY).color(t.text_primary),
            )
            .on_press_tx(|state: &mut State, tx: &mut Tx| {
                change(state, Route::default(), tx);
                finish(state, tx);
            }),
        );
    }
    for h in &rules.harnesses {
        let id = h.id.clone();
        let disabled = rules.require_available && !h.available;
        let content = row()
            .gap(space::S1)
            .items_center()
            .child(text(h.name.clone()).font(type_::BODY).color(t.text_primary))
            .children((!h.available).then(|| {
                text("●")
                    .key(format!(
                        "settings.route-picker.harness.{}.unavailable",
                        h.id
                    ))
                    .font(CAPTION)
                    .color(t.warning)
            }));
        let mut item = option(
            format!("settings.route-picker.harness.{}", h.id),
            &h.name,
            preview == h.id,
            disabled,
            t,
            content,
        );
        if !h.available {
            item = item.description("Unavailable on this daemon");
        }
        if !disabled {
            item = item.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                pick_harness(state, id.clone(), tx)
            });
        }
        list = list.child(item);
    }
    if !preview.is_empty() && !rules.harnesses.iter().any(|h| h.id == preview) {
        let label = format!("{preview} (not offered here)");
        list = list.child(option(
            "settings.route-picker.harness.missing".into(),
            &label,
            true,
            true,
            t,
            text(label.clone())
                .font(type_::BODY)
                .color(t.text_secondary),
        ));
    }
    list
}

fn main(state: &State, p: &Picker, view: &View, t: &Tokens) -> El {
    let k = attn_ui::kit(t);
    let refresh = view.harness.filter(|h| h.discovery).map(|h| {
        let id = h.id.clone();
        Button::new("Refresh")
            .variant(ButtonVariant::Ghost)
            .control_size(ControlSize::Small)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                catalogs::discover(state, &id, true, tx);
            })
            .build(k)
            .key("settings.route-picker.refresh")
            .busy(view.discovered.loading)
    });
    let mut main = col().grow(1.0).min_w(0.0).gap(space::S2).child(
        row()
            .gap(space::S2)
            .items_center()
            .child(kicker("Model", t).grow(1.0))
            .children(refresh),
    );
    if view.effective.is_empty() {
        return main.child(
            text("Choose a harness to see its models.")
                .font(CAPTION)
                .color(t.text_tertiary),
        );
    }
    if view.can_pin_model {
        main = main.child(
            TextInput::new(&p.query)
                .key(FILTER)
                .label("Filter models or enter an ID")
                .placeholder("Filter or enter a model ID")
                .on_edit(|state: &mut State| {
                    &mut state
                        .window
                        .settings
                        .route_picker
                        .as_mut()
                        .expect("the picker is open while its filter shows")
                        .query
                })
                .on_submit(submit_filter)
                .width(None)
                .build(k),
        );
    }
    main = main.child(model_list(p, view, t));
    let query = p.query.text.trim().to_owned();
    if view.can_pin_model && !query.is_empty() {
        let unsupported = manual(state).is_some_and(|(_, _, u)| u);
        let provider = has_provider(&view.effective).then(|| {
            TextInput::new(&p.provider)
                .key(PROVIDER)
                .label("Provider")
                .placeholder("Provider")
                .on_edit(|state: &mut State| {
                    &mut state
                        .window
                        .settings
                        .route_picker
                        .as_mut()
                        .expect("the picker is open while its provider shows")
                        .provider
                })
                .width(Some(120.0))
                .build(k)
        });
        main = main.child(
            row()
                .gap(space::S2)
                .items_center()
                .children(provider)
                .child(
                    Button::new(format!("Use “{query}”"))
                        .disabled(unsupported)
                        .on_press_tx(use_manual)
                        .build(k)
                        .key("settings.route-picker.manual"),
                ),
        );
    }
    main = main.children(effort_control(p, view, t));
    if let Some(detail) = view
        .discovered
        .catalog
        .map(|c| c.detail.clone())
        .filter(|d| !d.is_empty())
    {
        main = main.child(
            text(detail)
                .key("settings.route-picker.detail")
                .font(CAPTION)
                .color(t.text_tertiary)
                .wrap(),
        );
    }
    if view.harness.is_some_and(|h| !h.discovery) {
        let note = if view.can_pin_model {
            "Enter an exact model ID or use the harness default."
        } else {
            "The harness chooses its model."
        };
        main = main.child(text(note).font(CAPTION).color(t.text_tertiary).wrap());
    }
    if let Some(error) = view.discovered.error {
        main = main.child(
            text(error.to_owned())
                .key("settings.route-picker.error")
                .role("alert")
                .label(error.to_owned())
                .font(CAPTION)
                .color(t.error)
                .wrap(),
        );
    }
    main
}

fn model_list(p: &Picker, view: &View, t: &Tokens) -> El {
    let query = p.query.text.clone();
    let mut list = col()
        .key("settings.route-picker.models")
        .role("listbox")
        .label("Model")
        .gap(2.0)
        .max_h(280.0)
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        });
    let mut index = 0;
    if query.is_empty() || !view.can_pin_model {
        let next = view.with_model(None);
        let mut item = option(
            format!("settings.route-picker.model.{index}"),
            &view.unset_label,
            view.value.model.is_empty(),
            view.pins_unavailable,
            t,
            text(view.unset_label.clone())
                .font(type_::BODY)
                .color(t.text_primary),
        );
        if !view.pins_unavailable {
            item = item.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                pick_model(state, next.clone(), tx)
            });
        }
        list = list.child(item);
        index += 1;
    }
    let mut previous = String::new();
    for option_row in view.options(&query) {
        if !option_row.group.is_empty() && option_row.group != previous {
            let header = if option_row.group == "no tier" {
                "No tier".to_owned()
            } else if Some(option_row.group.as_str()) == view.rules.tier {
                format!("{} recommended", tier_label(&option_row.group))
            } else {
                tier_label(&option_row.group)
            };
            list = list.child(text(header).font(CAPTION).color(t.text_tertiary));
        }
        previous = option_row.group.clone();
        let (selected, disabled, mark, detail, next) = match &option_row.model {
            Some(m) => {
                let mark = TierInfo::mark(
                    Some(&TierInfo {
                        tier: m.tier.clone(),
                        source: m.tier_source.clone(),
                    }),
                    true,
                );
                let what = if m.tier_source == "alias" {
                    "The harness decides".to_owned()
                } else if !m.detail.is_empty() {
                    m.detail.clone()
                } else {
                    m.description.clone()
                };
                let detail = [
                    m.provider.clone(),
                    if m.name != m.id {
                        m.id.clone()
                    } else {
                        String::new()
                    },
                    what,
                ]
                .into_iter()
                .filter(|s| !s.is_empty())
                .collect::<Vec<_>>()
                .join(" · ");
                (
                    m.id == view.value.model && m.provider == view.value.provider,
                    m.access == "unsupported",
                    mark,
                    detail,
                    view.with_model(Some(m)),
                )
            }
            None => (
                true,
                false,
                TierInfo::mark(
                    Some(view.tier.as_ref().unwrap_or(&TierInfo {
                        tier: None,
                        source: "none".into(),
                    })),
                    true,
                ),
                if view.discovered.loading {
                    "Stored. Checking the catalog…".into()
                } else {
                    "Not in the catalog. Kept as stored.".into()
                },
                view.value.clone(),
            ),
        };
        let name = if mark.is_empty() {
            option_row.label.clone()
        } else {
            format!("{} {mark}", option_row.label)
        };
        let content = col()
            .gap(2.0)
            .child(text(name.clone()).font(type_::BODY).color(t.text_primary))
            .children(
                (!detail.is_empty())
                    .then(|| text(detail.clone()).font(CAPTION).color(t.text_tertiary)),
            );
        let mut item = option(
            format!("settings.route-picker.model.{index}"),
            &name,
            selected,
            disabled,
            t,
            content,
        );
        if !detail.is_empty() {
            item = item.description(detail);
        }
        if !disabled {
            item = item.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                pick_model(state, next.clone(), tx)
            });
        }
        list = list.child(item);
        index += 1;
    }
    if view.discovered.loading {
        list = list.child(
            text("Discovering models…")
                .key("settings.route-picker.loading")
                .role("status")
                .label("Discovering models…")
                .font(CAPTION)
                .color(t.text_tertiary),
        );
    }
    list
}

fn effort_control(p: &Picker, view: &View, t: &Tokens) -> Option<El> {
    let value = &view.value;
    let head = |body: El| {
        col()
            .key("settings.route-picker.effort-control")
            .gap(space::S1)
            .child(kicker("Effort", t))
            .child(body)
    };
    match view.effort_mode {
        EffortMode::Hidden => return None,
        EffortMode::Pending if !view.pins_unavailable => {
            let said = if value.effort.is_empty() {
                "Waiting for the catalog…".to_owned()
            } else {
                value.effort.clone()
            };
            return Some(head(text(said).font(CAPTION).color(t.text_tertiary)));
        }
        EffortMode::Unsupported if value.effort.is_empty() && p.effort.is_none() => {
            return Some(head(
                text("Not available for this model.")
                    .font(CAPTION)
                    .color(t.text_tertiary),
            ));
        }
        _ => {}
    }
    if view.effort_mode == EffortMode::Levels && p.effort.is_none() {
        let mut levels = vec![String::new()];
        levels.extend(view.effort_levels.iter().cloned());
        if view.effort_missing {
            levels.push(value.effort.clone());
        }
        let group = row()
            .key(EFFORT)
            .role("group")
            .label("Effort")
            .gap(space::S1)
            .wrap()
            .children(levels.into_iter().enumerate().map(|(i, level)| {
                let shown = if !level.is_empty() {
                    level.clone()
                } else if !view.default_effort.is_empty() {
                    format!("default ({})", view.default_effort)
                } else {
                    "default".into()
                };
                let next = Route {
                    effort: level.clone(),
                    ..value.clone()
                };
                let mut button = Button::new(shown)
                    .control_size(ControlSize::Small)
                    .disabled(view.pins_unavailable)
                    .build(attn_ui::kit(t))
                    .key(format!("{EFFORT}.{i}"))
                    .checked(value.effort == level);
                if !view.pins_unavailable {
                    button = button.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        change(state, next.clone(), tx);
                        finish(state, tx);
                    });
                }
                button
            }));
        return Some(head(group));
    }
    let empty = TextEdit::new(value.effort.clone());
    let draft = p.effort.as_ref().unwrap_or(&empty);
    let input = TextInput::new(draft)
        .key(EFFORT)
        .label("Effort")
        .placeholder("As the harness names it")
        .disabled(view.pins_unavailable)
        .on_edit(|state: &mut State| {
            let shown = current(state).map(|(_, r, _)| r.effort).unwrap_or_default();
            state
                .window
                .settings
                .route_picker
                .as_mut()
                .expect("the picker is open while its effort shows")
                .effort
                .get_or_insert_with(|| TextEdit::new(shown))
        })
        .on_submit(|state: &mut State, tx: &mut Tx| finish(state, tx))
        .width(Some(160.0))
        .build(attn_ui::kit(t));
    Some(head(input))
}

/// Up and Down move through a list, Home and End go to its ends, and Left and Right move
/// between the harnesses, the models and the efforts.
fn on_key(state: &mut State, key: &KeyEvent, tx: &mut Tx) -> bool {
    let Some(focused) = tx.focused().map(str::to_owned) else {
        return false;
    };
    let Some((_, shown, rules)) = current(state) else {
        return false;
    };
    let view = view(state, &shown, &rules);
    let p = picker(state).expect("current found the picker");
    let models = model_keys(p, &view);
    let harnesses = harness_keys(&rules);
    let efforts = effort_keys(p, &view);
    let zone = |keys: &[(String, bool)]| keys.iter().any(|(k, _)| *k == focused);
    let step = |keys: &[(String, bool)], tx: &mut Tx| -> bool {
        let enabled: Vec<&String> = keys.iter().filter(|(_, on)| *on).map(|(k, _)| k).collect();
        let at = enabled.iter().position(|k| **k == focused).unwrap_or(0);
        let next = match key.key {
            Key::Home => 0,
            Key::End => enabled.len().saturating_sub(1),
            Key::Down => (at + 1).min(enabled.len().saturating_sub(1)),
            Key::Up => at.saturating_sub(1),
            _ => return false,
        };
        match enabled.get(next) {
            Some(k) => {
                tx.focus((*k).clone(), true);
                true
            }
            None => false,
        }
    };
    let first_of = |keys: &[(String, bool)], selected: Option<String>| {
        selected
            .filter(|s| keys.iter().any(|(k, on)| k == s && *on))
            .or_else(|| keys.iter().find(|(_, on)| *on).map(|(k, _)| k.clone()))
    };
    if matches!(key.key, Key::Down | Key::Up | Key::Home | Key::End) {
        for keys in [&harnesses, &models, &efforts] {
            if zone(keys) {
                return step(keys, tx);
            }
        }
        if focused == FILTER && key.key == Key::Down {
            if let Some(k) = first_of(&models, None) {
                tx.focus(k, true);
            }
            return true;
        }
        return false;
    }
    if !matches!(key.key, Key::Left | Key::Right)
        || [FILTER, PROVIDER, EFFORT].contains(&focused.as_str())
    {
        return false;
    }
    let selected_model = selected_key(p, &view);
    let selected_harness = format!(
        "settings.route-picker.harness.{}",
        if shown.harness.is_empty() {
            "none"
        } else {
            &shown.harness
        }
    );
    let to = if key.key == Key::Left {
        if zone(&efforts) {
            first_of(&models, selected_model)
        } else {
            first_of(&harnesses, Some(selected_harness))
        }
    } else if zone(&harnesses) {
        first_of(&models, selected_model)
    } else {
        efforts.iter().find(|(_, on)| *on).map(|(k, _)| k.clone())
    };
    if let Some(to) = to {
        tx.focus(to, true);
    }
    true
}

fn harness_keys(rules: &Rules) -> Vec<(String, bool)> {
    let mut keys = Vec::new();
    if rules.fixed_harness {
        return keys;
    }
    if rules.allow_none {
        keys.push(("settings.route-picker.harness.none".into(), true));
    }
    for h in &rules.harnesses {
        keys.push((
            format!("settings.route-picker.harness.{}", h.id),
            !(rules.require_available && !h.available),
        ));
    }
    keys
}

fn model_keys(p: &Picker, view: &View) -> Vec<(String, bool)> {
    let mut keys = Vec::new();
    let mut index = 0;
    if p.query.text.is_empty() || !view.can_pin_model {
        keys.push((
            format!("settings.route-picker.model.{index}"),
            !view.pins_unavailable,
        ));
        index += 1;
    }
    for o in view.options(&p.query.text) {
        let enabled = o.model.as_ref().is_none_or(|m| m.access != "unsupported");
        keys.push((format!("settings.route-picker.model.{index}"), enabled));
        index += 1;
    }
    keys
}

fn selected_key(p: &Picker, view: &View) -> Option<String> {
    let mut index = 0;
    if p.query.text.is_empty() || !view.can_pin_model {
        if view.value.model.is_empty() {
            return Some(format!("settings.route-picker.model.{index}"));
        }
        index += 1;
    }
    for o in view.options(&p.query.text) {
        let selected = o
            .model
            .as_ref()
            .is_none_or(|m| m.id == view.value.model && m.provider == view.value.provider);
        if selected {
            return Some(format!("settings.route-picker.model.{index}"));
        }
        index += 1;
    }
    None
}

fn effort_keys(p: &Picker, view: &View) -> Vec<(String, bool)> {
    match view.effort_mode {
        EffortMode::Hidden | EffortMode::Pending | EffortMode::Unsupported
            if view.effort_mode != EffortMode::Unsupported || view.value.effort.is_empty() =>
        {
            Vec::new()
        }
        EffortMode::Levels if p.effort.is_none() => {
            let count = 1 + view.effort_levels.len() + usize::from(view.effort_missing);
            (0..count)
                .map(|i| (format!("{EFFORT}.{i}"), !view.pins_unavailable))
                .collect()
        }
        _ => vec![(EFFORT.into(), true)],
    }
}
