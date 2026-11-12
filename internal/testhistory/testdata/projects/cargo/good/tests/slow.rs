#[test]
fn waits_for_its_signal() {
    let path = std::env::var("FIXTURE_WAIT").unwrap_or_default();
    while !path.is_empty() && !std::path::Path::new(&path).exists() {
        std::thread::yield_now();
    }
}
