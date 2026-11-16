//! What the launcher's text names: a search, a folder to browse, either aimed at a machine by
//! a `<machine>:` prefix, or a prefix that names more than one machine.

use attn_protocol::OutpostInfo;

/// A machine a start can go to: this one, or an outpost.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Machine {
    pub(crate) host: Option<String>,
    pub(crate) name: String,
    /// What an address writes before its `:`: the name, or the id when another outpost shares
    /// the name.
    pub(crate) label: String,
}

pub(crate) fn this_mac() -> Machine {
    Machine {
        host: None,
        name: "This Mac".into(),
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

/// The machines the longest `<label>:` at the start of `query` names, and that prefix.
fn named(query: &str, outposts: &[OutpostInfo]) -> Option<(Vec<Machine>, String)> {
    let lower = query.to_lowercase();
    let labels = outposts
        .iter()
        .flat_map(|o| {
            let machine = machine_for(o, outposts);
            [o.name.clone(), o.id.clone()].map(|label| (label, machine.clone()))
        })
        .chain(std::iter::once(("home".to_owned(), this_mac())));
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

pub(crate) fn parse(query: &str, outposts: &[OutpostInfo]) -> Address {
    let addressed = named(query, outposts);
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
            machine: machine.unwrap_or_else(this_mac),
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
