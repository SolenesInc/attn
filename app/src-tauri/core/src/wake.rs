#[cfg(target_os = "macos")]
pub fn install(app: &tauri::AppHandle) {
    use block2::RcBlock;
    use objc2_app_kit::{
        NSWorkspace, NSWorkspaceDidWakeNotification, NSWorkspaceScreensDidWakeNotification,
    };
    use objc2_foundation::NSNotification;
    use std::ptr::NonNull;
    use tauri::Emitter;

    let center = NSWorkspace::sharedWorkspace().notificationCenter();
    for (name, reason) in [
        (
            unsafe { NSWorkspaceScreensDidWakeNotification },
            "screens_did_wake",
        ),
        (unsafe { NSWorkspaceDidWakeNotification }, "did_wake"),
    ] {
        let app = app.clone();
        let handler = RcBlock::new(move |_notification: NonNull<NSNotification>| {
            if let Err(error) = app.emit("native-wake", serde_json::json!({ "reason": reason })) {
                eprintln!("attn: native wake event: {error}");
            }
        });
        // The observer lives for the app process, as does its notification center.
        let observer = unsafe {
            center.addObserverForName_object_queue_usingBlock(Some(name), None, None, &handler)
        };
        std::mem::forget(observer);
    }
}
