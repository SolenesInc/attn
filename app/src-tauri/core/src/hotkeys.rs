use global_hotkey::{hotkey::HotKey, GlobalHotKeyEvent, GlobalHotKeyManager, HotKeyState};
use objc2_app_kit::{
    NSApplication, NSWindowDidBecomeKeyNotification, NSWindowDidResignKeyNotification,
};
use objc2_foundation::{MainThreadMarker, NSNotification, NSNotificationCenter};
use std::sync::{mpsc, Condvar, Mutex};
use std::time::Duration;
use tauri::{AppHandle, Manager, Runtime};

#[derive(Clone, Copy, Debug, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum Action {
    QuickCapture,
    Dictation,
}

enum Scope {
    Systemwide,
    WhileAttnWindowIsKey,
}

impl Action {
    const ALL: [Action; 2] = [Action::QuickCapture, Action::Dictation];

    fn scope(self) -> Scope {
        match self {
            Action::QuickCapture => Scope::Systemwide,
            Action::Dictation => Scope::WhileAttnWindowIsKey,
        }
    }

    fn name(self) -> &'static str {
        match self {
            Action::QuickCapture => "Quick Capture",
            Action::Dictation => "Dictation",
        }
    }
}

#[derive(Clone, Debug, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Press {
    pub action: Action,
    pub window: Option<String>,
    pub browser: Option<String>,
}

#[derive(Clone, Copy)]
struct Route {
    action: Action,
    hotkey: HotKey,
    registered: bool,
}

pub struct Changed(Condvar);

const WAIT_TRIPWIRE: Duration = Duration::from_secs(30);

pub struct Hotkeys {
    manager: Option<GlobalHotKeyManager>,
    routes: Vec<Route>,
    attn_window_is_key: bool,
    dictating: bool,
    presses: Vec<Press>,
}

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct RouteStatus {
    pub action: Action,
    pub binding: String,
    pub registered: bool,
}

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Status {
    pub attn_window_is_key: bool,
    pub routes: Vec<RouteStatus>,
    pub presses: Vec<Press>,
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

    fn wants_registration(&self, action: Action) -> bool {
        match action.scope() {
            Scope::Systemwide => true,
            Scope::WhileAttnWindowIsKey => {
                self.attn_window_is_key || (action == Action::Dictation && self.dictating)
            }
        }
    }

    fn bind(&mut self, action: Action, next: Option<HotKey>) -> Result<(), String> {
        let previous = self.route(action);
        if next == previous.map(|route| route.hotkey) {
            return Ok(());
        }
        if let Some(next) = next {
            if let Some(owner) = self
                .routes
                .iter()
                .find(|route| route.action != action && route.hotkey == next)
            {
                return Err(format!(
                    "{next} is already bound to {}. Previous shortcut remains active.",
                    owner.action.name()
                ));
            }
            self.manager()?.register_exclusive(next).map_err(|e| {
                format!("Cannot register {next}: {e}. Previous shortcut remains active.")
            })?;
        }
        if let Some(previous) = previous.filter(|route| route.registered) {
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
            self.routes.push(Route {
                action,
                hotkey,
                registered: true,
            });
            self.sync(action)?;
        }
        Ok(())
    }

    fn sync(&mut self, action: Action) -> Result<(), String> {
        let wanted = self.wants_registration(action);
        let Some(route) = self
            .route(action)
            .filter(|route| route.registered != wanted)
        else {
            return Ok(());
        };
        if wanted {
            self.manager()?
                .register_exclusive(route.hotkey)
                .map_err(|e| format!("Cannot register {}: {e}", route.hotkey))?;
        } else {
            self.manager()?
                .unregister(route.hotkey)
                .map_err(|e| format!("Cannot unregister {}: {e}", route.hotkey))?;
        }
        for route in self
            .routes
            .iter_mut()
            .filter(|route| route.action == action)
        {
            route.registered = wanted;
        }
        Ok(())
    }

    fn status(&self) -> Status {
        Status {
            attn_window_is_key: self.attn_window_is_key,
            routes: Action::ALL
                .into_iter()
                .filter_map(|action| self.route(action))
                .map(|route| RouteStatus {
                    action: route.action,
                    binding: route.hotkey.to_string(),
                    registered: route.registered,
                })
                .collect(),
            presses: self.presses.clone(),
        }
    }

    fn action_for(&self, id: u32) -> Option<Action> {
        self.routes
            .iter()
            .find(|route| route.registered && route.hotkey.id() == id)
            .map(|route| route.action)
    }
}

pub fn dictating<R: Runtime>(app: &AppHandle<R>, live: bool) -> Result<(), String> {
    let hotkeys = app.state::<Mutex<Hotkeys>>();
    let mut hotkeys = hotkeys.lock().unwrap();
    hotkeys.dictating = live;
    let synced = hotkeys.sync(Action::Dictation);
    drop(hotkeys);
    changed(app);
    synced
}

fn changed<R: Runtime>(app: &AppHandle<R>) {
    app.state::<Changed>().0.notify_all();
}

pub fn wait<R: Runtime>(
    app: &AppHandle<R>,
    describe: &str,
    done: impl Fn(&Status) -> bool,
) -> Result<Status, String> {
    let hotkeys = app.state::<Mutex<Hotkeys>>();
    let changed = app.state::<Changed>();
    let mut guard = hotkeys.lock().unwrap();
    let deadline = std::time::Instant::now() + WAIT_TRIPWIRE;
    loop {
        let status = guard.status();
        if done(&status) {
            return Ok(status);
        }
        let left = deadline.saturating_duration_since(std::time::Instant::now());
        if left.is_zero() {
            return Err(format!(
                "Hotkeys did not reach {describe} within {}s; last status: {}",
                WAIT_TRIPWIRE.as_secs(),
                serde_json::to_string(&status).unwrap_or_default()
            ));
        }
        guard = changed.0.wait_timeout(guard, left).unwrap().0;
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
        let outcome = app
            .state::<Mutex<Hotkeys>>()
            .lock()
            .unwrap()
            .bind(action, next);
        changed(app);
        outcome
    })
}

pub fn bound<R: Runtime>(app: &AppHandle<R>, action: Action) -> Option<HotKey> {
    app.state::<Mutex<Hotkeys>>()
        .lock()
        .unwrap()
        .route(action)
        .map(|route| route.hotkey)
}

pub fn status<R: Runtime>(app: &AppHandle<R>) -> Status {
    app.state::<Mutex<Hotkeys>>().lock().unwrap().status()
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
    let mtm = MainThreadMarker::new().expect("hotkeys install on the main thread");
    app.manage(Mutex::new(Hotkeys {
        manager: Some(GlobalHotKeyManager::new()?),
        routes: Vec::new(),
        attn_window_is_key: NSApplication::sharedApplication(mtm).keyWindow().is_some(),
        dictating: false,
        presses: Vec::new(),
    }));
    app.manage(Changed(Condvar::new()));
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
    observe_key_window(app.handle());
    Ok(())
}

pub fn focused_browser_in<R: tauri::Runtime>(
    app: &tauri::AppHandle<R>,
    window: Option<&str>,
) -> Option<String> {
    crate::browser_host::focused_browser_label().filter(|label| {
        app.get_webview(label)
            .is_some_and(|webview| Some(webview.window().label()) == window)
    })
}

fn dispatch(app: &AppHandle, action: Action) {
    match action {
        Action::QuickCapture => crate::quick_capture::show(app),
        Action::Dictation => {
            let window = key_window_label(app);
            let browser = focused_browser_in(app, window.as_deref());
            crate::dictation::press(app, window.clone(), browser.clone());
            let press = Press {
                action,
                window,
                browser,
            };
            if crate::instance::automation_enabled() {
                app.state::<Mutex<Hotkeys>>()
                    .lock()
                    .unwrap()
                    .presses
                    .push(press);
                changed(app);
            }
        }
    }
}

fn key_window_label(app: &AppHandle) -> Option<String> {
    let mtm = MainThreadMarker::new().expect("dispatch runs on the main thread");
    let key = NSApplication::sharedApplication(mtm).keyWindow()?;
    app.windows().into_iter().find_map(|(label, window)| {
        let native = window.ns_window().ok()?;
        std::ptr::eq(
            native.cast_const(),
            objc2::rc::Retained::as_ptr(&key).cast(),
        )
        .then_some(label)
    })
}

fn observe_key_window(app: &AppHandle) {
    use block2::RcBlock;
    use std::ptr::NonNull;

    let center = NSNotificationCenter::defaultCenter();
    for (name, is_key) in [
        (unsafe { NSWindowDidBecomeKeyNotification }, true),
        (unsafe { NSWindowDidResignKeyNotification }, false),
    ] {
        let app = app.clone();
        let handler = RcBlock::new(move |_notification: NonNull<NSNotification>| {
            let hotkeys = app.state::<Mutex<Hotkeys>>();
            let mut hotkeys = hotkeys.lock().unwrap();
            hotkeys.attn_window_is_key = is_key;
            let errors: Vec<String> = Action::ALL
                .into_iter()
                .filter_map(|action| hotkeys.sync(action).err())
                .collect();
            drop(hotkeys);
            changed(&app);
            for error in errors {
                crate::quick_capture::record_trace(&app, format!("[Hotkeys] {error}"));
            }
        });
        let observer = unsafe {
            center.addObserverForName_object_queue_usingBlock(Some(name), None, None, &handler)
        };
        std::mem::forget(observer);
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
