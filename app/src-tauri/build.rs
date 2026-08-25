use std::path::Path;

fn main() {
    track_frontend_dist();
    tauri_build::build()
}

/// With the plain `generate_context!` macro cargo never learns `app/dist` is an
/// input, so a frontend-only change leaves the crate fresh and ships stale assets.
fn track_frontend_dist() {
    let Ok(manifest_dir) = std::env::var("CARGO_MANIFEST_DIR") else {
        return;
    };
    let manifest_dir = Path::new(&manifest_dir);

    let Ok(raw) = std::fs::read_to_string(manifest_dir.join("tauri.conf.json")) else {
        return;
    };
    let Ok(config) = serde_json::from_str::<serde_json::Value>(&raw) else {
        return;
    };
    let Some(dist) = config
        .pointer("/build/frontendDist")
        .and_then(serde_json::Value::as_str)
    else {
        return;
    };

    // `frontendDist` may also be a dev-server URL or an explicit file list;
    // only a directory path is meaningful to track here.
    if dist.starts_with("http://") || dist.starts_with("https://") {
        return;
    }

    let dist_dir = manifest_dir.join(dist);
    if dist_dir.is_dir() {
        // Track the directory, not its files: cargo rescans it each build, so
        // additions and deletions register too.
        println!("cargo:rerun-if-changed={}", dist_dir.display());
    }
}
