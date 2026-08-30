use std::io::{Read, Write};
use std::thread;

use anyhow::{Context, Result};
use crossbeam_channel::{Receiver, unbounded};
use portable_pty::{Child, CommandBuilder, MasterPty, PtySize, native_pty_system};

pub enum ShellOutput {
    Bytes(Vec<u8>),
    Closed,
}

pub struct Shell {
    master: Box<dyn MasterPty + Send>,
    writer: Option<Box<dyn Write + Send>>,
    child: Box<dyn Child + Send + Sync>,
}

impl Shell {
    pub fn spawn(cols: u16, rows: u16) -> Result<(Self, Receiver<ShellOutput>)> {
        let pair = native_pty_system().openpty(PtySize {
            rows,
            cols,
            pixel_width: 0,
            pixel_height: 0,
        })?;
        let shell = std::env::var_os("SHELL").unwrap_or_else(|| "/bin/sh".into());
        let mut command = CommandBuilder::new(shell);
        command.env("TERM", "xterm-256color");
        command.cwd(std::env::current_dir()?);
        let child = pair
            .slave
            .spawn_command(command)
            .context("spawning $SHELL")?;
        drop(pair.slave);
        let mut reader = pair.master.try_clone_reader()?;
        let writer = pair.master.take_writer()?;
        let (sender, receiver) = unbounded();
        thread::spawn(move || {
            let mut buffer = [0; 8192];
            loop {
                match reader.read(&mut buffer) {
                    Ok(0) | Err(_) => break,
                    Ok(count) => {
                        if sender
                            .send(ShellOutput::Bytes(buffer[..count].to_vec()))
                            .is_err()
                        {
                            return;
                        }
                    }
                }
            }
            let _ = sender.send(ShellOutput::Closed);
        });
        Ok((
            Self {
                master: pair.master,
                writer: Some(writer),
                child,
            },
            receiver,
        ))
    }

    pub fn write(&mut self, bytes: &[u8]) -> Result<()> {
        self.writer
            .as_mut()
            .context("shell PTY is closed")?
            .write_all(bytes)?;
        Ok(())
    }

    pub fn resize(&self, cols: u16, rows: u16) -> Result<()> {
        self.master.resize(PtySize {
            rows,
            cols,
            pixel_width: 0,
            pixel_height: 0,
        })?;
        Ok(())
    }
}

impl Drop for Shell {
    fn drop(&mut self) {
        drop(self.writer.take());
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}
