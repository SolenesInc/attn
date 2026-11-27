//! Which machine a daemon path is on, as this app sees it: the app's own, the home's when the
//! home runs elsewhere, or an outpost's. The wire leaves `host` absent for the home's machine.

use attn_client::{AppMachine, Facts};
use attn_protocol::OutpostInfo;

pub(crate) const THIS_MAC: &str = "This Mac";

#[derive(Clone, Copy)]
pub(crate) struct Machines<'a> {
    pub(crate) outposts: &'a [OutpostInfo],
    pub(crate) app: &'a AppMachine,
}

impl<'a> Machines<'a> {
    pub(crate) fn of(facts: &'a Facts) -> Machines<'a> {
        Machines {
            outposts: &facts.outposts,
            app: &facts.machine,
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

    /// Why an agent cannot start in `path` on this machine, when its home runs on another.
    pub(crate) fn refuse_start_here(&self, path: &str) -> Option<String> {
        self.app.home.as_ref().map(|home| {
            format!("{path} is on {THIS_MAC}; {home} can't start agents on this machine yet")
        })
    }

    /// Why a path on `host` cannot be opened on this machine, when it cannot.
    pub(crate) fn refuse_open(&self, path: &str, host: Option<&str>) -> Option<String> {
        self.away(host).map(|name| {
            format!("{path} is on {name}; opening files on another machine isn't supported yet")
        })
    }
}
