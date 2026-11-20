//! Model pricing overrides: which model Codex auto-review is billed as, the saved price of each
//! model the built-in table misses, and the form that adds one.

use attn_ui::Tokens;
use attn_ui::blocks::{Block, Disclosure, Field, Inline, List, Notice, Prose, Section};
use attn_ui::system::{
    Button, FieldState, ListRow, Look, Reveal, Size, Trigger, TriggerLook, Type, line_box,
    number_box, words,
};
use rune::{El, TextEdit, Tx};
use rune_kit::{Select, SelectAppearance, TextInput};

use super::super::autosave::{self, Codec, PLAIN};
use super::super::controls::refusal_beside;
use crate::State;

const PRICE_PREFIX: &str = "session_cost.price.";
const BILLED_AS: &str = "session_cost.billed_as.codex-auto-review";
const AUTO_REVIEW_DEFAULT: &str = "gpt-6-luna";
const BILLING_MODELS: &[&str] = &[
    "gpt-6-luna",
    "gpt-6-sol",
    "gpt-6-astra",
    "gpt-5.6-luna",
    "gpt-5.6-terra",
    "gpt-5.6-sol",
    "gpt-5.5",
    "gpt-5.4-mini",
    "gpt-5-codex",
];
const DEFAULT_SUFFIX: &str = " (default)";
const BILLED_AS_SELECT: &str = "settings.price.billed-as";

const RATES: [(&str, &str); 5] = [
    ("input_usd_per_mtok", "Input"),
    ("output_usd_per_mtok", "Output"),
    ("cache_read_usd_per_mtok", "Cache read"),
    ("cache_write_5m_usd_per_mtok", "Cache write · 5m"),
    ("cache_write_1h_usd_per_mtok", "Cache write · 1h"),
];

const NEW: &str = "settings.price-new.";
const NEW_MODEL: &str = "settings.price-new.model";

#[derive(Default)]
pub(crate) struct Pricing {
    open: bool,
    billed_as_open: bool,
    cards: std::collections::BTreeMap<String, Card>,
    new: New,
}

/// A saved override's rate fields, and the draft they last showed.
#[derive(Default)]
struct Card {
    rates: [TextEdit; 5],
    mirrored: Option<String>,
}

#[derive(Default)]
struct New {
    model: TextEdit,
    rates: [TextEdit; 5],
    /// The override sent and the value it was sent with, until it lands.
    adding: Option<(String, String)>,
}

type Rates = [f64; 5];

fn parse_card(raw: &str) -> Option<Rates> {
    let value: serde_json::Map<String, serde_json::Value> = serde_json::from_str(raw).ok()?;
    if value.keys().any(|k| !RATES.iter().any(|(r, _)| r == k)) {
        return None;
    }
    let mut card = [0.0; 5];
    for (i, (key, _)) in RATES.iter().enumerate() {
        let rate = value.get(*key)?.as_f64()?;
        if !rate.is_finite() || rate < 0.0 {
            return None;
        }
        card[i] = rate;
    }
    Some(card)
}

fn card_from(typed: &[String]) -> Option<Rates> {
    let mut card = [0.0; 5];
    for (i, rate) in typed.iter().enumerate().take(5) {
        let rate: f64 = rate.trim().parse().ok()?;
        if !rate.is_finite() || rate < 0.0 {
            return None;
        }
        card[i] = rate;
    }
    (typed.len() == 5).then_some(card)
}

/// The override as the daemon stores it, its numbers written as JavaScript writes them.
fn stored(card: &Rates) -> String {
    let fields: Vec<String> = RATES
        .iter()
        .zip(card)
        .map(|((key, _), rate)| format!("\"{key}\":{rate}"))
        .collect();
    format!("{{{}}}", fields.join(","))
}

fn draft_of(card: Option<Rates>) -> Vec<String> {
    match card {
        Some(card) => card.iter().map(f64::to_string).collect(),
        None => vec![String::new(); 5],
    }
}

fn encode(typed: &[String]) -> String {
    serde_json::to_string(typed).expect("rates serialize")
}

fn decode(draft: &str) -> Vec<String> {
    serde_json::from_str(draft).unwrap_or_else(|_| vec![String::new(); 5])
}

const PRICE_CODEC: Codec = Codec {
    actual: |settings, key| encode(&draft_of(settings.get(key).and_then(|raw| parse_card(raw)))),
    serialize: |key, draft| {
        if draft.is_empty() {
            return Ok(String::new());
        }
        let model = key.strip_prefix(PRICE_PREFIX).unwrap_or(key);
        card_from(&decode(draft)).map(|card| stored(&card)).ok_or_else(|| {
            format!(
                "Complete every rate for {model} with a non-negative number; use 0 for unused rates."
            )
        })
    },
};

/// The saved overrides, by model id.
fn overrides(state: &State) -> Vec<(String, String)> {
    state
        .facts
        .settings
        .iter()
        .filter_map(|(key, raw)| {
            let model = key.strip_prefix(PRICE_PREFIX)?;
            (!raw.trim().is_empty() && !model.trim().is_empty())
                .then(|| (model.to_owned(), raw.clone()))
        })
        .collect()
}

fn price_key(model: &str) -> String {
    format!("{PRICE_PREFIX}{model}")
}

pub(super) fn fields(state: &State) -> Vec<(String, Codec)> {
    let mut fields = vec![(BILLED_AS.to_owned(), PLAIN)];
    fields.extend(
        overrides(state)
            .into_iter()
            .map(|(model, _)| (price_key(&model), PRICE_CODEC)),
    );
    if let Some((key, _)) = &state.window.settings.agents.pricing.new.adding {
        fields.push((key.clone(), PRICE_CODEC));
    }
    fields
}

/// Shows each override's draft in its rate fields when it changed under them, and clears the
/// new override's form once the override landed.
pub(super) fn follow(state: &mut State) -> bool {
    let mut changed = false;
    for (model, _) in overrides(state) {
        let draft = state
            .window
            .settings
            .autosave
            .draft(&price_key(&model))
            .to_owned();
        let card = state
            .window
            .settings
            .agents
            .pricing
            .cards
            .entry(model)
            .or_default();
        if card.mirrored.as_deref() == Some(draft.as_str()) {
            continue;
        }
        let typed = decode(&draft);
        for (edit, rate) in card.rates.iter_mut().zip(typed) {
            *edit = TextEdit::new(rate);
        }
        card.mirrored = Some(draft);
        changed = true;
    }
    let new = &state.window.settings.agents.pricing.new;
    if let Some((key, value)) = &new.adding {
        let refused = state
            .window
            .settings
            .autosave
            .field(key)
            .is_some_and(|f| f.error.is_some());
        if !refused && state.facts.settings.get(key) == Some(value) {
            state.window.settings.agents.pricing.new = New::default();
            changed = true;
        }
    }
    changed
}

fn typed(new: &New) -> Vec<String> {
    new.rates.iter().map(|r| r.text.clone()).collect()
}

/// Adds the override the form holds once its model id and every rate are filled.
fn add(state: &mut State, tx: &mut Tx) {
    let new = &state.window.settings.agents.pricing.new;
    let model = new.model.text.trim().to_owned();
    let Some(card) = card_from(&typed(new)) else {
        return;
    };
    let key = price_key(&model);
    let exists = overrides(state).iter().any(|(m, _)| *m == model);
    let in_flight = new
        .adding
        .as_ref()
        .is_some_and(|(adding, _)| state.window.settings.autosave.sending(adding));
    if model.is_empty() || exists || in_flight {
        return;
    }
    let value = stored(&card);
    state.window.settings.agents.pricing.new.adding = Some((key.clone(), value));
    let settings = state.facts.settings.clone();
    let drafts = &mut state.window.settings.autosave;
    drafts.ensure(&key, &settings, PRICE_CODEC);
    *drafts.draft_mut(&key) = TextEdit::new(encode(&draft_of(Some(card))));
    autosave::commit(state, &key, tx);
}

/// One rate's field, `input` named for its rate under `label` and saving as its form does.
fn rate_field(key: String, label: &str, input: TextInput, t: &Tokens) -> El {
    let named = input.label(format!("{label} · USD/MTok")).placeholder("0");
    Field::new(key, label.to_owned())
        .control(number_box(named, FieldState::Ready, t))
        .el(t)
}

/// The five rates of one price, side by side, onto further lines when the room runs out.
fn rates(key: String, fields: impl Iterator<Item = El>) -> Block {
    Block::custom(key, Inline::fields().children(fields))
}

fn card_typed(state: &mut State, model: &str) {
    let pricing = &mut state.window.settings.agents.pricing;
    let card = pricing.cards.entry(model.to_owned()).or_default();
    let draft = encode(
        &card
            .rates
            .iter()
            .map(|r| r.text.clone())
            .collect::<Vec<_>>(),
    );
    card.mirrored = Some(draft.clone());
    *state.window.settings.autosave.draft_mut(&price_key(model)) = TextEdit::new(draft);
}

fn existing(state: &State, model: &str, raw: &str, t: &Tokens) -> (ListRow, Vec<Block>) {
    let key = price_key(model);
    let empty = Card::default();
    let card = state
        .window
        .settings
        .agents
        .pricing
        .cards
        .get(model)
        .unwrap_or(&empty);
    let fields = RATES.iter().enumerate().map(|(i, (rate, label))| {
        let (edit, change, submit, left) =
            (model.to_owned(), model.to_owned(), key.clone(), key.clone());
        let input_key = super::super::part_field_key(&key, rate);
        let input = TextInput::new(&card.rates[i])
            .key(input_key.clone())
            .on_edit(move |state: &mut State| {
                &mut state
                    .window
                    .settings
                    .agents
                    .pricing
                    .cards
                    .entry(edit.clone())
                    .or_default()
                    .rates[i]
            })
            .on_change(move |state: &mut State, _: &mut Tx| card_typed(state, &change))
            .on_submit(move |state: &mut State, tx: &mut Tx| autosave::commit(state, &submit, tx))
            .on_blur(move |state: &mut State, tx: &mut Tx| {
                autosave::commit_on_leave(state, &left, tx)
            });
        rate_field(input_key, label, input, t)
    });
    let remove = key.clone();
    let refusal = refusal_beside(
        state,
        &key,
        super::super::part_field_key(&key, RATES[0].0),
        t,
    );
    let row = ListRow::new(format!("settings.price.{model}"), model.to_owned())
        .still()
        .wrap()
        .title_el(
            words(model.to_owned(), Type::Mono, t.text_primary)
                .wrap_anywhere()
                .w_full(),
        )
        .controls(
            [Button::new("Remove")
                .look(Look::Danger)
                .size(Size::Compact)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    autosave::apply(state, &remove, "", tx)
                })
                .build(t)
                .key(format!("settings.price.{model}.remove"))
                .into()],
            Reveal::Always,
        );
    let mut under = Vec::new();
    if parse_card(raw).is_none() {
        under.push(
            Notice::warn(
                format!("settings.price.{model}.invalid"),
                "This saved override is invalid. Complete every rate to repair it, or remove it.",
            )
            .build(t),
        );
    }
    under.extend(
        refusal.map(|refusal| Block::custom(format!("settings.price.{model}.refused"), refusal)),
    );
    under.push(rates(format!("settings.price.{model}.rates"), fields));
    (row, under)
}

fn billed_as(state: &State) -> String {
    let draft = state.window.settings.autosave.draft(BILLED_AS).trim();
    if draft.is_empty() {
        AUTO_REVIEW_DEFAULT.to_owned()
    } else {
        draft.to_owned()
    }
}

fn billed_as_options(selected: &str) -> Vec<String> {
    let mut options: Vec<String> = Vec::new();
    if !BILLING_MODELS.contains(&selected) {
        options.push(selected.to_owned());
    }
    options.extend(BILLING_MODELS.iter().map(|m| {
        if *m == AUTO_REVIEW_DEFAULT {
            format!("{m}{DEFAULT_SUFFIX}")
        } else {
            (*m).to_owned()
        }
    }));
    options
}

fn billed_as_shown(selected: &str, options: &[String]) -> Option<String> {
    options
        .iter()
        .find(|o| o.strip_suffix(DEFAULT_SUFFIX).unwrap_or(o) == selected)
        .cloned()
}

fn billed_as_select(state: &State) -> Select {
    let selected = billed_as(state);
    let options = billed_as_options(&selected);
    let current = billed_as_shown(&selected, &options);
    Select::new(BILLED_AS_SELECT, &options)
        .appearance(SelectAppearance::Field)
        .label(BILLED_AS_LABEL)
        .current(current.as_deref())
        .open(state.window.settings.agents.pricing.billed_as_open)
        .on_toggle(toggle_billed_as)
        .on_close(|state: &mut State| state.window.settings.agents.pricing.billed_as_open = false)
        .on_choose(|state: &mut State, option: &str, tx: &mut Tx| {
            state.window.settings.agents.pricing.billed_as_open = false;
            let model = option.strip_suffix(DEFAULT_SUFFIX).unwrap_or(option);
            let value = if model == AUTO_REVIEW_DEFAULT {
                ""
            } else {
                model
            };
            autosave::apply(state, BILLED_AS, value, tx);
        })
}

fn toggle_billed_as(state: &mut State) {
    let pricing = &mut state.window.settings.agents.pricing;
    pricing.billed_as_open = !pricing.billed_as_open;
}

const BILLED_AS_LABEL: &str = "Codex auto-review billed as";

/// The field that opens the billed-as list, keyed as the list's anchor.
fn billed_as_field(state: &State, t: &Tokens) -> Block {
    let selected = billed_as(state);
    let shown = billed_as_shown(&selected, &billed_as_options(&selected)).unwrap_or(selected);
    let trigger = Trigger::new(
        format!("{BILLED_AS_SELECT}-trigger"),
        BILLED_AS_LABEL,
        words(shown.clone(), Type::Body, t.text_primary).truncate(),
    )
    .look(TriggerLook::Field)
    .value(shown)
    .open(state.window.settings.agents.pricing.billed_as_open)
    .build(t, |state: &mut State, _: &mut Tx| toggle_billed_as(state));
    Field::new(BILLED_AS_SELECT, BILLED_AS_LABEL)
        .control(El::from(trigger).role("combobox"))
        .hint("Codex reports its auto-review reviewer as codex-auto-review and picks the real model on its servers. Guardian usage is priced at this model's rates.")
        .build(t)
}

/// The billed-as list over the page, while it is open.
pub(super) fn overlay(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let pricing = &state.window.settings.agents.pricing;
    if pricing.open && pricing.billed_as_open {
        billed_as_select(state).build(attn_ui::kit(t), layers);
    }
}

fn new_form(state: &State, t: &Tokens) -> Block {
    let new = &state.window.settings.agents.pricing.new;
    let model = new.model.text.trim();
    let card = card_from(&typed(new));
    let exists = !model.is_empty() && overrides(state).iter().any(|(m, _)| m == model);
    let fields = RATES.iter().enumerate().map(|(i, (rate, label))| {
        let input_key = format!("{NEW}{rate}");
        let input = TextInput::new(&new.rates[i])
            .key(input_key.clone())
            .on_edit(move |state: &mut State| {
                &mut state.window.settings.agents.pricing.new.rates[i]
            })
            .on_submit(add)
            .on_blur(add);
        rate_field(input_key, label, input, t)
    });
    let failure = new
        .adding
        .as_ref()
        .and_then(|(key, _)| refusal_beside(state, key, NEW_MODEL.to_owned(), t));
    let model_field = Field::new(NEW_MODEL, "Exact model ID")
        .control(line_box(
            TextInput::new(&new.model)
                .key(NEW_MODEL)
                .label("Exact model ID")
                .placeholder("provider-model-id")
                .on_edit(|state: &mut State| &mut state.window.settings.agents.pricing.new.model)
                .on_submit(add)
                .on_blur(add),
            FieldState::Ready,
            t,
        ))
        .build(t);
    let mut section = Section::new("settings.price-new", "New override")
        .heading(words("New override", Type::Name, t.text_primary))
        .push(model_field)
        .push(rates("settings.price-new.rates".to_owned(), fields));
    if exists {
        section = section.push(
            Notice::warn(
                "settings.price-new.exists",
                "That model already has an override above.",
            )
            .build(t),
        );
    }
    if let Some(failure) = failure {
        section = section.push(Block::custom("settings.price-new.refused", failure));
    }
    if !model.is_empty() && card.is_none() {
        section = section.push(
            Prose::hint(
                "settings.price-new.incomplete",
                "Complete every rate with a non-negative number.",
            )
            .build(t),
        );
    }
    section
        .push(
            Prose::hint(
                "settings.price-new.hint",
                "The override is added automatically when the model ID and every rate are filled. Use 0 for unused rates.",
            )
            .build(t),
        )
        .build(t)
}

pub(super) fn page(state: &State, t: &Tokens) -> Block {
    let mut part = Disclosure::new(
        "settings.pricing",
        "Model pricing overrides",
        state.window.settings.agents.pricing.open,
        |state: &mut State, _: &mut Tx| {
            let pricing = &mut state.window.settings.agents.pricing;
            pricing.open = !pricing.open;
        },
    )
    .body_key("settings.prices")
    .push(
        Prose::hint(
            "settings.prices.lead",
            "Add exact model IDs that are missing from the built-in price table, or correct stale rates. Every field is USD per million tokens; cache reads and writes keep their own rates.",
        )
        .build(t),
    )
    .push(billed_as_field(state, t));
    let saved = overrides(state);
    if !saved.is_empty() {
        part = part.push(
            saved
                .iter()
                .map(|(model, raw)| existing(state, model, raw, t))
                .fold(
                    List::new("settings.prices.saved", "Saved overrides").heading(),
                    |list, (row, under)| list.row_with(row, under),
                )
                .build(t),
        );
    }
    part.push(new_form(state, t)).build(t)
}
