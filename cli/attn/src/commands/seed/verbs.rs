use super::checks::parse_plant_checks;
use super::render::{self, ListView, parse_window_nanos, seed_line};
use super::{Session, Stop, fail, print_stdout};
use crate::commands::{Run, absolute, print_json};
use attn::garden::is_seed_id;
use attn::garden::plot::{Check, PlotSpec, parse_plot_spec, validate_body};
use attn::garden::section::section;
use attn::go::quote::quote;
use attn_protocol::go_values::prompts::AUTHORING_AGENT_SEED_GUIDE;
use attn_protocol::go_values::{
    EDGE_BLOCKS, LINKABLE_KINDS, NOTE_KIND_ATTACH, NOTE_KIND_DETACH, NOTE_KIND_HANDOFF,
};
use attn_protocol::{
    SeedArtifactReference, SeedArtifactTransferMessage, SeedArtifactTransferResult,
    SeedAuthorizationMessage, SeedAuthorizationResult, SeedCheckDefinition, SeedEditMessage,
    SeedEditResult, SeedHarvestWhenMerged, SeedLinkMessage, SeedLinkResult, SeedListMessage,
    SeedListResult, SeedNoteMessage, SeedNoteResult, SeedNotesMessage, SeedNotesResult,
    SeedPlantMessage, SeedPlantResult, SeedPlotChild, SeedPlotMessage, SeedPlotResult,
    SeedReadyMessage, SeedReadyResult, SeedReminderMessage, SeedReminderResult,
    SeedReviewActionContext, SeedReviewCancelMessage, SeedReviewKeepMessage, SeedReviewResult,
    SeedReviewRetryMessage, SeedReviewShowMessage, SeedReviewStartMessage, SeedRollbackMessage,
    SeedRollbackResult, SeedSearchMessage, SeedSearchResult, SeedSendToChiefMessage,
    SeedSendToChiefResult, SeedShowMessage, SeedShowResult, SeedStandbyMessage, SeedStandbyResult,
    SeedTransitionMessage, SeedTransitionResult, SeedWatchMessage, SeedWatchResult,
};
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::fmt::Write as _;
use std::io::Read;
use std::path::Path;

const STALE_SECONDS_NANOS: i64 = 1_000_000_000;

pub(super) fn dispatch(run: &mut Run<'_>, verb: &str, args: &[String]) -> Result<(), Stop> {
    match verb {
        "plant" => plant(run, args),
        "plot" => plot(run, args),
        "ls" => list(run, args),
        "search" => search(run, args),
        "show" => show(run, args),
        "review" => review(run, args),
        "edit" => edit(run, args),
        "rollback" => rollback(run, args),
        "authorization" => authorization(run, args),
        "remind" => remind(run, args),
        "standby" => standby(run, args),
        "send-to-chief" => send_to_chief(run, args),
        "export" => export(run, args),
        "tend" | "park" | "harvest" | "wither" | "replant" => transition(run, verb, args),
        "note" => note(run, args),
        "check" => super::checks::run(run, args),
        "watch" | "unwatch" => watch(run, verb, args),
        "attach" | "detach" => artifact(run, verb, args),
        "notes" => notes(run, args),
        "link" | "unlink" => link(run, verb, args),
        "ready" => ready(run, args),
        "prime" => prime(run, args),
        "guide" => guide(run, args),
        other => unreachable!("unrouted seed verb {other}"),
    }
}

fn non_empty(value: &str) -> Option<String> {
    (!value.is_empty()).then(|| value.to_string())
}

fn single_id<'p>(
    session: &Session<'_, '_>,
    positionals: &'p [String],
    message: impl FnOnce(usize) -> String,
) -> Result<&'p str, Stop> {
    match positionals {
        [only] => Ok(only),
        many => Err(session.usage(&message(many.len()))),
    }
}

fn result_or_json<T: serde::de::DeserializeOwned>(
    session: &mut Session<'_, '_>,
    reply: &crate::client::Reply,
    field: &str,
) -> Result<Option<T>, Stop> {
    if session.flags.on("json") {
        session.print_json(reply, &[field])?;
        return Ok(None);
    }
    session.read_field(reply, field).map(Some)
}

fn plant(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "plant");
    let positionals = session.parse(args)?;
    if positionals.len() != 1 {
        return Err(session.usage(&format!(
            r#"needs exactly one title, got {}: attn seed plant "what this is" [-m "the detail"]"#,
            positionals.len()
        )));
    }
    let mut message = SeedPlantMessage::new();
    message.title.clone_from(&positionals[0]);
    message.checks = Some(parse_plant_checks(session.flags.repeated("check")));
    message.source_session_id = non_empty(&session.session_id()?);
    message.body = non_empty(&session.text()?);
    message.part_of = non_empty(session.flags.string("part-of").trim());
    message.discovered_from = non_empty(session.flags.string("discovered-from").trim());
    let reply = session.call(
        &message,
        "seed_plant_result",
        "the daemon accepted the planting but returned no seed",
    )?;
    if session.flags.on("json") {
        return session.print_json(&reply, &["seed_plant_result", "seed"]);
    }
    let result: SeedPlantResult = session.read_field(&reply, "seed_plant_result")?;
    let seed = &result.seed;
    let mut out = format!("{}\n", seed_line(&seed.id, &seed.step_slug, &seed.title));
    for check in seed.checks.as_deref().unwrap_or_default() {
        if check.from.as_deref().unwrap_or_default().is_empty() {
            let _ = writeln!(out, "  {}  {}", check.id, check.text);
        }
    }
    render::similar(&mut out, &result.similar, false);
    print_stdout(&out);
    Ok(())
}

fn read_payload(session: &Session<'_, '_>, path: &str) -> Result<Vec<u8>, Stop> {
    if path.is_empty() || path == "-" {
        let mut payload = Vec::new();
        return match std::io::stdin().read_to_end(&mut payload) {
            Ok(_) => Ok(payload),
            Err(error) => Err(session.fail(&format!(
                "read the plot from stdin: {}",
                crate::paths::os_error_text(&error)
            ))),
        };
    }
    let open_error = |op: &str, error: &std::io::Error| {
        session.fail(&format!(
            "{op} {path}: {}",
            crate::paths::os_error_text(error)
        ))
    };
    let mut file = std::fs::File::open(path).map_err(|error| open_error("open", &error))?;
    let mut payload = Vec::new();
    file.read_to_end(&mut payload)
        .map_err(|error| open_error("read", &error))?;
    Ok(payload)
}

fn wire_checks(checks: &[Check]) -> Vec<SeedCheckDefinition> {
    checks
        .iter()
        .map(|check| SeedCheckDefinition {
            id: Some(check.id.clone()),
            text: check.text.clone(),
            shared: check.shared.then_some(true),
        })
        .collect()
}

fn plot_message(spec: &PlotSpec) -> SeedPlotMessage {
    let mut message = SeedPlotMessage::new();
    message.title.clone_from(&spec.title);
    message.checks = Some(wire_checks(&spec.checks));
    message.body = non_empty(&spec.body);
    message.part_of = non_empty(&spec.part_of);
    message.discovered_from = non_empty(&spec.discovered_from);
    message.children = spec
        .children
        .iter()
        .map(|child| SeedPlotChild {
            title: child.title.clone(),
            after: (!child.after.is_empty()).then(|| child.after.clone()),
            checks: Some(wire_checks(&child.checks)),
            body: non_empty(&child.body),
        })
        .collect();
    message
}

fn plot(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "plot");
    let positionals = session.parse(args)?;
    if let Some(first) = positionals.first() {
        return Err(session.usage(&format!(
            "takes no arguments, got {}; the plot is JSON on stdin or at -f <path>",
            quote(first.as_bytes())
        )));
    }
    let path = session.flags.string("f").trim().to_string();
    let payload = read_payload(&session, &path)?;
    let spec = parse_plot_spec(&payload).map_err(|error| session.fail(&error))?;
    let mut message = plot_message(&spec);
    message.source_session_id = non_empty(&session.session_id()?);
    let reply = session.call(
        &message,
        "seed_plot_result",
        "the daemon accepted the plot but returned no seeds",
    )?;
    let Some(result) = result_or_json::<SeedPlotResult>(&mut session, &reply, "seed_plot_result")?
    else {
        return Ok(());
    };
    let crown = &result.crown;
    let mut out = format!("{}\n", seed_line(&crown.id, &crown.step_slug, &crown.title));
    let mut rows = String::new();
    for child in &result.children {
        let _ = writeln!(rows, "  {}\t{}\t{}", child.id, child.step_slug, child.title);
    }
    out.push_str(&attn::go::tabwriter::format(&rows));
    render::similar(&mut out, &result.similar, true);
    print_stdout(&out);
    Ok(())
}

fn search(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "search");
    let query = session.parse(args)?.join(" ");
    if query.trim().is_empty() {
        return Err(session.usage("needs something to look for: `attn seed search <words>`"));
    }
    let mut message = SeedSearchMessage::new();
    message.query.clone_from(&query);
    let limit = session.flags.int("limit");
    if limit != 0 {
        message.limit = Some(limit);
    }
    message.source_session_id = non_empty(&session.session_id()?);
    let reply = session.call(
        &message,
        "seed_search_result",
        "the daemon answered without search results",
    )?;
    let Some(result) =
        result_or_json::<SeedSearchResult>(&mut session, &reply, "seed_search_result")?
    else {
        return Ok(());
    };
    print_stdout(&render::search(&query, &result));
    Ok(())
}

fn list(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "ls");
    let positionals = session.parse(args)?;
    if let Some(first) = positionals.first() {
        return Err(session.usage(&format!(
            "takes no arguments, got {}; to read one seed use `attn seed show <id>`",
            quote(first.as_bytes())
        )));
    }
    let stale = session.flags.on("stale");
    let standby = session.flags.on("standby");
    if !stale && session.flags.was_set("window") {
        return Err(
            session.fail("--window is the stale window; it only means something with --stale")
        );
    }
    let plot_id = session.flags.string("plot").trim().to_string();
    if !plot_id.is_empty() && stale {
        return Err(session.usage(
            "--plot lists every child of the plot and --stale narrows the whole garden; pass one of them",
        ));
    }
    if standby && (!plot_id.is_empty() || stale) {
        return Err(session.usage(
            "--standby narrows the whole garden to seeds on standby; pass it without --plot or --stale",
        ));
    }
    let mut message = SeedListMessage::new();
    session.scope()?;
    if !plot_id.is_empty() {
        message.plot = Some(plot_id.clone());
    } else if standby {
        message.standby = Some(true);
    } else {
        message.stale = stale.then_some(true);
        let raw = session.flags.string("window").trim().to_string();
        if !raw.is_empty() {
            let window = parse_window_nanos(&raw).map_err(|error| fail("ls", &error))?;
            let seconds = window / STALE_SECONDS_NANOS;
            if seconds > 0 {
                message.stale_window_seconds = Some(seconds);
            }
        }
    }
    message.source_session_id = non_empty(&session.session_id()?);
    let reply = session.call(
        &message,
        "seed_list_result",
        "the daemon answered without a seed list",
    )?;
    let Some(result) = result_or_json::<SeedListResult>(&mut session, &reply, "seed_list_result")?
    else {
        return Ok(());
    };
    print_stdout(&render::list(
        &result,
        &ListView {
            plot_id,
            flat: session.flags.on("flat"),
            stale,
            standby,
        },
    ));
    Ok(())
}

fn review(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let Some(verb) = args.first().filter(|first| !first.starts_with('-')) else {
        let mut session = Session::new(run, "review");
        session.parse(args)?;
        return Err(session.usage("needs start, show, cancel, retry, or keep"));
    };
    if !["start", "show", "cancel", "retry", "keep"].contains(&verb.as_str()) {
        run.path = "seed review".to_string();
        run.usage_error = Some(crate::commands::USAGE_UNKNOWN_COMMAND);
        return Err(super::usage_error(
            "review",
            &format!(
                "unknown command {}; use start, show, cancel, retry, or keep",
                quote(verb.as_bytes())
            ),
        ));
    }
    run.path = format!("seed review {verb}");
    let name = format!("review {verb}");
    let mut session = Session::new(run, &name);
    let positionals = session.parse(&args[1..])?;
    let reply = match verb.as_str() {
        "start" => {
            if !positionals.is_empty() {
                return Err(session.usage("takes no arguments"));
            }
            session.call(
                &SeedReviewStartMessage::new(),
                "seed_review_result",
                "the daemon started the Garden review but returned no review",
            )?
        }
        "show" => {
            if positionals.len() > 1 {
                return Err(session.usage("takes at most one review id"));
            }
            let mut message = SeedReviewShowMessage::new();
            message.review_id = positionals.first().cloned();
            session.call(
                &message,
                "seed_review_result",
                "the daemon answered without a Garden review",
            )?
        }
        "cancel" => {
            if positionals.len() != 1 {
                return Err(session.usage("needs exactly one review id"));
            }
            let mut message = SeedReviewCancelMessage::new();
            message.review_id.clone_from(&positionals[0]);
            session.call(
                &message,
                "seed_review_result",
                "the daemon canceled the Garden review but returned no review",
            )?
        }
        "retry" => {
            if positionals.len() != 2 {
                return Err(session.usage("needs a review id and seed id"));
            }
            let mut message = SeedReviewRetryMessage::new();
            message.review_id.clone_from(&positionals[0]);
            message.seed_id.clone_from(&positionals[1]);
            session.call(
                &message,
                "seed_review_result",
                "the daemon retried the Garden review item but returned no review",
            )?
        }
        _ => keep_review(&mut session, &positionals)?,
    };
    let Some(result) =
        result_or_json::<SeedReviewResult>(&mut session, &reply, "seed_review_result")?
    else {
        return Ok(());
    };
    match &result.review {
        None => print_stdout(&format!("{} seeds need review\n", result.candidate_count)),
        Some(review) => print_stdout(&render::review(review, verb == "show")),
    }
    Ok(())
}

fn seed_id_of(session: &mut Session<'_, '_>, reference: &str) -> Result<String, Stop> {
    if is_seed_id(reference) {
        return Ok(reference.to_string());
    }
    Ok(show_seed(session, reference, None)?.seed.id)
}

fn show_seed(
    session: &mut Session<'_, '_>,
    seed_id: &str,
    since: Option<i64>,
) -> Result<SeedShowResult, Stop> {
    let reply = show_reply(session, seed_id, since)?;
    session.read_field(&reply, "seed_show_result")
}

fn show_reply(
    session: &mut Session<'_, '_>,
    seed_id: &str,
    since: Option<i64>,
) -> Result<crate::client::Reply, Stop> {
    let mut message = SeedShowMessage::new();
    message.seed_id = seed_id.to_string();
    message.since = since;
    message.source_session_id = non_empty(&session.session_id()?);
    session.call(
        &message,
        "seed_show_result",
        "the daemon answered without a seed",
    )
}

fn keep_review(
    session: &mut Session<'_, '_>,
    positionals: &[String],
) -> Result<crate::client::Reply, Stop> {
    if positionals.len() != 2 {
        return Err(session.usage("needs a review id and seed id"));
    }
    let seed_id = seed_id_of(session, &positionals[1])?;
    let mut show = SeedReviewShowMessage::new();
    show.review_id = Some(positionals[0].clone());
    let shown = session.call(
        &show,
        "seed_review_result",
        "the daemon answered without a Garden review",
    )?;
    let shown: SeedReviewResult = session.read_field(&shown, "seed_review_result")?;
    let receipt = shown
        .review
        .as_ref()
        .and_then(|review| review.items.iter().find(|item| item.seed_id == seed_id));
    let Some(receipt) = receipt else {
        return Err(session.fail(&format!(
            "seed {} is not part of Garden review {}",
            positionals[1], positionals[0]
        )));
    };
    let mut message = SeedReviewKeepMessage::new();
    message.seed_id.clone_from(&seed_id);
    message.review = SeedReviewActionContext {
        review_id: positionals[0].clone(),
        evidence_version: receipt.evidence_version.clone(),
    };
    session.call(
        &message,
        "seed_review_result",
        "the daemon kept the seed growing but returned no review",
    )
}

fn bookmark(session: &Session<'_, '_>) -> Result<Option<i64>, Stop> {
    if !session.flags.was_set("since") {
        return Ok(None);
    }
    let since = session.flags.int("since");
    if since < 0 {
        return Err(session.usage(&format!(
            "--since {since} is not a bookmark; pass the number a read printed on its bookmark line"
        )));
    }
    Ok(Some(since))
}

fn show(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "show");
    let positionals = session.parse(args)?;
    if positionals.len() != 1 {
        return Err(session.usage(&format!(
            "needs exactly one seed id, got {}: attn seed show s-7k3f9m",
            positionals.len()
        )));
    }
    if session.flags.was_set("since") && session.flags.was_set("section") {
        return Err(
            session.usage("--since reads log entries and --section reads the body; pass one")
        );
    }
    let since = bookmark(&session)?;
    let reply = show_reply(&mut session, &positionals[0], since)?;
    let Some(result) = result_or_json::<SeedShowResult>(&mut session, &reply, "seed_show_result")?
    else {
        return Ok(());
    };
    let seed = &result.seed;
    if session.flags.was_set("section") {
        let part = section(&seed.body, session.flags.string("section"))
            .map_err(|error| session.fail(&format!("{}: {error}", seed.id)))?;
        print_stdout(&format!("{}  {}\n\n{part}\n", seed.id, seed.title));
        return Ok(());
    }
    let mut out = String::new();
    if let Some(window) = &result.since {
        match &window.expired {
            None => {
                render::since(&mut out, window.after, &window.entries);
                render::bookmark_line(&mut out, "show", &seed.id, result.bookmark, "");
                print_stdout(&out);
                return Ok(());
            }
            Some(expired) => render::expired_bookmark(&mut out, window.after, expired),
        }
    }
    out.push_str(&render::show(&result, result.bookmark));
    print_stdout(&out);
    Ok(())
}

fn body_stamp(id: &str, body: &str) -> String {
    let digest = Sha256::digest(body.as_bytes());
    let mut hex = String::with_capacity(64);
    for byte in digest {
        let _ = write!(hex, "{byte:02x}");
    }
    format!("<!-- attn-seed {id} body-sha256 {hex} -->\n{body}")
}

fn edit_recipe(id: &str) -> String {
    format!(
        "edit takes an exported file; run attn seed export {id}, edit it, then attn seed edit {id} -m - < {id}.md"
    )
}

fn parse_stamped(id: &str, text: &str) -> Result<(String, String), String> {
    let recipe = || edit_recipe(id);
    let Some((line, body)) = text.split_once('\n') else {
        return Err(recipe());
    };
    let line = line.strip_suffix('\r').unwrap_or(line);
    let inner = line
        .strip_prefix("<!-- attn-seed ")
        .and_then(|rest| rest.strip_suffix(" -->"))
        .ok_or_else(recipe)?;
    let (stamped, hash) = inner.split_once(" body-sha256 ").ok_or_else(recipe)?;
    let hash_is_hex = hash.len() == 64
        && hash
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte));
    if stamped.is_empty() || stamped.contains([' ', '\t']) || !hash_is_hex {
        return Err(recipe());
    }
    if stamped != id {
        return Err(format!(
            "this export belongs to {stamped}, not {id}; {}",
            recipe()
        ));
    }
    if body.trim().is_empty() {
        return Err(
            "a body edit needs nonempty text; empty or whitespace-only bodies are refused"
                .to_string(),
        );
    }
    validate_body(body)?;
    Ok((body.to_string(), hash.to_string()))
}

fn edit(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "edit");
    let positionals = session.parse(args)?;
    if positionals.len() != 1 {
        return Err(session.usage(&format!(
            "needs exactly one seed id, got {}: attn seed edit s-7k3f9m -m -",
            positionals.len()
        )));
    }
    if !session.flags.was_set("m") {
        return Err(session.usage(&edit_recipe(&positionals[0])));
    }
    let id = seed_id_of(&mut session, &positionals[0])?;
    let text = session.text()?;
    let (body, base) = parse_stamped(&id, &text).map_err(|error| session.fail(&error))?;
    let mut message = SeedEditMessage::new();
    message.seed_id = id;
    message.body = body;
    message.base_body_sha256 = base;
    message.note = Some(session.flags.string("note").to_string());
    let reply = session.call(
        &message,
        "seed_edit_result",
        "the daemon accepted the edit but returned no seed",
    )?;
    let Some(result) = result_or_json::<SeedEditResult>(&mut session, &reply, "seed_edit_result")?
    else {
        return Ok(());
    };
    print_stdout(&format!(
        "updated {}: body v{}, {} → {} bytes\n",
        result.seed.id, result.body_version, result.previous_bytes, result.new_bytes
    ));
    Ok(())
}

fn parse_version(raw: &str) -> Option<i64> {
    let digits = raw.strip_prefix('v').unwrap_or(raw);
    let unsigned = digits.strip_prefix(['+', '-']).unwrap_or(digits);
    if unsigned.is_empty() || !unsigned.bytes().all(|byte| byte.is_ascii_digit()) {
        return None;
    }
    digits.parse::<i64>().ok().filter(|number| *number >= 1)
}

fn rollback(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "rollback");
    let positionals = session.parse(args)?;
    if positionals.is_empty() || positionals.len() > 2 {
        return Err(session.usage(
            "needs a seed id and optional version: attn seed rollback s-7k3f9m [<version>]",
        ));
    }
    let mut message = SeedRollbackMessage::new();
    message.seed_id.clone_from(&positionals[0]);
    if let Some(raw) = positionals.get(1) {
        let Some(number) = parse_version(raw) else {
            return Err(session.usage(&format!(
                "version {} must be a positive number, like 2 or v2",
                quote(raw.as_bytes())
            )));
        };
        message.version = Some(number);
    }
    let reply = session.call(
        &message,
        "seed_rollback_result",
        "the daemon accepted the rollback but returned no result",
    )?;
    let Some(result) =
        result_or_json::<SeedRollbackResult>(&mut session, &reply, "seed_rollback_result")?
    else {
        return Ok(());
    };
    print_stdout(&format!(
        "rolled {} back to v{} as v{}: {} → {} bytes\n",
        result.seed.id,
        result.restored_version,
        result.body_version,
        result.previous_bytes,
        result.new_bytes
    ));
    Ok(())
}

fn authorization(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "authorization");
    let positionals = session.parse(args)?;
    if positionals.is_empty() || positionals.len() > 2 {
        return Err(session.usage(
            "needs a seed id and optional words: attn seed authorization s-7k3f9m \"Ask me before pushing.\"",
        ));
    }
    let clear = session.flags.on("clear");
    let mut text = String::new();
    if let Some(words) = positionals.get(1) {
        text = words.trim().to_string();
        if text.is_empty() {
            return Err(session.usage("the words are empty; --clear removes the authorization"));
        }
    }
    if !text.is_empty() && clear {
        return Err(session.usage("pass the new words or --clear, not both"));
    }
    let id = seed_id_of(&mut session, &positionals[0])?;
    let mut message = SeedAuthorizationMessage::new();
    message.seed_id = id;
    message.text = non_empty(&text);
    message.clear = clear.then_some(true);
    let reply = session.call(
        &message,
        "seed_authorization_result",
        "the daemon accepted the authorization but returned no seed",
    )?;
    let Some(result) = result_or_json::<SeedAuthorizationResult>(
        &mut session,
        &reply,
        "seed_authorization_result",
    )?
    else {
        return Ok(());
    };
    let changing = !text.is_empty() || clear;
    print_stdout(&format!(
        "{}\n",
        render::authorization_reply(&result, changing, clear)
    ));
    Ok(())
}

fn remind(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "remind");
    let positionals = session.parse(args)?;
    if positionals.len() != 1 {
        return Err(session.usage(
            "needs exactly one seed id: attn seed remind s-7k3f9m --in 20m -m \"Check the rollout\"",
        ));
    }
    let text = session.text()?.trim().to_string();
    let clear = session.flags.on("clear");
    let remind_in = session.flags.string("in").to_string();
    let remind_at = session.flags.string("at").to_string();
    if clear && (!remind_in.is_empty() || !remind_at.is_empty() || !text.is_empty()) {
        return Err(session.usage("--clear takes no time or message"));
    }
    if !clear && remind_in.is_empty() == remind_at.is_empty() {
        return Err(session.usage("pass one of --in or --at, or --clear"));
    }
    if !clear && text.is_empty() {
        return Err(session.usage("-m names what the reminder says"));
    }
    let id = seed_id_of(&mut session, &positionals[0])?;
    let mut message = SeedReminderMessage::new();
    message.seed_id = id;
    message.r#in = non_empty(&remind_in);
    message.at = non_empty(&remind_at);
    message.message = non_empty(&text);
    message.clear = clear.then_some(true);
    let reply = session.call(
        &message,
        "seed_reminder_result",
        "the daemon accepted the reminder but returned no seed",
    )?;
    let Some(result) =
        result_or_json::<SeedReminderResult>(&mut session, &reply, "seed_reminder_result")?
    else {
        return Ok(());
    };
    let line = if let Some(reminder) = &result.seed.reminder {
        format!(
            "{} reminder {}",
            result.seed.id,
            render::reminder_line(reminder)
        )
    } else if result.changed {
        format!("{} reminder cleared.", result.seed.id)
    } else {
        format!("{} has no reminder.", result.seed.id)
    };
    print_stdout(&format!("{line}\n"));
    Ok(())
}

fn outlive_close_of_own_session() {
    unsafe {
        libc::signal(libc::SIGTERM, libc::SIG_IGN);
        libc::signal(libc::SIGHUP, libc::SIG_IGN);
    }
}

fn standby(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "standby");
    let positionals = session.parse(args)?;
    if positionals.len() != 1 {
        return Err(session.usage(
            "needs exactly one seed id: attn seed standby s-7k3f9m -m \"what to do next\"",
        ));
    }
    let note = session.text()?.trim().to_string();
    let clear = session.flags.on("clear");
    let stay = session.flags.on("stay");
    let standby_in = session.flags.string("in").to_string();
    let standby_at = session.flags.string("at").to_string();
    if clear && (!note.is_empty() || stay || !standby_in.is_empty() || !standby_at.is_empty()) {
        return Err(session.usage("--clear takes no note, time or --stay"));
    }
    if !standby_in.is_empty() && !standby_at.is_empty() {
        return Err(session.usage("pass --in or --at, not both"));
    }
    if !clear && note.is_empty() {
        return Err(session.usage("-m says what to do when mail resumes you"));
    }
    let id = seed_id_of(&mut session, &positionals[0])?;
    if !clear && !stay {
        outlive_close_of_own_session();
    }
    let mut message = SeedStandbyMessage::new();
    message.seed_id = id;
    message.note = non_empty(&note);
    message.r#in = non_empty(&standby_in);
    message.at = non_empty(&standby_at);
    message.stay = stay.then_some(true);
    message.clear = clear.then_some(true);
    let reply = session.call(
        &message,
        "seed_standby_result",
        "the daemon accepted the standby but returned no seed",
    )?;
    let raw: SeedStandbyResult = session.read_field(&reply, "seed_standby_result")?;
    if let Some(close) = raw
        .close
        .as_ref()
        .filter(|c| c.pending_compact != Some(true))
    {
        session.run.closed_session = Some(close.target_session_id.clone());
    }
    if session.flags.on("json") {
        return session.print_json(&reply, &["seed_standby_result"]);
    }
    let mut out = String::new();
    if let Some(standby) = &raw.seed.standby {
        let _ = writeln!(
            out,
            "{} on standby: {}",
            raw.seed.id,
            quote(standby.note.as_bytes())
        );
    } else if raw.changed {
        let _ = writeln!(out, "{} is off standby.", raw.seed.id);
    } else {
        let _ = writeln!(out, "{} was not on standby.", raw.seed.id);
    }
    if let Some(reminder) = raw
        .seed
        .reminder
        .as_ref()
        .filter(|_| !standby_in.is_empty() || !standby_at.is_empty())
    {
        let _ = writeln!(
            out,
            "{} reminder {}",
            raw.seed.id,
            render::reminder_line(reminder)
        );
    }
    if let Some(close) = &raw.close {
        render::agent_close(&mut out, close);
    }
    print_stdout(&out);
    Ok(())
}

fn send_to_chief(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "send-to-chief");
    let positionals = session.parse(args)?;
    let reference = single_id(&session, &positionals, |count| {
        format!("needs exactly one seed id, got {count}: attn seed send-to-chief s-7k3f9m")
    })?;
    let seed_id = reference.trim().to_string();
    let document = show_seed(&mut session, &seed_id, None)?.seed;
    let guidance = session.text()?.trim().to_string();
    let mut message = SeedSendToChiefMessage::new();
    message.seed_id = document.id;
    message.expected_rev = document.rev;
    message.guidance = non_empty(&guidance);
    message.source_session_id = non_empty(&session.session_id()?);
    let reply = session.call(
        &message,
        "seed_send_to_chief_result",
        "the daemon accepted the seed for Chief but returned no result",
    )?;
    let Some(result) =
        result_or_json::<SeedSendToChiefResult>(&mut session, &reply, "seed_send_to_chief_result")?
    else {
        return Ok(());
    };
    print_stdout(&format!("{seed_id} sent to Chief ({})\n", result.detail));
    Ok(())
}

#[derive(Default)]
struct TransitionOptions {
    when_merged: bool,
    pull_request_url: String,
    clear_harvest_when: bool,
}

fn one_seed_id(verb: &str, positionals: &[String], tail: &str) -> Result<String, String> {
    match positionals {
        [only] => Ok(only.clone()),
        many => Err(format!(
            "needs exactly one seed id, got {}: attn seed {verb} s-7k3f9m{tail}",
            many.len()
        )),
    }
}

fn pull_request_url_problem(raw: &str) -> Option<String> {
    let trimmed = raw.trim();
    let invalid_syntax = trimmed.contains(char::is_whitespace);
    let shape = "pull request url must be an https repository pull URL";
    if invalid_syntax {
        return Some(shape.to_string());
    }
    let rest = trimmed.strip_prefix("https://")?;
    if rest.contains(['?', '#']) {
        return Some(shape.to_string());
    }
    let (host, path) = rest.split_once('/')?;
    if host.is_empty() || host.contains('@') || host.contains(':') {
        return Some(shape.to_string());
    }
    let parts: Vec<&str> = path.trim_matches('/').split('/').collect();
    if parts.len() != 4 || parts[2] != "pull" {
        return Some("pull request url path must be /owner/repository/pull/number".to_string());
    }
    if parts
        .iter()
        .any(|part| part.is_empty() || part.contains('%'))
    {
        return Some("pull request url contains an invalid path component".to_string());
    }
    if !parts[3].parse::<i64>().is_ok_and(|number| number > 0) {
        return Some("pull request url number must be positive".to_string());
    }
    None
}

fn pull_request_url_error(raw: &str) -> Option<String> {
    if !raw.trim().starts_with("https://") || !raw.trim()["https://".len()..].contains('/') {
        return Some("pull request url must be an https repository pull URL".to_string());
    }
    pull_request_url_problem(raw)
}

fn harvest_when_args(
    verb: &str,
    session: &Session<'_, '_>,
    positionals: &[String],
) -> Result<(String, TransitionOptions), String> {
    let when_merged = session.flags.on("when-merged");
    let clear = session.flags.on("clear");
    if !when_merged && !clear {
        return one_seed_id(verb, positionals, "").map(|id| (id, TransitionOptions::default()));
    }
    if verb != "harvest" {
        return Err(
            "harvest-on-merge belongs to harvest: `attn seed harvest <id> --when-merged [pr-url]`"
                .to_string(),
        );
    }
    if !when_merged {
        return Err("--clear disarms harvest-on-merge; say the whole thing: `attn seed harvest <id> --when-merged --clear`".to_string());
    }
    if clear {
        if session.flags.was_set("m") {
            return Err(
                "--clear takes no reason; put it on the log with `attn seed note <id> -m \"…\"`"
                    .to_string(),
            );
        }
        let id = one_seed_id(verb, positionals, " --when-merged --clear")?;
        return Ok((
            id,
            TransitionOptions {
                clear_harvest_when: true,
                ..TransitionOptions::default()
            },
        ));
    }
    if let [id, url] = positionals {
        if let Some(problem) = pull_request_url_error(url) {
            return Err(format!(
                "{} is not a pull request url: {problem}",
                quote(url.as_bytes())
            ));
        }
        return Ok((
            id.clone(),
            TransitionOptions {
                when_merged: true,
                pull_request_url: url.clone(),
                ..TransitionOptions::default()
            },
        ));
    }
    let id = one_seed_id(verb, positionals, " --when-merged [pr-url]")?;
    Ok((
        id,
        TransitionOptions {
            when_merged: true,
            ..TransitionOptions::default()
        },
    ))
}

fn transition(run: &mut Run<'_>, verb: &str, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, verb);
    let positionals = session.parse(args)?;
    let (seed_id, options) =
        harvest_when_args(verb, &session, &positionals).map_err(|error| session.usage(&error))?;
    let source = session.session_id()?;
    let reason = session.text()?;
    let mut message = SeedTransitionMessage::new();
    message.seed_id = seed_id;
    message.verb = verb.to_string();
    if options.when_merged {
        message.when_merged = Some(SeedHarvestWhenMerged {
            pull_request_url: non_empty(&options.pull_request_url),
        });
    }
    message.clear_harvest_when = options.clear_harvest_when.then_some(true);
    message.source_session_id = non_empty(&source);
    if !reason.is_empty() {
        if verb == "park" {
            message.comment = Some(reason);
        } else {
            message.reason = Some(reason);
        }
    }
    message.assignee = non_empty(session.flags.string("for").trim());
    message.force = session.flags.on("force").then_some(true);
    let reply = session.call(
        &message,
        "seed_transition_result",
        "the daemon accepted the move but returned no seed",
    )?;
    let Some(result) =
        result_or_json::<SeedTransitionResult>(&mut session, &reply, "seed_transition_result")?
    else {
        return Ok(());
    };
    print_stdout(&render::transition(&result, options.clear_harvest_when));
    Ok(())
}

fn note_message(
    session: &mut Session<'_, '_>,
    seed_id: &str,
    body: String,
) -> Result<SeedNoteMessage, Stop> {
    let mut message = SeedNoteMessage::new();
    message.seed_id = seed_id.to_string();
    message.body = body;
    message.source_session_id = non_empty(&session.session_id()?);
    Ok(message)
}

fn note(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "note");
    let positionals = session.parse(args)?;
    let id = single_id(&session, &positionals, |count| {
        format!(
            r#"needs exactly one seed id, got {count}: attn seed note s-7k3f9m -m "what happened""#
        )
    })?;
    session.scope()?;
    let body = session.text()?;
    let mut message = note_message(&mut session, id, body)?;
    if session.flags.on("handoff") {
        message.kind = Some(NOTE_KIND_HANDOFF.to_string());
    }
    let ring = session.flags.on("ring");
    message.ring = ring.then_some(true);
    let reply = session.call(
        &message,
        "seed_note_result",
        "the daemon accepted the note but returned nothing",
    )?;
    if session.flags.on("json") {
        return session.print_json(&reply, &["seed_note_result", "note"]);
    }
    let result: SeedNoteResult = session.read_field(&reply, "seed_note_result")?;
    let mut out = if result.note.kind == NOTE_KIND_HANDOFF {
        format!(
            "handoff left on {} — whoever tends it next reads this first\n",
            result.note.seed_id
        )
    } else {
        format!("noted on {}\n", result.note.seed_id)
    };
    if ring {
        render::plural_rang(
            &mut out,
            &result.note.seed_id,
            result.ring_recipients.as_deref(),
        );
    }
    print_stdout(&out);
    Ok(())
}

fn watch(run: &mut Run<'_>, verb: &str, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, verb);
    let positionals = session.parse(args)?;
    let id = single_id(&session, &positionals, |count| {
        format!("needs exactly one seed id, got {count}: attn seed {verb} s-7k3f9m")
    })?;
    let unwatch = verb == "unwatch";
    let mut message = SeedWatchMessage::new();
    message.seed_id = id.to_string();
    message.source_session_id = session.session_id()?;
    message.unwatch = unwatch.then_some(true);
    let reply = session.call(
        &message,
        "seed_watch_result",
        "the daemon accepted the watch change but returned no state",
    )?;
    let Some(result) =
        result_or_json::<SeedWatchResult>(&mut session, &reply, "seed_watch_result")?
    else {
        return Ok(());
    };
    print_stdout(&render::watch_result(&result, unwatch));
    Ok(())
}

struct TransferPlan {
    operation: &'static str,
    source: String,
    filename: String,
    destination: String,
}

fn transfer_plan(verb: &str, session: &Session<'_, '_>) -> Result<Option<TransferPlan>, String> {
    let flags = &session.flags;
    let path = flags.string("path");
    let repo = flags.string("repo").trim();
    let to = flags.string("to");
    let moved = flags.on("move");
    let copied = flags.on("copy");
    let local_flags = moved || copied || !to.is_empty();
    let plain_managed = !path.is_empty()
        && repo.is_empty()
        && flags.string("notebook").trim().is_empty()
        && flags.string("url").trim().is_empty()
        && !flags.on("reference");
    let managed_attach = verb == "attach" && plain_managed;
    let managed_detach = verb == "detach" && plain_managed;
    if !managed_attach && !managed_detach && !local_flags {
        return Ok(None);
    }
    if flags.was_set("m") {
        return Err(
            "managed artifact transfers carry their own receipt; -m applies to log associations"
                .to_string(),
        );
    }
    let resolve = |value: &str, label: &str| {
        absolute(value).map_err(|error| format!("{label}: {}", crate::paths::os_error_text(&error)))
    };
    if verb == "attach" {
        if !managed_attach {
            return Err(
                "--move and --copy apply only to a local --path without --repo".to_string(),
            );
        }
        if moved == copied {
            return Err(
                "local attach requires exactly one of --move or --copy; --move is recommended"
                    .to_string(),
            );
        }
        if !to.is_empty() {
            return Err("--to belongs to detach".to_string());
        }
        return Ok(Some(TransferPlan {
            operation: if moved { "move" } else { "copy" },
            source: resolve(path, "resolve source path")?,
            filename: String::new(),
            destination: String::new(),
        }));
    }
    if !managed_detach {
        return Err("--to applies only to a managed --path without --repo".to_string());
    }
    if moved || copied {
        return Err("detach moves the artifact out; do not pass --move or --copy".to_string());
    }
    if to.is_empty() {
        return Err("managed detach requires --to <destination>; use --reference to remove an old linked path".to_string());
    }
    Ok(Some(TransferPlan {
        operation: "detach",
        source: String::new(),
        filename: path.to_string(),
        destination: resolve(to, "resolve detach destination")?,
    }))
}

fn artifact_reference(session: &Session<'_, '_>) -> Result<SeedArtifactReference, String> {
    let path = session.flags.string("path").trim();
    let repo = session.flags.string("repo").trim();
    let notebook = session.flags.string("notebook").trim();
    let url = session.flags.string("url").trim();
    let named: Vec<&str> = [("--notebook", notebook), ("--path", path), ("--url", url)]
        .into_iter()
        .filter(|(_, value)| !value.is_empty())
        .map(|(flag, _)| flag)
        .collect();
    match named.len() {
        0 => return Err("name the document: --path <file> [--repo <repository>], --notebook <document-id>, or --url <url>".to_string()),
        1 => {}
        _ => {
            return Err(format!(
                "{} were all given and a reference names one document; run it once per document",
                named.join(" and ")
            ));
        }
    }
    let mut reference = SeedArtifactReference::default();
    if !path.is_empty() {
        reference.kind = "markdown_file".to_string();
        reference.path = Some(path.to_string());
        reference.repository = non_empty(repo);
    } else if !notebook.is_empty() {
        reference.kind = "notebook".to_string();
        reference.notebook_document_id = Some(notebook.to_string());
    } else {
        reference.kind = "url".to_string();
        reference.url = Some(url.to_string());
    }
    Ok(reference)
}

fn artifact_label(reference: &SeedArtifactReference) -> String {
    reference
        .path
        .clone()
        .or_else(|| reference.notebook_document_id.clone())
        .or_else(|| reference.url.clone())
        .or_else(|| reference.repository.clone())
        .unwrap_or_else(|| reference.kind.clone())
}

fn default_note_body(kind: &str, reference: &SeedArtifactReference) -> String {
    let verb = if kind == NOTE_KIND_DETACH {
        "detached"
    } else {
        "attached"
    };
    if let (Some(repository), Some(path)) = (&reference.repository, &reference.path)
        && !repository.is_empty()
        && !path.is_empty()
    {
        return format!("{verb} {path} ({repository})");
    }
    format!("{verb} {}", artifact_label(reference))
}

fn artifact(run: &mut Run<'_>, verb: &str, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, verb);
    let positionals = session.parse(args)?;
    let seed_id = single_id(&session, &positionals, |count| {
        format!(
            "needs exactly one seed id, got {count}: attn seed {verb} s-7k3f9m --path docs/plans/thing.md"
        )
    })?;
    let plan = transfer_plan(verb, &session).map_err(|error| session.fail(&error))?;
    if let Some(plan) = plan {
        return transfer_artifact(&mut session, seed_id, &plan);
    }
    let path = session.flags.string("path");
    let repo = session.flags.string("repo").trim();
    let reference_flag = session.flags.on("reference");
    if reference_flag && verb != "detach" {
        return Err(session.fail("--reference only removes an existing linked path"));
    }
    let extras = !repo.is_empty()
        || !session.flags.string("notebook").trim().is_empty()
        || !session.flags.string("url").trim().is_empty();
    if reference_flag && (path.is_empty() || extras) {
        return Err(session.fail("--reference removes one old linked --path without --repo"));
    }
    let reference = artifact_reference(&session).map_err(|error| session.fail(&error))?;
    let kind = if verb == "detach" {
        NOTE_KIND_DETACH
    } else {
        NOTE_KIND_ATTACH
    };
    session.scope()?;
    let body = session.text()?;
    let mut message = note_message(&mut session, seed_id, body)?;
    message.kind = Some(kind.to_string());
    message.artifact = Some(reference.clone());
    let reply = session.call(
        &message,
        "seed_note_result",
        "the daemon accepted the note but returned nothing",
    )?;
    if session.flags.on("json") {
        return session.print_json(&reply, &["seed_note_result", "note"]);
    }
    let result: SeedNoteResult = session.read_field(&reply, "seed_note_result")?;
    let moved = default_note_body(kind, &reference);
    let mut out = format!("{seed_id} {moved}\n");
    let body = result.note.body.trim();
    if !body.is_empty() && body != moved {
        let _ = writeln!(out, "{body}");
    }
    print_stdout(&out);
    Ok(())
}

fn transfer_artifact(
    session: &mut Session<'_, '_>,
    seed_id: &str,
    plan: &TransferPlan,
) -> Result<(), Stop> {
    let mut message = SeedArtifactTransferMessage::new();
    message.seed_id = seed_id.to_string();
    message.operation = plan.operation.to_string();
    message.source_path = non_empty(&plan.source);
    message.filename = non_empty(&plan.filename);
    message.destination_path = non_empty(&plan.destination);
    message.source_session_id = non_empty(&session.session_id()?);
    let reply = session.call(
        &message,
        "seed_artifact_transfer_result",
        "the daemon accepted the artifact transfer but returned no receipt",
    )?;
    let Some(result) = result_or_json::<SeedArtifactTransferResult>(
        session,
        &reply,
        "seed_artifact_transfer_result",
    )?
    else {
        return Ok(());
    };
    let mut out = format!(
        "{} {}\nto {}\n",
        plan.operation, result.source_path, result.destination_path
    );
    if result.replaced == Some(true) {
        let base = Path::new(&result.destination_path)
            .file_name()
            .map_or_else(String::new, |name| name.to_string_lossy().into_owned());
        let _ = writeln!(out, "replaced the seed's earlier {base}");
    }
    if plan.operation != "detach" {
        let _ = writeln!(out, "markdown target {}", result.relative_target);
    }
    print_stdout(&out);
    Ok(())
}

fn notes(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "notes");
    let positionals = session.parse(args)?;
    let id = single_id(&session, &positionals, |count| {
        format!("needs exactly one seed id, got {count}: attn seed notes s-7k3f9m")
    })?
    .to_string();
    let kind = session.flags.string("kind").trim().to_string();
    let since = bookmark(&session)?;
    let mut message = SeedNotesMessage::new();
    message.seed_id.clone_from(&id);
    message.since = since;
    let limit = session.flags.int("limit");
    if limit > 0 {
        message.limit = Some(limit);
    }
    message.kind = non_empty(&kind);
    message.source_session_id = non_empty(&session.session_id()?);
    let reply = session.call(
        &message,
        "seed_notes_result",
        "the daemon answered without a log",
    )?;
    let Some(result) =
        result_or_json::<SeedNotesResult>(&mut session, &reply, "seed_notes_result")?
    else {
        return Ok(());
    };
    let mut out = String::new();
    if let Some(after) = since
        && result.expired.is_none()
    {
        render::since(&mut out, after, &result.notes);
        render::bookmark_line(&mut out, "notes", &id, result.bookmark, &kind);
        print_stdout(&out);
        return Ok(());
    }
    if let Some(expired) = &result.expired {
        render::expired_bookmark(&mut out, since.unwrap_or_default(), expired);
    }
    if !result.notes.is_empty() {
        render::notes(
            &mut out,
            &result.notes,
            &id,
            result.total - render::count(result.notes.len()),
        );
    } else if !kind.is_empty() {
        let _ = writeln!(out, "no {kind} entries on this seed's log");
    } else {
        let _ = writeln!(
            out,
            "nothing on this seed's log yet — `attn seed note {id} -m \"what happened\"` starts it"
        );
    }
    out.push('\n');
    render::bookmark_line(&mut out, "notes", &id, result.bookmark, &kind);
    print_stdout(&out);
    Ok(())
}

fn link(run: &mut Run<'_>, verb: &str, args: &[String]) -> Result<(), Stop> {
    let unlink = verb == "unlink";
    let mut session = Session::new(run, verb);
    let positionals = session.parse(args)?;
    let [seed_id, kind, to] = positionals.as_slice() else {
        return Err(session.usage(&format!(
            "reads as a sentence: `attn seed {verb} s-7k3f9m {EDGE_BLOCKS} s-2p4qxv`, where the kind is {}",
            LINKABLE_KINDS.join(" or ")
        )));
    };
    let mut message = SeedLinkMessage::new();
    message.seed_id.clone_from(seed_id);
    message.kind.clone_from(kind);
    message.to_seed_id.clone_from(to);
    message.unlink = unlink.then_some(true);
    let reply = session.call(
        &message,
        "seed_link_result",
        "the daemon accepted the edge but returned no seed",
    )?;
    let Some(result) = result_or_json::<SeedLinkResult>(&mut session, &reply, "seed_link_result")?
    else {
        return Ok(());
    };
    let line = if unlink {
        format!("{seed_id} no longer {kind} {to}")
    } else if !result.changed {
        format!("{seed_id} already {kind} {to}")
    } else {
        format!("{seed_id} {kind} {to}")
    };
    print_stdout(&format!("{line}\n"));
    Ok(())
}

fn ready_result(
    session: &mut Session<'_, '_>,
    plot: &str,
    all: bool,
) -> Result<crate::client::Reply, Stop> {
    let mut message = SeedReadyMessage::new();
    message.source_session_id = non_empty(&session.session_id()?);
    message.plot = non_empty(plot);
    message.all = all.then_some(true);
    session.call(
        &message,
        "seed_ready_result",
        "the daemon answered without a ready list",
    )
}

fn ready(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "ready");
    let positionals = session.parse(args)?;
    if let Some(first) = positionals.first() {
        return Err(session.usage(&format!(
            "takes no arguments, got {}; scope it with --plot <plot> or --all",
            quote(first.as_bytes())
        )));
    }
    let plot = session.flags.string("plot").trim().to_string();
    let all = session.flags.on("all");
    let reply = ready_result(&mut session, &plot, all)?;
    let Some(result) =
        result_or_json::<SeedReadyResult>(&mut session, &reply, "seed_ready_result")?
    else {
        return Ok(());
    };
    print_stdout(&render::ready(&result));
    Ok(())
}

fn prime(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "prime");
    let positionals = session.parse(args)?;
    if let Some(first) = positionals.first() {
        return Err(session.usage(&format!(
            "takes no arguments, got {}",
            quote(first.as_bytes())
        )));
    }
    let reply = ready_result(&mut session, "", false)?;
    let result: SeedReadyResult = session.read_field(&reply, "seed_ready_result")?;
    print_stdout(&render::prime(&result));
    Ok(())
}

fn guide(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "guide");
    if !session.parse(args)?.is_empty() {
        return Err(session.usage("takes no arguments"));
    }
    print_stdout(AUTHORING_AGENT_SEED_GUIDE);
    Ok(())
}

#[derive(Serialize)]
struct ExportedFile<'a> {
    seed_id: &'a str,
    path: &'a str,
    bytes: usize,
}

fn export(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let mut session = Session::new(run, "export");
    let positionals = session.parse(args)?;
    let reference = single_id(&session, &positionals, |count| {
        format!("needs exactly one seed id, got {count}: attn seed export s-7k3f9m")
    })?
    .to_string();
    let seed = show_seed(&mut session, &reference, None)?.seed;
    let rendered = body_stamp(&seed.id, &seed.body);
    let out = session.flags.string("out").trim().to_string();
    if out == "-" {
        print_stdout(&rendered);
        return Ok(());
    }
    let path = if out.is_empty() {
        format!("{}.md", seed.id)
    } else {
        out
    };
    if let Err(error) = write_file(&path, rendered.as_bytes()) {
        return Err(session.fail(&format!(
            "writing {path}: open {path}: {}",
            crate::paths::os_error_text(&error)
        )));
    }
    let resolved = absolute(&path).unwrap_or_else(|_| path.clone());
    if session.flags.on("json") {
        let encoded = attn::go::json::encode(&ExportedFile {
            seed_id: &seed.id,
            path: &resolved,
            bytes: rendered.len(),
        })
        .expect("a plain struct always encodes");
        print_json(&encoded);
        return Ok(());
    }
    print_stdout(&format!(
        "wrote {resolved}; edit it with any tool, keep its first line, then: attn seed edit {} -m - < {resolved}\n",
        seed.id
    ));
    Ok(())
}

fn write_file(path: &str, contents: &[u8]) -> std::io::Result<()> {
    use std::io::Write as _;
    use std::os::unix::fs::OpenOptionsExt;
    let mut file = std::fs::OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .mode(0o644)
        .open(path)?;
    file.write_all(contents)
}
