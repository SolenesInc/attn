mod help_generated;
mod table_generated;

use super::{Run, Stop, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn::go::tabwriter;
use attn_protocol::go_values::{FEATURES, SETTINGS, Setting};
use attn_protocol::{SettingEntry, SettingsListResult};
use std::collections::BTreeMap;
use std::fmt::Write as _;

const SCOPE_PROFILE: &str = "profile";

struct Settings;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Settings, args)
}

impl Family for Settings {
    fn group(&self) -> &'static str {
        "settings"
    }

    fn flags(&self) -> &'static [&'static str] {
        table_generated::FLAGS
    }

    fn commands(&self) -> &'static [Command] {
        table_generated::COMMANDS
    }

    fn group_help(&self) -> &'static str {
        help_generated::GROUP_HELP
    }

    fn help(&self, verb: &str) -> &'static str {
        help_generated::help(verb)
    }

    fn usage(&self, verb: &str) -> &'static str {
        help_generated::usage(verb)
    }

    fn before_send(&self, cmd: &Command, parsed: &mut Parsed) -> Result<(), Stop> {
        if parsed.on("features") && (parsed.on("all") || parsed.json) {
            return Err(self.usage_error(cmd.name, "--features takes no other flag"));
        }
        Ok(())
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        let label = format!("settings {}", cmd.name);
        if cmd.name != "list" {
            let entry: SettingEntry = result
                .whole()
                .map_err(|error| run.fail_send(&label, &error))?;
            let value = entry.value.unwrap_or_default();
            if cmd.name == "set" {
                print_stdout(&format!("{} = {value}\n", entry.key));
            } else {
                print_stdout(&format!("{value}\n"));
            }
            return Ok(());
        }
        let list: SettingsListResult = result
            .whole()
            .map_err(|error| run.fail_send(&label, &error))?;
        if let Some(reason) = list
            .profile_unresolved
            .as_deref()
            .filter(|reason| !reason.is_empty())
        {
            eprintln!("settings: profile settings left out: {reason}");
        }
        let table = if parsed.on("features") {
            features_table(&list)
        } else {
            settings_table(&list)
        };
        print_stdout(&tabwriter::format(&table));
        Ok(())
    }
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
