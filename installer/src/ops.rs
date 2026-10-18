//! The server's commands: what the menu does, for scripts and old habits (the bash
//! `mikan` of 0.3.8 and before had the same names). Updates are in update.rs, backups in
//! backup.rs.

use std::fs;
use std::io::{BufRead, IsTerminal, Write};
use std::net::IpAddr;
use std::path::Path;
use std::process::Stdio;
use std::time::{Duration, SystemTime};

use anyhow::{Context, Result, anyhow, bail};

use crate::config::Config;
use crate::setup;
use crate::{DIR, acme, addon, docker, hello, host, lock, release, system};

pub struct Install {
    pub config: Config,
    pub node: bool,
}

impl Install {
    pub fn load() -> Result<Self> {
        let config = Config::load(DIR).map_err(|e| {
            let missing = e.root_cause().downcast_ref::<std::io::Error>().is_some_and(|io| io.kind() == std::io::ErrorKind::NotFound);
            // Only missing settings mean "not installed": settings that cannot be read say why.
            if missing { anyhow!("mikan is not installed here: run `mikan install`") } else { e }
        })?;
        let node = config.get("MIKAN_MODE") == Some("node");
        Ok(Self { config, node })
    }

    /// The version installed: the settings know it since 0.3.9, the image before that.
    pub fn version(&self) -> String {
        if let Some(v) = self.config.get("MIKAN_VERSION") {
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
        self.config.get("NODE_API_PORT").and_then(|p| p.parse().ok())
    }

    pub fn panel_only(&self) -> Result<()> {
        if self.node {
            bail!("this server is a node: the panel it is joined to manages it (Nodes page)");
        }
        Ok(())
    }

    pub fn ufw(&self) -> bool {
        self.config.get("MIKAN_UFW") != Some("0")
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

/// The largest PEM file the panel takes (tlscert.MaxPEM).
const MAX_PEM: u64 = 64 << 10;

/// Installs an own certificate: both files go to the panel on stdin, never on the
/// command line or into another file; the panel checks them and serves them at once.
/// `mikan cert proxy <domain>`: Let's Encrypt checks domain on port 80, which nginx or Caddy
/// may hold on this server. The rule that passes the check to mikan goes into that web
/// server (backed up, tested, reloaded; or shown when it cannot be added), and mikan
/// answers on a local port behind it. With port 80 free again, mikan takes it back.
pub fn cert_proxy(domain: &str, yes: bool) -> Result<()> {
    let domain = domain.trim().trim_end_matches('.').to_ascii_lowercase();
    if !crate::net::valid_domain(&domain) && domain.parse::<IpAddr>().is_err() {
        bail!("{domain:?} is not a domain or an IP address: give the address clients reach this server by");
    }
    let mut install = Install::load()?;
    // recreate writes this release's compose file, which runs the panel on PostgreSQL: a
    // panel still on SQLite moves there only through `mikan update`. Asked only where
    // something is about to change.
    let movable = |install: &Install| -> Result<()> {
        if !install.node && install.config.get("MIKAN_DATABASE_URL").is_none() {
            bail!("this panel still keeps its data in SQLite (mikan 0.4): run `mikan update` first, then this command");
        }
        Ok(())
    };
    let _lock = lock::acquire(lock::Wait::Block, &mut crate::out)?;
    let what = if install.node { "node" } else { "panel" };
    let owner = system::port_owner(80, system::Proto::Tcp);
    match acme::port80(owner.as_deref(), install.config.get("MIKAN_ACME_LISTEN"), acme::free_port) {
        acme::Port80::Own { drop: false } => {
            crate::out(&format!("Port 80 is free: the {what} answers Let's Encrypt there by itself, nothing to change."));
            Ok(())
        }
        acme::Port80::Own { drop: true } => {
            movable(&install)?;
            install.config.remove("MIKAN_ACME_LISTEN");
            install.config.save()?;
            recreate(&mut install)?;
            crate::out(&format!("Port 80 is free now: the {what} answers Let's Encrypt there by itself again."));
            Ok(())
        }
        acme::Port80::Other(who) => bail!(
            "port 80 is held by {who}, which mikan cannot configure. Free port 80, or make {who} pass /.well-known/acme-challenge/ to 127.0.0.1:{port}, set \"MIKAN_ACME_LISTEN\": \"127.0.0.1:{port}\" in {DIR}/config.json and run: mikan restart",
            port = acme::free_port()
        ),
        acme::Port80::Front(front, port) => {
            movable(&install)?;
            let question = format!(
                "{} holds port 80, where Let's Encrypt checks {domain}. Add a rule that passes only those checks to the {what}? Its config is backed up and tested before a reload.",
                front.name()
            );
            if !yes && !confirm(&question) {
                crate::out(&format!(
                    "Nothing changed. What to add by hand:

{}",
                    acme::manual_text(front, &domain, port)
                ));
                return Ok(());
            }
            let report = acme::setup(front, &domain, port, true);
            // The rule may be in, or left for the admin: mikan answers behind it either way.
            install.config.set("MIKAN_ACME_LISTEN", &acme::listen(port))?;
            install.config.save()?;
            recreate(&mut install)?;
            crate::out(&report.note());
            match report.text() {
                Some(t) => crate::out(&t),
                None => crate::out(&format!(
                    "Done. The {what} gets its certificate by itself within minutes; on the panel's {} page Retry asks for it now.",
                    if install.node { "Nodes" } else { "Settings → Security" }
                )),
            }
            Ok(())
        }
    }
}

/// Starts the containers again with what config.json says now, through this release's compose
/// file: one written by an older mikan may not pass MIKAN_ACME_LISTEN on.
fn recreate(install: &mut Install) -> Result<()> {
    if !install.node && install.config.get("MIKAN_DATABASE_URL").is_none() {
        bail!("this SQLite installation needs mikan update first");
    }
    docker::ensure_compose(Path::new(DIR), &install.config)?;
    docker::compose_run(&["up", "-d"])?;
    setup::wait_ready(install.node_port(), Duration::from_secs(90))?;
    install.config.retire_legacy()
}

pub fn cert_set(cert: &Path, key: &Path, node: Option<u32>) -> Result<()> {
    Install::load()?.panel_only()?;
    let mut pem = String::new();
    for path in [cert, key] {
        let size = fs::metadata(path).with_context(|| format!("{}", path.display()))?.len();
        if size > MAX_PEM {
            bail!("{} is larger than a certificate or a key can be", path.display());
        }
        pem.push_str(&fs::read_to_string(path).with_context(|| format!("{}", path.display()))?);
        pem.push('\n');
    }
    let mut args = vec!["cert", "set"];
    let n = node.map(|n| n.to_string());
    if let Some(n) = &n {
        args.extend(["--node", n.as_str()]);
    }
    let out = docker::check(docker::admin(&args, Some(&pem))?)?;
    print!("{}", String::from_utf8_lossy(&out.stdout));
    Ok(())
}

/// `mikan admin cert clear|show` for the panel or a node.
pub fn cert(cmd: &[&str], node: Option<u32>) -> Result<()> {
    let mut args = vec!["cert"];
    args.extend_from_slice(cmd);
    let n = node.map(|n| n.to_string());
    if let Some(n) = &n {
        args.extend(["--node", n.as_str()]);
    }
    admin(&args)
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
    if rule.is_empty() {
        return Ok(());
    }
    // What the panel printed is a port to open, nothing more: it is checked before it is
    // handed to the firewall.
    if !host::valid_rule(&rule) {
        eprintln!("mikan: the panel named {rule:?}, which is not a port to open: ufw is left alone");
        return Ok(());
    }
    if install.ufw() && system::ufw_active() {
        host::allow(&rule)?;
        crate::out(&format!("Opened {rule} in ufw."));
    }
    Ok(())
}

pub fn status() -> Result<()> {
    status_to(&Install::load()?, &mut crate::out)
}

/// `mikan status`, said line by line to say (`mikan support` keeps the lines).
pub fn status_to(install: &Install, say: &mut dyn FnMut(&str)) -> Result<()> {
    let version = install.version();
    say(&format!("mikan {version}, {}", if install.node { "node" } else { "panel" }));
    let mut wrong = false;
    let services = docker::services()?;
    for s in &services {
        say(&format!("  {:<6} {}", s.name, s.status));
        wrong |= s.state != "running";
    }
    for s in docker::stats(&services) {
        say(&format!("  {:<16} CPU {:>6}  memory {}", s.name, s.cpu, s.mem));
    }
    if let Some(p) = install.node_port() {
        match system::port_owner(p, system::Proto::Tcp) {
            Some(_) => say(&format!("The node waits for its panel on port {p}.")),
            None => {
                wrong = true;
                say(&format!("The node does not listen on port {p}: mikan logs node"));
            }
        }
    } else if docker::panel_healthy() {
        say("The panel answers.");
    } else {
        wrong = true;
        say("The panel does not answer: mikan logs panel");
    }
    if wrong {
        say("Run mikan doctor for a full check with fixes.");
    }
    match release::find(Some(&version), crate::update::channel(install)) {
        Ok(found) => {
            // Where the answer came from is worth a line only when it is not the index.
            if let Some(why) = &found.fallback {
                say(&format!("The release index is unavailable ({why}): the latest release on GitHub answers instead."));
            }
            let m = &found.manifest;
            match &found.newest {
                Some(newest) if found.unreachable => say(&format!("mikan {newest} is out, but this version cannot update to it directly.")),
                Some(newest) => say(&format!("mikan {newest} is out: mikan update (through mikan {} first)", m.version)),
                None if release::newer(&m.version, &version) => say(&format!("mikan {} is out: mikan update", m.version)),
                None => say("This is the latest release."),
            }
        }
        Err(e) => say(&format!("Cannot check for updates: {e:#}")),
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
    let _lock = lock::acquire(lock::Wait::Block, &mut crate::out)?;
    docker::compose_run(&["restart"])?;
    setup::wait_ready(install.node_port(), Duration::from_secs(90))?;
    crate::out("Restarted.");
    Ok(())
}

/// `mikan support-access on|off`: MIKAN_SUPPORT_ACCESS in config.json, the support terminal's units
/// (host.rs keeps them away while it is off) and, on a panel, its container, which reads the
/// variable when it is created.
pub fn support_access(on: bool) -> Result<()> {
    let mut install = Install::load()?;
    let _lock = lock::acquire(lock::Wait::Block, &mut crate::out)?;
    if on {
        install.config.remove("MIKAN_SUPPORT_ACCESS");
    } else {
        install.config.set("MIKAN_SUPPORT_ACCESS", "off")?;
    }
    install.config.save()?;
    if Path::new("/run/systemd/system").exists() {
        host::install_units(!install.node)?;
    }
    if !install.node {
        docker::compose_run(&["up", "-d"])?;
        setup::wait_ready(None, Duration::from_secs(90))?;
    }
    crate::out(if on {
        "Support access is back in the panel's hands: Settings, Security, Support access."
    } else {
        "Support access is off on this server: the panel opens none, and no support terminal runs here."
    });
    Ok(())
}

/// How long a restart the panel asked for waits: the answer to the page that asked goes out
/// before the container it came from stops.
const RESTART_GRACE: Duration = Duration::from_secs(2);

/// `mikan restart --requested` (the restart request unit): takes the request the panel, or
/// a node, wrote, and restarts the containers. A request that is not one is removed and
/// nothing restarts.
pub fn restart_requested() -> Result<()> {
    let install = Install::load()?;
    let Some(dir) = crate::hostreq::dir(install.node, "restart")? else { return Ok(()) };
    if crate::hostreq::take(&dir)?.is_none() {
        return Ok(());
    }
    crate::out("restart asked for by the panel");
    std::thread::sleep(RESTART_GRACE);
    restart()
}

pub fn confirm(question: &str) -> bool {
    print!("{question} [y/N] ");
    let _ = std::io::stdout().flush();
    let mut answer = String::new();
    std::io::stdin().lock().read_line(&mut answer).is_ok() && matches!(answer.trim(), "y" | "Y" | "yes")
}

/// The join key: from the command line, or from the environment (MIKAN_JOIN_KEY), or typed
/// or piped in. The key holds the node's private key, so the last two keep it out of the
/// process list and the shell's history.
pub fn join_key(arg: Option<String>) -> Result<String> {
    let key = match arg.or_else(|| std::env::var("MIKAN_JOIN_KEY").ok()) {
        Some(k) => k,
        None => {
            if std::io::stdin().is_terminal() {
                eprint!("Join key (from the panel's Nodes page): ");
                let _ = std::io::stderr().flush();
            }
            let mut line = String::new();
            std::io::stdin().lock().read_line(&mut line).context("read the join key")?;
            line
        }
    };
    let key = key.trim().to_owned();
    if key.is_empty() {
        bail!("no join key: pass it, set MIKAN_JOIN_KEY, or paste it when asked");
    }
    Ok(key)
}

/// A node takes a new join key from its panel. The firewall opens the node's API port for
/// everyone, or with the panel's address only for it.
pub fn join(key: &str, panel_ip: Option<IpAddr>) -> Result<()> {
    let _lock = lock::acquire(lock::Wait::Block, &mut crate::out)?;
    let mut install = Install::load()?;
    if !install.node {
        bail!("join is for a node: this server runs a panel");
    }
    let image = install.config.get("MIKAN_IMAGE").context("no MIKAN_IMAGE in config.json")?.to_owned();
    let port = setup::node_port(&image, key.trim())?;
    install.config.set("MIKAN_NODE_JOIN", key.trim())?;
    install.config.set("NODE_API_PORT", &port.to_string())?;
    install.config.save()?;
    if install.ufw() && system::ufw_active() {
        match panel_ip {
            Some(ip) => host::allow_from(ip, port)?,
            None => host::allow(&format!("{port}/tcp"))?,
        }
    }
    let since = SystemTime::now();
    docker::compose_run(&["up", "-d"])?;
    setup::wait_ready(Some(port), Duration::from_secs(90))?;
    crate::out(&format!("The node runs with the new key and waits for its panel on port {port}."));
    // What the panel found is told, never made an error: the key is in place either way.
    crate::out("Waiting for the panel to connect…");
    if let Some(h) = hello::wait(since, hello::LIMIT) {
        for line in hello::render(&hello::lines(&h, Some(port))) {
            crate::out(&line);
        }
    }
    Ok(())
}

/// Stops mikan and removes what it put on the host; the data stays, a panel's database in
/// its volume too. True for a panel.
pub fn uninstall() -> Result<bool> {
    let panel = !Install::load()?.node;
    let _lock = lock::acquire(lock::Wait::Block, &mut crate::out)?;
    docker::compose_run(&["down"])?;
    addon::down();
    addon::drop_network();
    host::remove_units();
    if fs::remove_file(host::SYSCTL).is_ok() {
        let _ = system::succeeds("sysctl", &["--system"]);
    }
    let _ = fs::remove_file(host::BIN);
    Ok(panel)
}
