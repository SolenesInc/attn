use attn_protocol::{
    ActorView, Seed, SeedCheck, SeedCheckMessage, SeedCheckResultMessage, SeedSharedCheck,
};
use attn_ui::GlyphButton;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, Tx, col, row, text, text_input};

use super::model::{self, SessionNames};
use super::sheet::{Sheet, meta_word};
use super::view::kit_button;
use crate::State;

/// A check move under way on one seed page, or the reason a reject is being written.
pub(crate) struct Judging {
    at: String,
    seed: String,
    check: String,
    rejecting: bool,
    busy: bool,
    failure: Option<String>,
}

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

fn box_key(at: &str, check: &str) -> String {
    format!("{at}.check.{check}.box")
}

fn reject_key(at: &str, check: &str) -> String {
    format!("{at}.check.{check}.reject")
}

impl Sheet<'_> {
    fn judging(&self, seed: &str, check: &str) -> Option<&Judging> {
        self.garden
            .judging
            .as_ref()
            .filter(|j| j.at == self.prefix && j.seed == seed && j.check == check)
    }

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
        let at = self.prefix.clone();
        let judging = self.judging(&here.id, &check.id);
        let busy = judging.is_some_and(|j| j.busy);
        let rejecting = judging.is_some_and(|j| j.rejecting);
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
            let mover = Move::new(&at, &here.id, &check.id);
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
                    mover.send(state, verb, None, tx)
                })
                .build(kit)
                .key(box_key(&at, &check.id))
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
            let mover = Move::new(&at, &here.id, &check.id);
            main = main.child(
                text_input(&self.garden.judge_words, type_::BODY)
                    .key(reject_key(&at, &check.id))
                    .single_line()
                    .label(format!("Reject {}: what would prove it", check.id))
                    .placeholder("what would prove it", t.text_muted)
                    .on_edit(|state: &mut State| {
                        let garden = &mut state.window.garden;
                        garden.revision += 1;
                        &mut garden.judge_words
                    })
                    .on_input_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                        mover.reject_key(state, ev, tx)
                    })
                    .w_full()
                    .color(t.text_primary)
                    .fill(t.bg_input)
                    .border(1.0, t.accent)
                    .radius(4.0)
                    .padding(space::S1),
            );
        }
        if let Some(failure) = judging.and_then(|j| j.failure.clone()) {
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
                let mover = Move::new(&at, &here.id, &check.id);
                kit_button(word)
                    .disabled(busy)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| match verb {
                        "reject" => mover.begin_reject(state, tx),
                        verb => mover.send(state, verb, None, tx),
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

/// One check on one seed page, and the moves that judge it.
#[derive(Clone)]
struct Move {
    at: String,
    seed: String,
    check: String,
}

impl Move {
    fn new(at: &str, seed: &str, check: &str) -> Move {
        Move {
            at: at.to_owned(),
            seed: seed.to_owned(),
            check: check.to_owned(),
        }
    }

    fn busy(state: &State) -> bool {
        state.window.garden.judging.as_ref().is_some_and(|j| j.busy)
    }

    fn begin_reject(&self, state: &mut State, tx: &mut Tx) {
        if Move::busy(state) {
            return;
        }
        let garden = &mut state.window.garden;
        garden.judging = Some(Judging {
            at: self.at.clone(),
            seed: self.seed.clone(),
            check: self.check.clone(),
            rejecting: true,
            busy: false,
            failure: None,
        });
        garden.judge_words = Default::default();
        garden.revision += 1;
        tx.focus(reject_key(&self.at, &self.check), true);
    }

    fn reject_key(&self, state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
        match ev.key {
            Key::Enter => {
                let reason = state.window.garden.judge_words.text.trim().to_owned();
                if reason.is_empty() {
                    if let Some(judging) = state.window.garden.judging.as_mut() {
                        judging.failure = Some(format!(
                            "rejecting {} needs a reason: say what would prove it",
                            self.check
                        ));
                    }
                    state.window.garden.revision += 1;
                } else {
                    self.send(state, "reject", Some(reason), tx);
                }
                true
            }
            Key::Escape => {
                self.close(state, tx);
                true
            }
            _ => false,
        }
    }

    fn close(&self, state: &mut State, tx: &mut Tx) {
        let garden = &mut state.window.garden;
        garden.judging = None;
        garden.revision += 1;
        tx.focus(box_key(&self.at, &self.check), true);
    }

    fn send(&self, state: &mut State, verb: &'static str, summary: Option<String>, tx: &mut Tx) {
        if Move::busy(state) {
            return;
        }
        let rejecting = verb == "reject";
        let garden = &mut state.window.garden;
        garden.judging = Some(Judging {
            at: self.at.clone(),
            seed: self.seed.clone(),
            check: self.check.clone(),
            rejecting,
            busy: true,
            failure: None,
        });
        garden.revision += 1;
        let (seed_id, check_id) = (self.seed.clone(), self.check.clone());
        let moved = self.clone();
        state.ask(
            |request_id| SeedCheckMessage {
                seed_id,
                check_id: Some(check_id),
                verb: verb.into(),
                summary,
                request_id: Some(request_id),
                ..SeedCheckMessage::new()
            },
            move |state, answer, tx| {
                let outcome = answer.outcome().and_then(|reply| {
                    reply
                        .parse::<SeedCheckResultMessage>()
                        .map(|_| ())
                        .ok_or_else(|| "The check did not move".to_owned())
                });
                moved.settle(state, rejecting, outcome, tx);
            },
            tx,
        );
    }

    fn settle(&self, state: &mut State, rejecting: bool, outcome: Result<(), String>, tx: &mut Tx) {
        let garden = &mut state.window.garden;
        garden.revision += 1;
        let ours = garden
            .judging
            .as_ref()
            .is_some_and(|j| j.at == self.at && j.seed == self.seed && j.check == self.check);
        if !ours {
            return;
        }
        match outcome {
            Ok(()) if rejecting => self.close(state, tx),
            Ok(()) => garden.judging = None,
            Err(reason) => {
                let judging = garden.judging.as_mut().expect("ours");
                judging.busy = false;
                judging.failure = Some(reason);
                if rejecting {
                    tx.focus(reject_key(&self.at, &self.check), true);
                }
            }
        }
    }
}
