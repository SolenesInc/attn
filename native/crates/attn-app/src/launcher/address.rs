//! What the launcher's text names: a search, a folder to browse, either aimed at a machine by
//! a `<machine>:` prefix, or a prefix that names more than one machine.

use attn_protocol::OutpostInfo;

use crate::machine::Machines;

/// A machine a start can go to: the home's, or an outpost.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Machine {
    pub(crate) host: Option<String>,
    pub(crate) name: String,
    /// What an address writes before its `:`: the name, or the id when another outpost shares
    /// the name.
    pub(crate) label: String,
}

/// The home's machine, which `home:` names wherever it runs.
pub(crate) fn home(machines: &Machines) -> Machine {
    Machine {
        host: None,
        name: machines.name(None),
        label: "home".into(),
    }
}

pub(crate) fn machine_for(outpost: &OutpostInfo, outposts: &[OutpostInfo]) -> Machine {
    let lower = outpost.name.to_lowercase();
    let shared = outposts
        .iter()
        .any(|o| o.id != outpost.id && o.name.to_lowercase() == lower);
    Machine {
        host: Some(outpost.id.clone()),
        name: outpost.name.clone(),
        label: if shared {
            outpost.id.clone()
        } else {
            outpost.name.clone()
        },
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum Address {
    /// Recents whose path holds `text`; aimed at `machine` when the text named one.
    Search {
        text: String,
        machine: Option<Machine>,
    },
    /// The folders of a path that starts with `~` or `/`, on `machine`.
    Browse { path: String, machine: Machine },
    /// A prefix that names several machines.
    Ambiguous {
        prefix: String,
        machines: Vec<Machine>,
    },
}

fn is_browse_path(text: &str) -> bool {
    let trimmed = text.trim();
    trimmed.starts_with('~') || trimmed.starts_with('/')
}

/// The URL schemes that start a link, never an address.
const SCHEMES: [&str; 5] = ["http", "https", "mailto", "attn", "file"];

/// The machines the longest `<label>:` at the start of `query` names, and that prefix.
fn named(query: &str, machines: &Machines) -> Option<(Vec<Machine>, String)> {
    let lower = query.to_lowercase();
    if SCHEMES
        .iter()
        .any(|scheme| lower.starts_with(&format!("{scheme}:")))
    {
        return None;
    }
    let outposts = machines.outposts;
    let labels = outposts
        .iter()
        .flat_map(|o| {
            let machine = machine_for(o, outposts);
            [o.name.clone(), o.id.clone()].map(|label| (label, machine.clone()))
        })
        .chain(std::iter::once(("home".to_owned(), home(machines))));
    let matches: Vec<(String, Machine)> = labels
        .filter(|(label, _)| lower.starts_with(&format!("{}:", label.to_lowercase())))
        .collect();
    let longest = matches.iter().map(|(label, _)| label.len()).max()?;
    let mut machines: Vec<Machine> = Vec::new();
    for (label, machine) in matches {
        if label.len() == longest && !machines.iter().any(|m| m.host == machine.host) {
            machines.push(machine);
        }
    }
    let prefix = query.get(..longest + 1)?.to_owned();
    Some((machines, prefix))
}

/// The `<machine>:` that starts `query`, which the field shows in the accent colour: empty
/// when the text names no machine.
pub(crate) fn machine_prefix<'a>(query: &'a str, machines: &Machines) -> &'a str {
    named(query, machines).map_or("", |(_, prefix)| &query[..prefix.len()])
}

pub(crate) fn parse(query: &str, machines: &Machines) -> Address {
    let addressed = named(query, machines);
    if let Some((machines, prefix)) = &addressed
        && machines.len() > 1
    {
        return Address::Ambiguous {
            prefix: prefix.clone(),
            machines: machines.clone(),
        };
    }
    let (machine, prefix) = match addressed {
        Some((mut machines, prefix)) => (machines.pop(), prefix),
        None => (None, String::new()),
    };
    let rest = &query[prefix.len()..];
    if is_browse_path(rest) {
        return Address::Browse {
            path: rest.trim().to_owned(),
            machine: machine.unwrap_or_else(|| home(machines)),
        };
    }
    Address::Search {
        text: rest.trim().to_owned(),
        machine,
    }
}

/// The outposts whose name starts with `text`, while it names no machine yet.
pub(crate) fn machines_named<'a>(text: &str, outposts: &'a [OutpostInfo]) -> Vec<&'a OutpostInfo> {
    let lower = text.trim().to_lowercase();
    if lower.is_empty() || lower.contains(':') {
        return Vec::new();
    }
    outposts
        .iter()
        .filter(|o| o.name.to_lowercase().starts_with(&lower))
        .collect()
}

/// `path` written as an address on `machine`: as it is on this machine.
pub(crate) fn address_of(machine: &Machine, path: &str) -> String {
    match machine.host {
        Some(_) => format!("{}:{path}", machine.label),
        None => path.to_owned(),
    }
}

#[cfg(test)]
mod tests {
    use attn_client::AppMachine;
    use attn_protocol::OutpostInfo;
    use serde::Deserialize;

    use super::{Address, parse};
    use crate::machine::Machines;

    #[derive(Deserialize)]
    struct Table {
        machines: Vec<TableMachine>,
        names: Vec<Case>,
        addresses: Vec<Case>,
    }

    #[derive(Deserialize)]
    struct TableMachine {
        id: String,
        name: String,
        #[serde(default)]
        home: bool,
    }

    #[derive(Deserialize)]
    struct Case {
        text: String,
        machine: Option<String>,
        rest: Option<String>,
        shared: Option<Vec<String>>,
        #[serde(default)]
        unknown: bool,
        #[serde(default)]
        plain: bool,
        #[serde(default = "read_by_the_launcher")]
        launcher: bool,
    }

    fn read_by_the_launcher() -> bool {
        true
    }

    /// The table the Go resolver runs too, so the launcher reads a machine's name by the same rule.
    const NAMES: &str = include_str!("../../../../../internal/machinename/testdata/names.json");

    fn host_of(id: &str, home: &str) -> Option<String> {
        (id != home).then(|| id.to_owned())
    }

    fn check(case: &Case, query: &str, rest: &str, machines: &Machines, home: &str) {
        let got = parse(query, machines);
        if case.unknown || case.plain {
            assert_eq!(
                super::machine_prefix(query, machines),
                "",
                "{query}: {got:?}"
            );
            return;
        }
        if let Some(shared) = &case.shared {
            let want: Vec<Option<String>> = shared.iter().map(|id| host_of(id, home)).collect();
            match got {
                Address::Ambiguous { machines, .. } => {
                    let hosts: Vec<Option<String>> = machines.into_iter().map(|m| m.host).collect();
                    assert_eq!(hosts, want, "{query}");
                }
                other => panic!("{query}: {other:?}, want shared by {shared:?}"),
            }
            return;
        }
        let want = host_of(case.machine.as_deref().expect("a machine"), home);
        match got {
            Address::Browse { path, machine } => {
                assert_eq!(machine.host, want, "{query}");
                assert_eq!(path, rest, "{query}");
            }
            other => panic!("{query}: {other:?}, want {want:?}"),
        }
    }

    #[test]
    fn reads_names_as_the_daemon_does() {
        let table: Table = serde_json::from_str(NAMES).expect("names.json");
        let home = table.machines.iter().find(|m| m.home).expect("a home");
        let outposts: Vec<OutpostInfo> = table
            .machines
            .iter()
            .filter(|m| !m.home)
            .map(|m| OutpostInfo {
                id: m.id.clone(),
                name: m.name.clone(),
                ..OutpostInfo::default()
            })
            .collect();
        let app = AppMachine {
            id: home.id.clone(),
            ..AppMachine::default()
        };
        let machines = Machines {
            outposts: &outposts,
            app: &app,
        };
        for case in table.names.iter().filter(|c| c.launcher) {
            check(case, &format!("{}:/", case.text), "/", &machines, &home.id);
        }
        for case in table.addresses.iter().filter(|c| c.launcher) {
            check(
                case,
                &case.text,
                case.rest.as_deref().unwrap_or(""),
                &machines,
                &home.id,
            );
        }
    }
}
