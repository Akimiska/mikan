//! Files shared with the panel container. The container runs unprivileged and owns
//! data/panel: whatever lies there may be a link, a directory, a FIFO or a huge file
//! planted by a compromised panel. root reads and writes there only through these helpers,
//! which never follow a link and never trust a size or a type.

use std::fs::{self, OpenOptions};
use std::io::{ErrorKind, Read, Write};
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
use std::path::Path;
use std::time::{SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result, bail};

/// The panel's user inside its container.
pub const PANEL_UID: u32 = 65532;

/// Reads a small regular file the panel wrote; None when it is not there.
pub fn read(path: &Path, limit: u64) -> Result<Option<Vec<u8>>> {
    let file = match OpenOptions::new().read(true).custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK).open(path) {
        Ok(f) => f,
        Err(e) if e.kind() == ErrorKind::NotFound => return Ok(None),
        Err(e) => return Err(e).with_context(|| format!("open {}", path.display())),
    };
    if !file.metadata()?.is_file() {
        bail!("{} is not a regular file", path.display());
    }
    let mut data = Vec::new();
    file.take(limit + 1).read_to_end(&mut data)?;
    if data.len() as u64 > limit {
        bail!("{} is larger than {limit} bytes", path.display());
    }
    Ok(Some(data))
}

/// Writes name in dir for the panel to read: a fresh file (never an existing one, never
/// through a link) owned by the panel, then renamed over name, which replaces a link at
/// name instead of following it.
pub fn write(dir: &Path, name: &str, data: &[u8], mode: u32) -> Result<()> {
    ensure_dir(dir)?;
    let nanos = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_nanos()).unwrap_or(0);
    let tmp = dir.join(format!(".{name}.{}.{nanos}", std::process::id()));
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(mode)
        .custom_flags(libc::O_NOFOLLOW)
        .open(&tmp)
        .with_context(|| format!("create {}", tmp.display()))?;
    let written = (|| -> Result<()> {
        file.write_all(data)?;
        file.sync_all()?;
        std::os::unix::fs::fchown(&file, Some(PANEL_UID), Some(PANEL_UID))?;
        file.set_permissions(fs::Permissions::from_mode(mode))?;
        fs::rename(&tmp, dir.join(name))?;
        Ok(())
    })();
    if written.is_err() {
        let _ = fs::remove_file(&tmp);
    }
    written.with_context(|| format!("write {}", dir.join(name).display()))
}

/// The directory is a real directory, made for the panel when missing; a link in its place
/// is refused.
fn ensure_dir(dir: &Path) -> Result<()> {
    match fs::symlink_metadata(dir) {
        Ok(m) if m.is_dir() => Ok(()),
        Ok(_) => bail!("{} is not a directory", dir.display()),
        Err(e) if e.kind() == ErrorKind::NotFound => {
            fs::create_dir_all(dir)?;
            std::os::unix::fs::lchown(dir, Some(PANEL_UID), Some(PANEL_UID))?;
            Ok(())
        }
        Err(e) => Err(e.into()),
    }
}

/// Removes what the panel left at path: a file, or a link itself (never its target), or
/// a directory planted in place of a file.
pub fn discard(path: &Path) -> Result<()> {
    let Ok(m) = fs::symlink_metadata(path) else { return Ok(()) };
    let r = if m.is_dir() { fs::remove_dir_all(path) } else { fs::remove_file(path) };
    match r {
        Err(e) if e.kind() != ErrorKind::NotFound => Err(e).with_context(|| format!("remove {}", path.display())),
        _ => Ok(()),
    }
}

/// A file only root reads: the panel cannot see or replace it.
pub fn write_root(path: &Path, data: &[u8]) -> Result<()> {
    if let Some(dir) = path.parent() {
        fs::create_dir_all(dir)?;
        fs::set_permissions(dir, fs::Permissions::from_mode(0o700))?;
    }
    crate::envfile::write_private(path, data)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::symlink;

    fn tmpdir(name: &str) -> std::path::PathBuf {
        let d = std::env::temp_dir().join(format!("panelfs-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&d);
        fs::create_dir_all(&d).unwrap();
        d
    }

    // A link planted in the panel's directory is never followed: not on read, not on write.
    #[test]
    fn links_are_not_followed() {
        let d = tmpdir("links");
        let secret = d.join("secret");
        fs::write(&secret, "root only").unwrap();
        symlink(&secret, d.join("request")).unwrap();
        assert!(read(&d.join("request"), 4096).is_err());
        symlink(&secret, d.join("status.json")).unwrap();
        write(&d, "status.json", b"{}", 0o644).unwrap();
        assert_eq!(fs::read_to_string(&secret).unwrap(), "root only");
        assert!(!fs::symlink_metadata(d.join("status.json")).unwrap().file_type().is_symlink());
        assert_eq!(fs::read(d.join("status.json")).unwrap(), b"{}");
        fs::remove_dir_all(&d).unwrap();
    }

    #[test]
    fn sizes_and_types() {
        let d = tmpdir("sizes");
        assert!(read(&d.join("missing"), 10).unwrap().is_none());
        fs::write(d.join("big"), vec![b'x'; 11]).unwrap();
        assert!(read(&d.join("big"), 10).is_err());
        fs::write(d.join("ok"), b"0123456789").unwrap();
        assert_eq!(read(&d.join("ok"), 10).unwrap().unwrap().len(), 10);
        fs::create_dir(d.join("dir")).unwrap();
        assert!(read(&d.join("dir"), 10).is_err());
        let out = d.join("out");
        symlink(&d, &out).unwrap();
        assert!(write(&out, "x", b"y", 0o600).is_err(), "a link in place of the directory");
        fs::remove_dir_all(&d).unwrap();
    }
}
