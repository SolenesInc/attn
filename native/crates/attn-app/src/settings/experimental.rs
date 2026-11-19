//! Settings → Experimental: every experimental feature, by group, each with its switch.

use attn_protocol::go_values::{FEATURES, Feature};
use attn_ui::Tokens;
use attn_ui::blocks::{Block, Section, SettingRow};

use super::controls::{feature_note, feature_switch};
use crate::{State, setting};

pub(super) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    let experimental: Vec<&Feature> = FEATURES
        .iter()
        .filter(|f| f.stage == "experimental")
        .collect();
    let mut groups: Vec<&str> = Vec::new();
    for feature in &experimental {
        if !groups.contains(&feature.group) {
            groups.push(feature.group);
        }
    }
    groups
        .into_iter()
        .map(|group| {
            let features: Vec<&Feature> = experimental
                .iter()
                .copied()
                .filter(|f| f.group == group)
                .collect();
            let on = features
                .iter()
                .filter(|f| setting::on(&state.facts.settings, f.key))
                .count();
            Section::new(format!("settings.feature-group.{group}"), group)
                .aside(format!("{on} of {} on", features.len()))
                .push_all(features.into_iter().map(|feature| flag(state, feature, t)))
                .build(t)
        })
        .collect()
}

fn flag(state: &State, feature: &Feature, t: &Tokens) -> Block {
    SettingRow::new(
        feature.name,
        feature.summary,
        feature_switch(state, feature.key, false, t),
    )
    .key(format!("settings.flag.{}", feature.key))
    .note(feature_note(state, feature.key, t))
    .build(t)
}
