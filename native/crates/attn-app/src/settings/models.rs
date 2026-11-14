//! Settings → Models: the tier each harness's models run at, as attn ships it or as the user
//! sets it, and the overrides for models no harness reports any more.

use attn_protocol::{Harness, HarnessModel};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, Tx, col, row, text};
use rune_kit::RadioItem;

use super::autosave::{self, Codec};
use super::catalogs::{self, TIER_OVERRIDES};
use super::harnesses;
use super::pages::{CAPTION, S4, block, button, named_button, warning};
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
        .map(|h| (h.id, h.discovery))
        .collect()
}

fn entries(state: &State) -> Vec<TierOverride> {
    tier_overrides(Some(state.window.settings.autosave.draft(TIER_OVERRIDES)))
}

fn same(entry: &TierOverride, harness: &str, provider: &str, model: &str) -> bool {
    entry.harness == harness && entry.provider == provider && entry.model == model
}

/// Gives `model` the tier `tier`, or attn's shipped tier when there is none or it is the same.
fn set_tier(state: &mut State, model: &HarnessModel, tier: Option<&str>, tx: &mut Tx) {
    let mut next: Vec<TierOverride> = entries(state)
        .into_iter()
        .filter(|e| !same(e, &model.harness, &model.provider, &model.id))
        .collect();
    if let Some(tier) = tier
        && model.shipped_tier.as_deref() != Some(tier)
    {
        next.push(TierOverride {
            harness: model.harness.clone(),
            provider: model.provider.clone(),
            model: model.id.clone(),
            tier: tier.to_owned(),
        });
    }
    autosave::apply(state, TIER_OVERRIDES, &stored(&next), tx);
}

fn remove(state: &mut State, entry: &TierOverride, tx: &mut Tx) {
    let next: Vec<TierOverride> = entries(state)
        .into_iter()
        .filter(|e| !same(e, &entry.harness, &entry.provider, &entry.model))
        .collect();
    autosave::apply(state, TIER_OVERRIDES, &stored(&next), tx);
}

fn joined(provider: &str, model: &str) -> String {
    [provider, model]
        .into_iter()
        .filter(|part| !part.is_empty())
        .collect::<Vec<_>>()
        .join(" / ")
}

fn caption(key: String, said: String, t: &Tokens) -> El {
    text(said.clone())
        .key(key)
        .label(said)
        .font(CAPTION)
        .color(t.text_tertiary)
        .wrap()
}

fn model_row(model: &HarnessModel, entries: &[TierOverride], t: &Tokens) -> El {
    let own = entries
        .iter()
        .find(|e| same(e, &model.harness, &model.provider, &model.id));
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
    let key = format!(
        "settings.models.{}.model.{}",
        model.harness,
        joined(&model.provider, &model.id)
    );
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
    let words = col()
        .grow(1.0)
        .min_w(0.0)
        .gap(space::S1)
        .child(
            row()
                .gap(space::S2)
                .items_center()
                .child(
                    text(name.clone())
                        .key(format!("{key}.name"))
                        .label(name.clone())
                        .font(type_::LABEL)
                        .color(t.text_primary),
                )
                .children((!mark.is_empty()).then(|| {
                    text(mark.clone())
                        .key(format!("{key}.tier"))
                        .label(mark)
                        .font(CAPTION)
                        .color(t.warning)
                })),
        )
        .child(caption(format!("{key}.detail"), detail, t));
    let mut controls = row().gap(S4).items_center();
    if !alias {
        let reset = model.clone();
        let mut group = row()
            .key(format!("{key}.tiers"))
            .role("radiogroup")
            .label(format!("Tier for {name}"))
            .focus_group()
            .gap(space::S3)
            .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                if matches!(ev.key, Key::Delete | Key::Backspace) {
                    set_tier(state, &reset, None, tx);
                    return true;
                }
                false
            });
        for (i, tier) in TIERS.into_iter().enumerate() {
            let model = model.clone();
            group = group.child(
                RadioItem::new(tier_label(tier), selected.as_deref() == Some(tier))
                    .on_select_tx(move |state: &mut State, tx: &mut Tx| {
                        set_tier(state, &model, Some(tier), tx)
                    })
                    .build(attn_ui::kit(t))
                    .key(format!("{key}.tier.{tier}"))
                    .position_in_set(i + 1, TIERS.len()),
            );
        }
        controls = controls.child(group);
    }
    if own.is_some() {
        let model = model.clone();
        controls = controls.child(named_button(
            format!("{key}.reset"),
            &format!("Reset tier for {name}"),
            "Reset",
            t,
            move |state, tx| set_tier(state, &model, None, tx),
        ));
    }
    item(key, &name, t).child(words).child(controls)
}

fn item(key: String, name: &str, t: &Tokens) -> El {
    row()
        .key(key)
        .role("group")
        .label(name.to_owned())
        .w_full()
        .gap(S4)
        .items_center()
        .padding(space::S3)
        .radius(6.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
}

fn orphan_row(entry: &TierOverride, t: &Tokens) -> El {
    let name = joined(&entry.provider, &entry.model);
    let key = format!("settings.models.{}.orphan.{name}", entry.harness);
    let removed = entry.clone();
    item(key.clone(), &name, t)
        .child(
            col()
                .grow(1.0)
                .min_w(0.0)
                .gap(space::S1)
                .child(
                    text(name.clone())
                        .key(format!("{key}.name"))
                        .label(name)
                        .font(type_::LABEL)
                        .color(t.text_primary),
                )
                .child(caption(
                    format!("{key}.detail"),
                    format!("Your tier: {}", entry.tier),
                    t,
                )),
        )
        .child(button(
            format!("{key}.remove"),
            "Remove",
            t,
            move |state, tx| remove(state, &removed, tx),
        ))
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

fn harness_block(state: &State, harness: &Harness, entries: &[TierOverride], t: &Tokens) -> El {
    let found = catalogs::of(state, &harness.id, harness.discovery);
    let models: &[HarnessModel] = found.catalog.map_or(&[], |c| c.models.as_slice());
    let key = format!("settings.models.{}", harness.id);
    let mut heading = row().gap(space::S2).items_center().child(
        text(harness.name.clone())
            .key(format!("{key}.heading"))
            .role("heading")
            .label(harness.name.clone())
            .font(type_::SECTION)
            .color(t.text_primary),
    );
    if harness.discovery {
        let id = harness.id.clone();
        heading = heading.child(button(
            format!("{key}.refresh"),
            "Refresh",
            t,
            move |state, tx| {
                catalogs::discover(state, &id, true, tx);
            },
        ));
    }
    let defaults = row()
        .gap(S4)
        .child(caption(
            format!("{key}.light-default"),
            format!(
                "Light default → {}",
                default_name(harness, &found, models, "light")
            ),
            t,
        ))
        .child(caption(
            format!("{key}.deep-default"),
            format!(
                "Deep default → {}",
                default_name(harness, &found, models, "deep")
            ),
            t,
        ));
    let mut body = vec![heading, defaults];
    if let Some(error) = found.error {
        body.push(warning(format!("{key}.error"), error.to_owned(), t));
    }
    if !harness.discovery {
        body.push(caption(
            format!("{key}.no-catalog"),
            "This harness does not report a model catalog.".into(),
            t,
        ));
    }
    if found.loading && found.catalog.is_none() {
        body.push(
            caption(
                format!("{key}.discovering"),
                "Discovering models…".into(),
                t,
            )
            .role("status"),
        );
    }
    body.extend(models.iter().map(|m| model_row(m, entries, t)));
    let shows_orphans = !harness.discovery
        || found.catalog.is_some()
        || found.error.is_some()
        || !harness.available;
    let orphans: Vec<&TierOverride> = entries
        .iter()
        .filter(|e| {
            e.harness == harness.id
                && shows_orphans
                && !models
                    .iter()
                    .any(|m| m.id == e.model && m.provider == e.provider)
        })
        .collect();
    if !orphans.is_empty() {
        let title = if found.error.is_some() || !harness.available {
            "Saved overrides"
        } else {
            "No longer reported"
        };
        body.push(
            text(title)
                .key(format!("{key}.orphans"))
                .role("heading")
                .label(title)
                .font(type_::LABEL)
                .color(t.text_primary),
        );
        body.extend(orphans.into_iter().map(|e| orphan_row(e, t)));
    }
    col()
        .key(key)
        .role("region")
        .label(format!("{} models", harness.name))
        .w_full()
        .gap(space::S2)
        .pb(S4)
        .children(body)
}

fn missing_harnesses(listed: &[Harness], entries: &[TierOverride], t: &Tokens) -> Option<El> {
    let missing: Vec<&TierOverride> = entries
        .iter()
        .filter(|e| !listed.iter().any(|h| h.id == e.harness))
        .collect();
    if missing.is_empty() {
        return None;
    }
    let rows = missing
        .into_iter()
        .map(|entry| {
            let said = format!(
                "{} / {} · {}",
                entry.harness,
                joined(&entry.provider, &entry.model),
                entry.tier
            );
            let key = format!(
                "settings.models.missing.{}/{}",
                entry.harness,
                joined(&entry.provider, &entry.model)
            );
            let removed = entry.clone();
            item(key.clone(), &said, t)
                .child(
                    text(said.clone())
                        .key(format!("{key}.name"))
                        .label(said)
                        .font(type_::LABEL)
                        .color(t.text_primary)
                        .grow(1.0),
                )
                .child(button(
                    format!("{key}.remove"),
                    "Remove",
                    t,
                    move |state, tx| remove(state, &removed, tx),
                ))
        })
        .collect();
    Some(block(
        "models-missing",
        "Harnesses no longer reported",
        "",
        t,
        rows,
    ))
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<El> {
    let listed = harnesses::listed(state);
    let entries = entries(state);
    let mut out = Vec::new();
    out.extend(harnesses::failure_row(state, "models", t));
    if harnesses::first_loading(state) {
        out.push(
            caption(
                "settings.models.loading".into(),
                "Loading harnesses…".into(),
                t,
            )
            .role("status"),
        );
    }
    out.extend(listed.iter().map(|h| harness_block(state, h, &entries, t)));
    out.extend(missing_harnesses(&listed, &entries, t));
    out
}
