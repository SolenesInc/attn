//! The automation protocol: tests, scenarios, `scripts/vm drive` and drive-attn read a live
//! app's elements and feed it input through it. The same requests drive the app in a wire
//! test, in a windowless process and in its window; each [`Host`] runs them on its `Runtime`.

mod act;
mod chord;
mod headless;
mod host;
mod server;
mod target;

pub use act::{Attempt, Hooks, Request, act};
pub use chord::key_event;
pub use headless::{Change, Headless, Work};
pub use host::{Host, Inspection, WindowInfo};
pub use server::{Manifest, Server};
pub use target::{Found, Target};
