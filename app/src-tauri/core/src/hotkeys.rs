use global_hotkey::{hotkey::HotKey, GlobalHotKeyEvent, GlobalHotKeyManager, HotKeyState};
use objc2_foundation::MainThreadMarker;
use std::sync::{mpsc, Mutex};
use tauri::{AppHandle, Manager, Runtime};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Action {
    QuickCapture,
}

#[derive(Clone, Copy)]
struct Route {
    action: Action,
    hotkey: HotKey,
}

pub struct Hotkeys {
    manager: Option<GlobalHotKeyManager>,
    routes: Vec<Route>,
}

impl Hotkeys {
    fn manager(&self) -> Result<&GlobalHotKeyManager, String> {
        self.manager
            .as_ref()
            .ok_or_else(|| "Hotkeys are shutting down".into())
    }

    fn route(&self, action: Action) -> Option<Route> {
        self.routes
            .iter()
            .copied()
            .find(|route| route.action == action)
    }

    fn bind(&mut self, action: Action, next: Option<HotKey>) -> Result<(), String> {
        let previous = self.route(action);
        if next == previous.map(|route| route.hotkey) {
            return Ok(());
        }
        if let Some(next) = next {
            self.manager()?.register_exclusive(next).map_err(|e| {
                format!("Cannot register {next}: {e}. Previous shortcut remains active.")
            })?;
        }
        if let Some(previous) = previous {
            if let Err(error) = self.manager()?.unregister(previous.hotkey) {
                if let Some(next) = next {
                    self.manager()?.unregister(next).map_err(|rollback| {
                        format!(
                            "Cannot unregister {}: {error}. Replacement rollback also failed: {rollback}",
                            previous.hotkey
                        )
                    })?;
                }
                return Err(format!("Cannot unregister {}: {error}", previous.hotkey));
            }
        }
        self.routes.retain(|route| route.action != action);
        if let Some(hotkey) = next {
            self.routes.push(Route { action, hotkey });
        }
        Ok(())
    }

    fn action_for(&self, id: u32) -> Option<Action> {
        self.routes
            .iter()
            .find(|route| route.hotkey.id() == id)
            .map(|route| route.action)
    }
}

pub fn parse(binding: Option<String>) -> Result<Option<HotKey>, String> {
    let next = binding
        .map(|s| s.parse::<HotKey>().map_err(|e| e.to_string()))
        .transpose()?;
    if next.is_some_and(|key| {
        !key.mods.intersects(
            global_hotkey::hotkey::Modifiers::CONTROL
                | global_hotkey::hotkey::Modifiers::ALT
                | global_hotkey::hotkey::Modifiers::SUPER
                | global_hotkey::hotkey::Modifiers::META,
        )
    }) {
        return Err(
            "Use Command, Control or Option with another key for a system-wide shortcut.".into(),
        );
    }
    Ok(next)
}

pub fn bind<R: Runtime>(
    app: &AppHandle<R>,
    action: Action,
    binding: Option<String>,
) -> Result<(), String> {
    on_main(app, move |app| {
        let next = parse(binding)?;
        app.state::<Mutex<Hotkeys>>()
            .lock()
            .unwrap()
            .bind(action, next)
    })
}

pub fn bound<R: Runtime>(app: &AppHandle<R>, action: Action) -> Option<HotKey> {
    app.state::<Mutex<Hotkeys>>()
        .lock()
        .unwrap()
        .route(action)
        .map(|route| route.hotkey)
}

pub fn on_main<R: Runtime, T: Send + 'static>(
    app: &AppHandle<R>,
    action: impl FnOnce(&AppHandle<R>) -> Result<T, String> + Send + 'static,
) -> Result<T, String> {
    if MainThreadMarker::new().is_some() {
        return action(app);
    }
    let (sender, receiver) = mpsc::channel();
    let handle = app.clone();
    app.run_on_main_thread(move || {
        let _ = sender.send(action(&handle));
    })
    .map_err(|error| error.to_string())?;
    receiver.recv().map_err(|error| error.to_string())?
}

pub fn install(app: &tauri::App) -> Result<(), Box<dyn std::error::Error>> {
    app.manage(Mutex::new(Hotkeys {
        manager: Some(GlobalHotKeyManager::new()?),
        routes: Vec::new(),
    }));
    let handle = app.handle().clone();
    GlobalHotKeyEvent::set_event_handler(Some(move |event: GlobalHotKeyEvent| {
        crate::quick_capture::record_trace(
            &handle,
            format!(
                "[QuickCapture] hotkey id={} state={:?} main_thread={}",
                event.id,
                event.state,
                MainThreadMarker::new().is_some()
            ),
        );
        if event.state != HotKeyState::Pressed {
            return;
        }
        let Some(action) = handle
            .state::<Mutex<Hotkeys>>()
            .lock()
            .unwrap()
            .action_for(event.id)
        else {
            return;
        };
        let app = handle.clone();
        let _ = handle.run_on_main_thread(move || dispatch(&app, action));
    }));
    Ok(())
}

fn dispatch(app: &AppHandle, action: Action) {
    match action {
        Action::QuickCapture => crate::quick_capture::show(app),
    }
}

pub fn shutdown(app: &AppHandle) {
    let manager = {
        let hotkeys = app.state::<Mutex<Hotkeys>>();
        let mut hotkeys = hotkeys.lock().unwrap();
        hotkeys.routes.clear();
        hotkeys.manager.take()
    };
    drop(manager);
}
