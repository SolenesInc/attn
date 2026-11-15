use std::collections::HashSet;

use attn_protocol::{
    SeedArtifact, SeedArtifactReference, SeedArtifactTargetMessage,
    SeedArtifactTargetResultMessage, SeedArtifactTransferMessage, SeedCheckEvidence, SeedDocument,
    SeedNoteMessage,
};
use rune::tok::{space, type_};
use rune::{El, FileDialogOptions, Tx, col, row, text};

use super::sheet::{Sheet, meta_word};
use super::spot::Spot;
use super::view::kit_button;
use crate::State;
use crate::requests::Refused;

/// How a linked artifact reads in its row: what it is, the part a person recognizes, where it
/// sits, and the file or page it opens.
struct Presented {
    kind: String,
    primary: String,
    secondary: String,
    path: String,
    href: String,
}

fn present(reference: &SeedArtifactReference) -> Presented {
    let path = reference.path.clone().unwrap_or_default();
    let (dir, name) = path.rsplit_once('/').unwrap_or(("", &path));
    let blank = Presented {
        kind: "artifact".into(),
        primary: String::new(),
        secondary: String::new(),
        path: String::new(),
        href: String::new(),
    };
    match reference.kind.as_str() {
        "markdown_file" | "repository" => {
            let secondary = [reference.repository.as_deref().unwrap_or(""), dir]
                .into_iter()
                .filter(|p| !p.is_empty())
                .collect::<Vec<_>>()
                .join(" / ");
            Presented {
                kind: if reference.kind == "repository" {
                    "repo file"
                } else {
                    "linked file"
                }
                .into(),
                primary: if name.is_empty() { &path } else { name }.to_owned(),
                secondary,
                path: path.clone(),
                ..blank
            }
        }
        "notebook" => Presented {
            kind: "notebook".into(),
            primary: reference.notebook_document_id.clone().unwrap_or_default(),
            ..blank
        },
        _ => match reference.url.as_deref().filter(|u| !u.is_empty()) {
            Some(url) => match pull_request(url) {
                Some((sort, number, repo)) => Presented {
                    kind: sort.into(),
                    primary: format!("#{number}"),
                    secondary: repo,
                    href: url.to_owned(),
                    ..blank
                },
                None => {
                    let rest = url.split_once("://").map_or(url, |(_, r)| r);
                    let (host, path) = rest.split_once('/').unwrap_or((rest, ""));
                    let path = path.split('#').next().unwrap_or("").trim_end_matches('/');
                    Presented {
                        kind: "link".into(),
                        primary: host.trim_start_matches("www.").to_owned(),
                        secondary: if path.is_empty() {
                            String::new()
                        } else {
                            format!("/{path}")
                        },
                        href: url.to_owned(),
                        ..blank
                    }
                }
            },
            None => Presented {
                kind: reference.kind.clone(),
                primary: reference
                    .repository
                    .clone()
                    .unwrap_or_else(|| reference.kind.clone()),
                ..blank
            },
        },
    }
}

/// A GitHub pull request or issue link as its kind, number and repository.
fn pull_request(url: &str) -> Option<(&'static str, String, String)> {
    let rest = url
        .strip_prefix("https://")
        .or_else(|| url.strip_prefix("http://"))?;
    let rest = rest.strip_prefix("www.").unwrap_or(rest);
    let mut parts = rest.strip_prefix("github.com/")?.split('/');
    let (owner, repo, sort, number) = (parts.next()?, parts.next()?, parts.next()?, parts.next()?);
    let number: String = number.chars().take_while(char::is_ascii_digit).collect();
    let sort = match sort {
        "pull" => "pull request",
        "issues" => "issue",
        _ => return None,
    };
    (!owner.is_empty() && !repo.is_empty() && !number.is_empty())
        .then(|| (sort, number, format!("{owner}/{repo}")))
}

fn bytes(size: i64) -> String {
    let digits = size.to_string();
    let mut grouped = String::new();
    for (i, digit) in digits.chars().enumerate() {
        if i > 0 && (digits.len() - i).is_multiple_of(3) {
            grouped.push(',');
        }
        grouped.push(digit);
    }
    format!("{grouped} bytes")
}

impl Sheet<'_> {
    /// The files the seed keeps, with Open for a document or image, Reveal and Move out, then
    /// what it links to: files, with Move and Copy into the seed and Remove link, notebook
    /// documents, and pages, which open in the browser.
    pub(super) fn artifacts(&self, document: Option<&SeedDocument>) -> Option<El> {
        let document = document?;
        if document.artifacts.is_empty() && document.references.is_empty() {
            return None;
        }
        let id = &document.seed.id;
        let missing = super::missing(self.garden, id);
        let mut list = col().role("list").w_full().gap(space::S1);
        for artifact in &document.artifacts {
            list = list.child(self.kept(id, artifact));
        }
        for (n, reference) in document.references.iter().enumerate() {
            list = list.child(self.linked(id, n, reference, missing));
        }
        Some(self.section("artifacts", "Artifacts").child(list))
    }

    fn kept(&self, id: &str, artifact: &SeedArtifact) -> El {
        let t = self.t;
        let kit = attn_ui::kit(t);
        let spot = Spot::new(&self.prefix, id, &format!("artifact.{}", artifact.filename));
        let shown = self.garden.shown(&spot);
        let mut actions = row().gap(space::S1);
        let target = artifact.relative_target.clone();
        if crate::markdown::links::is_safe_local(&artifact.filename) {
            let (opened, target) = (spot.clone(), target.clone());
            actions = actions.child(
                kit_button("Open")
                    .disabled(shown.busy)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        open_kept(&opened, state, &target, Show::Open, tx)
                    })
                    .build(kit)
                    .key(spot.key("open")),
            );
        }
        let revealed = spot.clone();
        actions = actions.child(
            kit_button("Reveal")
                .disabled(shown.busy)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    open_kept(&revealed, state, &target, Show::Reveal, tx)
                })
                .build(kit)
                .key(spot.key("reveal")),
        );
        let (moved, filename) = (spot.clone(), artifact.filename.clone());
        actions = actions.child(
            kit_button("Move out")
                .disabled(shown.busy)
                .on_press_tx(move |_: &mut State, tx: &mut Tx| move_out(&moved, &filename, tx))
                .build(kit)
                .key(spot.key("move-out")),
        );
        let line = row()
            .w_full()
            .flex_wrap()
            .items_center()
            .gap(space::S2)
            .child(meta_word("file", t))
            .child(
                text(artifact.filename.clone())
                    .font(type_::BODY)
                    .color(t.text_primary),
            )
            .child(meta_word(bytes(artifact.size), t))
            .child(actions);
        let named = format!("{} · {}", artifact.filename, bytes(artifact.size));
        self.artifact_row(&spot, &named, line, shown.failure)
    }

    fn linked(
        &self,
        id: &str,
        n: usize,
        reference: &SeedArtifactReference,
        missing: Option<&HashSet<String>>,
    ) -> El {
        let t = self.t;
        let kit = attn_ui::kit(t);
        let spot = Spot::new(&self.prefix, id, &format!("reference.{n}"));
        let shown = self.garden.shown(&spot);
        let shown_as = present(reference);
        let gone = !shown_as.path.is_empty() && missing.is_some_and(|m| m.contains(&shown_as.path));
        let mut line = row()
            .w_full()
            .flex_wrap()
            .items_center()
            .gap(space::S2)
            .child(meta_word(shown_as.kind.clone(), t));
        let primary = if !shown_as.href.is_empty() {
            let href = shown_as.href.clone();
            kit_button(shown_as.primary.clone())
                .help(href.clone())
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    if let Err(why) = tx.open_url(&href) {
                        state
                            .window
                            .notify(format!("Could not open {href}: {why}"), tx);
                    }
                })
                .build(kit)
                .key(spot.key("open"))
                .role("link")
        } else if !shown_as.path.is_empty() && !gone {
            let path = shown_as.path.clone();
            kit_button(shown_as.primary.clone())
                .help(path.clone())
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    crate::opener::open_markdown(state, path.clone(), None, None, tx)
                })
                .build(kit)
                .key(spot.key("open"))
        } else {
            text(shown_as.primary.clone())
                .font(type_::BODY)
                .color(t.text_primary)
        };
        line = line.child(primary);
        if !shown_as.secondary.is_empty() {
            line = line.child(meta_word(shown_as.secondary.clone(), t));
        }
        if gone {
            line = line.child(
                text("not on disk")
                    .key(spot.key("gone"))
                    .font(type_::LABEL)
                    .color(t.error),
            );
        }
        if !shown_as.href.is_empty() {
            line = line.child(meta_word("↗", t).label("opens outside attn"));
        }
        if reference.kind == "markdown_file" {
            let mut actions = row().gap(space::S1);
            for (label, part, operation) in [
                ("Move into seed", "move-in", "move"),
                ("Copy into seed", "copy-in", "copy"),
            ] {
                let (brought, reference) = (spot.clone(), reference.clone());
                actions = actions.child(
                    kit_button(label)
                        .disabled(shown.busy)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            bring_in(&brought, state, &reference, operation, tx)
                        })
                        .build(kit)
                        .key(spot.key(part)),
                );
            }
            let (removed, reference) = (spot.clone(), reference.clone());
            actions = actions.child(
                kit_button("Remove link")
                    .disabled(shown.busy)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        remove_link(&removed, state, &reference, tx)
                    })
                    .build(kit)
                    .key(spot.key("remove")),
            );
            line = line.child(actions);
        }
        let mut named = format!("{} {}", shown_as.kind, shown_as.primary);
        if !shown_as.secondary.is_empty() {
            named.push_str(&format!(" · {}", shown_as.secondary));
        }
        if gone {
            named.push_str(" · not on disk");
        }
        self.artifact_row(&spot, &named, line, shown.failure)
    }

    fn artifact_row(&self, spot: &Spot, name: &str, line: El, failure: Option<&str>) -> El {
        let mut item = col()
            .key(spot.key("row"))
            .role("listitem")
            .label(name.to_owned())
            .w_full()
            .gap(2.0)
            .child(line);
        if let Some(failure) = failure {
            item = item.child(
                text(failure.to_owned())
                    .key(spot.key("error"))
                    .role("alert")
                    .label(failure.to_owned())
                    .font(type_::LABEL)
                    .color(self.t.error)
                    .wrap(),
            );
        }
        item
    }
}

/// How the system shows a file the seed keeps: opened in its app, or selected in the file
/// manager.
#[derive(Clone, Copy)]
pub(super) enum Show {
    Open,
    Reveal,
}

/// Asks the daemon where the seed keeps `target`, and has the system show it.
fn open_kept(spot: &Spot, state: &mut State, target: &str, how: Show, tx: &mut Tx) {
    let (seed_id, relative_target) = (spot.seed().to_owned(), target.to_owned());
    let home = spot.key(match how {
        Show::Open => "open",
        Show::Reveal => "reveal",
    });
    spot.send_as(
        state,
        None,
        |request_id| target_of(seed_id, relative_target, request_id),
        home,
        move |state, answer, tx| {
            let path = answer
                .parse::<SeedArtifactTargetResultMessage>()
                .filter(|r| r.success)
                .and_then(|r| r.result)
                .and_then(|r| r.path);
            if let Some(path) = path {
                show(state, &path, how, tx);
            }
        },
        tx,
    );
}

/// Where a check's evidence opens.
pub(super) enum Opens {
    Kept { target: String, how: Show },
    Page(String),
    Markdown(String),
}

/// Where `evidence` opens: its link, or the kept file or linked artifact of `document` it
/// names; nothing when it names neither.
pub(super) fn opens(
    evidence: &SeedCheckEvidence,
    document: Option<&SeedDocument>,
) -> Option<Opens> {
    if let Some(url) = &evidence.url {
        return Some(Opens::Page(url.clone()));
    }
    let named = evidence.artifact.as_deref()?;
    let document = document?;
    if let Some(kept) = document.artifacts.iter().find(|a| a.filename == named) {
        let how = if crate::markdown::links::is_safe_local(&kept.filename) {
            Show::Open
        } else {
            Show::Reveal
        };
        return Some(Opens::Kept {
            target: kept.relative_target.clone(),
            how,
        });
    }
    let reference = document.references.iter().find(|r| label_of(r) == named)?;
    match (&reference.url, reference.kind.as_str(), &reference.path) {
        (Some(url), _, _) => Some(Opens::Page(url.clone())),
        (None, "markdown_file", Some(path)) => Some(Opens::Markdown(path.clone())),
        _ => None,
    }
}

/// The part of a linked artifact a person recognizes, as evidence names it.
fn label_of(reference: &SeedArtifactReference) -> &str {
    [
        &reference.path,
        &reference.notebook_document_id,
        &reference.url,
        &reference.repository,
    ]
    .into_iter()
    .flatten()
    .map(String::as_str)
    .find(|s| !s.is_empty())
    .unwrap_or(&reference.kind)
}

/// Opens a check's evidence of seed `seed`, saying why when it cannot.
pub(super) fn open_evidence(state: &mut State, seed: &str, opens: &Opens, tx: &mut Tx) {
    match opens {
        Opens::Kept { target, how } => {
            let (seed_id, relative_target, how) = (seed.to_owned(), target.clone(), *how);
            state.act(
                |request_id| target_of(seed_id, relative_target, request_id),
                Refused::Prefixed("Could not open the evidence"),
                move |state, reply, tx| {
                    let path = reply
                        .parse::<SeedArtifactTargetResultMessage>()
                        .and_then(|r| r.result)
                        .and_then(|r| r.path);
                    if let Some(path) = path {
                        show(state, &path, how, tx);
                    }
                },
                tx,
            );
        }
        Opens::Page(url) => {
            if let Err(why) = tx.open_url(url) {
                state
                    .window
                    .notify(format!("Could not open {url}: {why}"), tx);
            }
        }
        Opens::Markdown(path) => crate::opener::open_markdown(state, path.clone(), None, None, tx),
    }
}

fn target_of(
    seed_id: String,
    relative_target: String,
    request_id: String,
) -> SeedArtifactTargetMessage {
    SeedArtifactTargetMessage {
        seed_id,
        relative_target,
        purpose: "artifact".into(),
        request_id,
        ..SeedArtifactTargetMessage::new()
    }
}

fn show(state: &mut State, path: &str, how: Show, tx: &mut Tx) {
    let shown = match how {
        Show::Open => tx.open_uri(&attn_term::links::file_url(path)),
        Show::Reveal => tx.reveal_path(path),
    };
    if let Err(why) = shown {
        state
            .window
            .notify(format!("Could not show {path}: {why}"), tx);
    }
}

/// Asks where to put the seed's file `filename`, then moves it there out of the seed.
fn move_out(spot: &Spot, filename: &str, tx: &mut Tx) {
    let options = FileDialogOptions {
        title: "Move artifact out of this seed".into(),
        file_name: Some(filename.to_owned()),
        ..FileDialogOptions::default()
    };
    let (spot, filename) = (spot.clone(), filename.to_owned());
    tx.save_file(options, move |state: &mut State, chosen, tx| {
        let destination = match chosen {
            Ok(Some(destination)) => destination,
            Ok(None) => return,
            Err(why) => {
                return state
                    .window
                    .notify(format!("Could not ask where to move {filename}: {why}"), tx);
            }
        };
        let seed_id = spot.seed().to_owned();
        spot.send(
            state,
            |request_id| SeedArtifactTransferMessage {
                seed_id,
                operation: "detach".into(),
                filename: Some(filename),
                destination_path: Some(destination.to_string_lossy().into_owned()),
                request_id: Some(request_id),
                ..SeedArtifactTransferMessage::new()
            },
            spot.key("move-out"),
            tx,
        );
    });
}

/// Moves or copies the linked file into the seed, in place of the link; a link without an
/// absolute path first asks which file it means.
fn bring_in(
    spot: &Spot,
    state: &mut State,
    reference: &SeedArtifactReference,
    operation: &'static str,
    tx: &mut Tx,
) {
    let path = reference.path.clone().unwrap_or_default();
    if path.starts_with('/') {
        return transfer(spot, state, reference.clone(), operation, path, tx);
    }
    let options = FileDialogOptions {
        title: "Choose the linked file to bring into this seed".into(),
        ..FileDialogOptions::default()
    };
    let (spot, reference) = (spot.clone(), reference.clone());
    tx.open_file(options, move |state: &mut State, chosen, tx| match chosen {
        Ok(Some(source)) => {
            let source = source.to_string_lossy().into_owned();
            transfer(&spot, state, reference, operation, source, tx)
        }
        Ok(None) => {}
        Err(why) => state
            .window
            .notify(format!("Could not ask for the linked file: {why}"), tx),
    });
}

fn transfer(
    spot: &Spot,
    state: &mut State,
    reference: SeedArtifactReference,
    operation: &str,
    source: String,
    tx: &mut Tx,
) {
    let seed_id = spot.seed().to_owned();
    let operation = operation.to_owned();
    let home = spot.key(if operation == "move" {
        "move-in"
    } else {
        "copy-in"
    });
    spot.send(
        state,
        |request_id| SeedArtifactTransferMessage {
            seed_id,
            operation,
            source_path: Some(source),
            legacy_reference: Some(reference),
            request_id: Some(request_id),
            ..SeedArtifactTransferMessage::new()
        },
        home,
        tx,
    );
}

fn remove_link(spot: &Spot, state: &mut State, reference: &SeedArtifactReference, tx: &mut Tx) {
    let (seed_id, artifact) = (spot.seed().to_owned(), reference.clone());
    spot.send(
        state,
        |request_id| SeedNoteMessage {
            seed_id,
            body: String::new(),
            kind: Some("detach".into()),
            artifact: Some(artifact),
            request_id: Some(request_id),
            ..SeedNoteMessage::new()
        },
        spot.key("remove"),
        tx,
    );
}
