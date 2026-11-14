//! Model pricing overrides: which model Codex auto-review is billed as, the saved price of each
//! model the built-in table misses, and the form that adds one.

use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, TextEdit, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, Select, SelectAppearance, TextInput};

use super::super::autosave::{self, Codec, PLAIN};
use super::super::pages::{CAPTION, S4, refusal_beside};
use super::hint;
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
    /// The form field the focus was in at the last frame.
    focus: Option<String>,
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

/// Adds the new override when the focus leaves its form.
pub(super) fn poll(state: &mut State, tx: &mut Tx) {
    let focused = tx.focused().filter(|k| k.starts_with(NEW));
    let new = &mut state.window.settings.agents.pricing.new;
    if new.focus.as_deref() == focused {
        return;
    }
    let left = new.focus.is_some();
    new.focus = focused.map(str::to_owned);
    if left {
        add(state, tx);
    }
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

fn rate_input(
    key: String,
    label: &str,
    edit: &TextEdit,
    t: &Tokens,
    on_edit: impl for<'a> Fn(&'a mut State) -> &'a mut TextEdit + 'static,
    on_change: impl Fn(&mut State, &mut Tx) + 'static,
    on_submit: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    let name = format!("{label} · USD/MTok");
    col()
        .gap(space::S1)
        .child(text(name.clone()).font(CAPTION).color(t.text_secondary))
        .child(
            TextInput::new(edit)
                .key(key)
                .label(name)
                .placeholder("0")
                .on_edit(on_edit)
                .on_change(on_change)
                .on_submit(on_submit)
                .width(Some(110.0))
                .build(attn_ui::kit(t)),
        )
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

fn existing(state: &State, model: &str, raw: &str, t: &Tokens) -> El {
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
    let rates = RATES.iter().enumerate().map(|(i, (rate, label))| {
        let (edit, change, submit) = (model.to_owned(), model.to_owned(), key.clone());
        rate_input(
            super::super::part_field_key(&key, rate),
            label,
            &card.rates[i],
            t,
            move |state: &mut State| {
                &mut state
                    .window
                    .settings
                    .agents
                    .pricing
                    .cards
                    .entry(edit.clone())
                    .or_default()
                    .rates[i]
            },
            move |state: &mut State, _: &mut Tx| card_typed(state, &change),
            move |state: &mut State, tx: &mut Tx| autosave::commit(state, &submit, tx),
        )
    });
    let remove = key.clone();
    let refusal = refusal_beside(
        state,
        &key,
        super::super::part_field_key(&key, RATES[0].0),
        t,
    );
    col()
        .key(format!("settings.price.{model}"))
        .role("group")
        .label(model.to_owned())
        .w_full()
        .gap(space::S2)
        .padding(space::S3)
        .radius(6.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .child(
            row()
                .w_full()
                .items_center()
                .child(
                    text(model.to_owned())
                        .font(type_::MONO_CAPTION)
                        .color(t.text_primary)
                        .grow(1.0),
                )
                .child(
                    Button::new("Remove")
                        .variant(ButtonVariant::Danger)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            autosave::apply(state, &remove, "", tx)
                        })
                        .build(attn_ui::kit(t))
                        .key(format!("settings.price.{model}.remove")),
                ),
        )
        .children(parse_card(raw).is_none().then(|| {
            super::warning(
                &format!("settings.price.{model}.invalid"),
                "This saved override is invalid. Complete every rate to repair it, or remove it.",
                t,
            )
        }))
        .child(row().gap(space::S2).wrap().children(rates))
        .children(refusal)
}

fn billed_as_select(state: &State) -> Select {
    let draft = state.window.settings.autosave.draft(BILLED_AS).trim();
    let selected = if draft.is_empty() {
        AUTO_REVIEW_DEFAULT
    } else {
        draft
    };
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
    let current = options
        .iter()
        .find(|o| o.strip_suffix(DEFAULT_SUFFIX).unwrap_or(o) == selected)
        .cloned();
    Select::new(BILLED_AS_SELECT, &options)
        .appearance(SelectAppearance::Field)
        .label("Codex auto-review billed as")
        .current(current.as_deref())
        .open(state.window.settings.agents.pricing.billed_as_open)
        .on_toggle(|state: &mut State| {
            let pricing = &mut state.window.settings.agents.pricing;
            pricing.billed_as_open = !pricing.billed_as_open;
        })
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

/// The billed-as list over the page, while it is open.
pub(super) fn overlay(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let pricing = &state.window.settings.agents.pricing;
    if pricing.open && pricing.billed_as_open {
        billed_as_select(state).build(attn_ui::kit(t), layers);
    }
}

fn new_form(state: &State, t: &Tokens) -> El {
    let new = &state.window.settings.agents.pricing.new;
    let model = new.model.text.trim();
    let card = card_from(&typed(new));
    let exists = !model.is_empty() && overrides(state).iter().any(|(m, _)| m == model);
    let rates = RATES.iter().enumerate().map(|(i, (rate, label))| {
        rate_input(
            format!("{NEW}{rate}"),
            label,
            &new.rates[i],
            t,
            move |state: &mut State| &mut state.window.settings.agents.pricing.new.rates[i],
            |_: &mut State, _: &mut Tx| {},
            add,
        )
    });
    let failure = new
        .adding
        .as_ref()
        .and_then(|(key, _)| refusal_beside(state, key, NEW_MODEL.to_owned(), t));
    col()
        .key("settings.price-new")
        .role("group")
        .label("New override")
        .w_full()
        .gap(space::S2)
        .padding(space::S3)
        .radius(6.0)
        .border(1.0, t.border_subtle)
        .child(
            col()
                .gap(space::S1)
                .child(text("Exact model ID").font(CAPTION).color(t.text_secondary))
                .child(
                    TextInput::new(&new.model)
                        .key(NEW_MODEL)
                        .label("Exact model ID")
                        .placeholder("provider-model-id")
                        .on_edit(|state: &mut State| {
                            &mut state.window.settings.agents.pricing.new.model
                        })
                        .on_submit(add)
                        .width(Some(280.0))
                        .build(attn_ui::kit(t)),
                ),
        )
        .child(row().gap(space::S2).wrap().children(rates))
        .children(exists.then(|| {
            super::warning(
                "settings.price-new.exists",
                "That model already has an override above.",
                t,
            )
        }))
        .children(failure)
        .children((!model.is_empty() && card.is_none()).then(|| {
            text("Complete every rate with a non-negative number.")
                .key("settings.price-new.incomplete")
                .label("Complete every rate with a non-negative number.")
                .font(CAPTION)
                .color(t.text_tertiary)
                .wrap()
        }))
        .child(hint(
            "The override is added automatically when the model ID and every rate are filled. Use 0 for unused rates.",
            t,
        ))
}

pub(super) fn page(state: &State, t: &Tokens) -> El {
    let pricing = &state.window.settings.agents.pricing;
    let k = attn_ui::kit(t);
    let mut body = col()
        .key("settings.prices")
        .w_full()
        .gap(space::S3)
        .child(hint(
            "Add exact model IDs that are missing from the built-in price table, or correct stale rates. Every field is USD per million tokens; cache reads and writes keep their own rates.",
            t,
        ));
    if pricing.open {
        body = body
            .child(
                col()
                    .gap(space::S1)
                    .child(
                        text("Codex auto-review billed as")
                            .font(type_::LABEL)
                            .color(t.text_primary),
                    )
                    .child(billed_as_select(state).trigger(k))
                    .child(hint(
                        "Codex reports its auto-review reviewer as codex-auto-review and picks the real model on its servers. Guardian usage is priced at this model's rates.",
                        t,
                    )),
            )
            .children(
                overrides(state)
                    .iter()
                    .map(|(model, raw)| existing(state, model, raw, t)),
            )
            .child(new_form(state, t));
    }
    col()
        .w_full()
        .gap(space::S2)
        .pb(S4)
        .child(super::disclosure(
            "settings.pricing".into(),
            "Model pricing overrides",
            None,
            pricing.open,
            t,
            |state| {
                let pricing = &mut state.window.settings.agents.pricing;
                pricing.open = !pricing.open;
            },
        ))
        .children(pricing.open.then_some(body))
}
