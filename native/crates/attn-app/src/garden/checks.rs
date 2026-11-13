use attn_protocol::{ActorView, Seed, SeedCheck, SeedCheckMessage, SeedSharedCheck};
use attn_ui::GlyphButton;
use rune::tok::{space, type_};
use rune::{El, KeyEvent, Tx, col, row, text, text_input};

use super::model::{self, SessionNames};
use super::sheet::{Sheet, meta_word};
use super::spot::Spot;
use super::view::kit_button;
use crate::State;

fn box_glyph(state: &str) -> &'static str {
    match state {
        "reported" => "?",
        "confirmed" => "✓",
        "rejected" => attn_ui::icon::CLOSE,
        _ => "",
    }
}

fn judgeable(state: &str) -> bool {
    !matches!(state, "confirmed" | "rejected")
}

fn author(by: &ActorView, names: &SessionNames) -> String {
    if by.r#ref == "user" {
        return "you".into();
    }
    model::who_name(&by.r#ref, &by.name, names)
}

fn short_commit(commit: &str) -> &str {
    commit.get(..7).unwrap_or(commit)
}

/// The words a piece of evidence shows: its artifact, or its link without the scheme.
fn evidence_label(evidence: &attn_protocol::SeedCheckEvidence) -> String {
    if let Some(artifact) = &evidence.artifact {
        return artifact.clone();
    }
    let url = evidence.url.clone().unwrap_or_default();
    if let Some(number) = url
        .split_once("github.com/")
        .and_then(|(_, rest)| rest.splitn(3, '/').nth(2))
        .and_then(|rest| rest.strip_prefix("pull/"))
        .map(|rest| rest.split('/').next().unwrap_or(rest))
    {
        return format!("PR {number}");
    }
    let bare = url.split_once("://").map_or(url.as_str(), |(_, rest)| rest);
    bare.trim_start_matches("www.")
        .trim_end_matches('/')
        .to_owned()
}

impl Sheet<'_> {
    /// The seed's checks, each with the moves that judge it, then the checks it shares with
    /// every child, then the removed ones while shown.
    pub(super) fn checks(&self, here: &Seed) -> Option<El> {
        let checks: Vec<&SeedCheck> = here.checks.iter().flatten().collect();
        let shared: Vec<&SeedSharedCheck> = here.shared_checks.iter().flatten().collect();
        if checks.is_empty() && shared.is_empty() {
            return None;
        }
        let t = self.t;
        let mut list = super::sheet::section(&self.key("checks"), "Checks", t);
        if let Some(tally) = model::tally_of(checks.iter().copied()) {
            list = list.child(
                text(model::tally_words(&tally))
                    .key(self.key("checks.tally"))
                    .label(format!("Checks: {}", model::tally_sentence(&tally)))
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            );
        }
        let live = checks.iter().filter(|c| !c.removed);
        list = list.children(live.map(|c| self.check_row(here, c)));
        let live_shared: Vec<_> = shared.iter().filter(|c| !c.removed).collect();
        if !live_shared.is_empty() {
            list = list
                .child(meta_word("Shared by every child", t))
                .children(live_shared.into_iter().map(|c| self.shared_row(c)));
        }
        let removed = checks.iter().filter(|c| c.removed).count()
            + shared.iter().filter(|c| c.removed).count();
        if removed > 0 {
            let fold = self.key(&format!("checks.removed.{}", here.id));
            let open = self.garden.folds.contains(&fold);
            let toggled = fold.clone();
            list = list.child(
                kit_button(format!("{removed} removed"))
                    .expanded(open)
                    .on_press_tx(move |state: &mut State, _: &mut Tx| toggle_fold(state, &toggled))
                    .build(attn_ui::kit(t))
                    .key(self.key("checks.removed")),
            );
            if open {
                list = list
                    .children(
                        checks
                            .iter()
                            .filter(|c| c.removed)
                            .map(|c| self.check_row(here, c)),
                    )
                    .children(
                        shared
                            .iter()
                            .filter(|c| c.removed)
                            .map(|c| self.shared_row(c)),
                    );
            }
        }
        Some(list)
    }

    fn check_row(&self, here: &Seed, check: &SeedCheck) -> El {
        let t = self.t;
        let kit = attn_ui::kit(t);
        let spot = Spot::new(&self.prefix, &here.id, &format!("check.{}", check.id));
        let shown = self.garden.shown(&spot);
        let (busy, rejecting) = (shown.busy, shown.writing);
        let state = check.state.as_str();
        let mark = if check.removed {
            text(box_glyph(state))
                .label(state.to_owned())
                .font(type_::LABEL)
                .color(t.text_secondary)
                .min_w(22.0)
        } else {
            let confirm = judgeable(state);
            let (verb, word) = if confirm {
                ("confirm", "Confirm")
            } else {
                ("uncheck", "Uncheck")
            };
            let pressed = spot.clone();
            GlyphButton::new(box_glyph(state), format!("{word} {}", check.id))
                .help(format!(
                    "{state}: {}",
                    if confirm {
                        "confirm it"
                    } else {
                        "clear the verdict"
                    }
                ))
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    judge(&pressed, state, verb, None, tx)
                })
                .build(kit)
                .key(spot.key("box"))
                .border(1.0, t.border)
        };
        let mut line = row()
            .w_full()
            .gap(space::S2)
            .child(
                text(check.id.clone())
                    .font(type_::MONO_CAPTION)
                    .color(t.text_tertiary),
            )
            .child(
                text(check.text.clone())
                    .font(type_::BODY)
                    .color(t.text_primary)
                    .wrap()
                    .grow(1.0)
                    .min_w(0.0),
            );
        if let Some(from) = &check.from {
            line = line.child(meta_word(format!("from {from}"), t));
        }
        if check.removed {
            line = line.child(meta_word("removed", t));
        }
        let mut main = col().grow(1.0).min_w(0.0).gap(2.0).child(line);
        if let Some(report) = &check.report {
            let mut words = format!("report: {}", report.summary);
            if report.detail == Some(true) {
                words.push_str(" · detail in its note");
            }
            for evidence in &report.evidence {
                words.push_str(&format!(" · {}", evidence_label(evidence)));
                if evidence.changed_since_report == Some(true) {
                    words.push_str(" (changed since this report)");
                }
            }
            if let Some(commit) = &report.commit {
                words.push_str(&format!(" · @{}", short_commit(commit)));
            }
            words.push_str(&format!(" · {}", author(&report.by, self.names)));
            main = main.child(
                text(words.clone())
                    .key(self.key(&format!("check.{}.report", check.id)))
                    .label(words)
                    .font(type_::LABEL)
                    .color(t.text_secondary)
                    .wrap(),
            );
        }
        if let Some(verdict) = &check.verdict {
            let mut words = format!(
                "{} by {}",
                if verdict.confirmed {
                    "confirmed"
                } else {
                    "rejected"
                },
                author(&verdict.by, self.names)
            );
            if let Some(reason) = verdict.reason.as_ref().filter(|r| !r.is_empty()) {
                words.push_str(&format!(": {reason}"));
            }
            if check.report.is_none() {
                words.push_str(" · no report");
            }
            main = main.child(
                text(words.clone())
                    .key(self.key(&format!("check.{}.verdict", check.id)))
                    .label(words)
                    .font(type_::LABEL)
                    .color(if verdict.confirmed {
                        t.success
                    } else {
                        t.error
                    })
                    .wrap(),
            );
        }
        if rejecting {
            let typed = spot.clone();
            main = main.child(
                text_input(&self.garden.words, type_::BODY)
                    .key(spot.field())
                    .single_line()
                    .label(format!("Reject {}: what would prove it", check.id))
                    .placeholder("what would prove it", t.text_muted)
                    .on_edit(|state: &mut State| {
                        let garden = &mut state.window.garden;
                        garden.revision += 1;
                        &mut garden.words
                    })
                    .on_input_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                        let home = typed.key("box");
                        typed.field_key(state, ev, &home, |s, w, tx| reject(&typed, s, w, tx), tx)
                    })
                    .w_full()
                    .color(t.text_primary)
                    .fill(t.bg_input)
                    .border(1.0, t.accent)
                    .radius(4.0)
                    .padding(space::S1),
            );
        }
        if let Some(failure) = shown.failure.map(str::to_owned) {
            main = main.child(
                text(failure.clone())
                    .key(self.key(&format!("check.{}.error", check.id)))
                    .role("alert")
                    .label(failure)
                    .font(type_::LABEL)
                    .color(t.error)
                    .wrap(),
            );
        }
        let mut actions = row().gap(space::S1);
        if !check.removed && !rejecting {
            let action = |word: &str, verb: &'static str, name: String, part: &str| {
                let pressed = spot.clone();
                kit_button(word)
                    .disabled(busy)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| match verb {
                        "reject" => pressed.write(state, "", tx),
                        verb => judge(&pressed, state, verb, None, tx),
                    })
                    .build(kit)
                    .key(self.key(&format!("check.{}.{part}", check.id)))
                    .label(name)
            };
            if state == "rejected" {
                actions = actions.child(action("Uncheck", "uncheck", "Uncheck".into(), "uncheck"));
            }
            if check.report.is_some() {
                actions = actions.child(action(
                    "Reopen",
                    "reopen",
                    format!("Reopen {}", check.id),
                    "reopen",
                ));
            }
            if judgeable(state) {
                actions = actions.child(action(
                    "Reject…",
                    "reject",
                    format!("Reject {}", check.id),
                    "reject-start",
                ));
            }
        }
        row()
            .key(self.key(&format!("check.{}", check.id)))
            .role("listitem")
            .label(format!("{} {} · {}", check.id, check.text, check.state))
            .w_full()
            .gap(space::S2)
            .child(mark)
            .child(main)
            .child(actions)
    }

    fn shared_row(&self, check: &SeedSharedCheck) -> El {
        let t = self.t;
        let kit = attn_ui::kit(t);
        let key = self.key(&format!("shared.{}", check.id));
        let open = self.garden.folds.contains(&key);
        let rollup = model::tally_states(check.seeds.iter().map(|s| s.state.as_str()))
            .map(|tally| model::tally_sentence(&tally))
            .unwrap_or_default();
        let toggled = key.clone();
        let mut item = col().w_full().gap(2.0).child(
            kit_button(format!("{} {} · {rollup}", check.id, check.text))
                .expanded(open)
                .on_press_tx(move |state: &mut State, _: &mut Tx| toggle_fold(state, &toggled))
                .build(kit)
                .key(key.clone()),
        );
        if open {
            let mut seeds = col()
                .key(format!("{key}.seeds"))
                .role("list")
                .label(format!("Seeds that prove {}", check.id))
                .pl(space::S3);
            for entry in &check.seeds {
                let held = self.seeds.iter().find(|s| s.id == entry.seed_id);
                let title = held.map_or(entry.seed_id.as_str(), |s| s.title.as_str());
                let status = held.map_or(entry.seed_id.as_str(), |s| s.status.as_str());
                let (go, id) = (self.go.clone(), entry.seed_id.clone());
                let mark = box_glyph(&entry.state);
                seeds = seeds.child(
                    kit_button(format!("{title} · {status} · {}", entry.state))
                        .label_with({
                            let words = format!("{mark} {title} · {status}");
                            move |font, ink| text(words).font(font).color(ink)
                        })
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| go.to(state, &id, tx))
                        .build(kit)
                        .key(format!("{key}.{}", entry.seed_id)),
                );
            }
            item = item.child(seeds);
        }
        item
    }
}

fn toggle_fold(state: &mut State, key: &str) {
    let garden = &mut state.window.garden;
    if !garden.folds.remove(key) {
        garden.folds.insert(key.to_owned());
    }
    garden.revision += 1;
}

/// Sends `verb` for the check at `spot`, whose keyboard home is its box.
fn judge(spot: &Spot, state: &mut State, verb: &'static str, summary: Option<String>, tx: &mut Tx) {
    let (seed_id, check_id) = (spot.seed().to_owned(), check_of(spot));
    spot.send(
        state,
        |request_id| SeedCheckMessage {
            seed_id,
            check_id: Some(check_id),
            verb: verb.into(),
            summary,
            request_id: Some(request_id),
            ..SeedCheckMessage::new()
        },
        spot.key("box"),
        tx,
    );
}

fn check_of(spot: &Spot) -> String {
    spot.part().trim_start_matches("check.").to_owned()
}

fn reject(spot: &Spot, state: &mut State, reason: String, tx: &mut Tx) {
    if reason.is_empty() {
        let check = check_of(spot);
        spot.fail(
            state,
            format!("rejecting {check} needs a reason: say what would prove it"),
        );
        return;
    }
    judge(spot, state, "reject", Some(reason), tx);
}
