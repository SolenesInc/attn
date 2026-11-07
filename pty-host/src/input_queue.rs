use std::collections::VecDeque;
use std::fs::File;
use std::io::{ErrorKind, Write};
use std::os::fd::{AsRawFd, FromRawFd, OwnedFd};
use std::sync::{Arc, Condvar, Mutex};
use std::thread;
use std::time::{Duration, Instant};

use crate::quiesce::{Quiesce, set_cloexec};

const LARGEST_DAEMON_FRAME_BYTES: usize = 8 * 1024 * 1024;
pub const INPUT_QUEUE_MAX_BYTES: usize = 2 * LARGEST_DAEMON_FRAME_BYTES;
const WRITER_STACK_BYTES: usize = 128 * 1024;

pub struct InputQueue {
    state: Mutex<State>,
    settled: Condvar,
    wake_read: OwnedFd,
    wake_write: OwnedFd,
}

struct Chunk {
    id: u64,
    data: Vec<u8>,
}

#[derive(Default)]
struct State {
    chunks: VecDeque<Chunk>,
    next_chunk: u64,
    front_written: usize,
    waiting: usize,
    failure: Option<String>,
    stopped: bool,
}

enum Progress {
    Idle,
    Blocked,
    Stopped,
}

impl InputQueue {
    pub fn new(pending: &[u8]) -> Result<Self, String> {
        let mut fds = [0; 2];
        if unsafe { libc::pipe(fds.as_mut_ptr()) } != 0 {
            return Err(format!(
                "create input wake pipe: {}",
                std::io::Error::last_os_error()
            ));
        }
        let (wake_read, wake_write) =
            unsafe { (OwnedFd::from_raw_fd(fds[0]), OwnedFd::from_raw_fd(fds[1])) };
        for fd in [&wake_read, &wake_write] {
            set_cloexec(fd.as_raw_fd(), true)?;
            set_nonblocking(fd.as_raw_fd())?;
        }
        let queue = Self {
            state: Mutex::new(State::default()),
            settled: Condvar::new(),
            wake_read,
            wake_write,
        };
        if !pending.is_empty() {
            queue.push(pending)?;
        }
        Ok(queue)
    }

    pub fn push(&self, data: &[u8]) -> Result<(), String> {
        if data.is_empty() {
            return Ok(());
        }
        self.push_chunk(data).map(|_| ())
    }

    pub fn push_chunk(&self, data: &[u8]) -> Result<u64, String> {
        let mut state = self.state.lock().expect("input queue mutex poisoned");
        if state.waiting + data.len() > INPUT_QUEUE_MAX_BYTES {
            return Err(format!(
                "the program in this terminal is not reading its input: the input queue holds at most {INPUT_QUEUE_MAX_BYTES} bytes, {} are waiting and this write adds {}",
                state.waiting,
                data.len()
            ));
        }
        let was_empty = state.chunks.is_empty();
        let id = state.next_chunk;
        state.next_chunk += 1;
        state.chunks.push_back(Chunk {
            id,
            data: data.to_vec(),
        });
        state.waiting += data.len();
        drop(state);
        if was_empty {
            self.wake();
        }
        Ok(id)
    }

    pub fn settle_or_withdraw(&self, id: u64, within: Duration) -> Result<(), String> {
        let deadline = Instant::now() + within;
        let mut state = self.state.lock().expect("input queue mutex poisoned");
        loop {
            let Some(at) = state.chunks.iter().position(|chunk| chunk.id == id) else {
                return state.failure.take().map_or(Ok(()), Err);
            };
            if state.stopped {
                return Err("session not running".to_owned());
            }
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                let skip = if at == 0 {
                    std::mem::take(&mut state.front_written)
                } else {
                    0
                };
                let withdrawn = state.chunks.remove(at).expect("position is in the queue");
                state.waiting -= withdrawn.data.len() - skip;
                return Err(format!(
                    "the program in this terminal is not reading its input: this write did not finish within {} ms, and attn withdrew what it had not written",
                    within.as_millis()
                ));
            }
            state = self
                .settled
                .wait_timeout(state, remaining)
                .expect("input queue mutex poisoned")
                .0;
        }
    }

    pub fn take_failure(&self) -> Option<String> {
        self.state
            .lock()
            .expect("input queue mutex poisoned")
            .failure
            .take()
    }

    pub fn pending(&self) -> Vec<u8> {
        let state = self.state.lock().expect("input queue mutex poisoned");
        let mut pending = Vec::with_capacity(state.waiting);
        for (index, chunk) in state.chunks.iter().enumerate() {
            let skip = if index == 0 { state.front_written } else { 0 };
            pending.extend_from_slice(&chunk.data[skip..]);
        }
        pending
    }

    pub fn stop(&self) {
        self.state
            .lock()
            .expect("input queue mutex poisoned")
            .stopped = true;
        self.settled.notify_all();
        self.wake();
    }

    fn wake(&self) {
        let _ = unsafe { libc::write(self.wake_write.as_raw_fd(), [1_u8].as_ptr().cast(), 1) };
    }

    fn clear_wake(&self) {
        let mut sink = [0_u8; 64];
        while unsafe {
            libc::read(
                self.wake_read.as_raw_fd(),
                sink.as_mut_ptr().cast(),
                sink.len(),
            )
        } > 0
        {}
    }

    fn write_waiting(&self, mut master: &File) -> Progress {
        let mut guard = self.state.lock().expect("input queue mutex poisoned");
        let state = &mut *guard;
        if state.stopped {
            return Progress::Stopped;
        }
        while let Some(chunk) = state.chunks.front() {
            match master.write(&chunk.data[state.front_written..]) {
                Ok(written) if written > 0 => {
                    state.waiting -= written;
                    state.front_written += written;
                    if state.front_written == chunk.data.len() {
                        state.chunks.pop_front();
                        state.front_written = 0;
                    }
                }
                Err(error) if error.kind() == ErrorKind::Interrupted => {}
                Err(error) if error.kind() == ErrorKind::WouldBlock => return Progress::Blocked,
                failed => {
                    state.chunks.clear();
                    state.front_written = 0;
                    state.waiting = 0;
                    state.failure = Some(match failed {
                        Err(error) => format!("write PTY: {error}"),
                        Ok(_) => "write PTY: the terminal took no bytes".to_owned(),
                    });
                }
            }
        }
        Progress::Idle
    }
}

pub fn start_writer(
    name: &str,
    queue: Arc<InputQueue>,
    master: File,
    quiesce: Arc<Quiesce>,
) -> Result<(), String> {
    set_nonblocking(master.as_raw_fd())?;
    quiesce.enter();
    let member = Arc::clone(&quiesce);
    thread::Builder::new()
        .name(format!("pty-write-{name}"))
        .stack_size(WRITER_STACK_BYTES)
        .spawn(move || {
            write_until_stopped(&queue, &master, &quiesce);
            quiesce.leave();
        })
        .map(|_| ())
        .map_err(|error| {
            member.leave();
            format!("start PTY writer: {error}")
        })
}

fn write_until_stopped(queue: &InputQueue, master: &File, quiesce: &Quiesce) {
    let mut blocked = false;
    loop {
        let writable = blocked.then(|| master.as_raw_fd());
        if let Err(error) = quiesce.wait_input_turn(queue.wake_read.as_raw_fd(), writable) {
            eprintln!("PTY input wait failed: {error}");
            return;
        }
        queue.clear_wake();
        let progress = queue.write_waiting(master);
        queue.settled.notify_all();
        match progress {
            Progress::Idle => blocked = false,
            Progress::Blocked => blocked = true,
            Progress::Stopped => return,
        }
    }
}

fn set_nonblocking(fd: i32) -> Result<(), String> {
    let flags = unsafe { libc::fcntl(fd, libc::F_GETFL) };
    if flags < 0 || unsafe { libc::fcntl(fd, libc::F_SETFL, flags | libc::O_NONBLOCK) } < 0 {
        return Err(format!(
            "make descriptor {fd} non-blocking: {}",
            std::io::Error::last_os_error()
        ));
    }
    Ok(())
}
