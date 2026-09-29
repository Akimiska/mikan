//! /opt/mikan/.env: the KEY=VALUE lines compose reads. Keys the installer does not know
//! and the order of lines stay as they are.

use std::fs;
use std::io::Write;
use std::os::unix::fs::OpenOptionsExt;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result, bail};

pub struct EnvFile {
    path: PathBuf,
    lines: Vec<String>,
}

impl EnvFile {
    pub fn load(path: impl AsRef<Path>) -> Result<Self> {
        let path = path.as_ref().to_path_buf();
        let text = fs::read_to_string(&path).with_context(|| format!("read {}", path.display()))?;
        Ok(Self::parse(path, &text))
    }

    pub fn new(path: impl AsRef<Path>) -> Self {
        Self::parse(path.as_ref().to_path_buf(), "")
    }

    fn parse(path: PathBuf, text: &str) -> Self {
        Self { path, lines: text.lines().map(str::to_owned).collect() }
    }

    pub fn get(&self, key: &str) -> Option<&str> {
        self.lines.iter().find_map(|l| l.strip_prefix(key)?.strip_prefix('=')).filter(|v| !v.is_empty())
    }

    /// Sets key to value, in place when the key is there.
    pub fn set(&mut self, key: &str, value: &str) -> Result<()> {
        if value.contains(['\n', '\r']) {
            bail!("{key}: a value cannot span lines");
        }
        let line = format!("{key}={value}");
        match self.lines.iter_mut().find(|l| l.strip_prefix(key).is_some_and(|r| r.starts_with('='))) {
            Some(l) => *l = line,
            None => self.lines.push(line),
        }
        Ok(())
    }

    pub fn render(&self) -> String {
        let mut s = self.lines.join("\n");
        s.push('\n');
        s
    }

    /// Writes the file whole (a new file, then a rename), readable by root only: the join
    /// key of a node is in it.
    pub fn save(&self) -> Result<()> {
        write_private(&self.path, self.render().as_bytes())
    }
}

/// Writes a file readable by root only, replacing it at once.
pub fn write_private(path: &Path, data: &[u8]) -> Result<()> {
    let tmp = path.with_extension("tmp");
    let mut f = fs::OpenOptions::new().write(true).create(true).truncate(true).mode(0o600).open(&tmp)?;
    f.write_all(data)?;
    f.sync_all()?;
    fs::rename(&tmp, path).with_context(|| format!("write {}", path.display()))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn keeps_unknown_lines_and_order() {
        let mut e = EnvFile::parse("x".into(), "MIKAN_IMAGE=mikan:0.3.8\n# note\nPANEL_PORT=21355\nMIKAN_UFW=1\n");
        assert_eq!(e.get("MIKAN_IMAGE"), Some("mikan:0.3.8"));
        assert_eq!(e.get("MIKAN"), None);
        e.set("MIKAN_IMAGE", "ghcr.io/miroshka000/mikan@sha256:abc").unwrap();
        e.set("MIKAN_VERSION", "0.3.9").unwrap();
        assert_eq!(
            e.render(),
            "MIKAN_IMAGE=ghcr.io/miroshka000/mikan@sha256:abc\n# note\nPANEL_PORT=21355\nMIKAN_UFW=1\nMIKAN_VERSION=0.3.9\n"
        );
        assert!(e.set("X", "a\nb").is_err());
    }
}
