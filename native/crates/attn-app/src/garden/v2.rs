use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use attn_client::Facts;
use attn_protocol::{
    GardenBed, GardenBedDeleteMessage, GardenBedDeleteResultMessage, GardenBedSaveMessage,
    GardenBedSaveResultMessage, Seed, SeedBedSetMessage, SeedBedSetResultMessage, Session,
};
use attn_ui::Tokens;
use attn_ui::blocks::{
    Actions, Bar, Block, Card, Dialog, Field, Inline, Pane, Popover, Refusal, Stack,
};
use attn_ui::system::{
    Button, DotShape, DotSize, FieldState, GroupHead, IconSize, ListRow, Look, Segment, Segmented,
    Size, Type, area_box, dot, icon_button, line_box, over_window, words,
};
use rune::{Color, El, Key, KeyEvent, TextEdit, Tx};
use rune_kit::{TextArea, TextInput};

use super::model::{self, Index};
use crate::State;
use crate::sidebar::model::AgentState;
use crate::sidebar::queue;

pub(crate) const SETTING: &str = "garden_v2_enabled";
pub(crate) const BED_RAIL_WIDTH: f32 = 180.0;
const FORM_NAME: &str = "garden.bed-form.name";
const FORM_NOTE: &str = "garden.bed-form.note";
const FORM_REPOS: &str = "garden.bed-form.repos";
const PICKER_PREFIX: &str = "garden.bed-picker.item.";
const PICKER_CLEAR: &str = "garden.bed-picker.clear";
const PICKER_NEW: &str = "garden.bed-picker.new";
const MAX_PARENT_HOPS: usize = 32;

pub(crate) fn enabled(state: &State) -> bool {
    crate::setting::on(&state.facts.settings, SETTING)
}

pub(crate) fn modal_open(state: &State) -> bool {
    enabled(state)
        && (state.window.garden.bed_form.is_some() || state.window.garden.bed_picker.is_some())
}

pub(crate) fn beds(state: &State) -> &[GardenBed] {
    state.facts.garden.beds_of(&state.facts.profiles.selected)
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) enum BedFilter {
    #[default]
    All,
    Bed(String),
    Unassigned,
    Automations,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) enum Lens {
    #[default]
    Now,
    Triage,
    All,
}

impl Lens {
    pub(crate) fn name(self) -> &'static str {
        match self {
            Lens::Now => "now",
            Lens::Triage => "triage",
            Lens::All => "all",
        }
    }

    pub(crate) fn label(self) -> &'static str {
        match self {
            Lens::Now => "Now",
            Lens::Triage => "Triage",
            Lens::All => "All",
        }
    }
}

pub(crate) fn active_bed(state: &State) -> BedFilter {
    match &state.window.garden.bed {
        BedFilter::Bed(id) if !beds(state).iter().any(|b| b.id == *id) => BedFilter::All,
        other => other.clone(),
    }
}

pub(crate) fn bed_label(state: &State, filter: &BedFilter) -> String {
    match filter {
        BedFilter::All => "All".to_owned(),
        BedFilter::Unassigned => "Unassigned".to_owned(),
        BedFilter::Automations => "Automations".to_owned(),
        BedFilter::Bed(id) => beds(state)
            .iter()
            .find(|b| b.id == *id)
            .map_or_else(|| "All".to_owned(), |b| b.name.clone()),
    }
}

pub(crate) fn select_bed(state: &mut State, filter: BedFilter, tx: &mut Tx) {
    state.window.garden.bed = filter;
    state.window.garden.trail.clear();
    state.window.garden.wide_in = None;
    state.window.garden.board_walk.clear_walk();
    super::focus_frame(state, tx);
    super::follow(state, tx);
}

pub(crate) fn select_lens(state: &mut State, lens: Lens, tx: &mut Tx) {
    state.window.garden.lens = lens;
    super::focus_frame(state, tx);
}

pub(crate) fn is_automation(seed: &Seed) -> bool {
    seed.automation_id.is_some()
}

pub(crate) fn is_hidden_closed(seed: &Seed) -> bool {
    model::is_closed(seed) && !model::has_open_work_below(seed)
}

pub(crate) fn filter_for_root(root: &Seed) -> BedFilter {
    if is_automation(root) {
        BedFilter::Automations
    } else if let Some(id) = root.bed_id.as_deref().filter(|id| !id.is_empty()) {
        BedFilter::Bed(id.to_owned())
    } else {
        BedFilter::Unassigned
    }
}

pub(crate) fn matches_bed(seed: &Seed, filter: &BedFilter) -> bool {
    match filter {
        BedFilter::Automations => is_automation(seed),
        BedFilter::All => !is_automation(seed),
        BedFilter::Bed(id) => !is_automation(seed) && seed.bed_id.as_deref() == Some(id.as_str()),
        BedFilter::Unassigned => {
            !is_automation(seed) && seed.bed_id.as_deref().unwrap_or("").is_empty()
        }
    }
}

pub(crate) fn root_id<'a>(mut seed_id: &'a str, index: &'a Index) -> &'a str {
    for _ in 0..MAX_PARENT_HOPS {
        let Some(seed) = index.by_id.get(seed_id) else {
            break;
        };
        let parent = model::parent_of(seed);
        if parent.is_empty() || !index.by_id.contains_key(parent) {
            break;
        }
        seed_id = parent;
    }
    seed_id
}

pub(crate) fn matches_any_bed(seed: &Seed, filters: &[BedFilter], index: &Index) -> bool {
    let root = index
        .by_id
        .get(root_id(&seed.id, index))
        .map_or(seed, Arc::as_ref);
    filters.iter().any(|filter| matches_bed(root, filter))
}

pub(crate) fn search_beds(beds: &[GardenBed], terms: &[String]) -> Vec<BedFilter> {
    let mut filters = Vec::new();
    for term in terms {
        match term.as_str() {
            "unassigned" | "none" => filters.push(BedFilter::Unassigned),
            "automations" | "auto" => filters.push(BedFilter::Automations),
            term => {
                if let Some(bed) = beds.iter().find(|bed| bed.id == term) {
                    filters.push(BedFilter::Bed(bed.id.clone()));
                } else {
                    filters.extend(
                        beds.iter()
                            .filter(|bed| {
                                bed.name.to_lowercase().contains(term)
                                    || bed.slug.to_lowercase().contains(term)
                            })
                            .map(|bed| BedFilter::Bed(bed.id.clone())),
                    );
                }
            }
        }
    }
    filters
}

pub(crate) fn live_session<'a>(
    seed: &Seed,
    sessions: &'a [Session],
) -> Option<(&'a Session, AgentState)> {
    let tender = seed.tender.as_ref()?;
    let id = tender
        .session_id
        .as_deref()
        .filter(|s| !s.is_empty())
        .or_else(|| tender.r#ref.strip_prefix("session:"))?;
    sessions
        .iter()
        .find(|s| s.id == id)
        .map(|s| (s, AgentState::parse(&s.state)))
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub(crate) enum Attention {
    Approval,
    Waiting,
    Working,
    Live,
    Quiet,
    Parked,
    Ready,
    Closed,
}

impl Attention {
    pub(crate) fn is_waiting(self) -> bool {
        matches!(self, Attention::Approval | Attention::Waiting)
    }

    pub(crate) fn is_now(self) -> bool {
        matches!(
            self,
            Attention::Approval | Attention::Waiting | Attention::Working | Attention::Live
        )
    }

    pub(crate) fn dot_look(self, t: &Tokens) -> (Color, DotShape) {
        match self {
            Attention::Approval => (t.state_approval, DotShape::Filled),
            Attention::Waiting => (t.state_waiting, DotShape::Filled),
            Attention::Working => (t.state_working, DotShape::Filled),
            Attention::Live => (t.brand, DotShape::Filled),
            Attention::Quiet => (t.warning, DotShape::Hollow),
            Attention::Parked => (t.warning, DotShape::Filled),
            Attention::Ready => (t.accent, DotShape::Filled),
            Attention::Closed => (t.text_tertiary, DotShape::Filled),
        }
    }
}

pub(crate) struct AttentionIndex<'a> {
    sessions: HashMap<&'a str, (&'a Session, AgentState)>,
    ask_seeds: HashSet<&'a str>,
    ask_sessions: HashSet<&'a str>,
    queue_mode: bool,
    crew_in_queue: bool,
}

impl<'a> AttentionIndex<'a> {
    pub(crate) fn of(facts: &'a Facts) -> Self {
        let sessions = facts
            .sessions
            .iter()
            .map(|s| (s.id.as_str(), (s, AgentState::parse(&s.state))))
            .collect();
        let mut ask_seeds = HashSet::new();
        let mut ask_sessions = HashSet::new();
        for ask in facts.asks.iter().filter(|a| a.reply.is_none()) {
            if let Some(seed_id) = ask.seed_id.as_deref().filter(|id| !id.is_empty()) {
                ask_seeds.insert(seed_id);
            }
            if !ask.session_id.is_empty() {
                ask_sessions.insert(ask.session_id.as_str());
            }
        }
        Self {
            sessions,
            ask_seeds,
            ask_sessions,
            queue_mode: crate::setting::on(&facts.settings, queue::QUEUE_MODE),
            crew_in_queue: crate::setting::on(&facts.settings, queue::QUEUE_CREW),
        }
    }

    fn live_in_map(&self, seed: &Seed) -> Option<(&'a Session, AgentState)> {
        let tender = seed.tender.as_ref()?;
        let id = tender
            .session_id
            .as_deref()
            .filter(|s| !s.is_empty())
            .or_else(|| tender.r#ref.strip_prefix("session:"))?;
        self.sessions.get(id).copied()
    }

    fn own_attention(&self, seed: &Seed) -> Attention {
        if model::is_closed(seed) {
            return Attention::Closed;
        }
        let live = self.live_in_map(seed);
        if let Some((session, agent_state)) = live {
            if agent_state == AgentState::PendingApproval {
                return Attention::Approval;
            }
            let in_queue = self.queue_mode && queue::participates(session, self.crew_in_queue);
            if self.ask_seeds.contains(seed.id.as_str())
                || self.ask_sessions.contains(session.id.as_str())
                || session.turn_owed == Some(true)
                || (!in_queue && agent_state == AgentState::WaitingInput)
            {
                return Attention::Waiting;
            }
        } else if self.ask_seeds.contains(seed.id.as_str()) {
            return Attention::Waiting;
        }
        if seed.standby.as_ref().is_some_and(|s| {
            s.resume_blocked
                .as_deref()
                .is_some_and(|r| !r.trim().is_empty())
        }) {
            return Attention::Waiting;
        }
        if let Some((_, agent_state)) = live {
            return match agent_state {
                AgentState::Working | AgentState::Launching => Attention::Working,
                _ => Attention::Live,
            };
        }
        match seed.status.as_str() {
            "growing" => {
                if seed.claimed {
                    Attention::Live
                } else if seed.standby.is_some() {
                    Attention::Parked
                } else {
                    Attention::Quiet
                }
            }
            "parked" => Attention::Parked,
            _ => Attention::Ready,
        }
    }

    pub(crate) fn compute(&self, index: &Index) -> HashMap<String, Attention> {
        let mut out = HashMap::new();
        for seed in index.by_id.values() {
            if is_hidden_closed(seed) {
                continue;
            }
            self.compute_into(seed, index, &mut out);
        }
        out
    }

    fn compute_into(
        &self,
        seed: &Seed,
        index: &Index,
        out: &mut HashMap<String, Attention>,
    ) -> Attention {
        if is_hidden_closed(seed) {
            return Attention::Closed;
        }
        if let Some(&att) = out.get(&seed.id) {
            return att;
        }
        let own = self.own_attention(seed);
        out.insert(seed.id.clone(), own);
        let children = index.children_of(&seed.id);
        if children.is_empty() {
            return own;
        }
        let mut best = own;
        for child in children {
            if is_hidden_closed(child) {
                continue;
            }
            let child_att = self.compute_into(child, index, out);
            if child_att.is_now() && child_att < best {
                best = child_att;
            }
        }
        if best != own {
            out.insert(seed.id.clone(), best);
        }
        best
    }

    pub(crate) fn attention(&self, seed: &Seed, index: &Index) -> Attention {
        if index.children_of(&seed.id).is_empty() {
            return self.own_attention(seed);
        }
        self.compute_into(seed, index, &mut HashMap::new())
    }
}

#[derive(Default)]
pub(crate) struct BedCounts {
    pub(crate) all_open: usize,
    pub(crate) all_waiting: bool,
    pub(crate) unassigned_open: usize,
    pub(crate) unassigned_waiting: bool,
    pub(crate) automations_open: usize,
    pub(crate) automations_waiting: bool,
    pub(crate) has_automations: bool,
    pub(crate) open_by_bed: HashMap<String, usize>,
    pub(crate) waiting_by_bed: HashSet<String>,
}

impl BedCounts {
    pub(crate) fn of(roots: &[Arc<Seed>], attention: &HashMap<String, Attention>) -> BedCounts {
        let mut counts = BedCounts::default();
        for seed in roots {
            let automation = is_automation(seed);
            counts.has_automations |= automation;
            if is_hidden_closed(seed) {
                continue;
            }
            let waiting = attention
                .get(&seed.id)
                .copied()
                .unwrap_or(Attention::Ready)
                .is_waiting();
            if automation {
                counts.automations_open += 1;
                counts.automations_waiting |= waiting;
                continue;
            }
            counts.all_open += 1;
            counts.all_waiting |= waiting;
            match seed.bed_id.as_deref().filter(|id| !id.is_empty()) {
                Some(bed_id) => {
                    *counts.open_by_bed.entry(bed_id.to_owned()).or_default() += 1;
                    if waiting {
                        counts.waiting_by_bed.insert(bed_id.to_owned());
                    }
                }
                None => {
                    counts.unassigned_open += 1;
                    counts.unassigned_waiting |= waiting;
                }
            }
        }
        counts
    }

    pub(crate) fn open_for(&self, filter: &BedFilter) -> usize {
        match filter {
            BedFilter::All => self.all_open,
            BedFilter::Unassigned => self.unassigned_open,
            BedFilter::Automations => self.automations_open,
            BedFilter::Bed(id) => self.open_by_bed.get(id).copied().unwrap_or(0),
        }
    }

    pub(crate) fn waiting_for(&self, filter: &BedFilter) -> bool {
        match filter {
            BedFilter::All => self.all_waiting,
            BedFilter::Unassigned => self.unassigned_waiting,
            BedFilter::Automations => self.automations_waiting,
            BedFilter::Bed(id) => self.waiting_by_bed.contains(id),
        }
    }
}

pub(crate) struct Snapshot {
    pub(crate) on: bool,
    pub(crate) bed: BedFilter,
    pub(crate) counts: BedCounts,
    pub(crate) attention: HashMap<String, Attention>,
}

impl Snapshot {
    pub(crate) fn of(state: &State, index: &Index) -> Snapshot {
        let on = enabled(state);
        if !on {
            return Snapshot {
                on: false,
                bed: BedFilter::All,
                counts: BedCounts::default(),
                attention: HashMap::new(),
            };
        }
        let bed = active_bed(state);
        let att_idx = AttentionIndex::of(&state.facts);
        let attention = att_idx.compute(index);
        let counts = BedCounts::of(&index.roots, &attention);
        Snapshot {
            on: true,
            bed,
            counts,
            attention,
        }
    }

    pub(crate) fn attention_of(&self, seed: &Seed) -> Attention {
        if is_hidden_closed(seed) {
            return Attention::Closed;
        }
        self.attention
            .get(&seed.id)
            .copied()
            .unwrap_or(Attention::Ready)
    }
}

pub(crate) fn beds_rail(state: &State, counts: &BedCounts, t: &Tokens) -> El {
    let active = active_bed(state);
    let profile_beds = beds(state);
    let add_btn = icon_button(
        "garden.beds.add",
        "Add bed",
        &attn_ui::icon::PLUS,
        IconSize::Small,
    )
    .size(Size::Compact)
    .on_press_tx(|state: &mut State, tx: &mut Tx| open_new_bed(state, None, tx));
    let head = GroupHead::new("garden.beds.head", "Beds")
        .section("Beds")
        .heading()
        .trailing(add_btn.build(t).into())
        .build(t);

    let mut rows = ListRow::column()
        .key("garden.beds.list")
        .role("list")
        .label("Beds");
    rows = rows.child(bed_rail_row(
        "garden.bed.all",
        "All",
        counts.all_open,
        false,
        active == BedFilter::All,
        None,
        BedFilter::All,
        t,
    ));
    for bed in profile_beds {
        let count = counts.open_by_bed.get(&bed.id).copied().unwrap_or(0);
        let waiting = counts.waiting_by_bed.contains(&bed.id);
        let bed_clone = bed.clone();
        let edit_btn = icon_button(
            format!("garden.bed.{}.edit", bed.id),
            format!("Edit {}", bed.name),
            &attn_ui::icon::PENCIL,
            IconSize::Small,
        )
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            open_edit_bed(state, bed_clone.clone(), tx)
        });
        rows = rows.child(bed_rail_row(
            &format!("garden.bed.{}", bed.id),
            &bed.name,
            count,
            waiting,
            active == BedFilter::Bed(bed.id.clone()),
            Some(edit_btn),
            BedFilter::Bed(bed.id.clone()),
            t,
        ));
    }
    if counts.unassigned_open > 0 || active == BedFilter::Unassigned {
        rows = rows.child(bed_rail_row(
            "garden.bed.unassigned",
            "Unassigned",
            counts.unassigned_open,
            counts.unassigned_waiting,
            active == BedFilter::Unassigned,
            None,
            BedFilter::Unassigned,
            t,
        ));
    }

    let mut rail = Stack::loose().w_full().child(head).child(rows);
    if counts.has_automations || active == BedFilter::Automations {
        let foot = ListRow::column()
            .key("garden.beds.automations")
            .border_sides([1.0, 0.0, 0.0, 0.0], t.border_subtle)
            .child(bed_rail_row(
                "garden.bed.automations",
                "Automations",
                counts.automations_open,
                counts.automations_waiting,
                active == BedFilter::Automations,
                None,
                BedFilter::Automations,
                t,
            ));
        rail = rail.child(foot);
    }
    Pane::new("garden.beds", rail)
        .width(BED_RAIL_WIDTH)
        .build(t)
}

#[allow(clippy::too_many_arguments)]
fn bed_rail_row(
    key: &str,
    name: &str,
    count: usize,
    waiting: bool,
    active: bool,
    edit: Option<attn_ui::system::IconButton>,
    filter: BedFilter,
    t: &Tokens,
) -> El {
    let mut item = ListRow::new(key, name)
        .quiet_title(name.to_owned())
        .in_view(active)
        .aside(words(count.to_string(), Type::Mono, t.text_muted));
    if waiting {
        item = item
            .lead(dot(t.state_waiting, DotShape::Filled, DotSize::Small).a11y_hidden())
            .description("Waiting on you");
    }
    if let Some(edit) = edit {
        item = item.hover_controls([edit]);
    }
    item.build(t)
        .a11y_value(if active { "selected" } else { "" })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| select_bed(state, filter.clone(), tx))
}

pub(crate) fn dock_bed_chips(state: &State, counts: &BedCounts, t: &Tokens) -> El {
    let active = active_bed(state);
    let profile_beds = beds(state);
    let mut chips = Inline::tight()
        .key("garden.dock.beds.chips")
        .role("group")
        .label("Beds");
    chips = chips.child(bed_chip(
        "garden.dock.bed.all",
        "All",
        active == BedFilter::All,
        false,
        BedFilter::All,
        t,
    ));
    for bed in profile_beds {
        let waiting = counts.waiting_by_bed.contains(&bed.id);
        chips = chips.child(bed_chip(
            &format!("garden.dock.bed.{}", bed.id),
            &bed.name,
            active == BedFilter::Bed(bed.id.clone()),
            waiting,
            BedFilter::Bed(bed.id.clone()),
            t,
        ));
    }
    if counts.unassigned_open > 0 || active == BedFilter::Unassigned {
        chips = chips.child(bed_chip(
            "garden.dock.bed.unassigned",
            "Unassigned",
            active == BedFilter::Unassigned,
            counts.unassigned_waiting,
            BedFilter::Unassigned,
            t,
        ));
    }
    if counts.has_automations || active == BedFilter::Automations {
        chips = chips.child(bed_chip(
            "garden.dock.bed.automations",
            "Auto",
            active == BedFilter::Automations,
            counts.automations_waiting,
            BedFilter::Automations,
            t,
        ));
    }
    let add = icon_button(
        "garden.beds.add",
        "Add bed",
        &attn_ui::icon::PLUS,
        IconSize::Small,
    )
    .size(Size::Compact)
    .on_press_tx(|state: &mut State, tx: &mut Tx| open_new_bed(state, None, tx))
    .build(t);
    Bar::new("garden.dock.beds").lead(chips).push(add).build(t)
}

fn bed_chip(
    key: &str,
    name: &str,
    active: bool,
    waiting: bool,
    filter: BedFilter,
    t: &Tokens,
) -> El {
    let mut btn = Button::new(name.to_owned())
        .look(Look::Quiet)
        .size(Size::Compact)
        .chosen(active)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| select_bed(state, filter.clone(), tx));
    if waiting && !active {
        btn = btn
            .leading(dot(t.state_waiting, DotShape::Filled, DotSize::Small).a11y_hidden())
            .help("Waiting on you");
    }
    let el: El = btn.build(t).key(key).label(name.to_owned()).into();
    if waiting && !active {
        el.description("Waiting on you")
    } else {
        el
    }
}

pub(crate) fn lens_switch(state: &State, has_waiting: bool, t: &Tokens) -> El {
    let active = state.window.garden.lens;
    let choice = |lens: Lens| {
        let mut seg = Segment::new(format!("garden.lens.{}", lens.name()), lens.label())
            .chosen(active == lens)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| select_lens(state, lens, tx));
        if lens == Lens::Now && has_waiting {
            seg = seg
                .trailing(dot(t.state_waiting, DotShape::Filled, DotSize::Small).a11y_hidden())
                .help("Waiting on you");
        }
        seg
    };
    Segmented::new("garden.lens", "Garden lens")
        .option(choice(Lens::Now))
        .option(choice(Lens::Triage))
        .option(choice(Lens::All))
        .build(t)
        .into()
}

pub(crate) fn dock_lens_button(state: &State, t: &Tokens) -> El {
    let on_now = state.window.garden.lens == Lens::Now;
    let next = if on_now { Lens::All } else { Lens::Now };
    let label = if on_now { "Now" } else { "All" };
    Button::new(label)
        .look(Look::Quiet)
        .size(Size::Compact)
        .chosen(on_now)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| select_lens(state, next, tx))
        .build(t)
        .key("garden.dock.lens")
        .into()
}

pub(crate) struct BedForm {
    id: Option<String>,
    name: TextEdit,
    note: TextEdit,
    repos: TextEdit,
    return_focus: Option<String>,
    error: Option<String>,
    saving: bool,
}

pub(crate) fn open_new_bed(state: &mut State, return_focus: Option<String>, tx: &mut Tx) {
    state.window.garden.bed_picker = None;
    state.window.garden.bed_form = Some(BedForm {
        id: None,
        name: TextEdit::default(),
        note: TextEdit::default(),
        repos: TextEdit::default(),
        return_focus,
        error: None,
        saving: false,
    });
    tx.focus(FORM_NAME, true);
}

pub(crate) fn open_edit_bed(state: &mut State, bed: GardenBed, tx: &mut Tx) {
    state.window.garden.bed_picker = None;
    let mut name = TextEdit::new(bed.name);
    name.anchor = 0;
    state.window.garden.bed_form = Some(BedForm {
        id: Some(bed.id),
        name,
        note: TextEdit::new(bed.note.unwrap_or_default()),
        repos: TextEdit::new(bed.repos.join("\n")),
        return_focus: None,
        error: None,
        saving: false,
    });
    tx.focus(FORM_NAME, true);
}

pub(crate) fn close_bed_form(state: &mut State, tx: &mut Tx) {
    if state
        .window
        .garden
        .bed_form
        .as_ref()
        .is_some_and(|f| f.saving)
    {
        return;
    }
    let back = state
        .window
        .garden
        .bed_form
        .take()
        .and_then(|f| f.return_focus);
    restore_focus(state, back, tx);
}

fn restore_focus(state: &State, back: Option<String>, tx: &mut Tx) {
    match back {
        Some(key) => tx.focus(key, false),
        None if state.window.tile.is_none() => super::focus_frame(state, tx),
        None => {}
    }
}

fn submit_bed_form(state: &mut State, tx: &mut Tx) {
    let Some(form) = state.window.garden.bed_form.as_mut() else {
        return;
    };
    if form.saving {
        return;
    }
    let name = form.name.text.trim().to_owned();
    if name.is_empty() {
        form.error = Some("Give this bed a name.".to_owned());
        tx.focus(FORM_NAME, true);
        return;
    }
    let id = form.id.clone();
    let note_raw = form.note.text.trim().to_owned();
    let note = (!note_raw.is_empty()).then_some(note_raw);
    let repos = form
        .repos
        .text
        .lines()
        .map(str::trim)
        .filter(|repo| !repo.is_empty())
        .map(str::to_owned)
        .collect();
    form.saving = true;
    form.error = None;
    state.ask(
        |request_id| GardenBedSaveMessage {
            id,
            name,
            note,
            repos,
            request_id,
            ..GardenBedSaveMessage::new()
        },
        |state, answer, tx| {
            let outcome = answer.outcome().and_then(|reply| {
                reply
                    .parse::<GardenBedSaveResultMessage>()
                    .and_then(|r| r.bed)
                    .ok_or_else(|| "Could not save bed.".to_owned())
            });
            match outcome {
                Ok(_) => {
                    let back = state
                        .window
                        .garden
                        .bed_form
                        .take()
                        .and_then(|f| f.return_focus);
                    restore_focus(state, back, tx);
                }
                Err(reason) => {
                    if let Some(form) = state.window.garden.bed_form.as_mut() {
                        form.saving = false;
                        form.error = Some(reason);
                    }
                    tx.focus(FORM_NAME, true);
                }
            }
        },
        tx,
    );
}

fn delete_bed_form(state: &mut State, tx: &mut Tx) {
    let Some(form) = state.window.garden.bed_form.as_mut() else {
        return;
    };
    if form.saving {
        return;
    }
    let Some(id) = form.id.clone() else {
        return;
    };
    form.saving = true;
    form.error = None;
    let deleted_id = id.clone();
    state.ask(
        |request_id| GardenBedDeleteMessage {
            id,
            request_id,
            ..GardenBedDeleteMessage::new()
        },
        move |state, answer, tx| {
            let outcome = answer.outcome().and_then(|reply| {
                reply
                    .parse::<GardenBedDeleteResultMessage>()
                    .map(|_| ())
                    .ok_or_else(|| "Could not remove bed.".to_owned())
            });
            match outcome {
                Ok(()) => {
                    let back = state
                        .window
                        .garden
                        .bed_form
                        .take()
                        .and_then(|f| f.return_focus);
                    if state.window.garden.bed == BedFilter::Bed(deleted_id.clone()) {
                        state.window.garden.bed = BedFilter::All;
                    }
                    restore_focus(state, back, tx);
                }
                Err(reason) => {
                    if let Some(form) = state.window.garden.bed_form.as_mut() {
                        form.saving = false;
                        form.error = Some(reason);
                    }
                    tx.focus(FORM_NAME, true);
                }
            }
        },
        tx,
    );
}

fn form_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    match ev.key {
        Key::Enter => {
            submit_bed_form(state, tx);
            true
        }
        _ => false,
    }
}

fn bed_form_dialog(form: &BedForm, t: &Tokens, layers: &mut Vec<El>) {
    let editing = form.id.is_some();
    let title = if editing { "Edit bed" } else { "New bed" };
    let name_state = if form.saving {
        FieldState::Disabled
    } else if form.error.is_some() && form.name.text.trim().is_empty() {
        FieldState::Failed
    } else {
        FieldState::Ready
    };
    let field_state = if form.saving {
        FieldState::Disabled
    } else {
        FieldState::Ready
    };
    let name_field = Field::new("garden.bed-form.name-field", "Name")
        .control(line_box(
            TextInput::new(&form.name)
                .key(FORM_NAME)
                .label("Bed name")
                .placeholder("Payments")
                .on_edit(|state: &mut State| {
                    &mut state
                        .window
                        .garden
                        .bed_form
                        .as_mut()
                        .expect("bed form open while editing name")
                        .name
                })
                .on_key(form_key),
            name_state,
            t,
        ))
        .build(t);
    let note_field = Field::new("garden.bed-form.note-field", "Note (optional)")
        .control(line_box(
            TextInput::new(&form.note)
                .key(FORM_NOTE)
                .label("Bed note")
                .placeholder("Short description of what belongs here")
                .on_edit(|state: &mut State| {
                    &mut state
                        .window
                        .garden
                        .bed_form
                        .as_mut()
                        .expect("bed form open while editing note")
                        .note
                })
                .on_key(form_key),
            field_state,
            t,
        ))
        .build(t);
    let repos_field = Field::new("garden.bed-form.repos-field", "Repositories (optional)")
        .hint("Enter one repository path or name per line.")
        .control(area_box(
            TextArea::new(&form.repos)
                .key(FORM_REPOS)
                .rows(2)
                .resizable()
                .label("Bed repositories")
                .placeholder("payments-api\n~/code/billing")
                .on_edit(|state: &mut State| {
                    &mut state
                        .window
                        .garden
                        .bed_form
                        .as_mut()
                        .expect("bed form open while editing repos")
                        .repos
                }),
            field_state,
            t,
        ))
        .build(t);
    let refused = form.error.as_deref().map(|why| {
        Block::custom(
            "garden.bed-form.why",
            Refusal::new("garden.bed-form.error", why.to_owned()).build(t),
        )
    });
    let mut actions = Actions::new("garden.bed-form.actions");
    if editing {
        actions = actions.push(
            Button::new("Remove")
                .look(Look::Danger)
                .disabled(form.saving)
                .on_press_tx(delete_bed_form)
                .build(t)
                .key("garden.bed-form.delete"),
        );
    }
    actions = actions
        .then_end()
        .push(
            Button::new("Cancel")
                .disabled(form.saving)
                .on_press_tx(close_bed_form)
                .build(t)
                .key("garden.bed-form.cancel"),
        )
        .push(
            Button::new("Save")
                .look(Look::Primary)
                .disabled(form.saving)
                .on_press_tx(submit_bed_form)
                .build(t)
                .key("garden.bed-form.save"),
        );
    Dialog::new("garden.bed-form", title)
        .surface_keys(
            "garden.bed-form-layer",
            "garden.bed-form",
            "garden.bed-form-scrim",
        )
        .on_close_tx(close_bed_form)
        .push(name_field)
        .push(note_field)
        .push(repos_field)
        .push_all(refused)
        .actions(actions)
        .build(t, layers);
}

pub(crate) struct BedPicker {
    pub(crate) seed_id: String,
    pub(crate) seed_title: String,
    pub(crate) current_bed_id: Option<String>,
    pub(crate) return_focus: Option<String>,
    pub(crate) error: Option<String>,
    pub(crate) busy: bool,
}

pub(crate) fn open_bed_picker(
    state: &mut State,
    seed_id: &str,
    return_focus: Option<String>,
    tx: &mut Tx,
) {
    if !enabled(state) {
        return;
    }
    let index = Index::of(super::seeds(state));
    let root_id = root_id(seed_id, &index);
    let root = index.by_id.get(root_id).map(Arc::as_ref).or_else(|| {
        super::reading(&state.window.garden, seed_id)
            .0
            .map(|d| &d.seed)
    });
    let Some(root) = root else {
        return;
    };
    if is_automation(root) {
        return;
    }
    let (id, title, current_bed_id) = (
        root.id.clone(),
        root.title.clone(),
        root.bed_id.clone().filter(|id| !id.is_empty()),
    );
    state.window.garden.bed_form = None;
    state.window.garden.bed_picker = Some(BedPicker {
        seed_id: id,
        seed_title: title,
        current_bed_id,
        return_focus,
        error: None,
        busy: false,
    });
    if let Some(first_focus) = picker_keys(state).into_iter().next() {
        tx.focus(first_focus, true);
    }
}

pub(crate) fn close_bed_picker(state: &mut State, tx: &mut Tx) {
    if state
        .window
        .garden
        .bed_picker
        .as_ref()
        .is_some_and(|p| p.busy)
    {
        return;
    }
    let back = state
        .window
        .garden
        .bed_picker
        .take()
        .and_then(|p| p.return_focus);
    restore_focus(state, back, tx);
}

fn assign_bed(state: &mut State, bed_id: Option<String>, tx: &mut Tx) {
    let Some(picker) = state.window.garden.bed_picker.as_mut() else {
        return;
    };
    if picker.busy {
        return;
    }
    let seed_id = picker.seed_id.clone();
    picker.busy = true;
    picker.error = None;
    state.ask(
        |request_id| SeedBedSetMessage {
            seed_id,
            bed_id,
            request_id,
            ..SeedBedSetMessage::new()
        },
        |state, answer, tx| {
            let outcome = answer.outcome().and_then(|reply| {
                reply
                    .parse::<SeedBedSetResultMessage>()
                    .map(|_| ())
                    .ok_or_else(|| "Could not assign bed.".to_owned())
            });
            match outcome {
                Ok(()) => {
                    if let Some(picker) = state.window.garden.bed_picker.as_mut() {
                        picker.busy = false;
                    }
                    close_bed_picker(state, tx);
                }
                Err(reason) => {
                    if let Some(picker) = state.window.garden.bed_picker.as_mut() {
                        picker.busy = false;
                        picker.error = Some(reason);
                    }
                }
            }
        },
        tx,
    );
}

fn picker_keys(state: &State) -> Vec<String> {
    let mut keys: Vec<String> = beds(state)
        .iter()
        .map(|b| format!("{PICKER_PREFIX}{}", b.id))
        .collect();
    if state
        .window
        .garden
        .bed_picker
        .as_ref()
        .is_some_and(|p| p.current_bed_id.is_some())
    {
        keys.push(PICKER_CLEAR.to_owned());
    }
    keys.push(PICKER_NEW.to_owned());
    keys
}

fn picker_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    match ev.key {
        Key::Enter => {
            let Some(focused) = tx.focused().map(str::to_owned) else {
                return false;
            };
            if let Some(bed_id) = focused.strip_prefix(PICKER_PREFIX) {
                assign_bed(state, Some(bed_id.to_owned()), tx);
                true
            } else if focused == PICKER_CLEAR {
                assign_bed(state, None, tx);
                true
            } else if focused == PICKER_NEW {
                let back = state
                    .window
                    .garden
                    .bed_picker
                    .as_ref()
                    .and_then(|p| p.return_focus.clone());
                open_new_bed(state, back, tx);
                true
            } else {
                false
            }
        }
        Key::Down | Key::Up => {
            let keys = picker_keys(state);
            if keys.is_empty() {
                return false;
            }
            let at = tx.focused().and_then(|f| keys.iter().position(|k| k == f));
            let next = match (at, ev.key == Key::Down) {
                (None, true) => 0,
                (None, false) => keys.len() - 1,
                (Some(at), true) => (at + 1).min(keys.len() - 1),
                (Some(at), false) => at.saturating_sub(1),
            };
            tx.focus(&keys[next], true);
            true
        }
        _ => false,
    }
}

fn bed_picker_popover(state: &State, picker: &BedPicker, t: &Tokens) -> El {
    let profile_beds = beds(state);
    let mut list = ListRow::column()
        .key("garden.bed-picker.list")
        .role("listbox")
        .label("Assign bed");
    for bed in profile_beds {
        let chosen = picker.current_bed_id.as_deref() == Some(bed.id.as_str());
        let id = bed.id.clone();
        let mut row = ListRow::new(format!("{PICKER_PREFIX}{}", bed.id), bed.name.clone())
            .title(bed.name.clone())
            .chosen(chosen)
            .checked(chosen)
            .disabled(picker.busy)
            .role("option")
            .on(t.bg_elevated);
        if let Some(note) = bed.note.as_deref().filter(|n| !n.is_empty()) {
            row = row.subtitle(note.to_owned(), t);
        }
        list = list.child(
            row.build(t)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    assign_bed(state, Some(id.clone()), tx)
                }),
        );
    }
    if picker.current_bed_id.is_some() {
        list = list.child(
            ListRow::new(PICKER_CLEAR, "Unassigned")
                .quiet_title("Unassigned")
                .disabled(picker.busy)
                .role("option")
                .on(t.bg_elevated)
                .build(t)
                .on_press_tx(|state: &mut State, tx: &mut Tx| assign_bed(state, None, tx)),
        );
    }
    list = list.child(
        ListRow::new(PICKER_NEW, "New bed…")
            .quiet_title("+ New bed…")
            .disabled(picker.busy)
            .role("option")
            .on(t.bg_elevated)
            .build(t)
            .on_press_tx(|state: &mut State, tx: &mut Tx| {
                let back = state
                    .window
                    .garden
                    .bed_picker
                    .as_ref()
                    .and_then(|p| p.return_focus.clone());
                open_new_bed(state, back, tx)
            }),
    );
    let refused = picker.error.as_deref().map(|why| {
        Block::custom(
            "garden.bed-picker.why",
            Refusal::new("garden.bed-picker.error", why.to_owned()).build(t),
        )
    });
    let about = Block::custom(
        "garden.bed-picker.seed",
        words(picker.seed_title.clone(), Type::Small, t.text_tertiary)
            .single_line()
            .truncate(),
    );
    let body = Popover::new("garden.bed-picker", "Assign bed")
        .about(about)
        .push(Block::custom("garden.bed-picker.options", list))
        .push_all(refused)
        .build(t)
        .on_key(picker_key)
        .modal_tx(true, close_bed_picker);
    over_window(body)
}

pub(crate) fn overlay(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    if !enabled(state) {
        return;
    }
    if let Some(form) = &state.window.garden.bed_form {
        bed_form_dialog(form, t, layers);
    } else if let Some(picker) = &state.window.garden.bed_picker {
        layers.push(bed_picker_popover(state, picker, t));
    }
}

pub(crate) fn pulse_strip(
    key: String,
    pulse: Option<&str>,
    attention: Attention,
    live_session: Option<(String, String, AgentState)>,
    close_garden: bool,
    t: &Tokens,
) -> Option<El> {
    let pulse_text = pulse.map(str::trim).filter(|p| !p.is_empty());
    if pulse_text.is_none() && live_session.is_none() {
        return None;
    }
    let (dot_color, dot_shape) = attention.dot_look(t);
    let line_text = match (pulse_text, &live_session) {
        (Some(p), _) => p.to_owned(),
        (None, Some((_, name, state))) => format!("{name} · {}", state.spoken()),
        (None, None) => return None,
    };
    let mut strip = Inline::joined().w_full().justify_between().child(
        Inline::joined()
            .grow(1.0)
            .min_w(0.0)
            .child(dot(dot_color, dot_shape, DotSize::Small).a11y_hidden())
            .child(
                words(line_text, Type::Small, t.text_primary)
                    .single_line()
                    .truncate()
                    .grow(1.0)
                    .min_w(0.0),
            ),
    );
    if let Some((session_id, session_name, _)) = live_session {
        let go_key = format!("{key}.session");
        let label = format!("Go to {session_name}");
        let btn: El = Button::new(session_name)
            .look(Look::Plain)
            .size(Size::Compact)
            .words_at_most(180.0)
            .trailing(words("→", Type::Control, t.text_secondary).a11y_hidden())
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                if close_garden {
                    super::close(state);
                }
                crate::sidebar::open_agent(state, &session_id, tx);
                if state.window.tile.is_some() {
                    crate::frame::raise_main();
                }
            })
            .build(t)
            .key(go_key)
            .label(label)
            .into();
        strip = strip.child(btn);
    }
    Some(
        Card::new(key)
            .standing(attn_ui::blocks::Standing::Resting)
            .push(strip)
            .build(t),
    )
}
