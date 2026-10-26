use std::io;

pub fn signal_session(sid: i32, signal: i32) -> Result<(), String> {
    let mut processes =
        session_processes(sid).map_err(|error| format!("read session {sid}: {error}"))?;
    if let Some(leader) = processes.iter().position(|pid| *pid == sid) {
        let pid = processes.remove(leader);
        processes.push(pid);
    }
    let mut result = Ok(());
    for pid in processes {
        if unsafe { libc::kill(pid, signal) } != 0 {
            let error = io::Error::last_os_error();
            if error.raw_os_error() != Some(libc::ESRCH) {
                result = Err(format!("signal process {pid}: {error}"));
            }
        }
    }
    result
}

#[cfg(target_os = "linux")]
fn session_processes(sid: i32) -> io::Result<Vec<i32>> {
    let mut processes = Vec::new();
    let sid = sid.to_string();
    for entry in std::fs::read_dir("/proc")?.flatten() {
        let Ok(pid) = entry.file_name().to_string_lossy().parse::<i32>() else {
            continue;
        };
        let Ok(raw) = std::fs::read_to_string(entry.path().join("stat")) else {
            continue;
        };
        let Some((_, rest)) = raw.rsplit_once(')') else {
            continue;
        };
        if rest.split_whitespace().nth(3) == Some(sid.as_str()) {
            processes.push(pid);
        }
    }
    Ok(processes)
}

#[cfg(target_os = "macos")]
fn session_processes(sid: i32) -> io::Result<Vec<i32>> {
    let mut count = unsafe { libc::proc_listallpids(std::ptr::null_mut(), 0) };
    if count <= 0 {
        return Err(io::Error::last_os_error());
    }
    let pids = loop {
        let mut pids = vec![0_i32; count as usize * 2];
        let bytes =
            i32::try_from(std::mem::size_of_val(pids.as_slice())).map_err(io::Error::other)?;
        count = unsafe { libc::proc_listallpids(pids.as_mut_ptr().cast(), bytes) };
        if count < 0 {
            return Err(io::Error::last_os_error());
        }
        if (count as usize) < pids.len() {
            pids.truncate(count as usize);
            break pids;
        }
    };
    Ok(pids
        .into_iter()
        .filter(|pid| *pid > 0 && unsafe { libc::getsid(*pid) } == sid)
        .collect())
}
