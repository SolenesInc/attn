use std::collections::BTreeMap;

/// Where an edit stands against what the daemon acknowledged.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Phase {
    Loading,
    Saved,
    Dirty,
    Saving,
    Error,
    Conflict,
}

/// How a kind of edit compares a draft with what the daemon holds.
pub(crate) trait Spec {
    type Draft: Clone;
    type Ack: Clone;
    fn equal(draft: &Self::Draft, ack: &Self::Ack) -> bool;
    fn settled(ack: &Self::Ack) -> Self::Draft;
    /// Whether `candidate` is older than `current`, so it is ignored.
    fn stale(_candidate: &Self::Ack, _current: &Self::Ack) -> bool {
        false
    }
}

/// What a write came back with.
pub(crate) enum Sent<A> {
    Ack(A),
    /// The daemon holds another version; it is the user's choice which wins.
    Conflict(A),
    /// The daemon refused the write, with what it holds when it said.
    Refused(String, Option<A>),
    /// The write never reached the daemon, or its answer never came back.
    Lost(String),
}

pub(crate) struct Edit<S: Spec> {
    pub(crate) draft: S::Draft,
    pub(crate) acknowledged: Option<S::Ack>,
    pub(crate) conflict: Option<S::Ack>,
    pub(crate) phase: Phase,
    pub(crate) error: Option<String>,
    /// A write went out whose outcome is unknown: the daemon may or may not hold it.
    uncertain: bool,
    generation: u64,
    /// Rises with each version the daemon acknowledges, so a read can tell one landed.
    acks: u64,
    retry_on_reconnect: bool,
    active: bool,
}

/// One edit per member, each written to the daemon one write at a time.
pub(crate) struct Edits<S: Spec> {
    entries: BTreeMap<String, Edit<S>>,
}

impl<S: Spec> Default for Edits<S> {
    fn default() -> Self {
        Edits {
            entries: BTreeMap::new(),
        }
    }
}

/// A write to send: the draft, against the version it was made on.
pub(crate) struct Write<S: Spec> {
    pub(crate) draft: S::Draft,
    pub(crate) acknowledged: S::Ack,
    pub(crate) generation: u64,
}

impl<S: Spec> Edits<S> {
    pub(crate) fn get(&self, member: &str) -> Option<&Edit<S>> {
        self.entries.get(member)
    }

    fn settle(entry: &mut Edit<S>, ack: S::Ack) {
        entry.draft = S::settled(&ack);
        entry.acknowledged = Some(ack);
        entry.acks += 1;
        entry.conflict = None;
        entry.phase = Phase::Saved;
        entry.error = None;
        entry.retry_on_reconnect = false;
    }

    /// Starts reading `member`, unless what it holds is already acknowledged.
    pub(crate) fn loading(&mut self, member: &str, draft: S::Draft) {
        let entry = self.entries.entry(member.to_owned()).or_insert(Edit {
            draft,
            acknowledged: None,
            conflict: None,
            phase: Phase::Loading,
            error: None,
            uncertain: false,
            generation: 0,
            acks: 0,
            retry_on_reconnect: false,
            active: false,
        });
        if entry.acknowledged.is_none() {
            entry.phase = Phase::Loading;
            entry.error = None;
        }
    }

    /// A read that failed before anything was acknowledged.
    pub(crate) fn fail(&mut self, member: &str, error: String) {
        if let Some(entry) = self.entries.get_mut(member)
            && entry.acknowledged.is_none()
        {
            entry.phase = Phase::Error;
            entry.error = Some(error);
        }
    }

    /// The edit's version, so a read that answers after a write or an edit can tell.
    pub(crate) fn fence(&self, member: &str) -> Option<(u64, u64)> {
        self.entries
            .get(member)
            .filter(|e| !e.active)
            .map(|e| (e.generation, e.acks))
    }

    /// Whether `fence` still names the edit as it is: nothing was written or typed since.
    pub(crate) fn current(&self, member: &str, fence: Option<(u64, u64)>) -> bool {
        fence.is_some() && self.fence(member) == fence
    }

    /// Takes what the daemon holds: an edit with nothing of its own shows it, one with its own
    /// change keeps the change against it.
    pub(crate) fn adopt(&mut self, member: &str, ack: S::Ack) {
        let Some(entry) = self.entries.get_mut(member) else {
            self.entries.insert(
                member.to_owned(),
                Edit {
                    draft: S::settled(&ack),
                    acknowledged: Some(ack),
                    conflict: None,
                    phase: Phase::Saved,
                    error: None,
                    uncertain: false,
                    generation: 0,
                    acks: 0,
                    retry_on_reconnect: false,
                    active: false,
                },
            );
            return;
        };
        if entry
            .acknowledged
            .as_ref()
            .is_some_and(|held| S::stale(&ack, held))
        {
            return;
        }
        let idle = !entry.active && !entry.uncertain;
        let clean = matches!(entry.phase, Phase::Saved | Phase::Loading)
            || (idle && S::equal(&entry.draft, &ack));
        if clean {
            Self::settle(entry, ack);
        } else {
            entry.acknowledged = Some(ack);
            entry.acks += 1;
        }
    }

    pub(crate) fn conflict(&mut self, member: &str, external: S::Ack) {
        if let Some(entry) = self.entries.get_mut(member) {
            entry.conflict = Some(external);
            entry.phase = Phase::Conflict;
            entry.error = None;
            entry.uncertain = false;
            entry.retry_on_reconnect = false;
        }
    }

    /// The user's new draft; whether it needs a write now.
    pub(crate) fn update(&mut self, member: &str, draft: S::Draft) -> bool {
        let Some(entry) = self.entries.get_mut(member) else {
            return false;
        };
        let Some(ack) = &entry.acknowledged else {
            return false;
        };
        let dirty = entry.uncertain || !S::equal(&draft, ack);
        entry.draft = draft;
        entry.conflict = None;
        entry.phase = if entry.active {
            Phase::Saving
        } else if dirty {
            Phase::Dirty
        } else {
            Phase::Saved
        };
        entry.error = None;
        entry.generation += 1;
        entry.retry_on_reconnect = false;
        !entry.active && dirty
    }

    /// The next write for `member`, when one is due and none is out; settles an edit that
    /// came back to what the daemon holds.
    pub(crate) fn start(&mut self, member: &str) -> Option<Write<S>> {
        let entry = self.entries.get_mut(member)?;
        if entry.active || entry.conflict.is_some() {
            return None;
        }
        let ack = entry.acknowledged.clone()?;
        if !entry.uncertain && S::equal(&entry.draft, &ack) {
            Self::settle(entry, ack);
            return None;
        }
        entry.phase = Phase::Saving;
        entry.error = None;
        entry.active = true;
        Some(Write {
            draft: entry.draft.clone(),
            acknowledged: ack,
            generation: entry.generation,
        })
    }

    /// Whether a write is out for `member`.
    pub(crate) fn writing(&self, member: &str) -> bool {
        self.entries.get(member).is_some_and(|e| e.active)
    }

    /// Takes a write's outcome; whether another write should follow.
    pub(crate) fn finish(&mut self, member: &str, generation: u64, sent: Sent<S::Ack>) -> bool {
        let Some(entry) = self.entries.get_mut(member) else {
            return false;
        };
        entry.active = false;
        let held = entry.acknowledged.clone();
        let (ack, error) = match sent {
            Sent::Conflict(external) => {
                self.conflict(member, external);
                return false;
            }
            Sent::Lost(error) => {
                entry.phase = Phase::Error;
                entry.error = Some(error);
                entry.uncertain = true;
                entry.retry_on_reconnect = true;
                return false;
            }
            Sent::Ack(ack) => (Some(ack), None),
            Sent::Refused(error, ack) => (ack, Some(error)),
        };
        let fresh = match (&ack, &held) {
            (Some(ack), Some(held)) => !S::stale(ack, held),
            (Some(_), None) => true,
            (None, _) => false,
        };
        let next = if fresh { ack } else { held };
        if fresh {
            entry.uncertain = false;
        }
        if let Some(error) = error {
            entry.acknowledged = next;
            entry.acks += 1;
            entry.phase = Phase::Error;
            entry.error = Some(error);
            entry.retry_on_reconnect = false;
            return false;
        }
        let Some(next) = next else { return false };
        if entry.generation == generation {
            Self::settle(entry, next);
            return false;
        }
        entry.acknowledged = Some(next);
        entry.acks += 1;
        true
    }

    /// Takes the file's version over the user's edit.
    pub(crate) fn use_external(&mut self, member: &str) {
        if let Some(entry) = self.entries.get_mut(member)
            && let Some(external) = entry.conflict.take()
        {
            Self::settle(entry, external);
            entry.uncertain = false;
        }
    }

    /// Keeps the user's edit over the file's version; whether a write is due.
    pub(crate) fn keep_mine(&mut self, member: &str) -> bool {
        let Some(entry) = self.entries.get_mut(member) else {
            return false;
        };
        let Some(external) = entry.conflict.take() else {
            return false;
        };
        entry.acknowledged = Some(external);
        entry.acks += 1;
        entry.phase = Phase::Dirty;
        entry.error = None;
        true
    }

    /// The members whose write was lost with the connection, to write again on reconnect.
    pub(crate) fn lost_to_the_connection(&self) -> Vec<String> {
        self.entries
            .iter()
            .filter(|(_, e)| e.retry_on_reconnect)
            .map(|(m, _)| m.clone())
            .collect()
    }
}

/// What a lost answer says, by why it was lost.
pub(crate) fn lost(answer: &crate::requests::Answer) -> Option<String> {
    match answer {
        crate::requests::Answer::NotSent => Some("not connected to the daemon".into()),
        crate::requests::Answer::Dropped => {
            Some("the connection closed before the daemon answered".into())
        }
        crate::requests::Answer::Reply(_) => None,
    }
}
