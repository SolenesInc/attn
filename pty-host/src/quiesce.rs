use std::io::ErrorKind;
use std::os::fd::{AsRawFd, FromRawFd, OwnedFd, RawFd};
use std::sync::{Condvar, Mutex};
use std::time::{Duration, Instant};

const SEQUENCE_GRACE: Duration = Duration::from_secs(1);

pub struct Quiesce {
    wake_read: OwnedFd,
    wake_write: OwnedFd,
    state: Mutex<State>,
    changed: Condvar,
}

#[derive(Default)]
struct State {
    requested: bool,
    requested_at: Option<Instant>,
    members: usize,
    parked: usize,
}

impl Quiesce {
    pub fn new() -> Result<Self, String> {
        let mut fds = [0; 2];
        if unsafe { libc::pipe(fds.as_mut_ptr()) } != 0 {
            return Err(format!(
                "create quiesce pipe: {}",
                std::io::Error::last_os_error()
            ));
        }
        let (wake_read, wake_write) =
            unsafe { (OwnedFd::from_raw_fd(fds[0]), OwnedFd::from_raw_fd(fds[1])) };
        for fd in [&wake_read, &wake_write] {
            set_cloexec(fd.as_raw_fd(), true)?;
        }
        Ok(Self {
            wake_read,
            wake_write,
            state: Mutex::new(State::default()),
            changed: Condvar::new(),
        })
    }

    pub fn enter(&self) {
        self.state.lock().expect("quiesce mutex poisoned").members += 1;
    }

    pub fn leave(&self) {
        self.state.lock().expect("quiesce mutex poisoned").members -= 1;
        self.changed.notify_all();
    }

    pub fn wait_readable(&self, fd: RawFd, at_rest: impl Fn() -> bool) -> std::io::Result<()> {
        let mut finishing_since: Option<Instant> = None;
        loop {
            let wake = self.wake_read.as_raw_fd();
            let watch_wake = finishing_since.is_none();
            let timeout = finishing_since.map_or(-1, |since| {
                let left = SEQUENCE_GRACE.saturating_sub(since.elapsed());
                i32::try_from(left.as_millis()).unwrap_or(i32::MAX)
            });
            if timeout == 0 {
                self.park_mid_sequence();
                finishing_since = None;
                continue;
            }
            let mut fds = [
                libc::pollfd {
                    fd,
                    events: libc::POLLIN,
                    revents: 0,
                },
                libc::pollfd {
                    fd: if watch_wake { wake } else { -1 },
                    events: libc::POLLIN,
                    revents: 0,
                },
            ];
            let ready = unsafe { libc::poll(fds.as_mut_ptr(), 2, timeout) };
            if ready < 0 {
                let error = std::io::Error::last_os_error();
                if error.kind() == ErrorKind::Interrupted {
                    continue;
                }
                return Err(error);
            }
            if ready == 0 {
                self.park_mid_sequence();
                finishing_since = None;
                continue;
            }
            if fds[1].revents != 0 {
                if at_rest() {
                    self.park();
                } else {
                    finishing_since = self.requested_at();
                }
                continue;
            }
            if fds[0].revents != 0 {
                return Ok(());
            }
        }
    }

    fn requested_at(&self) -> Option<Instant> {
        self.state
            .lock()
            .expect("quiesce mutex poisoned")
            .requested_at
    }

    fn park_mid_sequence(&self) {
        if self.requested_at().is_none() {
            return;
        }
        eprintln!(
            "PTY host stopped a terminal {SEQUENCE_GRACE:?} into an unfinished escape sequence; its next bytes may show as text"
        );
        self.park();
    }

    fn park(&self) {
        let mut state = self.state.lock().expect("quiesce mutex poisoned");
        if !state.requested {
            return;
        }
        state.parked += 1;
        self.changed.notify_all();
        let mut state = self
            .changed
            .wait_while(state, |state| state.requested)
            .expect("quiesce mutex poisoned");
        state.parked -= 1;
    }

    pub fn hold(&self) -> Result<(), String> {
        let mut state = self.state.lock().expect("quiesce mutex poisoned");
        if state.requested {
            return Err("terminals are already stopped for a handover".to_owned());
        }
        state.requested = true;
        state.requested_at = Some(Instant::now());
        if unsafe { libc::write(self.wake_write.as_raw_fd(), [1_u8].as_ptr().cast(), 1) } != 1 {
            state.requested = false;
            state.requested_at = None;
            return Err(format!(
                "wake terminal readers: {}",
                std::io::Error::last_os_error()
            ));
        }
        Ok(())
    }

    pub fn stop(&self) -> Result<(), String> {
        self.hold()?;
        let state = self.state.lock().expect("quiesce mutex poisoned");
        drop(
            self.changed
                .wait_while(state, |state| state.parked < state.members)
                .expect("quiesce mutex poisoned"),
        );
        Ok(())
    }

    pub fn resume(&self) {
        let mut state = self.state.lock().expect("quiesce mutex poisoned");
        let mut byte = 0_u8;
        let _ = unsafe { libc::read(self.wake_read.as_raw_fd(), (&raw mut byte).cast(), 1) };
        state.requested = false;
        state.requested_at = None;
        drop(state);
        self.changed.notify_all();
    }
}

pub fn set_cloexec(fd: RawFd, enabled: bool) -> Result<(), String> {
    let flags = unsafe { libc::fcntl(fd, libc::F_GETFD) };
    if flags < 0 {
        return Err(format!(
            "read descriptor {fd} flags: {}",
            std::io::Error::last_os_error()
        ));
    }
    let flags = if enabled {
        flags | libc::FD_CLOEXEC
    } else {
        flags & !libc::FD_CLOEXEC
    };
    if unsafe { libc::fcntl(fd, libc::F_SETFD, flags) } < 0 {
        return Err(format!(
            "set descriptor {fd} close-on-exec: {}",
            std::io::Error::last_os_error()
        ));
    }
    Ok(())
}
