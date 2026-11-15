use std::hash::{BuildHasher, Hasher};
use std::sync::Arc;

use attn_client::{Clock, LinkConfig, Pulse, ReplySink, TerminalSink};

use crate::daemon;
use crate::instance::Instance;

pub struct Launch {
    pub instance: Instance,
    pub client_token: String,
    pub host_token: String,
}

/// Everything the app needs before its first window: the baked instance, its lock and the two
/// credentials its hello carries. The link starts the daemon. Call from `main` before any
/// thread starts.
pub fn prepare() -> Result<Launch, String> {
    let instance = Instance::baked()?;
    instance.route_environment();
    instance.hold_app_lock()?;
    let launch = Launch {
        client_token: instance.ensure_client_token()?,
        host_token: crate::instance::ensure_host_token(&instance.data_dir)?,
        instance,
    };
    #[cfg(target_os = "macos")]
    import_tauri_settings(&launch.instance);
    launch.instance.write_app_pid()?;
    Ok(launch)
}

#[cfg(target_os = "macos")]
fn import_tauri_settings(instance: &Instance) {
    let mut prefs = rune::prefs::Prefs::open(&instance.bundle_id);
    match crate::tauri_settings::import(&instance.tauri_web_data, &mut prefs) {
        Ok(crate::tauri_settings::Import::Imported(count)) => {
            eprintln!("attn-app: imported {count} settings from the Tauri app");
        }
        Ok(_) => {}
        Err(reason) => eprintln!("attn-app: the Tauri app's settings were not imported: {reason}"),
    }
}

impl Launch {
    /// `terminals` takes what the link hears about terminals, and `replies` the answers to
    /// requests, on the link's thread.
    pub fn link_config(&self, terminals: TerminalSink, replies: ReplySink) -> LinkConfig {
        let process_nonce = std::collections::hash_map::RandomState::new()
            .build_hasher()
            .finish();
        LinkConfig {
            daemon: format!("127.0.0.1:{}", self.instance.ws_port),
            client_token: self.client_token.clone(),
            host_token: Some(self.host_token.clone()),
            instance: self.instance.label().to_string(),
            profile_id: None,
            client_id: format!("native-{}-{process_nonce:016x}", std::process::id()),
            app_pid: std::process::id(),
            clock: Clock::system(),
            pulse: Pulse::default(),
            terminals,
            replies,
            start_daemon: Some(Arc::new(daemon::ensure)),
            migration_marker: Some(self.instance.data_dir.join("migration-failure.json")),
        }
    }
}
