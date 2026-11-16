//! The Quick Capture panel in a wire test: a windowless runtime bound to the world's store, in
//! a window that records whether the platform would show it.

use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};

use attn_app::quick_capture::{self, Panel, Update};
use attn_client::{Commands, Pulse, Store, Subscription};
use rune::{Env, Runtime};

use crate::screen::Screen;

/// The panel's window as the platform would hold it.
pub(crate) struct HeadlessPanel {
    shown: AtomicBool,
    inbox: Mutex<Vec<Update>>,
    pulse: Pulse,
}

impl quick_capture::Window for HeadlessPanel {
    fn show(&self) {
        self.shown.store(true, Ordering::Release);
        self.pulse.beat();
    }

    fn hide(&self) {
        self.shown.store(false, Ordering::Release);
        self.pulse.beat();
    }

    fn update(&self, update: Update) {
        self.inbox.lock().unwrap().push(update);
        self.pulse.beat();
    }
}

pub(crate) struct CaptureRuntime {
    pub runtime: Runtime<Panel>,
    pub window: Arc<HeadlessPanel>,
    pub data_dir: PathBuf,
    pub clipboard: rune::clipboard::MemoryClipboard,
    _bound: Subscription,
}

impl CaptureRuntime {
    pub(crate) fn start(
        store: &Store,
        daemon: Commands,
        pulse: &Pulse,
        data_dir: &Path,
    ) -> CaptureRuntime {
        let env = Env {
            system_dark: true,
            scale: 2.0,
        };
        let mut runtime = quick_capture::app().headless_runtime(quick_capture::SIZE, env);
        let clipboard = rune::clipboard::memory();
        runtime.use_clipboard(&clipboard);
        let window = Arc::new(HeadlessPanel {
            shown: AtomicBool::new(false),
            inbox: Mutex::default(),
            pulse: pulse.clone(),
        });
        let bound = {
            let window = window.clone();
            quick_capture::bind(
                store,
                daemon,
                window.clone(),
                data_dir.to_owned(),
                move |u| {
                    quick_capture::Window::update(&*window, u);
                },
            )
        };
        CaptureRuntime {
            runtime,
            window,
            data_dir: data_dir.to_owned(),
            clipboard,
            _bound: bound,
        }
    }

    pub(crate) fn shown(&self) -> bool {
        self.window.shown.load(Ordering::Acquire)
    }

    pub(crate) fn screen(&self) -> Screen {
        Screen(self.runtime.accessibility_json())
    }

    /// Runs what was handed the panel, and draws it.
    pub(crate) fn settle(&mut self) {
        let updates = std::mem::take(&mut *self.window.inbox.lock().unwrap());
        for update in updates {
            self.runtime.update(update);
        }
        self.frame();
        while self.runtime.has_pending_input() {
            self.frame();
        }
    }

    fn frame(&mut self) {
        self.runtime.frame(0.0);
        if self.runtime.animating {
            self.runtime.settle_all();
            self.runtime.frame(0.0);
        }
    }

    pub(crate) fn advance(&mut self, by: std::time::Duration) {
        self.runtime.frame(by.as_secs_f64());
    }

    pub(crate) fn idle(&self) -> bool {
        self.window.inbox.lock().unwrap().is_empty() && !self.runtime.state().copying()
    }

    pub(crate) fn at_rest(&self) -> bool {
        !self.runtime.animating && self.runtime.wake_deadline().is_none()
    }
}
