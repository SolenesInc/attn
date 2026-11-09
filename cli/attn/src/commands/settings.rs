use super::support::{
    SessionLookup, bool_flag, client, current_session, has_help_flag, parse_interspersed,
    string_flag,
};
use super::{Run, Stop, print_reply_field, print_stdout};
use crate::client::SendError;
use attn::go::flag::{FlagError, FlagSet};
use attn::go::quote::quote;
use attn::go::tabwriter;
use attn_protocol::go_values::{FEATURES, SETTINGS, Setting};
use attn_protocol::{GetSettingsMessage, SetSettingMessage, SettingEntry, SettingsListResult};
use std::collections::BTreeMap;
use std::fmt::Write as _;

const HELP: &str = r#"usage: attn settings <command> [--profile <name|id>]

Read and change settings. Most apply to all profiles; a few apply to one
profile, and list shows which. Inside an attn session, profile settings belong
to the session's profile; outside, --profile chooses one when several exist.

commands:
  list [--all] [--json]  show keys, scopes, values and descriptions;
                       --all adds read-only values attn computes
  list --features       show each experimental feature, whether it is on,
                       and when a change applies
  get <key> [--json]    show the value in effect; empty when unset, unless
                       the setting is on by default
  set <key> <value>     change a setting; "" restores the default for a
                       true or false setting, or if the description allows it
"#;

const SCOPE_PROFILE: &str = "profile";

fn usage(message: &str) -> Stop {
    eprint!("settings: {message}\n{HELP}");
    Stop(2)
}

fn refused(run: &mut Run<'_>, error: &SendError) -> Stop {
    run.fail_send("settings", error)
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    if args.is_empty() || has_help_flag(args) {
        print_stdout(HELP);
        return Ok(());
    }
    let verb = args[0].as_str();
    if matches!(verb, "list" | "get" | "set") {
        run.path = format!("settings {verb}");
    }
    let mut flags = FlagSet::new(
        "settings",
        &[
            string_flag("profile"),
            bool_flag("json"),
            bool_flag("all"),
            bool_flag("features"),
        ],
    );
    let positionals =
        parse_interspersed(run, &mut flags, &args[1..]).map_err(|error| match error {
            FlagError::Help | FlagError::Invalid(_) => usage(&error.to_string()),
        })?;
    let json = flags.bool("json");
    let all = flags.bool("all");
    let features = flags.bool("features");
    validate(run, verb, &positionals, json, all, features)?;
    let session = match current_session(run) {
        Ok(SessionLookup::Found(session)) => session,
        Ok(SessionLookup::NotShowing(_)) => String::new(),
        Err(error) => return Err(refused(run, &error)),
    };
    let source_session_id = (!session.is_empty()).then_some(session);
    let profile = flags.string("profile").trim().to_string();
    let profile_id = (!profile.is_empty()).then_some(profile);
    if verb == "set" {
        let mut message = SetSettingMessage::new();
        message.source_session_id = source_session_id;
        message.profile_id = profile_id;
        message.key.clone_from(&positionals[0]);
        message.value.clone_from(&positionals[1]);
        let reply = client(run)
            .call(&message)
            .map_err(|error| refused(run, &error))?;
        if json {
            return print_reply_field(&reply, &["setting"]).map_err(|error| refused(run, &error));
        }
        let entry: SettingEntry = reply
            .value("setting")
            .map_err(|error| refused(run, &error))?;
        print_stdout(&format!(
            "{} = {}\n",
            entry.key,
            entry.value.unwrap_or_default()
        ));
        return Ok(());
    }
    let mut message = GetSettingsMessage::new();
    message.source_session_id = source_session_id;
    message.profile_id = profile_id;
    message.key = Some(positionals.first().cloned().unwrap_or_default());
    message.all = Some(all);
    let reply = client(run)
        .call(&message)
        .map_err(|error| refused(run, &error))?;
    if verb == "get" {
        if json {
            return print_reply_field(&reply, &["setting"]).map_err(|error| refused(run, &error));
        }
        let entry: SettingEntry = reply
            .value("setting")
            .map_err(|error| refused(run, &error))?;
        print_stdout(&format!("{}\n", entry.value.unwrap_or_default()));
        return Ok(());
    }
    if json {
        return print_reply_field(&reply, &["settings_list"]).map_err(|error| refused(run, &error));
    }
    let result: SettingsListResult = reply
        .value("settings_list")
        .map_err(|error| refused(run, &error))?;
    if let Some(reason) = result
        .profile_unresolved
        .as_deref()
        .filter(|reason| !reason.is_empty())
    {
        eprintln!("settings: profile settings left out: {reason}");
    }
    let table = if features {
        features_table(&result)
    } else {
        settings_table(&result)
    };
    print_stdout(&tabwriter::format(&table));
    Ok(())
}

fn validate(
    run: &mut Run<'_>,
    verb: &str,
    positionals: &[String],
    json: bool,
    all: bool,
    features: bool,
) -> Result<(), Stop> {
    match verb {
        "list" => {
            if !positionals.is_empty() {
                return Err(usage("list takes no arguments"));
            }
            if features && (all || json) {
                return Err(usage("list --features takes no other flags"));
            }
        }
        "get" if positionals.len() != 1 || all || features => {
            return Err(usage("get needs one key"));
        }
        "set" if positionals.len() != 2 || all || features => {
            return Err(usage("set needs a key and value"));
        }
        "get" | "set" => {}
        _ => {
            run.usage_error = Some(super::USAGE_UNKNOWN_COMMAND);
            return Err(usage(&format!(
                "unknown command {}",
                quote(verb.as_bytes())
            )));
        }
    }
    Ok(())
}

fn features_table(result: &SettingsListResult) -> String {
    let values: BTreeMap<&str, &str> = result
        .entries
        .iter()
        .map(|entry| (entry.key.as_str(), entry.value.as_deref().unwrap_or("")))
        .collect();
    let mut out = String::new();
    for feature in FEATURES {
        let state = if values.get(feature.key) == Some(&"true") {
            "on"
        } else {
            "off"
        };
        let _ = writeln!(
            out,
            "{}\t{state}\t{}\t{}\t{}",
            feature.key, feature.stage, feature.applies, feature.name
        );
    }
    out
}

fn is_machine(key: &str) -> bool {
    let family = |setting: &&Setting| setting.key.contains('<');
    let led_by_pattern = |setting: &&Setting| setting.key.starts_with('<');
    let exact = SETTINGS.iter().find(|setting| setting.key == key);
    let prefix_led = || {
        SETTINGS
            .iter()
            .filter(family)
            .filter(|setting| !led_by_pattern(setting))
            .find(|setting| pattern_matches(setting.key, key))
    };
    let pattern_led = || {
        SETTINGS
            .iter()
            .filter(family)
            .filter(led_by_pattern)
            .find(|setting| pattern_matches(setting.key, key))
    };
    exact
        .or_else(prefix_led)
        .or_else(pattern_led)
        .is_some_and(|setting| setting.machine)
}

fn pattern_matches(pattern: &str, key: &str) -> bool {
    let (mut pattern, mut key) = (pattern, key);
    loop {
        let Some(start) = pattern.find('<') else {
            return pattern == key;
        };
        let Some(rest) = key.strip_prefix(&pattern[..start]) else {
            return false;
        };
        key = rest;
        let end = pattern[start..]
            .find('>')
            .map_or(start, |offset| offset + start);
        pattern = &pattern[end + 1..];
        let suffix = pattern.find('<').map_or(pattern, |next| &pattern[..next]);
        if suffix.is_empty() {
            return !key.is_empty();
        }
        match key.find(suffix) {
            Some(at) if at > 0 => key = &key[at..],
            _ => return false,
        }
    }
}
fn settings_table(result: &SettingsListResult) -> String {
    let mut out = String::new();
    for entry in &result.entries {
        let mut scope = if entry.scope == SCOPE_PROFILE {
            format!("profile {}", result.profile.as_deref().unwrap_or(""))
                .trim()
                .to_string()
        } else if is_machine(&entry.key) {
            "this machine".to_string()
        } else {
            "home".to_string()
        };
        if entry.read_only == Some(true) {
            scope.push_str(", read-only");
        }
        if entry.set_at_home == Some(true) {
            scope.push_str(", set at the home");
        }
        let value = entry
            .value
            .as_deref()
            .filter(|value| !value.is_empty())
            .unwrap_or("-");
        let _ = writeln!(
            out,
            "{}\t{scope}\t{value}\t{}",
            entry.key, entry.description
        );
    }
    out
}
