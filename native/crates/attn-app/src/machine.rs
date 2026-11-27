//! Which machine a daemon path is on, as this app sees it: the app's own, the home's when the
//! home runs elsewhere, or an outpost's. The wire leaves `host` absent for the home's machine.

use std::borrow::Cow;
use std::collections::BTreeMap;

use attn_client::{AppMachine, Facts, Reach};
use attn_protocol::OutpostInfo;

pub(crate) const THIS_MAC: &str = "This Mac";

#[derive(Clone, Copy)]
pub(crate) struct Machines<'a> {
    pub(crate) outposts: &'a [OutpostInfo],
    pub(crate) app: &'a AppMachine,
    /// The name of the home's own machine, which an address names it by as well as `home`.
    pub(crate) home_name: &'a str,
}

impl<'a> Machines<'a> {
    pub(crate) fn of(facts: &'a Facts) -> Machines<'a> {
        Machines {
            outposts: &facts.outposts,
            app: &facts.machine,
            home_name: &facts.home_name,
        }
    }

    /// Whether a path on `host` is on this app's disk.
    pub(crate) fn on_app_machine(&self, host: Option<&str>) -> bool {
        match host.filter(|h| !h.is_empty()) {
            None => self.app.home.is_none(),
            Some(host) => host == self.app.id,
        }
    }

    /// The `host` that names this app's machine on the wire: absent while the home runs here.
    pub(crate) fn app_host(&self) -> Option<String> {
        self.app.home.as_ref().map(|_| self.app.id.clone())
    }

    /// The name of the machine `host` names, or none for this app's own machine.
    pub(crate) fn away(&self, host: Option<&str>) -> Option<String> {
        if self.on_app_machine(host) {
            return None;
        }
        Some(match host.filter(|h| !h.is_empty()) {
            None => self.app.home.clone().unwrap_or_default(),
            Some(host) => crate::desktop::outpost_name(host, self.outposts),
        })
    }

    /// The name of the machine `host` names, this one included.
    pub(crate) fn name(&self, host: Option<&str>) -> String {
        self.away(host).unwrap_or_else(|| THIS_MAC.to_owned())
    }

    /// Where the home runs, as a sentence says it: the home's machine by name, or "this machine".
    pub(crate) fn home_place(&self) -> String {
        self.away(None).unwrap_or_else(|| "this machine".to_owned())
    }

    /// Whether more than one machine is in play, so that rows name theirs.
    pub(crate) fn several(&self) -> bool {
        !self.outposts.is_empty() || self.app.home.is_some()
    }

    /// Why a path on `host` cannot be opened on this machine, when it cannot.
    pub(crate) fn refuse_open(&self, path: &str, host: Option<&str>) -> Option<String> {
        self.away(host).map(|name| {
            format!("{path} is on {name}; opening files on another machine isn't supported yet")
        })
    }
}

/// The settings about the machine the app runs on, which that machine's own daemon keeps.
const OWN_SETTINGS: [&str; 2] = [
    crate::quick_capture::shortcut::SETTING,
    crate::settings::EDITOR,
];

/// The daemon that keeps `key` for this app: its own machine's for a setting about that
/// machine while the home runs elsewhere, the home otherwise.
pub(crate) fn reach(facts: &Facts, key: &str) -> Reach {
    if facts.machine.home.is_some() && OWN_SETTINGS.contains(&key) {
        Reach::Machine
    } else {
        Reach::Home
    }
}

/// Whether the daemon that keeps `key` has given the app its state.
pub(crate) fn heard(facts: &Facts, key: &str) -> bool {
    match reach(facts, key) {
        Reach::Machine => facts.machine.connected,
        Reach::Home => facts.connection == attn_client::Connection::Connected,
    }
}

/// The settings as this app reads them: the home's, with this machine's own for the settings
/// about it while the home runs elsewhere.
pub(crate) fn settings(facts: &Facts) -> Cow<'_, BTreeMap<String, String>> {
    if facts.machine.home.is_none() {
        return Cow::Borrowed(&facts.settings);
    }
    let mut settings = (*facts.settings).clone();
    for key in OWN_SETTINGS {
        match facts.machine.settings.get(key) {
            Some(value) => settings.insert(key.to_owned(), value.clone()),
            None => settings.remove(key),
        };
    }
    Cow::Owned(settings)
}

/// Sends `command` to the daemon that keeps `key`, with no answer awaited.
pub(crate) fn send_setting(
    state: &crate::State,
    key: &str,
    command: &attn_protocol::SetSettingMessage,
) -> Result<(), attn_client::NotConnected> {
    match reach(&state.facts, key) {
        Reach::Machine => state.terminals.links.machine.send(command),
        Reach::Home => state.daemon.send(command),
    }
}
