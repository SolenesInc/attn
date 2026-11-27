mod clock;
mod frame;
pub mod layout;
mod link;
mod performance;
mod presence;
mod pulse;
pub mod setting;
mod store;
mod wall;

pub use clock::{Clock, ManualClock};
pub use frame::{Frame, KittyFormat};
pub use link::{
    Commands, FETCH_PR_DETAILS_ID, LIST_PLUGINS, Link, LinkConfig, NotConnected, Parked,
    REFRESH_PRS_ID, Reply, ReplySink, StartDaemon, TerminalEvent, TerminalSink, Traffic,
    context_cap_id, plugin_action, pr_action_id,
};
pub use performance::Performance;
pub use presence::Sight;
pub use pulse::Pulse;
pub use store::{
    CLOSES_KEPT, Closes, Connection, Desktop, Facts, Garden, GitHub, HomeUnreachable,
    MigrationMarker, Notifications, Plugins, Profiles, PullRequests, Refusal, SWEEPS_KEPT,
    SharedCodex, Slices, Store, Subscription, TileContent, TileContents, TileKey, WorktreePush,
    WorktreePushes,
};
pub use wall::WallClock;

pub const CLIENT_KIND: &str = "native-app";
