//! Settings → Models: the tier each harness's models run at, as attn ships it or as the user
//! sets it, and the overrides for models no harness reports any more.

use attn_protocol::{Harness, HarnessModel};
use attn_ui::Tokens;
use attn_ui::blocks::{Actions, Block, Facts, Inline, List, Notice, Prose, Section, Status};
use attn_ui::system::{ListRow, Reveal, Segment, Segmented, Size, Type, words};
use rune::{El, Key, KeyEvent, Tx};

use super::autosave::{self, Codec};
use super::catalogs::{self, TIER_OVERRIDES};
use super::harnesses;
use attn_ui::system::Button;

use super::route::{TierInfo, TierOverride, tier_label, tier_overrides};
use crate::State;

const TIERS: [&str; 3] = ["light", "standard", "deep"];

fn stored(entries: &[TierOverride]) -> String {
    serde_json::to_string(entries).expect("tier overrides serialize")
}

const OVERRIDES_CODEC: Codec = Codec {
    actual: |settings, key| stored(&tier_overrides(settings.get(key).map(String::as_str))),
    serialize: |_, draft| Ok(stored(&tier_overrides(Some(draft)))),
};

/// The page's field, with how it reads and saves.
pub(super) fn fields() -> Vec<(String, Codec)> {
    vec![(TIER_OVERRIDES.into(), OVERRIDES_CODEC)]
}

/// Every listed harness, whose catalogs the page shows.
pub(super) fn wanted(state: &State) -> Vec<(String, bool)> {
    harnesses::listed(state)
        .into_iter()
        .map(|h| (h.id, h.options.discovery))
        .collect()
}

fn entries(state: &State) -> Vec<TierOverride> {
    tier_overrides(Some(state.window.settings.autosave.draft(TIER_OVERRIDES)))
}

fn same(entry: &TierOverride, harness: &str, model: &str) -> bool {
    entry.harness == harness && entry.model == model
}

/// Gives `model` the tier `tier`, or attn's shipped tier when there is none or it is the same.
fn set_tier(state: &mut State, model: &HarnessModel, tier: Option<&str>, tx: &mut Tx) {
    let mut next: Vec<TierOverride> = entries(state)
        .into_iter()
        .filter(|e| !same(e, &model.harness, &model.id))
        .collect();
    if let Some(tier) = tier
        && model.shipped_tier.as_deref() != Some(tier)
    {
        next.push(TierOverride {
            harness: model.harness.clone(),
            model: model.id.clone(),
            tier: tier.to_owned(),
        });
    }
    autosave::apply(state, TIER_OVERRIDES, &stored(&next), tx);
}

fn remove(state: &mut State, entry: &TierOverride, tx: &mut Tx) {
    let next: Vec<TierOverride> = entries(state)
        .into_iter()
        .filter(|e| !same(e, &entry.harness, &entry.model))
        .collect();
    autosave::apply(state, TIER_OVERRIDES, &stored(&next), tx);
}

fn joined(provider: &str, model: &str) -> String {
    if provider.is_empty() || model.starts_with(&format!("{provider}/")) {
        model.to_owned()
    } else {
        format!("{provider} / {model}")
    }
}

fn model_row(model: &HarnessModel, entries: &[TierOverride], t: &Tokens) -> El {
    let own = entries.iter().find(|e| same(e, &model.harness, &model.id));
    let alias = model.tier_source == "alias";
    let selected = if alias {
        None
    } else {
        own.map(|e| e.tier.clone())
            .filter(|tier| !tier.is_empty())
            .or_else(|| model.shipped_tier.clone())
    };
    let shown = if model.name.is_empty() {
        &model.id
    } else {
        &model.name
    };
    let name = joined(&model.provider, shown);
    let key = format!("settings.models.{}.model.{}", model.harness, model.id);
    let mark = TierInfo::mark(
        Some(&TierInfo {
            tier: selected.clone(),
            source: model.tier_source.clone(),
        }),
        true,
    );
    let mut detail = if alias {
        "The harness decides this alias’s model.".to_owned()
    } else {
        format!(
            "{}attn: {}",
            if own.is_some() { "Your tier · " } else { "" },
            model.shipped_tier.as_deref().unwrap_or("no tier")
        )
    };
    if model.name != model.id {
        detail.push_str(&format!(" · {}", model.id));
    }
    let tiers = (!alias).then(|| {
        let reset = model.clone();
        El::from(
            Segmented::new(format!("{key}.tiers"), format!("Tier for {name}"))
                .options(TIERS.into_iter().map(|tier| {
                    let model = model.clone();
                    Segment::new(format!("{key}.tier.{tier}"), tier_label(tier))
                        .chosen(selected.as_deref() == Some(tier))
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            set_tier(state, &model, Some(tier), tx)
                        })
                }))
                .build(t),
        )
        .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
            if matches!(ev.key, Key::Delete | Key::Backspace) {
                set_tier(state, &reset, None, tx);
                return true;
            }
            false
        })
    });
    let reset = own.is_some().then(|| {
        let model = model.clone();
        quiet(
            format!("{key}.reset"),
            &format!("Reset tier for {name}"),
            "Reset",
            t,
            move |state, tx| set_tier(state, &model, None, tx),
        )
    });
    let line = Inline::tight()
        .children((!mark.is_empty()).then(|| {
            words(mark.clone(), Type::Small, t.warning)
                .key(format!("{key}.tier"))
                .label(mark)
        }))
        .child(
            words(detail.clone(), Type::Small, t.text_tertiary)
                .key(format!("{key}.detail"))
                .label(detail)
                .wrap(),
        );
    item(&key, &name, t)
        .subtitle_el(line)
        .controls(tiers.into_iter().chain(reset), Reveal::Always)
        .build(t)
}

/// A model's line in its harness's list: no card, a
/// hairline under it.
/// A still, wrapping row of a models list, its name keyed `{key}.name`.
fn item(key: &str, name: &str, t: &Tokens) -> ListRow {
    ListRow::new(key.to_owned(), name.to_owned())
        .still()
        .wrap()
        .title_el(
            words(name.to_owned(), Type::Body, t.text_primary)
                .key(format!("{key}.name"))
                .label(name.to_owned())
                .wrap_anywhere()
                .w_full()
                .min_w(0.0),
        )
}

fn orphan_row(entry: &TierOverride, t: &Tokens) -> El {
    let name = &entry.model;
    let key = format!("settings.models.{}.orphan.{name}", entry.harness);
    let removed = entry.clone();
    let detail = format!("Your tier: {}", entry.tier);
    item(&key, name, t)
        .subtitle_el(
            Inline::tight().child(
                words(detail.clone(), Type::Small, t.text_tertiary)
                    .key(format!("{key}.detail"))
                    .label(detail)
                    .wrap(),
            ),
        )
        .controls(
            [quiet(
                format!("{key}.remove"),
                "Remove",
                "Remove",
                t,
                move |state, tx| remove(state, &removed, tx),
            )],
            Reveal::Always,
        )
        .build(t)
}

fn default_name(
    harness: &Harness,
    found: &catalogs::Discovered,
    models: &[HarnessModel],
    tier: &str,
) -> String {
    if found.loading {
        return "Updating…".into();
    }
    let id = found.catalog.and_then(|c| c.tier_default(tier));
    models
        .iter()
        .find(|m| {
            Some(m.id.as_str()) == id
                && m.tier.as_deref() == Some(tier)
                && m.access != "unsupported"
        })
        .map(|m| {
            if m.name.is_empty() {
                m.id.clone()
            } else {
                m.name.clone()
            }
        })
        .unwrap_or_else(|| format!("none found, {} decides", harness.name))
}

fn harness_block(state: &State, harness: &Harness, entries: &[TierOverride], t: &Tokens) -> Block {
    let found = catalogs::of(state, &harness.id, harness.options.discovery);
    let models: &[HarnessModel] = found.catalog.map_or(&[], |c| c.models.as_slice());
    let key = format!("settings.models.{}", harness.id);
    let mut section = Section::new(key.clone(), harness.name.clone())
        .region(format!("{} models", harness.name))
        .push(
            Facts::new(format!("{key}.defaults"))
                .fact(
                    format!("{key}.light-default"),
                    "Light default",
                    default_name(harness, &found, models, "light"),
                )
                .fact(
                    format!("{key}.deep-default"),
                    "Deep default",
                    default_name(harness, &found, models, "deep"),
                )
                .build(t),
        );
    if harness.options.discovery {
        let id = harness.id.clone();
        section = section.push(
            Actions::new(format!("{key}.actions"))
                .push(quiet(
                    format!("{key}.refresh"),
                    "Refresh",
                    "Refresh",
                    t,
                    move |state, tx| {
                        catalogs::discover(state, &id, true, tx);
                    },
                ))
                .build(),
        );
    }
    if let Some(error) = found.error {
        section = section.push(Notice::warn(format!("{key}.error"), error.to_owned()).build(t));
    }
    if !harness.options.discovery {
        section = section.push(
            Prose::hint(
                format!("{key}.no-catalog"),
                "This harness does not report a model catalog.",
            )
            .build(t),
        );
    }
    if found.loading && found.catalog.is_none() {
        section = section
            .push(Status::new(format!("{key}.discovering"), "Discovering models…", true).build(t));
    }
    if !models.is_empty() {
        section = section.push(
            List::new(format!("{key}.models"), format!("{} models", harness.name))
                .rows(models.iter().map(|m| model_row(m, entries, t)))
                .build(t),
        );
    }
    let shows_orphans = !harness.options.discovery
        || found.catalog.is_some()
        || found.error.is_some()
        || !harness.available();
    let orphans: Vec<&TierOverride> = entries
        .iter()
        .filter(|e| {
            e.harness == harness.id && shows_orphans && !models.iter().any(|m| m.id == e.model)
        })
        .collect();
    if !orphans.is_empty() {
        let title = if found.error.is_some() || !harness.available() {
            "Saved overrides"
        } else {
            "No longer reported"
        };
        section = section.push(
            Section::new(format!("{key}.orphans"), title)
                .push(
                    List::new(format!("{key}.orphans.list"), title)
                        .rows(orphans.into_iter().map(|e| orphan_row(e, t)))
                        .build(t),
                )
                .build(t),
        );
    }
    section.build(t)
}

fn missing_harnesses(listed: &[Harness], entries: &[TierOverride], t: &Tokens) -> Option<Block> {
    let missing: Vec<&TierOverride> = entries
        .iter()
        .filter(|e| !listed.iter().any(|h| h.id == e.harness))
        .collect();
    if missing.is_empty() {
        return None;
    }
    let rows: Vec<El> = missing
        .into_iter()
        .map(|entry| {
            let said = format!("{} / {} · {}", entry.harness, entry.model, entry.tier);
            let key = format!("settings.models.missing.{}/{}", entry.harness, entry.model);
            let removed = entry.clone();
            item(&key, &said, t)
                .controls(
                    [quiet(
                        format!("{key}.remove"),
                        "Remove",
                        "Remove",
                        t,
                        move |state, tx| remove(state, &removed, tx),
                    )],
                    Reveal::Always,
                )
                .build(t)
        })
        .collect();
    Some(
        Section::new(
            "settings.block.models-missing",
            "Harnesses no longer reported",
        )
        .push(
            List::new("settings.models.missing", "Harnesses no longer reported")
                .rows(rows)
                .build(t),
        )
        .build(t),
    )
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    let listed = harnesses::listed(state);
    let entries = entries(state);
    let mut out = Vec::new();
    if !state.facts.harnesses_loaded {
        out.push(Status::new("settings.models.loading", "Loading harnesses…", true).build(t));
    }
    out.extend(
        listed
            .iter()
            .map(|h| harness_block(state, h, &entries, t))
            .chain(missing_harnesses(&listed, &entries, t)),
    );
    out
}

/// A quiet model action, which outside Delegation draws as any other action.
fn quiet(
    key: String,
    label: &str,
    shown: &'static str,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    Button::new(shown)
        .size(Size::Compact)
        .on_press_tx(press)
        .build(t)
        .key(key)
        .label(label)
        .into()
}
