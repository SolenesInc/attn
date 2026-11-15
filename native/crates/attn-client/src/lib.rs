mod clock;
mod frame;
mod link;
mod pulse;
mod store;

pub use clock::{Clock, ManualClock};
pub use frame::{Frame, KittyFormat};
pub use link::{
    Commands, LIST_PLUGINS, Link, LinkConfig, NotConnected, Parked, REFRESH_PRS_ID, Reply,
    ReplySink, StartDaemon, TerminalEvent, TerminalSink, Traffic, plugin_action, pr_action_id,
};
pub use pulse::Pulse;
pub use store::{
    CLOSES_KEPT, Closes, Connection, Facts, Garden, GitHub, MigrationMarker, Notifications,
    PendingMigration, Plugins, Profiles, PullRequests, Refusal, SWEEPS_KEPT, Slices, Store,
    Subscription, TileContent, TileContents, TileKey, WorktreePush, WorktreePushes,
};

pub const CLIENT_KIND: &str = "native-app";
