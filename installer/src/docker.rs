//! Docker and compose, through the docker command: what the server's admin would run by
//! hand, so nothing here needs the Docker socket's API.

use std::collections::BTreeMap;
use std::io::{BufRead, BufReader, Read, Write};
use std::process::{Child, Command, Output, Stdio};
use std::sync::mpsc;
use std::thread;

use anyhow::{Context, Result, bail};
use serde::Deserialize;

use crate::DIR;
use crate::system::output;

/// The panel with its own node. Both run as an unprivileged user on the host network with
/// a read-only root; the panel reaches the node over a socket in a shared volume.
pub const PANEL_COMPOSE: &str = r#"name: mikan

x-hardening: &hardening
  image: ${MIKAN_IMAGE}
  network_mode: host
  restart: unless-stopped
  user: "65532:65532"
  cap_drop: [ALL]
  cap_add: [NET_BIND_SERVICE]
  security_opt: ["no-new-privileges:true"]
  read_only: true
  tmpfs: ["/tmp:rw,size=64m"]
  logging:
    driver: json-file
    options: {max-size: "10m", max-file: "3"}

services:
  node:
    <<: *hardening
    entrypoint: ["/usr/local/bin/mikan-node"]
    environment:
      MIKAN_DATA_DIR: /data/node
      MIKAN_NODE_SOCKET: /run/mikan/node.sock
    volumes: ["./data:/data", "run:/run/mikan"]

  panel:
    <<: *hardening
    command: ["serve"]
    depends_on: [node]
    environment:
      MIKAN_DATA_DIR: /data/panel
      MIKAN_NODE_SOCKET: /run/mikan/node.sock
      MIKAN_PANEL_LISTEN: 0.0.0.0:${PANEL_PORT}
    volumes: ["./data:/data", "run:/run/mikan"]
    healthcheck:
      test: ["CMD", "/usr/local/bin/mikan", "health"]
      interval: 30s
      timeout: 5s
      retries: 3

volumes:
  run: {}
"#;

/// A node of another panel, which drives it over its API port.
pub const NODE_COMPOSE: &str = r#"name: mikan

services:
  node:
    image: ${MIKAN_IMAGE}
    network_mode: host
    restart: unless-stopped
    user: "65532:65532"
    cap_drop: [ALL]
    cap_add: [NET_BIND_SERVICE]
    security_opt: ["no-new-privileges:true"]
    read_only: true
    tmpfs: ["/tmp:rw,size=64m"]
    logging:
      driver: json-file
      options: {max-size: "10m", max-file: "3"}
    entrypoint: ["/usr/local/bin/mikan-node"]
    environment:
      MIKAN_DATA_DIR: /data/node
      MIKAN_NODE_SOCKET: /run/mikan/node.sock
      MIKAN_NODE_JOIN: ${MIKAN_NODE_JOIN}
    volumes: ["./data:/data", "run:/run/mikan"]

volumes:
  run: {}
"#;

/// The Docker engine's version, None without Docker.
pub fn version() -> Option<String> {
    output("docker", &["version", "--format", "{{.Server.Version}}"]).map(|v| v.trim().to_owned()).filter(|v| !v.is_empty())
}

pub fn compose_ok() -> bool {
    output("docker", &["compose", "version"]).is_some()
}

/// Runs a command and hands each line of its output (stdout and stderr) to line.
pub fn stream(mut cmd: Command, mut line: impl FnMut(&str)) -> Result<()> {
    let name = format!("{cmd:?}");
    let mut child =
        cmd.stdin(Stdio::null()).stdout(Stdio::piped()).stderr(Stdio::piped()).spawn().with_context(|| format!("run {name}"))?;
    let (tx, rx) = mpsc::channel::<String>();
    let readers = [forward(child.stdout.take().context("stdout")?, tx.clone()), forward(child.stderr.take().context("stderr")?, tx)];
    let mut tail = Vec::new();
    for l in rx {
        line(&l);
        tail.push(l);
        if tail.len() > 20 {
            tail.remove(0);
        }
    }
    for r in readers {
        let _ = r.join();
    }
    let status = child.wait()?;
    if !status.success() {
        bail!("{name} failed ({status}):\n{}", tail.join("\n"));
    }
    Ok(())
}

/// Starts a command whose output lines come through the receiver; stopping the child is
/// the caller's.
pub fn follow(mut cmd: Command) -> Result<(Child, mpsc::Receiver<String>)> {
    let mut child = cmd.stdin(Stdio::null()).stdout(Stdio::piped()).stderr(Stdio::piped()).spawn()?;
    let (tx, rx) = mpsc::channel();
    forward(child.stdout.take().context("stdout")?, tx.clone());
    forward(child.stderr.take().context("stderr")?, tx);
    Ok((child, rx))
}

/// Sends the lines of a pipe to tx; bytes that are not UTF-8 do not stop it.
fn forward(r: impl Read + Send + 'static, tx: mpsc::Sender<String>) -> thread::JoinHandle<()> {
    thread::spawn(move || {
        let mut r = BufReader::new(r);
        let mut buf = Vec::new();
        while r.read_until(b'\n', &mut buf).is_ok_and(|n| n > 0) {
            let _ = tx.send(String::from_utf8_lossy(&buf).trim_end().to_owned());
            buf.clear();
        }
    })
}

/// Installs Docker with its official script, get.docker.com.
pub fn install(line: impl FnMut(&str)) -> Result<()> {
    let script = crate::net::get("https://get.docker.com", 1 << 20).context("download get.docker.com")?;
    let path = std::env::temp_dir().join("mikan-get-docker.sh");
    std::fs::write(&path, script)?;
    let mut cmd = Command::new("sh");
    cmd.arg(&path).env("DEBIAN_FRONTEND", "noninteractive");
    let r = stream(cmd, line);
    let _ = std::fs::remove_file(&path);
    r?;
    let _ = Command::new("systemctl").args(["enable", "--now", "docker"]).stdout(Stdio::null()).stderr(Stdio::null()).status();
    if !compose_ok() {
        bail!("Docker is installed without compose v2");
    }
    Ok(())
}

/// Counts layers in `docker pull` output: its share of done layers is the progress.
#[derive(Default)]
pub struct PullProgress {
    layers: BTreeMap<String, bool>,
}

impl PullProgress {
    /// Takes one line of `docker pull`; returns the progress when it moved.
    pub fn feed(&mut self, line: &str) -> Option<f64> {
        let (id, state) = line.split_once(": ")?;
        if id.len() != 12 || !id.bytes().all(|b| b.is_ascii_hexdigit()) {
            return None;
        }
        let done = matches!(state.trim(), "Pull complete" | "Already exists");
        let entry = self.layers.entry(id.to_owned()).or_insert(false);
        *entry |= done;
        let finished = self.layers.values().filter(|d| **d).count();
        Some(finished as f64 / self.layers.len() as f64)
    }
}

pub fn pull(reference: &str, mut progress: impl FnMut(f64)) -> Result<()> {
    let mut p = PullProgress::default();
    let mut cmd = Command::new("docker");
    cmd.args(["pull", reference]);
    stream(cmd, |l| {
        if let Some(x) = p.feed(l) {
            progress(x);
        }
    })
}

/// Loads an image archive (docker save | gzip) and returns the image's name.
pub fn load(archive: &str) -> Result<String> {
    let out = Command::new("docker").args(["load", "-i", archive]).output()?;
    if !out.status.success() {
        bail!("docker load: {}", String::from_utf8_lossy(&out.stderr).trim());
    }
    String::from_utf8_lossy(&out.stdout)
        .lines()
        .filter_map(|l| l.strip_prefix("Loaded image: "))
        .next_back()
        .map(str::to_owned)
        .context("the archive has no image")
}

/// `docker compose` for /opt/mikan.
pub fn compose(args: &[&str]) -> Command {
    let mut cmd = Command::new("docker");
    cmd.args(["compose", "--project-directory", DIR]).args(args).current_dir(DIR);
    cmd
}

/// Runs a compose command and fails with its output.
pub fn compose_run(args: &[&str]) -> Result<Output> {
    let out = compose(args).stdin(Stdio::null()).output().with_context(|| format!("docker compose {}", args.join(" ")))?;
    if !out.status.success() {
        let msg = String::from_utf8_lossy(&out.stderr);
        bail!("docker compose {}: {}", args.first().unwrap_or(&""), msg.trim());
    }
    Ok(out)
}

/// `mikan admin …` in the running panel. stdin is fed to the command when given.
pub fn admin(args: &[&str], stdin: Option<&str>) -> Result<Output> {
    let mut full = vec!["exec", "-T", "panel", "mikan", "admin"];
    full.extend_from_slice(args);
    with_stdin(compose(&full), stdin)
}

/// `mikan admin …` in a one-off panel container, before the panel runs (bootstrap).
pub fn admin_once(args: &[&str], stdin: Option<&str>) -> Result<Output> {
    let mut full = vec!["run", "--rm", "--no-deps", "-T", "panel", "admin"];
    full.extend_from_slice(args);
    with_stdin(compose(&full), stdin)
}

fn with_stdin(mut cmd: Command, stdin: Option<&str>) -> Result<Output> {
    let mut child = cmd.stdin(Stdio::piped()).stdout(Stdio::piped()).stderr(Stdio::piped()).spawn()?;
    if let Some(s) = stdin {
        child.stdin.take().context("stdin")?.write_all(s.as_bytes())?;
    }
    drop(child.stdin.take());
    Ok(child.wait_with_output()?)
}

/// Fails with what the command said on stderr.
pub fn check(out: Output) -> Result<Output> {
    if out.status.success() {
        return Ok(out);
    }
    let err = String::from_utf8_lossy(&out.stderr);
    let msg = err.lines().rev().find(|l| !l.trim().is_empty()).unwrap_or("failed").trim().trim_start_matches("mikan: ");
    bail!("{msg}")
}

/// The panel answers on its port (the container's own health check).
pub fn panel_healthy() -> bool {
    compose(&["exec", "-T", "panel", "mikan", "health"])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .is_ok_and(|s| s.success())
}

#[derive(Deserialize, Debug, Clone, Default)]
pub struct Service {
    #[serde(rename = "Service", default)]
    pub name: String,
    #[serde(rename = "State", default)]
    pub state: String,
    #[serde(rename = "Status", default)]
    pub status: String,
}

/// The containers of the compose project.
pub fn services() -> Result<Vec<Service>> {
    let out = compose_run(&["ps", "--all", "--format", "json"])?;
    parse_services(&String::from_utf8_lossy(&out.stdout))
}

/// Compose prints a JSON array (older versions) or one object per line.
fn parse_services(text: &str) -> Result<Vec<Service>> {
    let text = text.trim();
    if text.starts_with('[') {
        return Ok(serde_json::from_str(text)?);
    }
    text.lines().filter(|l| !l.trim().is_empty()).map(|l| Ok(serde_json::from_str(l)?)).collect()
}

#[derive(Deserialize, Debug, Clone, Default)]
pub struct Stats {
    #[serde(rename = "Name", default)]
    pub name: String,
    #[serde(rename = "CPUPerc", default)]
    pub cpu: String,
    #[serde(rename = "MemUsage", default)]
    pub mem: String,
}

/// CPU and memory of the running containers.
pub fn stats() -> Vec<Stats> {
    let Some(ids) = compose(&["ps", "-q"]).output().ok().map(|o| String::from_utf8_lossy(&o.stdout).into_owned()) else {
        return Vec::new();
    };
    let ids: Vec<&str> = ids.split_whitespace().collect();
    if ids.is_empty() {
        return Vec::new();
    }
    let mut args = vec!["stats", "--no-stream", "--format", "{{json .}}"];
    args.extend(ids);
    output("docker", &args).map(|o| o.lines().filter_map(|l| serde_json::from_str(l).ok()).collect()).unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn pull_progress() {
        let mut p = PullProgress::default();
        assert_eq!(p.feed("0.3.9: Pulling from miroshka000/mikan"), None);
        assert_eq!(p.feed("4f4fb700ef54: Pulling fs layer"), Some(0.0));
        assert_eq!(p.feed("a1b2c3d4e5f6: Already exists"), Some(0.5));
        assert_eq!(p.feed("4f4fb700ef54: Download complete"), Some(0.5));
        assert_eq!(p.feed("4f4fb700ef54: Pull complete"), Some(1.0));
        assert_eq!(p.feed("Digest: sha256:abc"), None);
    }

    #[test]
    fn compose_ps_formats() {
        let lines = "{\"Service\":\"node\",\"State\":\"running\",\"Health\":\"\",\"Status\":\"Up 2 hours\"}\n{\"Service\":\"panel\",\"State\":\"running\",\"Health\":\"healthy\",\"Status\":\"Up 2 hours (healthy)\"}\n";
        let s = parse_services(lines).unwrap();
        assert_eq!((s.len(), s[1].name.as_str(), s[1].status.as_str()), (2, "panel", "Up 2 hours (healthy)"));
        let array = "[{\"Service\":\"node\",\"State\":\"exited\",\"Status\":\"Exited (1)\"}]";
        assert_eq!(parse_services(array).unwrap()[0].state, "exited");
        assert!(parse_services("").unwrap().is_empty());
    }
}
