//! The server's commands: what the menu does, for scripts and old habits (the bash
//! `mikan` of 0.3.8 and before had the same names).

use std::fs;
use std::io::{BufRead, Write};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::Duration;

use anyhow::{Context, Result, anyhow, bail};
use sha2::{Digest, Sha256};

use crate::envfile::EnvFile;
use crate::setup;
use crate::{DIR, docker, host, net, release, system};

pub struct Install {
    pub env: EnvFile,
    pub node: bool,
}

impl Install {
    pub fn load() -> Result<Self> {
        let env = EnvFile::load(Path::new(DIR).join(".env")).map_err(|_| anyhow!("mikan is not installed here: run `mikan install`"))?;
        let node = env.get("MIKAN_MODE") == Some("node");
        Ok(Self { env, node })
    }

    /// The version installed: .env knows it since 0.3.9, the image before that.
    pub fn version(&self) -> String {
        if let Some(v) = self.env.get("MIKAN_VERSION") {
            return v.to_owned();
        }
        let service = if self.node { "node" } else { "panel" };
        let bin = if self.node { "/usr/local/bin/mikan-node" } else { "mikan" };
        docker::compose(&["exec", "-T", service, bin, "version"])
            .stderr(Stdio::null())
            .output()
            .ok()
            .filter(|o| o.status.success())
            .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_owned())
            .filter(|v| !v.is_empty())
            .unwrap_or_else(|| "unknown".into())
    }

    /// A node's API port; None for a panel.
    pub fn node_port(&self) -> Option<u16> {
        if !self.node {
            return None;
        }
        self.env.get("NODE_API_PORT").and_then(|p| p.parse().ok())
    }

    pub fn panel_only(&self) -> Result<()> {
        if self.node {
            bail!("this server is a node: the panel it is joined to manages it (Nodes page)");
        }
        Ok(())
    }

    pub fn ufw(&self) -> bool {
        self.env.get("MIKAN_UFW") != Some("0")
    }
}

/// `mikan admin …` in the panel with the terminal attached; exits with its status.
pub fn admin(args: &[&str]) -> Result<()> {
    Install::load()?.panel_only()?;
    let mut full = vec!["exec", "-T", "panel", "mikan", "admin"];
    full.extend_from_slice(args);
    let status = docker::compose(&full).status()?;
    if !status.success() {
        std::process::exit(status.code().unwrap_or(1));
    }
    Ok(())
}

/// Inbounds; a new or moved port on this server is opened in ufw.
pub fn inbound(args: &[String]) -> Result<()> {
    let args: Vec<&str> = args.iter().map(String::as_str).collect();
    if !matches!(args.first(), Some(&("add" | "set"))) {
        let mut full = vec!["inbound"];
        full.extend(&args);
        return admin(&full);
    }
    let install = Install::load()?;
    install.panel_only()?;
    let mut full = vec!["exec", "-T", "panel", "mikan", "admin", "inbound"];
    full.extend_from_slice(&args);
    // stdout is "port/network" when the port is on this server; messages are on stderr.
    let out = docker::compose(&full).stderr(Stdio::inherit()).output()?;
    if !out.status.success() {
        std::process::exit(out.status.code().unwrap_or(1));
    }
    let rule = String::from_utf8_lossy(&out.stdout).trim().replacen('-', ":", 1);
    if !rule.is_empty() && install.ufw() && system::ufw_active() {
        host::allow(&rule)?;
        println!("Opened {rule} in ufw.");
    }
    Ok(())
}

pub fn status() -> Result<()> {
    let install = Install::load()?;
    let version = install.version();
    println!("mikan {version}, {}", if install.node { "node" } else { "panel" });
    for s in docker::services()? {
        println!("  {:<6} {}", s.name, s.status);
    }
    for s in docker::stats() {
        println!("  {:<16} CPU {:>6}  memory {}", s.name, s.cpu, s.mem);
    }
    if let Some(p) = install.node_port() {
        match system::port_owner(p, system::Proto::Tcp) {
            Some(_) => println!("The node waits for its panel on port {p}."),
            None => println!("The node does not listen on port {p}: mikan logs node"),
        }
    } else if docker::panel_healthy() {
        println!("The panel answers.");
    } else {
        println!("The panel does not answer: mikan logs panel");
    }
    match release::latest() {
        Ok(m) if release::newer(&m.version, &version) => println!("mikan {} is out: mikan update", m.version),
        Ok(_) => println!("This is the latest release."),
        Err(e) => println!("Cannot check for updates: {e:#}"),
    }
    Ok(())
}

pub fn logs(service: Option<&str>) -> Result<()> {
    Install::load()?;
    let mut args = vec!["logs", "-f", "--tail", "200"];
    args.extend(service);
    docker::compose(&args).status()?;
    Ok(())
}

pub fn restart() -> Result<()> {
    let install = Install::load()?;
    docker::compose_run(&["restart"])?;
    setup::wait_ready(install.node_port(), Duration::from_secs(90))?;
    println!("Restarted.");
    Ok(())
}

fn now(format: &str) -> String {
    system::output("date", &["-u", format]).map(|s| s.trim().to_owned()).unwrap_or_else(|| "now".into())
}

/// A backup of the database (a consistent copy while the panel runs), certificates and
/// settings, in /opt/mikan/backups.
pub fn backup() -> Result<PathBuf> {
    let install = Install::load()?;
    let file = PathBuf::from(format!("{DIR}/backups/mikan-{}.tar.gz", now("+%Y%m%d-%H%M%S")));
    fs::create_dir_all(format!("{DIR}/backups"))?;
    let mut members = vec![".env", "compose.yaml"];
    if install.node {
        members.push("data/node");
    } else {
        docker::check(docker::admin(&["backup", "/data/panel/backup.db"], None)?)?;
        members.extend(["data/panel/backup.db", "data/panel/tls", "data/node"]);
    }
    let status = Command::new("tar").arg("-czf").arg(&file).arg("-C").arg(DIR).args(&members).stderr(Stdio::null()).status();
    let _ = fs::remove_file(format!("{DIR}/data/panel/backup.db"));
    if !status.is_ok_and(|s| s.success()) {
        bail!("tar failed");
    }
    use std::os::unix::fs::PermissionsExt;
    fs::set_permissions(&file, fs::Permissions::from_mode(0o600))?;
    Ok(file)
}

pub fn backups() -> Vec<PathBuf> {
    let mut list: Vec<PathBuf> = fs::read_dir(format!("{DIR}/backups"))
        .map(|d| d.filter_map(|e| e.ok().map(|e| e.path())).filter(|p| p.extension().is_some_and(|x| x == "gz")).collect())
        .unwrap_or_default();
    list.sort();
    list.reverse();
    list
}

/// Replaces the data with a backup's.
pub fn restore(file: &Path) -> Result<()> {
    Install::load()?;
    if !file.is_file() {
        bail!("no file {}", file.display());
    }
    docker::compose_run(&["down"])?;
    let ok = Command::new("tar").arg("-xzf").arg(file).arg("-C").arg(DIR).status().is_ok_and(|s| s.success());
    if !ok {
        bail!("tar could not unpack {}", file.display());
    }
    let panel = Path::new(DIR).join("data/panel");
    if panel.join("backup.db").exists() {
        fs::rename(panel.join("backup.db"), panel.join("mikan.db"))?;
        let _ = fs::remove_file(panel.join("mikan.db-wal"));
        let _ = fs::remove_file(panel.join("mikan.db-shm"));
    }
    setup::own_data()?;
    docker::compose_run(&["up", "-d"])?;
    // The backup brought its own .env.
    setup::wait_ready(Install::load()?.node_port(), Duration::from_secs(90))
}

pub fn confirm(question: &str) -> bool {
    print!("{question} [y/N] ");
    let _ = std::io::stdout().flush();
    let mut answer = String::new();
    std::io::stdin().lock().read_line(&mut answer).is_ok() && matches!(answer.trim(), "y" | "Y" | "yes")
}

/// What `mikan update` was asked for.
#[derive(clap::Args, Clone, Debug, Default)]
pub struct UpdateArgs {
    /// An image (ghcr.io/…@sha256:…) or an image archive (.tar.gz) instead of the latest release
    pub target: Option<String>,
    /// Only when automatic updates are on (the daily timer runs this)
    #[arg(long)]
    pub auto: bool,
    /// The panel's Update button asked for it (a systemd path unit runs this)
    #[arg(long, hide = true)]
    pub requested: bool,
    /// Say what is out, change nothing
    #[arg(long)]
    pub check: bool,
}

/// Where the panel and the host updater talk: the panel writes request (its Update
/// button) and policy.json ({"auto": true} with automatic updates on); the host writes
/// status.json. The panel sees the directory as /data/panel/update.
pub const UPDATE_DIR: &str = "/opt/mikan/data/panel/update";

fn auto_on(install: &Install) -> bool {
    if install.node {
        return install.env.get("MIKAN_AUTO_UPDATE") == Some("1");
    }
    fs::read(format!("{UPDATE_DIR}/policy.json"))
        .ok()
        .and_then(|b| serde_json::from_slice::<serde_json::Value>(&b).ok())
        .is_some_and(|v| v["auto"] == true)
}

/// Tells the panel how the update went.
fn report(install: &Install, state: &str, version: &str, from: &str, error: &str) {
    if install.node {
        return;
    }
    let body = serde_json::json!({"state": state, "version": version, "from": from, "error": error, "at": now("+%Y-%m-%dT%H:%M:%SZ")});
    let dir = Path::new(UPDATE_DIR);
    let _ = fs::create_dir_all(dir);
    let _ = Command::new("chown").args(["65532:65532"]).arg(dir).status();
    let _ = fs::write(dir.join("status.json"), body.to_string());
}

/// Updates to the latest release (or target) and rolls back when the new version does
/// not start; then updates this command too.
pub fn update(a: &UpdateArgs, say: &mut dyn FnMut(&str), progress: &mut dyn FnMut(f64)) -> Result<()> {
    let mut install = Install::load()?;
    if a.requested {
        match fs::remove_file(format!("{UPDATE_DIR}/request")) {
            Ok(()) => {}
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(()),
            Err(e) => return Err(e.into()),
        }
    } else if a.auto && !auto_on(&install) {
        return Ok(());
    }
    let current = install.version();
    let mut manifest = None;
    let (image, version) = match a.target.as_deref() {
        Some(t) if Path::new(t).is_file() => {
            say(&format!("Loading {t}"));
            let image = docker::load(t)?;
            let v = setup::image_version(&image)?;
            (image, v)
        }
        Some(t) => {
            say(&format!("Pulling {t}"));
            docker::pull(t, &mut *progress)?;
            (t.to_owned(), setup::image_version(t)?)
        }
        None => {
            let m = release::latest()?;
            if !release::newer(&m.version, &current) {
                say(&format!("mikan {current} is the latest release."));
                self_update(&m, say);
                return Ok(());
            }
            if a.check {
                say(&format!("mikan {} is out, this server runs {current}.", m.version));
                if let Some(notes) = m.notes.get("en") {
                    say(notes);
                }
                return Ok(());
            }
            say(&format!("Updating mikan {current} → {}", m.version));
            report(&install, "running", &m.version, &current, "");
            docker::pull(&m.reference(), &mut *progress)?;
            let reference = m.reference();
            let v = m.version.clone();
            manifest = Some(m);
            (reference, v)
        }
    };
    if a.check {
        say(&format!("{image} is mikan {version}; this server runs {current}."));
        return Ok(());
    }
    let saved = backup()?;
    say(&format!("Backup: {}", saved.display()));
    let old_image = install.env.get("MIKAN_IMAGE").unwrap_or_default().to_owned();
    let old_version = install.env.get("MIKAN_VERSION").map(str::to_owned);
    install.env.set("MIKAN_IMAGE", &image)?;
    install.env.set("MIKAN_VERSION", &version)?;
    install.env.save()?;
    docker::compose_run(&["up", "-d"])?;
    if let Err(e) = setup::wait_ready(install.node_port(), Duration::from_secs(90)) {
        say(&format!("mikan {version} did not start: going back to {current}"));
        install.env.set("MIKAN_IMAGE", &old_image)?;
        install.env.set("MIKAN_VERSION", old_version.as_deref().unwrap_or(""))?;
        install.env.save()?;
        docker::compose_run(&["up", "-d"])?;
        let back = setup::wait_ready(install.node_port(), Duration::from_secs(90));
        report(&install, "failed", &version, &current, &format!("{e:#}"));
        back.context("the old version did not start either")?;
        bail!("mikan {version} did not start, {current} runs again: {e:#}");
    }
    report(&install, "ok", &version, &current, "");
    say(&format!("mikan {version} is running."));
    if let Some(m) = manifest {
        self_update(&m, say);
    }
    if let Err(e) = host::install_units(!install.node) {
        say(&format!("No automatic updates: {e}"));
    }
    Ok(())
}

/// Replaces this command with the release's installer when it differs.
fn self_update(m: &release::Manifest, say: &mut dyn FnMut(&str)) {
    let Some(asset) = m.installer() else { return };
    let sha = |b: &[u8]| Sha256::digest(b).iter().map(|x| format!("{x:02x}")).collect::<String>();
    if fs::read(host::BIN).map(|b| sha(&b)).ok().as_deref() == Some(asset.sha256.as_str()) {
        return;
    }
    let result = net::get(&asset.url, 64 << 20).and_then(|data| {
        if sha(&data) != asset.sha256 {
            bail!("the downloaded installer does not match the release manifest");
        }
        host::replace_bin(&data)
    });
    match result {
        Ok(()) => say(&format!("The mikan command is {} now too.", m.version)),
        Err(e) => say(&format!("The mikan command stays as it is: {e:#}")),
    }
}

/// A node takes a new join key from its panel.
pub fn join(key: &str) -> Result<()> {
    let mut install = Install::load()?;
    if !install.node {
        bail!("join is for a node: this server runs a panel");
    }
    let image = install.env.get("MIKAN_IMAGE").context("no MIKAN_IMAGE in .env")?.to_owned();
    let port = setup::node_port(&image, key.trim())?;
    install.env.set("MIKAN_NODE_JOIN", key.trim())?;
    install.env.set("NODE_API_PORT", &port.to_string())?;
    install.env.save()?;
    if install.ufw() && system::ufw_active() {
        host::allow(&format!("{port}/tcp"))?;
    }
    docker::compose_run(&["up", "-d"])?;
    setup::wait_ready(Some(port), Duration::from_secs(90))?;
    println!("The node runs with the new key and waits for its panel on port {port}.");
    Ok(())
}

/// Stops mikan and removes what it put on the host; the data stays.
pub fn uninstall() -> Result<()> {
    Install::load()?;
    docker::compose_run(&["down"])?;
    host::remove_units();
    if fs::remove_file(host::SYSCTL).is_ok() {
        let _ = Command::new("sysctl").arg("--system").stdout(Stdio::null()).stderr(Stdio::null()).status();
    }
    let _ = fs::remove_file(host::BIN);
    Ok(())
}
