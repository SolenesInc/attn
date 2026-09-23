use std::io::{self, Read, Write};
use std::mem::MaybeUninit;

pub const FLAG: &str = "--probe-child";
pub const CAPABILITY: &str = "probe_child";

pub fn run() -> Result<(), String> {
    enter_raw_mode()?;
    let mut stdout = io::stdout().lock();
    let mut line = Vec::new();
    for byte in io::stdin().lock().bytes() {
        let Ok(byte) = byte else {
            return Ok(());
        };
        if byte != b'\r' && byte != b'\n' {
            line.push(byte);
            continue;
        }
        if line.is_empty() {
            continue;
        }
        let (cols, rows) = window_size()?;
        let mut reply = b"ATTN-PROBE ".to_vec();
        reply.extend_from_slice(&line);
        reply.extend_from_slice(format!(" {cols}x{rows}\r\n").as_bytes());
        line.clear();
        stdout
            .write_all(&reply)
            .and_then(|()| stdout.flush())
            .map_err(|error| format!("answer probe: {error}"))?;
    }
    Ok(())
}

fn enter_raw_mode() -> Result<(), String> {
    let mut termios = MaybeUninit::<libc::termios>::uninit();
    if unsafe { libc::tcgetattr(libc::STDIN_FILENO, termios.as_mut_ptr()) } != 0 {
        return Err(format!(
            "read terminal mode: {}",
            io::Error::last_os_error()
        ));
    }
    let mut termios = unsafe { termios.assume_init() };
    unsafe { libc::cfmakeraw(&raw mut termios) };
    if unsafe { libc::tcsetattr(libc::STDIN_FILENO, libc::TCSANOW, &raw const termios) } != 0 {
        return Err(format!("enter raw mode: {}", io::Error::last_os_error()));
    }
    Ok(())
}

fn window_size() -> Result<(u16, u16), String> {
    let mut size = libc::winsize {
        ws_row: 0,
        ws_col: 0,
        ws_xpixel: 0,
        ws_ypixel: 0,
    };
    if unsafe { libc::ioctl(libc::STDOUT_FILENO, libc::TIOCGWINSZ, &raw mut size) } != 0 {
        return Err(format!("read window size: {}", io::Error::last_os_error()));
    }
    Ok((size.ws_col, size.ws_row))
}
